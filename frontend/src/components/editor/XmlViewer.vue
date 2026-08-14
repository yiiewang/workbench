<script setup lang="ts">
// XML 树形查看器：和 JsonViewer 类似，支持折叠/展开、显示行号、原始视图
// 实现策略：用 DOMParser 解析 XML 构造递归的标签/属性/文本节点结构
import { computed, ref } from 'vue'
import { copyToClipboard, showToast } from '../../lib/common'
import XmlNode from './XmlNode.vue'

const props = defineProps<{ text: string }>()

// 解析状态：解析后的单根节点（XML 文档只有一个根元素），或解析错误信息
const parseError = ref('')
let parsed: ParsedNode | null = null

interface ParsedNode {
  tag: string
  attrs: Record<string, string>
  children: (ParsedNode | string)[]   // 混合子节点：标签或文本
  text: string                         // 原始文本
}

try {
  const doc = new DOMParser().parseFromString(props.text, 'application/xml')
  const err = doc.querySelector('parsererror')
  if (err) {
    parseError.value = err.textContent || 'Invalid XML'
  } else {
    parsed = parseElement(doc.documentElement)
  }
} catch (e: any) {
  parseError.value = e.message || 'Invalid XML'
}

function parseElement(el: Element): ParsedNode {
  const attrs: Record<string, string> = {}
  for (let i = 0; i < el.attributes.length; i++) {
    const a = el.attributes[i]
    attrs[a.name] = a.value
  }
  const children: (ParsedNode | string)[] = []
  for (let i = 0; i < el.childNodes.length; i++) {
    const n = el.childNodes[i]
    if (n.nodeType === 1) children.push(parseElement(n as Element))
    else if (n.nodeType === 3) {
      const t = (n.textContent || '').trim()
      if (t) children.push(t)
    }
  }
  return { tag: el.tagName, attrs, children, text: el.textContent || '' }
}

const mode = ref<'viewer' | 'raw'>('viewer')
const expanded = ref(new Set<string>())

function toggle(key: string) { expanded.value.has(key) ? expanded.value.delete(key) : expanded.value.add(key) }
function isExpanded(key: string) { return expanded.value.has(key) }
function expandAll() {
  if (!parsed) return
  expanded.value.add('root')
  expandNodeKeys(parsed.children, 'root', true)
}
function collapseAll() { expanded.value = new Set() }
function expandNodeKeys(nodes: (ParsedNode | string)[], prefix: string, value: boolean) {
  nodes.forEach((node, i) => {
    const k = prefix + '-' + i
    if (typeof node === 'object') {
      if (value) expanded.value.add(k)
      if (node.children.length) expandNodeKeys(node.children, k, value)
    }
  })
}

function onCopyRaw() {
  if (!props.text) return
  copyToClipboard(props.text)
  showToast('Copied raw text')
}

function countAttrs(node: ParsedNode | string | null): number {
  if (!node || typeof node !== 'object') return 0
  return Object.keys(node.attrs).length + node.children.reduce((s, c) => s + countAttrs(c), 0)
}

const lineCount = computed(() => (props.text || '').split('\n').length)
const byteSize = computed(() => new Blob([props.text || '']).size)
const attrCount = computed(() => countAttrs(parsed))
</script>

<template>
  <div class="xml-viewer">
    <div v-if="parseError" class="xml-error">Invalid XML: {{ parseError }}</div>

    <div v-else class="xml-toolbar">
      <div class="xml-info">
        <span>Lines: {{ lineCount }}</span>
        <span>Size: {{ byteSize }} B</span>
        <span>Root: {{ parsed?.tag || '—' }}</span>
        <span>Attrs: {{ attrCount }}</span>
      </div>
      <div class="xml-actions">
        <button class="xml-btn" @click="expandAll" v-show="mode === 'viewer'">Expand all</button>
        <button class="xml-btn" @click="collapseAll" v-show="mode === 'viewer'">Collapse all</button>
        <button class="xml-btn" @click="mode = 'viewer'" :class="{ active: mode === 'viewer' }">Viewer</button>
        <button class="xml-btn" @click="mode = 'raw'" :class="{ active: mode === 'raw' }">Raw view</button>
        <button class="xml-btn" @click="onCopyRaw">Copy raw</button>
      </div>
    </div>

    <!-- Viewer 模式：递归渲染 XML 树（XML 文档只有单根） -->
    <div v-if="!parseError && mode === 'viewer' && parsed" class="xml-tree">
      <XmlNode
        :node="parsed"
        :index="'root'"
        :expanded="isExpanded"
        :toggle="toggle"
      />
    </div>

    <!-- Raw 模式：高亮原始 XML 文本（用 escapeHtml 包转义） -->
    <pre v-else-if="!parseError && mode === 'raw'" class="xml-raw"><code>{{ props.text }}</code></pre>
  </div>
</template>

<style scoped>
.xml-viewer { display: flex; flex-direction: column; height: 100%; font-family: var(--font-mono, monospace); font-size: 13px; }
.xml-toolbar { display: flex; align-items: center; justify-content: space-between; padding: 8px 16px; border-bottom: 1px solid var(--border, #e0e0e0); background: var(--bg-soft, #f5f5f5); flex-shrink: 0; }
.xml-info { display: flex; gap: 16px; color: var(--text-dim, #666); font-size: 12px; }
.xml-actions { display: flex; gap: 4px; }
.xml-btn { padding: 4px 10px; border: 1px solid var(--border, #e0e0e0); background: var(--bg, #fff); color: var(--text, #333); cursor: pointer; border-radius: 3px; font-size: 12px; }
.xml-btn:hover { background: var(--hover, #f0f0f0); }
.xml-btn.active { background: var(--accent, #409eff); color: #fff; border-color: var(--accent, #409eff); }
.xml-tree { flex: 1; overflow: auto; padding: 8px 16px; }
.xml-raw { flex: 1; overflow: auto; padding: 16px 24px; margin: 0; white-space: pre-wrap; word-break: break-all; font-family: var(--font-mono, monospace); font-size: 13px; }
.xml-error { padding: 20px; color: #d00; }
</style>
