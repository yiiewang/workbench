<script setup lang="ts">
// PC 端 AI 对话浮动面板：核心对话逻辑在 composables/useAIChat（与移动端 MAI 共用），
// 本组件只保留面板形态特有部分：fab/开关、宽度拖拽、外部事件监听。
import { ref, nextTick, onMounted, onUnmounted } from 'vue'
import { loggedIn } from '../../stores/auth'
import { showToast } from '../../lib/common'
import { useAIChat } from '../../composables/useAIChat'
import FilePickerModal from '../modal/FilePickerModal.vue'
import ToolCallList from './ToolCallList.vue'

const listEl = ref<HTMLElement | null>(null)

async function scrollToBottom() {
  await nextTick()
  if (listEl.value) listEl.value.scrollTop = listEl.value.scrollHeight
}

// ---- 对话核心（composable） ----
const {
  aiEnabled, aiModel, messages, input, streaming,
  pendingCtxFiles, showFilePicker,
  dbSessionId, sessions, sessionsOpen, sessionsLoading,
  openReasoning,
  maybeRestoreLastSession,
  send, stopStreaming, askWith,
  removeCtxFile, onPickFiles,
  toggleSessions, loadSessions, loadSession, removeSession, clearChat, timeAgo,
  copyMessage, regenerate, saveReplyAsFile,
  isStreamingTail, toggleReasoning, renderMd, userInitial,
  onInputKeydown: onKeydown,
} = useAIChat(scrollToBottom)

// ---- 面板特有：开关 / 自动恢复 ----
const panelOpen = ref(false)

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

onMounted(() => {
  window.addEventListener('workbench:ai-ask', onExternalAsk as EventListener)
  window.addEventListener('workbench:ai-open', onExternalOpen)
})
onUnmounted(() => {
  window.removeEventListener('workbench:ai-ask', onExternalAsk as EventListener)
  window.removeEventListener('workbench:ai-open', onExternalOpen)
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

    <!-- 对话面板：锚定在按钮上方 -->
    <div v-if="panelOpen" class="ai-panel" :style="{ width: panelWidth + 'px' }">
      <!-- 左缘拖拽调宽 -->
      <div class="ai-resizer" :class="{ active: resizing }" @mousedown="onResizeStart"></div>

      <div class="ai-header">
        <div class="ai-title-wrap">
          <span class="ai-title">AI 助手</span>
          <span v-if="aiModel" class="ai-model">{{ aiModel }}</span>
        </div>
        <div class="ai-header-actions">
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

      <div v-show="!sessionsOpen" ref="listEl" class="ai-list">
        <div v-if="messages.length === 0" class="ai-empty">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
          </svg>
          <div>有问题尽管问，Enter 发送</div>
        </div>

        <div v-for="(m, i) in messages" :key="i" class="ai-msg" :class="m.role">
          <!-- hover 工具条：复制 / 重新生成（末尾回复）/ 保存为文件 -->
          <div class="ai-msg-actions">
            <button
              v-if="m.role === 'assistant' && !streaming && i === messages.length - 1 && m.content"
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

          <!-- 气泡：assistant 走 markdown 渲染，user 纯文本 -->
          <div class="ai-bubble" :class="m.role">
            <template v-if="m.role === 'assistant'">
              <!-- 工具调用（Agent 模式）：每次调用一张可折叠卡片 -->
              <ToolCallList v-if="m.toolCalls?.length" :calls="m.toolCalls" />
              <!-- 思考过程（推理模型）：默认折叠，流式中标题显示"思考中…" -->
              <div v-if="m.reasoning" class="ai-reasoning">
                <button class="ai-reasoning-toggle" @click="toggleReasoning(i)">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M9.1 12a3 3 0 1 0 5.8 1c.6-1.8-1-2.4-1-4A3 3 0 0 0 12 6"/>
                    <circle cx="12" cy="3" r=".8" fill="currentColor"/><circle cx="5" cy="12" r=".8" fill="currentColor"/><circle cx="19" cy="12" r=".8" fill="currentColor"/>
                  </svg>
                  {{ openReasoning.has(i) ? '收起思考' : (isStreamingTail(i) && !m.content ? '思考中…' : '思考过程') }}
                </button>
                <div v-show="openReasoning.has(i)" class="ai-reasoning-body">{{ m.reasoning }}</div>
              </div>
              <div v-if="m.content" class="ai-md" v-html="renderMd(m.content)"></div>
              <span v-else-if="isStreamingTail(i) && !m.reasoning" class="ai-cursor">▍</span>
              <span v-else-if="isStreamingTail(i) && m.reasoning && !openReasoning.has(i)" class="ai-cursor ai-cursor-dim">▍</span>
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
