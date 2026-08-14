// Thin wrapper — actual logic is in ShareModal.vue component
export function openShareModal(resourceType: string) {
  const fn = (window as any).openShareModal
  if (typeof fn === 'function') fn(resourceType)
}
