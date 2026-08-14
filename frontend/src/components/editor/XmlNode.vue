<script setup lang="ts">
// 递归 XML 节点：标签 + 属性 + 子节点，支持折叠
// 子节点结构：扁平化的子节点数组（ParsedNode 对象或 string 文本）
import { computed } from 'vue'

interface ParsedNode {
  tag: string
  attrs: Record<string, string>
  children: (ParsedNode | string)[]   // 子节点：标签节点或文本
  text: string                         // 该标签内所有文本合并（用于叶子快速展示）
}

const props = defineProps<{
  node: ParsedNode | string   // 接受 string 表示当前是文本节点
  index: string
  expanded: (k: string) => boolean
  toggle: (k: string) => void
}>()

// 如果是 string 文本节点
const isText = computed(() => typeof props.node === 'string')
const tagNode = computed(() => (isText.value ? null : (props.node as ParsedNode)))
const hasChildren = computed(() => !isText.value && tagNode.value!.children.length > 0)
const hasOnlyText = computed(() => hasChildren.value && tagNode.value!.children.every(c => typeof c === 'string'))
const isOpen = computed(() => props.expanded(props.index))
const attrEntries = computed(() => Object.entries(tagNode.value?.attrs || {}))
</script>

<template>
  <!-- 文本节点 -->
  <div v-if="isText" class="xml-line xml-text-line">
    <span class="xml-toggle-placeholder">·</span>
    <span class="xml-text">{{ node }}</span>
  </div>

  <!-- 标签节点 -->
  <div v-else class="xml-line">
    <!-- 折叠箭头 -->
    <span v-if="hasChildren" class="xml-toggle" @click="toggle(index)">
      {{ isOpen ? '▼' : '▶' }}
    </span>
    <span v-else class="xml-toggle-placeholder">·</span>

    <!-- 起始标签 -->
    <span class="xml-tag">&lt;{{ tagNode!.tag }}</span>

    <!-- 属性 -->
    <span v-for="[key, val] in attrEntries" :key="key" class="xml-attr">
      <span class="xml-attr-name"> {{ key }}</span><span class="xml-attr-eq">=</span><span class="xml-attr-value">"{{ val }}"</span>
    </span>

    <span class="xml-tag">&gt;</span>

    <!-- 子节点：折叠时显示 ... -->
    <template v-if="hasChildren && !isOpen">
      <span class="xml-collapsed">…</span>
      <span class="xml-tag">&lt;/{{ tagNode!.tag }}&gt;</span>
    </template>

    <!-- 无子节点：单行闭合 -->
    <template v-else-if="!hasChildren">
      <span class="xml-tag">&lt;/{{ tagNode!.tag }}&gt;</span>
    </template>

    <!-- 子节点全是文本：内联展示 -->
    <template v-else-if="hasOnlyText">
      <span class="xml-text">{{ tagNode!.text.trim() }}</span>
      <span class="xml-tag">&lt;/{{ tagNode!.tag }}&gt;</span>
    </template>

    <!-- 展开时递归渲染子节点 -->
    <div v-else-if="isOpen" class="xml-children">
      <XmlNode
        v-for="(child, i) in tagNode!.children"
        :key="index + '-' + i"
        :node="child"
        :index="index + '-' + i"
        :expanded="expanded"
        :toggle="toggle"
      />
      <div class="xml-close">
        <span class="xml-toggle-placeholder">·</span>
        <span class="xml-tag">&lt;/{{ tagNode!.tag }}&gt;</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.xml-line { line-height: 1.5; }
.xml-toggle { display: inline-block; width: 14px; cursor: pointer; color: var(--text-dim, #666); user-select: none; text-align: center; }
.xml-toggle-placeholder { display: inline-block; width: 14px; color: var(--text-dim, #999); text-align: center; }
.xml-tag { color: #2c5fa3; font-weight: 500; }
.xml-attr { display: inline; }
.xml-attr-name { color: #c0392b; }
.xml-attr-eq { color: var(--text-dim, #666); }
.xml-attr-value { color: #27ae60; }
.xml-text { color: var(--text, #333); margin: 0 4px; }
.xml-text-line { color: var(--text, #333); }
.xml-collapsed { color: var(--text-dim, #999); margin: 0 4px; }
.xml-children { margin-left: 18px; border-left: 1px dashed var(--border, #e0e0e0); padding-left: 6px; }
.xml-close { line-height: 1.5; }
</style>
