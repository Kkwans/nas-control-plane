import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { bootstrapRoot, requestAuthStatus } from './auth'

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
