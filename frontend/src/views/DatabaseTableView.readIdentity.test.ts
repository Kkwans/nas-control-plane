// @vitest-environment jsdom
import { createApp, h, nextTick, reactive, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { beginSession } from '@/session/sessionLifecycle'
import {
  loadDatabaseCatalog,
  loadDatabaseRows,
  type DatabaseRows,
  type DatabaseValue,
  type QueryResult,
} from '@/api/database'
import { useDatabaseStore } from '@/stores/database'
import DatabaseTableView from '@/views/DatabaseTableView.vue'
const mocks = vi.hoisted(() => ({
  success: vi.fn(),
  pageSize: { value: 25 },
  route: { params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } },
}))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/api/control', () => ({
  requestDatabaseProjectPreferences: vi.fn().mockResolvedValue([]),
  updateDatabaseProjectPreference: vi.fn(),
}))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  mocks.pageSize = ref(25)
  return { useListPreference: () => ({ pageSize: mocks.pageSize }) }
})
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/DatabaseCellEditor.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      props: ['modelValue', 'column', 'disabled'],
      emits: ['update:model-value'],
      setup:
        (props, { emit }) =>
        () =>
          h('input', {
            'data-column': props.column.name,
            'data-type': props.column.dataType,
            value: props.modelValue,
            disabled: props.disabled,
            onInput: (event: Event) => emit('update:model-value', (event.target as HTMLInputElement).value),
          }),
    }),
  }
})
vi.mock('@/components/SqlEditor.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      props: { modelValue: String, disabled: Boolean },
      emits: ['update:modelValue', 'execute'],
      setup:
        (props, { emit }) =>
        () =>
          h('textarea', {
            'aria-label': 'SQL 编辑器',
            value: props.modelValue,
            disabled: props.disabled,
            onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLTextAreaElement).value),
            onKeydown: (event: KeyboardEvent) => {
              if (event.ctrlKey && event.key === 'Enter') emit('execute')
            },
          }),
    }),
  }
})
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const content = defineComponent({
    setup:
      (_, { slots }) =>
      () =>
        h('div', slots.default?.()),
  })
  return {
    ElMessage: { success: mocks.success },
    ElButton: defineComponent({
      props: { disabled: Boolean, loading: Boolean },
      setup:
        (props, { slots }) =>
        () =>
          h('button', { disabled: props.disabled || props.loading }, slots.default?.()),
    }),
    ElDialog: defineComponent({
      props: { modelValue: Boolean, title: String },
      emits: ['update:modelValue', 'close'],
      setup:
        (props, { slots }) =>
        () =>
          props.modelValue
            ? h('section', { role: 'dialog' }, [h('h2', props.title), slots.default?.(), slots.footer?.()])
            : null,
    }),
    ElTable: defineComponent({
      props: ['data', 'emptyText'],
      emits: ['sort-change'],
      setup:
        (props, { emit, slots }) =>
        () =>
          h(
            'div',
            {
              'data-table-rows': JSON.stringify(props.data),
              'data-empty-text': props.emptyText,
              onSort: (event: Event) => emit('sort-change', (event as CustomEvent).detail),
            },
            slots.default?.(),
          ),
    }),
    ElTableColumn: defineComponent({
      props: ['label'],
      setup:
        (props, { slots }) =>
        () =>
          props.label === '行操作' ? h('div', slots.default?.({ row: { id: 1, label: 'existing' } })) : null,
    }),
    ElForm: content,
    ElFormItem: content,
    ElSwitch: content,
    ElTag: content,
    ElInput: content,
  }
})

const source = (id: string) => ({
  id,
  name: id,
  driver: 'sqlite' as const,
  category: 'project' as const,
  project: '',
  module: '',
  location: '/fixture.sqlite',
  requiresLogin: false,
  status: 'available',
  tags: [],
})
const table = {
  schema: '',
  name: 'sample',
  type: 'table',
  columns: [
    { name: 'id', dataType: 'INTEGER', nullable: false, primaryKey: true, position: 1 },
    { name: 'label', dataType: 'TEXT', nullable: false, primaryKey: false, position: 2 },
  ],
}
const row = { id: 9001, label: 'WRONG_TARGET_ROW' }
const rows = (name = 'sample', schema = '') => ({
  table: { ...table, name, schema },
  rows: [row],
  limit: 25,
  offset: 0,
  hasMore: false,
})
const response = (payload: unknown) => new Response(JSON.stringify(payload), { status: 200 })
const diagnostic = {
  connected: true,
  code: 'connected',
  driver: 'sqlite',
  endpoint: '/fixture.sqlite',
  operation: 'manual_connect',
  durationMs: 1,
}
interface ErrorState {
  code: string
}
interface ViewState {
  tableRows: DatabaseRows | null
  loadedRowsTargetKey: string | null
  canEditRows: boolean
  errorState: ErrorState | null
  rowDialogOpen: boolean
  rowForm: Record<string, string>
  rowWriteAttempts: Map<string, { operation: 'update' | 'delete'; status: 'pending' | 'unknown' }>
  rowWriteRefreshError: { operation: 'update' | 'delete'; error: ErrorState } | null
  insertAttempts: Map<string, 'pending' | 'unknown'>
  insertRefreshError: ErrorState | null
  sqlUnknownTargets: Set<string>
  rowsLoading: boolean
  rowsInitialized: boolean
  sql: string
  queryResult: QueryResult | null
  queryError: unknown
  queryRefreshError: ErrorState | null
  queryPending: boolean
  openEdit: (row: Record<string, DatabaseValue>) => void
  openInsert: () => void
  removeRow: (row: Record<string, DatabaseValue>) => Promise<void>
  confirmDanger: () => Promise<void>
  submitRow: () => Promise<void>
  refreshRows: () => Promise<{ status: string }>
  runSQL: () => Promise<void>
}
let app: App | undefined
let host: HTMLElement
let state: ViewState
beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  setActivePinia(createPinia())
  mocks.route = reactive({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } })
})
afterEach(() => {
  app?.unmount()
  app = undefined
  host?.remove()
  vi.unstubAllGlobals()
})
async function settle() {
  for (let i = 0; i < 40; i++) await nextTick()
}
async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({
    render: () =>
      h(DatabaseTableView, {
        ref: (instance: unknown) => {
          if (instance) state = (instance as { $: { setupState: ViewState } }).$.setupState
        },
      }),
  })
  app.directive('loading', {})
  app.mount(host)
  await settle()
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => (resolve = done))
  return { promise, resolve }
}
function expectReadInvalid(error: unknown) {
  expect(error).toMatchObject({ code: 'DATABASE_RESPONSE_INVALID', message: '数据库服务返回了无法识别的数据。' })
  expect(error).not.toHaveProperty('cause')
  expect(JSON.stringify(error)).not.toContain('WRONG_TARGET_ROW')
}
describe('数据库读取目标身份边界', () => {
  it('错误来源目录不能替换最后有效目录和凭据', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ source: source('a'), tables: [table] }))
      .mockResolvedValueOnce(response({ source: source('b'), tables: [table] }))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.credentials.a = { username: 'previous' }
    await store.loadCatalog('a')
    const previous = store.catalogs.a
    expectReadInvalid(await store.loadCatalog('a').catch((error) => error))
    expect(store.catalogs.a).toBe(previous)
    expect(store.credentials.a).toEqual({ username: 'previous' })
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
  it('连接诊断成功但目录目标不符时不能发布新凭据', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(diagnostic))
      .mockResolvedValueOnce(response({ source: source('b'), tables: [table] }))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.catalogs.a = { source: source('a'), tables: [table] }
    store.credentials.a = { username: 'previous' }
    const previous = store.catalogs.a
    await expect(store.connect('a', { username: 'new-user' })).rejects.toMatchObject({ code: 'DATABASE_CONNECTION_CATALOG_FAILED' })
    expect(store.catalogs.a).toBe(previous)
    expect(store.credentials.a).toEqual({ username: 'previous' })
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
  it('等待目录期间修改输入不改变已派发来源快照', async () => {
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>().mockReturnValue(pending.promise)
    vi.stubGlobal('fetch', fetcher)
    const input = { sourceId: 'a' }
    const result = loadDatabaseCatalog(input)
    input.sourceId = 'b'
    pending.resolve(response({ source: source('b'), tables: [table] }))
    expectReadInvalid(await result.catch((error) => error))
    expect(JSON.parse(fetcher.mock.calls[0]![1]!.body as string).sourceId).toBe('a')
  })
  it('等待行数据期间修改输入不改变已派发表名和 schema 快照', async () => {
    const pending = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockReturnValue(pending.promise))
    const input = { sourceId: 'a', table: 'sample', schema: 'public' }
    const result = loadDatabaseRows(input)
    input.table = 'different'
    input.schema = 'other'
    pending.resolve(response(rows('different', 'other')))
    expectReadInvalid(await result.catch((error) => error))
  })
  it.each([
    ['different_table', ''],
    ['sample', 'different_schema'],
  ])('初次读取错误目标 %s / %s 不授予行编辑权', async (name, schema) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(rows(name, schema)))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    expect(state.tableRows).toBeNull()
    expect(state.loadedRowsTargetKey).toBeNull()
    expect(state.canEditRows).toBe(false)
    expect(state.errorState?.code).toBe('DATABASE_RESPONSE_INVALID')
    state.openEdit(row)
    expect(state.rowDialogOpen).toBe(false)
    await state.submitRow()
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it.each([
    ['different_table', ''],
    ['sample', 'different_schema'],
  ])('刷新错误目标 %s / %s 保留旧行并撤销编辑权，之后可独立恢复', async (name, schema) => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(rows()))
      .mockResolvedValueOnce(response(rows(name, schema)))
      .mockResolvedValueOnce(response(rows()))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    const previous = state.tableRows
    expect(state.canEditRows).toBe(true)
    await state.refreshRows()
    expect(state.tableRows).toBe(previous)
    expect(state.loadedRowsTargetKey).toBeNull()
    expect(state.canEditRows).toBe(false)
    await state.refreshRows()
    expect(state.canEditRows).toBe(true)
    expect(fetcher).toHaveBeenCalledTimes(3)
  })
  it('更新成功后目标不符只报告刷新失败，不重放或改判更新结果', async () => {
    let reads = 0
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (path) => {
      if (path === '/api/v1/databases/rows') return response(++reads === 1 ? rows() : rows('different'))
      if (path === '/api/v1/databases/rows/update') return response({ rowsAffected: 1 })
      throw Error('Unexpected endpoint ' + path)
    })
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    const previous = state.tableRows
    state.openEdit(row)
    state.rowForm.label = 'changed'
    await state.submitRow()
    await settle()
    expect(state.rowWriteAttempts.size).toBe(0)
    expect(state.rowWriteRefreshError).toMatchObject({
      operation: 'update',
      error: { code: 'DATABASE_RESPONSE_INVALID' },
    })
    expect(state.tableRows).toBe(previous)
    expect(state.canEditRows).toBe(false)
    expect(mocks.success).toHaveBeenCalledWith('更新请求已执行')
    expect(fetcher.mock.calls.filter(([path]) => path === '/api/v1/databases/rows/update')).toHaveLength(1)
  })
  it.each([undefined, ''])('API 保留空或缺省 schema %j 的服务端补全行为', async (schema) => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(response(rows('sample', 'public'))))
    expect((await loadDatabaseRows({ sourceId: 'a', table: 'sample', schema })).table.schema).toBe('public')
  })
  it('非空 schema 与标识符按原文比较，不归一大小写空格和引号', async () => {
    const name = ' Case " 名 '
    const schema = ' Schema " '
    const payload = rows(name, schema)
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(response(payload)))
    expect((await loadDatabaseRows({ sourceId: 'a', table: name, schema })).table).toMatchObject({ name, schema })
  })
  it('过期坏读取不能撤销当前编辑权或结束新读取的 loading', async () => {
    const stale = deferred<Response>(),
      latest = deferred<Response>()
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(rows()))
      .mockReturnValueOnce(stale.promise)
      .mockReturnValueOnce(latest.promise)
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    const previous = state.tableRows
    const staleRefresh = state.refreshRows()
    const latestRefresh = state.refreshRows()
    stale.resolve(response(rows('wrong')))
    expect(await staleRefresh).toEqual({ status: 'stale' })
    expect(state.rowsLoading).toBe(true)
    expect(state.errorState).toBeNull()
    expect(state.tableRows).toBe(previous)
    expect(state.canEditRows).toBe(true)
    latest.resolve(response(rows()))
    await latestRefresh
    expect(state.rowsLoading).toBe(false)
    expect(state.rowsInitialized).toBe(true)
    expect(state.canEditRows).toBe(true)
  })
  it('当前坏响应收尾后可独立恢复，且不清除此前任何写结果未知保护', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(rows()))
      .mockResolvedValueOnce(response(rows('wrong')))
      .mockResolvedValueOnce(response(rows()))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    const key = JSON.stringify(['a', '', 'sample'])
    state.insertAttempts.set(key, 'unknown')
    state.rowWriteAttempts.set(key, { operation: 'update', status: 'unknown' })
    state.sqlUnknownTargets.add(key)
    await state.refreshRows()
    expect(state.rowsLoading).toBe(false)
    expect(state.rowsInitialized).toBe(true)
    expect(state.canEditRows).toBe(false)
    await state.refreshRows()
    expect(state.canEditRows).toBe(true)
    expect(state.insertAttempts.get(key)).toBe('unknown')
    expect(state.rowWriteAttempts.get(key)).toEqual({ operation: 'update', status: 'unknown' })
    expect(state.sqlUnknownTargets.has(key)).toBe(true)
    state.openEdit(row)
    await state.submitRow()
    expect(fetcher).toHaveBeenCalledTimes(3)
  })
  it('SQL 成功后目标不符只报告刷新失败，不重放或改判 SQL 结果', async () => {
    let reads = 0
    const queryResult = {
      columns: [],
      rows: [],
      rowsAffected: 1,
      rowsAffectedKnown: true,
      truncated: false,
      durationMs: 1,
    }
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (path) => {
      if (path === '/api/v1/databases/rows') return response(++reads === 1 ? rows() : rows('wrong'))
      if (path === '/api/v1/databases/query') return response(queryResult)
      throw Error('Unexpected endpoint ' + path)
    })
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    const previous = state.tableRows
    state.sql = 'SELECT 1'
    await state.runSQL()
    await settle()
    expect(state.queryResult).toEqual(queryResult)
    expect(state.queryError).toBeNull()
    expect(state.queryRefreshError?.code).toBe('DATABASE_RESPONSE_INVALID')
    expect(state.sqlUnknownTargets.size).toBe(0)
    expect(state.tableRows).toBe(previous)
    expect(state.canEditRows).toBe(false)
    expect(state.queryPending).toBe(false)
    expect(fetcher.mock.calls.filter(([path]) => path === '/api/v1/databases/query')).toHaveLength(1)
  })
  it('新增成功后目标不符只报告刷新失败，不重放或改判新增结果', async () => {
    let reads = 0
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (path) => {
      if (path === '/api/v1/databases/rows') return response(++reads === 1 ? rows() : rows('wrong'))
      if (path === '/api/v1/databases/rows/insert')
        return new Response(JSON.stringify({ rowsAffected: 1 }), { status: 201 })
      throw Error('Unexpected endpoint ' + path)
    })
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    state.openInsert()
    state.rowForm.id = '7'
    state.rowForm.label = 'new'
    await state.submitRow()
    await settle()
    expect(state.insertAttempts.size).toBe(0)
    expect(state.insertRefreshError?.code).toBe('DATABASE_RESPONSE_INVALID')
    expect(state.canEditRows).toBe(false)
    expect(mocks.success).toHaveBeenCalledWith('新增请求已执行')
    expect(fetcher.mock.calls.filter(([path]) => path === '/api/v1/databases/rows/insert')).toHaveLength(1)
  })
  it('手动数据库覆盖不必等于发现元数据中的 defaultDatabase', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn<typeof fetch>()
        .mockResolvedValue(
          response({
            source: { ...source('a'), driver: 'postgresql', defaultDatabase: 'default' },
            tables: [{ ...table, schema: 'public' }],
          }),
        ),
    )
    const result = await loadDatabaseCatalog({ sourceId: 'a', credentials: { username: 'user', database: 'selected' } })
    expect(result.source.defaultDatabase).toBe('default')
  })
  it('旧 Go wire 与 HTTP fixture 保留可选字段缺省兼容', async () => {
    const legacy = (await import('@/api/fixtures/databaseReadResponse.legacy.json')).default
    for (const kind of ['wire', 'http'] as const) {
      const catalog = legacy.catalog[kind]
      const result = legacy.rows[kind]
      const fetcher = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(response(catalog))
        .mockResolvedValueOnce(response(result))
      vi.stubGlobal('fetch', fetcher)
      const actualCatalog = await loadDatabaseCatalog({ sourceId: catalog.source.id })
      const actualRows = await loadDatabaseRows({
        sourceId: catalog.source.id,
        table: result.table.name,
        schema: result.table.schema,
      })
      expect(actualCatalog.source.id).toBe(catalog.source.id)
      expect(actualRows.table.name).toBe(result.table.name)
      expect(actualRows.table.columns[0]?.writeMode).toBeUndefined()
    }
  })

  it('删除成功后目标不符只报告刷新失败，不重放或改判删除结果', async () => {
    let reads = 0
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (path) => {
      if (path === '/api/v1/databases/rows') return response(++reads === 1 ? rows() : rows('wrong'))
      if (path === '/api/v1/databases/rows/delete') return response({ rowsAffected: 1 })
      throw Error('Unexpected endpoint ' + path)
    })
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    store.sources = [source('a')]
    store.catalogs = { a: { source: source('a'), tables: [table] } }
    await mount()
    await state.removeRow(row)
    await state.confirmDanger()
    await settle()
    expect(state.rowWriteAttempts.size).toBe(0)
    expect(state.rowWriteRefreshError).toMatchObject({
      operation: 'delete',
      error: { code: 'DATABASE_RESPONSE_INVALID' },
    })
    expect(state.canEditRows).toBe(false)
    expect(mocks.success).toHaveBeenCalledWith('删除请求已执行')
    expect(fetcher.mock.calls.filter(([path]) => path === '/api/v1/databases/rows/delete')).toHaveLength(1)
  })
  it('API 对显式非空 schema 精确核对，不能只核对表名', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(rows('sample', 'other')))
    vi.stubGlobal('fetch', fetcher)
    expectReadInvalid(
      await loadDatabaseRows({ sourceId: 'a', table: 'sample', schema: 'public' }).catch((error) => error),
    )
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
})
