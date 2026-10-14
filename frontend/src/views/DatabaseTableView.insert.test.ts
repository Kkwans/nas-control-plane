// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, QueryResult } from '@/api/database'
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
interface DraftState {
  rowForm: Record<string, string>
  rowNullFields: Record<string, boolean>
  originalKeys: Record<string, unknown>
  insertAttempts: Map<string, string>
}
let state: DraftState
let app: App | undefined
let host: HTMLDivElement
async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseTableView, { ref: (instance: unknown) => {
    if (instance) state = (instance as { $: { setupState: DraftState } }).$.setupState
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
async function openSQL() { button('执行 SQL').click(); await settle() }
async function editSQL(statement: string) {
  const input = host.querySelector('textarea')!
  input.value = statement
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}
function executeButton() { return host.querySelector<HTMLButtonElement>('.sql-toolbar__actions button')! }
function dialog() { return host.querySelector<HTMLElement>('[role="dialog"]') }
async function confirm() { button('确认执行', dialog()!).click(); await settle() }

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  mocks.pageSize.value = 25
  mocks.route = reactive({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } })
  mocks.store.mockReturnValue({
    sources: [source('a'), source('b')],
    catalogs: { a: { source: source('a'), tables: [table, { ...table, name: 'other' }] }, b: { source: source('b'), tables: [table] } },
    source, connection: (sourceId: string) => ({ sourceId }),
  })
  mocks.rows.mockImplementation(async (input: { table: string; schema?: string }) => ({
    ...tableRows, table: { ...tableRows.table, name: input.table, schema: input.schema ?? '' },
  }))
  mocks.execute.mockResolvedValue(result())
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.unstubAllGlobals() })


async function newDraft(label: string) {
 button('新增行').click(); await settle()
 const input=host.querySelector<HTMLInputElement>('[data-column="label"]')!
 input.value=label; input.dispatchEvent(new Event('input',{bubbles:true})); await settle()
}
function save() { button('新增数据行',dialog()!).click() }

function inserted(rowsAffected = 1) {
  return new Response(JSON.stringify({ rowsAffected }), { status: 201 })
}
function draftValue() { return host.querySelector<HTMLInputElement>('[data-column="label"]')!.value }
async function cancelDraft() { button('取消', dialog()!).click(); await settle() }
async function runDangerSQL() {
  await openSQL()
  await editSQL('UPDATE sample SET label = label')
  executeButton().click()
  await settle()
  await confirm()
}
async function deleteRow() {
  button('删除').click()
  await settle()
  button('永久删除', dialog()!).click()
  await settle()
}

describe('数据库 INSERT 结果与草稿归属', () => {
  it.each(['lost', 'malformed'] as const)('提交后 %s 响应持续警告并禁止重复新增，只读刷新不解除保护', async (failure) => {
    const remoteRows: unknown[] = []
    const fetcher = vi.fn<typeof fetch>(async (_path, init) => {
      remoteRows.push(JSON.parse(init!.body as string).values)
      if (failure === 'lost') throw new TypeError('raw-diagnostic-secret')
      return new Response('{raw-response-secret', { status: 201 })
    })
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle()
    expect(remoteRows).toEqual([{ label: 'first' }])
    expect(draftValue()).toBe('first')
    expect(dialog()).not.toBeNull()
    expect(host.textContent).toContain('可能已生效')
    expect(host.textContent).not.toContain('secret')
    expect(host.textContent).not.toContain('稍后重试')
    expect(button('新增数据行', dialog()!).disabled).toBe(true)
    // 同时验证 handler 自身拒绝调用，而不只依赖按钮 disabled。
    button('新增数据行', dialog()!).dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await settle()
    await cancelDraft()
    button('刷新').click(); await settle()
    expect(mocks.rows).toHaveBeenCalledTimes(2)
    expect(host.textContent).toContain('不能证明此次新增成功或失败')
    expect(host.textContent).toContain('服务端尚无幂等保障')
    await newDraft('second'); save(); await settle()
    expect(button('新增数据行', dialog()!).disabled).toBe(true)
    expect(remoteRows).toEqual([{ label: 'first' }])
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(mocks.success).not.toHaveBeenCalled()
  })

  it.each([null, {}, { rowsAffected: '1' }])('HTTP 201 非法载荷 %j 不显示成功且保留草稿', async (body) => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(body), { status: 201 })))
    await mount(); await newDraft('first'); save(); await settle()
    expect(mocks.success).not.toHaveBeenCalled()
    expect(draftValue()).toBe('first')
    expect(host.textContent).toContain('可能已生效')
    expect(button('新增数据行', dialog()!).disabled).toBe(true)
  })

  it.each([0, 1])('合法影响行数 %s 确认成功并只读刷新', async (count) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(inserted(count))
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle()
    expect(mocks.success).toHaveBeenCalledExactlyOnceWith('新增请求已执行')
    expect(dialog()).toBeNull()
    expect(host.textContent).not.toContain('可能已生效')
    expect(mocks.rows).toHaveBeenCalledTimes(2)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('同步重复提交只发送一次，不中止第一次观察', async () => {
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>(() => pending.promise)
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); save(); await settle()
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(false)
    expect(button('新增数据行', dialog()!).disabled).toBe(true)
    expect(host.querySelector<HTMLInputElement>('[data-column="label"]')!.disabled).toBe(true)
    pending.resolve(inserted()); await settle()
  })

  it.each(['success', 'unknown'] as const)('旧请求 %s 不关闭或清除取消后重新打开的新草稿', async (outcome) => {
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>(() => pending.promise)
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle(); await cancelDraft()
    await newDraft('second')
    expect(draftValue()).toBe('second')
    expect(host.querySelector<HTMLInputElement>('[data-column="label"]')!.disabled).toBe(false)
    save(); await settle()
    expect(fetcher).toHaveBeenCalledTimes(1)
    if (outcome === 'success') pending.resolve(inserted())
    else pending.reject(new TypeError('lost'))
    await settle()
    expect(dialog()).not.toBeNull()
    expect(draftValue()).toBe('second')
    expect(button('新增数据行', dialog()!).disabled).toBe(outcome === 'unknown')
    expect(mocks.success).toHaveBeenCalledTimes(outcome === 'success' ? 1 : 0)
  })

  it.each(['source', 'table', 'schema'] as const)('%s 切换隔离旧完成；返回原目标仍保留未知保护', async (target) => {
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>().mockImplementationOnce(() => pending.promise).mockResolvedValue(inserted())
    vi.stubGlobal('fetch', fetcher)
    const store = mocks.store()
    store.catalogs.a.tables.push({ ...table, schema: 'other' })
    await mount(); await newDraft('first'); save(); await settle()
    const body = JSON.parse(fetcher.mock.calls[0]![1]!.body as string)
    expect(body).toMatchObject({ sourceId: 'a', schema: '', table: 'sample', values: { label: 'first' } })
    if (target === 'source') mocks.route.params.sourceId = 'b'
    else if (target === 'table') mocks.route.params.table = 'other'
    else mocks.route.query.schema = 'other'
    await settle(); await newDraft('second')
    pending.resolve(inserted()); await settle()
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(true)
    expect(mocks.success).not.toHaveBeenCalled()
    expect(dialog()).not.toBeNull()
    expect(draftValue()).toBe('second')
    expect(host.textContent).not.toContain('可能已生效')
    save(); await settle()
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(mocks.success).toHaveBeenCalledExactlyOnceWith('新增请求已执行')
    mocks.route.params.sourceId = 'a'
    mocks.route.params.table = 'sample'
    mocks.route.query.schema = ''
    await settle(); await newDraft('third'); save(); await settle()
    expect(host.textContent).toContain('可能已生效')
    expect(button('新增数据行', dialog()!).disabled).toBe(true)
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it.each(['pending', 'unknown'] as const)('%s INSERT 与 SQL/删除相互独立，不丢失未知结果或解锁', async (state) => {
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>((path) => String(path).endsWith('/insert') ? pending.promise : Promise.resolve(new Response('{"rowsAffected":1}', { status: 200 })))
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle(); await cancelDraft()
    if (state === 'unknown') { pending.reject(new TypeError('lost')); await settle() }
    await runDangerSQL()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    button('表数据').click(); await settle()
    await deleteRow()
    expect(mocks.success).toHaveBeenCalledWith('删除请求已执行')
    if (state === 'pending') {
      expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(false)
      pending.reject(new TypeError('lost')); await settle()
    }
    expect(host.textContent).toContain('可能已生效')
    await newDraft('second'); save(); await settle()
    expect(button('新增数据行', dialog()!).disabled).toBe(true)
    expect(fetcher.mock.calls.filter(([path]) => String(path).endsWith('/insert'))).toHaveLength(1)
  })

  it('本地字段校验失败不发送请求、不锁定，修正后可以提交', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(inserted())
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft(''); save(); await settle()
    expect(fetcher).not.toHaveBeenCalled()
    expect(host.textContent).toContain('字段值格式不正确')
    expect(host.textContent).not.toContain('可能已生效')
    expect(button('新增数据行', dialog()!).disabled).toBe(false)
    const input = host.querySelector<HTMLInputElement>('[data-column="label"]')!
    input.value = 'corrected'; input.dispatchEvent(new Event('input', { bubbles: true })); await settle()
    save(); await settle()
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(mocks.success).toHaveBeenCalledWith('新增请求已执行')
  })

  it('确切的派发前拒绝允许用户处理后主动提交，不显示原始诊断', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(new Response(JSON.stringify({ code: 'DATABASE_INPUT_INVALID', message: 'raw-secret', requestId: 'raw-secret' }), { status: 400 })).mockResolvedValue(inserted())
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle()
    expect(draftValue()).toBe('first')
    expect(host.textContent).toContain('未提交到数据库执行')
    expect(host.textContent).not.toMatch(/可能已生效|raw-secret/)
    expect(button('新增数据行', dialog()!).disabled).toBe(false)
    save(); await settle()
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(mocks.success).toHaveBeenCalledWith('新增请求已执行')
  })

  it('确认 INSERT 后刷新失败单独报告，恢复读取不再写入', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(inserted())
    vi.stubGlobal('fetch', fetcher)
    await mount(); mocks.rows.mockRejectedValueOnce(new NcpApiError('timeout', 'raw-secret'))
    await newDraft('first'); save(); await settle()
    expect(mocks.success).toHaveBeenCalledWith('新增请求已执行')
    expect(host.textContent).toContain('新增请求已执行，表数据刷新失败')
    expect(host.textContent).not.toMatch(/数据表操作失败|可能已生效|raw-secret/)
    expect(dialog()).toBeNull()
    button('刷新').click(); await settle()
    expect(mocks.rows).toHaveBeenCalledTimes(3)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(host.textContent).not.toContain('刷新失败')
  })

  it('页面卸载后旧请求不会显示成功或执行读刷新', async () => {
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>(() => pending.promise)
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle()
    app!.unmount(); app = undefined
    pending.resolve(inserted()); await settle()
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(true)
    expect(mocks.success).not.toHaveBeenCalled()
    expect(mocks.rows).toHaveBeenCalledTimes(1)
  })

  it.each(['route', 'unmount'] as const)('%s 清除过期敏感草稿，但未知记录只存目标和状态', async (cleanup) => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockRejectedValue(new TypeError('response-secret')))
    await mount(); await newDraft('draft-secret')
    state.rowForm.password = 'password-secret'
    state.rowNullFields.password = false
    state.originalKeys.token = 'key-secret'
    save(); await settle()
    expect(state.rowForm.label).toBe('draft-secret')
    expect([...state.insertAttempts]).toEqual([[JSON.stringify(['a', '', 'sample']), 'unknown']])
    expect(JSON.stringify([...state.insertAttempts])).not.toContain('secret')
    if (cleanup === 'route') { mocks.route.params.sourceId = 'b'; await settle() }
    else { app!.unmount(); app = undefined }
    expect(state.rowForm).toEqual({})
    expect(state.rowNullFields).toEqual({})
    expect(state.originalKeys).toEqual({})
  })

  it.each(['logout', 'replacement'] as const)('%s 会话失效立即中止观察，旧成功不关闭新草稿或刷新', async (cleanup) => {
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>(() => pending.promise)
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle()
    await cancelDraft(); await newDraft('second')
    if (cleanup === 'logout') invalidateSession()
    else beginSession()
    await settle()
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(true)
    expect(button('新增数据行', dialog()!).disabled).toBe(true)
    expect(host.textContent).toContain('可能已生效')
    pending.resolve(inserted()); await settle()
    expect(mocks.success).not.toHaveBeenCalled()
    expect(mocks.rows).toHaveBeenCalledTimes(1)
    expect(draftValue()).toBe('second')
  })


  it('新增确认后的只读刷新也隔离已失效会话', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(inserted()))
    await mount()
    const pending = deferred<DatabaseRows>()
    mocks.rows.mockReturnValueOnce(pending.promise)
    await newDraft('first'); save(); await settle()
    expect(mocks.success).toHaveBeenCalledWith('新增请求已执行')
    invalidateSession()
    pending.resolve({ ...tableRows, rows: [{ id: 999, label: 'stale-session-secret' }] })
    await settle()
    expect(mocks.rows.mock.calls[1]![1].aborted).toBe(true)
    expect(host.querySelector('[data-table-rows]')!.getAttribute('data-table-rows')).not.toContain('stale-session-secret')
    expect(host.textContent).not.toContain('刷新失败')
  })


  it('目标名称包含竖线时也必须失效旧草稿与请求归属', async () => {
    const store = mocks.store()
    store.catalogs.a.tables.push({ ...table, schema: '|' }, { ...table, name: '|sample' })
    mocks.route.query.schema = '|'
    const pending = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>(() => pending.promise)
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('target-a-secret'); save(); await settle()
    mocks.route.query.schema = ''
    mocks.route.params.table = '|sample'
    await settle()
    expect(dialog()).toBeNull()
    expect(state.rowForm).toEqual({})
    expect(fetcher.mock.calls[0]![1]!.signal!.aborted).toBe(true)
    await newDraft('target-b')
    pending.resolve(inserted()); await settle()
    expect(draftValue()).toBe('target-b')
    expect(mocks.success).not.toHaveBeenCalled()
    expect(host.textContent).not.toContain('可能已生效')
  })


  it('序列化本地失败不会因同期路由中止被误记为已发送', async () => {
    const store = mocks.store()
    store.connection = (sourceId: string) => ({ sourceId, credentials: { toJSON() {
      mocks.route.params.sourceId = 'b'
      throw new TypeError('local-secret')
    } } })
    const fetcher = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetcher)
    await mount(); await newDraft('first'); save(); await settle()
    expect(fetcher).not.toHaveBeenCalled()
    expect([...state.insertAttempts]).toEqual([])
    expect(host.textContent).not.toMatch(/可能已生效|local-secret/)
  })

})
