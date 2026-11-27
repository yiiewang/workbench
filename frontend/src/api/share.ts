// 分享相关 API
import { apiCall } from '../lib/common'

export interface CreateShareBody {
  resourcePath: string
  resourceType: 'file' | 'dir'
  maxAccessCount: number
  password: string
  remark: string
  effectiveAt: string
  expiresAt: string
}

export interface ShareItem {
  id: string
  token: string
  resourcePath: string
  resourceType: 'file' | 'dir'
  hasPassword: boolean
  maxAccessCount: number
  accessCount: number
  expiresAt: string
  remark: string
  createdAt: string
}

/** 分享访问响应：目录分享给 dirs/files，文件分享给文件元信息与内容；字段按资源类型出现 */
export interface ShareData {
  resourcePath?: string
  isDir?: boolean
  dirs?: { name: string }[]
  files?: { name: string; size?: number }[]
  fileName?: string
  ext?: string
  size?: number
  contentType?: string
  content?: string
  isBinary?: boolean
}

// 创建分享（POST /api/share）
export async function createShare(body: CreateShareBody): Promise<{ token: string }> {
  return apiCall('/api/share', {
    method: 'POST',
    json: body,
  })
}

// 列出我的分享（GET /api/share）
export async function listShares(): Promise<{ shares: ShareItem[] }> {
  return apiCall('/api/share')
}

// 删除分享（DELETE /api/share/{id}）
export async function deleteShare(id: string) {
  return apiCall(`/api/share/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// 访问分享数据（GET/POST /api/share/{token}）
//   GET  — 获取目录列表或文件元信息
//   POST — 获取文件内容（带密码时用 POST）
export async function getShareData(token: string, path?: string, password?: string): Promise<ShareData> {
  let url = `/api/share/${encodeURIComponent(token)}`
  if (path) url += `?path=${encodeURIComponent(path)}`
  const opts: RequestInit = {}
  if (password) {
    opts.method = 'POST'
    opts.headers = { 'Content-Type': 'application/json' }
    opts.body = JSON.stringify({ password })
  }
  return apiCall(url, opts)
}
