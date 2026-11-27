// 定时任务 API（/api/jobs）：任务定义 + 触发规则（cron/enabled 由后端映射）。
import { apiCall } from '../lib/common'

export type AIJobTargetType = 'ai' | 'tool' | 'interface' | 'pipeline'

/** 执行对象：ai 用 prompt，tool/interface/pipeline 用各自字段 */
export interface AIJobTarget {
  type: AIJobTargetType
  prompt?: string
  tool?: string
  params?: Record<string, unknown>
  method?: string
  path?: string
  query?: Record<string, unknown>
  body?: unknown
  definition?: Record<string, unknown>
}

export interface AIJob {
  id: number
  name: string
  cronExpr: string
  prompt: string
  enabled: boolean
  /** 全局公共任务（只读，所有有邮箱的用户各自执行投递） */
  isGlobal?: boolean
  targetType?: AIJobTargetType
  target?: AIJobTarget
  allowWrite?: string[]
  retry?: number
  lastRunAt?: string
  lastStatus?: string
  createdAt?: string
}

export interface AIJobBody {
  name: string
  cronExpr: string
  prompt?: string
  enabled: boolean
  target?: AIJobTarget
  allowWrite?: string[]
  context?: Record<string, unknown>
  notify?: Record<string, unknown>
  retry?: number
}

export async function listAIJobs(): Promise<{ jobs: AIJob[] }> {
  return apiCall('/api/jobs')
}

export async function createAIJob(body: AIJobBody): Promise<{ id: number }> {
  return apiCall('/api/jobs', {
    method: 'POST',
    json: body,
  })
}

export async function updateAIJob(id: number, body: AIJobBody): Promise<void> {
  return apiCall(`/api/jobs/${id}`, {
    method: 'PUT',
    json: body,
  })
}

export async function deleteAIJob(id: number): Promise<void> {
  return apiCall(`/api/jobs/${id}`, { method: 'DELETE' })
}

// 手动触发一次任务：后端异步执行，结果写 job_runs
export async function runAIJob(id: number): Promise<void> {
  return apiCall(`/api/jobs/${id}/run`, { method: 'POST' })
}

// 执行历史
export interface AIJobRun {
  id: number
  jobId: number
  ranAt: string
  status: string
  output: string
  targetType?: AIJobTargetType
  pipelineRunId?: string
  stepsSummary?: string
  errMsg?: string
  durationMs?: number
  finishedAt?: string
}

export async function listAIJobRuns(id: number, limit = 20): Promise<{ runs: AIJobRun[] }> {
  return apiCall(`/api/jobs/${id}/runs?limit=${limit}`)
}
