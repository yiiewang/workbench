<script setup lang="ts">
// 代码文件预览（VSCode 风格）：行号槽 + highlight.js 语法高亮 + 可折叠代码块
//
// 折叠规则（与 VSCode 默认一致）：
//   1. 缩进：某行的下一个非空行缩进更大 → 该行到本缩进块最后一行构成可折叠区（嵌套天然成立）
//   2. #region / #endregion：按栈配对，跨缩进层级显式分组
// 超大文件（>512KB 或 >5000 行）降级为整段 <pre>，避免生成上万行 DOM 卡死页面。
import { computed, ref, watch } from 'vue'
import { highlightLines, langForExt, type CodeToken } from '../../lib/highlighter'
import { copyToClipboard, showToast, formatSize } from '../../lib/common'

const props = defineProps<{ text: string; ext: string }>()

const MAX_BYTES = 512 * 1024
const MAX_LINES = 5000
const TAB_WIDTH = 4

const lang = computed(() => langForExt(props.ext))
const rawText = computed(() => String(props.text || '').replace(/\r\n?/g, '\n'))
const rawLines = computed(() => rawText.value.split('\n'))
const tooLarge = computed(() => rawText.value.length > MAX_BYTES || rawLines.value.length > MAX_LINES)
const tokenLines = computed(() => (tooLarge.value ? [] : highlightLines(rawText.value, lang.value)))

function indentOf(line: string) {
  let w = 0
  for (const ch of line) {
    if (ch === ' ') w += 1
    else if (ch === '\t') w += TAB_WIDTH
    else break
  }
  return w
}

function isBlank(line: string) { return line.trim() === '' }

// 起始行号 → 折叠区最后一行行号
const foldRanges = computed(() => {
  const lines = rawLines.value
  const map = new Map<number, number>()

  for (let i = 0; i < lines.length; i++) {
    if (isBlank(lines[i])) continue
    const ind = indentOf(lines[i])
    let j = i + 1
    while (j < lines.length && isBlank(lines[j])) j++
    if (j >= lines.length) continue
    if (indentOf(lines[j]) <= ind) continue
    let end = j
    for (let k = j + 1; k < lines.length; k++) {
      if (isBlank(lines[k])) continue
      if (indentOf(lines[k]) <= ind) break
      end = k
    }
    if (end > i) map.set(i, end)
  }

  const stack: number[] = []
  const regionStart = /^\s*(?:\/\/|#|--|;)?\s*#region\b/
  lines.forEach((line, i) => {
    if (regionStart.test(line)) stack.push(i)
    else if (/#endregion\b/.test(line) && stack.length) map.set(stack.pop() as number, i)
  })

  return map
})

const collapsed = ref<Set<number>>(new Set())
watch(() => props.text, () => collapsed.value.clear())

interface Row {
  kind: 'line' | 'fold'
  n: number
  tokens: CodeToken[]
  foldable: boolean
  isCollapsed: boolean
  count: number
}

const rows = computed<Row[]>(() => {
  if (tooLarge.value) return []
  const lines = tokenLines.value
  const folds = foldRanges.value
  const out: Row[] = []
  for (let i = 0; i < lines.length; i++) {
    const foldable = folds.has(i)
    const isCollapsed = foldable && collapsed.value.has(i)
    out.push({ kind: 'line', n: i, tokens: lines[i], foldable, isCollapsed, count: 0 })
    if (isCollapsed) {
      const end = folds.get(i) as number
      out.push({ kind: 'fold', n: i, tokens: [], foldable: false, isCollapsed: true, count: end - i })
      i = end
    }
  }
  return out
})

function toggle(n: number) {
  if (collapsed.value.has(n)) collapsed.value.delete(n)
  else collapsed.value.add(n)
}

function collapseAll() {
  const next = new Set<number>()
  foldRanges.value.forEach((_, start) => next.add(start))
  collapsed.value = next
}

function expandAll() { collapsed.value.clear() }

function onCopy() {
  if (rawText.value) { copyToClipboard(rawText.value); showToast('Code copied to clipboard') }
}

const wrap = ref(false)
</script>

<template>
  <div class="code-container">
    <div class="code-toolbar">
      <span class="code-lang">{{ lang || 'Plain Text' }}</span>
      <span class="code-meta">{{ rawLines.length }} lines · {{ formatSize(rawText.length) }}</span>
      <span class="code-spacer"></span>
      <button class="code-btn" @click="onCopy">Copy</button>
      <button class="code-btn" @click="collapseAll">Collapse all</button>
      <button class="code-btn" @click="expandAll">Expand all</button>
      <button class="code-btn" :class="{ active: wrap }" @click="wrap = !wrap">Wrap</button>
    </div>

    <pre v-if="tooLarge" class="code-plain">{{ rawText }}</pre>

    <div v-else class="code-body" :class="{ 'is-wrap': wrap }">
      <div v-for="row in rows" :key="row.kind + '-' + row.n" class="code-row">
        <template v-if="row.kind === 'line'">
          <span class="code-gutter">
            <span class="fold-toggle" :class="{ 'is-hidden': !row.foldable }"
              @click="row.foldable && toggle(row.n)">{{ row.isCollapsed ? '▸' : '▾' }}</span>
            <span class="code-ln">{{ row.n + 1 }}</span>
          </span>
          <code class="code-line"><span v-for="(t, i) in row.tokens" :key="i" :class="t.cls">{{ t.text }}</span></code>
        </template>
        <template v-else>
          <span class="code-gutter"><span class="fold-toggle" @click="toggle(row.n)">▸</span></span>
          <span class="code-fold-hint" @click="toggle(row.n)">⋯ {{ row.count }} line{{ row.count > 1 ? 's' : '' }} hidden</span>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.code-container { display: flex; flex-direction: column; height: 100%; overflow: hidden; }

.code-toolbar {
  display: flex; align-items: center; gap: 8px; flex-shrink: 0;
  padding: 4px 12px; border-bottom: 1px solid var(--border, #e5e5e5);
  background: var(--header-bg, #f3f3f3); font-size: 12px; color: var(--text-dim, #888);
}
.code-spacer { flex: 1; }
.code-lang { color: var(--text, #333); font-weight: 600; }
.code-btn {
  padding: 2px 8px; cursor: pointer; font-size: 12px;
  border: 1px solid var(--tab-border, #ddd); border-radius: 4px;
  background: var(--bg, #fff); color: var(--text, #333);
}
.code-btn:hover { background: var(--hover, #e8e8e8); }
.code-btn.active { background: var(--active, #e4e6f1); border-color: var(--accent, #007acc); }

.code-body {
  flex: 1; overflow: auto; padding: 4px 0;
  background: var(--preview-bg, #fff);
  font-family: var(--font-mono, monospace); font-size: 13px; line-height: 1.5;
}
.code-row { display: flex; white-space: pre; }
.code-body.is-wrap .code-row { white-space: pre-wrap; word-break: break-all; }

/* 行号槽：横向滚动时固定在左侧 */
.code-gutter {
  position: sticky; left: 0; z-index: 1;
  display: flex; justify-content: flex-end; align-items: flex-start; gap: 4px;
  flex-shrink: 0; width: 64px; padding-right: 10px;
  background: var(--preview-bg, #fff);
  color: var(--text-muted, #bbb); user-select: none; text-align: right;
}
.fold-toggle { width: 12px; cursor: pointer; color: var(--text, #333); opacity: 0.75; }
.fold-toggle:hover { opacity: 1; color: var(--accent, #007acc); }
.fold-toggle.is-hidden { visibility: hidden; }
.code-ln { min-width: 24px; }

.code-line { flex: 1; min-height: 1.5em; padding-right: 12px; color: var(--text, #333); }
.code-fold-hint {
  cursor: pointer; color: var(--text-dim, #888); font-size: 12px;
  background: var(--code-bg, #f5f5f5); border: 1px solid var(--border, #e5e5e5);
  border-radius: 3px; padding: 0 8px;
}
.code-fold-hint:hover { color: var(--accent, #007acc); }

.code-plain {
  margin: 0; padding: 16px 24px; flex: 1; overflow: auto;
  white-space: pre-wrap; word-break: break-all;
  font-size: 13px; font-family: var(--font-mono, monospace);
}
</style>
