<script setup lang="ts">
// 任务选择器弹窗：列出未完成任务供多选，把指定文件挂到所选任务（反向建立关联）。
import { ref, onMounted } from 'vue'
import ModalShell from '../ui/ModalShell.vue'
import { getOpenTasks, type Task } from '../../api/tasks'

const props = defineProps<{
  /** 要挂载的文件路径 */
  filePath: string
  /** 已关联该文件的任务 id（列表中禁选，避免重复挂载） */
  linkedIds?: string[]
}>()

const emit = defineEmits<{ close: []; confirm: [taskIds: string[]] }>()

const tasks = ref<Task[]>([])
const loading = ref(true)
const checked = ref<Set<string>>(new Set())

onMounted(async () => {
  try {
    const data = await getOpenTasks()
    tasks.value = (data?.tasks || []) as Task[]
  } catch {
    tasks.value = []
  } finally {
    loading.value = false
  }
})

function isLinked(t: Task): boolean {
  return (props.linkedIds || []).includes(String(t.id)) || (t.files || []).includes(props.filePath)
}

function toggle(t: Task) {
  const id = String(t.id)
  if (checked.value.has(id)) checked.value.delete(id)
  else checked.value.add(id)
}

function confirm() {
  if (!checked.value.size) return
  emit('confirm', [...checked.value])
}

const statusLabel: Record<string, string> = { todo: '待办', progress: '进行中' }
</script>

<template>
  <ModalShell title="挂到任务" width="420px" @close="emit('close')">
    <div class="tp-hint">
      勾选任务后，当前文件 <code>{{ filePath }}</code> 将加入其关联文件列表。
    </div>
    <div class="tp-list">
      <div v-if="loading" class="tp-empty">加载中…</div>
      <div v-else-if="!tasks.length" class="tp-empty">暂无未完成任务</div>
      <label
        v-for="t in tasks"
        :key="t.id"
        class="tp-item"
        :class="{ checked: checked.has(String(t.id)), linked: isLinked(t), disabled: isLinked(t) }"
        @click="!isLinked(t) && toggle(t)"
      >
        <input
          type="checkbox"
          :checked="checked.has(String(t.id)) || isLinked(t)"
          :disabled="isLinked(t)"
          @click.stop="!isLinked(t) && toggle(t)"
        >
        <div class="tp-main">
          <div class="tp-title">{{ t.title }}</div>
          <div class="tp-meta">
            <span class="tp-status" :class="t.status">{{ statusLabel[t.status] || t.status }}</span>
            <span v-if="t.due">截止 {{ t.due }}</span>
          </div>
        </div>
        <span v-if="isLinked(t)" class="tp-linked-tag">已关联</span>
      </label>
    </div>
    <template #actions>
      <button class="btn" @click="emit('close')">取消</button>
      <button class="btn btn-primary" :disabled="!checked.size" @click="confirm">
        挂载 {{ checked.size || '' }}{{ checked.size ? ' 个任务' : '' }}
      </button>
    </template>
  </ModalShell>
</template>

<style scoped>
.tp-hint { font-size: 12px; color: var(--text-dim); margin-bottom: 10px; line-height: 1.6; }
.tp-hint code {
  font-family: var(--mono, monospace);
  background: var(--code-bg);
  border-radius: 3px;
  padding: 0 4px;
  word-break: break-all;
}
.tp-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 320px;
  overflow-y: auto;
}
.tp-empty { padding: 20px; text-align: center; font-size: 12px; color: var(--text-muted); }
.tp-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
}
.tp-item:hover { border-color: var(--text-dim); }
.tp-item.checked { border-color: var(--accent); background: var(--active, #e4e6f1); }
.tp-item.disabled { cursor: default; opacity: .6; }
.tp-item input { margin-top: 3px; accent-color: var(--accent); }
.tp-main { flex: 1; min-width: 0; }
.tp-title {
  font-size: 13px;
  color: var(--text);
  line-height: 1.5;
  word-break: break-word;
}
.tp-meta { display: flex; align-items: center; gap: 8px; margin-top: 3px; font-size: 11px; color: var(--text-dim); }
.tp-status { padding: 0 5px; border-radius: 3px; background: var(--code-bg); }
.tp-status.progress { color: #b35900; background: #fff4e1; }
.tp-linked-tag {
  flex-shrink: 0;
  font-size: 10px;
  color: var(--text-dim);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1px 6px;
}
.btn { padding: 6px 14px; border-radius: 4px; border: 1px solid var(--border); background: var(--bg); color: var(--text); cursor: pointer; font-size: 13px; }
.btn-primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.btn:disabled { opacity: .5; cursor: default; }
</style>
