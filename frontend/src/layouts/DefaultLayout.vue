<script setup lang="ts">
// 应用外壳布局：activity-bar + sidebar + resizer + main。
// 仅包裹需要应用界面的路由；/login 等独立页面不走此布局。
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { dsMode, shareRootPath } from '../stores/indexStore'
import { loggedIn, currentUser, isAdmin, clearAuth } from '../stores/auth'
import { useSidebarResize } from '../composables/useSidebarResize'

const {
  sidebarWidth, collapsed, resizerActive,
  onResizerMouseDown, toggle: toggleSidebar,
} = useSidebarResize()

const route = useRoute()
const router = useRouter()

const isExplorerActive = computed(() =>
  route.path === '/' || route.path.startsWith('/s/')
)
const isSharesActive = computed(() => route.path === '/shares')
const isTodoActive = computed(() => route.path === '/todo')
const isAdminActive = computed(() => route.path === '/admin')

// 折叠按钮 left 位置：activity-bar(48px) + sidebar 宽度 - 按钮一半宽度(6px)。
// 分享模式无 activity-bar，偏移 0；折叠态 sidebar 宽度算 0。
const ACTIVITY_BAR_W = 48
const TOGGLE_BTN_HALF = 6
const toggleBtnLeft = computed(() => {
  const activityBarW = dsMode.value === 'share' ? 0 : ACTIVITY_BAR_W
  const sidebarW = collapsed.value ? 0 : sidebarWidth.value
  return activityBarW + sidebarW - TOGGLE_BTN_HALF
})

function handleExplorerClick() { router.push('/') }
function handleShareClick() { router.push('/shares') }
function handleTodoClick() { router.push('/todo') }
function handleAdminClick() { router.push('/admin') }

// 用户信息弹窗显隐
const showUserMenu = ref(false)

// 已登录 → 点击用户图标弹出用户信息面板；未登录 → 跳登录页
function onUserClick() {
  if (loggedIn.value) {
    showUserMenu.value = true
  } else {
    router.push('/login?redirect=' + encodeURIComponent(route.fullPath))
  }
}

// 退出登录
function doLogout() {
  showUserMenu.value = false
  clearAuth()
  router.push('/login')
}
</script>

<template>
<!-- Activity Bar -->
<div v-show="dsMode !== 'share'" class="activity-bar">
  <div class="activity-item" :class="{ active: isExplorerActive }" title="文件浏览器" @click="handleExplorerClick">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
  </div>
  <div class="activity-item" :class="{ active: isSharesActive }" title="分享管理" @click="handleShareClick">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="M8.59 13.51l6.83 3.98M15.41 6.51l-6.82 3.98"/></svg>
    <span class="badge" id="shareBadge" style="display:none"></span>
  </div>
  <div class="activity-item" :class="{ active: isTodoActive }" title="Todo 看板" @click="handleTodoClick">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>
  </div>
  <div v-if="isAdmin" class="activity-item" :class="{ active: isAdminActive }" title="用户管理" @click="handleAdminClick">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/></svg>
  </div>
  <div class="activity-divider"></div>
  <!-- 用户图标：已登录显示头像（点击弹出用户信息），未登录显示登录图标（跳转登录页） -->
  <div class="activity-item activity-user" :title="loggedIn ? currentUser?.userName : '登录'" @click="onUserClick">
    <svg v-if="!loggedIn" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
      <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
      <circle cx="12" cy="7" r="4"/>
    </svg>
    <div v-else class="user-badge">{{ (currentUser?.userName || '?').charAt(0).toUpperCase() }}</div>
  </div>
</div>

<div class="sidebar" :style="{ width: sidebarWidth + 'px' }" :class="{ collapsed: collapsed, resizing: resizerActive }" v-show="!(dsMode === 'share' && !shareRootPath)">
  <RouterView name="sidebar" />
</div>
<div
  v-if="!collapsed && !(dsMode === 'share' && !shareRootPath)"
  class="resizer"
  :class="{ active: resizerActive }"
  @mousedown="onResizerMouseDown"
></div>
<!-- 悬浮折叠/展开按钮：作为 #app 子元素绝对定位，避免被 sidebar overflow:hidden 裁剪。
     left = sidebar 宽度（折叠态 0）+ activity-bar 偏移（48px，分享模式无 activity-bar 为 0） -->
<button
  v-if="!(dsMode === 'share' && !shareRootPath)"
  class="sidebar-toggle"
  :class="{ collapsed: collapsed }"
  :style="{ left: toggleBtnLeft + 'px' }"
  @click="toggleSidebar"
  :title="collapsed ? 'Expand sidebar' : 'Collapse sidebar'"
>
  <svg width="6" height="10" viewBox="0 0 6 10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path :d="collapsed ? 'M1 1L5 5L1 9' : 'M5 1L1 5L5 9'" />
  </svg>
</button>

<div class="main">
  <RouterView v-slot="{ Component }">
    <KeepAlive>
      <component :is="Component" />
    </KeepAlive>
  </RouterView>
</div>

<!-- 用户信息弹窗：点击活动栏底部头像弹出，展示账号信息 + 退出登录 -->
<div v-if="showUserMenu" class="modal-mask" @click.self="showUserMenu = false">
  <div class="modal-content user-menu">
    <div class="modal-header">Account</div>
    <div class="modal-body">
      <div class="user-profile">
        <div class="user-avatar">{{ (currentUser?.userName || '?').charAt(0).toUpperCase() }}</div>
        <div class="user-meta">
          <div class="user-name">{{ currentUser?.userName }}</div>
          <div class="user-org">{{ currentUser?.orgName }}</div>
        </div>
        <span class="role-tag" :class="currentUser?.role">{{ currentUser?.role }}</span>
      </div>
      <div class="user-fields">
        <div class="field-row"><span class="field-label">User ID</span><span class="field-value">{{ currentUser?.userId }}</span></div>
        <div class="field-row"><span class="field-label">Org ID</span><span class="field-value">{{ currentUser?.orgId }}</span></div>
        <div class="field-row"><span class="field-label">Role</span><span class="field-value">{{ currentUser?.role }}</span></div>
      </div>
    </div>
    <div class="modal-actions">
      <button class="btn" @click="showUserMenu = false">Close</button>
      <button class="btn btn-danger" @click="doLogout">Logout</button>
    </div>
  </div>
</div>
</template>

<style scoped>
.activity-user { margin-top: auto; }
.user-badge { width:24px; height:24px; border-radius:50%; background:var(--accent); color:#fff; display:flex; align-items:center; justify-content:center; font-size:12px; font-weight:600; }

/* 用户信息弹窗 */
.user-menu { width: 320px; max-width: 92vw; }
.user-profile { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
.user-avatar {
  width: 44px; height: 44px; border-radius: 50%;
  background: var(--accent); color: #fff;
  display: flex; align-items: center; justify-content: center;
  font-size: 20px; font-weight: 600; flex-shrink: 0;
}
.user-meta { flex: 1; min-width: 0; }
.user-name { font-size: 15px; font-weight: 600; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.user-org { font-size: 12px; color: var(--text-dim); margin-top: 2px; }

/* 角色标签 */
.role-tag { display: inline-block; padding: 1px 8px; border-radius: 3px; font-size: 11px; font-weight: 500; flex-shrink: 0; }
.role-tag.admin { background: #fde8ea; color: #cf222e; }
.role-tag.org_admin { background: #fff4e1; color: #b35900; }
.role-tag.user { background: var(--code-bg); color: var(--text-dim); }

.user-fields { border-top: 1px solid var(--border); padding-top: 8px; }
.field-row { display: flex; justify-content: space-between; padding: 4px 0; font-size: 12px; }
.field-label { color: var(--text-dim); }
.field-value { color: var(--text); font-family: var(--mono, monospace); }
</style>
