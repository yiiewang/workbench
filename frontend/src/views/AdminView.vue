<script setup lang="ts">
// Admin 用户管理主区：内联详情表单（新建/编辑/重置密码/删除，无弹窗）
// 与侧栏通过 adminStore 共享状态：选中列表项 → 此处显示详情表单
import { ref, computed, watch } from 'vue'
import { showToast } from '../lib/common'
import * as adminApi from '../api/admin'
import {
  roles,
  selectedUser,
  isNew,
  loadAdminData,
  selectUser,
  clearSelectionIfDeleted,
} from '../stores/adminStore'
import { isAdmin, currentUser } from '../stores/auth'

const saving = ref(false)
const deleting = ref(false)
const showChangePwd = ref(false)

// 本地编辑表单（从选中用户派生，避免直接改 store）
const form = ref({
  name: '',
  org: '',
  mobile: '',
  roleId: 2,
  password: '',
})

// 可选角色：org_admin 不能授予 admin 角色（id=1）
const selectableRoles = computed(() =>
  roles.value.filter((r) => isAdmin.value || r.id !== 1),
)

// 标题
const title = computed(() => {
  if (isNew.value) return 'New User'
  return selectedUser.value ? `Edit: ${selectedUser.value.name}` : 'User Detail'
})

// 监听选中/新建状态，初始化表单
watch([selectedUser, isNew], () => {
  showChangePwd.value = false
  if (isNew.value) {
    form.value = {
      name: '',
      org: isAdmin.value ? '' : (currentUser.value?.orgName || ''),
      mobile: '',
      roleId: 2,
      password: '',
    }
  } else if (selectedUser.value) {
    const u = selectedUser.value
    form.value = {
      name: u.name,
      org: u.orgName,
      mobile: u.mobile || '',
      roleId: u.roleId,
      password: '',
    }
  }
}, { immediate: true })

// 校验：name 必填；新建时 org + password 必填
const canSave = computed(() => {
  if (!form.value.name.trim()) return false
  if (isNew.value) {
    if (!form.value.org.trim() || !form.value.password) return false
  }
  return true
})

// org 是否只读（org_admin 只能操作自己 org）
const orgReadonly = computed(() => !isAdmin.value)

async function onSave() {
  if (!canSave.value) return
  saving.value = true
  try {
    if (isNew.value) {
      const created = await adminApi.createUser({
        org: form.value.org.trim(),
        name: form.value.name.trim(),
        password: form.value.password,
        roleId: form.value.roleId,
        mobile: form.value.mobile.trim(),
      })
      showToast('User created')
      await loadAdminData()
      selectUser(created.id) // 新建后选中该用户
    } else if (selectedUser.value) {
      const id = selectedUser.value.id
      const body: any = {
        name: form.value.name.trim(),
        mobile: form.value.mobile.trim(),
        roleId: form.value.roleId,
      }
      // 展开改密码且填了新密码时，一并提交
      if (showChangePwd.value && form.value.password) {
        body.password = form.value.password
      }
      await adminApi.updateUser(id, body)
      showToast('User updated')
      await loadAdminData()
      showChangePwd.value = false
    }
  } catch (err: any) {
    showToast('Save failed: ' + (err.msg || err.message))
  } finally {
    saving.value = false
  }
}

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
  <div class="admin-detail">
    <!-- 空状态 -->
    <div v-if="!isNew && !selectedUser" class="empty-state">
      <div class="empty-icon">👥</div>
      <div class="empty-title">No user selected</div>
      <div class="empty-hint">Select a user from the left, or create a new one.</div>
    </div>

    <!-- 详情 / 新建表单 -->
    <div v-else class="detail-panel">
      <div class="detail-header">
        <h2 class="detail-title">{{ title }}</h2>
        <button class="btn btn-primary" :disabled="!canSave || saving" @click="onSave">
          {{ isNew ? 'Create' : 'Save' }}
        </button>
      </div>

      <div class="detail-body">
        <div class="form-row">
          <label class="form-label">Name</label>
          <input v-model="form.name" class="form-input" placeholder="user name" spellcheck="false" />
        </div>

        <div class="form-row">
          <label class="form-label">Org</label>
          <input v-model="form.org" class="form-input" :readonly="orgReadonly" placeholder="org name" spellcheck="false" />
        </div>

        <div class="form-row">
          <label class="form-label">Role</label>
          <select v-model="form.roleId" class="form-input">
            <option v-for="r in selectableRoles" :key="r.id" :value="r.id">{{ r.name }}</option>
          </select>
        </div>

        <div class="form-row">
          <label class="form-label">Mobile</label>
          <input v-model="form.mobile" class="form-input" placeholder="optional" spellcheck="false" />
        </div>

        <!-- 新建：密码必填 -->
        <div v-if="isNew" class="form-row">
          <label class="form-label">Password</label>
          <input v-model="form.password" type="password" class="form-input" placeholder="required" />
        </div>

        <!-- 编辑：展示创建时间 + 危险区 -->
        <template v-else>
          <div class="form-row">
            <label class="form-label">Created</label>
            <div class="readonly-text">{{ selectedUser?.createdAt }}</div>
          </div>

          <div class="danger-zone">
            <!-- 改密码 -->
            <div v-if="!showChangePwd" class="danger-row">
              <button class="btn btn-sm" @click="showChangePwd = true">Change Password</button>
            </div>
            <div v-else class="danger-row pwd-row">
              <input
                v-model="form.password"
                type="password"
                class="form-input"
                placeholder="new password"
              />
              <button class="btn btn-sm" @click="showChangePwd = false">Cancel</button>
            </div>

            <!-- 删除 -->
            <div class="danger-row">
              <button class="btn btn-sm btn-danger" :disabled="deleting" @click="onDelete">
                Delete User
              </button>
            </div>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-detail {
  padding: 16px 20px;
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
  gap: 6px;
  color: var(--text-muted);
}
.empty-icon {
  font-size: 32px;
  opacity: 0.4;
}
.empty-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--text-dim);
}
.empty-hint {
  font-size: 12px;
}

/* 详情面板 */
.detail-panel {
  max-width: 560px;
}
.detail-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}
.detail-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
}

.detail-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.form-row {
  display: flex;
  align-items: center;
  gap: 12px;
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
.readonly-text {
  flex: 1;
  font-size: 12px;
  color: var(--text-dim);
}

/* 危险区 */
.danger-zone {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.danger-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.danger-row .btn {
  font-size: 12px;
}
.pwd-row .form-input {
  flex: 1;
  max-width: 240px;
}
</style>
