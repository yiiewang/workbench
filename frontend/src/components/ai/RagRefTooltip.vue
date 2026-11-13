<script lang="ts">
// RAG 引用悬浮预览（全局单例）：document 级事件委托监听 .rag-ref hover，
// 读元素 data-*（path/line/quote，由 markdownRenderer.linkRagRefs 注入）。
// PC 面板与移动端各挂载一个组件实例，但状态/委托必须是模块级单份
//（实例级闭包会导致后挂载实例的 handler 未注册、tooltip 失效），
// 故放在普通 <script> 块（模块作用域，执行一次）而非 <script setup>（每实例一次）。
import { ref } from 'vue'

const visible = ref(false)
const path = ref('')
const line = ref(0)
const quote = ref('')
const below = ref(false)
const pos = ref({ left: 0, top: 0 })
let hideTimer = 0

function show(el: HTMLElement) {
  path.value = el.dataset.path || ''
  line.value = Number(el.dataset.line) || 0
  quote.value = el.dataset.quote || ''
  const r = el.getBoundingClientRect()
  // 预期宽 ~420px：水平钳制在视口内；垂直默认在元素上方，空间不足（卡片高约 240px）时翻到下方
  const left = Math.min(Math.max(12, r.left + r.width / 2 - 210), window.innerWidth - 432)
  below.value = r.top < 260
  pos.value = { left, top: below.value ? r.bottom + 8 : r.top - 8 }
  visible.value = true
}

function onOver(e: MouseEvent) {
  const el = (e.target as Element)?.closest?.('.rag-ref') as HTMLElement | null
  if (!el) return
  clearTimeout(hideTimer)
  show(el)
}

function onOut(e: MouseEvent) {
  if (!(e.target as Element)?.closest?.('.rag-ref')) return
  clearTimeout(hideTimer)
  hideTimer = window.setTimeout(() => { visible.value = false }, 200)
}

function keep() { clearTimeout(hideTimer) }
function scheduleHide() {
  clearTimeout(hideTimer)
  hideTimer = window.setTimeout(() => { visible.value = false }, 200)
}

// 实例引用计数（模块级）：首个实例注册 document 委托，最后一个卸载时移除
let instanceCount = 0
let bound = false
</script>

<script setup lang="ts">
import { onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { openRagFile } from '../../lib/ragNav'

const route = useRoute()
const router = useRouter()

// 悬浮卡片里的文件路径点击 → 打开文件（同引用列表跳转逻辑）
async function openPath() {
  if (!path.value) return
  visible.value = false
  await openRagFile(router, route, path.value, line.value)
}

onMounted(() => {
  instanceCount++
  if (bound) return
  bound = true
  document.addEventListener('mouseover', onOver)
  document.addEventListener('mouseout', onOut)
})
onBeforeUnmount(() => {
  instanceCount = Math.max(0, instanceCount - 1)
  visible.value = false
  clearTimeout(hideTimer)
  if (instanceCount === 0 && bound) {
    bound = false
    document.removeEventListener('mouseover', onOver)
    document.removeEventListener('mouseout', onOut)
  }
})
</script>

<template>
  <Teleport to="body">
    <div
      v-if="visible"
      class="rag-ref-tip"
      :class="{ below }"
      :style="{ left: pos.left + 'px', top: pos.top + 'px' }"
      @mouseenter="keep"
      @mouseleave="scheduleHide"
    >
      <div class="rag-ref-tip-head">
        <button class="rag-ref-tip-path" title="打开文件" @click="openPath">{{ path }}</button>
        <span v-if="line > 0" class="rag-ref-tip-line">第 {{ line }} 行起</span>
      </div>
      <div class="rag-ref-tip-quote">{{ quote }}</div>
    </div>
  </Teleport>
</template>

<style scoped>
.rag-ref-tip {
  position: fixed;
  transform: translate(0, -100%);
  width: 420px;
  max-width: calc(100vw - 24px);
  background: var(--bg, #fff);
  border: 1px solid var(--border, #ddd);
  border-radius: 8px;
  box-shadow: 0 8px 28px rgba(0, 0, 0, .18);
  padding: 8px 10px;
  z-index: 1600;
  pointer-events: auto;
}
.rag-ref-tip.below { transform: none; }
.rag-ref-tip-head {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  margin-bottom: 5px; font-size: 11px;
}
.rag-ref-tip-path {
  color: var(--accent, #007acc);
  font-family: var(--font-mono, monospace);
  word-break: break-all;
  background: none; border: none; padding: 0; cursor: pointer;
  font-size: 11px; text-align: left;
}
.rag-ref-tip-path:hover { text-decoration: underline; }
.rag-ref-tip-line { color: var(--text-dim, #888); }
.rag-ref-tip-quote {
  font-size: 11px; line-height: 1.6; color: var(--text-dim, #666);
  white-space: pre-wrap; word-break: break-word;
  max-height: 200px; overflow-y: auto;
  border-left: 2px solid var(--border, #ddd);
  padding-left: 8px;
}
</style>
