<script lang="ts">
// 模块级 ESC 层叠栈：所有 ModalShell 实例共享，保证 ESC 只关最上层弹窗
const escStack: symbol[] = []
</script>

<script setup lang="ts">
// 统一弹窗外壳：遮罩 + 内容卡片 + ESC/遮罩点击关闭 + 层级管理。
// 弹窗组件只负责内容（title/body/actions 插槽），不再各写遮罩样式与关闭逻辑。
// 多个弹窗叠加时 ESC 只关最上层（escStack 栈保证）。
import { onMounted, onUnmounted } from 'vue'

const props = withDefaults(defineProps<{
  /** 头部标题（不传且无 header 插槽则不渲染 header） */
  title?: string
  /** 内容宽度 CSS 值，如 '380px' */
  width?: string
  /** 层级：modal=普通弹窗；top=叠加弹窗（如密码框，盖在普通弹窗上） */
  layer?: 'modal' | 'top'
  /** 点遮罩关闭（默认 true） */
  maskClose?: boolean
  /** ESC 关闭（默认 true） */
  escClose?: boolean
}>(), {
  width: '400px',
  layer: 'modal',
  maskClose: true,
  escClose: true,
})

const emit = defineEmits<{ close: [] }>()

// ---- ESC 层叠栈：只响应栈顶弹窗 ----
const escId = Symbol('modal-esc')

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && props.escClose && escStack[escStack.length - 1] === escId) {
    emit('close')
  }
}

onMounted(() => {
  escStack.push(escId)
  window.addEventListener('keydown', onKeydown)
})
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  const i = escStack.indexOf(escId)
  if (i >= 0) escStack.splice(i, 1)
})

function onMaskClick() {
  if (props.maskClose) emit('close')
}
</script>

<template>
  <Teleport to="body">
    <!-- 复用全局 .modal-mask（style.css，层级 --z-modal）；top 层级单独提升 -->
    <div class="modal-mask" :class="{ 'modal-mask-top': layer === 'top' }" @click.self="onMaskClick">
      <div class="modal-shell" :style="{ width, maxWidth: '92vw' }">
        <div v-if="title || $slots.header" class="modal-header">
          <slot name="header">{{ title }}</slot>
        </div>
        <div class="modal-shell-body">
          <slot />
        </div>
        <div v-if="$slots.actions" class="modal-actions">
          <slot name="actions" />
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* 叠加弹窗层级（盖在普通弹窗之上） */
.modal-mask-top {
  z-index: var(--z-modal-top);
}

/* 内容卡片：复用全局 header/actions 样式，这里只管卡片本体与 body 滚动 */
.modal-shell {
  background: var(--bg);
  border-radius: 8px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.2);
  max-height: 85vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.modal-shell-body {
  flex: 1;      /* 弹性填充：固定高度（移动全屏）时 actions 贴底 */
  min-height: 0;
  padding: 20px 24px;
  overflow-y: auto;
}

/* 移动断点全屏化（≤900px 与全局断点一致；移动端 FilePickerModal/任务详情等共用）：
   覆盖 inline width */
@media (max-width: 900px) {
  .modal-shell {
    width: 100vw !important;
    max-width: 100vw !important;
    height: 88dvh;
    border-radius: 0;
    border: none;
  }
}
</style>
