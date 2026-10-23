<script setup lang="ts">
// File viewer component — replaces renderToContainer / showWelcome innerHTML manipulation
import { ref, watch, onUnmounted } from 'vue'
import { dataSource } from '../../composables/useEditor'
import { isBinaryExt, isImageExt, formatSize, API_CODE } from '../../lib/common'
import { showSharePasswordModal } from '../../lib/sharePasswordModal'
import JsonViewer from './JsonViewer.vue'
import XmlViewer from './XmlViewer.vue'
import MarkdownPreview from '../markdown/MarkdownPreview.vue'

// srcdoc 文档中 href="#id" 会被解析成导航到 about:srcdoc#id，触发整页重载（表现为点了没反应）。
// 注入这段脚本把页内锚点点击改成 scrollIntoView。sandbox 不含 allow-same-origin，
// 该脚本运行在独立源里，访问不到父页面（DOM / localStorage）。
const ANCHOR_HOOK = `<script>
(function(){
  document.addEventListener('click', function(e){
    var a = e.target && e.target.closest ? e.target.closest('a[href]') : null;
    if (!a) return;
    var href = a.getAttribute('href') || '';
    if (href.charAt(0) !== '#') return;
    var id = decodeURIComponent(href.slice(1));
    if (!id) return;
    var t = document.getElementById(id) || document.getElementsByName(id)[0];
    if (!t) return;
    e.preventDefault();
    t.scrollIntoView();
  }, false);
})();
<\/script>`

function withAnchorHook(html: string): string {
  if (!html) return html
  const i = html.lastIndexOf('</body>')
  return i === -1 ? html + ANCHOR_HOOK : html.slice(0, i) + ANCHOR_HOOK + html.slice(i)
}

const props = defineProps<{ path: string; name: string; ext: string; size: number }>()
const emit = defineEmits<{ passwordRequired: [] }>()

const loading = ref(true)
const error = ref('')
const blobUrl = ref('')
const textContent = ref('')
const htmlText = ref('')
const jsonText = ref('')
const xmlText = ref('')

onUnmounted(() => {
  if (blobUrl.value) URL.revokeObjectURL(blobUrl.value)
})

watch(() => props.path, loadFile, { immediate: true })

async function loadFile() {
  if (!props.path) return
  loading.value = true; error.value = ''
  blobUrl.value = ''; textContent.value = ''; htmlText.value = ''; jsonText.value = ''; xmlText.value = ''

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
      // 用 srcdoc 内联渲染：blob: URL 在无 allow-same-origin 的 sandbox iframe 里
      // 会被 Chrome 拒绝（Not allowed to load local resource），锚点完全不可用
      htmlText.value = withAnchorHook(await file.text() || '')
    } else if (ext === 'json') {
      jsonText.value = await file.text() || ''
    } else if (ext === 'xml') {
      xmlText.value = await file.text() || ''
    } else if (['md', 'markdown'].includes(ext)) {
      textContent.value = await file.text() || ''
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

    <!-- HTML：srcdoc 内联渲染，sandbox 不含 allow-same-origin（不可访问父页面存储） -->
    <iframe v-else-if="['html','htm'].includes(ext) && htmlText" :srcdoc="htmlText"
      sandbox="allow-scripts allow-popups"
      style="width:100%;height:100%;border:none;background:#fff;" />

    <!-- JSON -->
    <JsonViewer v-else-if="ext === 'json'" :text="jsonText" />

    <!-- XML -->
    <XmlViewer v-else-if="['xml'].includes(ext)" :text="xmlText" />

    <!-- Markdown -->
    <MarkdownPreview v-else-if="['md','markdown'].includes(ext)" :content="textContent" />

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
