// 通用定时触发规则 API（/api/schedule/triggers）
// 规则属于调度领域：指向「某类目标对象 + cron」，执行定义由目标对象自己持有。
import { apiCall } from '../lib/common'

export interface CronTrigger {
  id: number
  targetType: string
  targetId: number
  cronExpr: string
  enabled: boolean
  lastRunAt?: string
  lastStatus?: string
  createdAt?: string
  updatedAt?: string
}

export interface CronTriggerCreateBody {
  targetType: string
  targetId: number
  cronExpr: string
  enabled?: boolean
}

export interface CronTriggerUpdateBody {
  cronExpr: string
  enabled: boolean
}

export async function listScheduleTriggers(params: { targetType?: string; targetId?: number } = {}): Promise<{ triggers: CronTrigger[] }> {
  const q = new URLSearchParams()
  if (params.targetType) q.set('targetType', params.targetType)
  if (params.targetId) q.set('targetId', String(params.targetId))
  const suffix = q.toString()
  return apiCall(`/api/schedule/triggers${suffix ? '?' + suffix : ''}`)
}

export async function createScheduleTrigger(body: CronTriggerCreateBody): Promise<{ id: number }> {
  return apiCall('/api/schedule/triggers', {
    method: 'POST',
    json: body,
  })
}

export async function updateScheduleTrigger(id: number, body: CronTriggerUpdateBody): Promise<void> {
  return apiCall(`/api/schedule/triggers/${id}`, {
    method: 'PUT',
    json: body,
  })
}

export async function deleteScheduleTrigger(id: number): Promise<void> {
  return apiCall(`/api/schedule/triggers/${id}`, { method: 'DELETE' })
}
