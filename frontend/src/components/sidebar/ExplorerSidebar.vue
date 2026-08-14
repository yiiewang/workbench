<script setup lang="ts">
// Explorer sidebar: uses recursive TreeNode + reactive treeStore instead of DOM manipulation
//   - 浏览器模式（dsMode='browser'）：根节点是 /，侧栏展示完整文件系统
//   - 分享模式（dsMode='share'）：根节点是 shareRootPath（分享内容的实际路径），
//     侧栏只展示分享根范围内的内容，且 onMounted 不再触发 treeStore.loadDir('/')
//     （避免在 dsMode 切换前误用浏览器模式请求暴露完整目录）
import { onMounted, watch, computed } from 'vue'
import { activeTabPath, dsMode, shareRootPath } from '../../stores/indexStore'
import { treeStore } from '../../composables/useTreeStore'
import TreeNode from './TreeNode.vue'

// 浏览器模式：挂载时按需加载根目录；分享模式由 useIndexApp 在 initApp 中加载
onMounted(async () => {
  if (dsMode.value === 'share') return
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

// 侧栏 header：分享模式显示 "SHARED: {分享根的 basename}"，浏览器模式显示 "EXPLORER"
const headerText = computed(() => {
  if (dsMode.value !== 'share') return 'EXPLORER'
  const root = shareRootPath.value
  if (!root || root === '/') return 'SHARED'
  const basename = root.split('/').filter(Boolean).pop() || root
  return 'SHARED: ' + basename
})
</script>

<template>
  <div class="tree-panel active" id="explorerPanel">
    <div class="sidebar-header">{{ headerText }}</div>
    <div class="file-tree" id="tree">
      <TreeNode v-if="rootPath" :path="rootPath" :depth="0" />
    </div>
  </div>
</template>
