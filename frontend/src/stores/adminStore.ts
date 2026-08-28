// 用户管理页共享状态：侧栏列表与主区看板通过本模块共享响应式状态
import { ref, computed } from 'vue'
import * as adminApi from '../api/admin'
import type { AdminUser, Role, UserDashboard } from '../api/admin'

// 原始数据
export const users = ref<AdminUser[]>([])
export const roles = ref<Role[]>([])

// 选中状态：selectedId = 用户 id（查看/编辑）
export const selectedId = ref<number | null>(null)

// 新建用户弹窗显隐（侧栏与主区共用，点击 + New User 直接弹窗，不影响选中状态）
export const createVisible = ref(false)

// 侧栏搜索关键词（按 name / orgName 过滤）
export const searchQuery = ref('')

// 过滤后的列表
export const filteredUsers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return users.value
  return users.value.filter(
    (u) =>
      u.name.toLowerCase().includes(q) ||
      u.orgName.toLowerCase().includes(q),
  )
})

// 当前选中的用户对象（从 users 派生，保证数据刷新后同步）
export const selectedUser = computed<AdminUser | null>(() => {
  if (selectedId.value == null) return null
  return users.value.find((u) => u.id === selectedId.value) ?? null
})

// 当前选中用户的看板统计
export const dashboard = ref<UserDashboard | null>(null)

// 加载选中用户的看板统计
export async function loadDashboard(id: number) {
  try {
    dashboard.value = await adminApi.getUserDashboard(id)
  } catch {
    dashboard.value = null
  }
}

// 加载用户与角色
export async function loadAdminData(orgId?: number) {
  const [u, r] = await Promise.all([adminApi.listUsers(orgId), adminApi.listRoles()])
  users.value = u.users || []
  roles.value = r.roles || []
}

// 选中某个用户（进入看板，自动加载统计）
export function selectUser(id: number) {
  selectedId.value = id
  loadDashboard(id)
}

// 打开新建用户弹窗（不清空选中，main 看板保持不变）
export function openCreate() {
  createVisible.value = true
}

// 关闭新建用户弹窗
export function closeCreate() {
  createVisible.value = false
}

// 选中项被删除后，清除选中状态
export function clearSelectionIfDeleted(id: number) {
  if (selectedId.value === id) {
    selectedId.value = null
  }
}
