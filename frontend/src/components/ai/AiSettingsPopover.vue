<script setup lang="ts">
// 模型设置弹层：上下文长度只裁剪前端历史；输出上限/推理强度透传上游。
import type { AIGateway } from '../../api/ai'
import type { ModelSettings } from '../../composables/useAIChat'

defineProps<{ gateways: AIGateway[]; settings: ModelSettings }>()
const emit = defineEmits<{ change: [patch: Partial<ModelSettings>] }>()

const CONTEXT_OPTIONS = [
  { label: '64K', value: 64_000 },
  { label: '128K', value: 128_000 },
  { label: '256K', value: 256_000 },
  { label: '1M', value: 1_000_000 },
]
const REASONING_OPTIONS: { label: string; value: ModelSettings['reasoningEffort'] }[] = [
  { label: '默认', value: '' },
  { label: 'Low', value: 'low' },
  { label: 'Medium', value: 'medium' },
  { label: 'High', value: 'high' },
  { label: 'Max', value: 'max' },
]
const MAX_TOKENS_OPTIONS = [
  { label: '默认', value: 0 },
  { label: '4096', value: 4096 },
  { label: '8192', value: 8192 },
]
</script>

<template>
  <div class="ai-settings" @click.stop>
    <div v-if="gateways.length" class="ai-settings-group">
      <div class="ai-settings-label">模型网关</div>
      <div class="ai-settings-options">
        <button v-for="g in gateways" :key="g.id" class="ai-settings-option"
          :class="{ active: settings.gateway === g.id }"
          @click="emit('change', { gateway: g.id })">{{ g.name || g.model }}</button>
      </div>
    </div>
    <div class="ai-settings-group">
      <div class="ai-settings-label">上下文长度</div>
      <div class="ai-settings-options">
        <button v-for="o in CONTEXT_OPTIONS" :key="o.value" class="ai-settings-option"
          :class="{ active: settings.contextTokens === o.value }"
          @click="emit('change', { contextTokens: o.value })">{{ o.label }}</button>
      </div>
    </div>
    <div class="ai-settings-group">
      <div class="ai-settings-label">推理强度</div>
      <div class="ai-settings-options">
        <button v-for="o in REASONING_OPTIONS" :key="o.value" class="ai-settings-option"
          :class="{ active: settings.reasoningEffort === o.value }"
          @click="emit('change', { reasoningEffort: o.value })">{{ o.label }}</button>
      </div>
    </div>
    <div class="ai-settings-group">
      <div class="ai-settings-label">输出上限</div>
      <div class="ai-settings-options">
        <button v-for="o in MAX_TOKENS_OPTIONS" :key="o.value" class="ai-settings-option"
          :class="{ active: settings.maxTokens === o.value }"
          @click="emit('change', { maxTokens: o.value })">{{ o.label }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 弹层容器 .ai-settings 的定位样式留在 AiChat.vue（根元素共享作用域） */
.ai-settings-group + .ai-settings-group { margin-top: 10px; }
.ai-settings-label { font-size: 11px; color: var(--text-muted); margin-bottom: 4px; }
.ai-settings-options { display: flex; flex-wrap: wrap; gap: 4px; }
.ai-settings-option {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 6px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--text);
  cursor: pointer;
}
.ai-settings-option:hover { background: var(--hover-bg, rgba(127, 127, 127, .12)); }
.ai-settings-option.active { background: var(--accent); color: #fff; }
</style>
