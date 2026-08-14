// Reactive tree store: replaces renderTree/expandNode/findTreeNode/findTreeRow DOM manipulation
import { reactive, ref } from 'vue'
import { dsListDir, dsMode } from '../stores/indexStore'

export interface TreeItem {
  name: string
  isDir: boolean
  size?: number
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
    allChildren,
    childPath,
    loadDir,
    toggle,
    expandTo,
  }
}

// singleton instance
const treeStore = useTreeStore()
export { treeStore }
