// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseCatalog, DatabaseRows, DatabaseSource, DatabaseTable, QueryResult } from '@/api/database'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
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
async function confirm() { button('确认执行', dialog()!).click(); await settle() }

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

// 使用真实 SQL 与行读取 transport，只替换 fetch，不开启服务或接触数据库。
describe('SQL 确认目标快照', () => {
  it('冻结确认时连接凭据、SQL 和显式模式，执行时不借用后来编辑的连接', async () => {
    await mount()
    await selectMode('exec')
    await prepareDanger()
    credentials.username = 'changed-user'
    credentials.password = 'fixture-changed'
    credentials.database = 'changed-db'
    await editSQL('DELETE FROM other_table')
    await selectMode('rows')
    await confirm()
    expect(queryCalls()).toHaveLength(1)
    expect(JSON.parse(queryCalls()[0]![1]!.body as string)).toEqual({
      sourceId: 'a', sql: 'DELETE FROM sample', resultMode: 'exec',
      credentials: { username: 'original-user', password: 'fixture-original', database: 'original-db' },
    })
    expect(state.pendingQuery).toBeNull()
  })

  it.each(['source', 'schema', 'table', 'round-trip'] as const)('路由 %s 同 tick 改变后旧确认零派发', async (kind) => {
    await mount(); await prepareDanger()
    expect(rowsCalls()).toHaveLength(1)
    const oldConfirm = button('确认执行', dialog()!)
    if (kind === 'schema') mocks.route.query.schema = 'other'
    else if (kind === 'table') mocks.route.params.table = 'other'
    else mocks.route.params.sourceId = 'b'
    if (kind === 'round-trip') mocks.route.params.sourceId = 'a'
    oldConfirm.click()
    await settle()
    expect(queryCalls()).toHaveLength(0)
    expect(state.pendingQuery).toBeNull()
    if (kind === 'round-trip') {
      expect(rowsCalls().map((call) => JSON.parse(call[1]!.body as string).sourceId)).toEqual(['a', 'a'])
      await openSQL(); await editSQL('SELECT 2'); executeButton().click(); await settle()
      expect(queryCalls()).toHaveLength(1)
      expect(JSON.parse(queryCalls()[0]![1]!.body as string)).toMatchObject({ sourceId: 'a', sql: 'SELECT 2' })
    }
  })

  it.each(['button', 'shortcut'] as const)('路由变化同 tick 旧编辑器 %s 不借用新目标', async (trigger) => {
    await mount(); await editSQL('SELECT id FROM sample')
    mocks.route.params.sourceId = 'b'
    if (trigger === 'button') executeButton().click()
    else shortcut()
    await settle()
    expect(queryCalls()).toHaveLength(0)
    expect(rowsCalls().map((call) => JSON.parse(call[1]!.body as string).sourceId)).toEqual(['a', 'b'])
    await openSQL(); executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(JSON.parse(queryCalls()[0]![1]!.body as string).sourceId).toBe('b')
  })

  it('取消确认释放连接快照，下一次主动确认重新捕获当前连接', async () => {
    await mount(); await prepareDanger()
    button('取消', dialog()!).click(); await settle()
    expect(state.pendingQuery).toBeNull()
    credentials.database = 'new-db'
    await prepareDanger(); await confirm()
    expect(JSON.parse(queryCalls()[0]![1]!.body as string).credentials.database).toBe('new-db')
  })
})

describe('SQL 会话失效归属', () => {
  it.each(['catalog', 'discovery'] as const)('初始化 %s 迟到成功不能给旧组件授予新会话 SQL 权限', async (stage) => {
    const pending = deferred<void>()
    const catalogs = reactive<Record<string, DatabaseCatalog>>({})
    const store = reactive({
      sources: stage === 'discovery' ? [] as DatabaseSource[] : [source('a')],
      catalogs,
      source,
      connection: (sourceId: string) => ({ sourceId, credentials }),
      loadCatalog: vi.fn(() => pending.promise),
      refreshDiscovery: vi.fn(() => pending.promise),
    })
    mocks.store.mockReturnValue(store)
    await mount(false)
    beginSession()
    store.sources = [source('a')]
    catalogs.a = { source: source('a'), tables: [table] }
    pending.resolve()
    await settle(); await openSQL()
    expect(state.sql).toBe('')
    await editSQL('SELECT id FROM sample'); executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(0)
    expect(rowsCalls()).toHaveLength(0)
  })

  it('初始化目录迟到失败不能在新会话发布旧错误', async () => {
    const pending = deferred<void>()
    const store = mocks.store.getMockImplementation()!() as Record<string, unknown>
    mocks.store.mockReturnValue({ ...store, catalogs: {}, loadCatalog: () => pending.promise })
    await mount(false)
    beginSession()
    pending.reject(new Error('fixture late failure'))
    await settle()
    expect(host.querySelector('.database-error')).toBeNull()
    expect(queryCalls()).toHaveLength(0)
    expect(rowsCalls()).toHaveLength(0)
  })

  it('初始化目录请求信号属于原会话', async () => {
    const pending = deferred<void>()
    const loadCatalog = vi.fn(() => pending.promise)
    const store = mocks.store.getMockImplementation()!() as Record<string, unknown>
    mocks.store.mockReturnValue({ ...store, catalogs: {}, loadCatalog })
    await mount(false)
    const signal = (loadCatalog.mock.calls as unknown as Array<[string, AbortSignal]>)[0]![1]
    beginSession()
    expect(signal.aborted).toBe(true)
    pending.resolve(); await settle()
  })

  it.each([['invalidate', invalidateSession], ['begin', beginSession]] as const)('%s 同 tick 撤销待确认和旧编辑器，不能用新会话派发', async (_, replaceSession) => {
    await mount(); await prepareDanger()
    const oldConfirm = button('确认执行', dialog()!)
    replaceSession()
    oldConfirm.click()
    await state.confirmDanger()
    await state.runSQL()
    expect(queryCalls()).toHaveLength(0)
    expect(state.pendingQuery).toBeNull()
    expect(state.dangerOpen).toBe(false)
    await settle()
    await editSQL('SELECT id FROM sample')
    shortcut(); await settle()
    expect(queryCalls()).toHaveLength(0)
  })

  for (const replaceSession of [invalidateSession, beginSession]) {
    it.each(['success', 'error', 'disconnect'] as const)(`${replaceSession.name} 中止在途观察，迟到 %s 不发布结果、错误、刷新或通知`, async (outcome) => {
      const pending = deferred<Response>()
      queryResponse.mockReturnValueOnce(pending.promise)
      await mount(); await prepareDanger(); await confirm()
      const signal = queryCalls()[0]![1]!.signal!
      replaceSession()
      expect(signal.aborted).toBe(true)
      expect(state.queryPending).toBe(false)
      expect(state.dangerBusy).toBe(false)
      expect(state.pendingQuery).toBeNull()
      if (outcome === 'success') pending.resolve(json(result({ columns: ['old'], rows: [[99]] })))
      else if (outcome === 'error') pending.resolve(json({ code: 'sql_invalid', message: 'fixture' }, 400))
      else pending.reject(new TypeError('fixture disconnected'))
      await settle()
      expect(queryCalls()).toHaveLength(1)
      expect(rowsCalls()).toHaveLength(1)
      expect(host.querySelector('.query-table')).toBeNull()
      expect(host.querySelector('.query-message--error')).toBeNull()
      expect(host.querySelector('.query-refresh-warning')).toBeNull()
      expect(mocks.success).not.toHaveBeenCalled()
    })
  }

  it.each(['success', 'error'] as const)('SQL 已确认后读取继续使用原会话，失效后迟到读取 %s 不污染新确认', async (outcome) => {
    await mount(); await prepareDanger()
    const refresh = deferred<Response>()
    rowsResponse.mockReturnValueOnce(refresh.promise)
    await confirm()
    expect(rowsCalls()).toHaveLength(2)
    const signal = rowsCalls()[1]![1]!.signal!
    beginSession()
    expect(signal.aborted).toBe(true)
    await state.initialize(); await openSQL()
    await prepareDanger('UPDATE sample SET id = 8')
    const pending = deferred<Response>()
    queryResponse.mockReturnValueOnce(pending.promise)
    await confirm()
    expect(state.queryPending).toBe(true)
    expect(state.dangerBusy).toBe(true)
    if (outcome === 'success') refresh.resolve(json({ ...tableRows, rows: [{ id: 900 }] }))
    else refresh.resolve(json({ code: 'timeout', message: 'fixture' }, 504))
    await settle()
    expect(state.queryPending).toBe(true)
    expect(state.dangerBusy).toBe(true)
    expect(dialog()?.textContent).toContain('UPDATE sample SET id = 8')
    expect(host.querySelector('.query-refresh-warning')).toBeNull()
    expect(mocks.success).not.toHaveBeenCalled()
    pending.resolve(json(result({ columns: ['current'], rows: [[8]] })))
    await settle()
    expect(state.queryPending).toBe(false)
    expect(state.dangerBusy).toBe(false)
    expect(mocks.success).toHaveBeenCalledTimes(1)
  })

  it('旧执行 finally 不清新会话请求 busy 或关闭新确认', async () => {
    const old = deferred<Response>()
    const current = deferred<Response>()
    queryResponse.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    await mount(); await prepareDanger(); await confirm()
    beginSession()
    await state.initialize(); await openSQL()
    await prepareDanger('UPDATE sample SET id = 9'); await confirm()
    old.resolve(json(result({ columns: ['old'], rows: [[99]] }))); await settle()
    expect(state.queryPending).toBe(true)
    expect(state.dangerBusy).toBe(true)
    expect(dialog()?.textContent).toContain('UPDATE sample SET id = 9')
    expect(host.querySelector('.query-table')).toBeNull()
    expect(rowsCalls()).toHaveLength(2)
    expect(mocks.success).not.toHaveBeenCalled()
    current.resolve(json(result({ columns: ['current'], rows: [[9]] }))); await settle()
    expect(host.querySelector('.query-table [data-table-rows]')?.getAttribute('data-table-rows')).toBe('[{"current":9}]')
    expect(rowsCalls()).toHaveLength(3)
    expect(mocks.success).toHaveBeenCalledTimes(1)
  })

  it('卸载中止观察并清除确认引用，迟到成功不刷新或通知', async () => {
    const pending = deferred<Response>()
    queryResponse.mockReturnValueOnce(pending.promise)
    await mount(); await prepareDanger(); await confirm()
    const signal = queryCalls()[0]![1]!.signal!
    app?.unmount(); app = undefined
    expect(signal.aborted).toBe(true)
    expect(state.pendingQuery).toBeNull()
    pending.resolve(json(result())); await settle()
    expect(rowsCalls()).toHaveLength(1)
    expect(mocks.success).not.toHaveBeenCalled()
  })
})

describe('SQL 有效会话保持可用', () => {
  it.each(['auto', 'rows', 'exec'] as const)('同会话 %s 模式连续主动执行可用且每次只派发一次', async (mode) => {
    await mount(); await selectMode(mode)
    executeButton().click(); shortcut(); await settle()
    executeButton().click(); shortcut(); await settle()
    expect(queryCalls()).toHaveLength(2)
    for (const call of queryCalls()) {
      expect(JSON.parse(call[1]!.body as string).resultMode).toBe(mode === 'auto' ? undefined : mode)
      expect(call[1]!.signal!.aborted).toBe(false)
    }
    expect(rowsCalls()).toHaveLength(3)
  })

  it('初始化行读取失败不能禁用独立 SQL，成功后刷新仍分态', async () => {
    rowsResponse.mockImplementation(async () => json({ code: 'permission_denied', message: 'fixture' }, 403))
    await mount(); executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(host.querySelector('.query-message--error')).toBeNull()
    expect(host.querySelector('.query-refresh-warning')?.textContent).toContain('SQL 已执行，表数据刷新失败')
    executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(2)
  })

  it('新会话正常重新挂载建立新编辑器归属', async () => {
    await mount(); await prepareDanger()
    beginSession(); app?.unmount(); app = undefined; host.remove()
    await mount(); executeButton().click(); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(queryCalls()[0]![1]!.signal!.aborted).toBe(false)
  })
})
