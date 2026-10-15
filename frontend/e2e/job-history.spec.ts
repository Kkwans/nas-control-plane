import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { parseJobHistory } from '../src/api/jobHistory'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const endpoint = '/api/v1/job-history'
const stamp = '2026-10-14T08:09:10Z'
const longId = `z-legacy-${'a0123456789'.repeat(22)}`
const unknownType = 'future-maintenance'
const nextCursor = Buffer.from(JSON.stringify({ v: 1, type: '', createdAtUnix: String(Date.parse(stamp) / 1000), id: 'saved-0001' })).toString('base64url')
const notice = '仅显示最后保存的记录，可能落后于当前执行；不代表当前资源状态'
const saved = (id = longId, type = unknownType) => ({ id, type, status: 'future-paused', reference: `保存记录 ${id}`, message: '最后保存的消息 <script>不得执行</script>', createdAt: stamp, updatedAt: stamp })
const envelope = (items: ReturnType<typeof saved>[], nextCursor?: string) => ({ source: 'persisted', items, ...(nextCursor ? { nextCursor } : {}) })
const firstPage = () => {
  const payload = envelope(Array.from({ length: 50 }, (_, i) => saved(i === 0 ? longId : `saved-${String(50 - i).padStart(4, '0')}`)), nextCursor)
  expect(parseJobHistory(payload, { type: '', limit: 50 })).toEqual(payload)
  const ids = payload.items.map(item => item.id)
  expect(ids).toEqual([...ids].sort((a, b) => Buffer.compare(Buffer.from(b), Buffer.from(a))))
  expect(JSON.parse(Buffer.from(nextCursor, 'base64url').toString('utf8')).id).toBe(ids.at(-1))
  expect(Buffer.compare(Buffer.from('older-page'), Buffer.from(ids.at(-1)!))).toBeLessThan(0)
  return payload
}
// Pure protocol/oracle check also runs under --list; no browser or listener required.
firstPage()
const history = (page: Page) => page.getByRole('region', { name: '已保存任务历史', exact: true })
const drawer = (page: Page) => page.locator('.task-center-drawer')
const button = (page: Page, name: string) => drawer(page).getByRole('button', { name, exact: true })
const query = (limit = 50, type = '', cursor?: string) => {
  const params = new URLSearchParams({ limit: String(limit) })
  if (type) params.set('type', type)
  if (cursor) params.set('cursor', cursor)
  return `${endpoint}?${params}`
}
function deferred() { let resolve!: () => void; const promise = new Promise<void>((done) => { resolve = done }); return { promise, resolve } }
type Reply = { target: string; body: unknown; status?: number; held?: { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; delivered: ReturnType<typeof deferred> } }

// The shared data fixture is behind an exact-origin, GET-only allowlist. Its permissive
// mutation/auth handlers cannot be reached; unknown APIs and all sockets fail closed.
async function fixture(page: Page, origin: string) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: string[] = [], denied: string[] = [], errors: string[] = [], replies: Reply[] = []
  const expectedHistory: string[] = [], actualHistory: string[] = []
  const captures: Array<{ state: string; sha256: string }> = []
  const reads = new Set(['/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities', '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/sites', '/api/v1/sites/ignored'])
  page.on('pageerror', error => errors.push(error.message))
  await page.routeWebSocket('**', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), target = `${request.method()} ${url.pathname}${url.search}`
    if (url.pathname.startsWith('/api/')) requests.push(target)
    if (url.origin !== origin || request.method() !== 'GET') { denied.push(target); return route.abort('blockedbyclient') }
    if (url.pathname === endpoint) {
      actualHistory.push(url.pathname + url.search)
      const reply = replies.shift()
      if (!reply || reply.target !== url.pathname + url.search) { denied.push(target); return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED' } }) }
      if (reply.held) { reply.held.received.resolve(); await reply.held.release.promise }
      try { await route.fulfill({ status: reply.status ?? 200, headers: { 'Cache-Control': 'no-store' }, json: reply.body }) } finally { reply.held?.delivered.resolve() }
      return
    }
    if (url.pathname === '/api/v1/jobs' && !url.search) return route.fulfill({ json: { jobs: [] } })
    if (reads.has(url.pathname + url.search)) return route.fallback()
    if (!url.search && (url.pathname === '/' || url.pathname === '/favicon.svg' || /^\/assets\/[A-Za-z0-9_.-]+$/.test(url.pathname))) return route.fallback()
    denied.push(target); return route.abort('blockedbyclient')
  })
  return { requests, denied, errors, captures, expectedHistory, actualHistory,
    enqueue(body: unknown, target = query(), status = 200, hold = false) {
      const held = hold ? { received: deferred(), release: deferred(), delivered: deferred() } : undefined
      replies.push({ target, body, status, held }); expectedHistory.push(target); return held
    },
    assert() { expect(actualHistory).toEqual(expectedHistory); expect(replies).toHaveLength(0); expect(denied).toEqual([]); expect(errors).toEqual([]); expect(requests.every(item => item.startsWith('GET '))).toBe(true) },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function open(page: Page) {
  await page.goto('/')
  await expect(page.getByRole('button', { name: '任务', exact: true })).toBeVisible()
  // Observe every subscription after bootstrap, including subscriptions hidden by the
  // HTTP mock. History must not instantiate even a fixture EventSource.
  await page.evaluate(() => {
    const Original = window.EventSource
    const host = window as typeof window & { __historyStreams: string[] }
    host.__historyStreams = []
    window.EventSource = class extends Original {
      constructor(url: string | URL, options?: EventSourceInit) { host.__historyStreams.push(String(url)); super(url, options) }
    }
  })
  await page.getByRole('button', { name: '任务', exact: true }).click()
  await expect(button(page, '刷新任务')).toBeEnabled()
  await button(page, '已保存历史').click()
  await expect(history(page)).toBeVisible()
}
const hash = (value: string | Buffer) => createHash('sha256').update(value).digest('hex')
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async file => [file, hash(await readFile(resolve(root, file)))]))) }
}
async function measure(page: Page, control?: Locator) {
  if (control) await control.evaluate(element => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }))
  return drawer(page).evaluate(element => {
    const box = element.getBoundingClientRect()
    return {
      bounds: box.toJSON(), rootFontSize: getComputedStyle(document.documentElement).fontSize,
      root: { width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth },
      controls: [...element.querySelectorAll('button,input,select,summary')].map(control => {
        const rect = control.getBoundingClientRect(), hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)
        return { label: control.getAttribute('aria-label') ?? control.textContent, bounds: rect.toJSON(), hit: !!hit && control.contains(hit), disabled: control.hasAttribute('disabled') }
      }),
      text: [...element.querySelectorAll('h3,dd,p,time')].map(field => {
        const rect = field.getBoundingClientRect(), range = document.createRange(); range.selectNodeContents(field)
        return { text: field.textContent, bounds: rect.toJSON(), overflow: field.scrollWidth - field.clientWidth, linesFit: [...range.getClientRects()].filter(line => line.width && line.height).every(line => line.left >= rect.left - 1 && line.right <= rect.right + 1) }
      }),
    }
  })
}
async function capture(page: Page, info: TestInfo, api: Fixture, phase: string, control?: Locator) {
  await page.evaluate(() => document.fonts.ready)
  const before = await measure(page, control)
  const name = `job-history-${page.viewportSize()!.width}-${phase}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, animations: 'disabled' })
  const after = await measure(page), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await measure(page)
  const state = `${name}-capture-state.json`, statePath = info.outputPath('ui-captures', state)
  await writeFile(statePath, JSON.stringify({ ...await provenance(), fixtureOnly: true, testTitle: info.title, phase, screenshot: `${name}.png`, screenshotSha256: hash(await readFile(png)), before, after, afterAxe, axe, requestLedger: [...api.requests] }, null, 2))
  api.captures.push({ state, sha256: hash(await readFile(statePath)) })
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  expect(axe.violations).toEqual([])
  expect(after.root.scrollWidth).toBeLessThanOrEqual(after.root.width + 1)
  expect(after.bounds.left).toBeGreaterThanOrEqual(-1)
  expect(after.bounds.right).toBeLessThanOrEqual(after.root.width + 1)
  for (const field of after.text.filter(field => field.bounds.width && field.bounds.height)) { expect(field.overflow, field.text ?? '').toBeLessThanOrEqual(1); expect(field.linesFit, field.text ?? '').toBe(true) }
  if (control) {
    await expect(control).toBeInViewport({ ratio: 1 })
    const metrics = await control.evaluate(element => { const rect = element.getBoundingClientRect(), hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2); return { width: rect.width, height: rect.height, hit: !!hit && element.contains(hit) } })
    expect(metrics.width).toBeGreaterThanOrEqual(44); expect(metrics.height).toBeGreaterThanOrEqual(44); expect(metrics.hit).toBe(true)
  }
}
async function finish(page: Page, info: TestInfo, api: Fixture) {
  api.assert()
  const streams = await page.evaluate(() => (window as typeof window & { __historyStreams: string[] }).__historyStreams)
  expect(streams.filter(url => url.includes('/jobs/') || url.includes('job-history'))).toEqual([])
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const path = info.outputPath('ui-captures', 'job-history-final-request-ledger-capture-state.json')
  await writeFile(path, JSON.stringify({ ...await provenance(), fixtureOnly: true, linkedCaptures: api.captures, requestLedger: api.requests, expectedHistory: api.expectedHistory, actualHistory: api.actualHistory, streams, denied: api.denied, errors: api.errors }, null, 2))
  await info.attach('final-history-request-ledger', { path, contentType: 'application/json' })
}

for (const width of [390, 1440]) {
  test(`${width}px saved history controls, unknown saved state and long ID at 100/200%`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    const api = await fixture(page, new URL(baseURL!).origin)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    api.enqueue(envelope([saved()]))
    await open(page)
    await expect(history(page)).toContainText(notice)
    const row = history(page).getByRole('article')
    await expect(row).toContainText(unknownType)
    await expect(row).toContainText('保存状态：future-paused')
    await expect(row).not.toContainText('已完成')
    await expect(row.locator('time')).toHaveAttribute('datetime', stamp)
    await expect(row.locator('time')).toContainText('记录更新时间')
    for (const scale of [1, 2]) {
      await page.addStyleTag({ content: `html { font-size: ${15 * scale}px !important; }` })
      const toggle = row.locator('summary')
      await capture(page, info, api, `font-${scale * 100}-summary`, toggle)
      if (await row.locator('details').getAttribute('open') === null) { await toggle.focus(); await page.keyboard.press('Enter') }
      await expect(row.locator('dd').first()).toHaveText(longId)
      await expect(row).toContainText('最后保存的消息 <script>不得执行</script>')
      await expect(row.locator('script')).toHaveCount(0)
      await row.locator('dd').first().evaluate(element => element.scrollIntoView({ block: 'center', behavior: 'instant' }))
      await expect(row.locator('dd').first()).toBeInViewport()
      await capture(page, info, api, `font-${scale * 100}-details`)
      await capture(page, info, api, `font-${scale * 100}-pagination`, button(page, '回到最新页'))
      let controlIndex = 0
      for (const control of [history(page).getByLabel('已保存任务类型', { exact: true }), history(page).getByLabel('每页条数', { exact: true }), history(page).getByLabel('其他类型精确查询（保留空格）', { exact: true }), button(page, '查询类型'), button(page, '关闭已保存历史')]) {
        await capture(page, info, api, `font-${scale * 100}-control-${controlIndex++}`, control)
      }
    }
    await button(page, '关闭已保存历史').click()
    await expect(drawer(page)).toHaveCount(0)
    await expect(page.getByRole('button', { name: '任务', exact: true })).toBeFocused()
    await finish(page, info, api)
  })
}

test('next-page failure retains current page, retry is identical; latest/type/size reset cursor', async ({ page, baseURL }, info) => {
  const api = await fixture(page, new URL(baseURL!).origin)
  api.enqueue(firstPage()); await open(page)
  await expect(history(page).getByRole('article')).toHaveCount(50)
  api.enqueue({ code: 'JOB_HISTORY_UNAVAILABLE' }, query(50, '', nextCursor), 503)
  await button(page, '下一页').click()
  await expect(history(page)).toContainText('读取失败，未切页')
  await expect(history(page).getByRole('article')).toHaveCount(50)
  await expect(history(page)).toContainText(longId)
  await expect(button(page, '下一页')).toBeDisabled()
  await capture(page, info, api, 'failed-next-retained', button(page, '重试同一读取'))
  api.enqueue(envelope([saved('older-page')]), query(50, '', nextCursor))
  await button(page, '重试同一读取').focus(); await page.keyboard.press('Enter')
  await expect(history(page).getByRole('article')).toHaveCount(1)
  await expect(history(page)).toBeFocused()
  await page.keyboard.press('Tab')
  expect(await drawer(page).evaluate(element => element.contains(document.activeElement))).toBe(true)
  await expect(history(page)).toContainText('older-page')
  await expect(history(page)).not.toContainText(longId)
  api.enqueue(envelope([saved('latest-again')]))
  await button(page, '回到最新页').click()
  await expect(history(page)).toContainText('latest-again')
  api.enqueue(envelope([saved('filtered', 'compose-deploy')]), query(50, 'compose-deploy'))
  await history(page).getByLabel('已保存任务类型', { exact: true }).selectOption('compose-deploy')
  await expect(history(page)).toContainText('filtered')
  api.enqueue(envelope([]), query(20, 'compose-deploy'))
  await history(page).getByLabel('每页条数', { exact: true }).selectOption('20')
  await expect(history(page)).toContainText('本次查询没有更多已保存记录')
  const rawType = ' future-kind '
  api.enqueue(envelope([saved('exact-type', rawType)]), query(20, rawType))
  await history(page).getByLabel('其他类型精确查询（保留空格）', { exact: true }).fill(rawType)
  await button(page, '查询类型').click()
  await expect(history(page)).toContainText('exact-type')
  await capture(page, info, api, 'exact-type-current-page', button(page, '回到最新页'))
  const count = api.requests.filter(request => request === 'GET /api/v1/jobs').length
  await page.clock.install(); await page.clock.fastForward(60_100)
  expect(api.actualHistory).toHaveLength(7)
  expect(api.requests.filter(request => request === 'GET /api/v1/jobs')).toHaveLength(count)
  await page.clock.resume()
  await finish(page, info, api)
})

test('404 and 501 explicitly unsupported; recent controls recover with keyboard focus', async ({ page, baseURL }, info) => {
  const api = await fixture(page, new URL(baseURL!).origin)
  api.enqueue({}, query(), 404); await open(page)
  for (const status of [404, 501]) {
    if (status === 501) { api.enqueue({ code: 'JOB_HISTORY_UNSUPPORTED' }, query(), 501); await button(page, '已保存历史').click() }
    await expect(history(page)).toContainText('此服务暂不能提供已保存历史，可返回最近任务')
    await expect(button(page, '重试同一读取')).toHaveCount(0)
    await expect(history(page).getByRole('article')).toHaveCount(0)
    await capture(page, info, api, `unsupported-${status}`, button(page, '最近任务'))
    await button(page, '最近任务').focus(); await page.keyboard.press('Enter')
    await expect(button(page, '刷新任务')).toBeEnabled()
    await expect(history(page)).toHaveCount(0)
    await button(page, '刷新任务').click()
  }
  for (let index = 0; index < 12; index++) { await page.keyboard.press('Tab'); expect(await drawer(page).evaluate(element => element.contains(document.activeElement))).toBe(true) }
  await page.keyboard.press('Escape'); await expect(drawer(page)).toHaveCount(0)
  await expect(page.getByRole('button', { name: '任务', exact: true })).toBeFocused()
  await finish(page, info, api)
})

test('503 and malformed 200 remain retryable errors, never empty or unsupported', async ({ page, baseURL }, info) => {
  const api = await fixture(page, new URL(baseURL!).origin)
  api.enqueue({ code: 'JOB_HISTORY_UNAVAILABLE' }, query(), 503); await open(page)
  await expect(history(page)).toContainText('已保存历史暂时无法读取')
  await capture(page, info, api, 'read-unavailable', button(page, '重试同一读取'))
  api.enqueue({ source: 'persisted', items: [{ status: 'running' }] })
  await button(page, '重试同一读取').click()
  await expect(history(page)).toContainText('已保存历史响应不符合协议')
  await expect(history(page)).not.toContainText('没有更多')
  await expect(history(page)).not.toContainText('暂不能提供')
  await capture(page, info, api, 'protocol-error', button(page, '重试同一读取'))
  api.enqueue(envelope([])); await button(page, '重试同一读取').click()
  await expect(history(page)).toContainText('本次查询没有更多已保存记录')
  await expect(button(page, '下一页')).toBeDisabled()
  await capture(page, info, api, 'empty-recovered', button(page, '回到最新页'))
  await finish(page, info, api)
})

test('late old reads after view switch and close cannot overwrite a new history owner', async ({ page, baseURL }, info) => {
  const api = await fixture(page, new URL(baseURL!).origin)
  const old = api.enqueue(envelope([saved('obsolete-switch')]), query(), 200, true)!
  const closed = { received: deferred(), release: deferred() }
  try {
    await open(page); await old.received.promise
    await button(page, '最近任务').click()
    api.enqueue(envelope([saved('fresh-owner')]))
    await button(page, '已保存历史').click()
    await expect(history(page)).toContainText('fresh-owner')
    old.release.resolve(); await old.delivered.promise
    await expect(history(page)).not.toContainText('obsolete-switch')
    const held = api.enqueue(envelope([saved('obsolete-close')]), query(), 200, true)!
    closed.release = held.release; closed.received = held.received
    await button(page, '回到最新页').click(); await held.received.promise
    await button(page, '关闭已保存历史').click(); await expect(drawer(page)).toHaveCount(0)
    api.enqueue(envelope([saved('reopened-owner')]))
    await page.getByRole('button', { name: '任务', exact: true }).click()
    await button(page, '已保存历史').click()
    await expect(history(page)).toContainText('reopened-owner')
    held.release.resolve(); await held.delivered.promise
    await capture(page, info, api, 'reopened-current-owner', button(page, '回到最新页'))
    await expect(history(page)).not.toContainText('obsolete-close')
    await expect(history(page)).not.toContainText('obsolete-switch')
    await finish(page, info, api)
  } finally { old.release.resolve(); closed.release.resolve() }
})
