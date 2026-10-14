// @vitest-environment jsdom

import { createApp, h, nextTick, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, DatabaseValue } from '@/api/database'
import { beginSession } from '@/session/sessionLifecycle'
import DatabaseTableView from './DatabaseTableView.vue'

const mocks = vi.hoisted(() => ({ rows: vi.fn(), store: vi.fn(), insert: vi.fn(), update: vi.fn(), success: vi.fn() }))
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { sourceId: 'a', table: 'sample' }, query: { schema: '' } }),
  useRouter: () => ({ replace: vi.fn() }),
}))
vi.mock('@/stores/database', () => ({ useDatabaseStore: mocks.store }))
vi.mock('@/api/database', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/database')>(),
  loadDatabaseRows: mocks.rows, insertDatabaseRow: mocks.insert, updateDatabaseRow: mocks.update,
}))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  return { useListPreference: () => ({ pageSize: ref(25) }) }
})
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/SqlEditor.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', async (importOriginal) => ({
  ...await importOriginal<typeof import('element-plus')>(),
  ElMessage: { success: mocks.success },
}))

const source: DatabaseSource = {
  id: 'a', name: 'Source A', driver: 'sqlite', category: 'project', project: '', module: '',
  location: '', requiresLogin: false, status: 'connected', tags: [],
}
const row = { id: 1, enabled: null, label: null }
let table: DatabaseTable
interface ViewState {
  rowForm: Record<string, string>
  rowNullFields: Record<string, boolean>
  rowDraftPending: boolean
  rowDialogOpen: boolean
  openEdit: (row: Record<string, DatabaseValue>) => void
  openInsert: () => void
  submitRow: () => Promise<void>
}
let state: ViewState
let app: App | undefined
let host: HTMLDivElement
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
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
function field(name: string) {
  const found = [...document.querySelectorAll<HTMLElement>('.row-form .el-form-item')]
    .find((element) => element.querySelector('.form-label > span')?.firstChild?.textContent === name)
  if (!found) throw new Error(`缺少字段：${name}`)
  return found
}
function booleanInput() { return field('enabled').querySelector<HTMLInputElement>('input[role="combobox"]')! }
async function openBoolean() {
  field('enabled').querySelector<HTMLElement>('.el-select__wrapper')!.click()
  await settle()
}
async function selectBoolean(label: string) {
  const popup = document.getElementById(booleanInput().getAttribute('aria-controls')!)!
  const option = [...popup.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((element) => element.textContent?.trim() === label)
  expect(option).toBeDefined()
  option!.click()
  await settle()
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((yes) => { resolve = yes })
  return { promise, resolve }
}

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('禁止真实网络请求') }))
  table = {
    schema: '', name: 'sample', type: 'table',
    columns: [
      { name: 'id', dataType: 'INTEGER', nullable: false, primaryKey: true, position: 1, writeMode: 'server-generated' },
      { name: 'enabled', dataType: 'BOOLEAN', nullable: true, primaryKey: false, position: 2, writeMode: 'required' },
      { name: 'label', dataType: 'TEXT', nullable: true, primaryKey: false, position: 3, writeMode: 'required' },
    ],
  }
  mocks.store.mockReturnValue({
    sources: [source], catalogs: { a: { source, tables: [table] } },
    source: () => source, connection: () => ({ sourceId: 'a' }),
  })
  mocks.rows.mockImplementation(async (): Promise<DatabaseRows> => ({ table, rows: [row], limit: 25, offset: 0, hasMore: false }))
})
afterEach(async () => { app?.unmount(); app = undefined; host?.remove(); await settle(); vi.unstubAllGlobals() })

// 页面与编辑器均为真实组件；仅数据边界和无关内容被替换，不运行浏览器或数据库。
describe('DatabaseTableView 可空布尔字段与硬锁', () => {
  it('NULL 布尔字段可退出并重新选择 NULL，非布尔 NULL 输入仍禁用', async () => {
    await mount()
    state.openEdit(row)
    await settle()
    expect(booleanInput().disabled).toBe(false)
    expect(field('label').querySelector<HTMLInputElement>('.database-cell-editor input')!.disabled).toBe(true)
    await openBoolean()
    await selectBoolean('false')
    expect(state.rowNullFields.enabled).toBe(false)
    expect(state.rowForm.enabled).toBe('false')
    await openBoolean()
    await selectBoolean('NULL（显式）')
    expect(state.rowNullFields.enabled).toBe(true)
    expect(booleanInput().disabled).toBe(false)
  })

  it.each(['insert', 'edit'] as const)('%s 请求等待期间，NULL 选择器与已打开的选项均不能改写草稿', async (mode) => {
    const pending = deferred<{ rowsAffected: number }>()
    const write = mode === 'insert' ? mocks.insert : mocks.update
    write.mockReturnValue(pending.promise)
    await mount()
    if (mode === 'insert') {
      state.openInsert()
      state.rowNullFields.label = true
      await settle()
      await openBoolean()
      await selectBoolean('NULL（显式）')
    } else {
      state.openEdit(row)
      await settle()
    }
    await openBoolean()
    expect(booleanInput().getAttribute('aria-expanded')).toBe('true')
    const request = state.submitRow()
    try {
      await settle()
      expect(write).toHaveBeenCalledOnce()
      expect(write.mock.calls[0]![0].values.enabled).toBeNull()
      expect(state.rowDraftPending).toBe(true)
      expect(booleanInput().disabled).toBe(true)
      await selectBoolean('true')
      expect(state.rowNullFields.enabled).toBe(true)
      expect(state.rowForm.enabled).toBe('')
      await openBoolean()
      expect(booleanInput().getAttribute('aria-expanded')).toBe('false')
      expect(write).toHaveBeenCalledOnce()
    } finally {
      pending.resolve({ rowsAffected: 1 })
      await request
    }
    expect(state.rowDialogOpen).toBe(false)
  })

  it.each([null, true, false])('编辑时布尔主键 %s 始终禁用，新增时仍可从 NULL 选值', async (value) => {
    table.columns[1]!.primaryKey = true
    await mount()
    state.openEdit({ ...row, enabled: value })
    await settle()
    expect(booleanInput().disabled).toBe(true)
    await openBoolean()
    expect(booleanInput().getAttribute('aria-expanded')).toBe('false')
    expect(state.rowNullFields.enabled).toBe(value === null)
    expect(mocks.update).not.toHaveBeenCalled()
    state.openInsert()
    state.rowNullFields.enabled = true
    await settle()
    expect(booleanInput().disabled).toBe(false)
    await openBoolean()
    await selectBoolean('false')
    expect(state.rowNullFields.enabled).toBe(false)
    expect(state.rowForm.enabled).toBe('false')
  })
})
