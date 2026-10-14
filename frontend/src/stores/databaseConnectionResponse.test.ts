import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { invalidateSession } from '@/session/sessionLifecycle'
import { useDatabaseStore } from './database'

vi.mock('@/api/control', () => ({ requestDatabaseProjectPreferences: vi.fn(async () => []), updateDatabaseProjectPreference: vi.fn() }))
const source = { id: 'fixture', name: 'Fixture', driver: 'mysql', category: 'project', project: '', module: '', location: 'fixture:3306', requiresLogin: true, status: 'credentials_required', tags: [] }
const diagnostic = { connected: true, driver: 'mysql', endpoint: 'fixture:3306', operation: 'test_connection', durationMs: 0 }
function response(payload: unknown, status = 200) { return new Response(JSON.stringify(payload), { status }) }
beforeEach(() => { invalidateSession(); setActivePinia(createPinia()) })
afterEach(() => vi.unstubAllGlobals())

describe('真实连接 API 与 store 发布边界', () => {
  it.each(['false', {}, [], 1, null, undefined].map(connected => ({ connected })))('畸形 connected=$connected 不触发目录读取，不发布或覆盖浏览器凭据', async ({ connected }) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ ...diagnostic, connected })); vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore(); store.credentials = { fixture: { username: 'previous' } }
    await expect(store.connect('fixture', { username: 'next', password: 'private-fixture' })).rejects.toMatchObject({ code: 'DATABASE_CONNECTION_OUTCOME_UNKNOWN' })
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(store.credentials).toEqual({ fixture: { username: 'previous' } })
    expect(store.catalogs).toEqual({})
  })
  it('合法 false 诊断不读取目录或发布凭据', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ ...diagnostic, connected: false, code: 'auth_failed' })); vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    await expect(store.connect('fixture', { username: 'fixture-user' })).rejects.toThrow('auth_failed')
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(store.credentials).toEqual({}); expect(store.catalogs).toEqual({})
  })
  it('已确认连接后的坏目录只报告目录阶段，不把已保存可能性说成连接失败', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(response(diagnostic)).mockResolvedValueOnce(response({ tables: [] })); vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    const error = await store.connect('fixture', { username: 'fixture-user', password: 'private-fixture' }).catch(error => error)
    expect(error).toMatchObject({ code: 'DATABASE_CONNECTION_CATALOG_FAILED' })
    expect(String(error)).not.toContain('private-'); expect(error).not.toHaveProperty('cause')
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(store.credentials).toEqual({}); expect(store.catalogs).toEqual({})
  })
  it('合法连接和目录后才发布快照，保持自动与手动请求行为', async () => {
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (path) => String(path).endsWith('/connect') ? response(diagnostic) : response({ source, tables: [] })); vi.stubGlobal('fetch', fetcher)
    const store = useDatabaseStore()
    await store.connect('fixture', {})
    expect(JSON.parse(String(fetcher.mock.calls[0]![1]!.body))).toEqual({ sourceId: 'fixture' })
    await store.connect('fixture', { username: 'fixture-user' })
    expect(store.credentials.fixture).toEqual({ username: 'fixture-user' })
    expect(store.catalogs.fixture?.source.id).toBe('fixture')
    expect(fetcher).toHaveBeenCalledTimes(4)
  })
})
