// RAG 引用跳转（RagCitations 引用列表与 RagRefTooltip 悬浮卡片共用）：
// 移动端 → /m/file?path=&line=；PC → /file + #路径#L行号 hash
//（活页 hash watcher 消费：展开目录树 + 开 tab + CodePreview 行定位）。
import type { Router, RouteLocationNormalizedLoaded } from 'vue-router'
import { openFileFromHash } from '../stores/indexStore'

export async function openRagFile(
  router: Router,
  route: RouteLocationNormalizedLoaded,
  path: string,
  line: number,
): Promise<void> {
  if (route.path.startsWith('/m')) {
    const query: Record<string, string> = { path }
    if (line > 0) query.line = String(line)
    await router.push({ path: '/m/file', query })
    return
  }
  await router.push('/file')
  const hash = `#${path}` + (line > 0 ? `#L${line}` : '')
  if (window.location.hash === hash) {
    // 同 hash 重复点击不触发 hashchange，手动消费兜底
    await openFileFromHash(hash).catch(() => {})
  } else {
    window.location.hash = hash
  }
}
