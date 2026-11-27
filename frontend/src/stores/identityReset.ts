// 登录身份相关的全局状态重置：登出 / 登录 / 切换组织 / 换账号时调用。
//
// 单独成文件而不是挂在 indexStore：useTreeStore 依赖 indexStore（dsListDir），
// 若 indexStore 反向 import treeStore 会形成 indexStore ↔ useTreeStore 的循环。
// 这里作为聚合层，允许同时依赖 indexStore、treeStore 与 todoStore。
import { treeStore } from '../composables/useTreeStore'
import { tasks, orgTasks, viewingMember, orgMembers } from './todoStore'
import { openTabs, activeTabPath } from './indexStore'

// useTodoApp 挂载时注册自己的缓存清理回调（清 /api/tasks 请求缓存）
let todoAppResetFn: (() => void) | null = null
export function registerTodoAppReset(fn: (() => void) | null): void { todoAppResetFn = fn }

/**
 * 重置登录身份相关的所有前端状态（SPA 内切换身份不刷新页面，内存单例会残留）：
 *   - 文件区：树缓存、展开状态、移动剪贴板、打开的 tab
 *   - todo：任务列表 + 查看成员状态（避免新账号一进 todo 就处于「查看他人」只读态）
 *   - todo：/api/tasks 请求缓存（useTodoApp 闭包内，经注册回调清理）
 */
export function resetIdentityScopedState(): void {
  treeStore.dirCache.clear()
  treeStore.expanded.clear()
  treeStore.activePath.value = null
  treeStore.clipboard.value = null
  openTabs.clear()
  activeTabPath.value = ''
  tasks.value = []
  orgTasks.value = []
  viewingMember.value = null
  orgMembers.value = []
  todoAppResetFn?.()
}
