import { NcpApiError } from '@/api/system'

export interface UserPreferences {
  refreshIntervalSeconds: number
  interfaceDensity: 'comfortable' | 'compact'
  baseFontSize: number
  pageSize: 20 | 25 | 50 | 100
  sidebarDefault: 'collapsed' | 'expanded'
  linkOpenMode: 'new-tab' | 'same-tab'
  siteDefaultProtocol: 'http' | 'https'
  chineseFont: 'system' | 'noto-sans-sc'
  latinFont: 'system' | 'manrope'
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
  source: 'auto' | 'labels' | 'built-in' | 'edited'
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
  const parameters = new URLSearchParams({
    source: input.source,
    level: input.level || 'all',
    query: input.query || '',
    hours: String(input.hours || 6),
    limit: String(input.limit || 150),
  })
  if (input.containerId) parameters.set('containerId', input.containerId)
  return request(`/api/v1/logs?${parameters}`)
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
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
  try {
    return await response.json() as T
  } catch {
    throw new NcpApiError('CONTROL_RESPONSE_INVALID', '控制面返回了无法识别的数据。')
  }
}
