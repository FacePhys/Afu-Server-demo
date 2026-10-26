import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    // 开发时接口代理到本地后台端口
    proxy: { '/afu/internal/api': 'http://127.0.0.1:18081' },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true, // public/.keep 会被复制回 dist，保证 go:embed 始终有文件
    chunkSizeWarningLimit: 3000, // 内部工具，不拆包
  },
})
