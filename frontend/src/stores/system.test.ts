import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useSystemStore } from './system'

const apiMocks = vi.hoisted(() => ({
  requestCapabilities: vi.fn(),
  requestDockerInventory: vi.fn(),
  requestSystemSummary: vi.fn(),
  subscribeSystemEvents: vi.fn(),
}))

vi.mock('@/api/control', () => ({
  requestListPreference: vi.fn(),
  requestPreferences: vi.fn(),
  updateListPreference: vi.fn(),
  updatePreferences: vi.fn(),
}))

vi.mock('@/api/system', () => ({
  NcpApiError: class NcpApiError extends Error {
    code = 'TEST_ERROR'
  },
  ...apiMocks,
}))

const summary = {
  collectedAt: '2026-08-24T00:00:00.000Z',
  host: { hostname: 'ncp-test' },
  cpu: { usagePercent: 10, logicalCores: 4, load1: 0.5, load5: 0.4, load15: 0.3 },
  memory: { totalBytes: 100, usedBytes: 40, availableBytes: 60 },
  network: [],
} as never

const inventory = {
  collectedAt: '2026-08-24T00:00:00.000Z',
  projects: [],
} as never

describe('useSystemStore.refresh', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
    apiMocks.subscribeSystemEvents.mockReturnValue({ close: vi.fn() })
    apiMocks.requestSystemSummary.mockResolvedValue(summary)
    apiMocks.requestDockerInventory.mockResolvedValue(inventory)
    apiMocks.requestCapabilities.mockResolvedValue({})
  })

  it('does not drop an inventory refresh while summary is in flight', async () => {
    let resolveSummary: ((value: typeof summary) => void) | undefined
    apiMocks.requestSystemSummary.mockReturnValueOnce(new Promise((resolve) => {
      resolveSummary = resolve
    }))

    const store = useSystemStore()
    const summaryRefresh = store.refresh({ summary: true })
    await Promise.resolve()
    const inventoryRefresh = store.refresh({ inventory: true })

    expect(apiMocks.requestSystemSummary).toHaveBeenCalledTimes(1)
    expect(apiMocks.requestDockerInventory).toHaveBeenCalledTimes(1)

    resolveSummary?.(summary)
    await Promise.all([summaryRefresh, inventoryRefresh])
    expect(store.summary).toEqual(summary)
    expect(store.inventory).toEqual(inventory)
  })

  it('coalesces concurrent refreshes for the same resource', async () => {
    let resolveInventory: ((value: typeof inventory) => void) | undefined
    apiMocks.requestDockerInventory.mockReturnValueOnce(new Promise((resolve) => {
      resolveInventory = resolve
    }))

    const store = useSystemStore()
    const first = store.refresh({ inventory: true })
    const second = store.refresh({ inventory: true })

    expect(apiMocks.requestDockerInventory).toHaveBeenCalledTimes(1)
    resolveInventory?.(inventory)
    await Promise.all([first, second])
  })

  it('keeps successful data when another requested resource fails', async () => {
    apiMocks.requestDockerInventory.mockRejectedValueOnce(new Error('inventory unavailable'))

    const store = useSystemStore()
    await store.refresh({ summary: true, inventory: true })

    expect(store.summary).toEqual(summary)
    expect(store.inventory).toBeNull()
    expect(store.connectionState).toBe('degraded')
    expect(store.errorCode).toBe('NETWORK_UNAVAILABLE')
  })
})
