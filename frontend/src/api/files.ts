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

// 获取媒体播放地址（GET /api/media/ticket?path=...）— 换取带短时效 ticket 的 URL + 文件大小。
// <video>/<audio> 的 src 请求由浏览器直接发起，带不上 Authorization 头，所以不能直接
// 指向 /api/file；ticket URL 自带凭证，且后端支持 Range，可拖动进度条、不必整文件入内存。
// 失败（非媒体 / 无权限 / 分享模式）返回 null，调用方需回退到 blob 播放。
export async function getMediaTicket(path: string): Promise<{ url: string; size: number } | null> {
  try {
    const data: any = await apiCall(`/api/media/ticket?path=${encodeURIComponent(path)}`)
    if (!data || !data.url) return null
    return { url: String(data.url), size: Number(data.size) || 0 }
  } catch {
    return null
  }
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
