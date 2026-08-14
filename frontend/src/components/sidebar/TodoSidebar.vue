<script setup>
// Todo 侧栏：按状态分组的任务列表，保持与文件树一致的外观风格
import { computed } from 'vue'
import { tasks, activeTaskId, viewingMember, syncStatus } from '../../stores/todoStore'

const priorityMap = { high: '高', medium: '中', low: '低' }

function isOverdue(task) {
  if (!task.due) return false
  const today = new Date().toISOString().slice(0, 10)
  return task.due < today && task.status !== 'done'
}

const displayTasks = computed(() => {
  if (viewingMember.value) return tasks.value.filter(t => t.assignee === viewingMember.value)
  return tasks.value
})

// 按 due 倒序排序（无 due 的放最后，按 createdAt 降序兜底）
function sortByDueDesc(items) {
  return [...items].sort((a, b) => {
    // 有 due 优先于无 due
    if (a.due && !b.due) return -1
    if (!a.due && b.due) return 1
    if (a.due && b.due) return a.due < b.due ? 1 : (a.due > b.due ? -1 : 0)
    // 都没有 due，按 id 倒序（id 通常随时间递增，等价于按时间倒序）
    return a.id < b.id ? 1 : (a.id > b.id ? -1 : 0)
  })
}

const groupedTasks = computed(() => [
  { key: 'todo',     label: '待办',   items: sortByDueDesc(displayTasks.value.filter(t => t.status === 'todo')) },
  { key: 'progress', label: '进行中', items: sortByDueDesc(displayTasks.value.filter(t => t.status === 'progress')) },
  { key: 'done',     label: '已完成', items: sortByDueDesc(displayTasks.value.filter(t => t.status === 'done')) },
])
</script>

<template>
  <div class="todo-sidebar-panel" id="todo-sidebar-app">
    <div class="sidebar-header">
      TASKS
      <span v-if="syncStatus === 'syncing'" class="sync-badge">⏳</span>
      <span v-if="syncStatus === 'error'" class="sync-badge" title="Sync failed">⚠️</span>
    </div>
    <div class="task-groups">
      <div v-if="displayTasks.length === 0" class="empty-hint">No tasks</div>
      <template v-for="group in groupedTasks" :key="group.key">
        <div class="task-group" v-if="group.items.length > 0">
          <div class="group-label">{{ group.label }} ({{ group.items.length }})</div>
          <div
            v-for="task in group.items"
            :key="task.id"
            class="task-row"
            :class="{ active: task.id === activeTaskId, overdue: isOverdue(task), done: task.status === 'done' }"
            @click="activeTaskId = task.id"
          >
            <span class="task-priority" :class="'p-' + task.priority">{{ priorityMap[task.priority] || task.priority }}</span>
            <span class="task-name">{{ task.title }}</span>
            <span v-if="task.due" class="task-due">{{ task.due }}</span>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<!-- todo sidebar 样式已统一在 src/styles/todo.css（#todo-app 前缀全局样式） -->
