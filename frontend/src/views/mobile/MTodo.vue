<script setup lang="ts">
// 移动端 Todo：按状态分组的任务列表（友好展示 + 轻编辑：状态切换）。
// 四象限/甘特等复杂视图留在 PC；点击任务看详情（含关联文件，可跳移动预览）。
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { getTasks, getTaskDetail, updateTask, type Task } from '../../api/tasks'
import { currentUser } from '../../stores/auth'
import { showToast } from '../../lib/common'
import { renderMarkdown } from '../../lib/markdownRenderer'
import ModalShell from '../../components/ui/ModalShell.vue'

const router = useRouter()
const tasks = ref<Task[]>([])
const loading = ref(true)
const loadError = ref('')
const selected = ref<Task | null>(null)
// 服务端权威版本号（乐观锁基准）：加载时从 getTasks 响应取，写入后更新
let lastRevision = 0

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const data: any = await getTasks()
    // 响应结构：orgs.{orgId}.{userId} = {revision, tasks}
    const orgKey = String(currentUser.value?.orgId)
    const userKey = String(currentUser.value?.userId)
    const u = data?.orgs?.[orgKey]?.[userKey]
    tasks.value = (u?.tasks || []) as Task[]
    lastRevision = Number(u?.revision) || 0
  } catch (e: any) {
    loadError.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

// 按状态分组，组内按截止日升序（空日期在后）
const groups = computed(() => {
  const by = (s: string) => tasks.value
    .filter(t => t.status === s)
    .sort((a, b) => (a.due || '9999') < (b.due || '9999') ? -1 : 1)
  return [
    { key: 'progress', label: '进行中', color: '#b35900', list: by('progress') },
    { key: 'todo', label: '待办', color: '#888', list: by('todo') },
    { key: 'done', label: '已完成', color: '#1a7f37', list: by('done') },
  ]
})

const priorityLabel: Record<string, string> = { high: '高', medium: '中', low: '低' }

// 轻编辑：状态切换（乐观锁 PATCH，失败回滚；409 冲突重拉后提示）
async function setStatus(t: Task, status: string) {
  if (t.status === status) return
  const prev = t.status
  t.status = status
  if (status === 'done') t.progress = 100
  try {
    const data: any = await updateTask({ ...t, status }, lastRevision)
    if (typeof data?.revision === 'number') lastRevision = data.revision
    showToast('已更新')
  } catch (e: any) {
    t.status = prev
    if (e?.code === 40901) {
      showToast('任务被其他设备修改，已刷新')
      load()
    } else {
      showToast('更新失败')
    }
  }
}

// 任务内容 markdown 渲染（共享渲染层；样式复用全局 ai-md 类，见 ai-chat.css）
function renderContent(text: string): string {
  return renderMarkdown(text)
}

function toggleDone(t: Task) {
  setStatus(t, t.status === 'done' ? 'todo' : 'done')
}

// 打开任务详情：列表已瘦身（无 content 全文），拉详情后再展示；
// 拉取失败降级用列表数据（正文区不显示，其余字段可看可改）
async function selectTask(t: Task) {
  try {
    const data = await getTaskDetail(t.id)
    selected.value = data?.task || t
  } catch {
    selected.value = t
  }
}

// 关联文件 → 移动端预览
function openFile(path: string) {
  selected.value = null
  router.push({ path: '/m/file', query: { path } })
}
</script>

<template>
<div class="m-page">
  <header class="m-page-header">
    <span class="m-page-title">待办任务</span>
    <button class="mt-refresh" title="刷新" @click="load">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
    </button>
  </header>

  <div class="mt-body">
    <div v-if="loading" class="m-page-empty">加载中…</div>
    <div v-else-if="loadError" class="m-page-empty is-error">{{ loadError }}</div>
    <div v-else-if="!tasks.length" class="m-page-empty">暂无任务，去 PC 端创建吧</div>

    <section v-for="g in groups" v-else :key="g.key" class="mt-group">
      <div class="mt-group-head" :style="{ color: g.color }">
        <span>{{ g.label }}</span>
        <span class="mt-count">{{ g.list.length }}</span>
      </div>
      <div
        v-for="t in g.list"
        :key="t.id"
        class="mt-task"
        @click="selectTask(t)"
      >
        <button class="mt-check" :class="{ done: t.status === 'done' }" @click.stop="toggleDone(t)">
          <svg v-if="t.status === 'done'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5"/></svg>
        </button>
        <div class="mt-main">
          <div class="mt-name" :class="{ done: t.status === 'done' }">{{ t.title }}</div>
          <div class="mt-meta">
            <span class="mt-pri" :class="t.priority">{{ priorityLabel[t.priority] || t.priority }}</span>
            <span v-if="t.due" class="mt-due">截止 {{ t.due }}</span>
            <span v-if="(t.files || []).length" class="mt-files">📎 {{ (t.files || []).length }}</span>
          </div>
        </div>
        <svg class="mt-arrow" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg>
      </div>
    </section>
  </div>

  <!-- 任务详情 -->
  <ModalShell v-if="selected" :title="selected.title" width="420px" @close="selected = null">
    <div class="mt-detail-status">
      <button
        v-for="s in ['todo', 'progress', 'done']"
        :key="s"
        class="mt-status-btn"
        :class="[s, { active: selected.status === s }]"
        @click="setStatus(selected, s)"
      >{{ s === 'todo' ? '待办' : s === 'progress' ? '进行中' : '已完成' }}</button>
    </div>
    <div v-if="selected.content" class="ai-md mt-detail-content" v-html="renderContent(selected.content)"></div>
    <div class="mt-detail-meta">
      <div class="mt-detail-row"><span>优先级</span><b>{{ priorityLabel[selected.priority] || selected.priority }}</b></div>
      <div class="mt-detail-row"><span>计划</span><b>{{ selected.scheduled || '—' }}</b></div>
      <div class="mt-detail-row"><span>截止</span><b>{{ selected.due || '—' }}</b></div>
    </div>
    <div v-if="(selected.files || []).length" class="mt-detail-files">
      <div class="mt-detail-files-title">关联文件</div>
      <span
        v-for="f in selected.files"
        :key="f"
        class="mt-file-chip"
        :title="f"
        @click="openFile(f)"
      >{{ f.split('/').pop() || f }}</span>
    </div>
  </ModalShell>
</div>
</template>
<style scoped>

.mt-body { flex: 1; min-height: 0; overflow-y: auto; padding: 12px 16px; }
.is-error { color: #cf222e; }

.mt-refresh {
  display: flex;
  border: none;
  background: none;
  color: var(--text-dim);
  cursor: pointer;
  padding: 4px;
}
.mt-refresh svg { width: 18px; height: 18px; }

.mt-group { margin-bottom: 18px; }
.mt-group-head {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 8px;
}
.mt-count {
  font-size: 11px;
  font-weight: 400;
  background: var(--code-bg);
  border-radius: 9px;
  padding: 0 7px;
  color: var(--text-dim);
}

.mt-task {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg);
  margin-bottom: 8px;
  cursor: pointer;
}
.mt-task:active { border-color: var(--accent); }

.mt-check {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  border: 2px solid var(--border);
  background: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: #fff;
}
.mt-check.done { background: #1a7f37; border-color: #1a7f37; }
.mt-check svg { width: 13px; height: 13px; }

.mt-main { flex: 1; min-width: 0; }
.mt-name {
  font-size: 15px;
  color: var(--text);
  line-height: 1.4;
  word-break: break-word;
}
.mt-name.done { text-decoration: line-through; color: var(--text-dim); }
.mt-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 4px;
  font-size: 11px;
  color: var(--text-dim);
}
.mt-pri { padding: 0 5px; border-radius: 3px; background: var(--code-bg); }
.mt-pri.high { color: #cf222e; background: #fde8ea; }
.mt-arrow { width: 16px; height: 16px; color: var(--text-muted); flex-shrink: 0; }

/* 详情弹窗内容 */
.mt-detail-status { display: flex; gap: 8px; margin-bottom: 12px; }
.mt-status-btn {
  flex: 1;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg);
  color: var(--text-dim);
  font-size: 13px;
  padding: 8px 0;
  cursor: pointer;
}
.mt-status-btn.active.todo { border-color: #888; color: #555; background: var(--code-bg); }
.mt-status-btn.active.progress { border-color: #b35900; color: #b35900; background: #fff4e1; }
.mt-status-btn.active.done { border-color: #1a7f37; color: #1a7f37; background: #dafbe1; }

.mt-detail-content {
  font-size: 13px;
  color: var(--text);
  line-height: 1.6;
  word-break: break-word;
  background: var(--code-bg);
  border-radius: 8px;
  padding: 12px;
  margin-bottom: 12px;
}
.mt-detail-meta {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 4px 12px;
  margin-bottom: 12px;
}
.mt-detail-row {
  display: flex;
  justify-content: space-between;
  padding: 7px 0;
  font-size: 13px;
  color: var(--text-dim);
  border-bottom: 1px solid var(--border);
}
.mt-detail-row:last-child { border-bottom: none; }
.mt-detail-row b { color: var(--text); font-weight: 500; }

.mt-detail-files-title { font-size: 12px; color: var(--text-dim); margin-bottom: 6px; }
.mt-file-chip {
  display: inline-flex;
  align-items: center;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--code-bg);
  color: var(--accent);
  font-size: 12px;
  padding: 4px 10px;
  margin: 0 6px 6px 0;
  cursor: pointer;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mt-file-chip:hover { border-color: var(--accent); }
</style>
