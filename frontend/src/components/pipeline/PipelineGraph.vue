<script setup lang="ts">
// 编排流程图：模板详情的主体视图。SVG 连线 + 卡片节点，无第三方图库。
// 分层规则复刻 internal/pipeline 的 needs 语义：
//   缺省 = 依赖数组前一步（顺序）、[] = 无依赖可并行、id 列表 = 菱形 DAG。
// steps 传入时叠加运行状态；点击节点 emit select，详情由父级 Inspector 展示。
import { computed, ref, watch } from 'vue'
import type { PipelineGraphStep, PipelineStepResult } from '../../api/pipelines'

interface LayoutNode {
  step: PipelineGraphStep
  x: number
  y: number
  status: string
  result?: PipelineStepResult
  kind: string
}

interface Edge {
  d: string
}

interface Layout {
  nodes: LayoutNode[]
  edges: Edge[]
  width: number
  height: number
  error: string
}

const props = defineProps<{
  definition?: unknown
  steps?: PipelineStepResult[]
}>()
const emit = defineEmits<{
  select: [payload: { step: PipelineGraphStep; result?: PipelineStepResult } | null]
}>()

const NODE_W = 208
const NODE_H = 76
const GAP_X = 96
const GAP_Y = 26
const PAD = 28
const arrowId = 'pg-arrow-' + Math.random().toString(36).slice(2, 9)
const selectedId = ref('')

function empty(error: string): Layout {
  return { nodes: [], edges: [], width: 0, height: 0, error }
}

const parsed = computed<{ error: string; steps: PipelineGraphStep[] }>(() => {
  let obj: any = props.definition
  if (typeof obj === 'string') {
    try {
      obj = JSON.parse(obj)
    } catch {
      return { error: 'Definition 不是合法 JSON，无法渲染流程图', steps: [] }
    }
  }
  if (!obj || !Array.isArray(obj.steps) || !obj.steps.length) {
    return { error: '没有可渲染的步骤', steps: [] }
  }
  const steps: PipelineGraphStep[] = obj.steps.map((s: any) => ({
    id: String(s?.id || ''),
    uses: String(s?.uses || ''),
    // null / 缺省都归一到 undefined（=依赖前一步），只有数组是显式声明
    needs: Array.isArray(s?.needs) ? s.needs.map(String) : undefined,
    when: String(s?.when || ''),
    retry: Number(s?.retry) || 0,
    with: s?.with,
  }))
  return { error: '', steps }
})

const statusMap = computed(() => {
  const m = new Map<string, PipelineStepResult>()
  for (const s of props.steps || []) m.set(s.id, s)
  return m
})

function kindOf(uses: string): string {
  if (uses.startsWith('tool/')) return 'tool'
  if (uses.startsWith('api/')) return 'api'
  if (uses === 'llm') return 'llm'
  return 'other'
}

const layout = computed<Layout>(() => {
  const steps = parsed.value.steps
  if (parsed.value.error) return empty(parsed.value.error)
  if (!steps.length) return empty('没有可渲染的步骤')

  const index = new Map<string, number>()
  steps.forEach((s, i) => index.set(s.id, i))
  const dependencies = (i: number): string[] => {
    const n = steps[i].needs
    if (n !== undefined) return n
    return i > 0 ? [steps[i - 1].id] : []
  }
  const indeg = new Array(steps.length).fill(0)
  const children: number[][] = steps.map(() => [])
  for (let i = 0; i < steps.length; i++) {
    for (const dep of dependencies(i)) {
      const j = index.get(dep)
      if (j === undefined) return empty(`步骤 ${steps[i].id} 依赖了不存在的 ${dep}`)
      if (j === i) return empty(`步骤 ${steps[i].id} 不能依赖自己`)
      indeg[i]++
      children[j].push(i)
    }
  }

  // Kahn 分层：同层可并行，层内按 Definition 顺序稳定排列
  const layers: number[][] = []
  let current = steps.map((_, i) => i).filter(i => indeg[i] === 0)
  let processed = 0
  while (current.length) {
    layers.push(current)
    processed += current.length
    const next: number[] = []
    for (const i of current) {
      for (const c of children[i]) {
        indeg[c]--
        if (indeg[c] === 0) next.push(c)
      }
    }
    next.sort((a, b) => a - b)
    current = next
  }
  if (processed !== steps.length) return empty('依赖存在环，无法渲染流程图')

  const maxRows = Math.max(...layers.map(l => l.length))
  const height = Math.max(PAD * 2 + NODE_H, maxRows * (NODE_H + GAP_Y) + PAD * 2)
  const width = layers.length * NODE_W + (layers.length - 1) * GAP_X + PAD * 2

  const nodes: LayoutNode[] = []
  layers.forEach((layer, li) => {
    const total = layer.length * NODE_H + (layer.length - 1) * GAP_Y
    const top = Math.max(PAD, (height - total) / 2)
    layer.forEach((idx, k) => {
      const result = statusMap.value.get(steps[idx].id)
      nodes.push({
        step: steps[idx],
        x: PAD + li * (NODE_W + GAP_X),
        y: top + k * (NODE_H + GAP_Y),
        status: result?.status || '',
        result,
        kind: kindOf(steps[idx].uses),
      })
    })
  })

  const byId = new Map(nodes.map(n => [n.step.id, n]))
  const edges: Edge[] = []
  steps.forEach((s, i) => {
    const target = byId.get(s.id)
    if (!target) return
    for (const dep of dependencies(i)) {
      const from = byId.get(dep)
      if (!from) continue
      const x1 = from.x + NODE_W
      const y1 = from.y + NODE_H / 2
      const x2 = target.x
      const y2 = target.y + NODE_H / 2
      const bend = Math.max(32, (x2 - x1) / 2)
      edges.push({ d: `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${x2 - bend} ${y2}, ${x2} ${y2}` })
    }
  })
  return { nodes, edges, width, height, error: '' }
})

function statusMark(status: string): string {
  if (status === 'ok') return '✓'
  if (status === 'failed') return '✕'
  if (status === 'skipped') return '–'
  return ''
}

function chips(n: LayoutNode): string[] {
  const out = [n.kind]
  if (n.step.when && n.step.when !== 'on_success') out.push(n.step.when)
  if (n.step.retry) out.push(`retry ${n.step.retry}`)
  if (n.result?.durationMs) out.push(`${n.result.durationMs}ms`)
  return out
}

function toggle(n: LayoutNode) {
  selectedId.value = selectedId.value === n.step.id ? '' : n.step.id
  emit('select', selectedId.value ? { step: n.step, result: n.result } : null)
}

// 模板 JSON 或叠加的运行状态变化时，同步刷新 Inspector（或清空选中）
watch([() => props.definition, () => props.steps], () => {
  const n = layout.value.nodes.find(x => x.step.id === selectedId.value)
  if (!n) {
    selectedId.value = ''
    emit('select', null)
  } else {
    emit('select', { step: n.step, result: n.result })
  }
})
</script>

<template>
<div class="pg-wrap">
  <div v-if="layout.error" class="pg-error">{{ layout.error }}</div>
  <div v-else class="pg-scroll">
    <div class="pg-canvas" :style="{ width: layout.width + 'px', height: layout.height + 'px' }">
      <svg class="pg-edges" :width="layout.width" :height="layout.height" aria-hidden="true">
        <defs>
          <marker :id="arrowId" markerWidth="9" markerHeight="9" refX="8" refY="4.5" orient="auto">
            <path d="M 0 0 L 9 4.5 L 0 9 z" fill="var(--border)" />
          </marker>
        </defs>
        <path v-for="(e, i) in layout.edges" :key="i" :d="e.d" :marker-end="`url(#${arrowId})`" />
      </svg>
      <div
        v-for="n in layout.nodes"
        :key="n.step.id"
        class="pg-node"
        :class="[n.kind, n.status, { active: selectedId === n.step.id }]"
        :style="{ left: n.x + 'px', top: n.y + 'px', width: NODE_W + 'px', height: NODE_H + 'px' }"
        :title="n.step.uses"
        @click="toggle(n)"
      >
        <span class="pg-bar"></span>
        <div class="pg-top">
          <code class="pg-id">{{ n.step.id }}</code>
          <span v-if="n.status" class="pg-badge" :class="n.status">{{ statusMark(n.status) }}</span>
        </div>
        <div class="pg-uses">{{ n.step.uses }}</div>
        <div class="pg-chips">
          <span v-for="c in chips(n)" :key="c" class="pg-chip">{{ c }}</span>
        </div>
      </div>
    </div>
  </div>
</div>
</template>

<style scoped>
.pg-wrap { min-width: 0; width: 100%; flex: 1; min-height: 0; display: flex; flex-direction: column; }
.pg-error {
  font-size: 12px; color: #c62828; background: var(--code-bg);
  border-radius: 10px; padding: 14px; line-height: 1.6;
}
/* 圆角统一由父级 .pipe-board（overflow:hidden）裁切：这里再自带 border+radius
   会叠成双层描边，左上角露缝。flex 撑满剩余高度；margin:auto 让画布在可用
   空间居中，内容超宽时仍可横向滚动且不裁掉左侧。 */
.pg-scroll {
  flex: 1; min-height: 0; overflow: auto; display: flex;
  background-color: var(--bg);
  background-image: radial-gradient(rgba(128, 128, 128, .22) 1px, transparent 1px);
  background-size: 20px 20px;
}
.pg-canvas { position: relative; flex-shrink: 0; margin: auto; }
.pg-edges { position: absolute; left: 0; top: 0; pointer-events: none; }
.pg-edges path { fill: none; stroke: var(--border); stroke-width: 1.5; opacity: .9; }
.pg-node {
  position: absolute; box-sizing: border-box; padding: 10px 12px 8px 15px;
  border: 1px solid var(--border); border-radius: 12px;
  background: var(--bg); box-shadow: 0 2px 10px rgba(0, 0, 0, .07);
  display: flex; flex-direction: column; gap: 5px; cursor: pointer;
  transition: transform .15s, box-shadow .15s;
}
.pg-node:hover { transform: translateY(-2px); box-shadow: 0 6px 18px rgba(0, 0, 0, .12); }
.pg-node.active { outline: 2px solid var(--accent); outline-offset: 2px; }
.pg-node.skipped { opacity: .55; }
.pg-bar { position: absolute; left: 0; top: 10px; bottom: 10px; width: 4px; border-radius: 0 4px 4px 0; background: var(--accent); }
.pg-node.tool .pg-bar { background: #3b82f6; }
.pg-node.api .pg-bar { background: #8b5cf6; }
.pg-node.llm .pg-bar { background: #10a37f; }
.pg-top { display: flex; align-items: center; gap: 8px; min-width: 0; }
.pg-id { font-size: 13px; font-weight: 650; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pg-badge {
  margin-left: auto; width: 18px; height: 18px; border-radius: 50%; flex-shrink: 0;
  display: flex; align-items: center; justify-content: center; font-size: 11px; color: #fff;
}
.pg-badge.ok { background: #2e7d32; }
.pg-badge.failed { background: #cf222e; }
.pg-badge.skipped { background: #9e9e9e; }
.pg-uses {
  font-size: 11px; color: var(--text-dim); font-family: var(--mono, monospace);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.pg-chips { display: flex; gap: 4px; flex-wrap: nowrap; overflow: hidden; margin-top: auto; }
.pg-chip {
  font-size: 9px; line-height: 1.5; padding: 1px 6px; border-radius: 10px;
  border: 1px solid var(--border); color: var(--text-dim); white-space: nowrap;
}
</style>
