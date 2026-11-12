import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { watch } from 'vue'

import { NcpApiError } from '@/api/system'

import { useAuthStore } from './auth'

const apiMocks = vi.hoisted(() => ({
  requestAuthStatus: vi.fn(),
  bootstrapRoot: vi.fn(),
  loginRoot: vi.fn(),
  logoutRoot: vi.fn(),
}))

vi.mock('@/api/system', () => ({
  NcpApiError: class NcpApiError extends Error {
    constructor(readonly code: string, message: string) { super(message) }
  },
  ...apiMocks,
}))

const user = { id: 1, username: 'root', role: 'root' as const }

describe('useAuthStore lifecycle', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
    apiMocks.logoutRoot.mockResolvedValue(undefined)
    apiMocks.loginRoot.mockResolvedValue({ user, expiresAt: '2026-08-30T00:00:00Z' })
  })

  it('does not let a slow refresh overwrite a newer login', async () => {
    let resolveRefresh: ((value: unknown) => void) | undefined
    apiMocks.requestAuthStatus.mockReturnValueOnce(new Promise((resolve) => { resolveRefresh = resolve }))
    const store = useAuthStore()

    const refresh = store.refresh()
    await store.login({ username: 'root', password: 'secret' })
    resolveRefresh?.({ initialized: true, authenticated: false })
    await refresh

    expect(store.isAuthenticated).toBe(true)
    expect(store.user).toEqual(user)
  })

  it('always clears local state when server logout cannot be confirmed', async () => {
    const store = useAuthStore()
    await store.login({ username: 'root', password: 'secret' })
    apiMocks.logoutRoot.mockRejectedValueOnce(new Error('offline'))

    await expect(store.logout()).resolves.toEqual({ serverRevoked: false })
    expect(store.isAuthenticated).toBe(false)
    expect(store.state).toBe('anonymous')
    expect(store.errorCode).toBe('LOGOUT_UNCONFIRMED')
  })

  it('does not invalidate an authenticated session during transient status failures', async () => {
    const store = useAuthStore()
    await store.login({ username: 'root', password: 'secret' })
    const transitions: boolean[] = []
    const stop = watch(() => store.isAuthenticated, (value) => transitions.push(value), { flush: 'sync' })
    apiMocks.requestAuthStatus.mockRejectedValueOnce(new Error('temporary failure'))
    await store.refresh()
    stop()
    expect(store.isAuthenticated).toBe(true)
    expect(store.user).toEqual(user)
    expect(transitions).toEqual([])
    expect(store.errorCode).toBe('NETWORK_UNAVAILABLE')
  })

  it('clears the session when the server confirms it is no longer authenticated', async () => {
    const store = useAuthStore()
    await store.login({ username: 'root', password: 'secret' })
    apiMocks.requestAuthStatus.mockResolvedValueOnce({ initialized: true, authenticated: false })
    await store.refresh()
    expect(store.state).toBe('anonymous')
    expect(store.user).toBeNull()
  })

  it('does not grant authentication when the initial status request fails', async () => {
    const store = useAuthStore()
    apiMocks.requestAuthStatus.mockRejectedValueOnce(new Error('offline'))
    await store.refresh()
    expect(store.state).toBe('unavailable')
    expect(store.isAuthenticated).toBe(false)
  })
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

const credentials = { username: 'root', password: 'test-secret' }

describe('bootstrap 只读状态调和', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('成功初始化不额外查询或自动登录', async () => {
    apiMocks.bootstrapRoot.mockResolvedValue({ user })
    const store = useAuthStore()
    expect(await store.bootstrap(credentials)).toBe('authenticated')
    expect(store.isAuthenticated).toBe(true)
    expect(apiMocks.requestAuthStatus).not.toHaveBeenCalled()
    expect(apiMocks.loginRoot).not.toHaveBeenCalled()
  })

  it.each(['AUTH_UNAVAILABLE', 'AUTH_ALREADY_INITIALIZED', 'AUTH_SESSION_RESPONSE_INVALID', 'API_REQUEST_FAILED', 'network'])(
    '%s 后仅查询一次，已有账号时切到登录', async (code) => {
      apiMocks.bootstrapRoot.mockRejectedValue(code === 'network' ? new TypeError('offline') : new NcpApiError(code, 'failure'))
      apiMocks.requestAuthStatus.mockResolvedValue({ initialized: true, authenticated: false })
      const store = useAuthStore()
      store.state = 'setup'
      expect(await store.bootstrap(credentials)).toBe('reconciled')
      expect(store.state).toBe('anonymous')
      expect(store.user).toBeNull()
      expect(store.errorMessage).toBe('控制台已初始化，请登录。')
      expect(apiMocks.bootstrapRoot).toHaveBeenCalledTimes(1)
      expect(apiMocks.requestAuthStatus).toHaveBeenCalledTimes(1)
      expect(apiMocks.requestAuthStatus.mock.calls[0]?.[1]).toBe(apiMocks.bootstrapRoot.mock.calls[0]?.[2])
      expect(apiMocks.loginRoot).not.toHaveBeenCalled()
    },
  )

  it.each(['AUTH_INPUT_INVALID', 'VALIDATION_ERROR', 'AUTH_ORIGIN_FORBIDDEN', 'AUTH_RATE_LIMITED', 'REQUEST_BODY_TOO_LARGE', 'UNSUPPORTED_MEDIA_TYPE'])(
    '明确 %s 不触发调和', async (code) => {
      const error = new NcpApiError(code, 'AUTH_UNAVAILABLE 503')
      apiMocks.bootstrapRoot.mockRejectedValue(error)
      const store = useAuthStore()
      store.state = 'setup'
      await expect(store.bootstrap(credentials)).rejects.toBe(error)
      expect(store.state).toBe('setup')
      expect(apiMocks.requestAuthStatus).not.toHaveBeenCalled()
    },
  )

  it.each([
    [{ initialized: false, authenticated: false }, 'setup', 'reconciled'],
    [{ initialized: true, authenticated: true, user }, 'authenticated', 'authenticated'],
  ])('只采用权威状态 %j', async (status, state, result) => {
    apiMocks.bootstrapRoot.mockRejectedValue(new NcpApiError('AUTH_UNAVAILABLE', 'failure'))
    apiMocks.requestAuthStatus.mockResolvedValue(status)
    const store = useAuthStore()
    expect(await store.bootstrap(credentials)).toBe(result)
    expect(store.state).toBe(state)
    expect(store.isAuthenticated).toBe(state === 'authenticated')
  })

  it('状态失败保持不可用，后续刷新只读', async () => {
    apiMocks.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
    apiMocks.requestAuthStatus.mockRejectedValueOnce(new Error('offline'))
    const store = useAuthStore()
    expect(await store.bootstrap(credentials)).toBe('reconciled')
    expect(store.state).toBe('unavailable')
    expect(store.errorCode).toBe('BOOTSTRAP_STATUS_UNCONFIRMED')
    expect(store.user).toBeNull()
    apiMocks.requestAuthStatus.mockResolvedValueOnce({ initialized: true, authenticated: false })
    await store.refresh()
    expect(store.state).toBe('anonymous')
    expect(apiMocks.bootstrapRoot).toHaveBeenCalledTimes(1)
    expect(apiMocks.loginRoot).not.toHaveBeenCalled()
  })

  it.each(['login', 'logout', 'refresh'] as const)('迟到调和不覆盖新的 %s', async (action) => {
    const status = deferred<unknown>()
    apiMocks.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
    apiMocks.requestAuthStatus.mockReturnValueOnce(status.promise)
    const store = useAuthStore()
    store.state = 'setup'
    const pending = store.bootstrap(credentials)
    await Promise.resolve()
    const signal = apiMocks.requestAuthStatus.mock.calls[0]?.[1] as AbortSignal
    apiMocks.loginRoot.mockResolvedValue({ user })
    apiMocks.logoutRoot.mockResolvedValue(undefined)
    apiMocks.requestAuthStatus.mockResolvedValueOnce({ initialized: true, authenticated: false })
    if (action === 'login') await store.login(credentials)
    else await store[action]()
    const state = store.state
    expect(signal.aborted).toBe(true)
    status.resolve({ initialized: true, authenticated: true, user })
    expect(await pending).toBe('superseded')
    expect(store.state).toBe(state)
  })

  it('被取代的创建失败不开始调和，也不结束新的请求', async () => {
    const bootstrap = deferred<unknown>()
    const login = deferred<unknown>()
    apiMocks.bootstrapRoot.mockReturnValue(bootstrap.promise)
    apiMocks.loginRoot.mockReturnValue(login.promise)
    const store = useAuthStore()
    const old = store.bootstrap(credentials)
    const newer = store.login(credentials)
    bootstrap.reject(new TypeError('offline'))
    expect(await old).toBe('superseded')
    expect(apiMocks.requestAuthStatus).not.toHaveBeenCalled()
    const loginSignal = apiMocks.loginRoot.mock.calls[0]?.[2] as AbortSignal
    apiMocks.logoutRoot.mockResolvedValue(undefined)
    await store.logout()
    expect(loginSignal.aborted).toBe(true)
    login.resolve({ user })
    expect(await newer).toBe('superseded')
  })

  it.each(['bootstrap', 'status'])('中止 %s 不制造失败状态', async (phase) => {
    const aborted = new DOMException('aborted', 'AbortError')
    apiMocks.bootstrapRoot.mockRejectedValue(phase === 'bootstrap' ? aborted : new TypeError('offline'))
    apiMocks.requestAuthStatus.mockRejectedValue(aborted)
    const store = useAuthStore()
    store.state = 'setup'
    expect(await store.bootstrap(credentials)).toBe('superseded')
    expect(store.state).toBe('setup')
    expect(store.errorCode).toBeNull()
    expect(apiMocks.requestAuthStatus).toHaveBeenCalledTimes(phase === 'bootstrap' ? 0 : 1)
  })
})
