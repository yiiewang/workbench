import {
  storeAddTab, storeHasTab, storeSetActiveTab,
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
