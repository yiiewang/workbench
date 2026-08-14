<script setup lang="ts">
// Recursive tree node component — replaces renderTree / expandNode DOM manipulation
import { computed } from 'vue'
import { activeTabPath, dsMode, updateHash, openTabFn } from '../../stores/indexStore'
import { treeStore } from '../../composables/useTreeStore'
import type { TreeItem } from '../../composables/useTreeStore'

const props = defineProps<{ path: string; depth: number }>()

const expIcon = `<svg viewBox="0 0 16 16"><path d="M5.7 13.7L4.3 12.3 8.6 8 4.3 3.7 5.7 2.3l6 6-6 6z"/></svg>`
const spHtml = '<span class="spinner"></span>'

const items = computed<TreeItem[]>(() => treeStore.allChildren(props.path))

function isExpanded(itemPath: string): boolean {
  return treeStore.expanded.has(itemPath)
}

const ICONS: Record<string, string> = {
  folder: `<svg viewBox="0 0 16 16"><path d="M1.5 3h4l1 1h7v9h-12V3z" fill="currentColor"/></svg>`,
  folderOpen: `<svg viewBox="0 0 16 16"><path d="M1.5 3h4l1 1h7v2h-12V3z"/><path d="M1.5 6h12l-1 7h-10l-1-7z"/></svg>`,
  html: `<svg viewBox="0 0 16 16"><path d="M2 1h12l-1 13-5 2-5-2L2 1z" fill="currentColor"/><text x="4.5" y="11" font-size="6" fill="#fff" font-weight="bold">&lt;/&gt;</text></svg>`,
  css:  `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="4" y="12" font-size="7" fill="#fff" font-weight="bold">#</text></svg>`,
  js:   `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11.5" font-size="5" fill="#fff" font-weight="bold">JS</text></svg>`,
  ts:   `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11.5" font-size="5" fill="#fff" font-weight="bold">TS</text></svg>`,
  json: `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11.5" font-size="5" fill="#fff" font-weight="bold">{}</text></svg>`,
  md:   `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11" font-size="6" fill="#fff" font-weight="bold">M</text></svg>`,
  py:   `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11.5" font-size="5" fill="#fff" font-weight="bold">PY</text></svg>`,
  go:   `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11.5" font-size="5" fill="#fff" font-weight="bold">Go</text></svg>`,
  yaml: `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3" y="11" font-size="5" fill="#fff" font-weight="bold">YML</text></svg>`,
  image:`<svg viewBox="0 0 16 16"><rect x="2" y="2" width="12" height="12" rx="1" fill="currentColor"/><circle cx="6" cy="6" r="1.5" fill="#fff"/><path d="M2 11l3-3 2 2 3-4 4 5H2z" fill="#fff"/></svg>`,
  pdf:  `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11.5" font-size="5" fill="#fff" font-weight="bold">PDF</text></svg>`,
  zip:  `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><path d="M7 4h2v2H7zM7 7h2v2H7z" fill="#fff"/></svg>`,
  video:`<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><polygon points="7,5 11,8 7,11" fill="#fff"/></svg>`,
  audio:`<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><circle cx="8" cy="8" r="3" fill="#fff"/></svg>`,
  sh:   `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><text x="3.5" y="11" font-size="6" fill="#fff" font-weight="bold">$</text></svg>`,
  db:   `<svg viewBox="0 0 16 16"><ellipse cx="8" cy="3" rx="5" ry="2" fill="currentColor"/><path d="M3 3v4c0 1.1 2.2 2 5 2s5-.9 5-2V3" fill="none" stroke="currentColor" stroke-width="1"/><path d="M3 7v4c0 1.1 2.2 2 5 2s5-.9 5-2V7" fill="none" stroke="currentColor" stroke-width="1"/></svg>`,
  _file: `<svg viewBox="0 0 16 16"><path d="M3 1h6l5 5v9H3V1z" fill="none" stroke="currentColor" stroke-width="1"/><path d="M9 1v5h5" fill="none" stroke="currentColor" stroke-width="1"/></svg>`,
  _lock: `<svg viewBox="0 0 16 16"><rect x="2" y="1" width="12" height="14" rx="1" fill="currentColor"/><path d="M6 6V4a2 2 0 114 0v2" fill="none" stroke="#fff" stroke-width="1.2"/><circle cx="8" cy="9" r="1.5" fill="#fff"/></svg>`,
}

const EXT_SVG_KEYS = new Set(['html','htm','css','js','ts','json','md','markdown','py','go','yml','yaml','sh','bash','zsh','png','jpg','jpeg','gif','svg','webp','ico','pdf','zip','tar','gz','tgz','rar','7z','bz2','xz','mp4','mov','avi','mkv','mp3','wav','flac','db','sqlite'])

function getExt(name: string) { return ((name || '').split('.').pop() || '').toLowerCase() }

function fileIcon(ext: string, name: string): string {
  if (EXT_SVG_KEYS.has(ext)) return (ICONS[ext] || ICONS._file)
  if (name.startsWith('.')) return ICONS._lock
  return ICONS._file
}

function iconHtml(item: TreeItem): string {
  if (item.isDir) return isExpanded(childPath(item.name)) ? ICONS.folderOpen : ICONS.folder
  return fileIcon(getExt(item.name), item.name)
}

function childPath(name: string): string {
  return treeStore.childPath(props.path, name)
}

async function onDirClick(path: string) {
  if (treeStore.expanded.has(path)) {
    treeStore.toggle(path)
    return
  }
  try {
    await treeStore.loadDir(path)
    treeStore.expanded.add(path)
  } catch {
    return // load failed, don't update activePath
  }
  treeStore.activePath.value = path
  updateHash(path)
}

function onFileClick(path: string, item: TreeItem) {
  treeStore.activePath.value = path
  updateHash(path)
  if (openTabFn) openTabFn(path, item.name, getExt(item.name), item.size || 0)
}

function onContextmenu(path: string, item: TreeItem) {
  if (dsMode.value === 'share') return
  treeStore.activePath.value = path
  activeTabPath.value = path
  const type = item.isDir ? 'dir' : 'file'
  if (typeof (window as any).openShareModal === 'function') {
    (window as any).openShareModal(type)
  }
}
</script>

<template>
  <template v-for="item in items" :key="item.name">
    <div class="tree-node">
    <div
      class="tree-row"
      :class="{ active: treeStore.activePath.value === childPath(item.name) }"
      :style="{ paddingLeft: `${depth * 16}px` }"
      @click="item.isDir ? onDirClick(childPath(item.name)) : onFileClick(childPath(item.name), item)"
      @contextmenu.prevent="onContextmenu(childPath(item.name), item)"
    >
      <span
        v-if="item.isDir"
        class="tree-icon expand"
        :class="{ open: isExpanded(childPath(item.name)) }"
        v-html="treeStore.loading.has(childPath(item.name)) ? spHtml : expIcon"
      ></span>
      <span v-else class="tree-icon"></span>
      <span
        class="tree-icon"
        :class="item.isDir ? 'dir' : 'file'"
        v-html="iconHtml(item)"
      ></span>
      <span class="tree-name" :title="item.name">{{ item.name }}</span>
    </div>

    <div v-if="item.isDir && isExpanded(childPath(item.name))" class="tree-children open">
      <TreeNode :path="childPath(item.name)" :depth="depth + 1" />
    </div>
  </div>
  </template>
</template>
