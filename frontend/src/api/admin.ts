// 用户管理（admin）相关 API
import { apiCall } from '../lib/common'

// 用户完整信息（对应后端 db.UserInfo）
export interface AdminUser {
  id: number
  orgId: number
  orgName: string
  name: string
  mobile: string
  roleId: number
  role: string
  createdAt: string
}

// 角色（对应后端 db.Role）
export interface Role {
  id: number
  name: string
  description: string
}

// 用户看板统计（对应后端 db.UserDashboard）
export interface UserDashboard {
  totalTasks: number
  doneTasks: number
  shareCount: number
}

// 列出用户（GET /api/admin/users），orgId 可选（超级 admin 过滤某 org）
export async function listUsers(orgId?: number): Promise<{ users: AdminUser[] }> {
  const q = orgId ? `?orgId=${orgId}` : ''
  return apiCall('/api/admin/users' + q)
}

// 列出所有角色（GET /api/admin/roles）
export async function listRoles(): Promise<{ roles: Role[] }> {
  return apiCall('/api/admin/roles')
}

// 创建用户（POST /api/admin/users）
export async function createUser(body: {
  org: string
  name: string
  password: string
  roleId: number
  mobile?: string
}): Promise<AdminUser> {
  return apiCall('/api/admin/users', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// 更新用户（PATCH /api/admin/users/{id}），可选字段传哪个改哪个
export async function updateUser(
  id: number,
  body: { name?: string; mobile?: string; password?: string; roleId?: number },
): Promise<AdminUser> {
  return apiCall(`/api/admin/users/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// 删除用户（DELETE /api/admin/users/{id}）
export async function deleteUser(id: number): Promise<{ deleted: number }> {
  return apiCall(`/api/admin/users/${id}`, { method: 'DELETE' })
}

// 用户看板统计（GET /api/admin/users/{id}/dashboard）
export async function getUserDashboard(id: number): Promise<UserDashboard> {
  return apiCall(`/api/admin/users/${id}/dashboard`)
}
