// AI 对话相关 API
// chat 走 SSE 流式（后端 /api/ai/chat 代理 OpenAI 兼容端点，key 不出后端）
import { apiCall, authFetch, clearAuthState } from '../lib/common'

// 对话消息（OpenAI chat 格式）
export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
  /** 引用的文件路径（仅前端 UI 展示用，后端忽略） */
  files?: string[]
  /** 推理模型思考过程（仅前端 UI 展示用，后端忽略） */
  reasoning?: string
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
 * 上游返回 {code,msg} 错误信封（如 50301 未配置）时抛 Error(msg)。
 * signal 用于中断生成（组件的"停止"按钮）。
 * qaId：一次提问的 ID（多轮模型调用复用）；sessionId：会话 ID（多次提问复用）。
 * 两者由后端透传为 X-QA-Id / X-Session-Id，用于上游链路排障与会话亲和。
 */
export async function chatStream(
  messages: ChatMessage[],
  opts: {
    onDelta: (text: string) => void
    onReasoning?: (text: string) => void
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

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })

    // SSE 按行解析：只关心 data: {...} 行，[DONE] 结束
    let nl: number
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl).replace(/\r$/, '')
      buf = buf.slice(nl + 1)
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      if (payload === '[DONE]') return
      try {
        const obj = JSON.parse(payload)
        const delta = obj?.choices?.[0]?.delta
        if (delta?.reasoning_content) opts.onReasoning?.(delta.reasoning_content)
        if (delta?.content) opts.onDelta(delta.content)
      } catch { /* 半截 JSON（跨 chunk 截断），丢弃等下一轮 */ }
    }
  }
}
