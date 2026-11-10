// Shared reactive state for TodoApp — allows TodoSidebar and TodoView to share a single data source.
// useTodoApp imports from here instead of declaring local refs; TodoSidebar imports directly.
import { ref } from 'vue'
import type { Member } from '../api/tasks'

// ============================================================
// Auth — re-export from stores/auth.ts (Single Source of Truth)
// ============================================================
export { authToken, currentUser } from './auth'

// ============================================================
// Core Task Data
// ============================================================
export const tasks = ref<any[]>([])
export const orgTasks = ref<any[]>([])

// Sidebar ↔ Main selection sync
export const activeTaskId = ref<string | null>(null)

// ============================================================
// Member Switching
// ============================================================
export const viewingMember = ref<string | null>(null)
export const orgMembers = ref<Member[]>([])

// ============================================================
// Sync Status
// ============================================================
export const syncStatus = ref<'idle' | 'syncing' | 'error'>('idle')
export const lastSyncError = ref('')

// ============================================================
// Task Edit Modal
// ============================================================
export const showModal = ref(false)
export const isCreating = ref(false)
export const editingTask = ref<Record<string, any>>({})
export const editingDateRange = ref<string[]>([])

// ============================================================
// Task Conflict Card（409 任务级冲突：本地 vs 服务端对比）
// ============================================================
export const conflictCard = ref<any>(null)
