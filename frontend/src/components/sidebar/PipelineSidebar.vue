<script setup lang="ts">
// 编排模板侧栏：模板列表（名称 + cron + 启用状态点），点击在主区域打开。
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { listPipelineTemplates, type PipelineTemplate } from '../../api/pipelines'

const route = useRoute()
const router = useRouter()
const templates = ref<PipelineTemplate[]>([])
const loading = ref(false)
const activeId = ref<number | 'new' | null>(null)

function syncFromRoute() {
  const q = route.query.id
  if (q === 'new') activeId.value = 'new'
  else if (q) activeId.value = Number(q)
  else activeId.value = null
}

function open(id: number | 'new') {
  router.push({ query: { id: String(id) } })
}

async function load() {
  loading.value = true
  try {
    const data = await listPipelineTemplates()
    templates.value = data?.templates || []
  } catch { /* 静默 */ } finally {
    loading.value = false
  }
}

// 主区域增删改后刷新列表
function onChanged() { load() }
watch(() => route.query.id, syncFromRoute, { immediate: true })
onMounted(() => {
  window.addEventListener('pipelines:changed', onChanged)
  load()
})
onUnmounted(() => window.removeEventListener('pipelines:changed', onChanged))
</script>

<template>
  <div class="pipe-sidebar">
    <button class="pipe-new" @click="open('new')">＋ 新建模板</button>
    <div v-if="loading && !templates.length" class="pipe-s-empty">加载中…</div>
    <div v-else-if="!templates.length" class="pipe-s-empty">暂无模板</div>
    <div
      v-for="t in templates"
      :key="t.id"
      class="pipe-s-item"
      :class="{ active: activeId === t.id }"
      @click="open(t.id)"
    >
      <span class="pipe-s-dot on"></span>
      <div class="pipe-s-main">
        <div class="pipe-s-name">{{ t.name }}</div>
        <code class="pipe-s-cron">{{ (t.createdAt || '').slice(0, 10) }}</code>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pipe-sidebar { padding: 12px; display: flex; flex-direction: column; gap: 4px; }
.pipe-new {
  border: 1px dashed var(--border); background: none; color: var(--accent);
  border-radius: 8px; padding: 8px; font-size: 12px; cursor: pointer; margin-bottom: 6px;
}
.pipe-new:hover { border-color: var(--accent); }
.pipe-s-empty { font-size: 12px; color: var(--text-dim); text-align: center; padding: 20px 0; }
.pipe-s-item { display: flex; align-items: flex-start; gap: 8px; padding: 8px; border-radius: 8px; cursor: pointer; }
.pipe-s-item:hover, .pipe-s-item.active { background: var(--code-bg); }
.pipe-s-dot { width: 8px; height: 8px; border-radius: 50%; margin-top: 5px; flex-shrink: 0; }
.pipe-s-dot.on { background: var(--accent); }
.pipe-s-main { min-width: 0; flex: 1; }
.pipe-s-name {
  font-size: 13px; color: var(--text); font-weight: 500;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.pipe-s-cron { font-size: 10px; color: var(--text-dim); font-family: var(--mono, monospace); }
</style>
