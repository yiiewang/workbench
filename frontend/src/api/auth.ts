// 认证相关 API
import { apiCall } from '../lib/common'

// 登录（POST /api/login）
export async function login(userId: string, password: string, orgId?: string) {
  return apiCall('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user_id: userId, password, org_id: orgId || '' }),
  })
}

// 设置密码（POST /api/set-password）
export async function setPassword(userId: string, password: string) {
  return apiCall('/api/set-password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user_id: userId, password }),
  })
}
