import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  requestListPreference,
  requestPreferences,
  updateListPreference,
  updatePreferences,
  type ListPreference,
  type UserPreferences,
} from '@/api/control'
import {
  NcpApiError,
  requestCapabilities,
  requestDockerInventory,
  requestSystemSummary,
  subscribeSystemEvents,
  type DockerInventory,
  type RealtimeSnapshot,
  type SystemCapabilities,
  type SystemSummary,
} from '@/api/system'

export type SystemConnectionState = 'loading' | 'connected' | 'degraded' | 'unavailable'
export type RealtimeState = 'connecting' | 'streaming' | 'offline'
export type RealtimeScope = 'summary' | 'docker'

export interface RefreshOptions {
  summary?: boolean
  inventory?: boolean
  capabilities?: boolean
}

const DEFAULT_REFRESH_INTERVAL_SECONDS = 5
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
  const refreshIntervalSeconds = ref(DEFAULT_REFRESH_INTERVAL_SECONDS)
  const realtimeScopes = ref<RealtimeScope[]>([])
  const preferences = ref<UserPreferences>({
    refreshIntervalSeconds: DEFAULT_REFRESH_INTERVAL_SECONDS,
    interfaceDensity: 'comfortable',
    baseFontSize: 15,
    pageSize: 10,
    sidebarDefault: 'collapsed',
    linkOpenMode: 'new-tab',
    siteDefaultProtocol: 'http',
    chineseFont: 'system',
    latinFont: 'system',
  })
  const listPreferences = ref<Record<string, ListPreference>>({})
  const listPreferenceRequests = new Map<string, Promise<ListPreference>>()

  let eventSource: EventSource | null = null
  let refreshPromise: Promise<void> | null = null
  let previousNetwork: { timestamp: number; receiveBytes: number; transmitBytes: number } | null = null

  const deviceName = computed(() => summary.value?.host.hostname || capabilities.value?.hostname || 'NAS 管理面板')
  const lastUpdated = computed(() => summary.value?.collectedAt || inventory.value?.collectedAt || null)
  const services = computed(() => inventory.value?.projects ?? [])

  async function refresh(options: RefreshOptions = {}) {
    if (refreshPromise) return refreshPromise
    const hasExplicitScope = options.summary !== undefined || options.inventory !== undefined || options.capabilities !== undefined
    const normalized = hasExplicitScope
      ? options
      : {
          summary: realtimeScopes.value.includes('summary'),
          inventory: realtimeScopes.value.includes('docker'),
          capabilities: false,
        }
    refreshPromise = runRefresh(normalized)
    try {
      await refreshPromise
    } finally {
      refreshPromise = null
    }
  }

  async function loadPreferences() {
    const loaded = await requestPreferences()
    preferences.value = loaded
    refreshIntervalSeconds.value = loaded.refreshIntervalSeconds
    applyExperiencePreferences(loaded)
  }

  async function setRefreshInterval(seconds: number) {
    await setPreferences({ ...preferences.value, refreshIntervalSeconds: seconds })
  }

  async function setPreferences(input: UserPreferences) {
    const saved = await updatePreferences(input)
    const intervalChanged = saved.refreshIntervalSeconds !== refreshIntervalSeconds.value
    preferences.value = saved
    refreshIntervalSeconds.value = saved.refreshIntervalSeconds
    applyExperiencePreferences(saved)
    if (eventSource && intervalChanged) {
      const scopes = [...realtimeScopes.value]
      stopRealtime()
      startRealtime(scopes)
    }
    return saved
  }

  function listPreference(listKey: string): ListPreference {
    return listPreferences.value[listKey] ?? {
      listKey,
      pageSize: 10,
      sortKey: '',
      sortDirection: 'asc',
    }
  }

  async function ensureListPreference(listKey: string) {
    if (listPreferences.value[listKey]) return listPreferences.value[listKey]
    const pending = listPreferenceRequests.get(listKey)
    if (pending) return pending
    const request = requestListPreference(listKey)
      .then((loaded) => {
        listPreferences.value = { ...listPreferences.value, [listKey]: loaded }
        return loaded
      })
      .finally(() => listPreferenceRequests.delete(listKey))
    listPreferenceRequests.set(listKey, request)
    return request
  }

  async function setListPreference(listKey: string, input: Partial<Omit<ListPreference, 'listKey'>>) {
    const current = listPreference(listKey)
    const saved = await updateListPreference(listKey, {
      pageSize: input.pageSize ?? current.pageSize,
      sortKey: input.sortKey ?? current.sortKey,
      sortDirection: input.sortDirection ?? current.sortDirection,
    })
    listPreferences.value = { ...listPreferences.value, [listKey]: saved }
    return saved
  }

  function previewPreferences(input: UserPreferences) {
    applyExperiencePreferences(input)
  }

  async function runRefresh(options: RefreshOptions) {
    isRefreshing.value = true
    connectionState.value = summary.value || inventory.value ? 'degraded' : 'loading'
    const requests: Array<{
      type: 'summary' | 'inventory' | 'capabilities'
      promise: Promise<SystemCapabilities | SystemSummary | DockerInventory>
    }> = []
    if (options.summary) requests.push({ type: 'summary', promise: requestSystemSummary() })
    if (options.inventory) requests.push({ type: 'inventory', promise: requestDockerInventory() })
    if (options.capabilities) requests.push({ type: 'capabilities', promise: requestCapabilities() })
    if (!requests.length) {
      isRefreshing.value = false
      return
    }

    const results = await Promise.allSettled(requests.map((request) => request.promise))
    results.forEach((result, index) => {
      if (result.status !== 'fulfilled') return
      const type = requests[index]?.type
      if (type === 'summary') applySummary(result.value as SystemSummary)
      if (type === 'inventory') inventory.value = result.value as DockerInventory
      if (type === 'capabilities') capabilities.value = result.value as SystemCapabilities
    })

    const failures = results.filter((result): result is PromiseRejectedResult => result.status === 'rejected')
    errorCode.value = failures.length > 0 ? errorCodeFor(failures[0]?.reason) : null
    const successful = results.length - failures.length
    connectionState.value = successful === results.length ? 'connected' : successful > 0 ? 'degraded' : 'unavailable'
    isRefreshing.value = false
  }

  function startRealtime(scopes: RealtimeScope[]) {
    const normalized = [...new Set(scopes)].sort()
    const unchanged = normalized.length === realtimeScopes.value.length &&
      normalized.every((scope, index) => scope === realtimeScopes.value[index])
    if (unchanged && eventSource) return
    stopRealtime()
    realtimeScopes.value = normalized
    if (!normalized.length) return
    realtimeState.value = 'connecting'
    eventSource = subscribeSystemEvents(normalized, refreshIntervalSeconds.value, {
      open: () => { realtimeState.value = 'streaming' },
      snapshot: applyRealtimeSnapshot,
      error: () => {
        realtimeState.value = 'offline'
        connectionState.value = summary.value || inventory.value ? 'degraded' : 'unavailable'
      },
    })
  }

  function stopRealtime() {
    eventSource?.close()
    eventSource = null
    realtimeState.value = 'offline'
  }

  function applyRealtimeSnapshot(snapshot: RealtimeSnapshot) {
    if (snapshot.summary) applySummary(snapshot.summary)
    if (snapshot.docker) inventory.value = snapshot.docker
    errorCode.value = snapshot.errors?.[0] ?? null
    const expected = realtimeScopes.value.length
    const received = Number(Boolean(snapshot.summary)) + Number(Boolean(snapshot.docker))
    connectionState.value = received === expected ? 'connected' : received > 0 ? 'degraded' : 'unavailable'
    realtimeState.value = 'streaming'
  }

  function applySummary(nextSummary: SystemSummary) {
    summary.value = nextSummary
    appendResourceSample(nextSummary)
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
    realtimeScopes.value = []
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
    refreshIntervalSeconds,
    realtimeScopes,
    preferences,
    listPreferences,
    deviceName,
    lastUpdated,
    refresh,
    loadPreferences,
    setRefreshInterval,
    setPreferences,
    listPreference,
    ensureListPreference,
    setListPreference,
    previewPreferences,
    startRealtime,
    stopRealtime,
    clear,
  }
})

function applyExperiencePreferences(preferences: UserPreferences) {
  const root = document.documentElement
  root.style.fontSize = `${preferences.baseFontSize}px`
  root.style.setProperty('--ncp-base-font-size', `${preferences.baseFontSize}px`)
  root.style.setProperty('--ncp-density-scale', preferences.interfaceDensity === 'compact' ? '0.88' : '1')
  root.style.setProperty(
    '--ncp-font-ui',
    preferences.chineseFont === 'noto-sans-sc'
      ? "'Noto Sans SC', 'Microsoft YaHei UI', 'PingFang SC', sans-serif"
      : "'Microsoft YaHei UI', 'PingFang SC', 'Segoe UI Variable', sans-serif",
  )
  root.style.setProperty(
    '--ncp-font-latin',
    preferences.latinFont === 'manrope'
      ? "'Manrope Variable', 'Segoe UI Variable', sans-serif"
      : "'Segoe UI Variable', 'Microsoft YaHei UI', sans-serif",
  )
  root.dataset.density = preferences.interfaceDensity
  if (preferences.chineseFont === 'noto-sans-sc') void import('@fontsource-variable/noto-sans-sc/wght.css')
  if (preferences.latinFont === 'manrope') void import('@fontsource-variable/manrope/wght.css')
}

function errorCodeFor(error: unknown): string {
  return error instanceof NcpApiError ? error.code : 'NETWORK_UNAVAILABLE'
}
