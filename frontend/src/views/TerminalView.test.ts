// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { mockReducedMotion } from '@/test/reducedMotion'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import TerminalView from './TerminalView.vue'
import { TERMINAL_INPUT_BYTES, TERMINAL_OUTPUT_FRAME_BYTES, TERMINAL_SOCKET_BUFFER_BYTES } from '@/domain/terminalBudget'

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
vi.mock('@/components/NcpSelect.vue', () => ({ default: { props: ['disabled', 'modelValue', 'options'], emits: ['update:modelValue'], template: `<select :disabled="disabled" @change="$emit('update:modelValue', 'fake-container')"><option>fake</option></select>` } }))
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
function mount(initial = false) {
  motion = mockReducedMotion(initial)
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(TerminalView) })
  app.mount(host)
}
function button(text: string) { return [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === text)! }
async function flush() { await nextTick(); await nextTick() }
async function connect() { button('连接终端').click(); await flush() }
function actions() { return (host.querySelector('.terminal-panel-content') as unknown as { __vueParentComponent: { setupState: { close: () => void; connect: () => Promise<void> } } }).__vueParentComponent.setupState }
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

describe('R1 等待边界不创建晚到资源', () => {
  it.each(['cancel', 'unmount', 'abort'])('connect 后同步 %s 阻止 nextTick 初始化', async (stop) => {
    mount()
    button('连接终端').click()
    if (stop === 'cancel') actions().close()
    else if (stop === 'abort') invalidateSession()
    else { app!.unmount(); app = undefined }
    await flush()
    expect(sockets).toHaveLength(0)
    expect(mocks.terminals).toHaveLength(0)
  })
})

describe('R2 旧 owner 回调与新连接隔离', () => {
  it('晚到全部旧事件、timer、输入和 resize 不影响新连接', async () => {
    mount()
    const timers = vi.spyOn(window, 'setTimeout')
    await connect()
    const old = sockets[0]!
    const terminal = mocks.terminals[0]!
    const message = old.onmessage!
    const error = old.onerror!
    const close = old.onclose!
    const callbacks = timers.mock.calls.map(call => call[0]).filter(callback => typeof callback === 'function')
    old.started()
    await flush()
    button('断开连接').click()
    old.closed()
    await flush()
    await connect()
    const fresh = sockets[1]!
    const next = mocks.terminals[1]!
    for (const data of [JSON.stringify({ type: 'started' }), JSON.stringify({ type: 'error' }), JSON.stringify({ type: 'closed' }), '{', new ArrayBuffer(1)]) message({ data })
    error(); close(); terminal.data('ignored'); terminal.resize({ rows: 20, cols: 50 }); observers[0]!.callback()
    for (const callback of callbacks) (callback as () => void)()
    await flush()
    expect(fresh.send).not.toHaveBeenCalled()
    expect(fresh.close).not.toHaveBeenCalled()
    expect(next.write).not.toHaveBeenCalled()
    expect(next.focus).not.toHaveBeenCalled()
    expect(button('取消连接')).toBeTruthy()
    vi.advanceTimersByTime(9999)
    expect(fresh.close).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(fresh.close).toHaveBeenCalledTimes(1)
  })
})

describe('R3 连接中取消与目标锁定', () => {
  it('重复连接只创建一个，关闭中保留名额，onclose 后仅手动重试', async () => {
    mount()
    button('Docker 容器容器 Shell').click()
    await flush()
    host.querySelector('select')!.dispatchEvent(new Event('change'))
    await flush()
    await connect()
    await actions().connect()
    expect(sockets).toHaveLength(1)
    expect(sockets[0]!.url).toContain('containerId=fake-container')
    expect(host.querySelector('select')!.disabled).toBe(true)
    expect([...host.querySelectorAll('.target-tabs button')].every(button => (button as HTMLButtonElement).disabled)).toBe(true)
    button('取消连接').click()
    await flush()
    sockets[0]!.started()
    await actions().connect()
    expect(button('关闭中').disabled).toBe(true)
    expect(sockets).toHaveLength(1)
    expect(mocks.terminals[0]!.focus).not.toHaveBeenCalled()
    sockets[0]!.closed()
    await flush()
    expect(sockets).toHaveLength(1)
    expect(host.textContent).toContain('服务端资源回收可能仍在进行')
    await connect()
    expect(sockets).toHaveLength(2)
  })
})

describe('R4 初始化与清理异常隔离', () => {
  it.each(['construct', 'open', 'fit', 'writeln', 'socket'])('%s 抛错仍清理已创建资源且可重试', async (failure) => {
    mount()
    mocks.fail = failure
    await connect()
    expect(sockets).toHaveLength(0)
    if (mocks.terminals[0]) expect(mocks.terminals[0].dispose).toHaveBeenCalledTimes(1)
    expect(host.textContent).toContain('终端初始化失败')
    mocks.fail = ''
    await connect()
    expect(sockets).toHaveLength(1)
  })
  it.each(['send', 'close', 'dispose', 'inputDispose'])('%s 抛错不能阻断其他清理，重复 teardown 幂等', async (failure) => {
    mount()
    await connect()
    sockets[0]!.started()
    await flush()
    const terminal = mocks.terminals[0]!
    mocks.fail = failure
    button('断开连接').click()
    terminal.data('ignored'); terminal.resize({ rows: 10, cols: 10 }); paste()
    invalidateSession()
    app!.unmount(); app = undefined
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
    expect(sockets[0]!.send).toHaveBeenCalledTimes(1)
    expect(terminal.dispose).toHaveBeenCalledTimes(1)
    expect(terminal.inputDispose).toHaveBeenCalledTimes(1)
    expect(terminal.resizeDispose).toHaveBeenCalledTimes(1)
    expect(observers[0]!.disconnect).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })
  it('write 抛错只终止当前 owner', async () => {
    mount(); await connect(); sockets[0]!.started()
    mocks.fail = 'write'
    sockets[0]!.onmessage!({ data: new ArrayBuffer(8) })
    mocks.terminals[0]!.data('ignored')
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
    expect(mocks.terminals[0]!.dispose).toHaveBeenCalledTimes(1)
    await flush()
    expect(host.textContent).toContain('后续输出未保留')
  })
})

describe('R5 认证与页面同步撤销', () => {
  it.each(['beginSession', 'invalidateSession', 'pagehide', 'unmount'])('%s 同步拒绝输入、粘贴、resize 和晚到焦点', async (stop) => {
    mount(); await connect(); sockets[0]!.started(); await flush()
    const terminal = mocks.terminals[0]!
    paste()
    const canvas = host.querySelector('.terminal-canvas')!
    const focus = terminal.focus.mock.calls.length
    if (stop === 'beginSession') beginSession()
    else if (stop === 'invalidateSession') invalidateSession()
    else if (stop === 'pagehide') window.dispatchEvent(new Event('pagehide'))
    else { app!.unmount(); app = undefined }
    const sends = sockets[0]!.send.mock.calls.length
    terminal.data('ignored'); terminal.resize({ rows: 20, cols: 50 }); paste('ignored', canvas)
    observers[0]!.callback()
    await flush(); vi.advanceTimersByTime(10000)
    expect(sockets[0]!.send).toHaveBeenCalledTimes(sends)
    expect(terminal.focus).toHaveBeenCalledTimes(focus)
    expect(host.querySelector('.paste-confirmation')).toBeNull()
    if (stop === 'pagehide') {
      sockets[0]!.closed()
      window.dispatchEvent(new Event('pageshow'))
      await flush()
      expect(sockets).toHaveLength(1)
      expect(host.textContent).toContain('已断开')
    }
  })
})

describe('R6 握手、输出、粘贴、能力与减少动效', () => {
  it('只接受一次 started，正常输出与键盘输入保留，false resize 不发送', async () => {
    mount(); await connect()
    const socket = sockets[0]!
    const terminal = mocks.terminals[0]!
    socket.onmessage!({ data: new ArrayBuffer(1) })
    expect(terminal.write).not.toHaveBeenCalled()
    socket.started({ resize: false, multilinePaste: true, bracketedPaste: true })
    socket.control({ type: 'started', capabilities: { resize: true } })
    terminal.resize({ rows: 40, cols: 100 })
    expect(socket.send).not.toHaveBeenCalled()
    terminal.data('typed')
    expect(socket.send).toHaveBeenCalledTimes(1)
    socket.onmessage!({ data: new ArrayBuffer(2) })
    expect(terminal.write).toHaveBeenCalledTimes(1)
    expect(terminal.clear).toHaveBeenCalledTimes(1)
    expect(terminal.options.scrollback).toBe(5000)
    paste(); await flush()
    expect(host.querySelector('.paste-confirmation')).not.toBeNull()
    socket.closed(); await flush()
    expect(host.querySelector('.paste-confirmation')).toBeNull()
  })
  it('10 秒握手超时后 late started/output 无效', async () => {
    mount(); await connect()
    const socket = sockets[0]!
    const message = socket.onmessage!
    vi.advanceTimersByTime(9999)
    expect(socket.close).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    socket.started()
    message({ data: new ArrayBuffer(2) })
    expect(mocks.terminals[0]!.write).not.toHaveBeenCalled()
    expect(mocks.terminals[0]!.focus).not.toHaveBeenCalled()
    expect(socket.close).toHaveBeenCalledTimes(1)
  })
  it.each(['control', 'socket'])('当前 %s error 清粘贴并丢弃晚到输出', async (source) => {
    mount(); await connect(); sockets[0]!.started(); await flush(); paste(); await flush()
    const socket = sockets[0]!
    const message = socket.onmessage!
    if (source === 'control') socket.control({ type: 'error', code: 'TERMINAL_SESSION_LIMIT_REACHED' })
    else socket.onerror!()
    message({ data: new ArrayBuffer(2) })
    await flush()
    expect(host.querySelector('.paste-confirmation')).toBeNull()
    expect(mocks.terminals[0]!.write).not.toHaveBeenCalled()
    expect(socket.close).toHaveBeenCalledTimes(1)
    if (source === 'control') expect(host.textContent).toContain('稍后手动重试')
  })
  it('正常 resize 与显式粘贴确认只发送一次，聚焦失败不重放', async () => {
    mount(); await connect(); sockets[0]!.started(); await flush()
    const socket = sockets[0]!
    const terminal = mocks.terminals[0]!
    terminal.resize({ rows: 40, cols: 100 })
    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'resize', rows: 40, cols: 100 }))
    socket.send.mockClear()
    paste('single')
    expect(socket.send).toHaveBeenCalledWith(new TextEncoder().encode('\x1b[200~single\x1b[201~'))
    socket.send.mockClear()
    paste(); await flush()
    expect(socket.send).not.toHaveBeenCalled()
    terminal.focus.mockImplementation(() => { throw Error('fake focus') })
    button('粘贴到终端').click(); await flush()
    expect(socket.send).toHaveBeenCalledTimes(1)
    expect(socket.send).toHaveBeenCalledWith(new TextEncoder().encode('\x1b[200~first\rsecond\x1b[201~'))
    expect(socket.close).not.toHaveBeenCalled()
  })
  it.each([true, false])('减少动效初值 %s 与变化不重建；手动重连使用当前值', async (initial) => {
    mount(initial); await connect()
    const terminal = mocks.terminals[0]!
    expect(terminal.options.cursorBlink).toBe(!initial)
    motion.change(!initial); await flush()
    expect(terminal.options.cursorBlink).toBe(initial)
    expect(mocks.terminals).toHaveLength(1)
    sockets[0]!.started(); await flush()
    button('断开连接').click(); sockets[0]!.closed(); await flush()
    motion.change(initial); await flush(); await connect()
    expect(mocks.terminals[1]!.options.cursorBlink).toBe(!initial)
    expect(terminal.dispose).toHaveBeenCalledTimes(1)
  })
})

describe('R7 取消操作与原粘贴键盘语义', () => {
  it('取消为可聚焦原生按钮、状态 polite；取消后焦点仍在操作区', async () => {
    mount(); await connect()
    const cancel = button('取消连接')
    expect(cancel.disabled).toBe(false)
    cancel.focus()
    expect(document.activeElement).toBe(cancel)
    expect(host.querySelector('.terminal-live-status')?.getAttribute('aria-live')).toBe('polite')
    cancel.click(); await flush()
    expect(host.querySelector('.terminal-toolbar-actions')?.contains(document.activeElement)).toBe(true)
  })
  it('多行先确认，Esc 不发送；literal-next 只由显式快捷键发送', async () => {
    mount(); await connect(); sockets[0]!.started(); await flush()
    paste(); await flush()
    expect(sockets[0]!.send).not.toHaveBeenCalled()
    host.querySelector('.paste-confirmation')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flush()
    expect(sockets[0]!.send).not.toHaveBeenCalled()
    mocks.terminals[0]!.key(new KeyboardEvent('keydown', { key: 'q', ctrlKey: true }))
    expect(sockets[0]!.send).toHaveBeenCalledTimes(1)
  })
})


describe('R8 单 owner 输出与输入预算', () => {
  it('延迟消费的 256 KiB 精确边界接纳，超一字节终止并保持 closing', async () => {
    mount(); await connect(); sockets[0]!.started(); await flush()
    const socket = sockets[0]!
    const message = socket.onmessage!
    const terminal = mocks.terminals[0]!
    for (let index = 0; index < 16; index++) message({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    expect(terminal.write).toHaveBeenCalledTimes(16)
    expect(socket.close).not.toHaveBeenCalled()
    paste(); await flush()
    message({ data: new ArrayBuffer(1) })
    terminal.data('ignored'); terminal.resize({ rows: 30, cols: 90 })
    message({ data: new ArrayBuffer(1) })
    await flush()
    expect(terminal.write).toHaveBeenCalledTimes(16)
    expect(terminal.dispose).toHaveBeenCalledTimes(1)
    expect(socket.close).toHaveBeenCalledTimes(1)
    expect(socket.send).toHaveBeenCalledExactlyOnceWith(JSON.stringify({ type: 'close' }))
    expect(host.querySelector('.paste-confirmation')).toBeNull()
    expect(button('关闭中').disabled).toBe(true)
    expect(host.textContent).toContain('输出处理过载')
    await actions().connect()
    expect(sockets).toHaveLength(1)
  })
  it('消费回调允许后续完整帧，重复与乱序回调不能额外扩大预算', async () => {
    mount(); await connect(); sockets[0]!.started()
    const socket = sockets[0]!
    const terminal = mocks.terminals[0]!
    for (let index = 0; index < 16; index++) socket.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    terminal.consumed[8]!(); terminal.consumed[8]!(); terminal.consumed[1]!()
    socket.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    socket.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    expect(socket.close).not.toHaveBeenCalled()
    socket.onmessage!({ data: new ArrayBuffer(1) })
    expect(socket.close).toHaveBeenCalledTimes(1)
    expect(terminal.write).toHaveBeenCalledTimes(18)
  })
  it.each(['overflow', 'write'])('%s 关闭后的回调不改变替换 owner 的预算', async failure => {
    mount(); await connect(); sockets[0]!.started()
    const old = sockets[0]!
    if (failure === 'write') mocks.fail = 'write'
    old.onmessage!({ data: new ArrayBuffer(1) })
    const consumed = mocks.terminals[0]!.consumed[0]!
    if (failure === 'overflow') old.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES + 1) })
    old.closed(); mocks.fail = ''; await flush(); await connect(); sockets[1]!.started()
    const next = sockets[1]!
    for (let index = 0; index < 16; index++) next.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    consumed(); consumed()
    expect(next.close).not.toHaveBeenCalled()
    next.onmessage!({ data: new ArrayBuffer(1) })
    expect(next.close).toHaveBeenCalledTimes(1)
    expect(mocks.terminals[1]!.write).toHaveBeenCalledTimes(16)
  })
  it('16 KiB + 1 异常帧未交给 xterm，零长度帧不消耗预算', async () => {
    mount(); await connect(); sockets[0]!.started()
    sockets[0]!.onmessage!({ data: new ArrayBuffer(0) })
    sockets[0]!.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES + 1) })
    expect(mocks.terminals[0]!.write).not.toHaveBeenCalled()
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
  })
  it('同步 write 回调同样释放预算，不触发伪超限', async () => {
    mount(); await connect(); sockets[0]!.started()
    mocks.terminals[0]!.write.mockImplementation((_data: Uint8Array, callback: () => void) => callback())
    for (let index = 0; index < 32; index++) sockets[0]!.onmessage!({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    expect(mocks.terminals[0]!.write).toHaveBeenCalledTimes(32)
    expect(sockets[0]!.close).not.toHaveBeenCalled()
  })
  it('32 KiB UTF-8 输入边界，超限不截断、不发送、不重放', async () => {
    mount(); await connect(); sockets[0]!.started()
    const terminal = mocks.terminals[0]!
    const socket = sockets[0]!
    terminal.data('😀'.repeat(TERMINAL_INPUT_BYTES / 4))
    expect(socket.send).toHaveBeenCalledExactlyOnceWith(new TextEncoder().encode('😀'.repeat(TERMINAL_INPUT_BYTES / 4)))
    socket.send.mockClear()
    terminal.data('😀'.repeat(TERMINAL_INPUT_BYTES / 4) + 'x')
    await flush()
    expect(socket.send).not.toHaveBeenCalled()
    expect(socket.close).not.toHaveBeenCalled()
    expect(host.textContent).toContain('超过 32 KiB，未发送')
    vi.advanceTimersByTime(1000)
    expect(socket.send).not.toHaveBeenCalled()
    terminal.data('x')
    expect(socket.send).toHaveBeenCalledExactlyOnceWith(new TextEncoder().encode('x'))
  })
  it('粘贴按归一化及括号包装后的字节限制，过大原文不保留确认', async () => {
    mount(); await connect(); sockets[0]!.started(); await flush()
    paste('a'.repeat(TERMINAL_INPUT_BYTES - 12))
    expect(sockets[0]!.send).toHaveBeenCalledTimes(1)
    sockets[0]!.send.mockClear()
    paste('a'.repeat(TERMINAL_INPUT_BYTES - 11))
    paste('a\n' + 'b'.repeat(TERMINAL_INPUT_BYTES))
    await flush()
    expect(sockets[0]!.send).not.toHaveBeenCalled()
    expect(host.querySelector('.paste-confirmation')).toBeNull()
    expect(sockets[0]!.close).not.toHaveBeenCalled()
    paste('\r\n'.repeat(TERMINAL_INPUT_BYTES - 12)); await flush()
    expect(host.querySelector('.paste-confirmation')).not.toBeNull()
    button('粘贴到终端').click()
    expect(sockets[0]!.send).toHaveBeenCalledTimes(1)
  })
  it.each(['input', 'resize', 'paste'])('64 KiB 发送积压拒绝 %s，不重放且回收当前 owner', async source => {
    mount(); await connect(); sockets[0]!.started(); await flush()
    const socket = sockets[0]!
    socket.bufferedAmount = TERMINAL_SOCKET_BUFFER_BYTES
    if (source === 'input') mocks.terminals[0]!.data('x')
    else if (source === 'resize') mocks.terminals[0]!.resize({ rows: 30, cols: 90 })
    else { paste(); await flush(); button('粘贴到终端').click() }
    await flush()
    expect(socket.send).toHaveBeenCalledExactlyOnceWith(JSON.stringify({ type: 'close' }))
    expect(socket.close).toHaveBeenCalledTimes(1)
    expect(host.textContent).toContain('已发送的输入无法撤回')
    socket.bufferedAmount = 0
    vi.advanceTimersByTime(10000)
    expect(socket.send).toHaveBeenCalledTimes(1)
  })
  it('现有积压低于 64 KiB，但加入完整载荷超一字节时整笔拒绝', async () => {
    mount(); await connect(); sockets[0]!.started()
    const socket = sockets[0]!
    socket.bufferedAmount = TERMINAL_SOCKET_BUFFER_BYTES - TERMINAL_INPUT_BYTES + 1
    expect(socket.bufferedAmount).toBeLessThan(TERMINAL_SOCKET_BUFFER_BYTES)
    mocks.terminals[0]!.data('a'.repeat(TERMINAL_INPUT_BYTES))
    expect(socket.send).toHaveBeenCalledExactlyOnceWith(JSON.stringify({ type: 'close' }))
    expect(socket.close).toHaveBeenCalledTimes(1)
    socket.bufferedAmount = 0
    vi.advanceTimersByTime(10000)
    expect(socket.send).toHaveBeenCalledTimes(1)
    await flush()
    expect(host.textContent).toContain('当前输入或窗口调整未发送')
  })
  it('bufferedAmount 加当前载荷精确 64 KiB 可以发送', async () => {
    mount(); await connect(); sockets[0]!.started()
    sockets[0]!.bufferedAmount = TERMINAL_SOCKET_BUFFER_BYTES - TERMINAL_INPUT_BYTES
    mocks.terminals[0]!.data('a'.repeat(TERMINAL_INPUT_BYTES))
    expect(sockets[0]!.send).toHaveBeenCalledTimes(1)
    expect(sockets[0]!.close).not.toHaveBeenCalled()
  })
})

describe('M1b 真正独立的本地终端标签', () => {
  const tabs = () => [...host.querySelectorAll<HTMLButtonElement>('[role="tab"]')]
  const panels = () => [...host.querySelectorAll<HTMLElement>('[role="tabpanel"]')]
  const activePanel = () => panels().find(panel => !panel.hidden)!
  const activeButton = (text: string) => [...activePanel().querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim() === text)!
  const closeButton = () => host.querySelector<HTMLButtonElement>('.terminal-tab-close')!
  async function add() { button('新建标签').click(); await flush() }
  async function connectActive() { activeButton('连接终端').click(); await flush() }

  it('新增最多四页且不连接，ARIA 关联与方向/Home/End 焦点只切换标签', async () => {
    mount()
    for (let i = 0; i < 4; i++) await add()
    expect(tabs()).toHaveLength(4)
    expect(button('新建标签').disabled).toBe(true)
    expect(sockets).toHaveLength(0)
    expect(mocks.terminals).toHaveLength(0)
    expect(tabs().filter(tab => tab.tabIndex === 0)).toHaveLength(1)
    const list = host.querySelector('[role="tablist"]')!
    expect([...list.children].every(child => child.getAttribute('role') === 'tab')).toBe(true)
    expect(list.querySelectorAll('button:not([role="tab"])')).toHaveLength(0)
    expect(list.contains(closeButton())).toBe(false)
    expect(closeButton().getAttribute('aria-hidden')).toBeNull()
    expect(closeButton().tabIndex).toBe(0)
    expect(closeButton().getAttribute('aria-label')).toBe('关闭当前标签：终端 4，root@主机')
    for (const tab of tabs()) {
      const panel = document.getElementById(tab.getAttribute('aria-controls')!)!
      expect(panel.getAttribute('aria-labelledby')).toBe(tab.id)
      expect(tab.querySelector('button')).toBeNull()
    }
    for (const [key, index] of [['Home', 0], ['ArrowLeft', 3], ['ArrowRight', 0], ['End', 3]] as const) {
      const current = tabs().find(tab => tab.getAttribute('aria-selected') === 'true')!
      current.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))
      await flush()
      expect(document.activeElement).toBe(tabs()[index])
      expect(closeButton().getAttribute('aria-label')).toBe(`关闭当前标签：终端 ${index + 1}，root@主机`)
      expect(tabs()[index]!.getAttribute('aria-selected')).toBe('true')
      expect(panels().filter(panel => panel.hidden && panel.hasAttribute('inert'))).toHaveLength(3)
    }
    expect(host.textContent).toContain('服务端默认全局上限为 1')
    expect(host.textContent).not.toContain('DH4300Plus')
    expect(sockets).toHaveLength(0)
  })

  it('新增意图同步封闭旧 owner；异步渲染前认证撤销不会抢焦点或重连', async () => {
    mount()
    await connectActive(); sockets[0]!.started(); await flush()
    const old = mocks.terminals[0]!
    sockets[0]!.send.mockClear()
    button('新建标签').click()
    old.data('same-stack'); old.resize({ rows: 20, cols: 40 })
    paste('same-stack')
    expect(sockets[0]!.send).not.toHaveBeenCalled()
    const external = document.createElement('button')
    document.body.append(external); external.focus()
    invalidateSession()
    await flush()
    expect(document.activeElement).toBe(external)
    expect(sockets).toHaveLength(1)
    external.remove()
  })

  it.each(['abort', 'pagehide'])('%s 同步失效后的旧关闭确认不得删除标签或抛错', async cause => {
    mount(); await connectActive()
    closeButton().click(); await flush()
    const confirm = button('断开并关闭标签')
    const id = tabs()[0]!.id
    if (cause === 'abort') invalidateSession()
    else window.dispatchEvent(new Event('pagehide'))
    confirm.click()
    await flush()
    expect(tabs()).toHaveLength(1)
    expect(tabs()[0]!.id).toBe(id)
    expect(host.querySelector('[role="alertdialog"]')).toBeNull()
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
  })

  it('删除等待期间较新的选页优先，不把焦点拉回旧相邻标签', async () => {
    mount(); await add(); await add()
    tabs()[0]!.click(); await flush()
    closeButton().click()
    tabs()[1]!.focus(); tabs()[1]!.click()
    await flush()
    expect(tabs().find(tab => tab.getAttribute('aria-selected') === 'true')?.id).toBe('tab-terminal-2')
    expect(document.activeElement?.id).toBe('tab-terminal-2')
    expect(sockets).toHaveLength(0)
  })

  it('后台持续有界输出但不能输入/fit/抢焦点，切换取消旧粘贴且目标与能力隔离', async () => {
    mount()
    await connectActive()
    sockets[0]!.started()
    await flush()
    paste()
    await flush()
    expect(host.querySelector('.paste-confirmation')).not.toBeNull()
    await add()
    expect(host.querySelector('.paste-confirmation')).toBeNull()
    activeButton('Docker 容器容器 Shell').click()
    await flush()
    activePanel().querySelector('select')!.dispatchEvent(new Event('change'))
    await flush()
    await connectActive()
    sockets[1]!.started({ resize: false, multilinePaste: false, bracketedPaste: false })
    await flush()
    const first = mocks.terminals[0]!
    const second = mocks.terminals[1]!
    first.focus.mockClear(); second.focus.mockClear()
    mocks.fits[0]!.fit.mockClear()
    sockets[0]!.send.mockClear(); sockets[1]!.send.mockClear()
    sockets[0]!.onmessage!({ data: new ArrayBuffer(3) })
    first.data('hidden'); first.resize({ rows: 20, cols: 40 }); observers[0]!.callback()
    paste('hidden\ninput', panels()[0]!.querySelector('.terminal-canvas')!)
    vi.advanceTimersByTime(80)
    expect(first.write).toHaveBeenCalledTimes(1)
    expect(first.focus).not.toHaveBeenCalled()
    expect(mocks.fits[0]!.fit).not.toHaveBeenCalled()
    expect(sockets[0]!.send).not.toHaveBeenCalled()
    second.data('second')
    expect(sockets[1]!.send).toHaveBeenCalledTimes(1)
    expect(sockets[1]!.url).toContain('containerId=fake-container')
    expect(tabs()[0]!.textContent).toContain('root@主机')
    expect(tabs()[1]!.textContent).toContain('fake')
    tabs()[0]!.focus(); tabs()[0]!.click(); await flush(); vi.advanceTimersByTime(80)
    expect(mocks.fits[0]!.fit).toHaveBeenCalledTimes(1)
    expect(first.focus).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(tabs()[0])
    activeButton('清空内容').click()
    expect(first.clear).toHaveBeenCalledTimes(2)
    expect(second.clear).toHaveBeenCalledTimes(1)
    expect(sockets).toHaveLength(2)
  })

  it('隐藏页超出输出预算仅退役自己；其他页仍可输入且不自动重连', async () => {
    mount(); await connectActive(); sockets[0]!.started(); await flush()
    await add(); await connectActive(); sockets[1]!.started(); await flush()
    const oldMessage = sockets[0]!.onmessage!
    for (let i = 0; i < 16; i++) oldMessage({ data: new ArrayBuffer(TERMINAL_OUTPUT_FRAME_BYTES) })
    oldMessage({ data: new ArrayBuffer(1) })
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
    expect(sockets[1]!.close).not.toHaveBeenCalled()
    expect(mocks.terminals[0]!.dispose).toHaveBeenCalledTimes(1)
    sockets[1]!.send.mockClear()
    mocks.terminals[1]!.data('still-active')
    expect(sockets[1]!.send).toHaveBeenCalledTimes(1)
    await flush()
    expect(tabs()[0]!.textContent).toContain('关闭中')
    expect(sockets).toHaveLength(2)
  })

  it.each(['connecting', 'connected'])('关闭 %s 必须确认，保留不关闭；确认后最后一页换为空页且旧 lease 不释放', async state => {
    mount()
    await connectActive()
    if (state === 'connected') sockets[0]!.started()
    await flush()
    const oldId = tabs()[0]!.id
    closeButton().click(); await flush()
    expect(host.querySelector('[role="alertdialog"]')?.textContent).toContain('不能保证远端任务已结束')
    expect(document.activeElement).toBe(button('保留标签'))
    expect(sockets[0]!.close).not.toHaveBeenCalled()
    button('保留标签').click(); await flush()
    expect(tabs()).toHaveLength(1)
    expect(sockets[0]!.close).not.toHaveBeenCalled()
    closeButton().click(); await flush()
    button('断开并关闭标签').click(); await flush()
    expect(tabs()).toHaveLength(1)
    expect(tabs()[0]!.id).not.toBe(oldId)
    expect(tabs()[0]!.textContent).toContain('未连接')
    expect(document.activeElement).toBe(tabs()[0])
    expect(sockets).toHaveLength(1)
    expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
    expect(mocks.terminals[0]!.dispose).toHaveBeenCalledTimes(1)
    expect(host.querySelector('.terminal-retired-status')?.textContent).toContain('1 个')
    sockets[0]!.closed(); await flush()
    expect(host.querySelector('.terminal-retired-status')).toBeNull()
    expect(host.querySelector('.terminal-capacity-note')?.textContent).toContain('0/4')
  })

  it.each(['connecting', 'connected'])('关闭 %s 时原按钮禁用导致浏览器失焦，确认仍接收焦点且 Escape 可取消', async state => {
    mount(); await connectActive()
    if (state === 'connected') sockets[0]!.started()
    await flush()
    const close = closeButton()
    // jsdom 不实现 Chromium 在禁用活动按钮时自动 blur 的行为；在真实属性写入处补齐。
    const disabled = Object.getOwnPropertyDescriptor(HTMLButtonElement.prototype, 'disabled')!
    vi.spyOn(close, 'disabled', 'set').mockImplementation(value => {
      if (value && document.activeElement === close) close.blur()
      disabled.set!.call(close, value)
    })
    close.focus(); close.click(); await flush()
    expect(close.disabled).toBe(true)
    expect(document.activeElement).toBe(button('保留标签'))
    document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    await flush()
    expect(host.querySelector('[role="alertdialog"]')).toBeNull()
    expect(document.activeElement).toBe(tabs()[0])
    expect(sockets[0]!.close).not.toHaveBeenCalled()
  })

  it.each(['focused', 'blurred', 'abort', 'pagehide'])('关闭确认渲染前新的外部焦点优先（%s），不抢走焦点', async action => {
    mount(); await connectActive()
    closeButton().focus(); closeButton().click()
    const external = document.createElement('button')
    document.body.append(external)
    try {
      external.focus()
      if (action === 'blurred') external.blur()
      if (action === 'abort') invalidateSession()
      if (action === 'pagehide') window.dispatchEvent(new Event('pagehide'))
      await flush()
      expect(document.activeElement).toBe(action === 'blurred' ? document.body : external)
      if (action === 'abort' || action === 'pagehide') {
        expect(host.querySelector('[role="alertdialog"]')).toBeNull()
        expect(sockets[0]!.close).toHaveBeenCalledTimes(1)
      } else {
        expect(host.querySelector('[role="alertdialog"]')).not.toBeNull()
        expect(sockets[0]!.close).not.toHaveBeenCalled()
      }
    } finally { external.remove() }
  })

  it('删除再新建不能绕过四个 opening/live/closing lease；准确 onclose 后才重新准入', async () => {
    mount()
    for (let i = 0; i < 4; i++) {
      if (i) await add()
      await connectActive()
      sockets[i]!.started()
    }
    await flush()
    closeButton().click(); await flush()
    button('断开并关闭标签').click(); await flush()
    await add()
    expect(activeButton('连接终端').disabled).toBe(true)
    expect(host.querySelector('.terminal-capacity-note')?.textContent).toContain('4/4')
    expect(host.querySelector('.terminal-retired-status')?.textContent).toContain('1 个')
    expect(sockets).toHaveLength(4)
    sockets[3]!.closed(); await flush()
    expect(activeButton('连接终端').disabled).toBe(false)
    await connectActive()
    expect(sockets).toHaveLength(5)
    expect(host.querySelector('.terminal-capacity-note')?.textContent).toContain('4/4')
    for (const socket of sockets.slice(0, 3)) expect(socket.close).not.toHaveBeenCalled()
    sockets[4]!.control({ type: 'error', code: 'TERMINAL_SESSION_LIMIT_REACHED' })
    await flush()
    expect(activePanel().textContent).toContain('终端会话名额暂不可用')
    expect(activePanel().textContent).toContain('手动重试')
    expect(sockets).toHaveLength(5)
  })

  it('关闭空白标签不需要确认；页面失效同步关闭所有独立 owner，不自动恢复', async () => {
    mount()
    closeButton().click(); await flush()
    expect(host.querySelector('[role="alertdialog"]')).toBeNull()
    expect(tabs()).toHaveLength(1)
    for (let i = 0; i < 3; i++) {
      if (i) await add()
      await connectActive(); sockets[i]!.started()
    }
    window.dispatchEvent(new Event('pagehide'))
    for (const terminal of mocks.terminals) terminal.data('late')
    for (const socket of sockets) {
      expect(socket.close).toHaveBeenCalledTimes(1)
      expect(socket.send.mock.calls).toHaveLength(1)
    }
    window.dispatchEvent(new Event('pageshow')); await flush()
    expect(sockets).toHaveLength(3)
    for (const terminal of mocks.terminals) expect(terminal.dispose).toHaveBeenCalledTimes(1)
  })
})
