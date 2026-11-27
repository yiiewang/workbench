<script setup lang="ts">
// 输入卡片：textarea 融入卡片；底部工具栏（引用入口 + 快捷键提示 / 发送）。
// 待引用文件 chips 由父级维护（@ 选择后注入），本组件只负责展示与移除。
const props = defineProps<{
  modelValue: string
  pendingFiles: string[]
  streaming: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  keydown: [e: KeyboardEvent]
  removeFile: [path: string]
  pickFile: []
  send: []
  stop: []
}>()

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
}
</script>

<template>
  <div class="ai-input">
    <!-- 待引用文件 chips（@ 选择后出现，可移除，发送时注入） -->
    <div v-if="props.pendingFiles.length" class="ai-ctx-list">
      <span v-for="p in props.pendingFiles" :key="p" class="ai-ctx-chip" :title="p">
        <svg class="ai-ctx-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2">
          <path d="M3 1h6l5 5v9H3V1z"/><path d="M9 1v5h5"/>
        </svg>
        <span class="ai-ctx-name">{{ p.split('/').pop() || p }}</span>
        <button class="ai-ctx-remove" title="移除引用" @click="emit('removeFile', p)">&times;</button>
      </span>
    </div>
    <textarea
      :value="props.modelValue"
      rows="2"
      placeholder="发送消息，输入 @ 引用文件"
      @input="onInput"
      @keydown="emit('keydown', $event)"
    ></textarea>
    <div class="ai-input-toolbar">
      <div class="ai-toolbar-left">
        <button class="ai-chip" title="引用文件（也可直接输入 @）" @click="emit('pickFile')">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2">
            <path d="M3 1h6l5 5v9H3V1z"/><path d="M9 1v5h5"/>
          </svg>
          @ 引用文件
        </button>
        <span class="ai-input-hint">Enter 发送 · Shift+Enter 换行</span>
      </div>
      <button v-if="props.streaming" class="ai-send stop" title="停止生成" @click="emit('stop')">
        <svg viewBox="0 0 24 24" fill="currentColor"><rect x="7" y="7" width="10" height="10" rx="2"/></svg>
      </button>
      <button v-else class="ai-send" :disabled="!props.modelValue.trim()" title="发送" @click="emit('send')">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 19V5M5 12l7-7 7 7"/>
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
/* 卡片容器 .ai-input 的布局样式留在 AiChat.vue（根元素共享作用域） */
.ai-input:focus-within { border-color: var(--accent); }
.ai-input textarea {
  display: block;
  width: 100%;
  resize: none;
  border: none;
  padding: 0;
  font-size: 13px;
  font-family: inherit;
  line-height: 1.55;
  background: transparent;
  color: var(--text);
  outline: none;
}
.ai-input textarea::placeholder { color: var(--text-muted); }

/* 底部工具行：左（引用开关 + 提示）/ 右圆形发送 */
.ai-input-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 4px;
}
.ai-toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

/* 待引用文件 chips（@ 选择后显示在输入框上方） */
.ai-ctx-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 6px;
}
.ai-ctx-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--code-bg);
  color: var(--text);
  font-size: 11px;
  padding: 2px 4px 2px 7px;
}
.ai-ctx-icon { width: 11px; height: 11px; flex-shrink: 0; color: var(--text-dim); }
.ai-ctx-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 140px;
}
.ai-ctx-remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  flex-shrink: 0;
  border: none;
  border-radius: 50%;
  background: none;
  color: var(--text-dim);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}
.ai-ctx-remove:hover { background: #fde8ea; color: #cf222e; }

/* 工具行提示 */
.ai-input-hint {
  font-size: 11px;
  color: var(--text-muted);
  user-select: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 工具栏功能按钮：图标 + 文字的轻量 chip */
.ai-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: transparent;
  color: var(--text-dim);
  font-size: 11px;
  padding: 2px 8px;
  cursor: pointer;
  transition: color .15s, border-color .15s;
  white-space: nowrap;
}
.ai-chip svg { width: 12px; height: 12px; flex-shrink: 0; }
.ai-chip:hover { color: var(--accent); border-color: var(--accent); }

.ai-send {
  border: none;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  cursor: pointer;
  width: 30px;
  height: 30px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: opacity .15s;
}
.ai-send svg { width: 15px; height: 15px; }
.ai-send:disabled { opacity: .35; cursor: default; }
.ai-send.stop { background: var(--text-dim); }
</style>
