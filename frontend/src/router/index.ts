import { createRouter, createWebHistory } from 'vue-router'
import IndexView from '../views/IndexView.vue'
import ExplorerSidebar from '../components/sidebar/ExplorerSidebar.vue'
import SharesSidebar from '../components/sidebar/SharesSidebar.vue'

// SPA 路由（命名视图：default=主区域, sidebar=侧栏面板）
//   /          → 文件浏览器     → main: IndexView,  sidebar: ExplorerSidebar
//   /shares    → 分享管理       → main: IndexView,  sidebar: SharesSidebar
//   /todo      → 看板           → main: TodoView,   sidebar: TodoSidebar (task list)
//   /s/:token  → 分享预览       → main: IndexView,  sidebar: ExplorerSidebar
// 注意：/todo 路由的组件必须用动态 import，否则 todoStore 的 formatTasks 会被
// 打入主 bundle，与 common.ts 的 extFromPath/baseName 产生压缩名冲突 (ee)，
// 导致主界面"X.split is not a function"运行时错误。
const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      components: { default: IndexView, sidebar: ExplorerSidebar },
    },
    {
      path: '/shares',
      components: { default: IndexView, sidebar: SharesSidebar },
    },
    {
      path: '/todo',
      components: {
        default: () => import('../views/TodoView.vue'),
        sidebar: () => import('../components/sidebar/TodoSidebar.vue'),
      },
    },
    {
      path: '/s/:token',
      components: { default: IndexView, sidebar: ExplorerSidebar },
    },
    {
      path: '/s/:token/:path(.*)*',
      components: { default: IndexView, sidebar: ExplorerSidebar },
    },
  ],
})

export default router
