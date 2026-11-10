<script setup lang="ts">
// 移动端分享访问视图（/m/s/:token(/:path)，公开）：
// 目录分享 → 列表下钻；文件分享 / 目录内文件 → FileViewer 预览（复用 dataSource share 模式）。
// 密码流复用全局 SharePasswordModal + sessionStorage 缓存；卸载时复位 dataSource/dsMode。
import { ref, computed, watch, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getShareData } from '../../api/share'
import { showSharePasswordModal } from '../../lib/sharePasswordModal'
import { API_CODE, formatSize } from '../../lib/common'
import { dataSource } from '../../composables/useEditor'
import { dsMode, shareRootPath } from '../../stores/indexStore'
import FileViewer from '../../components/editor/FileViewer.vue'

const route = useRoute()
const router = useRouter()

const token = computed(() => String(route.params.token || ''))
// path param = static_dir 绝对路径（与 dataSource/treeStore 同体系；分享根访问时为空）
const curPath = computed(() => {
  const p = String(route.params.path || '')
  return p ? '/' + p.replace(/^\/+/, '') : ''
})

// ---- 状态：list（目录列表）/ file（文件预览） ----
const viewMode = ref<'list' | 'file'>('list')
const dirs = ref<{ name: string }[]>([])
const files = ref<{ name: string; size?: number }[]>([])
const filePreview = ref({ path: '', name: '', ext: '' })
const loading = ref(true)
const loadError = ref('')
const shareTitle = ref('分享')

// 初始化 dataSource（FileViewer 经它取数据，自动带密码）
dataSource.mode = 'share'
dataSource.shareToken = token.value
dataSource.sharePassword = sessionStorage.getItem('workbench-share-pwd') || ''
dsMode.value = 'share'

async function load() {
  loading.value = true
  loadError.value = ''
  dirs.value = []
  files.value = []
  // 密码重试循环（最多 5 次，同 PC useIndexApp）
  for (let attempt = 0; attempt < 5; attempt++) {
    try {
      const data: any = await getShareData(token.value, curPath.value || undefined, dataSource.sharePassword)
      // resourcePath = 分享资源根（文件分享 = 文件路径；目录分享 = 目录路径），dataSource 依赖它算子路径
      shareRootPath.value = data.resourcePath || ''
      if (data.isDir === false) {
        // 文件：进预览态（name/ext 由 FileViewer 内部从 dataSource 拿，这里只给 path）
        viewMode.value = 'file'
        filePreview.value = {
          path: data.resourcePath || '',
          name: data.fileName || '',
          ext: (data.ext || '').replace('.', '').toLowerCase(),
        }
        shareTitle.value = data.fileName || '分享'
      } else {
        viewMode.value = 'list'
        dirs.value = (data.dirs || []) as any[]
        files.value = (data.files || []) as any[]
        // 分享根显示资源名，子目录显示目录名
        shareTitle.value = curPath.value ? (curPath.value.split('/').pop() || '分享') : (data.resourcePath || '分享').split('/').pop() || '分享'
      }
      break
    } catch (e: any) {
      if (e?.code === API_CODE.PASSWORD_REQUIRED || e?.code === API_CODE.INVALID_SHARE_PWD) {
        const pwd = await showSharePasswordModal(e.msg)
        if (!pwd) break // 取消：保留错误提示
        dataSource.sharePassword = pwd
        try { sessionStorage.setItem('workbench-share-pwd', pwd) } catch { /* 忽略 */ }
        continue
      }
      loadError.value = e?.msg || e?.message || '加载失败'
      break
    }
  }
  loading.value = false
}

watch([token, curPath], load, { immediate: true })

// 子项路径：curPath 为空（分享根）时以 shareRootPath 为基准拼接
function childPath(name: string): string {
  const base = curPath.value || shareRootPath.value
  return (base === '/' ? '' : base) + '/' + name
}

function openChild(name: string) {
  // 目录与文件统一 push：load 按 isDir 自动分列表/预览
  const p = childPath(name)
  router.push({ path: `/m/s/${token.value}${p}` })
}

// 返回：有上级则回上级路径，无则退出（浏览器返回/首页）
function goBack() {
  if (router.options.history.state?.back) {
    router.back()
  } else {
    router.push('/m')
  }
}

// 卸载：复位 dataSource/dsMode，避免污染移动端文件浏览（browser 模式）
onUnmounted(() => {
  dataSource.mode = 'browser'
  dataSource.shareToken = ''
  dataSource.sharePassword = ''
  dsMode.value = 'browser'
  shareRootPath.value = ''
})
</script>

<template>
<div class="m-page wide">
  <!-- 顶部：返回 + 标题 -->
  <header class="m-page-header">
    <button class="m-back-btn" title="返回" @click="goBack">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6"/></svg>
    </button>
    <div class="m-page-title">{{ shareTitle }}</div>
  </header>

  <!-- 加载/错误 -->
  <div v-if="loading" class="m-page-empty">加载中…</div>
  <div v-else-if="loadError" class="m-page-empty is-error">{{ loadError }}</div>

  <!-- 文件预览（FileViewer 内部走 dataSource share 模式，含密码重试/媒体回退/下载） -->
  <div v-else-if="viewMode === 'file'" class="ms-viewer">
    <FileViewer
      :path="filePreview.path"
      :name="filePreview.name"
      :ext="filePreview.ext"
      :size="0"
    />
  </div>

  <!-- 目录列表 -->
  <div v-else class="m-list">
    <div v-if="!dirs.length && !files.length" class="m-page-empty">空目录</div>
    <template v-else>
      <div v-for="d in dirs" :key="d.name" class="m-list-row" @click="openChild(d.name)">
        <span class="m-row-icon dir"><svg viewBox="0 0 16 16"><path d="M1.5 3h4l1 1h7v9h-12V3z" fill="currentColor"/></svg></span>
        <span class="m-row-name">{{ d.name }}</span>
        <svg class="m-row-arrow" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg>
      </div>
      <div v-for="f in files" :key="f.name" class="m-list-row" @click="openChild(f.name)">
        <span class="m-row-icon"><svg viewBox="0 0 16 16"><path d="M3 1h6l5 5v9H3V1z" fill="none" stroke="currentColor" stroke-width="1.2"/><path d="M9 1v5h5" fill="none" stroke="currentColor" stroke-width="1.2"/></svg></span>
        <span class="m-row-name">{{ f.name }}</span>
        <span v-if="f.size" class="m-row-size">{{ formatSize(f.size) }}</span>
      </div>
    </template>
  </div>
</div>
</template>
<style scoped>
/* 宽内容页（预览类）超出公共 640px 限宽 */
.m-page.wide { max-width: 768px; }

/* 文件预览容器（FileViewer 内部滚动） */
.ms-viewer {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
