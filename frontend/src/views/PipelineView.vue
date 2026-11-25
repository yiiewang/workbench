<script setup lang="ts">
// 编排模板页：模板详情的主体是流程图（不是 JSON 文本框）。
// 顶部紧凑设置栏、右侧节点 Inspector、底部运行记录；点某次运行会把状态
// 叠加到同一张模板流程图上，而不是在记录里另画一张图。
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  getPipelineTemplate, createPipelineTemplate,
  updatePipelineTemplate, deletePipelineTemplate, runPipelineTemplate,
  listPipelineRuns, getPipelineRun,
  type PipelineRun, type PipelineStepResult, type PipelineGraphStep,
} from '../api/pipelines'
import { showToast } from '../lib/common'
import PipelineGraph from '../components/pipeline/PipelineGraph.vue'
import PipelineTriggerCard from '../components/ai/PipelineTriggerCard.vue'
import PipelineVersionCard from '../components/pipeline/PipelineVersionCard.vue'

const route = useRoute()
const router = useRouter()

const DEFAULT_DEF = JSON.stringify({
  name: '索引巡检',
  allowWrite: [],
  steps: [
    { id: 'check', uses: 'tool/reindex_status' },
    { id: 'summary', uses: 'llm', with: { prompt: '根据 {{ steps.check.output }} 用一句话总结索引状态' } },
  ],
}, null, 2)

const runs = ref<PipelineRun[]>([])
const runDetail = ref<Record<string, PipelineRun>>({})
const saving = ref(false)
const running = ref(false)
const runsLoading = ref(false)
const form = ref({ name: '', definition: DEFAULT_DEF })
const showJson = ref(false)
const activeRunId = ref<string | null>(null)
const inspected = ref<{ step: PipelineGraphStep; result?: PipelineStepResult } | null>(null)

const selected = computed<number | 'new' | null>(() => {
  const q = route.query.id
  if (q === 'new') return 'new'
  const n = Number(q)
  return q && Number.isFinite(n) && n > 0 ? n : null
})
const runSteps = computed<PipelineStepResult[] | undefined>(() =>
  activeRunId.value ? runDetail.value[activeRunId.value]?.steps : undefined
)

async function loadRuns() {
  runsLoading.value = true
  try {
    const data = await listPipelineRuns(20)
    runs.value = data?.runs || []
    runDetail.value = {}
    activeRunId.value = null
    inspected.value = null
  } catch {
    runs.value = []
  } finally {
    runsLoading.value = false
  }
}

async function loadTemplate(id: number) {
  try {
    const t = await getPipelineTemplate(id)
    form.value = { name: t.name, definition: t.definition || DEFAULT_DEF }
  } catch (e: any) {
    showToast('载入模板失败：' + (e?.msg || e?.message || ''))
  }
}

async function onVersionRestored() {
  if (typeof selected.value === 'number') await loadTemplate(selected.value)
}

watch(selected, async (sel) => {
  showJson.value = false
  activeRunId.value = null
  inspected.value = null
  if (sel === 'new') {
    form.value = { name: '', definition: DEFAULT_DEF }
    await loadRuns()
    return
  }
  if (typeof sel === 'number') {
    await loadTemplate(sel)
    await loadRuns()
  }
}, { immediate: true })

function notifyChanged() {
  window.dispatchEvent(new Event('pipelines:changed'))
}

function validate(): boolean {
  if (!form.value.name.trim()) {
    showToast('名称必填')
    return false
  }
  try {
    JSON.parse(form.value.definition)
  } catch {
    showToast('Definition 不是合法 JSON')
    return false
  }
  return true
}

function body() {
  return {
    name: form.value.name.trim(),
    definition: form.value.definition,
  }
}

async function save() {
  if (!validate()) return
  saving.value = true
  try {
    if (selected.value === 'new') {
      const data = await createPipelineTemplate(body())
      showToast('已创建')
      router.replace({ query: { id: String(data.id) } })
    } else if (typeof selected.value === 'number') {
      await updatePipelineTemplate(selected.value, body())
      showToast('已保存')
    }
    notifyChanged()
  } catch (e: any) {
    showToast('保存失败：' + (e?.msg || e?.message || ''))
  } finally {
    saving.value = false
  }
}

async function remove() {
  if (typeof selected.value !== 'number') return
  if (!confirm(`删除模板「${form.value.name}」？`)) return
  try {
    await deletePipelineTemplate(selected.value)
    showToast('已删除')
    notifyChanged()
    router.replace({ query: {} })
  } catch (e: any) {
    showToast('删除失败：' + (e?.msg || e?.message || ''))
  }
}

async function runNow() {
  if (typeof selected.value !== 'number') {
    showToast('请先保存模板再执行')
    return
  }
  running.value = true
  try {
    const data = await runPipelineTemplate(selected.value)
    showToast('已在后台启动，runId：' + data.runId)
    await loadRuns()
    notifyChanged()
  } catch (e: any) {
    showToast('触发失败：' + (e?.msg || e?.message || ''))
  } finally {
    running.value = false
  }
}

async function selectRun(r: PipelineRun) {
  if (activeRunId.value === r.id) {
    activeRunId.value = null
    return
  }
  activeRunId.value = r.id
  if (!runDetail.value[r.id]) {
    try {
      runDetail.value[r.id] = await getPipelineRun(r.id)
    } catch {
      showToast('加载运行详情失败')
    }
  }
}

async function refreshRuns() {
  await loadRuns()
}

function statusText(): string {
  const latest = runs.value[0]
  if (!latest) return '尚无运行记录'
  return `最近 ${latest.startedAt} · ${latest.status}`
}

function pretty(v: unknown): string {
  if (v === undefined || v === null) return '—'
  if (typeof v === 'string') return v
  return JSON.stringify(v, null, 2)
}

const inspectedOutput = computed(() => {
  const r = inspected.value?.result
  if (!r) return '（未叠加运行状态）'
  if (r.error) return r.error
  return pretty(r.output ?? '（无输出）')
})

const needsText = computed(() => {
  const n = inspected.value?.step.needs
  if (n === undefined) return '默认：依赖前一步'
  if (!n.length) return '[]（无依赖，可并行）'
  return n.join('、')
})
</script>

<template>
<div class="pipe-page">
  <div v-if="selected === null" class="pipe-empty">
    <div class="pipe-empty-icon">⛓</div>
    <p>从左侧选择一个编排模板，或新建一个。</p>
    <p class="pipe-empty-sub">
      模板的主体是一张流程图：每个节点是一种能力（AI 工具 / 站内 API / LLM 生成），
      用 needs 声明依赖，用双花括号引用上一步输出。定时触发在独立的规则里配置。
    </p>
  </div>

  <template v-else>
    <div class="pipe-head">
      <div class="pipe-head-main">
        <h2>{{ selected === 'new' ? '新建编排模板' : (form.name || '编排模板') }}</h2>
        <p class="pipe-status">{{ statusText() }}</p>
      </div>
      <div class="pipe-head-actions">
        <button v-if="selected !== 'new'" class="btn" :disabled="running" @click="runNow">
          {{ running ? '启动中…' : '立即执行' }}
        </button>
        <button v-if="selected !== 'new'" class="btn btn-danger-ghost" @click="remove">删除</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </div>

    <!-- 紧凑设置栏：模板只存名称与 Definition -->
    <div class="pipe-settings">
      <div class="form-row">
        <label>模板名称</label>
        <input v-model="form.name" class="form-input" placeholder="如：每日索引巡检" maxlength="100" />
      </div>
      <div class="pipe-settings-hint">流程由下方 Definition 定义；定时运行是独立的触发规则。</div>
    </div>

    <PipelineTriggerCard :template-id="typeof selected === 'number' ? selected : null" />
    <PipelineVersionCard :template-id="typeof selected === 'number' ? selected : null" @restored="onVersionRestored" />

    <div class="pipe-main">
      <section class="pipe-board">
        <div class="pipe-board-head">
          <span class="pipe-card-title">流程图</span>
          <div class="pipe-board-tools">
            <button v-if="activeRunId" class="pipe-tool" @click="activeRunId = null">清除运行状态</button>
            <button class="pipe-tool" :class="{ on: showJson }" @click="showJson = !showJson">
              {{ showJson ? '返回流程图' : '查看 / 编辑 JSON' }}
            </button>
          </div>
        </div>
        <div v-if="showJson" class="pipe-json">
          <textarea v-model="form.definition" class="form-input mono" rows="16" spellcheck="false"></textarea>
          <p class="form-hint">
            uses 支持 tool/&lt;工具名&gt;、api/&lt;站内路径&gt;、llm；needs 不填 = 依赖前一步，
            填 [] = 同层并行，填 id 列表 = DAG；when 可选 on_success / on_failure / always，retry 为 0–5；
            写类工具与非 GET 的 api 步骤须列入 allowWrite。
          </p>
        </div>
        <PipelineGraph v-else :definition="form.definition" :steps="runSteps" @select="inspected = $event" />
      </section>

      <aside class="pipe-inspector">
        <div class="pipe-card-title">节点详情</div>
        <template v-if="inspected">
          <div class="pi-head">
            <code>{{ inspected.step.id }}</code>
            <button class="pi-close" @click="inspected = null">×</button>
          </div>
          <div class="pi-row"><label>能力</label><code>{{ inspected.step.uses }}</code></div>
          <div class="pi-row"><label>依赖 needs</label><span>{{ needsText }}</span></div>
          <div class="pi-row"><label>条件 when</label><span>{{ inspected.step.when || 'on_success' }}</span></div>
          <div class="pi-row"><label>重试 retry</label><span>{{ inspected.step.retry || 0 }}</span></div>
          <div class="pi-row" v-if="inspected.result">
            <label>运行状态</label>
            <span class="pi-state" :class="inspected.result.status">{{ inspected.result.status }}</span>
          </div>
          <div class="pi-block">
            <label>参数 with</label>
            <pre>{{ pretty(inspected.step.with) }}</pre>
          </div>
          <div class="pi-block">
            <label>输出 / 错误</label>
            <pre>{{ inspectedOutput }}</pre>
          </div>
        </template>
        <div v-else class="pi-empty">
          点击流程图中的节点查看定义与运行结果。
          <div class="pi-legend">
            <span><i class="lg tool"></i>AI 工具</span>
            <span><i class="lg api"></i>站内 API</span>
            <span><i class="lg llm"></i>LLM 生成</span>
          </div>
        </div>
      </aside>
    </div>

    <section class="pipe-runs">
      <div class="pipe-runs-head">
        <span class="pipe-card-title">最近执行</span>
        <span class="pipe-runs-tip">点击某次运行，状态会叠加到上方流程图</span>
        <button class="pipe-tool" @click="refreshRuns">{{ runsLoading ? '刷新中…' : '刷新' }}</button>
      </div>
      <div class="pipe-runs-list">
        <div v-if="!runs.length && !runsLoading" class="pipe-runs-empty">还没有执行记录：手动执行或 cron 触发后显示在这里</div>
        <div
          v-for="r in runs"
          :key="r.id"
          class="pipe-run-row"
          :class="{ active: activeRunId === r.id, failed: r.status === 'failed' }"
          @click="selectRun(r)"
        >
          <span class="pipe-dot" :class="r.status"></span>
          <span class="pipe-run-name">{{ r.name || '未命名流程' }}</span>
          <span class="pipe-run-time">{{ r.startedAt }}</span>
          <span class="pipe-run-state">{{ r.status }}</span>
          <span class="pipe-run-hint">{{ activeRunId === r.id ? '已叠加 · 点击取消' : '叠加到流程图' }}</span>
        </div>
      </div>
    </section>
  </template>
</div>
</template>

<style scoped>
.pipe-page { padding: 20px 24px; height: 100%; box-sizing: border-box; display: flex; flex-direction: column; min-height: 0; gap: 14px; }
.pipe-empty { margin: auto; text-align: center; color: var(--text-dim); max-width: 560px; line-height: 1.8; }
.pipe-empty-icon { font-size: 36px; margin-bottom: 12px; }
.pipe-empty-sub { font-size: 12px; }

/* 顶部标题与操作 */
.pipe-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
.pipe-head h2 { margin: 0; font-size: 19px; color: var(--text); }
.pipe-status { margin: 4px 0 0; font-size: 12px; color: var(--text-dim); }
.pipe-head-actions { display: flex; gap: 8px; flex-wrap: wrap; }

/* 表单：本组件自己定义，AiJobsView 的同名类是 scoped 的，不能复用 */
.pipe-settings {
  display: flex; align-items: flex-start; gap: 16px; flex-wrap: wrap;
  padding: 14px 16px; border: 1px solid var(--border); border-radius: 12px; background: var(--bg);
}
.pipe-settings .form-row { flex: 1 1 260px; min-width: 200px; }
.pipe-settings-hint { font-size: 11px; color: var(--text-dim); align-self: flex-end; padding-bottom: 9px; }
.form-row label { display: block; font-size: 11px; color: var(--text-dim); margin-bottom: 5px; font-weight: 600; letter-spacing: .3px; }
.form-input {
  width: 100%; box-sizing: border-box; border: 1px solid var(--border); border-radius: 8px;
  padding: 8px 10px; font-size: 13px; font-family: inherit; background: var(--bg); color: var(--text);
}
.form-input:focus { outline: none; border-color: var(--accent); }
.form-input.mono { font-family: var(--mono, monospace); font-size: 12px; }
textarea.form-input { resize: vertical; line-height: 1.6; }
.form-hint { font-size: 11px; color: var(--text-dim); line-height: 1.7; margin: 8px 0 0; }

/* 主区：流程图 + Inspector */
.pipe-main { display: grid; grid-template-columns: minmax(0, 1fr) 280px; gap: 14px; flex: 1; min-height: 0; }
@media (max-width: 1100px) { .pipe-main { grid-template-columns: 1fr; } }
.pipe-board {
  border: 1px solid var(--border); border-radius: 12px; background: var(--bg);
  display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden;
}
.pipe-board-head, .pipe-runs-head {
  display: flex; align-items: center; gap: 10px; padding: 10px 14px;
  border-bottom: 1px solid var(--border); background: var(--code-bg);
}
.pipe-card-title { font-size: 11px; font-weight: 600; letter-spacing: .8px; color: var(--text-dim); text-transform: uppercase; }
.pipe-board-tools { margin-left: auto; display: flex; gap: 6px; }
.pipe-tool {
  border: 1px solid var(--border); background: var(--bg); color: var(--text-dim);
  border-radius: 8px; padding: 3px 10px; font-size: 11px; cursor: pointer;
}
.pipe-tool:hover, .pipe-tool.on { color: var(--accent); border-color: var(--accent); }
.pipe-json { padding: 14px; overflow: auto; }

/* Inspector */
.pipe-inspector {
  border: 1px solid var(--border); border-radius: 12px; background: var(--bg);
  padding: 12px 14px; overflow: auto; min-height: 0;
}
.pi-head { display: flex; align-items: center; gap: 8px; margin: 10px 0 12px; }
.pi-head code { font-size: 14px; font-weight: 650; color: var(--text); }
.pi-close { margin-left: auto; border: none; background: none; color: var(--text-dim); font-size: 18px; cursor: pointer; line-height: 1; }
.pi-row { display: flex; align-items: baseline; gap: 8px; margin-bottom: 8px; font-size: 12px; }
.pi-row label, .pi-block label { color: var(--text-dim); font-size: 10px; min-width: 66px; }
.pi-row code, .pi-row span { color: var(--text); word-break: break-all; }
.pi-state { font-size: 10px; padding: 1px 7px; border-radius: 8px; border: 1px solid var(--border); }
.pi-state.ok { color: #2e7d32; }
.pi-state.failed { color: #cf222e; }
.pi-block { margin-top: 10px; }
.pi-block label { display: block; margin-bottom: 4px; }
.pi-block pre {
  margin: 0; padding: 8px; border-radius: 8px; background: var(--code-bg);
  font-size: 10px; white-space: pre-wrap; word-break: break-all; max-height: 180px; overflow: auto;
}
.pi-empty { font-size: 12px; color: var(--text-dim); line-height: 1.8; margin-top: 16px; }
.pi-legend { display: flex; flex-direction: column; gap: 6px; margin-top: 16px; font-size: 11px; }
.pi-legend span { display: flex; align-items: center; gap: 6px; }
.lg { width: 4px; height: 12px; border-radius: 2px; display: inline-block; }
.lg.tool { background: #3b82f6; }
.lg.api { background: #8b5cf6; }
.lg.llm { background: #10a37f; }

/* 运行记录：只叠加到上方流程图 */
.pipe-runs { border: 1px solid var(--border); border-radius: 12px; background: var(--bg); overflow: hidden; }
.pipe-runs-tip { font-size: 10px; color: var(--text-dim); }
.pipe-runs-head .pipe-tool { margin-left: auto; }
.pipe-runs-list { max-height: 168px; overflow: auto; }
.pipe-runs-empty { font-size: 12px; color: var(--text-dim); text-align: center; padding: 20px; }
.pipe-run-row {
  display: flex; align-items: center; gap: 10px; padding: 8px 14px; cursor: pointer;
  border-bottom: 1px solid var(--border); font-size: 12px; color: var(--text);
}
.pipe-run-row:last-child { border-bottom: none; }
.pipe-run-row:hover { background: var(--code-bg); }
.pipe-run-row.active { background: var(--code-bg); }
.pipe-dot { width: 8px; height: 8px; border-radius: 50%; background: #9e9e9e; flex-shrink: 0; }
.pipe-dot.done { background: #2e7d32; }
.pipe-dot.failed { background: #cf222e; }
.pipe-dot.running { background: #f0a020; }
.pipe-run-row.failed { border-left: 3px solid #cf222e; }
.pipe-run-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pipe-run-time { font-size: 10px; color: var(--text-dim); font-family: var(--mono, monospace); }
.pipe-run-state { font-size: 10px; color: var(--text-dim); }
.pipe-run-hint { font-size: 10px; color: var(--accent); flex-shrink: 0; }
</style>
