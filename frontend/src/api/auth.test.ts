import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { bootstrapRoot, loginRoot, requestAuthStatus } from './auth'

describe('认证恢复响应校验', () => {
  it.each([
    { initialized: true, authenticated: true },
    { initialized: false, authenticated: true, user: { id: 1, username: 'root', role: 'root' } },
    { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'guest' } },
  ])('拒绝缺少有效认证账号或自相矛盾的状态 %j', async (status) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(status)))
    await expect(requestAuthStatus(fetcher)).rejects.toMatchObject({ code: 'AUTH_STATUS_RESPONSE_INVALID' })
  })

  it.each(['not-json', JSON.stringify({ user: { role: 'guest' } })])('损坏的创建响应有稳定错误码', async (body) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(body))
    await expect(bootstrapRoot({ username: 'root', password: 'test-only' }, fetcher)).rejects.toMatchObject({ code: 'AUTH_SESSION_RESPONSE_INVALID' })
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
})


describe('真实认证响应校验与初始化状态恢复', () => {
  beforeEach(() => { setActivePinia(createPinia()) })
  afterEach(() => { vi.unstubAllGlobals() })

  it.each([
    { initialized: true, authenticated: true },
    { initialized: false, authenticated: true, user: { id: 1, username: 'root', role: 'root' } },
    { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'guest' } },
  ])('损坏状态 %j 必须落到不可用，不能变成登录成功', async (status) => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'AUTH_UNAVAILABLE', message: '暂不可用', requestId: 'test-bootstrap' }), { status: 503 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(status)))
    vi.stubGlobal('fetch', fetcher)
    const store = useAuthStore()
    store.state = 'setup'
    expect(await store.bootstrap({ username: 'root', password: 'test-only' })).toBe('reconciled')
    expect(store.state).toBe('unavailable')
    expect(store.errorCode).toBe('BOOTSTRAP_STATUS_UNCONFIRMED')
    expect(store.isAuthenticated).toBe(false)
    expect(store.user).toBeNull()
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual(['/api/v1/auth/bootstrap', '/api/v1/auth/status'])
    expect(fetcher.mock.calls[0]?.[1]?.signal).toBe(fetcher.mock.calls[1]?.[1]?.signal)
    expect(fetcher.mock.calls[1]?.[1]?.body).toBeUndefined()
    expect(fetcher.mock.calls[1]?.[1]?.method).toBeUndefined()
  })
})


describe('HTTP 认证预算响应', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

  it.each([
    ['bootstrap', 'setup', '/api/v1/auth/bootstrap'],
    ['login', 'anonymous', '/api/v1/auth/login'],
  ] as const)('%s 的标准 429 保持原状态，等待用户手动重试', async (action, state, path) => {
    const credentials = { username: 'root', password: 'fake-rate-limit-secret-marker' }
    const message = '认证请求过于频繁，请稍后重试。'
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'AUTH_RATE_LIMITED', message, requestId: 'rate-test' }), {
        status: 429, headers: { 'Retry-After': '5' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ user: { id: 1, username: 'root', role: 'root' }, expiresAt: '2026-10-14T00:00:00Z' })))
    vi.stubGlobal('fetch', fetcher)
    const store = useAuthStore()
    store.state = state
    store.initialized = state === 'anonymous'

    const failure = await store[action](credentials).catch((error: unknown) => error)
    expect(failure).toMatchObject({ code: 'AUTH_RATE_LIMITED', message, requestId: 'rate-test' })
    expect(String(failure)).not.toContain(credentials.password)
    expect(store.state).toBe(state)
    expect(store.initialized).toBe(state === 'anonymous')
    expect(store.user).toBeNull()
    expect(store.isAuthenticated).toBe(false)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0]?.[0]).toBe(path)
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: 'POST', body: JSON.stringify(credentials), credentials: 'same-origin' })

    expect(await store[action](credentials)).toBe('authenticated')
    expect(store.isAuthenticated).toBe(true)
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([path, path])
  })

  it.each([bootstrapRoot, loginRoot])('非标准 429 不信任代理正文或泄露假凭据', async (request) => {
    const secret = 'fake-proxy-secret-marker'
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(`<html>${secret}</html>`, {
      status: 429, headers: { 'Retry-After': '1' },
    }))
    await expect(request({ username: 'root', password: secret }, fetcher)).rejects.toMatchObject({
      code: 'API_REQUEST_FAILED', message: 'NCP 服务暂不可用。',
    })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('非标准 bootstrap 429 保留现有的一次只读调和，不重放凭据', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response('proxy response', { status: 429 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ initialized: false, authenticated: false })))
    vi.stubGlobal('fetch', fetcher)
    const store = useAuthStore()
    store.state = 'setup'
    expect(await store.bootstrap({ username: 'root', password: 'fake-only-secret' })).toBe('reconciled')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(store.state).toBe('setup')
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual(['/api/v1/auth/bootstrap', '/api/v1/auth/status'])
    expect(fetcher.mock.calls[1]?.[1]?.body).toBeUndefined()
    expect(fetcher.mock.calls[1]?.[1]?.method).toBeUndefined()
  })
})
