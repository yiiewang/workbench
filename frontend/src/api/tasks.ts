// 任务相关 API
import { apiCall, authFetch } from '../lib/common'

// 任务对象（和后端 db.TaskItem 对应）
export interface Task {
  id: string
  title: string
  /**
   * 任务正文（markdown）。列表响应（getTasks/getMemberTasks）已瘦身不携带
   * content 全文，此处为 undefined；编辑/详情场景经 getTaskDetail 拉取完整任务。
   * 注意：快捷修改（拖拽/勾选）直接 PATCH 任务对象时 content 为 undefined，
   * JSON 序列化省略该字段 → 后端按"缺失"语义保留服务端现值，不会清空正文。
   */
  content?: string
  /** 列表响应中的正文预览（截断），卡片/列表项展示用 */
  contentPreview?: string
  status: string
  priority: string
  scheduled: string
  due: string
  /** 计划开始时刻（HH:mm，空 = 无精确时间提醒） */
  scheduledTime?: string
  /** 截止时刻（HH:mm，空 = 无精确时间提醒） */
  dueTime?: string
  progress: number
  assignee: string
  postponedCount: number
  autoPostponed: boolean
  sortOrder: number
  // 关联的文件路径（相对用户根目录，todo ↔ file 联动）
  files?: string[]
}

// 全量同步：获取所有任务（GET /api/tasks）— 仅返回当前登录用户自己的任务
export async function getTasks() {
  return apiCall('/api/tasks')
}

// 任务详情（GET /api/tasks/{id}）— 含 content 全文（列表已瘦身），
// 编辑弹窗/详情查看用；同时返回当前 revision
export async function getTaskDetail(id: string | number): Promise<{ task: Task; revision: number }> {
  return apiCall(`/api/tasks/${encodeURIComponent(String(id))}`)
}

// 查看同组织成员的任务（只读）— GET /api/tasks/user/{userId}
// 服务端校验目标成员与当前用户同 org 且关系 active，返回结构与 getTasks 一致
export async function getMemberTasks(userId: number | string) {
  return apiCall(`/api/tasks/user/${encodeURIComponent(String(userId))}`)
}

// 反查关联了指定文件的任务 — GET /api/tasks/by-file?path=...
// todo ↔ file 联动：返回当前用户任务中 files 包含该路径的任务
export async function getTasksByFile(path: string): Promise<{ tasks: Task[] }> {
  return apiCall(`/api/tasks/by-file?path=${encodeURIComponent(path)}`)
}

// 当前用户未完成任务（status != done，按截止日升序）— GET /api/tasks/open
// AI 面板任务抽屉等场景用
export async function getOpenTasks(): Promise<{ tasks: Task[] }> {
  return apiCall('/api/tasks/open')
}

// 轻量版本号端点（GET /api/tasks/revision）：多设备轮询对账用
export async function getTaskRevision(): Promise<{ revision: number }> {
  return apiCall('/api/tasks/revision')
}

// 增量写入统一解析：成功返回 {revision}；409（code 40901）抛带 serverTask/serverRevision 的错误
function parseMutationResponse(r: Response) {
  return r.json().then((body: any) => {
    if (r.ok && body?.code === 0) return body?.data || {}
    // 乐观锁冲突：服务端任务副本与当前 revision 附在错误对象上
    if (body?.code === 40901) {
      throw Object.assign(new Error(body?.msg || 'task conflict'), {
        code: 40901,
        serverTask: body?.data?.task,
        serverRevision: body?.data?.revision,
      })
    }
    throw Object.assign(new Error(body?.msg || `HTTP ${r.status}`), { code: body?.code, status: r.status })
  })
}

// 增量更新单条任务（PATCH /api/tasks/{id}，baseRevision 必填乐观锁）
export async function updateTask(task: Task, baseRevision: number) {
  const r = await authFetch(`/api/tasks/${encodeURIComponent(task.id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task, baseRevision }),
  })
  return parseMutationResponse(r)
}

// 增量新增单条任务（POST /api/tasks/{id}，baseRevision 必填）
export async function addTask(task: Task, baseRevision: number) {
  const r = await authFetch(`/api/tasks/${encodeURIComponent(task.id)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task, baseRevision }),
  })
  return parseMutationResponse(r)
}

// 增量删除单条任务（DELETE /api/tasks/{id}?baseRevision=，必填）
export async function deleteTask(taskId: string, baseRevision: number) {
  const r = await authFetch(`/api/tasks/${encodeURIComponent(taskId)}?baseRevision=${baseRevision}`, {
    method: 'DELETE',
  })
  return parseMutationResponse(r)
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
