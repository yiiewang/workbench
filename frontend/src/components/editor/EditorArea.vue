<script setup lang="ts">
import { computed } from 'vue'
import { activeTabPath as storeActiveTabPath, storeGetTab } from '../../stores/indexStore'
import { dataSource } from '../../composables/useEditor'
import FileViewer from './FileViewer.vue'

const emit = defineEmits<{ passwordRequired: [] }>()

const tabInfo = computed(() => {
  const path = storeActiveTabPath.value
  if (!path) return null
  return storeGetTab(path) || null
})

// markdown 内本地文件链接拦截 → hash 路由跳转
function onEditorClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a')
  if (!a) return
  const href = a.getAttribute('href')
  if (!href) return
  if (/^(https?:|mailto:|tel:|#)/i.test(href)) return

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
    <FileViewer
      v-if="tabInfo"
      :path="storeActiveTabPath"
      :name="tabInfo.name"
      :ext="tabInfo.ext"
      :size="tabInfo.size"
      @password-required="emit('passwordRequired')"
    />
    <div v-else class="editor-empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6z"/><path d="M14 2v6h6"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/></svg>
      <span>Select a file from the Explorer to preview</span>
    </div>
  </div>
</template>
