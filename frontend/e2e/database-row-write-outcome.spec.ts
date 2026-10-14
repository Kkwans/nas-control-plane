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

type Operation = 'update' | 'delete'
type Reply = 'disconnect' | 'malformed-200' | 'confirmed-200' | 'confirmed-zero-200'
type Row = { id: string; key: string; value: string }
type RequestRecord = { method: string; path: string; body?: unknown }
const target = { sourceId: 'sqlite-main', schema: '', table: 'settings' }
const rowsPath = '/api/v1/databases/rows'
const writePath = (operation: Operation) => `${rowsPath}/${operation}`
const fixedTime = '2026-10-14T12:00:00.000Z'
const originalRows: Row[] = [
  { id: '9007199254740993', key: 'fixture-original-first-row', value: '纯内存样例第一行，不含真实数据库内容。' },
  { id: '9007199254740995', key: 'fixture-original-other-row', value: '纯内存样例另一行，用于验证整表保护。' },
]
const submittedValues: Row = {
  id: originalRows[0]!.id,
  key: 'fixture-committed-update-abcdefghijklmnopqrstuvwxyz-0123456789',
  value: '纯内存已提交修改。响应丢失不代表未执行，不能重复编辑或删除。',
}
const currentValues: Row = {
  id: originalRows[1]!.id,
  key: 'fixture-new-unsent-draft-abcdefghijklmnopqrstuvwxyz-0123456789-abcdefghijklmnopqrstuvwxyz',
  value: '另一行的新草稿仍然保留。只读刷新不会重放修改，也不能证明旧请求的执行结果。',
}
const table = {
  schema: '', name: 'settings', type: 'table',
  columns: [
    { name: 'id', dataType: 'INTEGER', nullable: false, primaryKey: true, position: 1 },
    { name: 'key', dataType: 'TEXT', nullable: false, primaryKey: false, position: 2 },
    { name: 'value', dataType: 'TEXT', nullable: true, primaryKey: false, default: null, position: 3 },
  ],
}
const editDialog = (page: Page) => page.getByRole('dialog', { name: '编辑数据行', exact: true })
const deleteDialog = (page: Page) => page.getByRole('dialog', { name: '确认删除数据行', exact: true })
const save = (page: Page) => editDialog(page).getByRole('button', { name: '保存修改', exact: true })
const remove = (page: Page) => deleteDialog(page).getByRole('button', { name: '永久删除', exact: true })
const refresh = (page: Page) => page.locator('.data-toolbar').getByRole('button', { name: '刷新', exact: true })
const pageNotice = (page: Page) => page.locator('.table-workbench-page > .insert-outcome-warning')
const edits = (page: Page) => page.getByRole('button', { name: '编辑当前行', exact: true })
const deletes = (page: Page) => page.getByRole('button', { name: '删除当前行', exact: true })
const operationLabel = (operation: Operation) => operation === 'update' ? '更新' : '删除'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = {
  operation: Operation; reply: Reply
  received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred>
}
function expectedWrite(operation: Operation): RequestRecord {
  return {
    method: 'POST', path: writePath(operation),
    body: { ...target, ...(operation === 'update' ? { values: submittedValues } : {}), keys: { id: originalRows[0]!.id } },
  }
}
function expectedRows(operation: Operation, reply: Reply) {
  if (reply === 'confirmed-zero-200') return originalRows
  return operation === 'update' ? [submittedValues, originalRows[1]!] : [originalRows[1]!]
}

// 所有 API 都先经过此白名单；catalog/rows 的 POST 仅为读取。只许可一次指定写入。
// 未知结果在返回网络错误/损坏响应前改变纯内存数据；合法零行结果不伪造已改变数据。
// 额外写入、SQL、未列出的路径均拒绝，绝不访问真实数据库、Agent 或 NAS。
async function fixture(page: Page) {
  await page.clock.setFixedTime(new Date(fixedTime))
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: RequestRecord[] = []
  const writes: RequestRecord[] = []
  const denied: RequestRecord[] = []
  const rows = structuredClone(originalRows)
  const effects: Array<{ operation: Operation; reply: Reply; rowsChanged: number }> = []
  const rowsReads: Array<{ body: unknown; failed: boolean; returnedRows: Row[] }> = []
  const getReads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities',
    '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/jobs',
    '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/databases/discovery',
    '/api/v1/databases/project-preferences',
  ])
  let permitted: Gate | undefined
  let readFailure = false
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    const entry: RequestRecord = { method, path, ...(body === undefined ? {} : { body }) }
    requests.push(entry)
    if (method === 'POST' && permitted && path === writePath(permitted.operation)) {
      const gate = permitted
      permitted = undefined
      writes.push(entry)
      expect(entry).toEqual(expectedWrite(gate.operation))
      rows.splice(0, rows.length, ...structuredClone(expectedRows(gate.operation, gate.reply)))
      effects.push({ operation: gate.operation, reply: gate.reply, rowsChanged: gate.reply === 'confirmed-zero-200' ? 0 : 1 })
      gate.received.resolve()
      try {
        await gate.release.promise
        if (gate.reply === 'disconnect') await route.abort('failed')
        else if (gate.reply === 'malformed-200') await route.fulfill({ status: 200, contentType: 'application/json', body: '{' })
        else await route.fulfill({ status: 200, json: { rowsAffected: gate.reply === 'confirmed-zero-200' ? 0 : 1 } })
      } finally { gate.finished.resolve() }
      return
    }
    if (method === 'POST' && path === rowsPath) {
      expect(body).toMatchObject(target)
      rowsReads.push({ body, failed: readFailure, returnedRows: readFailure ? [] : structuredClone(rows) })
      if (readFailure) return route.fulfill({ status: 503, json: { code: 'agent_unavailable', message: 'fixture-only read failure', requestId: 'fixture-read-failure' } })
      return route.fulfill({ json: { table, rows, limit: 10, offset: 0, hasMore: false, ordering: { stable: true, columns: ['id'] } } })
    }
    if (method === 'POST' && path === '/api/v1/databases/catalog') {
      expect(body).toEqual({ sourceId: target.sourceId })
      return route.fallback()
    }
    if (method === 'GET' && (getReads.has(path) || path.startsWith('/api/v1/preferences/lists/'))) return route.fallback()
    denied.push(entry)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期的 API 请求已拒绝' } })
  })
  return {
    requests, writes, denied, rows, effects, rowsReads,
    failReads(value: boolean) { readFailure = value },
    holdWrite(operation: Operation, reply: Reply) {
      expect(permitted).toBeUndefined()
      expect(writes).toHaveLength(0)
      const gate = { operation, reply, received: deferred(), release: deferred(), finished: deferred() }
      permitted = gate
      return gate
    },
    async release(gate: Gate) {
      const completed = gate.reply === 'disconnect'
        ? page.waitForEvent('requestfailed', (request) => new URL(request.url()).pathname === writePath(gate.operation) && request.method() === 'POST').then(() => undefined)
        : page.waitForResponse((response) => new URL(response.url()).pathname === writePath(gate.operation) && response.request().method() === 'POST').then((response) => response.finished())
      gate.release.resolve()
      await gate.finished.promise
      await completed
      await settle(page)
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>

async function settle(page: Page) {
  await page.evaluate(() => new Promise<void>((done) => requestAnimationFrame(() => requestAnimationFrame(() => done()))))
}
async function ready(page: Page) {
  await page.goto('/databases/sqlite-main/tables/settings', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'settings', exact: true })).toBeVisible()
  await expect(edits(page)).toHaveCount(2)
  await expect(page.locator('.data-table')).toContainText(originalRows[0]!.key)
  await expect(refresh(page)).toBeEnabled()
}
async function openEdit(page: Page, index: number) {
  await edits(page).nth(index).click()
  await expect(editDialog(page)).toBeVisible()
}
async function fillDraft(page: Page, values: Row) {
  await expect(editDialog(page).getByRole('textbox', { name: 'id', exact: true })).toHaveValue(values.id)
  await expect(editDialog(page).getByRole('textbox', { name: 'id', exact: true })).toBeDisabled()
  for (const name of ['key', 'value'] as const) await editDialog(page).getByRole('textbox', { name, exact: true }).fill(values[name])
}
async function expectDraft(page: Page, values: Row) {
  await expect(editDialog(page)).toBeVisible()
  for (const [name, value] of Object.entries(values)) await expect(editDialog(page).getByRole('textbox', { name, exact: true })).toHaveValue(value)
}
async function prepareWrite(page: Page, operation: Operation) {
  if (operation === 'update') {
    await openEdit(page, 0)
    await fillDraft(page, submittedValues)
  } else {
    await deletes(page).first().click()
    await expect(deleteDialog(page)).toBeVisible()
  }
}
async function submitWrite(page: Page, operation: Operation) {
  await (operation === 'update' ? save(page) : remove(page)).click()
}
async function expectProtected(page: Page, operation: Operation, count: number) {
  await expect(pageNotice(page)).toContainText(`${operationLabel(operation)}结果未确认，可能已生效。`)
  await expect(pageNotice(page)).toContainText('包括其他行')
  await expect(pageNotice(page)).toContainText('刷新仅重新读取数据，不能证明此次请求成功或失败，也不会解除保护')
  await expect(pageNotice(page)).toContainText('完整重载或离开此页面后不再保留')
  await expect(edits(page)).toHaveCount(count)
  await expect(deletes(page)).toHaveCount(count)
  for (const actions of [edits(page), deletes(page)]) {
    for (let index = 0; index < count; index++) await expect(actions.nth(index)).toBeDisabled()
  }
  await expect(page.locator('.el-message')).toHaveCount(0)
}
function expectOneWrite(api: Fixture, operation: Operation, reply: Reply) {
  const mutations = api.requests.filter((request) => !['GET', 'HEAD'].includes(request.method) && ![rowsPath, '/api/v1/databases/catalog'].includes(request.path))
  expect(mutations).toEqual([expectedWrite(operation)])
  expect(api.writes).toEqual([expectedWrite(operation)])
  expect(api.effects).toEqual([{ operation, reply, rowsChanged: reply === 'confirmed-zero-200' ? 0 : 1 }])
  expect(api.rows).toEqual(expectedRows(operation, reply))
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
type CaptureView = 'draft' | 'table'
async function captureState(page: Page, view: CaptureView) {
  return page.evaluate((view) => {
    const panel = document.querySelector<HTMLElement>(view === 'draft'
      ? '.el-overlay-dialog[aria-label="编辑数据行"] .el-dialog' : '.table-workbench-page')!
    const selectors = view === 'draft' ? [
      '.el-dialog__title', '.el-dialog__headerbtn', '.row-dialog-context strong', '.row-dialog-context small',
      '.insert-outcome-warning', '.insert-outcome-warning strong', '.insert-outcome-warning span',
      '.form-label', '.database-cell-editor__hint', '.row-form input',
      '.row-form .database-cell-editor input', '.row-form .el-switch',
      '.el-dialog__footer button', '.el-dialog__footer button > span',
    ] : [
      ':scope > .insert-outcome-warning', ':scope > .insert-outcome-warning strong',
      ':scope > .insert-outcome-warning span', ':scope > .insert-outcome-warning small',
      '.data-toolbar button', '.row-actions button',
    ]
    function visible(element: HTMLElement) {
      const bounds = element.getBoundingClientRect()
      if (bounds.width <= 0 || bounds.height <= 0) return false
      for (let ancestor: HTMLElement | null = element; ancestor; ancestor = ancestor.parentElement) {
        const style = getComputedStyle(ancestor)
        if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse' || Number(style.opacity) === 0) return false
      }
      return true
    }
    function diagnostic(element: HTMLElement) {
      return { visible: visible(element), bounds: element.getBoundingClientRect().toJSON(), ariaHidden: element.closest('[aria-hidden="true"]') !== null }
    }
    const fields = [...panel.querySelectorAll<HTMLInputElement>('.row-form .database-cell-editor input')]
    const switches = [...panel.querySelectorAll<HTMLElement>('.row-form .el-switch')]
    const actions = [...panel.querySelectorAll<HTMLButtonElement>('.el-dialog__footer button')]
    const describeField = (input: HTMLInputElement) => ({ name: input.getAttribute('aria-label'), value: input.value, disabled: input.disabled })
    const describeSwitch = (wrapper: HTMLElement) => {
      const input = wrapper.querySelector<HTMLInputElement>('input')
      return { field: wrapper.closest('.el-form-item')?.querySelector('.database-cell-editor input')?.getAttribute('aria-label'), role: input?.getAttribute('role'), checked: input?.checked, ariaChecked: input?.getAttribute('aria-checked'), disabled: input?.disabled }
    }
    const describeAction = (button: HTMLButtonElement) => ({ text: button.textContent?.trim(), disabled: button.disabled })
    return {
      viewport: { width: innerWidth, height: innerHeight, devicePixelRatio },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth, scrollHeight: document.documentElement.scrollHeight },
      scroll: { x: scrollX, y: scrollY, containers: [...document.querySelectorAll<HTMLElement>('*')].filter((element) => element.scrollTop || element.scrollLeft).map((element) => ({ tag: element.tagName, class: element.className, top: element.scrollTop, left: element.scrollLeft })) },
      focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label'), class: document.activeElement?.className },
      semantic: {
        notice: panel.querySelector('.insert-outcome-warning')?.textContent,
        pageNotice: document.querySelector('.table-workbench-page > .insert-outcome-warning')?.textContent,
        fields: fields.filter(visible).map(describeField),
        switches: switches.filter(visible).map(describeSwitch),
        actions: actions.filter(visible).map(describeAction),
        rowActions: [...document.querySelectorAll<HTMLButtonElement>('.row-actions button')].map((button) => ({ label: button.getAttribute('aria-label'), disabled: button.disabled })),
        renderedRows: [...document.querySelectorAll('.data-table .el-table__body tr')].map((row) => row.textContent),
        toasts: [...document.querySelectorAll('.el-message')].map((message) => message.textContent),
      },
      // ElDialog 关闭后可保留在表页面的 DOM 下。可见语义与原始保留节点分开记录，
      // 不能把隐藏的空草稿误认为仍在展示，也不能过滤掉其值、状态或几何诊断。
      rawDiagnostics: {
        fields: fields.map((input) => ({ ...describeField(input), ...diagnostic(input) })),
        switches: switches.map((wrapper) => ({ ...describeSwitch(wrapper), ...diagnostic(wrapper) })),
        actions: actions.map((button) => ({ ...describeAction(button), ...diagnostic(button) })),
        inputs: [...panel.querySelectorAll<HTMLInputElement>('.row-form input')].map((input) => ({
          ...describeField(input), type: input.type, role: input.getAttribute('role'), checked: input.checked,
          ariaChecked: input.getAttribute('aria-checked'), ...diagnostic(input),
        })),
      },
      controls: selectors.flatMap((selector) => [...panel.querySelectorAll<HTMLElement>(selector)].map((element, index) => {
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
          selector, index, tag: element.tagName, role: element.getAttribute('role'),
          inputType: element instanceof HTMLInputElement ? element.type : null,
          disabled: element instanceof HTMLInputElement || element instanceof HTMLButtonElement ? element.disabled : null,
          text: element.textContent?.trim(), bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
          scrollWidth: element.scrollWidth, clientWidth: element.clientWidth, scrollLeft: element.scrollLeft,
          visible: bounds.width > 0 && bounds.height > 0 && style.display !== 'none' && style.visibility !== 'hidden',
          inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth + 1 && bounds.bottom <= innerHeight + 1,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every((clip) => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
        }
      })),
    }
  }, view)
}

// 只在实际浏览器运行时生成截图、完整 axe 和状态；不修饰 DOM/CSS、隐藏控件或改变焦点。
// table 使用全页图，并保留真实滚动/命中证据；不把视口外控件误报为可点击。
async function capture(page: Page, info: TestInfo, api: Fixture, gate: Gate, view: CaptureView) {
  await page.evaluate(() => document.fonts.ready)
  await waitForPageReady(page)
  const provenance = await sourceProvenance()
  const beforeCapture = await captureState(page, view)
  const requestsBefore = structuredClone(api.requests)
  const name = `database-row-write-${gate.operation}-${gate.reply}-${view}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: view === 'table', animations: 'disabled' })
  const afterCapture = await captureState(page, view)
  const requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await captureState(page, view)
  const metadata = {
    ...provenance, fixtureOnly: true, nativeDatabaseAcceptance: false,
    fixtureSource: 'frontend/e2e/database-row-write-outcome.spec.ts + frontend/e2e/mockApi.ts',
    view, operation: gate.operation, reply: gate.reply, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
    screenshot: `${name}.png`, fullPage: view === 'table', screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    fixture: { target, originalRows, submittedValues, currentDraft: currentValues, rows: api.rows, effects: api.effects, writes: api.writes, rowsReads: api.rowsReads, denied: api.denied },
    requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  await info.attach(`${name}-axe-state`, { path: axePath, contentType: 'application/json' })

  // 先落盘完整未过滤证据，再做 soft 断言；即使视觉失败也继续检查唯一写入与只读恢复。
  expect.soft(afterCapture).toEqual(beforeCapture)
  expect.soft(afterAxe.semantic).toEqual(beforeCapture.semantic)
  expect.soft(afterAxe.scroll).toEqual(beforeCapture.scroll)
  expect.soft(afterAxe.focus).toEqual(beforeCapture.focus)
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  expect.soft(beforeCapture.semantic.rowActions.length).toBeGreaterThan(0)
  expect.soft(beforeCapture.semantic.rowActions.every((action) => action.disabled)).toBe(true)
  if (view === 'draft') {
    expect.soft(beforeCapture.semantic.fields).toEqual(Object.entries(currentValues).map(([name, value]) => ({ name, value, disabled: name === 'id' })))
    expect.soft(beforeCapture.semantic.switches).toEqual([{ field: 'value', role: 'switch', checked: false, ariaChecked: 'false', disabled: false }])
    expect.soft(beforeCapture.semantic.actions).toEqual([{ text: '取消', disabled: false }, { text: '保存修改', disabled: true }])
    expect.soft(beforeCapture.controls.filter((item) => item.selector === '.row-form .database-cell-editor input')).toHaveLength(3)
    expect.soft(beforeCapture.controls.filter((item) => item.selector === '.row-form .el-switch')).toHaveLength(1)
  } else {
    expect.soft(beforeCapture.semantic.fields).toEqual([])
    expect.soft(beforeCapture.semantic.switches).toEqual([])
    expect.soft(beforeCapture.semantic.actions).toEqual([])
    expect.soft(beforeCapture.rawDiagnostics.fields.every((field) => !field.visible && field.value === '')).toBe(true)
    expect.soft(beforeCapture.controls.filter((item) => item.selector.startsWith(':scope > .insert-outcome-warning'))).toHaveLength(4)
  }
  // ElSwitch 的原生 checkbox 可能为 0×0；保留所有 input 诊断，几何只检查可见编辑框和 switch 外壳。
  for (const control of beforeCapture.controls.filter((item) => item.selector !== '.row-form input')) {
    expect.soft(control.visible, JSON.stringify(control)).toBe(true)
    if (view === 'draft') expect.soft(control.inViewport, JSON.stringify(control)).toBe(true)
    if (control.selector !== '.row-form .database-cell-editor input') expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (view === 'draft' && !control.disabled && ['.el-dialog__headerbtn', '.el-dialog__footer button', '.row-form .database-cell-editor input', '.row-form .el-switch'].includes(control.selector)) {
      expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
    }
  }
  expect.soft(axe.violations, JSON.stringify(axe.violations, null, 2)).toEqual([])
}

for (const width of [390, 1440]) {
  for (const operation of ['update', 'delete'] as const) {
    const reply = operation === 'update' ? 'disconnect' : 'malformed-200'
    test(`${width}px ${operation.toUpperCase()} 已提交后 ${reply}：保留新草稿、整表保护、只读刷新不重放`, async ({ page }, info) => {
      test.setTimeout(120_000)
      await page.setViewportSize({ width, height: 900 })
      const api = await fixture(page)
      await ready(page)
      await prepareWrite(page, operation)
      const gate = api.holdWrite(operation, reply)
      await submitWrite(page, operation)
      await gate.received.promise
      await expect(operation === 'update' ? save(page) : remove(page)).toBeDisabled()
      if (operation === 'update') {
        await expect(editDialog(page).getByRole('textbox', { name: 'key', exact: true })).toBeDisabled()
        await editDialog(page).getByRole('button', { name: '取消', exact: true }).click()
      } else {
        await expect(deleteDialog(page).getByRole('button', { name: '取消', exact: true })).toBeDisabled()
        // ElDialog 的 Escape 是真实可达的关闭路径，不能用 Vue 内部状态伪造新的确认。
        await page.keyboard.press('Escape')
      }
      await expect(operation === 'update' ? editDialog(page) : deleteDialog(page)).toBeHidden()
      await expect(deletes(page).last()).toBeDisabled()
      await openEdit(page, 1)
      await fillDraft(page, currentValues)
      await expect(save(page)).toBeDisabled()
      await editDialog(page).getByRole('textbox', { name: 'key', exact: true }).press('Enter')
      expectOneWrite(api, operation, reply)
      await api.release(gate)
      await expectDraft(page, currentValues)
      await expect(editDialog(page).locator('.insert-outcome-warning')).toContainText('当前表所有行的编辑与删除已暂停。只读刷新不会解除保护。')
      await expect(save(page)).toBeDisabled()
      await expectProtected(page, operation, 2)
      expect(api.rowsReads).toHaveLength(1)
      expectOneWrite(api, operation, reply)
      await capture(page, info, api, gate, 'draft')

      await page.keyboard.press('Escape')
      await expect(editDialog(page)).toBeHidden()
      const beforeRead = api.requests.length
      await refresh(page).click()
      await expect(refresh(page)).toBeEnabled()
      if (operation === 'update') await expect(page.locator('.data-table')).toContainText(submittedValues.key)
      else await expect(page.locator('.data-table')).not.toContainText(originalRows[0]!.key)
      await expect(page.locator('.data-table')).toContainText(originalRows[1]!.key)
      expect(api.rowsReads).toHaveLength(2)
      expect(api.rowsReads.at(-1)).toMatchObject({ failed: false, returnedRows: expectedRows(operation, reply) })
      expect(api.requests.slice(beforeRead)).toEqual([{ method: 'POST', path: rowsPath, body: api.rowsReads.at(-1)!.body }])
      await expectProtected(page, operation, operation === 'delete' ? 1 : 2)
      expectOneWrite(api, operation, reply)
      await capture(page, info, api, gate, 'table')
      expectOneWrite(api, operation, reply)
    })
  }
}

for (const operation of ['update', 'delete'] as const) {
  test(`${operation.toUpperCase()} 合法 200 / rowsAffected 0 与读失败分别报告，只读恢复不重发`, async ({ page }) => {
    const api = await fixture(page)
    await ready(page)
    await prepareWrite(page, operation)
    const gate = api.holdWrite(operation, 'confirmed-zero-200')
    api.failReads(true)
    await submitWrite(page, operation)
    await gate.received.promise
    await api.release(gate)
    await expect(operation === 'update' ? editDialog(page) : deleteDialog(page)).toBeHidden()
    await expect(pageNotice(page)).toContainText(`${operationLabel(operation)}请求已执行，表数据刷新失败。`)
    await expect(pageNotice(page)).toContainText('请只刷新表数据，勿重复修改或删除')
    await expect(pageNotice(page)).not.toContainText('结果未确认')
    await expect(page.locator('.el-message--success')).toContainText(`${operationLabel(operation)}请求已执行`)
    expect(api.rowsReads).toHaveLength(2)
    expect(api.rowsReads[1]).toMatchObject({ failed: true, returnedRows: [] })
    expectOneWrite(api, operation, gate.reply)

    api.failReads(false)
    const beforeRead = api.requests.length
    await refresh(page).click()
    await expect(refresh(page)).toBeEnabled()
    await expect(pageNotice(page)).toHaveCount(0)
    await expect(page.locator('.data-table')).toContainText(originalRows[0]!.key)
    await expect(page.locator('.data-table')).not.toContainText(submittedValues.key)
    await expect(edits(page).last()).toBeEnabled()
    await expect(deletes(page).last()).toBeEnabled()
    expect(api.rowsReads).toHaveLength(3)
    expect(api.requests.slice(beforeRead)).toEqual([{ method: 'POST', path: rowsPath, body: api.rowsReads.at(-1)!.body }])
    expectOneWrite(api, operation, gate.reply)
  })
}

test('UPDATE 旧的确认成功不会关闭或覆盖另一行新草稿', async ({ page }) => {
  const api = await fixture(page)
  await ready(page)
  await prepareWrite(page, 'update')
  const gate = api.holdWrite('update', 'confirmed-200')
  await submitWrite(page, 'update')
  await gate.received.promise
  await editDialog(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(editDialog(page)).toBeHidden()
  await openEdit(page, 1)
  await fillDraft(page, currentValues)
  await api.release(gate)
  await expect(page.locator('.el-message--success')).toContainText('更新请求已执行')
  await expectDraft(page, currentValues)
  await expect(save(page)).toBeEnabled()
  await expect(pageNotice(page)).toHaveCount(0)
  expect(api.rowsReads).toHaveLength(2)
  expectOneWrite(api, 'update', gate.reply)
})

for (const reply of ['disconnect', 'confirmed-200'] as const) {
  test(`UPDATE 迟到 ${reply} 不关闭新 SQL 确认，不自动执行 SQL`, async ({ page }) => {
    const api = await fixture(page)
    await ready(page)
    await prepareWrite(page, 'update')
    const gate = api.holdWrite('update', reply)
    await submitWrite(page, 'update')
    await gate.received.promise
    await editDialog(page).getByRole('button', { name: '取消', exact: true }).click()
    await expect(editDialog(page)).toBeHidden()
    await page.getByRole('tab', { name: '执行 SQL', exact: true }).click()
    const sql = 'DELETE FROM fixture_never_execute'
    await page.locator('.sql-editor .cm-content').fill(sql)
    await page.locator('.sql-toolbar__actions').getByRole('button', { name: '执行' }).click()
    const confirmation = page.getByRole('dialog').filter({ has: page.locator('.sql-confirmation-snapshot') })
    await expect(confirmation).toBeVisible()
    await expect(confirmation.locator('.sql-confirmation-preview')).toHaveText(sql)
    await expect(confirmation.getByRole('button', { name: '确认执行', exact: true })).toBeDisabled()
    await api.release(gate)
    await expect(confirmation).toBeVisible()
    await expect(confirmation.locator('.sql-confirmation-preview')).toHaveText(sql)
    await expect(confirmation.getByRole('button', { name: '确认执行', exact: true })).toBeEnabled()
    expectOneWrite(api, 'update', reply)
    expect(api.rowsReads).toHaveLength(reply === 'disconnect' ? 1 : 2)
    await confirmation.getByRole('button', { name: '取消', exact: true }).click()
    await expect(confirmation).toBeHidden()
    await page.getByRole('tab', { name: '表数据', exact: true }).click()
    if (reply === 'disconnect') await expectProtected(page, 'update', 2)
    else await expect(pageNotice(page)).toHaveCount(0)
    expectOneWrite(api, 'update', reply)
  })
}
