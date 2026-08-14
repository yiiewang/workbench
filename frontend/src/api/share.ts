// 分享相关 API
import { apiCall, authFetch } from '../lib/common'

// 创建分享（POST /api/share）
export async function createShare(body: any) {
  return apiCall('/api/share', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// 列出我的分享（GET /api/share）
export async function listShares() {
  return apiCall('/api/share')
}

// 删除分享（DELETE /api/share/{id}）
export async function deleteShare(id: string) {
  return apiCall(`/api/share/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// 访问分享数据（GET/POST /api/share/{token}）
//   GET  — 获取目录列表或文件元信息
//   POST — 获取文件内容（带密码时用 POST）
export async function getShareData(token: string, path?: string, password?: string) {
  let url = `/api/share/${encodeURIComponent(token)}`
  if (path) url += `?path=${encodeURIComponent(path)}`
  const opts: any = {}
  if (password) {
    opts.method = 'POST'
    opts.headers = { 'Content-Type': 'application/json' }
    opts.body = JSON.stringify({ password })
  }
  return apiCall(url, opts)
}

// 原始文件流获取（分享模式下载）— 返回 Response（blob）
export async function getShareFileRaw(token: string, path?: string, password?: string) {
  let url = `/api/share/${encodeURIComponent(token)}`
  if (path) url += `?path=${encodeURIComponent(path)}`
  const opts: any = {}
  if (password) {
    opts.method = 'POST'
    opts.headers = { 'Content-Type': 'application/json' }
    opts.body = JSON.stringify({ password })
  }
  return authFetch(url, opts)
}
