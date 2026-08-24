import { describe, expect, it, vi } from 'vitest'

import { createManualRefreshRegistry } from './manualRefresh'

describe('createManualRefreshRegistry', () => {
  it('waits for every registered refresh handler', async () => {
    const registry = createManualRefreshRegistry()
    const first = vi.fn(async () => undefined)
    const second = vi.fn(async () => undefined)
    registry.register(first)
    registry.register(second)

    await registry.refresh()

    expect(first).toHaveBeenCalledOnce()
    expect(second).toHaveBeenCalledOnce()
  })

  it('removes handlers when their registration is disposed', async () => {
    const registry = createManualRefreshRegistry()
    const handler = vi.fn()
    const unregister = registry.register(handler)

    expect(unregister()).toBe(true)
    expect(unregister()).toBe(false)
    await registry.refresh()

    expect(handler).not.toHaveBeenCalled()
  })

  it('does not prevent other pages from refreshing after one handler fails', async () => {
    const registry = createManualRefreshRegistry()
    const failing = vi.fn(() => { throw new Error('fixture failure') })
    const healthy = vi.fn()
    registry.register(failing)
    registry.register(healthy)

    await expect(registry.refresh()).resolves.toBeUndefined()
    expect(healthy).toHaveBeenCalledOnce()
  })
})
