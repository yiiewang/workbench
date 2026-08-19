<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { setAuth, loggedIn } from '../stores/auth'
import * as authApi from '../api/auth'

const router = useRouter()
const route = useRoute()
const userId = ref('')
const password = ref('')
const orgId = ref('')
const error = ref('')
const loading = ref(false)

onMounted(() => {
  if (loggedIn.value) {
    router.replace((route.query.redirect as string) || '/')
  }
})

async function doLogin() {
  error.value = ''
  if (!userId.value.trim()) { error.value = 'User ID required'; return }
  if (!password.value.trim()) { error.value = 'Password required'; return }
  loading.value = true
  try {
    const data = await authApi.login(userId.value.trim(), password.value, orgId.value.trim())
    setAuth(data.token, {
      userId: data.user.userId,
      orgId: data.user.orgId,
      userName: data.user.userName,
      orgName: data.user.orgName,
    })
    const redirect = (route.query.redirect as string) || '/'
    router.replace(redirect)
  } catch (err: any) {
    error.value = err.msg || err.message || 'Login failed'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <!-- 背景装饰 -->
    <div class="bg-decoration">
      <div class="bg-circle bg-circle-1"></div>
      <div class="bg-circle bg-circle-2"></div>
      <div class="bg-circle bg-circle-3"></div>
    </div>

    <div class="login-card">
      <!-- Logo -->
      <div class="logo">
        <svg viewBox="0 0 16 16" width="40" height="40">
          <rect x="2" y="1" width="12" height="14" rx="1.5" fill="#007acc"/>
          <path d="M6 4h4l2 2v7H4V4z" fill="#fff" opacity=".85"/>
        </svg>
        <h1>Workbench</h1>
        <p class="subtitle">Sign in to continue</p>
      </div>

      <!-- 表单 -->
      <form @submit.prevent="doLogin" class="login-form">
        <div class="field">
          <input v-model="userId" type="text" placeholder=" " id="userId" autofocus />
          <label for="userId">User ID</label>
        </div>
        <div class="field">
          <input v-model="password" type="password" placeholder=" " id="password" />
          <label for="password">Password</label>
        </div>
        <div class="field">
          <input v-model="orgId" type="text" placeholder=" " id="orgId" />
          <label for="orgId">Org ID <span class="optional">(optional)</span></label>
        </div>

        <transition name="fade">
          <p v-if="error" class="error">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="7" cy="7" r="6"/>
              <path d="M7 4v3M7 9.5v.5"/>
            </svg>
            {{ error }}
          </p>
        </transition>

        <button type="submit" class="btn-login" :disabled="loading">
          <span v-if="loading" class="spinner"></span>
          {{ loading ? 'Signing in...' : 'Sign in' }}
        </button>
      </form>
    </div>
  </div>
</template>

<style scoped>
/* 显式覆盖 #app flex 父级的约束 — 让 login-page 占满整个 viewport */
.login-page {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 50%, #007acc 100%);
  overflow: hidden;
  z-index: 100;
}

/* 背景装饰圆 */
.bg-decoration {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
}
.bg-circle {
  position: absolute;
  border-radius: 50%;
  opacity: 0.08;
  background: #fff;
}
.bg-circle-1 { width: 600px; height: 600px; top: -200px; right: -150px; }
.bg-circle-2 { width: 400px; height: 400px; bottom: -100px; left: -100px; }
.bg-circle-3 { width: 300px; height: 300px; top: 50%; left: 60%; opacity: 0.05; }

/* 卡片 */
.login-card {
  position: relative;
  z-index: 1;
  width: 380px;
  max-width: 90vw;
  background: #fff;
  border-radius: 16px;
  padding: 40px 36px;
  box-shadow: 0 20px 60px rgba(0,0,0,0.3), 0 6px 20px rgba(0,0,0,0.15);
  animation: cardIn 0.4s ease;
}

@keyframes cardIn {
  from { opacity: 0; transform: translateY(20px) scale(0.96); }
  to   { opacity: 1; transform: translateY(0) scale(1); }
}

/* Logo 区 */
.logo {
  text-align: center;
  margin-bottom: 32px;
}
.logo svg {
  margin-bottom: 12px;
  filter: drop-shadow(0 2px 8px rgba(0,122,204,0.3));
}
.logo h1 {
  margin: 0;
  font-size: 26px;
  font-weight: 700;
  color: #1a1a1a;
  letter-spacing: -0.5px;
}
.logo .subtitle {
  margin: 6px 0 0;
  font-size: 14px;
  color: #999;
}

/* 表单 — 浮动标签输入框 */
.login-form { display: flex; flex-direction: column; gap: 16px; }

.field {
  position: relative;
}
.field input {
  width: 100%;
  padding: 14px 14px 14px 14px;
  border: 1.5px solid #e0e0e0;
  border-radius: 8px;
  font-size: 14px;
  color: #333;
  background: #fafafa;
  transition: all 0.2s;
  box-sizing: border-box;
}
.field input:hover {
  border-color: #c0c0c0;
  background: #fff;
}
.field input:focus {
  outline: none;
  border-color: #007acc;
  background: #fff;
  box-shadow: 0 0 0 3px rgba(0,122,204,0.1);
}
.field label {
  position: absolute;
  left: 14px;
  top: 14px;
  font-size: 14px;
  color: #aaa;
  pointer-events: none;
  transition: all 0.2s;
  padding: 0 2px;
  background: transparent;
}
/* 浮动标签效果 */
.field input:focus + label,
.field input:not(:placeholder-shown) + label {
  top: -8px;
  left: 10px;
  font-size: 11px;
  font-weight: 600;
  color: #007acc;
  background: #fff;
}
.field .optional {
  font-weight: 400;
  color: #bbb;
}

/* 错误提示 */
.error {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 8px 12px;
  background: #fef0f0;
  border: 1px solid #fde2e2;
  border-radius: 6px;
  color: #d00;
  font-size: 12px;
}

/* 登录按钮 */
.btn-login {
  position: relative;
  width: 100%;
  padding: 12px;
  border: none;
  border-radius: 8px;
  background: linear-gradient(135deg, #007acc 0%, #0060c0 100%);
  color: #fff;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-top: 4px;
}
.btn-login:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(0,122,204,0.3);
}
.btn-login:active:not(:disabled) {
  transform: translateY(0);
}
.btn-login:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* Loading spinner */
.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid rgba(255,255,255,0.3);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

/* 过渡动画 */
.fade-enter-active, .fade-leave-active { transition: opacity 0.2s, transform 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; transform: translateY(-4px); }

/* 响应式 */
@media (max-width: 480px) {
  .login-card { padding: 32px 24px; border-radius: 12px; }
  .logo h1 { font-size: 22px; }
}
</style>
