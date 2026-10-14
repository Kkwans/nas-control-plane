import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })

const connectPath = '/api/v1/databases/connect'
const diagnosticPath = '/api/v1/databases/test-connection'
const catalogPath = '/api/v1/databases/catalog'
const source = {
  id: 'fixture-postgresql', name: 'Fixture PostgreSQL', driver: 'postgresql', category: 'project',
  project: 'Fixture 项目', module: '连接结果验证', location: 'fixture-db.invalid:5432',
  host: 'fixture-db.invalid', port: 5432, defaultDatabase: 'fixture_catalog',
  requiresLogin: true, status: 'credentials_required', reachability: 'host', evidence: 'published-port', tags: ['fixture-only'],
}
const originalDraft = { username: 'fixture_reader', password: 'fixture-only-original-password', database: 'fixture_catalog' }
const correctedDraft = { username: 'fixture_corrected_reader', password: 'fixture-only-corrected-password', database: 'fixture_corrected_catalog' }
const diagnostic = { connected: true, code: 'connected', driver: 'postgresql', endpoint: source.location, database: source.defaultDatabase, operation: 'test_connection', durationMs: 2 }
const catalog = { source, tables: [{ schema: 'public', name: 'fixture_records', type: 'table', columns: [{ name: 'id', dataType: 'bigint', nullable: false, primaryKey: true, position: 1 }], rowCount: 2, sizeBytes: 4096 }] }
const form = (page: Page) => page.locator('.connection-form')
const field = (page: Page, label: string) => form(page).getByLabel(label, { exact: true })
const testConnection = (page: Page) => form(page).getByRole('button', { name: '测试连接', exact: true })
const connect = (page: Page) => form(page).getByRole('button', { name: '连接并读取数据表', exact: true })
const error = (page: Page) => page.locator('.database-error[role="alert"]')
type Draft = typeof originalDraft
type Connection = { sourceId: string; credentials?: Draft }
type RequestRecord = { method: string; path: string; body?: unknown }
type Reply = 'credentials-required' | 'malformed-diagnostic' | 'malformed-connect' | 'disconnect' | 'connected' | 'catalog-error' | 'catalog'
type Permit = { expected: Connection; reply: Reply }

// 所有凭据、主机和目录均为内存 fixture。connect、诊断和 catalog 逐次许可；
// 非本地请求、未知 API 和任何未许可变更均失败关闭，绝不访问真实 NAS。
async function fixture(page: Page, baseURL: string) {
  await page.clock.install({ time: new Date('2026-10-14T05:00:00.000Z') })
  await installMockApi(page, { clock: false, manualRealtime: true })
  const origin = new URL(baseURL).origin
  const requests: RequestRecord[] = []
  const denied: RequestRecord[] = []
  const permits = new Map<string, Permit>()
  const getReads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities',
    '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/jobs',
    '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/databases/project-preferences',
  ])
  await page.route('**/*', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    if (url.origin === origin && !url.pathname.startsWith('/api/') && method === 'GET') return route.fallback()
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    const entry: RequestRecord = { method, path: url.pathname, ...(body === undefined ? {} : { body }) }
    requests.push(entry)
    if (url.origin === origin) {
      if (method === 'POST' && permits.has(url.pathname)) {
        const permit = permits.get(url.pathname)!
        permits.delete(url.pathname)
        expect(body).toEqual(permit.expected)
        if (permit.reply === 'credentials-required') return route.fulfill({ status: 400, json: { code: 'credentials_required', message: 'fixture-only', requestId: 'fixture-initial-connect' } })
        if (permit.reply === 'disconnect') return route.abort('failed')
        if (permit.reply === 'malformed-connect') return route.fulfill({ status: 200, contentType: 'application/json', body: '{' })
        if (permit.reply === 'malformed-diagnostic') return route.fulfill({ json: { ...diagnostic, connected: 'false', code: 'auth_failed', privateDiagnostic: 'fixture-private-diagnostic-must-not-render' } })
        if (permit.reply === 'catalog-error') return route.fulfill({ status: 503, json: { code: 'permission_denied', message: 'fixture-private-diagnostic-must-not-render', requestId: 'fixture-catalog-error' } })
        return route.fulfill({ json: permit.reply === 'catalog' ? catalog : { ...diagnostic, database: permit.expected.credentials?.database ?? diagnostic.database } })
      }
      if (method === 'GET' && url.pathname === '/api/v1/databases/discovery') return route.fulfill({ json: { collectedAt: '2026-10-14T05:00:00.000Z', sources: [source] } })
      if (method === 'GET' && getReads.has(url.pathname)) return route.fallback()
    }
    denied.push(entry)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return {
    requests, denied,
    allow(path: string, expected: Connection, reply: Reply) {
      expect([connectPath, diagnosticPath, catalogPath]).toContain(path)
      expect(permits.has(path), '不能覆盖未消费的一次性许可').toBe(false)
      permits.set(path, { expected: structuredClone(expected), reply })
    },
    expectCounts(connections: number, diagnostics: number, catalogs: number) {
      for (const [path, count] of [[connectPath, connections], [diagnosticPath, diagnostics], [catalogPath, catalogs]] as const) {
        expect(requests.filter((entry) => entry.path === path)).toHaveLength(count)
      }
      expect(denied).toEqual([])
      expect(permits.size, '所有已许可请求均应消费一次').toBe(0)
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>

async function draft(page: Page, values: Draft, fill = false) {
  for (const [key, label] of [['username', '用户名'], ['password', '密码'], ['database', '数据库名']] as const) {
    if (fill) await field(page, label).fill(values[key])
    await expect(field(page, label)).toBeEditable()
    await expect(field(page, label)).toHaveValue(values[key])
  }
  await expect(field(page, '密码')).toHaveAttribute('type', 'password')
}
async function noConfirmedConnection(page: Page) {
  await expect(page.locator('.connection-pill--success, .diagnostic-result--success')).toHaveCount(0)
  await expect(page.getByRole('region', { name: '数据表和视图列表', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '刷新结构', exact: true })).toHaveCount(0)
  await expect(page.locator('.el-message')).toHaveCount(0)
  await expect(error(page)).not.toContainText('fixture-private-diagnostic-must-not-render')
  await expect(testConnection(page)).toBeEnabled()
  await expect(connect(page)).toBeEnabled()
}
async function keyboardActions(page: Page) {
  await field(page, '数据库名').focus()
  await page.keyboard.press('Tab')
  await expect(testConnection(page)).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(connect(page)).toBeFocused()
}
async function noAutomaticRetry(page: Page, api: Fixture, connections: number, catalogs: number) {
  // 跨过默认五秒刷新周期；时间推进本身不得重新保存凭据或读取目录。
  await page.clock.fastForward(15_000)
  await waitForPageReady(page)
  api.expectCounts(connections, 1, catalogs)
}

async function sourceProvenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  const sourceHashes = Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')])))
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes, trackedDiffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex') }
}

async function captureState(page: Page) {
  return page.evaluate(() => {
    const scope = document.querySelector<HTMLElement>('.database-detail')!
    const describe = (element: Element | null) => element ? { tag: element.tagName, id: element.id, role: element.getAttribute('role'), label: element.getAttribute('aria-label'), text: element.textContent?.trim() } : null
    const selectors = ['.database-error__body strong', '.database-error__body span', '.database-error__body small', '.database-error code', '.source-summary__status .connection-pill', '.source-summary__status small', '.connection-form .el-form-item__label', '.connection-form input', '.connection-form__actions button', '.connection-form__actions button > span']
    return {
      viewport: { width: innerWidth, height: innerHeight, devicePixelRatio },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY }, focus: describe(document.activeElement),
      semantic: {
        error: scope.querySelector('.database-error')?.textContent, status: scope.querySelector('.source-summary__status')?.textContent,
        diagnostic: scope.querySelector('.diagnostic-result')?.textContent ?? null, catalog: Boolean(scope.querySelector('[aria-label="数据表和视图列表"]')),
        inputs: [...scope.querySelectorAll<HTMLInputElement>('.connection-form input')].map((input) => ({ id: input.id, labels: [...(input.labels ?? [])].map((label) => label.textContent?.trim()), type: input.type, value: input.value, disabled: input.disabled })),
        actions: [...scope.querySelectorAll<HTMLButtonElement>('.connection-form__actions button')].map((button) => ({ text: button.textContent?.trim(), disabled: button.disabled })),
      },
      controls: selectors.flatMap((selector) => [...scope.querySelectorAll<HTMLElement>(selector)].map((element, index) => {
        const bounds = element.getBoundingClientRect()
        const style = getComputedStyle(element)
        const range = document.createRange()
        range.selectNodeContents(element)
        const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
        const clips: DOMRect[] = []
        for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
          const overflow = getComputedStyle(ancestor)
          if (/(hidden|clip|auto|scroll)/.test(`${overflow.overflowX} ${overflow.overflowY}`)) clips.push(ancestor.getBoundingClientRect())
        }
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return {
          selector, index, ...describe(element), bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
          visible: bounds.width > 0 && bounds.height > 0 && style.display !== 'none' && style.visibility !== 'hidden',
          inViewport: bounds.top >= 0 && bounds.bottom <= innerHeight + 1,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every((clip) => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
        }
      })),
    }
  })
}

// 与 SQL outcome 使用相同 CI 配对产物约定；完整 axe，不改样式、不隐藏控件。
async function capture(page: Page, info: TestInfo, api: Fixture, view: 'connection-unknown' | 'catalog-failed') {
  await page.evaluate(() => document.fonts.ready)
  await waitForPageReady(page)
  const initialState = await captureState(page)
  await page.evaluate(() => scrollTo({ top: 0, left: 0, behavior: 'instant' }))
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(0)
  const provenance = await sourceProvenance()
  let beforeCapture = await captureState(page)
  let stable = 0
  await expect.poll(async () => {
    const next = await captureState(page)
    stable = JSON.stringify(next) === JSON.stringify(beforeCapture) ? stable + 1 : 0
    beforeCapture = next
    return stable
  }, { intervals: [80], timeout: 5_000 }).toBeGreaterThanOrEqual(3)
  const requestsBefore = structuredClone(api.requests)
  const name = `database-connection-outcome-${view}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: true, animations: 'disabled' })
  const afterCapture = await captureState(page)
  const requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await captureState(page)
  const metadata = {
    ...provenance, fixtureOnly: true, nativeDatabaseAcceptance: false,
    fixtureSource: 'frontend/e2e/database-connection-outcome.spec.ts + frontend/e2e/mockApi.ts',
    view, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), fullPage: true,
    screenshot: `${name}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    fixture: { sourceId: source.id, denied: api.denied }, requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests),
    initialState, beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  await info.attach(`${name}-axe-state`, { path: axePath, contentType: 'application/json' })
  // 先保存完整失败证据，再断言实际语义、焦点、裁剪和无重试。
  for (const state of [afterCapture, afterAxe]) {
    expect.soft(state.semantic).toEqual(beforeCapture.semantic)
    expect.soft(state.focus).toEqual(beforeCapture.focus)
    expect.soft(state.scroll).toEqual(beforeCapture.scroll)
    expect.soft(state.controls).toHaveLength(beforeCapture.controls.length)
    for (const [index, control] of state.controls.entries()) {
      const previous = beforeCapture.controls[index]!
      expect.soft({ ...control, bounds: undefined, lines: undefined, clips: undefined }).toEqual({ ...previous, bounds: undefined, lines: undefined, clips: undefined })
      for (const key of ['left', 'right', 'top', 'bottom', 'width', 'height'] as const) expect.soft(Math.abs(control.bounds[key] - previous.bounds[key])).toBeLessThanOrEqual(1 / 8)
    }
  }
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  expect.soft(beforeCapture.semantic.inputs.map((input) => input.labels)).toEqual([['用户名'], ['密码'], ['数据库名']])
  expect.soft(beforeCapture.semantic.actions).toEqual([{ text: '测试连接', disabled: false }, { text: '连接并读取数据表', disabled: false }])
  for (const control of beforeCapture.controls) {
    expect.soft(control.visible, JSON.stringify(control)).toBe(true)
    expect.soft(control.bounds.left).toBeGreaterThanOrEqual(0)
    expect.soft(control.bounds.right).toBeLessThanOrEqual(beforeCapture.viewport.width + 1)
    expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (control.inViewport && control.tag === 'BUTTON') expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
  }
  expect.soft(axe.violations, JSON.stringify(axe.violations, null, 2)).toEqual([])
}

for (const scenario of [{ width: 1440, reply: 'malformed-connect' }, { width: 390, reply: 'disconnect' }] as const) {
  test(`${scenario.width}px 诊断不可信、${scenario.reply} 连接未知、目录失败与明确更正后成功分阶段显示`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    expect(baseURL).toBeTruthy()
    await page.setViewportSize({ width: scenario.width, height: 900 })
    const api = await fixture(page, baseURL!)
    api.allow(connectPath, { sourceId: source.id }, 'credentials-required')
    await page.goto(`/databases/${source.id}`, { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('heading', { name: source.name, exact: true })).toBeVisible()
    await expect(connect(page)).toBeEnabled()
    api.expectCounts(1, 0, 0)
    await draft(page, originalDraft, true)

    api.allow(diagnosticPath, { sourceId: source.id, credentials: originalDraft }, 'malformed-diagnostic')
    await testConnection(page).click()
    await expect(error(page)).toContainText('数据库连接诊断结果未确认')
    await expect(error(page)).toContainText('DATABASE_RESPONSE_INVALID')
    await expect(error(page)).toContainText('测试连接不会保存凭据')
    await expect(page.locator('.connection-pill')).toHaveText('诊断结果未确认')
    await expect(page.locator('.diagnostic-result')).toHaveCount(0)
    await noConfirmedConnection(page)
    await draft(page, originalDraft)
    api.expectCounts(1, 1, 0)

    api.allow(connectPath, { sourceId: source.id, credentials: originalDraft }, scenario.reply)
    await connect(page).click()
    await expect(error(page)).toContainText('数据库连接结果未确认')
    await expect(error(page)).toContainText('DATABASE_CONNECTION_OUTCOME_UNKNOWN')
    await expect(error(page)).toContainText('服务端可能已保存本次凭据')
    await expect(error(page)).toContainText('不会自动重试')
    await expect(page.locator('.connection-pill')).toHaveText('连接结果未确认')
    await noConfirmedConnection(page)
    await draft(page, originalDraft)
    await noAutomaticRetry(page, api, 2, 0)
    await keyboardActions(page)
    await capture(page, info, api, 'connection-unknown')

    // 用户明确再次提交，连接已确认后才许可目录读取；目录失败不能冒充连接失败。
    api.allow(connectPath, { sourceId: source.id, credentials: originalDraft }, 'connected')
    api.allow(catalogPath, { sourceId: source.id, credentials: originalDraft }, 'catalog-error')
    await connect(page).click()
    await expect(error(page)).toContainText('数据库连接已确认，目录读取失败')
    await expect(error(page)).toContainText('DATABASE_CONNECTION_CATALOG_FAILED')
    await expect(error(page)).not.toContainText('数据库连接失败')
    await expect(error(page)).not.toContainText('DATABASE_CONNECTION_OUTCOME_UNKNOWN')
    await expect(page.locator('.connection-pill')).toHaveText('目录读取失败')
    await expect(page.locator('.source-summary__status')).toContainText('连接已确认，尚未读取到可靠的对象目录')
    await noConfirmedConnection(page)
    await draft(page, originalDraft)
    await noAutomaticRetry(page, api, 3, 1)
    await keyboardActions(page)
    await capture(page, info, api, 'catalog-failed')

    await draft(page, correctedDraft, true)
    api.expectCounts(3, 1, 1)
    api.allow(connectPath, { sourceId: source.id, credentials: correctedDraft }, 'connected')
    api.allow(catalogPath, { sourceId: source.id, credentials: correctedDraft }, 'catalog')
    await keyboardActions(page)
    await page.keyboard.press('Enter')
    await expect(page.locator('.connection-pill--success')).toHaveText('已连接')
    await expect(page.getByRole('link', { name: '打开 fixture_records 数据表详情', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: '刷新结构', exact: true })).toBeEnabled()
    await expect(error(page)).toHaveCount(0)
    await expect(form(page)).toHaveCount(0)
    await expect(page.getByRole('region', { name: '数据表和视图列表', exact: true })).toBeVisible()
    await noAutomaticRetry(page, api, 4, 2)
    expect(api.requests.filter((request) => request.method === 'POST').map((request) => request.path)).toEqual([
      connectPath, diagnosticPath, connectPath, connectPath, catalogPath, connectPath, catalogPath,
    ])
  })
}
