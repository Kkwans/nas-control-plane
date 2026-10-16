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
const ids = [containerId, 'b'.repeat(64), 'c'.repeat(64)]
const stamp = '2026-10-16T01:00:00Z'
type Action = 'start' | 'stop' | 'restart'
type NoticeObservation = { event: string; dateNow: number; performanceNow: number; detail: unknown }
type NoticeWindow = typeof window & { __projectJobsNoticeEvents: NoticeObservation[] }
type RequestRecord = { stage: string; method: string; target: string; body: string | null }
const receipt = (action: Action = 'restart') => ({ id: `fixture-${action}-job`, type: `docker-project-${action}`, reference: 'standalone', cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} })
const inventory = {
  collectedAt: stamp,
  engine: { ...overviewDocker.engine, containers: 3, containersRunning: 2, containersStopped: 1 },
  projects: [{ id: projectId, name, kind: 'standalone', workingDirectory: '', configFiles: [], containerCount: 3, runningCount: 2, state: 'degraded' }],
  containers: ids.map((id, index) => ({ ...overviewDocker.containers[0]!, id, name: `${name}-${index}`, projectId, projectName: name, state: index === 2 ? 'exited' : 'running', status: index === 2 ? 'Exited (0)' : 'Up', ports: [] })),
}
function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done }); return { promise, resolve } }
async function fixture(page: Page, origin: string) {
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  // 保持 Date 流动；固定 Date 会令 Vue capture/bubble invoker时间戳相同而跳过 RouterLink。
  // 安装可观测时钟但从不 pause；axe、动画及普通异步计时器照常运行。
  await page.clock.install({ time: new Date(stamp) })
  await page.addInitScript(() => {
    const host = window as typeof window & { __projectJobsPolls: number; __projectJobsStreams: string[]; __projectJobsDocument: string }
    host.__projectJobsPolls = 0; host.__projectJobsStreams = []; host.__projectJobsDocument = crypto.randomUUID()
    class SilentSource extends EventTarget {
      static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2
      readyState = 1; onopen: (() => void) | null = null; onerror: (() => void) | null = null
      constructor(readonly url: string) {
        super(); host.__projectJobsStreams.push(url)
        const endpoint = new URL(url, location.origin)
        if (endpoint.origin !== location.origin || !['/api/v1/system/events?interval=5&scope=docker', '/api/v1/system/events?interval=5&scope=summary', '/api/v1/system/events?interval=5&scope=docker&scope=summary'].includes(endpoint.pathname + endpoint.search)) throw new Error(`Fixture refused EventSource: ${url}`)
        queueMicrotask(() => { this.onopen?.(); this.dispatchEvent(new Event('open')) })
      }
      close() { this.readyState = 2 }
    }
    window.EventSource = SilentSource as unknown as typeof EventSource
    const noticeHost = window as NoticeWindow
    noticeHost.__projectJobsNoticeEvents = []
    const record = (event: string, detail: unknown) => {
      if (noticeHost.__projectJobsNoticeEvents.length < 200) noticeHost.__projectJobsNoticeEvents.push({ event, dateNow: Date.now(), performanceNow: performance.now(), detail })
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
    window.clearTimeout = ((id?: number) => {
      if (id !== undefined && noticeTimers.delete(id)) record('timer-clear', { id })
      return originalClear(id)
    }) as typeof window.clearTimeout
    window.setTimeout = ((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      // 只隔离 useTaskCenter 的成功刷新 15 秒 timer；本专项不验证轮询。
      if (delay === 15_000) return -(++host.__projectJobsPolls)
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
  const captures: Array<{ file: string; sha256: string }> = [], checkpoints: Array<{ stage: string; requests: RequestRecord[]; writes: number }> = []
  const state = { stage: 'bootstrap', journeyCompleted: false, inventory: structuredClone(inventory), inventoryFails: false, jobsFail: false, jobStatus: 200, job: receipt() as Record<string, unknown>, jobs: [] as Record<string, unknown>[], authenticated: true }
  let allowance: { action: Action; mode: 'accepted' | 'lost' | 'held' } | undefined
  const authAllowance: string[] = []
  let permittedWrites = 0, authWrites = 0, inventoryTimestamp = Date.parse(stamp)
  const held = { received: deferred(), release: deferred(), delivered: deferred() }
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs', { jobs: [] }],
    ['/api/v1/jobs?type=docker-image-pull', { jobs: [] }],
    ['/api/v1/docker/images', { collectedAt: stamp, images: [{ id: 'sha256:fixture-image', repoTags: ['fixture:latest'], repoDigests: [], sizeBytes: 1024, createdAt: stamp, containers: 0 }] }],
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
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
    if (request.method() === 'POST' && allowance && target === `/api/v1/docker/projects/standalone/action-jobs/${allowance.action}`) {
      const allowed = allowance; allowance = undefined
      expect(JSON.parse(entry.body!)).toEqual({ containerIds: allowed.action === 'start' ? [ids[2]] : ids.slice(0, 2) })
      if (allowed.mode === 'lost') { responses.push({ stage: state.stage, target, status: 'aborted', payload: null }); return route.abort('failed') }
      if (allowed.mode === 'held') { held.received.resolve(); await held.release.promise }
      await reply(receipt(allowed.action), 202)
      if (allowed.mode === 'held') held.delivered.resolve()
      return
    }
    if (target === '/api/v1/auth/logout' && request.method() === 'POST' && authAllowance[0] === 'logout') { authAllowance.shift(); state.authenticated = false; return route.fulfill({ status: 204 }) }
    if (target === '/api/v1/auth/login' && request.method() === 'POST' && authAllowance[0] === 'login') { authAllowance.shift(); state.authenticated = true; return reply({ user: { id: 1, username: 'root', role: 'root' }, expiresAt: '2026-10-17T01:00:00Z' }) }
    if (request.method() !== 'GET') return deny()
    if (target === '/api/v1/auth/status') return reply({ initialized: true, authenticated: state.authenticated, ...(state.authenticated ? { user: { id: 1, username: 'root', role: 'root' } } : {}) })
    if (['start', 'stop', 'restart'].some(action => target === `/api/v1/jobs?type=docker-project-${action}`)) return state.jobsFail ? reply({ code: 'FIXTURE_READ_UNAVAILABLE' }, 503) : reply({ jobs: state.jobs.filter(job => job.type === url.searchParams.get('type')) })
    const fail = () => reply({ code: 'FIXTURE_READ_UNAVAILABLE', message: '只读 fixture 失败' }, 503)
    if (target === '/api/v1/docker/inventory') {
      // Store 只接受较新快照；每次读回独立版本，不能用相同 collectedAt 假装清单已更新。
      inventoryTimestamp = Math.max(inventoryTimestamp + 1, await page.evaluate(() => Date.now()))
      const collectedAt = new Date(inventoryTimestamp).toISOString()
      return state.inventoryFails ? fail() : reply({ ...state.inventory, collectedAt })
    }
    if (target === '/api/v1/jobs') return state.jobsFail ? fail() : reply({ jobs: state.jobs })
    if (target === `/api/v1/jobs/fixture-${String(state.job.type).replace('docker-project-', '')}-job`) return state.jobStatus !== 200 ? reply({ code: 'JOB_NOT_FOUND', message: 'fixture精确任务读取失败' }, state.jobStatus) : reply(state.job)
    if (reads.has(target)) return reply(reads.get(target))
    // 应用入口与构建资源同样精确白名单；不允许未知 API、query、外网或真实 WS。
    if (!url.search && (['/', '/docker', '/sites', '/login', '/favicon.svg'].includes(url.pathname) || /^\/assets\/[A-Za-z0-9_.-]+$/.test(url.pathname))) return route.fallback()
    return deny()
  })
  return { requests, responses, writes, denied, errors, captures, checkpoints, state, held,
    allowSessionSwitch() { expect(authAllowance).toEqual([]); authAllowance.push('logout', 'login'); authWrites += 2 },
    stage(stage: string) { checkpoints.push({ stage: state.stage, requests: structuredClone(requests), writes: writes.length }); state.stage = stage },
    allow(action: Action, mode: 'accepted' | 'lost' | 'held' = 'accepted') { expect(allowance).toBeUndefined(); allowance = { action, mode }; permittedWrites++; state.job = receipt(action) },
    assert() { expect.soft(writes).toHaveLength(permittedWrites + authWrites); expect.soft(allowance).toBeUndefined(); expect.soft(authAllowance).toEqual([]); expect.soft(denied).toEqual([]); expect.soft(errors).toEqual([]) },
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
const panel = (page: Page) => page.getByRole('region', { name: '项目操作防重复保护', exact: true })
const dialog = (page: Page) => page.locator('.ncp-container-action-confirmation')
const check = (page: Page) => panel(page).getByRole('button', { name: '只读核对任务与容器', exact: true })
const release = (page: Page) => panel(page).getByRole('button', { name: '确认重复执行风险并解除本会话保护', exact: true })
const actionButton = (page: Page, action: Action) => {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  return page.viewportSize()!.width === 390 ? page.locator('.mobile-project:visible').filter({ hasText: name }).getByRole('button', { name: label, exact: true }) : page.getByRole('button', { name: `${label}项目 ${name}`, exact: true })
}
async function navigate(page: Page, name: 'Docker' | '站点管理') {
  const documentId = await page.evaluate(() => (window as typeof window & { __projectJobsDocument: string }).__projectJobsDocument)
  if (page.viewportSize()!.width === 390) await page.getByRole('button', { name: '打开主菜单', exact: true }).click()
  await page.locator('.app-sidebar').getByRole('link', { name, exact: true }).click()
  await expect(page).toHaveURL(name === 'Docker' ? /\/docker$/ : /\/sites$/)
  expect(await page.evaluate(() => (window as typeof window & { __projectJobsDocument: string }).__projectJobsDocument)).toBe(documentId)
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
    await expect(dialog(page)).toContainText(containerId)
    await expect(dialog(page)).toContainText('已接收不代表完成')
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
  await expect(panel(page)).toBeVisible()
}
async function measure(page: Page, selector: string) {
  return page.evaluate(selector => {
    const root = document.querySelector<HTMLElement>(selector)!
    const fields = root.matches('[role="tooltip"]') ? [root] : [...root.querySelectorAll<HTMLElement>('h2,strong,p,li.identity,summary,.el-message-box__title,.el-message-box__message,button')]
    return {
      viewport: { width: innerWidth, height: innerHeight }, root: { width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth },
      tooltip: root.matches('[role="tooltip"]') ? { bounds: root.getBoundingClientRect().toJSON(), ownerTag: root.closest('main, [role="main"], [role="dialog"]')?.tagName ?? null, anchor: document.querySelector('.page-size-control:hover')?.getBoundingClientRect().toJSON() ?? null } : null,
      text: root.textContent, scroll: { x: scrollX, y: scrollY }, focus: { tag: document.activeElement?.tagName, text: document.activeElement?.textContent, label: document.activeElement?.getAttribute('aria-label') },
      timerIsolation: { clockMode: 'installed-running-never-paused', suppressedTaskPollDelayMs: 15000, suppressedCount: (window as typeof window & { __projectJobsPolls: number }).__projectJobsPolls, realPollingVerified: false },
      mockedStreams: (window as typeof window & { __projectJobsStreams: string[] }).__projectJobsStreams,
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
        return { tag: element.tagName, text: element.textContent, tooltipRoot: element.matches('[role="tooltip"]'), decorativeArrows: [...element.querySelectorAll('.el-popper__arrow')].map(arrow => ({ text: arrow.textContent, bounds: arrow.getBoundingClientRect().toJSON() })), bounds: rect.toJSON(), lines: lines.filter(line => line.width && line.height).map(line => line.toJSON()), ancestors,
          clientWidth: element.clientWidth, scrollWidth: element.scrollWidth, clientHeight: element.clientHeight, scrollHeight: element.scrollHeight,
          focusVisible: element.matches(':focus-visible'), outlineWidth: style.outlineWidth, outlineStyle: style.outlineStyle, outlineColor: style.outlineColor,
          disabled: element.matches(':disabled'), hits: points.map(([x, y]) => { const target = document.elementFromPoint(x!, y!); return Boolean(target && (target === element || element.contains(target))) }) }
      }).filter(field => field.bounds.width > 0 && field.bounds.height > 0),
    }
  }, selector)
}
async function capture(page: Page, info: TestInfo, api: Fixture, state: string, modal = false, selectorOverride?: string, preserveHover = false) {
  // 本例捕获目标操作状态而非分页 hover；释放后布局回缩可能让静止指针落在无关分页控件。
  // 显式移至中性页头并等待真实 tooltip关闭，不隐藏DOM、不删除axe规则或命中断言。
  if (!preserveHover) {
    await page.mouse.move(page.viewportSize()!.width / 2, 10)
    await expect(page.getByRole('tooltip')).toHaveCount(0)
  }
  // 不改原5秒断言；诊断文件不代表已验收capture，错误仍原样抛出。
  const noticeSample = () => page.evaluate(() => ({
    dateNow: Date.now(), performanceNow: performance.now(),
    events: (window as NoticeWindow).__projectJobsNoticeEvents,
    timerIsolation: { clockMode: 'installed-running-never-paused', suppressedTaskPollDelayMs: 15000, count: (window as typeof window & { __projectJobsPolls: number }).__projectJobsPolls },
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
      const file = `project-action-jobs-${state}-${page.viewportSize()!.width}-notice-failure-capture-state.json`
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
  const selector = selectorOverride ?? (modal ? '.ncp-container-action-confirmation' : '.project-action-protection')
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
  const prefix = `project-action-jobs-${state}-${page.viewportSize()!.width}`
  const directory = info.outputPath('ui-captures'); await mkdir(directory, { recursive: true })
  const png = `${directory}/${prefix}.png`, screenshot = await page.screenshot({ path: png, fullPage: !modal, animations: 'disabled' })
  const afterCapture = await measure(page, selector), requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await measure(page, selector)
  const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, executionScope: 'isolated static fixture; browser execution CI only', state, testTitle: info.title, project: info.project.name, retry: info.retry, scope: selector, pointerIsolation: preserveHover ? { intentionalPageSizeHelpHover: true } : { x: page.viewportSize()!.width / 2, y: 10, unrelatedTooltipClosed: true }, touchTargetBaseline: preserveHover ? null : { pixels: minimumTarget, source: selectorOverride ? 'existing container action controls: desktop40/mobile42; not new protection UI' : 'new protection panel/dialog:44' }, fullPage: !modal, scrollPositions, controlReachability, screenshot: `${prefix}.png`, screenshotSha256: hash(screenshot), beforeCapture, afterCapture, afterAxe, requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), responses: structuredClone(api.responses), writes: api.writes, denied: api.denied, errors: api.errors }
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
    if (preserveHover) {
      expect.soft(sample.tooltip?.ownerTag).toBe('MAIN')
      expect.soft(sample.tooltip?.anchor).not.toBeNull()
      if (sample.tooltip?.anchor) {
        const gap = sample.tooltip.anchor.top - sample.tooltip.bounds.bottom
        expect.soft(gap).toBeGreaterThanOrEqual(0); expect.soft(gap).toBeLessThanOrEqual(24)
      }
    }
    expect.soft(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.width + 1)
    expect.soft(sample.fields.map(field => ({ bounds: field.bounds, lines: field.lines }))).toEqual(beforeCapture.fields.map(field => ({ bounds: field.bounds, lines: field.lines })));
    expect.soft(sample.text).toEqual(beforeCapture.text); expect.soft(sample.focus).toEqual(beforeCapture.focus); expect.soft(sample.scroll).toEqual(beforeCapture.scroll)
    for (const field of sample.fields) {
      // Tooltip装饰箭头在边框外，原始scrollHeight可能包含箭头；文字仍逐Range严查且记录箭头几何。
      if (!field.tooltipRoot) {
        if (field.clientWidth) expect.soft(field.scrollWidth, field.text ?? '').toBeLessThanOrEqual(field.clientWidth + 1)
        if (field.clientHeight) expect.soft(field.scrollHeight, field.text ?? '').toBeLessThanOrEqual(field.clientHeight + 1)
      } else {
        expect.soft(field.decorativeArrows).toHaveLength(1)
        expect.soft(field.decorativeArrows[0]?.text?.trim()).toBe('')
      }
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
  await expect(dialog(page)).toContainText('可能')
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
  await dialog(page).getByRole('button', { name: confirm ? '确认风险并解除' : '保留保护', exact: true }).focus()
  await page.keyboard.press('Space'); await expect(dialog(page)).toHaveCount(0)
  if (confirm) {
    await expect(panel(page)).toHaveCount(0)
  }
  else { await expect(trigger).toBeFocused(); await expect(panel(page)).toBeVisible() }
  expect(api.writes).toHaveLength(writes)
}
async function finish(page: Page, info: TestInfo, api: Fixture) {
  api.assert()
  api.state.journeyCompleted = api.state.journeyCompleted && info.errors.length === 0
  api.checkpoints.push({ stage: api.state.stage, requests: structuredClone(api.requests), writes: api.writes.length })
  const path = info.outputPath('ui-captures', 'project-action-jobs-final-request-ledger-capture-state.json')
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, artifacts: api.captures, checkpoints: api.checkpoints, finalStage: api.state.stage, journeyCompleted: api.state.journeyCompleted, requests: api.requests, requestsSha256: hash(JSON.stringify(api.requests)), responses: structuredClone(api.responses), writes: api.writes, denied: api.denied, errors: api.errors, noticeEvents: await page.evaluate(() => (window as NoticeWindow).__projectJobsNoticeEvents).catch(() => null), timerIsolation: await page.evaluate(() => ({ clockMode: 'installed-running-never-paused', suppressedTaskPollDelayMs: 15000, count: (window as typeof window & { __projectJobsPolls: number }).__projectJobsPolls })).catch(() => null) }, null, 2))
  await info.attach('project-action-jobs-final-request-ledger', { path, contentType: 'application/json' })
}

function terminal(action: Action, outcome: 'completed' | 'failed' | 'interrupted' = 'completed') {
  return { ...receipt(action), status: outcome, progress: outcome === 'completed' ? 100 : 0, completedAt: stamp,
    ...(outcome === 'failed' ? { errorCode: 'DOCKER_PROJECT_STATE_MISMATCH' } : outcome === 'interrupted' ? { errorCode: 'JOB_EXECUTION_INTERRUPTED' } : {}),
    projectActionResult: { projectId: 'standalone', kind: 'standalone', action, items: (action === 'start' ? [ids[2]!] : ids.slice(0, 2)).map((containerId, index) => index === 1 && outcome !== 'completed'
      ? outcome === 'failed' ? { containerId, outcome: 'state-mismatch', state: action === 'stop' ? 'running' : 'stopped', errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' } : { containerId, outcome: 'unknown', state: 'unknown', errorCode: 'JOB_EXECUTION_INTERRUPTED' }
      : { containerId, outcome: 'succeeded', state: action === 'stop' ? 'stopped' : 'running' }) },
  }
}
async function frozenIDs(page: Page, expected: string[]) {
  const details = panel(page).locator('.frozen-details')
  if (!await details.evaluate(element => (element as HTMLDetailsElement).open)) await details.locator('summary').click()
  await expect(panel(page).getByRole('list', { name: '冻结的容器 ID', exact: true }).locator('li')).toHaveText(expected)
}
async function guards(page: Page, api: Fixture) {
  const writes = api.writes.length
  for (const action of ['start', 'stop', 'restart'] as const) await expect(actionButton(page, action)).toBeDisabled()
  await page.getByRole('button', { name: `查看 Docker 项目 ${name}`, exact: true }).filter({ visible: true }).click()
  const drawer = page.locator('.project-drawer')
  for (const label of ['启动', '停止', '重启']) await expect(drawer.locator('.project-lifecycle__actions').getByRole('button', { name: label, exact: true })).toBeDisabled()
  for (const label of ['停止', '重启']) await expect(drawer.getByRole('button', { name: `${label} ${name}-0`, exact: true })).toBeDisabled()
  await page.keyboard.press('Escape'); await expect(drawer).not.toBeVisible()
  await page.getByRole('button', { name: '容器', exact: true }).click()
  const containers = page.locator(page.viewportSize()!.width === 390 ? '.container-mobile-list' : '.container-panel')
  const buttons = containers.getByRole('button', { name: page.viewportSize()!.width === 390 ? /^(启动|停止|重启)$/ : /^(启动|停止|重启)容器$/ })
  expect(await buttons.count()).toBeGreaterThan(0)
  for (const button of await buttons.all()) await expect(button).toBeDisabled()
  await page.getByRole('button', { name: '项目', exact: true }).click(); expect(api.writes).toHaveLength(writes)
}
for (const width of [390, 1440]) {
  test(`${width}px exact202 frozen subset queued running pending wrong ID full completion`, async ({ page, baseURL }, info) => {
    test.setTimeout(240_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); api.stage('confirmation-frozen-subset')
      await actionButton(page, 'restart').focus(); await page.keyboard.press('Enter')
      for (const id of ids.slice(0, 2)) await expect(dialog(page)).toContainText(id)
      await expect(dialog(page)).not.toContainText(ids[2]!); expect(api.writes).toHaveLength(0)
      await capture(page, info, api, 'frozen-confirmation', true)
      api.allow('restart'); await dialog(page).getByRole('button', { name: '确认重启', exact: true }).click()
      await expect(panel(page)).toContainText('排队中'); await expect(check(page)).toBeEnabled()
      await frozenIDs(page, ids.slice(0, 2)); await expect(release(page)).toBeDisabled()
      await guards(page, api); await capture(page, info, api, 'queued')
      for (const phase of ['running', 'pending', 'wrong-id', 'read-failed'] as const) {
        api.stage(phase); api.state.job = { ...receipt(), status: 'running', ...(phase === 'pending' ? { persistenceStatus: 'pending' } : {}), ...(phase === 'wrong-id' ? { id: 'unrelated-exact-job' } : {}) }
        api.state.jobStatus = phase === 'read-failed' ? 503 : 200
        await readonlyCheck(page); await expect(release(page)).toBeDisabled()
        await expect(panel(page)).toContainText(phase === 'running' ? '执行中' : phase === 'pending' ? '结果保存中' : '任务读取失败')
        await expect(panel(page)).toContainText('任务 ID：fixture-restart-job'); await frozenIDs(page, ids.slice(0, 2)); await capture(page, info, api, phase)
      }
      api.stage('inventory-changed-not-new-scope'); api.state.jobStatus = 200; api.state.job = receipt()
      api.state.inventory.containers[2]!.state = 'running'; api.state.inventory.containers[2]!.status = 'Up'; api.state.inventory.projects[0]!.state = 'running'; api.state.inventory.projects[0]!.runningCount = 3; api.state.inventory.engine.containersRunning = 3; api.state.inventory.engine.containersStopped = 0; await refresh(page, api); await frozenIDs(page, ids.slice(0, 2)); await guards(page, api)
      api.stage('full-saved-result'); api.state.job = terminal('restart'); await check(page).click()
      await expect(panel(page).getByRole('status', { name: '已确认的项目操作结果', exact: true })).toBeVisible()
      await expect(check(page)).toHaveCount(0); await expect(actionButton(page, 'restart')).toBeEnabled()
      await expect(panel(page).getByRole('list', { name: '已保存的逐项结果', exact: true }).locator('li .identity')).toHaveText(ids.slice(0, 2).map(id => `容器 ID：${id}`))
      await expect(panel(page)).toContainText('不保证容器持续运行'); await capture(page, info, api, 'full-completion')
      expect(api.writes).toHaveLength(1); api.state.journeyCompleted = true
    } finally { await finish(page, info, api) }
  })
  for (const outcome of ['failed', 'interrupted'] as const) test(`${width}px partial ${outcome} retains lock risk cancel focus zero-write release`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); await submit(page, api, 'stop'); await expect(check(page)).toBeEnabled()
      api.stage('partial-result-inventory-failure'); api.state.job = terminal('stop', outcome); api.state.inventoryFails = true
      await readonlyCheck(page); await expect(release(page)).toBeDisabled(); await frozenIDs(page, ids.slice(0, 2))
      const result = panel(page).getByRole('list', { name: '任务逐项结果', exact: true })
      await expect(result.locator('li .identity')).toHaveText(ids.slice(0, 2).map(id => `容器 ID：${id}`))
      await expect(result).toContainText(outcome === 'failed' ? '回读与目标状态不符' : '执行结果未知')
      await expect(panel(page)).toContainText('部分符合目标不代表全部成功'); await capture(page, info, api, `${outcome}-inventory-failed`)
      api.stage('partial-successful-read'); api.state.inventoryFails = false; await readonlyCheck(page)
      await expect(release(page)).toBeEnabled(); await capture(page, info, api, `${outcome}-checked`); await guards(page, api)
      api.stage('risk-cancel'); await risk(page, info, api, `${outcome}-cancel`, false)
      api.stage('risk-release'); await risk(page, info, api, `${outcome}-release`, true)
      await expect(actionButton(page, 'restart')).toBeEnabled(); expect(api.writes).toHaveLength(1); api.state.journeyCompleted = true
    } finally { await finish(page, info, api) }
  })
  test(`${width}px lost202 three filtered lists history never claimed sticky active failed reads`, async ({ page, baseURL }, info) => {
    test.setTimeout(240_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); api.stage('lost202'); await submit(page, api, 'restart', 'lost')
      await expect(panel(page)).toContainText('接收结果未知'); await expect(panel(page)).not.toContainText('任务 ID：'); await expect(release(page)).toBeDisabled()
      await capture(page, info, api, 'lost202'); api.stage('route-roundtrip'); await navigate(page, '站点管理'); await navigate(page, 'Docker')
      await expect(panel(page)).toContainText('接收结果未知'); await guards(page, api)
      api.stage('unknown-failed-reads'); api.state.jobsFail = true; api.state.inventoryFails = true; await readonlyCheck(page)
      await expect(release(page)).toBeDisabled(); await capture(page, info, api, 'unknown-read-failed')
      api.stage('active-observed-unbound'); api.state.jobsFail = false; api.state.inventoryFails = false
      api.state.jobs = [{ ...receipt('start'), id: 'unrelated-active-job', status: 'running' }]
      const before = api.requests.length; await readonlyCheck(page)
      expect(api.requests.slice(before).filter(row => row.target.startsWith('/api/v1/jobs?')).map(row => row.target).sort()).toEqual(['restart', 'start', 'stop'].map(action => `/api/v1/jobs?type=docker-project-${action}`))
      await expect(panel(page)).toContainText('观察到活动项目任务'); await expect(panel(page)).not.toContainText('任务 ID：'); await expect(release(page)).toBeDisabled(); await capture(page, info, api, 'active-unbound')
      api.stage('sticky-active-read-failure'); api.state.jobsFail = true; await readonlyCheck(page); await expect(release(page)).toBeDisabled()
      await expect(panel(page)).toContainText('观察到活动项目任务'); await capture(page, info, api, 'sticky-read-failure')
      api.stage('saved-history-not-receipt'); api.state.jobsFail = false; api.state.jobs = [{ ...terminal('restart'), id: 'history-only-completed' }]
      await readonlyCheck(page); await expect(release(page)).toBeEnabled(); await expect(panel(page)).not.toContainText('任务 ID：')
      expect(api.requests.some(row => row.target === '/api/v1/jobs/history-only-completed')).toBe(false)
      await capture(page, info, api, 'historical-result-unclaimed'); await risk(page, info, api, 'unknown-release', true)
      expect(api.writes).toHaveLength(1); api.state.journeyCompleted = true
    } finally { await finish(page, info, api) }
  })
  for (const newSession of [false, true]) test(`${width}px late202 ${newSession ? 'session isolation' : 'SPA ownership absent target'}`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000); await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, new URL(baseURL!).origin)
    try {
      await open(page); api.stage('held-submit'); await submit(page, api, 'stop', 'held'); await api.held.received.promise
      await expect(panel(page)).toContainText('正在提交'); await expect(check(page)).toBeDisabled(); await capture(page, info, api, 'submitting')
      if (newSession) {
        api.stage('session-replaced'); api.allowSessionSwitch(); if (width === 390) await page.getByRole('button', { name: '打开主菜单', exact: true }).click()
        await page.getByRole('button', { name: '退出登录', exact: true }).click()
        await page.getByLabel('账号', { exact: true }).fill('root'); await page.getByLabel('密码', { exact: true }).fill('isolated-fixture-only')
        await page.getByRole('button', { name: '进入控制台' }).click(); await expect(page.locator('.navigation')).toBeAttached()
        if (new URL(page.url()).pathname !== '/docker') await navigate(page, 'Docker')
        await expect(panel(page)).toHaveCount(0); await submit(page, api, 'restart', 'lost'); await expect(panel(page)).toContainText('接收结果未知')
      } else { await navigate(page, '站点管理'); await navigate(page, 'Docker'); await expect(panel(page)).toContainText('正在提交') }
      api.stage('late-old-receipt'); api.held.release.resolve(); await api.held.delivered.promise
      await page.evaluate(() => new Promise<void>(done => requestAnimationFrame(() => requestAnimationFrame(() => done()))))
      if (newSession) {
        await expect(panel(page)).toContainText('接收结果未知'); await expect(panel(page)).not.toContainText('任务 ID：fixture-stop-job')
        await expect(actionButton(page, 'restart')).toBeDisabled(); await capture(page, info, api, 'new-session-unknown-isolated')
      } else {
        await expect(panel(page)).toContainText('任务 ID：fixture-stop-job'); await expect(panel(page)).toContainText('排队中')
        api.stage('targets-absent'); api.state.inventory.containers = []; api.state.inventory.projects = []
        api.state.inventory.engine.containers = 0; api.state.inventory.engine.containersRunning = 0; api.state.inventory.engine.containersStopped = 0
        await refresh(page, api); await frozenIDs(page, ids.slice(0, 2)); await readonlyCheck(page); await expect(release(page)).toBeDisabled()
        await capture(page, info, api, 'absent-target-still-checkable')
      }
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      expect(api.writes.filter(row => row.target.includes('/action-jobs/'))).toHaveLength(newSession ? 2 : 1); api.state.journeyCompleted = true
    } finally { api.held.release.resolve(); await finish(page, info, api) }
  })
}

// 大范围只截可见视窗；逐项横向 Range 检查保留完整 ID，不把128项缩成不可读的巨图。
test('390px 128 full IDs bounded confirmation keyboard cancel zero writes', async ({ page, baseURL }, info) => {
  test.setTimeout(120_000); await page.setViewportSize({ width: 390, height: 900 })
  const api = await fixture(page, new URL(baseURL!).origin)
  try {
    const fullIDs = Array.from({ length: 128 }, (_, i) => (i + 1).toString(16).padStart(64, '0'))
    api.state.inventory.containers = fullIDs.map((id, index) => ({ ...inventory.containers[0]!, id, name: `${name}-${index}`, state: 'running' }))
    api.state.inventory.projects[0]!.state = 'running'; api.state.inventory.projects[0]!.containerCount = 128; api.state.inventory.projects[0]!.runningCount = 128
    api.state.inventory.engine.containers = 128; api.state.inventory.engine.containersRunning = 128; api.state.inventory.engine.containersStopped = 0
    await open(page); api.stage('128-confirmation'); await actionButton(page, 'restart').focus(); await page.keyboard.press('Enter')
    await expect(dialog(page)).toContainText('范围：128 个容器'); const message = dialog(page).locator('.el-message-box__message')
    for (const id of fullIDs) await expect(message).toContainText(id)
    await waitForPageReady(page); await page.evaluate(() => document.fonts.ready)
    const measureLarge = () => dialog(page).evaluate(root => {
      const message = root.querySelector<HTMLElement>('.el-message-box__message')!, box = message.getBoundingClientRect()
      const walker = document.createTreeWalker(message, NodeFilter.SHOW_TEXT), ranges: { text: string | null; rects: ReturnType<DOMRect['toJSON']>[] }[] = []
      while (walker.nextNode()) { const range = document.createRange(); range.selectNodeContents(walker.currentNode); ranges.push({ text: walker.currentNode.textContent, rects: [...range.getClientRects()].map(rect => rect.toJSON()) }) }
      const active = document.activeElement, focusStyle = active ? getComputedStyle(active) : null
      return { focus: { tag: active?.tagName, text: active?.textContent, contained: !!active && root.contains(active), visible: active?.matches(':focus-visible'), outlineWidth: focusStyle?.outlineWidth, outlineStyle: focusStyle?.outlineStyle, outlineColor: focusStyle?.outlineColor }, box: box.toJSON(), scrollHeight: message.scrollHeight, clientHeight: message.clientHeight, scrollTop: message.scrollTop, overflowY: getComputedStyle(message).overflowY, viewport: { width: innerWidth, height: innerHeight }, rootWidth: document.documentElement.scrollWidth, ranges,
        controls: [...root.querySelectorAll<HTMLElement>('button')].map(element => { const r = element.getBoundingClientRect(); return { text: element.textContent, bounds: r.toJSON(), hits: [[r.left + 5,r.top + 5],[r.right - 5,r.top + 5],[r.left + 5,r.bottom - 5],[r.right - 5,r.bottom - 5],[r.left + r.width / 2,r.top + r.height / 2]].map(([x,y]) => { const hit = document.elementFromPoint(x!,y!); return !!hit && (hit === element || element.contains(hit)) }) } }) }
    })
    const before = await measureLarge(), requestsBefore = structuredClone(api.requests)
    const directory = info.outputPath('ui-captures'); await mkdir(directory, { recursive: true })
    const path = `${directory}/project-action-128-confirmation.png`, png = await page.screenshot({ path, animations: 'disabled' })
    const afterScreenshot = await measureLarge(), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await measureLarge()
    const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, fullIDs, before, afterScreenshot, afterAxe, screenshotSha256: hash(png), requestsBefore, requestsAfter: api.requests, axe }
    const metadataText = JSON.stringify(metadata, null, 2)
    await writeFile(`${directory}/project-action-128-confirmation-capture-state.json`, metadataText)
    api.captures.push({ file: 'project-action-128-confirmation-capture-state.json', sha256: hash(metadataText) })
    api.captures.push({ file: 'project-action-128-confirmation.png', sha256: hash(png) })
    await info.attach('128-confirmation', { path, contentType: 'image/png' })
    expect.soft(axe.violations).toEqual([]); expect.soft(afterScreenshot).toEqual(before); expect.soft(afterAxe).toEqual(before); expect.soft(api.requests).toEqual(requestsBefore)
    expect(before.scrollHeight).toBeGreaterThan(before.clientHeight); expect(before.box.height).toBeLessThanOrEqual(420); expect(before.overflowY).toMatch(/auto|scroll/); expect(before.rootWidth).toBeLessThanOrEqual(390)
    for (const field of before.ranges) for (const rect of field.rects) { expect.soft(rect.left).toBeGreaterThanOrEqual(before.box.left - 1); expect.soft(rect.right).toBeLessThanOrEqual(before.box.right + 1) }
    for (const control of before.controls) { expect.soft(control.bounds.height).toBeGreaterThanOrEqual(44); expect.soft(control.bounds.width).toBeGreaterThanOrEqual(44); expect.soft(control.hits).toEqual([true,true,true,true,true]) }
    await message.hover(); await page.mouse.wheel(0, 100_000); await expect.poll(async () => { const sample = await measureLarge(); return sample.scrollHeight - sample.clientHeight - sample.scrollTop }).toBeLessThanOrEqual(1)
    const bottom = await measureLarge(), bottomRequestsBefore = structuredClone(api.requests)
    const bottomPath = `${directory}/project-action-128-confirmation-bottom.png`, bottomPNG = await page.screenshot({ path: bottomPath, animations: 'disabled' })
    const bottomAfterScreenshot = await measureLarge(), bottomAxe = await new AxeBuilder({ page }).analyze(), bottomAfterAxe = await measureLarge()
    const bottomData = JSON.stringify({ ...await provenance(), fixtureOnly: true, bottom, bottomAfterScreenshot, bottomAfterAxe, axe: bottomAxe, screenshotSha256: hash(bottomPNG), requestsBefore: bottomRequestsBefore, requestsAfter: api.requests }, null, 2)
    await writeFile(`${directory}/project-action-128-confirmation-bottom-capture-state.json`, bottomData)
    api.captures.push({ file: 'project-action-128-confirmation-bottom.png', sha256: hash(bottomPNG) }, { file: 'project-action-128-confirmation-bottom-capture-state.json', sha256: hash(bottomData) })
    expect.soft(bottomAxe.violations).toEqual([]); expect.soft(bottomAfterScreenshot).toEqual(bottom); expect.soft(bottomAfterAxe).toEqual(bottom); expect.soft(api.requests).toEqual(bottomRequestsBefore)
    // 原生 Tab/Shift+Tab 在对话框循环；不通过 DOM.focus 伪造焦点可见证据。
    const keyboardCycle = []
    for (let i = 0; i < 8; i++) {
      await page.keyboard.press('Tab'); const focus = (await measureLarge()).focus; keyboardCycle.push(focus)
      expect.soft(focus.contained).toBe(true); expect.soft(focus.visible).toBe(true); expect.soft(parseFloat(focus.outlineWidth ?? '0')).toBeGreaterThanOrEqual(2); expect.soft(focus.outlineStyle).toBe('solid')
      if (await dialog(page).getByRole('button', { name: '取消', exact: true }).evaluate(element => element === document.activeElement)) break
    }
    const cycleData = JSON.stringify({ keyboardCycle }, null, 2)
    await writeFile(`${directory}/project-action-128-keyboard-capture-state.json`, cycleData); api.captures.push({ file: 'project-action-128-keyboard-capture-state.json', sha256: hash(cycleData) })
    await expect(dialog(page).getByRole('button', { name: '取消', exact: true })).toBeFocused(); await page.keyboard.press('Enter')
    await expect(dialog(page)).toHaveCount(0); await expect(actionButton(page, 'restart')).toBeFocused(); expect(api.writes).toEqual([]); api.state.journeyCompleted = true
  } finally { await finish(page, info, api) }
})
for (const width of [390, 1440]) test(`${width}px start freezes only stopped IDs and guards compose deletion creation buttons`, async ({ page, baseURL }, info) => {
  test.setTimeout(120_000); await page.setViewportSize({ width, height: 900 })
  const api = await fixture(page, new URL(baseURL!).origin)
  try {
    api.state.inventory.projects.push({ ...inventory.projects[0]!, id: 'compose:fixture-other', name: 'fixture-other-compose', kind: 'compose', state: 'stopped', runningCount: 0, containerCount: 1 })
    api.state.inventory.engine.containers = 4; api.state.inventory.engine.containersStopped = 2
    api.state.inventory.containers.push({ ...inventory.containers[2]!, id: 'd'.repeat(64), name: 'fixture-other-container', projectId: 'compose:fixture-other', projectName: 'fixture-other-compose' })
    await open(page)
    const composeRow = page.locator(width === 390 ? '.mobile-project:visible' : '.project-row:visible').filter({ hasText: 'fixture-other-compose' })
    const composeStart = width === 390 ? composeRow.getByRole('button', { name: '启动', exact: true }) : page.getByRole('button', { name: '启动项目 fixture-other-compose', exact: true })
    const composeDelete = width === 390 ? composeRow.getByRole('button', { name: '删除', exact: true }) : page.getByRole('button', { name: '删除项目 fixture-other-compose', exact: true })
    await expect(composeStart).toBeEnabled(); await expect(composeDelete).toBeEnabled()
    api.stage('start-only-stopped'); await submit(page, api, 'start'); await expect(check(page)).toBeEnabled(); await frozenIDs(page, [ids[2]!])
    await expect(composeStart).toBeDisabled(); await expect(composeDelete).toBeDisabled(); await guards(page, api)
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    await expect(page.getByRole('button', { name: '从镜像 fixture:latest 创建容器', exact: true })).toBeDisabled()
    expect(api.writes).toHaveLength(1); await page.getByRole('button', { name: '项目', exact: true }).click()
    api.stage('start-complete'); api.state.job = terminal('start'); await check(page).click()
    await expect(check(page)).toHaveCount(0); await expect(composeStart).toBeEnabled(); await expect(composeDelete).toBeEnabled()
    await capture(page, info, api, 'start-only-stopped-completed'); expect(api.writes).toHaveLength(1); api.state.journeyCompleted = true
  } finally { await finish(page, info, api) }
})
