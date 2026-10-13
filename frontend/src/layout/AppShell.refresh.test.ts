// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import AppShell from './AppShell.vue'

vi.mock('vue-router', () => ({
  RouterLink: { render: () => null },
  useRoute: () => ({ path: '/', meta: { title: '总览' }, params: {}, query: {} }),
}))
let app: App | undefined
let host: HTMLDivElement
async function flush() { await nextTick(); await vi.advanceTimersByTimeAsync(250); await nextTick() }
async function mount() {
  vi.useFakeTimers()
  vi.stubGlobal('matchMedia', () => Object.assign(new EventTarget(), { matches: false }))
  const props = reactive({ deviceName: 'fixture', connectionState: 'connected' as const, realtimeState: 'streaming' as const, refreshIntervalSeconds: 5, userName: 'root', liveDataActive: true, isRefreshing: false })
  const refresh = vi.fn()
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(AppShell, { ...props, onRefresh: refresh }) }); app.mount(host)
  await flush()
  return { props, refresh, button: host.querySelector<HTMLButtonElement>('button[aria-label="立即刷新数据"]')! }
}
const mouse = (element: HTMLElement, type: 'mouseenter' | 'mouseleave') => element.dispatchEvent(new MouseEvent(type))
function help(button: HTMLButtonElement) {
  const id = button.getAttribute('aria-describedby')
  expect(id).toBeTruthy()
  const tooltip = host.querySelector<HTMLElement>(`[id="${id}"]`)!
  expect(tooltip?.getAttribute('role')).toBe('tooltip')
  expect(tooltip?.getAttribute('aria-hidden')).toBe('false')
  expect(tooltip?.parentElement).toBe(host.querySelector('header.topbar'))
  expect(tooltip?.textContent).toBe('立即刷新数据')
  return tooltip
}
function escape() {
  const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
  document.dispatchEvent(event)
  expect(event.defaultPrevented).toBe(false)
}
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals() })
describe('AppShell 刷新帮助', () => {
  it('真实帮助属于现有 header，保留焦点并可 Escape 关闭/重新打开', async () => {
    const { button } = await mount()
    button.focus(); await flush()
    const tooltip = help(button)
    expect(host.querySelectorAll('header')).toHaveLength(1)
    expect(host.querySelectorAll('main')).toHaveLength(1)
    escape(); await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    expect(tooltip.isConnected).toBe(false)
    expect(document.activeElement).toBe(button)
    button.blur(); button.focus(); await flush(); help(button)
  })
  it('焦点与按钮/完整提示悬停相互保留，全部离开才关闭', async () => {
    const { button } = await mount()
    button.focus(); mouse(button, 'mouseenter'); await flush()
    mouse(button, 'mouseleave'); await flush()
    let tooltip = help(button)
    mouse(tooltip, 'mouseenter'); button.blur(); await flush()
    tooltip = help(button)
    mouse(tooltip, 'mouseleave'); await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    mouse(button, 'mouseenter'); button.focus(); await flush()
    button.blur(); await flush(); help(button)
    mouse(button, 'mouseleave'); await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
  })
  it('仅悬停及待显示计时器也可 Escape 关闭；Enter/Space 保持原生行为', async () => {
    const { button, refresh } = await mount()
    mouse(button, 'mouseenter'); escape(); await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    mouse(button, 'mouseleave'); mouse(button, 'mouseenter'); await flush(); help(button)
    escape(); await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    button.focus(); await flush(); help(button)
    for (const [key, code] of [['Enter', 'Enter'], [' ', 'Space'], ['Enter', 'NumpadEnter']]) {
      const event = new KeyboardEvent('keydown', { key, code, bubbles: true, cancelable: true })
      button.dispatchEvent(event); expect(event.defaultPrevented).toBe(false)
    }
    button.click(); expect(refresh).toHaveBeenCalledOnce()
  })
  it('loading 清理帮助和旧交互，恢复后由新交互打开；移除按钮及卸载清理提示', async () => {
    const { button, props, refresh } = await mount()
    button.focus(); await flush(); const tooltip = help(button)
    props.isRefreshing = true; await flush()
    expect(button.disabled).toBe(true)
    expect(tooltip.isConnected).toBe(false)
    mouse(button, 'mouseenter'); button.click(); await flush()
    expect(refresh).not.toHaveBeenCalled()
    props.isRefreshing = false; await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    button.blur(); button.focus(); await flush(); help(button)
    props.liveDataActive = false; await flush()
    expect(host.querySelector('[role="tooltip"]')).toBeNull()
    props.liveDataActive = true; await flush()
    const next = host.querySelector<HTMLButtonElement>('button[aria-label="立即刷新数据"]')!
    expect(next.hasAttribute('aria-describedby')).toBe(false)
    next.focus(); await flush(); const finalHelp = help(next)
    const remove = vi.spyOn(window, 'removeEventListener')
    app!.unmount(); app = undefined
    expect(finalHelp.isConnected).toBe(false)
    expect(remove).toHaveBeenCalledWith('keydown', expect.any(Function))
  })
})
