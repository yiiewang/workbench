<script setup lang="ts">
// 统一的 Markdown 预览组件：文件预览（FileViewer）与 todo 编辑器预览（MarkdownEditor）共用
// 渲染链路：marked(npm) → DOMPurify(npm) → v-html
// v-html 落地后再做两件事：给 table 套滚动容器（sticky 表头 + 边缘滚动提示）、按需渲染 mermaid
import { ref, watch, nextTick } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { wrapMarkdownTables } from '../../lib/markdownTable'
import { setupHeadingFold, scrollToId, type MdHeading } from '../../lib/markdownFold'
import MarkdownToc from './MarkdownToc.vue'

const props = withDefaults(
  defineProps<{
    content: string
    // 紧凑模式：小字号、小内边距（todo 侧编辑器内的分栏预览）
    compact?: boolean
    // 是否渲染 mermaid 代码块（CDN 懒加载，内容里没有 mermaid 时不加载）
    mermaid?: boolean
    // 右下角悬浮目录（todo 分栏预览空间小，默认不启用）
    toc?: boolean
  }>(),
  { compact: false, mermaid: true, toc: false }
)

const root = ref<HTMLElement | null>(null)
const html = ref('')
// 大纲（由 setupHeadingFold 产出）：文件预览用它渲染目录面板
const outline = ref<MdHeading[]>([])

// marked 定制（代码高亮 + ==高亮==）带 highlight.js，体积不小；
// 用动态 import 拆包，只在真正要渲染 markdown 时才加载（首屏不付这个成本）
let markedConfigured: Promise<void> | null = null
function configureMarkedOnce(): Promise<void> {
  if (!markedConfigured) {
    markedConfigured = import('../../lib/markdownRenderer').then((m) => m.configureMarked())
  }
  return markedConfigured
}

async function render() {
  try {
    await configureMarkedOnce()
    const raw = marked.parse(props.content || '', { async: false }) as string
    html.value = DOMPurify.sanitize(raw)
  } catch (e) {
    console.warn('[MarkdownPreview] render failed', e)
    html.value = ''
  }
}

watch(() => props.content, render, { immediate: true })

// mermaid 脚本按需加载，结果缓存（加载失败后不再重试，避免每次输入都打网络）
let mermaidReady: boolean | null = null
function ensureMermaid(): Promise<boolean> {
  return new Promise((resolve) => {
    if (mermaidReady !== null) return resolve(mermaidReady)
    const s = document.createElement('script')
    s.src = 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js'
    s.crossOrigin = 'anonymous'
    s.onload = () => {
      mermaidReady = true
      ;(window as any).mermaid?.initialize?.({ startOnLoad: false, theme: 'default' })
      resolve(true)
    }
    s.onerror = () => {
      mermaidReady = false
      console.warn('[MarkdownPreview] Failed to load mermaid script; mermaid blocks will render as plain code')
      resolve(false)
    }
    document.head.appendChild(s)
  })
}

async function runMermaid() {
  const ok = await ensureMermaid()
  if (!ok) return
  await nextTick()
  const nodes = root.value?.querySelectorAll('.language-mermaid')
  if (!nodes?.length) return
  try {
    await (window as any).mermaid.run({ nodes, suppressErrors: true })
  } catch {}
}

// v-html patch 之后再增强，否则拿不到真实 DOM
watch(html, async () => {
  await nextTick()
  wrapMarkdownTables(root.value)
  outline.value = setupHeadingFold(root.value)
  if (props.mermaid && html.value.includes('language-mermaid')) runMermaid()
}, { flush: 'post', immediate: true })

// 正文里手写的站内锚点（如 [跳转](#标题)）：改走 scrollToId，
// 避免往浏览器历史塞 hash、且目标被折叠时自动展开
function onAnchorClick(e: MouseEvent) {
  const el = e.target as HTMLElement | null
  const a = el?.closest?.('a[href]') as HTMLAnchorElement | null
  if (!a || !root.value?.contains(a)) return
  const href = a.getAttribute('href') || ''
  if (href.length < 2 || href[0] !== '#') return
  if (scrollToId(root.value, decodeURIComponent(href.slice(1)))) e.preventDefault()
}

// 供外部做滚动同步（todo 编辑器：编辑区滚动时同步预览区）与目录渲染
defineExpose({ el: root, outline })
</script>

<template>
  <div
    ref="root"
    class="markdown-body"
    :class="{ 'markdown-body-compact': compact }"
    v-html="html"
    @click="onAnchorClick"
  />

  <MarkdownToc v-if="toc" :outline="outline" :root="root" />
</template>
