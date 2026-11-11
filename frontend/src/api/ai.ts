// AI 对话相关 API
// chat 走 SSE 流式（后端 /api/ai/chat 代理 OpenAI 兼容端点，key 不出后端）
import { apiCall, authFetch, clearAuthState } from '../lib/common'

// 工具调用（Agent 模式，后端 aitools.go 执行）：
// 展示态字段 —— result 到达前表示"执行中"
export interface ToolCallView {
  id: string
  name: string
  /** 模型给出的参数（JSON 字符串，展示态保留原文） */
  arguments: string
  /** 工具执行结果（undefined = 尚未返回） */
  result?: unknown
}

// 对话消息（OpenAI chat 格式 + 前端展示扩展）
export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
  /** 引用的文件路径（仅前端 UI 展示用，后端忽略） */
  files?: string[]
  /** 推理模型思考过程（仅前端 UI 展示用，后端忽略） */
  reasoning?: string
  /** 本条回复触发的工具调用（Agent 模式，仅前端 UI 展示用） */
  toolCalls?: ToolCallView[]
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
export interface AIStatus {
  enabled: boolean
  model: string
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

// 最近会话列表 — GET /api/ai/sessions
export async function listAISessions(): Promise<{ sessions: AISessionSummary[] }> {
  return apiCall('/api/ai/sessions')
}

// 创建会话 — POST /api/ai/sessions（title 默认取首条用户消息截断）
export async function createAISession(body: { title: string; messages: ChatMessage[] }): Promise<{ id: number }> {
  return apiCall('/api/ai/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// 会话详情 — GET /api/ai/sessions/{id}
export async function getAISession(id: number): Promise<AISessionDetail> {
  return apiCall(`/api/ai/sessions/${id}`)
}

// 更新会话（保存消息快照；title 传空保留原标题）— PUT /api/ai/sessions/{id}
export async function updateAISession(id: number, body: { title?: string; messages: ChatMessage[] }): Promise<void> {
  return apiCall(`/api/ai/sessions/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// 删除会话 — DELETE /api/ai/sessions/{id}
export async function deleteAISession(id: number): Promise<void> {
  return apiCall(`/api/ai/sessions/${id}`, { method: 'DELETE' })
}

// 查询 AI 是否可用及当前模型（决定前端是否显示 AI 入口）
export async function getAIStatus(): Promise<AIStatus> {
  const resp = await authFetch('/api/ai/status')
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
  const body = await resp.json()
  return body.data ?? body
}

// 401 全局处理：token 过期清登录态并跳登录页（与 apiCall 行为一致）
function handleUnauthorized() {
  clearAuthState()
  const current = window.location.pathname + window.location.search
  if (!current.startsWith('/login')) {
    window.location.href = '/login?redirect=' + encodeURIComponent(current)
  }
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
    onToolCall?: (tc: { id: string; name: string; arguments: string }) => void
    onToolResult?: (tr: { id: string; name: string; result: unknown }) => void
    signal?: AbortSignal
    qaId?: string
    sessionId?: string
  },
): Promise<void> {
  const resp = await authFetch('/api/ai/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ messages, qaId: opts.qaId || undefined, sessionId: opts.sessionId || undefined }),
    signal: opts.signal,
  })

  if (resp.status === 401) {
    handleUnauthorized()
    throw new Error('Unauthorized')
  }
  if (!resp.ok || !resp.body) {
    // 非流式错误：后端返回标准 {code,msg} 信封
    let msg = `HTTP ${resp.status}`
    try {
      const body = await resp.json()
      if (body && body.msg) msg = body.msg
    } catch { /* 非 JSON 响应，用默认 msg */ }
    throw new Error(msg)
  }

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  // SSE 事件名：event: 行设置，作用于紧随的 data: 行，消费后复位
  let curEvent = ''

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })

    // SSE 按行解析：event: 行记录事件名；data: 行按事件名分发
    let nl: number
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl).replace(/\r$/, '')
      buf = buf.slice(nl + 1)
      if (!line) continue
      if (line.startsWith('event:')) {
        curEvent = line.slice(6).trim()
        continue
      }
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      if (payload === '[DONE]') return
      const ev = curEvent
      curEvent = '' // data 行消费完毕，事件名复位
      try {
        const obj = JSON.parse(payload)
        if (ev === 'tool_call') {
          opts.onToolCall?.({
            id: obj?.id || '',
            name: obj?.function?.name || '',
            arguments: obj?.function?.arguments || '',
          })
          continue
        }
        if (ev === 'tool_result') {
          opts.onToolResult?.({ id: obj?.id || '', name: obj?.name || '', result: obj?.result })
          continue
        }
        if (ev === 'error') {
          throw new Error(obj?.msg || 'AI 流式请求失败')
        }
        // 默认事件：上游 OpenAI 兼容增量
        const delta = obj?.choices?.[0]?.delta
        if (delta?.reasoning_content) opts.onReasoning?.(delta.reasoning_content)
        if (delta?.content) opts.onDelta(delta.content)
      } catch (e) {
        // 半截 JSON（跨 chunk 截断）重新抛出前先识别是否为业务错误
        if (e instanceof Error && !(e instanceof SyntaxError)) throw e
      }
    }
  }
}
