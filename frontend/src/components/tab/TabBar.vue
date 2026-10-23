<script setup lang="ts">
import { computed } from 'vue'
import {
  openTabs as storeOpenTabs,
  activeTabPath as storeActiveTabPath,
  storeSetActiveTab,
  storeRemoveTab,
  storeGetTab,
  dsMode,
} from '../../stores/indexStore'
import { dataSource } from '../../composables/useEditor'
import { showToast } from '../../lib/common'
import { openShareModal } from '../../lib/shareModal'
import { treeStore } from '../../composables/useTreeStore'

const tabs = computed<[string, { name: string; ext: string; size: number }][]>(() =>
  Array.from(storeOpenTabs.entries())
)

// 点击 tab → 激活
function onTabClick(path: string) {
  storeSetActiveTab(path)
}

// 点击 × → 关闭
function onTabClose(path: string, e: Event) {
  e.stopPropagation()
  storeRemoveTab(path)
  if (storeActiveTabPath.value === path) {
    const remaining = tabs.value
    if (remaining.length > 0) {
      storeSetActiveTab(remaining[remaining.length - 1][0])
    } else {
      storeSetActiveTab('')
    }
  }
}

// 创建分享链接（分享模式下隐藏：分享页不能再创建分享）
function onShareClick() {
  if (!storeActiveTabPath.value) return
  const dirData = treeStore.dirCache.get(storeActiveTabPath.value)
  openShareModal(dirData ? 'dir' : 'file')
}

// 下载当前激活 tab 的文件（浏览器模式 / 分享模式均走 dataSource）
async function onDownloadActive() {
  const path = storeActiveTabPath.value
  if (!path) return
  const info = storeGetTab(path)
  try {
    const file = await dataSource.getFile(path)
    const blob = await file.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = info?.name || path.split('/').pop() || 'file'
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 5000)
  } catch (err: any) {
    showToast('Download failed: ' + (err.msg || err.message))
  }
}
</script>

<template>
  <div class="tab-bar-row">
    <div class="tab-bar">
      <div
        v-for="[path, info] in tabs"
        :key="path"
        class="tab"
        :class="{ active: storeActiveTabPath === path }"
        :data-path="path"
        @click="onTabClick(path)"
      >
        <span class="tab-name">{{ info.name }}</span>
        <span class="tab-close" title="Close" @click="onTabClose(path, $event)">&times;</span>
      </div>
    </div>

    <!-- 右上角操作组：flex 右对齐，成员隐藏时剩余按钮自动贴右 -->
    <div class="tab-actions">
      <div
        v-show="storeActiveTabPath && dsMode !== 'share'"
        class="tab-share"
        title="Create share link"
        @click="onShareClick"
      >
        <svg width="14" height="14" viewBox="0 0 16 16"><path d="M11 2.5a1.5 1.5 0 11-3 0 1.5 1.5 0 013 0zm-.98 4.3l1.19.88-4.42 5.64-1.21-.91 4.44-5.61zM5.5 8a1.5 1.5 0 100-3 1.5 1.5 0 000 3zm8 5.5a1.5 1.5 0 11-3 0 1.5 1.5 0 013 0z" fill="currentColor"/></svg>
        <span>Share</span>
      </div>
      <div
        v-show="storeActiveTabPath"
        class="tab-download"
        title="Download this file"
        @click="onDownloadActive"
      >
        <svg width="14" height="14" viewBox="0 0 16 16"><path d="M8 1a.5.5 0 01.5.5v7.793l2.146-2.147a.5.5 0 01.708.708l-3 3a.5.5 0 01-.708 0l-3-3a.5.5 0 11.708-.708L7.5 9.293V1.5A.5.5 0 018 1zM2 13.5a.5.5 0 01.5-.5h11a.5.5 0 010 1h-11a.5.5 0 01-.5-.5z" fill="currentColor"/></svg>
        <span>Download</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tab-bar-row {
  display: flex;
  align-items: stretch;
  flex-shrink: 0;
  background: var(--header-bg);
  border-bottom: 1px solid var(--tab-border);
}

.tab-actions {
  display: flex;
  align-items: stretch;
  flex-shrink: 0;
}
</style>
