// Reactive tree store: replaces renderTree/expandNode/findTreeNode/findTreeRow DOM manipulation
import { reactive, ref } from 'vue'
import { dsListDir } from '../stores/indexStore'

export interface TreeItem {
  name: string
  isDir: boolean
  size?: number
  isLink?: boolean
}

export interface DirData {
  path: string
  dirs: TreeItem[]
  files: TreeItem[]
}

export function useTreeStore() {
  // loaded directory data, keyed by absolute path
  const dirCache = reactive(new Map<string, DirData>())
  // set of expanded paths (directories whose children are visible)
  const expanded = reactive(new Set<string>())
  // currently highlighted path in the tree
  const activePath = ref<string | null>(null)

  let loading = reactive(new Set<string>())

  // 剪贴板：移动操作的源路径标记（右键"移动"标记，目标目录"移动到此处"完成）
  const clipboard = ref<{ path: string } | null>(null)

  function allChildren(path: string): TreeItem[] {
    const data = dirCache.get(path)
    if (!data) return []
    const dirs = data.dirs || []
    const files = data.files || []
    return [
      ...dirs.map(d => ({ ...d, isDir: true as const })),
      ...files.map(f => ({ ...f, isDir: false as const })),
    ]
  }

  function childPath(parentPath: string, name: string): string {
    return parentPath === '/' ? `/${name}` : `${parentPath}/${name}`
  }

  async function loadDir(path: string): Promise<DirData> {
    const cached = dirCache.get(path)
    if (cached) return cached
    if (loading.has(path)) {
      // wait for in-flight request
      return new Promise(resolve => {
        const check = setInterval(() => {
          const c = dirCache.get(path)
          if (c) { clearInterval(check); resolve(c) }
        }, 50)
      })
    }
    loading.add(path)
    try {
      const data = await dsListDir(path)
      dirCache.set(path, data)
      return data
    } finally {
      loading.delete(path)
    }
  }

  function toggle(path: string) {
    if (expanded.has(path)) {
      expanded.delete(path)
    } else {
      expanded.add(path)
      loadDir(path) // trigger load if not cached
    }
  }

  /** 目录内容变化（上传等写操作）后刷新：失效缓存，树上可见（有缓存）或已展开的目录立即重新加载。 */
  async function refreshDir(path: string) {
    // 等待可能进行中的同目录加载结束，避免并发期间拿到旧数据
    if (loading.has(path)) {
      await new Promise<void>(resolve => {
        const check = setInterval(() => {
          if (!loading.has(path)) { clearInterval(check); resolve() }
        }, 50)
      })
    }
    // 有缓存 = 当前树上正显示（含根目录：根的子项始终渲染，但根不在 expanded 集合中）
    const wasVisible = dirCache.has(path)
    dirCache.delete(path)
    if (wasVisible || expanded.has(path)) {
      try { await loadDir(path) } catch { /* 刷新失败保留现状，下次展开重新拉取 */ }
    }
  }

  /** 删除/移动后失效路径及全部子孙目录的缓存与展开状态（旧内容已不存在，必须作废）。 */
  function pruneCache(prefix: string) {
    const p = (prefix !== '/' && prefix.endsWith('/')) ? prefix.slice(0, -1) : prefix
    for (const key of [...dirCache.keys()]) {
      if (key === p || key.startsWith(p + '/')) dirCache.delete(key)
    }
    for (const e of [...expanded]) {
      if (e === p || e.startsWith(p + '/')) expanded.delete(e)
    }
  }

  /** expand all ancestor directories and load data for the target path.
   *  If target is a directory, also expand it (so children are visible).
   *  注意：最后一段可能是文件（loadDir 只接受目录路径，对文件调会 404），
   *  所以循环只处理到最后一段之前，最后一段的展开由 dirCache.has 判断。 */
  async function expandTo(targetPath: string) {
    if (!targetPath.startsWith('/')) targetPath = '/' + targetPath
    const parts = targetPath.split('/').filter(Boolean)
    let cur = ''
    // 最后一段跳过 loadDir（可能是文件），只处理祖先目录
    for (let i = 0; i < parts.length - 1; i++) {
      cur += '/' + parts[i]
      try { await loadDir(cur) } catch { /* handled by caller */ }
      expanded.add(cur)
    }
    // 如果 target 本身是目录（已在 dirCache），加载并展开
    if (dirCache.has(targetPath)) {
      expanded.add(targetPath)
    } else if (parts.length > 0) {
      // target 可能是未加载的目录，尝试 loadDir（失败说明是文件，忽略）
      try {
        await loadDir(targetPath)
        expanded.add(targetPath)
      } catch { /* target is a file, not a dir — that's fine */ }
    }
    activePath.value = targetPath
  }

  return {
    dirCache,
    expanded,
    activePath,
    loading,
    clipboard,
    allChildren,
    childPath,
    loadDir,
    toggle,
    refreshDir,
    pruneCache,
    expandTo,
  }
}

// singleton instance
const treeStore = useTreeStore()
export { treeStore }
