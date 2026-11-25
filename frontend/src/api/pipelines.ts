// 编排 API（/api/pipeline/*）：Definition 直调、模板固化复用、运行状态查询
import { apiCall } from '../lib/common'

/** 流程图节点与 Inspector 共用的步骤结构（needs 已归一化） */
export interface PipelineGraphStep {
  id: string
  uses: string
  needs?: string[]
  when?: string
  retry?: number
  with?: unknown
}

/** 模板只存纯 Definition；cron/启用/最近执行状态属于调度领域的 CronTrigger */
export interface PipelineTemplate {
  id: number
  name: string
  /** 仅详情接口返回（列表为减小载荷不含） */
  definition?: string
  createdAt?: string
  updatedAt?: string
}

export interface PipelineTemplateBody {
  name: string
  definition: string
}

export interface PipelineStepResult {
  id: string
  uses: string
  status: 'ok' | 'failed' | 'skipped'
  output?: unknown
  error?: string
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

export async function listPipelineTemplates(): Promise<{ templates: PipelineTemplate[] }> {
  return apiCall('/api/pipeline/templates')
}

export async function getPipelineTemplate(id: number): Promise<PipelineTemplate> {
  return apiCall(`/api/pipeline/templates/${id}`)
}

export async function createPipelineTemplate(body: PipelineTemplateBody): Promise<{ id: number }> {
  return apiCall('/api/pipeline/templates', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export async function updatePipelineTemplate(id: number, body: PipelineTemplateBody): Promise<void> {
  return apiCall(`/api/pipeline/templates/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export async function deletePipelineTemplate(id: number): Promise<void> {
  return apiCall(`/api/pipeline/templates/${id}`, { method: 'DELETE' })
}

/** 手动触发已固化的模板（后台异步，立即返回 runId） */
export async function runPipelineTemplate(id: number): Promise<{ runId: string; started: boolean }> {
  return apiCall(`/api/pipeline/templates/${id}/run`, { method: 'POST' })
}

/** 模板版本历史（每次保存/回滚产生新版本） */
export interface PipelineTemplateVersion {
  version: number
  name: string
  createdAt?: string
}

export async function listPipelineTemplateVersions(id: number): Promise<{ versions: PipelineTemplateVersion[] }> {
  return apiCall(`/api/pipeline/templates/${id}/versions`)
}

export async function restorePipelineTemplateVersion(id: number, version: number): Promise<{ version: number }> {
  return apiCall(`/api/pipeline/templates/${id}/versions/${version}/restore`, { method: 'POST' })
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
