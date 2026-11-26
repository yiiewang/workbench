import { createRouter, createWebHistory } from 'vue-router'
import { loggedIn, canManageUsers } from '../stores/auth'
import IndexView from '../views/IndexView.vue'
import ExplorerSidebar from '../components/sidebar/ExplorerSidebar.vue'
import SharesSidebar from '../components/sidebar/SharesSidebar.vue'

// SPA 路由（嵌套布局）：
//   /login              → 独立页面（无布局），公开
//   /s/:token           → 分享预览（无布局守卫），公开
//   /                   → PC 功能首页（各功能入口卡片，无侧栏）
//   /file  /shares  /todo  /admin → PC 应用界面（DefaultLayout 包裹），需登录
//   /m/*                → 移动端专属 UI（MobileLayout + 独立页面，功能做减法）
//
// 设备分流：PC 路径 + 移动设备视口 → redirect /m 对应页；/m + 桌面 → redirect PC。
// 移动端与 PC 共用 api/stores/lib 与纯展示组件，仅视图层独立。
const router = createRouter({
  history: createWebHistory(),
  routes: [
    // 独立页面：不走 DefaultLayout
    {
      path: '/login',
      component: () => import('../views/LoginView.vue'),
      meta: { public: true },
    },
    // 移动端专属 UI（MobileLayout：底部导航 + 全屏页面，无侧栏/抽屉/tab 概念）
    {
      path: '/m',
      component: () => import('../views/mobile/MobileLayout.vue'),
      children: [
        { path: '', component: () => import('../views/mobile/MHome.vue') },
        { path: 'files', component: () => import('../views/mobile/MFiles.vue') },
        { path: 'file', component: () => import('../views/mobile/MFileView.vue') },
        { path: 'ai', component: () => import('../views/mobile/MAI.vue') },
        { path: 'todo', component: () => import('../views/mobile/MTodo.vue') },
        { path: 'profile', component: () => import('../views/mobile/MProfile.vue') },
        // 分享访问（公开）：目录列表/文件预览统一，isDir=false 自动预览
        { path: 's/:token', component: () => import('../views/mobile/MShareView.vue'), meta: { public: true } },
        { path: 's/:token/:path(.*)*', component: () => import('../views/mobile/MShareView.vue'), meta: { public: true } },
      ],
    },
    // 应用界面：DefaultLayout 包裹，子路由用命名视图
    {
      path: '/',
      component: () => import('../layouts/DefaultLayout.vue'),
      children: [
        {
          // 功能首页：入口卡片聚合页，无侧栏（hideSidebar 由 DefaultLayout 识别）
          path: '',
          component: () => import('../views/HomeView.vue'),
          meta: { hideSidebar: true },
        },
        {
          path: 'file',
          components: { default: IndexView, sidebar: ExplorerSidebar },
          meta: { sidebarTitle: 'EXPLORER' },
        },
        {
          path: 'shares',
          components: { default: IndexView, sidebar: SharesSidebar },
          meta: { sidebarTitle: 'MY SHARES' },
        },
        {
          path: 'todo',
          components: {
            default: () => import('../views/TodoView.vue'),
            sidebar: () => import('../components/sidebar/TodoSidebar.vue'),
          },
          meta: { sidebarTitle: 'TASKS' },
        },
        {
          path: 'jobs',
          components: {
            default: () => import('../views/AiJobsView.vue'),
            sidebar: () => import('../components/sidebar/AiJobsSidebar.vue'),
          },
          meta: { sidebarTitle: 'AI JOBS' },
        },
        {
          path: 'admin',
          components: {
            default: () => import('../views/AdminView.vue'),
            sidebar: () => import('../components/sidebar/AdminSidebar.vue'),
          },
          meta: { requiresAdmin: true, sidebarTitle: 'USER MANAGEMENT' },
        },
        {
          path: 's/:token',
          components: { default: IndexView, sidebar: ExplorerSidebar },
          meta: { public: true },
        },
        {
          path: 's/:token/:path(.*)*',
          components: { default: IndexView, sidebar: ExplorerSidebar },
          meta: { public: true },
        },
      ],
    },
  ],
})

// ============================================================
// 设备分流：PC ↔ 移动端（/m）路径映射
// ============================================================

// 移动设备判定：窄视口（与全局断点 900px 一致）；桌面窄窗口同样适合展示型布局
function isMobileViewport(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(max-width: 900px)').matches
}

// PC → 移动端路径映射（移动端砍掉的功能回移动首页）
const PC_TO_MOBILE: Record<string, string> = {
  '/': '/m',
  '/file': '/m/files',
  '/todo': '/m/todo',
  '/shares': '/m',
  '/admin': '/m',
}
// 移动端 → PC 路径映射（桌面打开移动链接时回 PC 对应页）
const MOBILE_TO_PC: Record<string, string> = {
  '/m': '/',
  '/m/files': '/file',
  '/m/todo': '/todo',
  '/jobs': '/jobs',
  '/m/ai': '/',
  '/m/profile': '/',
}

// 路由守卫：设备分流 + 登录拦截。
// 登录页不分流（已自带适配）；分享页移动端有专属视图（/m/s/*）。
router.beforeEach((to) => {
  const mobile = isMobileViewport()
  // 分享页分流（带 token 的动态路径，前缀改写）
  if (mobile && to.path.startsWith('/s/')) {
    return { path: '/m' + to.path, replace: true }
  }
  if (!mobile && to.path.startsWith('/m/s/')) {
    return { path: to.path.slice(2), replace: true }
  }
  if (to.path !== '/login') {
    if (mobile && !to.path.startsWith('/m')) {
      const target = PC_TO_MOBILE[to.path]
      if (target) return { path: target, replace: true }
    }
    if (!mobile && to.path.startsWith('/m')) {
      const target = MOBILE_TO_PC[to.path]
      if (target) return { path: target, replace: true }
    }
  }
  if (to.meta.public) return true
  if (!loggedIn.value) return { path: '/login', query: { redirect: to.fullPath } }
  if (to.meta.requiresAdmin && !canManageUsers.value) return { path: '/' }
  return true
})

export default router
