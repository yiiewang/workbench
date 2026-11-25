// ModalShell 共享的 ESC 层叠栈：保证 ESC 只关最上层弹窗；
// 其他组件（如 AI 对话面板）用 hasOpenModal() 判断该不该响应 ESC。
export const escStack: symbol[] = []

export function hasOpenModal(): boolean {
  return escStack.length > 0
}
