<script setup lang="ts">
import { computed } from 'vue'
import {
  openTabs as storeOpenTabs,
  activeTabPath as storeActiveTabPath,
  storeSetActiveTab,
  storeRemoveTab,
} from '../../stores/indexStore'

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
  </div>
</template>
