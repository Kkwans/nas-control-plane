import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { overviewDocker, summary } from './mockApi'

// 严格内存合同，无 Docker/NAS/Job；静默 EventSource 不代表真实轮询验证。
// 自然流动 clock.install，不 freezeDate/pauseAt，不改写 store 制造状态。
test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const projectId = `compose:fixture-${'完整项目身份-'.repeat(16)}`
const projectName = '待核对项目'
const inventoryPath = '/api/v1/docker/inventory'
const deletePath = `/api/v1/docker/compose/projects/${encodeURIComponent(projectId)}`
const panel = (p: Page) => p.getByRole('region', { name: '项目删除防重复保护', exact: true })
const check = (p: Page) => panel(p).getByRole('button', { name: '只读核对项目清单', exact: true })
const release = (p: Page) => panel(p).getByRole('button', { name: '解除本会话删除保护', exact: true })
const deletion = (p: Page) => p.viewportSize()!.width < 768
  ? p.locator('.mobile-project:visible').filter({ hasText: projectName }).getByRole('button', { name: '删除', exact: true })
  : p.getByRole('button', { name: `删除项目 ${projectName}`, exact: true })
const receipt = () => ({ projectId, kind: 'compose', completed: true, partial: false, registryDeleted: true, registryRolledBack: false, containers: [{ containerId: 'fixture-stopped', name: 'fixture-stopped', state: 'stopped', deleted: true, success: true }] })
function inventory(present: boolean, collectedAt: string) {
  return { collectedAt,
    engine: { ...overviewDocker.engine, containers: present ? 1 : 0, containersRunning: 0, containersStopped: present ? 1 : 0 },
    containers: present ? [{ ...overviewDocker.containers[0]!, id: 'fixture-stopped', name: 'fixture-stopped', projectId, projectName, state: 'exited', status: 'Exited (0)', health: 'none', ports: [], createdAt: collectedAt, finishedAt: collectedAt }] : [],
    projects: present ? [{ ...overviewDocker.projects[0]!, id: projectId, name: projectName, state: 'stopped', kind: 'compose', workingDirectory: '/fixture/project', containerCount: 1, runningCount: 0 }] : [],
  }
}
function deferred() { let resolve!: () => void; const promise = new Promise<void>((done) => { resolve = done }); return { promise, resolve } }
type Mode = 'wrong-project' | 'contradiction' | 'lost' | 'success'
type Ledger = { sequence: number; method: string; path: string; query: string; body: unknown; response?: unknown; status?: number; outcome?: string }
async function fixture(page: Page, mode: Mode) {
  const baseURL = test.info().project.use.baseURL
  if (!baseURL) throw new Error('严格fixture需要已配置baseURL')
  const origin = new URL(baseURL).origin
  await page.clock.install({ time: new Date('2026-10-16T12:00:00Z') })
  await page.addInitScript((allowedOrigin) => {
    Object.defineProperty(window, '__projectDeletionDocumentUUID', { value: crypto.randomUUID() })
    class QuietSource extends EventTarget {
      static CONNECTING = 0; static OPEN = 1; static CLOSED = 2
      readyState = 1; url: string; withCredentials = false
      onopen = null; onmessage = null; onerror = null
      constructor(url: string) {
        super(); this.url = url
        const endpoint = new URL(url, location.href)
        if (endpoint.origin !== allowedOrigin || ![
          '/api/v1/system/events?interval=5&scope=summary&scope=docker',
          '/api/v1/system/events?interval=5&scope=docker&scope=summary',
          '/api/v1/system/events?interval=5&scope=summary',
          '/api/v1/system/events?interval=5&scope=docker',
        ].includes(endpoint.pathname + endpoint.search)) throw new Error(`Fixture refused EventSource: ${url}`)
      }
      close() { this.readyState = 2 }
    }
    window.EventSource = QuietSource as unknown as typeof EventSource
  }, origin)
  const ledger: Ledger[] = [], denied: string[] = [], pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  let present = true, failRead = false, authenticated = true, inventoryTimestamp = 0
  let gate: { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; completed: ReturnType<typeof deferred> } | undefined
  const user = { id: 1, username: 'root', role: 'root' }
  const reads = new Map<string, unknown>([
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'project-delete-fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/sites/ignored', []],
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  await page.routeWebSocket('**', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method(), path = url.pathname
    const item: Ledger = { sequence: ledger.length + 1, method, path, query: url.search, body: path === '/api/v1/auth/login' ? '[fixture credentials omitted]' : request.postData() }
    ledger.push(item)
    const deny = async () => { denied.push(`${method} ${url.href}`); item.outcome = 'blockedbyclient'; await route.abort('blockedbyclient') }
    if (url.origin !== origin) return deny()
    if (!path.startsWith('/api/')) {
      if (method === 'GET' && !url.search && (['/', '/docker', '/sites', '/favicon.svg'].includes(path) || /^\/assets\/[A-Za-z0-9_.-]+$/.test(path))) {
        item.outcome = 'allowed-same-origin-built-asset'; await route.continue(); return
      }
      return deny()
    }
    const send = async (response: unknown, status = 200) => { item.response = response; item.status = status; item.outcome = 'fulfilled'; await route.fulfill({ status, json: response }) }
    if (!url.search && method === 'GET') {
      const now = new Date(await page.evaluate(() => Date.now())).toISOString()
      if (path === '/api/v1/auth/status') return send({ initialized: true, authenticated, user: authenticated ? user : undefined })
      if (path === inventoryPath) {
        inventoryTimestamp = Math.max(inventoryTimestamp + 1, Date.parse(now))
        return failRead ? send({ code: 'AGENT_UNAVAILABLE', message: 'fixture read unavailable', requestId: 'fixture-read' }, 503) : send(inventory(present, new Date(inventoryTimestamp).toISOString()))
      }
      if (path === '/api/v1/system/summary') return send({ ...summary, collectedAt: now })
      if (path === '/api/v1/sites') return send({ collectedAt: now, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } })
      if (reads.has(path)) return send(reads.get(path))
    }
    if (!url.search && method === 'POST' && path === '/api/v1/auth/logout') { authenticated = false; item.status = 204; item.outcome = 'fulfilled'; return route.fulfill({ status: 204 }) }
    if (!url.search && method === 'POST' && path === '/api/v1/auth/login') { authenticated = true; return send({ user, expiresAt: '2026-10-17T12:00:00Z' }) }
    if (!url.search && method === 'DELETE' && path === deletePath) {
      expect(request.postDataJSON()).toEqual({ projectId, kind: 'compose', registryName: projectName })
      const responseMode = mode
      const held = gate; gate = undefined
      if (held) { held.received.resolve(); await held.release.promise }
      try {
        if (responseMode === 'lost') { item.outcome = 'connectionfailed'; await route.abort('connectionfailed'); return }
        const response = responseMode === 'wrong-project' ? { ...receipt(), projectId: 'compose:another' } : responseMode === 'contradiction' ? { ...receipt(), partial: true } : receipt()
        if (responseMode === 'success') present = false
        await send(response)
      } finally { held?.completed.resolve() }
      return
    }
    denied.push(`${method} ${path}${url.search}`)
    await send({ code: 'FIXTURE_DENIED', message: '未在精确白名单内' }, 503)
  })
  return { ledger, denied, pageErrors,
    writes: () => ledger.filter(item => item.method !== 'GET' && !item.path.startsWith('/api/v1/auth/')),
    reads: () => ledger.filter(item => item.method === 'GET' && item.path === inventoryPath),
    respondWith(value: Mode) { mode = value },
    catalog(value: boolean) { present = value }, failRead(value: boolean) { failRead = value },
    hold() { expect(gate).toBeUndefined(); gate = { received: deferred(), release: deferred(), completed: deferred() }; return gate },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function documentUUID(page: Page) { return page.evaluate(() => (window as unknown as { __projectDeletionDocumentUUID: string }).__projectDeletionDocumentUUID) }
async function menu(page: Page) { const button = page.getByRole('button', { name: '打开主菜单', exact: true }); if (await button.isVisible()) await button.click() }
async function navigate(page: Page, path: '/sites' | '/docker') { await menu(page); await page.locator(`.navigation a[href="${path}"]`).click(); await expect(page).toHaveURL(new RegExp(`${path}$`)) }
async function submit(page: Page) { await deletion(page).click(); await page.getByRole('button', { name: '删除项目', exact: true }).click() }
async function keyboardFocus(page: Page, target: Locator) {
  // 真实 Tab 序列，不用 element.focus 假装键盘可达。
  for (let i = 0; i < 100; i++) { await page.keyboard.press('Tab'); if (await target.evaluate(element => element === document.activeElement)) return }
  throw new Error('100 次 Tab 内目标不可达')
}
async function geometry(target: Locator) {
  return target.evaluate(element => {
    const rect = element.getBoundingClientRect(), style = getComputedStyle(element)
    const points = [[.5, .5], [.15, .15], [.85, .15], [.15, .85], [.85, .85]].map(([x, y]) => {
      const px = rect.left + rect.width * x!, py = rect.top + rect.height * y!, hit = document.elementFromPoint(px, py)
      return { x: px, y: py, hit: hit === element || !!hit && element.contains(hit) }
    })
    const range = document.createRange(); range.selectNodeContents(element)
    const clips = []
    for (let parent = element.parentElement; parent; parent = parent.parentElement) {
      const css = getComputedStyle(parent)
      if (/(hidden|clip|auto|scroll)/.test(`${css.overflowX} ${css.overflowY}`)) clips.push({ tag: parent.tagName, className: parent.className, rect: parent.getBoundingClientRect().toJSON(), overflowX: css.overflowX, overflowY: css.overflowY })
    }
    const inViewport = rect.left >= 0 && rect.top >= 0 && rect.right <= innerWidth && rect.bottom <= innerHeight
    const unclipped = clips.every(clip => rect.left >= clip.rect.left - 1 && rect.right <= clip.rect.right + 1 && rect.top >= clip.rect.top - 1 && rect.bottom <= clip.rect.bottom + 1)
    return { viewport: { width: innerWidth, height: innerHeight }, scroll: { x: scrollX, y: scrollY }, clips, inViewport, unclipped,
      text: element.textContent, rect: rect.toJSON(), points, lines: [...range.getClientRects()].map(r => r.toJSON()), scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
      focused: document.activeElement === element, outline: style.outline, visible: rect.width > 0 && rect.height > 0 && style.visibility !== 'hidden' }
  })
}
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = [...new Set(git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/index.html', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).trim().split('\n'))].sort()
  return { head: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes: Object.fromEntries(await Promise.all(files.map(async file => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')]))), diffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex') }
}
function journey(page: Page, info: TestInfo, api: Fixture) {
  let stage = 'fixture-installed', completed = false
  const captures: unknown[] = [], documents: string[] = []
  return {
    stage(value: string) { stage = value },
    async document() { const id = await documentUUID(page); documents.push(id); return id },
    async capture(name: string) {
      await page.evaluate(() => document.fonts.ready)
      // 真实退场与自然消失；不隐藏drawer、tooltip或toast，也不改变axe范围。
      await page.mouse.move(1, 1)
      await expect(page.locator('.project-drawer:visible')).toHaveCount(0)
      await expect(page.locator('.project-drawer-modal:visible')).toHaveCount(0)
      await expect(page.locator('.el-popper[role=tooltip]:visible')).toHaveCount(0)
      await expect(page.locator('.el-message:visible')).toHaveCount(0, { timeout: 10_000 })
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      await keyboardFocus(page, check(page))
      await panel(page).scrollIntoViewIfNeeded()
      const beforeRequests = structuredClone(api.ledger)
      const axe = await new AxeBuilder({ page }).analyze() // full-page violations/incomplete 原样保存。
      // 所有几何在同一个截图滚动位置采集，不逐按钮滚动后拼接证据。
      const beforeScroll = await page.evaluate(() => ({ x: scrollX, y: scrollY }))
      const controls = await Promise.all([check(page), release(page)].map(geometry))
      const focus = controls[0]!
      const text = await geometry(panel(page).locator('.deletion-protection__status p').first())
      const png = info.outputPath('ui-captures', `${name}.png`)
      await page.screenshot({ path: png, fullPage: true, animations: 'disabled' })
      const afterScroll = await page.evaluate(() => ({ x: scrollX, y: scrollY }))
      const afterRequests = structuredClone(api.ledger)
      const pngSha256 = createHash('sha256').update(await readFile(png)).digest('hex')
      await info.attach(name, { path: png, contentType: 'image/png' })
      captures.push({ name, png, pngSha256, controls, focus, text, axe, beforeRequests, afterRequests, beforeScroll, afterScroll, documentUUID: await documentUUID(page) })
      expect(afterRequests).toEqual(beforeRequests); expect(afterScroll).toEqual(beforeScroll)
      for (const control of controls) { expect(control.visible).toBe(true); expect(control.inViewport).toBe(true); expect(control.unclipped).toBe(true); expect(control.scroll).toEqual(beforeScroll); expect(control.rect.height).toBeGreaterThanOrEqual(44); expect(control.points.every(point => point.hit), JSON.stringify(control)).toBe(true); expect(control.scrollWidth).toBeLessThanOrEqual(control.clientWidth + 1) }
      expect(focus.focused).toBe(true); expect(focus.outline).not.toContain('none')
      expect(text.text).toContain(projectId); expect(text.scrollWidth).toBeLessThanOrEqual(text.clientWidth + 1)
      for (const line of text.lines) { expect(line.left).toBeGreaterThanOrEqual(text.rect.left - 1); expect(line.right).toBeLessThanOrEqual(text.rect.right + 1) }
      expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
    },
    finish() { expect(api.denied).toEqual([]); expect(api.pageErrors).toEqual([]); completed = true; stage = 'all-assertions-complete' },
    async save() {
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      await writeFile(info.outputPath('ui-captures', 'project-deletion-capture-state.json'), JSON.stringify({ fixtureOnly: true, realNAS: false, realPollingValidated: false, clock: 'clock.install with naturally flowing time', stage, journeyCompleted: completed && info.errors.length === 0, errors: info.errors.map(error => error.message), viewport: page.viewportSize(), documents, captures, requestLedger: api.ledger, denied: api.denied, pageErrors: api.pageErrors, provenance: await provenance() }, null, 2))
    },
  }
}
for (const width of [390, 1440]) test.describe(`项目删除会话保护 ${width}`, () => {
  test.use({ viewport: { width, height: 1100 } })
  for (const mode of ['wrong-project', 'contradiction', 'lost'] as const) test(`${mode} 保留保护、只读核对与显式解除`, async ({ page }, info) => {
    const api = await fixture(page, mode), evidence = journey(page, info, api)
    try {
      await page.goto('/docker'); await expect(deletion(page)).toBeEnabled(); const id = await evidence.document()
      evidence.stage('cancel-before-delete'); await keyboardFocus(page, deletion(page)); await page.keyboard.press('Enter')
      await page.getByRole('button', { name: '取消', exact: true }).click(); await expect(deletion(page)).toBeFocused(); expect(api.writes()).toHaveLength(0)
      evidence.stage('unknown-lock'); await submit(page)
      await expect(panel(page)).toContainText('删除结果未知，防重复保护仍保留'); await expect(deletion(page)).toBeDisabled(); await expect(release(page)).toBeDisabled(); expect(api.writes()).toHaveLength(1)
      await page.getByRole('button', { name: `查看 Docker 项目 ${projectName}`, exact: true }).filter({ visible: true }).click()
      await page.locator('.project-drawer:visible button.el-drawer__close-btn').click(); await expect(panel(page)).toBeVisible(); await expect(deletion(page)).toBeDisabled()
      evidence.stage('unknown-accessibility'); await evidence.capture(`unknown-${mode}-${width}`)
      evidence.stage('failed-read-keeps-protection'); api.failRead(true); await check(page).click()
      await expect(panel(page)).toContainText('本轮清单读取失败，继续保留保护。'); await expect(release(page)).toBeDisabled()
      evidence.stage('empty-inventory-is-not-proof'); api.failRead(false); api.catalog(false); await check(page).click()
      await expect(panel(page)).toContainText('本轮清单读取成功'); await expect(release(page)).toBeEnabled(); await expect(deletion(page)).toHaveCount(0); await expect(panel(page)).toContainText(projectId)
      evidence.stage('spa-return-without-target'); await navigate(page, '/sites'); await navigate(page, '/docker')
      expect(await evidence.document()).toBe(id); await expect(panel(page)).toBeVisible(); await expect(panel(page)).toContainText('删除结果未知'); expect(api.writes()).toHaveLength(1)
      evidence.stage('same-id-reappears-still-locked'); api.catalog(true); await check(page).click(); await expect(deletion(page)).toBeDisabled()
      evidence.stage('release-cancel-focus'); await keyboardFocus(page, release(page)); await page.keyboard.press('Enter')
      await expect(page.getByRole('dialog', { name: '解除项目删除保护' })).toContainText('再次删除可能重复执行')
      await page.getByRole('button', { name: '保留保护', exact: true }).click(); await expect(release(page)).toBeFocused(); await expect(deletion(page)).toBeDisabled(); expect(api.writes()).toHaveLength(1)
      evidence.stage('release-does-not-write'); const before = api.ledger.length
      await release(page).click(); await page.getByRole('button', { name: '确认风险并解除', exact: true }).click()
      await expect(panel(page)).toHaveCount(0); await expect(deletion(page)).toBeEnabled(); expect(api.ledger.slice(before).every(item => item.method === 'GET')).toBe(true); expect(api.writes()).toHaveLength(1); evidence.finish()
    } finally { await evidence.save() }
  })
  test('严格成功一次 DELETE 后只读清单', async ({ page }, info) => {
    const api = await fixture(page, 'success'), evidence = journey(page, info, api)
    try {
      await page.goto('/docker'); await expect(deletion(page)).toBeEnabled(); await evidence.document(); evidence.stage('strict-success-receipt'); const before = api.reads().length
      await submit(page); await expect(deletion(page)).toHaveCount(0); await expect(panel(page)).toHaveCount(0); await expect.poll(() => api.reads().length).toBeGreaterThan(before)
      expect(api.writes()).toHaveLength(1); const index = api.ledger.findIndex(item => item.method === 'DELETE')
      expect(api.ledger.slice(index + 1).every(item => item.method === 'GET')).toBe(true); expect(api.ledger[index]!.response).toEqual(receipt()); evidence.finish()
    } finally { await evidence.save() }
  })
  for (const newSession of [false, true]) test(`迟到成功隔离 ${newSession ? '新登录会话' : '旧路由视图'}`, async ({ page }, info) => {
    const api = await fixture(page, 'success'), evidence = journey(page, info, api), held = api.hold()
    try {
      await page.goto('/docker'); await expect(deletion(page)).toBeEnabled(); const id = await evidence.document()
      evidence.stage('held-delete'); await submit(page); await held.received.promise; await expect(panel(page)).toContainText('正在提交删除')
      if (newSession) {
        evidence.stage('new-session-before-old-receipt'); await menu(page); await page.getByRole('button', { name: '退出登录', exact: true }).click()
        await page.getByLabel('账号', { exact: true }).fill('root'); await page.getByLabel('密码', { exact: true }).fill('isolated-fixture-only'); await page.getByRole('button', { name: '进入控制台' }).click()
        await expect(page.locator('.navigation')).toBeAttached(); if (!new URL(page.url()).pathname.startsWith('/docker')) await navigate(page, '/docker')
        await expect(deletion(page)).toBeEnabled(); await expect(panel(page)).toHaveCount(0)
        api.respondWith('wrong-project'); await submit(page)
        await expect(panel(page)).toContainText('删除结果未知，防重复保护仍保留'); await expect(deletion(page)).toBeDisabled()
      } else {
        await navigate(page, '/sites'); await navigate(page, '/docker'); await expect(panel(page)).toContainText('正在提交删除'); await expect(deletion(page)).toBeDisabled()
      }
      expect(await evidence.document()).toBe(id); evidence.stage('late-receipt-does-not-refresh-or-toast'); const reads = api.reads().length
      const browserConsumedDelete = page.waitForEvent('requestfinished', { predicate: request => request.method() === 'DELETE' && new URL(request.url()).pathname === deletePath })
      held.release.resolve(); await held.completed.promise; await browserConsumedDelete
      await page.evaluate(() => new Promise<void>(done => requestAnimationFrame(() => requestAnimationFrame(() => done()))))
      if (newSession) await expect(panel(page)).toContainText('删除结果未知，防重复保护仍保留')
      else await expect(panel(page)).toHaveCount(0)
      // 排空帧与自然运行计时器，仅读隔离 fixture；不声称验证真实 SSE/poll。
      await page.clock.runFor(100); expect(api.reads()).toHaveLength(reads)
      if (newSession) await expect(deletion(page)).toBeDisabled()
      else await expect(deletion(page)).toBeEnabled()
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      expect(api.writes()).toHaveLength(newSession ? 2 : 1); evidence.finish()
    } finally { held.release.resolve(); await evidence.save() }
  })
})
