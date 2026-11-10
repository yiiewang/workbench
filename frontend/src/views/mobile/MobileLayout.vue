<script setup lang="ts">
// 移动端专属布局：全屏页面 + 底部导航（无侧栏/抽屉/tab 概念）。
// 功能做减法（友好展示）：首页 / 文件 / AI / Todo / 我的；
// 分享管理、用户管理等复杂功能在移动端不提供（PC 访问）。
import { useRouter } from 'vue-router'
import { loggedIn, clearAuth } from '../../stores/auth'
import { resetIdentityScopedState } from '../../stores/indexStore'

const router = useRouter()

// 底部导航定义：RouterLink 自动高亮当前项
const navs = [
  { to: '/m', label: '首页', exact: true, icon: '<path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><path d="M9 22V12h6v10"/>' },
  { to: '/m/files', label: '文件', icon: '<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>' },
  { to: '/m/ai', label: 'AI', icon: '<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>' },
  { to: '/m/todo', label: 'Todo', icon: '<path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>' },
  { to: '/m/profile', label: '我的', icon: '<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>' },
]

function doLogout() {
  clearAuth()
  resetIdentityScopedState()
  router.push('/login')
}
</script>

<template>
<div class="m-app">
  <!-- 页面区 -->
  <main class="m-main">
    <RouterView />
  </main>

  <!-- 底部导航 -->
  <nav v-if="loggedIn" class="m-tabbar">
    <RouterLink
      v-for="n in navs"
      :key="n.to"
      :to="n.to"
      class="m-tab"
      :exact-active-class="n.exact ? 'active' : undefined"
      active-class="active"
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" v-html="n.icon"></svg>
      <span>{{ n.label }}</span>
    </RouterLink>
  </nav>

  <!-- 未登录兜底（正常会被路由守卫拦截到 /login） -->
  <nav v-else class="m-tabbar">
    <button class="m-tab" @click="doLogout">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4M10 17l5-5-5-5M15 12H3"/></svg>
      <span>登录</span>
    </button>
  </nav>
</div>
</template>

<style scoped>
.m-app {
  /* #app 是 flex 容器：作为 flex 子项必须显式撑满，否则宽度收缩为内容宽 */
  width: 100%;
  flex: 1;
  min-width: 0;
  height: 100vh;
  height: 100dvh; /* 移动浏览器地址栏动态高度 */
  display: flex;
  flex-direction: column;
  background: var(--bg);
}
.m-main {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
}
.m-tabbar {
  flex-shrink: 0;
  display: flex;
  background: var(--sidebar-bg);
  border-top: 1px solid var(--border);
  padding-bottom: env(safe-area-inset-bottom, 0px);
  z-index: 10;
}
.m-tab {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  height: 54px;
  color: var(--text-dim);
  text-decoration: none;
  font-size: 10px;
  border: none;
  background: none;
  cursor: pointer;
  padding: 0;
  border-top: 2px solid transparent;
}
.m-tab svg { width: 22px; height: 22px; }
.m-tab.active {
  color: var(--accent);
  border-top-color: var(--accent);
}
</style>
