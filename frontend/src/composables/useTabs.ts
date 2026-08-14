import {
  storeAddTab, storeRemoveTab, storeHasTab, storeGetTab,
  storeSetActiveTab, storeTabCount,
} from '../stores/indexStore'

/** 打开 tab：仅数据操作，DOM 渲染由 TabBar.vue 负责 */
export function openTab(path: string, name: string, ext: string, size: number): boolean {
  if (!storeAddTab(path, name, ext, size)) {
    // 已存在 → 仅激活
    if (storeHasTab(path)) storeSetActiveTab(path)
    return false
  }
  storeSetActiveTab(path)
  return true
}

/** 关闭 tab */
export function closeTab(path: string): void {
  storeRemoveTab(path)
}

/** 激活 tab */
export function activateTab(path: string): void {
  storeSetActiveTab(path)
}

/** 关闭后切到上一个 tab，无 tab 时清空 */
export function closeAndActivatePrev(path: string): string {
  storeRemoveTab(path)
  // 需要外部传入剩余 tab 列表
  return ''
}
