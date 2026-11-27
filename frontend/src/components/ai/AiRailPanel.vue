<script setup lang="ts">
// 左栏：会话 / 索引状态 / 消息目录并排展示，不覆盖消息区。
// 会话数据与操作来自父级 provide 的 chat 实例（同一份状态），
// 索引面板的数据加载与分组留在 AiChat（切 tab 时由父级触发刷新）。
import { inject } from 'vue'
import type { AIIndexStatus } from '../../api/ai'
import { aiChatKey } from '../../composables/useAIChat'
import { RAIL_TABS, type IndexGroup, type RailTab } from './rail'

defineProps<{
  tab: RailTab
  activeToc: number
  indexData: AIIndexStatus | null
  indexLoading: boolean
  indexGroups: IndexGroup[]
  collapsedDirs: string[]
}>()
const emit = defineEmits<{
  selectTab: [tab: RailTab]
  refreshIndex: []
  toggleGroup: [dir: string]
  jump: [index: number]
}>()

const chat = inject(aiChatKey)!
const {
  sessions, sessionsLoading, dbSessionId, streamingSessions, visibleMessages,
  loadSessions, loadSession, removeSession, timeAgo,
} = chat

function tocSummary(m: { role: string; content: string; toolCalls?: readonly unknown[] }): string {
  if (m.toolCalls?.length && !m.content) return `[工具调用 ×${m.toolCalls.length}]`
  const s = (m.content || '').replace(/\s+/g, ' ').trim()
  return s.slice(0, 60) + (s.length > 60 ? '…' : '') || '（空）'
}
</script>

<template>
  <aside class="ai-rail">
    <div class="ai-rail-tabs">
      <button
        v-for="t in RAIL_TABS"
        :key="t.id"
        class="ai-rail-tab"
        :class="{ active: tab === t.id }"
        @click="emit('selectTab', t.id)"
      >{{ t.label }}</button>
    </div>

    <div class="ai-rail-content">
      <!-- 历史会话 -->
      <template v-if="tab === 'sessions'">
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
      <template v-else-if="tab === 'index'">
        <div class="ai-rail-tools">
          <span>文件索引状态</span>
          <button class="ai-icon-btn" title="刷新" @click="emit('refreshIndex')">
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
            <button class="ai-index-group" :class="{ collapsed: collapsedDirs.includes(g.dir) }" @click="emit('toggleGroup', g.dir)">
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
            @click="emit('jump', i)"
          >
            <span class="ai-toc-role" :class="m.role">{{ m.role === 'user' ? '问' : '答' }}</span>
            <span class="ai-toc-summary">{{ tocSummary(m) }}</span>
          </button>
          <div v-if="!visibleMessages.length" class="ai-sessions-empty">暂无消息</div>
        </div>
      </template>
    </div>
  </aside>
</template>

<style scoped>
/* 容器 .ai-rail 的网格定位、容器查询与移动端抽屉样式留在 AiChat.vue（根元素共享作用域） */
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

/* 图标按钮：与面板头部同款（子组件作用域内需自带一份） */
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
</style>
