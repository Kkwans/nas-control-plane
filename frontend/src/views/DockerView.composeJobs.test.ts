// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createApp, isVNode, nextTick, type App } from 'vue'
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
    projects: empty ? [] : [{ id: 'compose', name: 'demo-project', kind: 'compose', workingDirectory: '/srv/demo', configFiles: ['compose.yaml', 'override.yaml'], containerCount: 1, runningCount: 1, state: 'running' }],
    containers: empty ? [] : [{ id: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', name: 'demo-web', image: 'demo:latest', state: 'running', status: 'Up', createdAt: collectedAt, projectId: 'compose', projectName: 'demo-project', serviceName: 'web', ports: [] }],
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
function job(status = 'queued', action = 'restart') {
  return { id: 'project-job', type: `compose-${action}`, reference: 'compose', status, cancellable: false, artifactState: 'unknown', message: 'private-server-message', progress: status === 'completed' ? 100 : 0, createdAt: collectedAt, updatedAt: collectedAt, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...(['completed', 'failed', 'interrupted'].includes(status) ? { completedAt: collectedAt } : {}) }
}
function terminal(status: 'completed' | 'failed' | 'interrupted') {
  return { ...job(status), ...(status === 'completed' ? { composeActionResult: { projectId: 'compose', action: 'restart', state: 'running', serviceCount: 2 } } : { errorCode: status === 'failed' ? 'JOB_QUEUE_TIMEOUT' : 'JOB_EXECUTION_INTERRUPTED' }) }
}
let observed: unknown, observedStatus: number, currentInventory: DockerInventory, inventoryFailed: boolean
function panel() { return host.querySelector('[aria-label="Compose 操作防重复保护"]')! }
function named(label: string) { return [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === label)! }
function check() { named('只读核对任务与容器清单').click() }
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


describe('Compose lifecycle Job view integration', () => {
  it('posts the frozen scope exactly once and 202 is not completion across desktop, mobile and details', async () => {
    routeQuery.project = 'compose'; await ready(); await restart(); await restart()
    expect(posts()).toHaveLength(1)
    expect(posts()[0]![0]).toBe('/api/v1/docker/compose/projects/compose/action-jobs/restart')
    expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ workingDirectory: '/srv/demo', configFiles: ['compose.yaml', 'override.yaml'] })
    expect(posts()[0]![1]?.redirect).toBe('error')
    expect(panel().textContent).toContain('排队中'); expect(ElMessage.success).not.toHaveBeenCalled()
    expect(button('重启项目 demo-project').disabled).toBe(true)
    for (const selector of ['.mobile-project-actions', 'aside']) {
      const actions = [...host.querySelectorAll<HTMLButtonElement>(`${selector} button`)].filter(element => ['启动', '停止', '重启'].includes(element.textContent?.trim() ?? ''))
      expect(actions).toHaveLength(3)
      for (const element of actions) { expect(element.disabled).toBe(true); element.dispatchEvent(new MouseEvent('click', { bubbles: true })) }
    }
    await settle(); expect(posts()).toHaveLength(1)
    selectContainers(); await settle()
    expect(button('重启容器').disabled).toBe(false)
  })
  it('freezes ordered files and directory before confirmation and survives disappearance', async () => {
    await ready(); const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    button('停止项目 demo-project').click(); await settle()
    const message = vi.mocked(ElMessageBox.confirm).mock.calls[0]![0]
    expect(isVNode(message)).toBe(true)
    if (!isVNode(message)) throw new Error('expected focusable scope')
    expect(message.props).toMatchObject({ tabindex: 0, role: 'region' })
    expect(message.children).toContain('1. compose.yaml\n2. override.yaml')
    expect(message.children).toContain('不是冻结的 Compose 容器范围')
    currentInventory = inventory(1, true); store.inventory = currentInventory
    fetcher.mockImplementation(async (url, options) => options?.method === 'POST' ? response(job('queued', 'stop'), 202) : String(url).includes('/jobs/') ? response(job('queued', 'stop')) : response(currentInventory))
    confirmation.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(1)
    expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ workingDirectory: '/srv/demo', configFiles: ['compose.yaml', 'override.yaml'] })
    expect(panel().textContent).toContain('/srv/demo'); expect(panel().textContent).toContain('override.yaml')
  })
  it('starts directly without confirmation and retains a protection slot', async () => {
    await ready(); currentInventory.containers[0]!.state = 'exited'; currentInventory.projects[0]!.state = 'stopped'; currentInventory.projects[0]!.runningCount = 0
    store.inventory = currentInventory
    fetcher.mockImplementation(async (url, options) => options?.method === 'POST' ? response(job('queued', 'start'), 202) : String(url).includes('/jobs/') ? response(job('queued', 'start')) : response(currentInventory))
    await settle(); button('启动项目 demo-project').click(); await settle()
    expect(ElMessageBox.confirm).not.toHaveBeenCalled(); expect(posts()).toHaveLength(1)
    expect(posts()[0]![0]).toContain('/action-jobs/start'); expect(release().disabled).toBe(true)
  })
  it.each(['pending', '404', 'inventory-failed'])('retains protection for %s evidence', async mode => {
    await ready()
    observed = mode === 'pending' ? { ...job('running'), persistenceStatus: 'pending' } : mode === '404' ? { code: 'NOT_FOUND' } : terminal('interrupted')
    observedStatus = mode === '404' ? 404 : 200; inventoryFailed = mode === 'inventory-failed'
    await restart(); expect(release().disabled).toBe(true); expect(button('重启项目 demo-project').disabled).toBe(true)
  })
  it('unknown submission checks all actions with one manual inventory read without claiming list evidence', async () => {
    await ready(); fetcher.mockImplementation(async (url, options) => {
      if (options?.method === 'POST') throw new Error('private-secret')
      return response(String(url).includes('/jobs?') ? { jobs: [] } : currentInventory)
    })
    await restart(); const before = fetcher.mock.calls.length
    await Promise.all([...refreshHandlers].map(handler => handler())); await settle()
    const reads = fetcher.mock.calls.slice(before)
    expect(reads.filter(([url]) => String(url).includes('/jobs?')).map(([url]) => url).sort()).toEqual(['start', 'stop', 'restart'].map(action => `/api/v1/jobs?type=compose-${action}`).sort())
    expect(reads.filter(([url]) => !String(url).includes('/jobs?'))).toHaveLength(1)
    expect(panel().textContent).toContain('接收结果未知'); expect(panel().textContent).not.toContain('任务 ID：')
    expect(release().disabled).toBe(false); expect(posts()).toHaveLength(1)
  })
  it('requires fresh explicit risk confirmation, preserves focus on cancel and rejects stale acceptance', async () => {
    await ready(); observed = terminal('interrupted'); await restart()
    const trigger = release(); trigger.focus(); vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel'); trigger.click(); await settle()
    expect(document.activeElement).toBe(trigger); expect(button('重启项目 demo-project').disabled).toBe(true)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise); release().click(); await settle()
    check(); await settle(); confirmation.resolve(confirmed); await settle()
    expect(button('重启项目 demo-project').disabled).toBe(true)
    release().click(); await settle(); expect(panel()).toBeNull(); expect(posts()).toHaveLength(1)
    expect(button('重启项目 demo-project').disabled).toBe(false)
  })
  it('retains exact aggregate completion once despite failed refresh, then clears it on new intent', async () => {
    await ready(); await restart(); observed = terminal('completed'); inventoryFailed = true; check(); await settle()
    expect(panel().textContent).toContain('服务观察项数：2')
    expect(panel().textContent).toContain('不保证健康'); expect(panel().textContent).not.toContain('private-server-message')
    expect(host.querySelector('[aria-label="Compose 操作保护状态"]')).toBeNull()
    expect(button('重启项目 demo-project').disabled).toBe(false); expect(posts()).toHaveLength(1)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise); button('重启项目 demo-project').click(); await settle()
    expect(host.querySelector('[aria-label="已确认的 Compose 操作结果"]')).toBeNull()
    confirmation.reject('cancel'); await settle(); expect(panel()).toBeNull()
  })
  it('keeps a dispatched late 202 after route disposal without notifications or new reads', async () => {
    await ready(); const pending = deferred<Response>(); fetcher.mockReturnValueOnce(pending.promise); await restart()
    app!.unmount(); app = undefined; currentInventory = inventory(1, true); store.inventory = currentInventory; await mount()
    expect(panel().textContent).toContain('正在提交'); const before = fetcher.mock.calls.length
    pending.resolve(response(job(), 202)); await settle()
    expect(panel().textContent).toContain('project-job'); expect(fetcher).toHaveBeenCalledTimes(before)
    expect(ElMessage.info).not.toHaveBeenCalled(); expect(posts()).toHaveLength(1)
  })
  it('clears on logout and ignores the old session late 202', async () => {
    await ready(); const pending = deferred<Response>(); fetcher.mockReturnValueOnce(pending.promise); await restart()
    invalidateSession(); await settle(); expect(panel()).toBeNull(); beginSession(); app!.unmount(); app = undefined; await mount(); await restart()
    pending.resolve(response(job(), 202)); await settle()
    expect(posts()).toHaveLength(2); expect(panel().textContent).toContain('排队中'); expect(ElMessage.info).toHaveBeenCalledTimes(1)
  })
  it('Compose lease blocks standalone only until completion or explicit release', async () => {
    await ready(); currentInventory.projects.push({ ...currentInventory.projects[0]!, id: 'standalone', kind: 'standalone', name: 'plain' }); currentInventory.containers.push({ ...currentInventory.containers[0]!, id: 'b'.repeat(64), projectId: 'standalone' }); store.inventory = currentInventory
    await settle(); await restart(); expect(button('重启项目 plain').disabled).toBe(true)
    observed = terminal('completed'); check(); await settle(); expect(button('重启项目 plain').disabled).toBe(false)
  })
})

it('manual completion reuses its one inventory read without replacing the historical receipt', async () => {
  await ready(); await restart(); observed = terminal('completed'); inventoryFailed = true
  const before = fetcher.mock.calls.length
  await Promise.all([...refreshHandlers].map(handler => handler())); await settle()
  const reads = fetcher.mock.calls.slice(before)
  expect(reads.filter(([url]) => String(url).includes('/jobs/'))).toHaveLength(1)
  expect(reads.filter(([url]) => !String(url).includes('/jobs/'))).toHaveLength(1)
  expect(panel().textContent).toContain('服务观察项数：2')
  expect(button('重启项目 demo-project').disabled).toBe(false)
})

it('overlapping manual refreshes retain the shared inventory read until delayed completion arrives', async () => {
  await ready(); await restart()
  const pending = deferred<Response>()
  fetcher.mockImplementation(async url => String(url).includes('/jobs/') ? pending.promise : response(currentInventory))
  const before = fetcher.mock.calls.length
  const refresh = [...refreshHandlers][0]!
  const first = refresh(); await settle(); const second = refresh(); await settle()
  pending.resolve(response(terminal('completed'))); await Promise.all([first, second]); await settle()
  const reads = fetcher.mock.calls.slice(before)
  expect(reads.filter(([url]) => String(url).includes('/jobs/'))).toHaveLength(1)
  expect(reads.filter(([url]) => !String(url).includes('/jobs/'))).toHaveLength(1)
  expect(panel().textContent).toContain('服务观察项数：2')
})
