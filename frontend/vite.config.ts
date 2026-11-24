import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

const BACKEND_TARGET = process.env.VITE_BACKEND_URL ?? 'http://localhost:8418'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, '.'),
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (
            id.includes('/recharts/') ||
            id.includes('/d3-') ||
            id.includes('/victory-vendor/')
          ) {
            return 'vendor-charts'
          }
          if (id.includes('/lucide-react/')) {
            return 'vendor-icons'
          }
          if (
            id.includes('/@radix-ui/') ||
            id.includes('/cmdk/') ||
            id.includes('/vaul/') ||
            id.includes('/sonner/')
          ) {
            return 'vendor-ui'
          }
          if (
            id.includes('/react/') ||
            id.includes('/react-dom/') ||
            id.includes('/react-router') ||
            id.includes('/scheduler/')
          ) {
            return 'vendor-react'
          }
          return undefined
        },
      },
    },
  },
  server: {
    port: 3010,
    strictPort: true,
    proxy: {
      // 仅代理后端 API / 健康检查 / 公开转发 / 公开嵌入页；/gateway、/intelligence、/embed-admin 是前端 SPA 页面，不要代理。
      '/api':            { target: BACKEND_TARGET, changeOrigin: true },
      '/healthz':        { target: BACKEND_TARGET, changeOrigin: true },
      '/v1':             { target: BACKEND_TARGET, changeOrigin: true },
      '^/embed($|/|\\?)': { target: BACKEND_TARGET, changeOrigin: true },
    },
  },
})
