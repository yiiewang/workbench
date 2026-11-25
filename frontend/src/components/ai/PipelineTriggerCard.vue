<script setup lang="ts">
// 模板的定时触发配置：规则属于调度领域，通过 /api/schedule/triggers 独立管理，
// 模板本身只存 Definition。新建模板（templateId=null）时不可配置。
// 状态自管理，父页面不需要知道触发规则的任何字段。
import { ref, watch } from 'vue'
import {
  listScheduleTriggers, createScheduleTrigger, updateScheduleTrigger, deleteScheduleTrigger,
  type CronTrigger,
} from '../../api/scheduleTriggers'
import { showToast } from '../../lib/common'

const props = defineProps<{ templateId: number | null }>()

const TARGET_TYPE = 'pipeline_template'
const CRON_PRESETS = [
  { label: '每天 09:00', expr: '0 9 * * *' },
  { label: '每天 18:00', expr: '0 18 * * *' },
  { label: '每周一 09:00', expr: '0 9 * * 1' },
  { label: '每小时整点', expr: '0 * * * *' },
]

const trigger = ref<CronTrigger | null>(null)
const editing = ref(false)
const saving = ref(false)
const form = ref({ cronExpr: '0 9 * * *', enabled: true })

async function load() {
  trigger.value = null
  editing.value = false
  if (props.templateId === null) return
  try {
    const data = await listScheduleTriggers({ targetType: TARGET_TYPE, targetId: props.templateId })
    // 第一版一个模板管理一条规则（取最近创建的一条）
    trigger.value = data?.triggers?.[0] || null
  } catch { /* 静默 */ }
}

watch(() => props.templateId, load, { immediate: true })

function startEdit() {
  form.value = {
    cronExpr: trigger.value?.cronExpr || '0 9 * * *',
    enabled: trigger.value?.enabled ?? true,
  }
  editing.value = true
}

async function save() {
  if (!form.value.cronExpr.trim()) {
    showToast('cron 表达式必填')
    return
  }
  if (props.templateId === null) {
    showToast('请先保存模板')
    return
  }
  saving.value = true
  try {
    if (trigger.value) {
      await updateScheduleTrigger(trigger.value.id, {
        cronExpr: form.value.cronExpr.trim(), enabled: form.value.enabled,
      })
      showToast('触发规则已更新')
    } else {
      await createScheduleTrigger({
        targetType: TARGET_TYPE, targetId: props.templateId,
        cronExpr: form.value.cronExpr.trim(), enabled: form.value.enabled,
      })
      showToast('触发规则已创建，下一分钟生效')
    }
    editing.value = false
    await load()
  } catch (e: any) {
    showToast('保存失败：' + (e?.msg || e?.message || ''))
  } finally {
    saving.value = false
  }
}

async function toggleEnabled() {
  if (!trigger.value) return
  try {
    await updateScheduleTrigger(trigger.value.id, {
      cronExpr: trigger.value.cronExpr, enabled: !trigger.value.enabled,
    })
    await load()
  } catch (e: any) {
    showToast('操作失败：' + (e?.msg || e?.message || ''))
  }
}

async function remove() {
  if (!trigger.value || !confirm('删除这条定时触发规则？')) return
  try {
    await deleteScheduleTrigger(trigger.value.id)
    await load()
  } catch (e: any) {
    showToast('删除失败：' + (e?.msg || e?.message || ''))
  }
}

function statusText(t: CronTrigger): string {
  if (!t.lastRunAt) return '尚未执行'
  return `最近 ${t.lastRunAt} ${t.lastStatus || ''}`
}
</script>

<template>
<section class="pt-card">
  <div class="pt-head">
    <span class="pt-title">定时触发</span>
    <template v-if="trigger">
      <span class="pt-state" :class="{ off: !trigger.enabled }">{{ trigger.enabled ? '已启用' : '已停用' }}</span>
      <code class="pt-cron">{{ trigger.cronExpr }}</code>
      <span class="pt-last">{{ statusText(trigger) }}</span>
    </template>
    <span v-else class="pt-none">未配置</span>
    <div class="pt-actions">
      <button v-if="trigger" class="pipe-tool" @click="toggleEnabled">{{ trigger.enabled ? '停用' : '启用' }}</button>
      <button class="pipe-tool" :disabled="templateId === null" @click="editing ? (editing = false) : startEdit()">
        {{ editing ? '取消' : (trigger ? '编辑' : '添加定时触发') }}
      </button>
      <button v-if="trigger" class="pipe-tool danger" @click="remove">删除</button>
    </div>
  </div>

  <div v-if="templateId === null" class="pt-hint">保存模板后才能配置定时触发；在此之前模板仍可手动执行。</div>
  <div v-else-if="editing" class="pt-form">
    <label>执行频率（cron：分 时 日 月 周）</label>
    <input v-model="form.cronExpr" class="form-input mono" placeholder="0 9 * * *" />
    <div class="cron-presets">
      <button v-for="p in CRON_PRESETS" :key="p.expr" type="button"
        class="preset-btn" :class="{ active: form.cronExpr === p.expr }"
        @click="form.cronExpr = p.expr">{{ p.label }}</button>
    </div>
    <label class="switch-label">
      <input type="checkbox" v-model="form.enabled" />
      启用（cron 命中的下一分钟生效）
    </label>
    <div class="pt-form-actions">
      <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存规则' }}</button>
    </div>
  </div>
  <div v-else-if="!trigger" class="pt-hint">
    模板可手动执行，也可由对话中的 AI 调用；需要定时运行就添加一条触发规则。
  </div>
</section>
</template>

<style scoped>
.pt-card { border: 1px solid var(--border); border-radius: 12px; background: var(--bg); padding: 10px 14px; }
.pt-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.pt-title { font-size: 11px; font-weight: 600; letter-spacing: .8px; color: var(--text-dim); text-transform: uppercase; }
.pt-state { font-size: 10px; color: #2e7d32; border: 1px solid #2e7d32; border-radius: 8px; padding: 1px 7px; }
.pt-state.off { color: var(--text-dim); border-color: var(--border); }
.pt-cron { font-size: 11px; color: var(--text); font-family: var(--mono, monospace); }
.pt-last, .pt-none, .pt-hint { font-size: 11px; color: var(--text-dim); }
.pt-actions { margin-left: auto; display: flex; gap: 6px; }
.pipe-tool {
  border: 1px solid var(--border); background: var(--bg); color: var(--text-dim);
  border-radius: 8px; padding: 3px 10px; font-size: 11px; cursor: pointer;
}
.pipe-tool:hover { color: var(--accent); border-color: var(--accent); }
.pipe-tool:disabled { opacity: .45; cursor: not-allowed; }
.pipe-tool.danger:hover { color: #cf222e; border-color: #cf222e; }
.pt-hint { margin-top: 8px; line-height: 1.7; }
.pt-form { margin-top: 10px; display: flex; flex-direction: column; gap: 8px; }
.pt-form > label { display: block; font-size: 11px; color: var(--text-dim); font-weight: 600; }
.form-input {
  width: 100%; box-sizing: border-box; border: 1px solid var(--border); border-radius: 8px;
  padding: 8px 10px; font-size: 13px; font-family: inherit; background: var(--bg); color: var(--text);
}
.form-input:focus { outline: none; border-color: var(--accent); }
.form-input.mono { font-family: var(--mono, monospace); font-size: 12px; }
.cron-presets { display: flex; flex-wrap: wrap; gap: 6px; }
.preset-btn {
  border: 1px solid var(--border); background: none; border-radius: 12px;
  font-size: 11px; color: var(--text-dim); padding: 3px 10px; cursor: pointer;
}
.preset-btn.active, .preset-btn:hover { color: var(--accent); border-color: var(--accent); }
.switch-label { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text); }
.pt-form-actions { display: flex; justify-content: flex-end; }
</style>
