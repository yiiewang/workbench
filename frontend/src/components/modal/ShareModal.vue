<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { authToken, activeTabPath } from '../../stores/indexStore'
import { showToast, copyToClipboard } from '../../lib/common'
import * as shareApi from '../../api/share'

const visible = ref(false)
const resourcePath = ref('')
const resourceType = ref('file')
const maxAccessCount = ref(0)
const password = ref('')
const remark = ref('')
const dateRange = ref([])
const submitting = ref(false)
const effectiveAt = ref('')
const expiresAt = ref('')

watch(dateRange, (v) => {
  if (Array.isArray(v) && v.length === 2) {
    effectiveAt.value = new Date(v[0]).toISOString()
    expiresAt.value = new Date(v[1]).toISOString()
  } else {
    effectiveAt.value = ''
    expiresAt.value = ''
  }
})

function close() { visible.value = false }

async function submit() {
  if (submitting.value) return
  submitting.value = true
  try {
    const data = await shareApi.createShare({
      resourcePath: resourcePath.value,
      resourceType: resourceType.value,
      maxAccessCount: maxAccessCount.value,
      password: password.value,
      remark: remark.value,
      effectiveAt: effectiveAt.value,
      expiresAt: expiresAt.value,
    })
    if (password.value) {
      try { localStorage.setItem('share_pwd_' + data.token, password.value) } catch (e) {}
    }
    // 用 window.location.origin 拼接分享链接，避免 dev 代理模式下后端返回 localhost
    const shareUrl = window.location.origin + '/s/' + data.token
    const clipText = formatShareText(shareUrl, password.value, remark.value)
    copyToClipboard(clipText)
    showToast('Share created, link copied')
    close()
    const w = window as any
    if (typeof w.loadShares === 'function') w.loadShares()
  } catch (err: any) {
    showToast('Create failed: ' + (err.msg || err.message))
  } finally {
    submitting.value = false
  }
}

function formatShareText(url: string, pwd: string, rem: string) {
  let text = 'Link: ' + url
  if (pwd) text += '\nPassword: ' + pwd
  if (rem) text += '\nRemark: ' + rem
  return text
}

const router = useRouter()

// expose to window for context menu access (will be replaced by event bus)
;(window as any).openShareModal = (type: string) => {
  if (!authToken.value) {
    router.push('/login?redirect=' + encodeURIComponent(window.location.pathname))
    return
  }
  if (!activeTabPath.value) { showToast('Select a file first'); return }
  resourcePath.value = activeTabPath.value
  resourceType.value = type || 'file'
  maxAccessCount.value = 0
  password.value = ''
  remark.value = ''
  dateRange.value = []
  submitting.value = false
  visible.value = true
}
</script>

<template>
  <div class="modal-mask share-modal" v-if="visible" @click.self="close">
    <div class="modal-content share-modal-content">
      <div class="modal-header">创建分享</div>
      <div class="modal-body share-modal-body">
        <el-form label-position="top" @submit.prevent>
          <el-form-item label="Resource path">
            <el-input :model-value="resourcePath" readonly></el-input>
            <div style="font-size:11px;color:#999;margin-top:3px;">
              Type: {{ resourceType === 'dir' ? 'Folder' : 'File' }}
            </div>
          </el-form-item>
          <div style="display:flex;gap:12px;">
            <el-form-item label="Max access (0=unlimited)" style="flex:1;">
              <el-input-number v-model="maxAccessCount" :min="0" :step="1" controls-position="right" style="width:100%;"></el-input-number>
            </el-form-item>
            <el-form-item label="Password (optional)" style="flex:1;">
              <el-input v-model="password" placeholder="Leave empty for no password" clearable></el-input>
            </el-form-item>
          </div>
          <el-form-item label="Remark (optional)">
            <el-input v-model="remark" placeholder="Included when copying link" clearable maxlength="200" show-word-limit></el-input>
          </el-form-item>
          <el-form-item label="Time range (optional)">
            <el-date-picker
              v-model="dateRange"
              type="datetimerange"
              range-separator="to"
              start-placeholder="Effective"
              end-placeholder="Expires"
              format="YYYY-MM-DD HH:mm"
              value-format="X"
              style="width:100%;">
            </el-date-picker>
          </el-form-item>
          <div style="display:flex;justify-content:flex-end;gap:8px;margin-top:8px;">
            <el-button @click="close">Cancel</el-button>
            <el-button type="primary" :loading="submitting" @click="submit">Create</el-button>
          </div>
        </el-form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-mask { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,.4); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-content { background: var(--bg); border-radius: 8px; box-shadow: 0 4px 24px rgba(0,0,0,.15); width: 480px; max-width: 90vw; max-height: 80vh; overflow-y: auto; padding: 20px; }
.modal-header { font-size: 16px; font-weight: 600; margin-bottom: 16px; }
</style>
