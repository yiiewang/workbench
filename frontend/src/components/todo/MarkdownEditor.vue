<script setup>
// MarkdownEditor SFC — 从 todo-app.ts 内联组件迁移，自包含（props modelValue + emit）
import { ref, computed, nextTick } from 'vue'
import { marked } from 'marked'

const props = defineProps({
  modelValue: { type: String, default: '' },
  placeholder: { type: String, default: '支持 Markdown 语法...' },
  showToolbar: { type: Boolean, default: true },
})
const emit = defineEmits(['update:modelValue'])

const mdEditor = ref(null)
const mdPreviewBody = ref(null)

const previewHtml = computed(() => {
  if (!props.modelValue) return ''
  if (typeof marked !== 'undefined' && marked.parse) {
    return marked.parse(props.modelValue)
  }
  return props.modelValue
})

let isSyncing = false
function onEditorScroll(e) {
  if (isSyncing) return
  const el = e.target
  const preview = mdPreviewBody.value
  if (!preview || !el.scrollHeight) return
  isSyncing = true
  const ratio = el.scrollTop / (el.scrollHeight - el.clientHeight)
  preview.scrollTop = ratio * (preview.scrollHeight - preview.clientHeight)
  requestAnimationFrame(() => { isSyncing = false })
}

function insertSyntax(type) {
  const el = mdEditor.value
  if (!el) return
  const start = el.selectionStart
  const end = el.selectionEnd
  const text = props.modelValue || ''
  const before = text.substring(0, start)
  const selected = text.substring(start, end)
  let insert = ''
  let cursorOffset = 0
  switch (type) {
    case 'bold':
      insert = `**${selected || '粗体文本'}**`
      cursorOffset = selected ? 2 : 2
      break
    case 'italic':
      insert = `*${selected || '斜体文本'}*`
      cursorOffset = selected ? 1 : 1
      break
    case 'heading':
      insert = `\n## ${selected || '标题'}\n`
      cursorOffset = 4
      break
    case 'link':
      insert = `[${selected || '链接文本'}](url)`
      cursorOffset = 1
      break
    case 'code':
      if (selected) {
        insert = `\n\`\`\`\n${selected}\n\`\`\`\n`
        cursorOffset = 5
      } else {
        insert = '`代码`'
        cursorOffset = 1
      }
      break
    case 'list':
      insert = `\n- ${selected || '列表项'}`
      cursorOffset = 3
      break
    case 'quote':
      insert = `\n> ${selected || '引用文本'}\n`
      cursorOffset = 3
      break
    case 'hr':
      insert = '\n---\n'
      cursorOffset = 4
      break
    case 'todolist':
      insert = `\n- [ ] ${selected || '待办事项'}`
      cursorOffset = 7
      break
  }
  const newText = before + insert + text.substring(end)
  emit('update:modelValue', newText)
  nextTick(() => {
    el.focus()
    const newPos = start + cursorOffset
    const selLen = selected ? selected.length : 0
    el.setSelectionRange(newPos, newPos + selLen)
  })
}
</script>

<template>
  <div class="md-editor-wrapper">
    <div v-if="showToolbar" class="md-toolbar">
      <button @click="insertSyntax('bold')" title="粗体"><b>B</b></button>
      <button @click="insertSyntax('italic')" title="斜体"><i>I</i></button>
      <button @click="insertSyntax('heading')" title="标题">H</button>
      <button @click="insertSyntax('link')" title="链接"><svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6.5 8.5a3.5 3.5 0 005 0l2-2a3.5 3.5 0 00-5-5l-1 1"/><path d="M9.5 7.5a3.5 3.5 0 00-5 0l-2 2a3.5 3.5 0 005 5l1-1"/></svg></button>
      <button @click="insertSyntax('code')" title="代码">&lt;/&gt;</button>
      <button @click="insertSyntax('list')" title="列表">&bull;</button>
      <button @click="insertSyntax('quote')" title="引用">&#x201D;</button>
      <button @click="insertSyntax('todolist')" title="待办列表">&#x2611;</button>
      <button @click="insertSyntax('hr')" title="分隔线">&mdash;</button>
    </div>
    <div class="md-split">
      <div class="md-editor">
        <textarea :value="modelValue" @input="$emit('update:modelValue', $event.target.value)"
          :placeholder="placeholder"
          @scroll="onEditorScroll" ref="mdEditor"></textarea>
      </div>
      <div class="md-preview">
        <div class="md-preview-body" ref="mdPreviewBody" v-html="previewHtml"></div>
      </div>
    </div>
  </div>
</template>
