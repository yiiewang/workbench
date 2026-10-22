<script setup lang="ts">
// 文件/目录右键菜单：新建文件夹 / 分享 / 重命名 / 移动（剪贴模式）/ 删除。
// 全局单例：状态在 useContextMenu，本组件挂在 DefaultLayout；仅浏览器模式触发（TreeNode 分享模式 return）。
import { computed, ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { contextMenu, closeFileContextMenu } from '../../composables/useContextMenu'
import { treeStore } from '../../composables/useTreeStore'
import { activeTabPath, openTabs, storeRemoveTab, storeSetActiveTab, openTabFn } from '../../stores/indexStore'
import { showToast, baseName, extFromPath } from '../../lib/common'
import { openShareModal } from '../../lib/shareModal'
import { openPrompt } from '../../lib/promptModal'
import * as fileApi from '../../api/files'

const cm = contextMenu
const target = computed(() => cm.target)

// ---- 菜单项 ----
const clip = computed(() => treeStore.clipboard.value)

// "移动到此处"仅出现在目录上，且剪贴源不能是自身/自身子孙目录（后端还有最终校验）
const canMoveHere = computed(() => {
  const t = cm.target
  if (!t || !t.isDir) return false
  const c = clip.value
  if (!c) return false
  if (c.path === t.path) return false
  if (t.path.startsWith(c.path + '/')) return false
  return true
})

const items = computed(() => {
  const t = cm.target
  if (!t) return []
  const list: { key: string; label: string; danger?: boolean }[] = []
  if (t.isDir) {
    list.push({ key: 'mkdir', label: 'New folder' })
    if (canMoveHere.value) list.push({ key: 'movehere', label: 'Move here' })
    if (clip.value) list.push({ key: 'cancelmove', label: 'Cancel move' })
    // 根目录：仅支持新建/移入，不可分享、改名、移动、删除（后端对根的写操作也有拒绝保护）
    if (t.path === '/') return list
  }
  list.push({ key: 'share', label: 'Share' })
  list.push({ key: 'rename', label: 'Rename' })
  if (!clip.value || clip.value.path !== t.path) list.push({ key: 'move', label: 'Move' })
  list.push({ key: 'delete', label: 'Delete', danger: true })
  return list
})

// ---- 定位（弹出后按实际尺寸夹住，防溢出屏幕）----
const rootRef = ref<HTMLElement | null>(null)
const pos = ref({ x: 0, y: 0 })
watch(() => cm.visible, async v => {
  if (!v) return
  await nextTick()
  const el = rootRef.value
  const rect = el?.getBoundingClientRect()
  const w = rect?.width || 170
  const h = rect?.height || 200
  pos.value = {
    x: Math.max(8, Math.min(cm.x, window.innerWidth - w - 8)),
    y: Math.max(8, Math.min(cm.y, window.innerHeight - h - 8)),
  }
})

// ---- 工具 ----
function parentOf(p: string): string {
  const m = p.replace(/\/[^/]+$/, '')
  return m || '/'
}

/** 关闭路径及全部子孙路径上的 tab（删除场景）；关掉激活 tab 时按 TabBar 惯例激活剩余最后一个。 */
function closeTabsFor(prefix: string) {
  const p = (prefix !== '/' && prefix.endsWith('/')) ? prefix.slice(0, -1) : prefix
  for (const path of [...openTabs.keys()]) {
    if (path === p || path.startsWith(p + '/')) storeRemoveTab(path)
  }
  if (activeTabPath.value && !openTabs.has(activeTabPath.value)) {
    const remaining = [...openTabs.keys()]
    storeSetActiveTab(remaining.length ? remaining[remaining.length - 1] : '')
  }
}

/** 旧路径 tab 迁移到新路径（重命名/移动场景），保持 tab 打开状态与激活态。 */
function relocateTab(oldPath: string, newPath: string, newName: string) {
  const info = openTabs.get(oldPath)
  const wasActive = activeTabPath.value === oldPath
  if (info) storeRemoveTab(oldPath)
  if (info && openTabFn) openTabFn(newPath, newName, extFromPath(newName), info.size || 0)
  if (wasActive) storeSetActiveTab(info ? newPath : '')
}

// ---- 操作 ----
function onItem(key: string) {
  const t = target.value
  if (!t) return
  closeFileContextMenu()
  switch (key) {
    case 'mkdir': onMkdir(t.path); break
    case 'share': onShare(t); break
    case 'rename': onRename(t); break
    case 'move': onMoveMark(t.path); break
    case 'movehere': onMoveHere(t.path); break
    case 'cancelmove': treeStore.clipboard.value = null; showToast('Move cancelled'); break
    case 'delete': onDelete(t); break
  }
}

function onMkdir(dirPath: string) {
  openPrompt({ title: 'New folder' }, async name => {
    try {
      const base = dirPath === '/' ? '' : dirPath
      const data = await fileApi.createDir(base + '/' + name)
      // 新建后展开父目录显示新文件夹（refreshDir 只对已展开目录重载）
      treeStore.expanded.add(dirPath)
      await treeStore.refreshDir(dirPath)
      showToast(data.hidden ? `Created, but hidden by filter: ${name}` : `Folder created: ${name}`)
    } catch (err: any) {
      showToast('Create failed: ' + (err.msg || err.message))
    }
  })
}

function onShare(t: { path: string; isDir: boolean }) {
  // ShareModal 从 activeTabPath 读取资源路径（沿用原 TreeNode 右键的适配方式）
  activeTabPath.value = t.path
  openShareModal(t.isDir ? 'dir' : 'file')
}

function onRename(t: { path: string; name: string }) {
  openPrompt({ title: 'Rename', initial: t.name }, async name => {
    if (name === t.name) return
    try {
      const data = await fileApi.renamePath(t.path, name)
      await treeStore.refreshDir(parentOf(t.path))
      relocateTab(t.path, data.path, name)
      showToast(data.hidden ? `Renamed, but hidden by filter: ${name}` : `Renamed to ${name}`)
    } catch (err: any) {
      showToast('Rename failed: ' + (err.msg || err.message))
    }
  })
}

function onMoveMark(path: string) {
  treeStore.clipboard.value = { path }
  showToast('Marked. Right-click the target folder and choose "Move here"')
}

async function onMoveHere(destDir: string) {
  const c = clip.value
  if (!c) return
  try {
    const data = await fileApi.movePath(c.path, destDir)
    treeStore.clipboard.value = null
    await treeStore.refreshDir(parentOf(c.path))
    await treeStore.refreshDir(destDir)
    treeStore.pruneCache(c.path)
    relocateTab(c.path, data.path, baseName(c.path))
    showToast('Moved')
  } catch (err: any) {
    showToast('Move failed: ' + (err.msg || err.message))
  }
}

async function onDelete(t: { path: string; name: string; isDir: boolean }) {
  const msg = t.isDir
    ? `Delete folder "${t.name}" and ALL its contents? This cannot be undone.`
    : `Delete file "${t.name}"?`
  if (!confirm(msg)) return
  try {
    await fileApi.deletePath(t.path)
    await treeStore.refreshDir(parentOf(t.path))
    treeStore.pruneCache(t.path)
    closeTabsFor(t.path)
    showToast('Deleted')
  } catch (err: any) {
    showToast('Delete failed: ' + (err.msg || err.message))
  }
}

// ---- 全局关闭：点击外部 / ESC / 滚动 / 窗口变化 ----
function onGlobalMouseDown(e: MouseEvent) {
  if (!cm.visible) return
  if (rootRef.value && !rootRef.value.contains(e.target as Node)) closeFileContextMenu()
}
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') closeFileContextMenu()
}
onMounted(() => {
  document.addEventListener('mousedown', onGlobalMouseDown)
  document.addEventListener('keydown', onKeydown)
  window.addEventListener('resize', closeFileContextMenu)
  window.addEventListener('scroll', closeFileContextMenu, true)
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onGlobalMouseDown)
  document.removeEventListener('keydown', onKeydown)
  window.removeEventListener('resize', closeFileContextMenu)
  window.removeEventListener('scroll', closeFileContextMenu, true)
})
</script>

<template>
  <div
    v-if="cm.visible && target"
    ref="rootRef"
    class="ctx-menu"
    :style="{ left: pos.x + 'px', top: pos.y + 'px' }"
    @contextmenu.prevent.stop
  >
    <div
      v-for="it in items"
      :key="it.key"
      class="ctx-item"
      :class="{ danger: it.danger }"
      @click="onItem(it.key)"
    >{{ it.label }}</div>
  </div>
</template>

<style scoped>
.ctx-menu {
  position: fixed;
  z-index: 3000;
  min-width: 170px;
  padding: 4px 0;
  background: var(--bg, #fff);
  border: 1px solid var(--border, #ddd);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, .18);
}
.ctx-item {
  padding: 6px 14px;
  font-size: 13px;
  color: var(--text, #333);
  cursor: pointer;
  white-space: nowrap;
}
.ctx-item:hover { background: var(--hover, #f2f3f5); }
.ctx-item.danger { color: #d00; }
.ctx-item.danger:hover { background: #fde8ea; }
</style>
