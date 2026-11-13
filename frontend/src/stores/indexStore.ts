// Shared state for file explorer — reactive store replacing global function pointers
import { ref, reactive } from 'vue'
import * as fileApi from '../api/files'
import * as shareApi from '../api/share'
import type { LocateTarget } from '../components/editor/CodePreview.vue'

// ============================================================
// Auth — re-export from stores/auth.ts (Single Source of Truth)
// ============================================================
export {
  authToken,
  currentUser,
  loggedIn,
  restoreAuth,
  setAuth,
  clearAuth,
  checkAuth,
} from './auth'

// ============================================================
// Editor Tabs (shared by tree → editor)
// ============================================================
export const openTabs = reactive(new Map<string, { name: string; ext: string; size: number }>())
export const activeTabPath = ref('')
const currentInnerHash = ref('')

function isValidTabPath(p: unknown): p is string {
  return typeof p === 'string' && p.length > 0
    && !p.startsWith('async ') && !p.startsWith('function ') && !p.startsWith('(');
}

// ============================================================
// 行定位（RAG 引用跳转等外部触发）：path 命中当前 tab 时由 EditorArea
// 传给 CodePreview 滚动定位。seq 保证同文件同一行重复点击也能触发。
// ============================================================
export const locateRequest = ref<(LocateTarget & { path: string }) | null>(null)
let locateSeq = 0

export function requestLocate(path: string, line: number): void {
  if (!line || line < 1) return
  locateSeq += 1
  locateRequest.value = { path, line, seq: locateSeq }
}

/**
 * 解析 hash 为文件路径 + 行号（RAG 引用跳转）。
 * 形如 "#/a/b.md" 或 "#/a/b.md#L12"；非法/穿越路径返回 null。
 */
export function parseHashTarget(h: string): { path: string; line: number } | null {
  if (!h || !h.startsWith('#/')) return null
  let raw = ''
  try { raw = decodeURIComponent(h.slice(1)) } catch { raw = h.slice(1) }
  let line = 0
  const m = raw.match(/^(.+)#L(\d+)$/)
  if (m) { raw = m[1]; line = parseInt(m[2], 10) || 0 }
  if (/(^|\/)\.\.(\/|$)/.test(raw)) return null
  if (!raw.startsWith('/')) return null
  return { path: raw, line }
}

/**
 * 打开 hash 指向的文件（可选定位行）：展开目录树 → 开 tab → 请求行定位。
 * 供 useIndexApp 的 route.hash watcher（活页触发）与 AI 引用卡片（同 hash
 * 重复点击时 hashchange 不触发，手动调用兜底）共用。
 */
export async function openFileFromHash(h: string): Promise<void> {
  const target = parseHashTarget(h)
  if (!target) return
  await treeStore.expandTo(target.path)
  const name = target.path.split('/').pop() || ''
  const ext = (name.split('.').pop() || '').toLowerCase()
  openTabFn?.(target.path, name, ext, 0)
  if (target.line > 0) requestLocate(target.path, target.line)
}

export function storeAddTab(path: string, name: string, ext: string, size: number): boolean {
  if (!isValidTabPath(path)) {
    console.error('[store] storeAddTab rejected: invalid path', typeof path, String(path).slice(0, 60));
    return false;
  }
  if (openTabs.has(path)) return false;
  openTabs.set(path, { name, ext, size });
  return true;
}

export function storeRemoveTab(path: string): void { openTabs.delete(path); }
export function storeHasTab(path: string): boolean { return openTabs.has(path); }
export function storeGetTab(path: string) { return openTabs.get(path); }
export function storeSetActiveTab(path: string): void { activeTabPath.value = path; }

// ============================================================
// Identity-scoped state reset（登录/登出/切换组织/换账号时调用）
// ============================================================
// SPA 内切换身份不刷新页面，以下内存单例会残留上一个身份的数据：
//   - 文件区：树缓存（treeStore）、tab 列表——残留会让新账号看到旧账号的目录树
//   - todo：任务列表/查看成员状态——残留会把旧账号的任务写进新账号的存储（PUT 全量替换）
// treeStore 在 composables/useTreeStore，todoStore 在 stores/todoStore；
// 本文件（indexStore）已依赖两者之外的 auth，由这里聚合重置可避免循环依赖。
import { treeStore } from '../composables/useTreeStore'
import { tasks, orgTasks, viewingMember, orgMembers } from './todoStore'

/** 重置登录身份相关的所有前端状态。auth 变化（登出/登录/切换组织）的三个调用点统一走这里。 */
export function resetIdentityScopedState(): void {
  // 文件区：树缓存 + 展开状态 + 移动剪贴板
  treeStore.dirCache.clear()
  treeStore.expanded.clear()
  treeStore.activePath.value = null
  treeStore.clipboard.value = null
  // 文件区：打开的 tab
  openTabs.clear()
  activeTabPath.value = ''
  // todo：任务列表 + 查看成员状态（避免新账号一进 todo 就处于"查看他人"只读态）
  tasks.value = []
  orgTasks.value = []
  viewingMember.value = null
  orgMembers.value = []
  // todo：/api/tasks 请求缓存（useTodoApp 闭包内，通过注册回调清理，避免依赖环）
  todoAppResetFn?.()
}

// useTodoApp 挂载时注册自己的缓存清理回调（清 _tasksJsonCache）
let todoAppResetFn: (() => void) | null = null
export function registerTodoAppReset(fn: (() => void) | null): void { todoAppResetFn = fn }
// ============================================================
// Delegated functions (set by composables/components)
// ============================================================
export let openTabFn: ((path: string, name: string, ext: string, size: number) => void) | null = null
export let loadSharesFn: (() => Promise<void>) | null = null

export function setOpenTabFn(fn: typeof openTabFn) { openTabFn = fn }
export function setLoadSharesFn(fn: typeof loadSharesFn) { loadSharesFn = fn }

// ============================================================
// Data source abstraction
// ============================================================
type DataSrcMode = 'browser' | 'share'

export const dsMode = ref<DataSrcMode>('browser')
export const dsToken = ref('')
export const dsPathPrefix = ref('')
// shareRootPath 是分享内容的根路径（share.ResourcePath）：
//   - 目录分享时 = share.ResourcePath（如 /s3_blockfile/reports/my_scenario/）
//   - 文件分享时 = ''（空，侧栏不渲染 TreeNode，只展示主区域的文件）
// 浏览器模式下为空，侧栏根节点回退到 /
// 该路径同时是 treeStore.dirCache 中缓存分享内容所用的 key，
// 避免和浏览器模式的 '/' 冲突，确保侧栏只显示分享根范围内的内容
export const shareRootPath = ref('')

// dsListDir 加载目录列表：
//   - 浏览器模式：调 /api/tree?path=<p>
//   - 分享模式：token 已唯一确定 share.ResourcePath，
//     若 p 为空或等于 shareRootPath → 调 /api/share/{token}（无 path，拿分享根本身）
//     若 p 是 shareRootPath 的子路径 → 调 /api/share/{token}?path=<p>（浏览子目录）
export async function dsListDir(p: string): Promise<any> {
  if (dsMode.value === 'share') {
    const root = shareRootPath.value
    // p 为空，或 p 等于分享根 → 不传 path（token 本身就定位到分享根）
    const subPath = (!p || p === '/' || p === root) ? undefined : p
    return shareApi.getShareData(dsToken.value, subPath)
  }
  return fileApi.getTree(p)
}

// ============================================================
// Hash routing
// ============================================================
export function updateHash(p: string) {
  try {
    const url = '#' + String(p) + (currentInnerHash.value ? '#' + currentInnerHash.value : '')
    history.replaceState(null, '', url)
  } catch (e) {
    console.warn('updateHash failed:', e)
  }
}


