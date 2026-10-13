// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { mockReducedMotion } from '@/test/reducedMotion'
import TerminalView from './TerminalView.vue'

const mocks = vi.hoisted(() => ({ terminals: [] as { options: { cursorBlink: boolean }; dispose: ReturnType<typeof vi.fn> }[] }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ inventory: null, refresh: vi.fn() }) }))
vi.mock('element-plus', () => ({ ElDialog: { render: () => null } }))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    options: { cursorBlink: boolean }
    rows = 24
    cols = 80
    dispose = vi.fn()
    constructor(options: { cursorBlink: boolean }) { this.options = options; mocks.terminals.push(this) }
    loadAddon() {}
    open() {}
    attachCustomKeyEventHandler() {}
    writeln() {}
    clear() {}
    focus() {}
    onData() {}
    onResize() {}
  },
}))
let app: App | undefined
let host: HTMLDivElement

afterEach(() => {
  app?.unmount()
  app = undefined
  host?.remove()
  mocks.terminals.length = 0
  vi.unstubAllGlobals()
})

describe('终端动态减少动效', () => {
  it.each([true, false])('初始偏好 %s 与连接前后变化只更新光标，重连使用当前偏好', async (initial) => {
    const mock = mockReducedMotion(initial)
    const sockets: { close: ReturnType<typeof vi.fn>; onmessage?: (event: { data: string }) => void }[] = []
    vi.stubGlobal('WebSocket', class {
      static OPEN = 1
      readyState = 0
      onmessage?: (event: { data: string }) => void
      close = vi.fn()
      constructor() { sockets.push(this) }
    })
    vi.stubGlobal('ResizeObserver', class { observe() {}; disconnect() {} })
    host = document.createElement('div')
    document.body.append(host)
    app = createApp({ render: () => h(TerminalView) })
    app.mount(host)
    const connect = () => [...host.querySelectorAll('button')].find((button) => button.textContent?.includes('连接终端'))!.click()
    connect()
    await nextTick()
    await nextTick()
    const terminal = mocks.terminals[0]!
    expect(terminal.options.cursorBlink).toBe(!initial)
    for (const reduce of [!initial, initial]) {
      mock.change(reduce)
      await nextTick()
      expect(terminal.options.cursorBlink).toBe(!reduce)
      expect(mocks.terminals).toHaveLength(1)
      expect(sockets).toHaveLength(1)
      expect(sockets[0]!.close).not.toHaveBeenCalled()
      expect(terminal.dispose).not.toHaveBeenCalled()
    }
    sockets[0]!.onmessage!({ data: JSON.stringify({ type: 'started', shell: 'bash' }) })
    await nextTick()
    mock.change(!initial)
    await nextTick()
    expect(terminal.options.cursorBlink).toBe(initial)
    expect(sockets).toHaveLength(1)
    ;[...host.querySelectorAll('button')].find((button) => button.textContent?.includes('断开连接'))!.click()
    await nextTick()
    expect(terminal.dispose).toHaveBeenCalledTimes(1)
    mock.change(initial)
    await nextTick()
    expect(terminal.options.cursorBlink).toBe(initial)
    connect()
    await nextTick()
    await nextTick()
    expect(mocks.terminals).toHaveLength(2)
    const reconnected = mocks.terminals[1]!
    expect(reconnected.options.cursorBlink).toBe(!initial)
    app.unmount()
    app = undefined
    expect(reconnected.dispose).toHaveBeenCalledTimes(1)
    expect(terminal.dispose).toHaveBeenCalledTimes(1)
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
    expect(mock.media.removeEventListener).toHaveBeenCalledTimes(1)
    mock.change(!initial)
    await nextTick()
    expect(terminal.options.cursorBlink).toBe(initial)
    expect(reconnected.options.cursorBlink).toBe(!initial)
  })
})
