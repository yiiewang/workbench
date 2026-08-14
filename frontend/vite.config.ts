import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

// Vite 单页应用构建配置：
//   - 入口 index.html → 主界面（Vue Router 接管所有路由）
//   - 产物输出到 dist，由 Go 端 //go:embed 嵌入二进制
// base: '/' 让产物以绝对路径 /assets/... 引用，Go 端 isUIAsset 放行 assets/ 前缀
const API_TARGET = process.env.VITE_API_TARGET || 'http://localhost:80'

export default defineConfig({
  plugins: [vue()],
  base: '/',
  resolve: {
    alias: {
      vue: 'vue/dist/vue.esm-bundler.js',
    },
  },
  server: {
    host: '0.0.0.0',
    port: 4000,
    proxy: {
      // 所有后端 API 统一 /api 前缀，代理到 Go 后端
      '/api':          { target: API_TARGET, changeOrigin: true },
      // /s/{token} 分享页面由 SPA 路由处理（router /s/:token），不代理到 Go 后端：
      //   - 开发模式：Vite 提供 dev index.html + /@vite/client，资源正常加载
      //   - 生产模式：Go 二进制从 embed 提供 built index.html + assets，自洽
      //   - 若代理 /s/ 到 Go，Go 返回 built index.html（引用 /assets/*.css），
      //     但浏览器向 Vite 请求这些 hashed assets 时 Vite 无此文件，
      //     回退返回 index.html（text/html）→ MIME 类型不匹配 "text/html" != "text/css"
      // 分享数据仍通过 /api/share/{token} 获取（已代理）
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'index.html'),
      },
      output: {
        manualChunks: {
          vue: ['vue'],
          'element-plus': ['element-plus'],
          marked: ['marked'],
          'highlight.js': ['highlight.js'],
          'spark-md5': ['spark-md5'],
        },
      },
    },
  },
})
