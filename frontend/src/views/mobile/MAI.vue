<script setup lang="ts">
// 移动端 AI 对话页：全屏形态，核心逻辑复用 composables/useAIChat（与 PC 面板共用），
// 消息区样式复用全局 styles/ai-chat.css（类名与 PC 一致）。
import { ref, reactive, nextTick, onMounted } from 'vue'
import { useAIChat } from '../../composables/useAIChat'
import FilePickerModal from '../../components/modal/FilePickerModal.vue'
import ToolCallList from '../../components/ai/ToolCallList.vue'
import RagCitations from '../../components/ai/RagCitations.vue'
import RagRefTooltip from '../../components/ai/RagRefTooltip.vue'

const listEl = ref<HTMLElement | null>(null)

// scrollToBottom(force?) force=true 无条件拉底（发消息/载入会话/流结束）。
// 流式增量遵循 stick-to-bottom：用户上滚（scroll 事件距底 >40px）即暂停跟随，
// 滚回底部自动恢复。不能用纯距离阈值判定——高频增量拉底下用户单次滚轮
// 滚不出逃逸距离，表现为"完全滚不动"。
const stickBottom = ref(true)
let lastProgScroll = 0

function onStickScroll() {
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

const {
  aiEnabled, aiModel, messages, input, streaming,
  pendingCtxFiles, showFilePicker,
  dbSessionId, sessions, sessionsOpen, sessionsLoading,
  visibleMessages, hiddenCount, showMore,
  maybeRestoreLastSession,
  send, stopStreaming,
  removeCtxFile, onPickFiles,
  toggleSessions, loadSessions, loadSession, removeSession, clearChat, timeAgo,
  copyMessage, regenerate, saveReplyAsFile, approveToolCall,
  isStreamingTail, messageSegments, renderTextMd, usageText, hasCitedRefs, hasReplyContent, userInitial,
  onInputKeydown,
} = useAIChat(scrollToBottom)

// ---- 过程块折叠状态（key = 消息idx:段idx），与 PC 面板同逻辑 ----
const processManual = reactive(new Map<string, boolean>())

function processOpen(i: number, si: number): boolean {
  const manual = processManual.get(`${i}:${si}`)
  if (manual !== undefined) return manual
  return isStreamingTail(i)
}

function toggleProcess(i: number, si: number) {
  processManual.set(`${i}:${si}`, !processOpen(i, si))
}

function processRunning(i: number, si: number): boolean {
  const segs = messageSegments(visibleMessages.value[i])
  return isStreamingTail(i) && si === segs.length - 1 && segs[si]?.kind === 'process'
}

function processTitle(seg: { kind: string; steps: { kind: string }[] }, i: number, si: number): string {
  if (processRunning(i, si)) return '思考与工具调用中…'
  const tools = seg.steps.filter(s => s.kind === 'tool').length
  return tools > 0 ? `思考与工具调用（${tools} 步）` : '思考过程'
}

// 进入页面即对话场景：自动接上最近会话
onMounted(() => {
  maybeRestoreLastSession()
})
</script>

<template>
<div class="m-ai">
  <!-- 未配置提示 -->
  <div v-if="!aiEnabled" class="mai-empty">
    <div class="mai-empty-icon">✦</div>
    <div class="mai-empty-title">AI 未配置</div>
    <div class="mai-empty-desc">请在服务端 config.yaml 设置 ai.api_key 后使用</div>
  </div>

  <template v-else>
    <!-- 顶部 -->
    <header class="mai-header">
      <span class="mai-title">AI 助手</span>
      <span v-if="aiModel" class="mai-model">{{ aiModel }}</span>
      <div class="mai-actions">
        <button class="mai-icon-btn" :class="{ active: sessionsOpen }" title="历史会话" @click="toggleSessions">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>
        </button>
        <button class="mai-icon-btn" title="新对话" @click="clearChat">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M5 12h14"/></svg>
        </button>
      </div>
    </header>

    <!-- 历史会话覆盖层 -->
    <div v-if="sessionsOpen" class="mai-sessions">
      <div class="mai-sessions-head">
        <span>历史会话</span>
        <button class="mai-icon-btn" title="刷新" @click="loadSessions">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
        </button>
      </div>
      <div class="mai-sessions-list">
        <div v-if="sessionsLoading && !sessions.length" class="mai-sessions-empty">加载中…</div>
        <div v-else-if="!sessions.length" class="mai-sessions-empty">暂无历史会话</div>
        <div
          v-for="s in sessions"
          :key="s.id"
          class="mai-session-item"
          :class="{ current: s.id === dbSessionId }"
          :title="s.title"
          @click="loadSession(s.id)"
        >
          <div class="mai-session-title">{{ s.title || '新对话' }}</div>
          <div class="mai-session-meta">
            <span>{{ timeAgo(s.updatedAt) }}</span>
            <span v-if="s.id === dbSessionId" class="mai-session-tag">当前</span>
            <button class="mai-session-del" title="删除会话" @click="removeSession(s.id, $event)">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/></svg>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 消息列表（消息区结构与样式复用全局 ai-chat.css 类） -->
    <div v-show="!sessionsOpen" ref="listEl" class="mai-list" @scroll.passive="onStickScroll">
      <div v-if="messages.length === 0" class="mai-hello">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
        </svg>
        <div>有问题尽管问，输入 @ 可引用文件</div>
      </div>

      <button v-if="hiddenCount" class="ai-load-more" @click="showMore">
        加载更早的 {{ hiddenCount }} 条消息
      </button>
      <div v-for="(m, i) in visibleMessages" :key="i" class="ai-msg" :class="m.role">
        <div class="ai-msg-actions">
          <button
            v-if="m.role === 'assistant' && !streaming && i === visibleMessages.length - 1 && hasReplyContent(m)"
            class="ai-act" title="重新生成" @click="regenerate"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>
          </button>
          <button
            v-if="m.role === 'assistant' && !streaming && m.content"
            class="ai-act" title="保存为文件" @click="saveReplyAsFile(m, i)"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><path d="M17 21v-8H7v8M7 3v5h8"/></svg>
          </button>
          <button v-if="m.content" class="ai-act" title="复制" @click="copyMessage(m)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          </button>
        </div>

        <div class="ai-avatar" :class="m.role">
          <template v-if="m.role === 'user'">{{ userInitial }}</template>
          <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/>
          </svg>
        </div>

        <div class="ai-bubble" :class="m.role">
          <template v-if="m.role === 'assistant'">
            <template v-for="(seg, si) in messageSegments(m)" :key="si">
              <!-- 过程块（思考 + 工具调用，时间序）：默认折叠，流式尾消息展开 -->
              <div v-if="seg.kind === 'process'" class="ai-process">
                <button class="ai-process-toggle" @click="toggleProcess(i, si)">
                  <span :class="{ dim: processRunning(i, si) }">{{ processTitle(seg, i, si) }}</span>
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
                {{ f.split('/').pop() || f }}
              </span>
            </div>
          </template>
        </div>
      </div>
    </div>

    <!-- 输入区 -->
    <div v-show="!sessionsOpen" class="mai-input-wrap">
      <div class="mai-input">
        <div v-if="pendingCtxFiles.length" class="mai-ctx-list">
          <span v-for="p in pendingCtxFiles" :key="p" class="mai-ctx-chip" :title="p">
            <span class="mai-ctx-name">{{ p.split('/').pop() || p }}</span>
            <button class="mai-ctx-remove" @click="removeCtxFile(p)">&times;</button>
          </span>
        </div>
        <div class="mai-input-row">
          <textarea
            v-model="input"
            rows="1"
            placeholder="发送消息，输入 @ 引用文件"
            @keydown="onInputKeydown"
          ></textarea>
          <button v-if="streaming" class="mai-send stop" title="停止" @click="stopStreaming">
            <svg viewBox="0 0 24 24" fill="currentColor"><rect x="7" y="7" width="10" height="10" rx="2"/></svg>
          </button>
          <button v-else class="mai-send" :disabled="!input.trim()" title="发送" @click="send">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 19V5M5 12l7-7 7 7"/>
            </svg>
          </button>
        </div>
      </div>
    </div>

    <!-- @ 文件引用选择器 -->
    <FilePickerModal
      v-if="showFilePicker"
      title="引用文件"
      @confirm="onPickFiles"
      @close="showFilePicker = false"
    />

    <!-- RAG 引用编号悬浮预览（移动端触屏无 hover，仍支持触控笔/鼠标场景） -->
    <RagRefTooltip />
  </template>
</div>
</template>
<style scoped>
.m-ai {
  height: 100%;
  display: flex;
  flex-direction: column;
  max-width: 768px;
  margin: 0 auto;
  width: 100%;
  box-sizing: border-box;
}

/* 未配置 */
.mai-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--text-muted);
}
.mai-empty-icon { font-size: 36px; opacity: .5; }
.mai-empty-title { font-size: 16px; font-weight: 600; color: var(--text-dim); }
.mai-empty-desc { font-size: 12px; }

/* 顶部 */
.mai-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.mai-title { font-size: 16px; font-weight: 600; color: var(--text); }
.mai-model {
  font-size: 10px;
  color: var(--text-dim);
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 9px;
  padding: 1px 8px;
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mai-actions { margin-left: auto; display: flex; gap: 2px; }
.mai-icon-btn {
  border: none;
  background: none;
  color: var(--text-dim);
  cursor: pointer;
  padding: 6px;
  border-radius: 6px;
  display: flex;
}
.mai-icon-btn:hover, .mai-icon-btn.active { background: var(--hover); color: var(--accent); }
.mai-icon-btn svg { width: 18px; height: 18px; }

/* 会话历史覆盖层 */
.mai-sessions { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.mai-sessions-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px 4px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-dim);
}
.mai-sessions-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.mai-sessions-empty { text-align: center; color: var(--text-muted); font-size: 13px; margin-top: 24px; }
.mai-session-item {
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--bg);
  cursor: pointer;
}
.mai-session-item.current { border-color: var(--accent); background: var(--active, #e4e6f1); }
.mai-session-title {
  font-size: 14px;
  color: var(--text);
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mai-session-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 4px;
  font-size: 11px;
  color: var(--text-muted);
}
.mai-session-tag { color: var(--accent); border: 1px solid var(--accent); border-radius: 8px; padding: 0 5px; }
.mai-session-del {
  margin-left: auto;
  border: none;
  background: none;
  color: var(--text-dim);
  cursor: pointer;
  padding: 4px;
  border-radius: 4px;
  display: flex;
}
.mai-session-del:hover { background: #fde8ea; color: #cf222e; }
.mai-session-del svg { width: 13px; height: 13px; }

/* 消息列表容器（消息本身用全局 ai-chat.css 类） */
.mai-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.mai-hello {
  color: var(--text-muted);
  font-size: 13px;
  text-align: center;
  margin-top: 32px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.mai-hello svg { width: 28px; height: 28px; opacity: .5; }

/* 输入区 */
.mai-input-wrap {
  flex-shrink: 0;
  padding: 8px 12px 10px;
  border-top: 1px solid var(--border);
  background: var(--bg);
}
.mai-input {
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 8px 10px 6px;
  background: var(--bg);
  transition: border-color .15s;
}
.mai-input:focus-within { border-color: var(--accent); }
.mai-ctx-list { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 6px; }
.mai-ctx-chip {
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
.mai-ctx-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 140px;
}
.mai-ctx-remove {
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
.mai-ctx-remove:hover { background: #fde8ea; color: #cf222e; }
.mai-input-row { display: flex; align-items: center; gap: 8px; }
.mai-input-row textarea {
  flex: 1;
  resize: none;
  border: none;
  /* 单行时文字与 34px 圆形按钮中线对齐（按钮高/2 - 行高/2 ≈ 6px 顶部补偿） */
  padding: 6px 0 0;
  min-height: 34px;
  font-size: 14px;
  font-family: inherit;
  line-height: 1.55;
  background: transparent;
  color: var(--text);
  outline: none;
  max-height: 120px;
}
.mai-input-row textarea::placeholder { color: var(--text-muted); }
.mai-send {
  border: none;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  cursor: pointer;
  width: 34px;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.mai-send svg { width: 16px; height: 16px; }
.mai-send:disabled { opacity: .35; cursor: default; }
.mai-send.stop { background: var(--text-dim); }
</style>
