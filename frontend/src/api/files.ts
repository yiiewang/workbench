// 文件相关 API
import { apiCall, authFetch } from '../lib/common'

// 获取目录树（GET /api/tree?path=...）— 只接受目录路径
export async function getTree(path: string) {
  return apiCall(`/api/tree?path=${encodeURIComponent(path)}`)
}

// 获取文件内容（GET /api/file?path=...）— 返回 Response（text/blob 流）
export async function getFile(path: string) {
  const resp = await authFetch(`/api/file?path=${encodeURIComponent(path)}`)
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
  return resp
}
