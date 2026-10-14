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

const insertPath = '/api/v1/databases/rows/insert'
const rowsPath = '/api/v1/databases/rows'
const target = { sourceId: 'sqlite-main', schema: '', table: 'settings' }
const fixedTime = '2026-10-13T12:00:00.000Z'
const committedValues = {
  id: '9007199254740993',
  key: 'fixture-committed-insert-only-record-abcdefghijklmnopqrstuvwxyz-0123456789',
  value: '安全的纯内存已提交样例。此行只用于验证响应丢失后不能再次新增，不包含真实数据库内容。',
}
const currentValues = {
  id: '9007199254740995',
  key: 'fixture-current-unsent-draft-abcdefghijklmnopqrstuvwxyz-0123456789-abcdefghijklmnopqrstuvwxyz',
  value: '安全的未提交当前草稿，保留关闭重开后的编辑内容。只读刷新不会重放新增，也不能证明原请求的执行结果。',
}
const table = {
  schema: '', name: 'settings', type: 'table',
  columns: [
    { name: 'id', dataType: 'INTEGER', nullable: false, primaryKey: true, position: 1 },
    { name: 'key', dataType: 'TEXT', nullable: false, primaryKey: false, position: 2 },
    { name: 'value', dataType: 'TEXT', nullable: true, primaryKey: false, default: null, position: 3 },
  ],
}
const dialog = (page: Page) => page.getByRole('dialog', { name: '新增数据行', exact: true })
const submit = (page: Page) => dialog(page).getByRole('button', { name: '新增数据行', exact: true })
const open = (page: Page) => page.getByRole('button', { name: '新增行', exact: true })
const refresh = (page: Page) => page.locator('.data-toolbar').getByRole('button', { name: '刷新', exact: true })
const pageNotice = (page: Page) => page.locator('.table-workbench-page > .insert-outcome-warning')
const dialogNotice = (page: Page) => dialog(page).locator('.insert-outcome-warning')

type Reply = 'disconnect' | 'malformed-201' | 'confirmed-201'
type RequestRecord = { method: string; path: string; body?: unknown }
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = { reply: Reply; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }

// 真实浏览器只访问内存 API fixture；rows/catalog 的 POST 是只读请求。
// 仅许可一次 INSERT，并先更新伪造数据再丢失/损坏响应。其余变更和未知 API 路径失败关闭。
async function fixture(page: Page) {
  await page.clock.setFixedTime(new Date(fixedTime))
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: RequestRecord[] = []
  const writes: RequestRecord[] = []
  const denied: RequestRecord[] = []
  const committed: Array<typeof committedValues> = []
  const rowsReads: Array<{ body: unknown; failed: boolean; returnedCommittedRows: number }> = []
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
    if (method === 'POST' && path === insertPath && permitted) {
      const gate = permitted
      permitted = undefined
      writes.push(entry)
      expect(body).toEqual({ ...target, values: committedValues })
      // 提交发生在响应之前；断网后继续只读读取仍能看见此行。
      committed.push(structuredClone(committedValues))
      gate.received.resolve()
      try {
        await gate.release.promise
        if (gate.reply === 'disconnect') await route.abort('failed')
        else if (gate.reply === 'malformed-201') await route.fulfill({ status: 201, contentType: 'application/json', body: '{' })
        else await route.fulfill({ status: 201, json: { rowsAffected: 0 } })
      } finally { gate.finished.resolve() }
      return
    }
    if (method === 'POST' && path === rowsPath) {
      expect(body).toMatchObject(target)
      rowsReads.push({ body, failed: readFailure, returnedCommittedRows: readFailure ? 0 : committed.length })
      if (readFailure) return route.fulfill({ status: 503, json: { code: 'agent_unavailable', message: 'fixture-only read failure', requestId: 'fixture-read-failure' } })
      return route.fulfill({ json: {
        table, rows: [{ id: '1', key: 'fixture-original-row', value: 'fixture-only' }, ...committed],
        limit: 10, offset: 0, hasMore: false, ordering: { stable: true, columns: ['id'] },
      } })
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
    requests, writes, denied, committed, rowsReads,
    failReads(value: boolean) { readFailure = value },
    holdInsert(reply: Reply) {
      expect(permitted).toBeUndefined()
      const gate = { reply, received: deferred(), release: deferred(), finished: deferred() }
      permitted = gate
      return gate
    },
    async release(gate: Gate) {
      const completed = gate.reply === 'disconnect'
        ? page.waitForEvent('requestfailed', (request) => new URL(request.url()).pathname === insertPath && request.method() === 'POST').then(() => undefined)
        : page.waitForResponse((response) => new URL(response.url()).pathname === insertPath && response.request().method() === 'POST').then((response) => response.finished())
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
  await expect(page.locator('.data-table')).toContainText('fixture-original-row')
  await expect(refresh(page)).toBeEnabled()
}
async function openDraft(page: Page) {
  await open(page).click()
  await expect(dialog(page)).toBeVisible()
}
async function fillDraft(page: Page, values: typeof committedValues) {
  for (const [name, value] of Object.entries(values)) await dialog(page).getByRole('textbox', { name, exact: true }).fill(value)
}
async function expectDraft(page: Page, values: typeof committedValues) {
  for (const [name, value] of Object.entries(values)) await expect(dialog(page).getByRole('textbox', { name, exact: true })).toHaveValue(value)
}
async function closeDraft(page: Page, method: 'cancel' | 'escape') {
  if (method === 'cancel') await dialog(page).getByRole('button', { name: '取消', exact: true }).click()
  else await page.keyboard.press('Escape')
  await expect(dialog(page)).toBeHidden()
}
async function expectUnknown(page: Page) {
  await expect(dialogNotice(page)).toContainText('新增结果未确认，可能已生效。')
  await expect(dialogNotice(page)).toContainText('刷新不会解除保护')
  await expect(pageNotice(page)).toContainText('完整重载或离开此页面后不再保留')
  await expect(submit(page)).toBeDisabled()
  await expect(dialog(page).getByRole('button', { name: '取消', exact: true })).toBeEnabled()
  await expect(page.locator('.el-message')).toHaveCount(0)
}
function expectOneInsert(api: Fixture) {
  expect(api.requests.filter((request) => request.path === insertPath)).toEqual([
    { method: 'POST', path: insertPath, body: { ...target, values: committedValues } },
  ])
  expect(api.writes).toHaveLength(1)
  expect(api.committed).toEqual([committedValues])
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
async function captureState(page: Page) {
  return page.evaluate(() => {
    const panel = document.querySelector<HTMLElement>('.el-overlay-dialog[aria-label="新增数据行"] .el-dialog')!
    const selectors = [
      '.el-dialog__header', '.el-dialog__title', '.el-dialog__headerbtn',
      '.row-dialog-context', '.row-dialog-context strong', '.row-dialog-context small',
      '.insert-outcome-warning', '.insert-outcome-warning strong', '.insert-outcome-warning span',
      '.row-form', '.form-label', '.database-cell-editor__hint', '.row-form input',
      '.row-form .database-cell-editor input', '.row-form .el-switch',
      '.el-dialog__footer', '.el-dialog__footer button', '.el-dialog__footer button > span',
    ]
    return {
      viewport: { width: innerWidth, height: innerHeight },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY, overlay: panel.closest('.el-overlay-dialog')?.scrollTop, form: panel.querySelector('.row-form')?.scrollTop },
      focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label') },
      semantic: {
        notice: panel.querySelector('.insert-outcome-warning')?.textContent,
        pageNotice: document.querySelector('.table-workbench-page > .insert-outcome-warning')?.textContent,
        fields: [...panel.querySelectorAll<HTMLInputElement>('.row-form .database-cell-editor input')].map((input) => ({ name: input.getAttribute('aria-label'), value: input.value, disabled: input.disabled })),
        switches: [...panel.querySelectorAll<HTMLElement>('.row-form .el-switch')].map((wrapper) => {
          const input = wrapper.querySelector<HTMLInputElement>('input')
          return {
            field: wrapper.closest('.el-form-item')?.querySelector('.database-cell-editor input')?.getAttribute('aria-label'),
            role: input?.getAttribute('role'), checked: input?.checked, ariaChecked: input?.getAttribute('aria-checked'), disabled: input?.disabled,
          }
        }),
        actions: [...panel.querySelectorAll<HTMLButtonElement>('.el-dialog__footer button')].map((button) => ({ text: button.textContent?.trim(), disabled: button.disabled })),
        toasts: [...document.querySelectorAll('.el-message')].map((message) => message.textContent),
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
          text: element.textContent?.trim(), bounds: bounds.toJSON(),
          lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
          scrollWidth: element.scrollWidth, clientWidth: element.clientWidth, scrollLeft: element.scrollLeft,
          visible: bounds.width > 0 && bounds.height > 0 && style.display !== 'none' && style.visibility !== 'hidden',
          inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth + 1 && bounds.bottom <= innerHeight + 1,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every((clip) => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
        }
      })),
    }
  })
}

// 产物只能由真实浏览器执行生成；不注入 DOM/CSS、不隐藏控件、不改变焦点或筛掉 axe 规则。
async function capture(page: Page, info: TestInfo, api: Fixture, stateName: string) {
  await page.evaluate(() => document.fonts.ready)
  await waitForPageReady(page)
  const provenance = await sourceProvenance()
  const beforeCapture = await captureState(page)
  const requestsBefore = structuredClone(api.requests)
  const name = `database-insert-unknown-${stateName}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const afterCapture = await captureState(page)
  const requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await captureState(page)
  const metadata = {
    ...provenance, fixtureOnly: true,
    fixtureSource: 'frontend/e2e/database-insert-outcome.spec.ts + frontend/e2e/mockApi.ts',
    stateName, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
    screenshot: `${name}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    fixture: { target, committed: api.committed, currentDraft: currentValues, writes: api.writes, rowsReads: api.rowsReads, denied: api.denied },
    requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  await info.attach(`${name}-axe-state`, { path: axePath, contentType: 'application/json' })

  // 先保存完整证据再做 soft 断言，便于失败时继续验证刷新/关闭的写入计数。
  expect.soft(afterCapture).toEqual(beforeCapture)
  expect.soft(afterAxe.semantic).toEqual(beforeCapture.semantic)
  expect.soft(afterAxe.scroll).toEqual(beforeCapture.scroll)
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  const required = [
    '.el-dialog__title', '.el-dialog__headerbtn', '.row-dialog-context strong', '.row-dialog-context small',
    '.insert-outcome-warning', '.insert-outcome-warning strong', '.insert-outcome-warning span',
    '.form-label', '.database-cell-editor__hint', '.row-form .database-cell-editor input', '.row-form .el-switch',
    '.el-dialog__footer button', '.el-dialog__footer button > span',
  ]
  expect.soft(beforeCapture.semantic.fields).toEqual(Object.entries(currentValues).map(([name, value]) => ({ name, value, disabled: false })))
  expect.soft(beforeCapture.semantic.switches).toEqual([{ field: 'value', role: 'switch', checked: false, ariaChecked: 'false', disabled: false }])
  // CI 原始证据确认 ElSwitch 的语义 checkbox 为 0×0；保留全部 input 诊断，
  // 几何分别检查三个编辑框和实际接收点击的可见 switch 外壳，不能把隐藏 checkbox 当成编辑框。
  expect.soft(beforeCapture.controls.filter((item) => item.selector === '.row-form .database-cell-editor input')).toHaveLength(3)
  expect.soft(beforeCapture.controls.filter((item) => item.selector === '.row-form .el-switch')).toHaveLength(1)
  for (const control of beforeCapture.controls.filter((item) => required.includes(item.selector))) {
    expect.soft(control.visible && control.inViewport, JSON.stringify(control)).toBe(true)
    // 长输入值可在输入控件内正常横向滚动；提示和操作文案必须完整可读。
    if (control.selector !== '.row-form .database-cell-editor input') expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (['.el-dialog__headerbtn', '.el-dialog__footer button', '.row-form .database-cell-editor input', '.row-form .el-switch'].includes(control.selector)) expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
  }
  expect.soft(axe.violations, JSON.stringify(axe.violations, null, 2)).toEqual([])
}

for (const scenario of [{ width: 390, reply: 'disconnect' }, { width: 1440, reply: 'malformed-201' }] as const) {
  test(`${scenario.width}px 已提交 INSERT 遇到 ${scenario.reply}：旧响应不覆盖新草稿，关闭和只读刷新不重放`, async ({ page }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width: scenario.width, height: 900 })
    const api = await fixture(page)
    await ready(page)
    await openDraft(page)
    await fillDraft(page, committedValues)
    const gate = api.holdInsert(scenario.reply)
    await submit(page).click()
    await gate.received.promise
    await expect(submit(page)).toBeDisabled()
    await expect(dialog(page).getByRole('textbox', { name: 'key', exact: true })).toBeDisabled()
    expectOneInsert(api)

    await closeDraft(page, 'cancel')
    await openDraft(page)
    await fillDraft(page, currentValues)
    await expect(submit(page)).toBeDisabled()
    await dialog(page).getByRole('textbox', { name: 'key', exact: true }).press('Enter')
    expectOneInsert(api)
    await api.release(gate)
    await expectUnknown(page)
    await expectDraft(page, currentValues)
    expect(api.rowsReads).toHaveLength(1)
    await capture(page, info, api, `${scenario.reply}-current-draft`)

    await closeDraft(page, 'escape')
    await expect(pageNotice(page)).toContainText('新增结果未确认，可能已生效。')
    const beforeRead = api.requests.length
    await refresh(page).click()
    await expect(page.locator('.data-table')).toContainText(committedValues.key)
    await expect(refresh(page)).toBeEnabled()
    expect(api.requests.slice(beforeRead)).toEqual([{ method: 'POST', path: rowsPath, body: api.rowsReads.at(-1)!.body }])
    expect(api.rowsReads.at(-1)).toMatchObject({ failed: false, returnedCommittedRows: 1 })
    await expect(pageNotice(page)).toContainText('不能证明此次新增成功或失败，也不会解除保护')

    await openDraft(page)
    await fillDraft(page, currentValues)
    await expectUnknown(page)
    await dialog(page).getByRole('textbox', { name: 'value', exact: true }).press('Enter')
    await settle(page)
    await expectDraft(page, currentValues)
    expectOneInsert(api)
    await capture(page, info, api, `${scenario.reply}-after-read-reopen`)
    expectOneInsert(api)
  })
}

test('合法 201 / rowsAffected 0 与后续读失败分开报告；恢复只重新读取表数据', async ({ page }) => {
  const api = await fixture(page)
  await ready(page)
  await openDraft(page)
  await fillDraft(page, committedValues)
  const gate = api.holdInsert('confirmed-201')
  api.failReads(true)
  await submit(page).click()
  await gate.received.promise
  await api.release(gate)
  await expect(dialog(page)).toBeHidden()
  await expect(pageNotice(page)).toContainText('新增请求已执行，表数据刷新失败。')
  await expect(pageNotice(page)).toContainText('请只刷新表数据，勿重复新增')
  await expect(pageNotice(page)).not.toContainText('新增结果未确认')
  await expect(page.locator('.el-message--success')).toContainText('新增请求已执行')
  expect(api.rowsReads).toHaveLength(2)
  expect(api.rowsReads[1]).toMatchObject({ failed: true })
  expectOneInsert(api)

  api.failReads(false)
  const beforeRead = api.requests.length
  await refresh(page).click()
  await expect(page.locator('.data-table')).toContainText(committedValues.key)
  await expect(pageNotice(page)).toHaveCount(0)
  expect(api.requests.slice(beforeRead)).toEqual([{ method: 'POST', path: rowsPath, body: api.rowsReads.at(-1)!.body }])
  expect(api.rowsReads).toHaveLength(3)
  expectOneInsert(api)
})
