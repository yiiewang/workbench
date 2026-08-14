<script setup lang="ts">
import { ref } from 'vue'
import {
  currentUser as storeCurrentUser,
  loggedIn,
  setAuth,
  clearAuth,
  setShowLoginModalFn,
} from '../../stores/indexStore'
import * as authApi from '../../api/auth'

const visible = ref(false)
const loading = ref(false)
const error = ref('')
const userId = ref('')
const password = ref('')
const orgId = ref('')

function show() { visible.value = true; error.value = '' }
function hide() { visible.value = false; password.value = '' }

// Register self in store so other components can call showLoginModal
setShowLoginModalFn(show)

async function doLogin() {
  error.value = ''
  if (!userId.value.trim()) { error.value = 'User ID required'; return }
  if (!password.value.trim()) { error.value = 'Password required'; return }
  loading.value = true
  try {
    const data = await authApi.login(userId.value.trim(), password.value, orgId.value.trim())
    // setAuth 同时写入 localStorage + 响应式 ref，驱动所有 UI 更新
    setAuth(data.token, { userId: data.userId, orgId: data.orgId })
    hide()
    window.location.reload()
  } catch (err: any) {
    error.value = err.msg || err.message || 'Login failed'
  } finally {
    loading.value = false
  }
}

function doLogout() {
  clearAuth()
  window.location.reload()
}

function onUserClick() {
  if (loggedIn.value) { doLogout() } else { show() }
}
</script>

<template>
  <div class="activity-item activity-user" :title="loggedIn ? '点击登出' : '登录'" @click="onUserClick">
    <svg v-if="!loggedIn" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
      <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
      <circle cx="12" cy="7" r="4"/>
    </svg>
    <div v-else class="user-badge">{{ (storeCurrentUser?.userId || '?').charAt(0).toUpperCase() }}</div>
  </div>

  <teleport to="body">
    <div v-if="visible" class="modal-overlay" @click.self="hide">
      <div class="modal-box">
        <h3>Login to Workbench</h3>
        <form @submit.prevent="doLogin">
          <label>User ID</label>
          <input v-model="userId" type="text" placeholder="Enter user ID" autofocus />
          <label>Password</label>
          <input v-model="password" type="password" placeholder="Enter password" />
          <label>Org ID <small>(optional)</small></label>
          <input v-model="orgId" type="text" placeholder="Enter org ID" />
          <p v-if="error" class="error">{{ error }}</p>
          <div class="modal-actions">
            <button type="button" class="btn-cancel" @click="hide">Cancel</button>
            <button type="submit" class="btn-primary" :disabled="loading">
              {{ loading ? 'Logging in...' : 'Login' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </teleport>
</template>

<style scoped>
.activity-user { margin-top: auto; }
.user-badge { width:24px; height:24px; border-radius:50%; background:var(--accent); color:#fff; display:flex; align-items:center; justify-content:center; font-size:12px; font-weight:600; }
.modal-overlay { position:fixed; top:0; left:0; width:100%; height:100%; background:rgba(0,0,0,.5); display:flex; align-items:center; justify-content:center; z-index:1001; }
.modal-box { background:var(--bg); border:1px solid var(--border); border-radius:8px; padding:24px; min-width:320px; max-width:400px; }
.modal-box h3 { margin:0 0 16px; font-size:16px; }
.modal-box label { display:block; margin:8px 0 4px; font-size:13px; color:var(--text-muted); }
.modal-box input { width:100%; padding:8px 12px; border:1px solid var(--border); border-radius:4px; background:var(--input-bg); color:var(--text); font-size:14px; box-sizing:border-box; }
.modal-box .error { color:#d00; font-size:12px; margin:4px 0; }
.modal-actions { display:flex; gap:8px; justify-content:flex-end; margin-top:16px; }
.modal-actions button { padding:6px 16px; border-radius:4px; border:1px solid var(--border); cursor:pointer; font-size:13px; }
.btn-primary { background:var(--accent); color:#fff; border-color:var(--accent); }
.btn-primary:disabled { opacity:.5; cursor:not-allowed; }
.btn-cancel { background:var(--bg); color:var(--text); }
</style>
