import { expect, test, type Page } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })

type RequestRecord = { method: string; path: string; body?: unknown }
type Credentials = { username: string; password: string; database: string }
type Row = { id: string; label: string }
type Target = { sourceId: string; schema: string; table: string }
type StoreProjection = {
  catalogs: Record<string, { source: { id: string }; tables: Array<{ schema: string; name: string }> }>
  credentials: Record<string, Credentials>
}
const paths = {
  connect: '/api/v1/databases/connect', catalog: '/api/v1/databases/catalog',
  rows: '/api/v1/databases/rows', update: '/api/v1/databases/rows/update',
}
const fixedTime = '2026-10-14T12:00:00.000Z'
const source = {
  id: 'fixture-read-identity', name: '读取身份边界样例', driver: 'postgresql', category: 'project',
  project: '纯内存验收', module: '读取身份', location: 'fixture-identity.invalid:5432',
  host: 'fixture-identity.invalid', port: 5432, defaultDatabase: 'fixture_catalog',
  requiresLogin: true, status: 'credentials_required', reachability: 'host', evidence: 'published-port', tags: ['fixture-only'],
}
const candidate: Credentials = { username: 'fixture_candidate', password: 'fixture-only-candidate', database: 'fixture_catalog' }
const corrected: Credentials = { username: 'fixture_corrected', password: 'fixture-only-corrected', database: 'fixture_corrected_catalog' }
const originalRow: Row = { id: '9007199254740993', label: 'fixture-last-valid-row' }
const recoveredRow: Row = { ...originalRow, label: 'fixture-explicitly-corrected-read' }
const savedRow: Row = { ...originalRow, label: 'fixture-explicit-correct-target-update' }
const poison = 'WRONG_TARGET_MUST_NOT_RENDER'
const table = (schema: string, name = 'fixture_records') => ({
  schema, name, type: 'table',
  columns: [
    { name: 'id', dataType: 'BIGINT', nullable: false, primaryKey: true, position: 1 },
    { name: 'label', dataType: 'TEXT', nullable: false, primaryKey: false, position: 2 },
  ],
})
const rowsReply = (schema: string, name: string, row: Row) => ({
  table: table(schema, name), rows: [row], limit: 10, offset: 0, hasMore: false,
  ordering: { stable: true, columns: ['id'] },
})
const rowsRequest = (target: Target) => ({ ...target, limit: 10, offset: 0, sortColumn: '', sortDirection: '' })
const edit = (page: Page) => page.getByRole('button', { name: '编辑当前行', exact: true })
const remove = (page: Page) => page.getByRole('button', { name: '删除当前行', exact: true })
const insert = (page: Page) => page.getByRole('button', { name: '新增行', exact: true })
const refresh = (page: Page) => page.locator('.data-toolbar').getByRole('button', { name: '刷新', exact: true })
const dialog = (page: Page) => page.getByRole('dialog', { name: '编辑数据行', exact: true })
const save = (page: Page) => dialog(page).getByRole('button', { name: '保存修改', exact: true })
const error = (page: Page) => page.locator('.database-error[role="alert"]')
const connect = (page: Page) => page.locator('.connection-form').getByRole('button', { name: '连接并读取数据表', exact: true })

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }
type Permit = { expected: unknown; payload: unknown; status: number; gate?: Gate }

// 只有当前 localhost 静态资源和已列出的只读 GET 可以回退到公共 fixture。
// 每个数据库 POST 均逐次核对完整请求；未知 API、其他主机、额外写入和 WebSocket 失败关闭。
// 全部目录、凭据和写入效果仅存在于内存，不连接数据库、Agent、PC 或 NAS。
async function fixture(page: Page, baseURL: string, discoverySource = source) {
  const origin = new URL(baseURL).origin
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  await page.clock.install({ time: new Date(fixedTime) })
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: RequestRecord[] = []
  const denied: Array<RequestRecord & { url: string }> = []
  const sockets: string[] = []
  const permits = new Map<string, Permit>()
  const getReads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities',
    '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/jobs',
    '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/databases/project-preferences',
    '/api/v1/preferences/lists/database.table.rows',
  ])
  await page.routeWebSocket('**/*', (socket) => {
    sockets.push(socket.url())
    socket.close()
  })
  await page.route('**/*', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    if (url.origin === origin && !url.pathname.startsWith('/api/') && method === 'GET') return route.fallback()
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    const entry: RequestRecord = { method, path: url.pathname, ...(body === undefined ? {} : { body }) }
    requests.push(entry)
    if (url.origin === origin) {
      if (method === 'POST' && !url.search && permits.has(url.pathname)) {
        const permit = permits.get(url.pathname)!
        permits.delete(url.pathname)
        expect(body).toEqual(permit.expected)
        permit.gate?.received.resolve()
        try {
          if (permit.gate) await permit.gate.release.promise
          await route.fulfill({ status: permit.status, json: permit.payload })
        } finally { permit.gate?.finished.resolve() }
        return
      }
      if (method === 'GET' && url.pathname === '/api/v1/databases/discovery' && !url.search) {
        return route.fulfill({ json: { collectedAt: fixedTime, sources: [discoverySource] } })
      }
      if (method === 'GET' && getReads.has(url.pathname)) return route.fallback()
    }
    denied.push({ ...entry, url: request.url() })
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未许可请求已拒绝' } })
  })
  return {
    requests,
    allow(path: string, expected: unknown, payload: unknown, status = 200) {
      expect(Object.values(paths)).toContain(path)
      expect(permits.has(path), '不能覆盖尚未消费的一次性许可').toBe(false)
      permits.set(path, { expected: structuredClone(expected), payload: structuredClone(payload), status })
    },
    holdRows(expected: unknown, payload: unknown) {
      expect(permits.has(paths.rows)).toBe(false)
      const gate = { received: deferred(), release: deferred(), finished: deferred() }
      permits.set(paths.rows, { expected: structuredClone(expected), payload: structuredClone(payload), status: 200, gate })
      return gate
    },
    async releaseRows(gate: Gate) {
      const response = page.waitForResponse((reply) => new URL(reply.url()).pathname === paths.rows && reply.request().method() === 'POST')
      gate.release.resolve()
      await gate.finished.promise
      await (await response).finished()
    },
    posts() { return requests.filter((request) => request.method === 'POST') },
    expectClean() {
      expect(denied).toEqual([])
      expect(sockets).toEqual([])
      expect(permits.size, '每项许可均须恰好消费一次').toBe(0)
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>

// 仅读取真实 Pinia 投影验证发布边界；不调用 store 方法、不注入目录或凭据。
async function storeState(page: Page): Promise<StoreProjection> {
  return page.evaluate(() => {
    const app = document.getElementById('app') as HTMLElement & {
      __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, StoreProjection> } } } }
    }
    const store = app.__vue_app__.config.globalProperties.$pinia._s.get('database')!
    return JSON.parse(JSON.stringify({ catalogs: store.catalogs, credentials: store.credentials })) as StoreProjection
  })
}
async function fillCredentials(page: Page, credentials: Credentials) {
  for (const [key, label] of [['username', '用户名'], ['password', '密码'], ['database', '数据库名']] as const) {
    await page.locator('.connection-form').getByLabel(label, { exact: true }).fill(credentials[key])
  }
}
async function expectNoAutomaticPost(page: Page, api: Fixture) {
  const before = structuredClone(api.posts())
  // 跨过默认刷新周期，错误读取不得自动重试或升级为写入。
  await page.clock.fastForward(15_000)
  await waitForPageReady(page)
  expect(api.posts()).toEqual(before)
  api.expectClean()
}
async function expectBadRows(page: Page) {
  await expect(error(page)).toContainText('DATABASE_RESPONSE_INVALID')
  await expect(error(page)).toContainText('表数据读取失败')
  await expect(page.locator('body')).not.toContainText(poison)
  await expect(insert(page)).toBeDisabled()
  await expect(refresh(page)).toBeEnabled()
}

test('错误来源目录不发布表或凭据，明确更正可恢复且后续错误目录保留最后有效连接', async ({ page, baseURL }) => {
  expect(baseURL).toBeTruthy()
  const api = await fixture(page, baseURL!)
  const validCatalog = { source, tables: [table('public')] }
  const wrongCatalog = { source: { ...source, id: poison, name: poison }, tables: [table('public', poison)] }
  const diagnostic = { connected: true, code: 'connected', driver: 'postgresql', endpoint: source.location, operation: 'manual_connect', durationMs: 1 }
  api.allow(paths.connect, { sourceId: source.id }, { code: 'credentials_required', message: 'fixture-only', requestId: 'fixture-initial' }, 400)
  await page.goto(`/databases/${source.id}`, { waitUntil: 'domcontentloaded' })
  await expect(connect(page)).toBeEnabled()
  await fillCredentials(page, candidate)
  api.allow(paths.connect, { sourceId: source.id, credentials: candidate }, diagnostic)
  api.allow(paths.catalog, { sourceId: source.id, credentials: candidate }, wrongCatalog)
  await connect(page).click()
  await expect(error(page)).toContainText('DATABASE_CONNECTION_CATALOG_FAILED')
  await expect(error(page)).toContainText('数据库连接已确认，目录读取失败')
  await expect(page.getByRole('region', { name: '数据表和视图列表', exact: true })).toHaveCount(0)
  await expect(page.locator('.connection-pill--success')).toHaveCount(0)
  await expect(page.locator('body')).not.toContainText(poison)
  expect(await storeState(page)).toEqual({ catalogs: {}, credentials: {} })
  for (const [key, label] of [['username', '用户名'], ['password', '密码'], ['database', '数据库名']] as const) {
    await expect(page.locator('.connection-form').getByLabel(label, { exact: true })).toHaveValue(candidate[key])
  }
  await expectNoAutomaticPost(page, api)

  await fillCredentials(page, corrected)
  api.allow(paths.connect, { sourceId: source.id, credentials: corrected }, diagnostic)
  api.allow(paths.catalog, { sourceId: source.id, credentials: corrected }, validCatalog)
  await connect(page).click()
  const openTable = page.getByRole('link', { name: '打开 fixture_records 数据表详情', exact: true })
  await expect(openTable).toBeVisible()
  await expect(page.locator('.connection-pill--success')).toHaveText('已连接')
  await expect(error(page)).toHaveCount(0)
  const accepted = await storeState(page)
  expect(accepted).toEqual({ catalogs: { [source.id]: validCatalog }, credentials: { [source.id]: corrected } })

  api.allow(paths.catalog, { sourceId: source.id, credentials: corrected }, wrongCatalog)
  await page.getByRole('button', { name: '刷新结构', exact: true }).click()
  await expect(page.locator('.resource-state[role="alert"]')).toContainText('DATABASE_RESPONSE_INVALID')
  await expect(openTable).toBeVisible()
  await expect(page.locator('body')).not.toContainText(poison)
  expect(await storeState(page)).toEqual(accepted)
  await expectNoAutomaticPost(page, api)

  api.allow(paths.catalog, { sourceId: source.id, credentials: corrected }, validCatalog)
  await page.getByRole('button', { name: '重新读取目录', exact: true }).click()
  await expect(page.locator('.resource-state[role="alert"]')).toHaveCount(0)
  expect(await storeState(page)).toEqual(accepted)
  expect(api.posts()).toEqual([
    { method: 'POST', path: paths.connect, body: { sourceId: source.id } },
    { method: 'POST', path: paths.connect, body: { sourceId: source.id, credentials: candidate } },
    { method: 'POST', path: paths.catalog, body: { sourceId: source.id, credentials: candidate } },
    { method: 'POST', path: paths.connect, body: { sourceId: source.id, credentials: corrected } },
    ...Array.from({ length: 3 }, () => ({ method: 'POST', path: paths.catalog, body: { sourceId: source.id, credentials: corrected } })),
  ])
  api.expectClean()
})

for (const mismatch of ['table', 'schema'] as const) {
  test(`错误${mismatch === 'table' ? '表名' : '空 schema 身份'}读取不能授权行写入，保留旧行与草稿，明确更正后只写入正确目标`, async ({ page, baseURL }) => {
    expect(baseURL).toBeTruthy()
    const readSource = { ...source, requiresLogin: false, status: 'available' }
    const target = { sourceId: source.id, schema: mismatch === 'table' ? 'public' : '', table: 'fixture_records' }
    const api = await fixture(page, baseURL!, readSource)
    const valid = rowsReply(target.schema, target.table, originalRow)
    // 同一字段合同，仅改变目标身份。空 schema 必须由页面再次精确核对，不能接受服务端补全。
    const wrong = rowsReply(mismatch === 'schema' ? poison : target.schema, mismatch === 'table' ? poison : target.table, { ...originalRow, label: poison })
    api.allow(paths.catalog, { sourceId: target.sourceId }, { source: readSource, tables: [table(target.schema)] })
    api.allow(paths.rows, rowsRequest(target), wrong)
    await page.goto(`/databases/${target.sourceId}/tables/${target.table}?schema=${target.schema}`, { waitUntil: 'domcontentloaded' })
    await expectBadRows(page)
    await expect(edit(page)).toHaveCount(0)
    await expect(remove(page)).toHaveCount(0)
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expectNoAutomaticPost(page, api)

    api.allow(paths.rows, rowsRequest(target), valid)
    await refresh(page).click()
    await expect(page.locator('.data-table')).toContainText(originalRow.label)
    for (const button of [edit(page), remove(page), insert(page)]) await expect(button).toBeEnabled()
    await expect(error(page)).toHaveCount(0)

    const gate = api.holdRows(rowsRequest(target), wrong)
    await refresh(page).click()
    await gate.received.promise
    // 合法旧行在刷新中仍可键盘打开；错误响应到达后必须撤销这个已有草稿的提交权。
    await edit(page).press('Enter')
    await expect(dialog(page)).toBeVisible()
    const label = dialog(page).getByRole('textbox', { name: 'label', exact: true })
    await label.fill('fixture-retained-unsent-draft')
    await api.releaseRows(gate)
    await expectBadRows(page)
    await expect(page.locator('.data-table')).toContainText(originalRow.label)
    await expect(label).toHaveValue('fixture-retained-unsent-draft')
    await expect(save(page)).toBeDisabled()
    await label.press('Enter')
    await expectNoAutomaticPost(page, api)
    await dialog(page).getByRole('button', { name: '取消', exact: true }).click()
    await expect(dialog(page)).toBeHidden()
    for (const button of [edit(page), remove(page), insert(page)]) await expect(button).toBeDisabled()
    expect(api.posts().filter((request) => ![paths.catalog, paths.rows].includes(request.path))).toEqual([])

    api.allow(paths.rows, rowsRequest(target), rowsReply(target.schema, target.table, recoveredRow))
    await refresh(page).click()
    await expect(error(page)).toHaveCount(0)
    await expect(page.locator('.data-table')).toContainText(recoveredRow.label)
    await expect(page.locator('.data-table')).not.toContainText(originalRow.label)
    for (const button of [edit(page), remove(page), insert(page)]) await expect(button).toBeEnabled()
    await edit(page).click()
    await expect(label).toHaveValue(recoveredRow.label)
    await expect(dialog(page).getByRole('textbox', { name: 'id', exact: true })).toHaveValue(originalRow.id)
    await expect(dialog(page).getByRole('textbox', { name: 'id', exact: true })).toBeDisabled()
    await label.fill(savedRow.label)
    const write = { ...target, values: savedRow, keys: { id: originalRow.id } }
    api.allow(paths.update, write, { rowsAffected: 1 })
    api.allow(paths.rows, rowsRequest(target), rowsReply(target.schema, target.table, savedRow))
    await save(page).click()
    await expect(dialog(page)).toBeHidden()
    await expect(page.locator('.data-table')).toContainText(savedRow.label)
    await expect(edit(page)).toBeEnabled()
    await expectNoAutomaticPost(page, api)
    expect(api.posts()).toEqual([
      { method: 'POST', path: paths.catalog, body: { sourceId: target.sourceId } },
      ...Array.from({ length: 4 }, () => ({ method: 'POST', path: paths.rows, body: rowsRequest(target) })),
      { method: 'POST', path: paths.update, body: write },
      { method: 'POST', path: paths.rows, body: rowsRequest(target) },
    ])
    api.expectClean()
  })
}
