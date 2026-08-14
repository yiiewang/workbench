<script setup lang="ts">
// App 根组件：仅包含全局元素（toast + RouterView + 全局弹窗）。
// 布局由子路由的 layout 组件决定（DefaultLayout 或无布局的独立页面如 LoginView）。
import ShareModal from './components/modal/ShareModal.vue'
import SharePasswordModal from './components/modal/SharePasswordModal.vue'
</script>

<template>
<div class="toast" id="toast"></div>
<RouterView />
<ShareModal />
<SharePasswordModal />
</template>

<style>
.form-required { color: #e53935; }
.share-modal-content { max-width: 480px; }
.share-modal-body { display: block; padding: 20px 24px; }
</style>
