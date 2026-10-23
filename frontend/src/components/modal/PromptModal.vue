<script setup lang="ts">
// 通用输入弹窗：新建文件夹/重命名共用。
// window.openPrompt 由 lib/promptModal.ts 包装（同 ShareModal 注册模式）。
import { ref, nextTick, watch } from 'vue'

const visible = ref(false)
const title = ref('')
const value = ref('')
const inputEl = ref<any>(null)

let onOk: ((v: string) => void) | null = null

;(window as any).openPrompt = (opts: { title: string; initial?: string }, cb: (v: string) => void) => {
  title.value = opts.title
  value.value = opts.initial || ''
  onOk = cb
  visible.value = true
  nextTick(() => { inputEl.value?.focus?.() })
}

watch(visible, v => { if (!v) onOk = null })

function close() { visible.value = false }

function submit() {
  const v = value.value.trim()
  if (!v) return
  const cb = onOk
  close() // 先关再回调，回调自行处理错误提示
  cb?.(v)
}
</script>

<template>
  <div class="modal-mask" v-if="visible" @click.self="close">
    <div class="modal-content" style="width: 380px; max-width: 92vw;">
      <div class="modal-header">{{ title }}</div>
      <div class="modal-body">
        <el-input ref="inputEl" v-model="value" @keyup.enter="submit"></el-input>
      </div>
      <div class="modal-actions" style="display:flex;justify-content:flex-end;gap:8px;">
        <el-button @click="close">Cancel</el-button>
        <el-button type="primary" @click="submit">OK</el-button>
      </div>
    </div>
  </div>
</template>
