// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, DatabaseValue, QueryResult } from '@/api/database'
import { NcpApiError } from '@/api/system'
import { beginSession } from '@/session/sessionLifecycle'
import DatabaseTableView from './DatabaseTableView.vue'

const mocks = vi.hoisted(() => ({
  update: vi.fn(), remove: vi.fn(), insert: vi.fn(), execute: vi.fn(),
  rows: vi.fn(), store: vi.fn(), success: vi.fn(), fetch: vi.fn(),
  route: { params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } },
}))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/database', () => ({ useDatabaseStore: mocks.store }))
vi.mock('@/api/database', () => ({
  updateDatabaseRow: mocks.update, deleteDatabaseRow: mocks.remove,
  insertDatabaseRow: mocks.insert, executeDatabaseSQL: mocks.execute, loadDatabaseRows: mocks.rows,
}))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  return { useListPreference: () => ({ pageSize: ref(25) }) }
})
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/DatabaseCellEditor.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/SqlEditor.vue', () => ({ default: { render: () => null } }))
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
    ElTable: defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) }),
    ElTableColumn: defineComponent({
      props: ['label'],
      setup: (props, { slots }) => () => props.label === '行操作'
        ? h('div', slots.default?.({ row: { id: 1, label: 'existing' } })) : null,
    }),
    ElForm: content, ElFormItem: content, ElSwitch: content, ElTag: content, ElInput: content,
  }
})

type DangerKind = 'delete' | 'sql'
type Operation = 'update' | DangerKind
type MutationResult = { rowsAffected: number }
const row = { id: 1, label: 'existing' }
const table: DatabaseTable = {
  schema: '', name: 'sample', type: 'table',
  columns: [
    { name: 'id', dataType: 'integer', nullable: false, primaryKey: true, position: 1, writeMode: 'server-generated' },
    { name: 'label', dataType: 'text', nullable: false, primaryKey: false, position: 2 },
  ],
}
const tableRows: DatabaseRows = { table, rows: [row], limit: 25, offset: 0, hasMore: false }
const source: DatabaseSource = {
  id: 'a', name: 'Source A', driver: 'sqlite', category: 'project', project: '', module: '',
  location: '', requiresLogin: false, status: 'connected', tags: [],
}
const mutationResult: MutationResult = { rowsAffected: 1 }
const queryResult: QueryResult = { columns: [], rows: [], rowsAffected: 1, rowsAffectedKnown: true, truncated: false, durationMs: 2 }
const targetKey = JSON.stringify(['a', '', 'sample'])

interface ViewState {
  mutationPending: boolean
  dangerBusy: boolean
  dangerOpen: boolean
  rowDialogOpen: boolean
  rowForm: Record<string, string>
  sql: string
  insertAttempts: Map<string, 'pending' | 'unknown'>
  openEdit: (row: Record<string, DatabaseValue>) => void
  openInsert: () => void
  removeRow: (row: Record<string, DatabaseValue>) => Promise<void>
  runSQL: () => Promise<void>
  submitRow: () => Promise<void>
  confirmDanger: () => Promise<void>
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
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
function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((item) => item.textContent?.includes(label))
  if (!found) throw new Error(`Missing button ${label}`)
  return found
}
function editDraft(label = 'updated') {
  state.openEdit(row)
  state.rowForm.label = label
}
function insertDraft() {
  state.openInsert()
  state.rowForm.label = 'inserted'
}
function prepareDanger(kind: DangerKind) {
  if (kind === 'delete') void state.removeRow(row)
  else {
    state.sql = 'UPDATE sample SET label = label'
    void state.runSQL()
  }
  expect(state.dangerOpen).toBe(true)
}
function operationMock(operation: Operation) {
  return operation === 'update' ? mocks.update : operation === 'delete' ? mocks.remove : mocks.execute
}
function requestSignal(operation: Operation): AbortSignal {
  return operationMock(operation).mock.calls[0]![1] as AbortSignal
}
function dangerButton(kind: DangerKind) { return button(kind === 'delete' ? '永久删除' : '确认执行') }
function complete(pending: ReturnType<typeof deferred<MutationResult | QueryResult>>, operation: Operation, outcome: 'success' | 'error') {
  if (outcome === 'error') pending.reject(new NcpApiError('constraint_failed', 'fake rejection'))
  else pending.resolve(operation === 'sql' ? queryResult : mutationResult)
}

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  vi.stubGlobal('fetch', mocks.fetch.mockImplementation(() => { throw new Error('Real network is forbidden in this suite') }))
  mocks.route = reactive({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } })
  mocks.store.mockReturnValue({
    sources: [source], catalogs: { a: { source, tables: [table] } },
    source: () => source, connection: (sourceId: string) => ({ sourceId }),
  })
  mocks.rows.mockResolvedValue(tableRows)
  mocks.update.mockResolvedValue(mutationResult)
  mocks.remove.mockResolvedValue(mutationResult)
  mocks.insert.mockResolvedValue(mutationResult)
  mocks.execute.mockResolvedValue(queryResult)
})
afterEach(() => {
  app?.unmount()
  app = undefined
  host?.remove()
  expect(mocks.fetch).not.toHaveBeenCalled()
  vi.unstubAllGlobals()
})

const competingCases = (['delete', 'sql'] as const).flatMap((kind) =>
  (['same-tick', 'rendered'] as const).flatMap((timing) =>
    (['success', 'error'] as const).map((outcome) => ({ kind, timing, outcome }))))

describe('UPDATE 与危险确认的同步准入', () => {
  it.each(competingCases)('UPDATE 阻止 $kind 抢占（$timing / $outcome），完成后允许主动确认', async ({ kind, timing, outcome }) => {
    const pending = deferred<MutationResult | QueryResult>()
    mocks.update.mockReturnValueOnce(pending.promise)
    await mount()
    // 已打开的确认框不能绕过后续 UPDATE 的请求归属。
    prepareDanger(kind)
    editDraft()
    const active = state.submitRow()
    if (timing === 'rendered') await settle()
    await state.confirmDanger()
    await state.confirmDanger()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(operationMock(kind)).not.toHaveBeenCalled()
    expect(requestSignal('update').aborted).toBe(false)
    expect(state.mutationPending).toBe(true)
    expect(state.dangerBusy).toBe(false)
    await settle()
    expect(dangerButton(kind).disabled).toBe(true)
    complete(pending, 'update', outcome)
    await active
    await settle()
    expect(state.mutationPending).toBe(false)
    expect(state.dangerBusy).toBe(false)
    expect(requestSignal('update').aborted).toBe(false)
    expect(dangerButton(kind).disabled).toBe(false)
    await state.confirmDanger()
    expect(operationMock(kind)).toHaveBeenCalledTimes(1)
    expect(state.dangerBusy).toBe(false)
  })

  it.each(competingCases)('$kind 阻止 UPDATE 抢占（$timing / $outcome），关闭确认框也不释放在途请求', async ({ kind, timing, outcome }) => {
    const pending = deferred<MutationResult | QueryResult>()
    operationMock(kind).mockReturnValueOnce(pending.promise)
    await mount()
    editDraft()
    prepareDanger(kind)
    const active = state.confirmDanger()
    // 模拟对话框的 modelValue / X 关闭；不能以 UI 消失代替请求结束。
    state.dangerOpen = false
    if (timing === 'rendered') await settle()
    await state.submitRow()
    await state.submitRow()
    expect(operationMock(kind)).toHaveBeenCalledTimes(1)
    expect(mocks.update).not.toHaveBeenCalled()
    expect(requestSignal(kind).aborted).toBe(false)
    expect(state.dangerBusy).toBe(true)
    expect(state.mutationPending).toBe(false)
    await settle()
    expect(button('保存修改').disabled).toBe(true)
    complete(pending, kind, outcome)
    await active
    await settle()
    expect(state.dangerBusy).toBe(false)
    expect(state.mutationPending).toBe(false)
    expect(requestSignal(kind).aborted).toBe(false)
    expect(button('保存修改').disabled).toBe(false)
    await state.submitRow()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(state.mutationPending).toBe(false)
  })

  it('UPDATE 关闭并重新打开编辑草稿后，删除入口和保存仍拒绝竞争且保留新草稿', async () => {
    const pending = deferred<MutationResult>()
    mocks.update.mockReturnValueOnce(pending.promise)
    await mount()
    editDraft('first')
    const active = state.submitRow()
    state.rowDialogOpen = false
    await state.removeRow(row)
    expect(state.dangerOpen).toBe(false)
    editDraft('new unsent draft')
    await state.submitRow()
    await settle()
    expect(button('删除').disabled).toBe(true)
    expect(button('保存修改').disabled).toBe(true)
    // 直接调用 handler 验证保护不依赖 DOM 已完成 disabled 重绘。
    await state.removeRow(row)
    expect(state.dangerOpen).toBe(false)
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(mocks.update.mock.calls[0]![0]).toMatchObject({ values: { id: '1', label: 'first' }, keys: { id: 1 } })
    expect(requestSignal('update').aborted).toBe(false)
    pending.resolve(mutationResult)
    await active
    await settle()
    expect(state.rowDialogOpen).toBe(true)
    expect(state.rowForm.label).toBe('new unsent draft')
    expect(state.mutationPending).toBe(false)
    expect(button('删除').disabled).toBe(false)
    expect(button('保存修改').disabled).toBe(false)
    await state.removeRow(row)
    await state.confirmDanger()
    expect(mocks.remove).toHaveBeenCalledTimes(1)
  })

  it.each(['update', 'delete', 'sql'] as const)('%s 同一 tick 和重绘后重复调用都只发一次', async (operation) => {
    const pending = deferred<MutationResult | QueryResult>()
    operationMock(operation).mockReturnValueOnce(pending.promise)
    await mount()
    if (operation === 'update') editDraft()
    else prepareDanger(operation)
    const submit = operation === 'update' ? state.submitRow : state.confirmDanger
    const active = submit()
    await submit()
    await settle()
    await submit()
    expect(operationMock(operation)).toHaveBeenCalledTimes(1)
    expect(requestSignal(operation).aborted).toBe(false)
    complete(pending, operation, 'success')
    await active
    expect(state.mutationPending).toBe(false)
    expect(state.dangerBusy).toBe(false)
  })
})

describe('写后刷新仍保留原请求归属', () => {
  it.each((['update', 'delete', 'sql'] as const).flatMap((operation) =>
    (['success', 'error'] as const).map((outcome) => ({ operation, outcome }))))('$operation 刷新 $outcome 前拒绝竞争，结束后释放 busy', async ({ operation, outcome }) => {
    const refresh = deferred<DatabaseRows>()
    await mount()
    mocks.rows.mockReturnValueOnce(refresh.promise)
    editDraft()
    prepareDanger(operation === 'update' ? 'delete' : operation)
    const active = operation === 'update' ? state.submitRow() : state.confirmDanger()
    await settle()
    expect(mocks.rows).toHaveBeenCalledTimes(2)
    expect(operation === 'update' ? state.mutationPending : state.dangerBusy).toBe(true)
    if (operation === 'update') await state.confirmDanger()
    else await state.submitRow()
    expect(operation === 'update' ? mocks.remove : mocks.update).not.toHaveBeenCalled()
    expect(requestSignal(operation).aborted).toBe(false)
    if (outcome === 'success') refresh.resolve(tableRows)
    else refresh.reject(new NcpApiError('unreachable', 'fake read failure'))
    await active
    expect(state.mutationPending).toBe(false)
    expect(state.dangerBusy).toBe(false)
    if (operation === 'update') await state.confirmDanger()
    else await state.submitRow()
    expect(operation === 'update' ? mocks.remove : mocks.update).toHaveBeenCalledTimes(1)
  })
})

describe('INSERT 保持独立控制器与目标锁', () => {
  it.each(['delete', 'sql'] as const)('%s 在途时新增不被 UPDATE 专属 guard 阻止或中止', async (kind) => {
    const danger = deferred<MutationResult | QueryResult>()
    const insert = deferred<MutationResult>()
    operationMock(kind).mockReturnValueOnce(danger.promise)
    mocks.insert.mockReturnValueOnce(insert.promise)
    await mount()
    prepareDanger(kind)
    const activeDanger = state.confirmDanger()
    insertDraft()
    await settle()
    expect(button('新增数据行').disabled).toBe(false)
    const activeInsert = state.submitRow()
    await state.submitRow()
    expect(mocks.insert).toHaveBeenCalledTimes(1)
    expect(state.insertAttempts.get(targetKey)).toBe('pending')
    expect(state.dangerBusy).toBe(true)
    expect(requestSignal(kind).aborted).toBe(false)
    expect((mocks.insert.mock.calls[0]![1] as AbortSignal).aborted).toBe(false)
    insert.resolve(mutationResult)
    await activeInsert
    expect(state.insertAttempts.has(targetKey)).toBe(false)
    expect(state.dangerBusy).toBe(true)
    complete(danger, kind, 'success')
    await activeDanger
    expect(state.dangerBusy).toBe(false)
  })

  it.each(['update', 'delete', 'sql'] as const)('新增在途时 %s 不会改写新增目标锁或中止新增请求', async (operation) => {
    const insert = deferred<MutationResult>()
    mocks.insert.mockReturnValueOnce(insert.promise)
    await mount()
    insertDraft()
    const activeInsert = state.submitRow()
    const signal = mocks.insert.mock.calls[0]![1] as AbortSignal
    if (operation === 'update') { editDraft(); await state.submitRow() }
    else { prepareDanger(operation); await state.confirmDanger() }
    expect(operationMock(operation)).toHaveBeenCalledTimes(1)
    expect(signal.aborted).toBe(false)
    expect(state.insertAttempts.get(targetKey)).toBe('pending')
    insertDraft()
    await state.submitRow()
    expect(mocks.insert).toHaveBeenCalledTimes(1)
    insert.resolve(mutationResult)
    await activeInsert
    expect(state.insertAttempts.has(targetKey)).toBe(false)
    expect(signal.aborted).toBe(false)
    expect(state.mutationPending).toBe(false)
    expect(state.dangerBusy).toBe(false)
  })
})
