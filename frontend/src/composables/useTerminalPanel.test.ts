// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, onBeforeUnmount, type App } from 'vue'
import { mockReducedMotion } from '@/test/reducedMotion'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { useReducedMotion } from './useReducedMotion'
import { useTerminalPanel } from './useTerminalPanel'
import { createTerminalPageOwner } from '@/domain/terminalPageOwner'
import { TERMINAL_OUTPUT_FRAME_BYTES } from '@/domain/terminalBudget'

const mocks = vi.hoisted(() => ({ terminals: [] as FakeTerminal[], fits: [] as { fit: ReturnType<typeof vi.fn> }[], fail: '' }))
type FakeTerminal = {
  options: { cursorBlink: boolean; scrollback: number }
  data: (data: string) => void
  resize: (size: { rows: number; cols: number }) => void
  key: (event: KeyboardEvent) => boolean
  dispose: ReturnType<typeof vi.fn>
  inputDispose: ReturnType<typeof vi.fn>
  resizeDispose: ReturnType<typeof vi.fn>
  write: ReturnType<typeof vi.fn>
  consumed: (() => void)[]
  focus: ReturnType<typeof vi.fn>
  clear: ReturnType<typeof vi.fn>
}
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ inventory: { containers: [{ id: 'fake-container', name: 'fake', image: 'fake', state: 'running' }] }, refresh: vi.fn() }) }))
vi.mock('element-plus', () => ({ ElDialog: { render: () => null } }))
vi.mock('@/components/NcpSelect.vue', () => ({ default: { props: ['disabled', 'modelValue'], emits: ['update:modelValue'], template: `<select :disabled="disabled" @change="$emit('update:modelValue', 'fake-container')"><option>fake</option></select>` } }))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class {
  fit = vi.fn(() => { if (mocks.fail === 'fit') throw Error('fake fit') })
  constructor() { mocks.fits.push(this) }
} }))
vi.mock('@xterm/xterm', () => ({ Terminal: class {
  options: FakeTerminal['options']
  rows = 24
  cols = 80
  data: FakeTerminal['data'] = () => {}
  resize: FakeTerminal['resize'] = () => {}
  key: FakeTerminal['key'] = () => true
  dispose = vi.fn(() => { if (mocks.fail === 'dispose') throw Error('fake dispose') })
  inputDispose = vi.fn(() => { if (mocks.fail === 'inputDispose') throw Error('fake input dispose') })
  resizeDispose = vi.fn()
  consumed: (() => void)[] = []
  write = vi.fn((_data: Uint8Array, callback?: () => void) => {
    if (callback) this.consumed.push(callback)
    if (mocks.fail === 'write') throw Error('fake write')
  })
  clear = vi.fn()
  focus = vi.fn()
  constructor(options: FakeTerminal['options']) {
    if (mocks.fail === 'construct') throw Error('fake construct')
    this.options = options
    mocks.terminals.push(this)
  }
  loadAddon() {}
  open() { if (mocks.fail === 'open') throw Error('fake open') }
  attachCustomKeyEventHandler(callback: FakeTerminal['key']) { this.key = callback }
  writeln() { if (mocks.fail === 'writeln') throw Error('fake writeln') }
  onData(callback: FakeTerminal['data']) { this.data = callback; return { dispose: this.inputDispose } }
  onResize(callback: FakeTerminal['resize']) { this.resize = callback; return { dispose: this.resizeDispose } }
  scrollToBottom() {}
} }))
class FakeSocket {
  static OPEN = 1
  readyState = 0
  bufferedAmount = 0
  binaryType = ''
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null
  send = vi.fn(() => { if (mocks.fail === 'send') throw Error('fake send') })
  close = vi.fn(() => { if (mocks.fail === 'close') throw Error('fake close') })
  constructor(readonly url: string) {
    if (mocks.fail === 'socket') throw Error('fake socket')
    sockets.push(this)
  }
  control(control: object) { this.onmessage?.({ data: JSON.stringify(control) }) }
  started(capabilities = { resize: true, multilinePaste: true, bracketedPaste: true }) {
    this.readyState = FakeSocket.OPEN
    this.control({ type: 'started', shell: 'bash', capabilities })
  }
  closed() { this.readyState = 3; this.onclose?.() }
}
let sockets: FakeSocket[]
let observers: { callback: () => void; disconnect: ReturnType<typeof vi.fn> }[]
let app: App | undefined
let host: HTMLDivElement
let motion: ReturnType<typeof mockReducedMotion>
let panels: ReturnType<typeof useTerminalPanel>[]
let page: ReturnType<typeof createTerminalPageOwner>
function mount(initial = false) {
  motion = mockReducedMotion(initial)
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ setup() {
    page = createTerminalPageOwner()
    const abort = () => page.invalidate('认证失效', true)
    page.session.signal.addEventListener('abort', abort)
    onBeforeUnmount(() => { page.invalidate('卸载', true); page.session.signal.removeEventListener('abort', abort) })
    panels = ['a', 'b'].map(tabId => useTerminalPanel({ tabId, page,
      containers: ref([{ id: 'fake-container', name: 'fake', image: 'fake', state: 'running' }]),
      reducedMotion: useReducedMotion(),
    }))
    page.select('a')
    return () => h('div', panels.map((panel, index) => h('section', { 'data-panel': index }, [
      h('div', { class: 'terminal-canvas', ref: panel.terminalElement }),
      h('button', { ref: panel.pasteConfirmRef }, '确认'),
    ])))
  } })
  app.mount(host)
}
async function flush() { await nextTick(); await nextTick() }
function paste(text = 'first\nsecond', element = host.querySelector('.terminal-canvas')!) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { getData: () => text } })
  element.dispatchEvent(event)
}
beforeEach(() => {
  beginSession()
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(800)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600)
  vi.useFakeTimers()
  sockets = []
  observers = []
  mocks.fail = ''
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.stubGlobal('ResizeObserver', class {
    disconnect = vi.fn()
    constructor(readonly callback: () => void) { observers.push(this) }
    observe() {}
  })
})
afterEach(() => {
  app?.unmount()
  app = undefined
  host?.remove()
  mocks.terminals.length = 0
  mocks.fits.length = 0
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('独立面板 owner，尚无标签 UI', () => {
  it('隐藏面板持续有界输出但不能输入、resize 或 focus；能力互不串用', async () => {
    mount(); await panels[0]!.connect()
    page.select('b'); await panels[1]!.connect()
    sockets[0]!.started({ resize: false, multilinePaste: false, bracketedPaste: false })
    sockets[1]!.started()
    await flush(); vi.advanceTimersByTime(80)
    const hidden = mocks.terminals[0]!
    expect(hidden.focus).not.toHaveBeenCalled()
    const fitCount = mocks.fits[0]!.fit.mock.calls.length
    hidden.data('ignored'); hidden.resize({ rows: 20, cols: 50 }); observers[0]!.callback()
    vi.advanceTimersByTime(80)
    expect(mocks.fits[0]!.fit).toHaveBeenCalledTimes(fitCount)
    expect(sockets[0]!.send).not.toHaveBeenCalled()
    expect(panels[0]!.capabilities.value?.resize).toBe(false)
    expect(panels[1]!.capabilities.value?.resize).toBe(true)
    for (let index = 0; index < 16; index++) sockets[0]!.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    expect(hidden.write).toHaveBeenCalledTimes(16)
    sockets[0]!.onmessage!({ data: new ArrayBuffer(1) })
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
    expect(sockets[1]!.close).not.toHaveBeenCalled()
    mocks.terminals[1]!.data('kept')
    expect(sockets[1]!.send).toHaveBeenCalledWith(new TextEncoder().encode('kept'))
    expect(page.occupiedCount).toBe(2)
  })
  it('切换立即清除粘贴及延迟聚焦；切回不复活，零连接与零输入', async () => {
    mount(); await panels[0]!.connect(); sockets[0]!.started()
    paste(); expect(panels[0]!.pendingPaste.value).not.toBeNull()
    const focus = vi.spyOn(HTMLElement.prototype, 'focus')
    page.select('b'); await flush()
    expect(panels[0]!.pendingPaste.value).toBeNull()
    expect(focus).not.toHaveBeenCalled()
    page.select('a'); await flush(); panels[0]!.confirmPaste()
    expect(sockets).toHaveLength(1)
    expect(sockets[0]!.send).not.toHaveBeenCalled()
  })
  it('删除面板清理显示资源，准确 onclose 才释放退役计数', async () => {
    mount(); await panels[0]!.connect(); sockets[0]!.started()
    const close = sockets[0]!.onclose!
    panels[0]!.detach()
    expect(mocks.terminals[0]!.dispose).toHaveBeenCalledTimes(1)
    expect(page.occupiedCount).toBe(1)
    page.select('b'); await panels[1]!.connect(); sockets[1]!.started()
    close(); close()
    expect(page.occupiedCount).toBe(1)
    expect(sockets[1]!.close).not.toHaveBeenCalled()
    expect(panels[1]!.state.value).toBe('connected')
  })
  it.each(['abort', 'hide'])('%s 封闭全部面板，保留关闭计数，无恢复连接', async (reason) => {
    mount(); await panels[0]!.connect(); sockets[0]!.started()
    page.select('b'); await panels[1]!.connect(); sockets[1]!.started()
    if (reason === 'abort') invalidateSession()
    else page.invalidate('离开页面')
    for (const terminal of mocks.terminals) {
      terminal.data('ignored'); terminal.resize({ rows: 30, cols: 90 })
      expect(terminal.dispose).toHaveBeenCalledTimes(1)
    }
    for (const socket of sockets) expect(socket.send).toHaveBeenCalledTimes(1)
    expect(page.occupiedCount).toBe(2)
    page.show(); await flush()
    expect(sockets).toHaveLength(2)
    sockets.forEach(socket => socket.closed())
    expect(page.occupiedCount).toBe(0)
  })
  it('用户聚焦其他控件后 started 不抢焦点；零布局不 fit', async () => {
    mount()
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(0)
    await panels[0]!.connect()
    expect(mocks.fits[0]!.fit).not.toHaveBeenCalled()
    host.querySelector('button')!.focus()
    sockets[0]!.started(); await flush(); vi.advanceTimersByTime(80)
    expect(mocks.terminals[0]!.focus).not.toHaveBeenCalled()
    expect(mocks.fits[0]!.fit).not.toHaveBeenCalled()
  })
  it('不同目标快照及旧 write 回调互不影响，隐藏实例跟随减少动效', async () => {
    mount(); await panels[0]!.connect(); sockets[0]!.started()
    sockets[0]!.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    const consumed = mocks.terminals[0]!.consumed[0]!
    page.select('b')
    panels[1]!.target.value = 'container'; panels[1]!.containerId.value = 'fake-container'
    await panels[1]!.connect(); sockets[1]!.started()
    expect(sockets[0]!.url).toContain('target=host')
    expect(sockets[1]!.url).toContain('target=container')
    expect(sockets[1]!.url).toContain('containerId=fake-container')
    motion.change(true); await flush()
    for (const terminal of mocks.terminals) expect(terminal.options.cursorBlink).toBe(false)
    panels[0]!.close(); consumed(); consumed()
    for (let index = 0; index < 16; index++) sockets[1]!.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    consumed()
    sockets[1]!.onmessage!({ data: new ArrayBuffer(1) })
    expect(sockets[1]!.close).toHaveBeenCalledTimes(1)
    expect(panels[0]!.pasteTitleId).not.toBe(panels[1]!.pasteTitleId)
  })
  it.each(['switch', 'detach', 'abort'])('初始化 nextTick 前 %s 不创建越权资源或焦点', async (action) => {
    mount()
    const connecting = panels[0]!.connect()
    if (action === 'switch') page.select('b')
    else if (action === 'detach') panels[0]!.detach()
    else invalidateSession()
    await connecting; await flush()
    expect(sockets).toHaveLength(action === 'switch' ? 1 : 0)
    expect(page.occupiedCount).toBe(action === 'switch' ? 1 : 0)
    if (action === 'switch') {
      sockets[0]!.started(); vi.advanceTimersByTime(80)
      expect(mocks.fits[0]!.fit).not.toHaveBeenCalled()
      expect(mocks.terminals[0]!.focus).not.toHaveBeenCalled()
    }
  })
  it('切换使已排队 fit 失效；切回可 fit 但不聚焦；resize 限制服务端尺寸', async () => {
    mount(); await panels[0]!.connect(); sockets[0]!.started()
    const fitCount = mocks.fits[0]!.fit.mock.calls.length
    const focusCount = mocks.terminals[0]!.focus.mock.calls.length
    page.select('b'); await flush(); vi.advanceTimersByTime(80)
    expect(mocks.fits[0]!.fit).toHaveBeenCalledTimes(fitCount)
    page.select('a'); await flush(); vi.advanceTimersByTime(80)
    expect(mocks.fits[0]!.fit).toHaveBeenCalledTimes(fitCount + 1)
    expect(mocks.terminals[0]!.focus).toHaveBeenCalledTimes(focusCount)
    mocks.terminals[0]!.resize({ rows: 300, cols: 600 })
    expect(sockets[0]!.send).toHaveBeenCalledWith(JSON.stringify({ type: 'resize', rows: 200, cols: 400 }))
  })
  it('页面卸载先全局撤销，A dispose 同步触发 B 输入也不会发送', async () => {
    mount(); await panels[0]!.connect(); sockets[0]!.started()
    page.select('b'); await panels[1]!.connect(); sockets[1]!.started()
    mocks.terminals[0]!.dispose.mockImplementation(() => {
      mocks.terminals[1]!.data('must not send')
      mocks.terminals[1]!.resize({ rows: 35, cols: 90 })
    })
    app!.unmount(); app = undefined
    expect(sockets[1]!.send).toHaveBeenCalledTimes(1)
    expect(sockets[1]!.send).toHaveBeenCalledWith(JSON.stringify({ type: 'close' }))
  })
  it('过大 fit 同时限制 xterm 与 PTY 初始尺寸', async () => {
    mount()
    // 在本次 fake fit 时模拟超大屏幕，并保留 xterm resize API 的尺寸更新。
    const fit = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get')
    fit.mockImplementation(() => {
      const terminal = mocks.terminals[0] as unknown as { rows: number; cols: number; resize: (cols: number, rows: number) => void }
      if (terminal) {
        terminal.rows = 300; terminal.cols = 600
        terminal.resize = (cols, rows) => { terminal.cols = cols; terminal.rows = rows }
      }
      return 800
    })
    await panels[0]!.connect()
    expect(sockets[0]!.url).toContain('rows=200&cols=400')
    expect(mocks.terminals[0]).toMatchObject({ rows: 200, cols: 400 })
  })

})
