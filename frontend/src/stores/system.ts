import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  NcpApiError,
  requestCapabilities,
  requestDockerInventory,
  requestServices,
  requestSystemSummary,
  type DockerInventory,
  type ServiceListResponse,
  type SystemCapabilities,
  type SystemSummary,
} from '@/api/system'

export type SystemConnectionState = 'loading' | 'connected' | 'degraded' | 'unavailable'

export const useSystemStore = defineStore('system', () => {
  const connectionState = ref<SystemConnectionState>('loading')
  const capabilities = ref<SystemCapabilities | null>(null)
  const summary = ref<SystemSummary | null>(null)
  const inventory = ref<DockerInventory | null>(null)
  const services = ref<ServiceListResponse | null>(null)
  const errorCode = ref<string | null>(null)
  const isRefreshing = ref(false)

  const deviceName = computed(() => summary.value?.host.hostname || capabilities.value?.hostname || 'NAS Control Plane')
  const lastUpdated = computed(() => summary.value?.collectedAt || inventory.value?.collectedAt || services.value?.collectedAt || null)

  async function refresh() {
    isRefreshing.value = true
    connectionState.value = summary.value || inventory.value ? 'degraded' : 'loading'
    const [capabilitiesResult, summaryResult, inventoryResult, servicesResult] = await Promise.allSettled([
      requestCapabilities(),
      requestSystemSummary(),
      requestDockerInventory(),
      requestServices(),
    ])

    if (capabilitiesResult.status === 'fulfilled') capabilities.value = capabilitiesResult.value
    if (summaryResult.status === 'fulfilled') summary.value = summaryResult.value
    if (inventoryResult.status === 'fulfilled') inventory.value = inventoryResult.value
    if (servicesResult.status === 'fulfilled') services.value = servicesResult.value

    const failures = [capabilitiesResult, summaryResult, inventoryResult, servicesResult].filter(
      (result): result is PromiseRejectedResult => result.status === 'rejected',
    )
    errorCode.value = failures.length > 0 ? errorCodeFor(failures[0]?.reason) : null
    const successful = 4 - failures.length
    connectionState.value = successful === 4 ? 'connected' : successful > 0 ? 'degraded' : 'unavailable'
    isRefreshing.value = false
  }

  function clear() {
    capabilities.value = null
    summary.value = null
    inventory.value = null
    services.value = null
    errorCode.value = null
    connectionState.value = 'loading'
  }

  return {
    connectionState,
    capabilities,
    summary,
    inventory,
    services,
    errorCode,
    isRefreshing,
    deviceName,
    lastUpdated,
    refresh,
    clear,
  }
})

function errorCodeFor(error: unknown): string {
  return error instanceof NcpApiError ? error.code : 'NETWORK_UNAVAILABLE'
}
