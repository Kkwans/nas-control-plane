// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
const capabilities = { architecture: 'amd64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2 }
function inventory(offset = 0, empty = false): DockerInventory {
  return {
    collectedAt: new Date(Date.parse(collectedAt) + offset).toISOString(),
    projects: empty ? [] : [{ id: 'demo', name: 'demo-project', kind: 'compose', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'], containerCount: 1, runningCount: 1, state: 'running' }],
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
function retryButton() { return [...host.querySelectorAll<HTMLButtonElement>('button')].find((button) => button.textContent?.trim() === '重新加载')! }
function selectContainers() { [...host.querySelectorAll<HTMLButtonElement>('.section-tabs button')].find((button) => button.textContent?.trim() === '容器')!.click() }
beforeEach(() => {
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

describe('DockerView 清单恢复', () => {
  it('首次 mount 启动请求前显示等待，不发出虚假的失败提示', async () => {
    // 保持真正的 idle 状态，验证初始渲染本身不会把未请求误当失败。
    vi.spyOn(store, 'refresh').mockResolvedValueOnce({ requested: ['inventory'], succeeded: [], failed: [] })
    await mount()
    expect(store.inventoryRefreshing).toBe(false)
    expect(store.inventoryErrorCode).toBeNull()
    expect(host.textContent).toContain('正在读取 Docker 状态')
    expect(host.querySelector('.resource-state--error')).toBeNull()
    expect(host.querySelector('.table-empty')).toBeNull()
  })

  it('已加载 capabilities 时，首次坏清单显示可重试错误而非无限等待或空列表', async () => {
    fetcher.mockResolvedValueOnce(response(capabilities))
    await store.refresh({ capabilities: true })
    const initial = deferred<Response>()
    fetcher.mockReturnValueOnce(initial.promise)
    await mount()
    expect(host.textContent).toContain('正在读取 Docker 状态')
    expect(host.querySelector('[aria-busy="true"]')).not.toBeNull()
    expect(host.querySelector('.table-empty')).toBeNull()
    expect(host.querySelector('.workspace-stat dd')?.textContent).toBe('—')

    initial.resolve(response({ ...inventory(), projects: [null] }))
    await settle()
    expect(store.connectionState).toBe('degraded')
    expect(host.textContent).not.toContain('正在读取 Docker 状态')
    expect(host.querySelector('.resource-state--error')?.textContent).toContain('Docker 状态读取失败')
    expect(host.querySelector('.resource-state--error')?.textContent).toContain('DOCKER_INVENTORY_RESPONSE_INVALID')
    expect(host.querySelector('.table-empty')).toBeNull()

    const retry = deferred<Response>()
    fetcher.mockReturnValueOnce(retry.promise)
    const button = retryButton()
    button.click()
    button.click()
    await settle()
    expect(fetcher.mock.calls.filter(([url]) => url === '/api/v1/docker/inventory')).toHaveLength(2)
    expect(host.textContent).toContain('正在读取 Docker 状态')
    retry.resolve(response(inventory(0, true)))
    await settle()
    expect(host.querySelector('.resource-state')).toBeNull()
    expect(host.querySelector('.inventory-recovery')).toBeNull()
    expect(host.textContent).toContain('没有匹配的 Docker 项目。')
    expect(host.querySelector('.workspace-stat dd')?.textContent).toBe('0')
    expect(fetcher.mock.calls.every(([, options]) => options?.method === undefined || options.method === 'GET')).toBe(true)
  })

  it('缓存清单读取失败仍保留项目和容器；重试 busy 只跟随 inventory，同源 SSE 恢复后清除提示', async () => {
    fetcher.mockResolvedValueOnce(response(inventory())).mockResolvedValueOnce(response({ ...inventory(5000), containers: [null] }))
    await store.refresh({ inventory: true })
    store.startRealtime(['docker'])
    await mount()
    expect(host.querySelector('.inventory-recovery')?.textContent).toContain('Docker 状态更新失败，显示上次数据')
    expect(host.querySelector('.inventory-recovery')?.textContent).toContain(collectedAt)
    expect(host.querySelector('.project-row')?.textContent).toContain('demo-project')
    expect(host.querySelector('.resource-state')).toBeNull()
    selectContainers()
    await nextTick()
    expect(host.querySelector('.container-row')?.textContent).toContain('demo-web')

    const retry = deferred<Response>()
    fetcher.mockReturnValueOnce(retry.promise)
    retryButton().click()
    await settle()
    expect(retryButton().disabled).toBe(true)
    expect(retryButton().getAttribute('aria-busy')).toBe('true')
    expect(host.querySelector('.container-row')?.textContent).toContain('demo-web')
    const source = FakeEventSource.sources[0]!
    source.snapshot(inventory(1000))
    await settle()
    expect(host.querySelector('.inventory-recovery')).toBeNull()
    expect(host.querySelector('.container-row')?.textContent).toContain('demo-web')
    // 普通只读重试的迟到坏响应不能覆盖后来成功的 SSE。
    retry.resolve(response({ ...inventory(5000), engine: null }))
    await settle()
    expect(host.querySelector('.inventory-recovery')).toBeNull()
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.inventoryRefreshing).toBe(false)
    expect(FakeEventSource.sources).toHaveLength(1)
    expect(source.close).not.toHaveBeenCalled()
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual(Array(3).fill('/api/v1/docker/inventory'))
    expect(fetcher.mock.calls.every(([, options]) => options?.method === undefined || options.method === 'GET')).toBe(true)
  })

  it('有效空清单过期后显示时间提示，不被伪装为首次失败或重新等待', async () => {
    fetcher.mockResolvedValueOnce(response(inventory(0, true))).mockResolvedValueOnce(response(inventory(0, true)))
    await store.refresh({ inventory: true })
    store.startRealtime([])
    vi.advanceTimersByTime(16_000)
    await mount()
    expect(host.querySelector('.inventory-recovery')?.textContent).toContain('Docker 状态尚未更新，显示上次数据')
    expect(host.querySelector('.inventory-recovery')?.textContent).toContain(collectedAt)
    expect(host.querySelector('.resource-state')).toBeNull()
    expect(host.textContent).toContain('没有匹配的 Docker 项目。')
    expect(FakeEventSource.sources).toHaveLength(0)
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('卸载不破坏共享读取；重新进入复用在途请求，并正确显示迟到失败', async () => {
    const initial = deferred<Response>()
    fetcher.mockReturnValueOnce(initial.promise)
    await mount()
    app!.unmount()
    app = undefined
    expect(store.inventoryRefreshing).toBe(true)
    await mount()
    expect(fetcher).toHaveBeenCalledTimes(1)
    initial.resolve(response({ ...inventory(), containers: [null] }))
    await settle()
    expect(host.querySelector('.resource-state--error')?.textContent).toContain('DOCKER_INVENTORY_RESPONSE_INVALID')
    expect(host.textContent).not.toContain('正在读取 Docker 状态')
    expect(store.inventoryRefreshing).toBe(false)
    expect(FakeEventSource.sources).toHaveLength(0)
  })

  it('会话清除后的旧响应不污染新清单，也不释放新读取的等待状态', async () => {
    const old = deferred<Response>()
    const current = deferred<Response>()
    fetcher.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    await mount()
    invalidateSession()
    store.clear()
    beginSession()
    const newRefresh = store.refresh({ inventory: true })
    old.resolve(response({ ...inventory(), containers: [null] }))
    await settle()
    expect(host.textContent).toContain('正在读取 Docker 状态')
    expect(store.inventoryErrorCode).toBeNull()
    current.resolve(response(inventory(1000)))
    await newRefresh
    await settle()
    expect(host.querySelector('.resource-state')).toBeNull()
    expect(host.querySelector('.inventory-recovery')).toBeNull()
    expect(host.querySelector('.project-row')?.textContent).toContain('demo-project')
  })
})
