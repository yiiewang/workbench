<script setup lang="ts">
// 模板版本历史：每次保存/回滚都会产生新版本，可回滚到任意历史版本。
import { ref, watch } from 'vue'
import {
  listPipelineTemplateVersions, restorePipelineTemplateVersion,
  type PipelineTemplateVersion,
} from '../../api/pipelines'
import { showToast } from '../../lib/common'

const props = defineProps<{ templateId: number | null }>()
const emit = defineEmits<{ (e: 'restored'): void }>()

const versions = ref<PipelineTemplateVersion[]>([])
const loading = ref(false)

async function load() {
  if (!props.templateId) {
    versions.value = []
    return
  }
  loading.value = true
  try {
    const data = await listPipelineTemplateVersions(props.templateId)
    versions.value = data?.versions || []
  } catch {
    versions.value = []
  } finally {
    loading.value = false
  }
}

async function restore(v: PipelineTemplateVersion) {
  if (!props.templateId) return
  if (!confirm(`回滚到版本 v${v.version}（${v.name}）？会生成一个新版本，当前内容仍可在历史中找回。`)) return
  try {
    const data = await restorePipelineTemplateVersion(props.templateId, v.version)
    showToast(`已回滚，新版本 v${data.version}`)
    await load()
    emit('restored')
  } catch (e: any) {
    showToast('回滚失败：' + (e?.msg || e?.message || ''))
  }
}

watch(() => props.templateId, load, { immediate: true })
</script>

<template>
  <section v-if="templateId" class="pv-card">
    <div class="pv-head">
      <span class="pv-title">版本历史</span>
      <button class="pv-tool" @click="load">{{ loading ? '刷新中…' : '刷新' }}</button>
    </div>
    <div v-if="!versions.length && !loading" class="pv-empty">还没有版本记录</div>
    <div class="pv-list">
      <div v-for="v in versions" :key="v.version" class="pv-row">
        <span class="pv-no">v{{ v.version }}</span>
        <span class="pv-name">{{ v.name }}</span>
        <span class="pv-time">{{ v.createdAt }}</span>
        <button class="pv-tool" @click="restore(v)">回滚</button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.pv-card {
  border: 1px solid var(--border); border-radius: 10px;
  background: var(--bg); overflow: hidden;
}
.pv-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 8px 14px; border-bottom: 1px solid var(--border); background: var(--code-bg);
}
.pv-title { font-size: 11px; font-weight: 600; letter-spacing: .8px; color: var(--text-dim); text-transform: uppercase; }
.pv-tool {
  border: none; background: none; color: var(--text-dim); font-size: 11px;
  cursor: pointer; padding: 2px 6px; border-radius: 4px;
}
.pv-tool:hover { color: var(--accent); }
.pv-empty { padding: 14px; font-size: 12px; color: var(--text-dim); }
.pv-list { max-height: 180px; overflow-y: auto; }
.pv-row {
  display: flex; align-items: center; gap: 10px;
  padding: 6px 14px; font-size: 12px; border-top: 1px solid var(--border);
}
.pv-row:first-child { border-top: none; }
.pv-no { font-family: var(--mono, monospace); color: var(--accent); flex-shrink: 0; }
.pv-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pv-time { color: var(--text-muted); font-size: 11px; flex-shrink: 0; }
</style>
