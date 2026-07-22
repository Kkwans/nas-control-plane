export interface SystemCapabilities {
  hostname?: string
  architecture: string
  docker: boolean
  compose: boolean
  systemd: boolean
  journald: boolean
  cgroupVersion: number
  dataVolumes?: string[]
  networkInterfaces?: string[]
  [key: string]: unknown
}

export interface RootUser {
  id: number
  username: string
  role: 'root'
}

export interface AuthStatus {
  initialized: boolean
  authenticated: boolean
  user?: RootUser
}

export interface AuthSession {
  user: RootUser
  expiresAt: string
}

export interface SystemSummary {
  collectedAt: string
  host: {
    hostname: string
    operatingSystem: string
    kernelVersion: string
    architecture: string
    uptimeSeconds: number
    processCount: number
  }
  cpu: {
    usagePercent: number
    logicalCores: number
    load1: number
    load5: number
    load15: number
  }
  memory: {
    totalBytes: number
    usedBytes: number
    availableBytes: number
  }
  storage: Array<{
    mountpoint: string
    totalBytes: number
    usedBytes: number
    freeBytes: number
  }>
  network: Array<{
    name: string
    receiveBytes: number
    transmitBytes: number
  }>
  warnings: Array<{
    code: string
    source: string
  }>
}

export interface DockerProject {
  id: string
  name: string
  kind: 'compose' | 'standalone'
  state: 'running' | 'stopped' | 'degraded'
  workingDirectory: string
  containerCount: number
  runningCount: number
}

export interface DockerInventory {
  collectedAt: string
  engine: {
    serverVersion: string
    operatingSystem: string
    architecture: string
    containers: number
    containersRunning: number
    containersStopped: number
    images: number
  }
  containers: Array<{
    id: string
    name: string
    image: string
    state: string
    status: string
    createdAt: string
    ports: Array<{
      hostIp: string
      privatePort: number
      publicPort: number
      protocol: string
    }>
    projectId: string
    projectName: string
    serviceName: string
  }>
  projects: DockerProject[]
}

export interface ServiceListResponse {
  collectedAt: string
  services: DockerProject[]
}

interface ApiErrorResponse {
  code: string
  message: string
  requestId: string
}

export class NcpApiError extends Error {
  readonly code: string
  readonly requestId?: string

  constructor(code: string, message: string, requestId?: string) {
    super(message)
    this.name = 'NcpApiError'
    this.code = code
    this.requestId = requestId
  }
}

export async function requestAuthStatus(fetcher: typeof fetch = fetch): Promise<AuthStatus> {
  return requestJson('/api/v1/auth/status', {}, isAuthStatus, fetcher, 'AUTH_STATUS_RESPONSE_INVALID')
}

export async function bootstrapRoot(
  credentials: { username: string; password: string },
  fetcher: typeof fetch = fetch,
): Promise<AuthSession> {
  return requestCredentials('/api/v1/auth/bootstrap', credentials, fetcher)
}

export async function loginRoot(
  credentials: { username: string; password: string },
  fetcher: typeof fetch = fetch,
): Promise<AuthSession> {
  return requestCredentials('/api/v1/auth/login', credentials, fetcher)
}

export async function logoutRoot(fetcher: typeof fetch = fetch): Promise<void> {
  const response = await fetcher('/api/v1/auth/logout', requestOptions({ method: 'POST' }))
  if (!response.ok) {
    throw await responseError(response)
  }
}

export async function requestCapabilities(fetcher: typeof fetch = fetch): Promise<SystemCapabilities> {
  return requestJson(
    '/api/v1/system/capabilities',
    {},
    isSystemCapabilities,
    fetcher,
    'SYSTEM_CAPABILITIES_RESPONSE_INVALID',
  )
}

export async function requestSystemSummary(fetcher: typeof fetch = fetch): Promise<SystemSummary> {
  return requestJson('/api/v1/system/summary', {}, isSystemSummary, fetcher, 'SYSTEM_SUMMARY_RESPONSE_INVALID')
}

export async function requestDockerInventory(fetcher: typeof fetch = fetch): Promise<DockerInventory> {
  return requestJson('/api/v1/docker/inventory', {}, isDockerInventory, fetcher, 'DOCKER_INVENTORY_RESPONSE_INVALID')
}

export async function requestServices(fetcher: typeof fetch = fetch): Promise<ServiceListResponse> {
  return requestJson('/api/v1/services', {}, isServiceListResponse, fetcher, 'SERVICES_RESPONSE_INVALID')
}

async function requestCredentials(
  path: string,
  credentials: { username: string; password: string },
  fetcher: typeof fetch,
): Promise<AuthSession> {
  return requestJson(
    path,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(credentials),
    },
    isAuthSession,
    fetcher,
    'AUTH_SESSION_RESPONSE_INVALID',
  )
}

async function requestJson<T>(
  path: string,
  init: RequestInit,
  validate: (value: unknown) => value is T,
  fetcher: typeof fetch,
  invalidCode: string,
): Promise<T> {
  const response = await fetcher(path, requestOptions(init))
  if (!response.ok) {
    throw await responseError(response)
  }
  let payload: unknown
  try {
    payload = await response.json()
  } catch {
    throw new NcpApiError(invalidCode, 'NCP 服务返回了无法识别的数据。')
  }
  if (!validate(payload)) {
    throw new NcpApiError(invalidCode, 'NCP 服务返回了无法识别的数据。')
  }
  return payload
}

function requestOptions(init: RequestInit): RequestInit {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  return {
    ...init,
    credentials: 'same-origin',
    headers,
  }
}

async function responseError(response: Response): Promise<NcpApiError> {
  try {
    const payload = await response.json()
    if (isApiErrorResponse(payload)) {
      return new NcpApiError(payload.code, payload.message, payload.requestId)
    }
  } catch {
    // Stable client errors avoid exposing a proxy or HTML response to the UI.
  }
  return new NcpApiError('API_REQUEST_FAILED', 'NCP 服务暂不可用。')
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isApiErrorResponse(value: unknown): value is ApiErrorResponse {
  return (
    isRecord(value) &&
    typeof value.code === 'string' &&
    typeof value.message === 'string' &&
    typeof value.requestId === 'string'
  )
}

function isRootUser(value: unknown): value is RootUser {
  return isRecord(value) && typeof value.id === 'number' && typeof value.username === 'string' && value.role === 'root'
}

function isAuthStatus(value: unknown): value is AuthStatus {
  return (
    isRecord(value) &&
    typeof value.initialized === 'boolean' &&
    typeof value.authenticated === 'boolean' &&
    (value.user === undefined || isRootUser(value.user))
  )
}

function isAuthSession(value: unknown): value is AuthSession {
  return isRecord(value) && isRootUser(value.user) && typeof value.expiresAt === 'string'
}

function isSystemCapabilities(value: unknown): value is SystemCapabilities {
  return (
    isRecord(value) &&
    typeof value.architecture === 'string' &&
    typeof value.docker === 'boolean' &&
    typeof value.cgroupVersion === 'number'
  )
}

function isSystemSummary(value: unknown): value is SystemSummary {
  return isRecord(value) && typeof value.collectedAt === 'string' && isRecord(value.host) && isRecord(value.cpu) && isRecord(value.memory) && Array.isArray(value.storage) && Array.isArray(value.network) && Array.isArray(value.warnings)
}

function isDockerInventory(value: unknown): value is DockerInventory {
  return isRecord(value) && typeof value.collectedAt === 'string' && isRecord(value.engine) && Array.isArray(value.containers) && Array.isArray(value.projects)
}

function isServiceListResponse(value: unknown): value is ServiceListResponse {
  return isRecord(value) && typeof value.collectedAt === 'string' && Array.isArray(value.services)
}
