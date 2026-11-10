<script setup lang="ts">
// 任务级冲突对比卡（乐观锁 409）：本地修改 vs 服务端版本并排展示，
// 差异字段逐项选择保留哪边，一次弹窗解决——替代旧版三层冲突弹窗链。
import { reactive, computed } from 'vue'
import ModalShell from '../ui/ModalShell.vue'

const props = defineProps<{
  /** useTodoApp 的 conflictCard：{ localTask, serverTask, changedFields, serverRevision } */
  card: {
    localTask: any
    serverTask: any
    changedFields: string[]
    serverRevision: number
  }
}>()

const emit = defineEmits<{
  resolve: [choice: Record<string, 'local' | 'server'>]
  discard: []
}>()

const FIELD_LABELS: Record<string, string> = {
  title: '标题', content: '内容', status: '状态', priority: '优先级',
  progress: '进度', due: '截止', scheduled: '计划', assignee: '负责人',
  postponedCount: '延期次数', confirmedDate: '确认日期', files: '关联文件',
  autoPostponed: '自动延期', sortOrder: '排序',
}
const STATUS_LABELS: Record<string, string> = { todo: '待办', progress: '进行中', done: '已完成' }

const choice = reactive<Record<string, 'local' | 'server'>>(
  Object.fromEntries(props.card.changedFields.map(f => [f, 'local']))
)

const fields = computed(() => {
  const fs = [...props.card.changedFields]
  if (!fs.includes('title')) fs.unshift('title')
  return fs
})

function fmt(f: string, v: any): string {
  if (f === 'status') return STATUS_LABELS[v] || v || '—'
  if (f === 'files') return (v || []).length ? (v as string[]).map(x => x.split('/').pop()).join('、') : '—'
  if (v === '' || v == null) return '—'
  return String(v)
}

function isDiff(f: string): boolean {
  return props.card.changedFields.includes(f)
}

function submit() {
  emit('resolve', { ...choice })
}
</script>

<template>
  <ModalShell title="任务冲突" width="520px" @close="emit('discard')">
    <div class="tcc-hint">
      该任务在其他设备上被修改过。请逐字段选择要保留的版本：
    </div>

    <div class="tcc-table">
      <div class="tcc-row tcc-head">
        <span class="tcc-field">字段</span>
        <span class="tcc-col">我的修改</span>
        <span class="tcc-col">服务器版本</span>
        <span class="tcc-op">保留</span>
      </div>
      <div v-for="f in fields" :key="f" class="tcc-row" :class="{ diff: isDiff(f) }">
        <span class="tcc-field">{{ FIELD_LABELS[f] || f }}</span>
        <span class="tcc-col" :class="{ sel: isDiff(f) && choice[f] === 'local' }">{{ fmt(f, card.localTask[f]) }}</span>
        <span class="tcc-col" :class="{ sel: isDiff(f) && choice[f] === 'server' }">{{ fmt(f, card.serverTask[f]) }}</span>
        <span class="tcc-op">
          <template v-if="isDiff(f)">
            <label class="tcc-radio"><input type="radio" :name="f" value="local" v-model="choice[f]">我的</label>
            <label class="tcc-radio"><input type="radio" :name="f" value="server" v-model="choice[f]">服务器</label>
          </template>
          <span v-else class="tcc-same">一致</span>
        </span>
      </div>
    </div>

    <template #actions>
      <button class="tcc-btn" @click="emit('discard')">全部采用服务器</button>
      <button class="tcc-btn tcc-primary" @click="submit">按选择合并</button>
    </template>
  </ModalShell>
</template>

<style scoped>
.tcc-hint {
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.6;
  margin-bottom: 12px;
}
.tcc-table {
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.tcc-row {
  display: grid;
  grid-template-columns: 90px 1fr 1fr 130px;
  align-items: center;
  font-size: 13px;
  border-bottom: 1px solid var(--border);
  min-height: 40px;
}
.tcc-row:last-child { border-bottom: none; }
.tcc-head {
  background: var(--sidebar-bg);
  font-size: 11px;
  font-weight: 600;
  color: var(--text-dim);
  min-height: 32px;
}
.tcc-row.diff { background: #fff8e6; }
.tcc-field { padding: 0 10px; color: var(--text-dim); }
.tcc-col {
  padding: 6px 8px;
  color: var(--text);
  word-break: break-word;
  max-height: 120px;
  overflow-y: auto;
}
.tcc-col.sel {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
  border-radius: 4px;
  font-weight: 500;
}
.tcc-op {
  display: flex;
  gap: 8px;
  padding: 0 8px;
}
.tcc-radio {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 12px;
  cursor: pointer;
  color: var(--text-dim);
}
.tcc-radio input { accent-color: var(--accent); }
.tcc-same { font-size: 12px; color: var(--text-muted); }
.tcc-btn {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  color: var(--text);
  font-size: 13px;
  padding: 7px 16px;
  cursor: pointer;
}
.tcc-btn.tcc-primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
</style>
