// SPA 入口：创建 Vue 应用，挂载 App 根 + Router + ElementPlus
import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import App from './App.vue'
import router from './router'
import './styles/style.css'
import './styles/todo.css'

createApp(App).use(ElementPlus).use(router).mount('#app')
