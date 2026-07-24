import { NcpApiError } from '@/api/system'

export interface UserPreferences {
  refreshIntervalSeconds: number
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
  hidden: boolean
  source: 'auto' | 'edited'
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
  hidden: boolean
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
