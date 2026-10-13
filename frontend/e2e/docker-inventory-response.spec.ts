import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { overviewDocker, summary } from './mockApi'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })

const epoch = Date.parse('2026-10-13T12:00:00.000Z')
const at = (seconds = 0) => new Date(epoch + seconds * 1000).toISOString()
const inventoryPath = '/api/v1/docker/inventory'
const invalidCode = 'DOCKER_INVENTORY_RESPONSE_INVALID'
const firstError = (page: Page) => page.locator('.docker-page .resource-state--error')
const recovery = (page: Page) => page.locator('.inventory-recovery')
const retry = (page: Page) => page.getByRole('button', { name: '重新加载', exact: true })
const project = (page: Page, name: string) => page.getByRole('button', { name: `查看 Docker 项目 ${name}`, exact: true })
const metric = (page: Page, name: string) => page.locator('.metric-card').filter({ hasText: name }).locator('strong')

function inventory(collectedAt = at(), name = '清单 A') {
  return {
    collectedAt,
    engine: { ...overviewDocker.engine, containers: 1, containersRunning: 1, containersStopped: 0, images: 2 },
    containers: [{
      ...overviewDocker.containers[0]!, id: 'm29-container', name: 'm29-container',
      projectId: 'm29-project', projectName: name, createdAt: at(-3600), startedAt: at(-3600),
    }],
    projects: [{
      ...overviewDocker.projects[0]!, id: 'm29-project', name,
      workingDirectory: '/fixture/m29', containerCount: 1, runningCount: 1,
    }],
  }
}

function emptyInventory(collectedAt = at()) {
  return {
    collectedAt,
    engine: { ...inventory().engine, containers: 0, containersRunning: 0, containersStopped: 0, images: 0 },
    containers: null, projects: null,
  }
}

function snapshot(seconds: number, docker: unknown, usagePercent = 24.5, errors?: string[]) {
  return {
    collectedAt: at(seconds),
    summary: { ...summary, collectedAt: at(seconds), cpu: { ...summary.cpu, usagePercent } },
    docker, ...(errors ? { errors } : {}),
  }
}

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> }
type SourceState = { id: number; url: string; readyState: number; closed: number; snapshots: number }
type StoreProjection = {
  capabilities: { hostname?: string } | null
  inventory: ReturnType<typeof inventory> | null
  inventoryRefreshing: boolean
  inventoryErrorCode: string | null
  inventoryStale: boolean
  summary: typeof summary | null
  connectionState: string
  realtimeState: string
  errorCodes: string[]
}

// 单独的静默 EventSource 不注入初始合法清单，以免掩盖首次 GET 失败。
// 只允许下列精确 GET 路径；未知 API 和所有写请求都在内存中失败关闭。
// 页面真实点击仍使用真实控件，无 Docker、NAS、PTY 或宿主服务访问。
async function installFixture(page: Page, initial: unknown) {
  // 同时控制 Date、performance.now 和计时器，真实截图/axe 耗时不能令快照意外老化。
  await page.clock.install({ time: new Date(at(-1)) })
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000))
  await page.clock.setSystemTime(new Date(at()))
  await page.addInitScript(() => {
    const sources: FixtureEventSource[] = []
    class FixtureEventSource extends EventTarget {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSED = 2
      readonly url: string
      readonly withCredentials = false
      readonly id: number
      readyState = FixtureEventSource.CONNECTING
      closed = 0
      snapshots = 0
      onopen: ((event: Event) => void) | null = null
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      onerror: ((event: Event) => void) | null = null
      constructor(url: string | URL) {
        super()
        this.url = String(url)
        this.id = sources.length + 1
        sources.push(this)
        queueMicrotask(() => {
          if (this.readyState === FixtureEventSource.CLOSED) return
          this.readyState = FixtureEventSource.OPEN
          const event = new Event('open')
          this.onopen?.(event)
          this.dispatchEvent(event)
        })
      }
      close() {
        this.closed += 1
        this.readyState = FixtureEventSource.CLOSED
      }
    }
    const fixtureWindow = window as typeof window & { __m29Sources: () => SourceState[] }
    fixtureWindow.__m29Sources = () => sources.map(({ id, url, readyState, closed, snapshots }) => ({ id, url, readyState, closed, snapshots }))
    window.addEventListener('ncp:m29:snapshot', (event) => {
      for (const source of sources) {
        if (source.readyState !== FixtureEventSource.OPEN) continue
        source.snapshots += 1
        source.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify((event as CustomEvent<unknown>).detail) }))
      }
    })
    window.EventSource = FixtureEventSource as unknown as typeof EventSource
  })

  const requests: string[] = []
  const denied: string[] = []
  const pageErrors: string[] = []
  const emissions: unknown[] = []
  let payload = initial
  let gate: Gate | undefined
  page.on('pageerror', (error) => pageErrors.push(error.message))
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', {
      refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10,
      sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system',
      navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'],
    }],
    ['/api/v1/system/capabilities', {
      hostname: 'm29-capabilities-ready', architecture: 'aarch64', docker: true, compose: true,
      systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [],
    }],
    ['/api/v1/sites', {
      collectedAt: at(), sites: [],
      discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] },
    }],
    ['/api/v1/sites/ignored', []],
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((listKey): [string, unknown] => [
      `/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' },
    ]),
  ])
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const target = `${request.method()} ${path}`
    requests.push(target)
    if (request.method() === 'GET' && path === inventoryPath) {
      const body = structuredClone(payload)
      if (gate) {
        const held = gate
        gate = undefined
        held.received.resolve()
        await held.release.promise
      }
      await route.fulfill({ json: body })
      return
    }
    // 能力页仅用于先加载 capabilities；详情故意不可用，避免启动额外探测。
    if (request.method() === 'GET' && path === '/api/v1/system/details') {
      return route.fulfill({ status: 503, json: { code: 'SYSTEM_DETAILS_UNAVAILABLE', message: 'fixture details unavailable' } })
    }
    if (request.method() === 'GET' && path === '/api/v1/system/dns/capability') {
      return route.fulfill({ status: 503, json: { code: 'DNS_CAPABILITY_UNAVAILABLE', message: 'fixture DNS capability unavailable' } })
    }
    if (request.method() === 'GET' && reads.has(path)) return route.fulfill({ json: reads.get(path) })
    denied.push(target)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期的 API 请求已拒绝' } })
  })
  return {
    requests, denied, pageErrors, emissions,
    respondWith(value: unknown) { payload = value },
    count(path: string) { return requests.filter((request) => request === `GET ${path}`).length },
    hold() {
      expect(gate, '不得覆盖尚未消费的读取闸门').toBeUndefined()
      gate = { received: deferred(), release: deferred() }
      return gate
    },
    async emit(value: unknown) {
      emissions.push(structuredClone(value))
      await page.evaluate((detail) => window.dispatchEvent(new CustomEvent('ncp:m29:snapshot', { detail })), value)
      await settle(page)
    },
  }
}
type Fixture = Awaited<ReturnType<typeof installFixture>>

async function settle(page: Page) {
  // router.afterEach 和 Vue 过渡的真实 rAF 回调仍要执行，不能带着待交接焦点截图。
  await page.clock.runFor(34)
}

async function withClockFrames<T>(page: Page, operation: () => Promise<T>): Promise<T> {
  let finished = false
  let rejected = false
  let result!: T
  let failure: unknown
  const pending = operation().then((value) => { result = value; finished = true }, (error: unknown) => { failure = error; rejected = true; finished = true })
  // axe 的异步规则包含零延时回调。逐帧驱动暂停时钟，不恢复真实时间，也不跳过规则。
  // 显式的 2 秒预算远小于 15 秒新鲜度阈值；超出预算失败，绝不重置资源或放宽断言。
  for (let elapsed = 0; !finished && elapsed < 2000; elapsed += 16) await page.clock.runFor(16)
  expect(finished, 'axe 未在受控 2 秒时钟预算内结束').toBe(true)
  await pending
  if (rejected) throw failure
  return result
}

// 仅读取 Pinia 投影供证据核对，不调用 store 方法、不改写页面状态。
async function state(page: Page) {
  return page.evaluate(() => {
    const app = document.getElementById('app') as HTMLElement & {
      __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, StoreProjection> } } } }
    }
    const store = app.__vue_app__.config.globalProperties.$pinia._s.get('system')!
    return JSON.parse(JSON.stringify({
      capabilities: store.capabilities, inventory: store.inventory,
      inventoryRefreshing: store.inventoryRefreshing, inventoryErrorCode: store.inventoryErrorCode,
      inventoryStale: store.inventoryStale, summary: store.summary,
      connectionState: store.connectionState, realtimeState: store.realtimeState, errorCodes: store.errorCodes,
    })) as StoreProjection
  })
}

async function sources(page: Page) {
  return page.evaluate(() => (window as typeof window & { __m29Sources: () => SourceState[] }).__m29Sources())
}

async function navigate(page: Page, path: '/docker' | '/system') {
  await settle(page)
  const menu = page.getByRole('button', { name: '打开主菜单', exact: true })
  if (await menu.isVisible()) {
    await menu.click()
    await settle(page)
  }
  await page.locator(`.navigation a[href="${path}"]`).click()
  await expect(page).toHaveURL(new RegExp(`${path}$`))
  await settle(page)
}

async function expectSafe(api: Fixture) {
  expect(api.pageErrors).toEqual([])
  expect(api.denied).toEqual([])
  expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([])
}

async function sourceProvenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--',
    'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json',
    'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts',
  ]).split('\n').filter(Boolean)
  const sourceHashes = Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [
    file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex'),
  ])))
  return {
    sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes,
    trackedDiffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex'),
  }
}

async function captureState(page: Page, selector: string) {
  const browser = await page.evaluate((scope) => {
    const region = document.querySelector<HTMLElement>(scope)!
    const elements = [region, ...region.querySelectorAll<HTMLElement>('strong, span, button, p, dt, dd')]
    return {
      viewport: { width: innerWidth, height: innerHeight }, scroll: { x: scrollX, y: scrollY },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      semantic: {
        region: region.textContent?.trim(),
        error: document.querySelector('.resource-state--error')?.textContent?.trim() ?? null,
        recovery: document.querySelector('.inventory-recovery')?.textContent?.trim() ?? null,
        busy: document.querySelector('.docker-page [aria-busy="true"]') !== null,
        visibleProjects: [...document.querySelectorAll<HTMLElement>('.project-name')].filter((node) => node.getClientRects().length > 0).map((node) => node.textContent?.trim()),
        metrics: [...document.querySelectorAll('.metric-card > strong')].map((node) => node.textContent),
        toasts: [...document.querySelectorAll('.el-message')].map((node) => node.textContent),
      },
      controls: elements.map((element, index) => {
        const bounds = element.getBoundingClientRect()
        const style = getComputedStyle(element)
        const lines: DOMRect[] = []
        const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT)
        for (let node = walker.nextNode(); node; node = walker.nextNode()) {
          if (!node.textContent?.trim()) continue
          const range = document.createRange()
          range.selectNodeContents(node)
          lines.push(...[...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0))
        }
        const clips: Array<{ x: boolean; y: boolean; bounds: DOMRect }> = []
        let visible = bounds.width > 0 && bounds.height > 0
        for (let ancestor: Element | null = element; ancestor; ancestor = ancestor.parentElement) {
          const computed = getComputedStyle(ancestor)
          visible &&= computed.display !== 'none' && computed.visibility !== 'hidden' && Number(computed.opacity) > 0
          if (ancestor === document.documentElement || ancestor === document.body) continue
          const x = /hidden|clip|auto|scroll/.test(computed.overflowX)
          const y = /hidden|clip|auto|scroll/.test(computed.overflowY)
          if (x || y) clips.push({ x, y, bounds: ancestor.getBoundingClientRect() })
        }
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return {
          index, tag: element.tagName, className: element.className, text: element.textContent?.trim(),
          bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()),
          clips: clips.map((clip) => ({ ...clip, bounds: clip.bounds.toJSON() })),
          visible, inViewport: bounds.left >= -1 && bounds.top >= -1 && bounds.right <= innerWidth + 1 && bounds.bottom <= innerHeight + 1,
          scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
          overflowX: style.overflowX, whiteSpace: style.whiteSpace, textOverflow: style.textOverflow,
          disabled: element instanceof HTMLButtonElement ? element.disabled : null,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) =>
            line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1 &&
            clips.every((clip) => (!clip.x || (line.left >= clip.bounds.left - 1 && line.right <= clip.bounds.right + 1)) &&
              (!clip.y || (line.top >= clip.bounds.top - 1 && line.bottom <= clip.bounds.bottom + 1)))),
        }
      }),
    }
  }, selector)
  return { ...browser, store: await state(page), eventSources: await sources(page) }
}

// 只在隔离浏览器真正执行时生成证据；PNG、完整状态与未筛选 axe 都直接放 ui-captures。
async function capture(page: Page, info: TestInfo, api: Fixture, stateName: string, selector: string) {
  await page.evaluate(() => document.fonts.ready)
  await page.locator(selector).scrollIntoViewIfNeeded()
  await settle(page)
  const provenance = await sourceProvenance()
  const beforeCapture = await captureState(page, selector)
  const requestsBefore = [...api.requests]
  const name = `docker-inventory-${stateName}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const afterCapture = await captureState(page, selector)
  const axe = await withClockFrames(page, () => new AxeBuilder({ page }).analyze())
  const afterAxe = await captureState(page, selector)
  const metadata = {
    ...provenance, fixtureOnly: true, fixtureSource: 'frontend/e2e/docker-inventory-response.spec.ts + frontend/e2e/mockApi.ts',
    stateName, selector, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
    screenshot: `${name}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    requestsBefore, requestsAfter: [...api.requests],
    requestCounts: Object.fromEntries([...new Set(api.requests)].map((target) => [target, api.requests.filter((request) => request === target).length])),
    denied: api.denied, pageErrors: api.pageErrors, emissions: api.emissions,
    beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  for (const [label, path, contentType] of [[name, png, 'image/png'], [`${name}-state`, statePath, 'application/json'], [`${name}-axe-state`, axePath, 'application/json']]) {
    await info.attach(label!, { path: path!, contentType: contentType! })
  }
  // 失败也保留后续真实截图；soft 仍使该测试失败，不过滤 axe、不弱化规则。
  expect.soft(afterCapture).toEqual(beforeCapture)
  expect.soft(afterAxe.semantic).toEqual(beforeCapture.semantic)
  expect.soft(afterAxe.scroll).toEqual(beforeCapture.scroll)
  expect.soft(afterAxe.store).toEqual(beforeCapture.store)
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  for (const control of beforeCapture.controls) {
    expect.soft(control.visible && control.inViewport, JSON.stringify(control)).toBe(true)
    expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (control.tag === 'BUTTON') expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
  }
  expect.soft(axe.violations).toEqual([])
}

for (const width of [390, 1440]) {
  test(`${width}px 已加载 capabilities 后首次坏清单结束等待，只读重试区分合法空结果`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const api = await installFixture(page, { ...inventory(), containers: [null], diagnostic: 'private-m29-body' })
    await page.goto('/system', { waitUntil: 'domcontentloaded' })
    await expect.poll(async () => (await state(page)).capabilities?.hostname).toBe('m29-capabilities-ready')
    expect(api.count(inventoryPath)).toBe(0)
    const held = api.hold()
    try {
      await navigate(page, '/docker')
      await held.received.promise
      await expect(page.getByText('正在读取 Docker 状态', { exact: true })).toBeVisible()
      expect((await state(page)).inventoryRefreshing).toBe(true)
      await expect(firstError(page)).toHaveCount(0)
    } finally { held.release.resolve() }
    await expect(firstError(page)).toContainText('Docker 状态读取失败')
    await expect(firstError(page)).toContainText(invalidCode)
    await expect(page.getByText('正在读取 Docker 状态', { exact: true })).toHaveCount(0)
    await expect(page.locator('.docker-page .table-empty')).toHaveCount(0)
    await expect(page.locator('body')).not.toContainText('private-m29-body')
    expect((await state(page))).toMatchObject({ inventory: null, inventoryRefreshing: false, inventoryErrorCode: invalidCode, connectionState: 'degraded' })
    expect(api.count(inventoryPath)).toBe(1)
    expect(api.count('/api/v1/system/capabilities')).toBe(1)
    await capture(page, info, api, 'first-error', '.docker-page .resource-state--error')

    api.respondWith(emptyInventory())
    const beforeRetry = api.requests.length
    await retry(page).click()
    await expect(firstError(page)).toHaveCount(0)
    await expect(page.locator('.docker-page .table-empty:visible')).toHaveText('没有匹配的 Docker 项目。')
    await expect(page.locator('.workspace-stat dd')).toHaveText(['0', '0', '0', '0'])
    expect(await state(page)).toMatchObject({ inventory: { containers: [], projects: [] }, inventoryRefreshing: false, inventoryErrorCode: null, inventoryStale: false })
    expect(api.requests.slice(beforeRetry)).toEqual([`GET ${inventoryPath}`])
    expect(api.count(inventoryPath)).toBe(2)
    expect(await sources(page)).toEqual([])
    await capture(page, info, api, 'valid-empty', width === 390 ? '.docker-mobile-list .table-empty' : '.docker-table .table-empty')
    await expectSafe(api)
  })

  test(`${width}px 坏 GET 保留上次项目与时间，重新加载仅发清单 GET 并恢复`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const api = await installFixture(page, inventory())
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await expect(project(page, '清单 A')).toBeVisible()
    const previous = (await state(page)).inventory
    await navigate(page, '/system')
    await expect.poll(async () => (await state(page)).capabilities?.hostname).toBe('m29-capabilities-ready')
    const bad = inventory(at(3), '不可显示的清单')
    api.respondWith({ ...bad, containers: [{ ...bad.containers[0], ports: [null] }] })
    await navigate(page, '/docker')
    await expect(recovery(page)).toContainText('Docker 状态更新失败，显示上次数据')
    await expect(recovery(page)).toContainText(at())
    await expect(recovery(page)).toContainText(invalidCode)
    await expect(project(page, '清单 A')).toBeVisible()
    await expect(project(page, '不可显示的清单')).toHaveCount(0)
    expect((await state(page)).inventory).toEqual(previous)
    await capture(page, info, api, 'cached-error', '.inventory-recovery')

    await page.clock.setSystemTime(new Date(at(4)))
    api.respondWith(inventory(at(4), '恢复清单 B'))
    const held = api.hold()
    const beforeRetry = api.requests.length
    try {
      await retry(page).click()
      await held.received.promise
      await expect(retry(page)).toBeDisabled()
      await expect(retry(page)).toHaveAttribute('aria-busy', 'true')
      await expect(project(page, '清单 A')).toBeVisible()
      expect((await state(page)).inventoryRefreshing).toBe(true)
    } finally { held.release.resolve() }
    await expect(recovery(page)).toHaveCount(0)
    await expect(project(page, '恢复清单 B')).toBeVisible()
    expect(await state(page)).toMatchObject({ inventory: { collectedAt: at(4) }, inventoryRefreshing: false, inventoryErrorCode: null, inventoryStale: false })
    expect(api.requests.slice(beforeRetry)).toEqual([`GET ${inventoryPath}`])
    expect(api.count(inventoryPath)).toBe(3)
    await capture(page, info, api, 'get-recovered', '.docker-page .workspace-header')
    await expectSafe(api)
  })

  test(`${width}px 同一 SSE 隔离坏 Docker，summary 继续推进且合法新快照恢复`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const api = await installFixture(page, inventory())
    await page.goto('/', { waitUntil: 'domcontentloaded' })
    await expect.poll(async () => (await sources(page)).filter((source) => source.readyState === 1).length).toBe(1)
    const source = (await sources(page))[0]!
    expect(new URL(source.url, page.url()).pathname).toBe('/api/v1/system/events')
    await api.emit(snapshot(0, inventory()))
    await expect(metric(page, 'CPU 使用率')).toHaveText('24.5%')
    await expect(metric(page, 'Docker 容器')).toHaveText('1/1')
    const previous = (await state(page)).inventory

    // 坏清单带有更晚时间，不能先推进 Docker 基线；原始体也不能上屏。
    await page.clock.setSystemTime(new Date(at(1)))
    await api.emit(snapshot(1, { ...inventory(at(3), '不可显示的清单'), projects: [null], diagnostic: 'private-m29-stream-body' }, 42.7, ['MONITORING_WRITE_FAILED']))
    await expect(metric(page, 'CPU 使用率')).toHaveText('42.7%')
    await expect(metric(page, 'Docker 容器')).toHaveText('1/1')
    expect((await state(page)).inventory).toEqual(previous)
    expect(await state(page)).toMatchObject({ inventoryErrorCode: invalidCode, summary: { collectedAt: at(1) }, realtimeState: 'streaming' })
    expect((await state(page)).errorCodes).toEqual(expect.arrayContaining([invalidCode, 'MONITORING_WRITE_FAILED']))
    await expect(page.locator('body')).not.toContainText('private-m29-stream-body')
    expect(await sources(page)).toEqual([{ ...source, snapshots: 2 }])
    await capture(page, info, api, 'sse-retained-data', '.snapshot-health-note')

    // 结构合法但时间无效同样不落库、不推进基线；重复和旧清单也不能清错。
    await page.clock.setSystemTime(new Date(at(2)))
    await api.emit(snapshot(2, inventory('not-a-date', '无效时间清单'), 43.1))
    expect(await state(page)).toMatchObject({ inventoryErrorCode: invalidCode, summary: { collectedAt: at(2) }, realtimeState: 'streaming' })
    expect((await state(page)).inventory).toEqual(previous)
    for (const collectedAt of [at(), at(-1)]) {
      await api.emit(snapshot(2, inventory(collectedAt, '旧清单不能覆盖'), 43.1))
      expect((await state(page)).inventory).toEqual(previous)
      expect((await state(page)).inventoryErrorCode).toBe(invalidCode)
    }
    // 时间早于畸形清单的 at(3)，但晚于真正最后接纳的 at(0)，必须恢复。
    await api.emit(snapshot(2, inventory(at(2), '恢复清单 B'), 58.1))
    // summary 的同时间更新仍受既有新鲜度约束，不重写刚接纳的 43.1。
    await expect(metric(page, 'CPU 使用率')).toHaveText('43.1%')
    expect(await state(page)).toMatchObject({ inventory: { collectedAt: at(2), projects: [{ name: '恢复清单 B' }] }, inventoryErrorCode: null, realtimeState: 'streaming', connectionState: 'connected' })
    await expect(page.locator('.snapshot-health-note')).toHaveCount(0)
    expect(await sources(page)).toEqual([{ ...source, snapshots: 6 }])
    await capture(page, info, api, 'sse-recovered', '.overview-page .workspace-header')

    await page.clock.setSystemTime(new Date(at(3)))
    await api.emit(snapshot(3, inventory(at(1), '迟到旧清单'), 61.2))
    await expect(metric(page, 'CPU 使用率')).toHaveText('61.2%')
    expect(await state(page)).toMatchObject({ inventory: { collectedAt: at(2), projects: [{ name: '恢复清单 B' }] }, inventoryErrorCode: null, realtimeState: 'streaming' })
    expect(await sources(page)).toEqual([{ ...source, snapshots: 7 }])
    expect(api.count(inventoryPath)).toBe(0)
    await expectSafe(api)
  })

  test(`${width}px GET 清单静默 16 秒显示过期提示，新快照可清除且无后台请求`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const api = await installFixture(page, inventory())
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await expect(project(page, '清单 A')).toBeVisible()
    await expect(recovery(page)).toHaveCount(0)
    const readsBeforeAging = [...api.requests]
    await page.clock.runFor(16_000)
    await expect(recovery(page)).toContainText('Docker 状态尚未更新，显示上次数据')
    await expect(recovery(page)).toContainText(at())
    await expect(recovery(page)).not.toContainText(invalidCode)
    expect(await state(page)).toMatchObject({ inventoryErrorCode: null, inventoryStale: true })
    expect(api.requests).toEqual(readsBeforeAging)
    expect(await sources(page)).toEqual([])
    await capture(page, info, api, 'stale-without-error', '.inventory-recovery')
    const recoveredAt = await page.evaluate(() => new Date().toISOString())
    api.respondWith(inventory(recoveredAt))
    const before = api.requests.length
    await retry(page).click()
    await expect(recovery(page)).toHaveCount(0)
    expect(await state(page)).toMatchObject({ inventory: { collectedAt: recoveredAt }, inventoryErrorCode: null, inventoryStale: false })
    expect(api.requests.slice(before)).toEqual([`GET ${inventoryPath}`])
    await expectSafe(api)
  })
}

test('缺省 Docker 与显式 null Docker 分别保留独立错误，summary 与同一事件流仍可恢复', async ({ page }) => {
  const api = await installFixture(page, inventory())
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await expect.poll(async () => (await sources(page)).filter((source) => source.readyState === 1).length).toBe(1)
  const source = (await sources(page))[0]!
  await api.emit(snapshot(0, inventory()))
  await page.clock.setSystemTime(new Date(at(1)))
  await api.emit(snapshot(1, undefined, 31.2))
  expect(await state(page)).toMatchObject({ inventoryErrorCode: 'DOCKER_INVENTORY_UNAVAILABLE', summary: { collectedAt: at(1) }, realtimeState: 'streaming' })
  await page.clock.setSystemTime(new Date(at(2)))
  await api.emit(snapshot(2, null, 32.2))
  expect(await state(page)).toMatchObject({ inventoryErrorCode: invalidCode, inventory: { collectedAt: at() }, summary: { collectedAt: at(2) }, realtimeState: 'streaming' })
  await page.clock.setSystemTime(new Date(at(3)))
  await api.emit(snapshot(3, emptyInventory(at(3)), 33.2))
  await expect(metric(page, 'CPU 使用率')).toHaveText('33.2%')
  await expect(metric(page, 'Docker 容器')).toHaveText('0/0')
  expect(await state(page)).toMatchObject({ inventoryErrorCode: null, inventory: { collectedAt: at(3), containers: [], projects: [] }, realtimeState: 'streaming', connectionState: 'connected' })
  expect(await sources(page)).toEqual([{ ...source, snapshots: 4 }])
  await expectSafe(api)
})
