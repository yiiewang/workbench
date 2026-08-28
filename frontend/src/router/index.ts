import { createRouter, createWebHistory } from 'vue-router'
import { loggedIn, canManageUsers } from '../stores/auth'
import IndexView from '../views/IndexView.vue'
import ExplorerSidebar from '../components/sidebar/ExplorerSidebar.vue'
import SharesSidebar from '../components/sidebar/SharesSidebar.vue'

// SPA 路由（嵌套布局）：
//   /login              → 独立页面（无布局），公开
//   /s/:token           → 分享预览（无布局守卫），公开
//   /  /shares  /todo   → 应用界面（DefaultLayout 包裹），需登录
//
// 命名视图：default=主区域, sidebar=侧栏面板（在 DefaultLayout 中渲染）
//
// 注意：/todo 路由的组件必须用动态 import，否则 todoStore 的 formatTasks 会被
// 打入主 bundle，与 common.ts 的 extFromPath/baseName 产生压缩名冲突 (ee)，
// 导致主界面"X.split is not a function"运行时错误。
const router = createRouter({
  history: createWebHistory(),
  routes: [
    // 独立页面：不走 DefaultLayout
    {
      path: '/login',
      component: () => import('../views/LoginView.vue'),
      meta: { public: true },
    },
    // 应用界面：DefaultLayout 包裹，子路由用命名视图
    {
      path: '/',
      component: () => import('../layouts/DefaultLayout.vue'),
      children: [
        {
          path: '',
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

// 路由守卫：未登录用户访问受保护页面时跳转登录页，带 redirect 参数。
// 公开路由（/login 和 /s/:token 分享页）不拦截；/admin 额外要求 admin 角色。
router.beforeEach((to) => {
  if (to.meta.public) return true
  if (!loggedIn.value) return { path: '/login', query: { redirect: to.fullPath } }
  if (to.meta.requiresAdmin && !canManageUsers.value) return { path: '/' }
  return true
})

export default router
