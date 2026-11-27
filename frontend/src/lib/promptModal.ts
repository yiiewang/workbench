// Thin wrapper — actual logic is in PromptModal.vue component
export function openPrompt(opts: { title: string; initial?: string }, cb: (v: string) => void) {
  const fn = (window as any).openPrompt
  if (typeof fn === 'function') fn(opts, cb)
}
