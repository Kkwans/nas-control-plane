// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, effectScope, nextTick, type App, type EffectScope } from 'vue'
import { createPinia, setActivePinia, type Pinia } from 'pinia'

import type { DockerInventory } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { useSystemStore } from '@/stores/system'
import DockerView from './DockerView.vue'
import { ElMessageBox } from 'element-plus'
import { useDockerContainerCreateJobs } from '@/composables/useDockerContainerCreateJobs'

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

const time = '2026-10-15T00:00:00Z'
const emptyInventory: DockerInventory = { collectedAt: time, containers: [], projects: [], engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'amd64', containers: 0, containersRunning: 0, containersStopped: 0, images: 0 } }
const queued = { id: 'create-1', type: 'docker-container-create', status: 'queued', cancellable: false, artifactState: 'unknown', message: '', progress: 0, createdAt: time, updatedAt: time, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const completed = { ...queued, status: 'completed', progress: 100, completedAt: time, createResult: { containerId: 'a'.repeat(64), created: true, started: false } }
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes }); return { promise, resolve } }
let app: App | undefined, host: HTMLDivElement, pinia: Pinia, store: ReturnType<typeof useSystemStore>, scope: EffectScope, jobs: ReturnType<typeof useDockerContainerCreateJobs>
let fetcher: ReturnType<typeof vi.fn<typeof fetch>>
async function settle() { for (let i = 0; i < 80; i++) await Promise.resolve(); await nextTick() }
async function mount() { app = createApp(DockerView); app.use(pinia); app.mount(host); await settle() }
function button(text: string) { return [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === text)! }
async function submit() { const reservation = jobs.reserve(false)!; await jobs.dispatch(reservation, { image: 'secret-image', name: 'secret-name', environment: { TOKEN: 'secret-value' }, runContainer: false }); return reservation }
beforeEach(() => {
  vi.clearAllMocks(); beginSession(); pinia = createPinia(); setActivePinia(pinia); store = useSystemStore()
  scope = effectScope(); jobs = scope.run(() => useDockerContainerCreateJobs())!
  fetcher = vi.fn<typeof fetch>().mockImplementation(async (url, init) => init?.method === 'POST' ? response(queued, 202) : String(url).includes('/jobs/') ? response(queued) : response(emptyInventory))
  vi.stubGlobal('fetch', fetcher); host = document.createElement('div'); document.body.append(host)
})
afterEach(() => { app?.unmount(); app = undefined; scope.stop(); host.remove(); store.clear(); store.$dispose(); invalidateSession(); vi.unstubAllGlobals() })
describe('DockerView 创建 Job 跨 scope 接线', () => {
  it('清单加载和失败时顶层保护仍可见；202不显示成功与私密配置', async () => {
    await submit(); const pending = deferred<Response>(); fetcher.mockImplementation(async url => String(url).includes('/jobs/') ? response(queued) : pending.promise)
    await mount(); expect(host.textContent).toContain('正在读取 Docker 状态'); expect(host.querySelector('[aria-label="容器创建防重复保护"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="已确认的创建结果"]')).toBeNull(); expect(host.textContent).not.toContain('secret-')
    pending.resolve(response({ error: { code: 'UNAVAILABLE', message: 'unavailable' } }, 503)); await settle()
    expect(host.textContent).toContain('Docker 状态读取失败'); expect(host.querySelector('[aria-label="容器创建防重复保护"]')).not.toBeNull(); expect(jobs.blocked.value).toBe(true)
  })
  it('当前视图同步领取精确完成一次，空清单不抹去完整结果', async () => {
    await submit(); await mount()
    fetcher.mockImplementation(async url => String(url).includes('/jobs/') ? response(completed) : response(emptyInventory))
    button('只读核对任务与容器').click(); await settle()
    expect(jobs.blocked.value).toBe(false); expect(host.querySelector('[aria-label="已确认的创建结果"]')?.textContent).toContain('a'.repeat(64)); expect(host.textContent).toContain('不保证容器持续运行')
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1)
    invalidateSession(); await settle(); expect(host.querySelector('[aria-label="已确认的创建结果"]')).toBeNull()
  })
  it('离开视图后的完成留给下一有效视图，原视图不消费', async () => {
    await submit(); await mount(); const detail = deferred<Response>()
    fetcher.mockImplementation(async url => String(url).includes('/jobs/') ? detail.promise : response(emptyInventory))
    button('只读核对任务与容器').click(); await settle(); app!.unmount(); app = undefined
    detail.resolve(response(completed)); await settle(); expect(jobs.entry.value?.phase).toBe('resolved'); expect(jobs.blocked.value).toBe(true)
    await mount(); expect(jobs.entry.value).toBeNull(); expect(host.textContent).toContain('a'.repeat(64))
  })
  it('未知完整任务列表和清单核对后仍需风险确认，取消恢复焦点', async () => {
    fetcher.mockRejectedValueOnce(new TypeError('lost')); await submit()
    fetcher.mockImplementation(async url => String(url).includes('/jobs') ? response({ jobs: [] }) : response(emptyInventory))
    await mount(); expect(jobs.blocked.value).toBe(true)
    const target = button('解除本会话防重复保护'); expect(target.disabled).toBe(false); target.focus()
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel'); target.click(); await settle()
    expect(jobs.blocked.value).toBe(true); expect(document.activeElement).toBe(target)
    vi.mocked(ElMessageBox.confirm).mockResolvedValueOnce('confirm' as never); target.click(); await settle(); expect(jobs.blocked.value).toBe(false)
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1)
  })
  it('清单失败或畸形响应不能解除未知保护且不泄漏错误正文', async () => {
    fetcher.mockRejectedValueOnce(new TypeError('lost')); await submit()
    fetcher.mockImplementation(async url => String(url).includes('/jobs') ? response({ jobs: [] }) : response({ error: { code: 'PRIVATE', message: 'secret-diagnostic' } }, 500))
    await mount()
    expect(button('解除本会话防重复保护').disabled).toBe(true)
    expect(jobs.blocked.value).toBe(true); expect(host.textContent).not.toContain('secret-diagnostic')
    fetcher.mockImplementation(async url => String(url).includes('/jobs') ? response({ jobs: [] }) : response({ ...emptyInventory, containers: [null] }))
    button('只读核对任务与容器').click(); await settle()
    expect(button('解除本会话防重复保护').disabled).toBe(true); expect(jobs.blocked.value).toBe(true)
  })
  it('风险确认等待中的更新不能被旧确认解除', async () => {
    fetcher.mockRejectedValueOnce(new TypeError('lost')); await submit()
    fetcher.mockImplementation(async url => String(url).includes('/jobs') ? response({ jobs: [] }) : response(emptyInventory))
    await mount(); const confirmation = deferred<never>(); vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    button('解除本会话防重复保护').click(); await settle()
    await jobs.check(jobs.entry.value!, async () => true)
    confirmation.resolve('confirm' as never); await settle(); expect(jobs.blocked.value).toBe(true)
  })

})
