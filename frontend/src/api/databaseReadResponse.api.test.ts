import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { discoverDatabases, loadDatabaseCatalog, loadDatabaseRows } from './database'
import { databaseProjectKey, useDatabaseStore } from '@/stores/database'
import { invalidateSession } from '@/session/sessionLifecycle'

vi.mock('@/api/control', () => ({
  requestDatabaseProjectPreferences: vi.fn().mockResolvedValue([]),
  updateDatabaseProjectPreference: vi.fn(),
}))

const source = {
  id: 'fixture', name: '数据库', driver: 'sqlite', category: 'project', project: '项目', module: '',
  location: '/fixture.sqlite', requiresLogin: false, status: 'available', tags: [],
}
const table = {
  schema: '', name: 'sample', type: 'BASE TABLE',
  columns: [{ name: 'id', dataType: 'INTEGER', nullable: false, primaryKey: true, position: 1 }],
}
const rows = { table, rows: [{ id: '9007199254740993' }], limit: 25, offset: 0, hasMore: false }
const catalog = { source, tables: [table] }
const discovery = { collectedAt: '2026-10-14T00:00:00Z', sources: [source] }

function response(payload: unknown, status = 200) {
  return new Response(JSON.stringify(payload), { status })
}

beforeEach(() => {
  invalidateSession()
  setActivePinia(createPinia())
})
afterEach(() => vi.unstubAllGlobals())

describe('数据库读取 API 解码边界', () => {
  it('三类请求只派发一次，并保留现有请求体与取消信号', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(response(discovery))
      .mockResolvedValueOnce(response(catalog))
      .mockResolvedValueOnce(response(rows))
    vi.stubGlobal('fetch', fetcher)
    const controller = new AbortController()
    const connection = { sourceId: 'fixture', credentials: { username: 'fixture-user' } }
    expect(await discoverDatabases(true, controller.signal)).toEqual(discovery)
    expect(await loadDatabaseCatalog(connection, controller.signal)).toEqual(catalog)
    expect(await loadDatabaseRows({ ...connection, table: 'sample', limit: 25, offset: 0 }, controller.signal)).toEqual(rows)
    expect(fetcher).toHaveBeenCalledTimes(3)
    expect(fetcher).toHaveBeenNthCalledWith(1, '/api/v1/databases/discovery?refresh=true', expect.objectContaining({ method: 'GET', signal: controller.signal, credentials: 'same-origin' }))
    expect(fetcher).toHaveBeenNthCalledWith(2, '/api/v1/databases/catalog', expect.objectContaining({ method: 'POST', signal: controller.signal, body: JSON.stringify(connection) }))
    expect(fetcher).toHaveBeenNthCalledWith(3, '/api/v1/databases/rows', expect.objectContaining({ method: 'POST', signal: controller.signal }))
  })

  it.each([
    ['discovery', () => discoverDatabases(), { ...discovery, sources: [{ ...source, project: 17 }] }],
    ['catalog', () => loadDatabaseCatalog({ sourceId: 'fixture' }), { ...catalog, tables: {} }],
    ['rows', () => loadDatabaseRows({ sourceId: 'fixture', table: 'sample' }), { ...rows, rows: {} }],
  ] as const)('%s 拒绝畸形成功 JSON，不自动重试且不泄漏正文', async (_kind, request, payload) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ ...payload, password: 'secret-fixture', requestId: 'secret-fixture' }))
    vi.stubGlobal('fetch', fetcher)
    const error = await request().catch((error: unknown) => error)
    expect(error).toMatchObject({ code: 'DATABASE_RESPONSE_INVALID', message: '数据库服务返回了无法识别的数据。' })
    expect(error).not.toHaveProperty('cause')
    expect(JSON.stringify(error)).not.toContain('secret-fixture')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('rows:null 的 Go nil slice 通过 API 归一，不再把 null 发布给页面', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(response({ ...rows, rows: null })))
    const result = await loadDatabaseRows({ sourceId: 'fixture', table: 'sample' })
    expect(result.rows).toEqual([])
    expect(result.rows.length).toBe(0)
  })

  it('有效行字段文本和原型样式字段跨 JSON 边界原样保留', async () => {
    const row = JSON.parse('{"id":"9007199254740993","amount":"1.234567890123456789002500","__proto__":"合法列","":false}')
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(response({ ...rows, rows: [row] })))
    const result = await loadDatabaseRows({ sourceId: 'fixture', table: 'sample' })
    expect(result.rows[0]).toEqual(row)
    expect(Object.hasOwn(result.rows[0]!, '__proto__')).toBe(true)
  })

  it('JSON 语法错误仍返回相同安全错误码', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response('secret-fixture: invalid JSON', { status: 200 })))
    await expect(discoverDatabases()).rejects.toMatchObject({ code: 'DATABASE_RESPONSE_INVALID', message: '数据库服务返回了无法识别的数据。' })
  })

  it('HTTP 错误和请求取消不会被成功解码器改写或重放', async () => {
    const abort = new DOMException('Canceled', 'AbortError')
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(response({ code: 'result_too_large', message: '结果超过预算', requestId: 'fixture-budget' }, 422))
      .mockRejectedValueOnce(abort)
    vi.stubGlobal('fetch', fetcher)
    await expect(loadDatabaseRows({ sourceId: 'fixture', table: 'sample' })).rejects.toMatchObject({ code: 'result_too_large', requestId: 'fixture-budget' })
    await expect(discoverDatabases()).rejects.toBe(abort)
    expect(fetcher).toHaveBeenCalledTimes(2)
  })
})

describe('真实 API 到数据库 store 的发布边界', () => {
  it('畸形 discovery 保留最后有效来源、分组与收集时间，之后可正常恢复', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(response(discovery))
      .mockResolvedValueOnce(response({ collectedAt: 'changed', sources: [{ ...source, project: 17 }] }))
      .mockResolvedValueOnce(response({ ...discovery, collectedAt: 'recovered' }))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    await store.refreshDiscovery()
    const previous = store.sources
    await expect(store.refreshDiscovery()).rejects.toMatchObject({ code: 'DATABASE_RESPONSE_INVALID' })
    expect(store.sources).toBe(previous)
    expect(store.collectedAt).toBe(discovery.collectedAt)
    expect(databaseProjectKey(store.sources[0]!)).toBe('project:项目')
    expect(store.loading).toBe(false)
    await store.refreshDiscovery()
    expect(store.collectedAt).toBe('recovered')
  })

  it('畸形 catalog 不覆盖已加载目录，find 仍可用', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(response(catalog))
      .mockResolvedValueOnce(response({ ...catalog, tables: {} }))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    await store.loadCatalog('fixture')
    const previous = store.catalogs.fixture
    await expect(store.loadCatalog('fixture')).rejects.toMatchObject({ code: 'DATABASE_RESPONSE_INVALID' })
    expect(store.catalogs.fixture).toBe(previous)
    expect(store.catalogs.fixture?.tables.find((item) => item.name === 'sample')).toEqual(table)
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('连接后畸形目录也不能发布新凭据或覆盖有效目录', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(response({ connected: true }))
      .mockResolvedValueOnce(response(catalog))
      .mockResolvedValueOnce(response({ connected: true }))
      .mockResolvedValueOnce(response({ ...catalog, tables: [{ ...table, columns: false }] }))
    vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    await store.connect('fixture', { username: 'previous' })
    const previous = store.catalogs.fixture
    await expect(store.connect('fixture', { username: 'new-user' })).rejects.toMatchObject({ code: 'DATABASE_RESPONSE_INVALID' })
    expect(store.catalogs.fixture).toBe(previous)
    expect(store.credentials.fixture).toEqual({ username: 'previous' })
    expect(fetcher).toHaveBeenCalledTimes(4)
  })
})
