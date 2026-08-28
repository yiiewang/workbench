// ============================================================
// auth.ts — 认证状态唯一真相源 (Single Source of Truth)
//
// authToken + currentUser 在整个前端只存在这一份 ref。
// indexStore / todoStore 通过 re-export 引用，消费者无感知。
//
// 所有写入操作（登录成功 / 登出 / token 刷新失败）必须通过
// setAuth / clearAuth / checkAuth 三个 action 完成，
// 禁止外部直接赋值 ref.value，避免遗漏 localStorage 同步。
// ============================================================
import { ref, computed } from 'vue'
import {
  getAuthToken,
  getCurrentOrgId,
  setCurrentOrgId,
  getFeatures,
  setFeatures as persistFeatures,
  saveAuthState,
  clearAuthState,
  restoreUser,
  authHeaders,
  API_CODE,
} from '../lib/common'
import { fetchUserInfo, fetchFeatures, switchOrg as apiSwitchOrg, type OrgInfo } from '../api/auth'

// ============================================================
// State — 模块加载时从 localStorage 恢复，避免刷新闪烁
// ============================================================

/** 当前 Bearer token（从 localStorage 恢复，无则空串） */
export const authToken = ref<string>(getAuthToken() || '')

/** 认证用户信息：整数 id + 业务 name + 角色（双轨架构） */
export interface AuthUser {
  userId: number
  orgId: number
  userName: string
  orgName: string
  role: string
  isPlatformAdmin?: boolean
}

/** 当前登录用户（从 localStorage 恢复，无则 null） */
export const currentUser = ref<AuthUser | null>(
  restoreUser() as AuthUser | null,
)

/** 用户绑定的所有组织（各含内部角色/状态/功能），组织切换器与权限展示用 */
export const orgs = ref<OrgInfo[]>([])

/** 当前组织启用的功能列表，动态菜单渲染用（localStorage 持久化，刷新不丢） */
export const features = ref<string[]>(getFeatures())

/** 当前组织 id（localStorage 持久化，刷新不丢；无则 null，后端取默认组织） */
export const currentOrgId = ref<number | null>(getCurrentOrgId())

/** 是否已登录（token + user 均存在） */
export const loggedIn = computed(() => !!authToken.value && !!currentUser.value)

/** 是否超级管理员（admin 角色） */
export const isAdmin = computed(() => currentUser.value?.role === 'admin')

/** 是否组织管理员（org_admin 角色） */
export const isOrgAdmin = computed(() => currentUser.value?.role === 'org_admin')

/** 是否具备用户管理权限（admin 或 org_admin） */
export const canManageUsers = computed(() => isAdmin.value || isOrgAdmin.value)

/** 是否启用某功能（feature code: file/share/todo/admin） */
export function hasFeature(code: string): boolean {
  return features.value.includes(code)
}

// ============================================================
// Actions
// ============================================================

/**
 * 从 localStorage 恢复 token + user 到响应式 state。
 * 用于应用启动时或需要强制同步的场景。
 */
export function restoreAuth(): void {
  authToken.value = getAuthToken() || ''
  currentUser.value = restoreUser() as AuthUser | null
}

/**
 * 登录成功后设置认证状态。
 * 同时写入 localStorage（持久化）和响应式 ref（驱动 UI）。
 *
 * @param token - 后端返回的 Bearer token
 * @param user  - 用户信息 { userId, orgId, userName, orgName }
 */
export function setAuth(token: string, user: AuthUser): void {
  saveAuthState(token, user)
  authToken.value = token
  currentUser.value = user
}

/**
 * 清除认证状态（登出 / token 过期）。
 * 同时清除 localStorage 和响应式 ref。
 */
export function clearAuth(): void {
  clearAuthState()
  authToken.value = ''
  currentUser.value = null
  orgs.value = []
  features.value = []
  persistFeatures(null)
  currentOrgId.value = null
}

/**
 * 向后端校验 token 有效性，成功则刷新 currentUser + orgs + features。
 * 改调聚合接口 /api/userinfo，一次拉全上下文（替代原 /api/me）。
 *
 * - 401：清除认证状态，返回 false
 * - 200 且 data.user 存在：刷新 currentUser / orgs / features，返回 true
 * - 网络错误（无 status）：保留现有状态，返回 token 是否非空（离线兜底）
 *
 * @returns token 是否有效
 */
export async function checkAuth(): Promise<boolean> {
  const token = authToken.value
  if (!token) return false

  try {
    const resp = await fetch('/api/userinfo', { headers: authHeaders() })

    if (resp.status === 401) {
      clearAuth()
      return false
    }

    if (resp.ok) {
      const body = await resp.json()
      const data = body.data
      if (data && data.user) {
        applyUserInfo(token, data)
        return true
      }
    }

    // 非 401 的异常 HTTP 状态（如 500），保留现有状态
    return authToken.value !== ''
  } catch {
    // 网络错误（服务器不可达）：若有 token 则允许离线访问
    return authToken.value !== ''
  }
}

/**
 * 应用 userinfo 聚合载荷到响应式 state（供 checkAuth / 登录后 / 组织切换后复用）。
 * 若本地已持久化 currentOrgId，则以本地为准（组织切换的记忆）；否则用后端 currentOrgId。
 */
function applyUserInfo(token: string, data: any): void {
  orgs.value = data.orgs || []
  features.value = data.features || []
  persistFeatures(features.value)

  // 组织 id：本地记忆优先，且必须落在 userinfo.orgs 中（防失效 id）
  const localOrg = currentOrgId.value
  const validOrg = orgs.value.find((o) => o.orgId === localOrg)
  const orgId = validOrg ? localOrg! : data.currentOrgId

  currentOrgId.value = orgId
  setCurrentOrgId(orgId)

  const org = orgs.value.find((o) => o.orgId === orgId)
  const user: AuthUser = {
    userId: data.user.userId,
    orgId: orgId,
    userName: data.user.userName,
    orgName: org ? org.orgName : '',
    role: data.role || 'user',
    isPlatformAdmin: !!data.user.isPlatformAdmin,
  }
  saveAuthState(token, user)
  currentUser.value = user
}

/**
 * 刷新当前组织的功能列表（组织功能开关被修改后，供 DefaultLayout 动态菜单即时生效）。
 */
export async function refreshFeatures(): Promise<void> {
  try {
    const data = await fetchFeatures()
    features.value = data.features || []
    persistFeatures(features.value)
  } catch {
    // 刷新失败保持现有 features，避免菜单闪烁
  }
}

/**
 * 切换当前组织：调 /api/org/switch 校验归属，成功后更新本地组织上下文与功能集合。
 *
 * @returns 是否切换成功
 */
export async function switchOrg(orgId: number): Promise<boolean> {
  try {
    const data = await apiSwitchOrg(orgId)
    currentOrgId.value = orgId
    setCurrentOrgId(orgId)
    features.value = data.features || []
    persistFeatures(features.value)
    if (currentUser.value) {
      currentUser.value.orgId = orgId
      currentUser.value.orgName = data.orgName || ''
      currentUser.value.role = data.role || currentUser.value.role
      saveAuthState(authToken.value, currentUser.value)
    }
    return true
  } catch {
    return false
  }
}
