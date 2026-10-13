import { computed, onScopeDispose, ref } from 'vue'
import { createPreferencesState } from './preferences'
import { defineStore } from 'pinia'

import {
  requestListPreference,
  updateListPreference,
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
import { SNAPSHOT_FRESHNESS_MS, snapshotErrorMessage, snapshotTimestamp, summaryHasUnavailableSources, summarySourceAvailable } from '@/domain/systemHealth'
import { captureSession, isAbortError, isCurrentSession } from '@/session/sessionLifecycle'

export type SystemConnectionState = 'loading' | 'connected' | 'degraded' | 'unavailable'
export type RealtimeState = 'connecting' | 'streaming' | 'offline'
export type RealtimeScope = 'summary' | 'docker'

export interface RefreshOptions {
  summary?: boolean
  inventory?: boolean
  capabilities?: boolean
  // 操作完成后必须新发请求，不能复用操作前已开始的读取。
  force?: boolean
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
  const inventoryRefreshing = ref(false)
  const inventoryErrorCode = ref<string | null>(null)
  const inventoryStale = ref(false)
  const errorCode = ref<string | null>(null)
  const errorCodes = ref<string[]>([])
  const isRefreshing = ref(false)
  const resourceHistory = ref<ResourceSample[]>([])
  const refreshIntervalSeconds = ref(DEFAULT_REFRESH_INTERVAL_SECONDS)
  const realtimeScopes = ref<RealtimeScope[]>([])
  const preferenceState = createPreferencesState(DEFAULT_USER_PREFERENCES, applyExperiencePreferences, (saved) => {
    const intervalChanged = saved.refreshIntervalSeconds !== refreshIntervalSeconds.value
    refreshIntervalSeconds.value = saved.refreshIntervalSeconds
    if (eventSource && intervalChanged) {
      const scopes = [...realtimeScopes.value]
      stopRealtime()
      startRealtime(scopes)
    }
  })
  const listPreferences = ref<Record<string, ListPreference>>({})
  const listPreferenceRequests = new Map<string, Promise<ListPreference>>()

  let eventSource: EventSource | null = null
  type RefreshOutcome = { resource: RefreshResource; error?: unknown; stale?: boolean; failed?: boolean }
  const refreshPromises = new Map<RefreshResource, Promise<RefreshOutcome>>()
  const pendingRefreshes = new Map<RefreshResource, number>()
  let refreshSequence = 0
  const refreshErrors = new Map<RefreshResource, string>()
  const realtimeErrors = new Map<RefreshResource, string>()
  const observedResources = new Set<RefreshResource>()
  const realtimeErrorCode = ref<string | null>(null)
  const snapshotDiagnostics = ref<string[]>([])
  const summaryStale = ref(false)
  type Freshness = { timestamp: number; receivedAt: number; ageAtReceipt: number }
  const freshness = new Map<RefreshResource, Freshness>()
  let freshnessTimer: ReturnType<typeof setInterval> | null = null
  let stopSessionListener: (() => void) | null = null
  let realtimeSequence = 0
  let storeSequence = 0
  let previousNetwork: { timestamp: number; receiveBytes: number; transmitBytes: number } | null = null

  const deviceName = computed(() => summary.value?.host.hostname || capabilities.value?.hostname || 'NAS 管理面板')
  const lastUpdated = computed(() => summary.value?.collectedAt || inventory.value?.collectedAt || null)
  const connectionMessage = computed(() => snapshotErrorMessage(errorCode.value))
  const connectionLabel = computed(() => connectionState.value === 'unavailable' ? '数据暂不可用' : connectionMessage.value?.label ?? (connectionState.value === 'connected' ? '数据已同步' : '等待实时数据'))
  const connectionDetail = computed(() => errorCodes.value.length ? [...new Set(errorCodes.value.map((code) => snapshotErrorMessage(code)?.detail))].join(' ') : (connectionState.value === 'connected' ? '系统快照与所需资源均已同步。' : '正在等待本页面所需的实时快照。'))
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
    const outcomes = await Promise.all(requested.map((resource) => refreshResource(resource, options.force === true)))
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

  function refreshResource(resource: RefreshResource, force: boolean) {
    const pending = refreshPromises.get(resource)
    if (pending && !force) return pending

    const request = runRefreshResource(resource, force)
    const promise = request.finally(() => {
      if (refreshPromises.get(resource) === promise) refreshPromises.delete(resource)
    })
    refreshPromises.set(resource, promise)
    return promise
  }

  async function runRefreshResource(resource: RefreshResource, force: boolean) {
    const session = captureSession()
    const sequence = storeSequence
    const requestSequence = ++refreshSequence
    const currentSession = () => isCurrentSession(session.generation) && sequence === storeSequence
    const current = () => currentSession() && pendingRefreshes.get(resource) === requestSequence
    const initialSample = freshness.get(resource)
    observedResources.add(resource)
    pendingRefreshes.set(resource, requestSequence)
    updateRefreshState()
    try {
      if (resource === 'summary') {
        const next = await requestSystemSummary(fetch, session.signal)
        if (current()) acceptResource('summary', next)
        else if (force && currentSession()) validateResourceTimestamp('summary', next)
      }
      if (resource === 'inventory') {
        const next = await requestDockerInventory(fetch, session.signal)
        if (current()) acceptResource('inventory', next)
        else if (force && currentSession()) validateResourceTimestamp('inventory', next)
      }
      if (resource === 'capabilities') {
        const next = await requestCapabilities(fetch, session.signal)
        if (current()) capabilities.value = next
      }
      if (!currentSession() || (!current() && !force)) return { resource, stale: true }
      if (current() && resource === 'capabilities') refreshErrors.delete(resource)
      return { resource }
    } catch (error) {
      if (!currentSession() || isAbortError(error)) return { resource, stale: true }
      // 被后一次强刷替代仅撤销写入权，不能吞掉本次操作调用方需要的失败结果。
      if (!current()) return force ? { resource, error, failed: true } : { resource, stale: true }
      // SSE 的较新快照不证明操作后强刷成功；仍需保留强刷自身的失败结果。
      if (!force && freshness.get(resource) !== initialSample && isFresh(resource)) return { resource, stale: true }
      refreshErrors.set(resource, errorCodeFor(error))
      return { resource, error, failed: true }
    } finally {
      if (current()) {
        pendingRefreshes.delete(resource)
        updateRefreshState()
      }
    }
  }

  function resourceData(resource: RefreshResource) {
    return resource === 'summary' ? summary.value : resource === 'inventory' ? inventory.value : capabilities.value
  }

  function isFresh(resource: RefreshResource) {
    if (resource === 'capabilities') return Boolean(capabilities.value)
    const sample = freshness.get(resource)
    if (!sample) return false
    // 同一采集时间不会重置接收基准；本地时钟回拨也不能延长缓存寿命。
    const age = Math.max(Date.now() - sample.timestamp, sample.ageAtReceipt + performance.now() - sample.receivedAt)
    return age <= SNAPSHOT_FRESHNESS_MS
  }

  function updateRefreshState() {
    isRefreshing.value = pendingRefreshes.size > 0
    // Docker 首次读取与恢复只依据自身资源，不能被其他页面留下的能力数据掩盖。
    inventoryRefreshing.value = pendingRefreshes.has('inventory')
    inventoryErrorCode.value = refreshErrors.get('inventory') ?? realtimeErrors.get('inventory') ?? null
    inventoryStale.value = Boolean(inventory.value) && !isFresh('inventory')
    summaryStale.value = Boolean(summary.value) && !isFresh('summary')
    const expected: RefreshResource[] = realtimeScopes.value.length
      ? realtimeScopes.value.map((scope) => scope === 'docker' ? 'inventory' : scope)
      : [...observedResources]
    const dataAvailable = expected.some((resource) => Boolean(resourceData(resource)))
    const complete = expected.length > 0 && expected.every((resource) => Boolean(resourceData(resource)))
    const resourceErrors = expected.flatMap((resource) => [refreshErrors.get(resource), realtimeErrors.get(resource)]).filter((code): code is string => Boolean(code))
    const staleResource = expected.find((resource) => resourceData(resource) && !isFresh(resource))
    const staleCode = staleResource === 'summary' ? 'SYSTEM_SUMMARY_STALE' : staleResource === 'inventory' ? 'DOCKER_INVENTORY_STALE' : null
    const partialSummary = expected.includes('summary') && summary.value && summaryHasUnavailableSources(summary.value)
    errorCodes.value = [...new Set([...resourceErrors, staleCode, realtimeErrorCode.value, ...(expected.includes('summary') ? snapshotDiagnostics.value : []), partialSummary ? 'SYSTEM_SUMMARY_PARTIAL' : null].filter((code): code is string => Boolean(code)))]
    errorCode.value = errorCodes.value[0] ?? null
    if (errorCode.value) connectionState.value = dataAvailable ? 'degraded' : 'unavailable'
    else if (complete) connectionState.value = 'connected'
    else if (dataAvailable) connectionState.value = 'degraded'
    else connectionState.value = pendingRefreshes.size > 0 || realtimeState.value === 'connecting' ? 'loading' : 'unavailable'
  }

  function startFreshnessTimer(session: ReturnType<typeof captureSession>) {
    const update = () => {
      if (!isCurrentSession(session.generation)) return
      updateRefreshState()
    }
    freshnessTimer = setInterval(update, 1000)
    const visible = () => { if (document.visibilityState === 'visible') update() }
    document.addEventListener('visibilitychange', visible)
    const abort = () => stopRealtime()
    session.signal.addEventListener('abort', abort, { once: true })
    stopSessionListener = () => {
      document.removeEventListener('visibilitychange', visible)
      session.signal.removeEventListener('abort', abort)
    }
    update()
  }

  function startRealtime(scopes: RealtimeScope[]) {
    const normalized = [...new Set(scopes)].sort()
    const unchanged = normalized.length === realtimeScopes.value.length &&
      normalized.every((scope, index) => scope === realtimeScopes.value[index])
    if (unchanged && (eventSource || (!normalized.length && freshnessTimer !== null))) {
      updateRefreshState()
      return
    }
    stopRealtime()
    realtimeScopes.value = normalized
    const session = captureSession()
    // 无推送作用域的页面也需要让已读取快照自然过期；此时仅运行本地时钟，不轮询或订阅。
    realtimeState.value = normalized.length ? 'connecting' : 'offline'
    startFreshnessTimer(session)
    if (!normalized.length) return
    const streamSequence = ++realtimeSequence
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
    if (freshnessTimer !== null) clearInterval(freshnessTimer)
    freshnessTimer = null
    stopSessionListener?.()
    stopSessionListener = null
    realtimeSequence += 1
    eventSource?.close()
    eventSource = null
    realtimeState.value = 'offline'
  }

  function retryClosedRealtime(): boolean {
    // 仅由用户刷新触发；CONNECTING 交给浏览器重连，停止或会话失效后不复活旧流。
    if (document.visibilityState !== 'visible' || !eventSource ||
      eventSource.readyState !== EventSource.CLOSED || !realtimeScopes.value.length) return false
    const scopes = [...realtimeScopes.value]
    stopRealtime()
    startRealtime(scopes)
    return true
  }

  function applyRealtimeSnapshot(snapshot: RealtimeSnapshot) {
    const errors = snapshot.errors ?? []
    let receivedFreshData = false
    for (const scope of realtimeScopes.value) {
      const resource = scope === 'docker' ? 'inventory' : 'summary'
      const data = scope === 'summary' ? snapshot.summary : snapshot.docker
      const failure = errors.find((code) => scope === 'summary' ? code.startsWith('SYSTEM_SUMMARY_') : code.startsWith('DOCKER_'))
      if (failure || !data) {
        realtimeErrors.set(resource, failure ?? (scope === 'summary' ? 'SYSTEM_SUMMARY_UNAVAILABLE' : 'DOCKER_INVENTORY_UNAVAILABLE'))
        if (resource === 'summary') previousNetwork = null
      } else {
        try {
          receivedFreshData = acceptResource(resource, data) || receivedFreshData
        } catch {
          realtimeErrors.set(resource, scope === 'summary' ? 'SYSTEM_SUMMARY_RESPONSE_INVALID' : 'DOCKER_INVENTORY_RESPONSE_INVALID')
        }
      }
    }
    if (realtimeScopes.value.includes('summary')) snapshotDiagnostics.value = errors.filter((code) => !code.startsWith('SYSTEM_SUMMARY_') && !code.startsWith('DOCKER_'))
    // open、心跳、外层时间或重复数据都不能清除断流错误。
    if (receivedFreshData) realtimeErrorCode.value = null
    realtimeState.value = 'streaming'
    updateRefreshState()
  }

  function validateResourceTimestamp(resource: 'summary' | 'inventory', data: SystemSummary | DockerInventory) {
    const timestamp = snapshotTimestamp(data.collectedAt)
    if (timestamp === null) throw new NcpApiError(resource === 'summary' ? 'SYSTEM_SUMMARY_RESPONSE_INVALID' : 'DOCKER_INVENTORY_RESPONSE_INVALID', '快照采集时间无效。')
    return timestamp
  }

  function acceptResource(resource: 'summary' | 'inventory', data: SystemSummary | DockerInventory): boolean {
    const timestamp = validateResourceTimestamp(resource, data)
    const previous = freshness.get(resource)
    if (previous && timestamp <= previous.timestamp) return false
    freshness.set(resource, { timestamp, receivedAt: performance.now(), ageAtReceipt: Math.max(Date.now() - timestamp, 0) })
    if (resource === 'summary') summary.value = data as SystemSummary
    else inventory.value = data as DockerInventory
    if (!isFresh(resource)) return false
    refreshErrors.delete(resource)
    realtimeErrors.delete(resource)
    if (resource === 'summary') appendResourceSample(data as SystemSummary)
    return true
  }

  function clear() {
    storeSequence += 1
    stopRealtime()
    capabilities.value = null
    summary.value = null
    inventory.value = null
    inventoryRefreshing.value = false
    inventoryErrorCode.value = null
    inventoryStale.value = false
    errorCode.value = null
    errorCodes.value = []
    realtimeErrorCode.value = null
    snapshotDiagnostics.value = []
    summaryStale.value = false
    freshness.clear()
    observedResources.clear()
    realtimeErrors.clear()
    isRefreshing.value = false
    connectionState.value = 'loading'
    resourceHistory.value = []
    previousNetwork = null
    realtimeScopes.value = []
    refreshPromises.clear()
    pendingRefreshes.clear()
    refreshErrors.clear()
    listPreferenceRequests.clear()
    preferenceState.reset()
    refreshIntervalSeconds.value = DEFAULT_REFRESH_INTERVAL_SECONDS
    listPreferences.value = {}
  }

  function appendResourceSample(nextSummary: SystemSummary) {
    if (resourceHistory.value.at(-1)?.timestamp === nextSummary.collectedAt) return

    const timestamp = snapshotTimestamp(nextSummary.collectedAt)
    if (timestamp === null) return
    const networkAvailable = summarySourceAvailable(nextSummary, 'network')
    const receiveBytes = nextSummary.network.reduce((total, item) => total + item.receiveBytes, 0)
    const transmitBytes = nextSummary.network.reduce((total, item) => total + item.transmitBytes, 0)
    const elapsed = previousNetwork ? timestamp - previousNetwork.timestamp : 0
    const continuous = networkAvailable && elapsed > 0 && elapsed <= SNAPSHOT_FRESHNESS_MS
    const receiveRate = continuous && previousNetwork && receiveBytes >= previousNetwork.receiveBytes ? (receiveBytes - previousNetwork.receiveBytes) / (elapsed / 1000) : Number.NaN
    const transmitRate = continuous && previousNetwork && transmitBytes >= previousNetwork.transmitBytes ? (transmitBytes - previousNetwork.transmitBytes) / (elapsed / 1000) : Number.NaN
    const cpuAvailable = summarySourceAvailable(nextSummary, 'cpu')
    const memoryAvailable = summarySourceAvailable(nextSummary, 'memory')
    const sample: ResourceSample = {
      timestamp: nextSummary.collectedAt,
      cpuPercent: cpuAvailable ? nextSummary.cpu.usagePercent : Number.NaN,
      memoryPercent: memoryAvailable ? (nextSummary.memory.usedBytes / nextSummary.memory.totalBytes) * 100 : Number.NaN,
      load1: cpuAvailable ? nextSummary.cpu.load1 : Number.NaN,
      networkReceiveBps: receiveRate,
      networkTransmitBps: transmitRate,
    }
    resourceHistory.value = [...resourceHistory.value, sample].slice(-MAX_HISTORY_POINTS)
    previousNetwork = networkAvailable ? { timestamp, receiveBytes, transmitBytes } : null
  }

  onScopeDispose(() => { stopRealtime(); preferenceState.dispose() })

  return {
    connectionState,
    realtimeState,
    connectionLabel,
    connectionDetail,
    summaryStale,
    capabilities,
    summary,
    inventory,
    inventoryRefreshing,
    inventoryErrorCode,
    inventoryStale,
    services,
    errorCode,
    errorCodes,
    isRefreshing,
    resourceHistory,
    refreshIntervalSeconds,
    realtimeScopes,
    ...preferenceState,
    listPreferences,
    deviceName,
    lastUpdated,
    refresh,
    refreshStrict,
    listPreference,
    ensureListPreference,
    setListPreference,
    startRealtime,
    stopRealtime,
    retryClosedRealtime,
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
