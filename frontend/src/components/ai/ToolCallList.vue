<script setup lang="ts">
// Agent 工具调用卡片列表：每次工具调用一张可折叠卡片（名称 + 参数摘要），
// 展开后显示执行结果 JSON。result 未到达 = 执行中（转圈态）。
// 由 PC 面板（AiChat.vue）与移动端（MAI.vue）共用，样式在全局 ai-chat.css。
import { ref } from 'vue'
import type { ToolCallView } from '../../api/ai'

const props = defineProps<{ calls: ToolCallView[] }>()

const open = ref(new Set<number>())

function toggle(i: number) {
  const s = new Set(open.value)
  if (s.has(i)) s.delete(i)
  else s.add(i)
  open.value = s
}

// 参数摘要：解析 JSON 后截断展示（模型参数一般很短）
function argSummary(args: string): string {
  let s = args
  try {
    const o = JSON.parse(args)
    s = Object.entries(o).map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`).join(' ')
  } catch { /* 保留原文 */ }
  if (!s) s = '(无参数)'
  return s.length > 120 ? s.slice(0, 120) + '…' : s
}

// 结果文本：对象美化缩进，超长截断
function resultText(r: unknown): string {
  let s: string
  if (typeof r === 'string') s = r
  else { try { s = JSON.stringify(r, null, 2) ?? '' } catch { s = String(r) } }
  return s.length > 2000 ? s.slice(0, 2000) + '\n…（已截断）' : s
}

const hasError = (r: unknown) => !!r && typeof r === 'object' && 'error' in (r as Record<string, unknown>)
</script>

<template>
  <div class="ai-tools">
    <div v-for="(c, i) in props.calls" :key="c.id || i" class="ai-tool" :class="{ 'ai-tool-err': hasError(c.result) }">
      <button class="ai-tool-head" @click="toggle(i)">
        <svg class="ai-tool-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>
        </svg>
        <span class="ai-tool-name">{{ c.name || '未知工具' }}</span>
        <span class="ai-tool-args">{{ argSummary(c.arguments) }}</span>
        <span v-if="c.result === undefined" class="ai-tool-pending">执行中…</span>
        <svg class="ai-tool-chevron" :class="{ 'is-open': open.has(i) }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M6 9l6 6 6-6"/>
        </svg>
      </button>
      <pre v-show="open.has(i)" class="ai-tool-result">{{ resultText(c.result) }}</pre>
    </div>
  </div>
</template>
