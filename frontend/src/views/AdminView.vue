<script setup lang="ts">
// Admin 用户管理主区：右侧用户看板（信息概览 + 统计卡片）
// 创建/编辑/重置密码/删除均通过弹窗操作
import { ref, computed } from 'vue'
import { showToast } from '../lib/common'
import * as adminApi from '../api/admin'
import {
  roles,
  selectedUser,
  dashboard,
  loadAdminData,
  selectUser,
  clearSelectionIfDeleted,
  startCreate,
} from '../stores/adminStore'
import { isAdmin, currentUser } from '../stores/auth'

// ============ 弹窗状态 ============
const createVisible = ref(false)
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
function openCreate() {
  createForm.value = {
    org: isAdmin.value ? '' : (currentUser.value?.orgName || ''),
    name: '',
    password: '',
    roleId: 2,
    mobile: '',
  }
  createVisible.value = true
}

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
    createVisible.value = false
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
      </div>
    </div>

    <!-- 新建用户弹窗 -->
    <div v-if="createVisible" class="modal-mask" @click.self="createVisible = false">
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
          <button class="btn" @click="createVisible = false">Cancel</button>
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
