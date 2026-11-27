// 编排 API（/api/pipeline/*）：Definition 直调与运行状态查询。
// 编排流程内联在定时任务里，不单独固化模板。
import { apiCall, authFetch } from '../lib/common'
import { handleUnauthorized } from './ai'

/** 流程图节点与 Inspector 共用的步骤结构（needs 已归一化） */
export interface PipelineGraphStep {
  id: string
  uses: string
  needs?: string[]
  when?: string
  retry?: number
  with?: unknown
}

export interface PipelineStepResult {
  id: string
  uses: string
  status: 'ok' | 'failed' | 'skipped'
  output?: unknown
  error?: string
  /** llm 步的思考过程（非流式 Agent 一轮或多轮拼接） */
  reasoning?: string
  durationMs?: number
}

export interface PipelineRun {
  id: string
  name: string
  /** 后端以 json.RawMessage 返回对象（不是字符串） */
  definition?: unknown
  status: 'running' | 'done' | 'failed'
  steps?: PipelineStepResult[]
  error?: string
  startedAt: string
  updatedAt: string
}

/** 直接提交一份 Definition 执行（不落模板） */
export async function runPipeline(definition: unknown): Promise<{ runId: string; started: boolean }> {
  return apiCall('/api/pipeline/run', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ definition }),
  })
}

export async function listPipelineRuns(limit = 20): Promise<{ runs: PipelineRun[] }> {
  return apiCall(`/api/pipeline/runs?limit=${limit}`)
}

export async function getPipelineRun(id: string): Promise<PipelineRun> {
  return apiCall(`/api/pipeline/runs/${id}`)
}

/**
 * 实时订阅一次编排运行（SSE）：GET /api/pipeline/runs/{id}/stream。
 * 事件：progress → onProgress（整份运行快照，steps 随 DAG 层推进增长）；
 * end → 正常结束（resolve）；error → 抛 Error。服务端还会发 ": keep-alive"
 * 注释行（心跳），解析时忽略。断流（非 end 结束）以 resolve 收场，由调用方
 * 决定是否降级拉取最终详情。
 */
export async function streamPipelineRun(
  id: string,
  opts: {
    onProgress: (run: PipelineRun) => void
    signal?: AbortSignal
  },
): Promise<void> {
  const resp = await authFetch(`/api/pipeline/runs/${id}/stream`, { signal: opts.signal })
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
    if (done) break // 连接关闭（end 后服务端主动断开；或静默断流）
    buf += decoder.decode(value, { stream: true })

    let nl: number
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl).replace(/\r$/, '')
      buf = buf.slice(nl + 1)
      // 空行与心跳注释行（": keep-alive"）跳过
      if (!line || line.startsWith(':')) continue
      if (line.startsWith('event:')) {
        curEvent = line.slice(6).trim()
        continue
      }
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      const ev = curEvent
      curEvent = ''
      let obj: any
      try {
        obj = JSON.parse(payload)
      } catch {
        continue // 半截 JSON（跨 chunk 截断），丢弃
      }
      if (ev === 'progress') {
        opts.onProgress(obj as PipelineRun)
        continue
      }
      if (ev === 'end') return // 终态：服务端随后关闭连接
      if (ev === 'error') throw new Error(obj?.msg || '订阅编排运行失败')
    }
  }
}
