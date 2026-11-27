// 分享功能统一入口：创建弹窗 / 密码弹窗开合 + 列表刷新通知。
// ShareModal / SharePasswordModal / SharesSidebar 只读这里的响应式状态，不再互相注册全局函数或回调。
import { ref } from 'vue'
import { activeTabPath } from '../stores/indexStore'
import { authToken } from '../stores/auth'
import { showToast } from '../lib/common'

export type ShareResourceType = 'file' | 'dir'

export const shareModalVisible = ref(false)
export const shareResourceType = ref<ShareResourceType>('file')

// 分享列表版本号：创建/撤销后自增，SharesSidebar watch 后重新拉取
export const sharesRevision = ref(0)

export function openShareModal(resourceType: ShareResourceType) {
  if (!authToken.value) {
    const current = window.location.pathname + window.location.search
    window.location.href = '/login?redirect=' + encodeURIComponent(current)
    return
  }
  if (!activeTabPath.value) { showToast('Select a file first'); return }
  shareResourceType.value = resourceType
  shareModalVisible.value = true
}

export function closeShareModal() {
  shareModalVisible.value = false
}

export function notifySharesChanged() {
  sharesRevision.value++
}

// ---- 访问分享时的密码弹窗 ----
export const sharePasswordVisible = ref(false)
export const sharePasswordError = ref('')

let resolvePassword: ((pwd: string | null) => void) | null = null

export function showSharePasswordModal(err?: string): Promise<string | null> {
  sharePasswordError.value = err || ''
  sharePasswordVisible.value = true
  return new Promise(resolve => { resolvePassword = resolve })
}

export function submitSharePassword(pwd: string | null) {
  sharePasswordVisible.value = false
  resolvePassword!(pwd)
  resolvePassword = null
}
