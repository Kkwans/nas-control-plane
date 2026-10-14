import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { overviewDocker, summary } from './mockApi'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const stamp = '2026-10-14T20:00:00.000Z'
const createPath = '/api/v1/docker/containers'
const argv = ['/app/server ', '  leading  spaces  ', ' ', '"quoted value"', '$APP_MODE && echo ok; $(literal) | *', '--name=a b']
const mount = { type: 'bind', source: '/fixture/source  ', target: '/data/target  ', readOnly: false }
const device = { hostPath: '/dev/fixture  ', containerPath: '/dev/target  ', cgroupPermissions: 'rwm' }
const inventory = { ...overviewDocker, collectedAt: stamp, containers: [], projects: [], engine: { ...overviewDocker.engine, containers: 0, containersRunning: 0, containersStopped: 0, images: 1 } }
function expectedInput(defaultCommand: boolean) {
  return { image: 'identity:latest', name: 'identity', restartPolicy: 'no', environment: {}, mounts: defaultCommand ? [] : [mount], ports: [],
    ...(defaultCommand ? {} : { command: argv }), privileged: false, capAdd: [], capDrop: [], devices: defaultCommand ? [] : [device], runContainer: true }
}
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 只允许指定来源的静态 GET 与精确 API 白名单。创建许可仅供一次真实确认点击消费；
// 未知请求、重发、WebSocket 均失败关闭，内存 SSE 不访问 Docker、NAS 或宿主机。
async function fixture(page: Page, origin: string, expected: ReturnType<typeof expectedInput>) {
  await page.clock.setFixedTime(new Date(stamp))
  await page.addInitScript(() => {
    class SilentSource extends EventTarget {
      static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2
      readyState = 1; onopen: (() => void) | null = null; onerror: (() => void) | null = null
      constructor(readonly url: string) { super(); queueMicrotask(() => { this.onopen?.(); this.dispatchEvent(new Event('open')) }) }
      close() { this.readyState = 2 }
    }
    window.EventSource = SilentSource as unknown as typeof EventSource
  })
  const requests: string[] = [], denied: string[] = [], errors: string[] = []
  const writes: unknown[] = [], received = deferred(), release = deferred()
  let authorized = false
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs', { jobs: [] }], ['/api/v1/jobs?type=docker-image-pull', { jobs: [] }],
    ['/api/v1/docker/inventory', inventory],
    ['/api/v1/docker/resources', { collectedAt: stamp, networks: [], volumes: [] }],
    ['/api/v1/docker/images', { collectedAt: stamp, images: [{ id: 'sha256:identity-fixture', repoTags: ['identity:latest'], repoDigests: [], sizeBytes: 1024, createdAt: stamp, containers: 0 }] }],
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  page.on('pageerror', (error) => errors.push(error.message))
  await page.routeWebSocket('**', (socket) => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async (route) => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin || request.method() !== 'GET' || url.pathname.startsWith('/api/')) {
      denied.push(`${request.method()} ${url.href}`); return route.abort('blockedbyclient')
    }
    return route.fallback()
  })
  await page.route('**/api/**', async (route) => {
    const request = route.request(), url = new URL(request.url()), target = `${request.method()} ${url.pathname}${url.search}`
    requests.push(target)
    if (url.origin !== origin) { denied.push(target); return route.abort('blockedbyclient') }
    const readKey = url.pathname + url.search
    if (request.method() === 'GET' && reads.has(readKey)) return route.fulfill({ json: reads.get(readKey) })
    if (request.method() === 'POST' && url.pathname === createPath && !url.search && authorized) {
      authorized = false
      const body: unknown = request.postDataJSON()
      writes.push(body)
      received.resolve()
      expect(body).toEqual(expected)
      await release.promise
      return route.fulfill({ status: 201, json: { containerId: 'created-identity', name: 'identity', image: 'identity:latest', state: 'running', created: true, started: true, runContainer: true } })
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

async function geometry(page: Page) {
  return page.locator('.command-preview').evaluate((code) => {
    const cell = code.parentElement!, dialog = code.closest('[role="dialog"]')!
    const bounds = cell.getBoundingClientRect(), dialogBounds = dialog.getBoundingClientRect()
    const range = document.createRange(); range.selectNodeContents(code)
    const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
    const node = code.firstChild!
    const characters = Array.from({ length: node.textContent!.length }, (_, index) => {
      const character = document.createRange(); character.setStart(node, index); character.setEnd(node, index + 1)
      return { value: node.textContent![index], bounds: character.getBoundingClientRect().toJSON() }
    })
    const clips: Array<{ x: boolean; y: boolean; bounds: DOMRect }> = []
    for (let ancestor: Element | null = cell; ancestor; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor)
      const x = /hidden|clip|auto|scroll/.test(style.overflowX), y = /hidden|clip|auto|scroll/.test(style.overflowY)
      if (x || y) clips.push({ x, y, bounds: ancestor.getBoundingClientRect() })
    }
    return { text: code.textContent, whiteSpace: getComputedStyle(code).whiteSpace, characters,
      viewport: { width: innerWidth, height: innerHeight }, root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      bounds: bounds.toJSON(), dialogBounds: dialogBounds.toJSON(), lines: lines.map((line) => line.toJSON()),
      clips: clips.map((clip) => ({ ...clip, bounds: clip.bounds.toJSON() })),
      fullTextVisible: lines.length > 0 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1 && line.left >= -1 && line.right <= innerWidth + 1 && line.top >= -1 && line.bottom <= innerHeight + 1 && clips.every((clip) => (!clip.x || line.left >= clip.bounds.left - 1 && line.right <= clip.bounds.right + 1) && (!clip.y || line.top >= clip.bounds.top - 1 && line.bottom <= clip.bounds.bottom + 1))),
    }
  })
}
async function capture(page: Page, info: TestInfo, api: Fixture, defaultCommand: boolean) {
  await page.evaluate(() => document.fonts.ready)
  await page.locator('.command-preview').scrollIntoViewIfNeeded()
  // 等待弹窗进入动画结束，截图和几何必须来自同一稳定布局。
  await page.waitForTimeout(350)
  const before = await geometry(page), requestsBefore = [...api.requests]
  const prefix = `docker-create-identity-${defaultCommand ? 'default' : 'argv-paths'}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const after = await geometry(page), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(page)
  const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, expectedInput: expectedInput(defaultCommand), expectedPreview: defaultCommand ? '使用镜像默认命令' : JSON.stringify(argv), testTitle: info.title, screenshot: `${prefix}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), beforeCapture: before, afterCapture: after, afterAxe, requestsBefore, requestsAfterAxe: [...api.requests], writes: [...api.writes], denied: [...api.denied], axe }
  const statePath = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await info.attach(prefix, { path: png, contentType: 'image/png' })
  await info.attach(`${prefix}-axe-state`, { path: statePath, contentType: 'application/json' })
  expect(api.requests).toEqual(requestsBefore); expect(api.writes).toEqual([])
  for (const sample of [before, after, afterAxe]) {
    expect(sample.text).toBe(metadata.expectedPreview)
    expect(sample.whiteSpace).toBe('pre-wrap')
    expect(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.clientWidth + 1)
    expect(sample.fullTextVisible, JSON.stringify(sample)).toBe(true)
    if (!defaultCommand) {
      expect(JSON.parse(sample.text!)).toEqual(argv)
      // 每个空格必须占据真实字形宽度，不能只验证 DOM 字符串或截图存在。
      expect(sample.characters.filter((character) => character.value === ' ').every((character) => character.bounds.width > 0)).toBe(true)
    }
  }
  expect(after).toEqual(before); expect(afterAxe).toEqual(before)
  expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
  return {
    prefix, screenshot: `${prefix}.png`, screenshotSha256: metadata.screenshotSha256,
    captureState: `${prefix}-axe-capture-state.json`,
    captureStateSha256: createHash('sha256').update(await readFile(statePath)).digest('hex'),
  }
}

for (const width of [390, 1440]) for (const defaultCommand of [false, true]) {
  test(`${width}px ${defaultCommand ? '全空白命令省略 command' : 'argv 与设备挂载路径保留原始空白'}，确认前零写入且仅接受一次 201 回执`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const expected = expectedInput(defaultCommand), api = await fixture(page, new URL(baseURL!).origin, expected)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    await page.getByRole('button', { name: '从镜像 identity:latest 创建容器', exact: true }).filter({ visible: true }).click()
    const drawer = page.locator('.create-container-drawer')
    await expect(drawer).toBeVisible()
    await expect.poll(() => api.requests.includes('GET /api/v1/docker/resources')).toBe(true)
    await expect(drawer.getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0)
    await drawer.getByRole('textbox', { name: '容器启动参数（每行一个）', exact: true }).fill(defaultCommand ? ' \t\n\u00a0\u3000' : '\n' + argv.join('\n\n') + '\n')
    if (!defaultCommand) {
      await drawer.getByRole('button', { name: '添加挂载', exact: true }).click()
      await drawer.getByPlaceholder('/volume2/data', { exact: true }).fill(mount.source)
      await drawer.getByPlaceholder('容器路径，例如 /data', { exact: true }).fill(mount.target)
      await drawer.locator('.advanced-toggle').click()
      await drawer.getByRole('button', { name: '添加设备', exact: true }).click()
      await drawer.getByPlaceholder('主机设备 /dev/...', { exact: true }).fill(device.hostPath)
      await drawer.getByPlaceholder('容器设备 /dev/...', { exact: true }).fill(device.containerPath)
    }
    await drawer.getByRole('button', { name: '创建并启动', exact: true }).click()
    const preview = page.getByRole('dialog', { name: '确认容器配置', exact: true })
    await expect(preview).toBeVisible()
    expect(api.writes).toEqual([])
    const previewEvidence = await capture(page, info, api, defaultCommand)
    api.authorize()
    const responsePromise = page.waitForResponse((response) => new URL(response.url()).pathname === createPath && response.request().method() === 'POST')
    try {
      await preview.getByRole('button', { name: '确认创建并启动', exact: true }).click()
      await api.received.promise
      expect(api.writes).toEqual([expected])
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      await expect(drawer).toBeVisible()
      await expect(drawer.getByRole('button', { name: '创建并启动', exact: true })).toBeDisabled()
      api.release.resolve()
      const response = await responsePromise
      expect(response.status()).toBe(201)
      expect(await response.json()).toEqual({ containerId: 'created-identity', name: 'identity', image: 'identity:latest', state: 'running', created: true, started: true, runContainer: true })
      await expect(page.getByText('容器“identity”已创建并启动', { exact: true })).toBeVisible()
      await expect(preview).not.toBeVisible()
      // 覆盖五秒刷新周期，确认无自动重放；未授权第二次 POST 会失败关闭。
      await page.waitForTimeout(6000)
      expect(api.writes).toEqual([expected])
      expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([`POST ${createPath}`])
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      // 独立记录提交后的回执与重放观察；不能冒充确认前 PNG 的截图时状态。
      const receiptPath = info.outputPath('ui-captures', `${previewEvidence.prefix}-receipt-capture-state.json`)
      const receipt = {
        ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
        evidenceKind: 'post-create-receipt', phase: 'after-create-response-and-replay-observation',
        testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
        preCreatePreviewEvidence: previewEvidence, replayObservationMilliseconds: 6000,
        expected, requests: [...api.requests], writes: [...api.writes], denied: [...api.denied], errors: [...api.errors],
        status: response.status(), response: await response.json(),
      }
      await writeFile(receiptPath, JSON.stringify(receipt, null, 2))
      await info.attach('strict-create-receipt', { path: receiptPath, contentType: 'application/json' })
    } finally { api.release.resolve() }
  })
}
