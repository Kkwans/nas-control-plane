// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createApp, nextTick, type App } from 'vue'
import { createPinia, setActivePinia, type Pinia } from 'pinia'

import type { DockerInventory } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { useSystemStore } from '@/stores/system'
import DockerView from './DockerView.vue'

const refreshHandlers = vi.hoisted(() => new Set<() => Promise<void>>())
vi.mock('@/composables/manualRefresh', () => ({ useManualRefreshRegistry: () => ({ register: (handler: () => Promise<void>) => { refreshHandlers.add(handler); return () => refreshHandlers.delete(handler) } }) }))
const routerReplace = vi.hoisted(() => vi.fn())
const routeQuery = vi.hoisted(() => ({} as Record<string, string>))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: routeQuery }), useRouter: () => ({ replace: routerReplace }) }))
vi.mock('@/stores/sites', () => ({ useSitesStore: () => ({ visibleSites: [] }) }))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  return { useListPreference: () => ({ pageSize: ref(10) }) }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const stub = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
  return { ElDrawer: defineComponent({ props: { modelValue: Boolean }, setup: (props, { slots }) => () => props.modelValue ? h('aside', [slots.header?.({ titleId: 'docker-view-drawer-test-title' }), slots.default?.()]) : null }), ElInput: stub, ElTooltip: defineComponent({ props: { disabled: Boolean, triggerKeys: Array }, setup: (props, { slots }) => () => h('div', { 'data-tooltip-disabled': String(props.disabled), 'data-tooltip-trigger-keys': JSON.stringify(props.triggerKeys) }, slots.default?.()) }), ElMessage: { info: vi.fn(), error: vi.fn(), warning: vi.fn(), success: vi.fn() }, ElMessageBox: { confirm: vi.fn() } }
})
vi.mock('@/components/DockerImagePanel.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ContainerLogDrawer.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ComposeEditorDrawer.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))

const collectedAt = '2026-10-13T00:00:00.000Z'
function inventory(offset = 0, empty = false): DockerInventory {
  return {
    collectedAt: new Date(Date.parse(collectedAt) + offset).toISOString(),
    projects: empty ? [] : [{ id: 'demo', name: 'demo-project', kind: 'compose', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'], containerCount: 1, runningCount: 0, state: 'stopped' }],
    containers: empty ? [] : [{ id: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', name: 'demo-web', image: 'demo:latest', state: 'exited', status: 'Exited', createdAt: collectedAt, projectId: 'demo', projectName: 'demo-project', serviceName: 'web', ports: [] }],
    engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'amd64', containers: empty ? 0 : 1, containersRunning: empty ? 0 : 1, containersStopped: 0, images: 0 },
  }
}
function response(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }) }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
class FakeEventSource extends EventTarget {
  static sources: FakeEventSource[] = []
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn()
  constructor() { super(); FakeEventSource.sources.push(this) }
  snapshot(docker: unknown) { this.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify({ collectedAt: new Date().toISOString(), docker }) })) }
}
// Element Plus 的类型将普通 confirm 返回值与 prompt 数据交叉，真实 confirm 返回 action 字符串。
const confirmed = 'confirm' as Awaited<ReturnType<typeof ElMessageBox.confirm>>
let app: App | undefined
let host: HTMLDivElement
let pinia: Pinia
let store: ReturnType<typeof useSystemStore>
let fetcher: ReturnType<typeof vi.fn<typeof fetch>>
async function settle() { for (let index = 0; index < 80; index++) await Promise.resolve(); await nextTick() }
async function mount() {
  app = createApp(DockerView)
  app.use(pinia)
  app.mount(host)
  await settle()
}
beforeEach(() => {
  refreshHandlers.clear()
  delete routeQuery.project
  vi.clearAllMocks()
  vi.mocked(ElMessageBox.confirm).mockResolvedValue(confirmed)
  vi.useFakeTimers()
  vi.setSystemTime(new Date(collectedAt))
  beginSession()
  pinia = createPinia()
  setActivePinia(pinia)
  store = useSystemStore()
  fetcher = vi.fn<typeof fetch>()
  vi.stubGlobal('fetch', fetcher)
  FakeEventSource.sources = []
  vi.stubGlobal('EventSource', FakeEventSource)
  host = document.createElement('div')
  document.body.append(host)
})
afterEach(() => { app?.unmount(); app = undefined; host.remove(); store.clear(); store.$dispose(); invalidateSession(); vi.unstubAllGlobals(); vi.useRealTimers() })

function button(label: string) { return host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)! }
function deletes() { return fetcher.mock.calls.filter(([, options]) => options?.method === 'DELETE') }
function reads() { return fetcher.mock.calls.filter(([, options]) => options?.method !== 'DELETE') }
function receipt() { return { projectId: 'demo', kind: 'compose', completed: true, partial: false, registryDeleted: true, registryRolledBack: false, containers: [{ containerId: 'a'.repeat(64), name: 'demo-web', state: 'exited', deleted: true, success: true }] } }
async function ready() { fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? response({ code: 'DOCKER_UNAVAILABLE', message: 'unknown', requestId: 'test' }, 503) : response(inventory())); await mount() }
function protection() { return host.querySelector('[aria-label="项目删除防重复保护"]') }
function named(text: string) { return [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === text)! }

describe('DockerView 项目删除保护', () => {
  it('确认期间两个入口均禁用；取消恢复焦点并允许新确认', async () => {
    await ready(); const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const target = button('删除项目 demo-project'); target.focus(); target.click(); await settle()
    expect(target.disabled).toBe(true); expect(deletes()).toHaveLength(0)
    expect(ElMessageBox.confirm).toHaveBeenCalledWith(expect.any(String), '确认删除 Docker 项目', expect.objectContaining({ customClass: 'ncp-container-action-confirmation' }))
    confirmation.reject('cancel'); await settle()
    expect(target.disabled).toBe(false); expect(document.activeElement).toBe(target); expect(protection()).toBeNull()
  })
  it('unknown 跨路由保留且清单消失仍可见；只读失败不允许解除', async () => {
    await ready(); button('删除项目 demo-project').click(); await settle()
    expect(deletes()).toHaveLength(1); expect(protection()?.textContent).toContain('删除结果未知')
    expect(button('删除项目 demo-project').disabled).toBe(true)
    expect(button('启动项目 demo-project').disabled).toBe(false)
    app!.unmount(); app = undefined
    fetcher.mockImplementation(async () => response(inventory(1000, true))); await mount()
    expect(protection()?.textContent).toContain('项目 ID：demo')
    named('只读核对项目清单').click(); await settle()
    expect(named('解除本会话删除保护').disabled).toBe(false)
    expect(deletes()).toHaveLength(1); expect(protection()).not.toBeNull()
    fetcher.mockImplementation(async () => response({ code: 'INTERNAL_ERROR', message: 'read failed', requestId: 'test' }, 500))
    named('只读核对项目清单').click(); await settle()
    expect(named('解除本会话删除保护').disabled).toBe(true)
  })
  it('派发后卸载，迟到成功不向旧视图提示或额外读取', async () => {
    await ready(); const pending = deferred<Response>()
    fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? pending.promise : response(inventory()))
    button('删除项目 demo-project').click(); await settle(); const count = reads().length
    app!.unmount(); app = undefined; pending.resolve(response(receipt())); await settle()
    expect(ElMessage.success).not.toHaveBeenCalled(); expect(reads()).toHaveLength(count)
    await mount(); expect(button('删除项目 demo-project').disabled).toBe(false)
  })
  it('派发后卸载，迟到 unknown 保留原会话保护', async () => {
    await ready(); const pending = deferred<Response>()
    fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? pending.promise : response(inventory()))
    button('删除项目 demo-project').click(); await settle(); app!.unmount(); app = undefined
    await mount(); expect(button('删除项目 demo-project').disabled).toBe(true)
    pending.reject(new Error('offline')); await settle()
    expect(protection()?.textContent).toContain('删除结果未知'); expect(ElMessage.error).not.toHaveBeenCalled(); expect(deletes()).toHaveLength(1)
  })
  it('成功后刷新失败保留删除事实，只发一次独立 GET', async () => {
    await ready(); const before = reads().length
    fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? response(receipt()) : response({ code: 'INTERNAL_ERROR', message: 'failed', requestId: 'test' }, 500))
    button('删除项目 demo-project').click(); await settle()
    expect(ElMessage.success).toHaveBeenCalledWith(expect.stringContaining('已删除'))
    expect(ElMessage.warning).toHaveBeenCalledWith(expect.stringContaining('已删除，但状态刷新失败'))
    expect(protection()).toBeNull(); expect(reads()).toHaveLength(before + 1)
  })
  it('解除前重新核对；取消保留保护并恢复焦点，确认只解除不重发', async () => {
    await ready(); button('删除项目 demo-project').click(); await settle()
    named('只读核对项目清单').click(); await settle()
    const target = named('解除本会话删除保护'); target.focus()
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel'); target.click(); await settle()
    expect(protection()).not.toBeNull(); expect(document.activeElement).toBe(target)
    target.click(); await settle(); expect(protection()).toBeNull(); expect(deletes()).toHaveLength(1)
  })
  it('清单在 DELETE 等待期间移除目标不会取消或再次派发', async () => {
    await ready(); const pending = deferred<Response>()
    fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? pending.promise : response(inventory(1000, true)))
    button('删除项目 demo-project').click(); await settle()
    await store.refresh({ inventory: true, force: true }); await settle()
    expect(protection()?.textContent).toContain('正在提交删除')
    pending.reject(new Error('lost')); await settle()
    expect(protection()?.textContent).toContain('删除结果未知'); expect(deletes()).toHaveLength(1)
  })
  it('旧会话成功不能解除新会话同项目确认或发出提示', async () => {
    await ready(); const pending = deferred<Response>()
    fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? pending.promise : response(inventory()))
    button('删除项目 demo-project').click(); await settle(); app!.unmount(); app = undefined
    beginSession(); await mount()
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    button('删除项目 demo-project').click(); await settle()
    pending.resolve(response(receipt())); await settle()
    expect(protection()?.textContent).toContain('等待确认删除'); expect(ElMessage.success).not.toHaveBeenCalled()
    confirmation.reject('cancel'); await settle(); expect(protection()).toBeNull()
  })
  it('过期风险确认不能覆盖更新的核对版本', async () => {
    await ready(); button('删除项目 demo-project').click(); await settle()
    named('只读核对项目清单').click(); await settle()
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    named('解除本会话删除保护').click(); await settle()
    named('只读核对项目清单').click(); await settle()
    confirmation.resolve(confirmed); await settle()
    expect(protection()).not.toBeNull(); expect(deletes()).toHaveLength(1)
  })
  it('成功等待路由期间卸载后不继续刷新或提示', async () => {
    routeQuery.project = 'demo'; await ready()
    const routing = deferred<void>(); routerReplace.mockReturnValueOnce(routing.promise)
    fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? response(receipt()) : response(inventory()))
    button('删除项目 demo-project').click(); await settle()
    expect(ElMessage.success).toHaveBeenCalledOnce(); const count = reads().length
    app!.unmount(); app = undefined; routing.resolve(); await settle()
    expect(reads()).toHaveLength(count); expect(ElMessage.warning).not.toHaveBeenCalled()
  })

})
