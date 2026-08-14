<script setup lang="ts">
import { watch } from 'vue'
import { useRoute } from 'vue-router'
import TabBar from '../components/tab/TabBar.vue'
import EditorArea from '../components/editor/EditorArea.vue'
import {
  loadSharesFn,
  authToken as storeAuthToken,
  activeTabPath as storeActiveTabPath,
  dsMode,
} from '../stores/indexStore'
import { treeStore } from '../composables/useTreeStore'
import { setupIndexApp } from '../composables/useIndexApp'
import { showToast } from '../lib/common'
import { dataSource } from '../composables/useEditor'
import { openShareModal } from '../lib/shareModal'
import * as fileApi from '../api/files'

const route = useRoute()
setupIndexApp()

// /shares 路由切换
watch(() => route.path, (path) => {
  if (path === '/shares' && storeAuthToken.value) {
    loadSharesFn?.()
  }
}, { flush: 'post' })

function onShareClick() {
  if (!storeActiveTabPath.value) return
  const path = storeActiveTabPath.value
  const dirData = treeStore.dirCache.get(path)
  const resourceType = dirData ? 'dir' : 'file'
  openShareModal(resourceType)
}

async function onDownloadClick() {
  if (!storeActiveTabPath.value) return
  try {
    let blob: Blob
    if (dsMode.value === 'share') {
      // 分享模式：通过 dataSource.getFile 拿文件内容（走 /api/share/{token}）
      const file = await dataSource.getFile(storeActiveTabPath.value)
      blob = await file.blob()
    } else {
      // 浏览器模式：走鉴权接口 /api/file
      const resp = await fileApi.getFile(storeActiveTabPath.value)
      blob = await resp.blob()
    }
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = storeActiveTabPath.value.split('/').pop() || 'download'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err: any) {
    showToast('Download failed: ' + err.message)
  }
}
</script>

<template>
  <TabBar />

  <!-- Share 按钮：有活跃 tab 且非分享模式时显示（分享页面不能再创建分享） -->
  <div v-show="storeActiveTabPath && dsMode !== 'share'" class="tab-share" id="shareBtn" title="Create share link" @click="onShareClick">
    <svg width="14" height="14" viewBox="0 0 16 16"><path d="M11 2.5a1.5 1.5 0 11-3 0 1.5 1.5 0 013 0zm-.98 4.3l1.19.88-4.42 5.64-1.21-.91 4.44-5.61zM5.5 8a1.5 1.5 0 100-3 1.5 1.5 0 000 3zm8 5.5a1.5 1.5 0 11-3 0 1.5 1.5 0 013 0z" fill="currentColor"/></svg>
    <span id="shareLabel">Share</span>
  </div>

  <!-- Download 按钮：有活跃 tab 即显示（浏览器模式 + 分享模式均可下载） -->
  <div v-show="storeActiveTabPath" class="tab-download" id="downloadBtn" title="Download this file" @click="onDownloadClick">
    <svg width="14" height="14" viewBox="0 0 16 16"><path d="M8 1a.5.5 0 01.5.5v7.793l2.146-2.147a.5.5 0 01.708.708l-3 3a.5.5 0 01-.708 0l-3-3a.5.5 0 11.708-.708L7.5 9.293V1.5A.5.5 0 018 1zM2 13.5a.5.5 0 01.5-.5h11a.5.5 0 010 1h-11a.5.5 0 01-.5-.5z" fill="currentColor"/></svg>
    <span>Download</span>
  </div>

  <EditorArea @password-required="() => {}" />
</template>
