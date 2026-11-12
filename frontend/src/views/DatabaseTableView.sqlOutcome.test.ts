// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, QueryResult } from '@/api/database'
import { beginSession } from '@/session/sessionLifecycle'
import DatabaseTableView from './DatabaseTableView.vue'

const mocks = vi.hoisted(() => ({
  store: vi.fn(), success: vi.fn(),
  pageSize: { value: 25 },
  route: { params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } },
}))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/database', () => ({ useDatabaseStore: mocks.store }))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  mocks.pageSize = ref(25)
  return { useListPreference: () => ({ pageSize: mocks.pageSize }) }
})
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/DatabaseCellEditor.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/SqlEditor.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return { default: defineComponent({
    props: { modelValue: String, disabled: Boolean },
    emits: ['update:modelValue', 'execute'],
    setup: (props, { emit }) => () => h('textarea', {
      'aria-label': 'SQL 编辑器', value: props.modelValue, disabled: props.disabled,
      onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLTextAreaElement).value),
      onKeydown: (event: KeyboardEvent) => { if (event.ctrlKey && event.key === 'Enter') emit('execute') },
    }),
  }) }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const content = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
  return {
    ElMessage: { success: mocks.success },
    ElButton: defineComponent({
      props: { disabled: Boolean, loading: Boolean },
      setup: (props, { slots }) => () => h('button', { disabled: props.disabled || props.loading }, slots.default?.()),
    }),
    ElDialog: defineComponent({
      props: { modelValue: Boolean, title: String }, emits: ['update:modelValue', 'close'],
      setup: (props, { slots }) => () => props.modelValue
        ? h('section', { role: 'dialog' }, [h('h2', props.title), slots.default?.(), slots.footer?.()]) : null,
    }),
    ElTable: defineComponent({
      props: ['data', 'emptyText'],
      emits: ['sort-change'],
      setup: (props, { emit }) => () => h('div', {
        'data-table-rows': JSON.stringify(props.data), 'data-empty-text': props.emptyText,
        onSort: (event: Event) => emit('sort-change', (event as CustomEvent).detail),
      }),
    }),
    ElTableColumn: { render: () => null },
    ElForm: content, ElFormItem: content, ElSwitch: content, ElTag: content, ElInput: content,
  }
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const table: DatabaseTable = {
  schema: '', name: 'sample', type: 'table',
  columns: [{ name: 'id', dataType: 'integer', nullable: false, primaryKey: true, position: 1 }],
}
const tableRows: DatabaseRows = { table, rows: [{ id: 1 }], limit: 25, offset: 0, hasMore: false }
function source(id: string): DatabaseSource {
  return { id, name: `Source ${id}`, driver: 'sqlite', category: 'project', project: '', module: '', location: '', requiresLogin: false, status: 'connected', tags: [] }
}
function result(overrides: Partial<QueryResult> = {}): QueryResult {
  return { columns: [], rows: [], rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 2, ...overrides }
}
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
const vueErrors: unknown[] = []
let fetcher = vi.fn<typeof fetch>()
const queryResponse = vi.fn<() => Promise<Response>>()
const rowsResponse = vi.fn<() => Promise<Response>>()
let credentials = { username: 'original-user', password: 'fixture-original', database: 'original-db' }
interface ViewState {
  initialize: () => Promise<void>
  confirmDanger: () => Promise<void>
  runSQL: () => Promise<void>
  queryPending: boolean
  dangerBusy: boolean
  dangerOpen: boolean
  pendingQuery: unknown
  sql: string
  sqlUnknownTargets: Set<string>
  queryError: unknown
}
let state: ViewState
let app: App | undefined
let host: HTMLDivElement
async function mount(openEditor = true) {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseTableView, { ref: (instance: unknown) => {
    if (instance) state = (instance as { $: { setupState: ViewState } }).$.setupState
  } }) })
  app.config.errorHandler = (error) => { vueErrors.push(error) }
  app.config.warnHandler = () => {}
  app.directive('loading', {})
  app.mount(host)
  await settle()
  if (openEditor) await openSQL()
}
function button(label: string, root: ParentNode = host) {
  const found = [...root.querySelectorAll('button')].find((item) => item.textContent?.includes(label))
  if (!found) throw new Error(`Missing button ${label}`)
  return found
}
async function openSQL() { button('执行 SQL').click(); await settle() }
async function editSQL(statement: string) {
  const input = host.querySelector('textarea')!
  input.value = statement
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}
async function selectMode(mode: 'auto' | 'rows' | 'exec') {
  const select = host.querySelector<HTMLSelectElement>('[aria-label="SQL 结果模式"]')!
  select.value = mode
  select.dispatchEvent(new Event('change', { bubbles: true }))
  await settle()
}
function executeButton() { return host.querySelector<HTMLButtonElement>('.sql-toolbar__actions button')! }
function shortcut() { host.querySelector('textarea')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true })) }
function dialog() { return host.querySelector<HTMLElement>('[role="dialog"]') }
async function confirm() { button(dialog()!.textContent!.includes('确认风险并执行') ? '确认风险并执行' : '确认执行', dialog()!).click(); await settle() }

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  vueErrors.length = 0
  queryResponse.mockImplementation(async () => json(result()))
  rowsResponse.mockImplementation(async () => json(tableRows))
  credentials = { username: 'original-user', password: 'fixture-original', database: 'original-db' }
  fetcher = vi.fn<typeof fetch>().mockImplementation(async (path) => {
    if (path === '/api/v1/databases/query') return queryResponse()
    if (path === '/api/v1/databases/rows') return rowsResponse()
    throw new Error(`Unexpected request ${String(path)}`)
  })
  vi.stubGlobal('fetch', fetcher)
  mocks.pageSize.value = 25
  mocks.route = reactive({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } })
  mocks.store.mockReturnValue({
    sources: [source('a'), source('b')],
    catalogs: { a: { source: source('a'), tables: [table] }, b: { source: source('b'), tables: [table] } },
    source, connection: (sourceId: string) => ({ sourceId, credentials }),
  })
})
afterEach(() => {
  app?.unmount(); app = undefined; host?.remove()
  expect(vueErrors).toEqual([])
  vi.unstubAllGlobals()
})


function json(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status }) }
function queryCalls() { return fetcher.mock.calls.filter(([path]) => path === '/api/v1/databases/query') }
function rowsCalls() { return fetcher.mock.calls.filter(([path]) => path === '/api/v1/databases/rows') }
async function prepareDanger(statement = 'DELETE FROM sample') {
  await editSQL(statement); executeButton().click(); await settle()
  expect(dialog()).not.toBeNull()
}

// 真实 API + Vue；所有请求仅进入 fake fetch，不启动监听器或连接数据库。
function warning() { return host.querySelector('.sql-outcome-warning') }
function errorText() { return host.querySelector('.query-message--error')?.textContent ?? '' }
function envelope(code: string) { return { code, message: 'secret: raw SQL and password must not be shown', requestId: 'safe-request-1' } }
async function makeUnknown() {
  queryResponse.mockRejectedValueOnce(new TypeError('secret-disconnect'))
  await editSQL('UPDATE sample SET n=n+1 WHERE id=1')
  executeButton().click(); await settle()
  expect(queryCalls()).toHaveLength(1)
  expect(warning()).not.toBeNull()
}

describe('SQL 真实响应结果状态', () => {
  it.each([
    ['200 null', () => Promise.resolve(json(null))],
    ['200 empty object', () => Promise.resolve(json({}))],
    ['200 malformed JSON', () => Promise.resolve(new Response('{bad secret', { status: 200 }))],
    ['204 empty', () => Promise.resolve(new Response(null, { status: 204 }))],
    ['201 valid body', () => Promise.resolve(json(result(), 201))],
    ['202 valid body', () => Promise.resolve(json(result(), 202))],
    ['null inner row', () => Promise.resolve(json(result({ columns: ['id'], rows: [null] as unknown as number[][] })))],
    ['bad width', () => Promise.resolve(json(result({ columns: ['id'], rows: [[1, 2]] })))],
    ['negative known count', () => Promise.resolve(json(result({ rowsAffected: -1, rowsAffectedKnown: true })))],
    ['network disconnect', () => Promise.reject(new TypeError('secret-network'))],
    ['fetch abort', () => Promise.reject(new DOMException('secret-abort', 'AbortError'))],
    ['non-JSON 502', () => Promise.resolve(new Response('secret-gateway', { status: 502 }))],
  ])('%s 不冒称失败或完成，零自动刷新/重放且错误脱敏', async (_, respond) => {
    queryResponse.mockImplementationOnce(respond as () => Promise<Response>)
    await mount(); executeButton().click(); shortcut(); await settle()
    expect(errorText()).toContain('结果未确认，可能部分或全部生效')
    expect(errorText()).not.toMatch(/执行失败|SQL 未提交执行|请求未发送|secret/)
    expect(host.querySelector('.query-result header')?.textContent).toContain('结果未确认，可能部分或全部生效')
    expect(host.querySelector('.query-message--success')).toBeNull()
    expect(warning()).not.toBeNull()
    expect(mocks.success).not.toHaveBeenCalled()
    expect(queryCalls()).toHaveLength(1)
    expect(rowsCalls()).toHaveLength(1)
    expect(queryCalls()[0]![1]).toMatchObject({ method: 'POST', redirect: 'error' })
    expect(executeButton().disabled).toBe(false)
    expect(host.querySelector<HTMLTextAreaElement>('textarea')!.disabled).toBe(false)
    expect([...state.sqlUnknownTargets]).toEqual(['["a","","sample"]'])
    expect(JSON.stringify(state.queryError)).not.toMatch(/secret|raw SQL|password|safe-request-1/)
    executeButton().click(); shortcut(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(dialog()?.textContent).toContain('再次执行可能重复生效')
  })

  it.each([
    [400, 'sql_invalid'], [409, 'constraint_failed'], [403, 'permission_denied'],
    [504, 'timeout'], [503, 'agent_unavailable'], [503, 'operation_busy'], [422, 'result_too_large'],
    [400, 'DATABASE_OPERATION_FAILED'], [400, 'result_mode_unsupported'],
    [500, 'DATABASE_INPUT_INVALID'], [400, 'AUTH_UNAUTHORIZED'],
  ])('%i/%s 即使带稳定码也不能证明未执行', async (status, code) => {
    queryResponse.mockImplementationOnce(async () => json(envelope(code as string), status as number))
    await mount(); await selectMode('exec'); executeButton().click(); await settle()
    expect(errorText()).toContain('结果未确认，可能部分或全部生效')
    expect(errorText()).toContain(code)
    expect(errorText()).not.toMatch(/SQL 未提交执行|执行失败|secret/)
    expect(queryCalls()).toHaveLength(1)
    expect(rowsCalls()).toHaveLength(1)
  })

  it.each([
    [503, 'AUTH_UNAVAILABLE'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'],
    [400, 'DATABASE_INPUT_INVALID'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'],
  ])('严格 %i/%s 前置拒绝保留正常连续执行', async (status, code) => {
    queryResponse.mockImplementationOnce(async () => json(envelope(code as string), status as number))
    await mount(); executeButton().click(); await settle()
    expect(errorText()).toContain('SQL 未提交执行')
    expect(errorText()).toContain(code)
    expect(errorText()).not.toContain('secret')
    expect(warning()).toBeNull()
    expect(queryCalls()).toHaveLength(1)
    expect(rowsCalls()).toHaveLength(1)
    executeButton().click(); await settle()
    expect(dialog()).toBeNull()
    expect(queryCalls()).toHaveLength(2)
    expect(rowsCalls()).toHaveLength(2)
    expect(host.querySelector('.query-message--success')).not.toBeNull()
  })

  it('缺失错误信封成员不能借代码冒充确定拒绝', async () => {
    queryResponse.mockImplementationOnce(async () => json({ code: 'DATABASE_INPUT_INVALID', message: 'secret' }, 400))
    await mount(); executeButton().click(); await settle()
    expect(errorText()).toContain('结果未确认')
    expect(errorText()).not.toContain('SQL 未提交执行')
    expect(warning()).not.toBeNull()
  })

  it.each(['serialize', 'headers', 'connection'] as const)('本地 %s 错误零 SQL 请求且不保留原异常', async (kind) => {
    await mount()
    if (kind === 'serialize') credentials.password = 1n as unknown as string
    if (kind === 'headers') vi.stubGlobal('Headers', class { constructor() { throw new Error('secret-headers') } })
    if (kind === 'connection') {
      const store = mocks.store.getMockImplementation()!() as { connection: () => never }
      store.connection = () => { throw new Error('secret-connection') }
    }
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(0)
    expect(errorText()).toContain('SQL 请求未发送')
    expect(errorText()).not.toMatch(/结果未确认|secret|BigInt/)
    expect(warning()).toBeNull()
    expect(state.queryPending).toBe(false)
    expect(state.sqlUnknownTargets.size).toBe(0)
  })

  it.each(['auto', 'rows', 'exec'] as const)('%s 正常成功仍可连续主动执行且每次仅一次请求', async (mode) => {
    await mount(); await selectMode(mode)
    for (let i = 1; i <= 2; i++) {
      executeButton().click(); shortcut(); await settle()
      expect(queryCalls()).toHaveLength(i)
      expect(dialog()).toBeNull()
      expect(warning()).toBeNull()
    }
    expect(rowsCalls()).toHaveLength(3)
  })

  it('合法 Go nil 数组归一为空结果，不崩溃或伪造影响数', async () => {
    queryResponse.mockImplementationOnce(async () => json({ columns: null, rows: null, rowsAffected: -1, truncated: false, durationMs: 0 }))
    await mount(); executeButton().click(); await settle()
    expect(host.querySelector('.query-message--success')?.textContent).toContain('数据库未提供可用的影响行数')
    expect(warning()).toBeNull()
    expect(rowsCalls()).toHaveLength(2)
  })

  it('初次行读取失败仍可执行 SQL；合法成功后的刷新错误保持分态', async () => {
    rowsResponse.mockImplementation(async () => json(envelope('permission_denied'), 403))
    await mount(); executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(host.querySelector('.query-message--success')).not.toBeNull()
    expect(host.querySelector('.query-refresh-warning')?.textContent).toContain('SQL 已执行，表数据刷新失败')
    expect(warning()).toBeNull()
    expect(errorText()).toBe('')
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
  })

  it('合法 SQL 完成后 rows 的畸形错误响应安全降级且只报告刷新失败', async () => {
    await mount()
    rowsResponse.mockImplementationOnce(async () => json({ code: { toString: null }, message: 'private-read-error', requestId: 'fixture' }, 400))
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(rowsCalls()).toHaveLength(2)
    expect(host.querySelector('.query-message--success')).not.toBeNull()
    expect(host.querySelector('.query-refresh-warning')?.textContent).toContain('SQL 已执行，表数据刷新失败')
    expect(host.querySelector('.query-refresh-warning')?.textContent).toContain('DATABASE_OPERATION_FAILED')
    expect(host.textContent).not.toMatch(/SQL 请求未发送|private-read-error/)
    expect(errorText()).toBe('')
    expect(warning()).toBeNull()
  })
})

describe('未知 SQL 的再次主动确认', () => {
  it.each(['auto', 'rows', 'exec'] as const)('%s 同一旧草稿只点击执行不会重复生效；明确确认才发一次', async (mode) => {
    let effects = 0
    queryResponse.mockImplementation(async () => {
      effects += 1
      return effects === 1 ? json(envelope('sql_invalid'), 400) : json(result({ rowsAffectedKnown: true, rowsAffected: 1 }))
    })
    await mount(); await selectMode(mode)
    await editSQL('UPDATE sample SET n=n+1 WHERE id=1; INVALID SQL')
    executeButton().click(); await settle()
    expect(effects).toBe(1)
    expect(errorText()).toContain('结果未确认')
    executeButton().click(); shortcut(); await settle()
    expect(effects).toBe(1)
    expect(queryCalls()).toHaveLength(1)
    expect(dialog()?.textContent).toContain('再次执行可能重复生效')
    await confirm()
    expect(queryCalls()).toHaveLength(2)
    expect(effects).toBe(2)
    expect(warning()).not.toBeNull()
    expect(dialog()).toBeNull()
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(dialog()?.textContent).toContain('确认风险并执行')
  })

  it.each(['SELECT 1', '/* read-looking */ SELECT side_effect()', 'WITH changed AS (UPDATE sample SET id=2 RETURNING id) SELECT * FROM changed'])('新草稿 %s 也需明确风险确认，不能按首词豁免', async (statement) => {
    await mount(); await makeUnknown(); await editSQL(statement)
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(dialog()?.textContent).toContain('即使本次以 SELECT 开头')
    expect(dialog()?.querySelector('pre')?.textContent).toBe(statement)
    await confirm()
    expect(queryCalls()).toHaveLength(2)
    expect(warning()).not.toBeNull()
  })

  it('风险确认冻结目标、连接、原文和模式，草稿修改不替换确认，双击/快捷键不重发', async () => {
    await mount(); await makeUnknown(); await selectMode('rows')
    const original = '  SELECT side_effect(); -- <script>secret</script>\n'
    await editSQL(original); executeButton().click(); await settle()
    expect(dialog()?.textContent).toContain('目标：Source a / 未提供 schema / sample')
    expect(dialog()?.textContent).toContain('结果模式：返回行')
    expect(dialog()?.querySelector('pre')?.textContent).toBe(original)
    expect(dialog()?.querySelector('script')).toBeNull()
    credentials.database = 'later-db'; credentials.password = 'secret-later-password'
    await editSQL('SELECT different()'); await selectMode('exec')
    shortcut(); await state.runSQL(); await settle()
    expect(queryCalls()).toHaveLength(1)
    const pending = deferred<Response>()
    queryResponse.mockReturnValueOnce(pending.promise)
    await confirm(); await state.confirmDanger(); shortcut(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(JSON.parse(queryCalls()[1]![1]!.body as string)).toEqual({
      sourceId: 'a', credentials: { username: 'original-user', password: 'fixture-original', database: 'original-db' },
      sql: original, resultMode: 'rows',
    })
    pending.resolve(json(result())); await settle()
    expect(dialog()).toBeNull()
    expect(warning()).not.toBeNull()
    expect(state.pendingQuery).toBeNull()
    expect(JSON.stringify([...state.sqlUnknownTargets])).not.toMatch(/SELECT|password|secret|original-db/)
  })

  it('取消风险确认零请求，下次建立新快照且不能借旧确认重复执行', async () => {
    await mount(); await makeUnknown(); await editSQL('SELECT 1')
    executeButton().click(); await settle()
    const oldConfirm = button('确认风险并执行', dialog()!)
    button('取消', dialog()!).click(); await settle()
    oldConfirm.click(); await state.confirmDanger(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(state.pendingQuery).toBeNull()
    await editSQL('SELECT 2'); executeButton().click(); await settle()
    expect(dialog()?.querySelector('pre')?.textContent).toBe('SELECT 2')
    await confirm()
    expect(queryCalls()).toHaveLength(2)
    expect(JSON.parse(queryCalls()[1]![1]!.body as string).sql).toBe('SELECT 2')
  })

  it.each(['success', 'failure'] as const)('只读表刷新 %s、换页签和后续成功都不能确认旧未知', async (readOutcome) => {
    await mount(); await makeUnknown()
    if (readOutcome === 'failure') rowsResponse.mockImplementationOnce(async () => json(envelope('timeout'), 504))
    button('表数据').click(); await settle(); button('刷新').click(); await settle()
    expect(rowsCalls()).toHaveLength(2)
    expect(queryCalls()).toHaveLength(1)
    expect(warning()).not.toBeNull()
    await openSQL(); await editSQL('SELECT 1'); executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(1)
    await confirm()
    expect(queryCalls()).toHaveLength(2)
    expect(warning()).not.toBeNull()
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(dialog()).not.toBeNull()
  })

  it('新请求确定前置拒绝也不能替旧未知解锁', async () => {
    await mount(); await makeUnknown(); await editSQL('SELECT 1'); executeButton().click(); await settle()
    queryResponse.mockImplementationOnce(async () => json(envelope('DATABASE_INPUT_INVALID'), 400))
    await confirm()
    expect(errorText()).toContain('SQL 未提交执行')
    expect(warning()).not.toBeNull()
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(dialog()).not.toBeNull()
  })
})

describe('未知标记与迟到结果的页面会话归属', () => {
  it('路由等待新目录、暂时没有编辑器归属时会话失效仍同步清理旧标记', async () => {
    await mount(); await makeUnknown()
    const pending = deferred<void>()
    const store = mocks.store.getMockImplementation()!() as { catalogs: Record<string, unknown>; loadCatalog: () => Promise<void> }
    delete store.catalogs.b
    store.loadCatalog = () => pending.promise
    mocks.route.params.sourceId = 'b'; await settle()
    expect(state.sqlUnknownTargets.size).toBe(1)
    beginSession()
    expect(state.sqlUnknownTargets.size).toBe(0)
    pending.resolve(); await settle()
    expect(queryCalls()).toHaveLength(1)
  })

  it('目标 A 未知不锁 B，切回 A 保留警告与重新确认', async () => {
    await mount(); await makeUnknown()
    mocks.route.params.sourceId = 'b'; await settle(); await openSQL()
    expect(warning()).toBeNull()
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(dialog()).toBeNull()
    mocks.route.params.sourceId = 'a'; await settle(); await openSQL()
    expect(warning()).not.toBeNull()
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(dialog()).not.toBeNull()
  })

  it.each(['success', 'rejection', 'disconnect'] as const)('取消 A 的在途观察后迟到 %s 不污染 B，也不清除 A 未知标记', async (outcome) => {
    const old = deferred<Response>()
    queryResponse.mockReturnValueOnce(old.promise)
    await mount(); executeButton().click(); await settle()
    mocks.route.params.sourceId = 'b'; await settle(); await openSQL()
    expect(queryCalls()[0]![1]!.signal!.aborted).toBe(true)
    expect(warning()).toBeNull()
    executeButton().click(); await settle()
    if (outcome === 'success') old.resolve(json(result({ columns: ['old'], rows: [[999]] })))
    else if (outcome === 'rejection') old.resolve(json(envelope('DATABASE_INPUT_INVALID'), 400))
    else old.reject(new TypeError('secret-old-disconnect'))
    await settle()
    expect(errorText()).toBe('')
    expect(warning()).toBeNull()
    expect(host.textContent).not.toContain('999')
    expect(rowsCalls()).toHaveLength(3)
    mocks.route.params.sourceId = 'a'; await settle(); await openSQL()
    expect(warning()).not.toBeNull()
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(dialog()).not.toBeNull()
  })

  it('A 已返回合法 SQL 结果、仅刷新在途时离开，不新造未知结果', async () => {
    await mount()
    const rows = deferred<Response>(); rowsResponse.mockReturnValueOnce(rows.promise)
    executeButton().click(); await settle()
    expect(rowsCalls()).toHaveLength(2)
    mocks.route.params.sourceId = 'b'; await settle()
    rows.reject(new TypeError('secret-read-disconnect')); await settle()
    mocks.route.params.sourceId = 'a'; await settle(); await openSQL()
    expect(warning()).toBeNull()
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
    expect(dialog()).toBeNull()
  })

  it.each(['route', 'session'] as const)('%s 同 tick 变化撤销未知后的旧风险确认', async (change) => {
    await mount(); await makeUnknown(); await editSQL('SELECT 1'); executeButton().click(); await settle()
    const oldConfirm = button('确认风险并执行', dialog()!)
    if (change === 'route') mocks.route.params.sourceId = 'b'
    else beginSession()
    oldConfirm.click(); await state.confirmDanger(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(state.pendingQuery).toBeNull()
    expect(dialog()).toBeNull()
    if (change === 'session') {
      expect(state.sqlUnknownTargets.size).toBe(0)
      expect(state.sql).toBe('')
      await state.initialize(); await openSQL(); executeButton().click(); await settle()
      expect(queryCalls()).toHaveLength(2)
      expect(dialog()).toBeNull()
    }
  })

  it.each(['success', 'disconnect'] as const)('未知后的新确认执行失效会话，迟到 %s 不能污染新会话 busy/确认', async (outcome) => {
    await mount(); await makeUnknown(); await editSQL('SELECT old()'); executeButton().click(); await settle()
    const old = deferred<Response>(); queryResponse.mockReturnValueOnce(old.promise)
    await confirm(); beginSession()
    expect(state.sqlUnknownTargets.size).toBe(0)
    await state.initialize(); await openSQL(); await prepareDanger('DELETE FROM sample')
    const current = deferred<Response>(); queryResponse.mockReturnValueOnce(current.promise)
    await confirm()
    if (outcome === 'success') old.resolve(json(result()))
    else old.reject(new TypeError('secret-old'))
    await settle()
    expect(state.queryPending).toBe(true)
    expect(state.dangerBusy).toBe(true)
    expect(dialog()?.textContent).toContain('DELETE FROM sample')
    expect(warning()).toBeNull()
    expect(errorText()).toBe('')
    expect(mocks.success).not.toHaveBeenCalled()
    current.resolve(json(result())); await settle()
    expect(state.queryPending).toBe(false)
    expect(mocks.success).toHaveBeenCalledTimes(1)
  })

  it('卸载清除标记和敏感确认，迟到失败不创建跨页面锁', async () => {
    await mount(); await makeUnknown(); await editSQL('SELECT secret()'); executeButton().click(); await settle()
    const pending = deferred<Response>(); queryResponse.mockReturnValueOnce(pending.promise)
    await confirm(); app?.unmount(); app = undefined
    expect(state.sqlUnknownTargets.size).toBe(0)
    expect(state.pendingQuery).toBeNull()
    expect(state.sql).toBe('')
    pending.reject(new TypeError('secret-old')); await settle()
    expect(state.sqlUnknownTargets.size).toBe(0)
    host.remove(); await mount(); executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(3)
    expect(dialog()).toBeNull()
    expect(warning()).toBeNull()
  })
})
