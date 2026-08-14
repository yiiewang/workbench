<script setup lang="ts">
// Recursive JSON tree viewer — replaces renderJson / renderJsonNode innerHTML
import { ref, watch } from 'vue'
import { copyToClipboard, showToast } from '../../lib/common'
import JsonNode from './JsonNode.vue'

const props = defineProps<{ text: string }>()

const mode = ref<'viewer' | 'raw'>('viewer')
const parsed = ref<any>(null)
const parseError = ref('')

// text 变化时重新解析（FileViewer 的 jsonText 是异步赋值的，setup 时 props.text 可能还是空）
function reparse() {
  if (!props.text) {
    parsed.value = null
    parseError.value = ''
    return
  }
  try {
    parsed.value = JSON.parse(props.text)
    parseError.value = ''
  } catch (e: any) {
    parsed.value = null
    parseError.value = e.message
  }
}
watch(() => props.text, reparse, { immediate: true })

function onCopy() {
  if (props.text) { copyToClipboard(props.text); showToast('JSON copied to clipboard') }
}

function onToggleMode() {
  mode.value = mode.value === 'viewer' ? 'raw' : 'viewer'
}
</script>

<template>
  <div class="json-container">
    <div class="json-toolbar">
      <button class="json-btn" @click="onCopy">📋 Copy raw</button>
      <button class="json-btn" @click="onToggleMode">
        {{ mode === 'viewer' ? '{ } Raw view' : '▼ Tree view' }}
      </button>
    </div>
    <div class="json-content">
      <div v-if="parseError" class="viewer-error">
        Invalid JSON: {{ parseError }}
        <pre class="text-preview" style="margin-top:8px;">{{ text }}</pre>
      </div>
      <div v-else-if="mode === 'viewer'" class="json-viewer">
        <JsonNode :value="parsed" :key-name="'$'" :depth="0" />
      </div>
      <pre v-else class="text-preview">{{ text }}</pre>
    </div>
  </div>
</template>

<style scoped>
.json-container { display: flex; flex-direction: column; height: 100%; }
.json-toolbar { display: flex; align-items: center; gap: 8px; padding: 6px 16px; border-bottom: 1px solid var(--tab-border, #ddd); background: var(--header-bg, #f5f5f5); font-size: 12px; }
.json-btn { padding: 3px 10px; cursor: pointer; border: 1px solid var(--tab-border, #ddd); border-radius: 4px; background: var(--bg, #fff); color: var(--text, #333); }
.json-content { flex: 1; overflow: auto; }
.json-viewer { padding: 8px 0; }
.viewer-error { padding: 20px; color: #d00; }
.text-preview { margin: 0; padding: 16px 24px; white-space: pre-wrap; word-break: break-all; font-size: 13px; font-family: var(--font-mono, monospace); }
</style>
