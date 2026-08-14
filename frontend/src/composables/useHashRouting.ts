// Hash routing utilities — called directly by useIndexApp / ExplorerSidebar
import { ref } from 'vue'

export const isPopstateNavigating = ref(false)

/** 解析 URL hash 为文件路径 */
export function getHashPath(): string {
  const h = window.location.hash
  if (!h || !h.startsWith('#')) return ''
  let raw = decodeURIComponent(h.slice(1))
  if (raw === 'todo' || raw === 'shares') return ''
  const idx = raw.indexOf('#')
  if (idx >= 0) raw = raw.slice(0, idx)
  if (/(^|\/)\.\.(\/|$)/.test(raw)) { console.warn('[hash] traversal blocked', raw); return '' }
  return raw
}

/** 更新 URL hash（不触发 popstate） */
export function updateHash(path: string) {
  if (!path) return
  try {
    const url = '#' + String(path)
    if (window.location.hash === url) return
    history.replaceState(null, '', url)
  } catch {}
}

/** 清空 URL hash */
export function clearHash() {
  try { history.replaceState(null, '', window.location.pathname) } catch {}
}
