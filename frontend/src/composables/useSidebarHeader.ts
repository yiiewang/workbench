import { inject, provide, reactive, type InjectionKey } from 'vue'

// Sidebar header 共享状态。
// header/body 外壳由 DefaultLayout 统一定义（保证各侧栏风格一致）；
// 标题由 layout 根据路由 meta.sidebarTitle 计算，badge（右侧徽标）由各侧栏组件
// 注入后更新（如分享数量、同步状态、用户数等动态内容）。
export interface SidebarHeaderState {
  badge: string
}

const SidebarHeaderKey: InjectionKey<SidebarHeaderState> = Symbol('sidebar-header')

// layout 调用：初始化并 provide header 状态
export function useSidebarHeader(): SidebarHeaderState {
  const state = reactive<SidebarHeaderState>({ badge: '' })
  provide(SidebarHeaderKey, state)
  return state
}

// 侧栏组件调用：获取 header 状态（可写入 badge）
export function injectSidebarHeader(): SidebarHeaderState | null {
  return inject(SidebarHeaderKey, null)
}
