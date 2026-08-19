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
  saveAuthState,
  clearAuthState,
  restoreUser,
  authHeaders,
  API_CODE,
} from '../lib/common'

// ============================================================
// State — 模块加载时从 localStorage 恢复，避免刷新闪烁
// ============================================================

/** 当前 Bearer token（从 localStorage 恢复，无则空串） */
export const authToken = ref<string>(getAuthToken() || '')

/** 认证用户信息：整数 id + 业务 name（双轨架构） */
export interface AuthUser {
  userId: number
  orgId: number
  userName: string
  orgName: string
}

/** 当前登录用户（从 localStorage 恢复，无则 null） */
export const currentUser = ref<AuthUser | null>(
  restoreUser() as AuthUser | null,
)

/** 是否已登录（token + user 均存在） */
export const loggedIn = computed(() => !!authToken.value && !!currentUser.value)

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
}

/**
 * 向后端校验 token 有效性，成功则刷新 currentUser。
 *
 * - 401：清除认证状态，返回 false
 * - 200 且 data.userId 存在：刷新 currentUser，返回 true
 * - 网络错误（无 status）：保留现有状态，返回 token 是否非空（离线兜底）
 *
 * @returns token 是否有效
 */
export async function checkAuth(): Promise<boolean> {
  const token = authToken.value
  if (!token) return false

  try {
    const resp = await fetch('/api/me', { headers: { Authorization: 'Bearer ' + token } })

    if (resp.status === 401) {
      clearAuth()
      return false
    }

    if (resp.ok) {
      const body = await resp.json()
      if (body.data && body.data.userId) {
        // 刷新 user 信息（后端返回整数 id 与 name）
        const user: AuthUser = {
          userId: body.data.userId,
          orgId: body.data.orgId,
          userName: body.data.userName,
          orgName: body.data.orgName,
        }
        saveAuthState(token, user)
        currentUser.value = user
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
