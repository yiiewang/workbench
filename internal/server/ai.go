package server

// AI 对话代理（OpenAI 兼容协议）。
//
// 设计要点：
//   - API key 只保存在后端 config（或 AI_API_KEY 环境变量），前端永远拿不到；
//   - POST /api/ai/chat 以 SSE 流式转发上游 /chat/completions 的增量输出，
//     逐行 flush（与 /api/media 同理，loggingMiddleware 必须跳过 ctx.Record，
//     否则 ResponseRecorder 会攒内存、无法流式）；
//   - 上游错误（非 200）在读流之前就能拿到，转成标准 {code,msg} 信封返回；
//   - 客户端断开时上游请求不取消，后台 run 继续跑完并把增量与结果落库，
//     刷新后由 GET /api/ai/runs/{id}?offset= 按游标续读。

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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

// aiRunTimeout 一次后台对话运行的总超时（多轮工具执行 + 上游生成）。
const aiRunTimeout = 15 * time.Minute

// 后台运行的上下文与状态存入 iris Values 的 key：工具执行器与写工具审批读取
const (
	aiRunCtxKey   = "aiRunContext"
	aiRunStateKey = "aiRunState"
)

// aiRunFlushInterval 后台 run 正文增量落库的最小间隔：让断连/刷新后的恢复
// 能按游标持续拿到新文本，又不会每个 token 都写一次 SQLite。
const aiRunFlushInterval = 300 * time.Millisecond

// aiRunState 一次对话运行的状态：结构化事件、累积回复、断连标记。
// 由单个 handleAIChat 循环串行读写，无需加锁。
type aiRunState struct {
	id           string
	orgID        int64
	userID       int64
	events       []any
	content      strings.Builder
	disconnected bool
	lastFlush    time.Time
}

func (r *aiRunState) addEvent(name string, data any) {
	r.events = append(r.events, map[string]any{"event": name, "data": data})
}

// flush 把当前快照写入 ai_runs。收尾写用 background context：
// 客户端断开或运行超时也要把最终状态落库。
func (r *aiRunState) flush(s *Server, status, errMsg string) {
	events, _ := json.Marshal(r.events)
	if err := s.db.SaveAIRun(context.Background(), r.orgID, r.userID, db.AIRun{
		ID: r.id, Status: status, Events: events, Result: r.content.String(), Error: errMsg,
	}); err != nil {
		slog.Warn("ai run flush failed", "run", r.id, "err", err)
	}
}

// maybeFlush 流式期间按间隔落一次 running 快照，让刷新恢复能持续读到新文本。
func (r *aiRunState) maybeFlush(s *Server) {
	if time.Since(r.lastFlush) < aiRunFlushInterval {
		return
	}
	r.lastFlush = time.Now()
	r.flush(s, "running", "")
}

// aiToolRunCtx 工具执行用的 context：后台 run 与请求连接解耦，客户端断开后
// Request().Context() 已被取消，改用 run 的 context 继续读写 DB / RAG。
// 非对话路径（无 Values）回退到请求上下文，保持原有语义。
func aiToolRunCtx(ctx iris.Context) context.Context {
	if v := ctx.Values().Get(aiRunCtxKey); v != nil {
		if c, ok := v.(context.Context); ok {
			return c
		}
	}
	return ctx.Request().Context()
}

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
	// ReasoningContent 非流式响应里的思考过程（部分网关返回；定时任务落库用）
	ReasoningContent string `json:"reasoning_content,omitempty"`
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
	// MaxTokens 单次回答的输出上限（0 = 用网关默认）
	MaxTokens int `json:"maxTokens,omitempty"`
	// ReasoningEffort 推理强度（low/medium/high/max）；上游不支持时忽略
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// Gateway 模型网关 id；空 = 默认网关
	Gateway string `json:"gateway,omitempty"`
}

// ============================================================
// GET /api/ai/status — 前端探测 AI 是否可用（决定是否显示入口）
// ============================================================

func (s *Server) handleAIStatus(ctx iris.Context) {
	gateways := make([]map[string]any, 0, len(s.cfg.AI.Chat.Gateways))
	for _, g := range s.cfg.AI.Chat.Gateways {
		if !g.Enabled() {
			continue
		}
		gateways = append(gateways, map[string]any{
			"id": g.ID, "name": g.Name, "model": g.Model,
		})
	}
	def, _ := s.cfg.AI.Chat.DefaultGateway()
	writeOK(ctx, map[string]any{
		"enabled":  s.cfg.AI.Enabled(),
		"model":    def.Model,
		"default":  def.ID,
		"gateways": gateways,
		"tools":    s.cfg.AI.Chat.Tools,
	})
}

// ============================================================
// /api/ai/sessions — 会话历史 CRUD（回到历史会话继续沟通）
// ============================================================

// aiSessionMaxMessagesJSON 新建会话时首批消息大小上限（防滥用）
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
	id, ok := pathID(ctx)
	if !ok {
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

// ============================================================
// PUT /api/ai/sessions/{id}/messages — 消息级追加写入
// ============================================================

const (
	// aiSessionMaxBatchMessages 单批增量消息条数上限（防滥用）
	aiSessionMaxBatchMessages = 50
	// aiSessionMaxBatchBytes 单批消息体大小上限
	aiSessionMaxBatchBytes = 512 * 1024
	// aiSessionMaxClientMsgID clientMsgId 长度上限
	aiSessionMaxClientMsgID = 64
)

type aiSessionMessagesBody struct {
	Messages      json.RawMessage `json:"messages"`
	TruncateAfter string          `json:"truncateAfter"`
}

// validateAISessionMessagesBody 消息级写入要求每条都带 clientMsgId：
// 增量提交没有数组下标可用，缺 id 就无法区分「新增」与「更新」。
func validateAISessionMessagesBody(b *aiSessionMessagesBody) string {
	if len(b.Messages) == 0 {
		b.Messages = json.RawMessage("[]")
	}
	if len(b.Messages) > aiSessionMaxBatchBytes {
		return "messages too large (max 512KB)"
	}
	if len(b.TruncateAfter) > aiSessionMaxClientMsgID {
		return "truncateAfter too long (max 64)"
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(b.Messages, &arr); err != nil {
		return "messages must be a JSON array"
	}
	if len(arr) > aiSessionMaxBatchMessages {
		return "too many messages (max 50)"
	}
	for _, m := range arr {
		var id, role string
		if v, ok := m["clientMsgId"]; !ok || json.Unmarshal(v, &id) != nil || id == "" || len(id) > aiSessionMaxClientMsgID {
			return "each message requires a clientMsgId (max 64)"
		}
		if v, ok := m["role"]; !ok || json.Unmarshal(v, &role) != nil || role == "" {
			return "each message requires role"
		}
		if _, ok := m["content"]; !ok {
			return "each message requires content"
		}
	}
	return ""
}

// handleAISessionMessagesUpsert 只提交变动的那几条消息；truncateAfter 用于
// regenerate：保留锚点消息，删掉它之后的旧回复，再写入新回复。
func (s *Server) handleAISessionMessagesUpsert(ctx iris.Context) {
	id, ok := pathID(ctx)
	if !ok {
		return
	}
	var req aiSessionMessagesBody
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if msg := validateAISessionMessagesBody(&req); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}
	err := s.db.UpsertAISessionMessages(ctx.Request().Context(),
		currentOrgID(ctx), currentUserID(ctx), id, string(req.Messages), req.TruncateAfter)
	if errors.Is(err, sql.ErrNoRows) {
		// 会话不存在，或 truncateAfter 指向的锚点消息不在该会话里
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	if err != nil {
		serverError(ctx, "upsert ai session messages failed", err)
		return
	}
	writeOK(ctx, map[string]any{"status": "ok"})
}

func (s *Server) handleAISessionDelete(ctx iris.Context) {
	id, ok := pathID(ctx)
	if !ok {
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

// reindexStartStatus 全量重建的启动结果：HTTP 接口与 AI 工具共用同一套判定
type reindexStartStatus int

const (
	reindexStarted reindexStartStatus = iota
	reindexUnavailable
	reindexAlreadyRunning
)

// startReindex 以当前请求用户身份在后台启动全量重建；防重标记与异步执行语义
// 由 POST /api/ai/reindex 和 reindex_files 工具共享，避免两套逻辑漂移。
func (s *Server) startReindex(ctx iris.Context) reindexStartStatus {
	if s.rag == nil {
		return reindexUnavailable
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	key := fmt.Sprintf("%d:%d", orgID, userID)
	if _, running := ragReindexing.LoadOrStore(key, struct{}{}); running {
		return reindexAlreadyRunning
	}
	root := s.userRoot(ctx)
	// 同步预写 running 快照：堵住「返回 started 与后台写入进度之间」的窗口，
	// 否则紧随其后的 reindex_status（流水线背靠背步骤）误报「尚未触发过」
	s.rag.MarkReindexStarted(orgID, userID)
	go func() {
		defer ragReindexing.Delete(key)
		stats, err := s.rag.ReindexTree(context.Background(), orgID, userID, root, s.cfg.Server.AllowSymlink)
		if err != nil {
			slog.Error("rag: reindex failed", "err", err, "user", userID)
			return
		}
		slog.Info("rag: reindex done", "user", userID,
			"indexed", stats.Indexed, "skipped", stats.Skipped, "failed", stats.Failed,
			"pruned", stats.Pruned)
	}()
	return reindexStarted
}

func (s *Server) handleAIReindex(ctx iris.Context) {
	switch s.startReindex(ctx) {
	case reindexUnavailable:
		writeFail(ctx, http.StatusServiceUnavailable, CodeAINotConfigured)
	case reindexAlreadyRunning:
		writeFail(ctx, iris.StatusConflict, CodeReindexRunning)
	default:
		writeOK(ctx, map[string]any{"started": true})
	}
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
// GET /api/ai/index-status — 文件索引状态（汇总 + 明细）
// ============================================================

func (s *Server) handleAIIndexStatus(ctx iris.Context) {
	if s.rag == nil {
		writeFail(ctx, http.StatusServiceUnavailable, CodeAINotConfigured)
		return
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	summary, err := s.db.FileIndexSummaryOf(ctx.Request().Context(), orgID, userID)
	if err != nil {
		serverError(ctx, "index summary failed", err)
		return
	}
	items, err := s.db.ListFileIndexState(ctx.Request().Context(), orgID, userID, 500)
	if err != nil {
		serverError(ctx, "index list failed", err)
		return
	}
	writeOK(ctx, map[string]any{"summary": summary, "items": items})
}

// ============================================================
// 后台对话运行状态：客户端断开后仍可查询已跑完的结果
// ============================================================

func (s *Server) handleAIRunsList(ctx iris.Context) {
	limit := queryLimit(ctx)
	runs, err := s.db.ListAIRuns(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), limit)
	if err != nil {
		serverError(ctx, "list ai runs failed", err)
		return
	}
	writeOK(ctx, map[string]any{"runs": runs})
}

// aiRunDetail 增量详情响应：带 offset 时只回 delta + 下一游标 + 元信息，
// result 置空，避免长回复每 300ms 轮询都重复传输一遍全量正文。
type aiRunDetail struct {
	db.AIRun
	Delta  string `json:"delta"`
	Offset int    `json:"offset"`
}

// splitRunResult 按 rune 而非字节切增量：中文/emoji 在前后端口径一致。
// offset 越界时钳到两端，重复请求同一游标只会拿到空增量。
func splitRunResult(result string, offset int) (string, int) {
	runes := []rune(result)
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes) {
		offset = len(runes)
	}
	return string(runes[offset:]), len(runes)
}

func (s *Server) handleAIRunGet(ctx iris.Context) {
	run, ok, err := s.db.GetAIRun(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), ctx.Params().Get("id"))
	if err != nil {
		serverError(ctx, "get ai run failed", err)
		return
	}
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	rawOffset := ctx.URLParam("offset")
	if rawOffset == "" {
		// 不带游标：只查状态/归属的调用，返回原有全量快照
		writeOK(ctx, run)
		return
	}
	offset, _ := strconv.Atoi(rawOffset)
	delta, next := splitRunResult(run.Result, offset)
	run.Result = ""
	writeOK(ctx, aiRunDetail{AIRun: run, Delta: delta, Offset: next})
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

// aiSystemPrompt Agent 模式的系统提示：角色 + 当前时间 + 思考/执行协议。
// 只放模型无法从工具描述获得的约束：任务时间参数在 create_task/update_task，
// allowWrite 语义在 create_job，片段编号格式在 RAG 注入块，避免同一知识
// 常驻 system 并在每轮被重复自检。
func aiSystemPrompt(now time.Time) string {
	local := now.Format("2006-01-02 15:04") + " " + []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[now.Weekday()]
	return "你是 Workbench 个人开发平台的 AI 助手，可调用工具帮助用户管理文件、Todo 任务、定时任务与通知。\n" +
		"当前时间：" + local + "。涉及\"今天/明天/下周\"等日期推算时一律以此为准，不要凭空猜测。\n" +
		"思考与执行协议：\n" +
		"1. 先确定目标，再按需调用工具。同一轮可以并行发起互不依赖的调用，但同一目的不要重复发起。\n" +
		"2. 只有用户文件内容相关的问题才用 search_files；通用知识、方案讨论直接回答。信息足够时直接给出结论，不要为确认细节反复检索；检索无命中即停止并说明未找到。\n" +
		"3. 执行写操作（创建/修改任务、写文件、发通知、安排定时任务）前，先用一两句话说明计划与关键参数，一次只安排一个写操作，等确认后再进行下一个。\n" +
		"4. 用户消息中已附的文件内容无需再调用 read_file；read_file 只用于已确认存在的具体文件。\n" +
		"5. 回答只给结论与依据，不复述已排除的方案，不编造。\n"
}

// sanitizeToolCalls 把无法解析的 arguments 置为 {}：模型输出被 max_tokens
// 截断时，原样回传会让上游解析请求体失败（400 unexpected end of data），
// 模型连工具错误都看不到。工具执行仍用原始 call，回灌的消息只保留合法占位。
func sanitizeToolCalls(calls []aiToolCall) []aiToolCall {
	out := make([]aiToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		if !json.Valid([]byte(out[i].Function.Arguments)) {
			out[i].Function.Arguments = "{}"
		}
	}
	return out
}

// oneLine 把多行用户消息压成单行，避免日志出现换行与连续空白。
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// summarizeRagHits 聚合检索命中：同一文件计为 path×N，距离只打全局范围。
func summarizeRagHits(hits []rag.Hit) string {
	if len(hits) == 0 {
		return ""
	}
	counts := make(map[string]int, len(hits))
	order := make([]string, 0, len(hits))
	minD, maxD := hits[0].Distance, hits[0].Distance
	for _, h := range hits {
		if counts[h.Path] == 0 {
			order = append(order, h.Path)
		}
		counts[h.Path]++
		if h.Distance < minD {
			minD = h.Distance
		}
		if h.Distance > maxD {
			maxD = h.Distance
		}
	}
	parts := make([]string, 0, len(order))
	for _, p := range order {
		parts = append(parts, fmt.Sprintf("%s×%d", p, counts[p]))
	}
	return fmt.Sprintf("%s (%.3f~%.3f)", strings.Join(parts, ","), minD, maxD)
}

// ragBriefPrompt 后续轮使用的检索片段摘要：编号与来源行号保持一致，正文只留
// 前 200 字。每轮请求都是无状态的完整 messages，直接移除片段会让模型丢失首轮
// 已注入的内容；摘要版在保留引用定位的前提下把上下文压到约三分之一。
func ragBriefPrompt(hits []rag.Hit) string {
	var b strings.Builder
	b.WriteString("以下语义检索片段为摘要（完整原文已在首轮提供，编号一致）；回答用到时标注编号。\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "\n[%d] %s 第%d行起\n%s\n", i+1, h.Path, h.StartLine, truncateStr(h.Content, 200))
	}
	return b.String()
}

// summarizeToolResult 给工具结果前置一行模板摘要（状态/关键集合大小/字节数）。
// 模型仍能看到完整 JSON，但不必为了"确认自己看到了什么"把整段结果复述进思考。
func summarizeToolResult(name string, raw []byte) string {
	line := "[tool] " + name
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return line + fmt.Sprintf(" | %d bytes\n", len(raw))
	}
	if ok, exists := m["ok"]; exists {
		line += fmt.Sprintf(" | ok=%v", ok)
	}
	if e, _ := m["error"].(string); e != "" {
		line += " | error=" + truncateStr(e, 80)
	}
	for _, k := range []string{"jobs", "tasks", "hits", "files", "events", "results", "steps", "items", "runs"} {
		if arr, ok := m[k].([]any); ok {
			line += fmt.Sprintf(" | %s=%d", k, len(arr))
		}
	}
	return line + fmt.Sprintf(" | %d bytes\n", len(raw))
}

// ============================================================
// 非流式单次调用（编排 llm 步骤等后台场景用）
// ============================================================

// aiChatOnce 非流式调用上游 /chat/completions，返回首个回复文本。
// 不带工具；带工具白名单的 llm 步走 aiChatAgent。
func (s *Server) aiChatOnce(ctx context.Context, prompt string) (string, error) {
	msg, _, err := s.aiChatCompletion(ctx, map[string]any{
		"messages": []aiMessage{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

// truncateStr 字符串截断（错误信息拼装用）
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ============================================================
// AI 定时任务 CRUD — /api/jobs
// ============================================================

// aiJobBody 创建/更新请求体。prompt 与 target 至少提供一项：
// 只给 prompt 等价于 target={"type":"ai","prompt":prompt}（老客户端零改动）。
type aiJobBody struct {
	Name       string          `json:"name"`
	CronExpr   string          `json:"cronExpr"`
	Prompt     string          `json:"prompt"`
	Enabled    *bool           `json:"enabled"`
	Target     json.RawMessage `json:"target,omitempty"`
	AllowWrite []string        `json:"allowWrite,omitempty"`
	Context    json.RawMessage `json:"context,omitempty"`
	Notify     json.RawMessage `json:"notify,omitempty"`
	Retry      int             `json:"retry,omitempty"`
}

// validateAIJobBody 校验：name/cron 必填；prompt 或 target 至少一项；
// target 按类型校验字段完整性，allowWrite 为静态预授权清单。
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
	if len(b.Prompt) > 16*1024 {
		return "prompt too long (max 16KB)"
	}
	if len(b.Target) == 0 {
		if b.Prompt == "" {
			return "prompt or target is required"
		}
	} else {
		if len(b.Target) > 64*1024 {
			return "target too large (max 64KB)"
		}
		var t aiJobTarget
		if err := json.Unmarshal(b.Target, &t); err != nil {
			return "invalid target: " + err.Error()
		}
		if t.Type == "" {
			t.Type = aiJobTypeAI
		}
		if t.Type == aiJobTypeAI && strings.TrimSpace(t.Prompt) == "" {
			t.Prompt = b.Prompt
		}
		if err := t.validate(); err != nil {
			return err.Error()
		}
	}
	if len(b.AllowWrite) > 64 {
		return "allowWrite too many entries (max 64)"
	}
	if b.Retry < 0 || b.Retry > 3 {
		return "retry out of range (0-3)"
	}
	if b.Enabled == nil {
		t := true
		b.Enabled = &t
	}
	return ""
}

// aiJobSpecFromBody 请求体 → db 层规格；target 缺省时按历史 prompt 兼容。
func aiJobSpecFromBody(b *aiJobBody) db.AIJobSpec {
	spec := db.AIJobSpec{
		Name: b.Name, CronExpr: b.CronExpr, Prompt: b.Prompt, Enabled: *b.Enabled,
		AllowWrite: b.AllowWrite, Retry: b.Retry,
	}
	if len(b.Target) == 0 {
		spec.TargetType = aiJobTypeAI
		return spec
	}
	var t aiJobTarget
	_ = json.Unmarshal(b.Target, &t)
	if t.Type == "" {
		t.Type = aiJobTypeAI
	}
	if t.Type == aiJobTypeAI && strings.TrimSpace(t.Prompt) == "" {
		t.Prompt = b.Prompt
	}
	if raw, err := json.Marshal(t); err == nil {
		spec.TargetJSON = string(raw)
	}
	spec.TargetType = t.Type
	if len(b.Context) > 0 {
		spec.ContextJSON = string(b.Context)
	}
	if len(b.Notify) > 0 {
		spec.NotifyJSON = string(b.Notify)
	}
	// ai 类型把 prompt 同步到历史列，老前端与只读查询零改动。
	if t.Type == aiJobTypeAI {
		spec.Prompt = t.Prompt
	}
	return spec
}

// aiJobPreview 用请求体构造未入库任务，供创建/更新时做调度预检。
func aiJobPreview(spec db.AIJobSpec, orgID, userID int64) db.AIJobWithOwner {
	return db.AIJobWithOwner{
		AIJob: db.AIJob{
			Name:       spec.Name,
			Prompt:     spec.Prompt,
			TargetType: spec.TargetType,
			Target:     json.RawMessage(spec.TargetJSON),
			AllowWrite: spec.AllowWrite,
			Context:    json.RawMessage(spec.ContextJSON),
			Retry:      spec.Retry,
		},
		OrgID: orgID, UserID: userID,
	}
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
	rctx := ctx.Request().Context()
	id, err := s.saveAIJob(rctx, currentOrgID(ctx), currentUserID(ctx), &req, 0, func(j db.AIJobWithOwner) error {
		return s.validateAIJobTarget(rctx, j)
	})
	if err != nil {
		writeAIJobSaveError(ctx, err)
		return
	}
	slog.Info("ai job created", "user", currentUserID(ctx), "job", req.Name, "cron", req.CronExpr)
	writeOK(ctx, map[string]any{"id": id})
}

func (s *Server) handleAIJobUpdate(ctx iris.Context) {
	id, ok := pathID(ctx)
	if !ok {
		return
	}
	var req aiJobBody
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	rctx := ctx.Request().Context()
	if _, err := s.saveAIJob(rctx, currentOrgID(ctx), currentUserID(ctx), &req, id, func(j db.AIJobWithOwner) error {
		return s.validateAIJobTarget(rctx, j)
	}); err != nil {
		writeAIJobSaveError(ctx, err)
		return
	}
	slog.Info("ai job updated", "user", currentUserID(ctx), "job", req.Name)
	writeOK(ctx, map[string]any{"status": "ok"})
}

func (s *Server) handleAIJobDelete(ctx iris.Context) {
	id, ok := pathID(ctx)
	if !ok {
		return
	}
	if err := s.deleteAIJobTriggers(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id); err != nil {
		serverError(ctx, "delete ai job triggers failed", err)
		return
	}
	if err := s.db.DeleteAIJob(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id); err != nil {
		serverError(ctx, "delete ai job failed", err)
		return
	}
	slog.Info("ai job deleted", "user", currentUserID(ctx), "id", id)
	writeOK(ctx, map[string]any{"status": "ok"})
}

// handleAIJobRun 手动触发一次任务（POST /api/jobs/{id}/run）：异步执行并立即返回，
// 任务详情页通过 job_runs 的 running 记录轮询进度。
func (s *Server) handleAIJobRun(ctx iris.Context) {
	id, ok := pathID(ctx)
	if !ok {
		return
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	go func() {
		if err := s.RunJobNow(context.Background(), orgID, userID, id); err != nil {
			slog.Warn("manual run job failed", "job", id, "err", err)
		}
	}()
	writeOK(ctx, map[string]any{"ok": true, "jobId": id})
}

// handleAIJobRuns 任务执行历史（GET /api/jobs/{id}/runs?limit=20）。
// 归属：全局任务（user_id IS NULL）所有用户可查；私有任务仅归属人。
func (s *Server) handleAIJobRuns(ctx iris.Context) {
	id, ok := pathID(ctx)
	if !ok {
		return
	}
	limit := queryLimit(ctx)
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
	tool := s.aiToolByName(call.Function.Name)
	if tool == nil || !tool.NeedApproval {
		return s.execAITool(ctx, call) // 读类工具或未知工具直接走原路径
	}
	// 客户端已断开：无人能点确认，跳过写工具并把原因回传给模型自行收尾，
	// 后台循环的剩余步骤（含最终回复）继续执行。
	if run, ok := ctx.Values().Get(aiRunStateKey).(*aiRunState); ok && run.disconnected {
		return map[string]any{"error": "用户已断开连接，写操作已跳过"}
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
		// 等待期间断开：作废审批、不执行写工具，循环继续收尾
		s.aiApprovals.remove(call.ID)
		slog.Info("ai approval aborted", "tool", call.Function.Name, "reason", "client disconnected")
		return map[string]any{"error": "用户已断开连接，写操作已跳过"}
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

// forwardAIStream 逐行转发上游 SSE 给前端并同步解析增量：
//   - data: [DONE] 不转发（Agent 中间轮的结束标记不能提前终止前端读取，
//     由调用方在最终轮后统一补发）；其余行原样透传 + flush；
//   - tool_calls 分片按 index 累积（id/name 首个分片携带，arguments 逐片拼接）；
//   - 返回 alive=false 表示客户端已断开。
func forwardAIStream(w aiStreamWriter, reader *bufio.Reader, onDelta func(string)) (aiRoundResult, bool) {
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
			if delta := parseAIDataLine(line, &res, acc); delta != "" && onDelta != nil {
				onDelta(delta)
			}
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

// parseAIDataLine 解析一行 SSE data（data: {...}），累积内容与工具调用分片，
// 返回本行的正文增量（供后台 run 即时落库）。
func parseAIDataLine(line string, res *aiRoundResult, acc map[int]*aiToolCall) string {
	if !strings.HasPrefix(line, "data:") {
		return ""
	}
	payload := strings.TrimSpace(line[len("data:"):])
	if payload == "" || payload == "[DONE]" {
		return ""
	}
	var chunk aiStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return "" // 错误分片/未知格式：透传已做，解析忽略
	}
	var text strings.Builder
	if chunk.Usage != nil {
		res.Usage = chunk.Usage
	}
	for _, ch := range chunk.Choices {
		if ch.FinishReason != "" {
			res.FinishReason = ch.FinishReason
		}
		res.Content += ch.Delta.Content
		text.WriteString(ch.Delta.Content)
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
	return text.String()
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
