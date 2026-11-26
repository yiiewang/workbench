<script setup lang="ts">
// PC 端 AI 对话浮动面板：核心对话逻辑在 composables/useAIChat（与移动端 MAI 共用），
// 本组件只保留面板形态特有部分：fab/开关、宽度拖拽、外部事件监听。
import { ref, computed, watch, nextTick, onMounted, onUnmounted, provide } from 'vue'
import { loggedIn } from '../../stores/auth'
import { showToast } from '../../lib/common'
import { hasOpenModal } from '../../lib/modalEsc'
import { getAIIndexStatus, type AIIndexStatus } from '../../api/ai'
import { useAIChat, aiChatKey } from '../../composables/useAIChat'
import FilePickerModal from '../modal/FilePickerModal.vue'
import AiMessage from './AiMessage.vue'
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

// ---- 对话核心（composable）：整份实例 provide 给消息组件 ----
const chat = useAIChat(scrollToBottom)
provide(aiChatKey, chat)
const {
  aiEnabled, aiModel, messages, input, streaming,
  pendingCtxFiles, showFilePicker,
  dbSessionId, streamingSessions, sessions, sessionsLoading,
  visibleMessages, hiddenCount, showMore,
  maybeRestoreLastSession,
  send, stopStreaming, askWith,
  removeCtxFile, onPickFiles,
  loadSessions, loadSession, removeSession, clearChat, timeAgo,
  onInputKeydown: onKeydown,
} = chat

// ---- 侧栏：会话 / 索引状态 / 消息目录三合一 ----
// 三个入口不再各自覆盖消息区，统一在左栏切 tab；同一 tab 再点即收起。
type RailTab = 'sessions' | 'index' | 'toc'
const railOpen = ref(false)
const railTab = ref<RailTab>('sessions')
const railTabs: { id: RailTab; label: string }[] = [
  { id: 'sessions', label: '会话' },
  { id: 'index', label: '索引' },
  { id: 'toc', label: '目录' },
]

function loadRailData(tab: RailTab) {
  if (tab === 'sessions') loadSessions()
  if (tab === 'index') loadIndexStatus()
}

function openRail(tab: RailTab) {
  if (railOpen.value && railTab.value === tab) {
    railOpen.value = false
    return
  }
  railTab.value = tab
  railOpen.value = true
  loadRailData(tab)
}

// 标题栏图标是第二个打开侧栏的入口，之前只切 railOpen 不拉数据，
// 于是默认「会话」tab 显示 0 个会话，必须手动点刷新。
function toggleRail() {
  railOpen.value = !railOpen.value
  if (railOpen.value) loadRailData(railTab.value)
}

// 会话首次落库后列表仍是空的（侧栏打开时不会重拉），补一次刷新
watch(dbSessionId, (id) => {
  if (id != null && railOpen.value && railTab.value === 'sessions') loadSessions()
})

// 索引状态数据（RAG 可视化：哪些文件已索引/失败）
const indexLoading = ref(false)
const indexData = ref<AIIndexStatus | null>(null)

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

// 明细按目录分组（后端已按 path 排序），目录只显示一次、组内只留文件名
type IndexRow = AIIndexStatus['items'][number] & { name: string }
type IndexGroup = { dir: string; rows: IndexRow[]; chunks: number; errors: number }

const indexGroups = computed<IndexGroup[]>(() => {
  const groups: IndexGroup[] = []
  for (const it of indexData.value?.items ?? []) {
    const i = it.path.lastIndexOf('/')
    const dir = it.path.slice(0, i + 1)
    let g = groups[groups.length - 1]
    if (!g || g.dir !== dir) {
      g = { dir, rows: [], chunks: 0, errors: 0 }
      groups.push(g)
    }
    g.rows.push({ ...it, name: it.path.slice(i + 1) })
    g.chunks += it.chunkCount
    if (it.status !== 'ok') g.errors++
  }
  return groups
})

const collapsedDirs = ref<string[]>([])
function toggleIndexGroup(dir: string) {
  collapsedDirs.value = collapsedDirs.value.includes(dir)
    ? collapsedDirs.value.filter(d => d !== dir)
    : [...collapsedDirs.value, dir]
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

// ESC 分层处理：弹窗 > 剧场模式 > 侧栏 > 收起面板。
// 弹窗由 ModalShell 自己响应，这里必须让路，避免一次按键同时关弹窗又收面板。
function onEsc(e: KeyboardEvent) {
  if (e.key !== 'Escape' || hasOpenModal()) return
  if (theater.value) {
    theater.value = false
    return
  }
  if (railOpen.value) {
    railOpen.value = false
    return
  }
  if (panelOpen.value) panelOpen.value = false
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
  railOpen.value = false
  panelOpen.value = true
  scrollToBottom()
}

// ---- 消息目录：左栏列表 + 跳转 + 滚动高亮同步 ----
const activeToc = ref(0)
let tocRaf = 0

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
          <button class="ai-icon-btn" :class="{ active: railOpen }" :title="railOpen ? '收起侧栏 (Esc)' : '展开侧栏'" @click="toggleRail">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>
            </svg>
          </button>
          <span class="ai-title">AI 助手</span>
          <span v-if="aiModel" class="ai-model">{{ aiModel }}</span>
        </div>
        <div class="ai-header-actions">
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

      <div class="ai-body">
        <!-- 左栏：会话 / 索引状态 / 消息目录并排展示，不再覆盖消息区 -->
        <aside v-if="railOpen" class="ai-rail">
          <div class="ai-rail-tabs">
            <button
              v-for="t in railTabs"
              :key="t.id"
              class="ai-rail-tab"
              :class="{ active: railTab === t.id }"
              @click="openRail(t.id)"
            >{{ t.label }}</button>
          </div>

          <div class="ai-rail-content">
            <!-- 历史会话 -->
            <template v-if="railTab === 'sessions'">
              <div class="ai-rail-tools">
                <span>{{ sessions.length }} 个会话</span>
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
                    <span v-if="streamingSessions.has(s.id)" class="ai-session-generating"><i></i>生成中</span>
                    <button class="ai-session-del" title="删除会话" @click="removeSession(s.id, $event)">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/></svg>
                    </button>
                  </div>
                </div>
              </div>
            </template>

            <!-- 文件索引状态（RAG 可视化：哪些文件已索引/失败） -->
            <template v-else-if="railTab === 'index'">
              <div class="ai-rail-tools">
                <span>文件索引状态</span>
                <button class="ai-icon-btn" title="刷新" @click="loadIndexStatus">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
                </button>
              </div>
              <div v-if="indexData" class="ai-index-summary">
                <div class="ai-index-stat"><b>{{ indexData.summary.files }}</b><span>文件</span></div>
                <div class="ai-index-stat"><b>{{ indexData.summary.chunks }}</b><span>文本块</span></div>
                <div class="ai-index-stat" :class="{ bad: indexData.summary.errors > 0 }"><b>{{ indexData.summary.errors }}</b><span>失败</span></div>
              </div>
              <div class="ai-sessions-list ai-index-list">
                <div v-if="indexLoading && !indexData" class="ai-sessions-empty">加载中…</div>
                <div v-else-if="!indexData?.items?.length" class="ai-sessions-empty">
                  暂无索引（上传文本文件或触发全量重建）
                </div>
                <template v-for="g in indexGroups" :key="g.dir">
                  <button class="ai-index-group" :class="{ collapsed: collapsedDirs.includes(g.dir) }" @click="toggleIndexGroup(g.dir)">
                    <span class="ai-index-chev">▾</span>
                    <span class="ai-index-dir">{{ g.dir }}</span>
                    <span class="ai-index-group-meta" :class="{ bad: g.errors > 0 }">{{ g.rows.length }} 个文件 · {{ g.chunks }} 块<span v-if="g.errors"> · {{ g.errors }} 失败</span></span>
                  </button>
                  <template v-if="!collapsedDirs.includes(g.dir)">
                    <div
                      v-for="it in g.rows"
                      :key="it.path"
                      class="ai-index-item"
                      :class="{ error: it.status !== 'ok' }"
                      :title="it.error ? it.path + '：' + it.error : it.path"
                    >
                      <span class="ai-index-dot-slot"><span v-if="it.status !== 'ok'" class="ai-index-dot"></span></span>
                      <span class="ai-index-name">{{ it.name }}</span>
                      <span v-if="it.status === 'ok'" class="ai-index-chunks"><b>{{ it.chunkCount }}</b> 块</span>
                      <span v-else class="ai-index-fail">失败</span>
                    </div>
                  </template>
                </template>
              </div>
            </template>

            <!-- 消息目录：摘要直接列出，滚动时高亮当前条目 -->
            <template v-else>
              <div class="ai-rail-tools"><span>消息目录</span></div>
              <div class="ai-toc-list">
                <button
                  v-for="(m, i) in visibleMessages"
                  :key="'toc' + i"
                  class="ai-toc-item"
                  :class="{ active: i === activeToc }"
                  @click="jumpTo(i)"
                >
                  <span class="ai-toc-role" :class="m.role">{{ m.role === 'user' ? '问' : '答' }}</span>
                  <span class="ai-toc-summary">{{ tocSummary(m) }}</span>
                </button>
                <div v-if="!visibleMessages.length" class="ai-sessions-empty">暂无消息</div>
              </div>
            </template>
          </div>

          <div v-if="!isMobile" class="ai-rail-foot">
            <button class="ai-rail-view" :class="{ active: theater }" @click="toggleTheater">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                <path d="M15 3h6v6"/><path d="M9 21H3v-6"/><path d="M21 3l-7 7"/><path d="M3 21l7-7"/>
              </svg>
              {{ theater ? '退出剧场模式' : '剧场模式' }}
            </button>
          </div>
        </aside>

        <!-- 右区：消息流 + 输入（grid 第二列），侧栏打开时始终可见 -->
        <div ref="listEl" class="ai-list" @scroll.passive="onListScroll">
          <button v-if="hiddenCount" class="ai-load-more" @click="showMore">
            加载更早的 {{ hiddenCount }} 条消息
          </button>

          <div v-if="messages.length === 0" class="ai-empty">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
            </svg>
            <div>有问题尽管问，Enter 发送</div>
          </div>

          <AiMessage v-for="(m, i) in visibleMessages" :key="i" :message="m" :index="i" />
        </div>

        <!-- 输入卡片：textarea 融入卡片；底部工具栏（引用入口 + 快捷键提示 / 发送） -->
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
              <button class="ai-chip" title="引用文件（也可直接输入 @）" @click="showFilePicker = true">
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2">
                  <path d="M3 1h6l5 5v9H3V1z"/><path d="M9 1v5h5"/>
                </svg>
                @ 引用文件
              </button>
              <span class="ai-input-hint">Enter 发送 · Shift+Enter 换行</span>
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

/* 剧场模式：脱离 fab 锚定，视口居中放大（不遮挡整个屏幕，背景仍可见）。
   不用 transform 居中：position:fixed + transform 会新建合成层，面板内流式
   动态插入内容时 Chrome 的命中区域不会跟着更新，表现为按钮点不动、resize
   重排后才可点。改用 inset:0 + margin:auto，居中同样成立且无合成层副作用。 */
.ai-panel.theater {
  position: fixed;
  inset: 0;
  margin: auto;
  width: min(1100px, calc(100vw - 80px));
  height: min(820px, calc(100vh - 60px));
  z-index: 1500;
}
.ai-panel.theater .ai-resizer { display: none; }

/* ---- 侧栏：会话 / 索引状态 / 消息目录，与消息区并排 ---- */
.ai-body {
  flex: 1;
  min-height: 0;
  position: relative;
  container-type: inline-size;
  display: grid;
  grid-template-columns: auto 1fr;
  grid-template-rows: minmax(0, 1fr) auto;
}
.ai-rail {
  grid-column: 1;
  grid-row: 1 / 3;
  /* 固定 px 宽度：auto 轨道据此定宽，不随内容撑开；窄面板下改覆盖式 */
  width: 216px;
  max-width: 216px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: var(--sidebar-bg);
  border-right: 1px solid var(--border);
}
.ai-rail-tabs {
  display: flex;
  flex-shrink: 0;
  gap: 2px;
  padding: 0 6px;
  border-bottom: 1px solid var(--border);
}
.ai-rail-tab {
  flex: 1;
  border: none;
  border-bottom: 2px solid transparent;
  background: none;
  padding: 8px 2px;
  font-size: 11px;
  color: var(--text-dim);
  cursor: pointer;
}
.ai-rail-tab:hover { color: var(--text); }
.ai-rail-tab.active { color: var(--accent); border-bottom-color: var(--accent); }
.ai-rail-content {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.ai-rail-tools {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-shrink: 0;
  gap: 6px;
  padding: 6px 8px 2px 10px;
  font-size: 10px;
  color: var(--text-muted);
}
.ai-rail-foot {
  flex-shrink: 0;
  padding: 6px 8px;
  border-top: 1px solid var(--border);
}
.ai-rail-view {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: none;
  border-radius: 6px;
  background: none;
  color: var(--text-dim);
  font-size: 11px;
  padding: 5px 6px;
  cursor: pointer;
}
.ai-rail-view:hover { background: var(--hover); color: var(--text); }
.ai-rail-view.active { color: var(--accent); }
.ai-rail-view svg { width: 13px; height: 13px; }

/* 目录列表：摘要常显，滚动时高亮当前消息 */
.ai-toc-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 6px 8px;
}
.ai-toc-item {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  width: 100%;
  border: none;
  border-radius: 6px;
  background: none;
  padding: 5px 6px;
  font-size: 11px;
  line-height: 1.45;
  color: var(--text-dim);
  text-align: left;
  cursor: pointer;
}
.ai-toc-item:hover { background: var(--hover); color: var(--text); }
.ai-toc-item.active { background: var(--active); color: var(--text); }
.ai-toc-role {
  flex: none;
  width: 16px;
  height: 16px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 9px;
  background: var(--code-bg);
  color: var(--text-dim);
}
.ai-toc-role.user { background: var(--accent); color: #fff; }
.ai-toc-summary {
  min-width: 0;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

/* 左缘拖拽调宽：默认隐形细条，hover/拖拽时淡显居中线 */
.ai-resizer {
  position: absolute;
  left: -3px;
  top: 0;
  bottom: 0;
  width: 8px;
  cursor: col-resize;
  z-index: 3;
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

/* 会话 / 索引状态列表（侧栏内容区，窄栏适配） */
.ai-index-summary {
  display: flex; flex: none; gap: 6px; padding: 6px 10px 8px;
  border-bottom: 1px solid var(--border);
}
.ai-index-stat {
  flex: 1; min-width: 0; display: flex; align-items: baseline; gap: 4px;
  padding: 5px 7px; border: 1px solid var(--border); border-radius: 6px;
  background: var(--code-bg);
}
.ai-index-stat b { font-size: 13px; font-weight: 600; color: var(--text); font-variant-numeric: tabular-nums; }
.ai-index-stat span { font-size: 9.5px; color: var(--text-dim); }
.ai-index-stat.bad b { color: #c04040; }

.ai-sessions-list.ai-index-list { gap: 0; }
.ai-index-group {
  position: sticky; top: 0; z-index: 1;
  flex: none; display: flex; align-items: center; gap: 6px;
  width: 100%; height: 20px; margin-top: 3px; padding: 0 6px 0 4px;
  border: none; border-bottom: 1px solid var(--border); border-radius: 0;
  background: var(--bg); color: var(--text-dim);
  font-family: var(--font-mono, monospace); font-size: 9.5px;
  text-align: left; cursor: pointer;
}
.ai-index-group:first-child { margin-top: 0; }
.ai-index-group:hover { color: var(--text); }
.ai-index-chev {
  flex: none; width: 9px; font-size: 9px; line-height: 1;
  color: var(--text-muted); transition: transform .12s;
}
.ai-index-group.collapsed .ai-index-chev { transform: rotate(-90deg); }
.ai-index-dir {
  flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.ai-index-group-meta {
  flex: none; font-family: inherit; font-size: 9.5px; color: var(--text-muted);
}
.ai-index-group-meta.bad { color: #c04040; }

.ai-index-item {
  flex: none; display: flex; align-items: center; gap: 6px;
  height: 22px; padding: 0 6px 0 14px; font-size: 11.5px; cursor: default;
}
.ai-index-item:hover { background: var(--code-bg); }
.ai-index-dot-slot { flex: none; width: 7px; }
.ai-index-dot { display: block; width: 7px; height: 7px; border-radius: 50%; background: #c04040; }
.ai-index-name {
  flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  color: var(--text);
}
.ai-index-item.error .ai-index-name { color: #c04040; }
.ai-index-chunks {
  flex: none; font-size: 10px; color: var(--text-muted); font-variant-numeric: tabular-nums;
}
.ai-index-chunks b { font-weight: 500; color: var(--text-dim); }
.ai-index-fail { flex: none; font-size: 9.5px; color: #c04040; }

.ai-sessions-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px 8px 8px;
  display: flex;
  flex-direction: column;
  gap: 5px;
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
.ai-session-generating {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--accent);
  border: 1px solid var(--accent);
  border-radius: 8px;
  padding: 0 5px;
}
.ai-session-generating i {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  animation: ai-generating-pulse 1s ease-in-out infinite;
}
@keyframes ai-generating-pulse {
  0%, 100% { opacity: .25; }
  50% { opacity: 1; }
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

/* 消息列表（grid 第二列第一行） */
.ai-list {
  grid-column: 2;
  grid-row: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 14px;
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

/* 输入区：卡片式（无边框 textarea 融入卡片，底部工具行；grid 第二列第二行） */
.ai-input {
  grid-column: 2;
  grid-row: 2;
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

/* 工具栏功能按钮：图标 + 文字的轻量 chip */
.ai-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: transparent;
  color: var(--text-dim);
  font-size: 11px;
  padding: 2px 8px;
  cursor: pointer;
  transition: color .15s, border-color .15s;
  white-space: nowrap;
}
.ai-chip svg { width: 12px; height: 12px; flex-shrink: 0; }
.ai-chip:hover { color: var(--accent); border-color: var(--accent); }

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

/* 面板被拖到过窄时：侧栏退化为覆盖式抽屉，不与消息区争宽度 */
@container (max-width: 460px) {
  .ai-rail {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    z-index: 4;
    width: min(260px, 78%);
    box-shadow: 6px 0 20px rgba(0, 0, 0, .16);
  }
}

/* 移动端：面板近乎占满宽度（锚定 dock 条目右缘，dock 距视口右缘 12px），禁拖拽；
   fab 按钮隐藏（底部导航已有 AI 入口），面板经 dock 条目仍可锚定显示 */
@media (max-width: 900px) {
  .ai-panel { width: calc(100vw - 24px) !important; }
  .ai-resizer { display: none; }
  .ai-fab { display: none; }
  .ai-dock-item { width: 0; height: 0; }
  .ai-rail {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    z-index: 4;
    width: min(260px, 78%);
    box-shadow: 6px 0 20px rgba(0, 0, 0, .16);
  }
}
</style>
