<script setup lang="ts">
// PC 端 AI 对话浮动面板：核心对话逻辑在 composables/useAIChat（与移动端 MAI 共用），
// 本组件只保留面板形态特有部分：fab/开关、宽度拖拽、外部事件监听。
import { ref, reactive, nextTick, onMounted, onUnmounted, watch } from 'vue'
import { loggedIn } from '../../stores/auth'
import { showToast } from '../../lib/common'
import { getAIIndexStatus, type AIIndexStatus } from '../../api/ai'
import { useAIChat } from '../../composables/useAIChat'
import FilePickerModal from '../modal/FilePickerModal.vue'
import ToolCallList from './ToolCallList.vue'
import RagCitations from './RagCitations.vue'
import RagRefTooltip from './RagRefTooltip.vue'

const listEl = ref<HTMLElement | null>(null)

// scrollToBottom(force?) force=true 无条件拉底（发消息/载入会话/流结束）。
// 流式增量遵循 stick-to-bottom：用户上滚（scroll 事件距底 >40px）即暂停跟随，
// 滚回底部自动恢复。不能用纯距离阈值判定——高频增量拉底下用户单次滚轮
// 滚不出逃逸距离，表现为"完全滚不动"。
const stickBottom = ref(true)
let lastProgScroll = 0

function onStickScroll() {
  // 程序拉底自身触发的 scroll 事件不算用户交互（120ms 窗口）
  if (Date.now() - lastProgScroll < 120) return
  const el = listEl.value
  if (!el) return
  stickBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

async function scrollToBottom(force = false) {
  await nextTick()
  const el = listEl.value
  if (!el) return
  if (!force && !stickBottom.value) return
  stickBottom.value = true // 强制拉底场景（发消息等）恢复跟随
  lastProgScroll = Date.now()
  el.scrollTop = el.scrollHeight
}

// ---- 对话核心（composable） ----
const {
  aiEnabled, aiModel, messages, input, streaming,
  pendingCtxFiles, showFilePicker,
  dbSessionId, sessions, sessionsOpen, sessionsLoading,
  visibleMessages, hiddenCount, showMore,
  maybeRestoreLastSession,
  send, stopStreaming, askWith,
  removeCtxFile, onPickFiles,
  toggleSessions, loadSessions, loadSession, removeSession, clearChat, timeAgo,
  copyMessage, regenerate, saveReplyAsFile, approveToolCall,
  isStreamingTail, messageSegments, renderTextMd, usageText, hasCitedRefs, hasReplyContent, userInitial,
  onInputKeydown: onKeydown,
} = useAIChat(scrollToBottom)

// ---- 过程块折叠状态（key = 消息idx:段idx）----
// 默认：流式尾消息展开（看得见执行中），其余折叠；手动 toggle 覆盖默认值
const processManual = reactive(new Map<string, boolean>())

function processKey(i: number, si: number): string {
  return `${i}:${si}`
}

function processOpen(i: number, si: number): boolean {
  const manual = processManual.get(processKey(i, si))
  if (manual !== undefined) return manual
  return isStreamingTail(i)
}

function toggleProcess(i: number, si: number) {
  processManual.set(processKey(i, si), !processOpen(i, si))
}

// 块标题：流式执行中显示动态态；完成后显示工具步数
function processRunning(i: number, si: number): boolean {
  const segs = messageSegments(visibleMessages.value[i])
  return isStreamingTail(i) && si === segs.length - 1 && segs[si]?.kind === 'process'
}

function processTitle(seg: { kind: string; steps: { kind: string }[] }, i: number, si: number): string {
  if (processRunning(i, si)) return '思考与工具调用中…'
  const tools = seg.steps.filter(s => s.kind === 'tool').length
  return tools > 0 ? `思考与工具调用（${tools} 步）` : '思考过程'
}

// ---- 索引状态抽屉（RAG 可视化：哪些文件已索引/失败）----
const indexOpen = ref(false)
const indexLoading = ref(false)
const indexData = ref<AIIndexStatus | null>(null)

function toggleIndexPanel() {
  indexOpen.value = !indexOpen.value
  if (indexOpen.value) {
    sessionsOpen.value = false // 与历史会话抽屉互斥
    loadIndexStatus()
  }
}

// 反向互斥：打开历史会话时收起索引抽屉
watch(sessionsOpen, (v) => { if (v) indexOpen.value = false })

async function loadIndexStatus() {
  indexLoading.value = true
  try {
    indexData.value = await getAIIndexStatus()
  } catch (e: any) {
    showToast('读取索引状态失败：' + (e?.msg || e?.message || ''))
  } finally {
    indexLoading.value = false
  }
}

// ---- 面板特有：开关 / 自动恢复 ----
const panelOpen = ref(false)

// 剧场模式：面板脱离 fab 锚定，视口居中放大（宽 ~1100px、高 ~85vh），
// Esc 或再点按钮退出；移动端不提供（面板本就近乎全宽）
const theater = ref(false)
const isMobile = ref(window.innerWidth <= 900)

function onWindowResize() {
  isMobile.value = window.innerWidth <= 900
  if (isMobile.value) theater.value = false
}
window.addEventListener('resize', onWindowResize)

function toggleTheater() {
  theater.value = !theater.value
  scrollToBottom()
}

function onEsc(e: KeyboardEvent) {
  if (e.key === 'Escape' && theater.value) theater.value = false
}
window.addEventListener('keydown', onEsc)

function togglePanel() {
  panelOpen.value = !panelOpen.value
  if (panelOpen.value) {
    maybeRestoreLastSession()
    scrollToBottom()
  }
}

// ---- 外部事件（TabBar「AI 摘要」/ 底部导航 AI 入口） ----
async function onExternalAsk(e: Event) {
  const { files, question } = (e as CustomEvent).detail || {}
  if (!loggedIn.value || !aiEnabled.value) return
  panelOpen.value = true
  await askWith(Array.isArray(files) ? files : [], question || '')
}

function onExternalOpen() {
  if (!loggedIn.value) return
  if (!aiEnabled.value) {
    showToast('AI 未配置，请在服务端设置 ai.api_key')
    return
  }
  sessionsOpen.value = false
  panelOpen.value = true
  scrollToBottom()
}

// ---- 消息目录刻度：跳转 + 摘要预览 + 滚动律动 ----
const tocHover = ref(-1)
const tocVisible = ref(false)
const tocTipStyle = ref<Record<string, string>>({})
// 当前视口对应的消息索引：刻度条随滚动实时高亮（律动跟随）
const activeToc = ref(0)
let tocRaf = 0

// hover 期间 tip 常驻：切换条目只更新内容与垂直位置（水平位置固定在
// 目录列右缘——不随条宽左右跳动），tip 垂直居中于选中条目
function onTocEnter(i: number, e: MouseEvent) {
  tocHover.value = i
  const item = (e.currentTarget as HTMLElement)
  const toc = item.parentElement
  const itemR = item.getBoundingClientRect()
  const tocR = toc?.getBoundingClientRect()
  const centerY = itemR.top + itemR.height / 2
  const left = (tocR ? tocR.right : itemR.right) + 10
  const top = Math.max(30, Math.min(centerY, window.innerHeight - 60))
  tocTipStyle.value = { left: left + 'px', top: top + 'px' }
}

function onTocLeave() {
  tocVisible.value = false
  tocHover.value = -1
}

function jumpTo(i: number) {
  listEl.value?.querySelector(`[data-idx="${i}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

// 滚动同步：视口顶端可见的第一条消息 → activeToc（rAF 节流）
function onListScroll() {
  onStickScroll() // stick-to-bottom 状态维护（用户上滚暂停跟随）
  if (tocRaf) return
  tocRaf = requestAnimationFrame(() => {
    tocRaf = 0
    const list = listEl.value
    if (!list) return
    const listTop = list.getBoundingClientRect().top
    const threshold = listTop + 60
    let idx = 0
    list.querySelectorAll<HTMLElement>('[data-idx]').forEach(n => {
      if (n.getBoundingClientRect().top <= threshold) {
        idx = Number(n.getAttribute('data-idx')) || 0
      }
    })
    activeToc.value = idx
  })
}

// 刻度条宽度：活跃条最长最显眼，相邻按距离衰减（律动），其余基础长
function tocBarStyle(m: { content: string }, i: number) {
  if (i === activeToc.value) return { width: '28px' }
  const d = Math.abs(i - activeToc.value)
  const w = Math.max(8, 18 - d * 4)
  return { width: w + 'px' }
}

function tocSummary(m: { role: string; content: string; toolCalls?: readonly unknown[] }): string {
  if (m.toolCalls?.length && !m.content) return `[工具调用 ×${m.toolCalls.length}]`
  const s = (m.content || '').replace(/\s+/g, ' ').trim()
  return s.slice(0, 60) + (s.length > 60 ? '…' : '') || '（空）'
}

onMounted(() => {
  window.addEventListener('workbench:ai-ask', onExternalAsk as EventListener)
  window.addEventListener('workbench:ai-open', onExternalOpen)
})
onUnmounted(() => {
  window.removeEventListener('workbench:ai-ask', onExternalAsk as EventListener)
  window.removeEventListener('workbench:ai-open', onExternalOpen)
  window.removeEventListener('resize', onWindowResize)
  window.removeEventListener('keydown', onEsc)
  if (tocRaf) cancelAnimationFrame(tocRaf)
})

// ---- 面板宽度拖拽（左缘），localStorage 持久化 ----
const PANEL_W_KEY = 'workbench-ai-panel-width'
function clampW(w: number): number {
  if (!Number.isFinite(w) || w <= 0) return 440
  return Math.min(720, Math.max(320, Math.round(w)))
}
const panelWidth = ref(clampW(Number(localStorage.getItem(PANEL_W_KEY))))
const resizing = ref(false)

function onResizeStart(e: MouseEvent) {
  if (window.innerWidth <= 768) return // 移动端固定宽度
  resizing.value = true
  const onMove = (ev: MouseEvent) => {
    panelWidth.value = clampW(window.innerWidth - ev.clientX - 20)
  }
  const onUp = () => {
    resizing.value = false
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    try { localStorage.setItem(PANEL_W_KEY, String(panelWidth.value)) } catch { /* 忽略隐私模式 */ }
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
  e.preventDefault()
}
</script>

<template>
<!-- 悬浮按钮统一挂到全局 fab-dock 容器（见 DefaultLayout），与 Markdown 目录等互不感知 -->
<Teleport to="#fab-dock">
  <!-- 未登录 / AI 未配置时不渲染任何入口 -->
  <div v-if="loggedIn && aiEnabled" class="ai-dock-item">
    <!-- 浮动按钮 -->
    <button class="ai-fab" :class="{ open: panelOpen }" title="AI 助手" @click="togglePanel">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
        <path d="M19 15l.9 2.1L22 18l-2.1.9L19 21l-.9-2.1L16 18l2.1-.9z"/>
      </svg>
    </button>

    <!-- 对话面板：锚定在按钮上方；剧场模式 = 视口居中大面板 -->
    <div v-if="panelOpen" class="ai-panel" :class="{ theater }" :style="theater ? undefined : { width: panelWidth + 'px' }">
      <!-- 左缘拖拽调宽 -->
      <div class="ai-resizer" :class="{ active: resizing }" @mousedown="onResizeStart"></div>

      <div class="ai-header">
        <div class="ai-title-wrap">
          <span class="ai-title">AI 助手</span>
          <span v-if="aiModel" class="ai-model">{{ aiModel }}</span>
        </div>
        <div class="ai-header-actions">
          <button class="ai-icon-btn" :class="{ active: indexOpen }" title="文件索引状态" @click="toggleIndexPanel">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/>
            </svg>
          </button>
          <button v-if="!isMobile" class="ai-icon-btn" :class="{ active: theater }" :title="theater ? '退出剧场模式 (Esc)' : '剧场模式'" @click="toggleTheater">
            <svg v-if="!theater" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M15 3h6v6"/><path d="M9 21H3v-6"/><path d="M21 3l-7 7"/><path d="M3 21l7-7"/>
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M4 14h6v6"/><path d="M20 10h-6V4"/><path d="M14 10l7-7"/><path d="M3 21l7-7"/>
            </svg>
          </button>
          <button class="ai-icon-btn" :class="{ active: sessionsOpen }" title="历史会话" @click="toggleSessions">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>
            </svg>
          </button>
          <button class="ai-icon-btn" title="新对话" @click="clearChat">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 5v14M5 12h14"/>
            </svg>
          </button>
          <button class="ai-icon-btn" title="收起" @click="panelOpen = false">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>
          </button>
        </div>
      </div>

      <!-- 文件索引状态（覆盖消息区：RAG 可视化，哪些文件已索引/失败） -->
      <div v-if="indexOpen" class="ai-sessions ai-index">
        <div class="ai-sessions-head">
          <span>文件索引状态</span>
          <button class="ai-icon-btn" title="刷新" @click="loadIndexStatus">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
          </button>
        </div>
        <div class="ai-index-summary">
          <template v-if="indexData">
            <span>{{ indexData.summary.files }} 个文件</span>
            <span>{{ indexData.summary.chunks }} 个文本块</span>
            <span :class="{ err: indexData.summary.errors > 0 }">{{ indexData.summary.errors }} 个失败</span>
          </template>
        </div>
        <div class="ai-sessions-list">
          <div v-if="indexLoading && !indexData" class="ai-sessions-empty">加载中…</div>
          <div v-else-if="!indexData?.items?.length" class="ai-sessions-empty">
            暂无索引（上传文本文件或触发全量重建）
          </div>
          <div v-for="it in indexData?.items" :key="it.path" class="ai-index-item" :title="it.error ? it.path + '：' + it.error : it.path">
            <span class="ai-index-dot" :class="it.status"></span>
            <span class="ai-index-path">{{ it.path }}</span>
            <span class="ai-index-meta">{{ it.status === 'ok' ? it.chunkCount + ' 块' : '失败' }}</span>
          </div>
        </div>
      </div>

      <!-- 历史会话列表（覆盖消息区，点击恢复继续沟通） -->
      <div v-if="sessionsOpen" class="ai-sessions">
        <div class="ai-sessions-head">
          <span>历史会话</span>
          <button class="ai-icon-btn" title="刷新" @click="loadSessions">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
          </button>
        </div>
        <div class="ai-sessions-list">
          <div v-if="sessionsLoading && !sessions.length" class="ai-sessions-empty">加载中…</div>
          <div v-else-if="!sessions.length" class="ai-sessions-empty">暂无历史会话</div>
          <div
            v-for="s in sessions"
            :key="s.id"
            class="ai-session-item"
            :class="{ current: s.id === dbSessionId }"
            :title="s.title"
            @click="loadSession(s.id)"
          >
            <div class="ai-session-title">{{ s.title || '新对话' }}</div>
            <div class="ai-session-meta">
              <span>{{ timeAgo(s.updatedAt) }}</span>
              <span v-if="s.id === dbSessionId" class="ai-session-current-tag">当前</span>
              <button class="ai-session-del" title="删除会话" @click="removeSession(s.id, $event)">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/></svg>
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 消息目录刻度（固定于消息区左缘，不随滚动移动；滚动时律动跟随） -->
      <div
        v-if="messages.length > 6 && !sessionsOpen"
        class="ai-toc"
        @mouseenter="tocVisible = true"
        @mouseleave="onTocLeave"
      >
        <div
          v-for="(m, i) in visibleMessages"
          :key="'toc' + i"
          class="ai-toc-item"
          :class="{ active: i === activeToc }"
          @click="jumpTo(i)"
          @mouseenter="onTocEnter(i, $event)"
        >
          <span class="ai-toc-bar" :class="m.role" :style="tocBarStyle(m, i)"></span>
        </div>
      </div>
      <!-- 摘要卡：Teleport 到 body，fixed 定位（不受 toc 容器 overflow 裁剪）；
           hover 期间常驻，切换条目只更新内容与位置（不闪烁） -->
      <Teleport to="body">
        <div v-if="tocVisible && tocHover >= 0 && visibleMessages[tocHover]" class="ai-toc-tip" :style="tocTipStyle">
          <div class="ai-toc-tip-title">{{ visibleMessages[tocHover].role === 'user' ? '我的提问' : 'AI 回复' }}</div>
          <div class="ai-toc-tip-body">{{ tocSummary(visibleMessages[tocHover]) }}</div>
        </div>
      </Teleport>

      <div v-show="!sessionsOpen" ref="listEl" class="ai-list" @scroll.passive="onListScroll">
        <button v-if="hiddenCount" class="ai-load-more" @click="showMore">
          加载更早的 {{ hiddenCount }} 条消息
        </button>

        <div v-if="messages.length === 0" class="ai-empty">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
          </svg>
          <div>有问题尽管问，Enter 发送</div>
        </div>

        <div v-for="(m, i) in visibleMessages" :key="i" :data-idx="i" class="ai-msg" :class="m.role">
          <!-- hover 工具条：复制 / 重新生成（末尾回复）/ 保存为文件 -->
          <div class="ai-msg-actions">
            <button
              v-if="m.role === 'assistant' && !streaming && i === visibleMessages.length - 1 && hasReplyContent(m)"
              class="ai-act" title="重新生成" @click="regenerate"
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
            </button>
            <button
              v-if="m.role === 'assistant' && !streaming && m.content"
              class="ai-act" title="保存为文件（/ai/）" @click="saveReplyAsFile(m, i)"
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><path d="M17 21v-8H7v8M7 3v5h8"/></svg>
            </button>
            <button v-if="m.content" class="ai-act" title="复制" @click="copyMessage(m)">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
            </button>
          </div>

          <!-- 头像：user 首字母 / assistant 星标 -->
          <div class="ai-avatar" :class="m.role">
            <template v-if="m.role === 'user'">{{ userInitial }}</template>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
            </svg>
          </div>

          <!-- 气泡：assistant 按时序分段渲染（过程块可折叠 + 正文段），user 纯文本 -->
          <div class="ai-bubble" :class="m.role">
            <template v-if="m.role === 'assistant'">
              <template v-for="(seg, si) in messageSegments(m)" :key="si">
                <!-- 过程块（思考 + 工具调用，时间序）：默认折叠，流式尾消息展开 -->
                <div v-if="seg.kind === 'process'" class="ai-process">
                  <button class="ai-process-toggle" @click="toggleProcess(i, si)">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M9.1 12a3 3 0 1 0 5.8 1c.6-1.8-1-2.4-1-4A3 3 0 0 0 12 6"/>
                      <circle cx="12" cy="3" r=".8" fill="currentColor"/><circle cx="5" cy="12" r=".8" fill="currentColor"/><circle cx="19" cy="12" r=".8" fill="currentColor"/>
                    </svg>
                    <span :class="{ dim: processRunning(i, si) }">
                      {{ processTitle(seg, i, si) }}
                    </span>
                    <svg class="ai-process-chevron" :class="{ open: processOpen(i, si) }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>
                  </button>
                  <div v-show="processOpen(i, si)" class="ai-process-body">
                    <template v-for="(s, ki) in seg.steps" :key="ki">
                      <div v-if="s.kind === 'reasoning'" class="ai-process-reasoning">{{ s.text }}</div>
                      <ToolCallList v-else-if="s.call" :calls="[s.call]" @approve="approveToolCall" />
                    </template>
                  </div>
                </div>
                <!-- 正文段（Agent 中间轮说明文本 / 最终回答） -->
                <div v-else-if="seg.text" class="ai-md" v-html="renderTextMd(seg.text, m)"></div>
              </template>
              <span v-if="isStreamingTail(i)" class="ai-cursor">▍</span>
              <!-- RAG 引用来源：只展示模型实际引用的片段（正文出现对应 [n]） -->
              <RagCitations v-if="hasCitedRefs(m)" :hits="m.ragHits" />
              <!-- token 消耗（Agent 多轮已聚合；上游不支持 usage 时缺省不显示） -->
              <div v-if="usageText(m)" class="ai-usage">{{ usageText(m) }}</div>
            </template>
            <template v-else>
              {{ m.content }}
              <div v-if="m.files?.length" class="ai-msg-files">
                <span v-for="f in m.files" :key="f" class="ai-msg-file" :title="f">
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2">
                    <path d="M3 1h6l5 5v9H3V1z"/><path d="M9 1v5h5"/>
                  </svg>
                  <span class="ai-msg-file-name">{{ f.split('/').pop() || f }}</span>
                </span>
              </div>
            </template>
          </div>
        </div>
      </div>

      <!-- 输入卡片：textarea 融入卡片；@ 触发文件引用；底部工具行（左提示 / 右圆形发送） -->
      <div class="ai-input">
        <!-- 待引用文件 chips（@ 选择后出现，可移除，发送时注入） -->
        <div v-if="pendingCtxFiles.length" class="ai-ctx-list">
          <span v-for="p in pendingCtxFiles" :key="p" class="ai-ctx-chip" :title="p">
            <svg class="ai-ctx-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2">
              <path d="M3 1h6l5 5v9H3V1z"/><path d="M9 1v5h5"/>
            </svg>
            <span class="ai-ctx-name">{{ p.split('/').pop() || p }}</span>
            <button class="ai-ctx-remove" title="移除引用" @click="removeCtxFile(p)">&times;</button>
          </span>
        </div>
        <textarea
          v-model="input"
          rows="2"
          placeholder="发送消息，输入 @ 引用文件"
          @keydown="onKeydown"
        ></textarea>
        <div class="ai-input-toolbar">
          <div class="ai-toolbar-left">
            <span class="ai-input-hint">@ 引用文件 · Enter 发送 · Shift+Enter 换行</span>
          </div>
          <button v-if="streaming" class="ai-send stop" title="停止生成" @click="stopStreaming">
            <svg viewBox="0 0 24 24" fill="currentColor"><rect x="7" y="7" width="10" height="10" rx="2"/></svg>
          </button>
          <button v-else class="ai-send" :disabled="!input.trim()" title="发送" @click="send">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 19V5M5 12l7-7 7 7"/>
            </svg>
          </button>
        </div>
      </div>
    </div>
  </div>
</Teleport>

<!-- @ 文件引用选择器 -->
<FilePickerModal
  v-if="loggedIn && aiEnabled && showFilePicker"
  title="引用文件"
  @confirm="onPickFiles"
  @close="showFilePicker = false"
/>

<!-- RAG 引用编号悬浮预览（全局委托，body teleport） -->
<RagRefTooltip />
</template>

<style scoped>
/* dock 条目：相对定位供面板锚定；order 控制叠放位（AI 在上、目录在下） */
.ai-dock-item {
  position: relative;
  order: 10;
}

/* 浮动按钮（与目录按钮 .md-toc-fab 同尺寸 38px） */
.ai-fab {
  width: 38px;
  height: 38px;
  border-radius: 50%;
  border: none;
  background: linear-gradient(135deg, #007acc, #4d8de0);
  color: #fff;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 2px 10px rgba(0, 0, 0, .25);
  transition: transform .15s, background .15s;
}
.ai-fab:hover { transform: scale(1.06); }
.ai-fab.open { background: var(--text-dim); }
.ai-fab svg { width: 18px; height: 18px; }

/* 对话面板：锚定在 dock 条目（按钮）上方 */
.ai-panel {
  position: absolute;
  right: 0;
  bottom: calc(100% + 10px);
  max-width: calc(100vw - 40px);
  height: min(600px, calc(100vh - 130px));
  display: flex;
  flex-direction: column;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: -8px 0 24px rgba(0, 0, 0, .08), 0 8px 32px rgba(0, 0, 0, .16);
  overflow: hidden;
}

/* 剧场模式：脱离 fab 锚定，视口居中放大（不遮挡整个屏幕，背景仍可见） */
.ai-panel.theater {
  position: fixed;
  left: 50%;
  top: 50%;
  right: auto;
  bottom: auto;
  transform: translate(-50%, -50%);
  width: min(1100px, calc(100vw - 80px));
  height: min(820px, calc(100vh - 60px));
  z-index: 1500;
}
.ai-panel.theater .ai-resizer { display: none; }

/* ---- 消息目录刻度：固定在消息区左缘，hover 预览摘要（Teleport body），点击跳转 ---- */
.ai-toc {
  position: absolute;
  left: 0;
  top: 48px;
  bottom: 106px;
  width: 48px; /* 容纳最长活跃条（38px）不裁圆角 */
  display: flex;
  flex-direction: column;
  justify-content: center; /* 刻度组垂直居中 */
  align-items: flex-start; /* 条左对齐：贴分隔线向右伸展 */
  gap: 3px;
  padding: 2px 5px;
  overflow: hidden auto;
  z-index: 2;
}
.ai-toc-item {
  display: flex;
  align-items: center;
  cursor: pointer;
  flex-shrink: 0;
}
.ai-toc-bar {
  height: 4px;
  border-radius: 2px;
  background: var(--border);
  opacity: .9;
  transition: background .15s, width .15s;
}
.ai-toc-item:hover .ai-toc-bar { background: var(--accent); opacity: 1; }
.ai-toc-item.active .ai-toc-bar { background: var(--text); opacity: 1; }
.ai-toc-bar.user { background: var(--accent); opacity: .5; }
.ai-toc-item:hover .ai-toc-bar.user { opacity: 1; }
.ai-toc-tip {
  position: fixed;
  transform: translateY(-50%); /* 垂直居中于选中条目 */
  max-width: 300px;
  background: #1c2127;
  color: #e8eaed;
  border-radius: 8px;
  padding: 9px 12px;
  font-size: 12px;
  line-height: 1.55;
  white-space: normal;
  word-break: break-all;
  box-shadow: 0 6px 20px rgba(0, 0, 0, .28);
  z-index: 9999;
  /* 出现时淡入；hover 切换条目时仅垂直平滑跟随（水平固定不跳动） */
  animation: toc-tip-in .15s ease-out;
  transition: left .12s ease-out, top .12s ease-out;
}
@keyframes toc-tip-in {
  from { opacity: 0; }
  to { opacity: 1; }
}
.ai-toc-tip-title {
  font-weight: 600;
  font-size: 12px;
  margin-bottom: 3px;
  color: #fff;
}
.ai-toc-tip-body { color: #b8bfc7; }

/* 左缘拖拽调宽：默认隐形细条，hover/拖拽时淡显居中线 */
.ai-resizer {
  position: absolute;
  left: -3px;
  top: 0;
  bottom: 0;
  width: 8px;
  cursor: col-resize;
  z-index: 1;
  background: transparent;
}
.ai-resizer::after {
  content: '';
  position: absolute;
  left: 3px;
  top: 0;
  bottom: 0;
  width: 2px;
  border-radius: 2px;
  background: transparent;
  transition: background .15s;
}
.ai-resizer:hover::after,
.ai-resizer.active::after {
  background: var(--accent);
  opacity: .55;
}

/* 头部 */
.ai-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--sidebar-bg);
  flex-shrink: 0;
}
.ai-title-wrap { display: flex; align-items: center; gap: 8px; min-width: 0; }
.ai-title { font-size: 13px; font-weight: 600; color: var(--text); }
.ai-model {
  font-size: 10px;
  color: var(--text-dim);
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 9px;
  padding: 1px 8px;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ai-header-actions { display: flex; gap: 2px; }
.ai-icon-btn {
  border: none;
  background: none;
  color: var(--text-dim);
  cursor: pointer;
  padding: 4px;
  border-radius: 4px;
  display: flex;
}
.ai-icon-btn:hover { background: var(--hover); color: var(--text); }
.ai-icon-btn.active { color: var(--accent); }
.ai-icon-btn svg { width: 15px; height: 15px; }

/* 历史会话列表（覆盖消息区） */
/* 文件索引状态抽屉（复用 ai-sessions 骨架） */
.ai-index-summary {
  display: flex; gap: 14px; padding: 6px 12px;
  font-size: 11px; color: var(--text-dim);
  border-bottom: 1px solid var(--border);
}
.ai-index-summary .err { color: #c04040; }
.ai-index-item {
  display: flex; align-items: center; gap: 8px;
  padding: 5px 12px; font-size: 11px; cursor: default;
}
.ai-index-dot {
  flex: none; width: 7px; height: 7px; border-radius: 50%;
  background: #4caf50;
}
.ai-index-dot.error { background: #c04040; }
.ai-index-path {
  flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-family: var(--font-mono, monospace); color: var(--text);
}
.ai-index-meta { flex: none; color: var(--text-dim); }

.ai-sessions {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.ai-sessions-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 14px 4px;
  font-size: 11px;
  font-weight: 600;
  color: var(--text-dim);
  text-transform: uppercase;
  letter-spacing: .5px;
}
.ai-sessions-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ai-sessions-empty {
  text-align: center;
  color: var(--text-muted);
  font-size: 12px;
  margin-top: 24px;
}
.ai-session-item {
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg);
  cursor: pointer;
  transition: border-color .15s;
}
.ai-session-item:hover { border-color: var(--accent); }
.ai-session-item.current { border-color: var(--accent); background: var(--active, #e4e6f1); }
.ai-session-title {
  font-size: 12px;
  color: var(--text);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ai-session-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 3px;
  font-size: 10px;
  color: var(--text-muted);
}
.ai-session-current-tag {
  color: var(--accent);
  border: 1px solid var(--accent);
  border-radius: 8px;
  padding: 0 5px;
}
.ai-session-del {
  margin-left: auto;
  border: none;
  background: none;
  color: var(--text-dim);
  cursor: pointer;
  padding: 2px;
  border-radius: 4px;
  display: flex;
}
.ai-session-del:hover { background: #fde8ea; color: #cf222e; }
.ai-session-del svg { width: 11px; height: 11px; }

/* 消息列表 */
.ai-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 14px 14px 14px 52px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.ai-empty {
  color: var(--text-muted);
  font-size: 12px;
  text-align: center;
  margin-top: 28px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.ai-empty svg { width: 28px; height: 28px; opacity: .5; }

/* 消息行：头像 + 气泡 */
.ai-msg { display: flex; gap: 8px; align-items: flex-start; position: relative; }
.ai-msg.user { flex-direction: row-reverse; }

/* hover 工具条：浮在消息行右上角 */

/* 气泡 */
/* user 气泡内引用文件名 */
/* 文件名单独一层才能生效 ellipsis（裸文本节点不参与 text-overflow） */
.ai-msg-file-name {
  min-width: 0;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ai-cursor-dim { color: var(--text-dim); }
/* 输入区：卡片式（无边框 textarea 融入卡片，底部工具行） */
.ai-input {
  margin: 10px 12px 12px;
  padding: 10px 12px 8px;
  border: 1px solid var(--border);
  border-radius: 14px;
  background: var(--bg);
  flex-shrink: 0;
  transition: border-color .15s;
}
.ai-input:focus-within { border-color: var(--accent); }
.ai-input textarea {
  display: block;
  width: 100%;
  resize: none;
  border: none;
  padding: 0;
  font-size: 13px;
  font-family: inherit;
  line-height: 1.55;
  background: transparent;
  color: var(--text);
  outline: none;
}
.ai-input textarea::placeholder { color: var(--text-muted); }

/* 底部工具行：左（引用开关 + 提示）/ 右圆形发送 */
.ai-input-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 4px;
}
.ai-toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

/* 待引用文件 chips（@ 选择后显示在输入框上方） */
.ai-ctx-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 6px;
}
.ai-ctx-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--code-bg);
  color: var(--text);
  font-size: 11px;
  padding: 2px 4px 2px 7px;
}
.ai-ctx-icon { width: 11px; height: 11px; flex-shrink: 0; color: var(--text-dim); }
.ai-ctx-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 140px;
}
.ai-ctx-remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  flex-shrink: 0;
  border: none;
  border-radius: 50%;
  background: none;
  color: var(--text-dim);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}
.ai-ctx-remove:hover { background: #fde8ea; color: #cf222e; }

/* 工具行提示 */
.ai-input-hint {
  font-size: 11px;
  color: var(--text-muted);
  user-select: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 工具栏功能 chips（DeepSeek 风格：图标 + 文字，active 高亮） */
.ai-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 160px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: transparent;
  color: var(--text-dim);
  font-size: 11px;
  padding: 2px 8px;
  cursor: pointer;
  transition: all .15s;
  white-space: nowrap;
}
.ai-chip svg { width: 12px; height: 12px; flex-shrink: 0; }
.ai-chip .ai-chip-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ai-chip:hover { color: var(--text); border-color: var(--text-dim); }
.ai-chip.active {
  color: var(--accent);
  border-color: var(--accent);
  background: var(--active, #e4e6f1);
}
.ai-chip:disabled { opacity: .6; cursor: default; }

.ai-send {
  border: none;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  cursor: pointer;
  width: 30px;
  height: 30px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: opacity .15s;
}
.ai-send svg { width: 15px; height: 15px; }
.ai-send:disabled { opacity: .35; cursor: default; }
.ai-send.stop { background: var(--text-dim); }

/* 移动端：面板近乎占满宽度（锚定 dock 条目右缘，dock 距视口右缘 12px），禁拖拽；
   fab 按钮隐藏（底部导航已有 AI 入口），面板经 dock 条目仍可锚定显示 */
@media (max-width: 900px) {
  .ai-panel { width: calc(100vw - 24px) !important; }
  .ai-resizer { display: none; }
  .ai-fab { display: none; }
  .ai-dock-item { width: 0; height: 0; }
}
</style>
