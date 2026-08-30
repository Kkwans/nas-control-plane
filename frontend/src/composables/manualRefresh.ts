import { inject, provide, type InjectionKey } from 'vue'

export type ManualRefreshHandler = () => void | Promise<void>

export interface ManualRefreshFailure {
  handler: ManualRefreshHandler
  error: unknown
}

export interface ManualRefreshResult {
  requested: number
  succeeded: number
  failed: ManualRefreshFailure[]
}

export interface ManualRefreshRegistry {
  register(handler: ManualRefreshHandler): () => void
  refresh(): Promise<ManualRefreshResult>
}

export function createManualRefreshRegistry(): ManualRefreshRegistry {
  const handlers = new Set<ManualRefreshHandler>()

  return {
    register(handler) {
      handlers.add(handler)
      return () => handlers.delete(handler)
    },
    async refresh(): Promise<ManualRefreshResult> {
      const requested = [...handlers]
      const settled = await Promise.allSettled(requested.map((handler) => Promise.resolve().then(handler)))
      const failed: ManualRefreshFailure[] = []
      settled.forEach((result, index) => {
        const handler = requested[index]
        if (result.status === 'rejected' && handler) failed.push({ handler, error: result.reason })
      })
      return { requested: requested.length, succeeded: requested.length - failed.length, failed }
    },
  }
}

const manualRefreshRegistryKey: InjectionKey<ManualRefreshRegistry> = Symbol('ncp-manual-refresh-registry')

export function provideManualRefreshRegistry(registry: ManualRefreshRegistry) {
  provide(manualRefreshRegistryKey, registry)
}

export function useManualRefreshRegistry() {
  return inject(manualRefreshRegistryKey)
}
