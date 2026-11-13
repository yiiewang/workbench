<script setup lang="ts">
// RAG 引用列表：AI 回答底部的编号引用来源（rag_hits SSE 事件数据）。
// 只列文件（编号 + 路径 + 行号，可点击跳转定位）；原文片段详情在正文
// [n] 编号的悬浮预览（RagRefTooltip）里看。
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { openRagFile } from '../../lib/ragNav'
import type { RagHit } from '../../api/ai'

defineProps<{ hits?: RagHit[] }>()

const expanded = ref(false)

const route = useRoute()
const router = useRouter()

function openHit(h: RagHit) {
  openRagFile(router, route, h.path, h.startLine)
}
</script>

<template>
  <div v-if="hits?.length" class="rag-cites">
    <button class="rag-cites-toggle" @click="expanded = !expanded">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
        <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
      </svg>
      引用来源（{{ hits.length }}）
    </button>
    <div v-show="expanded" class="rag-cites-body">
      <button v-for="(h, i) in hits" :key="i" class="rag-cite-path" @click="openHit(h)" :title="'打开 ' + h.path">
        <span class="rag-cite-no">[{{ i + 1 }}]</span>
        <span class="rag-cite-file">{{ h.path }}</span>
        <span v-if="h.startLine > 0" class="rag-cite-line">第 {{ h.startLine }} 行起</span>
        <svg class="rag-cite-jump" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M7 17L17 7" /><path d="M7 7h10v10" />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.rag-cites { margin-top: 8px; }

.rag-cites-toggle {
  display: inline-flex; align-items: center; gap: 5px;
  font-size: 11px; color: var(--text-dim); cursor: pointer;
  background: none; border: none; padding: 2px 0;
}
.rag-cites-toggle:hover { color: var(--accent); }
.rag-cites-toggle svg { width: 12px; height: 12px; }

.rag-cites-body {
  margin-top: 6px; padding: 8px 10px;
  background: var(--bg); border: 1px solid var(--border); border-radius: 6px;
  display: flex; flex-direction: column; gap: 6px;
}

.rag-cite-path {
  display: flex; align-items: center; gap: 6px; flex-wrap: wrap;
  font-size: 11px; cursor: pointer; text-align: left;
  background: none; border: none; padding: 0; color: var(--accent);
}
.rag-cite-path:hover { text-decoration: underline; }
.rag-cite-no { color: var(--text-dim); font-weight: 600; }
.rag-cite-file { font-family: var(--font-mono, monospace); word-break: break-all; }
.rag-cite-line { color: var(--text-dim); }
.rag-cite-jump { width: 10px; height: 10px; color: var(--text-dim); flex: none; }
</style>
