import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { watch } from 'vue'

import { useAuthStore } from './auth'

const apiMocks = vi.hoisted(() => ({
  requestAuthStatus: vi.fn(),
  bootstrapRoot: vi.fn(),
  loginRoot: vi.fn(),
  logoutRoot: vi.fn(),
}))

vi.mock('@/api/system', () => ({
  NcpApiError: class NcpApiError extends Error {
    code = 'TEST_ERROR'
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
