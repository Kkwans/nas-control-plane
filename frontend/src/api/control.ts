import { NcpApiError } from '@/api/system'

export interface UserPreferences {
  refreshIntervalSeconds: number
  interfaceDensity: 'comfortable' | 'compact'
  baseFontSize: number
  pageSize: number
  sidebarDefault: 'collapsed' | 'expanded'
  linkOpenMode: 'new-tab' | 'same-tab'
  siteDefaultProtocol: 'http' | 'https'
  chineseFont: 'system' | 'noto-sans-sc'
  latinFont: 'system' | 'manrope'
}

export interface ListPreference {
  listKey: string
  pageSize: number
  sortKey: string
  sortDirection: 'asc' | 'desc'
}

export interface DatabaseProjectPreference {
  projectKey: string
  archived: boolean
}

export interface Site {
  id: string
  projectId: string
  name: string
  description: string
  iconUrl: string
  category: string
  state: 'running' | 'stopped' | 'degraded'
  primaryPort: number
  ports: number[]
  launchUrl: string
  favorite: boolean
  sortOrder: number
  lastVisitedAt: string | null
  hidden: boolean
  source: 'auto' | 'labels' | 'built-in' | 'edited' | 'manual'
}

export interface SiteListResponse {
  collectedAt: string
  sites: Site[]
}

export interface SiteProfileInput {
  name: string
  description: string
  iconUrl: string
  category: string
  primaryPort: number
  launchUrl: string
  favorite: boolean
  sortOrder: number
  hidden: boolean
}

export interface ComposeDraft {
  projectId: string
  configPath: string
  content: string
  contentHash: string
  updatedAt: string
}

export interface ComposeRevision {
  id: number
  projectId: string
  configPath: string
  content: string
  contentHash: string
  backupPath: string
  createdAt: string
}

export interface MetricSample {
  collectedAt: string
  cpuPercent: number
  memoryPercent: number
  load1: number
  diskPercent: number
  networkReceiveBytes: number
  networkTransmitBytes: number
}

export interface LogEntry {
  id: string
  timestamp: string
  source: 'system' | 'agent' | 'container'
  unit: string
  level: 'error' | 'warning' | 'info' | 'debug'
  message: string
}

export interface LogResponse {
  collectedAt: string
  entries: LogEntry[]
  nextCursor: string
}

export function requestPreferences(): Promise<UserPreferences> {
  return request('/api/v1/preferences')
}

export function updatePreferences(input: UserPreferences): Promise<UserPreferences> {
  return request('/api/v1/preferences', { method: 'PUT', body: JSON.stringify(input) })
}

export function requestListPreference(listKey: string): Promise<ListPreference> {
  return request(`/api/v1/preferences/lists/${encodeURIComponent(listKey)}`)
}

export function updateListPreference(listKey: string, input: Omit<ListPreference, 'listKey'>): Promise<ListPreference> {
  return request(`/api/v1/preferences/lists/${encodeURIComponent(listKey)}`, {
    method: 'PUT',
    body: JSON.stringify({ listKey, ...input }),
  })
}

export function requestDatabaseProjectPreferences(): Promise<DatabaseProjectPreference[]> {
  return request('/api/v1/databases/project-preferences')
}

export function updateDatabaseProjectPreference(input: DatabaseProjectPreference): Promise<DatabaseProjectPreference> {
  return request('/api/v1/databases/project-preferences', { method: 'PUT', body: JSON.stringify(input) })
}

export function requestSites(): Promise<SiteListResponse> {
  return request('/api/v1/sites')
}

export function updateSite(projectId: string, input: SiteProfileInput): Promise<SiteProfileInput & { projectId: string }> {
  return request(`/api/v1/sites/${encodeURIComponent(projectId)}`, { method: 'PUT', body: JSON.stringify(input) })
}

export function createSite(input: SiteProfileInput): Promise<SiteProfileInput & { projectId: string }> {
  return request('/api/v1/sites', { method: 'POST', body: JSON.stringify(input) })
}

export function deleteSite(siteId: string): Promise<void> {
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}`, { method: 'DELETE' })
}

export function requestIgnoredSites(): Promise<Array<SiteProfileInput & { projectId: string }>> {
  return request('/api/v1/sites/ignored')
}

export function restoreSite(siteId: string): Promise<void> {
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}/restore`, { method: 'POST' })
}

export function uploadSiteIcon(siteId: string, icon: File): Promise<{ siteId: string; iconUrl: string }> {
  const body = new FormData()
  body.append('icon', icon)
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}/icon`, { method: 'POST', body })
}

export function deleteSiteIcon(siteId: string): Promise<void> {
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}/icon`, { method: 'DELETE' })
}

export function recordSiteVisit(projectId: string): Promise<{ projectId: string; lastVisitedAt: string }> {
  return request(`/api/v1/sites/${encodeURIComponent(projectId)}/visit`, { method: 'POST' })
}

export function requestComposeDraft(projectId: string, configPath: string): Promise<ComposeDraft> {
  const parameters = new URLSearchParams({ projectId, configPath })
  return request(`/api/v1/docker/compose/drafts?${parameters}`)
}

export function saveComposeDraft(input: Pick<ComposeDraft, 'projectId' | 'configPath' | 'content'>): Promise<ComposeDraft> {
  return request('/api/v1/docker/compose/drafts', { method: 'PUT', body: JSON.stringify(input) })
}

export function requestComposeRevisions(projectId: string): Promise<ComposeRevision[]> {
  return request(`/api/v1/docker/compose/revisions?${new URLSearchParams({ projectId })}`)
}

export function requestMetricSamples(range: '1h' | '6h' | '24h' | '7d'): Promise<MetricSample[]> {
  return request(`/api/v1/monitoring/samples?${new URLSearchParams({ range })}`)
}

export function requestLogs(input: {
  source: 'system' | 'agent' | 'container'
  containerId?: string
  level?: string
  query?: string
  hours?: number
  limit?: number
}): Promise<LogResponse> {
  return request(`/api/v1/logs?${logParameters(input)}`)
}

export function followLogs(
  input: {
    source: 'system' | 'agent' | 'container'
    containerId?: string
    level?: string
    query?: string
    hours?: number
    limit?: number
  },
  intervalSeconds: number,
  onLogs: (result: LogResponse) => void,
  onError: () => void,
): EventSource {
  const parameters = logParameters(input)
  parameters.set('interval', String(intervalSeconds))
  const source = new EventSource(`/api/v1/logs/events?${parameters}`)
  source.addEventListener('logs', (event) => {
    try {
      const payload = JSON.parse((event as MessageEvent<string>).data) as LogResponse
      if (!Array.isArray(payload.entries) || typeof payload.collectedAt !== 'string') throw new Error('invalid log stream')
      onLogs(payload)
    } catch {
      source.close()
      onError()
    }
  })
  source.addEventListener('unavailable', onError)
  source.onerror = onError
  return source
}

function logParameters(input: {
  source: 'system' | 'agent' | 'container'
  containerId?: string
  level?: string
  query?: string
  hours?: number
  limit?: number
}) {
  const parameters = new URLSearchParams({
    source: input.source,
    level: input.level || 'all',
    query: input.query || '',
    hours: String(input.hours || 6),
    limit: String(input.limit || 150),
  })
  if (input.containerId) parameters.set('containerId', input.containerId)
  return parameters
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const isFormData = init.body instanceof FormData
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...(init.body && !isFormData ? { 'Content-Type': 'application/json' } : {}),
      ...init.headers,
    },
  })
  if (!response.ok) {
    try {
      const payload = await response.json() as { code?: string; message?: string; requestId?: string }
      throw new NcpApiError(payload.code || 'CONTROL_REQUEST_FAILED', payload.message || '控制面请求失败。', payload.requestId)
    } catch (error) {
      if (error instanceof NcpApiError) throw error
      throw new NcpApiError('CONTROL_REQUEST_FAILED', '控制面请求失败。')
    }
  }
  if (response.status === 204) return undefined as T
  try {
    return await response.json() as T
  } catch {
    throw new NcpApiError('CONTROL_RESPONSE_INVALID', '控制面返回了无法识别的数据。')
  }
}
