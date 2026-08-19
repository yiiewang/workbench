// 任务相关 API
import { apiCall, authFetch } from '../lib/common'

// 任务对象（和后端 db.TaskItem 对应）
export interface Task {
  id: string
  title: string
  content: string
  status: string
  priority: string
  scheduled: string
  due: string
  progress: number
  assignee: string
  postponedCount: number
  autoPostponed: boolean
  sortOrder: number
}

// 全量同步：获取所有任务（GET /api/tasks）
export async function getTasks() {
  return apiCall('/api/tasks')
}

// 全量同步：替换所有任务（PUT /api/tasks）
// body: { orgs: { cm: { userId: { tasks: [...], version: {...} } } } }
export async function putTasks(body: any) {
  return apiCall('/api/tasks', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// 增量更新单条任务（PATCH /api/tasks/{id}）
export async function updateTask(task: Task) {
  return authFetch(`/api/tasks/${encodeURIComponent(task.id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task }),
  }).then(r => {
    if (!r.ok) throw Object.assign(new Error(`HTTP ${r.status}`), { status: r.status })
    return r.json()
  })
}

// 增量新增单条任务（POST /api/tasks/{id}）
export async function addTask(task: Task) {
  return authFetch(`/api/tasks/${encodeURIComponent(task.id)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task }),
  }).then(r => {
    if (!r.ok) throw Object.assign(new Error(`HTTP ${r.status}`), { status: r.status })
    return r.json()
  })
}

// 增量删除单条任务（DELETE /api/tasks/{id}）
export async function deleteTask(taskId: string) {
  return authFetch(`/api/tasks/${encodeURIComponent(taskId)}`, {
    method: 'DELETE',
  }).then(r => {
    if (!r.ok) throw Object.assign(new Error(`HTTP ${r.status}`), { status: r.status })
    return r.json()
  })
}

// 组织成员（对应后端 db.Member：整数 id + 业务 name）
export interface Member {
  id: number
  name: string
}

// 获取当前组织的成员列表（GET /api/org-members）
// 后端强制使用登录用户的 orgId，返回 { members: [{ id, name }] }
export async function getOrgMembers(): Promise<{ members: Member[] }> {
  return apiCall('/api/org-members')
}
