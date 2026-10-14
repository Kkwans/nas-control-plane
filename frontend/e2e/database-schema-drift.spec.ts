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

type RequestRecord = { method: string; path: string; body?: unknown }
type Row = { id: string; label: string; enabled: string | boolean }
type ReadKind = 'catalog' | 'rows'
const target = { sourceId: 'sqlite-main', schema: '', table: 'settings' }
const paths = { catalog: '/api/v1/databases/catalog', rows: '/api/v1/databases/rows', update: '/api/v1/databases/rows/update' }
const fixedTime = '2026-10-14T12:00:00.000Z'
const originalRow: Row = { id: '9223372036854775807', label: 'fixture-original-row', enabled: 'false' }
const draftLabel = '纯内存未提交草稿 fixture-retained-label-abcdefghijklmnopqrstuvwxyz-0123456789'
const savedLabel = 'fixture-explicit-text-schema-write'
const source = {
  id: target.sourceId, name: '结构保护样例数据库', driver: 'sqlite', category: 'system', project: 'NCP',
  module: '纯内存验收', location: '/fixture-only/schema-drift.db', requiresLogin: false, status: 'available', tags: ['SQLite'],
}
function table(dataType: 'BOOLEAN' | 'TEXT', rows = false) {
  return {
    schema: '', name: target.table, type: rows ? 'BASE TABLE' : 'table',
    columns: [
      { name: 'id', dataType: 'BIGINT', nullable: false, primaryKey: true, position: 1 },
      { name: 'label', dataType: 'TEXT', nullable: false, primaryKey: false, position: 2 },
      { name: 'enabled', dataType, nullable: false, primaryKey: false, position: 3 },
    ],
  }
}
const dialog = (page: Page) => page.getByRole('dialog', { name: '编辑数据行', exact: true })
const editor = (page: Page, name: string) => dialog(page).getByRole('textbox', { name, exact: true })
const save = (page: Page) => dialog(page).getByRole('button', { name: '保存修改', exact: true })
const refresh = (page: Page) => page.locator('.data-toolbar').getByRole('button', { name: '刷新', exact: true })
const reload = (page: Page) => page.getByRole('button', { name: '重新读取表结构', exact: true })
const schemaNotice = (page: Page) => page.locator('.table-workbench-page > .insert-outcome-warning').filter({ hasText: '表结构已变化' })
const unknownNotice = (page: Page) => page.locator('.table-workbench-page > .insert-outcome-warning').filter({ hasText: '更新结果未确认' })
const edit = (page: Page) => page.getByRole('button', { name: '编辑当前行', exact: true })
const remove = (page: Page) => page.getByRole('button', { name: '删除当前行', exact: true })
const insert = (page: Page) => page.getByRole('button', { name: '新增行', exact: true })

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type ReadGate = { kind: ReadKind; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }
function expectedWrite(label: string, enabled: string | boolean): RequestRecord {
  return { method: 'POST', path: paths.update, body: { ...target, values: { ...originalRow, label, enabled }, keys: { id: originalRow.id } } }
}

// 最后一层路由先拦截全部 API，只放行列出的读取和一次明确许可的 UPDATE。
// 结构变化、写入和断网后的效果仅在内存中模拟；不访问数据库、Agent 或 NAS。
async function fixture(page: Page) {
  await page.clock.setFixedTime(new Date(fixedTime))
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: RequestRecord[] = []
  const writes: RequestRecord[] = []
  const denied: RequestRecord[] = []
  const reads: Array<{ kind: ReadKind; schema: 'BOOLEAN' | 'TEXT'; failed: boolean }> = []
  const effects: Row[] = []
  let row = structuredClone(originalRow)
  let currentSchema: 'BOOLEAN' | 'TEXT' = 'BOOLEAN'
  let failRead: ReadKind | undefined
  let heldRead: ReadGate | undefined
  let allowedWrite: { expected: RequestRecord; reply: 'success' | 'disconnect' } | undefined
  const getReads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities',
    '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/jobs',
    '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/databases/project-preferences',
  ])
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    const entry = { method, path, ...(body === undefined ? {} : { body }) }
    requests.push(entry)
    if (method === 'POST' && path === paths.update && allowedWrite) {
      const permission = allowedWrite
      allowedWrite = undefined
      writes.push(entry)
      expect(entry).toEqual(permission.expected)
      row = structuredClone((body as { values: Row }).values)
      effects.push(structuredClone(row))
      if (permission.reply === 'disconnect') return route.abort('failed')
      return route.fulfill({ json: { rowsAffected: 1 } })
    }
    if (method === 'POST' && (path === paths.catalog || path === paths.rows)) {
      const kind = path === paths.catalog ? 'catalog' : 'rows'
      if (kind === 'catalog') expect(body).toEqual({ sourceId: target.sourceId })
      else expect(body).toMatchObject(target)
      const gate = heldRead?.kind === kind ? heldRead : undefined
      if (gate) {
        heldRead = undefined
        gate.received.resolve()
        await gate.release.promise
      }
      try {
        const failed = failRead === kind
        reads.push({ kind, schema: currentSchema, failed })
        if (failed) return await route.fulfill({ status: 503, json: { code: 'agent_unavailable', message: 'fixture-only read failure', requestId: 'fixture-schema-read-failure' } })
        return await route.fulfill({ json: kind === 'catalog'
          ? { source, tables: [table(currentSchema)] }
          : { table: table(currentSchema, true), rows: [row], limit: 10, offset: 0, hasMore: false, ordering: { stable: true, columns: ['id'] } } })
      } finally { gate?.finished.resolve() }
    }
    if (method === 'GET' && path === '/api/v1/databases/discovery') return route.fulfill({ json: { collectedAt: fixedTime, sources: [source] } })
    if (method === 'GET' && (getReads.has(path) || path.startsWith('/api/v1/preferences/lists/'))) return route.fallback()
    denied.push(entry)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期的 API 请求已拒绝' } })
  })
  return {
    requests, writes, denied, reads, effects,
    get row() { return structuredClone(row) },
    drift() { currentSchema = 'TEXT'; row.enabled = 'false' },
    fail(kind?: ReadKind) { failRead = kind },
    hold(kind: ReadKind) {
      expect(heldRead).toBeUndefined()
      const gate = { kind, received: deferred(), release: deferred(), finished: deferred() }
      heldRead = gate
      return gate
    },
    async release(gate: ReadGate) {
      const response = page.waitForResponse((result) => new URL(result.url()).pathname === paths[gate.kind])
      gate.release.resolve()
      await gate.finished.promise
      await (await response).finished()
    },
    allowWrite(label: string, enabled: string | boolean, reply: 'success' | 'disconnect' = 'success') {
      expect(writes).toHaveLength(0)
      expect(allowedWrite).toBeUndefined()
      allowedWrite = { expected: expectedWrite(label, enabled), reply }
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>

function expectMutations(api: Fixture, expected: RequestRecord[] = []) {
  const mutations = api.requests.filter((entry) => !['GET', 'HEAD'].includes(entry.method) && ![paths.catalog, paths.rows].includes(entry.path))
  expect(mutations).toEqual(expected)
  expect(api.writes).toEqual(expected)
  expect(api.denied).toEqual([])
}
function readsSince(api: Fixture, start: number) {
  return api.requests.slice(start).map(({ method, path }) => ({ method, path }))
}
async function ready(page: Page) {
  await page.goto('/databases/sqlite-main/tables/settings', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: target.table, exact: true })).toBeVisible()
  await expect(page.locator('.data-table')).toContainText(originalRow.label)
  await expect(edit(page)).toBeEnabled()
}
async function expectProtected(page: Page) {
  await expect(schemaNotice(page)).toContainText('表结构已变化，新增、编辑和删除已暂停。')
  await expect(schemaNotice(page)).toContainText('普通刷新不会解除保护')
  for (const button of [edit(page), remove(page), insert(page)]) await expect(button).toBeDisabled()
}
async function expectDraft(page: Page, kind: 'boolean' | 'text', label: string) {
  await expect(dialog(page)).toBeVisible()
  await expect(editor(page, 'id')).toHaveValue(originalRow.id)
  await expect(editor(page, 'id')).toBeDisabled()
  await expect(editor(page, 'label')).toHaveValue(label)
  await expect(editor(page, 'label')).toBeEnabled()
  const enabled = dialog(page).locator(`.database-cell-editor[data-field-kind="${kind}"]`).last()
  if (kind === 'boolean') {
    await expect(dialog(page).getByRole('combobox', { name: '布尔字段值', exact: true })).toBeEnabled()
    await expect(enabled.locator('.el-select__selected-item').filter({ hasText: /^false$/ })).toBeVisible()
    await expect(editor(page, 'enabled')).toHaveCount(0)
  } else {
    await expect(editor(page, 'enabled')).toHaveValue('false')
    await expect(dialog(page).getByRole('combobox')).toHaveCount(0)
  }
}
async function closeDraft(page: Page) {
  await dialog(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(dialog(page)).toBeHidden()
}
async function driftDuringDraft(page: Page, api: Fixture) {
  const read = api.hold('rows')
  await refresh(page).click()
  await read.received.promise
  // 加载遮罩阻挡鼠标，但旧行的编辑按钮仍可用键盘激活；不强制穿透遮罩点击。
  await edit(page).press('Enter')
  await editor(page, 'label').fill(draftLabel)
  api.drift()
  await api.release(read)
  await expectProtected(page)
  await expectDraft(page, 'boolean', draftLabel)
  await expect(dialog(page).getByRole('alert')).toContainText('表结构已变化，草稿不能提交。')
  await expect(save(page)).toBeDisabled()
  await expect(reload(page)).toBeDisabled()
  await editor(page, 'label').press('Enter')
  expectMutations(api)
}
async function explicitRecovery(page: Page, api: Fixture) {
  const before = api.requests.length
  const gate = api.hold('rows')
  await reload(page).click()
  await gate.received.promise
  await expectProtected(page)
  expect(readsSince(api, before)).toEqual([{ method: 'POST', path: paths.catalog }, { method: 'POST', path: paths.rows }])
  await api.release(gate)
  await expect(schemaNotice(page)).toHaveCount(0)
  return before
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
type CaptureView = 'blocked-draft' | 'recovered-draft' | 'failed-recovery' | 'unknown-preserved'
async function captureState(page: Page, draft: boolean) {
  return page.evaluate((draft) => {
    const panel = document.querySelector<HTMLElement>(draft ? '.el-overlay-dialog[aria-label="编辑数据行"] .el-dialog' : '.table-workbench-page')!
    // ElSelect 的交互几何断言使用可见 wrapper；隐藏 input 的值、ARIA 和边界另行完整记录。
    const selectors = draft ? [
      '.el-dialog__title', '.el-dialog__headerbtn', '.row-dialog-context strong', '.row-dialog-context small',
      '.insert-outcome-warning', '.insert-outcome-warning strong', '.insert-outcome-warning span',
      '.form-label', '.database-cell-editor__hint', '.database-cell-editor > .el-input input',
      '.database-cell-editor .el-select__wrapper', '.el-dialog__footer button',
    ] : [':scope > .insert-outcome-warning', ':scope > .insert-outcome-warning strong', ':scope > .insert-outcome-warning span', ':scope > .insert-outcome-warning small', ':scope > .insert-outcome-warning button', '.data-toolbar button', '.row-actions button']
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
      const style = getComputedStyle(element)
      return {
        tag: element.tagName, visible: visible(element), bounds: element.getBoundingClientRect().toJSON(),
        role: element.getAttribute('role'), ariaHidden: element.closest('[aria-hidden="true"]') !== null,
        aria: Object.fromEntries([...element.attributes].filter((attribute) => attribute.name.startsWith('aria-')).map((attribute) => [attribute.name, attribute.value])),
        paint: {
          color: style.color, backgroundColor: style.backgroundColor, position: style.position,
          zIndex: style.zIndex, transform: style.transform, isolation: style.isolation,
          opacity: style.opacity, visibility: style.visibility, pointerEvents: style.pointerEvents,
        },
      }
    }
    const notices = [...document.querySelectorAll<HTMLElement>('.insert-outcome-warning')]
    const labels = [...panel.querySelectorAll<HTMLElement>('.form-label')]
    const fields = [...panel.querySelectorAll<HTMLInputElement>('.database-cell-editor > .el-input input')]
    const selects = [...panel.querySelectorAll<HTMLElement>('.database-cell-editor .el-select')]
    const actions = [...panel.querySelectorAll<HTMLButtonElement>('.el-dialog__footer button')]
    const rowActions = [...document.querySelectorAll<HTMLButtonElement>('.row-actions button')]
    const rows = [...document.querySelectorAll<HTMLElement>('.data-table .el-table__body tr')]
    const toasts = [...document.querySelectorAll<HTMLElement>('.el-message')]
    const describeField = (input: HTMLInputElement) => ({ name: input.getAttribute('aria-label'), value: input.value, disabled: input.disabled, selectedText: input.value.slice(input.selectionStart ?? 0, input.selectionEnd ?? 0) })
    const describeSelect = (node: HTMLElement) => ({ text: node.textContent, role: node.querySelector('[role="combobox"]')?.getAttribute('role'), disabled: node.querySelector<HTMLInputElement>('input')?.disabled })
    const describeAction = (button: HTMLButtonElement) => ({ text: button.textContent?.trim(), disabled: button.disabled })
    const describeRowAction = (button: HTMLButtonElement) => ({ label: button.getAttribute('aria-label'), disabled: button.disabled })
    const describeText = (node: HTMLElement) => node.textContent
    const describeTextDiagnostic = (node: HTMLElement) => ({ text: node.textContent, ...diagnostic(node) })
    return {
      viewport: { width: innerWidth, height: innerHeight, devicePixelRatio },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY, containers: [...document.querySelectorAll<HTMLElement>('*')].filter((node) => node.scrollTop || node.scrollLeft).map((node) => ({ tag: node.tagName, class: node.className, top: node.scrollTop, left: node.scrollLeft })) },
      focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label'), class: document.activeElement?.className },
      semantic: {
        notices: notices.filter(visible).map(describeText),
        labels: labels.filter(visible).map(describeText),
        fields: fields.filter(visible).map(describeField),
        selects: selects.filter(visible).map(describeSelect),
        actions: actions.filter(visible).map(describeAction),
        rowActions: rowActions.filter(visible).map(describeRowAction),
        rows: rows.filter(visible).map(describeText),
        toasts: toasts.filter(visible).map(describeText),
      },
      // 表页保留关闭的 ElDialog DOM。可见语义排除隐藏节点，但原始诊断完整保留，
      // 包括 ElSelect 的隐藏输入和值；不改 DOM，也不裁剪完整 axe 报告。
      rawDiagnostics: {
        notices: notices.map(describeTextDiagnostic), labels: labels.map(describeTextDiagnostic),
        fields: fields.map((input) => ({ ...describeField(input), ...diagnostic(input) })),
        selects: selects.map((node) => ({ ...describeSelect(node), ...diagnostic(node) })),
        actions: actions.map((button) => ({ ...describeAction(button), ...diagnostic(button) })),
        rowActions: rowActions.map((button) => ({ ...describeRowAction(button), ...diagnostic(button) })),
        rows: rows.map(describeTextDiagnostic), toasts: toasts.map(describeTextDiagnostic),
        inputs: [...panel.querySelectorAll<HTMLInputElement>('.row-form input')].map((input) => ({
          ...describeField(input), type: input.type, checked: input.checked, readOnly: input.readOnly,
          selectionStart: input.selectionStart, selectionEnd: input.selectionEnd, ...diagnostic(input),
        })),
        selectPaintAncestors: [...panel.querySelectorAll<HTMLElement>('.el-select__placeholder > span')].map((span) => {
          const chain = []
          for (let node: HTMLElement | null = span; node; node = node.parentElement) {
            chain.push({ class: node.className, ...diagnostic(node) })
          }
          return chain
        }),
      },
      controls: selectors.flatMap((selector) => [...panel.querySelectorAll<HTMLElement>(selector)].map((element, index) => {
        const bounds = element.getBoundingClientRect()
        const range = document.createRange()
        range.selectNodeContents(element)
        const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
        const clips: DOMRect[] = []
        for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
          if (/(hidden|clip|auto|scroll)/.test(`${getComputedStyle(ancestor).overflowX} ${getComputedStyle(ancestor).overflowY}`)) clips.push(ancestor.getBoundingClientRect())
        }
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return {
          selector, index, tag: element.tagName, text: element.textContent?.trim(), bounds: bounds.toJSON(),
          disabled: element instanceof HTMLInputElement || element instanceof HTMLButtonElement ? element.disabled : null,
          lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
          scrollWidth: element.scrollWidth, clientWidth: element.clientWidth, scrollLeft: element.scrollLeft,
          visible: visible(element),
          inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth + 1 && bounds.bottom <= innerHeight + 1,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every((clip) => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
        }
      })),
    }
  }, draft)
}

// 实际运行才生成 PNG、完整未过滤 axe、源码/图片 hash 和截图/axe 前后的状态与请求。
async function capture(page: Page, info: TestInfo, api: Fixture, view: CaptureView) {
  const draft = view.endsWith('draft')
  await page.evaluate(() => document.fonts.ready)
  await waitForPageReady(page)
  const provenance = await sourceProvenance()
  const beforeCapture = await captureState(page, draft)
  const requestsBefore = structuredClone(api.requests)
  const name = `database-schema-drift-${view}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: !draft, animations: 'disabled' })
  const afterCapture = await captureState(page, draft)
  const requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await captureState(page, draft)
  const metadata = {
    ...provenance, fixtureOnly: true, nativeDatabaseAcceptance: false,
    fixtureSource: 'frontend/e2e/database-schema-drift.spec.ts + frontend/e2e/mockApi.ts',
    view, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), fullPage: !draft,
    screenshot: `${name}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    fixture: { target, originalRow, draftLabel, row: api.row, reads: api.reads, writes: api.writes, effects: api.effects, denied: api.denied },
    requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  await info.attach(`${name}-axe-state`, { path: axePath, contentType: 'application/json' })
  expect.soft(afterCapture).toEqual(beforeCapture)
  expect.soft(afterAxe.semantic).toEqual(beforeCapture.semantic)
  expect.soft(afterAxe.scroll).toEqual(beforeCapture.scroll)
  expect.soft(afterAxe.focus).toEqual(beforeCapture.focus)
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  expect.soft(beforeCapture.semantic.rowActions).toHaveLength(2)
  expect.soft(beforeCapture.semantic.rowActions.every((action) => action.disabled)).toBe(view !== 'recovered-draft')
  if (draft) {
    expect.soft(beforeCapture.semantic.fields.find((field) => field.name === 'id')).toMatchObject({ value: originalRow.id, disabled: true })
    expect.soft(beforeCapture.semantic.actions).toEqual([{ text: '取消', disabled: false }, { text: '保存修改', disabled: view === 'blocked-draft' }])
    expect.soft(beforeCapture.controls.filter((control) => control.selector === '.database-cell-editor > .el-input input')).toHaveLength(view === 'blocked-draft' ? 2 : 3)
    expect.soft(beforeCapture.controls.filter((control) => control.selector === '.database-cell-editor .el-select__wrapper')).toHaveLength(view === 'blocked-draft' ? 1 : 0)
    if (view === 'blocked-draft') expect.soft(beforeCapture.semantic.fields.find((field) => field.name === 'label')?.selectedText).toBe(draftLabel)
  } else {
    expect.soft(beforeCapture.semantic.fields).toEqual([])
    expect.soft(beforeCapture.semantic.selects).toEqual([])
    expect.soft(beforeCapture.semantic.actions).toEqual([])
    expect.soft(beforeCapture.rawDiagnostics.fields.every((field) => !field.visible && field.value === '')).toBe(true)
    expect.soft(beforeCapture.rawDiagnostics.actions.every((action) => !action.visible)).toBe(true)
  }
  for (const control of beforeCapture.controls) {
    expect.soft(control.visible, JSON.stringify(control)).toBe(true)
    if (draft) expect.soft(control.inViewport, JSON.stringify(control)).toBe(true)
    if (!control.selector.endsWith(' input')) expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (draft && !control.disabled && ['.el-dialog__headerbtn', '.el-dialog__footer button', '.database-cell-editor > .el-input input', '.database-cell-editor .el-select__wrapper'].includes(control.selector)) {
      expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
    }
  }
  expect.soft(axe.violations, JSON.stringify(axe.violations, null, 2)).toEqual([])
}

async function recordFinalFlow(info: TestInfo, api: Fixture) {
  const path = info.outputPath('database-schema-drift-final-flow.json')
  await writeFile(path, JSON.stringify({
    ...(await sourceProvenance()), testTitle: info.title, fixtureOnly: true, nativeDatabaseAcceptance: false,
    target, originalRow, requests: api.requests, reads: api.reads, writes: api.writes,
    effects: api.effects, row: api.row, denied: api.denied,
  }, null, 2))
  await info.attach('database-schema-drift-final-flow', { path, contentType: 'application/json' })
}

for (const width of [390, 1440]) {
  test(`${width}px 同表 BOOLEAN→TEXT：草稿可复制且禁止写入，显式重读后保留字符串 false 和大整数`, async ({ page }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page)
    await ready(page)
    await driftDuringDraft(page, api)
    const combo = dialog(page).getByRole('combobox', { name: '布尔字段值', exact: true })
    const selectControl = dialog(page).locator('.database-cell-editor[data-field-kind="boolean"] .el-select__wrapper')
    for (const value of ['true', 'false']) {
      await expect(combo).toBeEnabled()
      // 原生只读 combobox 被选中值覆盖；点击真实可见外壳，不强制穿透覆盖层。
      await selectControl.click()
      await expect(combo).toHaveAttribute('aria-expanded', 'true')
      await page.getByRole('option', { name: value, exact: true }).click()
      await expect(combo).toHaveAttribute('aria-expanded', 'false')
    }
    await expectDraft(page, 'boolean', draftLabel)
    await editor(page, 'label').press('ControlOrMeta+A')
    await capture(page, info, api, 'blocked-draft')
    expectMutations(api)
    await closeDraft(page)
    const beforeRefresh = api.requests.length
    await refresh(page).click()
    await expect(refresh(page)).toBeEnabled()
    await expectProtected(page)
    expect(readsSince(api, beforeRefresh)).toEqual([{ method: 'POST', path: paths.rows }])
    await explicitRecovery(page, api)
    await expect(edit(page)).toBeEnabled()
    await expect(remove(page)).toBeEnabled()
    await expect(insert(page)).toBeEnabled()
    expectMutations(api)
    await edit(page).click()
    await expectDraft(page, 'text', originalRow.label)
    await editor(page, 'label').fill(savedLabel)
    await capture(page, info, api, 'recovered-draft')
    api.allowWrite(savedLabel, 'false')
    await save(page).click()
    await expect(dialog(page)).toBeHidden()
    await expect(page.locator('.data-table')).toContainText(savedLabel)
    expectMutations(api, [expectedWrite(savedLabel, 'false')])
    expect(api.effects).toEqual([{ ...originalRow, label: savedLabel }])
    expect(api.row).toEqual({ ...originalRow, label: savedLabel })
    await recordFinalFlow(info, api)
  })
}

test('390px 目录或行重读失败均保留结构保护，普通匹配读取也不能解锁', async ({ page }, info) => {
  test.setTimeout(120_000)
  await page.setViewportSize({ width: 390, height: 900 })
  const api = await fixture(page)
  await ready(page)
  await driftDuringDraft(page, api)
  await closeDraft(page)
  for (const failure of ['catalog', 'rows'] as const) {
    api.fail(failure)
    const before = api.requests.length
    const failures = api.reads.filter((read) => read.failed).length
    await reload(page).click()
    await expect.poll(() => api.reads.filter((read) => read.failed).length).toBe(failures + 1)
    await expect(schemaNotice(page)).toContainText('重新读取未完成：Root Agent 暂不可用')
    await expect(reload(page)).toBeEnabled()
    await expectProtected(page)
    expect(readsSince(api, before)).toEqual((failure === 'catalog' ? [paths.catalog] : [paths.catalog, paths.rows]).map((path) => ({ method: 'POST', path })))
    expectMutations(api)
  }
  api.fail()
  await refresh(page).click()
  await expect(refresh(page)).toBeEnabled()
  await expectProtected(page)
  await capture(page, info, api, 'failed-recovery')
  expectMutations(api)
  await explicitRecovery(page, api)
  await expect(edit(page)).toBeEnabled()
  expectMutations(api)
  await recordFinalFlow(info, api)
})

test('1440px 结构重读成功不解除先前 UPDATE 结果未知保护，也不重放写入', async ({ page }, info) => {
  test.setTimeout(120_000)
  await page.setViewportSize({ width: 1440, height: 900 })
  const api = await fixture(page)
  await ready(page)
  await edit(page).click()
  await editor(page, 'label').fill(savedLabel)
  api.allowWrite(savedLabel, false, 'disconnect')
  await save(page).click()
  await expect(unknownNotice(page)).toContainText('更新结果未确认，可能已生效。')
  await closeDraft(page)
  api.drift()
  await refresh(page).click()
  await expect(refresh(page)).toBeEnabled()
  await expectProtected(page)
  await explicitRecovery(page, api)
  await expect(unknownNotice(page)).toContainText('刷新仅重新读取数据，不能证明此次请求成功或失败，也不会解除保护')
  await expect(edit(page)).toBeDisabled()
  await expect(remove(page)).toBeDisabled()
  await capture(page, info, api, 'unknown-preserved')
  expectMutations(api, [expectedWrite(savedLabel, false)])
  expect(api.effects).toEqual([{ ...originalRow, label: savedLabel, enabled: false }])
  await recordFinalFlow(info, api)
})
