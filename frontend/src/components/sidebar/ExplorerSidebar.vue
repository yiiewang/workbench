<script setup lang="ts">
// Explorer sidebar: uses recursive TreeNode + reactive treeStore instead of DOM manipulation
//   - 浏览器模式（dsMode='browser'）：根节点是 /，侧栏展示完整文件系统
//   - 分享模式（dsMode='share'）：根节点是 shareRootPath（分享内容的实际路径），
//     侧栏只展示分享根范围内的内容，且 onMounted 不再触发 treeStore.loadDir('/')
//     （避免在 dsMode 切换前误用浏览器模式请求暴露完整目录）
import { onMounted, watch, computed } from 'vue'
import { useRoute } from 'vue-router'
import { activeTabPath, dsMode, shareRootPath } from '../../stores/indexStore'
import { treeStore } from '../../composables/useTreeStore'
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
</script>

<template>
  <TreeNode v-if="rootPath" :path="rootPath" :depth="0" />
</template>
