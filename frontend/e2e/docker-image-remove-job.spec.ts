import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { overviewDocker, summary } from './mockApi'
import type { JobSnapshot } from '../src/api/system'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const stamp = '2026-10-15T00:00:00.000Z'
const imageId = `sha256:${'a1'.repeat(32)}`
const imageName = 'fixture/remove-target:latest'
const removePath = '/api/v1/docker/images/remove-job'
const image = { id: imageId, repoTags: [imageName], repoDigests: [], sizeBytes: 1024, createdAt: stamp, containers: 0 }
const job: JobSnapshot = { id: 'fixture-image-remove-1', type: 'docker-image-remove', reference: imageId, cancellable: false, status: 'queued', artifactState: 'unknown', message: '', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const inventory = { ...overviewDocker, collectedAt: stamp, containers: [], projects: [], engine: { ...overviewDocker.engine, containers: 0, containersRunning: 0, containersStopped: 0, images: 1 } }
type Mode = 'accepted' | 'mismatch' | 'lost'
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
  let authorized = false, admitted = false, completed = false
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
    if (request.method() === 'GET' && readKey === '/api/v1/jobs') return route.fulfill({ json: { jobs: admitted ? [{ ...job, ...(completed ? { status: 'completed', progress: 100, completedAt: stamp } : {}) }] : [] } })
    if (request.method() === 'GET' && readKey === '/api/v1/docker/images') return route.fulfill({ json: { collectedAt: stamp, images: completed ? [] : [image] } })
    if (request.method() === 'GET' && reads.has(readKey)) return route.fulfill({ json: reads.get(readKey) })
    if (request.method() === 'POST' && url.pathname === removePath && !url.search && authorized) {
      authorized = false; admitted = true
      const body: unknown = request.postDataJSON()
      writes.push(body)
      received.resolve()
      expect(body).toEqual({ imageId })
      await release.promise
      if (mode === 'lost') return route.abort('failed')
      return route.fulfill({ status: 202, json: mode === 'mismatch' ? { ...job, reference: `sha256:${'b2'.repeat(32)}` } : job })
    }
    denied.push(target)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return { requests, denied, errors, writes, received, release, complete() { expect(admitted).toBe(true); completed = true }, authorize() { expect(authorized).toBe(false); expect(writes).toHaveLength(0); authorized = true } }
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
    const fields = [...element.querySelectorAll('span, h3, p, button')].filter((field) => field.children.length === 0 && field.textContent?.trim()).map((field) => {
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
async function capture(page: Page, info: TestInfo, api: Fixture, region: Locator, phase: string) {
  await expect(page.locator('.el-message')).toHaveCount(0)
  await region.scrollIntoViewIfNeeded()
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(350)
  const before = await geometry(region), requestsBefore = [...api.requests]
  const prefix = `docker-image-remove-${page.viewportSize()!.width}-${phase}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const after = await geometry(region), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(region)
  const statePath = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), phase, imageId, expectedRequest: { imageId }, screenshot: `${prefix}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), beforeCapture: before, afterCapture: after, afterAxe, axe, requestsBefore, requestsAfterAxe: [...api.requests], writes: [...api.writes], denied: [...api.denied], errors: [...api.errors] }, null, 2))
  await info.attach(prefix, { path: png, contentType: 'image/png' })
  await info.attach(`${prefix}-axe-state`, { path: statePath, contentType: 'application/json' })
  for (const sample of [before, after, afterAxe]) {
    expect(sample.text).toContain(imageId)
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
for (const width of [390, 1440]) for (const accepted of [true, false]) {
  const mode: Mode = accepted ? 'accepted' : width === 390 ? 'mismatch' : 'lost'
  test(`${width}px 单镜像删除 ${mode}：一次确认只提交一次后台任务，后续仅只读核验`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const api = await fixture(page, new URL(baseURL!).origin, mode)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    const action = page.getByRole('button', { name: `删除镜像 ${imageName}`, exact: true }).filter({ visible: true })
    await expect(action).toBeEnabled()
    // 同一事件循环内双击，确认框本身也不能重入。
    await action.evaluate((button: HTMLButtonElement) => { button.click(); button.click() })
    const confirmation = page.locator('.el-message-box')
    await expect(confirmation).toHaveCount(1); await expect(confirmation).toContainText(imageId)
    await expect(action).toBeDisabled(); expect(api.writes).toEqual([])
    await confirmation.getByRole('button', { name: '取消', exact: true }).click()
    await expect(confirmation).toHaveCount(0); await expect(action).toBeEnabled()
    expect(api.writes).toEqual([])
    await action.click()
    await expect(confirmation).toHaveCount(1); await expect(confirmation).toContainText(imageId)
    api.authorize()
    const admissionReceipt = mode === 'lost'
      ? page.waitForEvent('requestfailed', { predicate: (request) => new URL(request.url()).pathname === removePath }).then((request) => ({ status: null, failure: request.failure(), body: null }))
      : page.waitForResponse((response) => new URL(response.url()).pathname === removePath && response.request().method() === 'POST').then(async (response) => ({ status: response.status(), body: await response.json(), failure: null }))
    const captures: Awaited<ReturnType<typeof capture>>[] = []
    try {
      await confirmation.getByRole('button', { name: '提交删除任务', exact: true }).click()
      await api.received.promise
      await expect(action).toBeDisabled()
      expect(api.writes).toEqual([{ imageId }])
      await expect(page.getByText(/已删除/)).toHaveCount(0)
      api.release.resolve()
      const receiptResult = await admissionReceipt
      if (mode === 'lost') expect(receiptResult.failure).not.toBeNull()
      else { expect(receiptResult.status).toBe(202); expect(receiptResult.body).toEqual(mode === 'mismatch' ? { ...job, reference: `sha256:${'b2'.repeat(32)}` } : job) }
      const admission = page.locator('.image-workspace [role="status"]').filter({ hasText: imageId })
      await expect(admission).toContainText(accepted ? '删除任务已接收' : '删除任务可能已接收')
      await expect(admission).toContainText(accepted ? '尚未确认删除完成' : '请勿重复提交')
      await expect(action).toBeDisabled(); await expect(page.getByText(/已删除/)).toHaveCount(0)
      captures.push(await capture(page, info, api, admission, `${mode}-admission`))
      const imagesBefore = api.requests.filter((request) => request === 'GET /api/v1/docker/images').length
      await admission.getByRole('button', { name: '只读刷新镜像', exact: true }).click()
      await expect.poll(() => api.requests.filter((request) => request === 'GET /api/v1/docker/images').length).toBe(imagesBefore + 1)
      await expect(action).toBeDisabled()
      await admission.getByRole('button', { name: '查看最近任务', exact: true }).click()
      const drawer = page.getByRole('dialog', { name: '最近任务', exact: true })
      const row = drawer.getByRole('article', { name: imageId, exact: true })
      await expect(row).toBeVisible(); await expect(row).toContainText('镜像删除'); await expect(row).toContainText('排队中')
      await expect(row.getByRole('progressbar')).toHaveCount(0)
      await row.getByRole('button', { name: '查看详情', exact: true }).click()
      await expect(row).toContainText('此任务不支持取消')
      await expect(row.getByRole('button', { name: /取消|重试|删除/ })).toHaveCount(0)
      await row.getByRole('button', { name: '收起详情', exact: true }).click()
      if (accepted) {
        api.complete()
        await drawer.getByRole('button', { name: '刷新任务', exact: true }).click()
        await expect(row).toContainText('镜像删除任务已完成，可刷新本地镜像核验当前状态。')
        await expect(row.locator('.task-state')).toHaveText('已完成')
        captures.push(await capture(page, info, api, row, 'completed-job-readback'))
      }
      await drawer.getByRole('button', { name: '关闭最近任务', exact: true }).click()
      await expect(drawer).not.toBeVisible()
      await admission.getByRole('button', { name: '只读刷新镜像', exact: true }).click()
      if (accepted) await expect(action).toHaveCount(0)
      else await expect(action).toBeDisabled()
      // 覆盖默认五秒刷新周期，未知接收结果不得走同步/批量回退或自动重发。
      await page.waitForTimeout(6000)
      expect(api.writes).toEqual([{ imageId }])
      expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([`POST ${removePath}`])
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      const receipt = info.outputPath('ui-captures', `docker-image-remove-${width}-${mode}-request-ledger-capture-state.json`)
      await writeFile(receipt, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, mode, imageId, admissionReceipt: receiptResult, linkedCaptures: captures, requestLedger: api.requests, writes: api.writes, denied: api.denied, errors: api.errors, replayObservationMilliseconds: 6000 }, null, 2))
      await info.attach('read-only-follow-up-ledger', { path: receipt, contentType: 'application/json' })
    } finally { api.release.resolve() }
  })
}
