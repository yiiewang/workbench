<script setup lang="ts">
// Admin sidebar: 用户统计卡片，监听 admin-users-refresh 事件与主区同步刷新
// 紧凑布局：padding 6-8px、字号 11-12px
import { ref, computed, onMounted, onUnmounted } from 'vue'
import * as adminApi from '../../api/admin'
import type { AdminUser } from '../../api/admin'
import { isAdmin, currentUser } from '../../stores/auth'

const users = ref<AdminUser[]>([])

const userCount = computed(() => users.value.length)
const adminCount = computed(() => users.value.filter((u) => u.role === 'admin').length)
const orgAdminCount = computed(() => users.value.filter((u) => u.role === 'org_admin').length)
const orgCount = computed(() => new Set(users.value.map((u) => u.orgName)).size)
const scopeText = computed(() => isAdmin.value ? 'all orgs' : `org: ${currentUser.value?.orgName}`)

async function load() {
  try {
    const data = await adminApi.listUsers()
    users.value = data.users || []
  } catch { /* 静默失败，统计卡显示 0 */ }
}

onMounted(() => {
  load()
  window.addEventListener('admin-users-refresh', load)
})

onUnmounted(() => {
  window.removeEventListener('admin-users-refresh', load)
})
</script>

<template>
  <div class="admin-panel active">
    <div class="sidebar-header">USER MANAGEMENT <span class="scope">· {{ scopeText }}</span></div>
    <div class="admin-stats">
      <div class="stat-item">
        <span class="stat-num">{{ userCount }}</span>
        <span class="stat-label">Users</span>
      </div>
      <div class="stat-item">
        <span class="stat-num">{{ adminCount }}</span>
        <span class="stat-label">Admins</span>
      </div>
      <div v-if="isAdmin" class="stat-item">
        <span class="stat-num">{{ orgAdminCount }}</span>
        <span class="stat-label">Org Admins</span>
      </div>
      <div v-if="isAdmin" class="stat-item">
        <span class="stat-num">{{ orgCount }}</span>
        <span class="stat-label">Orgs</span>
      </div>
    </div>
    <p class="admin-hint">The first registered user becomes super admin. org_admin manages only own org.</p>
  </div>
</template>

<style scoped>
.admin-panel {
  display: none;
  flex-direction: column;
  flex: 1;
  overflow-y: auto;
}
.admin-panel.active {
  display: flex;
}

.admin-panel :deep(.sidebar-header) {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 8px 12px;
  font-size: 11px;
}
.admin-panel :deep(.scope) {
  font-size: 10px;
  color: var(--text-muted);
  text-transform: none;
  letter-spacing: 0;
  font-weight: 400;
}

.admin-stats {
  padding: 0;
}

.stat-item {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 5px 12px;
  border-bottom: 1px solid var(--border);
}
.stat-item:hover {
  background: var(--hover);
}
.stat-item:last-child {
  border-bottom: none;
}

.stat-num {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  min-width: 24px;
  text-align: right;
}
.stat-label {
  font-size: 11px;
  color: var(--text-dim);
}

.admin-hint {
  font-size: 10px;
  color: var(--text-muted);
  line-height: 1.4;
  margin: 8px 12px;
}
</style>
