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
const containerId = 'c'.repeat(64), projectId = 'fixture-keyboard-project', name = 'fixture-keyboard-web'
type Action = 'start' | 'stop' | 'restart'
type Surface = 'list' | 'drawer'
type RequestRecord = { method: string; path: string; body: unknown }
const stamp = '2026-10-14T18:00:00.000Z'
// 202 仅接收任务；完成与当前清单由后续只读 GET 核对。
const receipt = (action: Action) => ({ id: `keyboard-${action}`, type: `docker-container-${action}`, reference: containerId, cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} })
const inventory = {
  collectedAt: stamp,
  engine: { ...overviewDocker.engine, containers: 1, containersRunning: 1, containersStopped: 0 },
  projects: [{ id: projectId, name, kind: 'standalone', workingDirectory: '', configFiles: [], containerCount: 1, runningCount: 1, state: 'running' }],
  containers: [{ ...overviewDocker.containers[0]!, id: containerId, name, projectId, projectName: name, state: 'running', status: 'Up', ports: [] }],
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
  const writes: RequestRecord[] = [], servedReceipts: ReturnType<typeof receipt>[] = []
  let currentInventory = structuredClone(inventory)
  let allowed: Action | undefined
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
    if (req.method() === 'POST' && allowed && pathname === `/api/v1/docker/containers/${containerId}/action-jobs/${allowed}`) {
      const action = allowed; allowed = undefined
      writes.push(entry)
      expect(body).toBeNull()
      const stopped = action === 'stop'
      currentInventory = structuredClone(inventory)
      currentInventory.projects[0]!.state = stopped ? 'stopped' : 'running'
      currentInventory.projects[0]!.runningCount = stopped ? 0 : 1
      currentInventory.containers[0]!.state = stopped ? 'exited' : 'running'
      currentInventory.containers[0]!.status = stopped ? 'Exited (0)' : 'Up'
      currentInventory.engine.containersRunning = stopped ? 0 : 1
      currentInventory.engine.containersStopped = stopped ? 1 : 0
      const result = receipt(action)
      servedReceipts.push(result)
      reads.set(`/api/v1/jobs/${result.id}`, { ...result, status: 'completed', progress: 100, completedAt: stamp })
      return route.fulfill({ status: 202, json: result })
    }
    if (req.method() === 'GET' && reads.has(pathname)) return route.fulfill({ json: reads.get(pathname) })
    denied.push(`${req.method()} ${pathname}`)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return {
    requests, denied, errors, writes, servedReceipts,
    allow(action: Action) { expect(allowed).toBeUndefined(); allowed = action },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), trackedDiffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex'), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')]))) }
}
const confirmation = (page: Page) => page.locator('.ncp-container-action-confirmation')
const actionButton = (page: Page, action: Action) => {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  if (evidence.get(page)?.surface === 'drawer') return page.locator('.project-drawer .container-card__actions').getByRole('button', { name: `${label} ${name}`, exact: true })
  return page.viewportSize()!.width === 390
    ? page.locator('.container-mobile-list').getByRole('button', { name: label, exact: true })
    : page.getByRole('button', { name: `${label}容器`, exact: true }).filter({ visible: true })
}
async function geometry(page: Page) {
  return page.evaluate(() => {
    const selectors = ['.action-error', '.action-error > span', '.action-error button', '.el-message', '.el-message__content', '.el-message-box', '.el-message-box__message', '.el-message-box__title', '.el-message-box__headerbtn', '.el-message-box button', '[role="tooltip"]']
    return {
      viewport: { width: innerWidth, height: innerHeight, devicePixelRatio },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY }, focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label'), text: document.activeElement?.textContent },
      semantic: { alert: document.querySelector('.action-error')?.textContent, messages: [...document.querySelectorAll('.el-message')].map((node) => node.textContent), dialogs: [...document.querySelectorAll('.el-message-box')].map((node) => node.textContent) },
      controls: selectors.flatMap((selector) => [...document.querySelectorAll<HTMLElement>(selector)].map((element) => {
        const bounds = element.getBoundingClientRect(), style = getComputedStyle(element)
        const range = document.createRange(); range.selectNodeContents(element)
        const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return { selector, tag: element.tagName, text: element.textContent, bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()),
          visible: bounds.width > 0 && bounds.height > 0 && style.visibility !== 'hidden' && element.getAttribute('aria-hidden') !== 'true',
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && (element.tagName !== 'BUTTON' || (line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1))),
          centerHit: Boolean(hit && (hit === element || element.contains(hit))), noWrap: style.whiteSpace === 'nowrap', inMain: Boolean(element.closest('main, [role="main"]')) }
      })).filter((control) => control.visible),
    }
  })
}
type Artifact = { file: string; sha256: string }
type Dismissal = { action: Action; activationKey: 'Enter' | 'Space'; method: 'cancel' | 'close' | 'escape'; requests: RequestRecord[]; focusCycle: { key: string; contained: boolean; control: string; focusVisible: boolean; outlineWidth: number; outlineStyle: string; outlineColor: string }[]; restored: boolean; completed: boolean }
type Completion = { action: Action; activationKey: 'Enter' | 'Space'; requests: RequestRecord[]; receipt: ReturnType<typeof receipt>; completed: boolean }
const evidence = new WeakMap<Page, { api: Fixture; surface: Surface; artifacts: Artifact[]; dismissals: Dismissal[]; completions: Completion[] }>()
const hash = (data: string | Buffer) => createHash('sha256').update(data).digest('hex')
async function capture(page: Page, info: TestInfo, api: Fixture, state: string): Promise<void> {
  // Keep the clock running: full axe needs live asynchronous timers.
  await waitForPageReady(page)
  await page.evaluate(() => document.fonts.ready)
  if (state.endsWith('-confirmation')) {
    await expect(confirmation(page)).toContainText(name)
    await expect(confirmation(page)).toContainText(`完整容器 ID：${containerId}`)
    await expect(confirmation(page)).toContainText('接收不代表完成，任务不能取消')
    await expect(confirmation(page)).toContainText('容器、镜像和卷不会删除')
  }
  await page.evaluate(() => scrollTo({ top: 0, left: 0, behavior: 'instant' }))
  const beforeCapture = await geometry(page), requestsBefore = structuredClone(api.requests)
  const prefix = `container-action-keyboard-${evidence.get(page)!.surface}-${state}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: true, animations: 'disabled' })
  const artifacts = evidence.get(page)!.artifacts
  const screenshotSha256 = hash(await readFile(png))
  artifacts.push({ file: `${prefix}.png`, sha256: screenshotSha256 })
  const afterCapture = await geometry(page), requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(page)
  const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, gapBasis: 'source-inferred; no pre-fix browser failure claimed',
    fixtureSource: 'frontend/e2e/container-action-keyboard.spec.ts + frontend/e2e/mockApi.ts', surface: evidence.get(page)!.surface, state, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), fullPage: true,
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
    if (state.endsWith('-confirmation')) {
      const buttons = sample.controls.filter((control) => control.selector === '.el-message-box button')
      expect(buttons).toHaveLength(3)
      const title = sample.controls.find((control) => control.selector === '.el-message-box__title')!
      const close = sample.controls.find((control) => control.selector === '.el-message-box__headerbtn')!
      expect(title).toBeDefined(); expect(close).toBeDefined()
      expect(title.bounds.right).toBeLessThanOrEqual(close.bounds.left)
    }
    for (const control of sample.controls) {
      expect(control.textContained, JSON.stringify(control)).toBe(true)
      expect(control.bounds.left).toBeGreaterThanOrEqual(0)
      expect(control.bounds.right).toBeLessThanOrEqual(sample.viewport.width + 1)
      if (control.selector === '.el-message-box button') {
        expect(control.bounds.width, JSON.stringify(control)).toBeGreaterThanOrEqual(44)
        expect(control.bounds.height, JSON.stringify(control)).toBeGreaterThanOrEqual(44)
        expect(control.centerHit, JSON.stringify(control)).toBe(true)
        expect(control.bounds.top).toBeGreaterThanOrEqual(0)
        expect(control.bounds.bottom).toBeLessThanOrEqual(sample.viewport.height + 1)
      }
      if (control.selector === '[role="tooltip"]') expect(control.inMain).toBe(true)
    }
  }
  expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
}
async function verifyDismissals(page: Page, info: TestInfo, api: Fixture, action: 'stop' | 'restart') {
  for (const [method, activationKey] of [['cancel', 'Enter'], ['close', 'Enter'], ['escape', 'Enter'], ['escape', 'Space']] as const) {
    const trigger = actionButton(page, action), before = api.requests.length, writesBefore = api.writes.length
    const record: Dismissal = { action, method, activationKey, requests: [], focusCycle: [], restored: false, completed: false }
    evidence.get(page)!.dismissals.push(record)
    try {
      // 明确从键盘焦点进入，才可核实 Element Plus 真实焦点恢复而非页面碰巧聚焦。
      await trigger.focus()
      await trigger.press(activationKey)
      const dialog = confirmation(page)
      await expect(dialog).toBeVisible()
      await expect(dialog.getByRole('button')).toHaveCount(3)
      await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true)
      const focusCycle = record.focusCycle
      for (const key of ['Tab', 'Tab', 'Tab', 'Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab']) {
        await page.keyboard.press(key)
        const focused = await dialog.evaluate((element, key) => {
          const active = document.activeElement!, style = getComputedStyle(active)
          return { key, contained: element.contains(active), control: active.classList.contains('el-message-box__headerbtn') ? 'close' : active.textContent!.trim(),
            focusVisible: active.matches(':focus-visible'), outlineWidth: Number.parseFloat(style.outlineWidth), outlineStyle: style.outlineStyle, outlineColor: style.outlineColor }
        }, key)
        focusCycle.push(focused)
        expect(focused.contained).toBe(true)
        expect(focused.focusVisible).toBe(true)
        expect(focused.outlineWidth).toBeGreaterThanOrEqual(2)
        expect(focused.outlineStyle).toBe('solid')
        expect(focused.outlineColor).not.toMatch(/^(transparent|rgba\(0, 0, 0, 0\))$/)
      }
      expect(new Set(focusCycle.map((focused) => focused.control))).toEqual(new Set(['close', '取消', action === 'stop' ? '确认停止' : '确认重启']))
      if (method === 'cancel') await capture(page, info, api, `${action}-confirmation`)
      if (method === 'escape') await page.keyboard.press('Escape')
      else if (method === 'close') { await dialog.locator('.el-message-box__headerbtn').focus(); await page.keyboard.press('Enter') }
      else { await dialog.getByRole('button', { name: '取消', exact: true }).focus(); await page.keyboard.press('Space') }
      await expect(dialog).toHaveCount(0)
      await expect(trigger).toBeEnabled()
      record.restored = await trigger.evaluate((element) => element === document.activeElement)
      record.requests = structuredClone(api.requests.slice(before))
      await expect(trigger).toBeFocused()
      record.restored = true
      expect(record.requests.filter((entry) => entry.method !== 'GET')).toEqual([])
      expect(api.writes).toHaveLength(writesBefore)
      expect(api.denied).toEqual([])
      await expect(page.locator('.el-message, .action-error')).toHaveCount(0)
      record.completed = true
    } finally {
      // 失败也保留已观察到的键盘轨迹与本轮请求，便于 CI 诊断。
      record.requests = structuredClone(api.requests.slice(before))
    }
  }
}
async function finalLedger(info: TestInfo, api: Fixture, artifacts: Artifact[], dismissals: Dismissal[], completions: Completion[], surface: Surface) {
  const requests = structuredClone(api.requests)
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const file = info.outputPath('ui-captures', 'container-keyboard-final-request-ledger-capture-state.json')
  await writeFile(file, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, gapBasis: 'source-inferred; no pre-fix browser failure claimed', artifacts, dismissals, completions, surface,
    requests, requestsSha256: hash(JSON.stringify(requests)), writes: api.writes, servedReceipts: api.servedReceipts, denied: api.denied, errors: api.errors }, null, 2))
  await info.attach('container-keyboard-final-request-ledger', { path: file, contentType: 'application/json' })
  expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
}
test.afterEach(async ({ page }, info) => {
  const saved = evidence.get(page)
  if (saved) await finalLedger(info, saved.api, saved.artifacts, saved.dismissals, saved.completions, saved.surface)
})
for (const width of [1440, 390]) for (const surface of ['list', 'drawer'] as const) {
  test(`${width}px ${surface} 单容器原生键盘确认、取消和完成回执`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const api = await fixture(page, new URL(baseURL!).origin)
    const saved = { api, surface, artifacts: [] as Artifact[], dismissals: [] as Dismissal[], completions: [] as Completion[] }
    evidence.set(page, saved)
    await page.goto(surface === 'drawer' ? `/docker?project=${projectId}` : '/docker', { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible()
    if (surface === 'list') await page.getByRole('button', { name: '容器', exact: true }).click()
    else await expect(page.locator('.project-drawer')).toBeVisible()
    await expect(actionButton(page, 'restart')).toBeEnabled()
    // No write permission exists throughout native activation and all dismissal paths.
    for (const action of ['stop', 'restart'] as const) await verifyDismissals(page, info, api, action)
    expect(api.writes).toEqual([])
    // Stop produces the stopped fixture used by Start; Start must not open a dialog.
    for (const [action, activationKey] of [['stop', 'Space'], ['start', 'Enter'], ['restart', 'Enter']] as const) {
      const before = api.requests.length, writesBefore = api.writes.length
      const record: Completion = { action, activationKey, requests: [], receipt: receipt(action), completed: false }
      saved.completions.push(record)
      try {
        const trigger = actionButton(page, action)
        await expect(trigger).toBeEnabled()
        await trigger.focus()
        if (action === 'start') api.allow(action)
        await trigger.press(activationKey)
        if (action !== 'start') {
          const dialog = confirmation(page)
          await expect(dialog).toBeVisible()
          expect(api.writes).toHaveLength(writesBefore)
          expect(api.requests.slice(before).filter((entry) => entry.method !== 'GET')).toEqual([])
          api.allow(action)
          const confirm = dialog.getByRole('button', { name: action === 'stop' ? '确认停止' : '确认重启', exact: true })
          await confirm.focus(); await page.keyboard.press('Enter')
        }
        await expect(confirmation(page)).toHaveCount(0)
        const success = page.locator('.el-message--info')
        await expect(success).toContainText('容器操作任务已接收，尚未确认完成')
        await expect(page.locator('.el-message--success')).toHaveCount(0)
        await expect(success).not.toContainText(/健康|可用|部署成功/)
        await expect(page.locator('.action-error, .project-action-errors')).toHaveCount(0)
        await expect.poll(() => api.requests.slice(before).filter((entry) => entry.method === 'GET' && entry.path === '/api/v1/docker/inventory').length).toBe(1)
        expect(api.writes).toHaveLength(writesBefore + 1)
        expect(api.servedReceipts[writesBefore]).toEqual(record.receipt)
        await expect(actionButton(page, action === 'stop' ? 'start' : 'stop')).toBeEnabled()
        await page.clock.fastForward(6000)
        await expect(page.locator('.el-message')).toHaveCount(0)
        record.requests = structuredClone(api.requests.slice(before))
        expect(record.requests.filter((entry) => entry.method !== 'GET')).toEqual([api.writes[writesBefore]])
        const writeIndex = record.requests.findIndex((entry) => entry.method === 'POST')
        expect(writeIndex).toBeGreaterThanOrEqual(0)
        expect(record.requests.slice(writeIndex + 1)).toEqual([{ method: 'GET', path: `/api/v1/jobs/keyboard-${action}`, body: null }, { method: 'GET', path: '/api/v1/docker/inventory', body: null }])
        expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
        record.completed = true
      } finally { record.requests = structuredClone(api.requests.slice(before)) }
    }
  })
}
