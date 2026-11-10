// Todo 日历/甘特域：周导航、日期格任务、甘特轨道打包、日历拖放。
// 从 useTodoApp 抽出并类型化；共享状态经 context 注入。
import { ref, computed, type Ref } from 'vue'
import { getWeekStart, getWeekNumber, formatDate, newTaskId } from '../../lib/todoUtils'

export interface TodoCalendarContext {
  tasks: Ref<any[]>
  currentUser: Ref<any>
  viewingMember: Ref<string | null>
  /** 看板域的拖拽中任务 id（跨域共享：日历 drop 复位） */
  draggingTaskId: Ref<string | number | null>
  showToast: (msg: string, type?: string) => void
  applyMutation: (method: 'PATCH' | 'POST' | 'DELETE', task: any, changedFields: string[]) => Promise<boolean>
}

export function createTodoCalendar(ctx: TodoCalendarContext) {
  // --- 日历状态 ---
  const currentWeekStart = ref<Date>(getWeekStart(new Date()))
  const calendarDragOverDate = ref<string | null>(null)
  /** 日历格内联快速添加的标题输入（按日期索引） */
  const newTaskForDate = ref<Record<string, string>>({})

  // ========== computed ==========
  const calTitle = computed(() => {
    const ws = currentWeekStart.value
    return `${ws.getFullYear()}年 第${getWeekNumber(ws)}周`
  })

  const weekDays = computed(() => {
    const result: { name: string; month: number; date: number; dateStr: string; isToday: boolean; isWeekend: boolean }[] = []
    const weekdayNames = ['一', '二', '三', '四', '五', '六', '日']
    const today = new Date()
    today.setHours(0, 0, 0, 0)
    for (let i = 0; i < 7; i++) {
      const d = new Date(currentWeekStart.value)
      d.setDate(d.getDate() + i)
      result.push({
        name: weekdayNames[i],
        month: d.getMonth() + 1,
        date: d.getDate(),
        dateStr: formatDate(d),
        isToday: formatDate(d) === formatDate(today),
        isWeekend: d.getDay() === 0 || d.getDay() === 6,
      })
    }
    return result
  })

  /** 当前周内可见的甘特任务（与当前周有实际重叠） */
  const ganttTasks = computed(() => {
    const ws = currentWeekStart.value
    const we = new Date(ws)
    we.setDate(we.getDate() + 6)
    const wsStr = formatDate(ws)
    const weStr = formatDate(we)
    return ctx.tasks.value.filter(t => {
      if (!t.scheduled) return false
      const end = t.due || t.scheduled
      return t.scheduled <= weStr && end >= wsStr
    })
  })

  const ganttTaskCount = computed(() => ganttTasks.value.length)

  /** 甘特图行打包：时间不重叠的任务贪心分配到同一轨道 */
  const ganttTracks = computed(() => {
    const list = ganttTasks.value
    if (!list.length) return []
    const sorted = [...list].sort((a, b) => {
      const aStart = a.scheduled || ''
      const bStart = b.scheduled || ''
      if (aStart !== bStart) return aStart.localeCompare(bStart)
      return (a.due || a.scheduled || '').localeCompare(b.due || b.scheduled || '')
    })
    const tracks: { tasks: any[]; lastEnd: string }[] = []
    for (const task of sorted) {
      const taskStart = task.scheduled || ''
      const taskEnd = task.due || task.scheduled || ''
      let placed = false
      for (const track of tracks) {
        if (track.lastEnd < taskStart) {
          track.tasks.push(task)
          track.lastEnd = taskEnd
          placed = true
          break
        }
      }
      if (!placed) {
        tracks.push({ tasks: [task], lastEnd: taskEnd })
      }
    }
    return tracks.map(t => t.tasks)
  })

  // ========== getters ==========
  /** 指定日期格的任务（未完成且 scheduled..due 覆盖该日） */
  function getTasksForDate(dateStr: string): any[] {
    return ctx.tasks.value.filter(t => {
      if (t.status === 'done') return false
      if (!t.scheduled) return false
      const end = t.due || t.scheduled
      return dateStr >= t.scheduled && dateStr <= end
    })
  }

  /** 甘特条定位样式：在 7 列日期区域内按百分比定位 */
  function ganttBarStyleV2(task: any): { left: string; width: string; maxWidth: string } {
    const ws = currentWeekStart.value
    const we = new Date(ws)
    we.setDate(we.getDate() + 6)
    const start = new Date(task.scheduled + 'T00:00:00')
    const end = task.due ? new Date(task.due + 'T00:00:00') : start
    const visStart = start < ws ? ws : start
    const visEnd = end > we ? we : end
    const startDay = Math.round((visStart.getTime() - ws.getTime()) / 86400000)
    const endDay = Math.round((visEnd.getTime() - ws.getTime()) / 86400000)
    // CSS % 相对父容器总宽度统一除以 7；bar 两侧留 gap 使相邻任务有视觉间隔
    const GAP_PCT = 0.4
    const leftPct = (startDay / 7) * 100 + GAP_PCT
    const daySpan = Math.min(endDay, 6) - startDay + 1
    const widthPct = Math.max((daySpan / 7) * 100 - GAP_PCT * 2, 2)
    return {
      left: leftPct.toFixed(1) + '%',
      width: widthPct.toFixed(1) + '%',
      maxWidth: (100 - leftPct).toFixed(1) + '%',
    }
  }

  // ========== 周导航 ==========
  function changeWeek(delta: number) {
    const d = new Date(currentWeekStart.value)
    d.setDate(d.getDate() + delta * 7)
    currentWeekStart.value = d
  }

  function goToThisWeek() {
    currentWeekStart.value = getWeekStart(new Date())
  }

  // ========== 日历拖放 ==========
  function onCalendarDragOver(dateStr: string) {
    if (ctx.viewingMember.value) return
    calendarDragOverDate.value = dateStr
  }

  function onCalendarDragLeave() {
    if (ctx.viewingMember.value) return
    calendarDragOverDate.value = null
  }

  function onCalendarDrop(e: DragEvent, dateStr: string) {
    e.preventDefault()
    const bail = () => {
      calendarDragOverDate.value = null
      ctx.draggingTaskId.value = null
    }
    if (ctx.viewingMember.value) {
      ctx.showToast('查看他人日程时不可编辑', 'warning')
      bail()
      return
    }
    const today = formatDate(new Date())
    if (dateStr < today) {
      ctx.showToast('无法将任务安排到过往日期', 'error')
      bail()
      return
    }
    const taskId = e.dataTransfer!.getData('taskId')
    if (!taskId) return
    const task = ctx.tasks.value.find(t => String(t.id) === taskId)
    if (task) {
      task.scheduled = dateStr
      task.postponedFrom = null
      task.postponedCount = 0
      task.confirmedDate = null
      task.autoPostponed = false
      ctx.applyMutation('PATCH', task, ['scheduled', 'due', 'postponedFrom', 'postponedCount', 'confirmedDate', 'autoPostponed'])
      ctx.showToast(`已安排到 ${dateStr}`, 'success')
    }
    bail()
  }

  /** 日历格内联快速添加（当日在格内输入标题） */
  function addTaskToCalendar(dateStr: string) {
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    const title = (newTaskForDate.value[dateStr] || '').trim()
    if (!title) return
    const task = {
      id: newTaskId(),
      title,
      status: 'progress',
      priority: 'medium',
      progress: 0,
      assignee: String(ctx.currentUser.value.userId),
      due: dateStr,
      scheduled: dateStr,
      createdAt: formatDate(new Date()),
      confirmedDate: null,
      postponedFrom: null,
      postponedCount: 0,
      autoPostponed: false,
    }
    ctx.tasks.value.push(task)
    newTaskForDate.value[dateStr] = ''
    ctx.applyMutation('POST', task, [])
    ctx.showToast('任务已添加到日历', 'success')
  }

  return {
    // state
    currentWeekStart, calendarDragOverDate, newTaskForDate,
    // computed
    calTitle, weekDays, ganttTasks, ganttTaskCount, ganttTracks,
    // getters & actions
    getTasksForDate, ganttBarStyleV2, changeWeek, goToThisWeek,
    onCalendarDragOver, onCalendarDragLeave, onCalendarDrop, addTaskToCalendar,
  }
}
