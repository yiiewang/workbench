<script setup lang="ts">
// AI 定时任务主区域：选中任务（route query id）的详情编辑 + 执行历史。
// 任务列表在侧栏（AiJobsSidebar），选中经 route query ?id= 同步。
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { listAIJobs, listAIJobRuns, createAIJob, updateAIJob, deleteAIJob, type AIJob, type AIJobRun } from '../api/aiJobs'
import { showToast } from '../lib/common'

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
  templateId: '',
  definition: '',
  allowWrite: '',
  retry: 0,
})
const runsOpen = ref(new Set<number>())

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
      if (!form.value.templateId.trim() && !form.value.definition.trim()) return '编排模板 ID 或 Definition 必填'
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
    case 'pipeline': {
      const templateId = Number(form.value.templateId)
      if (Number.isFinite(templateId) && templateId > 0) {
        return { ...common, target: { type: 'pipeline', templateId } }
      }
      return { ...common, target: { type: 'pipeline', definition: parseJSONField(form.value.definition, 'Definition') } }
    }
  }
}

function fillForm(j: AIJob) {
  const t = j.target
  form.value = {
    name: j.name,
    cronExpr: j.cronExpr,
    prompt: t?.prompt || j.prompt || '',
    enabled: j.enabled,
    targetType: (j.targetType || t?.type || 'ai') as TargetType,
    tool: t?.tool || '',
    toolParams: t?.params ? JSON.stringify(t.params, null, 2) : '',
    method: t?.method || 'GET',
    path: t?.path || '',
    query: t?.query ? JSON.stringify(t.query, null, 2) : '',
    body: t?.body ? JSON.stringify(t.body, null, 2) : '',
    templateId: t?.templateId ? String(t.templateId) : '',
    definition: t?.definition ? JSON.stringify(t.definition, null, 2) : '',
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
  if (sel === 'new') {
    form.value = {
      name: '', cronExpr: '0 18 * * *', prompt: '', enabled: true,
      targetType: 'ai', tool: '', toolParams: '', method: 'GET', path: '',
      query: '', body: '', templateId: '', definition: '', allowWrite: '', retry: 0,
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

function toggleRun(i: number) {
  const s = new Set(runsOpen.value)
  if (s.has(i)) s.delete(i)
  else s.add(i)
  runsOpen.value = s
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
        <button
          v-if="selected !== 'new' && !selectedJob?.isGlobal"
          class="btn" :class="form.enabled ? '' : 'btn-primary'"
          @click="toggle"
        >{{ form.enabled ? '停用' : '启用' }}</button>
        <button v-if="selected !== 'new' && !selectedJob?.isGlobal" class="btn btn-danger-ghost" @click="remove">删除</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </div>

    <div class="aijobs-grid">
      <!-- 左：任务详情卡片 -->
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
              <label>编排模板 ID（优先；留空则用下方 Definition）</label>
              <input v-model="form.templateId" class="form-input mono" placeholder="如：3" />
            </div>
            <div class="form-row">
              <label>Definition（JSON，可选）</label>
              <textarea v-model="form.definition" class="form-input mono" rows="8"
                placeholder='{"name":"收盘总结","steps":[{"id":"run","uses":"llm"}]}'></textarea>
            </div>
          </template>

          <div class="form-row">
            <label>静态预授权（逗号分隔，写类工具或非 GET 接口）</label>
            <input v-model="form.allowWrite" class="form-input mono" placeholder="notify_user, api/tasks" />
            <p class="form-hint">定时执行无人审批，写操作必须在创建时显式列出；未列出的步骤会失败。</p>
          </div>
          <div class="form-row">
            <label>失败重试次数（0-3，仅无写步骤时生效）</label>
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

      <!-- 右：执行记录卡片 -->
      <section v-if="typeof selected === 'number'" class="aijobs-card">
        <div class="aijobs-card-head">
          <span class="aijobs-card-title">执行记录</span>
          <button class="aijobs-card-refresh" @click="loadRuns(selected)">{{ runsLoading ? '刷新中…' : '刷新' }}</button>
        </div>
        <div class="aijobs-card-body">
          <div v-if="!runs.length && !runsLoading" class="aijob-runs-empty">还没有执行记录<br /><span>到点触发后这里会显示每次的运行结果</span></div>
          <div v-for="(r, i) in runs" :key="r.id" class="aijob-run" :class="{ 'is-err': r.status !== 'ok' }">
            <button class="aijob-run-head" @click="toggleRun(i)">
              <span class="aijob-run-status" :class="r.status === 'ok' ? 'ok' : 'err'">
                {{ r.status === 'ok' ? '✓' : '✕' }}
              </span>
              <span class="aijob-run-time">{{ r.ranAt }}</span>
              <span v-if="r.targetType" class="aijob-run-type">{{ r.targetType }}</span>
              <span class="aijob-run-toggle">{{ runsOpen.has(i) ? '收起' : '查看输出' }}</span>
            </button>
            <div v-show="runsOpen.has(i)" class="aijob-run-detail">
              <pre class="aijob-run-output">{{ r.output || '（无输出）' }}</pre>
              <a v-if="r.pipelineRunId" class="aijob-run-link" :href="`/pipelines?run=${r.pipelineRunId}`" target="_blank" rel="noopener">查看编排运行详情</a>
            </div>
          </div>
        </div>
      </section>
    </div>
  </template>
</div>
</template>

<style scoped>
/* 铺满可用宽度：不设 max-width，两栏按 1.1:1 自动分配（宽屏左右不留白） */
.aijobs-page { padding: 24px 28px; height: 100%; box-sizing: border-box; display: flex; flex-direction: column; min-height: 0; }
/* 左右两栏：左详情卡片，右执行记录卡片（窄屏回落单列）；
   grid 占满剩余高度，两卡片等高拉伸 */
.aijobs-grid {
  display: grid;
  grid-template-columns: minmax(340px, 1.1fr) minmax(320px, 1fr);
  gap: 20px;
  align-items: stretch;
  flex: 1;
  min-height: 0;
}
@media (max-width: 1000px) {
  .aijobs-grid { grid-template-columns: 1fr; }
}

/* 卡片容器：两栏统一视觉基座 */
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

/* 右栏执行记录：占满卡片剩余高度（由 grid 拉伸决定） */
.aijobs-card-body { padding: 12px; overflow-y: auto; flex: 1; min-height: 0; }
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
.aijob-run-link { display: inline-block; padding: 6px 12px 10px; font-size: 11px; color: var(--accent); text-decoration: none; }
.aijob-run-link:hover { text-decoration: underline; }
</style>
