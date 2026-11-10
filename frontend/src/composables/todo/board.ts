// Todo 看板/列表视图域：tab 切换、状态分组/四象限 computed、拖拽、勾选、
// 列表快速添加。从 useTodoApp 抽出并类型化；共享状态经 context 注入。
import { ref, reactive, computed, type Ref } from 'vue'
import { formatDate, isOverdue, newTaskId, getStatusLabel } from '../../lib/todoUtils'

export interface TodoBoardContext {
  tasks: Ref<any[]>
  currentUser: Ref<any>
  /** 只读模式（查看他人日程） */
  viewingMember: Ref<string | null>
  showToast: (msg: string, type?: string) => void
  applyMutation: (method: 'PATCH' | 'POST' | 'DELETE', task: any, changedFields: string[]) => Promise<boolean>
}

export function createTodoBoard(ctx: TodoBoardContext) {
  // --- 视图状态 ---
  const activeTab = ref<string>('quadrant')
  const draggingTaskId = ref<string | number | null>(null)
  const listNewTitle = ref('')
  const listViewMode = ref<'status' | 'quadrant'>('status')
  const collapsedGroups = reactive<Record<string, boolean>>({
    todo: false, progress: false, done: false,
    q_q1: false, q_q2: false, q_q3: false, q_q4: false,
  })

  // ========== computed ==========
  const todayStr = computed(() => formatDate(new Date()))
  const donePercent = computed(() => {
    if (ctx.tasks.value.length === 0) return 0
    return Math.round(ctx.tasks.value.filter(t => t.status === 'done').length / ctx.tasks.value.length * 100)
  })
  const overdueCount = computed(() =>
    ctx.tasks.value.filter(t => t.status !== 'done' && isOverdue(t)).length)
  const postponedCount = computed(() =>
    ctx.tasks.value.filter(t => (t.postponedCount || 0) > 0 && t.status !== 'done').length)

  /** 日程拥挤检测：单日任务数超阈值（10）的日期 */
  const conflictDates = computed(() => {
    const THRESHOLD = 10
    const daily: Record<string, any[]> = {}
    for (const t of ctx.tasks.value) {
      if (t.scheduled) {
        (daily[t.scheduled] ||= []).push(t)
      }
    }
    const result: { date: string; tasks: any[] }[] = []
    for (const date in daily) {
      if (daily[date].length >= THRESHOLD) result.push({ date, tasks: daily[date] })
    }
    return result
  })
  const hasConflict = computed(() => conflictDates.value.length > 0)

  /** 列表视图：按状态分组 */
  const listGroupedTasks = computed(() => {
    const groups = [
      { key: 'todo', label: '📋 待办', items: [] as any[] },
      { key: 'progress', label: '🔄 进行中', items: [] as any[] },
      { key: 'done', label: '✅ 已完成', items: [] as any[] },
    ]
    for (const task of ctx.tasks.value) {
      const g = groups.find(g => g.key === task.status)
      if (g) g.items.push(task)
    }
    return groups
  })

  /** 四象限：重要=priority=high，紧急=due 3 天内或已逾期；已完成不进入象限 */
  const quadrantTasks = computed(() => {
    const qs = [
      { key: 'q1', icon: '🔥', label: '重要且紧急', items: [] as any[] },
      { key: 'q2', icon: '📅', label: '重要不紧急', items: [] as any[] },
      { key: 'q3', icon: '⚡', label: '紧急不重要', items: [] as any[] },
      { key: 'q4', icon: '🌱', label: '不重要不紧急', items: [] as any[] },
    ]
    for (const task of ctx.tasks.value) {
      if (task.status === 'done') continue
      const important = task.priority === 'high'
      let urgent = false
      if (task.due) {
        const d = new Date(task.due)
        if (!isNaN(d.getTime())) {
          const today = new Date(); today.setHours(0, 0, 0, 0)
          urgent = (d.getTime() - today.getTime()) <= 3 * 86400000
        }
      }
      if (important && urgent) qs[0].items.push(task)
      else if (important && !urgent) qs[1].items.push(task)
      else if (!important && urgent) qs[2].items.push(task)
      else qs[3].items.push(task)
    }
    return qs
  })

  // ========== getters ==========
  function getTasksByStatus(status: string): any[] {
    return ctx.tasks.value.filter(t => t.status === status)
  }
  function countByStatus(status: string): number {
    return ctx.tasks.value.filter(t => t.status === status).length
  }

  // ========== 交互 ==========
  function switchTab(tab: string) {
    activeTab.value = tab
  }

  function onDragStart(e: DragEvent, task: any) {
    if (ctx.viewingMember.value) {
      e.preventDefault()
      ctx.showToast('查看他人日程时不可编辑', 'warning')
      return
    }
    draggingTaskId.value = task.id
    e.dataTransfer!.setData('taskId', task.id.toString())
    e.dataTransfer!.effectAllowed = 'move'
  }

  function onBoardDrop(e: DragEvent, targetStatus: string) {
    e.preventDefault()
    if (ctx.viewingMember.value) {
      ctx.showToast('查看他人日程时不可编辑', 'warning')
      draggingTaskId.value = null
      return
    }
    const taskId = e.dataTransfer!.getData('taskId')
    if (!taskId) return
    const task = ctx.tasks.value.find(t => String(t.id) === taskId)
    if (task && task.status !== targetStatus) {
      task.status = targetStatus
      if (targetStatus === 'progress') {
        const today = formatDate(new Date())
        task.scheduled = today
        if (!task.due || task.due < today) task.due = today
      }
      if (targetStatus === 'done') task.progress = 100
      ctx.applyMutation('PATCH', task, ['status', 'scheduled', 'due', 'progress'])
      ctx.showToast(`已移动到 ${getStatusLabel(targetStatus)}`, 'success')
    }
    draggingTaskId.value = null
  }

  /** 勾选切换完成/待办 */
  function toggleTaskDone(task: any) {
    if (!ctx.currentUser.value || ctx.viewingMember.value) return
    if (task.status === 'done') {
      task.status = 'todo' // 保留用户原进度
    } else {
      task.status = 'done'
      task.progress = 100
    }
    ctx.applyMutation('PATCH', task, ['status', 'progress'])
  }

  /** 状态变更联动进度（newStatus 由 @change 传入，避免 v-model 时序问题） */
  function onStatusChange(task: any, newStatus: string) {
    if (newStatus === 'done') {
      task.progress = 100
    } else if (newStatus === 'todo' && task.progress === 100) {
      task.progress = 0
    }
  }

  /** 列表快速添加（顶部输入框） */
  function listAddTask() {
    if (ctx.viewingMember.value) return
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    const title = (listNewTitle.value || '').trim()
    if (!title) return
    const today = formatDate(new Date())
    const task = {
      id: newTaskId(),
      title,
      content: '',
      status: 'todo',
      priority: 'medium',
      scheduled: today,
      due: today,
      progress: 0,
      assignee: String(ctx.currentUser.value.userId),
      postponedCount: 0,
      autoPostponed: true,
    }
    ctx.tasks.value.push(task)
    listNewTitle.value = ''
    ctx.applyMutation('POST', task, [])
    ctx.showToast('已添加', 'success')
  }

  function toggleListGroup(key: string) {
    collapsedGroups[key] = !collapsedGroups[key]
  }

  function toggleListView() {
    listViewMode.value = listViewMode.value === 'status' ? 'quadrant' : 'status'
  }

  /** 确认完成（日历"今日完成"操作） */
  function confirmTask(taskId: string | number) {
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    const task = ctx.tasks.value.find(t => t.id === taskId)
    if (task) {
      task.confirmedDate = formatDate(new Date())
      task.status = 'done'
      task.progress = 100
      task.postponedFrom = null
      task.postponedCount = 0
      task.autoPostponed = false
      ctx.applyMutation('PATCH', task, ['status', 'progress', 'confirmedDate', 'postponedFrom', 'postponedCount', 'autoPostponed'])
      ctx.showToast('任务已确认完成', 'success')
    }
  }

  return {
    // state
    activeTab, draggingTaskId, listNewTitle, listViewMode, collapsedGroups,
    // computed
    todayStr, donePercent, overdueCount, postponedCount, conflictDates, hasConflict,
    listGroupedTasks, quadrantTasks,
    // getters & actions
    getTasksByStatus, countByStatus, switchTab,
    onDragStart, onBoardDrop, toggleTaskDone, onStatusChange,
    listAddTask, toggleListGroup, toggleListView, confirmTask,
  }
}
