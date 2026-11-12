// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ElTooltip, ElMessageBox } from 'element-plus'
import { createApp, h, nextTick, type App } from 'vue'
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
  const actual = await vi.importActual<typeof import('element-plus')>('element-plus')
  const stub = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
  return { ElDrawer: defineComponent({ props: { modelValue: Boolean }, setup: (props, { slots }) => () => props.modelValue ? h('aside', [slots.header?.({ titleId: 'docker-view-drawer-test-title' }), slots.default?.()]) : null }), ElInput: stub, ElTooltip: actual.ElTooltip, ElMessage: { info: vi.fn(), error: vi.fn(), warning: vi.fn(), success: vi.fn() }, ElMessageBox: { confirm: vi.fn() } }
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
    engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'amd64', containers: empty ? 0 : 1, containersRunning: 0, containersStopped: empty ? 0 : 1, images: 0 },
  }
}
function response(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }) }
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

async function ready() { fetcher.mockImplementation(async (_url, options) => options?.method === 'DELETE' ? response({ code: 'DOCKER_UNAVAILABLE', message: 'unknown', requestId: 'test' }, 503) : response(inventory())); await mount() }
// 真实 Element Plus 处理 cancelable 冒泡事件；jsdom 不会自动合成原生按钮点击。
// 本回归证明 tooltip 不再拦截浏览器默认动作；真实 Tab/Enter/取消仍由 E2E 保留。
describe('项目删除真实 Tooltip 键盘默认动作', () => {
  for (const surface of ['desktop', 'mobile'] as const) {
    it.each([{ key: 'Enter', code: 'Enter' }, { key: ' ', code: 'Space' }])(`${surface} $code 不被删除 tooltip 吞掉`, async ({ key, code }) => {
      // 对照旧默认行为，同样是 span 包裹的真实按钮与真实 ElTooltip。
      const baselineHost = document.createElement('div'); document.body.append(baselineHost)
      const baseline = createApp({ render: () => h(ElTooltip, { content: '删除说明' }, { default: () => h('span', [h('button', { type: 'button' }, '删除')]) }) })
      try {
        baseline.mount(baselineHost); await nextTick()
        const button = baselineHost.querySelector('button')!
        button.focus(); expect(document.activeElement).toBe(button)
        const event = new KeyboardEvent('keydown', { key, code, bubbles: true, cancelable: true })
        expect(button.dispatchEvent(event)).toBe(false)
        expect(event.defaultPrevented).toBe(true)
      } finally { baseline.unmount(); baselineHost.remove() }
      await ready()
      const selector = surface === 'desktop' ? '.project-row button[aria-label="删除项目 demo-project"]' : '.mobile-project .project-action-tooltip-target button'
      const target = host.querySelector<HTMLButtonElement>(selector)!
      expect(target).not.toBeNull(); expect(target.disabled).toBe(false)
      target.focus(); expect(document.activeElement).toBe(target)
      const event = new KeyboardEvent('keydown', { key, code, bubbles: true, cancelable: true })
      expect(target.dispatchEvent(event)).toBe(true)
      expect(event.defaultPrevented).toBe(false)
      expect(document.activeElement).toBe(target)
      expect(ElMessageBox.confirm).not.toHaveBeenCalled()
      expect(fetcher.mock.calls.filter(([, options]) => options?.method === 'DELETE')).toHaveLength(0)
    })
  }
})
