// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, DatabaseValue, QueryResult } from '@/api/database'
import { NcpApiError } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import DatabaseTableView from './DatabaseTableView.vue'

const mocks = vi.hoisted(() => ({
  execute: vi.fn(), rows: vi.fn(), store: vi.fn(), success: vi.fn(),
  pageSize: { value: 25 },
  route: { params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } },
}))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/database', () => ({ useDatabaseStore: mocks.store }))
vi.mock('@/api/database', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/database')>(),
  executeDatabaseSQL: mocks.execute, loadDatabaseRows: mocks.rows,
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
 return { default: defineComponent({props: ['modelValue','column','disabled'], emits:['update:model-value'], setup: (props,{emit}) => () => h('input', {'data-column': props.column.name, value: props.modelValue, disabled: props.disabled, onInput:(event:Event) => emit('update:model-value', (event.target as HTMLInputElement).value)})}) }
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
const table: DatabaseTable = {
  schema: '', name: 'sample', type: 'table',
  columns: [{ name: 'id', dataType: 'integer', nullable: false, primaryKey: true, position: 1, writeMode: 'server-generated' }, { name: 'label', dataType: 'text', nullable: false, primaryKey: false, position: 2 }],
}
const tableRows: DatabaseRows = { table, rows: [{ id: 1 }], limit: 25, offset: 0, hasMore: false }
function source(id: string): DatabaseSource {
  return { id, name: `Source ${id}`, driver: 'sqlite', category: 'project', project: '', module: '', location: '', requiresLogin: false, status: 'connected', tags: [] }
}
function result(overrides: Partial<QueryResult> = {}): QueryResult {
  return { columns: [], rows: [], rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 2, ...overrides }
}
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
type Operation = 'update' | 'delete'
interface ViewState {
  rowForm: Record<string, string>
  rowNullFields: Record<string, boolean>
  originalKeys: Record<string, unknown>
  rowWriteAttempts: Map<string, { operation: Operation; status: string }>
  insertAttempts: Map<string, string>
  mutationPending: boolean
  dangerBusy: boolean
  rowDialogOpen: boolean
  dangerOpen: boolean
  dangerGeneration: number
  pendingRow: Record<string, unknown> | null
  pendingQuery: { sql: string; resultMode: string } | null
  dangerAction: 'delete-row' | 'sql'
  sql: string
  rowDialogMode: 'insert' | 'edit'
  openEdit: (row: Record<string, DatabaseValue>) => void
  openInsert: () => void
  removeRow: (row: Record<string, DatabaseValue>) => Promise<void>
  submitRow: () => Promise<void>
  confirmDanger: () => Promise<void>
  runSQL: () => Promise<void>
  refreshRows: () => Promise<unknown>
}
let state: ViewState
let app: App | undefined
let host: HTMLDivElement
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
const row = { id: 1, label: 'existing' }
const keyA = JSON.stringify(['a', '', 'sample'])
function button(label: string, root: ParentNode = host) {
  const found = [...root.querySelectorAll('button')].find((item) => item.textContent?.includes(label))
  if (!found) throw new Error(`Missing button ${label}`)
  return found
}
function response(rowsAffected = 1) {
  return new Response(JSON.stringify({ rowsAffected }), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
function useFetch(implementation: typeof fetch = async () => response()) {
  const fetcher = vi.fn<typeof fetch>(implementation)
  vi.stubGlobal('fetch', fetcher)
  return fetcher
}
async function prepare(operation: Operation) {
  if (operation === 'update') { state.openEdit(row); state.rowForm.label = 'draft' }
  else await state.removeRow(row)
}
function submit(operation: Operation) { return operation === 'update' ? state.submitRow() : state.confirmDanger() }
function close(operation: Operation) {
  if (operation === 'update') state.rowDialogOpen = false
  else state.dangerOpen = false
}
function busy(operation: Operation) { return operation === 'update' ? state.mutationPending : state.dangerBusy }

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  mocks.pageSize.value = 25
  mocks.route = reactive({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } })
  mocks.store.mockReturnValue({
    sources: [source('a'), source('b')],
    catalogs: { a: { source: source('a'), tables: [table, { ...table, name: 'other' }, { ...table, schema: 'other' }, { ...table, schema: '|' }, { ...table, name: '|sample' }] }, b: { source: source('b'), tables: [table] } },
    source, connection: (sourceId: string) => ({ sourceId }),
  })
  mocks.rows.mockImplementation(async (input: { table: string; schema?: string }) => ({
    ...tableRows, table: { ...tableRows.table, name: input.table, schema: input.schema ?? '' },
  }))
  mocks.execute.mockResolvedValue(result())
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.unstubAllGlobals() })

describe.each(['update', 'delete'] as const)('%s 真实传输与页面结果保护', (operation) => {
  it.each(['lost', 'abort', 'malformed', 'wrong-status', 'agent-error'] as const)('%s 只发一次，整表 UPDATE/DELETE 持续锁定，读取与关闭不解锁', async (failure) => {
    const remoteWrites: unknown[] = []
    const fetcher = useFetch(async (_path, init) => {
      remoteWrites.push(JSON.parse(init!.body as string))
      if (failure === 'lost') throw new TypeError('raw-diagnostic-secret')
      if (failure === 'abort') throw new DOMException('raw-diagnostic-secret', 'AbortError')
      if (failure === 'wrong-status') return new Response('{"rowsAffected":1}', { status: 201 })
      if (failure === 'agent-error') return new Response(JSON.stringify({ code: 'constraint_failed', message: 'raw-diagnostic-secret', requestId: 'raw-secret' }), { status: 400 })
      return new Response('{raw-response-secret', { status: 200 })
    })
    await mount(); await prepare(operation); await submit(operation); await settle()
    expect(remoteWrites).toHaveLength(1)
    expect(state.rowWriteAttempts.get(keyA)).toEqual({ operation, status: 'unknown' })
    expect(host.textContent).toContain('包括其他行')
    expect(host.textContent).not.toMatch(/raw-.*secret|稍后重试|数据表操作失败/)
    expect(mocks.success).not.toHaveBeenCalled()
    expect(busy(operation)).toBe(false)
    close(operation)
    await settle()
    await state.refreshRows()
    expect(mocks.rows).toHaveBeenCalledTimes(2)
    expect(host.textContent).toContain('不会解除保护')
    await state.removeRow({ id: 2, label: 'another row' })
    expect(state.dangerOpen).toBe(false)
    state.openEdit({ id: 2, label: 'another row' })
    await state.submitRow()
    await state.confirmDanger()
    await settle()
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(button('保存修改').disabled).toBe(true)
    expect(state.rowWriteAttempts.get(keyA)).toEqual({ operation, status: 'unknown' })
    expect(mocks.execute).not.toHaveBeenCalled()
  })

  it('同 tick 与重绘后的重复提交都保留首个请求，原草稿字段在途锁定', async () => {
    const pending = deferred<Response>()
    const fetcher = useFetch(() => pending.promise)
    await mount(); await prepare(operation)
    const active = submit(operation)
    await submit(operation)
    await settle()
    await submit(operation)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(false)
    expect(busy(operation)).toBe(true)
    if (operation === 'update') expect(host.querySelector<HTMLInputElement>('[data-column="label"]')!.disabled).toBe(true)
    pending.resolve(response(0))
    await active
    expect(mocks.success).toHaveBeenCalledExactlyOnceWith(operation === 'update' ? '更新请求已执行' : '删除请求已执行')
    expect(state.rowWriteAttempts.size).toBe(0)
    expect(busy(operation)).toBe(false)
  })

  it.each(['source', 'table', 'schema'] as const)('A → B（%s）→ A 不串目标或 busy；迟到 A 不关闭 B，A 返回仍锁', async (dimension) => {
    const first = deferred<Response>()
    const second = deferred<Response>()
    const fetcher = useFetch().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    await mount(); await prepare(operation)
    const activeA = submit(operation)
    await settle()
    if (dimension === 'source') mocks.route.params.sourceId = 'b'
    if (dimension === 'table') mocks.route.params.table = 'other'
    if (dimension === 'schema') mocks.route.query.schema = 'other'
    await settle()
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(true)
    expect(state.rowForm).toEqual({})
    expect(state.originalKeys).toEqual({})
    expect(state.pendingRow).toBeNull()
    await prepare(operation)
    const activeB = submit(operation)
    await settle()
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(busy(operation)).toBe(true)
    first.resolve(response())
    await activeA; await settle()
    expect(busy(operation)).toBe(true)
    expect(operation === 'update' ? state.rowDialogOpen : state.dangerOpen).toBe(true)
    expect(mocks.success).not.toHaveBeenCalled()
    expect(mocks.rows).toHaveBeenCalledTimes(2)
    second.resolve(response())
    await activeB
    expect(busy(operation)).toBe(false)
    expect(mocks.success).toHaveBeenCalledTimes(1)
    mocks.route.params.sourceId = 'a'; mocks.route.params.table = 'sample'; mocks.route.query.schema = ''
    await settle(); await prepare(operation); await submit(operation)
    expect(state.rowWriteAttempts.get(keyA)).toEqual({ operation, status: 'unknown' })
    expect(host.textContent).toContain('可能已生效')
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it.each(['source', 'table', 'schema'] as const)('同 tick %s 切换不能把 A 草稿或确认派发到 B', async (dimension) => {
    const fetcher = useFetch()
    await mount(); await prepare(operation)
    if (dimension === 'source') mocks.route.params.sourceId = 'b'
    if (dimension === 'table') mocks.route.params.table = 'other'
    if (dimension === 'schema') mocks.route.query.schema = 'other'
    await submit(operation)
    expect(fetcher).not.toHaveBeenCalled()
    await settle(); await prepare(operation); await submit(operation)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each(['source', 'table', 'schema'] as const)('先同 tick 切换 %s 再点击旧渲染行，也不能把旧行绑定到新目标', async (dimension) => {
    const fetcher = useFetch()
    await mount()
    if (dimension === 'source') mocks.route.params.sourceId = 'b'
    if (dimension === 'table') mocks.route.params.table = 'other'
    if (dimension === 'schema') mocks.route.query.schema = 'other'
    // 模拟旧行点击回调在路由变化后、watcher/DOM 更新前仍被触发。
    if (operation === 'update') state.openEdit(row)
    else void state.removeRow(row)
    await submit(operation)
    expect(fetcher).not.toHaveBeenCalled()
    expect(state.rowDialogOpen).toBe(false)
    expect(state.dangerOpen).toBe(false)
    await settle(); await prepare(operation); await submit(operation)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each(['logout', 'replacement', 'unmount'] as const)('%s 中止只结束观察，旧响应不通知、不读取、不清新操作 busy', async (cleanup) => {
    const pending = deferred<Response>()
    const newer = deferred<Response>()
    const fetcher = useFetch().mockReturnValueOnce(pending.promise).mockReturnValueOnce(newer.promise)
    await mount(); await prepare(operation)
    const active = submit(operation)
    await settle()
    if (cleanup === 'logout') invalidateSession()
    else if (cleanup === 'replacement') beginSession()
    else { app!.unmount(); app = undefined }
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(true)
    expect(state.rowWriteAttempts.get(keyA)).toEqual({ operation, status: 'unknown' })
    let next: Promise<void> | undefined
    if (cleanup !== 'unmount') {
      mocks.route.params.sourceId = 'b'; await settle(); await prepare(operation)
      next = submit(operation); await settle()
      expect(busy(operation)).toBe(true)
    }
    pending.resolve(response()); await active; await settle()
    expect(mocks.success).not.toHaveBeenCalled()
    expect(mocks.rows).toHaveBeenCalledTimes(cleanup === 'unmount' ? 1 : 2)
    if (next) {
      expect(busy(operation)).toBe(true)
      newer.resolve(response()); await next
      expect(mocks.success).toHaveBeenCalledTimes(1)
    }
  })

  it('准确派发前拒绝保留草稿/确认，可主动修正后重试，原始诊断不进入 UI', async () => {
    const fetcher = useFetch().mockResolvedValueOnce(new Response(JSON.stringify({ code: 'DATABASE_INPUT_INVALID', message: 'raw-secret', requestId: 'safe-id' }), { status: 400 }))
    await mount(); await prepare(operation); await submit(operation); await settle()
    expect(state.rowWriteAttempts.size).toBe(0)
    expect(host.textContent).toContain('未提交到数据库执行')
    expect(host.textContent).not.toMatch(/raw-secret|可能已生效/)
    expect(operation === 'update' ? state.rowDialogOpen : state.dangerOpen).toBe(true)
    await submit(operation)
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(mocks.success).toHaveBeenCalledTimes(1)
  })

  it('确认写入 0 行后读取失败独立报告，只读恢复不重发写入', async () => {
    const fetcher = useFetch(async () => response(0))
    await mount(); await prepare(operation)
    mocks.rows.mockRejectedValueOnce(new NcpApiError('timeout', 'raw-secret'))
    await submit(operation); await settle()
    expect(host.textContent).toContain(`${operation === 'update' ? '更新' : '删除'}请求已执行，表数据刷新失败`)
    expect(host.textContent).not.toMatch(/数据表操作失败|可能已生效|raw-secret/)
    expect(operation === 'update' ? state.rowDialogOpen : state.dangerOpen).toBe(false)
    expect(state.rowWriteAttempts.size).toBe(0)
    await state.refreshRows(); await settle()
    expect(host.textContent).not.toContain('刷新失败')
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(mocks.rows).toHaveBeenCalledTimes(3)
  })

  it('已确认后的读取遇到会话失效不能回写旧数据，也不能降格未知', async () => {
    useFetch()
    await mount(); await prepare(operation)
    const read = deferred<DatabaseRows>()
    mocks.rows.mockReturnValueOnce(read.promise)
    const active = submit(operation)
    await settle()
    expect(mocks.success).toHaveBeenCalledTimes(1)
    invalidateSession()
    read.resolve({ ...tableRows, rows: [{ id: 999, label: 'stale-session-secret' }] })
    await active; await settle()
    expect(mocks.rows.mock.calls[1]![1].aborted).toBe(true)
    expect(host.textContent).not.toContain('刷新失败')
    expect(host.querySelector('[data-table-rows]')!.getAttribute('data-table-rows')).not.toContain('stale-session-secret')
    expect(state.rowWriteAttempts.size).toBe(0)
    expect(busy(operation)).toBe(false)
  })

  it('序列化失败与同期路由中止仍然是零派发，不留下未知目标', async () => {
    const store = mocks.store()
    store.connection = (sourceId: string) => ({ sourceId, credentials: { toJSON() {
      mocks.route.params.sourceId = 'b'
      throw new TypeError('local-secret')
    } } })
    const fetcher = useFetch()
    await mount(); await prepare(operation); await submit(operation); await settle()
    expect(fetcher).not.toHaveBeenCalled()
    expect(state.rowWriteAttempts.size).toBe(0)
    expect(host.textContent).not.toMatch(/local-secret|可能已生效/)
    expect(busy(operation)).toBe(false)
  })

  it('未知 ledger 仅留目标和 operation/status；关闭清除草稿与隐藏主键', async () => {
    useFetch(async () => { throw new TypeError('response-secret') })
    await mount()
    const secretRow = { id: 1, label: 'draft-secret', password: 'password-secret', token: 'key-secret' }
    if (operation === 'update') {
      state.openEdit(secretRow)
      state.rowForm.password = 'password-secret'
      state.originalKeys.token = 'key-secret'
    } else {
      await state.removeRow(secretRow)
      expect(state.pendingRow).toEqual({ id: 1 })
    }
    await submit(operation)
    expect([...state.rowWriteAttempts]).toEqual([[keyA, { operation, status: 'unknown' }]])
    expect(JSON.stringify([...state.rowWriteAttempts])).not.toContain('secret')
    close(operation); await settle()
    expect(state.rowForm).toEqual({})
    expect(state.rowNullFields).toEqual({})
    expect(state.originalKeys).toEqual({})
    expect(state.pendingRow).toBeNull()
    expect(state.pendingQuery).toBeNull()
  })

  it('未知行修改与 INSERT/明确 SQL 分离，其他成功不解除保护', async () => {
    const fetcher = useFetch(async (path) => {
      if (String(path).endsWith('/insert')) return new Response('{"rowsAffected":1}', { status: 201 })
      throw new TypeError('lost-response')
    })
    await mount(); await prepare(operation); await submit(operation)
    close(operation)
    state.openInsert(); state.rowForm.label = 'inserted'
    await state.submitRow()
    expect(mocks.success).toHaveBeenCalledWith('新增请求已执行')
    state.sql = 'UPDATE sample SET label = label'
    await state.runSQL()
    expect(mocks.execute).not.toHaveBeenCalled()
    await state.confirmDanger()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(state.rowWriteAttempts.get(keyA)).toEqual({ operation, status: 'unknown' })
    expect(state.insertAttempts.size).toBe(0)
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
})

describe('当前草稿/确认的精确归属', () => {
  it('UPDATE 关闭后新草稿仍可编辑，旧成功不能关闭或覆盖它', async () => {
    const pending = deferred<Response>()
    const fetcher = useFetch(() => pending.promise)
    await mount(); await prepare('update')
    const active = state.submitRow()
    close('update'); await settle()
    state.openEdit({ id: 2, label: 'new draft' }); await settle()
    expect(host.querySelector<HTMLInputElement>('[data-column="label"]')!.disabled).toBe(false)
    await state.submitRow()
    expect(fetcher).toHaveBeenCalledTimes(1)
    pending.resolve(response()); await active; await settle()
    expect(state.rowDialogOpen).toBe(true)
    expect(state.rowForm).toEqual({ id: '2', label: 'new draft' })
    expect(state.originalKeys).toEqual({ id: 2 })
    expect(state.mutationPending).toBe(false)
  })

  it('当前 UPDATE 草稿的同期字段或 NULL revision 改变后，旧成功也不能关闭', async () => {
    const pending = deferred<Response>()
    const fetcher = useFetch(() => pending.promise)
    await mount(); await prepare('update')
    const active = state.submitRow()
    state.rowForm.label = 'new value'
    state.rowNullFields.label = true
    pending.resolve(response()); await active
    expect(state.rowDialogOpen).toBe(true)
    expect(state.rowForm.label).toBe('new value')
    expect(state.rowNullFields.label).toBe(true)
    expect(JSON.parse(fetcher.mock.calls[0]![1]!.body as string).values.label).toBe('draft')
  })

  it('DELETE 确认关闭后新确认代次不被旧成功关闭或清除', async () => {
    const pending = deferred<Response>()
    useFetch(() => pending.promise)
    await mount(); await prepare('delete')
    const active = state.confirmDanger()
    state.dangerOpen = false
    expect(state.pendingRow).toBeNull()
    // 在途时正常入口拒绝；模拟新的 UI 确认代次，验证迟到结果也不越权清除。
    state.dangerGeneration += 1
    state.pendingQuery = { sql: 'DELETE FROM another_table', resultMode: 'exec' }
    state.dangerAction = 'sql'
    state.dangerOpen = true
    pending.resolve(response()); await active
    expect(state.dangerOpen).toBe(true)
    expect(state.pendingQuery?.sql).toBe('DELETE FROM another_table')
    expect(mocks.execute).not.toHaveBeenCalled()
    expect(state.dangerBusy).toBe(false)
  })

  it('字段转换本地失败不发送，修正后允许提交', async () => {
    const fetcher = useFetch()
    await mount(); await prepare('update')
    state.rowForm.id = 'not an integer'
    await state.submitRow(); await settle()
    expect(fetcher).not.toHaveBeenCalled()
    expect(state.rowWriteAttempts.size).toBe(0)
    expect(host.textContent).toContain('字段值格式不正确')
    state.rowForm.id = '1'
    await state.submitRow()
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('合法名称中的分隔符不导致 ledger 键碰撞', async () => {
    const pending = deferred<Response>()
    const fetcher = useFetch().mockReturnValueOnce(pending.promise)
    mocks.route.query.schema = '|'
    await mount(); await prepare('update')
    const active = state.submitRow()
    mocks.route.query.schema = ''; mocks.route.params.table = '|sample'
    await settle(); await prepare('update'); await state.submitRow()
    pending.resolve(response()); await active
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect([...state.rowWriteAttempts]).toEqual([[JSON.stringify(['a', '|', 'sample']), { operation: 'update', status: 'unknown' }]])
  })
})
