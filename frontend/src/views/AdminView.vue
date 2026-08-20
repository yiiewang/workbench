<script setup lang="ts">
// Admin 用户管理主区：用户表格 + 新建/编辑/重置密码/删除
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as adminApi from '../api/admin'
import type { AdminUser, Role } from '../api/admin'

const users = ref<AdminUser[]>([])
const roles = ref<Role[]>([])
const loading = ref(false)

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
    const [u, r] = await Promise.all([adminApi.listUsers(), adminApi.listRoles()])
    users.value = u.users || []
    roles.value = r.roles || []
    // 通知侧栏统计卡刷新
    window.dispatchEvent(new Event('admin-users-refresh'))
  } catch (err: any) {
    ElMessage.error(err.msg || err.message || 'Load failed')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  createForm.value = { org: '', name: '', password: '', roleId: 2, mobile: '' }
  createVisible.value = true
}

async function submitCreate() {
  if (!createForm.value.org.trim() || !createForm.value.name.trim() || !createForm.value.password) {
    ElMessage.warning('Org, name and password are required')
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
    ElMessage.success('User created')
    createVisible.value = false
    loadData()
  } catch (err: any) {
    ElMessage.error(err.msg || err.message || 'Create failed')
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
    ElMessage.warning('Name is required')
    return
  }
  editLoading.value = true
  try {
    await adminApi.updateUser(editForm.value.id, {
      name: editForm.value.name.trim(),
      mobile: editForm.value.mobile.trim(),
      roleId: editForm.value.roleId,
    })
    ElMessage.success('User updated')
    editVisible.value = false
    loadData()
  } catch (err: any) {
    ElMessage.error(err.msg || err.message || 'Update failed')
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
    ElMessage.warning('Password is required')
    return
  }
  pwdLoading.value = true
  try {
    await adminApi.updateUser(pwdForm.value.id, { password: pwdForm.value.password })
    ElMessage.success('Password reset')
    pwdVisible.value = false
  } catch (err: any) {
    ElMessage.error(err.msg || err.message || 'Reset failed')
  } finally {
    pwdLoading.value = false
  }
}

async function onDelete(u: AdminUser) {
  try {
    await ElMessageBox.confirm(`Delete user "${u.name}" (${u.orgName})? This cannot be undone.`, 'Confirm', {
      type: 'warning',
      confirmButtonText: 'Delete',
      cancelButtonText: 'Cancel',
    })
  } catch { return }
  try {
    await adminApi.deleteUser(u.id)
    ElMessage.success('User deleted')
    loadData()
  } catch (err: any) {
    ElMessage.error(err.msg || err.message || 'Delete failed')
  }
}

function roleTagType(role: string) {
  return role === 'admin' ? 'danger' : 'info'
}

onMounted(loadData)
</script>

<template>
  <div class="admin-view">
    <div class="admin-toolbar">
      <div class="admin-title">
        <h2>User Management</h2>
        <span class="admin-subtitle">{{ users.length }} users</span>
      </div>
      <el-button type="primary" @click="openCreate">New User</el-button>
    </div>

    <el-table :data="users" v-loading="loading" stripe>
      <el-table-column prop="name" label="User" min-width="120" />
      <el-table-column prop="orgName" label="Org" min-width="100" />
      <el-table-column label="Role" width="100">
        <template #default="{ row }">
          <el-tag :type="roleTagType(row.role)" size="small">{{ row.role }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Mobile" min-width="120">
        <template #default="{ row }">{{ row.mobile || '—' }}</template>
      </el-table-column>
      <el-table-column prop="createdAt" label="Created" min-width="150" />
      <el-table-column label="Actions" width="220" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">Edit</el-button>
          <el-button size="small" @click="openResetPwd(row)">Reset Pwd</el-button>
          <el-button size="small" type="danger" @click="onDelete(row)">Delete</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 新建用户 -->
    <el-dialog v-model="createVisible" title="New User" width="420px">
      <el-form label-width="90px">
        <el-form-item label="Org"><el-input v-model="createForm.org" placeholder="org name" /></el-form-item>
        <el-form-item label="Name"><el-input v-model="createForm.name" placeholder="user name" /></el-form-item>
        <el-form-item label="Password"><el-input v-model="createForm.password" type="password" show-password /></el-form-item>
        <el-form-item label="Role">
          <el-select v-model="createForm.roleId" style="width: 100%">
            <el-option v-for="r in roles" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="Mobile"><el-input v-model="createForm.mobile" placeholder="optional" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">Cancel</el-button>
        <el-button type="primary" :loading="createLoading" @click="submitCreate">Create</el-button>
      </template>
    </el-dialog>

    <!-- 编辑用户 -->
    <el-dialog v-model="editVisible" title="Edit User" width="420px">
      <el-form label-width="90px">
        <el-form-item label="Name"><el-input v-model="editForm.name" /></el-form-item>
        <el-form-item label="Mobile"><el-input v-model="editForm.mobile" placeholder="optional" /></el-form-item>
        <el-form-item label="Role">
          <el-select v-model="editForm.roleId" style="width: 100%">
            <el-option v-for="r in roles" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">Cancel</el-button>
        <el-button type="primary" :loading="editLoading" @click="submitEdit">Save</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码 -->
    <el-dialog v-model="pwdVisible" :title="`Reset Password — ${pwdForm.name}`" width="420px">
      <el-form label-width="90px">
        <el-form-item label="New Pwd"><el-input v-model="pwdForm.password" type="password" show-password /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdVisible = false">Cancel</el-button>
        <el-button type="primary" :loading="pwdLoading" @click="submitResetPwd">Reset</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-view { padding: 20px 24px; display: flex; flex-direction: column; gap: 16px; height: 100%; overflow: auto; box-sizing: border-box; }
.admin-toolbar { display: flex; align-items: center; justify-content: space-between; }
.admin-title { display: flex; align-items: baseline; gap: 10px; }
.admin-title h2 { margin: 0; font-size: 20px; font-weight: 600; color: var(--fg, #1a1a1a); }
.admin-subtitle { font-size: 13px; color: #999; }
</style>
