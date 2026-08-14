import { base64ToBytes, isBinaryExt } from '../lib/common'
import { shareRootPath } from '../stores/indexStore'
import * as fileApi from '../api/files'
import * as shareApi from '../api/share'

// ============================================================
// Data Source Abstraction
// ============================================================
export const dataSource = {
  mode: 'browser' as 'browser' | 'share',
  shareToken: '',
  sharePassword: '',

  async listDir(path: string) {
    if (this.mode === 'share') {
      // token 已唯一确定 share.ResourcePath：
      //   - path 为空或等于分享根 → 不传 path（拿分享根本身）
      //   - path 是分享根子路径 → 传 path（浏览子目录，后端校验沙箱）
      const root = shareRootPath.value
      const subPath = (path && path !== '/' && path !== root) ? path : undefined
      return shareApi.getShareData(this.shareToken, subPath, this.sharePassword)
    }
    return fileApi.getTree(path)
  },

  async getFile(path: string) {
    if (this.mode === 'share') {
      // token 已唯一确定 share.ResourcePath：
      //   - 文件分享（root 为空）或 path 等于分享根 → 不传 path
      //   - 目录分享的子文件 → 传 path
      const root = shareRootPath.value
      const subPath = (root && path !== root) ? path : undefined
      const data: any = await shareApi.getShareData(this.shareToken, subPath, this.sharePassword)
      const ext = (data.ext || '').replace('.', '').toLowerCase()
      const bytes = base64ToBytes(data.content)
      const text = data.isBinary ? null : new TextDecoder().decode(bytes)
      const blob = new Blob([bytes], { type: data.contentType })
      return { name: data.fileName, ext, size: data.size, isBinary: data.isBinary,
        text: () => Promise.resolve(text || ''),
        blob: () => Promise.resolve(blob) }
    }
    const safePath = String(path || '')
    const name = safePath.split('/').pop() || 'file'
    const ext = (name.split('.').pop() || '').toLowerCase()
    return {
      name, ext, size: 0, isBinary: isBinaryExt(ext),
      text: () => fileApi.getFile(safePath).then(r => r.text()),
      blob: () => fileApi.getFile(safePath).then(r => r.blob()),
    }
  }
}
