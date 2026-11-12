// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import AppShell from './AppShell.vue'
vi.mock('element-plus', () => ({ ElTooltip: { inheritAttrs: false, setup: (_: unknown, { slots }: { slots: { default: () => unknown } }) => () => slots.default() } }))
vi.mock('@/components/TaskCenterDrawer.vue', () => ({ __esModule: true, default: { emits: ['close'], setup: (_: unknown, { emit }: { emit: (event: string) => void }) => () => h('section', { 'data-testid': 'task-drawer' }, [h('button', { onClick: () => emit('close') }, 'close fixture')]) } }))
let app: App | undefined
let host: HTMLDivElement
async function flush() { for (let i = 0; i < 8; i++) await new Promise<void>(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount() {
  vi.stubGlobal('matchMedia', () => Object.assign(new EventTarget(), { matches: true }))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] }); await router.push('/'); await router.isReady()
  host = document.createElement('div'); document.body.append(host); const logout = vi.fn()
  app = createApp({ render: () => h(AppShell, { deviceName: 'fixture', connectionState: 'connected', realtimeState: 'streaming', refreshIntervalSeconds: 5, userName: 'root', onLogout: logout }, () => h('input', { id: 'unsaved', value: 'draft' })) }); app.use(router).mount(host); await nextTick(); return logout
}
function find(selector: string) { return host.querySelector<HTMLElement>(selector)! }
afterEach(() => { app?.unmount(); host?.remove(); vi.unstubAllGlobals() })
describe('AppShell 任务入口', () => {
  it('按需挂载，保留草稿，关闭恢复焦点', async () => {
    await mount(); expect(host.querySelector('[data-testid="task-drawer"]')).toBeNull(); const draft = find('#unsaved'); find('.task-center-button').click(); await flush()
    expect(find('[data-testid="task-drawer"]')).toBeTruthy(); expect(find('#unsaved')).toBe(draft); find('[data-testid="task-drawer"] button').click(); await nextTick()
    expect(host.querySelector('[data-testid="task-drawer"]')).toBeNull(); expect(document.activeElement).toBe(find('.task-center-button'))
  })
  it('移动菜单互斥且保持背景隔离规则', async () => {
    await mount(); find('.menu-button').click(); await nextTick(); expect(find('.app-stage').hasAttribute('inert')).toBe(true)
    find('.task-center-button').click(); await flush(); expect(find('.app-stage').hasAttribute('inert')).toBe(false); expect(document.body.classList.contains('mobile-navigation-open')).toBe(false)
    find('.menu-button').click(); await nextTick(); expect(host.querySelector('[data-testid="task-drawer"]')).toBeNull(); expect(document.activeElement).toBe(find('.sidebar-close'))
  })
  it('退出立即卸载', async () => {
    const logout = await mount(); find('.task-center-button').click(); await flush(); find('.sidebar-footer__logout').click(); await nextTick(); expect(host.querySelector('[data-testid="task-drawer"]')).toBeNull(); expect(logout).toHaveBeenCalledOnce()
  })
})
