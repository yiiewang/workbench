<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { activeTabPath as storeActiveTabPath, storeGetTab } from '../../stores/indexStore'
import { getTasksByFile, getOpenTasks, getTaskRevision, updateTask, type Task } from '../../api/tasks'
import { showToast } from '../../lib/common'
import FileViewer from './FileViewer.vue'
import TaskPickerModal from '../modal/TaskPickerModal.vue'

const tabInfo = computed(() => {
  const path = storeActiveTabPath.value
  if (!path) return null
  return storeGetTab(path) || null
})

// ============================================================
// 关联任务（todo ↔ file 联动）：当前文件被哪些任务引用
// ============================================================

const router = useRouter()
const relatedTasks = ref<Task[]>([])
const relatedLoading = ref(false)
const relatedOpen = ref(false)
// 记住用户在本次会话中的折叠偏好（有任务时是否展开）
const relatedAutoOpened = ref(false)

watch(storeActiveTabPath, async (path) => {
  relatedTasks.value = []
  if (!path) return
  relatedLoading.value = true
  try {
    const data = await getTasksByFile(path)
    relatedTasks.value = (data?.tasks || []) as Task[]
    // 首次发现关联任务时自动展开一次，之后跟随用户手动折叠的状态
    if (relatedTasks.value.length && !relatedAutoOpened.value) {
      relatedOpen.value = true
      relatedAutoOpened.value = true
    }
  } catch {
    relatedTasks.value = [] // 静默失败：面板只是辅助信息
  } finally {
    relatedLoading.value = false
  }
}, { immediate: true })

const statusLabel: Record<string, string> = { todo: '待办', progress: '进行中', done: '已完成' }

// 点击关联任务 → 跳转 todo 看板（TodoView 读 ?task= 自动打开编辑弹窗，关闭后回跳）
function gotoTask(task: Task) {
  router.push({ path: '/todo', query: { task: String(task.id) } })
}

// ============================================================
// 挂到任务：把当前文件加入已有任务的关联（反向建立关联）
// ============================================================

const showTaskPicker = ref(false)

async function refreshRelated() {
  const path = storeActiveTabPath.value
  if (!path) return
  try {
    const data = await getTasksByFile(path)
    relatedTasks.value = (data?.tasks || []) as Task[]
    relatedOpen.value = true
  } catch { /* 静默 */ }
}

// TaskPickerModal 确认后的落地：把当前文件追加到所选任务的 files（逐条 PATCH，乐观锁）
async function onPicked(taskIds: string[]) {
  const path = storeActiveTabPath.value
  if (!path || !taskIds.length) return
  const { tasks: openList } = await getOpenTasks()
  const targets = (openList || []).filter(t => taskIds.includes(String(t.id)))
  let ok = 0
  let revision = 0
  try {
    revision = (await getTaskRevision()).revision
  } catch { /* 拿不到版本号则放弃 */ }
  if (!revision) { showToast('关联失败，请重试'); return }
  for (const t of targets) {
    if ((t.files || []).includes(path)) { ok++; continue }
    try {
      const data: any = await updateTask({ ...t, files: [...(t.files || []), path] }, revision)
      if (typeof data?.revision === 'number') revision = data.revision
      ok++
    } catch (e) {
      console.error('[related] link task failed', e)
    }
  }
  showToast(ok ? `已关联到 ${ok} 个任务` : '关联失败，请重试')
  refreshRelated()
}

// markdown 内本地文件链接拦截 → hash 路由跳转
function onEditorClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a')
  if (!a) return
  // 下载链接（二进制/压缩包卡片）不是 markdown 内链，不能被下面的跳转逻辑 preventDefault 掉
  if (a.hasAttribute('download')) return
  const href = a.getAttribute('href')
  if (!href) return
  if (/^(https?:|mailto:|tel:|#|blob:|data:)/i.test(href)) return

  e.preventDefault()
  const [linkPath, linkAnchor] = href.split('#')
  const baseDir = storeActiveTabPath.value
    ? storeActiveTabPath.value.replace(/\/[^/]+$/, '')
    : ''
  let resolved = linkPath
  if (!linkPath.startsWith('/')) {
    const parts = (baseDir + '/' + linkPath).split('/')
    const stack: string[] = []
    for (const p of parts) {
      if (p === '' || p === '.') continue
      if (p === '..') stack.pop()
      else stack.push(p)
    }
    resolved = '/' + stack.join('/')
  }
  window.location.hash = '#' + resolved + (linkAnchor ? '#' + linkAnchor : '')
}
</script>

<template>
  <div class="editor-area" id="editorArea" @click="onEditorClick">
    <!-- 关联任务折叠条：当前文件被哪些 todo 任务引用 + 反向建立关联入口 -->
    <div v-if="relatedLoading || relatedTasks.length || tabInfo" class="related-tasks" :class="{ open: relatedOpen }">
      <button class="related-tasks-toggle" @click="relatedOpen = !relatedOpen">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>
        </svg>
        <span>关联任务</span>
        <span class="related-tasks-count">{{ relatedLoading ? '…' : relatedTasks.length }}</span>
        <!-- 挂到任务：把当前文件加入已有任务的关联（反向建立关联） -->
        <button
          class="rt-extract-btn"
          title="把当前文件挂到已有任务"
          @click.stop="showTaskPicker = true"
        >＋ 挂到任务</button>
        <svg class="chevron" :class="{ expanded: relatedOpen }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>
      </button>
      <div v-if="relatedOpen" class="related-tasks-list">
        <div
          v-for="t in relatedTasks"
          :key="t.id"
          class="related-task-item"
          :class="t.status"
          :title="t.title"
          @click="gotoTask(t)"
        >
          <span class="rt-status">{{ statusLabel[t.status] || t.status }}</span>
          <span class="rt-title">{{ t.title }}</span>
          <span v-if="t.due" class="rt-due">{{ t.due }}</span>
        </div>
        <div v-if="!relatedLoading && !relatedTasks.length" class="rt-empty">
          暂无关联任务，点击右上「挂到任务」建立关联
        </div>
      </div>
    </div>

    <FileViewer
      v-if="tabInfo"
      :path="storeActiveTabPath"
      :name="tabInfo.name"
      :ext="tabInfo.ext"
      :size="tabInfo.size"
    />
    <div v-else class="editor-empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6z"/><path d="M14 2v6h6"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/></svg>
      <span>Select a file from the Explorer to preview</span>
    </div>

    <!-- 挂到任务选择器（反向建立关联） -->
    <TaskPickerModal
      v-if="showTaskPicker"
      :file-path="storeActiveTabPath"
      :linked-ids="relatedTasks.map(t => String(t.id))"
      @confirm="onPicked"
      @close="showTaskPicker = false"
    />
  </div>
</template>

<style scoped>
/* 关联任务折叠条（todo ↔ file 联动） */
.related-tasks {
  flex-shrink: 0;
  border-bottom: 1px solid var(--border);
  background: var(--sidebar-bg);
  font-size: 12px;
}
.related-tasks-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: none;
  background: none;
  color: var(--text-dim);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: .5px;
  padding: 6px 12px;
  cursor: pointer;
}
.related-tasks-toggle:hover { color: var(--text); }
.related-tasks-toggle svg { width: 13px; height: 13px; }
.related-tasks-toggle .chevron { margin-left: auto; transition: transform .15s; }
.related-tasks-toggle .chevron.expanded { transform: rotate(180deg); }
.related-tasks-count {
  background: var(--code-bg);
  border-radius: 9px;
  padding: 0 7px;
  font-size: 10px;
  font-weight: 400;
  line-height: 1.6;
}
.related-tasks-list {
  padding: 0 12px 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.related-task-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  border-radius: 6px;
  cursor: pointer;
  background: var(--bg);
  border: 1px solid var(--border);
  transition: border-color .15s;
}
.related-task-item:hover { border-color: var(--accent); }
.rt-status {
  flex-shrink: 0;
  font-size: 10px;
  border-radius: 3px;
  padding: 1px 5px;
  background: var(--code-bg);
  color: var(--text-dim);
}
.related-task-item.progress .rt-status { color: #b35900; background: #fff4e1; }
.related-task-item.done .rt-status { color: #1a7f37; background: #dafbe1; }
.rt-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text);
}
.rt-due { flex-shrink: 0; font-size: 10px; color: var(--text-dim); }
.rt-empty { padding: 6px 8px; font-size: 11px; color: var(--text-muted); }

/* AI 提炼任务按钮 */
.rt-extract-btn {
  margin-left: 8px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg);
  color: var(--accent);
  font-size: 10px;
  font-weight: 500;
  text-transform: none;
  letter-spacing: 0;
  padding: 2px 8px;
  cursor: pointer;
  transition: border-color .15s;
}
.rt-extract-btn:hover { border-color: var(--accent); }
.rt-extract-btn:disabled { opacity: .6; cursor: default; }
</style>
