// SSE（Server-Sent Events）流式响应解析：fetch ReadableStream → 逐事件回调。
// 后端所有流式端点（/api/ai/chat、/api/pipeline/runs/{id}/stream）共用同一套帧格式：
//   event: <name>\n data: <json>\n\n
// 空行与 ": keep-alive" 心跳注释行忽略；跨 chunk 截断的半截 JSON 丢弃。
import { authFetch, handleUnauthorized } from './common'

/** 单个事件处理器：data 为解析后的 JSON；返回 true 结束解析 */
export type SSEHandler = (data: any) => boolean | void

export interface SSESubscription {
  /** 具名事件（event: 行）→ 处理器 */
  events: Record<string, SSEHandler>
  /** 未具名事件（OpenAI 兼容增量）处理器 */
  onMessage?: SSEHandler
  /** 收到 data: [DONE] 时回调，随后结束解析 */
  onDone?: () => void
}

/**
 * 发起鉴权请求并解析 SSE 流。
 * 统一处理 401（清登录态并跳登录页）与开流前的错误信封（{code,msg} → Error(msg)）。
 */
export async function openSSE(url: string, init: RequestInit, sub: SSESubscription): Promise<void> {
  const resp = await authFetch(url, init)
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
  await readSSE(resp, sub)
}

/** 逐行解析 SSE 帧并按事件名分发（连接关闭即结束） */
export async function readSSE(resp: Response, sub: SSESubscription): Promise<void> {
  if (!resp.body) return
  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  // SSE 事件名：event: 行设置，作用于紧随的 data: 行，消费后复位
  let curEvent = ''

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break // 连接关闭（终态事件后服务端主动断开；或静默断流）
    buf += decoder.decode(value, { stream: true })

    let nl: number
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl).replace(/\r$/, '')
      buf = buf.slice(nl + 1)
      if (!line || line.startsWith(':')) continue
      if (line.startsWith('event:')) {
        curEvent = line.slice(6).trim()
        continue
      }
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      const ev = curEvent
      curEvent = ''
      if (payload === '[DONE]') {
        sub.onDone?.()
        return
      }
      let data: any
      try {
        data = JSON.parse(payload)
      } catch {
        continue // 半截 JSON（跨 chunk 截断），丢弃
      }
      const handler = ev ? sub.events[ev] : sub.onMessage
      if (handler && handler(data) === true) return
    }
  }
}
