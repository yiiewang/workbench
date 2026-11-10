// Todo 纯工具函数（无组件/状态依赖），从 useTodoApp 抽出并类型化。
import type { Task } from '../api/tasks'

/** 新任务 id：UUID 替代 maxId+1（多设备并发新建不重复） */
export function newTaskId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10)
}

/** 归一化 assignee：历史数据可能残留 number，统一转字符串化 id */
export function normalizeAssignees(list: any[]): any[] {
  return (list || []).map(t => (t.assignee == null ? t : { ...t, assignee: String(t.assignee) }))
}

// ========== 日历 / 日期 ==========

/** 周一为一周起点 */
export function getWeekStart(date: Date): Date {
  const d = new Date(date)
  const day = d.getDay()
  const diff = day === 0 ? -6 : 1 - day
  d.setDate(d.getDate() + diff)
  d.setHours(0, 0, 0, 0)
  return d
}

export function getWeekNumber(date: Date): number {
  const start = new Date(date.getFullYear(), 0, 1)
  const ws = getWeekStart(date)
  return Math.ceil(((ws.getTime() - start.getTime()) / 86400000 + start.getDay() + 1) / 7)
}

/** Date → YYYY-MM-DD（本地时区） */
export function formatDate(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/** 日期字符串 → "M/D" 短格式 */
export function formatDateShort(d: string): string {
  if (!d) return ''
  const dt = new Date(d + 'T00:00:00')
  return (dt.getMonth() + 1) + '/' + dt.getDate()
}

/** startStr 到 dateStr 的天数偏移 */
export function dayOffset(startStr: string, dateStr: string): number {
  const start = new Date(startStr + 'T00:00:00')
  const d = new Date(dateStr + 'T00:00:00')
  return Math.round((d.getTime() - start.getTime()) / 86400000)
}

// ========== 任务判定 ==========

/** 是否逾期：有截止、未完成、进度未满且截止日早于今天 */
export function isOverdue(task: Partial<Task>): boolean {
  if (!task.due) return false
  if (task.status === 'done') return false
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return new Date(task.due + 'T00:00:00') < today && (task.progress ?? 0) < 100
}

/** 逾期天数（向上取整），未逾期返回 0 */
export function overdueDays(task: Partial<Task>): number {
  if (!isOverdue(task)) return 0
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const due = new Date(task.due! + 'T00:00:00')
  return Math.ceil((today.getTime() - due.getTime()) / 86400000)
}

// ========== 展示标签 ==========

const priorityLabelMap: Record<string, string> = { high: '🔴 高', medium: '🟡 中', low: '🟢 低' }
const statusIconMap: Record<string, string> = { todo: '🔴', progress: '🟡', done: '✅' }
const statusLabelMap: Record<string, string> = { todo: '待办', progress: '进行中', done: '已完成' }

export function priorityLabel(p: string): string {
  return priorityLabelMap[p] || '中'
}

export function statusIcon(s: string): string {
  return statusIconMap[s] || '❓'
}

export function getStatusLabel(s: string): string {
  return statusLabelMap[s] || s
}
