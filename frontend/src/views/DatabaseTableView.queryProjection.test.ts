// @vitest-environment jsdom

import { createApp, h, nextTick, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, QueryResult } from '@/api/database'
import { beginSession } from '@/session/sessionLifecycle'
import DatabaseTableView from './DatabaseTableView.vue'

const mocks = vi.hoisted(() => ({ rows: vi.fn(), execute: vi.fn(), store: vi.fn() }))
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } }),
  useRouter: () => ({ replace: vi.fn() }),
}))
vi.mock('@/stores/database', () => ({ useDatabaseStore: mocks.store }))
vi.mock('@/api/database', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/database')>(),
  loadDatabaseRows: mocks.rows, executeDatabaseSQL: mocks.execute,
}))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  return { useListPreference: () => ({ pageSize: ref(25) }) }
})
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/DatabaseCellEditor.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/SqlEditor.vue', () => ({ default: { render: () => null } }))

const table: DatabaseTable = {
  schema: '', name: 'sample', type: 'table',
  columns: [{ name: 'id', dataType: 'integer', nullable: false, primaryKey: true, position: 1 }],
}
const source: DatabaseSource = {
  id: 'a', name: 'Source A', driver: 'sqlite', category: 'project', project: '', module: '',
  location: '', requiresLogin: false, status: 'connected', tags: [],
}
const rows: DatabaseRows = { table, rows: [{ id: 1 }], limit: 25, offset: 0, hasMore: false }
function result(columns: string[], rows: QueryResult['rows']): QueryResult {
  return { columns, rows, rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 1 }
}

let app: App | undefined
let host: HTMLDivElement
let state: { queryResult: QueryResult | null }
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseTableView, { ref: (instance: unknown) => {
    if (instance) state = (instance as { $: { setupState: typeof state } }).$.setupState
  } }) })
  app.directive('loading', {})
  app.mount(host)
  await settle()
  const tab = [...host.querySelectorAll<HTMLButtonElement>('.table-tabs button')]
    .find((item) => item.textContent?.trim() === '执行 SQL')!
  tab.click()
  await settle()
}
async function execute() {
  host.querySelector<HTMLButtonElement>('.sql-toolbar__actions > .el-button')!.click()
  await settle()
}
function headers() {
  return [...host.querySelectorAll('.query-table .el-table__header th .cell')].map((cell) => cell.textContent)
}
function cells() {
  return [...host.querySelectorAll('.query-table .el-table__body tr')].map((row) =>
    [...row.querySelectorAll('td .cell')].map((cell) => cell.textContent))
}

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  mocks.store.mockReturnValue({
    sources: [source], catalogs: { a: { source, tables: [table] } },
    source: () => source, connection: () => ({ sourceId: 'a' }),
  })
  mocks.rows.mockResolvedValue(rows)
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

// 保留真实 Vue / Element Plus 列注册与单元格渲染，避免仅检查投影对象漏掉列复用问题。
describe('SQL 结果按列位置展示', () => {
  it('重复列名分别展示对应索引的值，不覆盖同名列', async () => {
    mocks.execute.mockResolvedValue(result(['x', 'x'], [[1, 2], [3, 4]]))
    await mount()
    await execute()
    expect(mocks.execute).toHaveBeenCalledOnce()
    expect(headers()).toEqual(['x', 'x'])
    expect(cells()).toEqual([['1', '2'], ['3', '4']])
  })

  it('空列名、原型名、点名和空白原样作为标签，不参与取值路径', async () => {
    const labels = ['', '__proto__', 'constructor', 'a.b', '  label  ', 'column_0', '']
    mocks.execute.mockResolvedValue(result(labels, [['empty first', 'prototype', 'constructor value', 'dot', 'spaces', 'alias', 'empty last']]))
    await mount()
    await execute()
    expect(headers()).toEqual(labels)
    expect(cells()).toEqual([['empty first', 'prototype', 'constructor value', 'dot', 'spaces', 'alias', 'empty last']])
  })

  it('保留大整数、小数字符串、布尔值、空字符串、NULL 与按原始标签遮罩', async () => {
    const values = ['9007199254740993', '1.234567890123456789002500', true, false, '', null, 'fixture-secret', null]
    mocks.execute.mockResolvedValue(result(['value', 'value', 'flag', 'flag', '', '', 'token', 'token'], [values]))
    await mount()
    await execute()
    expect(cells()).toEqual([['9007199254740993', '1.234567890123456789002500', 'true', 'false', '', 'NULL', '••••••••', 'NULL']])
    expect([...host.querySelectorAll('.query-table .null-value')].map((cell) => cell.textContent)).toEqual(['NULL', 'NULL'])
    expect(host.querySelector('.query-table')?.textContent).not.toContain('fixture-secret')
  })

  it('下一次查询的列重排与增减不会沿用旧结果', async () => {
    mocks.execute.mockResolvedValueOnce(result(['left', 'right'], [[1, 2]]))
    await mount()
    await execute()
    expect(headers()).toEqual(['left', 'right'])
    expect(cells()).toEqual([['1', '2']])
    mocks.execute.mockResolvedValueOnce(result(['right', 'left', 'left'], [[20, 10, 30]]))
    await execute()
    expect(mocks.execute).toHaveBeenCalledTimes(2)
    expect(headers()).toEqual(['right', 'left', 'left'])
    expect(cells()).toEqual([['20', '10', '30']])
  })

  it('同一表格实例更新结果时，列重排和增减也按新位置渲染', async () => {
    mocks.execute.mockResolvedValue(result(['left', 'right'], [[1, 2]]))
    await mount()
    await execute()
    const tableElement = host.querySelector('.query-table .el-table')
    state.queryResult = result(['right', 'left', 'left'], [[20, 10, 30]])
    await settle()
    expect(host.querySelector('.query-table .el-table')).toBe(tableElement)
    expect(headers()).toEqual(['right', 'left', 'left'])
    expect(cells()).toEqual([['20', '10', '30']])
    state.queryResult = result(['__proto__'], [['new value']])
    await settle()
    expect(host.querySelector('.query-table .el-table')).toBe(tableElement)
    expect(headers()).toEqual(['__proto__'])
    expect(cells()).toEqual([['new value']])
  })
})
