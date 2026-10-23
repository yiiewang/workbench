<script setup lang="ts">
// 右下角悬浮目录：点图标展开标题列表，点击即定位（fixed 定位，不占正文宽度）
import { ref, onMounted, onUnmounted } from 'vue'
import { scrollToId, type MdHeading } from '../../lib/markdownFold'

const props = defineProps<{ outline: MdHeading[]; root: HTMLElement | null }>()

const open = ref(false)
const activeId = ref('')

function toggle() {
  open.value = !open.value
}

function findEl(id: string): HTMLElement | null {
  // 只在预览容器内查找，避免与其它预览区的同名标题 id 冲突
  return props.root ? props.root.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`) : null
}

function goTo(h: MdHeading) {
  // scrollToId 会先展开目标所在的折叠区，再平滑滚动
  if (!scrollToId(props.root, h.id)) return
  activeId.value = h.id
  open.value = false
}

function onDocClick(e: MouseEvent) {
  const t = e.target as HTMLElement | null
  if (!t?.closest('.md-toc')) open.value = false
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false
}

// 滚动时高亮当前标题：capture 阶段监听，任何祖先滚动容器都能收到
function onScroll() {
  const list = props.outline
  if (!list.length) return
  let current = list[0].id
  for (const h of list) {
    const el = findEl(h.id)
    if (!el) continue
    if (el.getBoundingClientRect().top <= 80) current = h.id
    else break
  }
  activeId.value = current
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  document.addEventListener('keydown', onKey)
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', onScroll)
  onScroll()
})

onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onKey)
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', onScroll)
})
</script>

<template>
  <div v-if="outline.length" class="md-toc">
    <button
      class="md-toc-fab"
      :class="{ active: open }"
      type="button"
      title="目录"
      aria-label="目录"
      @click.stop="toggle"
    >
      <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
        <path d="M2.5 4h11M2.5 8h11M2.5 12h7" />
      </svg>
    </button>

    <div v-show="open" class="md-toc-panel" @click.stop>
      <div class="md-toc-head">目录</div>
      <div class="md-toc-list">
        <a
          v-for="(h, i) in outline"
          :key="h.id + '-' + i"
          class="md-toc-item"
          :class="[`lv-${h.level}`, { active: h.id === activeId }]"
          :href="`#${h.id}`"
          :title="h.text"
          @click.prevent="goTo(h)"
        >{{ h.text }}</a>
      </div>
    </div>
  </div>
</template>

<style scoped>
.md-toc {
  position: fixed;
  right: 24px;
  bottom: 24px;
  z-index: 60;
}

.md-toc-fab {
  width: 38px;
  height: 38px;
  border-radius: 50%;
  border: 1px solid var(--border, #e5e5e5);
  background: var(--bg, #fff);
  color: var(--text-dim, #888);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.12);
}

.md-toc-fab:hover,
.md-toc-fab.active {
  color: var(--accent, #007acc);
  border-color: var(--accent, #007acc);
}

.md-toc-panel {
  position: absolute;
  right: 0;
  bottom: 48px;
  width: 280px;
  max-width: calc(100vw - 48px);
  max-height: 50vh;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border, #e5e5e5);
  border-radius: 6px;
  background: var(--bg, #fff);
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.16);
  overflow: hidden;
}

.md-toc-head {
  padding: 8px 12px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-dim, #888);
  border-bottom: 1px solid var(--border, #e5e5e5);
  background: var(--header-bg, #f3f3f3);
}

.md-toc-list {
  overflow-y: auto;
  padding: 6px 0;
}

.md-toc-item {
  display: block;
  padding: 5px 12px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--text, #333);
  text-decoration: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.md-toc-item:hover {
  background: var(--hover, #e8e8e8);
}

.md-toc-item.active {
  color: var(--accent, #007acc);
  font-weight: 600;
  background: var(--active, #e4e6f1);
}

.md-toc-item.lv-1 { padding-left: 12px; }
.md-toc-item.lv-2 { padding-left: 24px; }
.md-toc-item.lv-3 { padding-left: 36px; }
.md-toc-item.lv-4 { padding-left: 48px; }
.md-toc-item.lv-5 { padding-left: 60px; }
.md-toc-item.lv-6 { padding-left: 72px; }

@media (max-width: 768px) {
  .md-toc {
    right: 16px;
    bottom: 16px;
  }
}
</style>
