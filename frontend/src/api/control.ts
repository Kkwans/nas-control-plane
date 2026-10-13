import { parseResourceAlerts, type ResourceAlerts } from '@/domain/monitoring/alerts'
import { parsePreferencesResponse } from './preferenceResponse'
import { requestSiteProfileWrite } from './siteWriteOutcome'
import { requestUserWrite } from './userWriteOutcome'
import { NcpApiError } from '@/api/system'
import { parseIgnoredSitesResponse, parseSiteListResponse, parseSiteProfileResponse } from './siteResponse'
import { normalizeManagedUsername, parseManagedUserResponse, parsePasswordPolicyResponse, parseUserListResponse } from './userResponse'

export interface UserPreferences {
  refreshIntervalSeconds: number
  interfaceDensity: 'comfortable' | 'compact'
  baseFontSize: number
  pageSize: number
  sidebarDefault: 'collapsed' | 'expanded'
  linkOpenMode: 'new-tab' | 'same-tab'
  siteDefaultProtocol: 'http' | 'https'
  chineseFont: 'system' | 'noto-sans-sc' | 'microsoft-yahei' | 'source-han-sans-sc' | 'misans' | 'harmonyos-sans-sc'
  latinFont: 'system' | 'manrope' | 'inter' | 'ibm-plex-sans'
  navigationOrder: string[]
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
  state: string
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
  discovery: SiteDiscoverySummary
}

export interface SiteDiscoverySummary {
  status: 'complete' | 'partial' | 'unavailable'
  probeAvailable: boolean
  candidateCount: number
  verifiedCount: number
  failedCount: number
  issues: SiteDiscoveryIssue[]
}

export interface SiteDiscoveryIssue {
  siteId: string
  projectId: string
  name: string
  ports: number[]
  reason: string
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

export interface SiteProfileResponse extends SiteProfileInput {
  projectId: string
  source?: Site['source']
  ignored?: boolean
  lastVisitedAt?: string | null
  detectedTitle?: string
  autoIconUrl?: string
  localIconName?: string
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
  diskReadBytes?: number
  diskWriteBytes?: number
  temperatures?: Array<{ name: string; temperatureCelsius: number }>
}

export interface LogEntry {
  id: string
  timestamp: string
  source: 'system' | 'agent' | 'container'
  unit: string
  level: 'error' | 'warning' | 'info' | 'debug'
  stream?: 'stdout' | 'stderr'
  message: string
  rawMessage?: string
}

export interface LogResponse {
  collectedAt: string
  entries: LogEntry[]
  nextCursor: string
}

export interface ManagedUser {
  id: number
  username: string
  role: 'root'
  disabled: boolean
  createdAt: string
  updatedAt: string
  lastLoginAt?: string | null
}

export interface PasswordPolicy {
  minLength: number
  requireUppercase: boolean
  requireLowercase: boolean
  requireDigit: boolean
  requireSpecial: boolean
}

export function requestPreferences(signal?: AbortSignal): Promise<UserPreferences> {
  return request('/api/v1/preferences', signal ? { signal } : {}, parsePreferencesResponse)
}

export function updatePreferences(input: UserPreferences, signal?: AbortSignal): Promise<UserPreferences> {
  return request('/api/v1/preferences', { method: 'PUT', body: JSON.stringify(input), ...(signal ? { signal } : {}) },
    (value) => parsePreferencesResponse(value, 'write'))
}

export function requestListPreference(listKey: string, signal?: AbortSignal): Promise<ListPreference> {
  return request(`/api/v1/preferences/lists/${encodeURIComponent(listKey)}`, signal ? { signal } : {})
}

export function updateListPreference(
  listKey: string,
  input: Omit<ListPreference, 'listKey'>,
  signal?: AbortSignal,
): Promise<ListPreference> {
  return request(`/api/v1/preferences/lists/${encodeURIComponent(listKey)}`, {
    method: 'PUT',
    body: JSON.stringify({ listKey, ...input }),
    ...(signal ? { signal } : {}),
  })
}

export function requestDatabaseProjectPreferences(signal?: AbortSignal): Promise<DatabaseProjectPreference[]> {
  return request('/api/v1/databases/project-preferences', signal ? { signal } : {})
}

export function updateDatabaseProjectPreference(
  input: DatabaseProjectPreference,
  signal?: AbortSignal,
): Promise<DatabaseProjectPreference> {
  return request('/api/v1/databases/project-preferences', {
    method: 'PUT',
    body: JSON.stringify(input),
    ...(signal ? { signal } : {}),
  })
}

export function requestSites(signal?: AbortSignal): Promise<SiteListResponse> {
  return request<unknown>('/api/v1/sites', signal ? { signal } : {}).then(parseSiteListResponse)
}

export function updateSite(
  siteId: string,
  input: SiteProfileInput,
  signal?: AbortSignal,
): Promise<SiteProfileInput & { projectId: string }> {
  return requestSiteProfileWrite(`/api/v1/sites/${encodeURIComponent(siteId)}`, {
    method: 'PUT',
    body: JSON.stringify(input),
    ...(signal ? { signal } : {}),
  }, 'update', (value) => parseSiteProfileResponse(value, { operation: 'update', siteId }), siteId)
}

export function createSite(
  input: SiteProfileInput,
  signal?: AbortSignal,
): Promise<SiteProfileInput & { projectId: string }> {
  return requestSiteProfileWrite('/api/v1/sites', { method: 'POST', body: JSON.stringify(input), ...(signal ? { signal } : {}) },
    'create', (value) => parseSiteProfileResponse(value, { operation: 'create' }))
}

export function deleteSite(siteId: string, signal?: AbortSignal): Promise<void> {
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}`, { method: 'DELETE', ...(signal ? { signal } : {}) })
}

export function requestIgnoredSites(signal?: AbortSignal): Promise<SiteProfileResponse[]> {
  return request<unknown>('/api/v1/sites/ignored', signal ? { signal } : {}).then(parseIgnoredSitesResponse)
}

export function restoreSite(siteId: string, signal?: AbortSignal): Promise<void> {
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}/restore`, {
    method: 'POST',
    ...(signal ? { signal } : {}),
  })
}

export function uploadSiteIcon(
  siteId: string,
  icon: File,
  signal?: AbortSignal,
): Promise<{ siteId: string; iconUrl: string }> {
  const body = new FormData()
  body.append('icon', icon)
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}/icon`, {
    method: 'POST',
    body,
    ...(signal ? { signal } : {}),
  })
}

export function deleteSiteIcon(siteId: string, signal?: AbortSignal): Promise<void> {
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}/icon`, {
    method: 'DELETE',
    ...(signal ? { signal } : {}),
  })
}

export function recordSiteVisit(
  siteId: string,
  signal?: AbortSignal,
): Promise<{ siteId: string; lastVisitedAt: string }> {
  return request(`/api/v1/sites/${encodeURIComponent(siteId)}/visit`, { method: 'POST', ...(signal ? { signal } : {}) })
}

export function requestComposeDraft(projectId: string, configPath: string): Promise<ComposeDraft> {
  const parameters = new URLSearchParams({ projectId, configPath })
  return request(`/api/v1/docker/compose/drafts?${parameters}`)
}

export function saveComposeDraft(
  input: Pick<ComposeDraft, 'projectId' | 'configPath' | 'content'>,
): Promise<ComposeDraft> {
  return request('/api/v1/docker/compose/drafts', { method: 'PUT', body: JSON.stringify(input) })
}

export function requestComposeRevisions(projectId: string): Promise<ComposeRevision[]> {
  return request(`/api/v1/docker/compose/revisions?${new URLSearchParams({ projectId })}`)
}

export function requestResourceAlerts(signal?: AbortSignal): Promise<ResourceAlerts> {
  return request('/api/v1/monitoring/alerts', signal ? { signal } : {}, parseResourceAlerts)
}

export function requestMetricSamples(
  input: '1h' | '6h' | '24h' | '7d' | { from: string; to: string },
  signal?: AbortSignal,
): Promise<MetricSample[]> {
  const parameters =
    typeof input === 'string'
      ? new URLSearchParams({ range: input })
      : new URLSearchParams({ from: input.from, to: input.to })
  return request(`/api/v1/monitoring/samples?${parameters}`, signal ? { signal } : {})
}

export function requestLogs(
  input: {
    source: 'system' | 'agent' | 'container'
    containerId?: string
    level?: string
    query?: string
    hours?: number
    limit?: number
    cursor?: string
  },
  signal?: AbortSignal,
): Promise<LogResponse> {
  return request(`/api/v1/logs?${logParameters(input)}`, { signal })
}

export function requestUsers(signal?: AbortSignal): Promise<ManagedUser[]> {
  return request('/api/v1/users', signal ? { signal } : {}, parseUserListResponse)
}

export function requestPasswordPolicy(signal?: AbortSignal): Promise<PasswordPolicy> {
  return request('/api/v1/users/password-policy', signal ? { signal } : {}, parsePasswordPolicyResponse)
}

export function updatePasswordPolicy(input: PasswordPolicy): Promise<PasswordPolicy> {
  return request('/api/v1/users/password-policy', { method: 'PUT', body: JSON.stringify(input) })
}

export function createUser(input: { username: string; password: string }): Promise<ManagedUser> {
  const username = normalizeManagedUsername(input.username)
  const body = JSON.stringify(input)
  return requestUserWrite('/api/v1/users', { method: 'POST', body }, 'create', 201, (value) => {
    const user = parseManagedUserResponse(value)
    if (!username || user.username !== username || user.disabled) throw new Error('invalid user result')
    return user
  })
}

export function updateUserStatus(userId: number, disabled: boolean): Promise<ManagedUser> {
  return requestUserWrite(`/api/v1/users/${userId}/status`, { method: 'PUT', body: JSON.stringify({ disabled }) }, 'status', 200, (value) => {
    const user = parseManagedUserResponse(value)
    if (user.id !== userId || user.disabled !== disabled) throw new Error('invalid user result')
    return user
  })
}

export function updateUserPassword(
  userId: number,
  input: { currentPassword?: string; newPassword: string },
): Promise<void> {
  return requestUserWrite(`/api/v1/users/${userId}/password`, {
    method: 'PUT',
    body: JSON.stringify({
      currentPassword: input.currentPassword || '',
      newPassword: input.newPassword,
    }),
  }, 'password', 204, () => undefined)
}

export function deleteUser(userId: number): Promise<void> {
  return requestUserWrite(`/api/v1/users/${userId}`, { method: 'DELETE' }, 'delete', 204, () => undefined)
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
  onError: (state?: 'reconnecting' | 'failed') => void,
): EventSource {
  const parameters = logParameters(input)
  parameters.set('interval', String(intervalSeconds))
  const source = new EventSource(`/api/v1/logs/events?${parameters}`)
  source.addEventListener('logs', (event) => {
    try {
      const payload = JSON.parse((event as MessageEvent<string>).data) as LogResponse
      if (!Array.isArray(payload.entries) || typeof payload.collectedAt !== 'string')
        throw new Error('invalid log stream')
      onLogs(payload)
    } catch {
      source.close()
      onError('failed')
    }
  })
  source.addEventListener('unavailable', () => onError('reconnecting'))
  source.onerror = () => onError(source.readyState === EventSource.CLOSED ? 'failed' : 'reconnecting')
  return source
}

function logParameters(input: {
  source: 'system' | 'agent' | 'container'
  containerId?: string
  level?: string
  query?: string
  hours?: number
  limit?: number
  cursor?: string
}) {
  const parameters = new URLSearchParams({
    source: input.source,
    level: input.level || 'all',
    query: input.query || '',
    hours: String(input.hours || 6),
    limit: String(input.limit || 150),
  })
  if (input.containerId) parameters.set('containerId', input.containerId)
  if (input.cursor) parameters.set('cursor', input.cursor)
  return parameters
}

async function request<T>(path: string, init: RequestInit = {}, parse?: (value: unknown) => T): Promise<T> {
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
      const payload = (await response.json()) as { code?: string; message?: string; requestId?: string }
      throw new NcpApiError(
        payload.code || 'CONTROL_REQUEST_FAILED',
        payload.message || '控制面请求失败。',
        payload.requestId,
      )
    } catch (error) {
      if (error instanceof NcpApiError) throw error
      throw new NcpApiError('CONTROL_REQUEST_FAILED', '控制面请求失败。')
    }
  }
  if (response.status === 204) return parse ? parse(undefined) : undefined as T
  let value: unknown
  try {
    value = await response.json()
  } catch {
    if (parse) return parse(undefined)
    throw new NcpApiError('CONTROL_RESPONSE_INVALID', '控制面返回了无法识别的数据。')
  }
  return parse ? parse(value) : value as T
}
