// 编排 API（/api/pipeline/*）：Definition 直调与运行状态查询。
// 编排流程内联在定时任务里，不单独固化模板。
import { apiCall } from '../lib/common'
import { openSSE } from '../lib/sse'

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
    json: { definition },
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
  await openSSE(`/api/pipeline/runs/${id}/stream`, { signal: opts.signal }, {
    events: {
      progress: (run) => {
        opts.onProgress(run as PipelineRun)
      },
      end: () => true, // 终态：服务端随后关闭连接
      error: (obj) => {
        throw new Error(obj?.msg || '订阅编排运行失败')
      },
    },
  })
}
