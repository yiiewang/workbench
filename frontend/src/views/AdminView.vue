<script setup lang="ts">
// Admin 用户管理主区：用户表格 + 新建/编辑/重置密码/删除
// UI 与项目整体保持一致：自定义 .modal-mask/.btn/.user-table + showToast/confirm
// 紧凑布局：padding 4-8px、字号 12px、表格 padding 4px 8px
import { ref, onMounted, computed } from 'vue'
import { showToast } from '../lib/common'
import * as adminApi from '../api/admin'
import type { AdminUser, Role } from '../api/admin'
import { isAdmin, currentUser } from '../stores/auth'

const users = ref<AdminUser[]>([])
const roles = ref<Role[]>([])
const loading = ref(false)
const orgFilter = ref<string>('') // 超级 admin 可指定 orgId 过滤；org_admin 自动锁定自己 org

const isSuperAdmin = computed(() => isAdmin.value)

// 新建用户弹窗
const createVisible = ref(false)
const createForm = ref({ org: '', name: '', password: '', roleId: 2, mobile: '' })
const createLoading = ref(false)

// 编辑用户弹窗
const editVisible = ref(false)
const editForm = ref({ id: 0, name: '', mobile: '', roleId: 2 })
const editLoading = ref(false)

// 重置密码弹窗
const pwdVisible = ref(false)
const pwdForm = ref({ id: 0, name: '', password: '' })
const pwdLoading = ref(false)

async function loadData() {
  loading.value = true
  try {
    const params = isSuperAdmin.value && orgFilter.value ? '?orgId=' + orgFilter.value : ''
    const [u, r] = await Promise.all([
      fetch('/api/admin/users' + params, { headers: { Authorization: 'Bearer ' + localStorage.getItem('workbench_auth_token') } }).then(res => res.json()).then(j => ({ users: j.data?.users || [] })),
      adminApi.listRoles(),
    ])
    users.value = u.users || []
    roles.value = r.roles || []
    window.dispatchEvent(new Event('admin-users-refresh'))
  } catch (err: any) {
    showToast('Load failed: ' + (err.msg || err.message))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  // org_admin 只能建自己 org 的用户，预填且锁定
  createForm.value = {
    org: isSuperAdmin.value ? '' : (currentUser.value?.orgName || ''),
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
  createLoading.value = true
  try {
    await adminApi.createUser({
      org: createForm.value.org.trim(),
      name: createForm.value.name.trim(),
      password: createForm.value.password,
      roleId: createForm.value.roleId,
      mobile: createForm.value.mobile.trim(),
    })
    showToast('User created')
    createVisible.value = false
    loadData()
  } catch (err: any) {
    showToast('Create failed: ' + (err.msg || err.message))
  } finally {
    createLoading.value = false
  }
}

function openEdit(u: AdminUser) {
  editForm.value = { id: u.id, name: u.name, mobile: u.mobile || '', roleId: u.roleId }
  editVisible.value = true
}

async function submitEdit() {
  if (!editForm.value.name.trim()) {
    showToast('Name is required')
    return
  }
  editLoading.value = true
  try {
    await adminApi.updateUser(editForm.value.id, {
      name: editForm.value.name.trim(),
      mobile: editForm.value.mobile.trim(),
      roleId: editForm.value.roleId,
    })
    showToast('User updated')
    editVisible.value = false
    loadData()
  } catch (err: any) {
    showToast('Update failed: ' + (err.msg || err.message))
  } finally {
    editLoading.value = false
  }
}

function openResetPwd(u: AdminUser) {
  pwdForm.value = { id: u.id, name: u.name, password: '' }
  pwdVisible.value = true
}

async function submitResetPwd() {
  if (!pwdForm.value.password) {
    showToast('Password is required')
    return
  }
  pwdLoading.value = true
  try {
    await adminApi.updateUser(pwdForm.value.id, { password: pwdForm.value.password })
    showToast('Password reset')
    pwdVisible.value = false
  } catch (err: any) {
    showToast('Reset failed: ' + (err.msg || err.message))
  } finally {
    pwdLoading.value = false
  }
}

async function onDelete(u: AdminUser) {
  if (!confirm(`Delete user "${u.name}" (${u.orgName})? This cannot be undone.`)) return
  try {
    await adminApi.deleteUser(u.id)
    showToast('User deleted')
    loadData()
  } catch (err: any) {
    showToast('Delete failed: ' + (err.msg || err.message))
  }
}

onMounted(loadData)
</script>

<template>
  <div class="admin-view">
    <div class="admin-toolbar">
      <div class="admin-title">
        <h2>User Management</h2>
        <span class="admin-subtitle">{{ users.length }} users</span>
        <span v-if="!isSuperAdmin" class="admin-scope">· scope: {{ currentUser?.orgName }}</span>
      </div>
      <div class="admin-actions">
        <input
          v-if="isSuperAdmin"
          v-model="orgFilter"
          class="form-input org-filter"
          placeholder="Filter by orgId (empty = all)"
          @keyup.enter="loadData"
        />
        <button class="btn btn-primary" @click="openCreate">New User</button>
      </div>
    </div>

    <table class="user-table">
      <thead>
        <tr>
          <th>User</th>
          <th>Org</th>
          <th>Role</th>
          <th>Mobile</th>
          <th>Created</th>
          <th class="th-actions">Actions</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="loading">
          <td colspan="6" class="cell-empty">Loading…</td>
        </tr>
        <tr v-else-if="!users.length">
          <td colspan="6" class="cell-empty">No users</td>
        </tr>
        <tr v-for="u in users" :key="u.id">
          <td class="cell-name">{{ u.name }}<span v-if="u.id === currentUser?.userId" class="self-tag"> (you)</span></td>
          <td>{{ u.orgName }}</td>
          <td><span class="role-tag" :class="u.role">{{ u.role }}</span></td>
          <td class="cell-mobile">{{ u.mobile || '—' }}</td>
          <td class="cell-created">{{ u.createdAt }}</td>
          <td class="cell-actions">
            <button class="btn btn-sm" @click="openEdit(u)">Edit</button>
            <button class="btn btn-sm" @click="openResetPwd(u)">Pwd</button>
            <button class="btn btn-sm btn-danger" @click="onDelete(u)">Del</button>
          </td>
        </tr>
      </tbody>
    </table>

    <!-- 新建用户 -->
    <div v-if="createVisible" class="modal-mask" @click.self="createVisible = false">
      <div class="modal-content admin-modal">
        <div class="modal-header">New User</div>
        <div class="modal-body">
          <div class="form-row">
            <label class="form-label">Org</label>
            <input v-model="createForm.org" class="form-input" :readonly="!isSuperAdmin" placeholder="org name" />
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
              <option v-for="r in roles" :key="r.id" :value="r.id">{{ r.name }}</option>
            </select>
          </div>
          <div class="form-row">
            <label class="form-label">Mobile</label>
            <input v-model="createForm.mobile" class="form-input" placeholder="optional" />
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn" @click="createVisible = false">Cancel</button>
          <button class="btn btn-primary" :disabled="createLoading" @click="submitCreate">Create</button>
        </div>
      </div>
    </div>

    <!-- 编辑用户 -->
    <div v-if="editVisible" class="modal-mask" @click.self="editVisible = false">
      <div class="modal-content admin-modal">
        <div class="modal-header">Edit User</div>
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
              <option v-for="r in roles.filter(r => isSuperAdmin || r.id !== 1)" :key="r.id" :value="r.id">{{ r.name }}</option>
            </select>
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn" @click="editVisible = false">Cancel</button>
          <button class="btn btn-primary" :disabled="editLoading" @click="submitEdit">Save</button>
        </div>
      </div>
    </div>

    <!-- 重置密码 -->
    <div v-if="pwdVisible" class="modal-mask" @click.self="pwdVisible = false">
      <div class="modal-content admin-modal">
        <div class="modal-header">Reset Password — {{ pwdForm.name }}</div>
        <div class="modal-body">
          <div class="form-row">
            <label class="form-label">New Pwd</label>
            <input v-model="pwdForm.password" type="password" class="form-input" />
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn" @click="pwdVisible = false">Cancel</button>
          <button class="btn btn-primary" :disabled="pwdLoading" @click="submitResetPwd">Reset</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-view {
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  overflow: auto;
  box-sizing: border-box;
}

.admin-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.admin-title {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex: 1;
  min-width: 0;
}
.admin-title h2 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}
.admin-subtitle,
.admin-scope {
  font-size: 11px;
  color: var(--text-dim);
}
.admin-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.org-filter {
  width: 200px;
  padding: 4px 8px;
  font-size: 12px;
}

/* 用户表格：紧凑（VS Code 风格细边框/浅灰表头/hover 高亮） */
.user-table {
  border-collapse: collapse;
  width: 100%;
  font-size: 12px;
  line-height: 1.4;
  background: var(--bg);
}
.user-table th,
.user-table td {
  border: 1px solid var(--border);
  padding: 4px 8px;
  text-align: left;
  vertical-align: middle;
}
.user-table th {
  background: var(--header-bg);
  font-weight: 600;
  color: var(--text-dim);
  white-space: nowrap;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.user-table tbody tr:hover {
  background: var(--hover);
}
.cell-name {
  font-weight: 500;
  color: var(--text);
}
.self-tag {
  font-size: 10px;
  color: var(--text-muted);
  margin-left: 4px;
}
.cell-mobile {
  color: var(--text-dim);
}
.cell-created {
  color: var(--text-dim);
  white-space: nowrap;
  font-size: 11px;
}
.cell-actions {
  white-space: nowrap;
  width: 1%;
}
.cell-actions .btn {
  margin-right: 3px;
}
.cell-empty {
  text-align: center;
  color: var(--text-muted);
  padding: 20px 0;
}

/* 角色标签 */
.role-tag {
  display: inline-block;
  padding: 0 6px;
  border-radius: 3px;
  font-size: 10px;
  line-height: 1.6;
  font-weight: 500;
}
.role-tag.admin {
  background: #fde8ea;
  color: #cf222e;
}
.role-tag.org_admin {
  background: #fff4e1;
  color: #b35900;
}
.role-tag.user {
  background: var(--code-bg);
  color: var(--text-dim);
}

/* 弹窗：复用全局 .modal-mask/.modal-content */
.admin-modal {
  width: 380px;
  max-width: 92vw;
}
.admin-modal :deep(.modal-header) {
  padding: 10px 16px;
  font-size: 13px;
}
.admin-modal :deep(.modal-body) {
  padding: 12px 16px;
}
.admin-modal :deep(.modal-actions) {
  padding: 8px 16px;
}

/* 表单：紧凑 */
.form-row {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin-bottom: 8px;
}
.form-label {
  font-size: 11px;
  font-weight: 600;
  color: var(--text-dim);
}
.form-input {
  width: 100%;
  padding: 5px 8px;
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
