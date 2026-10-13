import { writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'

type Callbacks = {
  message: ((event: MessageEvent) => void) | null
  error: ((event: Event) => void) | null
  close: ((event: CloseEvent) => void) | null
}
type Socket = {
  url: string
  readyState: number
  closeCalls: number
  sent: Array<{ kind: 'control' | 'input'; text: string }>
  saved: Callbacks
  snapshot(): void
  started(): void
  output(text: string): void
  malformed(): void
  finishClose(): void
  lateEvents(): void
}
type FixtureWindow = Window & { terminalTabSockets: Socket[] }
type Evidence = { unexpected: string[]; api: string[]; nativeSockets: string[] }
const evidence = new WeakMap<Page, Evidence>()
const tabs = (page: Page) => page.getByRole('tablist', { name: '终端标签', exact: true }).getByRole('tab')
const activePanel = (page: Page) => page.getByRole('tabpanel')
const panel = (page: Page, index: number) => page.locator('[role="tabpanel"]').nth(index)
const add = (page: Page) => page.getByRole('button', { name: '新建标签', exact: true })
const connect = (page: Page) => activePanel(page).locator('.terminal-toolbar').getByRole('button', { name: '连接终端', exact: true })
const status = (page: Page) => activePanel(page).locator('.terminal-frame > header [role="status"]')
async function closeTab(page: Page, index: number) {
  await tabs(page).nth(index).click()
  await expect(tabs(page).nth(index)).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('button', { name: new RegExp(`^关闭当前标签：终端 ${index + 1}，`) }).click()
}
const capacity = (page: Page) => page.locator('.terminal-capacity-note')

// 真 Chromium + 真实 xterm；只替换传输。所有 API fail-closed，禁止真实 WS、PTY、NAS。
async function install(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const recorded: Evidence = { unexpected: [], api: [], nativeSockets: [] }
  evidence.set(page, recorded)
  const allowed = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/summary',
    '/api/v1/docker/inventory', '/api/v1/system/capabilities', '/api/v1/sites',
  ])
  await page.route('**/api/**', route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    recorded.api.push(`${request.method()} ${path}`)
    if (request.method() === 'GET' && allowed.has(path)) return route.fallback()
    recorded.unexpected.push(`${request.method()} ${path}`)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_UNEXPECTED_API_BLOCKED' } })
  })
  await page.routeWebSocket('**/*', socket => socket.close({ code: 1008, reason: 'In-memory fixture only' }))
  page.on('websocket', socket => recorded.nativeSockets.push(socket.url()))
  await page.addInitScript(() => {
    const host = window as unknown as FixtureWindow
    host.terminalTabSockets = []
    class FakeSocket {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSING = 2
      static readonly CLOSED = 3
      readyState = FakeSocket.CONNECTING
      binaryType = 'blob'
      bufferedAmount = 0
      closeCalls = 0
      sent: Socket['sent'] = []
      saved: Callbacks = { message: null, error: null, close: null }
      onmessage: Callbacks['message'] = null
      onerror: Callbacks['error'] = null
      onclose: Callbacks['close'] = null
      constructor(readonly url: string) {
        if (new URL(url).pathname !== '/ws/terminal') throw new Error('Unexpected fixture socket')
        if (host.terminalTabSockets.filter(socket => socket.readyState !== FakeSocket.CLOSED).length >= 4) {
          throw new Error('Fixture permits at most four unreleased sockets')
        }
        host.terminalTabSockets.push(this)
      }
      send(data: string | ArrayBuffer | ArrayBufferView) {
        if (this.readyState !== FakeSocket.OPEN) throw new Error('Fixture socket is not open')
        this.sent.push(typeof data === 'string' ? { kind: 'control', text: data } : { kind: 'input', text: new TextDecoder().decode(data) })
      }
      close() { this.closeCalls++; this.readyState = FakeSocket.CLOSING }
      snapshot() { this.saved = { message: this.onmessage, error: this.onerror, close: this.onclose } }
      started() {
        this.readyState = FakeSocket.OPEN
        this.onmessage?.(new MessageEvent('message', { data: JSON.stringify({
          type: 'started', shell: 'bash', enhancement: 'readline',
          capabilities: { resize: false, readline: true, bracketedPaste: true, multilinePaste: true, ansiColors: true },
        }) }))
      }
      output(text: string) { this.onmessage?.(new MessageEvent('message', { data: new TextEncoder().encode(text).buffer })) }
      malformed() { this.onmessage?.(new MessageEvent('message', { data: '{invalid-json' })) }
      finishClose() { this.readyState = FakeSocket.CLOSED; this.onclose?.(new CloseEvent('close', { code: 1000 })) }
      lateEvents() {
        for (const data of [JSON.stringify({ type: 'started', shell: 'STALE' }), '{invalid-json',
          JSON.stringify({ type: 'error', code: 'TERMINAL_SESSION_FAILED' }), JSON.stringify({ type: 'closed' }),
          new TextEncoder().encode('STALE OUTPUT').buffer]) this.saved.message?.(new MessageEvent('message', { data }))
        this.saved.error?.(new Event('error'))
        // 不伪造 transport close；租约必须等 finishClose 明确到达。
      }
    }
    window.WebSocket = FakeSocket as unknown as typeof WebSocket
  })
  await page.goto('/terminal', { waitUntil: 'domcontentloaded' })
  await expect(connect(page)).toBeEnabled()
  await expect(tabs(page)).toHaveCount(1)
  // tablist 仅拥有 tab；普通关闭动作置于外部，避免 aria-required-children 回归。
  await expect(page.getByRole('tablist', { name: '终端标签', exact: true }).getByRole('button')).toHaveCount(0)
  await expect(page.locator('.terminal-tablist > [role="tab"]')).toHaveCount(1)
  await expect(page.getByRole('button', { name: /^关闭当前标签：/ })).toHaveCount(1)
  await expect.poll(() => recorded.api.filter(path => path === 'GET /api/v1/auth/status').length).toBeGreaterThan(0)
  await expect.poll(() => recorded.api.filter(path => path === 'GET /api/v1/docker/inventory').length).toBeGreaterThan(0)
}

test.afterEach(async ({ page }) => {
  expect(evidence.get(page)?.unexpected ?? [], '未模拟 API 必须 fail-closed').toEqual([])
  expect(evidence.get(page)?.nativeSockets ?? [], '不得创建真实 WebSocket').toEqual([])
})

const count = (page: Page) => page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets.length)
const frames = (page: Page, index: number) => page.evaluate(index => (window as unknown as FixtureWindow).terminalTabSockets[index]!.sent.filter(frame => frame.kind === 'input').map(frame => frame.text), index)
const closes = (page: Page) => page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets.map(socket => socket.closeCalls))
async function begin(page: Page, expectedCount: number, ready = true) {
  await connect(page).click()
  await expect.poll(() => count(page)).toBe(expectedCount)
  await page.evaluate(ready => {
    const socket = (window as unknown as FixtureWindow).terminalTabSockets.at(-1)!
    socket.snapshot()
    if (ready) socket.started()
  }, ready)
  await expect(status(page)).toHaveText(ready ? '已连接' : '连接中')
}
async function paste(target: Locator, text: string) {
  await target.locator('.terminal-canvas').evaluate((element, text) => {
    const clipboardData = new DataTransfer()
    clipboardData.setData('text/plain', text)
    element.dispatchEvent(new ClipboardEvent('paste', { clipboardData, bubbles: true, cancelable: true }))
  }, text)
}
async function twoConnections(page: Page) {
  await begin(page, 1)
  await add(page).click()
  await activePanel(page).getByRole('button', { name: /Docker 容器/ }).click()
  await activePanel(page).getByRole('combobox').click()
  await page.getByRole('option', { name: /media-api ·/ }).click()
  await begin(page, 2)
}

async function captureAndCheck(page: Page, info: TestInfo, name: string) {
  await page.evaluate(() => document.fonts.ready)
  // 只观测，不重置滚动或焦点；保留真实关闭确认焦点与截图/axe 的阶段边界。
  const snapshot = (phase: string) => page.evaluate(phase => {
    const describe = (element: Element | null) => {
      if (!element) return null
      const { x, y, width, height, right, bottom } = element.getBoundingClientRect()
      const style = getComputedStyle(element)
      return {
        tag: element.tagName, id: element.id, className: element.getAttribute('class'),
        role: element.getAttribute('role'), ariaLabel: element.getAttribute('aria-label'),
        text: element.textContent?.trim().slice(0, 160), focused: element === document.activeElement,
        rect: { x, y, width, height, right, bottom },
        style: { position: style.position, top: style.top, transform: style.transform, visibility: style.visibility, display: style.display },
      }
    }
    const viewport = window.visualViewport
    return {
      phase, timestamp: performance.now(), documentHasFocus: document.hasFocus(),
      activeElement: describe(document.activeElement),
      scroll: { x: scrollX, y: scrollY, documentTop: document.documentElement.scrollTop, documentLeft: document.documentElement.scrollLeft, bodyTop: document.body.scrollTop, bodyLeft: document.body.scrollLeft },
      viewport: { width: innerWidth, height: innerHeight, documentWidth: document.documentElement.scrollWidth, documentHeight: document.documentElement.scrollHeight },
      visualViewport: viewport ? { width: viewport.width, height: viewport.height, offsetLeft: viewport.offsetLeft, offsetTop: viewport.offsetTop, pageLeft: viewport.pageLeft, pageTop: viewport.pageTop, scale: viewport.scale } : null,
      topbar: describe(document.querySelector('.topbar')),
      skipLink: describe(document.querySelector('.skip-link')),
      heading: describe(document.querySelector('.terminal-page h1')),
      closeCancel: describe(document.querySelector('.terminal-close-actions > button:first-child')),
      scrollers: Array.from(document.querySelectorAll('.terminal-tablist, .xterm-viewport'), element => ({ id: element.id, className: element.className, left: element.scrollLeft, top: element.scrollTop })),
    }
  }, phase)
  const capturePhases = [await snapshot('before-png')]
  const path = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path, fullPage: true, animations: 'disabled' })
  capturePhases.push(await snapshot('after-png'))
  await info.attach(name, { path, contentType: 'image/png' })
  const geometry = await page.locator('.terminal-page').evaluate(root => {
    const rect = (element: Element) => {
      const { x, y, width, height, right, bottom } = element.getBoundingClientRect()
      return { x, y, width, height, right, bottom }
    }
    const nodes = Array.from(root.querySelectorAll('.terminal-tabs-panel, .terminal-close-confirm, .terminal-capacity-note, .terminal-retired-status, .terminal-close-actions button, .terminal-add-tab, .terminal-tab-close'))
    return {
      viewport: innerWidth, documentWidth: document.documentElement.scrollWidth,
      panels: Array.from(root.querySelectorAll('[role="tabpanel"]'), element => ({ id: element.id, hidden: (element as HTMLElement).hidden, inert: (element as HTMLElement).inert })),
      nodes: nodes.map(element => {
        const range = document.createRange()
        range.selectNodeContents(element)
        return { selector: element.className, text: element.textContent, rect: rect(element), clientWidth: element.clientWidth, scrollWidth: element.scrollWidth,
          lines: Array.from(range.getClientRects(), box => ({ x: box.x, right: box.right, y: box.y, bottom: box.bottom })) }
      }),
      sockets: (window as unknown as FixtureWindow).terminalTabSockets.map(socket => ({ url: socket.url, readyState: socket.readyState, closeCalls: socket.closeCalls })),
    }
  })
  const jsonPath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  // 先落盘 PNG 和 geometry，即使之后 axe 或布局断言失败也保留现场。
  const axe = new AxeBuilder({ page }).include('.terminal-tabs-panel')
  if (await page.locator('.terminal-close-confirm').count()) axe.include('.terminal-close-confirm')
  capturePhases.push(await snapshot('before-axe'))
  const writeGeometry = () => writeFile(jsonPath, JSON.stringify({ ...geometry, capturePhases }, null, 2))
  await writeGeometry()
  const accessibility = await axe.analyze().finally(async () => {
    capturePhases.push(await snapshot('after-axe'))
    await writeGeometry()
    await info.attach(`${name}-geometry`, { path: jsonPath, contentType: 'application/json' })
  })
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(axePath, JSON.stringify(accessibility, null, 2))
  await info.attach(`${name}-axe`, { path: axePath, contentType: 'application/json' })
  expect(accessibility.violations).toEqual([])
  await expect(page.getByRole('tablist', { name: '终端标签', exact: true }).getByRole('button')).toHaveCount(0)
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewport)
  expect(geometry.panels.filter(item => !item.hidden)).toHaveLength(1)
  expect(geometry.panels.filter(item => item.hidden).every(item => item.inert)).toBe(true)
  for (const node of geometry.nodes) {
    expect(node.rect.width).toBeGreaterThan(0)
    expect(node.rect.x).toBeGreaterThanOrEqual(-1)
    expect(node.rect.right).toBeLessThanOrEqual(geometry.viewport + 1)
    expect(node.scrollWidth).toBeLessThanOrEqual(node.clientWidth + 1)
    if (/terminal-(capacity-note|retired-status)/.test(node.selector)) {
      for (const line of node.lines) {
        expect(line.x).toBeGreaterThanOrEqual(node.rect.x - 1)
        expect(line.right).toBeLessThanOrEqual(node.rect.right + 1)
        expect(line.bottom).toBeLessThanOrEqual(node.rect.bottom + 1)
      }
    }
  }
}

test('创建、切换、键盘导航不连接；关闭最后标签留下新空白标签', async ({ page }) => {
  await install(page)
  await add(page).click()
  await add(page).click()
  await expect(tabs(page)).toHaveCount(3)
  await tabs(page).nth(2).focus()
  for (const [key, index] of [['Home', 0], ['ArrowLeft', 2], ['ArrowRight', 0], ['End', 2]] as const) {
    await page.keyboard.press(key)
    await expect(tabs(page).nth(index)).toBeFocused()
    await expect(tabs(page).nth(index)).toHaveAttribute('aria-selected', 'true')
    await expect(page.locator('[role="tab"][tabindex="0"]')).toHaveCount(1)
  }
  expect(await count(page)).toBe(0)
  await closeTab(page, 2)
  await closeTab(page, 1)
  const oldId = await tabs(page).first().getAttribute('id')
  await closeTab(page, 0)
  await expect(tabs(page)).toHaveCount(1)
  expect(await tabs(page).first().getAttribute('id')).not.toBe(oldId)
  await expect(connect(page)).toBeEnabled()
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  expect(await count(page)).toBe(0)
})

test('独立主机与容器输出保留；隐藏标签 inert 且拒绝输入、粘贴和焦点偷取', async ({ page }) => {
  await install(page)
  await twoConnections(page)
  expect(await page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets.map(socket => {
    const url = new URL(socket.url)
    return { target: url.searchParams.get('target'), container: url.searchParams.get('containerId') }
  }))).toEqual([{ target: 'host', container: null }, { target: 'container', container: 'media-api-id' }])
  await page.evaluate(() => {
    const sockets = (window as unknown as FixtureWindow).terminalTabSockets
    sockets[0]!.output('\r\nHOST_ONLY_OUTPUT\r\n')
    sockets[1]!.output('\r\nCONTAINER_ONLY_OUTPUT\r\n')
  })
  await expect(panel(page, 1).locator('.xterm-rows')).toContainText('CONTAINER_ONLY_OUTPUT')
  await expect(panel(page, 1).locator('.xterm-rows')).not.toContainText('HOST_ONLY_OUTPUT')
  await expect(panel(page, 0)).toHaveAttribute('inert', '')
  await expect(panel(page, 0)).toBeHidden()
  await tabs(page).nth(1).focus()
  await panel(page, 0).locator('.xterm-helper-textarea').evaluate(element => {
    ;(element as HTMLTextAreaElement).focus()
    element.dispatchEvent(new KeyboardEvent('keydown', { key: 'x', bubbles: true }))
  })
  await paste(panel(page, 0), 'hidden input')
  await expect(tabs(page).nth(1)).toBeFocused()
  expect(await frames(page, 0)).toEqual([])
  await paste(activePanel(page), 'container input')
  expect(await frames(page, 1)).toEqual(['\u001b[200~container input\u001b[201~'])
  await tabs(page).nth(0).click()
  await expect(activePanel(page).locator('.xterm-rows')).toContainText('HOST_ONLY_OUTPUT')
  await expect(activePanel(page).locator('.xterm-rows')).not.toContainText('CONTAINER_ONLY_OUTPUT')
  await activePanel(page).locator('.xterm-helper-textarea').focus()
  await page.keyboard.press('h')
  await expect.poll(() => frames(page, 0)).toEqual(['h'])
  expect(await count(page)).toBe(2)
  expect(await closes(page)).toEqual([0, 0])
})

for (const width of [390, 1440]) {
  test(`标签 ${width}px：两/四标签、关闭确认与退役租约证据；第五连接在精确 close 前拒绝`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    await install(page)
    await twoConnections(page)
    await captureAndCheck(page, info, `terminal-tabs-two-${width}`)
    await add(page).click(); await begin(page, 3)
    await add(page).click(); await begin(page, 4)
    await expect(add(page)).toBeDisabled()
    await expect(capacity(page)).toContainText('本页连接 4/4')
    await captureAndCheck(page, info, `terminal-tabs-four-${width}`)
    await closeTab(page, 3)
    await expect(page.getByRole('alertdialog')).toContainText('不能保证远端任务已结束')
    await expect(page.getByRole('button', { name: '保留标签', exact: true })).toBeFocused()
    await captureAndCheck(page, info, `terminal-tabs-close-confirm-${width}`)
    await page.getByRole('button', { name: '保留标签', exact: true }).click()
    expect(await closes(page)).toEqual([0, 0, 0, 0])
    await closeTab(page, 3)
    await page.getByRole('button', { name: '断开并关闭标签', exact: true }).click()
    await expect(tabs(page)).toHaveCount(3)
    expect(await closes(page)).toEqual([0, 0, 0, 1])
    await expect(page.locator('.terminal-retired-status')).toContainText('1 个已关闭标签的本地连接仍在关闭中')
    await expect(capacity(page)).toContainText('本页连接 4/4')
    await captureAndCheck(page, info, `terminal-tabs-retired-closing-${width}`)
    await add(page).click()
    await expect(connect(page)).toBeDisabled()
    expect(await count(page)).toBe(4)
    await page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets[3]!.lateEvents())
    await expect(connect(page)).toBeDisabled()
    await page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets[3]!.finishClose())
    await expect(capacity(page)).toContainText('本页连接 3/4')
    await expect(page.locator('.terminal-retired-status')).toHaveCount(0)
    await begin(page, 5)
    await page.evaluate(() => {
      const old = (window as unknown as FixtureWindow).terminalTabSockets[3]!
      old.lateEvents()
      old.saved.close?.(new CloseEvent('close', { code: 1006 }))
    })
    await expect(status(page)).toHaveText('已连接')
    await expect(capacity(page)).toContainText('本页连接 4/4')
    expect(await closes(page)).toEqual([0, 0, 0, 1, 0])
  })
}

test('切换清除待粘贴；快捷键可 Escape 关闭；连接中关闭先确认并可取消', async ({ page }) => {
  await install(page)
  await begin(page, 1)
  await paste(activePanel(page), 'pending\nnever sent')
  await expect(page.getByRole('dialog', { name: '确认多行粘贴' })).toBeVisible()
  await add(page).click()
  await expect(page.getByRole('dialog', { name: '确认多行粘贴', includeHidden: true })).toHaveCount(0)
  await begin(page, 2, false)
  await closeTab(page, 1)
  await expect(page.getByRole('alertdialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(tabs(page).nth(1)).toBeFocused()
  expect(await closes(page)).toEqual([0, 0])
  await closeTab(page, 1)
  await page.getByRole('button', { name: '断开并关闭标签', exact: true }).click()
  expect(await closes(page)).toEqual([0, 1])
  await activePanel(page).getByRole('button', { name: '快捷键', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '终端快捷键与粘贴' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '终端快捷键与粘贴' })).toHaveCount(0)
  await add(page).click()
  await expect(tabs(page)).toHaveCount(2)
  expect(await frames(page, 0)).toEqual([])
})

test('隐藏连接的晚到 started 不抢焦点，返回标签保留输出且不自动重连', async ({ page }) => {
  await install(page)
  await begin(page, 1, false)
  await add(page).click()
  await begin(page, 2)
  await tabs(page).nth(1).focus()
  await page.evaluate(() => {
    const first = (window as unknown as FixtureWindow).terminalTabSockets[0]!
    first.started()
    first.output('DELAYED_HOST_OUTPUT\r\n')
  })
  await expect(tabs(page).nth(1)).toBeFocused()
  await expect(tabs(page).nth(0)).toHaveAccessibleName(/已连接/)
  await expect(status(page)).toHaveText('已连接')
  await tabs(page).nth(0).click()
  await expect(activePanel(page).locator('.xterm-rows')).toContainText('DELAYED_HOST_OUTPUT')
  await expect(tabs(page).nth(0)).toBeFocused()
  expect(await count(page)).toBe(2)
  expect(await closes(page)).toEqual([0, 0])
})

test('隐藏标签格式错误仅关闭该连接；晚到回调不污染另一标签', async ({ page }) => {
  await install(page)
  await twoConnections(page)
  await tabs(page).nth(1).focus()
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets[0]!.malformed())
  expect(await closes(page)).toEqual([1, 0])
  await expect(status(page)).toHaveText('已连接')
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets[0]!.lateEvents())
  await expect(tabs(page).nth(1)).toBeFocused()
  await expect(status(page)).toHaveText('已连接')
  await paste(activePanel(page), 'still alive')
  expect(await frames(page, 1)).toEqual(['\u001b[200~still alive\u001b[201~'])
  expect(await frames(page, 0)).toEqual([])
  await tabs(page).nth(0).click()
  await expect(status(page)).toHaveText('关闭中')
  await expect(activePanel(page).locator('.terminal-live-status')).toContainText('终端数据处理失败')
  await page.evaluate(() => (window as unknown as FixtureWindow).terminalTabSockets[0]!.finishClose())
  await expect(status(page)).toHaveText('连接失败')
  expect(await closes(page)).toEqual([1, 0])
})

for (const action of ['pagehide', 'logout'] as const) {
  test(`${action} 同步撤销所有标签；晚到输出不能恢复、输入不再发送`, async ({ page }) => {
    await install(page)
    await twoConnections(page)
    await paste(activePanel(page), 'pending\nnever sent')
    let release: (() => void) | undefined
    let received = false
    if (action === 'logout') {
      const held = new Promise<void>(resolve => { release = resolve })
      await page.route('**/api/v1/auth/logout', async route => {
        const method = route.request().method()
        evidence.get(page)!.api.push(`${method} /api/v1/auth/logout`)
        if (method !== 'POST') {
          evidence.get(page)!.unexpected.push(`${method} /api/v1/auth/logout`)
          await route.fulfill({ status: 503, json: { code: 'FIXTURE_UNEXPECTED_API_BLOCKED' } })
          return
        }
        received = true
        await held
        await route.fulfill({ json: {} })
      })
    }
    try {
      if (action === 'logout') {
        await page.getByRole('button', { name: '退出登录', exact: true }).click()
        await expect.poll(() => received).toBe(true)
      } else await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true })))
      await expect(page.locator('.xterm')).toHaveCount(0)
      await expect(page.getByRole('dialog', { name: '确认多行粘贴', includeHidden: true })).toHaveCount(0)
      expect(await closes(page)).toEqual([1, 1])
      await page.evaluate(() => {
        for (const socket of (window as unknown as FixtureWindow).terminalTabSockets) socket.lateEvents()
        window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))
      })
      expect(await count(page)).toBe(2)
      expect(await frames(page, 0)).toEqual([])
      expect(await frames(page, 1)).toEqual([])
      await expect(page.locator('.xterm')).toHaveCount(0)
      await expect(capacity(page)).toContainText('本页连接 2/4')
      await page.evaluate(() => { for (const socket of (window as unknown as FixtureWindow).terminalTabSockets) socket.finishClose() })
      await expect(capacity(page)).toContainText('本页连接 0/4')
    } finally { release?.() }
  })
}
