// Todo 编辑弹窗域：创建/编辑/删除、日期区间、进度联动、关联文件、
// 快捷键与快速保存。从 useTodoApp 抽出并类型化；共享状态经 context 注入。
import { ref, computed, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { formatDate, newTaskId } from '../../lib/todoUtils'
import { baseName, extFromPath } from '../../lib/common'
import { openTabFn } from '../../stores/indexStore'
import { getTaskDetail } from '../../api/tasks'

export interface TodoEditorContext {
  tasks: Ref<any[]>
  currentUser: Ref<any>
  viewingMember: Ref<string | null>
  showModal: Ref<boolean>
  isCreating: Ref<boolean>
  editingTask: Ref<Record<string, any>>
  editingDateRange: Ref<string[]>
  activeTaskId: Ref<string | number | null>
  showToast: (msg: string, type?: string) => void
  applyMutation: (method: 'PATCH' | 'POST' | 'DELETE', task: any, changedFields: string[]) => Promise<boolean>
}

/** 编辑保存时的同步字段集（冲突对比卡按此全量展示） */
const EDIT_FIELDS = ['title', 'content', 'status', 'priority', 'scheduled', 'due',
  'progress', 'assignee', 'postponedCount', 'autoPostponed', 'sortOrder', 'files']

export function createTodoEditor(ctx: TodoEditorContext) {
  const router = useRouter()

  // --- 编辑器状态 ---
  /** 各状态列顶部快速添加输入（按 status 索引） */
  const newTaskTitle = ref<Record<string, string>>({ todo: '', progress: '', done: '' })
  /** markdown-editor v-model（get/set editingTask.content） */
  const editingTaskContent = computed({
    get: () => ctx.editingTask.value.content || '',
    set: (val: string) => { ctx.editingTask.value = { ...ctx.editingTask.value, content: val } },
  })
  const showFilePicker = ref(false)

  const editingMaxDate = computed(() => formatDate(new Date()))

  // ========== 打开/关闭 ==========
  /** 从状态列顶部输入创建：预填标题后打开弹窗（confirmCreate 落库） */
  function addTask(status: string) {
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    const title = (newTaskTitle.value[status] || '').trim()
    if (!title) return
    const today = formatDate(new Date())
    ctx.isCreating.value = true
    ctx.editingTask.value = {
      id: null, // 创建时 id 为 null，confirmCreate 中分配
      title,
      content: '',
      status,
      priority: 'medium',
      progress: 0,
      scheduled: today,
      due: today,
      assignee: String(ctx.currentUser.value.userId),
      postponedCount: 0,
      autoPostponed: false,
    }
    ctx.editingDateRange.value = [today + ' 00:00', today + ' 00:00']
    ctx.showModal.value = true
  }

  /** 编辑已有任务：先拉详情（content 不随列表下发），成功后再弹窗；
   *  失败不弹窗，避免无正文的任务对象进入保存链路 */
  async function editTask(task: any) {
    if (ctx.viewingMember.value) return
    let full = task
    try {
      const data = await getTaskDetail(task.id)
      if (data?.task) full = data.task
    } catch {
      ctx.showToast('加载任务详情失败，请重试', 'error')
      return
    }
    ctx.isCreating.value = false
    ctx.editingTask.value = { ...full }
    // 初始化日期时间区间选择器（datetimerange：YYYY-MM-DD HH:mm）并保证 scheduled <= due；
    // 无精确时间的任务回填 00:00 占位（提交时 00:00 归一为空 = 无精确提醒）
    if (full.scheduled && full.due) {
      const start = full.scheduled <= full.due ? full.scheduled : full.due
      const end = full.scheduled <= full.due ? full.due : full.scheduled
      const startT = start === full.scheduled ? (full.scheduledTime || '00:00') : (full.dueTime || '00:00')
      const endT = start === full.scheduled ? (full.dueTime || '00:00') : (full.scheduledTime || '00:00')
      ctx.editingDateRange.value = [start + ' ' + startT, end + ' ' + endT]
      ctx.editingTask.value.scheduled = start
      ctx.editingTask.value.due = end
    } else {
      ctx.editingDateRange.value = []
    }
    ctx.showModal.value = true
  }

  /** 关闭弹窗：清除 showModal + activeTaskId（否则再次点击同一任务 watch 不触发） */
  function closeModal() {
    ctx.showModal.value = false
    ctx.activeTaskId.value = null
  }

  // ========== 日期区间与进度联动 ==========
  // datetimerange（YYYY-MM-DD HH:mm）→ 日期 + 时刻字段拆分。
  // 约定：时刻 00:00 视为未指定精确提醒（存空串），避免无时间任务
  // 回填 00:00 后整点触发无意义的提醒风暴。
  function onEditingDateRangeChange(dates: string[] | null) {
    const split = (dt: string) => ({
      date: dt.slice(0, 10),
      time: dt.length > 10 && dt.slice(11, 16) !== '00:00' ? dt.slice(11, 16) : '',
    })
    if (dates && dates.length === 2) {
      const s = split(dates[0])
      const e = split(dates[1])
      ctx.editingTask.value.scheduled = s.date
      ctx.editingTask.value.due = e.date
      ctx.editingTask.value.scheduledTime = s.time
      ctx.editingTask.value.dueTime = e.time
    } else {
      ctx.editingTask.value.scheduled = null
      ctx.editingTask.value.due = null
      ctx.editingTask.value.scheduledTime = ''
      ctx.editingTask.value.dueTime = ''
    }
  }

  /** 进度改 100 → 状态置已完成；从 100 调低且状态已完成 → 回退进行中 */
  function setEditingProgress(p: number) {
    ctx.editingTask.value.progress = p
    if (p === 100) {
      ctx.editingTask.value.status = 'done'
    } else if (ctx.editingTask.value.status === 'done') {
      ctx.editingTask.value.status = 'progress'
    }
  }

  // ========== 保存/创建/删除 ==========
  /** 创建校验（返回错误提示，空串通过） */
  function validateEditing(): string {
    const t = ctx.editingTask.value
    if (!t.scheduled) return '请选择计划日期'
    if (!t.due) return '请选择截止日期'
    if (t.scheduled > t.due) return '计划日期不能晚于截止日期'
    return ''
  }

  function confirmCreate() {
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    const err = validateEditing()
    if (err) { ctx.showToast(err, 'error'); return }
    const task = {
      id: newTaskId(),
      title: ctx.editingTask.value.title,
      content: ctx.editingTask.value.content || '',
      status: ctx.editingTask.value.status,
      priority: ctx.editingTask.value.priority || 'medium',
      progress: ctx.editingTask.value.progress || 0,
      assignee: ctx.editingTask.value.assignee || String(ctx.currentUser.value.userId),
      due: ctx.editingTask.value.due,
      scheduled: ctx.editingTask.value.scheduled,
      createdAt: formatDate(new Date()),
      confirmedDate: null,
      postponedFrom: null,
      postponedCount: 0,
      autoPostponed: false,
      files: ctx.editingTask.value.files || [],
    }
    ctx.tasks.value.push(task)
    newTaskTitle.value[ctx.editingTask.value.status] = ''
    ctx.showModal.value = false
    ctx.applyMutation('POST', task, [])
    ctx.showToast('任务已创建', 'success')
  }

  function saveEdit() {
    if (ctx.viewingMember.value) return
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    const err = validateEditing()
    if (err) { ctx.showToast(err, 'error'); return }
    const updatedTask = { ...ctx.editingTask.value }
    if (updatedTask.status === 'done') {
      updatedTask.confirmedDate = formatDate(new Date())
    }
    const idx = ctx.tasks.value.findIndex(t => t.id === updatedTask.id)
    if (idx !== -1) {
      ctx.tasks.value.splice(idx, 1, updatedTask)
      ctx.applyMutation('PATCH', updatedTask, EDIT_FIELDS)
      ctx.showToast('任务已更新', 'success')
    }
    closeModal()
  }

  /** 快速保存（Ctrl+S）：保存但不关闭弹窗 */
  function quickSaveEdit() {
    if (ctx.viewingMember.value) return
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    const err = validateEditing()
    if (err) { ctx.showToast(err, 'error'); return }
    const updatedTask = { ...ctx.editingTask.value }
    if (updatedTask.status === 'done') {
      updatedTask.confirmedDate = formatDate(new Date())
    }
    const idx = ctx.tasks.value.findIndex(t => t.id === updatedTask.id)
    if (idx !== -1) {
      ctx.tasks.value.splice(idx, 1, updatedTask)
      ctx.applyMutation('PATCH', updatedTask, ['status', 'scheduled', 'due', 'progress'])
      ctx.showToast('任务已保存', 'success')
    }
  }

  function deleteTask(id: string | number) {
    if (ctx.viewingMember.value) return
    if (!ctx.currentUser.value) {
      ctx.showToast('请先填写用户信息', 'error')
      return
    }
    ctx.tasks.value = ctx.tasks.value.filter(t => t.id !== id)
    ctx.applyMutation('DELETE', { id }, [])
    closeModal()
    ctx.showToast('任务已删除', 'warning')
  }

  /** 弹窗键盘快捷键：Ctrl/Cmd+S 保存、Escape 关闭 */
  function handleModalKeydown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault()
      e.stopPropagation()
      if (ctx.isCreating.value) confirmCreate()
      else quickSaveEdit()
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      closeModal()
    }
  }

  // ========== 关联文件（todo ↔ file 联动） ==========
  function removeEditingFile(i: number) {
    (ctx.editingTask.value.files || []).splice(i, 1)
  }

  /** 关联文件 chip 点击 → 文件浏览页新 tab 打开（openTabFn 由 useIndexApp 注册） */
  function openLinkedFile(path: string) {
    const name = baseName(path)
    if (openTabFn) openTabFn(path, name, extFromPath(name), 0)
    router.push('/file')
  }

  function openFilePicker() {
    showFilePicker.value = true
  }

  /** FilePickerModal 确认：批量追加（去重、上限 20） */
  function confirmPickFiles(paths: string[]) {
    const files = ctx.editingTask.value.files || (ctx.editingTask.value.files = [])
    let added = 0
    for (const p of paths || []) {
      if (files.includes(p)) continue
      if (files.length >= 20) { ctx.showToast('关联文件最多 20 个', 'error'); break }
      files.push(p)
      added++
    }
    showFilePicker.value = false
    if (added) ctx.showToast(`已添加 ${added} 个关联文件`)
  }

  return {
    // state
    newTaskTitle, showFilePicker,
    // computed
    editingTaskContent, editingMaxDate,
    // 打开/关闭
    addTask, editTask, closeModal,
    // 日期与进度联动
    onEditingDateRangeChange, setEditingProgress,
    // 保存
    confirmCreate, saveEdit, quickSaveEdit, deleteTask, handleModalKeydown,
    // 关联文件
    removeEditingFile, openLinkedFile, openFilePicker, confirmPickFiles,
  }
}
