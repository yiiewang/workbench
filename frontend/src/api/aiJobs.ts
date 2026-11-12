// AI 定时任务 API（/api/ai/jobs）：用户自建"到点让 AI 执行 prompt"
import { apiCall } from '../lib/common'

export interface AIJob {
  id: number
  name: string
  cronExpr: string
  prompt: string
  enabled: boolean
  /** 全局公共任务（只读，所有有邮箱的用户各自执行投递） */
  isGlobal?: boolean
  lastRunAt?: string
  lastStatus?: string
  createdAt?: string
}

export interface AIJobBody {
  name: string
  cronExpr: string
  prompt: string
  enabled: boolean
}

export async function listAIJobs(): Promise<{ jobs: AIJob[] }> {
  return apiCall('/api/ai/jobs')
}

export async function createAIJob(body: AIJobBody): Promise<{ id: number }> {
  return apiCall('/api/ai/jobs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export async function updateAIJob(id: number, body: AIJobBody): Promise<void> {
  return apiCall(`/api/ai/jobs/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export async function deleteAIJob(id: number): Promise<void> {
  return apiCall(`/api/ai/jobs/${id}`, { method: 'DELETE' })
}

// 执行历史
export interface AIJobRun {
  id: number
  jobId: number
  ranAt: string
  status: string
  output: string
}

export async function listAIJobRuns(id: number, limit = 20): Promise<{ runs: AIJobRun[] }> {
  return apiCall(`/api/ai/jobs/${id}/runs?limit=${limit}`)
}
