// 认证相关 API
import { apiCall } from '../lib/common'

// 登录（POST /api/login）
// 注意：字段名必须用 camelCase（orgId/userId），与后端 server.go handleLogin 结构体定义一致
export async function login(userId: string, password: string, orgId?: string) {
  return apiCall('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ orgId: orgId || '', userId, password }),
  })
}

// 设置密码（POST /api/set-password）
// 注意：后端 handleSetPassword 字段名为 newPassword（非 password），此前写错导致永远 400
export async function setPassword(userId: string, password: string, orgId?: string) {
  return apiCall('/api/set-password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ orgId: orgId || '', userId, newPassword: password }),
  })
}

// ============================================================
// 组织上下文（RBAC 阶段 C）
// ============================================================

// 组织信息（与后端 db.UserOrgContext 对应，内部角色 owner/admin/member）
export interface OrgInfo {
  orgId: number
  orgName: string
  role: string
  status: string
  features: string[]
}

// userinfo 聚合接口载荷（与后端 userinfoData 对应）
export interface UserInfo {
  user: {
    userId: number
    userName: string
    mobile: string
    isPlatformAdmin: boolean
  }
  orgs: OrgInfo[]
  currentOrgId: number
  role: string
  features: string[]
}

// 聚合接口：一次返回用户全部上下文（GET /api/userinfo）
export async function fetchUserInfo(): Promise<UserInfo> {
  return apiCall('/api/userinfo')
}

// 组织切换（POST /api/org/switch）
export async function switchOrg(orgId: number) {
  return apiCall('/api/org/switch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ orgId }),
  })
}

// 当前组织功能列表（GET /api/org/features）
export async function fetchFeatures(): Promise<{ features: string[] }> {
  return apiCall('/api/org/features')
}
