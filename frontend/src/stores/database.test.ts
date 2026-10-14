import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { invalidateSession } from '@/session/sessionLifecycle'

import { useDatabaseStore } from './database'

const apiMocks = vi.hoisted(() => ({
  requestDatabaseProjectPreferences: vi.fn(),
  updateDatabaseProjectPreference: vi.fn(),
  discoverDatabases: vi.fn(),
  loadDatabaseCatalog: vi.fn(),
  connectDatabase: vi.fn(),
}))

vi.mock('@/api/control', () => ({
  requestDatabaseProjectPreferences: apiMocks.requestDatabaseProjectPreferences,
  updateDatabaseProjectPreference: apiMocks.updateDatabaseProjectPreference,
}))

vi.mock('@/api/database', () => ({
  discoverDatabases: apiMocks.discoverDatabases,
  loadDatabaseCatalog: apiMocks.loadDatabaseCatalog,
  connectDatabase: apiMocks.connectDatabase,
}))

describe('useDatabaseStore session isolation', () => {
  beforeEach(() => {
    invalidateSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('does not restore optimistic archive state after logout', async () => {
    let resolveUpdate: (() => void) | undefined
    apiMocks.updateDatabaseProjectPreference.mockReturnValueOnce(
      new Promise<void>((resolve) => {
        resolveUpdate = resolve
      }),
    )

    const store = useDatabaseStore()
    store.archivedProjectKeys = ['project:existing']
    const operation = store.setProjectArchived('project:new', true)

    invalidateSession()
    store.resetSessionState()
    resolveUpdate?.()
    await operation

    expect(store.archivedProjectKeys).toEqual([])
    expect(apiMocks.updateDatabaseProjectPreference).toHaveBeenCalledWith(
      { projectKey: 'project:new', archived: true },
      expect.any(AbortSignal),
    )
  })
})


describe('useDatabaseStore connection cancellation', () => {
  beforeEach(() => {
    invalidateSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('does not start a catalog read after an aborted connection completes', async () => {
    let resolve!: (value: { connected: boolean }) => void
    apiMocks.connectDatabase.mockReturnValue(new Promise((done) => { resolve = done }))
    const store = useDatabaseStore()
    const controller = new AbortController()
    const operation = store.connect('a', { username: 'alice', password: 'secret' }, controller.signal)
    const rejection = expect(operation).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    resolve({ connected: true })
    await rejection
    expect(apiMocks.loadDatabaseCatalog).not.toHaveBeenCalled()
    expect(store.catalogs).toEqual({})
    expect(store.credentials).toEqual({})
  })

  it('does not publish a catalog or credentials after cancellation during catalog loading', async () => {
    let resolve!: (value: { tables: [] }) => void
    apiMocks.connectDatabase.mockResolvedValue({ connected: true })
    apiMocks.loadDatabaseCatalog.mockReturnValue(new Promise((done) => { resolve = done }))
    const store = useDatabaseStore()
    const controller = new AbortController()
    const operation = store.connect('a', { username: 'alice' }, controller.signal)
    const rejection = expect(operation).rejects.toMatchObject({ name: 'AbortError' })
    await Promise.resolve()
    controller.abort()
    resolve({ tables: [] })
    await rejection
    expect(store.catalogs).toEqual({})
    expect(store.credentials).toEqual({})
  })

  it('uses a frozen credential snapshot and publishes only after both requests finish', async () => {
    let resolve!: (value: { connected: boolean }) => void
    apiMocks.connectDatabase.mockReturnValue(new Promise((done) => { resolve = done }))
    apiMocks.loadDatabaseCatalog.mockResolvedValue({ tables: [] })
    const store = useDatabaseStore()
    const input = { username: 'alice' }
    const operation = store.connect('a', input)
    input.username = 'bob'
    expect(store.credentials).toEqual({})
    resolve({ connected: true })
    await operation
    expect(apiMocks.loadDatabaseCatalog).toHaveBeenCalledWith({ sourceId: 'a', credentials: { username: 'alice' } }, expect.any(AbortSignal))
    expect(store.credentials.a).toEqual({ username: 'alice' })
    expect(store.catalogs.a).toEqual({ tables: [] })
  })
})

const tlsSource = { id: 'a', name: 'Fixture', driver: 'mysql' as const, category: 'project' as const, project: '', module: '', location: 'fixture:3306', host: 'fixture', port: 3306, defaultDatabase: 'fixture', requiresLogin: true, status: '', tags: [] }

describe('数据库 TLS 快照和目标隔离', () => {
  beforeEach(() => {
    invalidateSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
    useDatabaseStore().sources = [{ ...tlsSource }]
  })
  it('冻结嵌套 TLS，connect 和 catalog 一致，getter 不暴露缓存引用', async () => {
    let resolve!: (value: { connected: boolean }) => void
    apiMocks.connectDatabase.mockReturnValue(new Promise(done => { resolve = done }))
    apiMocks.loadDatabaseCatalog.mockResolvedValue({ source: tlsSource, tables: [] })
    const store = useDatabaseStore()
    const input = { username: 'alice', password: '', tls: { mode: 'verify-full' as const, caPEM: 'synthetic-ca', serverName: 'fixture' } }
    const expected = structuredClone(input)
    const pending = store.connect('a', input)
    input.tls.caPEM = 'new-draft'; input.tls.serverName = 'new.example'
    resolve({ connected: true })
    await pending
    for (const mock of [apiMocks.connectDatabase, apiMocks.loadDatabaseCatalog]) {
      expect(mock).toHaveBeenCalledWith({ sourceId: 'a', credentials: expected }, expect.any(AbortSignal))
    }
    const returned = store.connection('a')
    returned.credentials!.tls!.caPEM = 'caller-change'
    returned.credentials!.password = 'caller-secret'
    apiMocks.loadDatabaseCatalog.mock.calls[0]![0].credentials.tls.caPEM = 'callee-change'
    expect(store.credentials.a).toEqual(expected)
  })
  it('TLS-only 不能变自动连接或借用保存密码；完整手动意图允许空密码', async () => {
    const store = useDatabaseStore()
    store.credentials.a = { username: 'saved', password: 'saved-synthetic', tls: { mode: 'verify-full', caPEM: 'saved-ca' } }
    await expect(store.connect('a', { tls: { mode: 'verify-full' } })).rejects.toMatchObject({ code: 'credentials_required' })
    expect(apiMocks.connectDatabase).not.toHaveBeenCalled()
    apiMocks.connectDatabase.mockResolvedValue({ connected: true })
    apiMocks.loadDatabaseCatalog.mockResolvedValue({ source: tlsSource, tables: [] })
    await store.connect('a', { username: 'manual', password: '', tls: { mode: 'verify-full' } })
    expect(apiMocks.connectDatabase).toHaveBeenCalledWith({ sourceId: 'a', credentials: { username: 'manual', password: '', tls: { mode: 'verify-full' } } }, expect.any(AbortSignal))
  })
  it.each(['host', 'port', 'driver', 'location', 'path', 'defaultDatabase'] as const)('相同 ID 的 %s 改变清除旧凭据和目录，阻止 connect 后续读取', async (field) => {
    let resolve!: (value: { connected: boolean }) => void
    apiMocks.connectDatabase.mockReturnValue(new Promise(done => { resolve = done }))
    const store = useDatabaseStore()
    store.credentials.a = { password: 'old-synthetic', tls: { mode: 'verify-full' } }
    store.catalogs.a = { source: tlsSource, tables: [] }
    const pending = store.connect('a', { username: 'manual' })
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    store.sources = [{ ...tlsSource, [field]: field === 'port' ? 5432 : field === 'driver' ? 'postgresql' : 'other' }]
    expect(store.credentials.a).toBeUndefined()
    expect(store.catalogs.a).toBeUndefined()
    resolve({ connected: true }); await rejected
    expect(apiMocks.loadDatabaseCatalog).not.toHaveBeenCalled()
  })
  it.each(['connect', 'loadCatalog'] as const)('相同 ID 在 %s 的目录等候期间换目标不发布旧结果', async operation => {
    let resolve!: (value: { source: typeof tlsSource; tables: [] }) => void
    apiMocks.connectDatabase.mockResolvedValue({ connected: true })
    apiMocks.loadDatabaseCatalog.mockReturnValue(new Promise(done => { resolve = done }))
    const store = useDatabaseStore()
    const pending = operation === 'connect' ? store.connect('a', { username: 'manual' }) : store.loadCatalog('a')
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    await Promise.resolve()
    store.sources[0]!.host = 'changed'
    resolve({ source: tlsSource, tables: [] }); await rejected
    expect(store.catalogs).toEqual({}); expect(store.credentials).toEqual({})
  })
  it('同源新请求优先，早到的旧连接不能读取目录或覆盖新缓存', async () => {
    let resolve!: (value: { connected: boolean }) => void
    apiMocks.connectDatabase.mockReturnValueOnce(new Promise(done => { resolve = done })).mockResolvedValueOnce({ connected: true })
    apiMocks.loadDatabaseCatalog.mockResolvedValue({ source: tlsSource, tables: [] })
    const store = useDatabaseStore()
    const old = store.connect('a', { username: 'old' })
    const rejected = expect(old).rejects.toMatchObject({ name: 'AbortError' })
    await store.connect('a', { username: 'new' })
    resolve({ connected: true }); await rejected
    expect(store.credentials.a).toEqual({ username: 'new' })
    expect(apiMocks.loadDatabaseCatalog).toHaveBeenCalledTimes(1)
  })
})
