<script setup lang="ts">
// Shares sidebar: reactive list of shares, no DOM manipulation
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { injectSidebarHeader } from '../../composables/useSidebarHeader'
import { sharesRevision } from '../../composables/useShare'
import { authToken } from '../../stores/auth'
import { showToast, copyToClipboard } from '../../lib/common'
import * as shareApi from '../../api/share'
import type { ShareItem } from '../../api/share'

const shares = ref<ShareItem[]>([])

// 创建/撤销分享后由 useShare 递增版本号，触发列表刷新
watch(sharesRevision, () => { loadShares() })

const shareCount = computed(() => shares.value.length)

// 向 layout 的 sidebar-header 注入分享数量 badge
const header = injectSidebarHeader()
watch(shareCount, (v) => {
  if (header) header.badge = v ? String(v) : ''
}, { immediate: true })

async function loadShares() {
  if (!authToken.value) return
  try {
    const data = await shareApi.listShares()
    shares.value = data.shares || []
  } catch (err) {
    console.error('load shares failed', err)
  }
}

function formatShareText(token: string, remark: string) {
  const url = window.location.origin + '/s/' + token
  let text = 'Link: ' + url
  try {
    const pwd = localStorage.getItem('share_pwd_' + token)
    if (pwd) text += '\nPassword: ' + pwd
  } catch {}
  if (remark) text += '\nRemark: ' + remark
  return text
}

function onCopyShare(token: string, remark: string) {
  copyToClipboard(formatShareText(token, remark))
  showToast('Link copied')
}

async function onDeleteShare(id: string) {
  if (!confirm('Revoke this share?')) return
  try {
    await shareApi.deleteShare(id)
    showToast('Share revoked')
    loadShares()
  } catch (err: any) {
    showToast('Revoke failed: ' + (err.msg || err.message))
  }
}

function tagText(s: ShareItem): string {
  const parts = [s.resourceType === 'dir' ? 'Folder' : 'File']
  if (s.hasPassword) parts.push('Password')
  if (s.maxAccessCount > 0) parts.push(`Access ${s.accessCount}/${s.maxAccessCount}`)
  else parts.push(`Access ${s.accessCount}/∞`)
  if (s.expiresAt) parts.push(s.expiresAt.slice(0, 10))
  return parts.join(' · ')
}

onMounted(() => {
  if (authToken.value) loadShares()
})

onUnmounted(() => {
  if (header) header.badge = ''
})
</script>

<template>
  <div v-if="!shares.length" class="share-panel-empty">No shares yet</div>
  <div
    v-for="s in shares"
    :key="s.id"
    class="share-item"
  >
    <div class="share-item-path" :title="s.resourcePath">{{ s.resourcePath }}</div>
    <div v-if="s.remark" class="share-item-remark" :title="s.remark">{{ s.remark }}</div>
    <div class="share-item-meta">{{ tagText(s) }}</div>
    <div class="share-item-actions">
      <button class="btn btn-sm" @click="onCopyShare(s.token, s.remark)">Copy</button>
      <button class="btn btn-sm btn-danger" @click="onDeleteShare(s.id)">Revoke</button>
    </div>
  </div>
</template>
