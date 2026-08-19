// @ts-nocheck
import { onMounted } from 'vue'
import { openTab } from './useTabs'
import { dataSource } from './useEditor'
import { getHashPath, clearHash } from './useHashRouting'
import { showToast, API_CODE } from '../lib/common'
import { showSharePasswordModal } from '../lib/sharePasswordModal'
import {
  authToken as storeAuthToken,
  dsMode, dsToken, dsPathPrefix, shareRootPath,
  openTabs, activeTabPath,
  setOpenTabFn, loadSharesFn,
  checkAuth,
} from '../stores/indexStore'
import { treeStore } from './useTreeStore'

/**
 * 退出分享模式：清空分享残留状态并恢复浏览器模式。
 * 供 initApp（首次挂载）与 IndexView 的路由 watch（从 /s/:token 切回浏览器路由）复用。
 */
export function exitShareMode() {
  if (dsMode.value !== 'share') return
  dsMode.value = 'browser'
  shareRootPath.value = ''
  dsToken.value = ''
  dsPathPrefix.value = ''
  dataSource.mode = 'browser'
  dataSource.shareToken = ''
  dataSource.sharePassword = ''
  sessionStorage.removeItem('workbench-share-pwd')
  treeStore.dirCache.clear()
  treeStore.expanded.clear()
  treeStore.activePath.value = null
  openTabs.clear()
  activeTabPath.value = ''
}

export function setupIndexApp() {
  onMounted(() => {
    // ============================================================
    // Share URL detection
    // ============================================================
    function parseShareURL() {
      const p = window.location.pathname
      const parts = p.split('/').filter(Boolean)
      if (parts.length >= 2 && parts[0] === 's') {
        return { token: parts[1], subPath: parts.slice(2).join('/') }
      }
      return null
    }

    function getShareErrorTitle(status: number): string {
      if (status === 404) return 'Share not found'
      if (status === 403) return 'Share expired or max access reached'
      return 'Share error'
    }

    // ============================================================
    // Init
    // ============================================================
    async function initApp() {
      const shareInfo = parseShareURL()
      if (shareInfo) {
        // 场景：用户已登录访问主界面（treeStore.dirCache['/'] = 完整目录树），
        // 然后通过分享链接进入 /s/{token}。如果不清理，侧栏会渲染完整目录树，
        // 暴露 static_dir 下所有文件名给分享接收者。
        treeStore.dirCache.clear()
        treeStore.expanded.clear()
        treeStore.activePath.value = null
        shareRootPath.value = ''

        dataSource.mode = 'share'
        dataSource.shareToken = shareInfo.token
        dsMode.value = 'share'
        dsToken.value = shareInfo.token
        dsPathPrefix.value = shareInfo.subPath || ''
        dataSource.sharePassword = sessionStorage.getItem('workbench-share-pwd') || ''

        // Retry loop: if password required, show modal and retry with entered password
        for (let attempt = 0; attempt < 5; attempt++) {
          try {
            const data = await dataSource.listDir(shareInfo.subPath)
            if (data.isDir === false) {
              // 文件分享：后端 share.go 的沙箱只允许访问 share.ResourcePath 本身
              // （即这个文件），不允许访问父目录（path 必须精确匹配文件路径）。
              // 因此文件分享场景侧栏留空（shareRootPath=null → TreeNode 不渲染），
              // 只在主区域打开文件 tab。这是符合"只分享这一个文件"语义的正确行为。
              const filePath = (data.resourcePath || '/' + (shareInfo.subPath || '')).replace(/\/$/, '')
              shareRootPath.value = ''
              treeStore.activePath.value = filePath

              const name = data.fileName || shareInfo.subPath.split('/').pop() || 'file'
              const ext = (data.ext || '').replace('.', '').toLowerCase()
              openTab(filePath, name, ext, data.size || 0)
            } else {
              // 目录分享：缓存分享根的内容到 treeStore（用 share.ResourcePath 作 key，
              // 不再用 '/' 避免和浏览器模式冲突）。侧栏从 shareRootPath 开始渲染。
              const shareRoot = (data.resourcePath || '/').replace(/\/$/, '') || '/'
              treeStore.dirCache.set(shareRoot, data)
              treeStore.expanded.add(shareRoot)
              shareRootPath.value = shareRoot

              if (shareInfo.subPath) {
                const name = shareInfo.subPath.split('/').pop()
                const ext = (name.split('.').pop() || '').toLowerCase()
                openTab('/' + shareInfo.subPath, name, ext, 0)
              }

              // 处理 URL hash：刷新时从 hash 恢复打开的文件（如 #/sdk-go/docs/xxx.md）
              // hash 里的路径是 static_dir 下的绝对路径（和 shareRoot 同体系），直接用
              const hashPath = getHashPath()
              if (hashPath) {
                try {
                  await treeStore.expandTo(hashPath)
                  const name = hashPath.split('/').pop() || ''
                  const ext = (name.split('.').pop() || '').toLowerCase()
                  openTab(hashPath, name, ext, 0)
                } catch (e) {
                  console.warn('[workbench] expandTo failed for hash path:', e)
                }
              }
            }
            break // success
          } catch (err) {
            if (err.code === API_CODE.PASSWORD_REQUIRED || err.code === API_CODE.INVALID_SHARE_PWD) {
              const pwd = await showSharePasswordModal(err.msg)
              if (!pwd) break // user cancelled
              dataSource.sharePassword = pwd
              sessionStorage.setItem('workbench-share-pwd', pwd)
              continue // retry with password
            }
            showToast(getShareErrorTitle(err.status) + ': ' + (err.msg || err.message || ''), 'error')
            break
          }
        }
        return
      }

      // 浏览器模式：重置分享模式残留状态（从 /s/{token} 导航回主界面时）
      exitShareMode()

      // 未登录 → 路由守卫已拦截，此处不再弹窗
      if (!storeAuthToken.value) {
        return
      }
      const authed = await checkAuth()
      if (!authed) {
        // checkAuth 内部已处理 401 清除认证，apiCall 全局拦截会跳转登录页
        return
      }

      if (window.location.pathname === '/shares') {
        loadSharesFn?.()
        return
      }

      // load root directory data via treeStore (TreeNode renders reactively)
      try {
        await treeStore.loadDir('/')
      } catch (err) {
        if (err.message === 'Auth required') return
        showToast('Failed to load directory: ' + (err.message || ''), 'error')
        return
      }

      // handle URL hash: expand tree to target path
      const hashPath = getHashPath()
      if (hashPath) {
        try {
          await treeStore.expandTo(hashPath)
          const dirData = treeStore.dirCache.get(hashPath)
          if (!dirData) {
            const name = hashPath.split('/').pop()
            const ext = (name.split('.').pop() || '').toLowerCase()
            openTab(hashPath, name, ext, 0)
          }
        } catch {
          clearHash()
        }
      }
    }

    // Wire openTab so TreeNode can call it
    setOpenTabFn(openTab)

    // start
    initApp().catch(err => {
      console.error('[workbench] initApp threw an error:', err)
    })

    // Token refresh (hourly) — validate via auth.checkAuth.
    // 失效时 checkAuth 内部清除认证，apiCall 全局 401 拦截自动跳转登录页。
    setInterval(async () => {
      if (!storeAuthToken.value) return
      await checkAuth()
    }, 3600000)
  })
}
