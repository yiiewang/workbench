<script setup lang="ts">
// File viewer component — replaces renderToContainer / showWelcome innerHTML manipulation
import { ref, watch, onUnmounted } from 'vue'
import { dataSource } from '../../composables/useEditor'
import { isBinaryExt, isImageExt, formatSize, API_CODE } from '../../lib/common'
import { showSharePasswordModal } from '../../lib/sharePasswordModal'
import JsonViewer from './JsonViewer.vue'
import XmlViewer from './XmlViewer.vue'

const props = defineProps<{ path: string; name: string; ext: string; size: number }>()
const emit = defineEmits<{ passwordRequired: [] }>()

const loading = ref(true)
const error = ref('')
const blobUrl = ref('')
const textContent = ref('')
const iframeSrc = ref('')
const jsonText = ref('')
const xmlText = ref('')

onUnmounted(() => {
  if (blobUrl.value) URL.revokeObjectURL(blobUrl.value)
  if (iframeSrc.value) URL.revokeObjectURL(iframeSrc.value)
})

watch(() => props.path, loadFile, { immediate: true })

async function loadFile() {
  if (!props.path) return
  loading.value = true; error.value = ''
  blobUrl.value = ''; textContent.value = ''; iframeSrc.value = ''; jsonText.value = ''; xmlText.value = ''

  try {
    const file = await dataSource.getFile(props.path)
    const ext = file.ext

    if (isBinaryExt(ext)) {
      const blob = await file.blob()
      blobUrl.value = URL.createObjectURL(blob)
    } else if (isImageExt(ext)) {
      const blob = await file.blob()
      blobUrl.value = URL.createObjectURL(blob)
    } else if (['html', 'htm'].includes(ext)) {
      const blob = await file.blob()
      iframeSrc.value = URL.createObjectURL(blob)
    } else if (ext === 'json') {
      jsonText.value = await file.text() || ''
    } else if (ext === 'xml') {
      xmlText.value = await file.text() || ''
    } else if (['md', 'markdown'].includes(ext)) {
      textContent.value = await file.text() || ''
      await renderMarkdown()
    } else {
      textContent.value = await file.text() || ''
    }
    loading.value = false
  } catch (err: any) {
    if (err.code === API_CODE.PASSWORD_REQUIRED || err.code === API_CODE.INVALID_SHARE_PWD) {
      const pwd = await showSharePasswordModal(err.msg)
      if (pwd) {
        dataSource.sharePassword = pwd
        sessionStorage.setItem('workbench-share-pwd', pwd)
        await loadFile()
        return
      }
      loading.value = false
      return
    }
    error.value = err.msg || err.message || 'unknown'
    loading.value = false
  }
}

// ---- Markdown render ----
const markdownHtml = ref('')
let mermaidScript: HTMLScriptElement | null = null

async function renderMarkdown() {
  try {
    await Promise.all([ensureMarked(), ensurePurify()])
    const { marked, DOMPurify } = window as any
    markdownHtml.value = DOMPurify.sanitize(marked.parse(textContent.value))
    ensureMermaid().then(() => {
      try { (window as any).mermaid.run({ querySelector: '.markdown-body .language-mermaid' }) } catch {}
    })
  } catch {}
}

function ensureScript(src: string, readyKey: string): Promise<void> {
  return new Promise(resolve => {
    if ((window as any)[readyKey]) return resolve()
    const s = document.createElement('script')
    s.src = src; s.crossOrigin = 'anonymous'
    s.onload = () => { (window as any)[readyKey] = true; resolve() }
    s.onerror = () => resolve()
    document.head.appendChild(s)
  })
}
const ensureMarked = () => ensureScript('https://cdn.jsdelivr.net/npm/marked@12/marked.min.js', '_markedLoaded')
const ensurePurify = () => ensureScript('https://cdn.jsdelivr.net/npm/dompurify@3/dist/purify.min.js', '_purifyLoaded')
const ensureMermaid = () => ensureScript('https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js', '_mermaidLoaded')
  .then(() => { (window as any).mermaid?.initialize?.({ startOnLoad: false, theme: 'default' }) })
</script>

<template>
  <div class="file-viewer">
    <!-- Loading -->
    <div v-if="loading" class="viewer-loading"><span class="spinner"></span></div>

    <!-- Error -->
    <div v-else-if="error" class="viewer-error">Failed to load: {{ error }}</div>

    <!-- Binary -->
    <div v-else-if="isBinaryExt(ext)" class="file-detail">
      <div class="name">{{ name }}</div>
      <table>
        <tbody>
          <tr><td>Type</td><td>{{ ext.toUpperCase() }}</td></tr>
          <tr><td>Size</td><td>{{ formatSize(size || 0) }}</td></tr>
        </tbody>
      </table>
      <a v-if="blobUrl" :href="blobUrl" :download="name" class="btn-download">Download</a>
    </div>

    <!-- Image -->
    <img v-else-if="isImageExt(ext) && blobUrl" :src="blobUrl"
      style="max-width:100%;max-height:100%;display:block;margin:auto;object-fit:contain;" />

    <!-- HTML iframe -->
    <iframe v-else-if="['html','htm'].includes(ext) && iframeSrc" :src="iframeSrc"
      sandbox="allow-scripts allow-forms allow-popups"
      style="width:100%;height:100%;border:none;background:#fff;" />

    <!-- JSON -->
    <JsonViewer v-else-if="ext === 'json'" :text="jsonText" />

    <!-- XML -->
    <XmlViewer v-else-if="['xml'].includes(ext)" :text="xmlText" />

    <!-- Markdown -->
    <div v-else-if="['md','markdown'].includes(ext)" class="markdown-body" v-html="markdownHtml" />

    <!-- Text / Code -->
    <pre v-else class="text-preview">{{ textContent }}</pre>
  </div>
</template>

<style scoped>
.file-viewer { width: 100%; height: 100%; overflow: auto; }
.viewer-loading { display: flex; align-items: center; justify-content: center; height: 100%; }
.viewer-error { padding: 20px; color: #d00; }
.text-preview { margin: 0; padding: 16px 24px; white-space: pre-wrap; word-break: break-all; font-size: 13px; font-family: var(--font-mono, monospace); }
</style>
