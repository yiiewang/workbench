<script setup lang="ts">
// 移动端"我的"页：用户信息展示 + 通知邮箱编辑 + 退出登录（组织切换等复杂功能在 PC 端）
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { currentUser, clearAuth, orgs, currentOrgId, switchOrg } from '../../stores/auth'
import { resetIdentityScopedState } from '../../stores/indexStore'
import { showToast } from '../../lib/common'
import { updateMyEmail } from '../../api/auth'

const router = useRouter()

const roleLabel: Record<string, string> = {
  admin: '管理员',
  org_admin: '组织管理员',
  user: '成员',
}

// ---- 通知邮箱（任务到期提醒投递目标）----
const emailEditing = ref(false)
const emailInput = ref('')

function startEmailEdit() {
  emailInput.value = (currentUser.value as any)?.email || ''
  emailEditing.value = true
}

async function saveEmail() {
  const email = emailInput.value.trim()
  try {
    await updateMyEmail(email)
    if (currentUser.value) (currentUser.value as any).email = email
    emailEditing.value = false
    showToast(email ? '邮箱已更新' : '已清空邮箱（不再接收提醒）')
  } catch (e: any) {
    showToast(e?.msg || e?.message || '保存失败')
  }
}

async function onSwitchOrg(orgId: number) {
  if (orgId === currentOrgId.value) return
  try {
    await switchOrg(orgId)
    resetIdentityScopedState()
    showToast('已切换组织')
    router.push('/m')
  } catch {
    showToast('切换失败')
  }
}

function doLogout() {
  clearAuth()
  resetIdentityScopedState()
  router.push('/login')
}
</script>

<template>
<div class="m-profile">
  <!-- 用户卡片 -->
  <div class="m-card m-user-card">
    <div class="m-avatar">{{ (currentUser?.userName || '?').charAt(0).toUpperCase() }}</div>
    <div class="m-user-meta">
      <div class="m-user-name">{{ currentUser?.userName }}</div>
      <div class="m-user-org">{{ currentUser?.orgName }}</div>
      <span class="m-role-tag">{{ roleLabel[currentUser?.role || 'user'] || currentUser?.role }}</span>
    </div>
  </div>

  <!-- 信息 -->
  <div class="m-card">
    <div class="m-info-row"><span>用户 ID</span><b>{{ currentUser?.userId }}</b></div>
    <div class="m-info-row"><span>组织 ID</span><b>{{ currentUser?.orgId }}</b></div>
    <div class="m-info-row">
      <span>通知邮箱</span>
      <b v-if="!emailEditing">{{ (currentUser as any)?.email || '未设置' }}</b>
    </div>
    <!-- 邮箱编辑（任务到期提醒投递目标） -->
    <div v-if="!emailEditing" class="m-email-actions">
      <button class="m-email-btn" @click="startEmailEdit">
        {{ (currentUser as any)?.email ? '修改邮箱' : '设置邮箱（接收任务提醒）' }}
      </button>
    </div>
    <div v-else class="m-email-edit">
      <input v-model="emailInput" class="m-email-input" placeholder="you@example.com" inputmode="email" />
      <div class="m-email-edit-btns">
        <button class="m-email-btn" @click="emailEditing = false">取消</button>
        <button class="m-email-btn is-primary" @click="saveEmail">保存</button>
      </div>
    </div>
  </div>

  <!-- 组织切换（多组织用户） -->
  <div v-if="orgs.length > 1" class="m-card">
    <div class="m-section-title">切换组织</div>
    <div
      v-for="o in orgs"
      :key="o.orgId"
      class="m-org-item"
      :class="{ active: o.orgId === currentOrgId }"
      @click="onSwitchOrg(o.orgId)"
    >
      <span>{{ o.orgName }}</span>
      <span v-if="o.orgId === currentOrgId" class="m-org-check">✓</span>
    </div>
  </div>

  <button class="m-logout" @click="doLogout">退出登录</button>
</div>
</template>

<style scoped>
.m-profile {
  padding: 16px;
  max-width: 640px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.m-card {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 16px;
}
.m-user-card {
  display: flex;
  align-items: center;
  gap: 14px;
}
.m-avatar {
  width: 56px;
  height: 56px;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  font-weight: 600;
  flex-shrink: 0;
}
.m-user-meta { min-width: 0; }
.m-user-name { font-size: 17px; font-weight: 600; color: var(--text); }
.m-user-org { font-size: 13px; color: var(--text-dim); margin: 3px 0 6px; }
.m-role-tag {
  display: inline-block;
  font-size: 11px;
  color: var(--text-dim);
  background: var(--code-bg);
  border-radius: 3px;
  padding: 1px 8px;
}

.m-info-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 0;
  font-size: 13px;
  color: var(--text-dim);
  border-bottom: 1px solid var(--border);
}
.m-info-row:last-child { border-bottom: none; }
.m-info-row b { color: var(--text); font-family: var(--mono, monospace); font-weight: 500; }

.m-email-actions { margin-top: 8px; }
.m-email-btn {
  border: 1px solid var(--border);
  background: var(--bg);
  color: var(--accent);
  border-radius: 8px;
  font-size: 13px;
  padding: 6px 14px;
  cursor: pointer;
}
.m-email-btn.is-primary { background: var(--accent); color: #fff; }
.m-email-edit { margin-top: 8px; display: flex; flex-direction: column; gap: 8px; }
.m-email-input {
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg);
  color: var(--text);
  font-size: 14px;
  padding: 8px 10px;
}
.m-email-edit-btns { display: flex; gap: 8px; justify-content: flex-end; }

.m-section-title { font-size: 12px; color: var(--text-dim); margin-bottom: 8px; }
.m-org-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 14px;
  color: var(--text);
  margin-bottom: 8px;
  cursor: pointer;
}
.m-org-item.active { border-color: var(--accent); color: var(--accent); }
.m-org-check { font-weight: 700; }

.m-logout {
  margin-top: 8px;
  border: 1px solid #fde8ea;
  background: #fff;
  color: #cf222e;
  border-radius: 12px;
  font-size: 15px;
  padding: 12px;
  cursor: pointer;
}
.m-logout:active { background: #fde8ea; }
</style>
