// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createApp, nextTick, type App } from 'vue'
import { createPinia, setActivePinia, type Pinia } from 'pinia'

import type { DockerInventory } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { useSystemStore } from '@/stores/system'
import DockerView from './DockerView.vue'

vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/sites', () => ({ useSitesStore: () => ({ visibleSites: [] }) }))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  return { useListPreference: () => ({ pageSize: ref(10) }) }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const stub = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
  return { ElInput: stub, ElTooltip: stub, ElMessage: { error: vi.fn(), warning: vi.fn(), success: vi.fn() }, ElMessageBox: { confirm: vi.fn() } }
})
vi.mock('@/components/DockerImagePanel.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ContainerLogDrawer.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ComposeEditorDrawer.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ProjectDetailDrawer.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))

const collectedAt = '2026-10-13T00:00:00.000Z'
function inventory(offset = 0, empty = false): DockerInventory {
  return {
    collectedAt: new Date(Date.parse(collectedAt) + offset).toISOString(),
    projects: empty ? [] : [{ id: 'demo', name: 'demo-project', kind: 'standalone', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'], containerCount: 1, runningCount: 1, state: 'running' }],
    containers: empty ? [] : [{ id: 'demo-container', name: 'demo-web', image: 'demo:latest', state: 'running', status: 'Up', createdAt: collectedAt, projectId: 'demo', projectName: 'demo-project', serviceName: 'web', ports: [] }],
    engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'amd64', containers: empty ? 0 : 1, containersRunning: empty ? 0 : 1, containersStopped: 0, images: 0 },
  }
}
function response(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } }) }
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((yes) => { resolve = yes })
  return { promise, resolve }
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
async function settle() { for (let index = 0; index < 20; index++) await Promise.resolve(); await nextTick() }
async function mount() {
  app = createApp(DockerView)
  app.use(pinia)
  app.mount(host)
  await settle()
}
function selectContainers() { [...host.querySelectorAll<HTMLButtonElement>('.section-tabs button')].find((button) => button.textContent?.trim() === '容器')!.click() }
beforeEach(() => {
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
function receipt(state = 'running') { return { containerId: 'demo-container', name: 'demo-web', action: 'restart', state } }
function projectReceipt(state = 'running') { return { projectId: 'demo', kind: 'standalone', action: 'restart', state, completed: true, containers: [{ ...receipt(state), success: true }] } }
async function ready(container = true) {
  fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response(container ? receipt() : projectReceipt()) : response(inventory()))
  await mount()
  if (container) { selectContainers(); await settle() }
}

describe('DockerView 生命周期回执', () => {
  it.each([true, false])('确认等待期间阻止重复派发，固定目标快照 container=%s', async (container) => {
    await ready(container)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const target = button(container ? '重启容器' : '重启项目 demo-project')
    target.click(); target.click(); await settle()
    expect(ElMessageBox.confirm).toHaveBeenCalledTimes(1)
    expect(posts()).toHaveLength(0)
    if (!container) {
      store.inventory!.containers[0]!.id = 'later-container'
      store.inventory!.projects[0]!.name = 'later-project'
      store.inventory!.projects[0]!.id = 'later-project-id'
      store.inventory!.projects[0]!.kind = 'compose'
    }
    confirmation.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(1)
    expect(posts()[0]![1]?.redirect).toBe('error')
    if (!container) expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ projectId: 'demo', kind: 'standalone', containerIds: ['demo-container'], action: 'restart' })
    expect(ElMessage.success).toHaveBeenCalledWith(expect.stringContaining('读回时'))
  })
  it.each([true, false])('状态未达目标保留警告，刷新成功不伪装完成 container=%s', async (container) => {
    await ready(container)
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response(container ? receipt('stopped') : projectReceipt('stopped')) : response(inventory()))
    button(container ? '重启容器' : '重启项目 demo-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(ElMessage.warning).toHaveBeenCalled()
    expect(host.querySelector('.action-error')?.textContent).toContain('只读核对')
    expect(fetcher.mock.calls.filter(([, init]) => init?.method !== 'POST')).toHaveLength(2)
  })
  it.each([true, false])('未知回执与刷新失败分别保留且不自动重试 container=%s', async (container) => {
    await ready(container)
    fetcher.mockImplementation(async (_url, options) => {
      if (options?.method === 'POST') throw new Error('private-network-secret')
      return response({ invalid: true })
    })
    button(container ? '重启容器' : '重启项目 demo-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(host.querySelector('.action-error')?.textContent).toContain('可能已经执行')
    expect(host.querySelector('.action-error')?.textContent).toContain('状态刷新失败')
    expect(host.textContent).not.toContain('private-network-secret')
    await vi.advanceTimersByTimeAsync(15_000); await settle()
    expect(posts()).toHaveLength(1)
  })
  it.each([true, false])('会话在确认期间失效不派发，释放锁后新会话可操作 container=%s', async (container) => {
    await ready(container)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const label = container ? '重启容器' : '重启项目 demo-project'
    button(label).click(); await settle()
    invalidateSession(); beginSession(); confirmation.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(0)
    expect(ElMessage.success).not.toHaveBeenCalled()
    button(label).click(); await settle()
    expect(posts()).toHaveLength(1)
  })
  it.each([true, false])('卸载后迟到回执不通知或触发读取 container=%s', async (container) => {
    await ready(container)
    const pending = deferred<Response>()
    fetcher.mockReturnValueOnce(pending.promise)
    button(container ? '重启容器' : '重启项目 demo-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    app!.unmount(); app = undefined
    pending.resolve(response(container ? receipt() : projectReceipt())); await settle()
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(ElMessage.warning).not.toHaveBeenCalled()
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it.each([true, false])('取消确认不派发或刷新，随后仍可操作 container=%s', async (container) => {
    await ready(container)
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    const label = container ? '重启容器' : '重启项目 demo-project'
    button(label).click(); await settle()
    expect(posts()).toHaveLength(0)
    expect(fetcher).toHaveBeenCalledTimes(1)
    button(label).click(); await settle()
    expect(posts()).toHaveLength(1)
  })
  it.each([true, false])('确认等待时卸载，确认迟到不派发 container=%s', async (container) => {
    await ready(container)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    button(container ? '重启容器' : '重启项目 demo-project').click(); await settle()
    app!.unmount(); app = undefined
    confirmation.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(0)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(ElMessage.success).not.toHaveBeenCalled()
  })
  it.each([true, false])('派发后更换会话，迟到回执不通知或刷新 container=%s', async (container) => {
    await ready(container)
    const pending = deferred<Response>()
    fetcher.mockReturnValueOnce(pending.promise)
    button(container ? '重启容器' : '重启项目 demo-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    invalidateSession(); beginSession()
    pending.resolve(response(container ? receipt() : projectReceipt())); await settle()
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(ElMessage.warning).not.toHaveBeenCalled()
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
})
