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
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
)

// aiMaxMessages 单次请求允许携带的最大消息条数（含历史），防滥用。
const aiMaxMessages = 30

// aiMaxBodyBytes 请求体大小上限：消息总内容不超过 512KB。
const aiMaxBodyBytes = 512 * 1024

// aiStreamTimeout 单次对话上游流式响应的最长等待时间。
const aiStreamTimeout = 10 * time.Minute

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
		"model":   s.cfg.AI.Model,
		"tools":   s.cfg.AI.Tools,
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
	upstreamURL := normalizeAIEndpoint(s.cfg.AI.BaseURL)
	toolsOn := s.cfg.AI.Tools
	msgs := req.Messages

	// 绑定请求上下文：客户端断开 → 上游取消
	upCtx, cancel := context.WithTimeout(ctx.Request().Context(), aiStreamTimeout)
	defer cancel()

	w := ctx.ResponseWriter()
	wroteHeader := false

	for round := 0; ; round++ {
		payload := map[string]any{
			"model":    s.cfg.AI.Model,
			"messages": msgs,
			"stream":   true,
		}
		if toolsOn {
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
		upReq.Header.Set("Authorization", "Bearer "+s.cfg.AI.APIKey)
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
			serverError(ctx, "ai upstream request failed", err, "model", s.cfg.AI.Model)
			return
		}

		// 上游非 200：读有限字节拼进错误消息；未开流时按标准信封返回
		if resp.StatusCode != http.StatusOK {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			msg := fmt.Sprintf("upstream %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
			slog.Error("ai upstream error", "status", resp.StatusCode, "model", s.cfg.AI.Model)
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
		}

		// 转发 + 解析本轮流式响应
		reader := bufio.NewReaderSize(resp.Body, 16*1024)
		res, alive := forwardAIStream(w, reader)
		resp.Body.Close()
		if !alive {
			return // 客户端已断开
		}

		// 工具轮：模型请求调工具 → SSE 通知前端 → 后端执行 → 结果回传 → 下一轮
		if toolsOn && res.FinishReason == "tool_calls" && len(res.ToolCalls) > 0 && round < aiMaxToolRounds {
			msgs = append(msgs, aiMessage{Role: "assistant", Content: res.Content, ToolCalls: res.ToolCalls})
			for _, call := range res.ToolCalls {
				sseWriteEvent(w, "tool_call", call)
				result := s.execAITool(ctx, call)
				sseWriteEvent(w, "tool_result", map[string]any{
					"id":     call.ID,
					"name":   call.Function.Name,
					"result": result,
				})
				resultJSON, _ := json.Marshal(result)
				msgs = append(msgs, aiMessage{Role: "tool", ToolCallID: call.ID, Content: string(resultJSON)})
			}
			continue
		}

		// 最终轮（stop / 无工具请求 / 达到轮数上限）：补发统一结束标记
		if _, werr := io.WriteString(w, "data: [DONE]\n\n"); werr != nil {
			return
		}
		w.Flush()
		return
	}
}

// ============================================================
// 上游流式响应的转发与增量解析
// ============================================================

// aiRoundResult 一轮上游流式响应的解析结果
type aiRoundResult struct {
	Content      string      // 本轮增量文本（多轮拼接后即最终回答）
	FinishReason string      // stop | tool_calls | length | ...
	ToolCalls    []aiToolCall // 按 index 升序、分片已拼接完成的工具调用
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

// aiStreamChunk 上游 SSE data 行的结构（只取 choices[0]）
type aiStreamChunk struct {
	Choices []struct {
		Delta        aiStreamDelta `json:"delta"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
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
