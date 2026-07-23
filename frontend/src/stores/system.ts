import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  NcpApiError,
  requestCapabilities,
  requestDockerInventory,
  requestSystemSummary,
  type DockerInventory,
  type SystemCapabilities,
  type SystemSummary,
} from '@/api/system'

export type SystemConnectionState = 'loading' | 'connected' | 'degraded' | 'unavailable'
export type RealtimeState = 'connecting' | 'streaming' | 'polling' | 'offline'

const FALLBACK_INTERVAL = 15_000

export const useSystemStore = defineStore('system', () => {
  const connectionState = ref<SystemConnectionState>('loading')
  const realtimeState = ref<RealtimeState>('offline')
  const capabilities = ref<SystemCapabilities | null>(null)
  const summary = ref<SystemSummary | null>(null)
  const inventory = ref<DockerInventory | null>(null)
  const errorCode = ref<string | null>(null)
  const isRefreshing = ref(false)

  let eventSource: EventSource | null = null
  let fallbackTimer: number | null = null
  let refreshPromise: Promise<void> | null = null

  const deviceName = computed(() => summary.value?.host.hostname || capabilities.value?.hostname || 'NAS 管理面板')
  const lastUpdated = computed(() => summary.value?.collectedAt || inventory.value?.collectedAt || null)
  const services = computed(() => inventory.value?.projects ?? [])

  async function refresh(options: { includeCapabilities?: boolean } = {}) {
    if (refreshPromise) return refreshPromise
    refreshPromise = runRefresh(options.includeCapabilities ?? !capabilities.value)
    try {
      await refreshPromise
    } finally {
      refreshPromise = null
    }
  }

  async function runRefresh(includeCapabilities: boolean) {
    isRefreshing.value = true
    connectionState.value = summary.value || inventory.value ? 'degraded' : 'loading'
    const requests: Array<Promise<SystemCapabilities | SystemSummary | DockerInventory>> = [
      requestSystemSummary(),
      requestDockerInventory(),
    ]
    if (includeCapabilities) requests.push(requestCapabilities())

    const results = await Promise.allSettled(requests)
    const summaryResult = results[0]
    const inventoryResult = results[1]
    const capabilitiesResult = includeCapabilities ? results[2] : undefined

    if (summaryResult?.status === 'fulfilled') summary.value = summaryResult.value as SystemSummary
    if (inventoryResult?.status === 'fulfilled') inventory.value = inventoryResult.value as DockerInventory
    if (capabilitiesResult?.status === 'fulfilled') capabilities.value = capabilitiesResult.value as SystemCapabilities

    const failures = results.filter((result): result is PromiseRejectedResult => result.status === 'rejected')
    errorCode.value = failures.length > 0 ? errorCodeFor(failures[0]?.reason) : null
    const successful = results.length - failures.length
    connectionState.value = successful === results.length ? 'connected' : successful > 0 ? 'degraded' : 'unavailable'
    isRefreshing.value = false
  }

  function startRealtime() {
    if (eventSource) return
    realtimeState.value = 'connecting'
    eventSource = new EventSource('/api/v1/system/events')
    eventSource.onopen = () => {
      realtimeState.value = 'streaming'
      stopFallbackPolling()
    }
    eventSource.addEventListener('snapshot', () => {
      realtimeState.value = 'streaming'
      void refresh({ includeCapabilities: false })
    })
    eventSource.onerror = () => {
      realtimeState.value = 'polling'
      startFallbackPolling()
    }
  }

  function stopRealtime() {
    eventSource?.close()
    eventSource = null
    stopFallbackPolling()
    realtimeState.value = 'offline'
  }

  function startFallbackPolling() {
    if (fallbackTimer !== null) return
    fallbackTimer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void refresh({ includeCapabilities: false })
    }, FALLBACK_INTERVAL)
  }

  function stopFallbackPolling() {
    if (fallbackTimer === null) return
    window.clearInterval(fallbackTimer)
    fallbackTimer = null
  }

  function clear() {
    stopRealtime()
    capabilities.value = null
    summary.value = null
    inventory.value = null
    errorCode.value = null
    connectionState.value = 'loading'
  }

  return {
    connectionState,
    realtimeState,
    capabilities,
    summary,
    inventory,
    services,
    errorCode,
    isRefreshing,
    deviceName,
    lastUpdated,
    refresh,
    startRealtime,
    stopRealtime,
    clear,
  }
})

function errorCodeFor(error: unknown): string {
  return error instanceof NcpApiError ? error.code : 'NETWORK_UNAVAILABLE'
}
