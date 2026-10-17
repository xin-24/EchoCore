import { afterEach, expect, test, vi } from 'vitest'
import { api, previewURL, setCSRF, type Sticker } from './client'

afterEach(() => { vi.unstubAllGlobals(); setCSRF('') })
test('写请求附带 CSRF，始终使用同源与禁止缓存', async () => {
  const mock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { ok: true } })))
  vi.stubGlobal('fetch', mock); setCSRF('csrf-only-for-test')
  await api('/stickers/batch', { method: 'POST', body: '{}' })
  const [url, options] = mock.mock.calls[0]
  expect(url).toBe('/api/admin/v1/stickers/batch')
  expect(options.credentials).toBe('same-origin'); expect(options.cache).toBe('no-store')
  expect(options.headers.get('X-CSRF-Token')).toBe('csrf-only-for-test')
})
test('非 JSON 响应给出管理接口提示', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not found', { status: 404 })))
  await expect(api('/session')).rejects.toThrow('管理接口不可用')
})
test('预览只引用受保护 API，不使用远端 URL', () => {
  const url = previewURL({ id: 'qq_123', source: 'qq', account: '1234567890123456789' } as Sticker)
  expect(url).toContain('/api/admin/v1/stickers/qq_123/preview?')
  expect(url).toContain('account=1234567890123456789'); expect(url).toContain('still=1')
})
