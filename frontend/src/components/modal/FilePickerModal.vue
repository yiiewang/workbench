<script setup lang="ts">
// 通用文件选择器弹窗：懒加载目录树（同 ExplorerSidebar 的 /api/tree 接口逻辑，
// 但状态自管理——不触碰全局 treeStore，避免与文件浏览器侧栏互相污染）。
// 支持多选文件、上传到当前目录（上传成功后自动选中），供任务关联文件等场景复用。
import { computed, reactive, ref, onMounted } from 'vue'
import ModalShell from '../ui/ModalShell.vue'
import { getTree, uploadFiles } from '../../api/files'
import { showToast } from '../../lib/common'

interface PickerItem {
  name: string
  isDir: boolean
  size?: number
}

const props = withDefaults(defineProps<{
  title?: string
  /** 多选（默认）；单选时点击文件即替换选中 */
  multiple?: boolean
  /** 初始选中路径 */
  initialSelected?: string[]
}>(), {
  title: '选择文件',
  multiple: true,
  initialSelected: () => [],
})

const emit = defineEmits<{ close: []; confirm: [paths: string[]] }>()

// ---- 树状态（懒加载） ----
const children = reactive(new Map<string, PickerItem[]>())
const expanded = reactive(new Set<string>(['/']))
const loading = reactive(new Set<string>())
// 当前目录（上传目标）：最近展开的目录，初始根
const currentDir = ref('/')

const selected = reactive(new Set<string>(props.initialSelected))

async function loadDir(path: string): Promise<void> {
  if (children.has(path) || loading.has(path)) return
  loading.add(path)
  try {
    const data = await getTree(path)
    children.set(path, [...(data.dirs || []), ...(data.files || [])] as PickerItem[])
  } catch (e) {
    showToast('目录加载失败')
    children.set(path, []) // 失败置空防止重入死循环
  } finally {
    loading.delete(path)
  }
}

onMounted(() => { loadDir('/') })

function childPath(dir: string, name: string): string {
  return dir === '/' ? '/' + name : dir + '/' + name
}

async function onRowClick(item: PickerItem, path: string) {
  if (item.isDir) {
    if (expanded.has(path)) {
      expanded.delete(path)
    } else {
      await loadDir(path)
      expanded.add(path)
      currentDir.value = path
    }
    return
  }
  // 文件：多选 toggle / 单选替换
  if (props.multiple) {
    if (selected.has(path)) selected.delete(path)
    else selected.add(path)
  } else {
    selected.clear()
    selected.add(path)
  }
}

// ---- 扁平渲染行（DFS 展开，免递归组件） ----
interface Row {
  item: PickerItem
  path: string
  depth: number
}

const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const walk = (dir: string, depth: number) => {
    const items = children.get(dir)
    if (!items) return
    for (const it of items) {
      const p = childPath(dir, it.name)
      out.push({ item: it, path: p, depth })
      if (it.isDir && expanded.has(p)) walk(p, depth + 1)
    }
  }
  walk('/', 0)
  return out
})

const selectedList = computed(() => [...selected])

// ---- 上传到当前目录 ----
const uploading = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

function triggerUpload() {
  fileInput.value?.click()
}

async function onFilesChosen(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = '' // 允许重复选择同一文件
  if (!files.length) return
  uploading.value = true
  try {
    const data: any = await uploadFiles(currentDir.value, files)
    const uploaded: string[] = (data?.uploaded || []).map((u: any) => u.name)
    // 刷新当前目录并自动选中新上传的文件
    children.delete(currentDir.value)
    await loadDir(currentDir.value)
    for (const name of uploaded) {
      selected.add(childPath(currentDir.value, name))
    }
    const skipped = (data?.skipped || []).length
    showToast(uploaded.length
      ? `已上传 ${uploaded.length} 个文件${skipped ? `，跳过 ${skipped} 个` : ''}`
      : '上传失败或全部跳过')
  } catch (err: any) {
    showToast('上传失败：' + (err?.msg || err?.message || ''))
  } finally {
    uploading.value = false
  }
}

function confirm() {
  if (!selected.size) {
    showToast('请先选择文件')
    return
  }
  emit('confirm', [...selected])
}

// 简版图标（不依赖 ExplorerSidebar 的图标集）
const FOLDER_ICON = '<svg viewBox="0 0 16 16" width="14" height="14"><path d="M1.5 3h4l1 1h7v9h-12V3z" fill="currentColor"/></svg>'
const FILE_ICON = '<svg viewBox="0 0 16 16" width="14" height="14"><path d="M3 1h6l5 5v9H3V1z" fill="none" stroke="currentColor" stroke-width="1.2"/><path d="M9 1v5h5" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>'
</script>

<template>
  <ModalShell :title="title" width="480px" @close="emit('close')">
    <!-- 工具行：当前目录 + 上传 -->
    <div class="fp-toolbar">
      <span class="fp-cwd" :title="currentDir">📁 {{ currentDir }}</span>
      <button class="fp-upload-btn" :disabled="uploading" @click="triggerUpload">
        {{ uploading ? '上传中…' : '⬆ 上传到当前目录' }}
      </button>
      <input
        ref="fileInput"
        type="file"
        multiple
        style="display: none"
        @change="onFilesChosen"
      >
    </div>

    <!-- 目录树（懒加载，扁平渲染） -->
    <div class="fp-tree">
      <div v-if="!rows.length" class="fp-empty">加载中…</div>
      <div
        v-for="row in rows"
        :key="row.path"
        class="fp-row"
        :class="{ dir: row.item.isDir, selected: !row.item.isDir && selected.has(row.path) }"
        :style="{ paddingLeft: 8 + row.depth * 16 + 'px' }"
        :title="row.path"
        @click="onRowClick(row.item, row.path)"
      >
        <span class="fp-icon" v-html="row.item.isDir ? FOLDER_ICON : FILE_ICON"></span>
        <span class="fp-name">{{ row.item.name }}</span>
        <span v-if="!row.item.isDir && selected.has(row.path)" class="fp-check">✓</span>
      </div>
    </div>

    <template #actions>
      <span class="fp-count">已选 {{ selectedList.length }} 个</span>
      <button class="btn" @click="emit('close')">取消</button>
      <button class="btn btn-primary" :disabled="!selectedList.length" @click="confirm">确定</button>
    </template>
  </ModalShell>
</template>

<style scoped>
.fp-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}
.fp-cwd {
  font-size: 11px;
  color: var(--text-dim);
  font-family: var(--mono, monospace);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.fp-upload-btn {
  flex-shrink: 0;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg);
  color: var(--accent);
  font-size: 11px;
  padding: 3px 10px;
  cursor: pointer;
}
.fp-upload-btn:hover { border-color: var(--accent); }
.fp-upload-btn:disabled { opacity: .6; cursor: default; }

.fp-tree {
  max-height: 320px;
  overflow-y: auto;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 4px 0;
}
.fp-empty { padding: 20px; text-align: center; font-size: 12px; color: var(--text-muted); }
.fp-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px 3px 8px;
  font-size: 12px;
  cursor: pointer;
  color: var(--text);
}
.fp-row:hover { background: var(--hover); }
.fp-row.dir { color: var(--text-dim); font-weight: 500; }
.fp-row.dir:hover { color: var(--text); }
.fp-row.selected { background: var(--active, #e4e6f1); }
.fp-icon { display: flex; flex-shrink: 0; color: var(--text-dim); }
.fp-row.dir .fp-icon { color: var(--dir-color, #dcb67a); }
.fp-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fp-check { flex-shrink: 0; color: var(--accent); font-size: 12px; font-weight: 700; }

.fp-count { margin-right: auto; font-size: 11px; color: var(--text-dim); }
.btn { padding: 6px 14px; border-radius: 4px; border: 1px solid var(--border); background: var(--bg); color: var(--text); cursor: pointer; font-size: 13px; }
.btn-primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.btn:disabled { opacity: .5; cursor: default; }

/* 移动断点（≤900px，弹窗已全屏）：触屏热区加大、树占满可用高度 */
@media (max-width: 900px) {
  .fp-toolbar { margin-bottom: 10px; }
  .fp-cwd { font-size: 12px; }
  .fp-upload-btn { padding: 6px 12px; font-size: 12px; border-radius: 6px; }
  .fp-tree {
    max-height: calc(100dvh - 300px); /* 头部/工具行/操作行之外的可用高度 */
    border-radius: 8px;
    padding: 2px 0;
  }
  .fp-row {
    padding: 11px 12px;
    font-size: 14px;
    gap: 10px;
  }
  .fp-check { font-size: 14px; }
}
</style>
