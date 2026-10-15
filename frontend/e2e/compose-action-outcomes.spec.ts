import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { summary, overviewDocker } from './mockApi'
import { waitForPageReady } from './pageReadiness'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const containerId = 'fixture-before-recreate', projectId = 'compose:fixture-receipt', name = 'Compose 回执验证项目'
const workingDirectory = '/fixture/compose-original', configFiles = ['/fixture/compose-original/compose.yaml', '/fixture/compose-original/override.yaml']
type Action = 'start' | 'stop' | 'restart'
type Mode = 'completed' | 'wrong-project' | 'wrong-action' | 'verify-failed'
type RequestRecord = { method: string; path: string; body: unknown }
const stamp = '2026-10-14T18:00:00.000Z'
const receipt = (action: Action) => ({ projectId, action, state: action === 'stop' ? 'stopped' : 'running', completed: true, output: '', services: [
  { name: 'web', containerId: 'fixture-new-replica-1', state: action === 'stop' ? 'exited' : 'running', running: action !== 'stop' },
  { name: 'web', containerId: 'fixture-new-replica-2', state: action === 'stop' ? 'created' : 'running', running: action !== 'stop' },
] })
const inventory = {
  collectedAt: stamp,
  engine: { ...overviewDocker.engine, containers: 1, containersRunning: 1, containersStopped: 0 },
  projects: [{ id: projectId, name, kind: 'compose', workingDirectory, configFiles, containerCount: 1, runningCount: 1, state: 'running' }],
  containers: [{ ...overviewDocker.containers[0]!, id: containerId, name: 'fixture-web', projectId, projectName: name, state: 'running', status: 'Up', ports: [] }],
}
async function fixture(page: Page, origin: string) {
  await page.clock.install({ time: new Date(stamp) })
  // 使用已打开的静默内存流，禁止通用初始快照替换本例目标。
  await page.addInitScript(() => {
    class SilentSource extends EventTarget {
      static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2
      readyState = 1; onopen: (() => void) | null = null; onerror: (() => void) | null = null
      constructor(readonly url: string) { super(); queueMicrotask(() => { this.onopen?.(); this.dispatchEvent(new Event('open')) }) }
      close() { this.readyState = 2 }
    }
    window.EventSource = SilentSource as unknown as typeof EventSource
  })
  const requests: RequestRecord[] = [], denied: string[] = [], errors: string[] = []
  const writes: RequestRecord[] = []
  let currentInventory = structuredClone(inventory)
  let allowed: { mode: Mode; action: Action } | undefined
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs', { jobs: [] }],
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  page.on('pageerror', (error) => errors.push(error.message))
  await page.routeWebSocket('**', (socket) => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (url.origin !== origin) { denied.push(`HOST ${url.origin}`); return route.abort('blockedbyclient') }
    if (route.request().method() !== 'GET' || url.pathname.startsWith('/api/')) { denied.push(`${route.request().method()} ${url.pathname}`); return route.abort('blockedbyclient') }
    return route.fallback()
  })
  await page.route('**/api/**', async (route) => {
    const req = route.request(), url = new URL(req.url()), pathname = url.pathname
    if (url.origin !== origin) { denied.push(`HOST ${url.origin}`); return route.abort('blockedbyclient') }
    const body: unknown = req.postData() ? req.postDataJSON() : null
    const entry = { method: req.method(), path: pathname, body }
    requests.push(entry)
    if (req.method() === 'GET' && pathname === '/api/v1/docker/inventory') return route.fulfill({ json: { ...currentInventory, collectedAt: new Date(await page.evaluate(() => Date.now())).toISOString() } })
    if (req.method() === 'POST' && allowed && pathname === `/api/v1/docker/compose/projects/${encodeURIComponent(projectId)}/actions/${allowed.action}`) {
      const { mode, action } = allowed; allowed = undefined
      writes.push(entry)
      expect(body).toEqual({ projectId, action, workingDirectory, configFiles })
      if (mode === 'verify-failed') return route.fulfill({ status: 409, json: { code: 'COMPOSE_LIFECYCLE_VERIFY_FAILED', message: 'fixture-private-output-must-not-render' } })
      const result = receipt(action)
      if (mode === 'wrong-project') result.projectId = 'compose:other-project'
      if (mode === 'wrong-action') result.action = 'start'
      if (mode === 'completed') {
        const stopped = action === 'stop'
        currentInventory = structuredClone(inventory)
        currentInventory.projects[0]!.state = stopped ? 'stopped' : 'running'
        currentInventory.projects[0]!.runningCount = stopped ? 0 : 1
        currentInventory.containers[0]!.state = stopped ? 'stopped' : 'running'
        currentInventory.engine.containersRunning = stopped ? 0 : 1
        currentInventory.engine.containersStopped = stopped ? 1 : 0
      }
      return route.fulfill({ json: result })
    }
    if (req.method() === 'GET' && reads.has(pathname)) return route.fulfill({ json: reads.get(pathname) })
    denied.push(`${req.method()} ${pathname}`)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return {
    requests, denied, errors, writes,
    allow(mode: Mode, action: Action) { expect(allowed).toBeUndefined(); allowed = { mode, action } },
    stageInventory(changed: boolean) {
      currentInventory = structuredClone(inventory)
      if (changed) Object.assign(currentInventory.projects[0]!, { name: '清单已变化的项目', workingDirectory: '/fixture/changed', configFiles: ['/fixture/changed/compose.yaml'] })
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), trackedDiffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex'), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')]))) }
}
const actionButton = (page: Page, action: Action, projectName = name) => {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  return page.viewportSize()!.width === 390
    ? page.locator('.docker-mobile-list').getByRole('button', { name: label, exact: true })
    : page.getByRole('button', { name: `${label}项目 ${projectName}`, exact: true }).filter({ visible: true })
}
async function geometry(page: Page) {
  return page.evaluate(() => {
    const selectors = ['.action-error', '.action-error > span', '.action-error button', '.el-message', '.el-message__content', '.el-message-box', '.el-message-box__message', '.el-message-box button', '[role="tooltip"]']
    return {
      viewport: { width: innerWidth, height: innerHeight, devicePixelRatio },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY }, focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label'), text: document.activeElement?.textContent },
      tooltipTrigger: [...document.querySelectorAll<HTMLButtonElement>('button[aria-label]')].find((button) => button.getAttribute('aria-label')?.startsWith('重启项目 ') && button.getBoundingClientRect().width > 0)?.getBoundingClientRect().toJSON(),
      semantic: { alert: document.querySelector('.action-error')?.textContent, messages: [...document.querySelectorAll('.el-message')].map((node) => node.textContent), dialogs: [...document.querySelectorAll('.el-message-box')].map((node) => node.textContent) },
      controls: selectors.flatMap((selector) => [...document.querySelectorAll<HTMLElement>(selector)].map((element) => {
        const bounds = element.getBoundingClientRect(), style = getComputedStyle(element)
        const range = document.createRange(); range.selectNodeContents(element)
        const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return { selector, tag: element.tagName, text: element.textContent, bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()),
          visible: bounds.width > 0 && bounds.height > 0 && style.visibility !== 'hidden' && element.getAttribute('aria-hidden') !== 'true',
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1),
          centerHit: Boolean(hit && (hit === element || element.contains(hit))), noWrap: style.whiteSpace === 'nowrap', inMain: Boolean(element.closest('main, [role="main"]')) }
      })).filter((control) => control.visible),
    }
  })
}
type Artifact = { file: string; sha256: string }
const evidence = new WeakMap<Page, { api: Fixture; artifacts: Artifact[] }>()
const hash = (data: string | Buffer) => createHash('sha256').update(data).digest('hex')
async function capture(page: Page, info: TestInfo, api: Fixture, state: string): Promise<void> {
  // 保持时钟运行，完整 axe 依赖异步计时器；完成提示由真实 hover 暂停自动关闭。
  await waitForPageReady(page)
  await page.evaluate(() => document.fonts.ready)
  await page.evaluate(() => scrollTo({ top: 0, left: 0, behavior: 'instant' }))
  const beforeCapture = await geometry(page), requestsBefore = structuredClone(api.requests)
  const prefix = `compose-action-outcome-${state}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: true, animations: 'disabled' })
  const artifacts = evidence.get(page)!.artifacts
  const screenshotSha256 = hash(await readFile(png))
  artifacts.push({ file: `${prefix}.png`, sha256: screenshotSha256 })
  const afterCapture = await geometry(page), requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(page)
  const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
    fixtureSource: 'frontend/e2e/compose-action-outcomes.spec.ts + frontend/e2e/mockApi.ts', state, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), fullPage: true,
    screenshot: `${prefix}.png`, screenshotSha256, beforeCapture, afterCapture, afterAxe,
    requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), writes: structuredClone(api.writes), denied: api.denied, errors: api.errors }
  await info.attach(prefix, { path: png, contentType: 'image/png' })
  for (const [suffix, content] of [['capture-state', metadata], ['axe-capture-state', { ...metadata, axe }]] as const) {
    const file = `${prefix}-${suffix}.json`, path = info.outputPath('ui-captures', file), data = JSON.stringify(content, null, 2)
    await writeFile(path, data); artifacts.push({ file, sha256: hash(data) }); await info.attach(file, { path, contentType: 'application/json' })
  }
  // 先写完整证据，再检查真实 Element 对话框、提示、文本换行与命中区域。
  expect(api.requests).toEqual(requestsBefore); expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
  for (const sample of [beforeCapture, afterCapture, afterAxe]) {
    expect(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.clientWidth + 1)
    expect(sample.semantic).toEqual(beforeCapture.semantic)
    expect(sample.focus).toEqual(beforeCapture.focus)
    expect(sample.scroll).toEqual(beforeCapture.scroll)
    if (state !== 'stop-confirmation' && !state.endsWith('completed') && sample.viewport.width === 1440) {
      const tooltips = sample.controls.filter((control) => control.selector === '[role="tooltip"]')
      expect(tooltips).toHaveLength(1); expect(sample.tooltipTrigger).toBeDefined()
      const tip = tooltips[0]!.bounds, trigger = sample.tooltipTrigger!
      expect(trigger.top - tip.bottom).toBeGreaterThanOrEqual(0); expect(trigger.top - tip.bottom).toBeLessThanOrEqual(24)
      expect(Math.abs((trigger.left + trigger.right - tip.left - tip.right) / 2)).toBeLessThanOrEqual(4)
    }
    for (const control of sample.controls) {
      expect(control.textContained, JSON.stringify(control)).toBe(true)
      expect(control.bounds.left).toBeGreaterThanOrEqual(0)
      expect(control.bounds.right).toBeLessThanOrEqual(sample.viewport.width + 1)
      if (control.selector === '.action-error > span' && sample.viewport.width === 390) expect(control.lines.length).toBeGreaterThan(1)
      if (control.selector === '.el-message-box button') {
        expect(control.bounds.width, JSON.stringify(control)).toBeGreaterThan(0)
        expect(control.bounds.height, JSON.stringify(control)).toBeGreaterThan(0)
        expect(control.centerHit, JSON.stringify(control)).toBe(true)
      }
      if (control.selector === '.action-error button') {
        expect(control.bounds.width).toBeGreaterThanOrEqual(44); expect(control.bounds.height).toBeGreaterThanOrEqual(44)
        expect(control.centerHit).toBe(true); expect(control.noWrap).toBe(true); expect(control.lines).toHaveLength(1)
      }
      if (control.selector === '[role="tooltip"]') expect(control.inMain).toBe(true)
    }
  }
  expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
}
async function finalLedger(info: TestInfo, api: Fixture, artifacts: Artifact[]) {
  const requests = structuredClone(api.requests)
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const file = info.outputPath('ui-captures', 'compose-final-request-ledger-capture-state.json')
  await writeFile(file, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, artifacts,
    requests, requestsSha256: hash(JSON.stringify(requests)), writes: api.writes, denied: api.denied, errors: api.errors }, null, 2))
  await info.attach('compose-final-request-ledger', { path: file, contentType: 'application/json' })
  expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
}
async function open(page: Page, baseURL: string, width: number) {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
  const api = await fixture(page, new URL(baseURL).origin)
  await page.goto('/docker', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible()
  await expect(actionButton(page, 'restart')).toBeEnabled()
  return api
}
test.afterEach(async ({ page }, info) => {
  const saved = evidence.get(page)
  if (saved) await finalLedger(info, saved.api, saved.artifacts)
})
for (const width of [390, 1440]) {
  test(`${width}px Compose 完成回执只表述读回状态，重建副本不绑定提交前容器`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    const api = await open(page, baseURL!, width), artifacts: Artifact[] = []
    evidence.set(page, { api, artifacts })
    for (const action of ['stop', 'start'] as const) {
      const before = api.requests.length, writesBefore = api.writes.length
      api.allow('completed', action); await actionButton(page, action).click()
      if (action === 'stop') {
        const dialog = page.locator('.el-message-box')
        await expect(dialog).toBeVisible(); expect(api.writes).toHaveLength(writesBefore)
        await capture(page, info, api, 'stop-confirmation')
        await dialog.getByRole('button', { name: '确认停止', exact: true }).click()
        await expect(dialog).toHaveCount(0)
      }
      const success = page.locator('.el-message--success')
      // Element Plus 的公开鼠标交互暂停本条提示，不改写页面/axe 计时器。
      await success.hover()
      await expect(success).toContainText(action === 'stop' ? '读回时服务均处于非运行状态' : '读回时服务均运行中')
      await expect(success).not.toContainText(/健康|可用|部署成功/)
      await expect(page.locator('.action-error')).toHaveCount(0)
      await expect.poll(() => api.requests.slice(before).filter((entry) => entry.method === 'GET' && entry.path === '/api/v1/docker/inventory').length).toBe(1)
      expect(api.writes).toHaveLength(writesBefore + 1)
      await capture(page, info, api, `${action}-completed`)
      await page.mouse.move(0, 0)
      await page.clock.fastForward(6000)
      await expect(page.locator('.el-message')).toHaveCount(0)
      expect(api.requests.slice(before).filter((entry) => entry.method !== 'GET')).toEqual([api.writes[writesBefore]])
    }
  })
  test(`${width}px Compose 错配回执和 409 核验失败保持未知，确认配置且只读核对不重放`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    const api = await open(page, baseURL!, width), artifacts: Artifact[] = []
    evidence.set(page, { api, artifacts })
    for (const mode of ['wrong-project', 'wrong-action', 'verify-failed'] as const) {
      api.stageInventory(false)
      if (mode !== 'wrong-project') {
        // 新一轮经真实页面重载读取原始配置，不把暂存响应冒充已更新的客户端状态。
        await page.reload({ waitUntil: 'domcontentloaded' })
        await expect(actionButton(page, 'restart')).toBeEnabled()
      }
      const before = api.requests.length, writesBefore = api.writes.length
      api.allow(mode, 'restart'); await actionButton(page, 'restart').click()
      const dialog = page.locator('.el-message-box')
      await expect(dialog).toBeVisible(); await expect(dialog).toContainText(name)
      expect(api.writes).toHaveLength(writesBefore)
      // /docker 没有实时订阅：仅安排操作后的真实 GET 返回变化清单。
      // 确认期间客户端清单变更的冻结快照证据由 DockerView.actionOutcomes.test.ts 覆盖。
      api.stageInventory(true)
      await expect(dialog).toContainText(name); await expect(dialog).not.toContainText('清单已变化的项目')
      await dialog.getByRole('button', { name: '确认重启', exact: true }).click()
      await expect(dialog).toHaveCount(0)
      await expect(page.locator('.action-error')).toContainText('可能已经执行')
      await expect(page.locator('.action-error')).toContainText('只读核对，勿重复提交')
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      await expect(page.locator('main')).not.toContainText('fixture-private-output-must-not-render')
      await expect.poll(() => api.requests.slice(before).filter((entry) => entry.method === 'GET' && entry.path === '/api/v1/docker/inventory').length).toBe(1)
      await expect(page.getByText('清单已变化的项目', { exact: true }).filter({ visible: true })).toBeVisible()
      await page.clock.fastForward(6000)
      await expect(page.locator('.el-message')).toHaveCount(0)
      await expect(page.locator('.action-error')).toContainText('结果尚未确认')
      if (width === 1440) {
        await actionButton(page, 'restart', '清单已变化的项目').hover()
        await expect(page.getByRole('tooltip').filter({ hasText: '重启项目' })).toBeVisible()
      }
      await capture(page, info, api, mode)
      expect(api.writes).toHaveLength(writesBefore + 1)
      expect(api.requests.slice(before).filter((entry) => entry.method !== 'GET')).toEqual([api.writes[writesBefore]])
      expect(api.requests.slice(api.requests.findIndex((entry) => entry === api.writes[writesBefore]) + 1).every((entry) => entry.method === 'GET')).toBe(true)
      await page.mouse.move(0, 0)
    }
  })
}
