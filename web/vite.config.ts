import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // 不加载项目根目录的密钥文件，也不把环境变量注入浏览器。
  envDir: false,
  server: {
    forwardConsole: false,
    host: '127.0.0.1', port: 5173, strictPort: true,
    cors: { origin: 'http://127.0.0.1:5173' },
    fs: { strict: true, allow: ['.'] },
    proxy: { '/api/admin/v1/': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
    headers: { 'X-Frame-Options': 'DENY', 'X-Content-Type-Options': 'nosniff' },
  },
})
