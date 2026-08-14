<script>
// TaskItem SFC — 统一任务渲染组件，一套基础样式，variant 只控制布局差异：
//   variant='list'（默认）：紧凑横排，用于四象限
//   variant='card'：卡片竖排，显示备注摘要，用于看板列
// 颜色/状态/优先级等视觉方案完全统一，不区分 variant。
// 保留 $parent 依赖（普通 script + setup 模式下 $parent 仍指向父组件实例）。
export default {
  props: {
    task: { type: Object, required: true },
    variant: { type: String, default: 'list' },  // 'list' | 'card'
    draggable: { type: Boolean, default: false }
  },
  computed: {
    isOverdue() { return this.$parent.isOverdue(this.task) && this.task.status !== 'done' },
    overdueDays() { return this.$parent.overdueDays ? this.$parent.overdueDays(this.task) : 0 }
  },
  methods: {
    onDragStartHandler(e) {
      if (!this.draggable || this.$parent.viewingMember) {
        e.preventDefault()
        return
      }
      this.$parent.onDragStart(e, this.task)
    },
    onTaskClick() {
      if (this.$parent.viewingMember) return
      this.$parent.editTask(this.task)
    }
  }
}
</script>

<template>
  <div
    class="task-item"
    :class="['task-item--' + variant,
             'status-' + task.status,
             { 'is-overdue': isOverdue,
               'is-dragging': draggable && $parent.draggingTaskId === task.id }]"
    :draggable="draggable && !$parent.viewingMember"
    @dragstart="onDragStartHandler"
    @click="onTaskClick"
  >
    <!-- checkbox（list 变体独有） -->
    <label v-if="variant === 'list'" class="task-item-checkbox" @click.stop>
      <input type="checkbox" :checked="task.status === 'done'"
        @change="$parent.toggleTaskDone(task)" :disabled="$parent.viewingMember">
      <span class="checkmark"></span>
    </label>

    <div class="task-item-body">
      <!-- 标题 -->
      <div class="task-item-title" :class="{ 'text-done': task.status === 'done' }">
        {{ task.title }}
      </div>

      <!-- 备注（card 变体独有） -->
      <div v-if="variant === 'card' && task.content" class="task-item-content">
        {{ task.content }}
      </div>

      <!-- 元信息行（优先级 + 截止日期 + 进度环，统一样式） -->
      <div class="task-item-meta">
        <!-- 优先级标签 -->
        <span class="task-priority" :class="'priority-' + task.priority">
          {{ $parent.priorityLabel(task.priority) }}
        </span>

        <!-- 截止日期 + 逾期徽标 -->
        <span v-if="task.due" class="due-wrap">
          <svg class="due-icon" width="11" height="11" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
            <rect x="2" y="3" width="10" height="9" rx="1.5"/>
            <line x1="2" y1="6" x2="12" y2="6"/>
            <line x1="5" y1="1.5" x2="5" y2="4"/>
            <line x1="9" y1="1.5" x2="9" y2="4"/>
          </svg>
          <span :class="{ 'text-overdue': isOverdue }">{{ task.due }}</span>
          <span v-if="isOverdue" class="overdue-badge" :title="'逾期 ' + overdueDays + ' 天'">
            ! {{ overdueDays }}d
          </span>
        </span>

        <!-- 进度环：card 始终显示，list 仅 progress>0 显示 -->
        <span class="progress-ring"
          v-if="(variant === 'card') || (task.progress || 0) > 0"
          :title="(task.progress || 0) + '% 完成'">
          <svg viewBox="0 0 20 20">
            <circle class="ring-bg" cx="10" cy="10" r="7" stroke-width="2.5" />
            <circle class="ring-fg" cx="10" cy="10" r="7"
              stroke-dasharray="43.98"
              :stroke-dashoffset="43.98 * (1 - (task.progress || 0) / 100)" />
          </svg>
        </span>
      </div>
    </div>

    <!-- 删除按钮（list 变体独有） -->
    <div v-if="variant === 'list' && !$parent.viewingMember" class="task-item-actions" @click.stop>
      <button class="btn-icon" title="删除" @click="$parent.deleteTask(task.id)">✕</button>
    </div>
  </div>
</template>
