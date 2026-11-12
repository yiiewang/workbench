// useTodoApp：Todo 应用的组装层——身份/init/成员/数据导入导出 + 三域模块
// （board 看板 / calendar 日历 / editor 编辑弹窗）与同步层（todoSync）的接线。
// 各域实现见 composables/todo/*，均为类型化模块；本文件也已去除 @ts-nocheck。
import { watch, onMounted, onActivated, onDeactivated } from 'vue'
import {
    tasks, showModal, isCreating, editingTask, editingDateRange,
    currentUser, authToken,
    viewingMember, orgMembers, syncStatus, lastSyncError,
    activeTaskId, conflictCard,
} from '../stores/todoStore'
import { checkAuth, registerTodoAppReset } from '../stores/indexStore'
import * as taskApi from '../api/tasks'
import { ElNotification } from 'element-plus'
import {
    formatDate, formatDateShort, dayOffset,
    isOverdue, overdueDays, priorityLabel, statusIcon, getStatusLabel,
} from '../lib/todoUtils'
import { createTodoSync } from './todoSync'
import { createTodoBoard } from './todo/board'
import { createTodoCalendar } from './todo/calendar'
import { createTodoEditor } from './todo/editor'

/** 状态列定义（看板列头） */
export const columns = [
    { key: 'todo', label: '待办', icon: '🔴' },
    { key: 'progress', label: '进行中', icon: '🟡' },
    { key: 'done', label: '已完成', icon: '✅' },
]

export function setupTodoApp() {
    // --- Toast ---
    function showToast(msg: string, type?: string) {
        const typeMap: Record<string, 'success' | 'error' | 'warning' | 'info'> = {
            success: 'success', error: 'error', warning: 'warning', info: 'info',
        }
        ElNotification({
            message: msg,
            type: typeMap[type || ''] || 'success',
            duration: 2000,
            position: 'bottom-right',
        })
    }

    // --- 自动延期天数（只计算，不自动改状态） ---
    function autoUpdateStatus() {
        const today = formatDate(new Date())
        const todayDt = new Date(today + 'T00:00:00')
        for (const task of tasks.value) {
            if (task.status === 'done') continue
            if (!task.due) continue
            const dueDt = new Date(task.due + 'T00:00:00')
            if (dueDt < todayDt && task.progress < 100) {
                const diffDays = Math.round((todayDt.getTime() - dueDt.getTime()) / 86400000)
                task.postponedCount = diffDays > 0 ? diffDays : 0
                if (!task.postponedFrom) {
                    task.postponedFrom = task.scheduled
                }
            }
        }
    }

    // --- 组织成员 ---
    // 数据源 GET /api/org-members（返回 [{id, name}]）；key 为字符串化整数 id。
    async function loadOrgMembers() {
        if (!currentUser.value) return
        try {
            const data: any = await taskApi.getOrgMembers()
            orgMembers.value = (data && data.members) ? data.members : []
        } catch {
            orgMembers.value = []
        }
    }

    // id → name 反查：viewingMember/assignee 存字符串化 id，展示时还原业务 name
    function memberName(id: string | number | null): string {
        if (id == null) return ''
        const key = String(id)
        const m = orgMembers.value.find(m => String(m.id) === key)
        return m ? m.name : key
    }

    // ========== 同步层（服务端权威 revision + 乐观锁 + 轮询对账，见 todoSync.ts） ==========
    const sync = createTodoSync({
        tasks, currentUser, authToken, syncStatus, lastSyncError,
        viewingMember, showModal, conflictCard, orgMembers,
        showToast, autoUpdateStatus, loadOrgMembers,
    })
    const {
        loadUserTasks, saveLocal, applyMutation,
        resolveConflictCard, discardConflictCard,
        startRevisionPolling, stopRevisionPolling,
    } = sync

    // 身份切换（登出/换账号/切换组织）时清空任务请求缓存（todoSync 内部 3 秒 TTL）
    registerTodoAppReset(sync.invalidateCache)

    // KeepAlive：TodoView 失活暂停轮询，激活恢复
    onActivated(startRevisionPolling)
    onDeactivated(stopRevisionPolling)

    // ========== 三域模块 ==========
    // 先建看板（draggingTaskId 归它），日历复用该 ref 做拖放复位
    const board = createTodoBoard({
        tasks, currentUser, viewingMember,
        showToast, applyMutation,
    })
    const calendar = createTodoCalendar({
        tasks, currentUser, viewingMember,
        draggingTaskId: board.draggingTaskId,
        showToast, applyMutation,
    })
    const editor = createTodoEditor({
        tasks, currentUser, viewingMember,
        showModal, isCreating, editingTask, editingDateRange, activeTaskId,
        showToast, applyMutation,
    })

    // ========== 身份与初始化 ==========
    // 认证校验委托给 auth store 的 checkAuth()；未登录时路由守卫已拦截跳转 /login。
    async function init() {
        if (await checkAuth()) {
            startTokenRefresh()
            await loadUserTasks()
        }
    }

    // 每天自动校验 token，过期清除认证（apiCall 全局 401 拦截跳转登录页）
    let tokenTimer: ReturnType<typeof setInterval> | null = null
    const TOKEN_REFRESH_INTERVAL_MS = 24 * 60 * 60 * 1000
    function startTokenRefresh() {
        if (tokenTimer) clearInterval(tokenTimer)
        tokenTimer = setInterval(async () => {
            if (!authToken.value) return
            const valid = await checkAuth()
            if (!valid) {
                showToast('登录已过期，请重新登录', 'error')
            }
        }, TOKEN_REFRESH_INTERVAL_MS)
    }

    onMounted(() => { init() })

    // 查看他人日程（只读）：切换数据源（专用端点，不走缓存）
    watch(viewingMember, async (newMemberId) => {
        if (!newMemberId) {
            await loadUserTasks()
            showToast('已切换回我的日程', 'success')
            return
        }
        if (!currentUser.value) return
        const { orgId } = currentUser.value
        const memberNameText = memberName(newMemberId)
        try {
            const data: any = await taskApi.getMemberTasks(newMemberId)
            const org = (data.orgs || {})[orgId]
            if (org && org[newMemberId] && Array.isArray(org[newMemberId].tasks)) {
                tasks.value = org[newMemberId].tasks
                showToast(`正在查看 ${memberNameText} 的日程（只读）`, 'success')
            } else {
                tasks.value = []
                showToast(`${memberNameText} 暂无任务`, 'warning')
            }
        } catch {
            tasks.value = []
            showToast('加载成员日程失败', 'error')
        }
    })

    // TodoSidebar 点任务 → 主视图打开编辑弹窗
    watch(activeTaskId, (id) => {
        if (id != null) {
            const task = tasks.value.find(t => t.id === id)
            if (task) editor.editTask(task)
        }
    })

    // ========== 导入 / 导出 / 清空 ==========
    function exportJSON() {
        const payload = {
            version: '2.0',
            lastUpdated: new Date().toISOString(),
            config: { dailyCapacity: 3, conflictThreshold: 4, autoSave: true },
            tasks: tasks.value,
        }
        const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = `tasks-${formatDate(new Date())}.json`
        a.click()
        URL.revokeObjectURL(url)
        showToast('已导出 JSON', 'success')
    }

    /** 导入 JSON：合并语义——同 id 跳过，新任务逐条 POST 上传 */
    function importJSON() {
        const input = document.createElement('input')
        input.type = 'file'
        input.accept = '.json'
        input.onchange = (e) => {
            const file = (e.target as HTMLInputElement).files?.[0]
            if (!file) return
            const reader = new FileReader()
            reader.onload = async (ev) => {
                try {
                    const data = JSON.parse(String(ev.target?.result))
                    const imported: any[] = Array.isArray(data) ? data
                        : (Array.isArray(data?.tasks) ? data.tasks : null)
                    if (!imported) throw new Error('无法识别的格式（需要任务数组）')
                    const existing = new Set(tasks.value.map(t => String(t.id)))
                    const fresh = imported.filter(t => t && t.id && !existing.has(String(t.id)))
                    if (!fresh.length) {
                        showToast('导入的任务都已存在，未新增', 'info')
                        return
                    }
                    for (const t of fresh) tasks.value.push(t)
                    for (const t of fresh) await applyMutation('POST', t, [])
                    showToast(`导入成功，新增 ${fresh.length} 条（跳过 ${imported.length - fresh.length} 条已存在）`, 'success')
                } catch (err: any) {
                    showToast('导入失败: ' + err.message, 'error')
                }
            }
            reader.readAsText(file)
        }
        input.click()
    }

    function saveToFile() {
        saveLocal()
        exportJSON()
    }

    /** 清空当前用户全部任务（逐条 DELETE 同步服务器） */
    function clearAll() {
        if (!currentUser.value) {
            showToast('请先填写用户信息', 'error')
            return
        }
        const userKey = String(currentUser.value.userId)
        const userName = currentUser.value.userName || userKey
        const userTasks = tasks.value.filter(t => t.assignee === userKey)
        if (userTasks.length === 0) {
            showToast('你当前没有任务可清空', 'info')
            return
        }
        if (!confirm(`确定要清空「${userName}」的所有任务（共 ${userTasks.length} 项）吗？此操作不可撤销！`)) return
        const removedIds = userTasks.map(t => t.id)
        tasks.value = tasks.value.filter(t => t.assignee !== userKey)
        for (const id of removedIds) {
            applyMutation('DELETE', { id }, [])
        }
        saveLocal()
        showToast(`已清空「${userName}」的 ${userTasks.length} 项任务`, 'warning')
    }

    // ========== Expose to Template（保持与拆分前完全一致的导出面） ==========
    return {
        columns, tasks,
        // 看板/列表域
        ...board,
        // 日历/甘特域
        ...calendar,
        // 编辑弹窗域
        ...editor,
        // 编辑弹窗状态（store refs 经 ctx 传入各域模块，模块不再转发——此处补齐导出，
        // TodoView 的 watch(ShowModal)/模板 v-if/isCreating/editingTask 直接使用）
        showModal, isCreating, editingTask, editingDateRange, activeTaskId,
        // 纯工具转发（模板直接使用）
        formatDateShort, dayOffset, isOverdue, overdueDays,
        priorityLabel, statusIcon, getStatusLabel,
        // 数据管理
        exportJSON, importJSON, saveToFile, clearAll, saveLocal,
        // 身份与成员
        currentUser, viewingMember, orgMembers, memberName,
        // 同步状态
        syncStatus, lastSyncError,
        // 任务级冲突（409 对比卡）
        conflictCard, resolveConflictCard, discardConflictCard,
    }
}
