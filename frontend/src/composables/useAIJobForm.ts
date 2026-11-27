// 定时任务表单：Definition 编辑 + 校验 + 提交体组装（AiJobsView 专用）。
// 任务统一存 pipeline Definition（后端 jobs.target 只接受 pipeline 类型），
// 因此表单只有 Definition 一个编辑面，不再保留 ai/tool/interface 快捷表单的残留状态。
import { ref } from 'vue'
import type { AIJob, AIJobBody } from '../api/aiJobs'
import { prettyJson } from '../lib/common'

/** 新建任务的默认 Definition（索引巡检示例） */
export const DEFAULT_DEF = JSON.stringify({
  name: '索引巡检',
  allowWrite: [],
  steps: [
    { id: 'check', uses: 'tool/reindex_status' },
    { id: 'summary', uses: 'llm', with: { prompt: '根据 {{ steps.check.output }} 用一句话总结索引状态' } },
  ],
}, null, 2)

export const CRON_PRESETS: { label: string; expr: string }[] = [
  { label: '每天 18:00', expr: '0 18 * * *' },
  { label: '每天 09:00', expr: '0 9 * * *' },
  { label: '每周一 09:00', expr: '0 9 * * 1' },
  { label: '每周五 18:00', expr: '0 18 * * 5' },
  { label: '每小时整点', expr: '0 * * * *' },
]

export interface AIJobForm {
  name: string
  cronExpr: string
  enabled: boolean
  definition: string
  allowWrite: string
  retry: number
}

/** 空表单：新建场景（cron 默认每天 18:00） */
export function emptyAIJobForm(): AIJobForm {
  return {
    name: '',
    cronExpr: '0 18 * * *',
    enabled: true,
    definition: DEFAULT_DEF,
    allowWrite: '',
    retry: 0,
  }
}

export function useAIJobForm() {
  const form = ref<AIJobForm>(emptyAIJobForm())

  /** 任务 → 表单（Definition 美化后展示；缺省时用示例 Definition） */
  function fillForm(j: AIJob) {
    const t = j.target as { definition?: unknown } | undefined
    form.value = {
      name: j.name,
      cronExpr: j.cronExpr,
      enabled: j.enabled,
      definition: t?.definition ? prettyJson(t.definition) : DEFAULT_DEF,
      allowWrite: (j.allowWrite || []).join(', '),
      retry: j.retry || 0,
    }
  }

  function parseDefinition(raw: string): unknown {
    try {
      return JSON.parse(raw)
    } catch {
      throw new Error('Definition 不是合法 JSON')
    }
  }

  /** 保存前校验：返回错误文案，空串 = 通过 */
  function validateForm(): string {
    if (!form.value.name.trim()) return '任务名称必填'
    if (!form.value.cronExpr.trim()) return 'cron 表达式必填'
    if (!form.value.definition.trim()) return '编排 Definition 必填'
    try {
      parseDefinition(form.value.definition)
    } catch (e: any) {
      return e.message
    }
    return ''
  }

  /** 表单 → POST/PUT /api/jobs 请求体（target 统一 pipeline） */
  function buildBody(): AIJobBody {
    return {
      name: form.value.name.trim(),
      cronExpr: form.value.cronExpr.trim(),
      enabled: form.value.enabled,
      allowWrite: form.value.allowWrite.split(',').map(x => x.trim()).filter(Boolean),
      retry: form.value.retry,
      target: {
        type: 'pipeline',
        definition: parseDefinition(form.value.definition) as Record<string, unknown>,
      },
    }
  }

  return { form, emptyAIJobForm, fillForm, validateForm, buildBody }
}
