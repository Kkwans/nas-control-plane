// @vitest-environment jsdom

import { readFileSync } from 'node:fs'
import { createApp, h, nextTick, type App } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseRows, DatabaseSource, DatabaseTable, QueryResult } from '@/api/database'
import { beginSession } from '@/session/sessionLifecycle'
import DatabaseTableView from './DatabaseTableView.vue'
import viewSource from './DatabaseTableView.vue?raw'

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
  columns: [{ name: 'label', dataType: 'text', nullable: false, primaryKey: false, position: 1 }],
}
const source: DatabaseSource = {
  id: 'a', name: 'Source A', driver: 'sqlite', category: 'project', project: '', module: '',
  location: '', requiresLogin: false, status: 'connected', tags: [],
}
const rows: DatabaseRows = { table, rows: [{ label: 'example' }], limit: 25, offset: 0, hasMore: false }
const queryResult: QueryResult = {
  columns: ['label'], rows: [['example']], rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 1,
}
const scopeId = 'data-v-table-accessibility-test'
const tokensSource = readFileSync(`${import.meta.dirname}/../styles/tokens.css`, 'utf8')
const { descriptor } = parse(viewSource, { filename: 'DatabaseTableView.vue' })
const styles = descriptor.styles.map((style) => compileStyle({
  filename: 'DatabaseTableView.vue', id: scopeId, source: style.content, scoped: true,
}))

let app: App | undefined
let host: HTMLDivElement
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
async function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseTableView) })
  app.directive('loading', {})
  app.mount(host)
  await settle()
}
function button(label: string, selector = 'button') {
  const found = [...host.querySelectorAll<HTMLButtonElement>(selector)].find((element) => element.textContent?.trim() === label)
  if (!found) throw new Error(`缺少按钮：${label}`)
  return found
}
function declarations(selector: string) {
  const values: Record<string, string> = {}
  for (const style of styles) {
    style.rawResult!.root.walkRules((rule) => {
      if (!rule.selectors.includes(selector)) return
      rule.walkDecls((declaration) => { values[declaration.prop] = declaration.value })
    })
  }
  return values
}
function token(name: string) {
  const value = new RegExp(`${name}:\\s*(#[\\da-f]{6});`, 'i').exec(tokensSource)?.[1]
  if (!value) throw new Error(`缺少颜色 token：${name}`)
  return value
}
function luminance(color: string) {
  const channels = [1, 3, 5].map((start) => {
    const value = Number.parseInt(color.slice(start, start + 2), 16) / 255
    return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4
  })
  return channels[0]! * .2126 + channels[1]! * .7152 + channels[2]! * .0722
}
function contrast(first: string, second: string) {
  const values = [luminance(first), luminance(second)].sort((a, b) => b - a)
  return (values[0]! + .05) / (values[1]! + .05)
}

beforeEach(() => {
  vi.resetAllMocks()
  beginSession()
  mocks.store.mockReturnValue({
    sources: [source], catalogs: { a: { source, tables: [table] } },
    source: () => source, connection: () => ({ sourceId: 'a' }),
  })
  mocks.rows.mockResolvedValue(rows)
  mocks.execute.mockResolvedValue(queryResult)
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

// 使用真实 Element Plus 表格验证属性落点；JSDOM 不验证浏览器滚动距离或实际像素对比度。
describe('DatabaseTableView 表格键盘入口与按钮对比度', () => {
  it.each([
    ['表数据', '.data-table', 'sample 表数据'],
    ['表结构', '.structure-workbench', 'sample 表结构'],
    ['执行 SQL', '.query-table', 'sample SQL 查询结果'],
  ])('%s 的实际滚动容器可聚焦，并位于有名称的区域内', async (tab, selector, name) => {
    await mount()
    if (tab !== '表数据') {
      button(tab!, '.table-tabs button').click()
      await settle()
    }
    if (tab === '执行 SQL') {
      host.querySelector<HTMLButtonElement>('.sql-toolbar__actions > .el-button')!.click()
      await settle()
      expect(mocks.execute).toHaveBeenCalledOnce()
    }
    const region = host.querySelector<HTMLElement>(selector!)!
    expect(region.getAttribute('role')).toBe('region')
    expect(region.getAttribute('aria-label')).toBe(name)
    expect(region.hasAttribute('tabindex')).toBe(false)
    const tableRoot = region.querySelector<HTMLElement>('.el-table')!
    expect(tableRoot.hasAttribute('tabindex')).toBe(false)
    const scroller = tableRoot.querySelector<HTMLElement>('.el-table__body-wrapper > .el-scrollbar > .el-scrollbar__wrap')!
    expect(scroller.getAttribute('tabindex')).toBe('0')
    scroller.focus()
    expect(document.activeElement).toBe(scroller)
    if (tab === '表数据') {
      const actions = [...region.querySelectorAll<HTMLButtonElement>('.row-actions button')]
      expect(actions).toHaveLength(2)
      expect(actions.every((action) => action.disabled)).toBe(true)
    }
  })

  it('无数据时仍保留真实滚动容器的键盘入口', async () => {
    mocks.rows.mockResolvedValue({ ...rows, rows: [] })
    await mount()
    const scroller = host.querySelector<HTMLElement>('.data-table .el-scrollbar__wrap')!
    expect(scroller.tabIndex).toBe(0)
    scroller.focus()
    expect(document.activeElement).toBe(scroller)
  })

  it('真实滚动区域使用内侧焦点描边，避免被表格裁切', () => {
    expect(styles.flatMap((style) => style.errors)).toEqual([])
    for (const selector of ['.data-table', '.structure-workbench', '.query-table']) {
      expect(declarations(`${selector}[${scopeId}] .el-scrollbar__wrap:focus-visible`)).toEqual({
        outline: '2px solid var(--ncp-primary)', 'outline-offset': '-2px',
      })
    }
  })

  it('页内次要按钮仅加深悬停与按下文字，保留原有背景、禁用及焦点规则', () => {
    expect(declarations(`.database-header-action[${scopeId}]`)).toEqual({
      '--el-button-hover-text-color': 'var(--ncp-primary-strong)',
      '--el-button-active-text-color': 'var(--ncp-primary-strong)',
      'min-height': 'var(--ncp-control-height)',
    })
    // 这组 14px 文本须达到普通字号 4.5:1；浏览器全量 axe 继续覆盖最终计算样式。
    for (const background of ['--ncp-primary-soft', '--ncp-primary-hover', '--ncp-primary-active']) {
      expect(contrast(token('--ncp-primary-strong'), token(background))).toBeGreaterThanOrEqual(4.5)
    }
  })
})
