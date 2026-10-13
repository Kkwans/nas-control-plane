import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { overviewDocker, summary } from './mockApi'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })

const epoch = Date.parse('2026-10-13T12:00:00.000Z')
const at = (seconds = 0) => new Date(epoch + seconds * 1000).toISOString()
const summaryPath = '/api/v1/system/summary'
const inventoryPath = '/api/v1/docker/inventory'
const failure = 'REALTIME_STREAM_FAILED'
const refresh = (page: Page) => page.getByRole('button', { name: '立即刷新数据', exact: true })
const badge = (page: Page) => page.locator('.topbar .connection-state')
const cpu = (page: Page) => page.locator('.metric-card').filter({ hasText: 'CPU 使用率' }).locator('strong')
function snapshot(seconds = 0, usagePercent = 24.5) {
  return {
    collectedAt: at(seconds),
    summary: { ...summary, collectedAt: at(seconds), cpu: { ...summary.cpu, usagePercent } },
    docker: { ...overviewDocker, collectedAt: at(seconds) },
  }
}
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }
type SourceState = { id: number; url: string; readyState: number; closes: number; snapshots: number }
type Command = { id: number; action: 'open' | 'connecting' | 'closed' | 'raw' | 'queued'; raw?: string }
type Projection = {
  summary: typeof summary | null; inventory: typeof overviewDocker | null
  errorCodes: string[]; realtimeState: string; connectionState: string; realtimeScopes: string[]; isRefreshing: boolean
}

// 与共享自动推送 fixture 隔离：CLOSED 不再发帧、不自动 open，迟到回调须显式标记 queued。
// 全部 API（含唯一的退出请求）在内存中响应；仅预览页面及其静态资源可到达 CI 服务器。
async function installFixture(page: Page, baseURL: string) {
  await page.clock.install({ time: new Date(at(-1)) })
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000))
  await page.clock.setSystemTime(new Date(at()))
  await page.addInitScript(() => {
    const sources: FixtureEventSource[] = []
    class FixtureEventSource extends EventTarget {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSED = 2
      readonly id: number
      readonly url: string
      readonly withCredentials = false
      readyState = FixtureEventSource.CONNECTING
      closes = 0
      snapshots = 0
      onopen: ((event: Event) => void) | null = null
      onerror: ((event: Event) => void) | null = null
      constructor(url: string | URL) {
        super(); this.id = sources.length + 1; this.url = String(url); sources.push(this)
      }
      close() { this.closes += 1; this.readyState = FixtureEventSource.CLOSED }
    }
    const fixture = window as typeof window & { __closedStreamSources: () => SourceState[] }
    fixture.__closedStreamSources = () => sources.map(({ id, url, readyState, closes, snapshots }) => ({ id, url, readyState, closes, snapshots }))
    window.addEventListener('ncp:closed-stream', (event) => {
      const command = (event as CustomEvent<Command>).detail
      const source = sources.find((candidate) => candidate.id === command.id)
      if (!source) throw new Error('未找到 fixture EventSource')
      if (command.action === 'queued') {
        // 模拟关闭前已排队的回调，不能将源伪装成重新连接。
        source.onopen?.(new Event('open'))
        source.snapshots += 1
        source.dispatchEvent(new MessageEvent('snapshot', { data: command.raw }))
        source.onerror?.(new Event('error'))
      } else if (source.readyState !== FixtureEventSource.CLOSED) {
        if (command.action === 'open') { source.readyState = FixtureEventSource.OPEN; source.onopen?.(new Event('open')) }
        if (command.action === 'connecting') { source.readyState = FixtureEventSource.CONNECTING; source.onerror?.(new Event('error')) }
        if (command.action === 'closed') { source.readyState = FixtureEventSource.CLOSED; source.onerror?.(new Event('error')) }
        if (command.action === 'raw' && source.readyState === FixtureEventSource.OPEN) {
          source.snapshots += 1
          source.dispatchEvent(new MessageEvent('snapshot', { data: command.raw }))
        }
      }
    })
    window.EventSource = FixtureEventSource as unknown as typeof EventSource
  })
  const requests: string[] = []
  const denied: string[] = []
  const pageErrors: string[] = []
  const emissions: Command[] = []
  let payload = snapshot(1, 31)
  let gate: Gate | undefined
  page.on('pageerror', (error) => pageErrors.push(error.message))
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', {
      refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10,
      sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system',
      navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'],
    }],
    ['/api/v1/sites', {
      collectedAt: at(), sites: [],
      discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] },
    }],
    ['/api/v1/sites/ignored', []],
  ])
  await page.routeWebSocket('**/*', (socket) => { denied.push(`WebSocket ${socket.url()}`); socket.close() })
  await page.route('**/*', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const target = `${request.method()} ${url.pathname}`
    if (url.origin !== new URL(baseURL).origin) {
      denied.push(`${request.method()} ${url.href}`)
      return route.abort('blockedbyclient')
    }
    if (!url.pathname.startsWith('/api/')) {
      if (request.method() === 'GET' && (['/', '/favicon.svg'].includes(url.pathname) || url.pathname.startsWith('/assets/'))) return route.continue()
      denied.push(target)
      return route.abort('blockedbyclient')
    }
    requests.push(target)
    if (request.method() === 'GET' && url.pathname === summaryPath) {
      const body = structuredClone(payload.summary)
      const held = gate
      gate = undefined
      try {
        if (held) { held.received.resolve(); await held.release.promise }
        await route.fulfill({ json: body })
      } finally { held?.finished.resolve() }
      return
    }
    if (request.method() === 'GET' && url.pathname === inventoryPath) return route.fulfill({ json: payload.docker })
    if (request.method() === 'GET' && reads.has(url.pathname)) return route.fulfill({ json: reads.get(url.pathname) })
    if (request.method() === 'POST' && url.pathname === '/api/v1/auth/logout') return route.fulfill({ status: 204 })
    denied.push(target)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return {
    requests, denied, pageErrors, emissions,
    count(path: string) { return requests.filter((request) => request === `GET ${path}`).length },
    respondWith(value: ReturnType<typeof snapshot>) { payload = value },
    hold() {
      expect(gate, '不得覆盖待读取闸门').toBeUndefined()
      gate = { received: deferred(), release: deferred(), finished: deferred() }
      return gate
    },
    async emit(command: Command) {
      emissions.push(command)
      await page.evaluate((detail) => window.dispatchEvent(new CustomEvent('ncp:closed-stream', { detail })), command)
      await settle(page)
    },
  }
}
type Fixture = Awaited<ReturnType<typeof installFixture>>
async function settle(page: Page) { await page.clock.runFor(34) }
async function sources(page: Page) {
  return page.evaluate(() => (window as typeof window & { __closedStreamSources: () => SourceState[] }).__closedStreamSources())
}
// 仅检查公开 Pinia 状态投影；恢复、导航及退出均通过实际页面控件触发。
async function state(page: Page) {
  return page.evaluate(() => {
    const app = document.getElementById('app') as HTMLElement & {
      __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, Projection> } } } }
    }
    const store = app.__vue_app__.config.globalProperties.$pinia._s.get('system')!
    return JSON.parse(JSON.stringify({
      summary: store.summary, inventory: store.inventory, errorCodes: store.errorCodes,
      realtimeState: store.realtimeState, connectionState: store.connectionState,
      realtimeScopes: store.realtimeScopes, isRefreshing: store.isRefreshing,
    })) as Projection
  })
}
async function start(page: Page, baseURL: string) {
  const api = await installFixture(page, baseURL)
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: '系统总览', exact: true })).toBeVisible()
  await expect.poll(async () => (await sources(page)).length).toBe(1)
  expect((await sources(page))[0]!.url).toBe('/api/v1/system/events?interval=5&scope=docker&scope=summary')
  await api.emit({ id: 1, action: 'open' })
  await api.emit({ id: 1, action: 'raw', raw: JSON.stringify(snapshot()) })
  await expect(badge(page)).toHaveClass(/connection-state--connected/)
  await expect(cpu(page)).toHaveText('24.5%')
  await expect(refresh(page)).toBeEnabled()
  return api
}
function expectSafe(api: Fixture, logout = false) {
  expect(api.denied).toEqual([])
  expect(api.pageErrors).toEqual([])
  expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual(logout ? ['POST /api/v1/auth/logout'] : [])
}

// 完整 axe 使用逐帧时钟；仅容忍其已关闭 about:blank 汇总页引发的已知 clock 竞争。
async function analyze(page: Page) {
  const context = page.context()
  const auxiliaryPages = new Set<Page>()
  let contextClosed = false
  const onPage = (auxiliary: Page) => auxiliaryPages.add(auxiliary)
  const onClose = () => { contextClosed = true }
  const alive = () => !contextClosed && !page.isClosed() && context.browser()?.isConnected() === true
  context.on('page', onPage); context.on('close', onClose)
  let finished = false
  let rejected = false
  let result!: Awaited<ReturnType<AxeBuilder['analyze']>>
  let failure: unknown
  try {
    const pending = new AxeBuilder({ page }).analyze().then((value) => { result = value; finished = true }, (error: unknown) => { failure = error; rejected = true; finished = true })
    for (let elapsed = 0; !finished && elapsed < 2000; elapsed += 16) {
      try { await page.clock.runFor(16) } catch (error) {
        const summaryClosed = auxiliaryPages.size === 1 && [...auxiliaryPages].every((auxiliary) => auxiliary.url() === 'about:blank' && auxiliary.isClosed())
        if (!(error instanceof Error) || !/^clock\.runFor: Target page, context or browser has been closed(?:\n|$)/.test(error.message) || !summaryClosed || !alive()) throw error
        let timeout: ReturnType<typeof setTimeout> | undefined
        try {
          await Promise.race([pending, new Promise<never>((_, reject) => { timeout = setTimeout(() => reject(error), 1000) })])
        } finally { clearTimeout(timeout) }
        if (rejected) throw failure
        if (!alive()) throw error
      }
    }
    expect(finished, 'axe 未在受控 2 秒时钟内结束').toBe(true)
    await pending
    if (rejected) throw failure
    return result
  } finally { context.off('page', onPage); context.off('close', onClose) }
}
async function capture(page: Page, info: TestInfo, api: Fixture, name: string) {
  await page.evaluate(() => document.fonts.ready)
  await page.mouse.move(0, 0)
  await settle(page)
  const store = await state(page)
  const sourcesBefore = await sources(page)
  const clockBefore = await page.evaluate(() => ({ now: Date.now(), performance: performance.now() }))
  const requests = [...api.requests]
  const controls = await page.locator('.topbar .connection-state, .topbar .refresh-button').evaluateAll((nodes) => nodes.map((node) => {
    const bounds = node.getBoundingClientRect()
    return { text: node.textContent?.trim(), bounds: bounds.toJSON(), scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, viewport: innerWidth }
  }))
  const stem = `closed-stream-${name}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${stem}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const axe = await analyze(page)
  const json = info.outputPath('ui-captures', `${stem}-capture-state.json`)
  await writeFile(json, JSON.stringify({
    fixtureOnly: true, sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    fixtureSource: 'frontend/e2e/closed-stream-recovery.spec.ts + frontend/e2e/mockApi.ts',
    fixtureSha256: createHash('sha256').update(await readFile(new URL('./closed-stream-recovery.spec.ts', import.meta.url))).digest('hex'),
    mockApiSha256: createHash('sha256').update(await readFile(new URL('./mockApi.ts', import.meta.url))).digest('hex'),
    testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
    screenshot: `${stem}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    controls, store, afterAxe: await state(page), sourcesBefore, sources: await sources(page),
    clockBefore, clockAfter: await page.evaluate(() => ({ now: Date.now(), performance: performance.now() })),
    requestsBefore: requests, requestsAfter: api.requests, denied: api.denied, pageErrors: api.pageErrors, emissions: api.emissions,
    axe,
  }, null, 2))
  await info.attach(stem, { path: png, contentType: 'image/png' })
  await info.attach(`${stem}-state`, { path: json, contentType: 'application/json' })
  expect.soft(await state(page)).toEqual(store)
  expect.soft(await sources(page)).toEqual(sourcesBefore)
  expect.soft(api.requests).toEqual(requests)
  for (const control of controls) {
    expect.soft(control.bounds.left).toBeGreaterThanOrEqual(0)
    expect.soft(control.bounds.right).toBeLessThanOrEqual(control.viewport)
    expect.soft(control.scrollWidth).toBeLessThanOrEqual(control.clientWidth + 1)
  }
  expect.soft(axe.violations).toEqual([])
}

const invalidFrames = [
  { name: '坏 JSON', raw: '{invalid', width: 1440, capture: true },
  { name: '坏信封', raw: JSON.stringify({ collectedAt: at(), errors: [7] }), width: 1440, capture: false },
  { name: '坏摘要', raw: JSON.stringify({ collectedAt: at(), summary: {} }), width: 1440, capture: false },
  { name: '坏 JSON', raw: '{invalid', width: 390, capture: true },
]
for (const invalid of invalidFrames) {
  test(`${invalid.width}px ${invalid.name} 关闭流，顶部刷新只替换一次且仅新鲜 SSE 证明恢复`, async ({ page, baseURL }, info) => {
    await page.setViewportSize({ width: invalid.width, height: 900 })
    const api = await start(page, baseURL!)
    await page.clock.runFor(1000)
    // OPEN 时先点击得到挂起 GET；期间坏帧终止，GET 完成不能重连或标绿。
    const first = api.hold()
    try {
      await refresh(page).click()
      await first.received.promise
      await api.emit({ id: 1, action: 'raw', raw: invalid.raw })
      expect(await sources(page)).toEqual([expect.objectContaining({ id: 1, readyState: 2, closes: 1 })])
      await expect(badge(page)).toContainText('实时连接中断')
    } finally { first.release.resolve() }
    await first.finished.promise
    await expect(refresh(page)).toBeEnabled()
    await expect(cpu(page)).toHaveText('31.0%')
    expect(await state(page)).toMatchObject({ errorCodes: [failure], realtimeState: 'offline', connectionState: 'degraded' })
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await page.clock.runFor(1000)
    // 正常发帧不能穿过 CLOSED，刷新/可见性/时钟也不自动创建替代源。
    await api.emit({ id: 1, action: 'open' })
    await api.emit({ id: 1, action: 'raw', raw: JSON.stringify(snapshot(2, 99)) })
    expect((await sources(page))).toHaveLength(1)
    await expect(cpu(page)).toHaveText('31.0%')
    if (invalid.capture) await capture(page, info, api, 'closed-after-get')

    const second = api.hold()
    try {
      await refresh(page).click()
      await second.received.promise
      expect(await sources(page)).toEqual([
        expect.objectContaining({ id: 1, readyState: 2 }), expect.objectContaining({ id: 2, readyState: 0 }),
      ])
      expect((await sources(page))[1]!.url).toBe((await sources(page))[0]!.url)
      await expect(refresh(page)).toBeDisabled()
      // 第二次真实指针点击禁用按钮，不直接调用组件或 store。
      const bounds = (await refresh(page).boundingBox())!
      await page.mouse.click(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
      await settle(page)
      expect(api.count(summaryPath)).toBe(2)
      expect(api.count(inventoryPath)).toBe(2)
      expect((await sources(page))).toHaveLength(2)
      await api.emit({ id: 1, action: 'queued', raw: JSON.stringify(snapshot(2, 99)) })
      expect(await state(page)).toMatchObject({ errorCodes: [failure], realtimeState: 'connecting' })
      await expect(cpu(page)).toHaveText('31.0%')
    } finally { second.release.resolve() }
    await second.finished.promise
    await expect(refresh(page)).toBeEnabled()
    await api.emit({ id: 2, action: 'open' })
    await expect(badge(page)).toContainText('实时连接中断')
    await api.emit({ id: 2, action: 'raw', raw: JSON.stringify(snapshot(1, 31)) })
    expect((await state(page)).errorCodes).toEqual([failure])
    await api.emit({ id: 2, action: 'raw', raw: JSON.stringify(snapshot(2, 42)) })
    await expect(badge(page)).toHaveClass(/connection-state--connected/)
    await expect(cpu(page)).toHaveText('42.0%')
    expect((await state(page)).errorCodes).toEqual([])
    await api.emit({ id: 1, action: 'queued', raw: JSON.stringify(snapshot(3, 99)) })
    await expect(cpu(page)).toHaveText('42.0%')
    await expect(badge(page)).toHaveClass(/connection-state--connected/)
    if (invalid.capture) await capture(page, info, api, 'fresh-sse-recovered')
    expect((await sources(page))).toHaveLength(2)
    expect(api.count(summaryPath)).toBe(2)
    expect(api.count(inventoryPath)).toBe(2)
    expectSafe(api)
  })
}

test('原生 CONNECTING 保留同一源，顶部 GET 和 open 不能清除断流错误', async ({ page, baseURL }) => {
  const api = await start(page, baseURL!)
  await page.clock.runFor(1000)
  await api.emit({ id: 1, action: 'connecting' })
  await refresh(page).click()
  await expect(refresh(page)).toBeEnabled()
  await expect(cpu(page)).toHaveText('31.0%')
  expect(await sources(page)).toEqual([expect.objectContaining({ id: 1, readyState: 0, closes: 0 })])
  await expect(badge(page)).toContainText('实时连接中断')
  await api.emit({ id: 1, action: 'open' })
  expect((await state(page)).errorCodes).toEqual([failure])
  await page.clock.runFor(1000)
  await api.emit({ id: 1, action: 'raw', raw: JSON.stringify(snapshot(2, 42)) })
  await expect(badge(page)).toHaveClass(/connection-state--connected/)
  expect(await sources(page)).toEqual([expect.objectContaining({ id: 1, readyState: 1, closes: 0 })])
  expect(api.count(summaryPath)).toBe(1)
  expect(api.count(inventoryPath)).toBe(1)
  expectSafe(api)
})


test('原生 CLOSED 错误也仅由顶部刷新重建，GET 完成后仍需新鲜 SSE', async ({ page, baseURL }) => {
  const api = await start(page, baseURL!)
  await page.clock.runFor(1000)
  await api.emit({ id: 1, action: 'closed' })
  expect(await sources(page)).toEqual([expect.objectContaining({ id: 1, readyState: 2, closes: 0 })])
  await refresh(page).click()
  await expect(refresh(page)).toBeEnabled()
  await expect(cpu(page)).toHaveText('31.0%')
  expect(await sources(page)).toEqual([
    expect.objectContaining({ id: 1, readyState: 2, closes: 1 }),
    expect.objectContaining({ id: 2, readyState: 0, closes: 0 }),
  ])
  expect((await state(page)).errorCodes).toEqual([failure])
  await page.clock.runFor(1000)
  await api.emit({ id: 2, action: 'open' })
  expect((await state(page)).errorCodes).toEqual([failure])
  await api.emit({ id: 2, action: 'raw', raw: JSON.stringify(snapshot(2, 42)) })
  await expect(cpu(page)).toHaveText('42.0%')
  await expect(badge(page)).toHaveClass(/connection-state--connected/)
  expect(api.count(summaryPath)).toBe(1)
  expect(api.count(inventoryPath)).toBe(1)
  expectSafe(api)
})

for (const boundary of ['route', 'logout'] as const) {
  test(`${boundary} 在恢复 GET 未结束时清理，旧源和晚到结果不得复活订阅`, async ({ page, baseURL }) => {
    const api = await start(page, baseURL!)
    await page.clock.runFor(1000)
    await api.emit({ id: 1, action: 'raw', raw: '{invalid' })
    const held = api.hold()
    try {
      await refresh(page).click()
      await held.received.promise
      expect((await sources(page))).toHaveLength(2)
      if (boundary === 'route') {
        await page.locator('.navigation a[href="/sites"]').click()
        await expect(page).toHaveURL(/\/sites$/)
        await expect(page.getByRole('heading', { name: '站点管理', exact: true })).toBeVisible()
      } else {
        await page.getByRole('button', { name: '退出登录', exact: true }).click()
        await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
      }
      expect((await sources(page)).every((source) => source.readyState === 2)).toBe(true)
    } finally { held.release.resolve() }
    await held.finished.promise
    // route.fulfill 已提交响应不等于 fetch/store 已完成；等待应用真正释放刷新状态。
    await expect.poll(async () => (await state(page)).isRefreshing).toBe(false)
    await settle(page)
    const afterRead = await state(page)
    for (const id of [1, 2]) await api.emit({ id, action: 'queued', raw: JSON.stringify(snapshot(2, 99)) })
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await page.clock.runFor(2000)
    expect(await state(page)).toEqual(afterRead)
    expect(afterRead).toMatchObject({ realtimeScopes: [], realtimeState: 'offline', isRefreshing: false })
    if (boundary === 'logout') expect(afterRead).toMatchObject({ summary: null, inventory: null, errorCodes: [] })
    else expect(afterRead).toMatchObject({ summary: { collectedAt: at(1), cpu: { usagePercent: 31 } }, errorCodes: [failure] })
    expect((await sources(page))).toHaveLength(2)
    expect((await sources(page)).every((source) => source.readyState === 2)).toBe(true)
    expect(api.count(summaryPath)).toBe(1)
    expect(api.count(inventoryPath)).toBe(1)
    await expect(refresh(page)).toHaveCount(0)
    expectSafe(api, boundary === 'logout')
  })
}
