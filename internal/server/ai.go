package server

// AI 对话代理（OpenAI 兼容协议）。
//
// 设计要点：
//   - API key 只保存在后端 config（或 AI_API_KEY 环境变量），前端永远拿不到；
//   - POST /api/ai/chat 以 SSE 流式转发上游 /chat/completions 的增量输出，
//     逐行 flush（与 /api/media 同理，loggingMiddleware 必须跳过 ctx.Record，
//     否则 ResponseRecorder 会攒内存、无法流式）；
//   - 上游错误（非 200）在读流之前就能拿到，转成标准 {code,msg} 信封返回；
//   - 客户端断开时 ctx.Request().Context() 取消，上游请求随之取消，不浪费配额。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/rag"
	"github.com/yiiewang/workbench/internal/schedule"
)

// aiMaxMessages 单次请求允许携带的最大消息条数（含历史），防滥用。
const aiMaxMessages = 30

// aiMaxBodyBytes 请求体大小上限：消息总内容不超过 512KB。
const aiMaxBodyBytes = 512 * 1024

// aiStreamTimeout 单次对话上游流式响应的最长等待时间。
const aiStreamTimeout = 10 * time.Minute

// ragSearchTopK RAG 前置检索取回的文本块数。取多了稀释上下文、
// 取少了覆盖不足；5 块 × ~800 字 ≈ 4K 字，对个人知识库场景够用。
const ragSearchTopK = 5

// lastUserContent 取消息序列中最后一条 user 消息文本（RAG 检索 query）
func lastUserContent(msgs []aiMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// ragQueryMinRunes 短问题阈值：低于它视为依赖对话上下文（指代词如
// "v5 怎么用"），拼接上一条 user 消息前缀补充语境
const ragQueryMinRunes = 30

// ragQueryPrevPrefixRunes 上下文前缀截断长度
const ragQueryPrevPrefixRunes = 80

// ragQueryFrom 消息序列提取 RAG 检索 query：
//  1. @ 引用文件时消息为「【引用文件内容】…全文…【引用文件结束】问题」——
//     全文会主导 embedding 向量，只取结束标记后的问题部分；
//  2. 当前问题较短时拼接上一条 user 消息前缀——短问题里的指代
//     （"v5 怎么用"）依赖对话上下文，孤立检索会命中话题歧义内容
//     （库里另一个项目的 v5 报告）。
func ragQueryFrom(msgs []aiMessage) string {
	cur := ragQuery(lastUserContent(msgs))
	if runeLen(cur) >= ragQueryMinRunes {
		return cur
	}
	for i := len(msgs) - 2; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		prev := ragQuery(msgs[i].Content)
		if prev == "" {
			break
		}
		if r := []rune(prev); len(r) > ragQueryPrevPrefixRunes {
			prev = string(r[:ragQueryPrevPrefixRunes])
		}
		return prev + " " + cur
	}
	return cur
}

// ragQuery 剥离引用文件全文（见上），返回实际检索文本
func ragQuery(userContent string) string {
	const endMark = "【引用文件结束】"
	if idx := strings.LastIndex(userContent, endMark); idx >= 0 {
		if q := strings.TrimSpace(userContent[idx+len(endMark):]); q != "" {
			return q
		}
	}
	return strings.TrimSpace(userContent)
}

func runeLen(s string) int { return len([]rune(s)) }

// aiClient 上游 HTTP 客户端：不设整体超时（流式长连接），仅设拨号/TLS/头超时。
var aiClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        4,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

// aiToolCallFunction 工具调用的函数名与参数（arguments 为 JSON 字符串）
type aiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// aiToolCall OpenAI tool_calls 格式（assistant 消息携带）
type aiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // 固定 "function"
	Function aiToolCallFunction `json:"function"`
}

// aiMessage 对话消息（OpenAI chat 格式，含工具调用扩展）
type aiMessage struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`
	// ToolCalls assistant 消息发起的工具调用（模型要求调用工具时 content 可为空）
	ToolCalls []aiToolCall `json:"tool_calls,omitempty"`
	// ToolCallID tool 消息回传结果时对应的调用 ID
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// aiChatRequest POST /api/ai/chat 请求体
type aiChatRequest struct {
	Messages []aiMessage `json:"messages"`
	// QAId 一次用户提问的 ID：同一次提问内的多轮模型调用复用（透传为 X-QA-Id，用于链路排障/会话亲和）
	QAId string `json:"qaId,omitempty"`
	// SessionId 会话 ID：一个会话（面板打开/清空为一个周期）内多次提问复用（透传为 X-Session-Id）
	SessionId string `json:"sessionId,omitempty"`
}

// ============================================================
// GET /api/ai/status — 前端探测 AI 是否可用（决定是否显示入口）
// ============================================================

func (s *Server) handleAIStatus(ctx iris.Context) {
	writeOK(ctx, map[string]any{
		"enabled": s.cfg.AI.Enabled(),
		"model":   s.cfg.AI.Chat.Model,
		"tools":   s.cfg.AI.Chat.Tools,
	})
}

// ============================================================
// /api/ai/sessions — 会话历史 CRUD（回到历史会话继续沟通）
// ============================================================

// aiSessionMaxMessagesJSON messages 快照大小上限（防滥用）
const aiSessionMaxMessagesJSON = 256 * 1024

func (s *Server) handleAISessionsList(ctx iris.Context) {
	sessions, err := s.db.ListAISessions(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), 50)
	if err != nil {
		serverError(ctx, "list ai sessions failed", err)
		return
	}
	if sessions == nil {
		sessions = []db.AISession{}
	}
	writeOK(ctx, map[string]any{"sessions": sessions})
}

type aiSessionBody struct {
	Title    string          `json:"title"`
	Messages json.RawMessage `json:"messages"`
}

// validateAISessionBody 校验请求体：title 限长，messages 必须是合法 JSON 数组且不超上限
func validateAISessionBody(b *aiSessionBody) string {
	if len(b.Title) > 100 {
		return "title too long (max 100)"
	}
	if len(b.Messages) == 0 {
		b.Messages = json.RawMessage("[]")
	}
	if len(b.Messages) > aiSessionMaxMessagesJSON {
		return "messages too large (max 256KB)"
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(b.Messages, &arr); err != nil {
		return "messages must be a JSON array"
	}
	return ""
}

func (s *Server) handleAISessionCreate(ctx iris.Context) {
	var req aiSessionBody
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if msg := validateAISessionBody(&req); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}
	id, err := s.db.CreateAISession(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), req.Title, string(req.Messages))
	if err != nil {
		serverError(ctx, "create ai session failed", err)
		return
	}
	writeOK(ctx, map[string]any{"id": id})
}

func (s *Server) handleAISessionGet(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	sess, exists, err := s.db.GetAISession(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id)
	if err != nil {
		serverError(ctx, "get ai session failed", err)
		return
	}
	if !exists {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	writeOK(ctx, sess)
}

func (s *Server) handleAISessionUpdate(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	var req aiSessionBody
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if msg := validateAISessionBody(&req); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}
	// title 为空时由 SQL 层保留旧值（COALESCE/NULLIF），无需先查再写
	if err := s.db.UpdateAISession(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id, req.Title, string(req.Messages)); err != nil {
		serverError(ctx, "update ai session failed", err)
		return
	}
	writeOK(ctx, map[string]any{"status": "ok"})
}

func (s *Server) handleAISessionDelete(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	if err := s.db.DeleteAISession(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id); err != nil {
		serverError(ctx, "delete ai session failed", err)
		return
	}
	writeOK(ctx, map[string]any{"status": "ok"})
}

// ============================================================
// POST /api/ai/reindex — 手动触发当前用户文件全量重建向量索引
// ============================================================

// ragReindexing 全量重建进行中标记（org:user 维度）：全量 embed 成本高，
// 同用户重复触发直接 409，完成后自动清除。
var ragReindexing sync.Map

func (s *Server) handleAIReindex(ctx iris.Context) {
	if s.rag == nil {
		writeFail(ctx, http.StatusServiceUnavailable, CodeAINotConfigured)
		return
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	key := fmt.Sprintf("%d:%d", orgID, userID)
	if _, running := ragReindexing.LoadOrStore(key, struct{}{}); running {
		writeFail(ctx, iris.StatusConflict, CodeReindexRunning)
		return
	}
	root := s.userRoot(ctx)
	go func() {
		defer ragReindexing.Delete(key)
		stats, err := s.rag.ReindexTree(context.Background(), orgID, userID, root, s.cfg.Server.AllowSymlink)
		if err != nil {
			slog.Error("rag: reindex failed", "err", err, "user", userID)
			return
		}
		slog.Info("rag: reindex done", "user", userID,
			"indexed", stats.Indexed, "skipped", stats.Skipped, "failed", stats.Failed)
	}()
	writeOK(ctx, map[string]any{"started": true})
}

// ============================================================
// GET /api/ai/reindex/status — 全量重建进度（含最近一次已完成记录）
// ============================================================

func (s *Server) handleAIReindexStatus(ctx iris.Context) {
	if s.rag == nil {
		writeFail(ctx, http.StatusServiceUnavailable, CodeAINotConfigured)
		return
	}
	writeOK(ctx, s.rag.ProgressOf(currentOrgID(ctx), currentUserID(ctx)))
}

// ============================================================
// POST /api/ai/chat — SSE 流式对话代理（可选 Agent 循环）
//
// 纯对话模式（ai.tools=false）：上游 SSE 逐行透传（行为与历史版本一致）。
// Agent 模式（ai.tools=true）：
//   1. 携带内置工具清单请求上游（见 aitools.go）；
//   2. 上游返回 finish_reason=tool_calls → 执行工具 → 结果以 tool 消息
//      追加进对话 → 再次请求上游，循环直到模型给出最终回答；
//   3. 工具调用/结果通过自定义 SSE 事件推给前端展示：
//        event: tool_call   data: {id,type,function:{name,arguments}}
//        event: tool_result data: {id,name,result}
//   4. 中间轮上游的 data: [DONE] 不转发（否则前端会提前终止读取），
//      循环结束后统一补发。
// ============================================================

// aiMaxToolRounds 单次对话允许的最大工具轮数（防模型无限循环调用）
const aiMaxToolRounds = 5

// aiSystemPrompt Agent 模式的系统提示：角色 + 当前时间 + 写操作礼仪。
// 模型没有时间感知——此前用户让 AI "建个明天截止的任务"时日期全靠猜。
func aiSystemPrompt(now time.Time) string {
	local := now.Format("2006-01-02 15:04") + " " + []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[now.Weekday()]
	return "你是 Workbench 个人开发平台的 AI 助手，可调用工具帮助用户管理文件、Todo 任务、定时任务与通知。\n" +
		"当前时间：" + local + "。涉及\"今天/明天/下周\"等日期推算时一律以此为准，不要凭空猜测。\n" +
		"行为要求：\n" +
		"1. 执行写操作（创建/修改任务、写文件、发通知、安排定时任务）前，先用一两句话向用户说明你的计划（要做什么、关键参数如日期时刻），再发起工具调用等待批准；一次只安排一个写操作，等确认后再进行下一个。\n" +
		"2. 创建/修改任务时给全时间范围参数：用户说了开始与截止就都填；只说截止则 scheduled 与 due 相同；都没说则默认都为当天。涉及具体钟点（如\"晚上 8 点\"）时同时给对应的 Time 参数，到点会自动邮件提醒。\n" +
		"3. 用户消息中引用的文件内容已附在消息里，无需再调用 read_file。\n" +
		"4. 文件内容类问题优先依据语义检索片段（消息末尾的编号片段）；片段不足以回答时用 search_files 换关键词检索，不要猜测路径盲调 read_file；read_file 只用于已确认存在的具体文件（路径取自检索结果）。\n" +
		"5. 避免重复检索：本会话中已检索到、已读取过的内容视为已知信息，足以回答时直接总结作答，不要为\"确认细节\"反复检索；只有信息确有缺口（关键内容完全缺失）才再检索，且同一问题换关键词不超过两次。\n" +
		"6. 回答保持简洁，直接给出结果。"
}

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
	// QAId/SessionId 会写入上游 header：限长 + 禁控制字符，防 header 注入
	if !validTraceID(req.QAId) || !validTraceID(req.SessionId) {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "qaId/sessionId must be plain identifiers (max 64 chars)")
		return
	}

	// 组装上游请求（OpenAI 兼容 /chat/completions，stream=true）。
	// base_url 兼容两种写法：网关根（…/v1，自动追加 /chat/completions）
	// 或完整端点（…/v1/chat/completions，原样使用），避免配置歧义导致 404。
	upstreamURL := normalizeAIEndpoint(s.cfg.AI.Chat.BaseURL)
	toolsOn := s.cfg.AI.Chat.Tools
	msgs := req.Messages
	// Agent 模式注入系统提示：平台角色 + 当前时间（模型无时间感知，
	// 此前任务日期全靠猜）+ 写操作前置说明要求（先解释计划再请求确认）
	if toolsOn {
		msgs = append([]aiMessage{{Role: "system", Content: aiSystemPrompt(time.Now())}}, msgs...)
	}
	// RAG 前置检索：用最新一条 user 消息在其个人文件中做语义检索，
	// 命中片段以独立 system 消息追加（与前端"引用文件"机制并存）。
	// 检索失败/超时静默降级——对话可用性优先于检索完整性。
	var ragHits []rag.Hit
	if s.rag != nil {
		if q := ragQueryFrom(req.Messages); q != "" {
			hits, err := s.rag.Search(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), q, ragSearchTopK)
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
				ragHits = hits
				msgs = append(msgs, aiMessage{Role: "system", Content: rag.ContextPrompt(hits)})
			}
		}
	}

	// 绑定请求上下文：客户端断开 → 上游取消
	upCtx, cancel := context.WithTimeout(ctx.Request().Context(), aiStreamTimeout)
	defer cancel()

	w := ctx.ResponseWriter()
	wroteHeader := false
	usageSum := aiUsage{} // Agent 多轮 token 消耗累加

	for round := 0; ; round++ {
		// 工具轮次用尽后不再携带 tools：模型只能文本回答。
		// 否则上限轮模型仍可能请求调工具被掐断，表现为"只有工具卡片没有回答"。
		forceAnswer := round >= aiMaxToolRounds
		payload := map[string]any{
			"model":    s.cfg.AI.Chat.Model,
			"messages": msgs,
			"stream":   true,
			// 流式响应末尾带 usage 分片（token 消耗统计）；网关不支持时
			// 分片缺失，前端不显示消耗（无害降级）
			"stream_options": map[string]any{"include_usage": true},
		}
		if toolsOn && !forceAnswer {
			payload["tools"] = aiToolsPayload()
			payload["tool_choice"] = "auto"
		}
		body, err := json.Marshal(payload)
		if err != nil {
			serverError(ctx, "marshal ai payload failed", err)
			return
		}

		upReq, err := http.NewRequestWithContext(upCtx, http.MethodPost, upstreamURL, bytes.NewReader(body))
		if err != nil {
			serverError(ctx, "build ai upstream request failed", err)
			return
		}
		upReq.Header.Set("Content-Type", "application/json")
		upReq.Header.Set("Authorization", "Bearer "+s.cfg.AI.Chat.APIKey)
		upReq.Header.Set("Accept", "text/event-stream")
		// 链路排障三件套：每次模型调用新生成 Request-Id；QA/Session 由前端传入后透传
		upReq.Header.Set("X-Model-Request-Id", uuid.NewString())
		if req.QAId != "" {
			upReq.Header.Set("X-QA-Id", req.QAId)
		}
		if req.SessionId != "" {
			upReq.Header.Set("X-Session-Id", req.SessionId)
		}

		resp, err := aiClient.Do(upReq)
		if err != nil {
			// 流已开（Agent 前几轮已推给前端）时只能走 SSE 错误事件，
			// HTTP 状态码已发出无法再改
			if wroteHeader {
				sseWriteEvent(w, "error", map[string]string{"msg": "ai upstream request failed"})
				return
			}
			serverError(ctx, "ai upstream request failed", err, "model", s.cfg.AI.Chat.Model)
			return
		}

		// 上游非 200：读有限字节拼进错误消息；未开流时按标准信封返回
		if resp.StatusCode != http.StatusOK {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			msg := fmt.Sprintf("upstream %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
			slog.Error("ai upstream error", "status", resp.StatusCode, "model", s.cfg.AI.Chat.Model)
			if wroteHeader {
				sseWriteEvent(w, "error", map[string]string{"msg": msg})
				return
			}
			writeFailMsg(ctx, iris.StatusBadGateway, CodeInternalError, msg)
			return
		}

		if !wroteHeader {
			ctx.ContentType("text/event-stream; charset=utf-8")
			ctx.Header("Cache-Control", "no-cache")
			ctx.Header("X-Accel-Buffering", "no")
			ctx.StatusCode(iris.StatusOK)
			wroteHeader = true
			// RAG 引用卡片数据（编号/路径/起始行/原文）：SSE header 就绪后
			// 立刻推给前端（一次性），前端挂在当前回答气泡上
			if ragHits != nil {
				sseWriteEvent(w, "rag_hits", map[string]any{"hits": ragHits})
				ragHits = nil
			}
		}

		// 转发 + 解析本轮流式响应
		reader := bufio.NewReaderSize(resp.Body, 16*1024)
		res, alive := forwardAIStream(w, reader)
		resp.Body.Close()
		if !alive {
			return // 客户端已断开
		}
		// 聚合本轮 token 消耗（Agent 多轮累加）
		if res.Usage != nil {
			usageSum.PromptTokens += res.Usage.PromptTokens
			usageSum.CompletionTokens += res.Usage.CompletionTokens
			usageSum.TotalTokens += res.Usage.TotalTokens
		}

		// 工具轮：模型请求调工具 → SSE 通知前端 → 后端执行 → 结果回传 → 下一轮
		if toolsOn && !forceAnswer && res.FinishReason == "tool_calls" && len(res.ToolCalls) > 0 {
			msgs = append(msgs, aiMessage{Role: "assistant", Content: res.Content, ToolCalls: res.ToolCalls})
			for _, call := range res.ToolCalls {
				sseWriteEvent(w, "tool_call", call)
				result := s.runToolWithApproval(ctx, w, call)
				sseWriteEvent(w, "tool_result", map[string]any{
					"id":     call.ID,
					"name":   call.Function.Name,
					"result": result,
				})
				resultJSON, _ := json.Marshal(result)
				msgs = append(msgs, aiMessage{Role: "tool", ToolCallID: call.ID, Content: string(resultJSON)})
			}
			// 下一轮即上限轮：提醒模型收尾，避免轮次耗尽仍无文本输出
			if round+1 >= aiMaxToolRounds {
				msgs = append(msgs, aiMessage{Role: "system", Content: "工具调用轮次已达上限。请停止调用工具，基于以上已获得的工具结果直接回答用户问题；信息不足的部分明确说明，不要编造。"})
			}
			continue
		}

		// 最终轮（stop / 无工具请求 / 达到轮数上限）：usage 事件 + 统一结束标记
		if usageSum.TotalTokens > 0 {
			sseWriteEvent(w, "usage", usageSum)
		}
		if _, werr := io.WriteString(w, "data: [DONE]\n\n"); werr != nil {
			return
		}
		w.Flush()
		return
	}
}

// ============================================================
// 非流式单次调用（AI 定时任务等后台场景用）
// ============================================================

// aiChatOnce 非流式调用上游 /chat/completions，返回首个回复文本。
// 不走 Agent 循环/工具（定时任务是后台生成型任务，无需交互）。
func (s *Server) aiChatOnce(ctx context.Context, prompt string) (string, error) {
	if !s.cfg.AI.Enabled() {
		return "", fmt.Errorf("AI not configured (ai.api_key / AI_BASE_URL)")
	}
	payload, err := json.Marshal(map[string]any{
		"model": s.cfg.AI.Chat.Model,
		"messages": []aiMessage{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeAIEndpoint(s.cfg.AI.Chat.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.AI.Chat.APIKey)
	req.Header.Set("X-Model-Request-Id", uuid.NewString())

	resp, err := aiClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upstream %s: %s", resp.Status, truncateStr(string(body), 200))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("unexpected upstream response")
	}
	return out.Choices[0].Message.Content, nil
}

// truncateStr 字符串截断（错误信息拼装用）
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ============================================================
// AI 定时任务 CRUD — /api/ai/jobs
// ============================================================

// aiJobBody 创建/更新请求体
type aiJobBody struct {
	Name     string `json:"name"`
	CronExpr string `json:"cronExpr"`
	Prompt   string `json:"prompt"`
	Enabled  *bool  `json:"enabled"`
}

// validateAIJobBody 校验：name/cron/prompt 必填，cron 表达式可解析
func validateAIJobBody(b *aiJobBody) string {
	b.Name = strings.TrimSpace(b.Name)
	b.CronExpr = strings.TrimSpace(b.CronExpr)
	b.Prompt = strings.TrimSpace(b.Prompt)
	if b.Name == "" {
		return "name is required"
	}
	if len(b.Name) > 100 {
		return "name too long (max 100)"
	}
	if b.CronExpr == "" {
		return "cronExpr is required"
	}
	if _, err := schedule.ParseCron(b.CronExpr); err != nil {
		return "invalid cron expression: " + err.Error()
	}
	if b.Prompt == "" {
		return "prompt is required"
	}
	if len(b.Prompt) > 16*1024 {
		return "prompt too long (max 16KB)"
	}
	if b.Enabled == nil {
		t := true
		b.Enabled = &t
	}
	return ""
}

func (s *Server) handleAIJobsList(ctx iris.Context) {
	jobs, err := s.db.ListAIJobs(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx))
	if err != nil {
		serverError(ctx, "list ai jobs failed", err)
		return
	}
	writeOK(ctx, map[string]any{"jobs": jobs})
}

func (s *Server) handleAIJobCreate(ctx iris.Context) {
	var req aiJobBody
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if msg := validateAIJobBody(&req); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}
	id, err := s.db.CreateAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), req.Name, req.CronExpr, req.Prompt, *req.Enabled)
	if err != nil {
		serverError(ctx, "create ai job failed", err)
		return
	}
	slog.Info("ai job created", "user", currentUserID(ctx), "job", req.Name, "cron", req.CronExpr)
	writeOK(ctx, map[string]any{"id": id})
}

func (s *Server) handleAIJobUpdate(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	var req aiJobBody
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if msg := validateAIJobBody(&req); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}
	// 存在性 + 归属校验
	cur, err := s.db.GetAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id)
	if err != nil {
		serverError(ctx, "get ai job failed", err)
		return
	}
	if cur == nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	if err := s.db.UpdateAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id, req.Name, req.CronExpr, req.Prompt, *req.Enabled); err != nil {
		serverError(ctx, "update ai job failed", err)
		return
	}
	slog.Info("ai job updated", "user", currentUserID(ctx), "job", req.Name)
	writeOK(ctx, map[string]any{"status": "ok"})
}

func (s *Server) handleAIJobDelete(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	if err := s.db.DeleteAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id); err != nil {
		serverError(ctx, "delete ai job failed", err)
		return
	}
	slog.Info("ai job deleted", "user", currentUserID(ctx), "id", id)
	writeOK(ctx, map[string]any{"status": "ok"})
}

// handleAIJobRuns 任务执行历史（GET /api/ai/jobs/{id}/runs?limit=20）。
// 归属：全局任务（user_id IS NULL）所有用户可查；私有任务仅归属人。
func (s *Server) handleAIJobRuns(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	limit, _ := strconv.Atoi(ctx.URLParam("limit"))
	job, err := s.db.GetAIJobAny(ctx.Request().Context(), id)
	if err != nil {
		serverError(ctx, "get ai job failed", err)
		return
	}
	if job == nil || (!job.IsGlobal && int64(job.UserID) != currentUserID(ctx)) {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	runs, err := s.db.ListAIJobRuns(ctx.Request().Context(), id, limit)
	if err != nil {
		serverError(ctx, "list ai job runs failed", err)
		return
	}
	writeOK(ctx, map[string]any{"runs": runs})
}

// ============================================================
// 写类工具审批（Human-in-the-loop，协调器见 aiapproval.go）
// ============================================================

// runToolWithApproval 执行一次工具调用，写类工具先经用户审批：
//
//	tool_call 事件先发（前端立即建卡片）→ approval_request 事件（卡片进入待确认态）
//	→ 阻塞等待 POST /api/ai/approval 的决定（或超时/断开）
//	→ approval_resolved 事件（卡片定格）→ 执行或跳过
//
// 返回值即 tool 消息内容：拒绝/超时返回说明性结果，由模型向用户解释。
func (s *Server) runToolWithApproval(ctx iris.Context, w aiStreamWriter, call aiToolCall) any {
	tool := aiToolByName(call.Function.Name)
	if tool == nil || !tool.NeedApproval {
		return s.execAITool(ctx, call) // 读类工具或未知工具直接走原路径
	}

	// 审批 ID 直接复用 tool_call ID（一次对话内上游保证唯一）
	ch := s.aiApprovals.create(call.ID, currentUserID(ctx))
	sseWriteEvent(w, "approval_request", map[string]any{
		"id":        call.ID,
		"name":      call.Function.Name,
		"arguments": call.Function.Arguments,
	})

	var approved bool
	var outcome string
	select {
	case approved = <-ch:
		if approved {
			outcome = "approved"
		} else {
			outcome = "declined"
		}
	case <-time.After(aiApprovalTimeout):
		s.aiApprovals.remove(call.ID)
		approved, outcome = false, "timeout"
	case <-ctx.Request().Context().Done():
		// 客户端断开（关面板/停止生成）：作废审批，不执行
		s.aiApprovals.remove(call.ID)
		slog.Info("ai approval aborted", "tool", call.Function.Name, "reason", "client disconnected")
		return map[string]any{"error": "client disconnected before approval"}
	}

	sseWriteEvent(w, "approval_resolved", map[string]any{
		"id":      call.ID,
		"outcome": outcome,
	})

	if !approved {
		switch outcome {
		case "declined":
			return map[string]any{"error": "user declined this operation"}
		default: // timeout
			return map[string]any{"error": "user did not confirm within 5 minutes, operation cancelled"}
		}
	}
	return s.execAITool(ctx, call)
}

// handleAIApproval POST /api/ai/approval — 用户对写工具调用的审批决定。
// 请求体 {id, approved}；id 为 approval_request 事件携带的 tool_call ID。
func (s *Server) handleAIApproval(ctx iris.Context) {
	var req struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if req.ID == "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "id is required")
		return
	}
	if !s.aiApprovals.resolve(req.ID, currentUserID(ctx), req.Approved) {
		// 不存在 / 已处理 / 非本人发起：统一按未找到返回，不泄露其他用户的审批状态
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	slog.Info("ai approval resolved", "user", currentUserID(ctx), "id", req.ID, "approved", req.Approved)
	writeOK(ctx, map[string]any{"status": "ok"})
}

// ============================================================
// 上游流式响应的转发与增量解析
// ============================================================

// aiRoundResult 一轮上游流式响应的解析结果
type aiRoundResult struct {
	Content      string       // 本轮增量文本（多轮拼接后即最终回答）
	FinishReason string       // stop | tool_calls | length | ...
	ToolCalls    []aiToolCall // 按 index 升序、分片已拼接完成的工具调用
	Usage        *aiUsage     // 本轮 token 消耗（stream_options.include_usage，末尾分片携带）
}

// aiUsage 上游 usage 分片（Agent 多轮由调用方累加）
type aiUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// aiStreamDelta 上游流式分片中需要解析的增量字段
type aiStreamDelta struct {
	Content   string `json:"content"`
	ToolCalls []struct {
		Index    int    `json:"index"`
		ID       string `json:"id,omitempty"`
		Function struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

// aiStreamChunk 上游 SSE data 行的结构（只取 choices[0]；usage 分片 choices 为空）
type aiStreamChunk struct {
	Choices []struct {
		Delta        aiStreamDelta `json:"delta"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage *aiUsage `json:"usage"`
}

// aiStreamWriter SSE 输出流：写增量 + 立即 flush（iris 的 ResponseWriter 满足此接口）
type aiStreamWriter interface {
	io.Writer
	Flush()
}

// forwardAIStream 逐行转发上游 SSE 给前端并同步解析增量：
//   - data: [DONE] 不转发（Agent 中间轮的结束标记不能提前终止前端读取，
//     由调用方在最终轮后统一补发）；其余行原样透传 + flush；
//   - tool_calls 分片按 index 累积（id/name 首个分片携带，arguments 逐片拼接）；
//   - 返回 alive=false 表示客户端已断开。
func forwardAIStream(w aiStreamWriter, reader *bufio.Reader) (aiRoundResult, bool) {
	var res aiRoundResult
	acc := map[int]*aiToolCall{}

	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if strings.TrimRight(line, "\r\n") == "data: [DONE]" {
				// 不转发，等最终轮统一发送
			} else {
				if _, werr := io.WriteString(w, line); werr != nil {
					return res, false // 客户端已断开
				}
				w.Flush()
			}
			parseAIDataLine(line, &res, acc)
		}
		if err != nil {
			if err != io.EOF {
				slog.Error("ai stream read failed", "err", err)
			}
			break
		}
	}

	// index 升序输出累积完成的工具调用
	for i := 0; i < len(acc); i++ {
		if c, ok := acc[i]; ok {
			res.ToolCalls = append(res.ToolCalls, *c)
		}
	}
	return res, true
}

// parseAIDataLine 解析一行 SSE data（data: {...}），累积内容与工具调用分片
func parseAIDataLine(line string, res *aiRoundResult, acc map[int]*aiToolCall) {
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(line[len("data:"):])
	if payload == "" || payload == "[DONE]" {
		return
	}
	var chunk aiStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return // 错误分片/未知格式：透传已做，解析忽略
	}
	if chunk.Usage != nil {
		res.Usage = chunk.Usage
	}
	for _, ch := range chunk.Choices {
		if ch.FinishReason != "" {
			res.FinishReason = ch.FinishReason
		}
		res.Content += ch.Delta.Content
		for _, tc := range ch.Delta.ToolCalls {
			cur, ok := acc[tc.Index]
			if !ok {
				cur = &aiToolCall{Type: "function"}
				acc[tc.Index] = cur
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				cur.Function.Name = tc.Function.Name
			}
			cur.Function.Arguments += tc.Function.Arguments
		}
	}
}

// sseWriteEvent 向 SSE 流写一个自定义事件（event + data 两行 + 空行）
func sseWriteEvent(w aiStreamWriter, event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	w.Flush()
}

// normalizeAIEndpoint 归一化上游端点：
//   - 网关根形式（如 https://gw.example/v1）→ 追加 /chat/completions
//   - 完整端点形式（已以 /chat/completions 结尾）→ 原样返回
func normalizeAIEndpoint(baseURL string) string {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	return u + "/chat/completions"
}

// validTraceID 校验透传到上游 header 的链路 ID（X-QA-Id / X-Session-Id）：
// 空串合法（选填）；非空时限长 64 且只允许可见 ASCII（防 header 注入）。
func validTraceID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x21 || id[i] > 0x7e {
			return false
		}
	}
	return true
}

// validateAIMessages 校验消息列表，返回空串表示合法，否则为错误描述。
// 工具消息规则：
//   - assistant 带 tool_calls 时 content 允许为空；
//   - tool 消息必须携带 tool_call_id 且 content 非空（回传给模型的执行结果）。
func validateAIMessages(msgs []aiMessage) string {
	if len(msgs) == 0 {
		return "messages is required"
	}
	if len(msgs) > aiMaxMessages {
		return fmt.Sprintf("too many messages (max %d)", aiMaxMessages)
	}
	total := 0
	for i, m := range msgs {
		switch m.Role {
		case "system", "user":
			if strings.TrimSpace(m.Content) == "" {
				return fmt.Sprintf("messages[%d].content is empty", i)
			}
		case "assistant":
			if len(m.ToolCalls) == 0 && strings.TrimSpace(m.Content) == "" {
				return fmt.Sprintf("messages[%d].content is empty", i)
			}
			for j, tc := range m.ToolCalls {
				if tc.Function.Name == "" || tc.ID == "" {
					return fmt.Sprintf("messages[%d].tool_calls[%d] missing id or function.name", i, j)
				}
				total += len(tc.Function.Arguments)
			}
		case "tool":
			if m.ToolCallID == "" {
				return fmt.Sprintf("messages[%d].tool_call_id is required", i)
			}
			if strings.TrimSpace(m.Content) == "" {
				return fmt.Sprintf("messages[%d].content is empty", i)
			}
		default:
			return fmt.Sprintf("messages[%d].role must be system|user|assistant|tool", i)
		}
		total += len(m.Content)
		if total > aiMaxBodyBytes {
			return "messages too large (max 512KB)"
		}
	}
	return ""
}
