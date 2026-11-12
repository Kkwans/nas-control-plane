// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { RefreshResult } from '@/stores/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import AppRoot from './App.vue'

const auth = reactive({
  state: 'authenticated', isAuthenticated: true, user: { username: 'root' },
  refresh: vi.fn(async () => undefined), logout: vi.fn(async () => ({ serverRevoked: true })),
})
const system = {
  preferences: {}, realtimeScopes: ['summary'], errorCode: null as string | null,
  loadPreferences: vi.fn(async () => undefined), startRealtime: vi.fn(), stopRealtime: vi.fn(), clear: vi.fn(),
  retryClosedRealtime: vi.fn(() => true), refresh: vi.fn<() => Promise<RefreshResult>>(),
}
const registry = { refresh: vi.fn() }
const route = reactive({ fullPath: '/', meta: { realtime: ['summary'] } })
const router = { isReady: vi.fn(async () => undefined), replace: vi.fn(async () => undefined) }
const warning = vi.fn()
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => system }))
vi.mock('@/stores/database', () => ({ useDatabaseStore: () => ({ resetSessionState: vi.fn() }) }))
vi.mock('@/stores/sites', () => ({ useSitesStore: () => ({ resetSessionState: vi.fn() }) }))
vi.mock('@/composables/manualRefresh', () => ({ createManualRefreshRegistry: () => registry, provideManualRefreshRegistry: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => router, RouterView: { render: () => null } }))
vi.mock('element-plus', () => ({
  ElMessage: { warning: (...args: unknown[]) => warning(...args) },
  ElConfigProvider: { setup: (_: unknown, { slots }: { slots: { default: () => unknown } }) => () => slots.default() },
}))
vi.mock('@/views/LoginView.vue', () => ({ default: { render: () => null } }))
vi.mock('@/layout/AppShell.vue', () => ({
  default: {
    inheritAttrs: false, emits: ['refresh', 'logout'],
    setup: (_: unknown, { emit }: { emit: (event: string) => void }) => () => h('button', { 'data-refresh': '', onClick: () => emit('refresh') }, '刷新'),
  },
}))

let app: App | undefined
let host: HTMLDivElement | undefined
const success = (): RefreshResult => ({ requested: ['summary'], succeeded: ['summary'], failed: [] })
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((yes) => { resolve = yes })
  return { promise, resolve }
}
async function flush() { for (let i = 0; i < 12; i++) await Promise.resolve(); await nextTick() }
async function mount() {
  host = document.createElement('div'); document.body.append(host)
  app = createApp(AppRoot); app.mount(host)
  await flush()
  return host.querySelector<HTMLButtonElement>('[data-refresh]')!
}
beforeEach(() => {
  beginSession()
  vi.clearAllMocks()
  auth.state = 'authenticated'; auth.isAuthenticated = true
  route.fullPath = '/'; route.meta.realtime = ['summary']
  system.errorCode = null
  system.retryClosedRealtime.mockReturnValue(true)
  system.refresh.mockResolvedValue(success())
  registry.refresh.mockResolvedValue({ requested: 0, succeeded: 0, failed: [] })
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); invalidateSession(); vi.restoreAllMocks() })

describe('顶部显式刷新与 CLOSED 恢复入口', () => {
  it('首个 await 前调用恢复，重复点击只执行一次恢复和 GET，后续手动资源照常刷新', async () => {
    const button = await mount()
    const pending = deferred<RefreshResult>()
    const order: string[] = []
    system.retryClosedRealtime.mockImplementation(() => { order.push('retry'); return true })
    system.refresh.mockImplementation(() => { order.push('read'); return pending.promise })
    button.click(); button.click()
    expect(order).toEqual(['retry', 'read'])
    expect(system.retryClosedRealtime).toHaveBeenCalledOnce()
    expect(system.refresh).toHaveBeenCalledOnce()
    expect(registry.refresh).not.toHaveBeenCalled()
    pending.resolve(success()); await flush()
    expect(registry.refresh).toHaveBeenCalledOnce()
    expect(system.retryClosedRealtime).toHaveBeenCalledOnce()
    button.click()
    expect(system.retryClosedRealtime).toHaveBeenCalledTimes(2)
    await flush()
  })

  it.each(['route', 'logout'] as const)('%s 发生在 GET pending 期间，晚到完成不能再次调用恢复入口', async (action) => {
    const button = await mount()
    const pending = deferred<RefreshResult>()
    system.refresh.mockReturnValueOnce(pending.promise)
    button.click()
    if (action === 'route') { route.fullPath = '/plain'; route.meta.realtime = [] }
    else { auth.isAuthenticated = false; auth.state = 'anonymous' }
    await flush()
    const startCalls = system.startRealtime.mock.calls.length
    pending.resolve(success()); await flush()
    expect(system.retryClosedRealtime).toHaveBeenCalledOnce()
    expect(system.startRealtime).toHaveBeenCalledTimes(startCalls)
    if (action === 'logout') {
      expect(system.clear).toHaveBeenCalledOnce()
      // 已卸载按钮的旧事件也不能跨会话触发恢复或 GET。
      button.click()
      expect(system.retryClosedRealtime).toHaveBeenCalledOnce()
      expect(system.refresh).toHaveBeenCalledOnce()
    }
  })

  it('重建不能掩盖 GET 与页面资源的失败，既有警告和只读认证复核保留', async () => {
    const button = await mount()
    system.refresh.mockResolvedValueOnce({ requested: ['summary'], succeeded: [], failed: [{ resource: 'summary', error: new Error('read failed') }] })
    registry.refresh.mockResolvedValueOnce({ requested: 1, succeeded: 0, failed: [{ error: new Error('page read failed') }] })
    system.errorCode = 'AUTH_UNAUTHORIZED'
    const authReads = auth.refresh.mock.calls.length
    button.click(); await flush()
    expect(warning).toHaveBeenCalledWith('2 个资源刷新失败，已保留可用数据。')
    expect(auth.refresh).toHaveBeenCalledTimes(authReads + 1)
    expect(system.retryClosedRealtime).toHaveBeenCalledOnce()
  })
})
