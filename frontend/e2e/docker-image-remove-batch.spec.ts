import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { overviewDocker, summary } from './mockApi'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const stamp = '2026-10-15T00:00:00.000Z'
const imageIds = [`sha256:${'a1'.repeat(32)}`, `sha256:${'b2'.repeat(32)}`]
const imageNames = ['fixture/batch-first:latest', 'fixture/batch-second:latest']
const removePath = '/api/v1/docker/images/remove-batch'
const images = imageIds.map((id, index) => ({ id, repoTags: [imageNames[index]!], repoDigests: [], sizeBytes: 1024, createdAt: stamp, containers: 0 }))
const inventory = { ...overviewDocker, collectedAt: stamp, containers: [], projects: [], engine: { ...overviewDocker.engine, containers: 0, containersRunning: 0, containersStopped: 0, images: 2 } }
type Mode = 'mixed' | 'foreign' | 'duplicate'
function receipt(mode: Mode) {
  return { items: [{ imageId: imageIds[0], removed: true }, { imageId: mode === 'foreign' ? `sha256:${'c3'.repeat(32)}` : mode === 'duplicate' ? imageIds[0] : imageIds[1], removed: false, errorCode: 'DOCKER_IMAGE_IN_USE' }], removedCount: 1, failedCount: 1, completed: false }
}
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 只允许指定来源的静态 GET 与精确 API 白名单。删除许可仅供一次真实确认点击消费；
// 未知请求、重发、WebSocket 均失败关闭，不访问 Docker、NAS 或宿主机。
async function fixture(page: Page, origin: string, mode: Mode, bounded?: ReturnType<typeof boundedTransport>) {
  let snapshotTimestamp = Date.parse(stamp)
  const currentStamp = async () => {
    snapshotTimestamp = Math.max(snapshotTimestamp + 1, await page.evaluate(() => Date.now()))
    return new Date(snapshotTimestamp).toISOString()
  }
  if (bounded) await page.clock.install({ time: new Date(stamp) })
  else await page.clock.setFixedTime(new Date(stamp))
  await page.addInitScript(() => {
    // 本场景通过精确 GET 核验；任何意外 SSE 订阅直接暴露为页面错误。
    window.EventSource = class extends EventTarget {
      constructor(url: string | URL) { super(); throw new Error(`Unexpected fixture EventSource: ${String(url)}`) }
    } as unknown as typeof EventSource
  })
  if (bounded) await page.addInitScript(() => {
    const events: Array<{ browserNow: number; performanceNow: number; text: string | null; bounds: object | null }> = []
    ;(window as typeof window & { __boundedInventoryEvents: typeof events }).__boundedInventoryEvents = events
    let previous: string | null | undefined
    new MutationObserver(() => {
      const banner = document.querySelector('.inventory-recovery'), text = banner?.textContent ?? null
      if (text === previous) return
      previous = text
      events.push({ browserNow: Date.now(), performanceNow: performance.now(), text, bounds: banner?.getBoundingClientRect().toJSON() ?? null })
    }).observe(document, { childList: true, subtree: true, characterData: true })
  })
  const inventoryReads: Array<{ collectedAt: string; browserNow: number }> = []
  const requests: string[] = [], denied: string[] = [], errors: string[] = []
  const writes: unknown[] = [], received = deferred(), release = deferred()
  let authorized = false, responded = false
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs?type=docker-image-pull', { jobs: [] }],
    ['/api/v1/docker/inventory', bounded ? { ...inventory, engine: { ...inventory.engine, images: bounded.ids.length } } : inventory],
    ...['docker.projects', 'docker.containers', 'docker.images.local', 'docker.image-downloads'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: bounded && listKey === 'docker.images.local' ? 200 : 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  page.on('pageerror', (error) => errors.push(error.message))
  await page.routeWebSocket('**', (socket) => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async (route) => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin || request.method() !== 'GET' || url.search || !(url.pathname === '/docker' || url.pathname === '/favicon.svg' || /^\/assets\/[A-Za-z0-9_.-]+$/.test(url.pathname))) {
      denied.push(`${request.method()} ${url.href}`); return route.abort('blockedbyclient')
    }
    return route.fallback()
  })
  await page.route('**/api/**', async (route) => {
    const request = route.request(), url = new URL(request.url()), target = `${request.method()} ${url.pathname}${url.search}`
    requests.push(target)
    if (url.origin !== origin) { denied.push(target); return route.abort('blockedbyclient') }
    const readKey = url.pathname + url.search
    if (request.method() === 'GET' && readKey === '/api/v1/jobs') return route.fulfill({ json: { jobs: [] } })
    if (bounded && request.method() === 'GET' && readKey === '/api/v1/docker/images') return route.fulfill({ json: { collectedAt: await currentStamp(), images: bounded.images } })
    if (bounded && request.method() === 'POST' && readKey === removePath) {
      const body: unknown = request.postDataJSON()
      writes.push(body)
      const batch = bounded.consume(body)
      if (!batch) { denied.push(target); return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED' } }) }
      try {
        await batch.release.promise
        bounded.images = bounded.images.filter((image) => !batch.ids.includes(image.id))
        return await route.fulfill({ json: { items: batch.ids.map((imageId) => ({ imageId, removed: true })), removedCount: batch.ids.length, failedCount: 0, completed: true } })
      } finally { bounded.finish() }
    }
    if (request.method() === 'GET' && readKey === '/api/v1/docker/images') return route.fulfill({ json: { collectedAt: stamp, images: responded && mode === 'mixed' ? [{ ...images[1], containers: 1 }] : images } })
    if (bounded && request.method() === 'GET' && readKey === '/api/v1/docker/inventory') {
      const collectedAt = await currentStamp()
      inventoryReads.push({ collectedAt, browserNow: await page.evaluate(() => Date.now()) })
      return route.fulfill({ json: { ...inventory, collectedAt, engine: { ...inventory.engine, images: bounded.images.length } } })
    }
    if (request.method() === 'GET' && reads.has(readKey)) return route.fulfill({ json: reads.get(readKey) })
    if (request.method() === 'POST' && url.pathname === removePath && !url.search && authorized) {
      authorized = false
      const body: unknown = request.postDataJSON()
      writes.push(body)
      received.resolve()
      expect(body).toEqual({ imageIds })
      await release.promise
      responded = true
      return route.fulfill({ status: 200, json: receipt(mode) })
    }
    denied.push(target)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return { requests, denied, errors, writes, inventoryReads, received, release, authorize() { expect(authorized).toBe(false); expect(writes).toHaveLength(0); authorized = true } }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')]))) }
}

// 测量警告正文、完整不可变 ID 和操作按钮的真实文本布局。
async function geometry(region: Locator) {
  return region.evaluate((element) => {
    const bounds = element.getBoundingClientRect()
    const fields = [...element.querySelectorAll('span, h3, p, button, strong, li')].filter((field) => field.children.length === 0 && field.textContent?.trim()).map((field) => {
      const box = field.getBoundingClientRect(), range = document.createRange(); range.selectNodeContents(field)
      const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
      const clips: Array<{ x: boolean; y: boolean; box: DOMRect }> = []
      for (let parent: Element | null = field; parent; parent = parent.parentElement) {
        const style = getComputedStyle(parent)
        if (/hidden|clip|auto|scroll/.test(style.overflowX + style.overflowY)) clips.push({ x: /hidden|clip|auto|scroll/.test(style.overflowX), y: /hidden|clip|auto|scroll/.test(style.overflowY), box: parent.getBoundingClientRect() })
      }
      const fullTextVisible = lines.length > 0 && lines.every((line) => line.left >= -1 && line.right <= innerWidth + 1 && line.top >= -1 && line.bottom <= innerHeight + 1 && clips.every((clip) => (!clip.x || line.left >= clip.box.left - 1 && line.right <= clip.box.right + 1) && (!clip.y || line.top >= clip.box.top - 1 && line.bottom <= clip.box.bottom + 1)))
      return { text: field.textContent, bounds: box.toJSON(), lines: lines.map((line) => line.toJSON()), fullTextVisible }
    })
    const buttons = [...element.querySelectorAll('button')].map((button) => {
      const rect = button.getBoundingClientRect(), top = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2)
      return { text: button.textContent, bounds: rect.toJSON(), hitTest: !!top && (top === button || button.contains(top)) }
    })
    return { text: element.textContent, bounds: bounds.toJSON(), fields, buttons, viewport: { width: innerWidth, height: innerHeight }, root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth } }
  })
}
async function inventoryCaptureState(page: Page, api: Fixture) {
  return { ...await page.evaluate(() => {
    const banner = document.querySelector('.inventory-recovery')
    return {
      browserNow: Date.now(), performanceNow: performance.now(), scroll: { x: scrollX, y: scrollY },
      banner: banner ? { text: banner.textContent, bounds: banner.getBoundingClientRect().toJSON() } : null,
      mutations: (window as typeof window & { __boundedInventoryEvents?: unknown[] }).__boundedInventoryEvents ?? [],
    }
  }), inventoryReads: [...api.inventoryReads] }
}
async function capture(page: Page, info: TestInfo, api: Fixture, region: Locator, phase: string, expectedTexts: string[], capturedIds = imageIds) {
  const bounded = capturedIds.length > 50
  const freshnessBeforeRefresh = bounded ? await inventoryCaptureState(page, api) : undefined
  if (bounded) {
    // 通过真实只读刷新建立新清单；保留自然时钟、恢复提示和所有已发送未知保护。
    const writesBefore = structuredClone(api.writes), readsBefore = api.inventoryReads.length
    const unknown = page.locator('.removal-admission--unknown')
    const unknownBefore = await unknown.allTextContents()
    await expect(unknown).toHaveCount(50)
    const response = page.waitForResponse((item) => new URL(item.url()).pathname === '/api/v1/docker/inventory' && item.request().method() === 'GET')
    const refresh = page.getByRole('button', { name: '立即刷新数据', exact: true })
    await refresh.click()
    expect((await response).ok()).toBe(true)
    await expect.poll(() => api.inventoryReads.length).toBeGreaterThan(readsBefore)
    await expect(refresh).toBeEnabled()
    await expect(page.locator('.inventory-recovery')).toHaveCount(0)
    await expect(unknown).toHaveCount(50)
    expect(await unknown.allTextContents()).toEqual(unknownBefore)
    expect(api.writes).toEqual(writesBefore)
  }
  await expect(page.locator('.el-message')).toHaveCount(0)
  await region.scrollIntoViewIfNeeded()
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(350)
  const before = await geometry(region), requestsBefore = [...api.requests]
  const freshnessBeforeCapture = bounded ? await inventoryCaptureState(page, api) : undefined
  const prefix = `docker-image-remove-batch-${page.viewportSize()!.width}-${phase}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const after = await geometry(region)
  const freshnessAfterCapture = bounded ? await inventoryCaptureState(page, api) : undefined
  const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(region)
  const freshnessAfterAxe = bounded ? await inventoryCaptureState(page, api) : undefined
  const statePath = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), phase, freshnessBeforeRefresh, freshnessBeforeCapture, freshnessAfterCapture, freshnessAfterAxe, imageIds: capturedIds, expectedRequest: capturedIds.length <= 50 ? { imageIds: capturedIds } : undefined, expectedBatches: Array.from({ length: Math.ceil(capturedIds.length / 50) }, (_, index) => capturedIds.slice(index * 50, index * 50 + 50)), screenshot: `${prefix}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), beforeCapture: before, afterCapture: after, afterAxe, axe, requestsBefore, requestsAfterAxe: [...api.requests], writes: [...api.writes], denied: [...api.denied], errors: [...api.errors] }, null, 2))
  await info.attach(prefix, { path: png, contentType: 'image/png' })
  await info.attach(`${prefix}-axe-state`, { path: statePath, contentType: 'application/json' })
  for (const sample of [before, after, afterAxe]) {
    for (const text of expectedTexts) expect(sample.text).toContain(text)
    expect(sample.fields.length).toBeGreaterThan(0)
    expect(sample.fields.every((field) => field.fullTextVisible), JSON.stringify(sample)).toBe(true)
    expect(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.clientWidth + 1)
    expect(sample.bounds.left).toBeGreaterThanOrEqual(-1); expect(sample.bounds.right).toBeLessThanOrEqual(sample.viewport.width + 1)
    for (const button of sample.buttons) {
      expect(button.bounds.width).toBeGreaterThanOrEqual(44); expect(button.bounds.height).toBeGreaterThanOrEqual(44)
      expect(button.hitTest, JSON.stringify(button)).toBe(true)
    }
  }
  if (bounded) for (const state of [freshnessBeforeCapture, freshnessAfterCapture, freshnessAfterAxe]) {
    expect(state!.banner).toBeNull()
    expect(state!.browserNow - Date.parse(state!.inventoryReads.at(-1)!.collectedAt)).toBeLessThanOrEqual(15_000)
  }
  expect(after).toEqual(before); expect(afterAxe).toEqual(before)
  expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
  expect(api.requests).toEqual(requestsBefore); expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
  return { screenshot: `${prefix}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), captureState: `${prefix}-axe-capture-state.json`, captureStateSha256: createHash('sha256').update(await readFile(statePath)).digest('hex') }
}
for (const width of [390, 1440]) for (const valid of [true, false]) {
  const mode: Mode = valid ? 'mixed' : width === 390 ? 'foreign' : 'duplicate'
  test(`${width}px 批量镜像删除 ${mode}：冻结目标、一次确认与严格回执核验`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const api = await fixture(page, new URL(baseURL!).origin, mode)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    for (const name of imageNames) await page.getByRole('checkbox', { name: `选择镜像 ${name}`, exact: true }).check()
    const bulk = page.getByRole('button', { name: '批量删除', exact: true })
    const actions = imageNames.map((name) => page.getByRole('button', { name: `删除镜像 ${name}`, exact: true }).filter({ visible: true }))
    await expect(bulk).toBeEnabled()
    // 同一事件循环双击必须在打开确认框前取得互斥；取消不得写入。
    await bulk.evaluate((button: HTMLButtonElement) => { button.click(); button.click() })
    const confirmation = page.locator('.el-message-box')
    await expect(confirmation).toHaveCount(1)
    await expect(confirmation).toContainText('2 个未被引用的镜像')
    await expect(bulk).toBeDisabled()
    for (const action of actions) await expect(action).toBeDisabled()
    expect(api.writes).toEqual([])
    await confirmation.getByRole('button', { name: '取消', exact: true }).click()
    await expect(confirmation).toHaveCount(0)
    await expect(bulk).toBeEnabled()
    for (const action of actions) await expect(action).toBeEnabled()
    expect(api.writes).toEqual([])
    await bulk.click()
    await expect(confirmation).toHaveCount(1)
    // 模拟确认期间选择变化；请求仍只包含确认时冻结的两个 ID。
    await page.getByRole('checkbox', { name: `选择镜像 ${imageNames[1]}`, exact: true }).evaluate((checkbox: HTMLInputElement) => {
      checkbox.checked = false; checkbox.dispatchEvent(new Event('change', { bubbles: true }))
    })
    api.authorize()
    const actualReceipt = page.waitForResponse((response) => new URL(response.url()).pathname === removePath && response.request().method() === 'POST')
      .then(async (response) => ({ status: response.status(), contentType: response.headers()['content-type'], body: await response.json() }))
    const captures: Awaited<ReturnType<typeof capture>>[] = []
    const progress = page.getByRole('status', { name: '批量删除进度', exact: true })
    const successNotice = page.locator('.el-message--success').filter({ hasText: /^已删除 \d+ 个本地镜像$/ })
    try {
      await confirmation.getByRole('button', { name: '删除 2 个', exact: true }).click()
      await api.received.promise
      expect(api.writes).toEqual([{ imageIds }])
      for (const action of actions) await expect(action).toBeDisabled()
      // 进度中的‘已删除 0’不是成功通知；仍须证明没有提前宣告任何删除成功。
      await expect(successNotice).toHaveCount(0)
      await expect(page.getByText(/^已删除 \d+ 个本地镜像$/)).toHaveCount(0)
      await expect(progress).toContainText('已删除 0')
      await expect(progress).toContainText('已发送结果未确认 2')
      await expect(progress).toContainText('等待发送 0')
      await expect(progress).toContainText('正在处理 2')
      api.release.resolve()
      const receiptResult = await actualReceipt
      expect(receiptResult).toEqual({ status: 200, contentType: 'application/json', body: receipt(mode) })
      const unknown = page.locator('.image-workspace .removal-admission--unknown')
      if (valid) {
        await expect(page.getByText('已删除 1 个本地镜像', { exact: true })).toBeVisible()
        await expect(page.getByText('已删除 2 个本地镜像', { exact: true })).toHaveCount(0)
        await expect(unknown).toHaveCount(0)
        await expect(actions[0]!).toHaveCount(0); await expect(actions[1]!).toBeDisabled()
        const failures = page.locator('.bulk-remove-error')
        await expect(failures).toContainText(imageNames[1]!)
        await expect(failures).toContainText('镜像正在被容器引用，已跳过删除；不会强制删除。')
        captures.push(await capture(page, info, api, failures, mode, [imageNames[1]!, '部分镜像删除失败']))
      } else {
        await expect(unknown).toHaveCount(2)
        await expect(successNotice).toHaveCount(0)
        await expect(page.getByText(/^已删除 \d+ 个本地镜像$/)).toHaveCount(0)
        await expect(progress).toContainText('已删除 0')
        await expect(progress).toContainText('已发送结果未确认 2')
        await expect(progress).toContainText('未发送 0')
        await expect(page.locator('.bulk-remove-error')).toHaveCount(0)
        for (const [index, id] of imageIds.entries()) {
          const admission = unknown.filter({ hasText: id })
          await expect(admission).toContainText('批量删除结果未确认，请只读核对镜像，勿重复提交。')
          await expect(admission).not.toContainText('任务已接收')
          await expect(admission.getByRole('button', { name: '查看最近任务', exact: true })).toHaveCount(0)
          await expect(actions[index]!).toBeDisabled()
          captures.push(await capture(page, info, api, admission, `${mode}-${index}-unknown`, [id]))
        }
      }
      const imagesBefore = api.requests.filter((request) => request === 'GET /api/v1/docker/images').length
      if (valid) await page.getByRole('button', { name: '刷新本地镜像', exact: true }).click()
      else await unknown.first().getByRole('button', { name: '只读刷新镜像', exact: true }).click()
      await expect.poll(() => api.requests.filter((request) => request === 'GET /api/v1/docker/images').length).toBe(imagesBefore + 1)
      if (valid) { await expect(actions[0]!).toHaveCount(0); await expect(actions[1]!).toBeDisabled() }
      else {
        await expect(unknown).toHaveCount(2)
        for (const action of actions) await expect(action).toBeDisabled()
        // 主动触发批量处理也不得再次消费未知结果对应的删除权限。
        const availableBulk = page.getByRole('button', { name: '批量删除', exact: true })
        if (await availableBulk.count()) await availableBulk.evaluate((button: HTMLButtonElement) => button.click())
        await expect(confirmation).toHaveCount(0)
      }
      // 跨过五秒刷新周期：不创建删除 Job、不走同步回退，也不重放批量请求。
      await page.waitForTimeout(6000)
      expect(api.writes).toEqual([{ imageIds }])
      expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([`POST ${removePath}`])
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      const ledger = info.outputPath('ui-captures', `docker-image-remove-batch-${width}-${mode}-request-ledger-capture-state.json`)
      await writeFile(ledger, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, mode, imageIds, finalUnknownBanners: await unknown.allTextContents(), finalFailureText: await page.locator('.bulk-remove-error').allTextContents(), receipt: receiptResult, expectedRequest: { imageIds }, linkedCaptures: captures, requestLedger: api.requests, writes: api.writes, denied: api.denied, errors: api.errors, replayObservationMilliseconds: 6000 }, null, 2))
      await info.attach('batch-read-only-follow-up-ledger', { path: ledger, contentType: 'application/json' })
    } finally { api.release.resolve() }
  })
}

// 151 个目标只能按一次确认冻结的顺序串行发送；每次写入由对应测试显式许可。
function boundedTransport() {
  const ids = Array.from({ length: 151 }, (_, index) => `sha256:${(index + 1).toString(16).padStart(64, '0')}`)
  const names = ids.map((_, index) => `fixture/bounded-${String(index + 1).padStart(3, '0')}:latest`)
  const batches: Array<{ ids: string[]; release: ReturnType<typeof deferred> }> = []
  let allowed: string[][] = [], active = 0, maximumActive = 0
  return {
    ids, names, batches, captures: [] as Array<{ screenshot: string; screenshotSha256: string; captureState: string; captureStateSha256: string }>,
    images: ids.map((id, index) => ({ id, repoTags: [names[index]!], repoDigests: [], sizeBytes: 1024, createdAt: stamp, containers: 0 })),
    authorize(targets: string[]) {
      expect(active).toBe(0)
      allowed = Array.from({ length: Math.ceil(targets.length / 50) }, (_, index) => targets.slice(index * 50, index * 50 + 50))
    },
    consume(body: unknown) {
      const expected = allowed.shift()
      if (!expected) return null
      expect(body).toEqual({ imageIds: expected })
      expect(active).toBe(0)
      active += 1; maximumActive = Math.max(maximumActive, active)
      const batch = { ids: expected, release: deferred() }
      batches.push(batch)
      return batch
    },
    finish() { active -= 1 },
    get maximumActive() { return maximumActive },
    releaseAll() { for (const batch of batches) batch.release.resolve() },
  }
}
async function openBoundedSelection(page: Page, baseURL: string, width = 1440) {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
  const transport = boundedTransport(), api = await fixture(page, new URL(baseURL).origin, 'mixed', transport)
  await page.goto('/docker', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: '镜像', exact: true }).click()
  await expect(page.getByRole('checkbox', { name: /^选择镜像 fixture\/bounded-/ })).toHaveCount(151)
  await page.getByRole('checkbox', { name: '选择当前页镜像', exact: true }).check()
  await expect(page.locator('.selection-count')).toHaveText('已选 151 个')
  return { api, transport }
}
async function confirmBounded(page: Page, transport: ReturnType<typeof boundedTransport>, ids: string[]) {
  await page.getByRole('button', { name: '批量删除', exact: true }).click()
  const confirmation = page.locator('.el-message-box')
  await expect(confirmation).toContainText(`${ids.length} 个未被引用的镜像`)
  transport.authorize(ids)
  await confirmation.getByRole('button', { name: `删除 ${ids.length} 个`, exact: true }).click()
}
async function boundedLedger(info: TestInfo, api: Fixture, transport: ReturnType<typeof boundedTransport>, page: Page) {
  expect(transport.maximumActive).toBe(1)
  expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
  expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual(api.writes.map(() => `POST ${removePath}`))
  const path = info.outputPath('ui-captures', `bounded-image-batch-${page.viewportSize()!.width}-ledger-capture-state.json`)
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, JSON.stringify({
    ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
    testTitle: info.title, project: info.project.name, retry: info.retry,
    journeyCompleted: info.errors.length === 0, linkedCaptures: transport.captures,
    imageIds: transport.ids, writes: api.writes, maximumActive: transport.maximumActive,
    requests: api.requests, denied: api.denied, errors: api.errors,
    summary: await page.getByRole('status', { name: '批量删除进度', exact: true }).textContent(),
    unknown: await page.locator('.removal-admission--unknown').allTextContents(),
  }, null, 2))
  await info.attach('bounded-image-batch-ledger', { contentType: 'application/json', path })
}

test('151 个镜像按冻结选择串行发送 50/50/50/1，确认后选择变化不扩大或缩减请求', async ({ page, baseURL }, info) => {
  const { api, transport } = await openBoundedSelection(page, baseURL!)
  try {
    await confirmBounded(page, transport, transport.ids)
    await expect.poll(() => transport.batches.length).toBe(1)
    // 原生复选框仍可调整选择，但不能改变已确认的目标快照。
    await page.getByRole('checkbox', { name: `选择镜像 ${transport.names[150]}`, exact: true }).uncheck()
    await expect(page.locator('.selection-count')).toHaveText('已选 150 个')
    for (let index = 0; index < 4; index += 1) {
      await expect.poll(() => transport.batches.length).toBe(index + 1)
      expect(api.writes).toHaveLength(index + 1)
      await expect(page.getByRole('button', { name: '批量删除', exact: true })).toBeDisabled()
      transport.batches[index]!.release.resolve()
    }
    const status = page.getByRole('status', { name: '批量删除进度', exact: true })
    await expect(status).toContainText('已删除 151')
    await expect(status).toContainText('未发送 0')
    expect(transport.batches.map((batch) => batch.ids.length)).toEqual([50, 50, 50, 1])
    expect(transport.batches.flatMap((batch) => batch.ids)).toEqual(transport.ids)
    await expect(page.locator('.removal-admission--unknown')).toHaveCount(0)
    await boundedLedger(info, api, transport, page)
  } finally { transport.releaseAll() }
})

for (const width of [390, 1440]) test(`${width}px 中断首批保留 50 未确认和 101 未发送，独立重新确认仅发送未发送目标`, async ({ page, baseURL }, info) => {
  test.setTimeout(120_000)
  const { api, transport } = await openBoundedSelection(page, baseURL!, width)
  const status = page.getByRole('status', { name: '批量删除进度', exact: true })
  try {
    await confirmBounded(page, transport, transport.ids)
    await expect.poll(() => transport.batches.length).toBe(1)
    await expect(status).toContainText('等待发送 101')
    const modes = page.getByRole('group', { name: '镜像来源', exact: true })
    await modes.getByRole('button', { name: '线上仓库', exact: true }).click()
    await modes.getByRole('button', { name: /^本地镜像/ }).click()
    transport.batches[0]!.release.resolve()
    await expect(page.getByRole('button', { name: '批量删除', exact: true })).toBeEnabled()
    await expect(status).toContainText('已删除 0')
    await expect(status).toContainText('已发送结果未确认 50')
    await expect(status).toContainText('未发送 101')
    await expect(status).toContainText('未发送的镜像需要重新确认，不会自动继续。')
    const unknown = page.locator('.removal-admission--unknown')
    await expect(unknown).toHaveCount(50)
    for (const id of transport.ids.slice(0, 50)) await expect(unknown.filter({ hasText: id })).toHaveCount(1)
    transport.captures.push(await capture(page, info, api, status, 'bounded-interrupted', ['已发送结果未确认 50', '未发送 101'], transport.ids))
    transport.captures.push(await capture(page, info, api, unknown.first(), 'bounded-read-only', [transport.ids[0]!, '只读刷新镜像'], transport.ids))
    const reads = api.requests.filter((request) => request === 'GET /api/v1/docker/images').length
    await unknown.first().getByRole('button', { name: '只读刷新镜像', exact: true }).click()
    await expect.poll(() => api.requests.filter((request) => request === 'GET /api/v1/docker/images').length).toBe(reads + 1)
    await expect(unknown).toHaveCount(50)
    await page.waitForTimeout(6000)
    expect(api.writes).toEqual([{ imageIds: transport.ids.slice(0, 50) }])
    // 新操作必须再次展示确认框；取消后仍无写入。
    await page.getByRole('button', { name: '批量删除', exact: true }).click()
    await expect(page.locator('.el-message-box')).toContainText('101 个未被引用的镜像')
    await page.locator('.el-message-box').getByRole('button', { name: '取消', exact: true }).click()
    expect(api.writes).toHaveLength(1)
    await confirmBounded(page, transport, transport.ids.slice(50))
    for (let index = 1; index < 4; index += 1) {
      await expect.poll(() => transport.batches.length).toBe(index + 1)
      transport.batches[index]!.release.resolve()
    }
    await expect(status).toContainText('已删除 101')
    await expect(status).toContainText('未发送 0')
    await expect(unknown).toHaveCount(50)
    expect(transport.batches.slice(1).flatMap((batch) => batch.ids)).toEqual(transport.ids.slice(50))
    await boundedLedger(info, api, transport, page)
  } finally { transport.releaseAll() }
})

test('下一批目标只读刷新后变为被引用时，首批回执不能授权后续发送', async ({ page, baseURL }, info) => {
  const { api, transport } = await openBoundedSelection(page, baseURL!)
  try {
    await confirmBounded(page, transport, transport.ids)
    await expect.poll(() => transport.batches.length).toBe(1)
    transport.images[50]!.containers = 1
    await page.getByRole('button', { name: '刷新本地镜像', exact: true }).click()
    await expect(page.locator('.resource-row').filter({ hasText: transport.names[50]! })).toContainText('1 个容器使用中')
    transport.batches[0]!.release.resolve()
    const status = page.getByRole('status', { name: '批量删除进度', exact: true })
    await expect(status).toContainText('已删除 50')
    await expect(status).toContainText('已发送结果未确认 0')
    await expect(status).toContainText('未发送 101')
    await expect(page.getByRole('button', { name: '批量删除', exact: true })).toBeEnabled()
    await expect(page.locator('.removal-admission--unknown')).toHaveCount(0)
    await page.waitForTimeout(6000)
    expect(api.writes).toEqual([{ imageIds: transport.ids.slice(0, 50) }])
    await boundedLedger(info, api, transport, page)
  } finally { transport.releaseAll() }
})
