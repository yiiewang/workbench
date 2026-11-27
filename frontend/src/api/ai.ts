// AI 对话相关 API
// chat 走 SSE 流式（后端 /api/ai/chat 代理 OpenAI 兼容端点，key 不出后端）
import { apiCall, authFetch } from '../lib/common'
import { openSSE } from '../lib/sse'

// 工具调用（Agent 模式，后端 aitools.go 执行）：
// 展示态字段 —— result 到达前表示"执行中"。
// 写类工具（write_file / update_task）带审批状态（Human-in-the-loop）：
// pending 等待用户确认 → approved/declined（用户决定）→ timeout（60s 未确认）
export interface ToolCallView {
  id: string
  name: string
  /** 模型给出的参数（JSON 字符串，展示态保留原文） */
  arguments: string
  /** 工具执行结果（undefined = 尚未返回） */
  result?: unknown
  /** 写类工具的审批状态（读类工具无此字段） */
  approval?: 'pending' | 'approved' | 'declined' | 'timeout'
}

// RAG 语义检索命中（rag_hits SSE 事件推给前端，挂在本条回复上）
export interface RagHit {
  /** 用户根目录内相对路径（/ 开头） */
  path: string
  /** 命中的原文片段（引用卡展开可见） */
  content: string
  /** 片段在原文中的起始行号（1-based；0 = 旧索引未知） */
  startLine: number
  distance: number
}

// 回复过程步骤（时序记录）：按发生顺序排列思考/工具调用/正文片段，
// 渲染为"思考 → 调工具 → 分析结果 → 继续调工具 → 回答"的时间线，可折叠
export interface MsgStep {
  kind: 'reasoning' | 'tool' | 'text'
  /** reasoning/text 的文本增量累积 */
  text?: string
  /** tool 步骤的工具调用（与 toolCalls 中同一对象引用，result/approval 实时同步） */
  call?: ToolCallView
}

// token 消耗（usage SSE 事件，Agent 多轮已由后端累加）
export interface TokenUsage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
}

// 对话消息（OpenAI chat 格式 + 前端展示扩展）
export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
  /** 会话行级存储的稳定主键；发给上游 LLM 时会剥掉，仅用于持久化增量提交 */
  clientMsgId?: string
  /** 引用的文件路径（仅前端 UI 展示用，后端忽略） */
  files?: string[]
  /** 推理模型思考过程（仅前端 UI 展示用，后端忽略） */
  reasoning?: string
  /** 本条回复触发的工具调用（Agent 模式，仅前端 UI 展示用） */
  toolCalls?: ToolCallView[]
  /** 本条回答参考的 RAG 检索命中（编号引用卡片，仅前端 UI 展示用） */
  ragHits?: RagHit[]
  /** 回复过程时序步骤（steps 缺失时前端从扁平字段归一化重建，兼容旧会话快照） */
  steps?: MsgStep[]
  /** 本条回复的 token 消耗（仅前端 UI 展示用；上游不支持 include_usage 时缺省） */
  usage?: TokenUsage
}

// 发送给后端的上游消息（OpenAI chat 完整格式，含 Agent 工具消息）。
// ChatMessage（展示态）结构兼容此类型，可直接传入。
export interface AiPayloadMessage {
  role: 'system' | 'user' | 'assistant' | 'tool'
  content: string
  /** assistant 消息发起的工具调用（Agent 历史回传） */
  tool_calls?: { id: string; type: 'function'; function: { name: string; arguments: string } }[]
  /** tool 消息对应的调用 ID */
  tool_call_id?: string
}

// AI 服务状态（GET /api/ai/status）
export interface AIGateway {
  id: string
  name: string
  model: string
}

export interface AIStatus {
  enabled: boolean
  model: string
  /** 默认网关 id（对应 config 的 ai.chat.default） */
  default?: string
  /** 可用模型网关（不含 api_key） */
  gateways?: AIGateway[]
}

// 会话摘要（列表项，不含 messages）
export interface AISessionSummary {
  id: number
  title: string
  createdAt: string
  updatedAt: string
}

// 会话详情（含消息快照）
export interface AISessionDetail extends AISessionSummary {
  messages: ChatMessage[]
}

// 后台对话运行：客户端断开后服务端继续跑完，状态/事件/结果落库供前端恢复
export interface AIRunEvent {
  event: string
  data: any
}

export interface AIRun {
  id: string
  qaId: string
  sessionId: string
  status: 'running' | 'done' | 'failed'
  /** 结构化 SSE 事件（列表接口不返回） */
  events?: AIRunEvent[]
  /** 累积的助手回复文本 */
  result: string
  error: string
  startedAt: string
  updatedAt: string
  /** offset 游标之后的正文增量（详情接口按 offset 查询时返回） */
  delta?: string
  /** 下次请求应带的游标（rune 数，由服务端返回，前端无需换算） */
  offset?: number
}

// 单条运行详情 — GET /api/ai/runs/{id}[?offset=<rune 数>]
// 传 offset：服务端只回增量 delta（result 为空）；不传：返回全量快照
export async function getAIRun(id: string, offset?: number): Promise<AIRun> {
  const q = offset === undefined ? '' : `?offset=${offset}`
  return apiCall(`/api/ai/runs/${id}${q}`)
}

// 最近运行列表（不含 events）— GET /api/ai/runs
export async function listAIRuns(limit = 20): Promise<{ runs: AIRun[] }> {
  return apiCall(`/api/ai/runs?limit=${limit}`)
}

// 最近会话列表 — GET /api/ai/sessions
export async function listAISessions(): Promise<{ sessions: AISessionSummary[] }> {
  return apiCall('/api/ai/sessions')
}

// 创建会话并写入首批消息 — POST /api/ai/sessions
export async function createAISession(body: { title: string; messages: ChatMessage[] }): Promise<{ id: number }> {
  return apiCall('/api/ai/sessions', {
    method: 'POST',
    json: body,
  })
}

// 会话详情 — GET /api/ai/sessions/{id}
export async function getAISession(id: number): Promise<AISessionDetail> {
  return apiCall(`/api/ai/sessions/${id}`)
}

// 追加/更新一批消息 — PUT /api/ai/sessions/{id}/messages
// truncateAfter：保留该锚点消息、删除其后的旧回复（regenerate 重新生成末尾回复）
export async function appendAISessionMessages(id: number, body: { messages: ChatMessage[]; truncateAfter?: string }): Promise<void> {
  return apiCall(`/api/ai/sessions/${id}/messages`, {
    method: 'PUT',
    json: body,
  })
}

// 删除会话 — DELETE /api/ai/sessions/{id}
export async function deleteAISession(id: number): Promise<void> {
  return apiCall(`/api/ai/sessions/${id}`, { method: 'DELETE' })
}

// 审批写类工具调用 — POST /api/ai/approval（Agent 模式 Human-in-the-loop）：
// id 为 approval_request 事件携带的 tool_call ID
export async function approveAITool(id: string, approved: boolean): Promise<void> {
  return apiCall('/api/ai/approval', {
    method: 'POST',
    json: { id, approved },
  })
}

// 查询 AI 是否可用及当前模型（决定前端是否显示 AI 入口）
export async function getAIStatus(): Promise<AIStatus> {
  const resp = await authFetch('/api/ai/status')
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
  const body = await resp.json()
  return body.data ?? body
}

/**
 * 流式对话：POST /api/ai/chat，逐段解析 SSE，把增量文本回调给 onDelta。
 * 推理模型（如 hy4）思考阶段走 delta.reasoning_content，经 onReasoning 回调。
 * Agent 模式（ai.tools=true）下后端会额外推送自定义 SSE 事件：
 *   event: tool_call   → onToolCall（模型发起一次工具调用）
 *   event: tool_result → onToolResult（后端执行完成，携带结果）
 *   event: error       → 抛 Error（流中途失败，HTTP 状态码已不可改）
 * 上游返回 {code,msg} 错误信封（如 50301 未配置）时抛 Error(msg)。
 * signal 用于中断生成（组件的"停止"按钮）。
 * qaId：一次提问的 ID（多轮模型调用复用）；sessionId：会话 ID（多次提问复用）。
 * 两者由后端透传为 X-QA-Id / X-Session-Id，用于上游链路排障与会话亲和。
 */
export async function chatStream(
  messages: AiPayloadMessage[],
  opts: {
    onDelta: (text: string) => void
    onReasoning?: (text: string) => void
    onRagHits?: (hits: RagHit[]) => void
    onUsage?: (u: TokenUsage) => void
    onToolCall?: (tc: { id: string; name: string; arguments: string }) => void
    onToolResult?: (tr: { id: string; name: string; result: unknown }) => void
    onApprovalRequest?: (ar: { id: string; name: string; arguments: string }) => void
    onApprovalResolved?: (ar: { id: string; outcome: 'approved' | 'declined' | 'timeout' }) => void
    /** 后台 runId（SSE 开流时一次性推送，断连后据此轮询恢复） */
    onRunId?: (id: string) => void
    /** 收到 data: [DONE]：流完整结束（静默断流不会触发） */
    onDone?: () => void
    signal?: AbortSignal
    qaId?: string
    sessionId?: string
    /** 输出上限（0/undefined = 网关默认） */
    maxTokens?: number
    /** 推理强度 low/medium/high/max；空 = 不传 */
    reasoningEffort?: string
    /** 模型网关 id；空 = 默认网关 */
    gateway?: string
  },
): Promise<void> {
  await openSSE(
    '/api/ai/chat',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        messages,
        qaId: opts.qaId || undefined,
        sessionId: opts.sessionId || undefined,
        maxTokens: opts.maxTokens || undefined,
        reasoningEffort: opts.reasoningEffort || undefined,
        gateway: opts.gateway || undefined,
      }),
      signal: opts.signal,
    },
    {
      events: {
        run_id: (obj) => {
          const id = String(obj?.id || '')
          if (id) opts.onRunId?.(id)
        },
        rag_hits: (obj) => {
          // RAG 前置检索命中（编号引用卡片数据，流开始时一次性推送）
          const hits: RagHit[] = Array.isArray(obj?.hits)
            ? obj.hits.map((h: any) => ({
                path: String(h?.path || ''),
                content: String(h?.content || ''),
                startLine: Number(h?.startLine) || 0,
                distance: Number(h?.distance) || 0,
              }))
            : []
          if (hits.length) opts.onRagHits?.(hits)
        },
        usage: (obj) => {
          // token 消耗（Agent 多轮已聚合，[DONE] 前一次性推送）
          if (obj && typeof obj.total_tokens === 'number') {
            opts.onUsage?.({
              promptTokens: Number(obj.prompt_tokens) || 0,
              completionTokens: Number(obj.completion_tokens) || 0,
              totalTokens: Number(obj.total_tokens) || 0,
            })
          }
        },
        tool_call: (obj) => {
          opts.onToolCall?.({
            id: obj?.id || '',
            name: obj?.function?.name || '',
            arguments: obj?.function?.arguments || '',
          })
        },
        tool_result: (obj) => {
          opts.onToolResult?.({ id: obj?.id || '', name: obj?.name || '', result: obj?.result })
        },
        approval_request: (obj) => {
          opts.onApprovalRequest?.({ id: obj?.id || '', name: obj?.name || '', arguments: obj?.arguments || '' })
        },
        approval_resolved: (obj) => {
          opts.onApprovalResolved?.({ id: obj?.id || '', outcome: obj?.outcome || 'declined' })
        },
        error: (obj) => {
          const err: any = new Error(obj?.msg || 'AI 流式请求失败')
          err.business = true // 服务端明确报错：后台 run 已失败，不走断流恢复
          throw err
        },
      },
      // 未具名事件：上游 OpenAI 兼容增量
      onMessage: (obj) => {
        const delta = obj?.choices?.[0]?.delta
        if (delta?.reasoning_content) opts.onReasoning?.(delta.reasoning_content)
        if (delta?.content) opts.onDelta(delta.content)
      },
      onDone: opts.onDone,
    },
  )
}

// 文件索引状态（汇总 + 明细）— GET /api/ai/index-status
export interface FileIndexItem {
  path: string
  status: string
  chunkCount: number
  error?: string
  indexedAt: string
}
export interface AIIndexStatus {
  summary: { files: number; chunks: number; errors: number }
  items: FileIndexItem[]
}
export async function getAIIndexStatus(): Promise<AIIndexStatus> {
  return apiCall('/api/ai/index-status')
}
