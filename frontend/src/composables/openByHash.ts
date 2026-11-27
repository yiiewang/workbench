// 打开 hash 指向的文件（可选定位行）：展开目录树 → 开 tab → 请求行定位。
// 供 useIndexApp 的 route.hash watcher（活页触发）与 AI 引用卡片（同 hash
// 重复点击时 hashchange 不触发，手动调用兜底）共用。
//
// 放在 composable 层而不是 indexStore：这里需要同时依赖 indexStore（openTabFn /
// requestLocate / parseHashTarget）与 treeStore，而 useTreeStore 又依赖 indexStore
// 的 dsListDir——写进 indexStore 会形成 indexStore ↔ useTreeStore 的循环。
import { openTabFn, parseHashTarget, requestLocate } from '../stores/indexStore'
import { treeStore } from './useTreeStore'

export async function openFileFromHash(h: string): Promise<void> {
  const target = parseHashTarget(h)
  if (!target) return
  await treeStore.expandTo(target.path)
  const name = target.path.split('/').pop() || ''
  const ext = (name.split('.').pop() || '').toLowerCase()
  openTabFn?.(target.path, name, ext, 0)
  if (target.line > 0) requestLocate(target.path, target.line)
}
