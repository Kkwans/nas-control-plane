// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, QueryResult } from '@/api/database'
import { NcpApiError } from '@/api/system'
import DatabaseTableView from './DatabaseTableView.vue'

const mocks = vi.hoisted(() => ({
  execute: vi.fn(), rows: vi.fn(), store: vi.fn(), success: vi.fn(),
  route: { params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } },
}))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/database', () => ({ useDatabaseStore: mocks.store }))
vi.mock('@/api/database', () => ({
  executeDatabaseSQL: mocks.execute, loadDatabaseRows: mocks.rows,
  insertDatabaseRow: vi.fn(), updateDatabaseRow: vi.fn(), deleteDatabaseRow: vi.fn(),
}))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  return { useListPreference: () => ({ pageSize: ref(25) }) }
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
      props: ['data'],
      setup: (props) => () => h('div', { 'data-table-rows': JSON.stringify(props.data) }),
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
let app: App | undefined
let host: HTMLDivElement
async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseTableView) })
  app.directive('loading', {})
  app.mount(host)
  await settle()
  await openSQL()
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
  mocks.route = reactive({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } })
  mocks.store.mockReturnValue({
    sources: [source('a'), source('b')],
    catalogs: { a: { source: source('a'), tables: [table] }, b: { source: source('b'), tables: [table] } },
    source, connection: (sourceId: string) => ({ sourceId }),
  })
  mocks.rows.mockResolvedValue(tableRows)
  mocks.execute.mockResolvedValue(result())
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

describe('SQL 结果模式与计数', () => {
  it.each(['auto', 'rows', 'exec'] as const)('发送 %s 模式且只提交一次 SQL', async (mode) => {
    await mount()
    const statement = '  /* 原文 */ SELECT 1;\n'
    await editSQL(statement)
    await selectMode(mode)
    executeButton().click()
    await settle()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(mocks.execute.mock.calls[0]![0]).toEqual({ sourceId: 'a', sql: statement, ...(mode === 'auto' ? {} : { resultMode: mode }) })
    expect(mocks.rows).toHaveBeenCalledTimes(2)
  })

  it.each([
    { known: true, affected: 0, expected: '影响 0 行' },
    { known: true, affected: 7, expected: '影响 7 行' },
    { known: false, affected: 0, expected: '数据库未提供可用的影响行数' },
    { known: undefined, affected: 9, expected: '数据库未提供可用的影响行数' },
  ])('只信任明确的计数标志：$known / $affected', async ({ known, affected, expected }) => {
    mocks.execute.mockResolvedValue(result({ rowsAffectedKnown: known, rowsAffected: affected }))
    await mount()
    executeButton().click()
    await settle()
    expect(host.querySelector('.query-result')?.textContent).toContain(expected)
    if (known !== true) expect(host.querySelector('.query-result')?.textContent).not.toMatch(/影响 \d+ 行/)
  })

  it('RETURNING 有列仍刷新，返回行数不冒充影响行数', async () => {
    mocks.execute.mockResolvedValue(result({ columns: ['n'], rows: [[2]], truncated: true }))
    await mount()
    await editSQL('UPDATE sample SET n = n + 1 WHERE id = 1 RETURNING n')
    await selectMode('rows')
    executeButton().click()
    await settle()
    expect(mocks.rows).toHaveBeenCalledTimes(2)
    expect(host.querySelector('.query-result header')?.textContent).toContain('返回 1 行')
    expect(host.querySelector('.query-result')?.textContent).toContain('返回结果已截断')
    expect(host.querySelector('.query-result')?.textContent).toContain('数据库未提供可用的影响行数')
    expect(host.querySelector('.query-result')?.textContent).not.toContain('影响 1 行')
    expect(host.querySelector('.query-table [data-table-rows]')?.getAttribute('data-table-rows')).toBe('[{"n":2}]')
    expect(host.textContent).toContain('500 行仅是展示上限，不限制写入范围')
  })

  it('零行 RETURNING 保留结果列，显示返回 0 行且影响未知', async () => {
    mocks.execute.mockResolvedValue(result({ columns: ['id'] }))
    await mount()
    executeButton().click()
    await settle()
    expect(host.querySelector('.query-result header')?.textContent).toContain('返回 0 行')
    expect(host.querySelector('.query-table')).not.toBeNull()
    expect(host.querySelector('.query-result')?.textContent).not.toContain('影响 0 行')
  })

  it('刷新失败保留 SQL 成功结果，并单独提示只重新读取', async () => {
    mocks.execute.mockResolvedValue(result({ columns: ['id'], rows: [[2]] }))
    await mount()
    mocks.rows.mockRejectedValue(new NcpApiError('database_not_found', '表不存在'))
    executeButton().click()
    await settle()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(host.querySelector('.query-refresh-warning')?.textContent).toContain('SQL 已执行，表数据刷新失败')
    expect(host.querySelector('.query-refresh-warning')?.textContent).toContain('勿为刷新而重跑 SQL')
    expect(host.querySelector('.query-table')).not.toBeNull()
    expect(host.querySelector('.query-message--error')).toBeNull()
    mocks.rows.mockResolvedValue(tableRows)
    button('表数据').click()
    await settle()
    button('刷新').click()
    await settle()
    await openSQL()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(host.querySelector('.query-refresh-warning')).toBeNull()
    expect(host.querySelector('.query-table')).not.toBeNull()
  })
})

describe('SQL 确认、并发与路由归属', () => {
  it('确认预览逐字保留换行、缩进与注释，只渲染转义文本且保持原 SQL 快照', async () => {
    await mount()
    const statement = '  DELETE FROM sample; -- <script>alert(1)</script>\n\tDELETE FROM other_table;\n'
    await editSQL(statement)
    await selectMode('exec')
    executeButton().click()
    await settle()
    const preview = dialog()?.querySelector('.sql-confirmation-preview')
    expect(preview?.tagName).toBe('PRE')
    expect(preview?.textContent).toBe(statement)
    expect(preview?.children.length).toBe(0)
    expect(dialog()?.querySelector('script')).toBeNull()
    await editSQL('DELETE FROM changed_table')
    await selectMode('rows')
    expect(preview?.textContent).toBe(statement)
    expect(dialog()?.textContent).toContain('结果模式：执行（不展示返回行）')
    await confirm()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(mocks.execute.mock.calls[0]![0]).toEqual({ sourceId: 'a', sql: statement, resultMode: 'exec' })
  })

  it('确认保存 SQL 和模式快照，编辑与快捷键不能替换或重复提交', async () => {
    const pending = deferred<QueryResult>()
    mocks.execute.mockReturnValue(pending.promise)
    await mount()
    const statement = 'UPDATE sample SET n = n + 1 RETURNING n'
    await editSQL(statement)
    await selectMode('rows')
    executeButton().click()
    await settle()
    expect(dialog()?.textContent).toContain(statement)
    expect(dialog()?.textContent).toContain('结果模式：返回行')
    expect(dialog()?.textContent).not.toContain('其他数据库和表不受影响')
    await editSQL('DELETE FROM sample')
    await selectMode('exec')
    shortcut()
    await settle()
    expect(dialog()?.textContent).toContain(statement)
    expect(mocks.execute).not.toHaveBeenCalled()
    await confirm()
    shortcut()
    button('确认执行', dialog()!).click()
    await settle()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(mocks.execute.mock.calls[0]![0]).toEqual({ sourceId: 'a', sql: statement, resultMode: 'rows' })
    pending.resolve(result({ columns: ['n'], rows: [[2]] }))
    await settle()
    expect(dialog()).toBeNull()
    expect(mocks.success).toHaveBeenCalledTimes(1)
  })

  it('取消确认不执行，下一次主动执行采用新的快照', async () => {
    await mount()
    await editSQL('DELETE FROM sample')
    await selectMode('exec')
    executeButton().click()
    await settle()
    button('取消', dialog()!).click()
    await settle()
    expect(mocks.execute).not.toHaveBeenCalled()
    await editSQL('SELECT id FROM sample')
    await selectMode('rows')
    executeButton().click()
    await settle()
    expect(mocks.execute.mock.calls[0]![0]).toEqual({ sourceId: 'a', sql: 'SELECT id FROM sample', resultMode: 'rows' })
  })

  it('执行开始清掉旧结果，点击与 Ctrl+Enter 在执行和刷新期间均不能重复提交', async () => {
    await mount()
    mocks.execute.mockResolvedValueOnce(result({ columns: ['old'], rows: [[1]] }))
    executeButton().click()
    await settle()
    const pending = deferred<QueryResult>()
    const refresh = deferred<DatabaseRows>()
    mocks.execute.mockReturnValueOnce(pending.promise)
    mocks.rows.mockReturnValueOnce(refresh.promise)
    executeButton().click()
    shortcut()
    shortcut()
    await settle()
    expect(mocks.execute).toHaveBeenCalledTimes(2)
    expect(host.querySelector('.query-table')).toBeNull()
    pending.resolve(result({ columns: ['new'], rows: [[2]] }))
    await settle()
    shortcut()
    executeButton().click()
    await settle()
    expect(mocks.execute).toHaveBeenCalledTimes(2)
    refresh.resolve(tableRows)
    await settle()
    expect(executeButton().disabled).toBe(false)
  })

  it('旧执行晚返回不能覆盖新路由的结果或触发旧表刷新', async () => {
    const old = deferred<QueryResult>()
    const current = deferred<QueryResult>()
    mocks.execute.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    await mount()
    executeButton().click()
    await settle()
    const oldSignal = mocks.execute.mock.calls[0]![1] as AbortSignal
    mocks.route.params.sourceId = 'b'
    await settle()
    expect(oldSignal.aborted).toBe(true)
    await openSQL()
    executeButton().click()
    await settle()
    old.resolve(result({ columns: ['old'], rows: [[1]] }))
    await settle()
    expect(executeButton().disabled).toBe(true)
    expect(mocks.rows).toHaveBeenCalledTimes(2)
    current.resolve(result({ columns: ['current'], rows: [[2]] }))
    await settle()
    expect(host.querySelector('.query-table [data-table-rows]')?.getAttribute('data-table-rows')).toBe('[{"current":2}]')
    expect(mocks.rows).toHaveBeenCalledTimes(3)
  })

  it('等待刷新时切换路由，旧确认不能弹出成功或关闭新路由弹窗', async () => {
    await mount()
    const refresh = deferred<DatabaseRows>()
    mocks.rows.mockReturnValueOnce(refresh.promise)
    await editSQL('DELETE FROM sample')
    executeButton().click()
    await settle()
    await confirm()
    const oldSignal = mocks.execute.mock.calls[0]![1] as AbortSignal
    mocks.route.params.sourceId = 'b'
    await settle()
    expect(oldSignal.aborted).toBe(true)
    await openSQL()
    await editSQL('UPDATE sample SET id = 8')
    executeButton().click()
    await settle()
    refresh.resolve(tableRows)
    await settle()
    expect(dialog()?.textContent).toContain('UPDATE sample SET id = 8')
    expect(mocks.success).not.toHaveBeenCalled()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(host.querySelector('.query-refresh-warning')).toBeNull()
    expect(host.querySelector('.query-table')).toBeNull()
  })

  it('卸载中止请求并丢弃晚到的成功结果', async () => {
    const pending = deferred<QueryResult>()
    mocks.execute.mockReturnValue(pending.promise)
    await mount()
    executeButton().click()
    await settle()
    const signal = mocks.execute.mock.calls[0]![1] as AbortSignal
    app?.unmount()
    app = undefined
    expect(signal.aborted).toBe(true)
    pending.resolve(result())
    await settle()
    expect(mocks.rows).toHaveBeenCalledTimes(1)
    expect(mocks.success).not.toHaveBeenCalled()
  })
})

describe('SQL 失败没有自动重放', () => {
  it.each(['timeout', 'result_mode_unsupported'])('危险 SQL 的 %s 关闭原确认框，先展示错误再允许重新发起', async (code) => {
    mocks.execute.mockRejectedValue(new NcpApiError(code, '拒绝'))
    await mount()
    await editSQL('DELETE FROM sample')
    await selectMode('exec')
    executeButton().click()
    await settle()
    await confirm()
    expect(dialog()).toBeNull()
    expect(host.querySelector('.query-message--error')).not.toBeNull()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    executeButton().click()
    await settle()
    expect(dialog()).not.toBeNull()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
  })

  it.each(['result_mode_unsupported', 'DATABASE_INPUT_INVALID'])('%s 明确报告执行前拒绝，不降级重放', async (code) => {
    mocks.execute.mockRejectedValue(new NcpApiError(code, '拒绝'))
    await mount()
    await selectMode('rows')
    executeButton().click()
    await settle()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(mocks.execute.mock.calls[0]![0].resultMode).toBe('rows')
    expect(mocks.rows).toHaveBeenCalledTimes(1)
    expect(host.querySelector('.query-message--error')?.textContent).toContain('SQL 未提交执行')
    if (code === 'result_mode_unsupported') expect(host.textContent).toContain('当前 Agent 不支持结果模式，请先升级 Agent')
  })

  it.each(['sql_invalid', 'timeout', 'constraint_failed', 'DATABASE_OPERATION_FAILED'])('%s 提醒可能已有写入，不建议盲目重试', async (code) => {
    mocks.execute.mockRejectedValue(new NcpApiError(code, '数据库错误'))
    await mount()
    executeButton().click()
    await settle()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(mocks.rows).toHaveBeenCalledTimes(1)
    expect(host.querySelector('.query-message--error')?.textContent).toContain('写操作可能已生效；请先核对数据库状态，再决定是否重试')
    expect(host.querySelector('.query-message--error')?.textContent).not.toContain('稍后重试')
    expect(host.querySelector('.query-table')).toBeNull()
  })
})
