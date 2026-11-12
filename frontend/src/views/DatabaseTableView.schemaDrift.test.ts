// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseCatalog, DatabaseRows, DatabaseSource, DatabaseTable, DatabaseValue } from '@/api/database'
import { NcpApiError } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import DatabaseTableView from './DatabaseTableView.vue'

const mocks = vi.hoisted(() => ({
  execute: vi.fn(), rows: vi.fn(), catalog: vi.fn(), store: vi.fn(), success: vi.fn(),
  update: vi.fn(), remove: vi.fn(), insert: vi.fn(),
  pageSize: { value: 25 },
  route: { params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } },
}))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/database', () => ({ useDatabaseStore: mocks.store }))
vi.mock('@/api/database', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/database')>(),
  executeDatabaseSQL: mocks.execute, loadDatabaseRows: mocks.rows,
  updateDatabaseRow: mocks.update, deleteDatabaseRow: mocks.remove, insertDatabaseRow: mocks.insert,
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
 return { default: defineComponent({props: ['modelValue','column','disabled'], emits:['update:model-value'], setup: (props,{emit}) => () => h('input', {'data-column': props.column.name, 'data-type': props.column.dataType, value: props.modelValue, disabled: props.disabled, onInput:(event:Event) => emit('update:model-value', (event.target as HTMLInputElement).value)})}) }
})
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
      setup: (props, { emit, slots }) => () => h('div', {
        'data-table-rows': JSON.stringify(props.data), 'data-empty-text': props.emptyText,
        onSort: (event: Event) => emit('sort-change', (event as CustomEvent).detail),
      }, slots.default?.()),
    }),
    ElTableColumn: defineComponent({
      props: ['label'],
      setup: (props, { slots }) => () => props.label === '行操作' ? h('div', slots.default?.({ row: { id: 1, label: 'existing' } })) : null,
    }),
    ElForm: content, ElFormItem: content, ElSwitch: content, ElTag: content, ElInput: content,
  }
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const originalTable: DatabaseTable = {
  schema: '', name: 'sample', type: 'table',
  columns: [
    { name: 'id', dataType: 'BIGINT', nullable: false, primaryKey: true, position: 1, writeMode: 'required' },
    { name: 'label', dataType: 'TEXT', nullable: false, primaryKey: false, position: 2 },
    { name: 'enabled', dataType: 'BOOLEAN', nullable: false, primaryKey: false, position: 3 },
  ],
}
const originalRow = { id: '9223372036854775807', label: 'existing', enabled: 'false' }
const keyA = JSON.stringify(['a', '', 'sample'])
function source(id: string): DatabaseSource {
  return { id, name: `Source ${id}`, driver: 'sqlite', category: 'project', project: '', module: '', location: '', requiresLogin: false, status: 'connected', tags: [] }
}
function copyTable() { return structuredClone(originalTable) }
function catalogFor(table: DatabaseTable, sourceId = 'a'): DatabaseCatalog {
  return { source: source(sourceId), tables: [table, { ...table, name: 'other' }, { ...table, schema: 'other' }] }
}
function rowsFor(table: DatabaseTable): DatabaseRows {
  return { table, rows: [originalRow], limit: 25, offset: 0, hasMore: false }
}
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
interface ViewState {
  rowForm: Record<string, string>
  rowDraftColumns: DatabaseTable['columns']
  rowDialogOpen: boolean
  sortColumn: string
  sortDirection: string
  rowDialogMode: 'insert' | 'edit'
  originalKeys: Record<string, DatabaseValue>
  pendingRow: Record<string, DatabaseValue> | null
  dangerOpen: boolean
  mutationPending: boolean
  schemaDrift: boolean
  schemaReloadPending: boolean
  loadedRowsTargetKey: string | null
  rowWriteAttempts: Map<string, { operation: 'update' | 'delete'; status: 'pending' | 'unknown' }>
  insertAttempts: Map<string, 'pending' | 'unknown'>
  openEdit: (row: Record<string, DatabaseValue>) => void
  openInsert: () => void
  removeRow: (row: Record<string, DatabaseValue>) => Promise<void>
  submitRow: () => Promise<void>
  submitInsert: () => Promise<void>
  submitRowWrite: (operation: 'update' | 'delete') => Promise<void>
  confirmDanger: () => Promise<void>
  refreshRows: () => Promise<unknown>
  reloadTableSchema: () => Promise<void>
}
let state: ViewState
let app: App | undefined
let host: HTMLDivElement
let wireTable: DatabaseTable
let store: {
  catalogs: Record<string, DatabaseCatalog>
  sources: DatabaseSource[]
  source: typeof source
  connection: (sourceId: string) => { sourceId: string }
  loadCatalog: (sourceId: string, signal?: AbortSignal) => Promise<DatabaseCatalog>
}
async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseTableView, { ref: (instance: unknown) => {
    if (instance) state = (instance as { $: { setupState: ViewState } }).$.setupState
  } }) })
  app.directive('loading', {})
  app.mount(host)
  await settle()
}
function button(label: string, root: ParentNode = host) {
  const found = [...root.querySelectorAll('button')].find((item) => item.textContent?.includes(label))
  if (!found) throw new Error(`Missing button ${label}`)
  return found
}
function expectNoWrite() {
  expect(mocks.insert).not.toHaveBeenCalled()
  expect(mocks.update).not.toHaveBeenCalled()
  expect(mocks.remove).not.toHaveBeenCalled()
  expect(mocks.execute).not.toHaveBeenCalled()
}
function changedTable() {
  const changed = copyTable()
  changed.columns[2]!.dataType = 'TEXT'
  return changed
}
async function detectDrift() {
  wireTable = changedTable()
  await state.refreshRows()
  expect(state.schemaDrift).toBe(true)
}

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('Real network is forbidden') }))
  mocks.pageSize.value = 25
  mocks.route = reactive({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } })
  wireTable = copyTable()
  store = reactive({
    sources: [source('a'), source('b')], catalogs: { a: catalogFor(copyTable()), b: catalogFor(copyTable(), 'b') },
    source, connection: (sourceId: string) => ({ sourceId }),
    loadCatalog: async (sourceId: string, signal?: AbortSignal) => {
      const result = await mocks.catalog(sourceId, signal) as DatabaseCatalog
      if (signal?.aborted) throw new DOMException('Read aborted', 'AbortError')
      store.catalogs[sourceId] = result
      return result
    },
  })
  mocks.store.mockReturnValue(store)
  mocks.catalog.mockImplementation(async (sourceId: string) => catalogFor(structuredClone(wireTable), sourceId))
  mocks.rows.mockImplementation(async (input: { schema?: string; table: string }) => rowsFor({ ...wireTable, schema: input.schema ?? '', name: input.table }))
  mocks.update.mockResolvedValue({ rowsAffected: 1 })
  mocks.remove.mockResolvedValue({ rowsAffected: 1 })
  mocks.insert.mockResolvedValue({ rowsAffected: 1 })
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.unstubAllGlobals() })

describe('数据库同表结构漂移保护', () => {
  it('初次行结构尚未核对时不能从缓存目录派发新增或行修改', async () => {
    const pending = deferred<DatabaseRows>()
    mocks.rows.mockReturnValueOnce(pending.promise)
    await mount()
    state.openInsert(); state.openEdit(originalRow); await state.removeRow(originalRow)
    await state.submitRow(); await state.submitInsert(); await state.submitRowWrite('update'); await state.confirmDanger()
    expect(state.rowDialogOpen).toBe(false)
    expect(state.dangerOpen).toBe(false)
    expectNoWrite()
    pending.resolve(rowsFor(changedTable()))
    await settle()
    expect(state.schemaDrift).toBe(true)
    expect(state.loadedRowsTargetKey).toBeNull()
  })

  it('缓存 BOOLEAN 与当前 TEXT 不一致时，未编辑 false 不会被转换成布尔值发送', async () => {
    wireTable = changedTable()
    await mount()
    state.openEdit(originalRow); state.rowForm.label = 'changed label'
    await state.submitRow(); await state.submitRowWrite('update')
    state.openInsert(); await state.submitInsert()
    await state.removeRow(originalRow); await state.confirmDanger(); await state.submitRowWrite('delete')
    expectNoWrite()
    expect(state.schemaDrift).toBe(true)
    expect(state.loadedRowsTargetKey).toBeNull()
    expect(button('新增行').disabled).toBe(true)
    expect(host.textContent).toContain('表结构已变化')
  })

  it.each(['add', 'remove', 'type', 'primaryKey', 'nullable', 'default', 'writeMode'] as const)('%s 变化在当前读取接受前阻断写入并保留原编辑草稿', async (change) => {
    await mount()
    state.openEdit(originalRow)
    state.rowForm.label = 'unsaved label'
    const changed = copyTable()
    if (change === 'add') changed.columns.push({ name: 'added', dataType: 'TEXT', nullable: true, primaryKey: false, position: 4 })
    if (change === 'remove') changed.columns.splice(2, 1)
    if (change === 'type') changed.columns[2]!.dataType = 'TEXT'
    if (change === 'primaryKey') changed.columns[1]!.primaryKey = true
    if (change === 'nullable') changed.columns[2]!.nullable = true
    if (change === 'default') changed.columns[2]!.default = 'false'
    if (change === 'writeMode') changed.columns[2]!.writeMode = 'optional-default'
    wireTable = changed
    await state.refreshRows()
    await state.submitRow()
    expectNoWrite()
    expect(state.rowDialogOpen).toBe(true)
    expect(state.rowForm).toEqual({ id: originalRow.id, label: 'unsaved label', enabled: 'false' })
    expect(state.rowDraftColumns).toEqual(originalTable.columns)
    expect(state.originalKeys).toEqual({ id: originalRow.id })
    await settle()
    expect(button('保存修改').disabled).toBe(true)
    expect(host.querySelector<HTMLInputElement>('[data-column="enabled"]')!.dataset.type).toBe('BOOLEAN')
    expect(host.textContent).toContain('草稿不能提交')
  })

  it.each(['insert', 'edit'] as const)('共享目录同 tick 变化不会重解释 %s 草稿或允许旧按钮派发', async (kind) => {
    await mount()
    if (kind === 'insert') state.openInsert()
    else state.openEdit(originalRow)
    state.rowForm.id = originalRow.id; state.rowForm.label = 'kept'; state.rowForm.enabled = 'false'
    store.catalogs.a!.tables[0]!.columns[2]!.dataType = 'TEXT'
    await state.submitRow()
    expectNoWrite()
    expect(state.schemaDrift).toBe(true)
    expect(state.rowDraftColumns[2]!.dataType).toBe('BOOLEAN')
    expect(state.rowForm.enabled).toBe('false')
    await settle()
    expect(host.querySelector<HTMLInputElement>('[data-column="enabled"]')!.dataset.type).toBe('BOOLEAN')
  })

  it('已打开的删除确认遇到结构变化即失效并清除主键', async () => {
    await mount(); await state.removeRow(originalRow)
    expect(state.dangerOpen).toBe(true)
    await detectDrift()
    await state.confirmDanger()
    expect(state.dangerOpen).toBe(false)
    expect(state.pendingRow).toBeNull()
    expectNoWrite()
  })

  it('统计、字段排序、大小写、默认 null、缺省 required 和 rows BASE TABLE 不制造漂移', async () => {
    wireTable.type = 'BASE TABLE'
    wireTable.rowCount = 800
    wireTable.sizeBytes = 4000
    wireTable.definition = 'new display definition'
    wireTable.columns = [...wireTable.columns].reverse().map((column) => ({ ...column, dataType: ` ${column.dataType.toLowerCase()} `, position: 12, default: null, writeMode: 'required' }))
    await mount()
    expect(state.schemaDrift).toBe(false)
    state.openEdit(originalRow); state.rowForm.label = 'changed'
    await state.submitRow()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(mocks.update.mock.calls[0]![0]).toMatchObject({ keys: { id: originalRow.id }, values: { id: originalRow.id, enabled: false, label: 'changed' } })
  })

  it('普通匹配行刷新仍不解除漂移，只能关闭旧草稿后显式读取目录和行', async () => {
    await mount(); state.openEdit(originalRow); state.rowForm.label = 'keep me'
    await detectDrift()
    await state.reloadTableSchema()
    expect(mocks.catalog).not.toHaveBeenCalled()
    expect(state.rowForm.label).toBe('keep me')
    wireTable = copyTable()
    await state.refreshRows()
    expect(state.schemaDrift).toBe(true)
    state.rowDialogOpen = false
    wireTable = changedTable()
    await state.reloadTableSchema()
    expect(mocks.catalog).toHaveBeenCalledTimes(1)
    expect(state.schemaDrift).toBe(false)
    expect(state.rowForm).toEqual({})
    state.openEdit(originalRow); state.rowForm.label = 'new draft'
    await state.submitRow()
    expect(mocks.update.mock.calls[0]![0]).toMatchObject({ values: { enabled: 'false', id: originalRow.id, label: 'new draft' } })
  })

  it('重新读取目录和行的两个 await 期间同步拒绝所有行写入及竞争刷新', async () => {
    await mount(); await detectDrift()
    const pendingCatalog = deferred<DatabaseCatalog>()
    const pendingRows = deferred<DatabaseRows>()
    mocks.catalog.mockReturnValueOnce(pendingCatalog.promise)
    const readCount = mocks.rows.mock.calls.length
    const recovery = state.reloadTableSchema()
    state.openInsert(); state.openEdit(originalRow); await state.removeRow(originalRow)
    await state.submitInsert(); await state.submitRowWrite('delete'); await state.refreshRows(); await state.reloadTableSchema()
    expect(state.schemaReloadPending).toBe(true)
    expect(mocks.catalog).toHaveBeenCalledTimes(1)
    expect(mocks.rows).toHaveBeenCalledTimes(readCount)
    expectNoWrite()
    mocks.rows.mockReturnValueOnce(pendingRows.promise)
    pendingCatalog.resolve(catalogFor(changedTable()))
    await settle()
    expect(state.schemaReloadPending).toBe(true)
    state.openInsert(); await state.submitRow(); await state.refreshRows()
    expect(mocks.rows).toHaveBeenCalledTimes(readCount + 1)
    expectNoWrite()
    pendingRows.resolve(rowsFor(changedTable()))
    await recovery
    expect(state.schemaDrift).toBe(false)
    expect(state.schemaReloadPending).toBe(false)
  })

  it.each(['catalog', 'rows', 'mismatch'] as const)('重新读取 %s 失败或再次漂移仍保留保护，后续明确重读可恢复', async (failure) => {
    await mount(); await detectDrift()
    if (failure === 'catalog') mocks.catalog.mockRejectedValueOnce(new NcpApiError('timeout', 'read failed'))
    if (failure === 'rows') mocks.rows.mockRejectedValueOnce(new NcpApiError('timeout', 'read failed'))
    if (failure === 'mismatch') mocks.rows.mockResolvedValueOnce(rowsFor(copyTable()))
    await state.reloadTableSchema()
    state.openInsert(); await state.submitInsert()
    expect(state.schemaDrift).toBe(true)
    expect(state.schemaReloadPending).toBe(false)
    expectNoWrite()
    await state.reloadTableSchema()
    expect(state.schemaDrift).toBe(false)
  })

  it.each(['source', 'schema', 'table'] as const)('漂移保护按目标保存，%s 切换后的目标仍可用，返回原目标仍受保护', async (dimension) => {
    await mount(); await detectDrift()
    wireTable = copyTable()
    if (dimension === 'source') mocks.route.params.sourceId = 'b'
    if (dimension === 'schema') mocks.route.query.schema = 'other'
    if (dimension === 'table') mocks.route.params.table = 'other'
    await settle()
    expect(state.schemaDrift).toBe(false)
    state.openEdit(originalRow); state.rowForm.label = 'other target'
    await state.submitRow()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    mocks.route.params.sourceId = 'a'; mocks.route.params.table = 'sample'; mocks.route.query.schema = ''
    await settle()
    expect(state.schemaDrift).toBe(true)
    state.openEdit(originalRow); await state.submitRow()
    expect(mocks.update).toHaveBeenCalledTimes(1)
  })

  it.each(['source', 'schema', 'table', 'logout', 'replacement', 'unmount'] as const)('目录恢复遇到 %s 失效，迟到结果不解除原锁或污染新目标', async (change) => {
    await mount(); await detectDrift()
    const pending = deferred<DatabaseCatalog>()
    mocks.catalog.mockReturnValueOnce(pending.promise)
    const recovery = state.reloadTableSchema()
    const signal = mocks.catalog.mock.calls[0]![1] as AbortSignal
    const readCount = mocks.rows.mock.calls.length
    wireTable = copyTable()
    if (change === 'source') mocks.route.params.sourceId = 'b'
    if (change === 'schema') mocks.route.query.schema = 'other'
    if (change === 'table') mocks.route.params.table = 'other'
    if (change === 'logout') invalidateSession()
    if (change === 'replacement') beginSession()
    if (change === 'unmount') { app!.unmount(); app = undefined }
    await settle()
    expect(signal.aborted).toBe(true)
    pending.resolve(catalogFor(changedTable()))
    await recovery; await settle()
    expect(mocks.rows.mock.calls.length).toBe(readCount + (['source', 'schema', 'table'].includes(change) ? 1 : 0))
    expectNoWrite()
    if (['source', 'schema', 'table'].includes(change)) {
      expect(state.schemaDrift).toBe(false)
      mocks.route.params.sourceId = 'a'; mocks.route.params.table = 'sample'; mocks.route.query.schema = ''
      await settle()
    }
    expect(state.schemaDrift).toBe(true)
  })

  it.each(['route', 'logout', 'unmount'] as const)('结构恢复的行读取在 %s 失效后不能解锁或覆盖新数据', async (change) => {
    await mount(); await detectDrift()
    const pending = deferred<DatabaseRows>()
    mocks.rows.mockReturnValueOnce(pending.promise)
    const recovery = state.reloadTableSchema()
    await settle()
    const signal = mocks.rows.mock.calls.at(-1)![1] as AbortSignal
    if (change === 'route') { wireTable = copyTable(); mocks.route.params.sourceId = 'b' }
    if (change === 'logout') invalidateSession()
    if (change === 'unmount') { app!.unmount(); app = undefined }
    await settle()
    expect(signal.aborted).toBe(true)
    pending.resolve(rowsFor(changedTable()))
    await recovery
    expectNoWrite()
    if (change === 'route') {
      expect(state.schemaDrift).toBe(false)
      mocks.route.params.sourceId = 'a'
      await settle()
    }
    expect(state.schemaDrift).toBe(true)
  })

  it('恢复行读取期间目录再次改变，不能借用不配套结果解除保护', async () => {
    await mount(); await detectDrift()
    const pending = deferred<DatabaseRows>()
    mocks.rows.mockReturnValueOnce(pending.promise)
    const recovery = state.reloadTableSchema()
    await settle()
    store.catalogs.a!.tables[0]!.columns[1]!.nullable = true
    pending.resolve(rowsFor(changedTable()))
    await recovery
    expect(state.schemaDrift).toBe(true)
    state.openInsert(); await state.submitInsert()
    expectNoWrite()
  })

  it('目录变化后同 tick 恢复旧值，保护仍保留，草稿可复制但不能提交', async () => {
    await mount(); state.openEdit(originalRow)
    store.catalogs.a!.tables[0]!.columns[2]!.dataType = 'TEXT'
    store.catalogs.a!.tables[0]!.columns[2]!.dataType = 'BOOLEAN'
    await state.submitRow(); await settle()
    expect(state.schemaDrift).toBe(true)
    expect(host.querySelector<HTMLInputElement>('[data-column="label"]')!.disabled).toBe(false)
    expect(button('保存修改').disabled).toBe(true)
    expectNoWrite()
  })

  it('显式结构恢复丢弃已删除字段的旧排序，读取新首屏后可继续编辑', async () => {
    await mount()
    state.sortColumn = 'enabled'; state.sortDirection = 'asc'
    wireTable = copyTable(); wireTable.columns.pop()
    await state.refreshRows()
    expect(state.schemaDrift).toBe(true)
    await state.reloadTableSchema()
    expect(mocks.rows.mock.calls.at(-1)![0]).toMatchObject({ sortColumn: '', sortDirection: '', offset: 0 })
    expect(state.schemaDrift).toBe(false)
  })

  it('旧普通行读取在结构恢复开始后失去归属，不能抢回可编辑状态', async () => {
    await mount(); await detectDrift()
    const oldRows = deferred<DatabaseRows>()
    mocks.rows.mockReturnValueOnce(oldRows.promise)
    const oldRead = state.refreshRows()
    await state.reloadTableSchema()
    expect(state.schemaDrift).toBe(false)
    oldRows.resolve(rowsFor(copyTable()))
    await oldRead
    expect(state.schemaDrift).toBe(false)
    state.openEdit(originalRow); await state.submitRow()
    expect(mocks.update.mock.calls[0]![0]).toMatchObject({ values: { enabled: 'false' } })
  })

  it('漂移不会中止已发出的 UPDATE，已确认结果仍归原请求，恢复也不改变未知 ledger', async () => {
    await mount(); state.openEdit(originalRow); state.rowForm.label = 'in flight'
    const pending = deferred<{ rowsAffected: number }>()
    mocks.update.mockReturnValueOnce(pending.promise)
    const write = state.submitRow()
    const signal = mocks.update.mock.calls[0]![1] as AbortSignal
    await detectDrift()
    await state.reloadTableSchema()
    expect(signal.aborted).toBe(false)
    expect(state.mutationPending).toBe(true)
    expect(mocks.catalog).not.toHaveBeenCalled()
    pending.resolve({ rowsAffected: 0 })
    await write
    expect(mocks.success).toHaveBeenCalledExactlyOnceWith('更新请求已执行')
    expect(state.schemaDrift).toBe(true)
    expect(state.rowWriteAttempts.size).toBe(0)
    state.insertAttempts.set(keyA, 'unknown')
    state.rowWriteAttempts.set(keyA, { operation: 'delete', status: 'unknown' })
    await state.reloadTableSchema()
    expect(state.schemaDrift).toBe(false)
    expect(state.insertAttempts.get(keyA)).toBe('unknown')
    expect(state.rowWriteAttempts.get(keyA)).toEqual({ operation: 'delete', status: 'unknown' })
    state.openInsert(); await state.submitRow(); state.rowDialogOpen = false
    state.openEdit(originalRow); await state.submitRow(); await state.removeRow(originalRow)
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(mocks.insert).not.toHaveBeenCalled()
    expect(mocks.remove).not.toHaveBeenCalled()
    expect(mocks.execute).not.toHaveBeenCalled()
  })
})
