// Thin wrapper — actual logic is in SharePasswordModal.vue component.
// Components mount and register their show() function; callers import this module.
let showFn: ((err?: string) => Promise<string | null>) | null = null

export function registerSharePasswordModal(fn: (err?: string) => Promise<string | null>) {
  showFn = fn
}

export async function showSharePasswordModal(err?: string): Promise<string | null> {
  if (!showFn) return null
  return showFn(err)
}
