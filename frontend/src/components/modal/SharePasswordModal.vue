<script setup lang="ts">
// 分享密码弹窗：叠在普通弹窗之上（layer=top）。
// 外壳（遮罩/层级/ESC）统一由 ModalShell 提供，本组件只保留输入交互。
import { ref, nextTick, watch } from 'vue'
import {
  sharePasswordVisible, sharePasswordError, submitSharePassword,
} from '../../composables/useShare'
import ModalShell from '../ui/ModalShell.vue'

const password = ref('')
const pwdInput = ref<HTMLInputElement | null>(null)

watch(sharePasswordVisible, (v) => {
  if (!v) return
  password.value = ''
  nextTick(() => pwdInput.value?.focus())
})

function submit() {
  const pwd = password.value.trim()
  if (!pwd) return
  submitSharePassword(pwd)
}

function cancel() {
  submitSharePassword(null)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') submit()
}
</script>

<template>
  <ModalShell v-if="sharePasswordVisible" title="🔒 此分享需要密码" width="360px" layer="top" @close="cancel">
    <div v-if="sharePasswordError" class="error">{{ sharePasswordError }}</div>
    <input
      ref="pwdInput"
      v-model="password"
      type="password"
      placeholder="请输入密码"
      @keydown="onKeydown"
    />
    <template #actions>
      <button type="button" class="btn-cancel" @click="cancel">Cancel</button>
      <button type="button" class="btn-primary" @click="submit">确认</button>
    </template>
  </ModalShell>
</template>

<style scoped>
.error { color: #d00; font-size: 12px; margin-bottom: 8px; }
input {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--input-bg);
  color: var(--text);
  font-size: 14px;
  box-sizing: border-box;
}
button { padding: 6px 16px; border-radius: 4px; border: 1px solid var(--border); cursor: pointer; font-size: 13px; }
.btn-primary { background: var(--accent); color: #fff; border-color: var(--accent); }
.btn-cancel { background: var(--bg); color: var(--text); }
</style>
