// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import AppShell from './AppShell.vue'

vi.mock('element-plus', () => ({ ElTooltip: { inheritAttrs: false, setup: (_: unknown, { slots }: { slots: { default: () => unknown } }) => () => slots.default() } }))
let app: App | undefined
let host: HTMLDivElement
let viewport: EventTarget & { matches: boolean; media: string }
async function mount(mobile = true, path = '/') {
  viewport = Object.assign(new EventTarget(), { matches: mobile, media: '(max-width: 768px)' })
  vi.stubGlobal('matchMedia', vi.fn(() => viewport))
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockImplementation(function (this: HTMLElement) {
    return (this.classList.contains('sidebar-collapse') ? [] : [{}]) as unknown as DOMRectList
  })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] })
  await router.push(path); await router.isReady()
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(AppShell, {
    deviceName: 'fixture', connectionState: 'connected', realtimeState: 'streaming',
    refreshIntervalSeconds: 5, userName: 'root',
  }, () => h('button', { id: 'fixture-background' }, 'background')) })
  app.use(router).mount(host); await nextTick()
  return router
}
const find = (selector: string) => host.querySelector<HTMLElement>(selector)!
async function open() { find('.menu-button').click(); await nextTick() }
async function resize(matches: boolean) {
  viewport.matches = matches
  viewport.dispatchEvent(Object.assign(new Event('change'), { matches })); await nextTick()
}
function key(key: string, shiftKey = false) {
  window.dispatchEvent(new KeyboardEvent('keydown', { key, shiftKey, cancelable: true }))
}
afterEach(() => {
  app?.unmount(); app = undefined; host?.remove()
  vi.restoreAllMocks(); vi.unstubAllGlobals()
})
describe('AppShell 移动导航', () => {
  it('关闭时 inert，打开后隔离背景，双向循环并通过 Escape 返回按钮', async () => {
    await mount()
    expect(find('.app-sidebar').hasAttribute('inert')).toBe(true)
    await open()
    expect(document.activeElement).toBe(find('.sidebar-close'))
    expect(find('.app-sidebar').getAttribute('role')).toBe('dialog')
    expect(find('.app-stage').hasAttribute('inert')).toBe(true)
    expect(find('.skip-link').hasAttribute('inert')).toBe(true)
    find('.sidebar-footer__logout').focus(); key('Tab')
    expect(document.activeElement).toBe(find('.brand'))
    key('Tab', true)
    expect(document.activeElement).toBe(find('.sidebar-footer__logout'))
    find('#fixture-background').focus()
    expect(document.activeElement).toBe(find('.sidebar-close'))
    key('Escape'); await nextTick()
    expect(document.activeElement).toBe(find('.menu-button'))
    expect(find('.app-stage').hasAttribute('inert')).toBe(false)
    expect(document.body.classList.contains('mobile-navigation-open')).toBe(false)
  })
  it('遮罩关闭恢复焦点；同页选择关闭并聚焦 main', async () => {
    await mount(); await open()
    find('.navigation-scrim').click(); await nextTick()
    expect(document.activeElement).toBe(find('.menu-button'))
    find('.app-sidebar').scrollTop = 300
    await open()
    expect(find('.app-sidebar').scrollTop).toBe(0)
    find('.navigation__item').click(); await nextTick()
    expect(document.activeElement).toBe(find('#app-main'))
    expect(find('.app-sidebar').hasAttribute('inert')).toBe(true)
  })
  it('实时跨断点释放背景；隐藏的移动按钮不会保留焦点', async () => {
    await mount(); await open(); await resize(false)
    expect(find('.app-sidebar').hasAttribute('inert')).toBe(false)
    expect(find('.app-stage').hasAttribute('inert')).toBe(false)
    expect(document.activeElement).toBe(find('#app-main'))
    find('.brand').focus(); await resize(true)
    expect(document.activeElement).toBe(find('.menu-button'))
    expect(find('.app-sidebar').hasAttribute('inert')).toBe(true)
  })
  it('CSS 先隐藏并 blur、随后派发断点事件时仍恢复到菜单按钮', async () => {
    await mount(false)
    const link = find('a.navigation__item[href="/databases"]')
    link.focus()
    // 模拟浏览器先应用新媒体查询样式，再发送 matchMedia change。
    viewport.matches = true
    link.blur()
    expect(document.activeElement).toBe(document.body)
    await resize(true)
    expect(document.activeElement).toBe(find('.menu-button'))
  })
  it('移动关闭按钮先被桌面 CSS 隐藏并 blur 时恢复到 main', async () => {
    await mount(); await open()
    viewport.matches = false
    find('.sidebar-close').blur()
    await resize(false)
    expect(document.activeElement).toBe(find('#app-main'))
  })
  it('普通 blur 不留下可在之后断点变化中抢焦点的记录', async () => {
    await mount(false)
    find('.brand').focus(); find('.brand').blur()
    await resize(true)
    expect(document.activeElement).toBe(document.body)
  })
  it('断点恢复前已进入主内容时不使用旧侧栏焦点', async () => {
    await mount(false)
    find('.brand').focus()
    viewport.matches = true
    find('.brand').blur(); find('#fixture-background').focus()
    await resize(true)
    expect(document.activeElement).toBe(find('#fixture-background'))
  })
  it('断点回调到渲染完成期间的新焦点优先于待恢复焦点', async () => {
    await mount(false)
    find('.brand').focus()
    const resized = resize(true)
    find('#fixture-background').focus()
    await resized
    expect(document.activeElement).toBe(find('#fixture-background'))
  })
  it('快速往返断点后的过期事件不能再次隐藏桌面侧栏', async () => {
    await mount(false)
    find('.brand').focus()
    const enter = resize(true)
    const leave = resize(false)
    await Promise.all([enter, leave])
    viewport.dispatchEvent(Object.assign(new Event('change'), { matches: true }))
    await nextTick()
    expect(find('.app-sidebar').hasAttribute('inert')).toBe(false)
    expect(document.activeElement).toBe(find('.brand'))
  })
  it('数据库子路由保留模块标注，换路由聚焦 main', async () => {
    const router = await mount(true, '/databases/sqlite-main/tables/settings')
    const link = find('a.navigation__item[href="/databases"]')
    expect(link.classList.contains('navigation__item--active')).toBe(true)
    expect(link.getAttribute('aria-current')).toBe('location')
    await open(); await router.push('/settings'); await nextTick()
    expect(find('.app-stage').hasAttribute('inert')).toBe(false)
    expect(find('.app-sidebar').hasAttribute('inert')).toBe(true)
    expect(document.activeElement).toBe(find('#app-main'))
  })
  it('菜单清除同页 query 时仍聚焦 main', async () => {
    const router = await mount(true, '/settings?tab=display')
    await open(); await router.push('/settings'); await nextTick()
    expect(document.activeElement).toBe(find('#app-main'))
    expect(find('.app-sidebar').hasAttribute('inert')).toBe(true)
  })
  it('卸载移除监听和 body 标记，作废待执行的焦点回调', async () => {
    await mount()
    const remove = vi.spyOn(viewport, 'removeEventListener')
    find('.menu-button').click(); app?.unmount(); app = undefined; await nextTick()
    expect(document.body.classList.contains('mobile-navigation-open')).toBe(false)
    expect(remove).toHaveBeenCalledWith('change', expect.any(Function))
    key('Escape'); await resize(false)
    expect(document.body.classList.contains('mobile-navigation-open')).toBe(false)
  })
})
