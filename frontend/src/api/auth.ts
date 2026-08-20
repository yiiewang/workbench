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
