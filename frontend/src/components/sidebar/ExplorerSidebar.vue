<script setup lang="ts">
// Explorer sidebar: uses recursive TreeNode + reactive treeStore instead of DOM manipulation
//   - 浏览器模式（dsMode='browser'）：根节点是 /，侧栏展示完整文件系统
//   - 分享模式（dsMode='share'）：根节点是 shareRootPath（分享内容的实际路径），
//     侧栏只展示分享根范围内的内容，且 onMounted 不再触发 treeStore.loadDir('/')
//     （避免在 dsMode 切换前误用浏览器模式请求暴露完整目录）
import { onMounted, watch, computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { activeTabPath, authToken, dsMode, shareRootPath } from '../../stores/indexStore'
import { treeStore } from '../../composables/useTreeStore'
import { openFileContextMenu } from '../../composables/useContextMenu'
import { showToast } from '../../lib/common'
import * as fileApi from '../../api/files'
import TreeNode from './TreeNode.vue'

// 浏览器模式：挂载时按需加载根目录；分享模式由 useIndexApp 在 initApp 中加载。
// 注意：不能用 dsMode==='share' 判断——ExplorerSidebar 是 IndexView 的兄弟子组件，
// 挂载先于 IndexView.onMounted（Ms 里才设置 dsMode='share'），会误触发 loadDir('/')
// 导致未登录访问分享页时 /api/tree 401 全局跳登录。改用路由判断，组件 setup 时即可读。
const route = useRoute()
onMounted(async () => {
  if (route.path.startsWith('/s/')) return
  if (!treeStore.dirCache.has('/')) {
    try {
      await treeStore.loadDir('/')
    } catch { /* auth not ready yet, will be handled by useIndexApp init */ }
  }
})

// 浏览器模式：切换 tab 时高亮对应文件；分享模式由 useIndexApp 处理高亮
watch(activeTabPath, (newPath) => {
  if (dsMode.value === 'share') return
  if (newPath && newPath !== treeStore.activePath.value) {
    treeStore.expandTo(newPath).catch(() => {})
  }
})

// 侧栏根路径：分享模式用 shareRootPath（可能为空 → 暂不渲染），浏览器模式用 /
const rootPath = computed<string | null>(() => {
  if (dsMode.value === 'share') {
    return shareRootPath.value || null
  }
  return '/'
})

// ---- 上传（仅浏览器模式）----
// 分享模式后端无写端点；ExplorerSidebar 挂载早于 dsMode 设置（见顶部注释），
// 用 dsMode + authToken 双条件把工具栏限制在已登录的浏览器模式。
const showToolbar = computed(() => dsMode.value === 'browser' && !!authToken.value)
const uploading = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

// 上传目标目录：选中目录 → 自身；选中文件 → 父目录；未选中 → 根
function uploadTargetDir(): string {
  const ap = treeStore.activePath.value
  if (!ap) return '/'
  if (treeStore.dirCache.has(ap)) return ap
  const parent = ap.replace(/\/[^/]+$/, '')
  return parent || '/'
}

function onUploadClick() {
  if (uploading.value) return
  fileInput.value?.click()
}

async function onFilesPicked(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = '' // 清空以允许重复选择同一文件
  if (!files.length) return
  const dir = uploadTargetDir()
  uploading.value = true
  try {
    const data = await fileApi.uploadFiles(dir, files)
    const uploaded = data.uploaded || []
    const skipped = data.skipped || []
    const hidden = data.hiddenNames || []
    if (!uploaded.length && !skipped.length) {
      showToast('No file uploaded')
      return
    }
    let msg = `Uploaded ${uploaded.length} file(s) to ${dir}`
    if (skipped.length) msg += `, skipped ${skipped.length}`
    if (hidden.length) msg += `; hidden by filter: ${hidden.join(', ')}`
    showToast(msg)
    await treeStore.refreshDir(dir)
  } catch (err: any) {
    showToast('Upload failed: ' + (err.msg || err.message))
  } finally {
    uploading.value = false
  }
}

// ---- 根目录行：树本体只渲染子项，根目录无行则无法右键（移动到根/在根新建均无入口） ----
const FOLDER_ICON = '<svg viewBox="0 0 16 16" width="14" height="14"><path d="M1.5 3h4l1 1h7v9h-12V3z" fill="currentColor"/></svg>'

function onRootRowClick() {
  treeStore.activePath.value = '/'
}

function onRootContextmenu(e: MouseEvent) {
  treeStore.activePath.value = '/'
  openFileContextMenu('/', '/', true, e.clientX, e.clientY)
}
</script>

<template>
  <div v-if="rootPath && showToolbar" class="explorer-toolbar">
    <button class="btn btn-sm" :disabled="uploading" @click="onUploadClick" :title="`Upload to ${uploadTargetDir()}`">
      {{ uploading ? 'Uploading…' : 'Upload' }}
    </button>
    <input ref="fileInput" type="file" multiple hidden @change="onFilesPicked" />
  </div>
  <template v-if="rootPath && showToolbar">
    <!-- 根目录行：提供右键入口（移动到根 / 在根目录新建文件夹） -->
    <div
      class="tree-row explorer-root"
      :class="{ active: treeStore.activePath.value === '/' }"
      @click="onRootRowClick"
      @contextmenu.prevent="onRootContextmenu"
    >
      <span class="tree-icon dir" v-html="FOLDER_ICON"></span>
      <span class="tree-name" title="/">/</span>
    </div>
  </template>
  <TreeNode v-if="rootPath" :path="rootPath" :depth="0" />
</template>

<style scoped>
.explorer-toolbar { display: flex; justify-content: flex-end; padding: 6px 8px 2px; }
.explorer-root { padding-left: 6px; }
</style>
