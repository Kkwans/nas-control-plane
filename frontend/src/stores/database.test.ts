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
