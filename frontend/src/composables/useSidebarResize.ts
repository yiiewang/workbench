import { ref, onMounted, onUnmounted, readonly } from 'vue'

const STORAGE_KEY = 'workbench-sidebar-state'

interface SidebarState { width: number; collapsed: boolean }

let saved: SidebarState = { width: 260, collapsed: false }
try {
  const raw = localStorage.getItem(STORAGE_KEY)
  if (raw) { const p = JSON.parse(raw); saved.width = p.width || 260; saved.collapsed = !!p.collapsed }
} catch {}

function save(w: number, c: boolean) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ width: w, collapsed: c })) } catch {}
}

export function useSidebarResize() {
  const sidebarWidth = ref(saved.width)
  const collapsed = ref(saved.collapsed)
  const resizing = ref(false)
  const resizerActive = ref(false)

  // apply saved state on init
  sidebarWidth.value = saved.collapsed ? 0 : Math.max(180, Math.min(saved.width, 600))

  function onResizerMouseDown() {
    resizing.value = true
    resizerActive.value = true
    document.body.style.cursor = 'col-resize'
  }

  function onMouseMove(e: MouseEvent) {
    if (!resizing.value) return
    sidebarWidth.value = Math.max(180, Math.min(e.clientX, 600))
  }

  function onMouseUp() {
    if (!resizing.value) return
    resizing.value = false
    resizerActive.value = false
    document.body.style.cursor = ''
    const c = sidebarWidth.value < 50
    saved.width = c ? saved.width : sidebarWidth.value
    saved.collapsed = c
    save(saved.width, saved.collapsed)
    if (c) sidebarWidth.value = 0
    collapsed.value = c
  }

  function toggle() {
    collapsed.value = !collapsed.value
    sidebarWidth.value = collapsed.value ? 0 : Math.max(180, Math.min(saved.width, 600))
    saved.collapsed = collapsed.value
    saved.width = collapsed.value ? saved.width : sidebarWidth.value
    save(saved.width, saved.collapsed)
  }

  onMounted(() => {
    document.addEventListener('mousemove', onMouseMove)
    document.addEventListener('mouseup', onMouseUp)
  })
  onUnmounted(() => {
    document.removeEventListener('mousemove', onMouseMove)
    document.removeEventListener('mouseup', onMouseUp)
  })

  return {
    sidebarWidth: readonly(sidebarWidth),
    collapsed: readonly(collapsed),
    resizerActive: readonly(resizerActive),
    onResizerMouseDown,
    toggle,
  }
}
