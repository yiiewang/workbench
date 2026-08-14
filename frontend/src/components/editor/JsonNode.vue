<script setup lang="ts">
// Recursive JSON node — replaces renderJsonNode innerHTML
import { ref, computed } from 'vue'
import { escapeHtml } from '../../lib/common'
// 递归引用自身（Vue SFC <script setup> 需显式 import）
import JsonNode from './JsonNode.vue'

const props = defineProps<{ value: any; keyName: string; depth: number }>()

const collapsed = ref(false)

function toggle() { collapsed.value = !collapsed.value }

function typeClass(val: any): string {
  if (val === null) return 'json-null'
  if (typeof val === 'boolean') return 'json-bool'
  if (typeof val === 'number') return 'json-num'
  if (typeof val === 'string') return 'json-str'
  return ''
}

function displayVal(val: any): string {
  if (val === null) return 'null'
  if (typeof val === 'boolean') return String(val)
  if (typeof val === 'number') return String(val)
  if (typeof val === 'string') {
    const v = val.length > 200 ? val.slice(0, 200) + '…' : val
    return `"${escapeHtml(v)}"`
  }
  return ''
}

const isCollapsible = computed(() => Array.isArray(props.value) || (props.value !== null && typeof props.value === 'object' && Object.keys(props.value).length > 0))
</script>

<template>
  <div class="json-node" :style="{ paddingLeft: depth * 16 + 'px' }">
    <!-- collapsed summary -->
    <template v-if="isCollapsible && collapsed">
      <span class="json-toggle" @click="toggle">▶</span>
      <span class="json-key">{{ keyName }}</span>:
      <span class="json-bracket" v-if="Array.isArray(value)">[…] {{ value.length }} items</span>
      <span class="json-bracket" v-else>{…} {{ Object.keys(value).length }} keys</span>
    </template>

    <!-- expanded collapsible -->
    <template v-else-if="isCollapsible && !collapsed">
      <span class="json-toggle" @click="toggle">▼</span>
      <span class="json-key">{{ keyName }}</span>:
      <span class="json-bracket">{{ Array.isArray(value) ? '[' : '{' }}</span>
      <div class="json-children">
        <template v-for="(v, k) in value" :key="k">
          <JsonNode :value="v" :key-name="String(k)" :depth="depth + 1" />
        </template>
        <div class="json-bracket" :style="{ paddingLeft: depth * 16 + 'px' }">
          {{ Array.isArray(value) ? ']' : '}' }}
          <span class="json-count" v-if="value.length > 1">
            {{ Array.isArray(value) ? `${value.length} items` : `${Object.keys(value).length} keys` }}
          </span>
        </div>
      </div>
    </template>

    <!-- scalar value -->
    <template v-else>
      <span class="json-key">{{ keyName }}</span>: <span :class="typeClass(value)">{{ displayVal(value) }}</span>
    </template>
  </div>
</template>

<style scoped>
.json-node { line-height: 1.6; }
.json-toggle { cursor: pointer; margin-right: 4px; font-size: 10px; user-select: none; }
.json-key { color: var(--text-dim, #888); }
.json-null { color: #999; }
.json-bool { color: #d63384; }
.json-num { color: #0550ae; }
.json-str { color: #0a3069; }
.json-bracket { color: #666; }
.json-count { color: #999; font-size: 11px; margin-left: 8px; }
</style>
