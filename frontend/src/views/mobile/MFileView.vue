<script setup lang="ts">
// 移动端文件预览：复用 PC 端 FileViewer（markdown/代码/媒体/图片全类型预览），
// 加返回栏；markdown 内链拦截后仍走移动预览路由。
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { baseName, extFromPath } from '../../lib/common'
import FileViewer from '../../components/editor/FileViewer.vue'

const route = useRoute()
const router = useRouter()

const filePath = computed(() => {
  const p = String(route.query.path || '')
  return '/' + p.replace(/^\/+/, '')
})
const fileName = computed(() => baseName(filePath.value))
const fileExt = computed(() => extFromPath(fileName.value))

// markdown 内本地文件链接拦截 → 移动端预览路由（同 EditorArea 逻辑的移动版）
function onContentClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a')
  if (!a) return
  if (a.hasAttribute('download')) return
  const href = a.getAttribute('href')
  if (!href) return
  if (/^(https?:|mailto:|tel:|#|blob:|data:)/i.test(href)) return

  e.preventDefault()
  const [linkPath, linkAnchor] = href.split('#')
  const baseDir = filePath.value.replace(/\/[^/]+$/, '')
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
  router.push({ path: '/m/file', query: { path: resolved }, hash: linkAnchor ? '#' + linkAnchor : undefined })
}
</script>

<template>
<div class="m-page wide">
  <header class="m-page-header">
    <button class="m-back-btn" title="返回" @click="router.back()">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6"/></svg>
    </button>
    <div class="m-page-title" :title="filePath">{{ fileName }}</div>
  </header>

  <div class="mv-body" @click="onContentClick">
    <FileViewer :path="filePath" :name="fileName" :ext="fileExt" :size="0" />
  </div>
</div>
</template>

<style scoped>
/* 宽内容页（预览类）超出公共 640px 限宽 */
.m-page.wide { max-width: 768px; }

.mv-body {
  flex: 1;
  min-height: 0;
  overflow: hidden; /* FileViewer 内部自行滚动 */
}
</style>
