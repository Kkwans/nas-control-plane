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
describe('SQL 页大小草稿保护', () => {
  it('未修改的默认查询随页大小变化，表行请求使用新 limit', async () => {
    await mount()
    expect(state.sql).toBe('SELECT * FROM "sample" LIMIT 25')
    mocks.pageSize.value = 50; await settle()
    expect(state.sql).toBe('SELECT * FROM "sample" LIMIT 50')
    mocks.pageSize.value = 100; await settle()
    expect(state.sql).toBe('SELECT * FROM "sample" LIMIT 100')
    expect(rowsCalls().map((call) => JSON.parse(call[1]!.body as string).limit)).toEqual([25, 50, 100])
    expect(queryCalls()).toHaveLength(0)
  })

  it('用户恢复为当前默认文本后继续跟随页大小', async () => {
    await mount(); await editSQL('SELECT id FROM sample WHERE id = 3')
    await editSQL('SELECT * FROM "sample" LIMIT 25')
    mocks.pageSize.value = 50; await settle()
    expect(state.sql).toBe('SELECT * FROM "sample" LIMIT 50')
    expect(queryCalls()).toHaveLength(0)
  })

  it('首屏读取未结束时晚到偏好仍保留已输入草稿', async () => {
    const firstRows = deferred<Response>()
    rowsResponse.mockReturnValueOnce(firstRows.promise)
    await mount(); await editSQL('SELECT id FROM sample WHERE id = 3')
    mocks.pageSize.value = 50; await settle()
    expect(state.sql).toBe('SELECT id FROM sample WHERE id = 3')
    expect(rowsCalls()).toHaveLength(2)
    expect(rowsCalls()[0]![1]!.signal!.aborted).toBe(true)
    firstRows.resolve(json(tableRows)); await settle()
    expect(state.sql).toBe('SELECT id FROM sample WHERE id = 3')
    expect(queryCalls()).toHaveLength(0)
  })

  it('未修改默认查询在途时更新编辑器默认值，不改变已派发快照或重放 SQL', async () => {
    const pending = deferred<Response>()
    queryResponse.mockReturnValueOnce(pending.promise)
    await mount(); executeButton().click(); await settle()
    mocks.pageSize.value = 50; await settle()
    expect(state.sql).toBe('SELECT * FROM "sample" LIMIT 50')
    expect(JSON.parse(queryCalls()[0]![1]!.body as string).sql).toBe('SELECT * FROM "sample" LIMIT 25')
    pending.resolve(json(result())); await settle()
    expect(queryCalls()).toHaveLength(1)
  })

  it.each([
    'SELECT id FROM sample WHERE id = 3',
    'SELECT * FROM "sample" LIMIT 7',
    '',
    '   ',
    '-- custom\nSELECT id FROM sample\nWHERE id > 4',
  ])('页大小变化不改写用户草稿 %j', async (statement) => {
    await mount(); await editSQL(statement)
    mocks.pageSize.value = 50; await settle()
    expect(state.sql).toBe(statement)
    expect(rowsCalls()).toHaveLength(2)
    expect(JSON.parse(rowsCalls()[1]![1]!.body as string).limit).toBe(50)
    expect(queryCalls()).toHaveLength(0)
  })

  it('待确认 SQL 在页大小变化后保留草稿与冻结的 SQL/模式，明确确认只派发一次', async () => {
    await mount(); await selectMode('exec'); await prepareDanger('DELETE FROM sample')
    const snapshot = state.pendingQuery
    mocks.pageSize.value = 50; await settle()
    expect(queryCalls()).toHaveLength(0)
    expect(state.pendingQuery).toBe(snapshot)
    expect(dialog()?.textContent).toContain('DELETE FROM sample')
    await confirm()
    expect(queryCalls()).toHaveLength(1)
    expect(JSON.parse(queryCalls()[0]![1]!.body as string)).toMatchObject({ sql: 'DELETE FROM sample', resultMode: 'exec' })
    expect(state.sql).toBe('DELETE FROM sample')
  })

  it('在途 SQL 页大小变化不能丢失草稿、取消 SQL 或重放 SQL', async () => {
    const pending = deferred<Response>()
    queryResponse.mockReturnValueOnce(pending.promise)
    await mount(); await editSQL('SELECT id FROM sample WHERE id = 3')
    executeButton().click(); await settle()
    const signal = queryCalls()[0]![1]!.signal!
    mocks.pageSize.value = 50; await settle()
    expect(signal.aborted).toBe(false)
    expect(queryCalls()).toHaveLength(1)
    expect(JSON.parse(queryCalls()[0]![1]!.body as string).sql).toBe('SELECT id FROM sample WHERE id = 3')
    pending.resolve(json(result({ columns: ['id'], rows: [[3]] }))); await settle()
    expect(queryCalls()).toHaveLength(1)
    expect(state.sql).toBe('SELECT id FROM sample WHERE id = 3')
  })

  it.each([['invalidate', invalidateSession], ['begin', beginSession]] as const)('%s 后页大小变化不能复活 SQL 草稿或旧组件行请求', async (_, replaceSession) => {
    await mount(); await prepareDanger('DELETE FROM sample')
    replaceSession()
    expect(state.sql).toBe('')
    expect(state.pendingQuery).toBeNull()
    mocks.pageSize.value = 50; await settle()
    await state.runSQL(); await state.confirmDanger(); await settle()
    expect(queryCalls()).toHaveLength(0)
    expect.soft(state.sql).toBe('')
    expect(rowsCalls()).toHaveLength(1)
  })

  it('页大小与源切换在同一 tick 使用新目标的最新默认查询，旧确认零派发', async () => {
    await mount(); await prepareDanger('DELETE FROM sample')
    const oldConfirm = button('确认执行', dialog()!)
    mocks.pageSize.value = 50
    mocks.route.params.sourceId = 'b'
    oldConfirm.click(); await settle()
    expect(queryCalls()).toHaveLength(0)
    expect(state.sql).toBe('SELECT * FROM "sample" LIMIT 50')
    expect(rowsCalls().map((call) => JSON.parse(call[1]!.body as string))).toMatchObject([
      { sourceId: 'a', limit: 25 }, { sourceId: 'b', limit: 50 },
    ])
  })

  it('目录尚未建立编辑器归属时页大小变化应等待当前初始化', async () => {
    const pending = deferred<void>()
    const catalogs = reactive<Record<string, DatabaseCatalog>>({})
    const loadCatalog = vi.fn(() => pending.promise)
    mocks.store.mockReturnValue({ sources: [source('a')], catalogs, source, connection: (sourceId: string) => ({ sourceId, credentials }), loadCatalog })
    await mount(false)
    catalogs.a = { source: source('a'), tables: [table] }
    mocks.pageSize.value = 50; await settle()
    expect(rowsCalls()).toHaveLength(0)
    expect(state.sql).toBe('')
    pending.resolve(); await settle()
    expect(state.sql).toBe('SELECT * FROM "sample" LIMIT 50')
    expect(rowsCalls()).toHaveLength(1)
  })
})
