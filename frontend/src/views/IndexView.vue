<script setup lang="ts">
import { watch } from 'vue'
import { useRoute } from 'vue-router'
import TabBar from '../components/tab/TabBar.vue'
import EditorArea from '../components/editor/EditorArea.vue'
import {
  loadSharesFn,
  authToken as storeAuthToken,
  dsMode,
} from '../stores/indexStore'
import { treeStore } from '../composables/useTreeStore'
import { setupIndexApp, exitShareMode } from '../composables/useIndexApp'

const route = useRoute()
setupIndexApp()

// 路由切换：从分享页 /s/:token 切回浏览器路由时重置分享状态并恢复 explore
watch(() => route.path, (path) => {
  if (dsMode.value === 'share' && !path.startsWith('/s/')) {
    exitShareMode()
    if (path === '/file' && storeAuthToken.value) {
      treeStore.loadDir('/').catch(() => {})
    }
    return
  }
  if (path === '/shares' && storeAuthToken.value) {
    loadSharesFn?.()
  }
}, { flush: 'post' })

</script>

<template>
  <!-- Tab 栏（含右上角 Share / Download 操作组） -->
  <TabBar />

  <EditorArea @password-required="() => {}" />
</template>
