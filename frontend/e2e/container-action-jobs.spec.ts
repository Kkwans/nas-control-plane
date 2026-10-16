import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { summary, overviewDocker } from './mockApi'
import { waitForPageReady } from './pageReadiness'

// 仅 CI 静态 HTTP fixture；不证明真实 Docker/NAS、持久化、重启恢复或跨客户端幂等。
// 制品规则：scripts/ci/partition_ui_artifacts.py 只收 ui-captures/*.png 与 *-capture-state.json。
test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const containerId = 'a'.repeat(64), projectId = 'fixture-action-jobs', name = 'fixture-action-jobs-web'
const stamp = '2026-10-16T01:00:00Z'
type Action = 'start' | 'stop' | 'restart'
type RequestRecord = { stage: string; method: string; target: string; body: string | null }
const receipt = (action: Action = 'restart') => ({ id: `fixture-${action}-job`, type: `docker-container-${action}`, reference: containerId, cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} })
const inventory = {
  collectedAt: stamp,
  engine: { ...overviewDocker.engine, containers: 1, containersRunning: 1, containersStopped: 0 },
  projects: [{ id: projectId, name, kind: 'standalone', workingDirectory: '', configFiles: [], containerCount: 1, runningCount: 1, state: 'running' }],
  containers: [{ ...overviewDocker.containers[0]!, id: containerId, name, projectId, projectName: name, state: 'running', status: 'Up', ports: [] }],
}
function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done }); return { promise, resolve } }
async function fixture(page: Page, origin: string) {
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  // 固定 Date 而非暂停时钟；axe、动画及普通异步计时器照常运行。
  await page.clock.setFixedTime(new Date(stamp))
  await page.addInitScript(() => {
    const host = window as typeof window & { __actionJobsPolls: number; __actionJobsStreams: string[] }
    host.__actionJobsPolls = 0; host.__actionJobsStreams = []
    class SilentSource extends EventTarget {
      static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2
      readyState = 1; onopen: (() => void) | null = null; onerror: (() => void) | null = null
      constructor(readonly url: string) {
        super(); host.__actionJobsStreams.push(url)
        const endpoint = new URL(url, location.origin)
        if (endpoint.origin !== location.origin || !['/api/v1/system/events?interval=5&scope=docker', '/api/v1/system/events?interval=5&scope=summary', '/api/v1/system/events?interval=5&scope=docker&scope=summary'].includes(endpoint.pathname + endpoint.search)) throw new Error(`Fixture refused EventSource: ${url}`)
        queueMicrotask(() => { this.onopen?.(); this.dispatchEvent(new Event('open')) })
      }
      close() { this.readyState = 2 }
    }
    window.EventSource = SilentSource as unknown as typeof EventSource
    const original = window.setTimeout.bind(window)
    window.setTimeout = ((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      // 只隔离 useTaskCenter 的成功刷新 15 秒 timer；本专项不验证轮询。
      if (delay === 15_000) return -(++host.__actionJobsPolls)
      return original(handler, delay, ...args)
    }) as typeof window.setTimeout
  })
  const requests: RequestRecord[] = [], writes: RequestRecord[] = [], denied: string[] = [], errors: string[] = []
  const responses: Array<{ stage: string; target: string; status: number | 'aborted'; payload: unknown }> = []
  const captures: Array<{ file: string; sha256: string }> = [], checkpoints: Array<{ stage: string; requests: RequestRecord[]; writes: number }> = []
  const state = { stage: 'bootstrap', inventory: structuredClone(inventory), inventoryFails: false, jobsFail: false, jobStatus: 200, job: receipt() as Record<string, unknown>, jobs: [] as Record<string, unknown>[] }
  let allowance: { action: Action; mode: 'accepted' | 'lost' | 'held' } | undefined
  let permittedWrites = 0, inventoryRevision = 0
  const held = { received: deferred(), release: deferred(), delivered: deferred() }
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs', { jobs: [] }],
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  reads.set('/api/v1/job-history?limit=50', { source: 'persisted', items: [{ id: 'history-only-completed', type: 'docker-container-restart', reference: containerId, status: 'completed', message: '仅最后保存的历史', createdAt: stamp, updatedAt: stamp, completedAt: stamp }] })
  page.on('pageerror', error => errors.push(error.message))
  await page.routeWebSocket('**', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), target = url.pathname + url.search
    const entry = { stage: state.stage, method: request.method(), target, body: request.postData() }
    if (url.pathname.startsWith('/api/')) requests.push(entry)
    if (request.method() !== 'GET') writes.push(entry)
    const deny = () => { denied.push(`${entry.method} ${url.href}`); return route.abort('blockedbyclient') }
    const reply = (payload: unknown, status = 200) => { responses.push({ stage: state.stage, target, status, payload: structuredClone(payload) }); return route.fulfill({ status, json: payload }) }
    if (url.origin !== origin) return deny()
    if (request.method() === 'POST' && allowance && target === `/api/v1/docker/containers/${containerId}/action-jobs/${allowance.action}`) {
      const allowed = allowance; allowance = undefined
      expect(entry.body).toBeNull()
      if (allowed.mode === 'lost') { responses.push({ stage: state.stage, target, status: 'aborted', payload: null }); return route.abort('failed') }
      if (allowed.mode === 'held') { held.received.resolve(); await held.release.promise }
      await reply(receipt(allowed.action), 202)
      if (allowed.mode === 'held') held.delivered.resolve()
      return
    }
    if (request.method() !== 'GET') return deny()
    const fail = () => reply({ code: 'FIXTURE_READ_UNAVAILABLE', message: '只读 fixture 失败' }, 503)
    if (target === '/api/v1/docker/inventory') {
      // Store 只接受较新快照；每次读回独立版本，不能用相同 collectedAt 假装清单已更新。
      const collectedAt = new Date(Date.parse(stamp) + ++inventoryRevision).toISOString()
      return state.inventoryFails ? fail() : reply({ ...state.inventory, collectedAt })
    }
    if (target === '/api/v1/jobs') return state.jobsFail ? fail() : reply({ jobs: state.jobs })
    if (target === `/api/v1/jobs/${state.job.id}`) return state.jobStatus !== 200 ? reply({ code: 'JOB_NOT_FOUND', message: 'fixture精确任务读取失败' }, state.jobStatus) : reply(state.job)
    if (reads.has(target)) return reply(reads.get(target))
    // 应用入口与构建资源同样精确白名单；不允许未知 API、query、外网或真实 WS。
    if (!url.search && (['/docker', '/favicon.svg'].includes(url.pathname) || /^\/assets\/[A-Za-z0-9_.-]+$/.test(url.pathname))) return route.fallback()
    return deny()
  })
  return { requests, responses, writes, denied, errors, captures, checkpoints, state, held,
    stage(stage: string) { checkpoints.push({ stage: state.stage, requests: structuredClone(requests), writes: writes.length }); state.stage = stage },
    allow(action: Action, mode: 'accepted' | 'lost' | 'held' = 'accepted') { expect(allowance).toBeUndefined(); allowance = { action, mode }; permittedWrites++; state.job = receipt(action) },
    assert() { expect.soft(writes).toHaveLength(permittedWrites); expect.soft(allowance).toBeUndefined(); expect.soft(denied).toEqual([]); expect.soft(errors).toEqual([]) },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
const hash = (data: string | Buffer) => createHash('sha256').update(data).digest('hex')
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/playwright.config.ts', 'scripts/ci/partition_ui_artifacts.py', '.github/workflows/ci.yml']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), trackedDiffSha256: hash(git(['diff', 'HEAD', '--binary', '--', 'frontend'])), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async file => [file, hash(await readFile(resolve(root, file)))]))) }
}
const panel = (page: Page) => page.getByRole('region', { name: '单容器操作防重复保护', exact: true })
const dialog = (page: Page) => page.locator('.ncp-container-action-confirmation')
const check = (page: Page) => panel(page).getByRole('button', { name: '只读核对任务与状态', exact: true })
const release = (page: Page) => panel(page).getByRole('button', { name: '解除本页防重复保护', exact: true })
const actionButton = (page: Page, action: Action) => {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  return page.viewportSize()!.width === 390 ? page.locator('.container-mobile-list').getByRole('button', { name: label, exact: true }) : page.getByRole('button', { name: `${label}容器`, exact: true }).filter({ visible: true })
}
async function navigate(page: Page, name: 'Docker' | '站点管理') {
  if (page.viewportSize()!.width === 390) await page.getByRole('button', { name: '打开主菜单', exact: true }).click()
  await page.locator('.app-sidebar').getByRole('link', { name, exact: true }).click()
  await expect(page).toHaveURL(name === 'Docker' ? /\/docker$/ : /\/sites$/)
  await expect(page.locator('#app-main')).toBeFocused()
  if (name === 'Docker') await page.getByRole('button', { name: '容器', exact: true }).click()
}
async function open(page: Page) {
  await page.goto('/docker')
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '容器', exact: true }).click()
  await expect(actionButton(page, 'restart')).toBeEnabled()
}
async function submit(page: Page, api: Fixture, action: Action, mode: 'accepted' | 'lost' | 'held' = 'accepted') {
  const before = api.writes.length
  await actionButton(page, action).focus()
  if (action === 'start') api.allow(action, mode)
  await page.keyboard.press('Enter')
  if (action !== 'start') {
    await expect(dialog(page)).toContainText(containerId)
    await expect(dialog(page)).toContainText('接收不代表完成，任务不能取消')
    expect(api.writes).toHaveLength(before)
    api.allow(action, mode)
    await dialog(page).getByRole('button', { name: action === 'stop' ? '确认停止' : '确认重启', exact: true }).click()
  }
  await expect(dialog(page)).toHaveCount(0)
  await expect.poll(() => api.writes.length).toBe(before + 1)
}
async function refresh(page: Page, api: Fixture) {
  const before = api.requests.filter(row => row.target === '/api/v1/docker/inventory').length
  await page.getByRole('button', { name: '立即刷新数据', exact: true }).click()
  await expect.poll(() => api.requests.filter(row => row.target === '/api/v1/docker/inventory').length).toBeGreaterThan(before)
  await expect(page.getByRole('button', { name: '立即刷新数据', exact: true })).toBeEnabled()
}
async function readonlyCheck(page: Page) {
  await check(page).click()
  await expect(check(page)).toBeEnabled()
  await expect(panel(page).getByRole('status')).toContainText(/已只读核对任务|任务读取失败/)
}
async function measure(page: Page, selector: string) {
  return page.evaluate(selector => {
    const root = document.querySelector<HTMLElement>(selector)!
    const fields = [...root.querySelectorAll<HTMLElement>('h2,strong,p,.el-message-box__title,.el-message-box__message,button')]
    return {
      viewport: { width: innerWidth, height: innerHeight }, root: { width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth },
      text: root.textContent, scroll: { x: scrollX, y: scrollY }, focus: { tag: document.activeElement?.tagName, text: document.activeElement?.textContent, label: document.activeElement?.getAttribute('aria-label') },
      timerIsolation: { fixedDateOnly: true, suppressedTaskPollDelayMs: 15000, suppressedCount: (window as typeof window & { __actionJobsPolls: number }).__actionJobsPolls, realPollingVerified: false },
      mockedStreams: (window as typeof window & { __actionJobsStreams: string[] }).__actionJobsStreams,
      fields: fields.map(element => {
        const rect = element.getBoundingClientRect(), style = getComputedStyle(element)
        const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT), lines: DOMRect[] = []
        while (walker.nextNode()) if (walker.currentNode.textContent?.trim()) { const range = document.createRange(); range.selectNodeContents(walker.currentNode); lines.push(...range.getClientRects()) }
        const ancestors = []
        for (let parent = element.parentElement; parent; parent = parent.parentElement) {
          const css = getComputedStyle(parent)
          if (css.overflowX !== 'visible' || css.overflowY !== 'visible') ancestors.push({ bounds: parent.getBoundingClientRect().toJSON(), x: css.overflowX, y: css.overflowY })
        }
        const points = [[rect.left + 5, rect.top + 5], [rect.right - 5, rect.top + 5], [rect.left + 5, rect.bottom - 5], [rect.right - 5, rect.bottom - 5], [rect.left + rect.width / 2, rect.top + rect.height / 2]]
        return { tag: element.tagName, text: element.textContent, bounds: rect.toJSON(), lines: lines.filter(line => line.width && line.height).map(line => line.toJSON()), ancestors,
          clientWidth: element.clientWidth, scrollWidth: element.scrollWidth, clientHeight: element.clientHeight, scrollHeight: element.scrollHeight,
          focusVisible: element.matches(':focus-visible'), outlineWidth: style.outlineWidth, outlineStyle: style.outlineStyle, outlineColor: style.outlineColor,
          disabled: element.matches(':disabled'), hits: points.map(([x, y]) => { const target = document.elementFromPoint(x!, y!); return Boolean(target && (target === element || element.contains(target))) }) }
      }).filter(field => field.bounds.width > 0 && field.bounds.height > 0),
    }
  }, selector)
}
async function capture(page: Page, info: TestInfo, api: Fixture, state: string, modal = false, selectorOverride?: string) {
  await expect(page.locator('.el-message')).toHaveCount(0)
  await waitForPageReady(page); await page.evaluate(() => document.fonts.ready)
  const selector = selectorOverride ?? (modal ? '.ncp-container-action-confirmation' : '.action-protection')
  // 解除后的旧列表沿用桌面40px、移动42px基线；本次新增面板/风险框必须44px。
  const minimumTarget = selectorOverride ? (page.viewportSize()!.width === 1440 ? 40 : 42) : 44
  const target = page.locator(selector)
  await target.scrollIntoViewIfNeeded()
  // 对可滚动内容逐个实际滚动到按钮；不把正常纵向滚动误判为裁剪。
  const scrollPositions = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>('*')].filter(node => node.scrollTop || node.scrollLeft).map(node => ({ node: node.tagName, top: node.scrollTop, left: node.scrollLeft })))
  const controlReachability = []
  for (const button of await target.getByRole('button').all()) {
    await button.scrollIntoViewIfNeeded()
    controlReachability.push(await button.evaluate(element => {
      const box = element.getBoundingClientRect()
      const points = [[box.left + 5, box.top + 5], [box.right - 5, box.top + 5], [box.left + 5, box.bottom - 5], [box.right - 5, box.bottom - 5], [box.left + box.width / 2, box.top + box.height / 2]]
      return { text: element.textContent, bounds: box.toJSON(), hits: points.map(([x, y]) => { const hit = document.elementFromPoint(x!, y!); return Boolean(hit && (hit === element || element.contains(hit))) }) }
    }))
  }
  await target.scrollIntoViewIfNeeded()
  const beforeCapture = await measure(page, selector), requestsBefore = structuredClone(api.requests)
  const prefix = `container-action-jobs-${state}-${page.viewportSize()!.width}`
  const directory = info.outputPath('ui-captures'); await mkdir(directory, { recursive: true })
  const png = `${directory}/${prefix}.png`, screenshot = await page.screenshot({ path: png, fullPage: !modal, animations: 'disabled' })
  const afterCapture = await measure(page, selector), requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await measure(page, selector)
  const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, gapBasis: 'source-inferred; no local browser run or pre-fix failure claimed', state, testTitle: info.title, project: info.project.name, retry: info.retry, scope: selector, touchTargetBaseline: { pixels: minimumTarget, source: selectorOverride ? 'existing container action controls: desktop40/mobile42; not new protection UI' : 'new protection panel/dialog:44' }, fullPage: !modal, scrollPositions, controlReachability, screenshot: `${prefix}.png`, screenshotSha256: hash(screenshot), beforeCapture, afterCapture, afterAxe, requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), responses: structuredClone(api.responses), writes: api.writes, denied: api.denied, errors: api.errors }
  api.captures.push({ file: `${prefix}.png`, sha256: hash(screenshot) }); await info.attach(prefix, { path: png, contentType: 'image/png' })
  for (const [suffix, content] of [['capture-state', metadata], ['axe-capture-state', { ...metadata, axe }]] as const) {
    const file = `${prefix}-${suffix}.json`, path = `${directory}/${file}`, data = JSON.stringify(content, null, 2)
    await writeFile(path, data); api.captures.push({ file, sha256: hash(data) }); await info.attach(file, { path, contentType: 'application/json' })
  }
  // 完整 axe（含 incomplete）先保存，再断言；不能删减、过滤或豁免。
  expect.soft(axe.violations, JSON.stringify(axe.violations)).toEqual([])
  expect.soft(api.requests).toEqual(requestsBefore)
  for (const control of controlReachability) {
    expect.soft(control.hits, control.text ?? '').toEqual([true, true, true, true, true])
    expect.soft(control.bounds.width).toBeGreaterThanOrEqual(minimumTarget); expect.soft(control.bounds.height).toBeGreaterThanOrEqual(minimumTarget)
  }
  for (const sample of [beforeCapture, afterCapture, afterAxe]) {
    expect.soft(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.width + 1)
    expect.soft(sample.text).toEqual(beforeCapture.text); expect.soft(sample.focus).toEqual(beforeCapture.focus); expect.soft(sample.scroll).toEqual(beforeCapture.scroll)
    for (const field of sample.fields) {
      if (field.clientWidth) expect.soft(field.scrollWidth, field.text ?? '').toBeLessThanOrEqual(field.clientWidth + 1)
      if (field.clientHeight) expect.soft(field.scrollHeight, field.text ?? '').toBeLessThanOrEqual(field.clientHeight + 1)
      for (const line of field.lines) {
        expect.soft(line.left).toBeGreaterThanOrEqual(Math.max(0, field.bounds.left) - 1)
        expect.soft(line.right).toBeLessThanOrEqual(Math.min(sample.viewport.width, field.bounds.right) + 1)
        expect.soft(line.top).toBeGreaterThanOrEqual(field.bounds.top - 1)
        expect.soft(line.bottom).toBeLessThanOrEqual(field.bounds.bottom + 1)
        for (const parent of field.ancestors) {
          if (parent.x !== 'visible') { expect.soft(line.left).toBeGreaterThanOrEqual(parent.bounds.left - 1); expect.soft(line.right).toBeLessThanOrEqual(parent.bounds.right + 1) }
          if (['hidden', 'clip'].includes(parent.y)) { expect.soft(line.top).toBeGreaterThanOrEqual(parent.bounds.top - 1); expect.soft(line.bottom).toBeLessThanOrEqual(parent.bounds.bottom + 1) }
        }
      }
      if (field.tag === 'BUTTON') {
        expect.soft(field.bounds.width).toBeGreaterThanOrEqual(minimumTarget); expect.soft(field.bounds.height).toBeGreaterThanOrEqual(minimumTarget)
        // disabled 控件同样不能被其他层遮挡；角落内缩5px避开圆角外部。
        if (field.bounds.top >= 0 && field.bounds.bottom <= sample.viewport.height) expect.soft(field.hits, field.text ?? '').toEqual([true, true, true, true, true])
      }
    }
  }
  expect.soft(api.denied).toEqual([]); expect.soft(api.errors).toEqual([])
}
async function risk(page: Page, info: TestInfo, api: Fixture, state: string, confirm: boolean) {
  const writes = api.writes.length, trigger = release(page)
  await trigger.focus(); await page.keyboard.press('Enter')
  await expect(dialog(page)).toContainText('之前操作可能仍已执行')
  await expect(dialog(page)).toContainText('之后再次操作可能重复执行')
  const focusCycle = []
  for (const key of ['Tab', 'Tab', 'Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab']) {
    await page.keyboard.press(key)
    const focus = await dialog(page).evaluate(element => { const active = document.activeElement!, css = getComputedStyle(active); return { contained: element.contains(active), visible: active.matches(':focus-visible'), width: parseFloat(css.outlineWidth), style: css.outlineStyle, color: css.outlineColor, text: active.textContent } })
    focusCycle.push(focus)
    expect.soft(focus.contained).toBe(true); expect.soft(focus.visible).toBe(true); expect.soft(focus.width).toBeGreaterThanOrEqual(2)
    expect.soft(focus.style).toBe('solid'); expect.soft(focus.color).not.toMatch(/^(transparent|rgba\(0, 0, 0, 0\))$/)
  }
  const path = info.outputPath('ui-captures', `${state}-focus-cycle-capture-state.json`)
  await mkdir(dirname(path), { recursive: true }); await writeFile(path, JSON.stringify({ state, focusCycle }, null, 2))
  api.captures.push({ file: `${state}-focus-cycle-capture-state.json`, sha256: hash(await readFile(path)) })
  await capture(page, info, api, `${state}-risk-confirmation`, true)
  await dialog(page).getByRole('button', { name: confirm ? '确认风险并解除' : '保留保护', exact: true }).focus()
  await page.keyboard.press('Space'); await expect(dialog(page)).toHaveCount(0)
  if (confirm) {
    await expect(panel(page)).toHaveCount(0)
    await capture(page, info, api, `${state}-released`, false, page.viewportSize()!.width === 390 ? '.container-mobile-list .mobile-actions' : '.container-panel .container-actions')
  }
  else { await expect(trigger).toBeFocused(); await expect(panel(page)).toBeVisible() }
  expect(api.writes).toHaveLength(writes)
}
async function finish(page: Page, info: TestInfo, api: Fixture) {
  api.stage('finished')
  const path = info.outputPath('ui-captures', 'container-action-jobs-final-request-ledger-capture-state.json')
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, artifacts: api.captures, checkpoints: api.checkpoints, finalStage: api.state.stage, requests: api.requests, requestsSha256: hash(JSON.stringify(api.requests)), responses: structuredClone(api.responses), writes: api.writes, denied: api.denied, errors: api.errors, timerIsolation: await page.evaluate(() => ({ fixedDateOnly: true, suppressedTaskPollDelayMs: 15000, count: (window as typeof window & { __actionJobsPolls: number }).__actionJobsPolls })).catch(() => null) }, null, 2))
  await info.attach('container-action-jobs-final-request-ledger', { path, contentType: 'application/json' }); api.assert()
}

// 四条独立旅程 × 两个断点；每例唯一提交，终态例第二次为明确的新意图。
for (const width of [390, 1440]) {
  test(`${width}px queued、running、pending 锁定三动作和项目批量，completed 才释放`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000)
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); api.stage('restart-confirmation')
      await actionButton(page, 'restart').focus(); await page.keyboard.press('Enter')
      await expect(dialog(page)).toContainText(containerId)
      await expect(dialog(page)).toContainText('接收不代表完成，任务不能取消')
      await capture(page, info, api, 'initial-confirmation', true)
      expect(api.writes).toHaveLength(0)
      api.allow('restart'); await dialog(page).getByRole('button', { name: '确认重启', exact: true }).click()
      await expect(panel(page)).toContainText('已接收，排队中'); await expect(check(page)).toBeEnabled()
      await expect(panel(page)).toContainText('任务 ID：fixture-restart-job')
      await expect(page.locator('.el-message--success')).toHaveCount(0); await expect(release(page)).toBeDisabled()
      api.stage('queued'); await capture(page, info, api, 'queued')
      api.stage('known-exact-job-404'); api.state.jobStatus = 404; await readonlyCheck(page)
      await expect(panel(page)).toContainText('任务读取失败，不能据此判断未接收或未执行')
      await expect(panel(page)).toContainText('任务 ID：fixture-restart-job'); await expect(release(page)).toBeDisabled()
      await capture(page, info, api, 'known-job-404-keeps-lock'); api.state.jobStatus = 200
      for (const [phase, snapshot] of [
        ['running', { ...receipt(), status: 'running' }],
        ['pending', { ...receipt(), status: 'running', message: '执行已返回，结果等待保存', persistenceStatus: 'pending' }],
      ] as const) {
        api.stage(phase); api.state.job = snapshot; await readonlyCheck(page)
        await expect(panel(page)).toContainText(phase === 'running' ? '执行中' : '结果保存中')
        await expect(release(page)).toBeDisabled()
        await expect(actionButton(page, 'stop')).toBeDisabled(); await expect(actionButton(page, 'restart')).toBeDisabled()
        await capture(page, info, api, phase)
      }
      api.stage('stopped-inventory-still-locked')
      api.state.inventory.containers[0]!.state = 'exited'; api.state.inventory.containers[0]!.status = 'Exited (0)'
      api.state.inventory.projects[0]!.state = 'stopped'; api.state.inventory.projects[0]!.runningCount = 0
      api.state.inventory.engine.containersRunning = 0; api.state.inventory.engine.containersStopped = 1
      await refresh(page, api)
      await expect(actionButton(page, 'start')).toBeDisabled(); await expect(actionButton(page, 'restart')).toBeDisabled()
      await capture(page, info, api, 'changed-inventory-lock')
      await page.getByRole('button', { name: '项目', exact: true }).click()
      const projects = page.locator(width === 390 ? '.docker-mobile-list' : '.docker-table')
      for (const label of ['启动', '停止', '重启']) await expect(projects.getByRole('button', { name: width === 390 ? label : `${label}项目 ${name}`, exact: true })).toBeDisabled()
      await projects.getByRole('button', { name: `查看 Docker 项目 ${name}`, exact: true }).click()
      const drawer = page.locator('.project-drawer')
      await expect(drawer).toBeVisible()
      for (const label of ['启动', '停止', '重启']) await expect(drawer.locator('.project-lifecycle__actions').getByRole('button', { name: label, exact: true })).toBeDisabled()
      await expect(drawer.getByRole('button', { name: `启动 ${name}`, exact: true })).toBeDisabled()
      await expect(drawer.getByRole('button', { name: `重启 ${name}`, exact: true })).toBeDisabled()
      await page.keyboard.press('Escape'); await expect(drawer).not.toBeVisible(); expect(api.writes).toHaveLength(1)
      api.stage('strict-completed-releases'); api.state.job = { ...receipt(), status: 'completed', progress: 100, completedAt: stamp }
      await check(page).click(); await expect(panel(page)).toHaveCount(0)
      await page.getByRole('button', { name: '容器', exact: true }).click(); await expect(actionButton(page, 'start')).toBeEnabled()
      await capture(page, info, api, 'completed-released', false, width === 390 ? '.container-mobile-list .mobile-actions' : '.container-panel .container-actions')
      expect(api.writes).toHaveLength(1)
    } finally { await finish(page, info, api) }
  })

  test(`${width}px 悬挂 POST 跨路由后晚 202 绑定原会话，目标消失仍可核对`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); api.stage('submit-held'); await submit(page, api, 'stop', 'held'); await api.held.received.promise
      await expect(panel(page)).toContainText('正在提交，尚未确认接收')
      await expect(check(page)).toBeDisabled(); await expect(release(page)).toBeDisabled()
      await capture(page, info, api, 'submitting')
      api.stage('away-before-ack'); await navigate(page, '站点管理')
      api.held.release.resolve(); await api.held.delivered.promise; await expect(page.locator('.el-message')).toHaveCount(0)
      api.stage('return-after-late-ack'); await navigate(page, 'Docker')
      await expect(panel(page)).toContainText('任务 ID：fixture-stop-job'); await expect(panel(page)).toContainText('已接收，排队中')
      await expect(actionButton(page, 'stop')).toBeDisabled(); await expect(actionButton(page, 'restart')).toBeDisabled()
      await capture(page, info, api, 'late-202-bound')
      api.stage('target-disappears'); api.state.inventory.containers = []; api.state.inventory.projects = []
      api.state.inventory.engine.containers = 0; api.state.inventory.engine.containersRunning = 0
      await refresh(page, api); await expect(panel(page)).toContainText(containerId)
      const before = api.requests.filter(row => row.target === '/api/v1/jobs/fixture-stop-job').length
      await readonlyCheck(page)
      expect(api.requests.filter(row => row.target === '/api/v1/jobs/fixture-stop-job').length).toBeGreaterThan(before)
      await expect(release(page)).toBeDisabled(); await capture(page, info, api, 'missing-target-checkable')
      expect(api.writes).toHaveLength(1)
    } finally { api.held.release.resolve(); await finish(page, info, api) }
  })

  test(`${width}px failed/interrupted 必须核对并确认风险，取消保留、解除零 POST`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000)
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page)
      for (const [status, action, errorCode] of [['failed', 'stop', 'DOCKER_CONTAINER_STATE_MISMATCH'], ['interrupted', 'restart', 'JOB_EXECUTION_INTERRUPTED']] as const) {
        api.stage(`${status}-new-independent-intent`); await submit(page, api, action)
        await expect(panel(page)).toContainText('已接收，排队中'); await expect(check(page)).toBeEnabled()
        api.state.job = { ...receipt(action), status, completedAt: stamp, errorCode, error: '本次结果需核对' }; api.state.inventoryFails = true
        api.stage(`${status}-inventory-read-failure`); await readonlyCheck(page)
        await expect(panel(page)).toContainText(errorCode); await expect(release(page)).toBeDisabled()
        await capture(page, info, api, `${status}-inventory-failed`)
        api.state.inventoryFails = false; api.stage(`${status}-readonly-success`); await readonlyCheck(page); await expect(release(page)).toBeEnabled()
        await capture(page, info, api, `${status}-checked`)
        api.stage(`${status}-cancel-risk`); await risk(page, info, api, `${status}-keep`, false); await expect(actionButton(page, 'restart')).toBeDisabled()
        api.stage(`${status}-confirm-local-release`); await risk(page, info, api, `${status}-release`, true); await expect(actionButton(page, 'restart')).toBeEnabled()
      }
      expect(api.writes.map(row => row.target)).toEqual([`/api/v1/docker/containers/${containerId}/action-jobs/stop`, `/api/v1/docker/containers/${containerId}/action-jobs/restart`])
    } finally { await finish(page, info, api) }
  })

  test(`${width}px 丢 202 未知锁跨刷新路由保留，活动证据阻止解除且历史不能认领`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000)
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); api.stage('lost-202'); await submit(page, api, 'restart', 'lost')
      await expect(panel(page)).toContainText('接收未知，勿重复提交'); await expect(release(page)).toBeDisabled(); await expect(panel(page)).not.toContainText('任务 ID：')
      await capture(page, info, api, 'unknown-admission')
      api.stage('unknown-data-refresh'); await refresh(page, api)
      await expect(actionButton(page, 'stop')).toBeDisabled(); await expect(actionButton(page, 'restart')).toBeDisabled()
      api.stage('unknown-route-roundtrip'); await navigate(page, '站点管理'); await navigate(page, 'Docker')
      await expect(panel(page)).toContainText('接收未知，勿重复提交'); await expect(panel(page)).not.toContainText('任务 ID：'); await expect(actionButton(page, 'restart')).toBeDisabled()
      await capture(page, info, api, 'unknown-after-route-refresh')
      api.stage('unknown-read-failure'); api.state.jobsFail = true; api.state.inventoryFails = true; await readonlyCheck(page)
      await expect(panel(page)).toContainText('任务读取失败，不能据此判断未接收或未执行'); await expect(panel(page)).toContainText('容器清单刷新失败'); await expect(release(page)).toBeEnabled()
      await capture(page, info, api, 'unknown-read-failure'); await risk(page, info, api, 'unknown-failed-check-keep', false)
      api.stage('same-target-active'); api.state.jobsFail = false; api.state.inventoryFails = false
      api.state.jobs = [{ ...receipt('stop'), id: 'other-active-job', status: 'running' }]; await readonlyCheck(page)
      await expect(panel(page)).toContainText('已观察到此容器仍有活动任务，暂不能解除保护'); await expect(panel(page)).toContainText('不会自动绑定为本次提交')
      await expect(panel(page)).not.toContainText('任务 ID：'); await expect(release(page)).toBeDisabled(); await capture(page, info, api, 'observed-active-unbound')
      api.stage('active-evidence-sticky-on-read-failure'); api.state.jobsFail = true; await readonlyCheck(page); await expect(release(page)).toBeDisabled()
      await expect(panel(page)).toContainText('已观察到此容器仍有活动任务'); await capture(page, info, api, 'active-evidence-read-failure')
      api.stage('saved-history-is-not-receipt'); api.state.jobsFail = false; api.state.jobs = []
      await page.getByRole('button', { name: '任务', exact: true }).click()
      const tasks = page.locator('.task-center-drawer')
      await tasks.getByRole('button', { name: '已保存历史', exact: true }).click(); await expect(tasks).toContainText('仅最后保存的历史')
      await tasks.getByRole('button', { name: '关闭已保存历史', exact: true }).click(); await expect(tasks).not.toBeVisible()
      await expect(release(page)).toBeDisabled(); await expect(panel(page)).not.toContainText('任务 ID：')
      expect(api.requests.some(row => row.target === '/api/v1/jobs/history-only-completed')).toBe(false)
      api.stage('unknown-final-readonly-local-release'); await readonlyCheck(page); await expect(release(page)).toBeEnabled(); await expect(panel(page)).not.toContainText('任务 ID：')
      await risk(page, info, api, 'unknown-explicit-local-release', true); await expect(actionButton(page, 'restart')).toBeEnabled(); expect(api.writes).toHaveLength(1)
    } finally { await finish(page, info, api) }
  })
}
