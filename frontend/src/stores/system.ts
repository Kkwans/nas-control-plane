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
const MAX_HISTORY_POINTS = 60

export interface ResourceSample {
  timestamp: string
  cpuPercent: number
  memoryPercent: number
  load1: number
  networkReceiveBps: number
  networkTransmitBps: number
}

export const useSystemStore = defineStore('system', () => {
  const connectionState = ref<SystemConnectionState>('loading')
  const realtimeState = ref<RealtimeState>('offline')
  const capabilities = ref<SystemCapabilities | null>(null)
  const summary = ref<SystemSummary | null>(null)
  const inventory = ref<DockerInventory | null>(null)
  const errorCode = ref<string | null>(null)
  const isRefreshing = ref(false)
  const resourceHistory = ref<ResourceSample[]>([])

  let eventSource: EventSource | null = null
  let fallbackTimer: number | null = null
  let refreshPromise: Promise<void> | null = null
  let previousNetwork: { timestamp: number; receiveBytes: number; transmitBytes: number } | null = null

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

    if (summaryResult?.status === 'fulfilled') {
      const nextSummary = summaryResult.value as SystemSummary
      summary.value = nextSummary
      appendResourceSample(nextSummary)
    }
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
    resourceHistory.value = []
    previousNetwork = null
  }

  function appendResourceSample(nextSummary: SystemSummary) {
    if (resourceHistory.value.at(-1)?.timestamp === nextSummary.collectedAt) return

    const timestamp = new Date(nextSummary.collectedAt).valueOf()
    const validTimestamp = Number.isFinite(timestamp) ? timestamp : Date.now()
    const receiveBytes = nextSummary.network.reduce((total, item) => total + item.receiveBytes, 0)
    const transmitBytes = nextSummary.network.reduce((total, item) => total + item.transmitBytes, 0)
    const elapsedSeconds = previousNetwork ? Math.max((validTimestamp - previousNetwork.timestamp) / 1000, 0) : 0
    const receiveRate = previousNetwork && elapsedSeconds > 0 ? Math.max((receiveBytes - previousNetwork.receiveBytes) / elapsedSeconds, 0) : 0
    const transmitRate = previousNetwork && elapsedSeconds > 0 ? Math.max((transmitBytes - previousNetwork.transmitBytes) / elapsedSeconds, 0) : 0

    const sample: ResourceSample = {
      timestamp: new Date(validTimestamp).toISOString(),
      cpuPercent: nextSummary.cpu.usagePercent,
      memoryPercent: nextSummary.memory.totalBytes > 0 ? (nextSummary.memory.usedBytes / nextSummary.memory.totalBytes) * 100 : 0,
      load1: nextSummary.cpu.load1,
      networkReceiveBps: receiveRate,
      networkTransmitBps: transmitRate,
    }
    resourceHistory.value = [...resourceHistory.value, sample].slice(-MAX_HISTORY_POINTS)
    previousNetwork = { timestamp: validTimestamp, receiveBytes, transmitBytes }
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
    resourceHistory,
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
