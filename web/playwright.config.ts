import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './e2e', testMatch: '**/*.spec.ts', workers: 1,
  use: { baseURL: 'http://127.0.0.1:15173', channel: 'chrome', viewport: { width: 1365, height: 1000 } },
  webServer: [
    { command: 'go test ../internal/admin -run ^TestE2EServer$ -v -timeout 0', env: { ECHOCORE_ADMIN_E2E: '1' }, url: 'http://127.0.0.1:18081/api/admin/v1/session', reuseExistingServer: false, timeout: 60000 },
    { command: 'npm run dev -- --config e2e/vite.config.ts', url: 'http://127.0.0.1:15173', reuseExistingServer: false },
  ],
})
