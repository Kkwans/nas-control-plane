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
async function fixture(page: Page, origin: string, mode: Mode) {
  await page.clock.setFixedTime(new Date(stamp))
  await page.addInitScript(() => {
    // 本场景通过精确 GET 核验；任何意外 SSE 订阅直接暴露为页面错误。
    window.EventSource = class extends EventTarget {
      constructor(url: string | URL) { super(); throw new Error(`Unexpected fixture EventSource: ${String(url)}`) }
    } as unknown as typeof EventSource
  })
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
    ['/api/v1/docker/inventory', inventory],
    ...['docker.projects', 'docker.containers', 'docker.images.local', 'docker.image-downloads'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
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
    if (request.method() === 'GET' && readKey === '/api/v1/docker/images') return route.fulfill({ json: { collectedAt: stamp, images: responded && mode === 'mixed' ? [{ ...images[1], containers: 1 }] : images } })
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
  return { requests, denied, errors, writes, received, release, authorize() { expect(authorized).toBe(false); expect(writes).toHaveLength(0); authorized = true } }
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
async function capture(page: Page, info: TestInfo, api: Fixture, region: Locator, phase: string, expectedTexts: string[]) {
  await expect(page.locator('.el-message')).toHaveCount(0)
  await region.scrollIntoViewIfNeeded()
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(350)
  const before = await geometry(region), requestsBefore = [...api.requests]
  const prefix = `docker-image-remove-batch-${page.viewportSize()!.width}-${phase}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const after = await geometry(region), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(region)
  const statePath = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), phase, imageIds, expectedRequest: { imageIds }, screenshot: `${prefix}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), beforeCapture: before, afterCapture: after, afterAxe, axe, requestsBefore, requestsAfterAxe: [...api.requests], writes: [...api.writes], denied: [...api.denied], errors: [...api.errors] }, null, 2))
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
    try {
      await confirmation.getByRole('button', { name: '删除 2 个', exact: true }).click()
      await api.received.promise
      expect(api.writes).toEqual([{ imageIds }])
      for (const action of actions) await expect(action).toBeDisabled()
      await expect(page.getByText(/已删除/)).toHaveCount(0)
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
        await expect(page.getByText(/已删除/)).toHaveCount(0)
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
