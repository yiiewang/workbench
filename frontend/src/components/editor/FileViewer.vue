<script setup lang="ts">
// File viewer component — replaces renderToContainer / showWelcome innerHTML manipulation
import { ref, watch, onUnmounted } from 'vue'
import { dataSource } from '../../composables/useEditor'
import { isBinaryExt, isImageExt, looksBinary, formatSize, showToast, API_CODE } from '../../lib/common'
import { isCodeExt } from '../../lib/highlighter'
import { showSharePasswordModal } from '../../lib/sharePasswordModal'
import JsonViewer from './JsonViewer.vue'
import XmlViewer from './XmlViewer.vue'
import CodePreview from './CodePreview.vue'
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

const loading = ref(true)
const error = ref('')
const blobUrl = ref('')
const textContent = ref('')
const htmlText = ref('')
const jsonText = ref('')
const xmlText = ref('')
// 实际扩展名（来自后端/文件名解析，无扩展名为 ''）
const fileExt = ref('')
// 最终判定为二进制：扩展名命中，或内容嗅探命中（无扩展名的 ELF/PE 等）
const isBinary = ref(false)
const fileSize = ref(0)

onUnmounted(() => {
  if (blobUrl.value) URL.revokeObjectURL(blobUrl.value)
})

watch(() => props.path, loadFile, { immediate: true })

// 下载：/api/file 带鉴权（Authorization 头），不能直接用裸 <a href> 指向它（会 401），
// 所以走带 token 的请求取字节流再落盘。二进制卡片已持有 blobUrl 时直接复用，避免二次请求。
async function onDownload() {
  let tempUrl = false
  try {
    let url = blobUrl.value
    if (!url) {
      const file = await dataSource.getFile(props.path)
      url = URL.createObjectURL(await file.blob())
      tempUrl = true
    }
    const a = document.createElement('a')
    a.href = url
    a.download = props.name
    document.body.appendChild(a)
    a.click()
    a.remove()
    if (tempUrl) setTimeout(() => URL.revokeObjectURL(url), 5000)
  } catch (err: any) {
    showToast('Download failed: ' + (err.msg || err.message || 'unknown'))
  }
}

async function loadFile() {
  if (!props.path) return
  loading.value = true; error.value = ''
  blobUrl.value = ''; textContent.value = ''; htmlText.value = ''; jsonText.value = ''; xmlText.value = ''
  isBinary.value = false; fileExt.value = ''; fileSize.value = 0

  try {
    const file = await dataSource.getFile(props.path)
    const ext = file.ext || props.ext || ''
    fileExt.value = ext
    isBinary.value = isBinaryExt(ext) || !!file.isBinary

    if (isBinary.value) {
      // 二进制（含压缩包）：只展示信息卡片 + 下载，绝不当文本解码
      const blob = await file.blob()
      fileSize.value = blob.size
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
      // 未知/无扩展名：先取字节做内容嗅探，二进制则走下载卡片，避免把乱码当文本渲染
      const blob = await file.blob()
      const head = new Uint8Array(await blob.slice(0, 8192).arrayBuffer())
      if (looksBinary(head)) {
        isBinary.value = true
        fileSize.value = blob.size
        blobUrl.value = URL.createObjectURL(blob)
      } else {
        textContent.value = await blob.text()
      }
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

    <!-- Binary（含无扩展名但内容为二进制的文件）：不预览内容，只给信息 + 下载 -->
    <div v-else-if="isBinary" class="file-detail">
      <div class="name">{{ name }}</div>
      <table>
        <tbody>
          <tr><td>Type</td><td>{{ fileExt ? fileExt.toUpperCase() : 'Binary' }}</td></tr>
          <tr><td>Size</td><td>{{ formatSize(fileSize || size || 0) }}</td></tr>
        </tbody>
      </table>
      <a v-if="blobUrl" :href="blobUrl" :download="name" class="btn-download" @click.prevent="onDownload">Download</a>
    </div>

    <!-- Image -->
    <img v-else-if="isImageExt(fileExt) && blobUrl" :src="blobUrl"
      style="max-width:100%;max-height:100%;display:block;margin:auto;object-fit:contain;" />

    <!-- HTML：srcdoc 内联渲染，sandbox 不含 allow-same-origin（不可访问父页面存储） -->
    <iframe v-else-if="['html','htm'].includes(fileExt) && htmlText" :srcdoc="htmlText"
      sandbox="allow-scripts allow-popups"
      style="width:100%;height:100%;border:none;background:#fff;" />

    <!-- JSON -->
    <JsonViewer v-else-if="fileExt === 'json'" :text="jsonText" />

    <!-- XML -->
    <XmlViewer v-else-if="['xml'].includes(fileExt)" :text="xmlText" />

    <!-- Markdown -->
    <MarkdownPreview v-else-if="['md','markdown'].includes(fileExt)" :content="textContent" toc />

    <!-- Code：行号 + 语法高亮 + 折叠（超大文件在组件内降级为纯文本） -->
    <CodePreview v-else-if="isCodeExt(fileExt)" :text="textContent" :ext="fileExt" />

    <!-- Text -->
    <pre v-else class="text-preview">{{ textContent }}</pre>
  </div>
</template>

<style scoped>
.file-viewer { width: 100%; height: 100%; overflow: auto; }
.viewer-loading { display: flex; align-items: center; justify-content: center; height: 100%; }
.viewer-error { padding: 20px; color: #d00; }
.text-preview { margin: 0; padding: 16px 24px; white-space: pre-wrap; word-break: break-all; font-size: 13px; font-family: var(--font-mono, monospace); }
</style>
