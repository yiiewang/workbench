<script setup lang="ts">
// AI 定时任务侧栏：任务列表（名称 + cron + 启用状态点），点击在主区域打开。
import { ref, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { listAIJobs, type AIJob } from '../../api/aiJobs'

const route = useRoute()
const router = useRouter()

const jobs = ref<AIJob[]>([])
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
    const data = await listAIJobs()
    jobs.value = data?.jobs || []
  } catch { /* 静默 */ } finally {
    loading.value = false
  }
}

// 主区域增删改后刷新列表
function onChanged() { load() }
window.addEventListener('aijobs:changed', onChanged)
onMounted(() => { syncFromRoute(); load() })
onUnmounted(() => window.removeEventListener('aijobs:changed', onChanged))
</script>

<template>
  <div class="aijobs-sidebar">
    <button class="aijobs-new" @click="open('new')">＋ 新建任务</button>
    <div v-if="loading && !jobs.length" class="aijobs-s-empty">加载中…</div>
    <div v-else-if="!jobs.length" class="aijobs-s-empty">暂无任务</div>
    <div
      v-for="j in jobs"
      :key="j.id"
      class="aijobs-s-item"
      :class="{ active: activeId === j.id, off: !j.enabled }"
      @click="open(j.id)"
    >
      <span class="aijobs-s-dot" :class="j.enabled ? 'on' : 'off'"></span>
      <div class="aijobs-s-main">
        <div class="aijobs-s-name">
          {{ j.name }}
          <span v-if="j.isGlobal" class="aijobs-s-tag">公共</span>
        </div>
        <code class="aijobs-s-cron">{{ j.cronExpr }}</code>
      </div>
    </div>
  </div>
</template>

<style scoped>
.aijobs-sidebar { padding: 12px; display: flex; flex-direction: column; gap: 4px; }
.aijobs-new {
  border: 1px dashed var(--border); background: none; color: var(--accent);
  border-radius: 8px; padding: 8px; font-size: 12px; cursor: pointer; margin-bottom: 6px;
}
.aijobs-new:hover { border-color: var(--accent); }
.aijobs-s-empty { font-size: 12px; color: var(--text-dim); text-align: center; padding: 20px 0; }
.aijobs-s-item {
  display: flex; align-items: flex-start; gap: 8px; padding: 8px;
  border-radius: 8px; cursor: pointer;
}
.aijobs-s-item:hover { background: var(--code-bg); }
.aijobs-s-item.active { background: var(--code-bg); }
.aijobs-s-item.off { opacity: .5; }
.aijobs-s-dot { width: 8px; height: 8px; border-radius: 50%; margin-top: 5px; flex-shrink: 0; }
.aijobs-s-dot.on { background: #2e7d32; }
.aijobs-s-dot.off { background: var(--border); }
.aijobs-s-main { min-width: 0; flex: 1; }
.aijobs-s-name {
  font-size: 13px; color: var(--text); font-weight: 500;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  display: flex; align-items: center; gap: 4px;
}
.aijobs-s-tag {
  font-size: 9px; color: var(--accent); border: 1px solid var(--accent);
  border-radius: 6px; padding: 0 4px; flex-shrink: 0;
}
.aijobs-s-cron { font-size: 10px; color: var(--text-dim); font-family: var(--mono, monospace); }
</style>
