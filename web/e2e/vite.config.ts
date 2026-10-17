import { defineConfig, mergeConfig } from 'vite'
import base from '../vite.config.ts'
// 测试单独使用固定端口，不连接正在运行的机器人。
export default mergeConfig(base, defineConfig({ server: { port: 15173, cors: { origin: 'http://127.0.0.1:15173' }, proxy: { '/api/admin/v1/': { target: 'http://127.0.0.1:18081', changeOrigin: false } } } }))
