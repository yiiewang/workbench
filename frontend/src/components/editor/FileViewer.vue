<script setup lang="ts">
// File viewer component — replaces renderToContainer / showWelcome innerHTML manipulation
import { ref, watch, onUnmounted } from 'vue'
import { dataSource } from '../../composables/useEditor'
import { isBinaryExt, isImageExt, isVideoExt, isAudioExt, mediaMime, looksBinary, formatSize, showToast, API_CODE } from '../../lib/common'
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
// 可内联播放的媒体类型：'video' | 'audio' | ''
const mediaKind = ref('')
// 流式播放地址（带短时效 ticket，支持 Range）；为空表示回退到 blob 播放
const mediaUrl = ref('')
// 流式模式下文件不在本地，大小由 ticket 接口下发
const mediaSize = ref(0)

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
  mediaKind.value = ''; mediaUrl.value = ''; mediaSize.value = 0

  try {
    const file = await dataSource.getFile(props.path)
    const ext = file.ext || props.ext || ''
    fileExt.value = ext
    isBinary.value = isBinaryExt(ext) || !!file.isBinary

    if (isVideoExt(ext) || isAudioExt(ext)) {
      mediaKind.value = isVideoExt(ext) ? 'video' : 'audio'
      // 优先走 ticket URL 流式播放：后端支持 Range，可拖动进度条且不必整文件入内存。
      // 拿不到（分享模式 / 后端未开放）则回退到 blob 播放——后端 contentType() 对媒体
      // 扩展名返回 application/octet-stream，而 <video>/<audio> 是严格按 MIME 判定的
      //（不像 <img> 会嗅探字节），回退时必须自己带上正确类型重建 Blob。
      const ticket = await dataSource.getMediaTicket(props.path)
      if (ticket) {
        mediaUrl.value = ticket.url
        mediaSize.value = ticket.size
      } else {
        const raw = await file.blob()
        fileSize.value = raw.size
        blobUrl.value = URL.createObjectURL(new Blob([raw], { type: mediaMime(ext) }))
      }
    } else if (isBinary.value) {
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

    <!-- 视频：纯播放器，铺满黑底影院区（文件名在 tab 上，下载用 TabBar 右上角全局按钮） -->
    <div v-else-if="mediaKind === 'video' && (mediaUrl || blobUrl)" class="media-stage">
      <video :src="mediaUrl || blobUrl" controls preload="metadata" playsinline />
    </div>

    <div v-else-if="mediaKind === 'audio' && (mediaUrl || blobUrl)" class="media-view">
      <div class="media-toolbar">
        <span class="media-icon"><svg viewBox="0 0 16 16"><path d="M8 1.5a2.5 2.5 0 00-2.5 2.5v.2A3 3 0 003 7v3.5h1.5V7a1.5 1.5 0 013 0v4.5H9V7a3 3 0 00-2.5-2.8V4A1 1 0 018 3a1 1 0 011 1v7h1.5V4A2.5 2.5 0 008 1.5z" fill="currentColor"/></svg></span>
        <div class="media-meta">
          <div class="media-name" :title="name">{{ name }}</div>
          <div class="media-sub">{{ (fileExt || 'audio').toUpperCase() }} · {{ formatSize(mediaSize || fileSize || size || 0) }}</div>
        </div>
        <!-- 下载按钮不在这里放：TabBar 右上角已有全局 Download，放了会重复 -->
      </div>
      <div class="media-audio">
        <span class="media-icon-lg"><svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><circle cx="8" cy="8" r="3" fill="#fff"/></svg></span>
        <div class="media-name">{{ name }}</div>
        <audio :src="mediaUrl || blobUrl" controls preload="metadata" />
      </div>
    </div>

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
/* 媒体预览：顶栏 + 播放区（.btn-download 的按钮样式在全局 style.css 里） */
.media-view { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.media-toolbar { display: flex; align-items: center; gap: 10px; flex-shrink: 0; padding: 8px 14px; background: var(--header-bg, #f3f3f3); border-bottom: 1px solid var(--border, #e5e5e5); }
.media-toolbar .media-icon { display: flex; color: var(--text-dim, #888); }
.media-toolbar .media-icon svg { width: 18px; height: 18px; }
.media-meta { flex: 1; min-width: 0; }
.media-name { font-size: 13px; font-weight: 600; color: var(--text, #333); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.media-sub { margin-top: 2px; font-size: 12px; color: var(--text-dim, #888); }
/* 影院区：黑底铺满整个预览区，播放器按原始比例居中 */
.media-stage { width: 100%; height: 100%; display: flex; align-items: center; justify-content: center; background: #000; }
.media-stage video { display: block; max-width: 100%; max-height: 100%; }
.media-audio { flex: 1; min-height: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 14px; padding: 24px; }
.media-audio .media-icon-lg { display: flex; color: var(--text-muted, #bbb); }
.media-audio .media-icon-lg svg { width: 56px; height: 56px; }
.media-audio .media-name { max-width: 100%; }
.media-audio audio { width: 100%; max-width: 480px; }
.viewer-loading { display: flex; align-items: center; justify-content: center; height: 100%; }
.viewer-error { padding: 20px; color: #d00; }
.text-preview { margin: 0; padding: 16px 24px; white-space: pre-wrap; word-break: break-all; font-size: 13px; font-family: var(--font-mono, monospace); }
</style>
