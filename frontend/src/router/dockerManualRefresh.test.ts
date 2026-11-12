// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import AppRoot from '@/App.vue'
import router from './index'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'

const system = vi.hoisted(() => ({
  deviceName: 'fixture', connectionState: 'connected', connectionLabel: '', connectionDetail: '',
  realtimeState: 'offline', refreshIntervalSeconds: 5, realtimeScopes: [], isRefreshing: false,
  preferences: { sidebarDefault: 'collapsed', navigationOrder: [] }, errorCode: null,
  loadPreferences: vi.fn(async () => undefined), startRealtime: vi.fn(), stopRealtime: vi.fn(), clear: vi.fn(),
  retryClosedRealtime: vi.fn(() => false),
  refresh: vi.fn(async () => ({ requested: [], succeeded: [], failed: [] })),
}))
vi.mock('@/stores/system', () => ({ useSystemStore: () => system }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ state: 'authenticated', isAuthenticated: true, user: { username: 'root' }, refresh: vi.fn(async () => undefined) }) }))
vi.mock('@/stores/database', () => ({ useDatabaseStore: () => ({ resetSessionState: vi.fn() }) }))
vi.mock('@/stores/sites', () => ({ useSitesStore: () => ({ resetSessionState: vi.fn() }) }))
vi.mock('@/views/LoginView.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/TaskCenterDrawer.vue', () => ({ default: { render: () => null } }))
vi.mock('@/views/ServicesView.vue', () => ({ default: { render: () => null } }))
// 使用真实 Router → AppShell 按钮 → App → 注入注册表，页面桩仅登记只读处理器。
// DockerView 自身的精确 Job 核对与清单刷新在其 actionOutcomes 测试中覆盖。
vi.mock('@/views/DockerView.vue', async () => {
  const { onScopeDispose } = await import('vue')
  const { useManualRefreshRegistry } = await import('@/composables/manualRefresh')
  return { default: { setup() {
    const unregister = useManualRefreshRegistry()!.register(async () => {
      await fetch('/api/v1/jobs/fixture-job', { method: 'GET' })
      await fetch('/api/v1/docker/inventory', { method: 'GET' })
    })
    onScopeDispose(unregister)
    return () => null
  } } }
})
let app: App | undefined
let host: HTMLDivElement | undefined
async function flush() { for (let i = 0; i < 30; i++) await Promise.resolve(); await nextTick() }
beforeEach(() => {
  beginSession(); vi.clearAllMocks()
  vi.stubGlobal('matchMedia', () => Object.assign(new EventTarget(), { matches: false }))
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 1 })
  vi.stubGlobal('scrollTo', vi.fn())
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200 })))
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); invalidateSession(); vi.unstubAllGlobals() })

it('仅为 Docker 增加显式刷新元数据，不启用实时流或改变其他路由', () => {
  expect(router.resolve('/docker').meta).toEqual({ title: 'Docker 管理', manualRefresh: true })
  expect(router.getRoutes().filter(route => route.meta.manualRefresh).map(route => route.path).sort()).toEqual(['/docker', '/system'])
  expect(router.resolve('/sites').meta).toEqual({ title: '站点管理' })
})

it('Docker 无实时流时真实顶部按钮可见，点击只读刷新注册表一次且不发 POST', async () => {
  await router.push('/docker'); await router.isReady()
  host = document.createElement('div'); document.body.append(host)
  app = createApp(AppRoot); app.use(router); app.mount(host); await flush()
  expect(system.startRealtime).toHaveBeenCalledWith([])
  const button = host.querySelector<HTMLButtonElement>('button[aria-label="立即刷新数据"]')!
  expect(button).not.toBeNull(); expect(button.disabled).toBe(false)
  expect(fetch).not.toHaveBeenCalled()
  button.click(); button.click(); await flush()
  expect(system.refresh).toHaveBeenCalledOnce()
  expect(vi.mocked(fetch).mock.calls).toEqual([
    ['/api/v1/jobs/fixture-job', { method: 'GET' }],
    ['/api/v1/docker/inventory', { method: 'GET' }],
  ])
  expect(vi.mocked(fetch).mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false)
  await router.push('/sites'); await flush()
  expect(host.querySelector('button[aria-label="立即刷新数据"]')).toBeNull()
})
