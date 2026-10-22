// Thin wrapper — actual logic is in PromptModal.vue component (同 shareModal.ts 模式)
export function openPrompt(opts: { title: string; initial?: string }, cb: (v: string) => void) {
  const fn = (window as any).openPrompt
  if (typeof fn === 'function') fn(opts, cb)
}
