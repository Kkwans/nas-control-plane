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

const queryPath = '/api/v1/databases/query'
const rowsPath = '/api/v1/databases/rows'
const target = { sourceId: 'sqlite-main', schema: '', table: 'settings' }
const targetLabel = '目标：NCP 数据库 / 未提供 schema / settings'
const originalSQL = "UPDATE settings SET value = 'fixture-only' WHERE id = 1"
const nextSQL = "SELECT value FROM settings WHERE id = 1; -- fixture-only\n-- 保留 SQL 原文与换行"
const editor = (page: Page) => page.locator('.sql-editor .cm-content')
const execute = (page: Page) => page.locator('.sql-toolbar__actions').getByRole('button', { name: /执行/ })
const mode = (page: Page) => page.getByRole('combobox', { name: 'SQL 结果模式', exact: true })
const warning = (page: Page) => page.locator('.sql-outcome-warning')
const confirmation = (page: Page) => page.getByRole('dialog', { name: '确认再次执行 SQL', exact: true })
const confirm = (page: Page) => confirmation(page).getByRole('button', { name: '确认风险并执行', exact: true })
type Query = { sourceId: string; sql: string; resultMode?: 'rows' | 'exec' }
type RequestRecord = { method: string; path: string; body?: unknown }
type Reply = 'disconnect' | 'malformed-200' | 'sql-invalid' | 'success'
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }

// 仅使用内存 HTTP fixture：每次 SQL 必须预先单独放行，其余变更与未知 API 失败关闭。
// catalog/rows 的 POST 为只读；不会连接 NAS、真实数据库或任何真实写入端点。
async function fixture(page: Page) {
  await page.clock.setFixedTime(new Date('2026-10-14T04:00:00.000Z'))
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: RequestRecord[] = []
  const queries: RequestRecord[] = []
  const rowsReads: RequestRecord[] = []
  const denied: RequestRecord[] = []
  const getReads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities',
    '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/jobs',
    '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/databases/discovery',
    '/api/v1/databases/project-preferences',
  ])
  let permitted: { expected: Query; reply: Reply; gate?: Gate } | undefined
  let readFailure = false
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    const entry: RequestRecord = { method, path, ...(body === undefined ? {} : { body }) }
    requests.push(entry)
    if (method === 'POST' && path === queryPath) {
      queries.push(entry)
      if (permitted) {
        const submission = permitted
        permitted = undefined
        expect(body).toEqual(submission.expected)
        try {
          submission.gate?.received.resolve()
          if (submission.gate) await submission.gate.release.promise
          if (submission.reply === 'disconnect') await route.abort('failed')
          else if (submission.reply === 'malformed-200') await route.fulfill({ status: 200, contentType: 'application/json', body: '{' })
          else if (submission.reply === 'sql-invalid') await route.fulfill({ status: 400, json: { code: 'sql_invalid', message: 'fixture-private-diagnostic', requestId: 'fixture-sql-invalid' } })
          else await route.fulfill({ json: { columns: ['value'], rows: [['fixture-confirmed-new-result']], rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 2 } })
        } finally { submission.gate?.finished.resolve() }
        return
      }
    }
    if (method === 'POST' && path === rowsPath) {
      expect(body).toMatchObject(target)
      rowsReads.push(entry)
      if (readFailure) return route.fulfill({ status: 503, json: { code: 'agent_unavailable', message: 'fixture-only read failure', requestId: 'fixture-read-failure' } })
      return route.fallback()
    }
    if (method === 'POST' && path === '/api/v1/databases/catalog') {
      expect(body).toEqual({ sourceId: target.sourceId })
      return route.fallback()
    }
    if (method === 'GET' && (getReads.has(path) || path.startsWith('/api/v1/preferences/lists/'))) return route.fallback()
    denied.push(entry)
    await route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期的 API 请求已拒绝' } })
  })
  return {
    requests, queries, rowsReads, denied,
    failReads(value: boolean) { readFailure = value },
    allowQuery(expected: Query, reply: Reply, hold = false) {
      expect(permitted, '不能覆盖尚未消费的 SQL fixture 许可').toBeUndefined()
      const gate: Gate | undefined = hold ? { received: deferred(), release: deferred(), finished: deferred() } : undefined
      permitted = { expected, reply, gate }
      return gate
    },
    async release(gate: Gate) {
      const completed = page.waitForResponse((response) => new URL(response.url()).pathname === queryPath && response.request().method() === 'POST').then((response) => response.finished())
      gate.release.resolve()
      await gate.finished.promise
      await completed
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>

async function ready(page: Page) {
  await page.goto('/databases/sqlite-main/tables/settings', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'settings', exact: true })).toBeVisible()
  await expect(page.locator('.data-table')).toContainText('theme')
  await page.getByRole('tab', { name: '执行 SQL', exact: true }).click()
  await expect(editor(page)).toBeEditable()
  await waitForPageReady(page)
}
async function expectUnknown(page: Page, api: Fixture) {
  await expect(warning(page)).toContainText('此前 SQL 结果未确认，可能部分或全部生效。')
  await expect(warning(page)).toContainText('只读刷新或后续 SQL 成功，均不能确认此前请求的结果')
  await expect(page.locator('.query-message--error')).toContainText('结果未确认，可能部分或全部生效')
  await expect(page.locator('.query-message--error')).not.toContainText('SQL 未提交执行')
  await expect(page.locator('.query-message--success')).toHaveCount(0)
  await expect(page.locator('.el-message')).toHaveCount(0)
  await expect(execute(page)).toBeEnabled()
  await expect(editor(page)).toBeEditable()
  expect(api.queries).toHaveLength(1)
  expect(api.rowsReads).toHaveLength(1)
  expect(api.denied).toEqual([])
}
async function expectSnapshot(page: Page, sql: string, resultMode: string) {
  await expect(confirmation(page)).toBeVisible()
  await expect(confirmation(page)).toContainText('再次执行可能重复生效')
  await expect(confirmation(page)).toContainText('即使本次以 SELECT 开头')
  await expect(confirmation(page)).toContainText(targetLabel)
  await expect(confirmation(page)).toContainText(`结果模式：${resultMode}`)
  expect(await confirmation(page).locator('.sql-confirmation-preview').textContent()).toBe(sql)
  await expect(confirm(page)).toBeEnabled()
}
function expectQueryCount(api: Fixture, count: number) {
  expect(api.queries).toHaveLength(count)
  expect(api.denied).toEqual([])
}

async function sourceProvenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--',
    'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json',
    'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts',
  ]).split('\n').filter(Boolean)
  const sourceHashes = Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [
    file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex'),
  ])))
  return {
    sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes,
    trackedDiffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex'),
  }
}
type CaptureView = 'unknown-warning' | 'risk-confirmation'
async function captureState(page: Page, view: CaptureView) {
  return page.evaluate((captureView) => {
    const dialog = document.querySelector<HTMLElement>('.confirm-danger-dialog')
    const scope = captureView === 'risk-confirmation' ? dialog! : document.querySelector<HTMLElement>('.table-workbench-page')!
    const selectors = captureView === 'risk-confirmation' ? [
      '.el-dialog__title', '.el-dialog__headerbtn', '.confirm-danger-dialog__notice p',
      '.confirm-danger-dialog__section strong', '.confirm-danger-dialog__section li',
      '.sql-confirmation-snapshot > span', '.sql-confirmation-snapshot > strong', '.sql-confirmation-preview',
      '.el-dialog__footer button', '.el-dialog__footer button > span',
    ] : [
      '.sql-outcome-warning', '.sql-outcome-warning strong', '.sql-outcome-warning span', '.sql-outcome-warning small',
      '.sql-result-mode select', '.sql-toolbar__actions button', '.sql-mode-note',
      '.query-result header', '.query-message__body strong', '.query-message__body span', '.query-message__body small', '.query-message--error code',
    ]
    function describe(element: Element | null) {
      return element ? { tag: element.tagName, id: element.id, className: element.getAttribute('class'), role: element.getAttribute('role'), label: element.getAttribute('aria-label') } : null
    }
    return {
      viewport: { width: innerWidth, height: innerHeight, devicePixelRatio },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY, overlay: dialog?.closest('.el-overlay-dialog')?.scrollTop ?? null, editor: document.querySelector('.cm-scroller')?.scrollTop, preview: dialog?.querySelector('pre')?.scrollTop ?? null },
      focus: describe(document.activeElement),
      selection: getSelection()?.toString(),
      semantic: {
        warning: document.querySelector('.sql-outcome-warning')?.textContent,
        error: document.querySelector('.query-message--error')?.textContent,
        editor: document.querySelector<HTMLElement>('.sql-editor .cm-content')?.innerText,
        editable: document.querySelector('.sql-editor .cm-content')?.getAttribute('contenteditable'),
        mode: document.querySelector<HTMLSelectElement>('.sql-result-mode select')?.value,
        executeDisabled: document.querySelector<HTMLButtonElement>('.sql-toolbar__actions button')?.disabled,
        target: dialog?.querySelector('.sql-confirmation-snapshot > span')?.textContent ?? null,
        preview: dialog?.querySelector('.sql-confirmation-preview')?.textContent ?? null,
        actions: [...(dialog?.querySelectorAll<HTMLButtonElement>('.el-dialog__footer button') ?? [])].map((button) => ({ text: button.textContent?.trim(), disabled: button.disabled })),
        autocomplete: [...document.querySelectorAll('.cm-tooltip-autocomplete')].map((element) => ({ ...describe(element), text: element.textContent, bounds: element.getBoundingClientRect().toJSON() })),
        toasts: [...document.querySelectorAll('.el-message')].map((element) => element.textContent),
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
          selector, index, ...describe(element), text: element.textContent?.trim(), bounds: bounds.toJSON(),
          lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
          scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
          visible: bounds.width > 0 && bounds.height > 0 && style.display !== 'none' && style.visibility !== 'hidden',
          inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth + 1 && bounds.bottom <= innerHeight + 1,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every((clip) => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
        }
      })),
    }
  }, view)
}

// CI 自动收集 ui-captures 下的 PNG 与配对 JSON。保留全部 axe 规则及结果，
// 不改 DOM/CSS、焦点、选择区或补全菜单；仅为规范全页图显式重置窗口滚动。
async function capture(page: Page, info: TestInfo, api: Fixture, view: CaptureView) {
  await page.evaluate(() => document.fonts.ready)
  await waitForPageReady(page)
  const initialState = await captureState(page, view)
  if (view === 'unknown-warning') {
    await page.evaluate(() => scrollTo({ top: 0, left: 0, behavior: 'instant' }))
    await expect.poll(() => page.evaluate(() => scrollY)).toBe(0)
  }
  const provenance = await sourceProvenance()
  const beforeCapture = await captureState(page, view)
  const requestsBefore = structuredClone(api.requests)
  const name = `database-sql-outcome-${view}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: view === 'unknown-warning', animations: 'disabled' })
  const afterCapture = await captureState(page, view)
  const requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await captureState(page, view)
  const metadata = {
    ...provenance, fixtureOnly: true, nativeDatabaseAcceptance: false,
    fixtureSource: 'frontend/e2e/database-sql-outcome.spec.ts + frontend/e2e/mockApi.ts',
    view, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), fullPage: view === 'unknown-warning',
    screenshot: `${name}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    fixture: { target, queries: api.queries, rowsReads: api.rowsReads, denied: api.denied },
    requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), initialState, beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  await info.attach(`${name}-axe-state`, { path: axePath, contentType: 'application/json' })

  // 先保存真实失败证据；soft 断言仍使测试失败，但不会吞掉第二状态的采集。
  expect.soft(afterCapture).toEqual(beforeCapture)
  expect.soft(afterAxe.semantic).toEqual(beforeCapture.semantic)
  expect.soft(afterAxe.scroll).toEqual(beforeCapture.scroll)
  expect.soft(afterAxe.focus).toEqual(beforeCapture.focus)
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  expect.soft(beforeCapture.controls.length).toBeGreaterThan(0)
  for (const control of beforeCapture.controls) {
    expect.soft(control.visible, JSON.stringify(control)).toBe(true)
    expect.soft(control.bounds.left).toBeGreaterThanOrEqual(0)
    expect.soft(control.bounds.right).toBeLessThanOrEqual(beforeCapture.viewport.width + 1)
    if (control.selector !== '.sql-result-mode select') expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (view === 'risk-confirmation' || control.selector.startsWith('.sql-outcome-warning')) expect.soft(control.inViewport, JSON.stringify(control)).toBe(true)
    if (view === 'risk-confirmation' && ['.el-dialog__headerbtn', '.el-dialog__footer button'].includes(control.selector)) expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
  }
  expect.soft(axe.violations, JSON.stringify(axe.violations, null, 2)).toEqual([])
}

for (const scenario of [{ width: 390, reply: 'disconnect', readFailure: false }, { width: 1440, reply: 'malformed-200', readFailure: true }] as const) {
  test(`${scenario.width}px SQL ${scenario.reply} 后可复制编辑和只读刷新，新 SELECT 必须重新确认且取消零 SQL POST`, async ({ page }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width: scenario.width, height: 900 })
    const api = await fixture(page)
    await ready(page)
    await editor(page).fill(originalSQL)
    await mode(page).selectOption('exec')
    api.allowQuery({ sourceId: target.sourceId, sql: originalSQL, resultMode: 'exec' }, scenario.reply)
    await execute(page).click()
    await expectUnknown(page, api)
    await capture(page, info, api, 'unknown-warning')

    // 真实浏览器快捷键复制草稿，不注入 clipboard stub，也不把复制当成执行。
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(page.url()).origin })
    await editor(page).press('ControlOrMeta+a')
    await editor(page).press('ControlOrMeta+c')
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(originalSQL)
    await editor(page).fill(nextSQL)
    await mode(page).selectOption('rows')
    expectQueryCount(api, 1)

    api.failReads(scenario.readFailure)
    await page.getByRole('tab', { name: '表数据', exact: true }).click()
    const refresh = page.locator('.data-toolbar').getByRole('button', { name: '刷新', exact: true })
    await refresh.click()
    await expect(refresh).toBeEnabled()
    await expect.poll(() => api.rowsReads.length).toBe(2)
    if (scenario.readFailure) await expect(page.locator('.database-error')).toContainText('agent_unavailable')
    else await expect(page.locator('.data-table')).toContainText('theme')
    await expect(warning(page)).toBeVisible()
    expectQueryCount(api, 1)

    await page.getByRole('tab', { name: '执行 SQL', exact: true }).click()
    await expect(mode(page)).toHaveValue('rows')
    await editor(page).press('Control+Enter')
    await expectSnapshot(page, nextSQL, '返回行')
    await expect(execute(page)).toBeDisabled()
    expectQueryCount(api, 1)
    await capture(page, info, api, 'risk-confirmation')
    await confirmation(page).getByRole('button', { name: '取消', exact: true }).click()
    await expect(confirmation(page)).toBeHidden()
    await expect(execute(page)).toBeEnabled()
    await expect(editor(page)).toBeEditable()
    await expect(warning(page)).toBeVisible()
    await waitForPageReady(page)
    expectQueryCount(api, 1)
    expect(api.rowsReads).toHaveLength(2)
  })
}

test('sql_invalid 不能证明未执行；取消撤销旧快照，明确确认冻结目标/原文/模式一次且后续成功保留旧未知', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const api = await fixture(page)
  await ready(page)
  const partialSQL = `${originalSQL}; INVALID SQL`
  await editor(page).fill(partialSQL)
  api.allowQuery({ sourceId: target.sourceId, sql: partialSQL }, 'sql-invalid')
  await execute(page).click()
  await expectUnknown(page, api)
  await expect(page.locator('.query-message--error')).toContainText('sql_invalid')
  await expect(page.locator('.query-message--error')).not.toContainText('fixture-private-diagnostic')

  await execute(page).click()
  await expectSnapshot(page, partialSQL, '自动')
  expectQueryCount(api, 1)
  await confirmation(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(confirmation(page)).toBeHidden()
  expectQueryCount(api, 1)

  const frozenSQL = `  ${nextSQL}\n`
  await editor(page).fill(frozenSQL)
  await mode(page).selectOption('rows')
  await execute(page).click()
  await expectSnapshot(page, frozenSQL, '返回行')
  expectQueryCount(api, 1)
  const expected = { sourceId: target.sourceId, sql: frozenSQL, resultMode: 'rows' as const }
  const gate = api.allowQuery(expected, 'success', true)!
  await confirm(page).dblclick()
  await gate.received.promise
  await expect(execute(page)).toBeDisabled()
  await expect(editor(page)).not.toBeEditable()
  await expect(mode(page)).toBeDisabled()
  await expect(confirmation(page).getByRole('button', { name: '取消', exact: true })).toBeDisabled()
  expectQueryCount(api, 2)
  expect(api.queries[1]).toEqual({ method: 'POST', path: queryPath, body: expected })
  expect(await confirmation(page).locator('.sql-confirmation-preview').textContent()).toBe(frozenSQL)
  await api.release(gate)
  await expect(confirmation(page)).toBeHidden()
  await expect(page.locator('.query-table')).toContainText('fixture-confirmed-new-result')
  await expect(page.locator('.query-message--error')).toHaveCount(0)
  await expect(warning(page)).toContainText('此前 SQL 结果未确认，可能部分或全部生效')
  await expect(editor(page)).toBeEditable()
  await expect(execute(page)).toBeEnabled()
  expect(api.rowsReads).toHaveLength(2)
  expectQueryCount(api, 2)

  await execute(page).click()
  await expectSnapshot(page, frozenSQL, '返回行')
  expectQueryCount(api, 2)
  await confirmation(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(confirmation(page)).toBeHidden()
  await waitForPageReady(page)
  expectQueryCount(api, 2)
})
