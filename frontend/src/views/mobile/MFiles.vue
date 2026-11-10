<script setup lang="ts">
// 移动端文件浏览：单层目录列表 + 下钻导航（友好浏览定位，无管理操作）。
// 目录点击进入下级，文件点击跳预览页；路径由 query 驱动（可回退）。
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getTree } from '../../api/files'
import { formatSize } from '../../lib/common'

interface TreeItem { name: string; isDir: boolean; size?: number }

const route = useRoute()
const router = useRouter()

// 规范化当前目录路径（/ 开头）
const curPath = computed(() => {
  const p = String(route.query.path || '/')
  return '/' + p.replace(/^\/+/, '')
})

const dirs = ref<TreeItem[]>([])
const files = ref<TreeItem[]>([])
const loading = ref(true)
const loadError = ref('')

async function load() {
  loading.value = true
  loadError.value = ''
  dirs.value = []
  files.value = []
  try {
    const data = await getTree(curPath.value)
    dirs.value = (data.dirs || []) as TreeItem[]
    files.value = (data.files || []) as TreeItem[]
  } catch (e: any) {
    loadError.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

watch(curPath, load, { immediate: true })

// 父目录（根目录时为空，不显示返回行）
const parentPath = computed(() => {
  const p = curPath.value.replace(/\/+$/, '')
  if (!p || p === '/') return ''
  const i = p.lastIndexOf('/')
  return i <= 0 ? '/' : p.slice(0, i)
})

function childPath(name: string): string {
  return curPath.value === '/' ? '/' + name : curPath.value + '/' + name
}

function openDir(name: string) {
  router.push({ path: '/m/files', query: { path: childPath(name) } })
}

function openFile(name: string) {
  router.push({ path: '/m/file', query: { path: childPath(name) } })
}

function goParent() {
  if (parentPath.value) router.push({ path: '/m/files', query: { path: parentPath.value } })
}
</script>

<template>
<div class="m-page">
  <!-- 顶部：返回 + 当前目录名 -->
  <header class="m-page-header">
    <button v-if="parentPath" class="m-back-btn" title="上一级" @click="goParent">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 18l-6-6 6-6"/></svg>
    </button>
    <div class="m-page-title" :title="curPath">{{ curPath === '/' ? '我的文件' : curPath.split('/').pop() }}</div>
  </header>

  <div class="m-list">
    <div v-if="loading" class="m-page-empty">加载中…</div>
    <div v-else-if="loadError" class="m-page-empty is-error">{{ loadError }}</div>
    <div v-else-if="!dirs.length && !files.length" class="m-page-empty">空目录</div>
    <template v-else>
      <!-- 上级目录行 -->
      <div v-if="parentPath" class="m-list-row" @click="goParent">
        <span class="m-row-icon dir"><svg viewBox="0 0 16 16"><path d="M1.5 3h4l1 1h7v9h-12V3z" fill="currentColor"/></svg></span>
        <span class="m-row-name">..</span>
      </div>
      <div v-for="d in dirs" :key="d.name" class="m-list-row" @click="openDir(d.name)">
        <span class="m-row-icon dir"><svg viewBox="0 0 16 16"><path d="M1.5 3h4l1 1h7v9h-12V3z" fill="currentColor"/></svg></span>
        <span class="m-row-name">{{ d.name }}</span>
        <svg class="m-row-arrow" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg>
      </div>
      <div v-for="f in files" :key="f.name" class="m-list-row" @click="openFile(f.name)">
        <span class="m-row-icon"><svg viewBox="0 0 16 16"><path d="M3 1h6l5 5v9H3V1z" fill="none" stroke="currentColor" stroke-width="1.2"/><path d="M9 1v5h5" fill="none" stroke="currentColor" stroke-width="1.2"/></svg></span>
        <span class="m-row-name">{{ f.name }}</span>
        <span v-if="f.size" class="m-row-size">{{ formatSize(f.size) }}</span>
      </div>
    </template>
  </div>
</div>
</template>

