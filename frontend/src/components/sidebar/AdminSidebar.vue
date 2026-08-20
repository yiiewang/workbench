<script setup lang="ts">
// Admin sidebar: 用户统计卡片，监听 admin-users-refresh 事件与主区同步刷新
import { ref, computed, onMounted, onUnmounted } from 'vue'
import * as adminApi from '../../api/admin'
import type { AdminUser } from '../../api/admin'

const users = ref<AdminUser[]>([])

const userCount = computed(() => users.value.length)
const adminCount = computed(() => users.value.filter((u) => u.role === 'admin').length)
const orgCount = computed(() => new Set(users.value.map((u) => u.orgName)).size)

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
    <div class="sidebar-header">USER MANAGEMENT</div>
    <div class="admin-stats">
      <div class="stat-item">
        <span class="stat-num">{{ userCount }}</span>
        <span class="stat-label">Users</span>
      </div>
      <div class="stat-item">
        <span class="stat-num">{{ adminCount }}</span>
        <span class="stat-label">Admins</span>
      </div>
      <div class="stat-item">
        <span class="stat-num">{{ orgCount }}</span>
        <span class="stat-label">Orgs</span>
      </div>
    </div>
    <p class="admin-hint">Only admins can manage users. The first registered user becomes admin automatically.</p>
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

.admin-stats {
  padding: 4px 0;
}

.stat-item {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
}
.stat-item:hover {
  background: var(--hover);
}

.stat-num {
  font-size: 18px;
  font-weight: 600;
  color: var(--text);
  min-width: 28px;
}
.stat-label {
  font-size: 12px;
  color: var(--text-dim);
}

.admin-hint {
  font-size: 11px;
  color: var(--text-muted);
  line-height: 1.5;
  margin: 12px 16px;
}
</style>
