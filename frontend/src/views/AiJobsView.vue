<script setup lang="ts">
// AI 定时任务主区域：选中任务（route query id）的详情编辑 + 执行历史。
// 任务列表在侧栏（AiJobsSidebar），选中经 route query ?id= 同步。
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { listAIJobs, listAIJobRuns, createAIJob, updateAIJob, deleteAIJob, type AIJob, type AIJobRun } from '../api/aiJobs'
import {
  runPipeline, getPipelineRun, streamPipelineRun,
  type PipelineStepResult, type PipelineGraphStep,
} from '../api/pipelines'
import { showToast } from '../lib/common'
import PipelineGraph from '../components/pipeline/PipelineGraph.vue'

const route = useRoute()
const router = useRouter()

const jobs = ref<AIJob[]>([])
const runs = ref<AIJobRun[]>([])
const runsLoading = ref(false)
const saving = ref(false)

type TargetType = 'ai' | 'tool' | 'interface' | 'pipeline'

const form = ref({
  name: '',
  cronExpr: '',
  prompt: '',
  enabled: true,
  targetType: 'ai' as TargetType,
  tool: '',
  toolParams: '',
  method: 'GET',
  path: '',
  query: '',
  body: '',
  definition: '',
  allowWrite: '',
  retry: 0,
})
const runsOpen = ref(new Set<number>())
// 运行中任务的逐步快照：job_runs 先落 running，前端用 pipelineRunId 轮询 pipeline_runs.steps
const runLiveSteps = ref<Record<number, PipelineStepResult[] | undefined>>({})
const runPollTimers: Record<number, number> = {}

const DEFAULT_DEF = JSON.stringify({
  name: '索引巡检',
  allowWrite: [],
  steps: [
    { id: 'check', uses: 'tool/reindex_status' },
    { id: 'summary', uses: 'llm', with: { prompt: '根据 {{ steps.check.output }} 用一句话总结索引状态' } },
  ],
}, null, 2)

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

onBeforeUnmount(() => {
  stopRunStream()
  stopAllRunPolls()
})

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
  let def: unknown
  try {
    def = JSON.parse(form.value.definition || DEFAULT_DEF)
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
          showToast('实时连接中断：' + (e?.message || ''))
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
    showToast('触发失败：' + (e?.msg || e?.message || ''))
  } finally {
    running.value = false
  }
}

function validateForm(): string {
  if (!form.value.name.trim()) return '任务名称必填'
  if (!form.value.cronExpr.trim()) return 'cron 表达式必填'
  switch (form.value.targetType) {
    case 'ai':
      if (!form.value.prompt.trim()) return 'AI 提示词必填'
      break
    case 'tool':
      if (!form.value.tool.trim()) return '工具名必填'
      break
    case 'interface':
      if (!form.value.path.trim()) return '接口路径必填'
      break
    case 'pipeline':
      if (!form.value.definition.trim()) return '编排 Definition 必填'
      break
  }
  return ''
}

function parseJSONField(raw: string, label: string): unknown {
  const s = raw.trim()
  if (!s) return undefined
  try {
    return JSON.parse(s)
  } catch {
    throw new Error(label + ' 不是合法 JSON')
  }
}

function buildBody(): any {
  const allowWrite = form.value.allowWrite.split(',').map(x => x.trim()).filter(Boolean)
  const common: any = { name: form.value.name.trim(), cronExpr: form.value.cronExpr.trim(), enabled: form.value.enabled, allowWrite, retry: form.value.retry }
  if (form.value.targetType === 'ai') {
    const prompt = form.value.prompt.trim()
    return { ...common, prompt, target: { type: 'ai', prompt } }
  }
  switch (form.value.targetType) {
    case 'tool':
      return { ...common, target: { type: 'tool', tool: form.value.tool.trim(), params: parseJSONField(form.value.toolParams, '工具参数') } }
    case 'interface':
      return {
        ...common,
        target: {
          type: 'interface',
          method: form.value.method,
          path: form.value.path.trim(),
          query: parseJSONField(form.value.query, 'query'),
          body: parseJSONField(form.value.body, 'body'),
        },
      }
    case 'pipeline':
      return { ...common, target: { type: 'pipeline', definition: parseJSONField(form.value.definition, 'Definition') } }
  }
}

function fillForm(j: AIJob) {
  const t = j.target
  const storedType = (j.targetType || t?.type || 'ai') as TargetType
  const def = t?.definition as any
  let formType: TargetType = storedType
  let prompt = t?.prompt || j.prompt || ''
  let tool = t?.tool || ''
  let toolParams = t?.params ? JSON.stringify(t.params, null, 2) : ''
  let method = t?.method || 'GET'
  let path = t?.path || ''
  let query = t?.query ? JSON.stringify(t.query, null, 2) : ''
  let body = t?.body ? JSON.stringify(t.body, null, 2) : ''
  const definition = t?.definition ? JSON.stringify(t.definition, null, 2) : DEFAULT_DEF

  // 新建/更新统一存 pipeline；单步 Definition 回显成原来的快捷表单，
  // 编辑体验不变（ai 的 render 元数据随 Definition 保留）。
  if (storedType === 'pipeline' && Array.isArray(def?.steps) && def.steps.length === 1) {
    const step = def.steps[0] as any
    const w = step.with || {}
    if (step.uses === 'llm' && typeof w.prompt === 'string') {
      formType = 'ai'
      prompt = w.prompt
    } else if (typeof step.uses === 'string' && step.uses.startsWith('tool/')) {
      formType = 'tool'
      tool = step.uses.slice(5)
      toolParams = Object.keys(w).length ? JSON.stringify(w, null, 2) : ''
    } else if (typeof step.uses === 'string' && step.uses.startsWith('api/')) {
      formType = 'interface'
      path = step.uses.slice(4)
      method = w.method || 'GET'
      query = w.query ? JSON.stringify(w.query, null, 2) : ''
      body = w.body ? JSON.stringify(w.body, null, 2) : ''
    }
  }

  form.value = {
    name: j.name,
    cronExpr: j.cronExpr,
    prompt,
    enabled: j.enabled,
    targetType: formType,
    tool,
    toolParams,
    method,
    path,
    query,
    body,
    definition,
    allowWrite: (j.allowWrite || []).join(', '),
    retry: j.retry || 0,
  }
}

const CRON_PRESETS: { label: string; expr: string }[] = [
  { label: '每天 18:00', expr: '0 18 * * *' },
  { label: '每天 09:00', expr: '0 9 * * *' },
  { label: '每周一 09:00', expr: '0 9 * * 1' },
  { label: '每周五 18:00', expr: '0 18 * * 5' },
  { label: '每小时整点', expr: '0 * * * *' },
]

// 当前选中：'new' = 新建 | 数字 = 编辑 | null = 未选择
const selected = computed<number | 'new' | null>(() => {
  const q = route.query.id
  if (q === 'new') return 'new'
  const n = Number(q)
  return q && Number.isFinite(n) && n > 0 ? n : null
})

const selectedJob = computed(() => (typeof selected.value === 'number' ? jobs.value.find(j => j.id === selected.value) : undefined))

async function loadJobs() {
  try {
    const data = await listAIJobs()
    jobs.value = data?.jobs || []
  } catch { /* 静默 */ }
}

async function loadRuns(id: number) {
  runsLoading.value = true
  try {
    const data = await listAIJobRuns(id)
    runs.value = data?.runs || []
  } catch {
    runs.value = []
  } finally {
    runsLoading.value = false
  }
}

// 选中变化：new → 空表单；数字 → 填表单 + 拉执行历史
watch(selected, async (sel) => {
  runs.value = []
  showJson.value = false
  activeRunId.value = null
  runSteps.value = undefined
  inspected.value = null
  if (sel === 'new') {
    form.value = {
      name: '', cronExpr: '0 18 * * *', prompt: '', enabled: true,
      targetType: 'ai', tool: '', toolParams: '', method: 'GET', path: '',
      query: '', body: '', definition: DEFAULT_DEF, allowWrite: '', retry: 0,
    }
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
    showToast('保存失败：' + (e?.msg || e?.message || ''))
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
    showToast('操作失败：' + (e?.msg || e?.message || ''))
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
    showToast('删除失败：' + (e?.msg || e?.message || ''))
  }
}

function parseRunSteps(raw?: string): PipelineStepResult[] {
  if (!raw) return []
  try {
    const v = JSON.parse(raw)
    return Array.isArray(v) ? (v as PipelineStepResult[]) : []
  } catch {
    return []
  }
}

function runStepList(r: AIJobRun): PipelineStepResult[] {
  if (r.status === 'running') {
    const live = runLiveSteps.value[r.id]
    if (live && live.length) return live
  }
  return parseRunSteps(r.stepsSummary)
}

function stopRunPoll(id: number) {
  const t = runPollTimers[id]
  if (t) {
    clearInterval(t)
    delete runPollTimers[id]
  }
}

function stopAllRunPolls() {
  for (const id of Object.keys(runPollTimers)) stopRunPoll(Number(id))
}

async function refreshLiveRun(r: AIJobRun) {
  if (!r.pipelineRunId) return
  try {
    const detail = await getPipelineRun(r.pipelineRunId)
    runLiveSteps.value = { ...runLiveSteps.value, [r.id]: detail.steps }
    if (detail.status !== 'running') {
      stopRunPoll(r.id)
      if (typeof selected.value === 'number') loadRuns(selected.value)
    }
  } catch { /* 轮询失败静默，下一轮再试 */ }
}

function startRunPoll(r: AIJobRun) {
  if (!r.pipelineRunId || runPollTimers[r.id]) return
  refreshLiveRun(r)
  runPollTimers[r.id] = window.setInterval(() => refreshLiveRun(r), 3000)
}

function toggleRun(i: number) {
  const s = new Set(runsOpen.value)
  const opening = !s.has(i)
  const r = runs.value[i]
  if (opening) s.add(i)
  else {
    s.delete(i)
    if (r) stopRunPoll(r.id)
  }
  runsOpen.value = s
  if (opening && r?.status === 'running') startRunPoll(r)
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
        <p v-if="selectedJob" class="aijobs-status">{{ statusText(selectedJob) }}</p>
      </div>
      <div class="aijobs-head-actions">
        <button v-if="form.targetType === 'pipeline'" class="btn" :disabled="running" @click="runNow">
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
            <label>任务类型</label>
            <select v-model="form.targetType" class="form-input" :disabled="selectedJob?.isGlobal">
              <option value="ai">AI 提示词</option>
              <option value="tool">单个工具</option>
              <option value="interface">站内接口</option>
              <option value="pipeline">编排流程</option>
            </select>
          </div>

          <template v-if="form.targetType === 'ai'">
            <div class="form-row">
              <label>执行内容（到点后 AI 收到的指令）</label>
              <textarea v-model="form.prompt" class="form-input" rows="8"
                placeholder="如：总结我今天完成的任务和进展，生成一份简短的工作日报，指出明天应优先处理的事项。"></textarea>
              <p class="form-hint">执行时 AI 会自动附带你的任务列表，结果作为邮件正文发送到你的邮箱。</p>
            </div>
          </template>

          <template v-else-if="form.targetType === 'tool'">
            <div class="form-row">
              <label>工具名</label>
              <input v-model="form.tool" class="form-input mono" placeholder="如：list_tasks" />
            </div>
            <div class="form-row">
              <label>工具参数（JSON，可选）</label>
              <textarea v-model="form.toolParams" class="form-input mono" rows="4" placeholder='{"limit": 20}'></textarea>
            </div>
          </template>

          <template v-else-if="form.targetType === 'interface'">
            <div class="form-row">
              <label>请求方法与路径（站内 /api/*）</label>
              <div class="target-inline">
                <select v-model="form.method" class="form-input" style="max-width: 110px">
                  <option>GET</option><option>POST</option><option>PUT</option><option>PATCH</option><option>DELETE</option>
                </select>
                <input v-model="form.path" class="form-input mono" placeholder="tasks/open" />
              </div>
            </div>
            <div class="form-row">
              <label>query（JSON，可选）</label>
              <textarea v-model="form.query" class="form-input mono" rows="3" placeholder='{"date": "2026-11-25"}'></textarea>
            </div>
            <div class="form-row">
              <label>body（JSON，可选）</label>
              <textarea v-model="form.body" class="form-input mono" rows="3" placeholder='{"status": "done"}'></textarea>
            </div>
          </template>

          <template v-else>
            <div class="form-row">
              <label>Definition（编排流程，由 AI 生成或手动编辑）</label>
              <p class="form-hint">流程内联在本任务里；下方流程图可查看节点依赖与运行状态，也可切换回 JSON 编辑。</p>
            </div>
          </template>

          <div class="form-row">
            <label v-if="form.targetType === 'ai'">可用工具（逗号分隔，填写即预授权）</label>
            <label v-else>静态预授权（逗号分隔，写类工具或非 GET 接口）</label>
            <input v-model="form.allowWrite" class="form-input mono" :placeholder="form.targetType === 'ai' ? 'list_tasks, notify_user' : 'notify_user, api/tasks'" />
            <p v-if="form.targetType === 'ai'" class="form-hint">这里是模型唯一可用的工具清单，填写即视为提前审批通过（含写类工具，无需重复填写）；留空则只做一次文本生成，不调用任何工具。</p>
            <p v-else class="form-hint">定时执行无人审批，写操作必须在创建时显式列出；未列出的步骤会失败。</p>
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
      <div v-if="form.targetType === 'pipeline'" class="pipe-main">
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
