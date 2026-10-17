export const prefix = '/api/admin/v1'
let csrf = ''
export function setCSRF(value: string) { csrf = value }
export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message) }
}
export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if (options.method && options.method !== 'GET') headers.set('X-CSRF-Token', csrf)
  const timeout = AbortSignal.timeout(15_000)
  const response = await fetch(prefix + path, { ...options, headers, credentials: 'same-origin', cache: 'no-store', signal: options.signal ? AbortSignal.any([options.signal, timeout]) : timeout })
  let payload
  try { payload = await response.json() } catch { throw new APIError(response.status, 'invalid_response', '管理接口不可用，请确认 Go 服务和 admin 配置已启用') }
  if (!response.ok) {
    if (response.status === 401 && path !== '/login') window.dispatchEvent(new Event('admin-expired'))
    throw new APIError(response.status, payload.error?.code ?? 'request_failed', payload.error?.message ?? '请求失败')
  }
  return payload.data as T
}
export const errorText = (error: unknown) => error instanceof Error ? error.message : '请求失败，请重试'

export interface Sticker {
  id: string; name: string; description: string; source: 'qq' | 'local'; account: string;
  revision: string; approval: 'approved' | 'pending' | 'disabled'; cache: 'cached' | 'missing' | 'expired'; fetched_at: string;
}
export interface StickerPage { items: Sticker[]; total: number; page: number; page_size: number; accounts: string[]; favorites_enabled: boolean }
export interface SyncStatus { enabled: boolean; busy?: boolean; queued?: boolean; at?: string; received?: number; ready?: number; failed?: number; skipped?: number; error?: string; resource_hosts?: string[] }
export function previewURL(item: Sticker, still = true) {
  return `${prefix}/stickers/${encodeURIComponent(item.id)}/preview?${new URLSearchParams({ source: item.source, account: item.account, still: still ? '1' : '0' })}`
}
