<script setup lang="ts">
// Shares sidebar: reactive list of shares, no DOM manipulation
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { authToken, setLoadSharesFn, dsMode, currentUser } from '../../stores/indexStore'
import { showToast, copyToClipboard } from '../../lib/common'
import * as shareApi from '../../api/share'
import { openShareModal } from '../../lib/shareModal'

interface Share {
  id: string
  token: string
  resourcePath: string
  resourceType: string
  hasPassword: boolean
  maxAccessCount: number
  accessCount: number
  expiresAt: string
  remark: string
  createdAt: string
}

const shares = ref<Share[]>([])

const shareCount = computed(() => shares.value.length)

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

function tagText(s: Share): string {
  const parts = [s.resourceType === 'dir' ? 'Folder' : 'File']
  if (s.hasPassword) parts.push('Password')
  if (s.maxAccessCount > 0) parts.push(`Access ${s.accessCount}/${s.maxAccessCount}`)
  else parts.push(`Access ${s.accessCount}/∞`)
  if (s.expiresAt) parts.push(s.expiresAt.slice(0, 10))
  return parts.join(' · ')
}

onMounted(() => {
  setLoadSharesFn(loadShares)
  if (authToken.value) loadShares()
})

onUnmounted(() => {
  setLoadSharesFn(null)
})

defineExpose({ loadShares })

// expose openShareModal to window for context menu use
;(window as any).openShareModal = openShareModal
</script>

<template>
  <div class="share-panel active" id="sharesPanel">
    <div class="sidebar-header">
      MY SHARES
      <span v-if="shareCount" class="share-badge" id="shareBadge">{{ shareCount }}</span>
    </div>
    <div class="share-list" id="shareList">
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
    </div>
  </div>
</template>
