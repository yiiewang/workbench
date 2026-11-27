<script setup lang="ts">
import { ref, watch } from 'vue'
import { activeTabPath } from '../../stores/indexStore'
import { showToast, copyToClipboard } from '../../lib/common'
import * as shareApi from '../../api/share'
import {
  shareModalVisible, shareResourceType,
  closeShareModal, notifySharesChanged,
} from '../../composables/useShare'
import ModalShell from '../ui/ModalShell.vue'

const maxAccessCount = ref(0)
const password = ref('')
const remark = ref('')
const dateRange = ref([])
const submitting = ref(false)
const effectiveAt = ref('')
const expiresAt = ref('')

watch(dateRange, (v) => {
  if (Array.isArray(v) && v.length === 2) {
    // value-format="X" → Unix 秒时间戳字符串，需先按秒换算成毫秒
    effectiveAt.value = new Date(Number(v[0]) * 1000).toISOString()
    expiresAt.value = new Date(Number(v[1]) * 1000).toISOString()
  } else {
    effectiveAt.value = ''
    expiresAt.value = ''
  }
})

// 每次打开重置表单
watch(shareModalVisible, (v) => {
  if (!v) return
  maxAccessCount.value = 0
  password.value = ''
  remark.value = ''
  dateRange.value = []
  submitting.value = false
})

async function submit() {
  if (submitting.value) return
  submitting.value = true
  try {
    const data = await shareApi.createShare({
      resourcePath: activeTabPath.value,
      resourceType: shareResourceType.value,
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
    closeShareModal()
    notifySharesChanged()
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
</script>

<template>
  <ModalShell v-if="shareModalVisible" title="创建分享" width="480px" @close="closeShareModal">
    <el-form label-position="top" @submit.prevent>
      <el-form-item label="Resource path">
        <el-input :model-value="activeTabPath" readonly></el-input>
        <div style="font-size:11px;color:#999;margin-top:3px;">
          Type: {{ shareResourceType === 'dir' ? 'Folder' : 'File' }}
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
    </el-form>
    <template #actions>
      <el-button @click="closeShareModal">Cancel</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">Create</el-button>
    </template>
  </ModalShell>
</template>
