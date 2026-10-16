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
    projects: empty ? [] : [{ id: 'demo', name: 'demo-project', kind: 'standalone', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'], containerCount: 1, runningCount: 1, state: 'running' }],
    containers: empty ? [] : [{ id: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', name: 'demo-web', image: 'demo:latest', state: 'running', status: 'Up', createdAt: collectedAt, projectId: 'demo', projectName: 'demo-project', serviceName: 'web', ports: [] }],
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
function receipt(state = 'running') { return { containerId: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', name: 'demo-web', action: 'restart', state } }
function projectReceipt(state = 'running') { return { projectId: 'demo', kind: 'standalone', action: 'restart', state, completed: true, containers: [{ ...receipt(state), success: true }] } }
function projectJob(action = 'restart', state?: string) { return { ...actionJob(), id: 'project-job', type: `docker-project-${action}`, reference: 'standalone', ...(state ? { status: state === 'running' ? 'completed' : 'failed', progress: state === 'running' ? 100 : 0, completedAt: collectedAt, ...(state === 'running' ? {} : { errorCode: 'DOCKER_PROJECT_STATE_MISMATCH' }), projectActionResult: { projectId: 'standalone', kind: 'standalone', action, items: [{ containerId: 'a'.repeat(64), outcome: state === 'running' ? 'succeeded' : 'state-mismatch', state, ...(state === 'running' ? {} : { errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }) }] } } : {}) } }
function actionJob() { return { id: 'job-1', type: 'docker-container-restart', reference: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', status: 'queued', artifactState: 'unknown', cancellable: false, message: '已排队', progress: 0, createdAt: collectedAt, updatedAt: collectedAt, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} } }
async function ready(container = true) {
  fetcher.mockImplementation(async (url, options) => options?.method === 'POST' ? response(container ? actionJob() : projectJob(), 202) : String(url).includes('/jobs/') ? response(container ? actionJob() : projectJob()) : response(inventory()))
  await mount()
  if (container) { selectContainers(); await settle() }
}

describe('DockerView 生命周期回执', () => {
  it.each([true, false])('操作等待关闭原位置提示，结束后允许重新定位 container=%s', async (container) => {
    await ready(container)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const target = button(container ? '重启容器' : '重启项目 demo-project')
    const tooltip = () => target.closest('[data-tooltip-disabled]')!
    expect(tooltip().getAttribute('data-tooltip-disabled')).toBe('false')
    target.click(); await settle()
    expect(tooltip().getAttribute('data-tooltip-disabled')).toBe('true')
    const pending = deferred<Response>()
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? pending.promise : response(inventory()))
    confirmation.resolve(confirmed); await settle()
    expect(tooltip().getAttribute('data-tooltip-disabled')).toBe('true')
    pending.resolve(response(container ? receipt('stopped') : projectReceipt('stopped'))); await settle()
    expect(host.querySelector('.action-error')).not.toBeNull()
    expect(tooltip().getAttribute('data-tooltip-disabled')).toBe(container ? 'true' : 'false')
    expect(posts()).toHaveLength(1)
  })

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
    if (!container) expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ containerIds: ['aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'] })
    if (container) { expect(ElMessage.info).toHaveBeenCalledWith(expect.stringContaining('尚未确认完成')); expect(ElMessage.success).not.toHaveBeenCalled() }
    else { expect(ElMessage.info).toHaveBeenCalledWith(expect.stringContaining('尚未确认完成')); expect(ElMessage.success).not.toHaveBeenCalled() }
  })
  it.each([true, false])('旧同步回执不当作异步完成，保留警告与保护 container=%s', async (container) => {
    await ready(container)
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response(container ? receipt('stopped') : projectReceipt('stopped')) : response(inventory()))
    button(container ? '重启容器' : '重启项目 demo-project').click(); await settle()
    expect(posts()).toHaveLength(1)
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(ElMessage.warning).toHaveBeenCalled()
    expect(host.querySelector('.action-error')?.textContent).toContain('只读核对')
    expect(fetcher.mock.calls.filter(([, init]) => init?.method !== 'POST')).toHaveLength(1)
    if (!container) expect(button('重启项目 demo-project').disabled).toBe(true)
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
    if (!container) {
      const check = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === '只读核对任务与容器')!
      check.click(); await settle()
      const protection = host.querySelector('[aria-label="项目操作保护状态"]')!
      expect(protection.textContent).toContain('接收结果未知')
      expect(protection.textContent).toContain('容器清单未成功刷新')
      expect(protection.textContent).toContain('任务读取失败')
      expect(button('重启项目 demo-project').disabled).toBe(true)
    }
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
    app!.unmount(); app = undefined; await mount(); if (container) selectContainers(); await settle()
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


function composeInventory() { const value = inventory(); value.projects[0]!.kind = 'compose'; return value }
function composeResult() { return { projectId: 'demo', action: 'restart', state: 'running', completed: true, output: 'private-command-output', services: [{ name: 'web', containerId: 'new-container', state: 'running', running: true }] } }
async function readyCompose() {
  fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response(composeResult()) : response(composeInventory()))
  await mount()
}
describe('DockerView Compose 生命周期证据', () => {
  it('完成只报告读回观察，不显示命令日志', async () => {
    await readyCompose(); button('重启项目 demo-project').click(); await settle()
    expect(ElMessage.success).toHaveBeenCalledWith('项目“demo-project”重启命令已受理，读回时服务均运行中。')
    expect(host.textContent).not.toContain('private-command-output')
    expect(posts()).toHaveLength(1)
    expect(fetcher.mock.calls.filter(([, options]) => options?.method !== 'POST')).toHaveLength(2)
  })
  it.each(['incomplete', 'wrong-project', 'verify-failed', 'all-target-incomplete'])('未完成或未知保持警告与只读刷新 %s', async (mode) => {
    await readyCompose()
    fetcher.mockImplementation(async (_url, options) => {
      if (options?.method !== 'POST') return response(composeInventory())
      if (mode === 'verify-failed') return new Response(JSON.stringify({ code: 'COMPOSE_LIFECYCLE_VERIFY_FAILED', message: 'private-error 请重试', requestId: 'test' }), { status: 409 })
      return response(mode === 'wrong-project' ? { ...composeResult(), projectId: 'other' } : mode === 'all-target-incomplete' ? { ...composeResult(), completed: false } : { ...composeResult(), completed: false, state: 'stopped', services: [{ name: 'web', state: 'exited', running: false }] })
    })
    button('重启项目 demo-project').click(); await settle()
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(ElMessage.warning).toHaveBeenCalled()
    expect(host.querySelector('.action-error')?.textContent).toContain('只读核对')
    for (const selector of ['.project-row-feedback', '.docker-mobile-list .project-action-errors']) {
      const feedback = host.querySelector(selector)!
      expect(feedback.querySelector('strong')?.textContent).toBe('项目操作需核对')
      expect(feedback.textContent).not.toMatch(/项目操作失败|部分容器操作失败/)
      expect(feedback.querySelector('li')?.textContent).toContain(mode === 'incomplete' ? '状态复核尚未达到目标' : mode === 'all-target-incomplete' ? '项目回执未确认完成' : '可能已经执行')
    }
    expect(host.textContent).not.toMatch(/private-error|请重试|private-command-output/)
    expect(posts()).toHaveLength(1)
    expect(fetcher.mock.calls.filter(([, options]) => options?.method !== 'POST')).toHaveLength(2)
  })
  it('冻结确认前项目和配置，阻止重复点击', async () => {
    await readyCompose()
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const target = button('重启项目 demo-project'); target.click(); target.click(); await settle()
    store.inventory!.projects[0]!.id = 'changed'; store.inventory!.projects[0]!.workingDirectory = '/changed'; store.inventory!.projects[0]!.configFiles.push('later.yaml')
    confirmation.resolve(confirmed); await settle()
    expect(ElMessageBox.confirm).toHaveBeenCalledOnce()
    expect(posts()).toHaveLength(1)
    expect(JSON.parse(String(posts()[0]![1]?.body))).toEqual({ projectId: 'demo', action: 'restart', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'] })
    expect(posts()[0]![1]?.redirect).toBe('error')
  })
  it('未知回执遇到刷新失败仍保留原分类，不自动重发', async () => {
    await readyCompose()
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response({ ...composeResult(), services: [] }) : response({ invalid: true }))
    button('重启项目 demo-project').click(); await settle()
    expect(host.querySelector('.action-error')?.textContent).toContain('可能已经执行')
    expect(host.querySelector('.action-error')?.textContent).toContain('状态刷新失败')
    expect(ElMessage.success).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(15_000); await settle(); expect(posts()).toHaveLength(1)
  })
  it.each(['unmount', 'session'])('迟到回执在 %s 后不通知或刷新', async (mode) => {
    await readyCompose()
    const pending = deferred<Response>(); fetcher.mockReturnValueOnce(pending.promise)
    button('重启项目 demo-project').click(); await settle()
    if (mode === 'unmount') { app!.unmount(); app = undefined } else { invalidateSession(); beginSession() }
    pending.resolve(response(composeResult())); await settle()
    expect(ElMessage.success).not.toHaveBeenCalled(); expect(ElMessage.warning).not.toHaveBeenCalled()
    expect(fetcher).toHaveBeenCalledTimes(2); expect(posts()).toHaveLength(1)
  })
})


it('会话更换不重发旧项目请求，旧请求释放自身占用后新会话可操作', async () => {
  await readyCompose()
  const first = deferred<Response>()
  fetcher.mockReturnValueOnce(first.promise)
  button('重启项目 demo-project').click(); await settle()
  invalidateSession(); beginSession()
  expect(button('重启项目 demo-project').disabled).toBe(true)
  button('重启项目 demo-project').click(); await settle()
  expect(posts()).toHaveLength(1)
  first.resolve(response(composeResult())); await settle()
  expect(ElMessage.success).not.toHaveBeenCalled()
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(button('重启项目 demo-project').disabled).toBe(false)
  const second = deferred<Response>()
  fetcher.mockReturnValueOnce(second.promise)
  button('重启项目 demo-project').click(); await settle()
  expect(posts()).toHaveLength(2)
  expect(button('重启项目 demo-project').disabled).toBe(true)
  second.resolve(response(composeResult())); await settle()
  expect(ElMessage.success).toHaveBeenCalledOnce()
  expect(button('重启项目 demo-project').disabled).toBe(false)
})

// These tests exercise the real action gate with a mocked dialog decision; geometry
// and native focus/keyboard behavior belong to the separate browser evidence suite.
describe.each(['compose', 'standalone'] as const)('项目确认范围与取消门禁 %s', (kind) => {
  it.each(['stop', 'restart'] as const)('确认 %s 文案与专属样式，明确确认后仅派发一次', async (action) => {
    if (kind === 'compose') await readyCompose()
    else await ready(false)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const label = action === 'stop' ? '停止' : '重启'
    const target = button(`${label}项目 demo-project`)
    target.click(); target.click(); await settle()
    expect(ElMessageBox.confirm).toHaveBeenCalledOnce()
    expect(posts()).toHaveLength(0)
    const [message, title, options] = vi.mocked(ElMessageBox.confirm).mock.calls[0]!
    expect(title).toBe(`确认${label}项目`)
    const commonOptions = { confirmButtonText: `确认${label}`, cancelButtonText: '取消', type: action === 'stop' ? 'warning' : 'info' }
    if (kind === 'compose') {
      const interruption = action === 'stop' ? '服务会中断' : '期间服务会短暂中断'
      expect(message).toBe(`将通过 Compose ${label}项目“demo-project”。确认前最近一次清单观察到该项目 1 个容器；实际操作范围由执行时的 Compose 配置决定。${interruption}，镜像、卷、Compose 文件和工作目录不会删除。`)
      expect(options).toEqual({ ...commonOptions, customClass: 'ncp-compose-action-confirmation' })
    } else {
      expect(message).toContain(action === 'stop'
        ? '将停止项目“demo-project”的 1 个容器。容器会停止，但镜像、卷、Compose 文件和工作目录不会删除。'
        : '将重启项目“demo-project”的 1 个容器。容器会逐项重启，镜像、卷、Compose 文件和工作目录不会删除。')
      expect(message).toContain('冻结操作：'); expect(message).toContain('a'.repeat(64)); expect(message).toContain('已接收不代表完成')
      expect(options).toEqual({ ...commonOptions, customClass: 'ncp-container-action-confirmation' })
    }
    fetcher.mockImplementation(async (_url, init) => {
      if (init?.method !== 'POST') return response(String(_url).includes('/jobs/') ? projectJob(action) : kind === 'compose' ? composeInventory() : inventory())
      const state = action === 'stop' ? 'stopped' : 'running'
      return response(kind === 'compose'
        ? { ...composeResult(), action, state, services: [{ name: 'web', state: action === 'stop' ? 'exited' : 'running', running: action !== 'stop' }] }
        : projectJob(action), kind === 'compose' ? 200 : 202)
    })
    confirmation.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(1)
    if (kind === 'compose') { expect(JSON.parse(String(posts()[0]![1]?.body)).action).toBe(action); expect(ElMessage.success).toHaveBeenCalledOnce() }
    else { expect(posts()[0]![0]).toBe(`/api/v1/docker/projects/standalone/action-jobs/${action}`); expect(ElMessage.success).not.toHaveBeenCalled(); expect(ElMessage.info).toHaveBeenCalledWith(expect.stringContaining('尚未确认完成')) }
  })

  describe.each(['cancel', 'close'])('%s 不代表同意', (decision) => {
    it.each(['stop', 'restart'] as const)('%s 不派发或刷新，随后仍可确认', async (action) => {
      if (kind === 'compose') await readyCompose()
      else await ready(false)
      vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce(decision)
      const label = `${action === 'stop' ? '停止' : '重启'}项目 demo-project`
      button(label).click(); await settle()
      expect(posts()).toHaveLength(0)
      expect(fetcher).toHaveBeenCalledTimes(1)
      expect(ElMessage.success).not.toHaveBeenCalled()
      expect(button(label).disabled).toBe(false)
      button(label).click(); await settle()
      expect(posts()).toHaveLength(1)
    })
  })
})


it.each(['mismatch', 'all-target-incomplete'])('独立容器组异步结果保留核对与保护 %s', async (mode) => {
  await ready(false)
  fetcher.mockImplementation(async (url, options) => options?.method === 'POST'
    ? response(projectJob(), 202)
    : String(url).includes('/jobs/') ? response(mode === 'mismatch' ? projectJob('restart', 'stopped') : { ...projectJob('restart', 'running'), projectActionResult: undefined }) : response(inventory()))
  button('重启项目 demo-project').click(); await settle()
  const feedback = host.querySelector('[aria-label="项目操作保护状态"]')!
  expect(feedback.textContent).toContain(mode === 'mismatch' ? '该次调用回读与目标状态不符' : '任务读取失败')
  expect(button('重启项目 demo-project').disabled).toBe(true)
  expect(ElMessage.success).not.toHaveBeenCalled()
  expect(posts()).toHaveLength(1)
})


function focusMockDialog() {
  const dialogControl = document.createElement('button')
  host.append(dialogControl)
  dialogControl.focus()
}

// Mocked dialog tests cover application ownership and restoration guards only.
// The separate Playwright suite exercises the actual Element Plus focus trap.
describe.each(['compose', 'standalone', 'container', 'drawer'] as const)('生命周期取消焦点 %s', (kind) => {
  it.each(['cancel', 'close'] as const)('在 %s 后恢复触发点，保持无写请求', async (decision) => {
    if (kind === 'compose') await readyCompose()
    else if (kind === 'container' || kind === 'drawer') await readyContainerEntry(kind)
    else await ready(false)
    for (const action of ['停止', '重启']) {
      const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
      vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
      const target = button(kind === 'container' || kind === 'drawer' ? containerLabel(kind, action) : `${action}项目 demo-project`)
      target.focus(); target.click(); await settle()
      expect(target.disabled).toBe(true)
      focusMockDialog() // 模拟真实对话框已接管焦点，不替代浏览器焦点证据。
      expect(document.activeElement).not.toBe(target)
      confirmation.reject(decision); await settle()
      expect(target.disabled).toBe(false)
      expect(document.activeElement).toBe(target)
      expect(posts()).toHaveLength(0)
      expect(fetcher).toHaveBeenCalledTimes(1)
    }
  })
})
it('项目生命周期工具提示不拦截原生 Enter/Space', async () => {
  await readyCompose()
  for (const action of ['启动', '停止', '重启']) {
    expect(button(`${action}项目 demo-project`).parentElement?.dataset.tooltipTriggerKeys).toBe('[]')
  }
})
it.each(['stale-session', 'unmount', 'disconnected', 'disabled', 'unexpected-rejection', 'confirmed'] as const)('不向失效或非取消操作恢复焦点：%s', async (condition) => {
  await readyCompose()
  const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
  vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
  const target = button('停止项目 demo-project')
  target.focus(); target.click(); await settle(); focusMockDialog()
  const focus = vi.spyOn(target, 'focus')
  if (condition === 'stale-session') { invalidateSession(); beginSession() }
  if (condition === 'unmount') { app!.unmount(); app = undefined }
  if (condition === 'disconnected') target.remove()
  if (condition === 'disabled') {
    const observed = composeInventory()
    observed.containers[0]!.state = 'stopped'
    observed.projects[0]!.state = 'stopped'
    observed.projects[0]!.runningCount = 0
    store.inventory = observed; await settle()
  }
  if (condition === 'confirmed') confirmation.resolve(confirmed)
  else confirmation.reject(condition === 'unexpected-rejection' ? new Error('dialog unavailable') : 'cancel')
  await settle()
  expect(focus).not.toHaveBeenCalled()
  expect(posts()).toHaveLength(condition === 'confirmed' ? 1 : 0)
})
it('取消释放期间出现新的项目操作时不抢回焦点', async () => {
  await readyCompose()
  const first = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
  const second = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
  vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
  const stop = button('停止项目 demo-project'), restart = button('重启项目 demo-project')
  stop.focus(); stop.click(); await settle(); focusMockDialog()
  const focus = vi.spyOn(stop, 'focus')
  let started = false
  const observer = new MutationObserver(() => {
    if (!stop.disabled && !started) {
      started = true
      restart.click()
    }
  })
  observer.observe(stop, { attributes: true, attributeFilter: ['disabled'] })
  try {
    first.reject('cancel'); await settle()
    expect(started).toBe(true)
    expect(ElMessageBox.confirm).toHaveBeenCalledTimes(2)
    expect(restart.disabled).toBe(true)
    expect(focus).not.toHaveBeenCalled()
    expect(posts()).toHaveLength(0)
    second.reject('cancel'); await settle()
  } finally { observer.disconnect() }
})

function containerLabel(entry: 'container' | 'drawer', action: string) { return entry === 'drawer' ? `${action} demo-web` : `${action}容器` }
async function readyContainerEntry(entry: 'container' | 'drawer') {
  if (entry === 'drawer') routeQuery.project = 'demo'
  await ready(true)
}

describe.each(['container', 'drawer'] as const)('单容器键盘确认门禁 %s', (entry) => {
  it.each(['stop', 'restart'] as const)('%s 明确确认一次，重复点击不重发', async (action) => {
    await readyContainerEntry(entry)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const label = action === 'stop' ? '停止' : '重启'
    const target = button(containerLabel(entry, label))
    expect(target.parentElement?.dataset.tooltipTriggerKeys).toBe('[]')
    target.click(); target.click(); await settle()
    expect(ElMessageBox.confirm).toHaveBeenCalledOnce()
    expect(posts()).toHaveLength(0)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(target.parentElement?.dataset.tooltipDisabled).toBe('true')
    expect(ElMessageBox.confirm).toHaveBeenCalledWith(
      (action === 'stop' ? '将停止容器“demo-web”。容器、镜像和卷不会删除，之后可以再次启动。' : '将重启容器“demo-web”。容器、镜像和卷不会删除，期间服务会短暂中断。') + ` 完整容器 ID：${'a'.repeat(64)}。确认后提交后台任务，接收不代表完成，任务不能取消。`,
      `确认${label}容器`,
      { confirmButtonText: `确认${label}`, cancelButtonText: '取消', type: action === 'stop' ? 'warning' : 'info', customClass: 'ncp-container-action-confirmation' },
    )
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response({ ...actionJob(), type: `docker-container-${action}` }, 202) : String(_url).includes('/jobs/') ? response({ ...actionJob(), type: `docker-container-${action}` }) : response(inventory()))
    confirmation.resolve(confirmed); await settle()
    expect(posts()).toHaveLength(1)
    expect(posts()[0]![0]).toBe(`/api/v1/docker/containers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/action-jobs/${action}`)
    expect(posts()[0]![1]?.body).toBeUndefined()
    expect(ElMessage.info).toHaveBeenCalledOnce(); expect(ElMessage.success).not.toHaveBeenCalled()
    expect(fetcher).toHaveBeenCalledTimes(4)
    expect(target.parentElement?.dataset.tooltipDisabled).toBe('true')
  })
  it('启动无需确认，仅发送一次精确动作', async () => {
    await readyContainerEntry(entry)
    const observed = inventory(); observed.containers[0]!.state = 'stopped'
    store.inventory = observed; await settle()
    const target = button(containerLabel(entry, '启动'))
    expect(target.parentElement?.dataset.tooltipTriggerKeys).toBe('[]')
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response({ ...actionJob(), type: 'docker-container-start' }, 202) : String(_url).includes('/jobs/') ? response({ ...actionJob(), type: 'docker-container-start' }) : response(inventory()))
    target.click(); target.click(); await settle()
    expect(ElMessageBox.confirm).not.toHaveBeenCalled()
    expect(posts()).toHaveLength(1)
    expect(posts()[0]![0]).toBe('/api/v1/docker/containers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/action-jobs/start')
    expect(posts()[0]![1]?.body).toBeUndefined()
    expect(ElMessage.info).toHaveBeenCalledOnce(); expect(ElMessage.success).not.toHaveBeenCalled()
  })
  it.each(['stale-session', 'unmount', 'disconnected', 'disabled', 'unexpected-rejection', 'confirmed'] as const)('不向失效或非取消操作恢复焦点：%s', async (condition) => {
    await readyContainerEntry(entry)
    const confirmation = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(confirmation.promise)
    const target = button(containerLabel(entry, '重启'))
    target.focus(); target.click(); await settle(); focusMockDialog()
    const focus = vi.spyOn(target, 'focus')
    if (condition === 'stale-session') { invalidateSession(); beginSession() }
    if (condition === 'unmount') { app!.unmount(); app = undefined }
    if (condition === 'disconnected') target.remove()
    if (condition === 'disabled') {
      // 独立的原生禁用来源仍须被恢复保护识别。
      const observer = new MutationObserver(() => { if (!target.disabled) target.disabled = true })
      observer.observe(target, { attributes: true, attributeFilter: ['disabled'] })
      confirmation.reject('cancel'); await settle(); observer.disconnect()
    } else if (condition === 'confirmed') confirmation.resolve(confirmed)
    else confirmation.reject(condition === 'unexpected-rejection' ? new Error('dialog unavailable') : 'cancel')
    await settle()
    expect(focus).not.toHaveBeenCalled()
    expect(posts()).toHaveLength(condition === 'confirmed' ? 1 : 0)
    expect(fetcher).toHaveBeenCalledTimes(condition === 'confirmed' ? 4 : 1)
  })
  it('取消释放后新同名动作仍占用，旧操作不抢焦点', async () => {
    await readyContainerEntry(entry)
    const first = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    const second = deferred<Awaited<ReturnType<typeof ElMessageBox.confirm>>>()
    vi.mocked(ElMessageBox.confirm).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const target = button(containerLabel(entry, '重启'))
    target.focus(); target.click(); await settle(); focusMockDialog()
    const focus = vi.spyOn(target, 'focus')
    let started = false
    const observer = new MutationObserver(() => {
      if (!target.disabled && !started) { started = true; target.click() }
    })
    observer.observe(target, { attributes: true, attributeFilter: ['disabled'] })
    try {
      first.reject('cancel'); await settle()
      expect(started).toBe(true)
      expect(ElMessageBox.confirm).toHaveBeenCalledTimes(2)
      expect(target.disabled).toBe(true)
      expect(focus).not.toHaveBeenCalled()
      expect(posts()).toHaveLength(0)
      second.reject('close'); await settle()
      expect(target.disabled).toBe(false)
      expect(fetcher).toHaveBeenCalledTimes(1)
    } finally { observer.disconnect() }
  })
  it('详情单容器未知回执仍关联当前项目且不重放', async () => {
    await readyContainerEntry(entry)
    fetcher.mockImplementation(async (_url, options) => options?.method === 'POST' ? response({ invalid: true }) : response(inventory()))
    button(containerLabel(entry, '重启')).click(); await settle(); await settle()
    expect(posts()).toHaveLength(1)
    expect(ElMessage.success).not.toHaveBeenCalled()
    if (entry === 'drawer') expect(host.querySelector('.detail-section [role="alert"]')?.textContent).toContain('可能已经')
  })
})

describe('单容器 Job 页面闭环', () => {
  it('202只提示已接收，清单刷新或容器消失不清锁；同项目批量入口受保护', async () => {
    await ready()
    button('重启容器').click(); await settle()
    expect(posts()).toHaveLength(1)
    expect(ElMessage.success).not.toHaveBeenCalled()
    expect(button('停止容器').disabled).toBe(true)
    expect(button('重启容器').disabled).toBe(true)
    expect(button('查看容器详情与日志').disabled).toBe(false)
    const projectTab = [...host.querySelectorAll<HTMLButtonElement>('.section-tabs button')].find(item => item.textContent?.trim() === '项目')!
    projectTab.click(); await settle()
    expect(button('重启项目 demo-project').disabled).toBe(true)
    store.inventory = inventory(1000, true); await settle()
    const panel = host.querySelector('[aria-label="单容器操作防重复保护"]')!
    expect(panel.textContent).toContain('job-1')
    expect(panel.textContent).toContain('a'.repeat(64))
    expect([...panel.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === '解除本页防重复保护')!.disabled).toBe(true)
    expect(posts()).toHaveLength(1)
  })
  it('卸载后晚202在重挂载面板可见，不通知旧视图或重复POST', async () => {
    await ready()
    const pending = deferred<Response>(); fetcher.mockReturnValueOnce(pending.promise)
    button('重启容器').click(); await settle()
    app!.unmount(); app = undefined
    pending.resolve(response(actionJob(), 202)); await settle()
    expect(ElMessage.info).not.toHaveBeenCalled()
    await mount(); selectContainers(); await settle()
    expect(host.querySelector('[aria-label="单容器操作防重复保护"]')?.textContent).toContain('job-1')
    expect(button('重启容器').disabled).toBe(true)
    button('重启容器').click(); await settle(); expect(posts()).toHaveLength(1)
  })
  it('未知接收先只读核对再明确风险确认，本地解除不重发', async () => {
    await ready()
    fetcher.mockImplementation(async (url, options) => {
      if (options?.method === 'POST') throw new Error('offline')
      return String(url).includes('/jobs') ? response({ jobs: [] }) : response(inventory())
    })
    button('重启容器').click(); await settle()
    const panel = () => host.querySelector('[aria-label="单容器操作防重复保护"]')!
    const control = (text: string) => [...panel().querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === text)!
    expect(control('解除本页防重复保护').disabled).toBe(true)
    control('只读核对任务与状态').click(); await settle()
    expect(control('解除本页防重复保护').disabled).toBe(false)
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    const releaseButton = control('解除本页防重复保护'); releaseButton.focus()
    const restoreFocus = vi.spyOn(releaseButton, 'focus')
    releaseButton.click(); await settle()
    expect(restoreFocus).toHaveBeenCalledWith({ preventScroll: true })
    expect(panel()).not.toBeNull(); expect(posts()).toHaveLength(1)
    control('解除本页防重复保护').click(); await settle()
    expect(ElMessageBox.confirm).toHaveBeenLastCalledWith(expect.stringContaining('之前操作可能仍已执行'), '解除本页防重复保护', expect.objectContaining({ confirmButtonText: '确认风险并解除' }))
    expect(panel()).toBeNull(); expect(button('重启容器').disabled).toBe(false); expect(posts()).toHaveLength(1)
  })
  it('failed清单核对失败不解锁，精确completed才自动释放', async () => {
    await ready(); button('重启容器').click(); await settle()
    const panel = () => host.querySelector('[aria-label="单容器操作防重复保护"]')!
    fetcher.mockImplementation(async url => String(url).includes('/jobs/') ? response({ ...actionJob(), status: 'failed', completedAt: collectedAt, errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }) : response({ invalid: true }))
    panel().querySelector<HTMLButtonElement>('button')!.click(); await settle()
    expect(panel().textContent).toContain('DOCKER_CONTAINER_STATE_MISMATCH')
    expect([...panel().querySelectorAll<HTMLButtonElement>('button')][1]!.disabled).toBe(true)
    fetcher.mockImplementation(async url => String(url).includes('/jobs/') ? response({ ...actionJob(), status: 'completed', completedAt: collectedAt, progress: 100 }) : response(inventory()))
    panel().querySelector<HTMLButtonElement>('button')!.click(); await settle()
    expect(panel()).toBeNull(); expect(posts()).toHaveLength(1); expect(ElMessage.success).not.toHaveBeenCalled()
  })
})

it('手动数据刷新精确核对已知Job一次，卸载取消注册且不递归或POST', async () => {
  await ready(); button('重启容器').click(); await settle()
  const previous = fetcher.mock.calls.filter(([url]) => String(url).includes('/jobs/')).length
  await Promise.all([...refreshHandlers].map(handler => handler())); await settle()
  expect(fetcher.mock.calls.filter(([url]) => String(url).includes('/jobs/')).length).toBe(previous + 1)
  expect(posts()).toHaveLength(1)
  app!.unmount(); app = undefined; expect(refreshHandlers.size).toBe(0)
})

it.each(['manual', 'panel'] as const)('stale清单读取没有成功资源时不误称核对完成或解锁：%s', async source => {
  await ready(); button('重启容器').click(); await settle()
  fetcher.mockImplementation(async url => String(url).includes('/jobs/') ? response({ ...actionJob(), status: 'failed', completedAt: collectedAt, errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }) : response(inventory()))
  const refresh = vi.spyOn(store, 'refresh').mockResolvedValue({ requested: ['inventory'], succeeded: [], failed: [] })
  if (source === 'manual') await Promise.all([...refreshHandlers].map(handler => handler()))
  else host.querySelector<HTMLButtonElement>('[aria-label="单容器操作防重复保护"] button')!.click()
  await settle()
  const panel = host.querySelector('[aria-label="单容器操作防重复保护"]')!
  expect(panel.textContent).toContain('容器清单刷新失败')
  expect(panel.textContent).not.toContain('容器清单已刷新')
  expect([...panel.querySelectorAll<HTMLButtonElement>('button')][1]!.disabled).toBe(true)
  expect(button('重启容器').disabled).toBe(true)
  expect(posts()).toHaveLength(1)
  expect(refresh).toHaveBeenCalledWith({ inventory: true, ...(source === 'panel' ? { force: true } : {}) })
  refresh.mockRestore()
})
