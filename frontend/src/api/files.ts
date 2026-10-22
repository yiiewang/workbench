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

// 上传文件（POST /api/upload）— multipart: path(目标目录) + files(多文件)
// FormData 由浏览器自动设置 Content-Type（含 boundary），勿手动覆盖
export async function uploadFiles(dirPath: string, files: File[]) {
  const fd = new FormData()
  fd.append('path', dirPath || '/')
  for (const f of files) fd.append('files', f)
  return apiCall('/api/upload', { method: 'POST', body: fd })
}

// 新建目录（POST /api/mkdir）— path 为完整目标路径（父目录/新目录名），单层
export function createDir(path: string) {
  return apiCall('/api/mkdir', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path }),
  })
}

// 删除文件/目录（DELETE /api/file?path=...）— 目录递归删除，不可恢复
export function deletePath(path: string) {
  return apiCall(`/api/file?path=${encodeURIComponent(path)}`, { method: 'DELETE' })
}

// 重命名（POST /api/rename）— 同目录改名
export function renamePath(path: string, newName: string) {
  return apiCall('/api/rename', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, newName }),
  })
}

// 移动（POST /api/move）— 把 path 移动到 destDir 目录下（保持原名）
export function movePath(path: string, destDir: string) {
  return apiCall('/api/move', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, destDir }),
  })
}
