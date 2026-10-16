import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo, type Locator } from '@playwright/test'
import { summary, overviewDocker } from './mockApi'
import { waitForPageReady } from './pageReadiness'

// 仅 CI 静态 HTTP fixture；不证明真实 Docker/NAS、持久化、重启恢复或跨客户端幂等。
// 制品规则：scripts/ci/partition_ui_artifacts.py 只收 ui-captures/*.png 与 *-capture-state.json。
test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const containerId = 'a'.repeat(64), projectId = 'compose:fixture-jobs', name = 'fixture-compose-jobs'
const workingDirectory = '/fixture/compose-original', configFiles = ['/fixture/compose-original/z-base.yaml', '/fixture/compose-original/a-override.yaml']
const standaloneId = 'd'.repeat(64), standaloneName = 'fixture-standalone'
const ids = [containerId, 'b'.repeat(64), 'c'.repeat(64)]
const stamp = '2026-10-16T01:00:00Z'
type Action = 'start' | 'stop' | 'restart'
type NoticeObservation = { event: string; dateNow: number; performanceNow: number; detail: unknown }
type NoticeWindow = typeof window & { __composeJobsNoticeEvents: NoticeObservation[] }
type RequestRecord = { stage: string; method: string; target: string; body: string | null }
const standaloneJob = { id: 'fixture-standalone-job', type: 'docker-project-restart', reference: 'standalone', cancellable: false, status: 'queued', artifactState: 'unknown', message: '', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const receipt = (action: Action = 'restart') => ({ id: `fixture-${action}-job`, type: `compose-${action}`, reference: projectId, cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} })
const inventory = {
  collectedAt: stamp,
  engine: { ...overviewDocker.engine, containers: 3, containersRunning: 2, containersStopped: 1 },
  projects: [{ id: projectId, name, kind: 'compose', workingDirectory, configFiles, containerCount: 3, runningCount: 2, state: 'degraded' }],
  containers: ids.map((id, index) => ({ ...overviewDocker.containers[0]!, id, name: `${name}-${index}`, projectId, projectName: name, state: index === 2 ? 'exited' : 'running', status: index === 2 ? 'Exited (0)' : 'Up', ports: [] })),
}
function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done }); return { promise, resolve } }
async function fixture(page: Page, origin: string) {
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  const assets = new Set((await readdir(new URL('../dist/assets/', import.meta.url))).map(file => `/assets/${file}`))
  // 保持 Date 流动；固定 Date 会令 Vue capture/bubble invoker时间戳相同而跳过 RouterLink。
  // 安装可观测时钟但从不 pause；axe、动画及普通异步计时器照常运行。
  await page.clock.install({ time: new Date(stamp) })
  await page.addInitScript(() => {
    const host = window as typeof window & { __composeJobsPolls: number; __composeJobsStreams: string[]; __composeJobsDocument: string }
    host.__composeJobsPolls = 0; host.__composeJobsStreams = []; host.__composeJobsDocument = crypto.randomUUID()
    class SilentSource extends EventTarget {
      static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2
      readyState = 1; onopen: (() => void) | null = null; onerror: (() => void) | null = null
      constructor(readonly url: string) {
        super(); host.__composeJobsStreams.push(url)
        const endpoint = new URL(url, location.origin)
        if (endpoint.origin !== location.origin || !['/api/v1/system/events?interval=5&scope=docker', '/api/v1/system/events?interval=5&scope=summary', '/api/v1/system/events?interval=5&scope=docker&scope=summary'].includes(endpoint.pathname + endpoint.search)) throw new Error(`Fixture refused EventSource: ${url}`)
        queueMicrotask(() => { this.onopen?.(); this.dispatchEvent(new Event('open')) })
      }
      close() { this.readyState = 2 }
    }
    window.EventSource = SilentSource as unknown as typeof EventSource
    const noticeHost = window as NoticeWindow
    noticeHost.__composeJobsNoticeEvents = []
    const record = (event: string, detail: unknown) => {
      if (noticeHost.__composeJobsNoticeEvents.length < 200) noticeHost.__composeJobsNoticeEvents.push({ event, dateNow: Date.now(), performanceNow: performance.now(), detail })
    }
    const noticeTimers = new Set<number>()
    for (const event of ['mouseover', 'mouseout', 'transitionrun', 'transitionend', 'transitioncancel']) {
      document.addEventListener(event, e => {
        const notice = e.target instanceof Element ? e.target.closest('.el-message') : null
        if (notice) record(event, { text: notice.textContent, classes: notice.className, hovered: notice.matches(':hover') })
      }, true)
    }
    const original = window.setTimeout.bind(window)
    const originalClear = window.clearTimeout.bind(window)
    window.clearTimeout = (...args: Parameters<typeof window.clearTimeout>) => {
      const [id] = args
      if (typeof id === 'number' && noticeTimers.delete(id)) record('timer-clear', { id })
      return originalClear(...args)
    }
    window.setTimeout = ((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      // 只隔离 useTaskCenter 的成功刷新 15 秒 timer；本专项不验证轮询。
      if (delay === 15_000) return -(++host.__composeJobsPolls)
      if (delay === 3_000 && typeof handler === 'function') {
        const id = original(function (this: Window, ...callbackArgs: unknown[]) {
          noticeTimers.delete(id); record('timer-fire', { id })
          return Reflect.apply(handler, this, callbackArgs)
        }, delay, ...args)
        noticeTimers.add(id); record('timer-schedule', { id, delay })
        return id
      }
      return original(handler, delay, ...args)
    }) as typeof window.setTimeout
  })
  const requests: RequestRecord[] = [], writes: RequestRecord[] = [], denied: string[] = [], errors: string[] = []
  const responses: Array<{ stage: string; target: string; status: number | 'aborted'; payload: unknown }> = []
  const captures: Array<{ file: string; sha256: string }> = [], checkpoints: Array<{ stage: string; requestCount: number; writes: number }> = []
  const state = { stage: 'bootstrap', journeyCompleted: false, inventory: structuredClone(inventory), inventoryFails: false, jobsFail: false, jobStatus: 200, job: receipt() as Record<string, unknown>, jobs: [] as Record<string, unknown>[], authenticated: true }
  const expectedIntent = { workingDirectory, configFiles: [...configFiles] }
  let standaloneAllowed = false
  let allowance: { action: Action; mode: 'accepted' | 'lost' | 'held' } | undefined
  const authAllowance: string[] = []
  let permittedWrites = 0, authWrites = 0, inventoryTimestamp = Date.parse(stamp)
  const held = { received: deferred(), release: deferred(), delivered: deferred() }
  const inventoryHold = { received: deferred(), release: deferred(), delivered: deferred() }
  let holdInventory = false
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs', { jobs: [] }],
    ['/api/v1/jobs?type=docker-image-pull', { jobs: [] }],
    ['/api/v1/docker/images', { collectedAt: stamp, images: [{ id: 'sha256:fixture-image', repoTags: ['fixture:latest'], repoDigests: [], sizeBytes: 1024, createdAt: stamp, containers: 0 }] }],
    ...['docker.projects', 'docker.containers', 'docker.images.local', 'docker.image-downloads'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  reads.set('/api/v1/job-history?limit=50', { source: 'persisted', items: [{ id: 'history-only-completed', type: 'docker-container-restart', reference: containerId, status: 'completed', message: '仅最后保存的历史', createdAt: stamp, updatedAt: stamp, completedAt: stamp }] })
  page.on('pageerror', error => errors.push(error.message))
  await page.routeWebSocket('**', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), target = url.pathname + url.search
    const entry = { stage: state.stage, method: request.method(), target, body: target === '/api/v1/auth/login' ? '[fixture credentials omitted]' : request.postData() }
    requests.push(entry)
    if (request.method() !== 'GET') writes.push(entry)
    const deny = () => { denied.push(`${entry.method} ${url.href}`); return route.abort('blockedbyclient') }
    const reply = (payload: unknown, status = 200) => { responses.push({ stage: state.stage, target, status, payload: structuredClone(payload) }); return route.fulfill({ status, json: payload }) }
    if (url.origin !== origin) return deny()
    if (request.method() === 'POST' && allowance && target === `/api/v1/docker/compose/projects/${encodeURIComponent(projectId)}/action-jobs/${allowance.action}`) {
      const allowed = allowance; allowance = undefined
      expect(JSON.parse(entry.body!)).toEqual(expectedIntent)
      if (allowed.mode === 'lost') { responses.push({ stage: state.stage, target, status: 'aborted', payload: null }); return route.abort('failed') }
      if (allowed.mode === 'held') { held.received.resolve(); await held.release.promise }
      await reply(receipt(allowed.action), 202)
      if (allowed.mode === 'held') held.delivered.resolve()
      return
    }
    if (request.method() === 'POST' && target === '/api/v1/docker/projects/standalone/action-jobs/restart' && standaloneAllowed) {
      standaloneAllowed = false; expect(JSON.parse(entry.body!)).toEqual({ containerIds: [standaloneId] }); return reply(standaloneJob, 202)
    }
    if (target === '/api/v1/auth/logout' && request.method() === 'POST' && authAllowance[0] === 'logout') { expect(request.postData()).toBeNull(); authAllowance.shift(); state.authenticated = false; return route.fulfill({ status: 204 }) }
    if (target === '/api/v1/auth/login' && request.method() === 'POST' && authAllowance[0] === 'login') { expect(request.postDataJSON()).toEqual({ username: 'root', password: 'isolated-fixture-only' }); authAllowance.shift(); state.authenticated = true; return reply({ user: { id: 1, username: 'root', role: 'root' }, expiresAt: '2026-10-17T01:00:00Z' }) }
    if (request.method() !== 'GET') return deny()
    if (target === '/api/v1/auth/status') return reply({ initialized: true, authenticated: state.authenticated, ...(state.authenticated ? { user: { id: 1, username: 'root', role: 'root' } } : {}) })
    if (['start', 'stop', 'restart'].some(action => target === `/api/v1/jobs?type=compose-${action}`)) return state.jobsFail ? reply({ code: 'FIXTURE_READ_UNAVAILABLE' }, 503) : reply({ jobs: state.jobs.filter(job => job.type === url.searchParams.get('type')) })
    const fail = () => reply({ code: 'FIXTURE_READ_UNAVAILABLE', message: '只读 fixture 失败' }, 503)
    if (target === '/api/v1/docker/inventory') {
      const heldRead = holdInventory; holdInventory = false
      if (heldRead) { inventoryHold.received.resolve(); await inventoryHold.release.promise }
      // Store 只接受较新快照；每次读回独立版本，不能用相同 collectedAt 假装清单已更新。
      inventoryTimestamp = Math.max(inventoryTimestamp + 1, await page.evaluate(() => Date.now()))
      const collectedAt = new Date(inventoryTimestamp).toISOString()
      await (state.inventoryFails ? fail() : reply({ ...state.inventory, collectedAt }))
      if (heldRead) inventoryHold.delivered.resolve()
      return
    }
    if (target === '/api/v1/jobs') return state.jobsFail ? fail() : reply({ jobs: state.jobs })
    if (target === '/api/v1/jobs/fixture-standalone-job') return reply(standaloneJob)
    if (['start', 'stop', 'restart'].some(action => target === `/api/v1/jobs/fixture-${action}-job`)) return state.jobStatus !== 200 ? reply({ code: 'JOB_NOT_FOUND', message: 'fixture精确任务读取失败' }, state.jobStatus) : reply(state.job)
    if (reads.has(target)) return reply(reads.get(target))
    // 应用入口与构建资源同样精确白名单；不允许未知 API、query、外网或真实 WS。
    if (!url.search && (['/', '/docker', '/sites', '/login', '/favicon.svg'].includes(url.pathname) || assets.has(url.pathname))) return route.fallback()
    return deny()
  })
  return { requests, responses, writes, denied, errors, captures, checkpoints, state, held,
    inventoryHold,
    holdNextInventory() { expect(holdInventory).toBe(false); holdInventory = true },
    allowStandalone() { expect(standaloneAllowed).toBe(false); standaloneAllowed = true; permittedWrites++ },
    allowSessionSwitch() { expect(authAllowance).toEqual([]); authAllowance.push('logout', 'login'); authWrites += 2 },
    stage(stage: string) { checkpoints.push({ stage: state.stage, requestCount: requests.length, writes: writes.length }); state.stage = stage },
    allow(action: Action, mode: 'accepted' | 'lost' | 'held' = 'accepted') { expect(allowance).toBeUndefined(); allowance = { action, mode }; permittedWrites++; state.job = receipt(action) },
    assert() { expect.soft(writes).toHaveLength(permittedWrites + authWrites); expect.soft(allowance).toBeUndefined(); expect.soft(standaloneAllowed).toBe(false); expect.soft(authAllowance).toEqual([]); expect.soft(denied).toEqual([]); expect.soft(errors).toEqual([]) },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
const hash = (data: string | Buffer) => createHash('sha256').update(data).digest('hex')
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/vite.config.ts', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/playwright.config.ts', 'scripts/ci/partition_ui_artifacts.py', '.github/workflows/ci.yml']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), trackedDiffSha256: hash(git(['diff', 'HEAD', '--binary', '--', 'frontend'])), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async file => [file, hash(await readFile(resolve(root, file)))]))) }
}
const panel = (page: Page) => page.getByRole('region', { name: 'Compose 操作防重复保护', exact: true })
const dialog = (page: Page) => page.locator('.ncp-compose-action-confirmation')
const check = (page: Page) => panel(page).getByRole('button', { name: '只读核对任务与容器清单', exact: true })
const release = (page: Page) => panel(page).getByRole('button', { name: '确认重复执行风险并解除本会话保护', exact: true })
const actionButton = (page: Page, action: Action) => {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  return page.viewportSize()!.width === 390 ? page.locator('.mobile-project:visible').filter({ hasText: name }).getByRole('button', { name: label, exact: true }) : page.getByRole('button', { name: `${label}项目 ${name}`, exact: true })
}
async function navigate(page: Page, name: 'Docker' | '站点管理') {
  const documentId = await page.evaluate(() => (window as typeof window & { __composeJobsDocument: string }).__composeJobsDocument)
  if (page.viewportSize()!.width === 390) await page.getByRole('button', { name: '打开主菜单', exact: true }).click()
  await page.locator('.app-sidebar').getByRole('link', { name, exact: true }).click()
  await expect(page).toHaveURL(name === 'Docker' ? /\/docker$/ : /\/sites$/)
  expect(await page.evaluate(() => (window as typeof window & { __composeJobsDocument: string }).__composeJobsDocument)).toBe(documentId)
  await expect(page.locator('#app-main')).toBeFocused()
  if (name === 'Docker') await page.getByRole('button', { name: '项目', exact: true }).click()
}
async function open(page: Page) {
  await page.goto('/docker')
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '项目', exact: true }).click()
  await expect(actionButton(page, 'restart')).toBeEnabled()
}
async function submit(page: Page, api: Fixture, action: Action, mode: 'accepted' | 'lost' | 'held' = 'accepted') {
  const before = api.writes.length
  await actionButton(page, action).focus()
  if (action === 'start') api.allow(action, mode)
  await page.keyboard.press('Enter')
  if (action !== 'start') {
    await expect(dialog(page)).toContainText(workingDirectory)
    await expect(dialog(page)).toContainText('已接收不代表完成')
    expect(api.writes).toHaveLength(before)
    api.allow(action, mode)
    await tabTo(page, dialog(page).getByRole('button', { name: action === 'stop' ? '确认停止' : '确认重启', exact: true })); await page.keyboard.press('Enter')
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
  await expect(panel(page)).toBeVisible()
}
async function measure(page: Page, selector: string) {
  return page.evaluate(selector => {
    const root = document.querySelector<HTMLElement>(selector)!
    const fields = [...root.querySelectorAll<HTMLElement>('h2,strong,p,li.identity,summary,.el-message-box__title,.el-message-box__message,button')]
    return {
      viewport: { width: innerWidth, height: innerHeight }, root: { width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth },
      text: root.textContent, scroll: { x: scrollX, y: scrollY }, focus: { tag: document.activeElement?.tagName, text: document.activeElement?.textContent, label: document.activeElement?.getAttribute('aria-label') },
      timerIsolation: { clockMode: 'installed-running-never-paused', suppressedTaskPollDelayMs: 15000, suppressedCount: (window as typeof window & { __composeJobsPolls: number }).__composeJobsPolls, realPollingVerified: false },
      mockedStreams: (window as typeof window & { __composeJobsStreams: string[] }).__composeJobsStreams,
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
async function capture(page: Page, info: TestInfo, api: Fixture, state: string, modal = false) {
  // 本例捕获目标操作状态而非分页 hover；释放后布局回缩可能让静止指针落在无关分页控件。
  // 指针移到左边缘(1,10)，避开居中通知的真实悬停区；等待自然关闭，不隐藏DOM或修改计时器。
  await page.mouse.move(1, 10)
  await expect(page.getByRole('tooltip')).toHaveCount(0)
  // 不改原5秒断言；诊断文件不代表已验收capture，错误仍原样抛出。
  const noticeSample = () => page.evaluate(() => ({
    dateNow: Date.now(), performanceNow: performance.now(),
    events: (window as NoticeWindow).__composeJobsNoticeEvents,
    timerIsolation: { clockMode: 'installed-running-never-paused', suppressedTaskPollDelayMs: 15000, count: (window as typeof window & { __composeJobsPolls: number }).__composeJobsPolls },
    focus: { tag: document.activeElement?.tagName, text: document.activeElement?.textContent },
    notices: [...document.querySelectorAll<HTMLElement>('.el-message')].map(element => {
      const style = getComputedStyle(element)
      return { text: element.textContent, classes: element.className, hovered: element.matches(':hover'), bounds: element.getBoundingClientRect().toJSON(),
        display: style.display, visibility: style.visibility, opacity: style.opacity, transform: style.transform, transitionDuration: style.transitionDuration,
        connected: element.isConnected, ariaHidden: element.getAttribute('aria-hidden') }
    }),
  }))
  const noticeBefore = await noticeSample(), noticeWaitStarted = performance.now()
  try { await expect(page.locator('.el-message')).toHaveCount(0) }
  catch (error) {
    const elapsedMs = performance.now() - noticeWaitStarted
    try {
      const noticeAfter = await noticeSample()
      const file = `compose-action-jobs-${state}-${page.viewportSize()!.width}-notice-failure-capture-state.json`
      const path = info.outputPath('ui-captures', file)
      await mkdir(dirname(path), { recursive: true })
      const data = JSON.stringify({ diagnosticOnly: true, acceptedCapture: false, fixtureOnly: true, nativeDockerAcceptance: false,
        state, testTitle: info.title, elapsedMs, before: noticeBefore, after: noticeAfter }, null, 2)
      await writeFile(path, data); api.captures.push({ file, sha256: hash(data) })
      await info.attach(file, { path, contentType: 'application/json' })
    } catch (diagnosticError) {
      console.error('Notice failure diagnostic could not be saved:', diagnosticError)
    }
    throw error
  }
  await waitForPageReady(page); await page.evaluate(() => document.fonts.ready)
  const selector = modal ? '.ncp-compose-action-confirmation' : '.compose-action-protection'
  const minimumTarget = 44
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
  const prefix = `compose-action-jobs-${state}-${page.viewportSize()!.width}`
  const directory = info.outputPath('ui-captures'); await mkdir(directory, { recursive: true })
  const png = `${directory}/${prefix}.png`, screenshot = await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const afterCapture = await measure(page, selector), requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await measure(page, selector)
  const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, executionScope: 'isolated static fixture; browser execution CI only', state, testTitle: info.title, project: info.project.name, retry: info.retry, scope: selector, pointerIsolation: { x: 1, y: 10, unrelatedTooltipClosed: true }, touchTargetBaseline: { pixels: minimumTarget, source: 'new protection panel/dialog:44' }, fullPage: false, scrollPositions, controlReachability, screenshot: `${prefix}.png`, screenshotSha256: hash(screenshot), beforeCapture, afterCapture, afterAxe, requestsBefore, requestsAfterCaptureSha256: hash(JSON.stringify(requestsAfterCapture)), requestsAfterAxeSha256: hash(JSON.stringify(api.requests)), requestCount: api.requests.length, requestsSha256: hash(JSON.stringify(api.requests)), writes: api.writes, denied: api.denied, errors: api.errors }
  api.captures.push({ file: `${prefix}.png`, sha256: hash(screenshot) }); await info.attach(prefix, { path: png, contentType: 'image/png' })
  const file = `${prefix}-capture-state.json`, path = `${directory}/${file}`, data = JSON.stringify({ ...metadata, axe }, null, 2)
  await writeFile(path, data); api.captures.push({ file, sha256: hash(data) })
  await info.attach(file, { path, contentType: 'application/json' })
  // 完整 axe（含 incomplete）先保存，再断言；不能删减、过滤或豁免。
  expect.soft(axe.violations, JSON.stringify(axe.violations)).toEqual([])
  expect.soft(api.requests).toEqual(requestsBefore)
  for (const control of controlReachability) {
    expect.soft(control.hits, control.text ?? '').toEqual([true, true, true, true, true])
    expect.soft(control.bounds.width).toBeGreaterThanOrEqual(minimumTarget); expect.soft(control.bounds.height).toBeGreaterThanOrEqual(minimumTarget)
  }
  for (const sample of [beforeCapture, afterCapture, afterAxe]) {
    expect.soft(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.width + 1)
    expect.soft(sample.fields.map(field => ({ bounds: field.bounds, lines: field.lines }))).toEqual(beforeCapture.fields.map(field => ({ bounds: field.bounds, lines: field.lines })));
    expect.soft(sample.text).toEqual(beforeCapture.text); expect.soft(sample.focus).toEqual(beforeCapture.focus); expect.soft(sample.scroll).toEqual(beforeCapture.scroll)
    for (const field of sample.fields) {
      if (field.clientWidth) expect.soft(field.scrollWidth, field.text ?? '').toBeLessThanOrEqual(field.clientWidth + 1)
      if (field.clientHeight && !modal) expect.soft(field.scrollHeight, field.text ?? '').toBeLessThanOrEqual(field.clientHeight + 1)
      for (const line of field.lines) {
        expect.soft(line.left).toBeGreaterThanOrEqual(Math.max(0, field.bounds.left) - 1)
        expect.soft(line.right).toBeLessThanOrEqual(Math.min(sample.viewport.width, field.bounds.right) + 1)
        if (!modal) { expect.soft(line.top).toBeGreaterThanOrEqual(field.bounds.top - 1); expect.soft(line.bottom).toBeLessThanOrEqual(field.bounds.bottom + 1) }
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
  const readOffset = api.requests.length
  await trigger.focus(); await page.keyboard.press('Enter')
  await expect(dialog(page)).toContainText('可能')
  const freshReads = api.requests.slice(readOffset)
  expect(freshReads.some(row => row.target === '/api/v1/docker/inventory')).toBe(true)
  expect(freshReads.some(row => row.target.startsWith('/api/v1/jobs'))).toBe(true)
  expect(freshReads.every(row => row.method === 'GET')).toBe(true)
  await expect(dialog(page)).toContainText('重复执行')
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
  await tabTo(page, dialog(page).getByRole('button', { name: confirm ? '确认风险并解除' : '保留保护', exact: true }))
  await page.keyboard.press('Space'); await expect(dialog(page)).toHaveCount(0)
  if (confirm) {
    await expect(panel(page)).toHaveCount(0)
  }
  else { await expect(trigger).toBeFocused(); await expect(panel(page)).toBeVisible() }
  expect(api.writes).toHaveLength(writes)
}
async function finish(page: Page, info: TestInfo, api: Fixture) {
  api.inventoryHold.release.resolve()
  api.assert()
  api.state.journeyCompleted = api.state.journeyCompleted && info.errors.length === 0
  api.checkpoints.push({ stage: api.state.stage, requestCount: api.requests.length, writes: api.writes.length })
  const path = info.outputPath('ui-captures', 'compose-action-jobs-final-request-ledger-capture-state.json')
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, artifacts: api.captures, checkpoints: api.checkpoints, finalStage: api.state.stage, journeyCompleted: api.state.journeyCompleted, requests: api.requests, requestsSha256: hash(JSON.stringify(api.requests)), responses: structuredClone(api.responses), writes: api.writes, denied: api.denied, errors: api.errors, noticeEvents: await page.evaluate(() => (window as NoticeWindow).__composeJobsNoticeEvents).catch(() => null), timerIsolation: await page.evaluate(() => ({ clockMode: 'installed-running-never-paused', suppressedTaskPollDelayMs: 15000, count: (window as typeof window & { __composeJobsPolls: number }).__composeJobsPolls })).catch(() => null) }, null, 2))
  await info.attach('compose-action-jobs-final-request-ledger', { path, contentType: 'application/json' })
}


function terminal(action: Action, outcome: 'completed' | 'failed' | 'interrupted' = 'completed') {
  return { ...receipt(action), status: outcome, progress: outcome === 'completed' ? 100 : 0, completedAt: stamp,
    ...(outcome === 'completed' ? { composeActionResult: { projectId, action, state: action === 'stop' ? 'stopped' : 'running', serviceCount: 2 } }
      : { errorCode: outcome === 'failed' ? 'JOB_QUEUE_TIMEOUT' : 'JOB_EXECUTION_INTERRUPTED' }) }
}
async function tabTo(page: Page, target: Locator) {
  for (let i = 0; i < 16; i++) {
    if (await target.evaluate(element => element === document.activeElement)) return
    await page.keyboard.press('Tab')
  }
  await expect(target).toBeFocused()
}
async function frozenIntent(page: Page, files = configFiles) {
  const details = panel(page).locator('.frozen-details')
  if (!await details.evaluate(element => (element as HTMLDetailsElement).open)) await details.locator('summary').click()
  const intent = panel(page).getByRole('region', { name: '冻结的 Compose 提交意图', exact: true })
  await expect(intent).toContainText(`项目 ID：${projectId}`)
  await expect(intent).toContainText(`工作目录：${workingDirectory}`)
  await expect(intent.getByRole('list', { name: '按提交顺序冻结的配置文件', exact: true }).locator('li')).toHaveText(files)
  for (const id of ids) await expect(intent).not.toContainText(id)
}
function standaloneButton(page: Page, label: string) {
  return page.viewportSize()!.width === 390
    ? page.locator('.mobile-project:visible').filter({ hasText: standaloneName }).getByRole('button', { name: label, exact: true })
    : page.getByRole('button', { name: `${label}项目 ${standaloneName}`, exact: true })
}
function addStandalone(api: Fixture) {
  api.state.inventory.projects.push({ ...inventory.projects[0]!, id: 'standalone', name: standaloneName, kind: 'standalone', workingDirectory: '', configFiles: [], containerCount: 1, runningCount: 1, state: 'running' })
  api.state.inventory.containers.push({ ...inventory.containers[0]!, id: standaloneId, name: `${standaloneName}-0`, projectId: 'standalone', projectName: standaloneName })
  api.state.inventory.engine.containers++; api.state.inventory.engine.containersRunning++
}
async function guards(page: Page, api: Fixture) {
  const writes = api.writes.length
  for (const action of ['start', 'stop', 'restart'] as const) await expect(actionButton(page, action)).toBeDisabled()
  for (const label of ['启动', '停止', '重启']) await expect(standaloneButton(page, label)).toBeDisabled()
  await page.getByRole('button', { name: `查看 Docker 项目 ${name}`, exact: true }).filter({ visible: true }).click()
  const drawer = page.locator('.project-drawer')
  for (const label of ['启动', '停止', '重启']) await expect(drawer.locator('.project-lifecycle__actions').getByRole('button', { name: label, exact: true })).toBeDisabled()
  await page.keyboard.press('Escape'); await expect(drawer).not.toBeVisible()
  await page.getByRole('button', { name: '容器', exact: true }).click()
  const containers = page.locator(page.viewportSize()!.width === 390 ? '.container-mobile-list' : '.container-panel')
  const buttons = containers.getByRole('button', { name: page.viewportSize()!.width === 390 ? /^(启动|停止|重启)$/ : /^(启动|停止|重启)容器$/ })
  expect(await buttons.count()).toBeGreaterThan(0)
  for (const button of await buttons.all()) await expect(button).toBeDisabled()
  await page.getByRole('button', { name: '镜像', exact: true }).click()
  await expect(page.getByRole('button', { name: '从镜像 fixture:latest 创建容器', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '项目', exact: true }).click(); expect(api.writes).toHaveLength(writes)
}
for (const width of [390, 1440]) {
  test(`${width}px ordered frozen paths exact202 knownGET aggregate completion cross-standalone fence`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      addStandalone(api); await open(page); api.stage('manual-read-held')
      api.holdNextInventory(); await page.getByRole('button', { name: '立即刷新数据', exact: true }).click(); await api.inventoryHold.received.promise
      await expect(actionButton(page, 'restart')).toBeEnabled(); api.stage('frozen-confirmation')
      await actionButton(page, 'restart').focus(); await page.keyboard.press('Enter')
      await expect(dialog(page)).toContainText(configFiles[0]!)
      const scope = dialog(page).getByRole('region', { name: '冻结项目操作范围', exact: true })
      const text = await scope.innerText(); expect(text.indexOf(configFiles[0]!)).toBeLessThan(text.indexOf(configFiles[1]!))
      await expect(scope).toContainText('不是冻结的 Compose 容器范围'); await expect(scope).not.toContainText(containerId)
      expect(api.writes).toHaveLength(0); await capture(page, info, api, 'confirmation', true)
      // 确认期间清单改变，POST和面板仍必须使用用户看到的旧有序路径。
      api.state.inventory.projects[0]!.workingDirectory = '/fixture/changed'
      api.state.inventory.projects[0]!.configFiles = [...configFiles].reverse()
      api.inventoryHold.release.resolve(); await api.inventoryHold.delivered.promise
      const currentPath = width === 390 ? page.locator('.mobile-project:visible').filter({ hasText: name }).locator('dd[title]') : page.locator('.project-row:visible').filter({ hasText: name }).locator('.path-cell')
      await expect(currentPath).toBeVisible(); await expect(currentPath).toHaveText('/fixture/changed')
      await expect(scope).toContainText(workingDirectory); await expect(scope).not.toContainText('/fixture/changed')
      expect(api.writes).toHaveLength(0)
      api.allow('restart'); await tabTo(page, dialog(page).getByRole('button', { name: '确认重启', exact: true })); await page.keyboard.press('Enter')
      await expect(check(page)).toBeEnabled(); await frozenIntent(page); await expect(panel(page)).toContainText('排队中')
      await guards(page, api); await capture(page, info, api, 'queued-frozen')
      for (const phase of ['running', 'pending', 'wrong-id', 'read-failed', 'invalid-result'] as const) {
        api.stage(phase)
        api.state.job = phase === 'invalid-result' ? { ...terminal('restart'), composeActionResult: { projectId, action: 'restart', state: 'running', serviceCount: 0 } }
          : { ...receipt(), status: 'running', ...(phase === 'pending' ? { persistenceStatus: 'pending' } : {}), ...(phase === 'wrong-id' ? { id: 'foreign-job' } : {}) }
        api.state.jobStatus = phase === 'read-failed' ? 503 : 200
        await readonlyCheck(page); await expect(release(page)).toBeDisabled()
        await expect(panel(page)).toContainText(phase === 'running' ? '执行中' : phase === 'pending' ? '结果保存中' : '任务读取失败')
        await expect(panel(page)).toContainText('任务 ID：fixture-restart-job'); await frozenIntent(page)
      }
      await capture(page, info, api, 'invalid-result-protected')
      api.stage('known-completion'); api.state.jobStatus = 200; api.state.job = terminal('restart')
      const before = api.requests.length; await check(page).click(); await expect(check(page)).toHaveCount(0)
      expect(api.requests.slice(before).filter(row => row.target.startsWith('/api/v1/jobs')).map(row => row.target)).toEqual(['/api/v1/jobs/fixture-restart-job'])
      await expect(panel(page)).toContainText('该次调用聚合结果已保存'); await expect(panel(page)).toContainText('服务观察项数：2')
      await expect(panel(page)).toContainText('没有逐服务结果'); await expect(panel(page)).toContainText('不保证健康、持续状态、配置服务完整性或重启次数')
      await expect(panel(page).getByRole('list')).toHaveCount(0); await expect(standaloneButton(page, '重启')).toBeEnabled()
      await capture(page, info, api, 'aggregate-completed'); expect(api.writes).toHaveLength(1); api.state.journeyCompleted = true
    } finally { await finish(page, info, api) }
  })
  for (const outcome of ['failed', 'interrupted'] as const) test(`${width}px ${outcome} fresh reads risk cancel and explicit zeroPOST release`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); await submit(page, api, 'stop'); await expect(check(page)).toBeEnabled()
      api.stage(`${outcome}-inventory-failed`); api.state.job = terminal('stop', outcome); api.state.inventoryFails = true
      await readonlyCheck(page); await expect(release(page)).toBeDisabled(); await frozenIntent(page)
      await expect(panel(page)).toContainText(outcome === 'failed' ? '任务失败' : '任务中断')
      await expect(panel(page)).not.toContainText('聚合结果已保存'); await capture(page, info, api, `${outcome}-protected`)
      api.stage('successful-read'); api.state.inventoryFails = false; await readonlyCheck(page); await expect(release(page)).toBeEnabled()
      // 旧读成功不授权释放：点击后必须重新读，失败后不得打开风险确认。
      api.stage('fresh-release-read-failed'); api.state.jobStatus = 503
      const before = api.requests.length; await release(page).click(); await expect(check(page)).toBeEnabled()
      expect(api.requests.slice(before).some(row => row.target === '/api/v1/jobs/fixture-stop-job')).toBe(true)
      await expect(dialog(page)).toHaveCount(0); await expect(release(page)).toBeDisabled()
      api.state.jobStatus = 200; await readonlyCheck(page); await expect(release(page)).toBeEnabled()
      api.stage('risk-cancel'); await risk(page, info, api, `${outcome}-cancel`, false)
      api.stage('risk-confirm'); await risk(page, info, api, `${outcome}-release`, true)
      await expect(actionButton(page, 'restart')).toBeEnabled(); expect(api.writes).toHaveLength(1); api.state.journeyCompleted = true
    } finally { await finish(page, info, api) }
  })
  test(`${width}px lost202 three filtered lists never claim history sticky active unknown`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      addStandalone(api); await open(page); api.stage('lost202'); await submit(page, api, 'start', 'lost')
      await expect(panel(page)).toContainText('接收结果未知'); await expect(panel(page)).not.toContainText('任务 ID：'); await expect(release(page)).toBeDisabled()
      await frozenIntent(page); await capture(page, info, api, 'lost202'); await navigate(page, '站点管理'); await navigate(page, 'Docker'); await guards(page, api)
      api.stage('unknown-read-failed'); api.state.jobsFail = true; api.state.inventoryFails = true; await readonlyCheck(page); await expect(release(page)).toBeDisabled()
      api.stage('foreign-active-observed'); api.state.jobsFail = false; api.state.inventoryFails = false
      api.state.jobs = [{ ...receipt('stop'), id: 'foreign-active', reference: 'compose:foreign', status: 'running' }]
      const before = api.requests.length; await readonlyCheck(page)
      expect(api.requests.slice(before).filter(row => row.target.startsWith('/api/v1/jobs?')).map(row => row.target).sort()).toEqual(['restart', 'start', 'stop'].map(action => `/api/v1/jobs?type=compose-${action}`))
      await expect(panel(page)).toContainText('观察到活动 Compose 任务'); await expect(panel(page)).not.toContainText('任务 ID：'); await expect(release(page)).toBeDisabled()
      api.stage('sticky-failed-read'); api.state.jobsFail = true; await readonlyCheck(page)
      await expect(panel(page)).toContainText('观察到活动 Compose 任务'); await expect(release(page)).toBeDisabled(); await capture(page, info, api, 'sticky-active')
      api.stage('history-never-claimed'); api.state.jobsFail = false; api.state.jobs = [{ ...terminal('start'), id: 'history-only-completed' }]
      await readonlyCheck(page); await expect(release(page)).toBeEnabled(); await expect(panel(page)).not.toContainText('任务 ID：')
      await expect(panel(page)).not.toContainText('聚合结果已保存'); expect(api.requests.some(row => row.target === '/api/v1/jobs/history-only-completed')).toBe(false)
      await capture(page, info, api, 'history-unclaimed'); await risk(page, info, api, 'unknown-release', true)
      expect(api.writes).toHaveLength(1); api.state.journeyCompleted = true
    } finally { await finish(page, info, api) }
  })
  for (const newSession of [false, true]) test(`${width}px late202 ${newSession ? 'session isolation' : 'SPA ownership project disappears'}`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); api.stage('held-submit'); await submit(page, api, 'stop', 'held'); await api.held.received.promise
      await expect(panel(page)).toContainText('正在提交'); await expect(check(page)).toBeDisabled()
      if (newSession) {
        api.stage('new-session'); api.allowSessionSwitch(); if (width === 390) await page.getByRole('button', { name: '打开主菜单', exact: true }).click()
        await page.getByRole('button', { name: '退出登录', exact: true }).click()
        await page.getByLabel('账号', { exact: true }).fill('root'); await page.getByLabel('密码', { exact: true }).fill('isolated-fixture-only')
        await page.getByRole('button', { name: '进入控制台' }).click(); await expect(page.locator('.navigation')).toBeAttached()
        if (new URL(page.url()).pathname !== '/docker') await navigate(page, 'Docker')
        else await page.getByRole('button', { name: '项目', exact: true }).click()
        await expect(panel(page)).toHaveCount(0); await submit(page, api, 'restart', 'lost'); await expect(panel(page)).toContainText('接收结果未知')
      } else { await navigate(page, '站点管理'); await navigate(page, 'Docker'); await expect(panel(page)).toContainText('正在提交') }
      api.stage('late-old-receipt'); api.held.release.resolve(); await api.held.delivered.promise
      await page.evaluate(() => new Promise<void>(done => requestAnimationFrame(() => requestAnimationFrame(() => done()))))
      if (newSession) {
        await expect(panel(page)).toContainText('接收结果未知'); await expect(panel(page)).not.toContainText('任务 ID：fixture-stop-job')
        await expect(actionButton(page, 'restart')).toBeDisabled(); await capture(page, info, api, 'new-session-isolated')
      } else {
        await expect(panel(page)).toContainText('任务 ID：fixture-stop-job'); api.stage('project-disappears')
        api.state.inventory.containers = []; api.state.inventory.projects = []; api.state.inventory.engine.containers = 0
        api.state.inventory.engine.containersRunning = 0; api.state.inventory.engine.containersStopped = 0
        await refresh(page, api); await frozenIntent(page); await readonlyCheck(page); await expect(release(page)).toBeDisabled()
        await capture(page, info, api, 'absent-project-checkable')
      }
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      expect(api.writes.filter(row => row.target.includes('/action-jobs/'))).toHaveLength(newSession ? 2 : 1); api.state.journeyCompleted = true
    } finally { api.held.release.resolve(); await finish(page, info, api) }
  })
  test(`${width}px standalone admission fences Compose and cancel releases without POST`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      addStandalone(api); await open(page); api.stage('standalone-confirming')
      const otherDialog = page.locator('.ncp-container-action-confirmation')
      await standaloneButton(page, '重启').focus(); await page.keyboard.press('Enter'); await expect(otherDialog).toBeVisible()
      await expect(actionButton(page, 'restart')).toBeDisabled(); expect(api.writes).toHaveLength(0)
      await tabTo(page, otherDialog.getByRole('button', { name: '取消', exact: true })); await page.keyboard.press('Enter')
      await expect(otherDialog).toHaveCount(0); await expect(standaloneButton(page, '重启')).toBeFocused(); await expect(actionButton(page, 'restart')).toBeEnabled()
      api.stage('standalone-known'); await standaloneButton(page, '重启').press('Enter'); api.allowStandalone()
      await tabTo(page, otherDialog.getByRole('button', { name: '确认重启', exact: true })); await page.keyboard.press('Enter')
      await expect(page.getByRole('region', { name: '项目操作防重复保护', exact: true })).toContainText('任务 ID：fixture-standalone-job')
      await expect(actionButton(page, 'restart')).toBeDisabled(); await expect(panel(page)).toHaveCount(0)
      expect(api.writes).toHaveLength(1); expect(api.writes[0]!.target).toBe('/api/v1/docker/projects/standalone/action-jobs/restart')
      api.state.journeyCompleted = true
    } finally { await finish(page, info, api) }
  })
}

for (const width of [390, 1440]) test(`${width}px 32 long ordered config paths keyboard scroll cancel zero writes`, async ({ page, baseURL }, info) => {
  test.setTimeout(120_000); await page.setViewportSize({ width, height: 900 })
  const api = await fixture(page, new URL(baseURL!).origin)
  try {
    const files = Array.from({ length: 32 }, (_, i) => `${workingDirectory}/${32 - i}-${'long-config-directory-'.repeat(5)}/compose.yaml`)
    api.state.inventory.projects[0]!.configFiles = files
    await open(page); api.stage('long-config-confirmation'); await actionButton(page, 'restart').focus(); await page.keyboard.press('Enter')
    const scope = dialog(page).getByRole('region', { name: '冻结项目操作范围', exact: true }), text = await scope.innerText()
    files.forEach((file, i) => { expect(text).toContain(file); if (i) expect(text.indexOf(files[i - 1]!)).toBeLessThan(text.indexOf(file)) })
    await page.mouse.move(1, 10); await expect(page.locator('.el-message')).toHaveCount(0)
    await waitForPageReady(page); await page.evaluate(() => document.fonts.ready); await tabTo(page, scope)
    const geometry = () => dialog(page).evaluate(root => {
      const scope = root.querySelector<HTMLElement>('.ncp-project-action-scope')!, box = scope.getBoundingClientRect()
      const active = document.activeElement!, css = getComputedStyle(active)
      const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT), ranges: { left: number; right: number }[] = []
      while (walker.nextNode()) { const range = document.createRange(); range.selectNodeContents(walker.currentNode); ranges.push(...[...range.getClientRects()].map(rect => ({ left: rect.left, right: rect.right }))) }
      return { rootWidth: document.documentElement.scrollWidth, scope: { bounds: box.toJSON(), scrollHeight: scope.scrollHeight, clientHeight: scope.clientHeight, scrollTop: scope.scrollTop, overflowY: getComputedStyle(scope).overflowY },
        focus: { contained: root.contains(active), scope: active === scope, visible: active.matches(':focus-visible'), width: parseFloat(css.outlineWidth), style: css.outlineStyle, color: css.outlineColor, text: active.textContent }, ranges,
        controls: [...root.querySelectorAll<HTMLElement>('button')].map(element => { const r = element.getBoundingClientRect(); return { text: element.textContent, bounds: r.toJSON(), hits: [[r.left + 5,r.top + 5],[r.right - 5,r.top + 5],[r.left + 5,r.bottom - 5],[r.right - 5,r.bottom - 5],[r.left + r.width / 2,r.top + r.height / 2]].map(([x,y]) => { const hit = document.elementFromPoint(x!,y!); return !!hit && (hit === element || element.contains(hit)) }) } }) }
    })
    await page.keyboard.press('Home'); await expect.poll(async () => (await geometry()).scope.scrollTop).toBeLessThanOrEqual(1)
    const before = await geometry(), requestOffset = api.requests.length
    await page.keyboard.press('PageDown'); await expect.poll(async () => (await geometry()).scope.scrollTop).toBeGreaterThan(before.scope.scrollTop)
    await expect(scope).toBeFocused(); const scrolled = await geometry()
    await page.keyboard.press('End'); await expect.poll(async () => { const s = await geometry(); return s.scope.scrollHeight - s.scope.clientHeight - s.scope.scrollTop }).toBeLessThanOrEqual(1)
    const bottom = await geometry(), directory = info.outputPath('ui-captures'); await mkdir(directory, { recursive: true })
    const file = `compose-action-jobs-long-paths-${width}`, path = `${directory}/${file}.png`, requestsBefore = structuredClone(api.requests)
    const png = await page.screenshot({ path, animations: 'disabled' }), afterScreenshot = await geometry()
    const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(), keyboardCycle = []
    for (const key of ['Tab', 'Tab', 'Shift+Tab', 'Shift+Tab']) { await page.keyboard.press(key); keyboardCycle.push((await geometry()).focus) }
    const metadata = JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, fullPage: false, files, before, scrolled, bottom, afterScreenshot, afterAxe, keyboardCycle, axe, screenshotSha256: hash(png), requestsBefore, requestsAfter: api.requests, pointerIsolation: { x: 1, y: 10 } }, null, 2)
    await writeFile(`${directory}/${file}-capture-state.json`, metadata)
    api.captures.push({ file: `${file}.png`, sha256: hash(png) }, { file: `${file}-capture-state.json`, sha256: hash(metadata) }); await info.attach(file, { path, contentType: 'image/png' })
    expect.soft(axe.violations).toEqual([]); expect.soft(afterScreenshot).toEqual(bottom); expect.soft(afterAxe).toEqual(bottom); expect.soft(api.requests).toEqual(requestsBefore)
    expect(api.requests).toHaveLength(requestOffset); expect(before.scope.scrollHeight).toBeGreaterThan(before.scope.clientHeight)
    expect(before.scope.bounds.height).toBeLessThanOrEqual(420); expect(before.scope.overflowY).toMatch(/auto|scroll/)
    for (const sample of [before, scrolled, bottom]) {
      expect.soft(sample.rootWidth).toBeLessThanOrEqual(width)
      for (const range of sample.ranges) { expect.soft(range.left).toBeGreaterThanOrEqual(sample.scope.bounds.left - 1); expect.soft(range.right).toBeLessThanOrEqual(sample.scope.bounds.right + 1) }
      for (const control of sample.controls) { expect.soft(control.bounds.height).toBeGreaterThanOrEqual(44); expect.soft(control.bounds.width).toBeGreaterThanOrEqual(44); expect.soft(control.hits).toEqual([true,true,true,true,true]) }
    }
    for (const focus of [before.focus, scrolled.focus, bottom.focus, ...keyboardCycle]) {
      expect.soft(focus.contained).toBe(true); expect.soft(focus.visible).toBe(true); expect.soft(focus.width).toBeGreaterThanOrEqual(2)
      expect.soft(focus.style).toBe('solid'); expect.soft(focus.color).not.toMatch(/^(transparent|rgba\(0, 0, 0, 0\))$/)
    }
    await tabTo(page, dialog(page).getByRole('button', { name: '取消', exact: true })); await page.keyboard.press('Enter')
    await expect(dialog(page)).toHaveCount(0); await expect(actionButton(page, 'restart')).toBeFocused(); expect(api.writes).toEqual([]); api.state.journeyCompleted = true
  } finally { await finish(page, info, api) }
})
