// Shared reactive state for TodoApp — allows TodoSidebar and TodoView to share a single data source.
// useTodoApp imports from here instead of declaring local refs; TodoSidebar imports directly.
import { ref, reactive } from 'vue'

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
export const orgMembers = ref<string[]>([])

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
// Conflict Resolution
// ============================================================
export const showConflictModal = ref(false)
export const conflictInfo = ref<any>(null)
export const conflictTaskList = ref<any[]>([])
export const showManualResolve = ref(false)
export const conflictChoices = reactive<Record<string, any>>({})
export const showConfirmSummary = ref(false)
export const confirmSummary = reactive({
  localCount: 0, serverCount: 0, fieldCount: 0, dropCount: 0,
})
export const filterSeverity = ref<'all' | 'high' | 'medium' | 'low'>('all')
