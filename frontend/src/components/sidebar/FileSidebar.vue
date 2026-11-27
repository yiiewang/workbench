<script setup lang="ts">
// /file 侧栏容器：EXPLORER / MY SHARES 双 Tab 共用同一个路由与右侧文件编辑器，
// 避免独立 /shares 页复用文件主区却无人驱动、常驻空态。
// Tab 状态放 URL query（tab=shares），activity-bar / 首页卡片可直达。
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { hasFeature } from '../../stores/auth'
import ExplorerSidebar from './ExplorerSidebar.vue'
import SharesSidebar from './SharesSidebar.vue'

const route = useRoute()
const router = useRouter()

const tab = computed<'explorer' | 'shares'>(() =>
  route.query.tab === 'shares' && hasFeature('share') ? 'shares' : 'explorer'
)

function switchTab(next: 'explorer' | 'shares') {
  if (next === tab.value) return
  router.push({ path: '/file', query: next === 'shares' ? { tab: 'shares' } : {} })
}
</script>

<template>
  <div class="file-sidebar">
    <div v-if="hasFeature('share')" class="sidebar-tabs">
      <button
        class="sidebar-tab"
        :class="{ active: tab === 'explorer' }"
        type="button"
        @click="switchTab('explorer')"
      >Explorer</button>
      <button
        class="sidebar-tab"
        :class="{ active: tab === 'shares' }"
        type="button"
        @click="switchTab('shares')"
      >My Shares</button>
    </div>
    <div class="sidebar-tab-body">
      <ExplorerSidebar v-if="tab === 'explorer'" />
      <SharesSidebar v-else />
    </div>
  </div>
</template>

<style scoped>
.file-sidebar {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
}
.sidebar-tabs {
  display: flex;
  flex-shrink: 0;
  border-bottom: 1px solid var(--border);
  padding: 0 8px;
}
.sidebar-tab {
  flex: 1;
  padding: 9px 4px 8px;
  border: none;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--text-dim);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: .5px;
  text-transform: uppercase;
  cursor: pointer;
}
.sidebar-tab:hover { color: var(--text); }
.sidebar-tab.active {
  color: var(--accent);
  border-bottom-color: var(--accent);
}
.sidebar-tab-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  scrollbar-width: none;
}
.sidebar-tab-body::-webkit-scrollbar { display: none; }
</style>
