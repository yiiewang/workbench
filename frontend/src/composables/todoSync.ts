// Todo 同步层：服务端权威 revision + 任务级乐观锁 + 多设备轮询对账。
// 从 useTodoApp 抽出并类型化；状态经 context 注入（多为 todoStore 的 ref）。
import type { Ref } from 'vue'
import * as taskApi from '../api/tasks'
import { normalizeAssignees } from '../lib/todoUtils'

export type MutationMethod = 'PATCH' | 'POST' | 'DELETE'

/** 同步层依赖的状态与回调（由 useTodoApp 注入） */
export interface TodoSyncContext {
  tasks: Ref<any[]>
  currentUser: Ref<any>
  authToken: Ref<string>
  syncStatus: Ref<'idle' | 'syncing' | 'error'>
  lastSyncError: Ref<string>
  /** 正在查看他人日程（只读）时跳过轮询重拉 */
  viewingMember: Ref<string | null>
  /** 编辑弹窗打开时跳过轮询重拉（防打断输入） */
  showModal: Ref<boolean>
  /** 任务级冲突卡（todoStore 单例） */
  conflictCard: Ref<any>
  orgMembers: Ref<any[]>
  showToast: (msg: string, type?: string) => void
  autoUpdateStatus: () => void
  loadOrgMembers: () => Promise<void>
}

export function createTodoSync(ctx: TodoSyncContext) {
  // 服务端权威版本号（乐观锁基准）：loadUserTasks 拉取，每次增量写成功后更新
  let lastRevision = 0
  const STORAGE_KEY_USER = 'workbench-todo-user'

  // ========== /api/tasks 请求缓存（3 秒，避免重复拉取） ==========
  let cache: any = null
  let cacheTime = 0
  const CACHE_TTL = 3000

  async function fetchTasksJSON(forceRefresh = false): Promise<any> {
    const now = Date.now()
    if (!forceRefresh && cache && (now - cacheTime) < CACHE_TTL) return cache
    try {
      const data = await taskApi.getTasks()
      cache = data
      cacheTime = now
      return data
    } catch {
      return null
    }
  }

  function invalidateCache() {
    cache = null
    cacheTime = 0
  }

  // 本地离线缓存（服务器是唯一事实源，缓存仅供离线兜底）
  function saveLocal() {
    if (!ctx.currentUser.value) return
    try {
      const userKey = STORAGE_KEY_USER + '_' + ctx.currentUser.value.userId
      localStorage.setItem(userKey, JSON.stringify({ tasks: ctx.tasks.value }))
    } catch { /* ignore */ }
  }

  // 强制从服务器重拉（冲突兜底 / 轮询发现他端写入）
  async function reloadFromServer() {
    try {
      const { orgId, userId } = ctx.currentUser.value
      const data = await taskApi.getTasks()
      const u = data?.orgs?.[orgId]?.[userId]
      if (u && Array.isArray(u.tasks)) {
        lastRevision = Number(u.revision) || 0
        ctx.tasks.value = normalizeAssignees(u.tasks)
        saveLocal()
        ctx.syncStatus.value = 'idle'
        ctx.lastSyncError.value = ''
      }
    } catch { /* 离线忽略 */ }
  }

  // ========== 增量写统一入口（乐观锁） ==========
  // 成功更新 lastRevision；409 → 冲突处理；其他错误 → 状态标记（本地修改保留）
  async function applyMutation(method: MutationMethod, task: any, changedFields: string[]): Promise<boolean> {
    const base = lastRevision
    try {
      let data: any
      if (method === 'PATCH') data = await taskApi.updateTask(task, base)
      else if (method === 'POST') data = await taskApi.addTask(task, base)
      else data = await taskApi.deleteTask(task.id, base)
      if (data && typeof data.revision === 'number') lastRevision = data.revision
      ctx.syncStatus.value = 'idle'
      ctx.lastSyncError.value = ''
      saveLocal()
      return true
    } catch (err: any) {
      if (err && err.code === 40901) {
        return handleMutationConflict(method, task, changedFields, err.serverTask, err.serverRevision)
      }
      console.error('[todo] 增量同步失败', err)
      ctx.syncStatus.value = 'error'
      ctx.lastSyncError.value = err.message || '同步失败'
      ctx.showToast('同步失败，本地修改已保留', 'warning')
      return false
    }
  }

  // 409 处理：PATCH 且服务端同字段值一致 → 静默接受服务端；
  // 同字段不同值 → 弹对比卡；POST/DELETE 冲突 → 重拉服务端
  function handleMutationConflict(method: MutationMethod, task: any, changedFields: string[],
    serverTask: any, serverRevision: number): boolean {
    if (method === 'PATCH' && serverTask) {
      const clashing = changedFields.filter(f => serverTask[f] !== task[f])
      if (!clashing.length) {
        const local = ctx.tasks.value.find(t => String(t.id) === String(task.id))
        if (local) Object.assign(local, serverTask)
        lastRevision = serverRevision
        saveLocal()
        return true
      }
      ctx.conflictCard.value = { localTask: task, serverTask, changedFields, serverRevision }
      ctx.showToast('任务被其他设备修改，请确认', 'warning')
      return false
    }
    reloadFromServer()
    return true
  }

  // 冲突卡用户裁决：choice = { field: 'local' | 'server' }，未列字段取服务端
  async function resolveConflictCard(choice: Record<string, 'local' | 'server'>) {
    const cc = ctx.conflictCard.value
    if (!cc) return
    ctx.conflictCard.value = null
    const merged = { ...cc.serverTask }
    const replayFields: string[] = []
    for (const f of cc.changedFields) {
      if (choice[f] !== 'server') {
        merged[f] = cc.localTask[f]
        replayFields.push(f)
      }
    }
    const local = ctx.tasks.value.find(t => String(t.id) === String(cc.localTask.id))
    if (local) Object.assign(local, merged)
    lastRevision = cc.serverRevision
    await applyMutation('PATCH', merged, replayFields)
  }

  // 放弃本地修改：整条采用服务端
  function discardConflictCard() {
    const cc = ctx.conflictCard.value
    if (!cc) return
    const local = ctx.tasks.value.find(t => String(t.id) === String(cc.localTask.id))
    if (local) Object.assign(local, cc.serverTask)
    lastRevision = cc.serverRevision
    ctx.conflictCard.value = null
    saveLocal()
  }

  // ========== 初始加载 ==========
  async function loadUserTasks() {
    if (!ctx.currentUser.value) return
    const { userId, orgId } = ctx.currentUser.value
    const userKey = STORAGE_KEY_USER + '_' + userId

    // 本地离线缓存（兼容读旧格式的 .tasks）
    let localTasks: any[] = []
    try {
      const saved = JSON.parse(localStorage.getItem(userKey) || 'null')
      localTasks = normalizeAssignees(saved?.tasks || [])
    } catch { localTasks = [] }

    try {
      const data = await fetchTasksJSON()
      const u = data?.orgs?.[orgId]?.[userId]
      if (u && Array.isArray(u.tasks)) {
        lastRevision = Number(u.revision) || 0
        ctx.tasks.value = normalizeAssignees(u.tasks)
        saveLocal()
      } else {
        // 服务器无数据：用本地缓存并逐条上传
        ctx.tasks.value = localTasks
        for (const tsk of localTasks) {
          await applyMutation('POST', tsk, [])
        }
      }
    } catch {
      // 离线：使用本地缓存
      ctx.tasks.value = localTasks
    }

    await ctx.loadOrgMembers()

    if (ctx.tasks.value.length > 0) {
      ctx.autoUpdateStatus()
    }
  }

  // ========== 多设备自动对账（revision 轻量轮询） ==========
  // 30s 一次 GET /api/tasks/revision（仅版本号）；他端已写入 → 静默重拉。
  const POLL_INTERVAL_MS = 30_000
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function pollRevision() {
    if (!ctx.currentUser.value || !ctx.authToken.value) return
    // 跳过：待裁决冲突 / 编辑弹窗打开 / 本地有未同步修改 / 查看他人日程
    if (ctx.conflictCard.value || ctx.showModal.value || ctx.syncStatus.value === 'error' || ctx.viewingMember.value) return
    try {
      const data = await taskApi.getTaskRevision()
      const rev = Number(data?.revision) || 0
      if (rev && rev !== lastRevision) {
        await reloadFromServer()
      }
    } catch { /* 离线忽略 */ }
  }

  function startRevisionPolling() {
    if (pollTimer) return
    pollTimer = setInterval(pollRevision, POLL_INTERVAL_MS)
    document.addEventListener('visibilitychange', pollRevisionOnVisible)
  }

  function stopRevisionPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
    document.removeEventListener('visibilitychange', pollRevisionOnVisible)
  }

  // 页面切回前台立即对账一次（不等下一个 30s）
  function pollRevisionOnVisible() {
    if (!document.hidden) pollRevision()
  }

  return {
    loadUserTasks,
    fetchTasksJSON,
    invalidateCache,
    saveLocal,
    reloadFromServer,
    applyMutation,
    resolveConflictCard,
    discardConflictCard,
    pollRevision,
    startRevisionPolling,
    stopRevisionPolling,
  }
}
