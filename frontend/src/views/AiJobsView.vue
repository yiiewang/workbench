<script setup lang="ts">
// AI 定时任务主区域：选中任务（route query id）的详情编辑 + 执行历史。
// 任务列表在侧栏（AiJobsSidebar），选中经 route query ?id= 同步。
// 表单校验/提交体与执行记录轮询分别在 composables/useAIJobForm、useJobRuns。
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  listAIJobs, createAIJob, updateAIJob, deleteAIJob, runAIJob, type AIJob,
} from '../api/aiJobs'
import {
  runPipeline, getPipelineRun, streamPipelineRun,
  type PipelineStepResult, type PipelineGraphStep,
} from '../api/pipelines'
import { showToast, errMsg, prettyJson } from '../lib/common'
import { useAIJobForm, emptyAIJobForm, CRON_PRESETS } from '../composables/useAIJobForm'
import { useJobRuns } from '../composables/useJobRuns'
import PipelineGraph from '../components/pipeline/PipelineGraph.vue'

const route = useRoute()
const router = useRouter()

const jobs = ref<AIJob[]>([])
const saving = ref(false)

// 当前选中：'new' = 新建 | 数字 = 编辑 | null = 未选择
const selected = computed<number | 'new' | null>(() => {
  const q = route.query.id
  if (q === 'new') return 'new'
  const n = Number(q)
  return q && Number.isFinite(n) && n > 0 ? n : null
})

const selectedJob = computed(() => (typeof selected.value === 'number' ? jobs.value.find(j => j.id === selected.value) : undefined))

const { form, fillForm, validateForm, buildBody } = useAIJobForm()
const { runs, runsLoading, runsOpen, loadRuns, runStepList, toggleRun, resetRuns } = useJobRuns(
  () => (typeof selected.value === 'number' ? selected.value : null),
)

// 编排可视化：JSON / 流程图切换、节点 Inspector、运行状态叠加
const showJson = ref(false)
const inspected = ref<{ step: PipelineGraphStep; result?: PipelineStepResult } | null>(null)
const activeRunId = ref<string | null>(null)
const runSteps = ref<PipelineStepResult[] | undefined>(undefined)
const running = ref(false)
// 立即执行后的实时订阅：SSE 连接的 AbortController 与「运行中」指示
const runStreamAbort = ref<AbortController | null>(null)
const streaming = ref(false)

/** 停掉进行中的实时订阅（切换叠加目标 / 清除状态 / 离开页面） */
function stopRunStream() {
  runStreamAbort.value?.abort()
  runStreamAbort.value = null
  streaming.value = false
}

onBeforeUnmount(stopRunStream)

function pretty(v: unknown): string {
  if (v === undefined || v === null) return '—'
  if (typeof v === 'string') return v
  return prettyJson(v)
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

async function overlayRun(runId: string) {
  if (activeRunId.value === runId) {
    activeRunId.value = null
    runSteps.value = undefined
    stopRunStream()
    return
  }
  stopRunStream()
  try {
    const detail = await getPipelineRun(runId)
    activeRunId.value = runId
    runSteps.value = detail.steps
  } catch {
    showToast('加载编排运行详情失败')
  }
}

async function runNow() {
  // 已存任务：走 job_runs 记录的执行路径，详情页可轮询到 running/步骤
  if (typeof selected.value === 'number') {
    running.value = true
    try {
      await runAIJob(selected.value)
      showToast('已触发，执行状态见下方执行记录')
      await loadRuns(selected.value)
    } catch (e: any) {
      showToast('触发失败：' + (errMsg(e, '')))
    } finally {
      running.value = false
    }
    return
  }
  // 新建任务：用当前表单 Definition 试跑（不写任务历史）
  let def: unknown
  try {
    def = JSON.parse(form.value.definition || '')
  } catch {
    showToast('Definition 不是合法 JSON')
    return
  }
  running.value = true
  try {
    const data = await runPipeline(def)
    showToast('已在后台启动，runId：' + data.runId)
    if (typeof selected.value === 'number') await loadRuns(selected.value)
    // 叠加本次运行并建立 SSE 实时订阅：连接建立即推首份快照，
    // 之后每个 DAG 层落库都会推新快照，流程图节点状态实时刷新
    stopRunStream()
    activeRunId.value = data.runId
    runSteps.value = []
    const ac = new AbortController()
    runStreamAbort.value = ac
    streaming.value = true
    streamPipelineRun(data.runId, {
      signal: ac.signal,
      onProgress: (r) => {
        if (activeRunId.value !== data.runId) return // 已切换/清除叠加目标
        runSteps.value = r.steps
      },
    })
      .catch(async (e: any) => {
        // 断流兜底：拉一次最终详情补齐（非 end 结束 = 连接异常中断）
        if (activeRunId.value !== data.runId) return
        try {
          const detail = await getPipelineRun(data.runId)
          runSteps.value = detail.steps
          if (detail.status !== 'running') showToast('实时连接中断，已拉取最终结果')
        } catch {
          showToast('实时连接中断：' + (errMsg(e, '')))
        }
      })
      .finally(() => {
        if (runStreamAbort.value === ac) {
          runStreamAbort.value = null
          streaming.value = false
        }
        // 终态后刷新执行记录（断流兜底分支同样会走到这里）
        if (activeRunId.value === data.runId && typeof selected.value === 'number') {
          loadRuns(selected.value)
        }
      })
  } catch (e: any) {
    showToast('触发失败：' + (errMsg(e, '')))
  } finally {
    running.value = false
  }
}

async function loadJobs() {
  try {
    const data = await listAIJobs()
    jobs.value = data?.jobs || []
  } catch { /* 静默 */ }
}

// 选中变化：new → 空表单；数字 → 填表单 + 拉执行历史
watch(selected, async (sel) => {
  resetRuns()
  showJson.value = false
  activeRunId.value = null
  runSteps.value = undefined
  inspected.value = null
  if (sel === 'new') {
    form.value = emptyAIJobForm()
    return
  }
  if (typeof sel === 'number') {
    if (!jobs.value.length) await loadJobs()
    const j = jobs.value.find(x => x.id === sel)
    if (j) {
      fillForm(j)
      loadRuns(sel)
    }
  }
}, { immediate: true })

onMounted(loadJobs)

function notifyChanged() {
  window.dispatchEvent(new Event('aijobs:changed'))
}

async function save() {
  const msg = validateForm()
  if (msg) {
    showToast(msg)
    return
  }
  saving.value = true
  try {
    const body = buildBody()
    if (selected.value === 'new') {
      const data = await createAIJob(body)
      showToast('已创建，下一分钟生效')
      await loadJobs()
      router.replace({ query: { id: String(data.id) } })
    } else if (typeof selected.value === 'number') {
      await updateAIJob(selected.value, body)
      showToast('已更新')
      await loadJobs()
    }
    notifyChanged()
  } catch (e: any) {
    showToast('保存失败：' + (errMsg(e, '')))
  } finally {
    saving.value = false
  }
}

async function toggle() {
  const j = selectedJob.value
  if (!j) return
  try {
    await updateAIJob(j.id, { name: j.name, cronExpr: j.cronExpr, prompt: j.prompt, enabled: !j.enabled, target: j.target, allowWrite: j.allowWrite })
    form.value.enabled = !j.enabled
    await loadJobs()
    notifyChanged()
  } catch (e: any) {
    showToast('操作失败：' + (errMsg(e, '')))
  }
}

async function remove() {
  const j = selectedJob.value
  if (!j || j.isGlobal) return
  if (!confirm(`删除定时任务「${j.name}」？`)) return
  try {
    await deleteAIJob(j.id)
    showToast('已删除')
    notifyChanged()
    router.replace({ query: {} })
    await loadJobs()
  } catch (e: any) {
    showToast('删除失败：' + (errMsg(e, '')))
  }
}

function statusText(j: AIJob): string {
  if (!j.lastRunAt) return '未执行'
  if (j.lastStatus === 'ok') return `最近 ${j.lastRunAt} 成功`
  return `最近 ${j.lastRunAt} ${j.lastStatus || ''}`
}
</script>

<template>
<div class="aijobs-page">
  <!-- 未选择：空态引导 -->
  <div v-if="selected === null" class="aijobs-empty">
    <div class="aijobs-empty-icon">⏰</div>
    <p>从左侧选择一个定时任务，或新建一个。</p>
    <p class="aijobs-empty-sub">
      到点后 AI 会带着你的任务数据执行指令，结果邮件送达——
      比如每天 18:00 自动生成工作日报。
    </p>
  </div>

  <template v-else>
    <div class="aijobs-head">
      <div class="aijobs-head-main">
        <h2>{{ selected === 'new' ? '新建定时任务' : (form.name || '定时任务') }}</h2>
        <p v-if="running" class="aijobs-status">正在执行…</p>
        <p v-else-if="selectedJob" class="aijobs-status">{{ statusText(selectedJob) }}</p>
      </div>
      <div class="aijobs-head-actions">
        <button class="btn" :disabled="running" @click="runNow">
          {{ running ? '启动中…' : '立即执行' }}
        </button>
        <button
          v-if="selected !== 'new' && !selectedJob?.isGlobal"
          class="btn" :class="form.enabled ? '' : 'btn-primary'"
          @click="toggle"
        >{{ form.enabled ? '停用' : '启用' }}</button>
        <button v-if="selected !== 'new' && !selectedJob?.isGlobal" class="btn btn-danger-ghost" @click="remove">删除</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </div>

    <div class="aijobs-scroll">
      <!-- 任务详情卡片 -->
      <section class="aijobs-card">
        <div class="aijobs-card-head">
          <span class="aijobs-card-title">任务详情</span>
          <span v-if="selectedJob?.isGlobal" class="aijobs-global-tag">公共 · 系统内置</span>
        </div>
        <div class="aijob-form">
          <div class="form-row">
            <label>任务名称</label>
            <input v-model="form.name" class="form-input" placeholder="如：每日工作总结" maxlength="100"
              :readonly="selectedJob?.isGlobal" />
          </div>
          <div class="form-row">
            <label>执行频率（cron：分 时 日 月 周）</label>
            <input v-model="form.cronExpr" class="form-input mono" placeholder="0 18 * * *" />
            <div class="cron-presets">
              <button v-for="p in CRON_PRESETS" :key="p.expr" type="button"
                class="preset-btn" :class="{ active: form.cronExpr === p.expr }"
                @click="form.cronExpr = p.expr">{{ p.label }}</button>
            </div>
          </div>
          <div class="form-row">
            <label>Definition（编排流程，由 AI 生成或手动编辑）</label>
            <p class="form-hint">流程内联在本任务里；可以切换 JSON 编辑或流程图查看节点依赖与运行状态。</p>
          </div>

          <div class="form-row">
            <label>静态预授权（逗号分隔，写类工具或非 GET 接口）</label>
            <input v-model="form.allowWrite" class="form-input mono" placeholder="notify_user, api/tasks" />
            <p class="form-hint">定时执行无人审批，写操作必须在创建时显式列出；未列出的步骤会失败。</p>
          </div>
          <div class="form-row">
            <label>失败重试次数（0-3，仅无写操作时生效）</label>
            <input v-model.number="form.retry" type="number" min="0" max="3" class="form-input" style="max-width: 120px" />
          </div>
          <div class="form-row form-row-inline">
            <label class="switch-label">
              <input type="checkbox" v-model="form.enabled" />
              启用（下一分钟生效）
            </label>
          </div>
        </div>
      </section>

      <!-- 编排流程图 + 节点 Inspector -->
      <div class="pipe-main">
        <section class="pipe-board">
          <div class="pipe-board-head">
            <span class="aijobs-card-title">流程图</span>
            <div class="pipe-board-tools">
              <span v-if="streaming" class="pipe-live">运行中…</span>
              <button v-if="activeRunId" class="pipe-tool pipe-tool-on" @click="overlayRun(activeRunId)">清除运行状态</button>
              <button class="pipe-tool" :class="{ 'pipe-tool-on': showJson }" @click="showJson = !showJson">
                {{ showJson ? '返回流程图' : '查看 / 编辑 JSON' }}
              </button>
            </div>
          </div>
          <div v-if="showJson" class="pipe-json">
            <textarea v-model="form.definition" class="form-input mono" rows="16" spellcheck="false"></textarea>
            <p class="form-hint">
              uses 支持 tool/&lt;工具名&gt;、api/&lt;站内路径&gt;、llm；needs 不填 = 依赖前一步，
              填 [] = 同层并行，填 id 列表 = DAG；when 可选 on_success / on_failure / always，retry 为 0–5；
              写类工具与非 GET 的 api 步骤须列入静态预授权。
            </p>
          </div>
          <PipelineGraph v-else :definition="form.definition" :steps="runSteps" @select="inspected = $event" />
        </section>

        <aside class="pipe-inspector">
          <div class="aijobs-card-title">节点详情</div>
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

      <!-- 执行记录卡片 -->
      <section v-if="typeof selected === 'number'" class="aijobs-card">
        <div class="aijobs-card-head">
          <span class="aijobs-card-title">执行记录</span>
          <button class="aijobs-card-refresh" @click="loadRuns(selected)">{{ runsLoading ? '刷新中…' : '刷新' }}</button>
        </div>
        <div class="aijobs-card-body">
          <div v-if="!runs.length && !runsLoading" class="aijob-runs-empty">还没有执行记录<br /><span>到点触发后这里会显示每次的运行结果</span></div>
          <div v-for="(r, i) in runs" :key="r.id" class="aijob-run" :class="{ 'is-err': r.status !== 'ok' && r.status !== 'running' }">
            <button class="aijob-run-head" @click="toggleRun(i)">
              <span class="aijob-run-status" :class="r.status === 'ok' ? 'ok' : r.status === 'running' ? 'running' : 'err'">
                {{ r.status === 'ok' ? '✓' : r.status === 'running' ? '…' : '✕' }}
              </span>
              <span class="aijob-run-time">{{ r.ranAt }}</span>
              <span v-if="r.status === 'running'" class="aijob-run-live">运行中</span>
              <span v-if="r.targetType" class="aijob-run-type">{{ r.targetType }}</span>
              <span class="aijob-run-toggle">{{ runsOpen.has(i) ? '收起' : '查看输出' }}</span>
            </button>
            <div v-show="runsOpen.has(i)" class="aijob-run-detail">
              <div v-if="r.errMsg" class="aijob-run-error">{{ r.errMsg }}</div>
              <ul v-if="runStepList(r).length" class="aijob-run-steps">
                <li v-for="s in runStepList(r)" :key="s.id" :class="{ 'is-fail': s.status === 'failed' }">
                  <span class="s-id">{{ s.id }}</span>
                  <span class="s-uses">{{ s.uses }}</span>
                  <span class="s-status">{{ s.status }}</span>
                  <span v-if="s.durationMs" class="s-dur">{{ (s.durationMs / 1000).toFixed(1) }}s</span>
                  <span v-if="s.error" class="s-err">{{ s.error }}</span>
                </li>
              </ul>
              <pre class="aijob-run-output">{{ r.output || (r.status === 'running' ? '运行中…' : '（无输出）') }}</pre>
              <button v-if="r.pipelineRunId" class="aijob-run-link" @click="overlayRun(r.pipelineRunId!)">
                {{ activeRunId === r.pipelineRunId ? '已叠加 · 点击取消' : '叠加到流程图' }}
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>
  </template>
</div>
</template>

<style scoped>
/* 主区域：标题行固定，内容整体纵向滚动，避免嵌套 flex 高度塌陷 */
.aijobs-page { padding: 24px 28px; height: 100%; box-sizing: border-box; display: flex; flex-direction: column; min-height: 0; }
/* 子项禁止收缩：flex 列默认 shrink=1 会把卡片压扁并裁掉内容，
   超出高度应由外层滚动，而不是压缩单个区块 */
.aijobs-scroll { flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 14px; padding-bottom: 24px; }
.aijobs-scroll > * { flex-shrink: 0; }

/* 卡片容器 */
.aijobs-card {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--bg);
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
}
.aijobs-card-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 16px; border-bottom: 1px solid var(--border);
  background: var(--code-bg);
}
.aijobs-card-title {
  font-size: 11px; font-weight: 600; letter-spacing: .8px;
  color: var(--text-dim); text-transform: uppercase;
}
.aijobs-global-tag { font-size: 10px; color: var(--accent); }
.aijobs-card-refresh {
  border: none; background: none; color: var(--text-dim); font-size: 11px;
  cursor: pointer; padding: 2px 6px; border-radius: 4px;
}
.aijobs-card-refresh:hover { color: var(--accent); }

/* 顶部标题行 */
.aijobs-head { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 16px; }
.aijobs-head h2 { font-size: 18px; margin: 0 0 2px; }
.aijobs-status { font-size: 12px; color: var(--text-dim); margin: 0; }
.aijobs-head-actions { display: flex; gap: 8px; flex-shrink: 0; }
.aijobs-empty {
  padding: 60px 20px; text-align: center; color: var(--text-dim); font-size: 14px;
  border: 1px dashed var(--border); border-radius: 12px;
}
.aijobs-empty-icon { font-size: 32px; margin-bottom: 12px; }
.aijobs-empty-sub { font-size: 12px; color: var(--text-muted); }

/* 左栏表单 */
.aijob-form { padding: 16px; display: flex; flex-direction: column; gap: 14px; }
.form-row label { display: block; font-size: 11px; color: var(--text-dim); margin-bottom: 5px; font-weight: 600; letter-spacing: .3px; }
.form-input {
  width: 100%; box-sizing: border-box; border: 1px solid var(--border); border-radius: 8px;
  padding: 8px 10px; font-size: 13px; font-family: inherit; background: var(--bg); color: var(--text);
}
.form-input:focus { outline: none; border-color: var(--accent); }
.form-input.mono { font-family: var(--mono, monospace); }
textarea.form-input { resize: vertical; line-height: 1.6; }
.cron-presets { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
.preset-btn {
  border: 1px solid var(--border); background: none; border-radius: 12px;
  font-size: 11px; color: var(--text-dim); padding: 3px 10px; cursor: pointer;
}
.preset-btn.active, .preset-btn:hover { color: var(--accent); border-color: var(--accent); }
.form-hint { font-size: 11px; color: var(--text-muted); margin: 6px 0 0; line-height: 1.5; }
.form-row-inline .switch-label { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--text); }

/* 执行记录 */
.aijobs-card-body { padding: 12px; }
.aijob-runs-empty {
  font-size: 12px; color: var(--text-dim); padding: 40px 0; text-align: center; line-height: 1.8;
}
.aijob-runs-empty span { font-size: 11px; color: var(--text-muted); }
.aijob-run { border: 1px solid var(--border); border-radius: 8px; margin-bottom: 6px; overflow: hidden; background: var(--bg); }
.aijob-run.is-err { border-color: rgba(220, 80, 80, .4); }
.aijob-run-head {
  display: flex; align-items: center; gap: 10px; width: 100%;
  border: none; background: none; padding: 8px 12px; cursor: pointer;
  font-size: 12px; color: var(--text);
}
.aijob-run-head:hover { background: var(--code-bg); }
.aijob-run-status { width: 16px; height: 16px; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 10px; color: #fff; flex-shrink: 0; }
.aijob-run-status.ok { background: #2e7d32; }
.aijob-run-status.err { background: #cf222e; }
.aijob-run-time { font-family: var(--mono, monospace); flex: 1; text-align: left; }
.aijob-run-toggle { color: var(--text-dim); font-size: 11px; flex-shrink: 0; }
.aijob-run-output {
  margin: 0; padding: 10px 12px; border-top: 1px solid var(--border);
  font-family: var(--mono, monospace); font-size: 12px; line-height: 1.6;
  max-height: 260px; overflow: auto; white-space: pre-wrap; word-break: break-word;
  background: var(--code-bg);
}
.btn-danger-ghost { color: #a04040; }
.target-inline { display: flex; gap: 8px; }
.target-inline .form-input { min-width: 0; }
.aijob-run-type {
  font-size: 10px; color: var(--text-muted); border: 1px solid var(--border);
  border-radius: 8px; padding: 0 6px; flex-shrink: 0;
}
.aijob-run-detail { border-top: 1px solid var(--border); }
.aijob-run-detail .aijob-run-output { border-top: none; }
.aijob-run-error { padding: 8px 12px; font-size: 12px; color: #cf222e; background: #fff5f5; border-bottom: 1px solid var(--border); white-space: pre-wrap; word-break: break-word; }
.aijob-run-steps { list-style: none; margin: 0; padding: 8px 12px; border-bottom: 1px solid var(--border); display: flex; flex-direction: column; gap: 4px; }
.aijob-run-steps li { display: block; font-size: 12px; }
.aijob-run-steps .s-head { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
.aijob-run-steps .s-id { font-weight: 600; }
.aijob-run-steps .s-status { color: #2e7d32; }
.aijob-run-steps li.is-fail .s-status { color: #cf222e; }
.aijob-run-steps .s-uses, .aijob-run-steps .s-dur { color: var(--text-muted, #888); }
.aijob-run-steps .s-err { color: #cf222e; word-break: break-word; }
.aijob-run-steps .s-detail { margin-top: 4px; }
.aijob-run-steps .s-detail summary { cursor: pointer; color: var(--text-muted, #888); }
.aijob-run-steps .s-detail pre { margin: 4px 0 8px; padding: 8px; background: var(--bg-soft, #f6f8fa); border-radius: 4px; white-space: pre-wrap; word-break: break-word; max-height: 320px; overflow: auto; }
.aijob-run-link {
  display: inline-block; padding: 6px 12px 10px; font-size: 11px; color: var(--accent);
  text-decoration: none; border: none; background: none; cursor: pointer;
}
.aijob-run-link:hover { text-decoration: underline; }

/* 流程图 + Inspector：默认上下堆叠，只有足够宽的主区域才并排。
   侧栏 + activity bar 会吃掉 ~290px，视口宽度不能代表这里可用宽度。 */
.pipe-main { display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; }
@media (min-width: 1700px) { .pipe-main { grid-template-columns: minmax(0, 1fr) 280px; } }
.pipe-board {
  border: 1px solid var(--border); border-radius: 12px; background: var(--bg);
  display: flex; flex-direction: column; min-width: 0; height: 320px; overflow: hidden;
}
.pipe-board-head {
  display: flex; align-items: center; gap: 10px; padding: 10px 14px;
  border-bottom: 1px solid var(--border); background: var(--code-bg);
}
.pipe-board-tools { margin-left: auto; display: flex; gap: 6px; align-items: center; }
.pipe-live {
  color: var(--text-dim); font-size: 11px; white-space: nowrap;
}
.pipe-live::before {
  content: ''; display: inline-block; width: 7px; height: 7px; border-radius: 50%;
  background: var(--accent); margin-right: 5px; vertical-align: 1px;
  animation: pipe-live-blink 1.2s ease-in-out infinite;
}
@keyframes pipe-live-blink { 50% { opacity: 0.25; } }
.pipe-tool {
  border: 1px solid var(--border); background: var(--bg); color: var(--text-dim);
  border-radius: 8px; padding: 3px 10px; font-size: 11px; cursor: pointer; white-space: nowrap;
}
.pipe-tool:hover, .pipe-tool-on { color: var(--accent); border-color: var(--accent); }
.pipe-json { padding: 14px; overflow: auto; }

.pipe-inspector {
  border: 1px solid var(--border); border-radius: 12px; background: var(--bg);
  padding: 12px 14px; overflow: auto; max-height: 340px;
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
  font-size: 10px; white-space: pre-wrap; word-break: break-all; max-height: 160px; overflow: auto;
}
.pi-empty { font-size: 12px; color: var(--text-dim); line-height: 1.8; margin-top: 16px; }
.pi-legend { display: flex; flex-direction: column; gap: 6px; margin-top: 16px; font-size: 11px; }
.pi-legend span { display: flex; align-items: center; gap: 6px; }
.lg { width: 4px; height: 12px; border-radius: 2px; display: inline-block; }
.lg.tool { background: #3b82f6; }
.lg.api { background: #8b5cf6; }
.lg.llm { background: #10a37f; }
</style>
