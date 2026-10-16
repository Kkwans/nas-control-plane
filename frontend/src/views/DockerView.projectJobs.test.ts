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
const routeQuery = vi.hoisted(() => ({} as Record<string, string>))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: routeQuery }), useRouter: () => ({ replace: vi.fn() }) }))
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
    projects: empty ? [] : [{ id: 'standalone', name: 'demo-project', kind: 'standalone', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'], containerCount: 1, runningCount: 1, state: 'running' }],
    containers: empty ? [] : [{ id: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', name: 'demo-web', image: 'demo:latest', state: 'running', status: 'Up', createdAt: collectedAt, projectId: 'standalone', projectName: 'demo-project', serviceName: 'web', ports: [] }],
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
function selectContainers() { [...host.querySelectorAll<HTMLButtonElement>('.section-tabs button')].find((button) => button.textContent?.trim() === '容器')!.click() }
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
function posts() { return fetcher.mock.calls.filter(([, options]) => options?.method === 'POST') }
const id = 'a'.repeat(64), secondId = 'b'.repeat(64)
function job(status = 'queued', action = 'restart') {
  return { id: 'project-job', type: `docker-project-${action}`, reference: 'standalone', status, cancellable: false, artifactState: 'unknown', message: 'private-server-message', progress: status === 'completed' ? 100 : 0, createdAt: collectedAt, updatedAt: collectedAt, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...(['completed', 'failed', 'interrupted'].includes(status) ? { completedAt: collectedAt } : {}) }
}
function terminal(status: 'completed' | 'failed' | 'interrupted', ids = [id]) {
  return { ...job(status), ...(status === 'failed' ? { errorCode: 'DOCKER_PROJECT_STATE_MISMATCH' } : status === 'interrupted' ? { errorCode: 'JOB_EXECUTION_INTERRUPTED' } : {}), projectActionResult: { projectId: 'standalone', kind: 'standalone', action: 'restart', items: ids.map((containerId, index) => ({ containerId, ...(status === 'completed' || index > 0 ? { outcome: 'succeeded', state: 'running' } : status === 'failed' ? { outcome: 'state-mismatch', state: 'stopped', errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' } : { outcome: 'unknown', state: 'unknown', errorCode: 'JOB_EXECUTION_INTERRUPTED' }) })) } }
}
let observed: unknown, observedStatus: number, currentInventory: DockerInventory, inventoryFailed: boolean
function panel() { return host.querySelector('[aria-label="项目操作防重复保护"]')! }
function named(label: string) { return [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === label)! }
function check() { named('只读核对任务与容器').click() }
function release() { return named('确认重复执行风险并解除本会话保护') }
async function ready() {
  observed = job(); observedStatus = 200; currentInventory = inventory(); inventoryFailed = false
  fetcher.mockImplementation(async (url, options) => {
    if (options?.method === 'POST') return response(job(), 202)
    if (String(url).includes('/jobs/')) return response(observed, observedStatus)
    if (String(url).includes('/jobs?')) return response({ jobs: [] })
    return response(inventoryFailed ? { invalid: true } : currentInventory)
  })
  await mount()
}
async function restart() { button('重启项目 demo-project').click(); await settle() }

describe('standalone project Job view integration', () => {
  it('submits only the async endpoint once, and 202 remains protected rather than completed', async () => {
    await ready(); await restart(); await restart()
    expect(posts()).toHaveLength(1)
    expect(posts()[0]![0]).toBe('/api/v1/docker/projects/standalone/action-jobs/restart')
    expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ containerIds: [id] })
    expect(posts()[0]![1]?.redirect).toBe('error')
    expect(panel().textContent).toContain('排队中')
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(ElMessage.info).toHaveBeenCalledWith(expect.stringContaining('尚未确认完成'))
    expect(button('停止项目 demo-project').disabled).toBe(true)
    selectContainers(); await settle()
    expect(button('重启容器').disabled).toBe(true)
    await vi.advanceTimersByTimeAsync(30_000); await settle()
    expect(posts()).toHaveLength(1)
  })
  it('freezes the eligible subset and full IDs before inventory changes during confirmation', async () => {
    await ready()
    const initial = inventory(); initial.containers.push({ ...initial.containers[0]!, id: secondId, state: 'exited' }); initial.projects[0]!.containerCount = 2
    store.inventory = initial; await settle()
    const confirm = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirm.promise)
    button('停止项目 demo-project').click(); await settle()
    expect(ElMessageBox.confirm).toHaveBeenCalledOnce()
    expect(vi.mocked(ElMessageBox.confirm).mock.calls[0]![0]).toContain(id)
    expect(vi.mocked(ElMessageBox.confirm).mock.calls[0]![0]).not.toContain(secondId)
    store.inventory = inventory(1, true); await settle()
    fetcher.mockImplementation(async (url, options) => options?.method === 'POST' ? response(job('queued', 'stop'), 202) : String(url).includes('/jobs/') ? response(job('queued', 'stop')) : response(inventory(2, true)))
    confirm.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(1); expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ containerIds: [id] })
    expect(panel().textContent).toContain(id)
  })
  it.each(['failed', 'interrupted'] as const)('keeps safe per-item %s evidence and the lock despite a partial success', async status => {
    await ready(); currentInventory.containers.push({ ...currentInventory.containers[0]!, id: secondId }); currentInventory.projects[0]!.containerCount = 2; store.inventory = currentInventory; await settle()
    observed = terminal(status, [id, secondId]); await restart()
    expect(panel().querySelectorAll('[aria-label="任务逐项结果"] li')).toHaveLength(2)
    expect(panel().textContent).toContain('该次调用回读符合目标状态')
    expect(panel().textContent).toContain(status === 'failed' ? '该次调用回读与目标状态不符' : '执行结果未知，可能已执行')
    expect(panel().textContent).not.toContain('private-server-message')
    expect(button('重启项目 demo-project').disabled).toBe(true)
    expect(release().disabled).toBe(false); expect(ElMessage.success).not.toHaveBeenCalled()
  })
  it.each(['pending', '404'])('a known %s job cannot release protection', async mode => {
    await ready(); observed = mode === 'pending' ? { ...job('running'), persistenceStatus: 'pending' } : { code: 'NOT_FOUND' }; observedStatus = mode === '404' ? 404 : 200
    await restart(); expect(release().disabled).toBe(true)
    expect(panel().textContent).toContain(mode === 'pending' ? '结果保存中' : '任务读取失败')
    expect(button('重启项目 demo-project').disabled).toBe(true)
  })
  it('unknown admission checks all three action types without claiming a matching job', async () => {
    await ready(); fetcher.mockImplementation(async (url, options) => {
      if (options?.method === 'POST') throw new Error('private-network-secret')
      if (String(url).includes('/jobs?')) return response({ jobs: String(url).endsWith('restart') ? [job()] : [] })
      return response(currentInventory)
    })
    await restart(); check(); await settle()
    expect(fetcher.mock.calls.filter(([url]) => String(url).includes('/jobs?')).map(([url]) => url).sort()).toEqual(['start', 'stop', 'restart'].map(action => `/api/v1/jobs?type=docker-project-${action}`).sort())
    expect(panel().textContent).toContain('接收结果未知')
    expect(panel().textContent).toContain('观察到活动项目任务')
    expect(panel().textContent).not.toContain('任务 ID：project-job')
    expect(release().disabled).toBe(true); expect(posts()).toHaveLength(1)
  })
  it('requires explicit risk confirmation and rejects a confirmation made stale by another check', async () => {
    await ready(); observed = terminal('failed'); await restart()
    const confirm = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirm.promise)
    release().click(); await settle()
    expect(vi.mocked(ElMessageBox.confirm).mock.calls.at(-1)![0]).toContain('重复执行')
    check(); await settle(); confirm.resolve(confirmed); await settle()
    expect(panel()).not.toBeNull(); expect(button('重启项目 demo-project').disabled).toBe(true)
    release().click(); await settle()
    expect(host.querySelector('[aria-label="项目操作保护状态"]')).toBeNull()
    expect(button('重启项目 demo-project').disabled).toBe(false); expect(posts()).toHaveLength(1)
  })
  it('consumes completed evidence once, retaining the receipt when inventory refresh fails', async () => {
    await ready(); await restart(); observed = terminal('completed'); inventoryFailed = true; check(); await settle()
    expect(panel().querySelectorAll('[aria-label="已保存的逐项结果"] li')).toHaveLength(1)
    expect(panel().textContent).toContain('该次调用结果已保存')
    expect(host.querySelector('[aria-label="项目操作保护状态"]')).toBeNull()
    expect(button('重启项目 demo-project').disabled).toBe(false)
    expect(posts()).toHaveLength(1)
    app!.unmount(); app = undefined; await mount()
    expect(host.querySelector('[aria-label="项目操作防重复保护"]')).toBeNull()
  })
  it('keeps admitted protection across SPA unmount and an empty inventory', async () => {
    await ready(); await restart(); app!.unmount(); app = undefined
    currentInventory = inventory(1, true); store.inventory = currentInventory; await mount()
    expect(panel().textContent).toContain(id); expect(panel().textContent).toContain('project-job')
    expect(posts()).toHaveLength(1); expect(release().disabled).toBe(true)
  })
  it('a late old-session response cannot populate or unlock the new session', async () => {
    await ready(); const pending = deferred<Response>(); fetcher.mockReturnValueOnce(pending.promise)
    await restart(); invalidateSession(); beginSession(); app!.unmount(); app = undefined; await mount(); await restart()
    pending.resolve(response(job(), 202)); await settle()
    expect(posts()).toHaveLength(2); expect(panel().textContent).toContain('排队中'); expect(button('重启项目 demo-project').disabled).toBe(true)
    expect(ElMessage.info).toHaveBeenCalledTimes(1)
  })
  it('rejects 129 targets before confirmation without splitting or dispatch', async () => {
    await ready(); const value = inventory(); value.containers = Array.from({ length: 129 }, (_, i) => ({ ...value.containers[0]!, id: i.toString(16).padStart(64, '0') })); value.projects[0]!.containerCount = 129; value.projects[0]!.runningCount = 129; store.inventory = value; await settle(); await restart()
    expect(posts()).toHaveLength(0); expect(ElMessageBox.confirm).not.toHaveBeenCalled(); expect(ElMessage.warning).toHaveBeenCalledWith(expect.stringContaining('1–128'))
  })
})

function addCompose(stopped = false) {
  currentInventory.projects.push({ ...currentInventory.projects[0]!, id: 'compose', name: 'compose-project', kind: 'compose', state: stopped ? 'stopped' : 'running', runningCount: stopped ? 0 : 1 })
  currentInventory.containers.push({ ...currentInventory.containers[0]!, id: secondId, projectId: 'compose', projectName: 'compose-project', state: stopped ? 'exited' : 'running' })
  store.inventory = currentInventory
}
function composeReceipt() { return { projectId: 'compose', action: 'restart', output: '', completed: true, state: 'running', services: [{ name: 'web', containerId: secondId, state: 'running', running: true }] } }
describe('Compose and standalone view leases', () => {
  it('real Compose lifecycle and eligible delete buttons are disabled by standalone admission', async () => {
    await ready(); addCompose(true); await settle()
    expect(button('启动项目 compose-project').disabled).toBe(false)
    expect(button('删除项目 compose-project').disabled).toBe(false)
    await restart()
    expect(button('启动项目 compose-project').disabled).toBe(true)
    expect(button('删除项目 compose-project').disabled).toBe(true)
    button('启动项目 compose-project').click(); button('删除项目 compose-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    expect(fetcher.mock.calls.filter(([, options]) => options?.method === 'DELETE')).toHaveLength(0)
  })
  it('Compose stays synchronous and holds a shared lease through confirmation then releases on cancel', async () => {
    await ready(); addCompose(); await settle()
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    button('重启项目 compose-project').click(); await settle()
    expect(button('重启项目 demo-project').disabled).toBe(true); expect(posts()).toHaveLength(0)
    confirmation.reject('cancel'); await settle()
    expect(button('重启项目 demo-project').disabled).toBe(false)
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response(composeReceipt()) : response(currentInventory))
    button('重启项目 compose-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    expect(posts()[0]![0]).toBe('/api/v1/docker/compose/projects/compose/actions/restart')
    expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ projectId: 'compose', action: 'restart', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'] })
    expect(ElMessage.success).toHaveBeenCalledOnce()
    expect(button('重启项目 demo-project').disabled).toBe(false)
  })
  it('unmounting a pending Compose confirmation releases its lease; late confirmation cannot dispatch', async () => {
    await ready(); addCompose(); await settle()
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    button('重启项目 compose-project').click(); await settle()
    app!.unmount(); app = undefined; await mount()
    expect(button('重启项目 demo-project').disabled).toBe(false)
    confirmation.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(0)
  })
  it('dispatched Compose retains its lease across SPA disposal until late settle, without stale UI', async () => {
    await ready(); addCompose(); await settle()
    const pending = deferred<Response>(); fetcher.mockReturnValueOnce(pending.promise)
    button('重启项目 compose-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    app!.unmount(); app = undefined; await mount()
    expect(button('重启项目 demo-project').disabled).toBe(true)
    const reads = fetcher.mock.calls.length
    pending.resolve(response(composeReceipt())); await settle()
    expect(button('重启项目 demo-project').disabled).toBe(false)
    expect(ElMessage.success).not.toHaveBeenCalled(); expect(ElMessage.warning).not.toHaveBeenCalled()
    expect(fetcher).toHaveBeenCalledTimes(reads)
  })
  it('late standalone acceptance after SPA disposal is retained for read-only checking without notifications', async () => {
    await ready(); const pending = deferred<Response>(); fetcher.mockReturnValueOnce(pending.promise)
    await restart(); app!.unmount(); app = undefined; await mount()
    expect(panel().textContent).toContain('正在提交')
    pending.resolve(response(job(), 202)); await settle()
    expect(panel().textContent).toContain('project-job'); expect(panel().textContent).toContain('排队中')
    expect(ElMessage.info).not.toHaveBeenCalled(); expect(posts()).toHaveLength(1)
    expect(button('重启项目 demo-project').disabled).toBe(true)
  })
})

it('manual refresh checks an unknown project submission across all action types with one inventory read', async () => {
  await ready()
  fetcher.mockImplementation(async (url, options) => {
    if (options?.method === 'POST') throw new Error('unknown')
    return response(String(url).includes('/jobs?') ? { jobs: [] } : currentInventory)
  })
  await restart()
  const before = fetcher.mock.calls.length
  expect(refreshHandlers.size).toBe(1)
  await Promise.all([...refreshHandlers].map(handler => handler())); await settle()
  const reads = fetcher.mock.calls.slice(before)
  expect(reads.filter(([url]) => String(url).includes('/jobs?'))).toHaveLength(3)
  expect(reads.filter(([url]) => !String(url).includes('/jobs?'))).toHaveLength(1)
  expect(panel().textContent).toContain('接收结果未知')
  expect(panel().textContent).not.toContain('任务 ID：')
  expect(release().disabled).toBe(false)
  expect(posts()).toHaveLength(1)
})

it('clears an old completed receipt as soon as a new frozen intent starts confirmation', async () => {
  await ready(); observed = terminal('completed'); await restart()
  expect(host.querySelector('[aria-label="已确认的项目操作结果"]')).not.toBeNull()
  const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
  vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
  button('重启项目 demo-project').click(); await settle()
  expect(host.querySelector('[aria-label="已确认的项目操作结果"]')).toBeNull()
  expect(panel().textContent).toContain('等待确认冻结范围')
  expect(posts()).toHaveLength(1)
  confirmation.reject('cancel'); await settle()
  expect(host.querySelector('[aria-label="项目操作防重复保护"]')).toBeNull()
})
