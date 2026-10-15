/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// dev server：/api 代理到后端（与 backend 同机开发）；
// build 产物默认 <frontend>/dist，由 server express.static 托管（生产不依赖本文件）。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:7897',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    globals: true,
    // antd Table 在 jsdom 下挂载/更新较慢（滚动条测量 + CSS-in-JS），默认 5s 偏紧。
    testTimeout: 30000,
  },
})
