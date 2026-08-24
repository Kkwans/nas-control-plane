import { inject, provide, type InjectionKey } from 'vue'

export type ManualRefreshHandler = () => void | Promise<void>

export interface ManualRefreshRegistry {
  register(handler: ManualRefreshHandler): () => void
  refresh(): Promise<void>
}

export function createManualRefreshRegistry(): ManualRefreshRegistry {
  const handlers = new Set<ManualRefreshHandler>()

  return {
    register(handler) {
      handlers.add(handler)
      return () => handlers.delete(handler)
    },
    async refresh() {
      await Promise.allSettled([...handlers].map((handler) => Promise.resolve().then(handler)))
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
