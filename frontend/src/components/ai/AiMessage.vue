<script setup lang="ts">
// 单条消息渲染：PC 浮动面板与移动端全屏页共用，消除两份重复模板。
// 会话状态与操作函数由父级 provide（aiChatKey），子组件不再调用 useAIChat——
// 否则会生成独立会话状态，审批/复制/重新生成会作用在错误的实例上。
import { computed, inject, reactive } from 'vue'
import type { ChatMessage } from '../../api/ai'
import { aiChatKey, type MsgSegment } from '../../composables/useAIChat'
import ToolCallList from './ToolCallList.vue'
import RagCitations from './RagCitations.vue'

const props = defineProps<{ message: ChatMessage; index: number }>()

const chat = inject(aiChatKey)!
const streaming = chat.streaming
const userInitial = chat.userInitial

const segments = computed(() => chat.messageSegments(props.message))
const isLast = computed(() => props.index === chat.visibleMessages.value.length - 1)
const streamingTail = computed(() => chat.isStreamingTail(props.index))
const usage = computed(() => chat.usageText(props.message))
const cited = computed(() => chat.hasCitedRefs(props.message))

// 过程块折叠状态：组件对应单条消息，key 只需段 idx。
// 流式尾消息默认展开（看得见执行中），手动切换覆盖默认值。
const processManual = reactive(new Map<number, boolean>())

function processOpen(si: number): boolean {
  const manual = processManual.get(si)
  return manual !== undefined ? manual : streamingTail.value
}

function toggleProcess(si: number) {
  processManual.set(si, !processOpen(si))
}

function processRunning(si: number): boolean {
  return streamingTail.value && si === segments.value.length - 1 && segments.value[si]?.kind === 'process'
}

function processTitle(seg: MsgSegment, si: number): string {
  if (processRunning(si)) return '思考与工具调用中…'
  const tools = seg.steps.filter(s => s.kind === 'tool').length
  return tools > 0 ? `思考与工具调用（${tools} 步）` : '思考过程'
}
</script>

<template>
  <div class="ai-msg" :class="message.role" :data-idx="index">
    <div class="ai-avatar" :class="message.role">
      <template v-if="message.role === 'user'">{{ userInitial }}</template>
      <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
      </svg>
    </div>

    <div class="ai-msg-col">
      <template v-if="message.role === 'assistant'">
        <template v-for="(seg, si) in segments" :key="si">
          <div v-if="seg.kind === 'process'" class="ai-process">
            <button class="ai-process-toggle" @click="toggleProcess(si)">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                <path d="M9.1 12a3 3 0 1 0 5.8 1c.6-1.8-1-2.4-1-4A3 3 0 0 0 12 6"/>
                <circle cx="12" cy="3" r=".8" fill="currentColor"/><circle cx="5" cy="12" r=".8" fill="currentColor"/><circle cx="19" cy="12" r=".8" fill="currentColor"/>
              </svg>
              <span :class="{ dim: processRunning(si) }">{{ processTitle(seg, si) }}</span>
              <svg class="ai-process-chevron" :class="{ open: processOpen(si) }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>
            </button>
            <div v-show="processOpen(si)" class="ai-process-body">
              <template v-for="(s, ki) in seg.steps" :key="ki">
                <div v-if="s.kind === 'reasoning'" class="ai-process-reasoning">{{ s.text }}</div>
                <ToolCallList v-else-if="s.call" :calls="[s.call]" @approve="chat.approveToolCall" />
              </template>
            </div>
          </div>
          <div v-else-if="seg.text" class="ai-md" v-html="chat.renderTextMd(seg.text || '', message)"></div>
        </template>
        <span v-if="streamingTail" class="ai-cursor">▍</span>
      </template>

      <template v-else>
        <div class="ai-bubble user">
          {{ message.content }}
          <div v-if="message.files?.length" class="ai-msg-files">
            <span v-for="f in message.files" :key="f" class="ai-msg-file" :title="f">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2">
                <path d="M3 1h6l5 5v9H3V1z"/><path d="M9 1v5h5"/>
              </svg>
              <span class="ai-msg-file-name">{{ f.split('/').pop() || f }}</span>
            </span>
          </div>
        </div>
      </template>

      <!-- 元信息行：操作 / 引用来源 / token 消耗统一为一条轻量行 -->
      <div class="ai-meta">
        <div class="ai-msg-actions">
          <button v-if="message.role === 'assistant' && !streaming && isLast && chat.hasReplyContent(message)" class="ai-act" title="重新生成" @click="chat.regenerate()">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
          </button>
          <button v-if="message.role === 'assistant' && !streaming && message.content" class="ai-act" title="保存为文件（/ai/）" @click="chat.saveReplyAsFile(message, index)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><path d="M17 21v-8H7v8M7 3v5h8"/></svg>
          </button>
          <button v-if="message.content" class="ai-act" title="复制" @click="chat.copyMessage(message)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          </button>
        </div>
        <RagCitations v-if="cited" :hits="message.ragHits" />
        <span v-if="usage" class="ai-usage">{{ usage }}</span>
      </div>
    </div>
  </div>
</template>
