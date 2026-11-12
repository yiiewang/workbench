<script setup lang="ts">
// 移动端首页：用户问候 + 功能入口卡片（文件/AI/Todo）
import { computed } from 'vue'
import { loggedIn, currentUser } from '../../stores/auth'

const greeting = computed(() => {
  const h = new Date().getHours()
  if (h < 6) return '夜深了'
  if (h < 12) return '早上好'
  if (h < 18) return '下午好'
  return '晚上好'
})

const entries = [
  {
    to: '/m/files',
    title: '文件',
    desc: '浏览与预览你的文件',
    icon: '<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>',
  },
  {
    to: '/m/ai',
    title: 'AI 助手',
    desc: '随时提问，可 @ 引用文件',
    icon: '<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>',
  },
  {
    to: '/m/todo',
    title: '待办任务',
    desc: '查看任务进度与日程',
    icon: '<path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>',
  },
  {
    to: '/jobs',
    title: 'AI 定时任务',
    desc: '到点让 AI 自动执行并邮件送达',
    icon: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/>',
  },
]
</script>

<template>
<div class="m-home">
  <!-- 问候区 -->
  <div class="m-hero">
    <div class="m-avatar">{{ (currentUser?.userName || '?').charAt(0).toUpperCase() }}</div>
    <div class="m-hero-meta">
      <div class="m-hero-hi">{{ greeting }}，{{ currentUser?.userName }}</div>
      <div class="m-hero-org">{{ currentUser?.orgName }}</div>
    </div>
  </div>

  <!-- 功能入口 -->
  <div class="m-entries">
    <RouterLink v-for="e in entries" :key="e.to" :to="e.to" class="m-entry">
      <div class="m-entry-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" v-html="e.icon"></svg>
      </div>
      <div class="m-entry-main">
        <div class="m-entry-title">{{ e.title }}</div>
        <div class="m-entry-desc">{{ e.desc }}</div>
      </div>
      <svg class="m-entry-arrow" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg>
    </RouterLink>
  </div>

  <div v-if="!loggedIn" class="m-login-hint">
    <RouterLink to="/login" class="m-login-link">登录后使用完整功能</RouterLink>
  </div>
</div>
</template>

<style scoped>
.m-home {
  padding: 20px 16px;
  max-width: 640px;
  margin: 0 auto;
}
.m-hero {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 8px 4px 20px;
}
.m-avatar {
  width: 52px;
  height: 52px;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  font-weight: 600;
  flex-shrink: 0;
}
.m-hero-meta { min-width: 0; }
.m-hero-hi { font-size: 18px; font-weight: 600; color: var(--text); }
.m-hero-org { font-size: 13px; color: var(--text-dim); margin-top: 2px; }

.m-entries { display: flex; flex-direction: column; gap: 10px; }
.m-entry {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 16px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 12px;
  text-decoration: none;
  transition: border-color .15s;
}
.m-entry:active { border-color: var(--accent); }
.m-entry-icon {
  width: 44px;
  height: 44px;
  border-radius: 10px;
  background: var(--active, #e4e6f1);
  color: var(--accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.m-entry-icon svg { width: 22px; height: 22px; }
.m-entry-main { flex: 1; min-width: 0; }
.m-entry-title { font-size: 15px; font-weight: 600; color: var(--text); }
.m-entry-desc { font-size: 12px; color: var(--text-dim); margin-top: 3px; }
.m-entry-arrow { width: 16px; height: 16px; color: var(--text-muted); flex-shrink: 0; }

.m-login-hint {
  margin-top: 24px;
  text-align: center;
  font-size: 13px;
}
.m-login-link { color: var(--accent); text-decoration: none; }
</style>
