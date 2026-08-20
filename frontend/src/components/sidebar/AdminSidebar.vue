<script setup lang="ts">
// Admin 侧栏：可搜索的用户列表（点击选中 → 主区显示详情）
import { onMounted } from 'vue'
import {
  users,
  filteredUsers,
  searchQuery,
  selectedId,
  isNew,
  loadAdminData,
  selectUser,
  startCreate,
} from '../../stores/adminStore'
import { isAdmin, currentUser } from '../../stores/auth'

onMounted(() => {
  loadAdminData()
})
</script>

<template>
  <div class="admin-sidebar active">
    <div class="sidebar-header">
      USER MANAGEMENT
      <span class="count">{{ users.length }}</span>
    </div>

    <div class="search-box">
      <input
        v-model="searchQuery"
        class="search-input"
        type="text"
        placeholder="Search name / org…"
        spellcheck="false"
      />
    </div>

    <button class="btn btn-primary new-btn" @click="startCreate">+ New User</button>

    <div class="user-list">
      <div v-if="!filteredUsers.length" class="list-empty">No users</div>
      <div
        v-for="u in filteredUsers"
        :key="u.id"
        class="user-item"
        :class="{ active: selectedId === u.id && !isNew }"
        @click="selectUser(u.id)"
      >
        <div class="user-main">
          <span class="user-name">{{ u.name }}</span>
          <span class="role-tag" :class="u.role">{{ u.role }}</span>
          <span v-if="u.id === currentUser?.userId" class="you-tag">you</span>
        </div>
        <div class="user-sub">{{ u.orgName }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-sidebar {
  display: none;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
}
.admin-sidebar.active {
  display: flex;
}

.sidebar-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.5px;
  color: var(--text-dim);
  text-transform: uppercase;
  flex-shrink: 0;
}
.count {
  font-size: 10px;
  font-weight: 400;
  color: var(--text-muted);
  background: var(--code-bg);
  border-radius: 9px;
  padding: 0 6px;
  line-height: 1.6;
}

.search-box {
  padding: 0 12px 8px;
  flex-shrink: 0;
}
.search-input {
  width: 100%;
  padding: 4px 8px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: 3px;
  background: var(--bg);
  color: var(--text);
  box-sizing: border-box;
}
.search-input:focus {
  outline: none;
  border-color: var(--accent);
}

.new-btn {
  margin: 0 12px 8px;
  flex-shrink: 0;
  font-size: 12px;
}

.user-list {
  flex: 1;
  overflow-y: auto;
}
.list-empty {
  padding: 16px 12px;
  font-size: 12px;
  color: var(--text-muted);
  text-align: center;
}

.user-item {
  padding: 5px 12px;
  cursor: pointer;
  border-bottom: 1px solid var(--border);
  transition: background 0.1s;
}
.user-item:hover {
  background: var(--hover);
}
.user-item.active {
  background: var(--selection);
}
.user-item.active .user-name {
  color: var(--text);
}

.user-main {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.user-name {
  font-size: 12px;
  font-weight: 500;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.user-sub {
  font-size: 10px;
  color: var(--text-muted);
  margin-top: 1px;
  padding-left: 2px;
}
.you-tag {
  font-size: 9px;
  color: var(--text-muted);
  font-style: italic;
}

/* 角色标签（与主区一致） */
.role-tag {
  display: inline-block;
  padding: 0 5px;
  border-radius: 3px;
  font-size: 9px;
  line-height: 1.5;
  font-weight: 500;
  flex-shrink: 0;
}
.role-tag.admin {
  background: #fde8ea;
  color: #cf222e;
}
.role-tag.org_admin {
  background: #fff4e1;
  color: #b35900;
}
.role-tag.user {
  background: var(--code-bg);
  color: var(--text-dim);
}
</style>
