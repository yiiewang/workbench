<script setup lang="ts">
import { ref, nextTick, onMounted } from 'vue'
import { registerSharePasswordModal } from '../../lib/sharePasswordModal'

let resolvePromise: ((value: string | null) => void) | null = null

const visible = ref(false)
const password = ref('')
const error = ref('')
const pwdInput = ref<HTMLInputElement | null>(null)

async function show(err?: string): Promise<string | null> {
  return new Promise((resolve) => {
    resolvePromise = resolve
    password.value = ''
    error.value = err || ''
    visible.value = true
    nextTick(() => pwdInput.value?.focus())
  })
}

// Register globally so useIndexApp / FileViewer can trigger the modal
onMounted(() => {
  registerSharePasswordModal(show)
})

function submit() {
  const pwd = password.value.trim()
  if (!pwd) return
  resolvePromise?.(pwd)
  visible.value = false
}

function cancel() {
  resolvePromise?.(null)
  visible.value = false
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') submit()
}

defineExpose({ show })
</script>

<template>
  <teleport to="body">
    <div v-if="visible" class="modal-overlay" @click.self="cancel">
      <div class="modal-box">
        <h3>🔒 此分享需要密码</h3>
        <div v-if="error" class="error">{{ error }}</div>
        <input ref="pwdInput" v-model="password" type="password" placeholder="请输入密码" @keydown="onKeydown" />
        <div class="modal-actions">
          <button type="button" class="btn-cancel" @click="cancel">Cancel</button>
          <button type="button" class="btn-primary" @click="submit">确认</button>
        </div>
      </div>
    </div>
  </teleport>
</template>

<style scoped>
.modal-overlay { position:fixed; top:0; left:0; width:100%; height:100%; background:rgba(0,0,0,.5); display:flex; align-items:center; justify-content:center; z-index:1001; }
.modal-box { background:var(--bg); border:1px solid var(--border); border-radius:8px; padding:24px; min-width:320px; max-width:400px; }
.modal-box h3 { margin:0 0 16px; font-size:16px; }
.modal-box .error { color:#d00; font-size:12px; margin-bottom:8px; }
.modal-box input { width:100%; padding:8px 12px; border:1px solid var(--border); border-radius:4px; background:var(--input-bg); color:var(--text); font-size:14px; box-sizing:border-box; margin-bottom:16px; }
.modal-actions { display:flex; gap:8px; justify-content:flex-end; }
.modal-actions button { padding:6px 16px; border-radius:4px; border:1px solid var(--border); cursor:pointer; font-size:13px; }
.btn-primary { background:var(--accent); color:#fff; border-color:var(--accent); }
.btn-cancel { background:var(--bg); color:var(--text); }
</style>
