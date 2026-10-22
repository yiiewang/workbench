<script setup lang="ts">
// 功能首页：聚合各功能入口卡片（file/share/todo/admin）。
// 挂在路由 /，按当前组织 features 与用户角色动态显隐；
// 点击卡片跳转对应功能路由。此页无侧栏（meta.hideSidebar）。
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { currentUser, canManageUsers, hasFeature } from '../stores/auth'

const router = useRouter()

// 按时段问候
const greeting = computed(() => {
  const h = new Date().getHours()
  if (h < 6) return 'Good night'
  if (h < 12) return 'Good morning'
  if (h < 18) return 'Good afternoon'
  return 'Good evening'
})

interface FeatureCard {
  key: string
  title: string
  desc: string
  path: string
  // SVG 图标 inner markup（与 activity-bar 同款线条图标）
  icon: string
}

// 功能卡片：与 activity-bar 入口一一对应，按权限/功能开关过滤
const cards = computed<FeatureCard[]>(() => {
  const list: FeatureCard[] = []
  if (hasFeature('file')) {
    list.push({
      key: 'file',
      title: '文件浏览器',
      desc: '浏览、预览与管理你的文件',
      path: '/file',
      icon: '<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>',
    })
  }
  if (hasFeature('share')) {
    list.push({
      key: 'share',
      title: '分享管理',
      desc: '创建并管理文件分享链接',
      path: '/shares',
      icon: '<circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="M8.59 13.51l6.83 3.98M15.41 6.51l-6.82 3.98"/>',
    })
  }
  if (hasFeature('todo')) {
    list.push({
      key: 'todo',
      title: 'Todo 看板',
      desc: '任务管理与进度跟踪',
      path: '/todo',
      icon: '<path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>',
    })
  }
  if (canManageUsers.value) {
    list.push({
      key: 'admin',
      title: '用户管理',
      desc: '管理组织成员与账号',
      path: '/admin',
      icon: '<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>',
    })
  }
  return list
})
</script>

<template>
  <div class="home">
    <div class="home-hero">
      <div class="home-title">{{ greeting }}，{{ currentUser?.userName || 'Welcome' }}</div>
      <div class="home-subtitle">
        <template v-if="currentUser?.orgName">当前组织：{{ currentUser.orgName }} · </template>选择一个功能开始
      </div>
    </div>

    <!-- 功能入口卡片 -->
    <div v-if="cards.length" class="home-grid">
      <div
        v-for="c in cards"
        :key="c.key"
        class="home-card"
        @click="router.push(c.path)"
      >
        <svg class="card-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" v-html="c.icon" />
        <div class="card-title">{{ c.title }}</div>
        <div class="card-desc">{{ c.desc }}</div>
        <div class="card-enter">进入 <span class="card-arrow">→</span></div>
      </div>
    </div>

    <!-- 无任何功能权限时的空态提示 -->
    <div v-else class="home-empty">
      当前组织未启用任何功能，请联系管理员开通。
    </div>
  </div>
</template>

<style scoped>
.home {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px 24px;
  gap: 36px;
}

.home-hero {
  text-align: center;
}

.home-title {
  font-size: 26px;
  font-weight: 600;
  color: var(--text);
}

.home-subtitle {
  margin-top: 8px;
  font-size: 13px;
  color: var(--text-dim);
}

.home-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 16px;
  width: 100%;
  max-width: 760px;
}

.home-card {
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg);
  padding: 22px 20px 18px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: border-color 0.15s, box-shadow 0.15s, transform 0.15s;
}

.home-card:hover {
  border-color: var(--accent);
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.08);
  transform: translateY(-2px);
}

.card-icon {
  width: 28px;
  height: 28px;
  color: var(--accent);
}

.card-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
  margin-top: 4px;
}

.card-desc {
  font-size: 12px;
  color: var(--text-dim);
  flex: 1;
}

.card-enter {
  font-size: 12px;
  color: var(--text-dim);
  display: flex;
  align-items: center;
  gap: 4px;
}

.home-card:hover .card-enter {
  color: var(--accent);
}

.card-arrow {
  display: inline-block;
  transition: transform 0.15s;
}

.home-card:hover .card-arrow {
  transform: translateX(3px);
}

.home-empty {
  color: var(--text-dim);
  font-size: 13px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  padding: 24px 32px;
}
</style>
