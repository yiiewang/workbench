package server

// AI 对话流式代理：多轮工具循环与 SSE 输出（prepare / payload / upstream / tool round 分段）。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/rag"
)

func (s *Server) handleAIChat(ctx iris.Context) {
	if !s.cfg.AI.Enabled() {
		writeFail(ctx, http.StatusServiceUnavailable, CodeAINotConfigured)
		return
	}

	var req aiChatRequest
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if err := validateAIMessages(req.Messages); err != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, err)
		return
	}
	gw, ok := s.cfg.AI.Chat.Gateway(req.Gateway)
	if !ok {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "unknown or unavailable gateway: "+req.Gateway)
		return
	}
	// QAId/SessionId 会写入上游 header：限长 + 禁控制字符，防 header 注入
	if !validTraceID(req.QAId) || !validTraceID(req.SessionId) {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "qaId/sessionId must be plain identifiers (max 64 chars)")
		return
	}

	// 后台运行上下文与请求连接解耦：客户端断开后循环继续跑完，
	// 工具执行器经 aiRunCtxKey 取用同一个 context（见 aitools.go）。
	runCtx, runCancel := context.WithTimeout(context.Background(), aiRunTimeout)
	defer runCancel()
	ctx.Values().Set(aiRunCtxKey, runCtx)

	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	run := &aiRunState{id: uuid.NewString(), orgID: orgID, userID: userID, events: []any{}}
	ctx.Values().Set(aiRunStateKey, run)
	if err := s.db.CreateAIRun(runCtx, orgID, userID, run.id, req.QAId, req.SessionId); err != nil {
		serverError(ctx, "create ai run failed", err)
		return
	}
	// 任一路径退出都落库最终状态；正常结束时在 [DONE] 前改为 done。
	runStatus, runErrMsg := "failed", ""
	defer func() {
		if runStatus == "failed" && runErrMsg == "" {
			runErrMsg = "对话未完成（上游错误或运行中断）"
		}
		run.flush(s, runStatus, runErrMsg)
	}()

	prep := s.prepareAIChat(ctx, runCtx, &req, gw)

	// 上游请求绑定后台 run 上下文：客户端断开不再取消上游流
	upCtx, cancel := context.WithTimeout(runCtx, aiStreamTimeout)
	defer cancel()

	// SSE 仅为镜像输出：断连后 writer 静默吞掉写入，循环继续跑完并落库
	w := newSSEStreamWriter(ctx.ResponseWriter(), run)
	wroteHeader := false
	usageSum := aiUsage{} // Agent 多轮 token 消耗累加
	toolCallsUsed := 0    // 已执行的工具调用数（用于每轮预算提示）

	for round := 0; ; round++ {
		payload, forceAnswer := s.buildAIChatPayload(prep, &req, gw, round, toolCallsUsed)
		body, err := json.Marshal(payload)
		if err != nil {
			serverError(ctx, "marshal ai payload failed", err)
			return
		}

		upReq, err := newAIUpstreamRequest(upCtx, prep.upstreamURL, gw.APIKey, &req, body)
		if err != nil {
			serverError(ctx, "build ai upstream request failed", err)
			return
		}

		resp, err := aiClient.Do(upReq)
		if err != nil {
			// 流已开（Agent 前几轮已推给前端）时只能走 SSE 错误事件，
			// HTTP 状态码已发出无法再改
			if wroteHeader {
				sseWriteEvent(w, "error", map[string]string{"msg": "ai upstream request failed"})
				return
			}
			serverError(ctx, "ai upstream request failed", err, "model", gw.Model)
			return
		}

		// 上游非 200：读有限字节拼进错误消息；未开流时按标准信封返回
		if resp.StatusCode != http.StatusOK {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			msg := fmt.Sprintf("upstream %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
			slog.Error("ai upstream error", "status", resp.StatusCode, "model", gw.Model)
			if wroteHeader {
				sseWriteEvent(w, "error", map[string]string{"msg": msg})
				return
			}
			writeFailMsg(ctx, iris.StatusBadGateway, CodeInternalError, msg)
			return
		}

		if !wroteHeader {
			openAIChatStream(ctx, w, run, &prep)
			wroteHeader = true
		}

		// 转发 + 解析本轮流式响应（writer 断连后仍继续解析，只是不再输出）。
		// 每个正文分片即时并入后台 run 并节流落库，供刷新后按游标持续续读；
		// 原先只在整轮结束后 WriteString(res.Content)，断连期间库里看不到增量。
		reader := bufio.NewReaderSize(resp.Body, 16*1024)
		res, alive := forwardAIStream(w, reader, func(delta string) {
			run.content.WriteString(delta)
			run.maybeFlush(s)
		})
		resp.Body.Close()
		if !alive {
			return // 非镜像 writer 的断连兜底
		}
		// 聚合本轮 token 消耗（Agent 多轮累加）
		if res.Usage != nil {
			usageSum.PromptTokens += res.Usage.PromptTokens
			usageSum.CompletionTokens += res.Usage.CompletionTokens
			usageSum.TotalTokens += res.Usage.TotalTokens
		}

		// 工具轮：模型请求调工具 → SSE 通知前端 → 后端执行 → 结果回传 → 下一轮
		if prep.toolsOn && !forceAnswer && res.FinishReason == "tool_calls" && len(res.ToolCalls) > 0 {
			prep.msgs = s.runAIChatToolRound(ctx, w, run, prep.msgs, res, round)
			toolCallsUsed += len(res.ToolCalls)
			continue
		}

		// 最终轮（stop / 无工具请求 / 达到轮数上限）：usage 事件 + 统一结束标记
		if usageSum.TotalTokens > 0 {
			sseWriteEvent(w, "usage", usageSum)
			run.addEvent("usage", usageSum)
		}
		if _, werr := io.WriteString(w, "data: [DONE]\n\n"); werr != nil {
			return
		}
		w.Flush()
		runStatus = "done"
		return
	}
}

// aiChatPrep 一次对话的上游恒定部分：端点、工具开关、消息体与 RAG 上下文。
type aiChatPrep struct {
	upstreamURL string
	toolsOn     bool
	msgs        []aiMessage
	ragHits     []rag.Hit // SSE 首帧推送用；推送后置 nil
	// ragPromptHits 后续轮的摘要数据（ragHits 置 nil 后仍要保留）
	ragPromptHits []rag.Hit
	ragMsg        *aiMessage
}

// prepareAIChat 组装上游请求的恒定部分：端点归一化、工具开关解析、
// Agent 系统提示注入、RAG 前置检索（失败/超时静默降级）。
func (s *Server) prepareAIChat(ctx iris.Context, runCtx context.Context, req *aiChatRequest, gw config.ChatGateway) aiChatPrep {
	// base_url 兼容两种写法：网关根（…/v1，自动追加 /chat/completions）
	// 或完整端点（…/v1/chat/completions，原样使用），避免配置歧义导致 404。
	prep := aiChatPrep{upstreamURL: normalizeAIEndpoint(gw.BaseURL)}
	prep.toolsOn = s.cfg.AI.Chat.Tools
	if gw.Tools != nil {
		prep.toolsOn = *gw.Tools
	}
	prep.msgs = req.Messages
	// Agent 模式注入系统提示：平台角色 + 当前时间（模型无时间感知，
	// 此前任务日期全靠猜）+ 写操作前置说明要求（先解释计划再请求确认）
	if prep.toolsOn {
		prep.msgs = append([]aiMessage{{Role: "system", Content: aiSystemPrompt(time.Now())}}, prep.msgs...)
	}
	// RAG 前置检索：用最新一条 user 消息在其个人文件中做语义检索。
	// 完整片段只在首轮注入；后续轮换成摘要（编号与行号不变），因为每轮请求
	// 都是无状态的完整 messages，直接移除会让模型丢失已注入的内容。
	if s.rag == nil {
		return prep
	}
	q := ragQueryFrom(req.Messages)
	if q == "" {
		return prep
	}
	hits, err := s.rag.Search(runCtx, currentOrgID(ctx), currentUserID(ctx), q, ragSearchTopK)
	switch {
	case err != nil:
		slog.Warn("rag: search failed, fallback to no context", "err", err)
	case len(hits) > 0:
		// 可观测性：哪些回答参考了哪些文件 + 实际距离
		// （distance 观测值是 max_distance 阈值调参的唯一依据）
		files := make([]string, len(hits))
		for i, h := range hits {
			files[i] = fmt.Sprintf("%s(%.3f)", h.Path, h.Distance)
		}
		slog.Info("rag: context injected", "user", currentUserID(ctx),
			"query", truncateStr(q, 100), "hits", len(hits), "files", files)
		prep.ragHits = hits
		prep.ragPromptHits = hits
		msg := aiMessage{Role: "system", Content: rag.ContextPrompt(hits)}
		prep.ragMsg = &msg
	}
	return prep
}

// buildAIChatPayload 组装本轮上游请求体，返回 payload 与是否已进入强制回答轮。
// 每轮按需组装 messages，不改动 prep.msgs 本身：
//  1. 检索片段首轮给全文、后续轮给摘要，避免整段原文随每轮重发；
//  2. 工具轮显式告知剩余轮次，避免预算隐到最后一轮才收敛
//     （实测 6 轮 thinking 逐轮膨胀到 29070 字）。
//
// 工具轮次用尽后不再携带 tools：模型只能文本回答，否则上限轮模型仍可能请求调工具
// 被掐断，表现为"只有工具卡片没有回答"。
func (s *Server) buildAIChatPayload(prep aiChatPrep, req *aiChatRequest, gw config.ChatGateway, round, toolCallsUsed int) (map[string]any, bool) {
	forceAnswer := round >= aiMaxToolRounds

	payloadMsgs := make([]aiMessage, len(prep.msgs), len(prep.msgs)+2)
	copy(payloadMsgs, prep.msgs)
	if prep.ragMsg != nil {
		if round == 0 {
			payloadMsgs = append(payloadMsgs, *prep.ragMsg)
		} else {
			payloadMsgs = append(payloadMsgs, aiMessage{Role: "system", Content: ragBriefPrompt(prep.ragPromptHits)})
		}
	}
	if prep.toolsOn && !forceAnswer && round > 0 {
		payloadMsgs = append(payloadMsgs, aiMessage{Role: "system", Content: fmt.Sprintf(
			"工具轮次预算：剩余 %d 轮（已调用 %d 次工具）。信息足够时直接给出最终回答。",
			aiMaxToolRounds-round, toolCallsUsed)})
	}

	payload := map[string]any{
		"model":    gw.Model,
		"messages": payloadMsgs,
		"stream":   true,
		// 流式响应末尾带 usage 分片（token 消耗统计）；网关不支持时
		// 分片缺失，前端不显示消耗（无害降级）
		"stream_options": map[string]any{"include_usage": true},
	}
	if s.cfg.AI.Chat.Thinking {
		payload["chat_template_kwargs"] = map[string]any{"thinking": true}
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	switch req.ReasoningEffort {
	case "low", "medium", "high", "max":
		payload["reasoning_effort"] = req.ReasoningEffort
	}
	if prep.toolsOn && !forceAnswer {
		payload["tools"] = aiToolsPayload(s.allTools())
		payload["tool_choice"] = "auto"
	}
	return payload, forceAnswer
}

// newAIUpstreamRequest 构造上游请求：鉴权头 + SSE Accept + 链路排障 header。
func newAIUpstreamRequest(ctx context.Context, endpoint, apiKey string, req *aiChatRequest, body []byte) (*http.Request, error) {
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+apiKey)
	upReq.Header.Set("Accept", "text/event-stream")
	// 链路排障三件套：每次模型调用新生成 Request-Id；QA/Session 由前端传入后透传
	upReq.Header.Set("X-Model-Request-Id", uuid.NewString())
	if req.QAId != "" {
		upReq.Header.Set("X-QA-Id", req.QAId)
	}
	if req.SessionId != "" {
		upReq.Header.Set("X-Session-Id", req.SessionId)
	}
	return upReq, nil
}

// openAIChatStream 首次建流时写 SSE 响应头，并推送 run_id 与 RAG 命中：
// runId 供断连/刷新后轮询 /api/ai/runs/{id} 恢复；RAG 引用卡片数据一次性下发
// （编号/路径/起始行/原文，前端挂在当前回答气泡上）。
func openAIChatStream(ctx iris.Context, w aiStreamWriter, run *aiRunState, prep *aiChatPrep) {
	ctx.ContentType("text/event-stream; charset=utf-8")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("X-Accel-Buffering", "no")
	ctx.StatusCode(iris.StatusOK)
	sseWriteEvent(w, "run_id", map[string]string{"id": run.id})
	run.addEvent("run_id", map[string]string{"id": run.id})
	if prep.ragHits != nil {
		data := map[string]any{"hits": prep.ragHits}
		sseWriteEvent(w, "rag_hits", data)
		run.addEvent("rag_hits", data)
		prep.ragHits = nil
	}
}

// runAIChatToolRound 执行一轮工具调用：SSE 通知前端 → 执行（写类工具走审批）→
// 结果回传下一轮 messages。每个工具轮落一次快照，断连期间也能看到完成到哪一步。
func (s *Server) runAIChatToolRound(ctx iris.Context, w aiStreamWriter, run *aiRunState, msgs []aiMessage, res aiRoundResult, round int) []aiMessage {
	msgs = append(msgs, aiMessage{Role: "assistant", Content: res.Content, ToolCalls: sanitizeToolCalls(res.ToolCalls)})
	for _, call := range res.ToolCalls {
		sseWriteEvent(w, "tool_call", call)
		run.addEvent("tool_call", call)
		result := s.runToolWithApproval(ctx, w, call)
		data := map[string]any{
			"id":     call.ID,
			"name":   call.Function.Name,
			"result": result,
		}
		sseWriteEvent(w, "tool_result", data)
		run.addEvent("tool_result", data)
		resultJSON, _ := json.Marshal(result)
		msgs = append(msgs, aiMessage{Role: "tool", ToolCallID: call.ID, Content: string(resultJSON)})
	}
	run.flush(s, "running", "")
	// 下一轮即上限轮：提醒模型收尾，避免轮次耗尽仍无文本输出
	if round+1 >= aiMaxToolRounds {
		msgs = append(msgs, aiMessage{Role: "system", Content: "工具调用轮次已达上限。请停止调用工具，基于以上已获得的工具结果直接回答用户问题；信息不足的部分明确说明，不要编造。"})
	}
	return msgs
}
