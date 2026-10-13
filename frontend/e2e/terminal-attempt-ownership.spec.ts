import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'

type SavedCallbacks = {
  message: ((event: MessageEvent) => void) | null
  error: ((event: Event) => void) | null
  close: ((event: CloseEvent) => void) | null
}
type SocketProbe = {
  url: string
  readyState: number
  closeCalls: number
  sent: Array<{ kind: 'control' | 'input'; text: string }>
  saved: SavedCallbacks
  snapshot(): void
  started(): void
  finishClose(): void
  lateEvents(includeClose?: boolean): void
}
type FixtureWindow = Window & { terminalSockets: SocketProbe[] }

const toolbar = (page: Page) => page.locator('.terminal-toolbar')
const connectionStatus = (page: Page) => page.locator('.terminal-frame > header [role="status"]')
const connectButton = (page: Page) => toolbar(page).getByRole('button', { name: '连接终端', exact: true })

async function installTerminalFixture(page: Page) {
  // 所有 HTTP/SSE 都由共享 fixture 接管；WebSocket 完全替换，不调用原生构造器。
  // 这是浏览器交互回归，不是 NAS / Agent / PTY 验收。
  await installMockApi(page, { clock: false, manualRealtime: true })
  await page.route('**/api/**', (route) => {
    if (route.request().method() !== 'GET') return route.fulfill({ status: 503, json: { code: 'FIXTURE_WRITE_BLOCKED' } })
    return route.fallback()
  })
  await page.routeWebSocket('**/*', (socket) => {
    // 防御性拦截：即使 fixture 意外被替换，也绝不连接真实服务端。
    socket.close({ code: 1008, reason: 'Only in-memory terminal fixture is allowed' })
  })
  await page.addInitScript(() => {
    const host = window as unknown as FixtureWindow
    host.terminalSockets = []
    class FakeSocket {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSING = 2
      static readonly CLOSED = 3
      readyState = FakeSocket.CONNECTING
      binaryType = 'blob'
      bufferedAmount = 0
      closeCalls = 0
      sent: SocketProbe['sent'] = []
      saved: SavedCallbacks = { message: null, error: null, close: null }
      onmessage: SavedCallbacks['message'] = null
      onerror: SavedCallbacks['error'] = null
      onclose: SavedCallbacks['close'] = null
      constructor(readonly url: string) {
        if (new URL(url).pathname !== '/ws/terminal') throw new Error('Unexpected fixture WebSocket')
        if (host.terminalSockets.length >= 2) throw new Error('Fixture permits at most two attempts')
        host.terminalSockets.push(this)
      }
      send(data: string | ArrayBuffer | ArrayBufferView) {
        if (this.readyState !== FakeSocket.OPEN) throw new Error('Fixture socket is not open')
        this.sent.push(typeof data === 'string'
          ? { kind: 'control', text: data }
          : { kind: 'input', text: new TextDecoder().decode(data) })
      }
      close() {
        this.closeCalls += 1
        this.readyState = FakeSocket.CLOSING
        // close() 不伪造 close 事件；测试显式确认本地传输关闭。
      }
      snapshot() {
        this.saved = { message: this.onmessage, error: this.onerror, close: this.onclose }
      }
      started() {
        this.readyState = FakeSocket.OPEN
        this.onmessage?.(new MessageEvent('message', { data: JSON.stringify({
          type: 'started', shell: 'bash', enhancement: 'readline',
          capabilities: { resize: false, readline: true, bracketedPaste: true, multilinePaste: true, ansiColors: true },
        }) }))
      }
      finishClose() {
        this.readyState = FakeSocket.CLOSED
        this.onclose?.(new CloseEvent('close', { code: 1000 }))
      }
      lateEvents(includeClose = true) {
        // 保存的闭包绕过事件解绑，直接模拟已排队的旧回调。
        for (const data of [
          JSON.stringify({ type: 'started', shell: 'old-shell', capabilities: { resize: true } }),
          JSON.stringify({ type: 'error', code: 'TERMINAL_SESSION_FAILED' }),
          JSON.stringify({ type: 'closed' }), '{invalid-json',
          new TextEncoder().encode('STALE FIXTURE OUTPUT').buffer,
        ]) this.saved.message?.(new MessageEvent('message', { data }))
        this.saved.error?.(new Event('error'))
        if (includeClose) this.saved.close?.(new CloseEvent('close', { code: 1006 }))
      }
    }
    window.WebSocket = FakeSocket as unknown as typeof WebSocket
  })
  const realSockets: string[] = []
  page.on('websocket', (socket) => realSockets.push(socket.url()))
  await page.goto('/terminal', { waitUntil: 'domcontentloaded' })
  await expect(connectButton(page)).toBeEnabled()
  return realSockets
}

async function beginAttempt(page: Page, count = 1) {
  await connectButton(page).click()
  await expect.poll(() => page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets.length)).toBe(count)
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets.at(-1)!.snapshot())
  await expect(connectionStatus(page)).toHaveText('连接中')
}
async function startAttempt(page: Page) {
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets.at(-1)!.started())
  await expect(connectionStatus(page)).toHaveText('已连接')
}
async function inputFrames(page: Page, index = 0) {
  return page.evaluate((i) => (window as unknown as FixtureWindow).terminalSockets[i]!.sent.filter((frame) => frame.kind === 'input').map((frame) => frame.text), index)
}
async function paste(page: Page, text: string) {
  await page.locator('.terminal-canvas').evaluate((element, content) => {
    const clipboardData = new DataTransfer()
    clipboardData.setData('text/plain', content)
    element.dispatchEvent(new ClipboardEvent('paste', { clipboardData, bubbles: true, cancelable: true }))
  }, text)
}
async function capture(page: Page, info: TestInfo, name: string) {
  const path = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path, fullPage: true, animations: 'disabled' })
  await info.attach(name, { path, contentType: 'image/png' })
}

for (const width of [390, 1440]) {
  test(`内存终端 fixture ${width}px：可键盘取消连接，关闭事件前锁定目标和重连`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    const realSockets = await installTerminalFixture(page)
    await toolbar(page).getByRole('button', { name: /Docker 容器/ }).click()
    const containerSelect = toolbar(page).getByRole('combobox')
    await containerSelect.click()
    await page.getByRole('option', { name: /media-api ·/ }).click()
    await beginAttempt(page)
    await expect(containerSelect).toBeDisabled()
    await expect(toolbar(page).getByRole('button', { name: /NAS 主机/ })).toBeDisabled()
    await expect(toolbar(page).getByRole('button', { name: /Docker 容器/ })).toBeDisabled()
    const cancel = toolbar(page).getByRole('button', { name: '取消连接', exact: true })
    await expect(cancel).toBeEnabled()
    await cancel.focus()
    await expect(cancel).toBeFocused()
    await expect(cancel).toBeInViewport()
    await capture(page, info, `fixture-terminal-connecting-${width}`)
    const accessibility = await new AxeBuilder({ page }).include('.terminal-toolbar').analyze()
    expect(accessibility.violations).toEqual([])
    await page.keyboard.press('Enter')
    await expect(connectionStatus(page)).toHaveText('关闭中')
    await expect(toolbar(page).getByRole('button', { name: '关闭中', exact: true })).toBeDisabled()
    await expect(connectButton(page)).toHaveCount(0)
    await expect(containerSelect).toBeDisabled()
    await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[0]!.lateEvents(false))
    await expect(connectionStatus(page)).toHaveText('关闭中')
    await capture(page, info, `fixture-terminal-closing-${width}`)
    await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[0]!.finishClose())
    await expect(connectButton(page)).toBeEnabled()
    await expect(containerSelect).toBeEnabled()
    expect(await inputFrames(page)).toEqual([])
    expect(realSockets).toEqual([])
  })
}

test('手动重连后的旧 started/error/close/输出回调不能改变新连接或发送输入', async ({ page }) => {
  const realSockets = await installTerminalFixture(page)
  await beginAttempt(page)
  await startAttempt(page)
  await paste(page, 'old fixture\npending text')
  await expect(page.getByRole('dialog', { name: '确认多行粘贴' })).toBeVisible()
  await toolbar(page).getByRole('button', { name: '断开连接', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '确认多行粘贴' })).toHaveCount(0)
  await expect(toolbar(page).getByRole('button', { name: '关闭中', exact: true })).toBeDisabled()
  await expect(connectButton(page)).toHaveCount(0)
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[0]!.finishClose())
  await beginAttempt(page, 2)
  await startAttempt(page)
  await toolbar(page).getByRole('button', { name: '快捷键', exact: true }).focus()
  const before = await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[1]!.sent)
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[0]!.lateEvents())
  await expect(connectionStatus(page)).toHaveText('已连接')
  await expect(toolbar(page).getByRole('button', { name: '快捷键', exact: true })).toBeFocused()
  expect(await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[1]!.closeCalls)).toBe(0)
  expect(await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[1]!.sent)).toEqual(before)
  await paste(page, 'new fixture text')
  expect(await inputFrames(page, 0)).toEqual([])
  expect(await inputFrames(page, 1)).toEqual(['\u001b[200~new fixture text\u001b[201~'])
  expect(realSockets).toEqual([])
})

test('started 后正常键盘与多行粘贴保持原语义，仅写入 fake socket', async ({ page }) => {
  const realSockets = await installTerminalFixture(page)
  await beginAttempt(page)
  await paste(page, 'not ready')
  expect(await inputFrames(page)).toEqual([])
  await startAttempt(page)
  const input = page.locator('.xterm-helper-textarea')
  await input.focus()
  await input.press('x')
  await input.press('Control+q')
  await expect.poll(() => inputFrames(page)).toEqual(['x', '\u0016'])
  await paste(page, 'first fixture line\nsecond fixture line')
  await expect(page.getByRole('dialog', { name: '确认多行粘贴' })).toBeVisible()
  expect(await inputFrames(page)).toEqual(['x', '\u0016'])
  await page.getByRole('button', { name: '粘贴到终端', exact: true }).click()
  await expect.poll(() => inputFrames(page)).toEqual(['x', '\u0016', '\u001b[200~first fixture line\rsecond fixture line\u001b[201~'])
  await paste(page, 'discard fixture\ntext')
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[0]!.finishClose())
  await expect(page.getByRole('dialog', { name: '确认多行粘贴' })).toHaveCount(0)
  await expect(page.locator('.xterm')).toHaveCount(0)
  expect(realSockets).toEqual([])
})

test('pagehide 清理输入和待粘贴，pageshow 不自动恢复连接', async ({ page }) => {
  const realSockets = await installTerminalFixture(page)
  await beginAttempt(page)
  await startAttempt(page)
  await paste(page, 'discard pagehide\nfixture')
  await page.evaluate(() => {
    const canvas = document.querySelector('.terminal-canvas')!
    const input = document.querySelector('.xterm-helper-textarea')!
    window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }))
    const clipboardData = new DataTransfer()
    clipboardData.setData('text/plain', 'late fixture input')
    canvas.dispatchEvent(new ClipboardEvent('paste', { clipboardData, bubbles: true, cancelable: true }))
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'x', bubbles: true }))
    window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))
  })
  await expect(page.getByRole('dialog', { name: '确认多行粘贴' })).toHaveCount(0)
  await expect(page.locator('.xterm')).toHaveCount(0)
  await page.evaluate(() => {
    const socket = (window as unknown as FixtureWindow).terminalSockets[0]!
    socket.lateEvents()
    socket.finishClose()
  })
  expect(await inputFrames(page)).toEqual([])
  expect(await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets.length)).toBe(1)
  await expect(connectionStatus(page)).not.toHaveText('已连接')
  expect(realSockets).toEqual([])
})

test('退出会话在服务端响应前立即清理本地终端和待确认粘贴', async ({ page }) => {
  const realSockets = await installTerminalFixture(page)
  await beginAttempt(page)
  await startAttempt(page)
  await paste(page, 'discard logout\nfixture')
  let release!: () => void
  const held = new Promise<void>((resolve) => { release = resolve })
  let received = false
  await page.route('**/api/v1/auth/logout', async (route) => {
    received = true
    await held
    await route.fulfill({ json: {} })
  })
  try {
    await page.getByRole('button', { name: '退出登录', exact: true }).click()
    await expect.poll(() => received).toBe(true)
    await expect(page.locator('.xterm')).toHaveCount(0)
    await expect(page.getByRole('dialog', { name: '确认多行粘贴' })).toHaveCount(0)
    await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[0]!.lateEvents())
    expect(await inputFrames(page)).toEqual([])
    expect(await page.evaluate(() => (window as unknown as FixtureWindow).terminalSockets[0]!.closeCalls)).toBe(1)
    expect(realSockets).toEqual([])
  } finally { release() }
})
