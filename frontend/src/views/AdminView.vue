<script setup lang="ts">
// Admin 用户管理主区：右侧用户看板（信息概览 + 统计卡片）
// 创建/编辑/重置密码/删除均通过弹窗操作
import { ref, computed, watch } from 'vue'
import { showToast } from '../lib/common'
import * as adminApi from '../api/admin'
import type { UserOrgFeature } from '../api/admin'
import {
  roles,
  users,
  selectedId,
  selectedUser,
  dashboard,
  loadAdminData,
  selectUser,
  clearSelectionIfDeleted,
  createVisible,
  openCreate,
  closeCreate,
} from '../stores/adminStore'
import { isAdmin, currentUser, refreshFeatures } from '../stores/auth'

// ============ 成员功能配置（per-user-per-org） ============
// 功能标识 → 展示名（UI 全英文）
const featureLabels: Record<string, string> = {
  file: 'File Browser',
  share: 'Share',
  todo: 'Todo Board',
  admin: 'User Management',
}
const userFeatures = ref<UserOrgFeature[]>([])
const featuresLoading = ref(false)

// 选中用户变化时，加载该用户在当前组织的功能配置
watch(selectedUser, (u) => {
  if (u) {
    loadUserFeatures(u.id)
  } else {
    userFeatures.value = []
  }
})

async function loadUserFeatures(userId: number) {
  featuresLoading.value = true
  try {
    const data = await adminApi.listUserFeatures(userId)
    userFeatures.value = data.features || []
  } catch {
    userFeatures.value = []
  } finally {
    featuresLoading.value = false
  }
}

async function toggleFeature(code: string, enabled: boolean) {
  const u = selectedUser.value
  if (!u) return
  try {
    await adminApi.updateUserFeature(u.id, code, enabled)
    showToast(`${featureLabels[code] || code} ${enabled ? 'enabled' : 'disabled'}`)
    await loadUserFeatures(u.id)
    // 仅当配置的是当前登录用户自己时，才刷新全局功能集合（动态菜单即时生效）
    if (u.id === currentUser.value?.userId) {
      await refreshFeatures()
    }
  } catch (err: any) {
    showToast('Update failed: ' + (err.msg || err.message))
    await loadUserFeatures(u.id) // 回滚本地状态
  }
}

// ============ 弹窗状态 ============
const editVisible = ref(false)
const pwdVisible = ref(false)
const saving = ref(false)
const deleting = ref(false)

// 新建表单
const createForm = ref({ org: '', name: '', password: '', roleId: 2, mobile: '' })
// 编辑表单
const editForm = ref({ name: '', mobile: '', roleId: 2 })
// 重置密码表单
const pwdForm = ref('')

// 可选角色：org_admin 不能授予 admin 角色（id=1）
const selectableRoles = computed(() =>
  roles.value.filter((r) => isAdmin.value || r.id !== 1),
)

// 完成率（百分比，避免除零）
const doneRate = computed(() => {
  if (!dashboard.value || dashboard.value.totalTasks === 0) return 0
  return Math.round((dashboard.value.doneTasks / dashboard.value.totalTasks) * 100)
})

// ============ 新建 ============
// 打开新建弹窗时初始化表单（org 字段：admin 可跨 org，org_admin 锁定自己 org）
watch(createVisible, (v) => {
  if (!v) return
  createForm.value = {
    org: isAdmin.value ? '' : (currentUser.value?.orgName || ''),
    name: '',
    password: '',
    roleId: 2,
    mobile: '',
  }
})

// 进入页面时若无选中用户，默认选中第一个用户，保证 main 始终展示看板
watch(users, (list) => {
  if (selectedId.value == null && list.length > 0) {
    selectUser(list[0].id)
  }
}, { immediate: true })

async function submitCreate() {
  if (!createForm.value.org.trim() || !createForm.value.name.trim() || !createForm.value.password) {
    showToast('Org, name and password are required')
    return
  }
  saving.value = true
  try {
    const created = await adminApi.createUser({
      org: createForm.value.org.trim(),
      name: createForm.value.name.trim(),
      password: createForm.value.password,
      roleId: createForm.value.roleId,
      mobile: createForm.value.mobile.trim(),
    })
    showToast('User created')
    closeCreate()
    await loadAdminData()
    selectUser(created.id)
  } catch (err: any) {
    showToast('Create failed: ' + (err.msg || err.message))
  } finally {
    saving.value = false
  }
}

// ============ 编辑 ============
function openEdit() {
  const u = selectedUser.value
  if (!u) return
  editForm.value = { name: u.name, mobile: u.mobile || '', roleId: u.roleId }
  editVisible.value = true
}

async function submitEdit() {
  const u = selectedUser.value
  if (!u) return
  if (!editForm.value.name.trim()) {
    showToast('Name is required')
    return
  }
  saving.value = true
  try {
    await adminApi.updateUser(u.id, {
      name: editForm.value.name.trim(),
      mobile: editForm.value.mobile.trim(),
      roleId: editForm.value.roleId,
    })
    showToast('User updated')
    editVisible.value = false
    await loadAdminData()
  } catch (err: any) {
    showToast('Update failed: ' + (err.msg || err.message))
  } finally {
    saving.value = false
  }
}

// ============ 重置密码 ============
function openPwd() {
  if (!selectedUser.value) return
  pwdForm.value = ''
  pwdVisible.value = true
}

async function submitPwd() {
  const u = selectedUser.value
  if (!u) return
  if (!pwdForm.value) {
    showToast('Password is required')
    return
  }
  saving.value = true
  try {
    await adminApi.updateUser(u.id, { password: pwdForm.value })
    showToast('Password reset')
    pwdVisible.value = false
  } catch (err: any) {
    showToast('Reset failed: ' + (err.msg || err.message))
  } finally {
    saving.value = false
  }
}

// ============ 删除 ============
async function onDelete() {
  const u = selectedUser.value
  if (!u) return
  if (!confirm(`Delete user "${u.name}" (${u.orgName})? This cannot be undone.`)) return
  deleting.value = true
  try {
    await adminApi.deleteUser(u.id)
    showToast('User deleted')
    clearSelectionIfDeleted(u.id)
    await loadAdminData()
  } catch (err: any) {
    showToast('Delete failed: ' + (err.msg || err.message))
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <div class="admin-board">
    <!-- 空状态 -->
    <div v-if="!selectedUser" class="empty-state">
      <div class="empty-icon">👥</div>
      <div class="empty-title">No user selected</div>
      <div class="empty-hint">Select a user from the left, or create a new one.</div>
      <button class="btn btn-primary" @click="openCreate">+ New User</button>
    </div>

    <!-- 用户看板：左侧概览 + 操作，右侧统计 + 详情，水平占满主区 -->
    <div v-else class="board">
      <!-- 左侧：概览 + 操作按钮 -->
      <div class="board-left">
        <div class="avatar">{{ selectedUser.name.charAt(0).toUpperCase() }}</div>
        <div class="overview-meta">
          <div class="ov-name">
            {{ selectedUser.name }}
            <span v-if="selectedUser.id === currentUser?.userId" class="you-tag">you</span>
          </div>
          <div class="ov-org">{{ selectedUser.orgName }}</div>
          <span class="role-tag" :class="selectedUser.role">{{ selectedUser.role }}</span>
        </div>
        <div class="overview-actions">
          <button class="btn btn-primary" @click="openEdit">Edit</button>
          <button class="btn" @click="openPwd">Reset Pwd</button>
          <button class="btn btn-danger" :disabled="deleting" @click="onDelete">Delete</button>
        </div>
      </div>

      <!-- 右侧：统计 + 详细信息 -->
      <div class="board-right">
        <div class="stats-grid">
          <div class="stat-card">
            <div class="stat-num">{{ dashboard?.totalTasks ?? '—' }}</div>
            <div class="stat-label">Total Tasks</div>
          </div>
          <div class="stat-card">
            <div class="stat-num">{{ dashboard?.doneTasks ?? '—' }}</div>
            <div class="stat-label">Done Tasks</div>
          </div>
          <div class="stat-card">
            <div class="stat-num">{{ dashboard?.shareCount ?? '—' }}</div>
            <div class="stat-label">Shares</div>
          </div>
          <div class="stat-card">
            <div class="stat-num">{{ doneRate }}%</div>
            <div class="stat-label">Done Rate</div>
          </div>
        </div>

        <div class="detail-card">
          <div class="field-row"><span class="field-label">User ID</span><span class="field-value">{{ selectedUser.id }}</span></div>
          <div class="field-row"><span class="field-label">Org ID</span><span class="field-value">{{ selectedUser.orgId }}</span></div>
          <div class="field-row"><span class="field-label">Mobile</span><span class="field-value">{{ selectedUser.mobile || '—' }}</span></div>
          <div class="field-row"><span class="field-label">Created</span><span class="field-value">{{ selectedUser.createdAt }}</span></div>
        </div>

        <!-- 成员功能配置：当前选中用户在当前组织的功能开关（owner/admin 与平台 admin 可操作） -->
        <div class="features-panel">
          <div class="features-header">
            <span class="features-title">Member Features</span>
            <span class="features-sub">{{ selectedUser.name }}</span>
          </div>
          <div v-if="featuresLoading" class="features-empty">Loading…</div>
          <div v-else class="features-list">
            <div v-for="f in userFeatures" :key="f.featureCode" class="feature-item">
              <div class="feature-info">
                <div class="feature-name">{{ featureLabels[f.featureCode] || f.featureCode }}</div>
                <div class="feature-code">{{ f.featureCode }}</div>
              </div>
              <label class="switch">
                <input
                  type="checkbox"
                  :checked="f.enabled"
                  @change="toggleFeature(f.featureCode, ($event.target as HTMLInputElement).checked)"
                />
                <span class="slider"></span>
              </label>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 新建用户弹窗 -->
    <div v-if="createVisible" class="modal-mask" @click.self="closeCreate()">
      <div class="modal-content admin-modal">
        <div class="modal-header">New User</div>
        <div class="modal-body">
          <div class="form-row">
            <label class="form-label">Org</label>
            <input v-model="createForm.org" class="form-input" :readonly="!isAdmin" placeholder="org name" />
          </div>
          <div class="form-row">
            <label class="form-label">Name</label>
            <input v-model="createForm.name" class="form-input" placeholder="user name" />
          </div>
          <div class="form-row">
            <label class="form-label">Password</label>
            <input v-model="createForm.password" type="password" class="form-input" />
          </div>
          <div class="form-row">
            <label class="form-label">Role</label>
            <select v-model="createForm.roleId" class="form-input">
              <option v-for="r in selectableRoles" :key="r.id" :value="r.id">{{ r.name }}</option>
            </select>
          </div>
          <div class="form-row">
            <label class="form-label">Mobile</label>
            <input v-model="createForm.mobile" class="form-input" placeholder="optional" />
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn" @click="closeCreate()">Cancel</button>
          <button class="btn btn-primary" :disabled="saving" @click="submitCreate">Create</button>
        </div>
      </div>
    </div>

    <!-- 编辑用户弹窗 -->
    <div v-if="editVisible" class="modal-mask" @click.self="editVisible = false">
      <div class="modal-content admin-modal">
        <div class="modal-header">Edit: {{ selectedUser?.name }}</div>
        <div class="modal-body">
          <div class="form-row">
            <label class="form-label">Name</label>
            <input v-model="editForm.name" class="form-input" />
          </div>
          <div class="form-row">
            <label class="form-label">Mobile</label>
            <input v-model="editForm.mobile" class="form-input" placeholder="optional" />
          </div>
          <div class="form-row">
            <label class="form-label">Role</label>
            <select v-model="editForm.roleId" class="form-input">
              <option v-for="r in selectableRoles" :key="r.id" :value="r.id">{{ r.name }}</option>
            </select>
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn" @click="editVisible = false">Cancel</button>
          <button class="btn btn-primary" :disabled="saving" @click="submitEdit">Save</button>
        </div>
      </div>
    </div>

    <!-- 重置密码弹窗 -->
    <div v-if="pwdVisible" class="modal-mask" @click.self="pwdVisible = false">
      <div class="modal-content admin-modal">
        <div class="modal-header">Reset Password — {{ selectedUser?.name }}</div>
        <div class="modal-body">
          <div class="form-row">
            <label class="form-label">New Pwd</label>
            <input v-model="pwdForm" type="password" class="form-input" />
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn" @click="pwdVisible = false">Cancel</button>
          <button class="btn btn-primary" :disabled="saving" @click="submitPwd">Reset</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-board {
  padding: 12px 16px;
  height: 100%;
  overflow: auto;
  box-sizing: border-box;
}

/* 成员功能配置面板 */
.features-panel {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  padding: 12px 16px;
}
.features-header {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 10px;
}
.features-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
}
.features-sub {
  font-size: 11px;
  color: var(--text-dim);
}
.features-empty {
  font-size: 12px;
  color: var(--text-muted);
  padding: 4px 0;
}
.features-list {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 8px;
}
.feature-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 4px;
}
.feature-name {
  font-size: 12px;
  font-weight: 500;
  color: var(--text);
}
.feature-code {
  font-size: 10px;
  color: var(--text-muted);
  font-family: var(--mono, monospace);
  margin-top: 1px;
}

/* 开关 */
.switch {
  position: relative;
  display: inline-block;
  width: 34px;
  height: 18px;
  flex-shrink: 0;
}
.switch input {
  opacity: 0;
  width: 0;
  height: 0;
}
.slider {
  position: absolute;
  cursor: pointer;
  inset: 0;
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 18px;
  transition: 0.2s;
}
.slider::before {
  content: '';
  position: absolute;
  height: 12px;
  width: 12px;
  left: 2px;
  bottom: 2px;
  background: #fff;
  border-radius: 50%;
  transition: 0.2s;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
}
.switch input:checked + .slider {
  background: var(--accent);
  border-color: var(--accent);
}
.switch input:checked + .slider::before {
  transform: translateX(16px);
}

/* 空状态 */
.empty-state {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--text-muted);
}
.empty-icon { font-size: 32px; opacity: 0.4; }
.empty-title { font-size: 14px; font-weight: 500; color: var(--text-dim); }
.empty-hint { font-size: 12px; margin-bottom: 8px; }

/* 看板：左侧概览+操作 / 右侧统计+详情，水平占满主区 */
.board {
  display: grid;
  grid-template-columns: 240px 1fr;
  gap: 12px;
  width: 100%;
  align-items: start;
}

/* 左侧：概览 + 操作 */
.board-left {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
}
.avatar {
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
  margin: 0 auto;
  flex-shrink: 0;
}
.overview-meta {
  text-align: center;
  min-width: 0;
}
.ov-name {
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
}
.you-tag {
  font-size: 10px;
  color: var(--text-muted);
  font-style: italic;
  font-weight: 400;
}
.ov-org {
  font-size: 12px;
  color: var(--text-dim);
  margin: 2px 0 6px;
}
.overview-actions {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 4px;
}
.overview-actions .btn {
  width: 100%;
}

/* 右侧：统计 + 详情 */
.board-right {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

/* 角色标签 */
.role-tag {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 500;
}
.role-tag.admin { background: #fde8ea; color: #cf222e; }
.role-tag.org_admin { background: #fff4e1; color: #b35900; }
.role-tag.user { background: var(--code-bg); color: var(--text-dim); }

/* 统计卡片 */
.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 10px;
}
.stat-card {
  padding: 14px 12px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  text-align: center;
}
.stat-num {
  font-size: 22px;
  font-weight: 700;
  color: var(--accent);
  line-height: 1.2;
}
.stat-label {
  font-size: 11px;
  color: var(--text-dim);
  margin-top: 4px;
}

/* 详细信息 */
.detail-card {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg);
  padding: 8px 16px;
}
.field-row {
  display: flex;
  justify-content: space-between;
  padding: 6px 0;
  font-size: 12px;
  border-bottom: 1px solid var(--border);
}
.field-row:last-child { border-bottom: none; }
.field-label { color: var(--text-dim); }
.field-value { color: var(--text); font-family: var(--mono, monospace); }

/* 弹窗 */
.admin-modal { width: 400px; max-width: 92vw; }
.form-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.form-label {
  flex: 0 0 80px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-dim);
  text-align: right;
}
.form-input {
  flex: 1;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 3px;
  font-size: 12px;
  color: var(--text);
  background: var(--bg);
  box-sizing: border-box;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.form-input:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 2px rgba(0, 122, 204, 0.1);
}
.form-input[readonly] {
  background: var(--code-bg);
  color: var(--text-dim);
}
</style>
