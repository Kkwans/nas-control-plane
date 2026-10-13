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
