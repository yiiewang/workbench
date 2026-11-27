<script setup lang="ts">
// PC 端 AI 对话浮动面板：核心对话逻辑在 composables/useAIChat（与移动端 MAI 共用），
// 本组件只保留面板形态特有部分：fab/开关、宽度拖拽、外部事件监听。
import { ref, computed, watch, onMounted, onUnmounted, provide } from 'vue'
import { loggedIn } from '../../stores/auth'
import { showToast, errMsg } from '../../lib/common'
import { hasOpenModal } from '../../lib/modalEsc'
import { getAIIndexStatus, type AIIndexStatus } from '../../api/ai'
import { useAIChat, aiChatKey } from '../../composables/useAIChat'
import { useStickyScroll } from '../../composables/useStickyScroll'
import FilePickerModal from '../modal/FilePickerModal.vue'
import AiMessage from './AiMessage.vue'
import AiRailPanel from './AiRailPanel.vue'
import AiSettingsPopover from './AiSettingsPopover.vue'
import AiComposer from './AiComposer.vue'
import RagRefTooltip from './RagRefTooltip.vue'
import { type IndexGroup, type RailTab } from './rail'

const listEl = ref<HTMLElement | null>(null)
const { onStickScroll, scrollToBottom } = useStickyScroll(listEl)

// ---- 对话核心（composable）：整份实例 provide 给消息组件 ----
const chat = useAIChat(scrollToBottom)
// 模型设置：上下文长度只影响前端历史裁剪；输出上限/推理强度透传给上游
const settingsOpen = ref(false)
const modelSettings = chat.modelSettings
provide(aiChatKey, chat)
const {
  aiEnabled, currentModel, gateways, messages, input, streaming,
  pendingCtxFiles, showFilePicker,
  dbSessionId,
  visibleMessages, hiddenCount, showMore,
  maybeRestoreLastSession,
  send, stopStreaming, askWith,
  removeCtxFile, onPickFiles,
  loadSessions, clearChat,
  onInputKeydown: onKeydown,
} = chat

// ---- 侧栏：会话 / 索引状态 / 消息目录三合一 ----
// 三个入口不再各自覆盖消息区，统一在左栏切 tab；同一 tab 再点即收起。
const railOpen = ref(false)
const railTab = ref<RailTab>('sessions')

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
    showToast('读取索引状态失败：' + (errMsg(e, '')))
  } finally {
    indexLoading.value = false
  }
}

// 明细按目录分组（后端已按 path 排序），目录只显示一次、组内只留文件名
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
          <span v-if="currentModel" class="ai-model">{{ currentModel }}</span>
        </div>
        <div class="ai-header-actions">
          <button v-if="!isMobile" class="ai-icon-btn" :class="{ active: theater }"
            :title="theater ? '退出剧场模式' : '剧场模式'" @click="toggleTheater">
            <svg v-if="!theater" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M8 3H5a2 2 0 0 0-2 2v3M16 3h3a2 2 0 0 1 2 2v3M16 21h3a2 2 0 0 0 2-2v-3M8 21H5a2 2 0 0 1-2-2v-3"/>
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <path d="M8 3v3a2 2 0 0 1-2 2H3M16 3v3a2 2 0 0 0 2 2h3M16 21v-3a2 2 0 0 1 2-2h3M8 21v-3a2 2 0 0 0-2-2H3"/>
            </svg>
          </button>
          <button class="ai-icon-btn" :class="{ active: settingsOpen }" data-settings-toggle title="模型设置" @click="settingsOpen = !settingsOpen">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="3"/>
              <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>
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

        <!-- 模型设置弹层：上下文长度只裁剪前端历史；输出上限/推理强度透传上游 -->
        <AiSettingsPopover
          v-if="settingsOpen"
          :gateways="gateways"
          :settings="modelSettings"
          @change="chat.updateModelSettings"
        />
      </div>

      <div class="ai-body">
        <!-- 左栏：会话 / 索引状态 / 消息目录并排展示，不再覆盖消息区 -->
        <!-- 左栏：会话 / 索引状态 / 消息目录并排展示，不再覆盖消息区 -->
        <AiRailPanel
          v-if="railOpen"
          :tab="railTab"
          :active-toc="activeToc"
          :index-data="indexData"
          :index-loading="indexLoading"
          :index-groups="indexGroups"
          :collapsed-dirs="collapsedDirs"
          @select-tab="openRail"
          @refresh-index="loadIndexStatus"
          @toggle-group="toggleIndexGroup"
          @jump="jumpTo"
        />

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
        <AiComposer
          v-model="input"
          :pending-files="pendingCtxFiles"
          :streaming="streaming"
          @keydown="onKeydown"
          @remove-file="removeCtxFile"
          @pick-file="showFilePicker = true"
          @send="send"
          @stop="stopStreaming"
        />
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
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--sidebar-bg);
  flex-shrink: 0;
}
/* 模型设置弹层 */
.ai-settings {
  position: absolute;
  top: 46px;
  right: 10px;
  z-index: 30;
  width: 260px;
  padding: 10px;
  border-radius: 10px;
  background: var(--bg);
  border: 1px solid var(--border);
  box-shadow: 0 12px 32px rgba(0, 0, 0, .18);
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
