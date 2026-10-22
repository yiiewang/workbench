// 文件树右键菜单全局单例状态（FileContextMenu.vue 消费，TreeNode 触发）
import { reactive } from 'vue'

export interface ContextMenuTarget {
  path: string
  name: string
  isDir: boolean
}

export const contextMenu = reactive({
  visible: false,
  x: 0,
  y: 0,
  target: null as ContextMenuTarget | null,
})

export function openFileContextMenu(path: string, name: string, isDir: boolean, x: number, y: number) {
  contextMenu.target = { path, name, isDir }
  contextMenu.x = x
  contextMenu.y = y
  contextMenu.visible = true
}

export function closeFileContextMenu() {
  contextMenu.visible = false
}
