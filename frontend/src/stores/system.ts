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
import { captureSession, isAbortError, isCurrentSession } from '@/session/sessionLifecycle'

export type SystemConnectionState = 'loading' | 'connected' | 'degraded' | 'unavailable'
export type RealtimeState = 'connecting' | 'streaming' | 'offline'
export type RealtimeScope = 'summary' | 'docker'

export interface RefreshOptions {
  summary?: boolean
  inventory?: boolean
  capabilities?: boolean
}

export type RefreshResource = 'summary' | 'inventory' | 'capabilities'

export interface RefreshFailure {
  resource: RefreshResource
  error: unknown
}

export interface RefreshResult {
  requested: RefreshResource[]
  succeeded: RefreshResource[]
  failed: RefreshFailure[]
}

export class RefreshFailureError extends Error {
  readonly result: RefreshResult

  constructor(result: RefreshResult) {
    super('一个或多个系统资源刷新失败。')
    this.name = 'RefreshFailureError'
    this.result = result
  }
}

const DEFAULT_REFRESH_INTERVAL_SECONDS = 5
const MAX_HISTORY_POINTS = 60

export const DEFAULT_USER_PREFERENCES: UserPreferences = {
  refreshIntervalSeconds: DEFAULT_REFRESH_INTERVAL_SECONDS,
  interfaceDensity: 'comfortable',
  baseFontSize: 15,
  pageSize: 10,
  sidebarDefault: 'collapsed',
  linkOpenMode: 'new-tab',
  siteDefaultProtocol: 'http',
  chineseFont: 'system',
  latinFont: 'system',
  navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'],
}

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
  const preferences = ref<UserPreferences>({ ...DEFAULT_USER_PREFERENCES, navigationOrder: [...DEFAULT_USER_PREFERENCES.navigationOrder] })
  const listPreferences = ref<Record<string, ListPreference>>({})
  const listPreferenceRequests = new Map<string, Promise<ListPreference>>()

  let eventSource: EventSource | null = null
  type RefreshOutcome = { resource: RefreshResource; error?: unknown; stale?: boolean; failed?: boolean }
  const refreshPromises = new Map<RefreshResource, Promise<RefreshOutcome>>()
  const pendingRefreshes = new Map<RefreshResource, number>()
  const refreshErrors = new Map<RefreshResource, string>()
  const realtimeErrorCode = ref<string | null>(null)
  let realtimeSequence = 0
  let previousNetwork: { timestamp: number; receiveBytes: number; transmitBytes: number } | null = null

  const deviceName = computed(() => summary.value?.host.hostname || capabilities.value?.hostname || 'NAS 管理面板')
  const lastUpdated = computed(() => summary.value?.collectedAt || inventory.value?.collectedAt || null)
  const services = computed(() => inventory.value?.projects ?? [])

  async function refresh(options: RefreshOptions = {}): Promise<RefreshResult> {
    const hasExplicitScope = options.summary !== undefined || options.inventory !== undefined || options.capabilities !== undefined
    const normalized = hasExplicitScope
      ? options
      : {
          summary: realtimeScopes.value.includes('summary'),
          inventory: realtimeScopes.value.includes('docker'),
          capabilities: false,
        }
    const requested = (['summary', 'inventory', 'capabilities'] as RefreshResource[])
      .filter((resource) => normalized[resource])
    const outcomes = await Promise.all(requested.map((resource) => refreshResource(resource)))
    const failed = outcomes
      .filter((outcome): outcome is RefreshOutcome & { failed: true } => outcome.failed === true && !outcome.stale)
      .map(({ resource, error }) => ({ resource, error }))
    const outcomeByResource = new Map(outcomes.map((outcome) => [outcome.resource, outcome]))
    return {
      requested,
      succeeded: requested.filter((resource) => {
        const outcome = outcomeByResource.get(resource)
        return Boolean(outcome && !outcome.stale && !outcome.failed)
      }),
      failed,
    }
  }

  async function refreshStrict(options: RefreshOptions = {}) {
    const result = await refresh(options)
    if (result.failed.length > 0) throw new RefreshFailureError(result)
    return result
  }

  async function loadPreferences() {
    const session = captureSession()
    const loaded = await requestPreferences(session.signal)
    if (!isCurrentSession(session.generation)) return
    preferences.value = loaded
    refreshIntervalSeconds.value = loaded.refreshIntervalSeconds
    applyExperiencePreferences(loaded)
  }

  async function setRefreshInterval(seconds: number) {
    await setPreferences({ ...preferences.value, refreshIntervalSeconds: seconds })
  }

  async function setPreferences(input: UserPreferences) {
    const session = captureSession()
    const saved = await updatePreferences(input, session.signal)
    if (!isCurrentSession(session.generation)) return saved
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
    const session = captureSession()
    const request = requestListPreference(listKey, session.signal)
      .then((loaded) => {
        if (!isCurrentSession(session.generation)) return loaded
        listPreferences.value = { ...listPreferences.value, [listKey]: loaded }
        return loaded
      })
      .finally(() => {
        if (listPreferenceRequests.get(listKey) === request) listPreferenceRequests.delete(listKey)
      })
    listPreferenceRequests.set(listKey, request)
    return request
  }

  async function setListPreference(listKey: string, input: Partial<Omit<ListPreference, 'listKey'>>) {
    const current = listPreference(listKey)
    const session = captureSession()
    const saved = await updateListPreference(listKey, {
      pageSize: input.pageSize ?? current.pageSize,
      sortKey: input.sortKey ?? current.sortKey,
      sortDirection: input.sortDirection ?? current.sortDirection,
    }, session.signal)
    if (!isCurrentSession(session.generation)) return saved
    listPreferences.value = { ...listPreferences.value, [listKey]: saved }
    return saved
  }

  function previewPreferences(input: UserPreferences) {
    applyExperiencePreferences(input)
  }

  function refreshResource(resource: RefreshResource) {
    const pending = refreshPromises.get(resource)
    if (pending) return pending

    const request = runRefreshResource(resource)
    const promise = request.finally(() => {
      if (refreshPromises.get(resource) === promise) refreshPromises.delete(resource)
    })
    refreshPromises.set(resource, promise)
    return promise
  }

  async function runRefreshResource(resource: RefreshResource) {
    const session = captureSession()
    pendingRefreshes.set(resource, session.generation)
    updateRefreshState()
    try {
      if (resource === 'summary') {
        const next = await requestSystemSummary(fetch, session.signal)
        if (isCurrentSession(session.generation)) applySummary(next)
      }
      if (resource === 'inventory') {
        const next = await requestDockerInventory(fetch, session.signal)
        if (isCurrentSession(session.generation)) inventory.value = next
      }
      if (resource === 'capabilities') {
        const next = await requestCapabilities(fetch, session.signal)
        if (isCurrentSession(session.generation)) capabilities.value = next
      }
      if (!isCurrentSession(session.generation)) return { resource, stale: true }
      refreshErrors.delete(resource)
      return { resource }
    } catch (error) {
      if (!isCurrentSession(session.generation) || isAbortError(error)) return { resource, stale: true }
      refreshErrors.set(resource, errorCodeFor(error))
      return { resource, error, failed: true }
    } finally {
      if (pendingRefreshes.get(resource) === session.generation) {
        pendingRefreshes.delete(resource)
        if (isCurrentSession(session.generation)) updateRefreshState()
      }
    }
  }

  function updateRefreshState() {
    isRefreshing.value = pendingRefreshes.size > 0
    const dataAvailable = Boolean(summary.value || inventory.value || capabilities.value)
    const firstError = refreshErrors.values().next().value as string | undefined
    errorCode.value = firstError ?? realtimeErrorCode.value
    if (pendingRefreshes.size > 0) {
      connectionState.value = dataAvailable ? 'degraded' : 'loading'
    } else if (firstError) {
      connectionState.value = dataAvailable ? 'degraded' : 'unavailable'
    } else if (dataAvailable) {
      connectionState.value = 'connected'
    }
  }

  function startRealtime(scopes: RealtimeScope[]) {
    const normalized = [...new Set(scopes)].sort()
    const unchanged = normalized.length === realtimeScopes.value.length &&
      normalized.every((scope, index) => scope === realtimeScopes.value[index])
    if (unchanged && eventSource) return
    stopRealtime()
    realtimeErrorCode.value = null
    realtimeScopes.value = normalized
    if (!normalized.length) return
    const session = captureSession()
    const streamSequence = ++realtimeSequence
    realtimeState.value = 'connecting'
    eventSource = subscribeSystemEvents(normalized, refreshIntervalSeconds.value, {
      open: () => {
        if (isCurrentSession(session.generation) && realtimeSequence === streamSequence) realtimeState.value = 'streaming'
      },
      snapshot: (snapshot) => {
        if (isCurrentSession(session.generation) && realtimeSequence === streamSequence) applyRealtimeSnapshot(snapshot)
      },
      error: () => {
        if (!isCurrentSession(session.generation) || realtimeSequence !== streamSequence) return
        realtimeState.value = 'offline'
        realtimeErrorCode.value = 'REALTIME_STREAM_FAILED'
        updateRefreshState()
      },
    })
  }

  function stopRealtime() {
    realtimeSequence += 1
    eventSource?.close()
    eventSource = null
    realtimeState.value = 'offline'
  }

  function applyRealtimeSnapshot(snapshot: RealtimeSnapshot) {
    if (snapshot.summary) applySummary(snapshot.summary)
    if (snapshot.docker) inventory.value = snapshot.docker
    realtimeErrorCode.value = snapshot.errors?.[0] ?? null
    const expected = realtimeScopes.value.length
    const received = Number(Boolean(snapshot.summary)) + Number(Boolean(snapshot.docker))
    updateRefreshState()
    if (!refreshErrors.size && !realtimeErrorCode.value) {
      connectionState.value = received === expected ? 'connected' : received > 0 ? 'degraded' : 'unavailable'
    }
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
    realtimeErrorCode.value = null
    isRefreshing.value = false
    connectionState.value = 'loading'
    resourceHistory.value = []
    previousNetwork = null
    realtimeScopes.value = []
    refreshPromises.clear()
    pendingRefreshes.clear()
    refreshErrors.clear()
    listPreferenceRequests.clear()
    preferences.value = { ...DEFAULT_USER_PREFERENCES, navigationOrder: [...DEFAULT_USER_PREFERENCES.navigationOrder] }
    refreshIntervalSeconds.value = DEFAULT_REFRESH_INTERVAL_SECONDS
    listPreferences.value = {}
    applyExperiencePreferences(preferences.value)
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
    refreshStrict,
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
  const chineseFonts: Record<UserPreferences['chineseFont'], string> = {
    system: "'Microsoft YaHei UI', 'PingFang SC', 'Segoe UI Variable', sans-serif",
    'noto-sans-sc': "'Noto Sans SC', 'Microsoft YaHei UI', 'PingFang SC', sans-serif",
    'microsoft-yahei': "'Microsoft YaHei UI', 'Microsoft YaHei', sans-serif",
    'source-han-sans-sc': "'Source Han Sans SC', 'Noto Sans CJK SC', 'Microsoft YaHei UI', sans-serif",
    misans: "'MiSans', 'Microsoft YaHei UI', sans-serif",
    'harmonyos-sans-sc': "'HarmonyOS Sans SC', 'Microsoft YaHei UI', sans-serif",
  }
  const latinFonts: Record<UserPreferences['latinFont'], string> = {
    system: "'Segoe UI Variable', 'Microsoft YaHei UI', sans-serif",
    manrope: "'Manrope Variable', 'Segoe UI Variable', sans-serif",
    inter: "'Inter Variable', 'Segoe UI Variable', sans-serif",
    'ibm-plex-sans': "'IBM Plex Sans Variable', 'Segoe UI Variable', sans-serif",
  }
  root.style.fontSize = `${preferences.baseFontSize}px`
  root.style.setProperty('--ncp-base-font-size', `${preferences.baseFontSize}px`)
  root.style.setProperty('--ncp-density-scale', preferences.interfaceDensity === 'compact' ? '0.8' : '1')
  root.style.setProperty('--ncp-font-ui', chineseFonts[preferences.chineseFont])
  root.style.setProperty('--ncp-font-latin', latinFonts[preferences.latinFont])
  root.style.setProperty('--ncp-font-body', `${latinFonts[preferences.latinFont]}, ${chineseFonts[preferences.chineseFont]}`)
  root.dataset.density = preferences.interfaceDensity
  if (preferences.chineseFont === 'noto-sans-sc') void import('@fontsource-variable/noto-sans-sc/wght.css')
  if (preferences.latinFont === 'manrope') void import('@fontsource-variable/manrope/wght.css')
  if (preferences.latinFont === 'inter') void import('@fontsource-variable/inter/wght.css')
  if (preferences.latinFont === 'ibm-plex-sans') void import('@fontsource-variable/ibm-plex-sans/wght.css')
}

function errorCodeFor(error: unknown): string {
  return error instanceof NcpApiError ? error.code : 'NETWORK_UNAVAILABLE'
}
