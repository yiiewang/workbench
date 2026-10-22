<script setup lang="ts">
import { computed } from 'vue'
import {
  openTabs as storeOpenTabs,
  activeTabPath as storeActiveTabPath,
  storeSetActiveTab,
  storeRemoveTab,
  storeGetTab,
} from '../../stores/indexStore'
import { dataSource } from '../../composables/useEditor'
import { showToast } from '../../lib/common'

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

// 下载当前激活 tab 的文件（browser 模式走 /api/file 流；分享模式走分享接口取 blob）
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
    <button
      v-if="storeActiveTabPath"
      class="tab-download"
      title="Download active file"
      @click="onDownloadActive"
    >
      <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M8 2v8M4.5 6.5L8 10l3.5-3.5M2.5 13h11"/></svg>
    </button>
  </div>
</template>

<style scoped>
.tab-download {
  margin-left: auto;
  align-self: center;
  margin-right: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: none;
  background: none;
  color: var(--text-dim);
  cursor: pointer;
  border-radius: 4px;
  flex-shrink: 0;
}
.tab-download:hover { background: var(--hover, #f2f3f5); color: var(--text); }
</style>
