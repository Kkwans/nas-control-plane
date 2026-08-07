export interface SystemCapabilities {
  hostname?: string
  architecture: string
  operatingSystem?: { id: string; versionId: string; prettyName: string }
  deviceModel?: string | null
  docker: boolean
  compose: boolean
  systemd: boolean
  journald: boolean
  cgroupVersion: number
  procReadable?: boolean
  sysReadable?: boolean
  smartctl?: boolean
  nvmeCli?: boolean
  temperatureSensors?: string[]
  canManageSystemUsers?: boolean
  rootFilesystemWritable?: boolean
  hostTerminal?: boolean
  tailscale?: TailscaleCapability
  mihomo?: MihomoCapability
  dns?: DNSCapability
  publicEgress?: PublicEgressCapability
  warnings?: Array<{ code: string; source: string }>
  dataVolumes?: string[]
  networkInterfaces?: string[]
  [key: string]: unknown
}

export interface CapabilityEvidence {
  source: string
  status: string
  detail: string
}

export interface TailscaleCapability {
  detected: boolean
  state: string
  backendState: string
  version: string
  interface: string
  overlayIps: string[]
  online: boolean
  linkState: string
  heartbeatState: string
  reachable: boolean
  evidence: CapabilityEvidence[]
  warnings: Array<{ code: string; source: string }>
}

export interface MihomoCapability {
  detected: boolean
  state: string
  processName: string
  executable: string
  version: string
  controller: {
    detected: boolean
    endpoint: string
    reachable: boolean
    authRequired: boolean
    tokenConfigured: boolean
    operations: string[]
    detectionSource: string
  }
  evidence: CapabilityEvidence[]
  warnings: Array<{ code: string; source: string }>
}

export interface DNSCapability {
  backend: string
  detected: boolean
  state: string
  readOnly: boolean
  canRead: boolean
  canPreview: boolean
  canConfirm: boolean
  canRollback: boolean
  nameservers: string[]
  detectionSource: string
  errorCode: string
}

export interface PublicEgressCapability {
  configured: boolean
  status: string
  endpoint: string
  requiresUserAction: boolean
  detectionSource: string
  errorCode: string
}

export interface PublicEgressResult {
  status: string
  address: string
  country?: string
  region?: string
  isp?: string
  checkedAt: string
  detectionSource: string
  errorCode: string
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
  sensors?: Array<{
    name: string
    temperatureCelsius: number
  }>
  diskIO?: {
    readBytes: number
    writeBytes: number
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

export interface SystemDetails {
  collectedAt: string
  warnings: string[]
  device: {
    hostname: string
    model: string
    operatingSystem: string
    kernelVersion: string
    architecture: string
    uptimeSeconds: number
    processCount: number
    cgroupVersion: string
  }
  hardware: {
    cpu: {
      model: string
      physicalCores: number
      logicalCores: number
      frequencyMHz: number
      temperatureCelsius: number
    }
    memory: {
      totalBytes: number
      availableBytes: number
    }
    sensors: Array<{
      name: string
      temperatureCelsius: number
    }>
  }
  network: {
    interfaces: Array<{
      name: string
      hardwareAddress: string
      mtu: number
      state: string
      speedMbps: number
      duplex: string
      addresses: Array<{
        address: string
        prefixLength: number
        family: string
      }>
    }>
    gateway: string
    routes: Array<{
      destination: string
      gateway: string
      interface: string
      metric: number
    }>
    dnsServers: string[]
    listeningPorts: Array<{
      protocol: string
      address: string
      port: number
      pid: number
      processName?: string
      executable?: string
      systemdUnit?: string
      containerId?: string
      containerName?: string
      service?: string
      detectionSource?: string
      detectionSources?: string[]
      detectionStatus?: string
      detectionErrorCode?: string
    }>
  }
  storage: {
    mounts: Array<{
      path: string
      device: string
      filesystem: string
      totalBytes: number
      usedBytes: number
      availableBytes: number
      usedPercent: number
    }>
    disks: Array<{
      name: string
      model: string
      sizeBytes: number
      rotational: boolean
      health: string
      temperatureCelsius: number
    }>
    raid: Array<{
      name: string
      level: string
      state: string
      devices: string[]
    }>
  }
  proxy: {
    mihomo: {
      detected: boolean
      state: string
      detail: string
      controllerEndpoint?: string
      authRequired?: boolean
      operations?: string[]
    }
    mihomoCapability?: MihomoCapability
    system: Array<{
      source: string
      method: string
      evidence: string
      address: string
      detail: string
    }>
    associations: Array<{
      subject: string
      kind: string
      method: string
      evidence: string
      endpoint: string
      detail: string
    }>
  }
  tailscale?: TailscaleCapability
  dns?: DNSCapability
  publicEgress?: PublicEgressCapability
  control: {
    nodes: Array<{
      id: string
      name: string
      detail: string
      status: string
      version: string
      lastSeen: string
    }>
  }
}

export interface DockerProject {
  id: string
  name: string
  kind: 'compose' | 'standalone'
  state: 'running' | 'stopped' | 'degraded'
  workingDirectory: string
  configFiles: string[]
  containerCount: number
  runningCount: number
}

export interface DockerProjectContainerActionResult {
  containerId: string
  name: string
  action: ContainerAction
  state: string
  success: boolean
  errorCode?: string
}

export interface DockerProjectActionResult {
  projectId: string
  kind: DockerProject['kind']
  action: ContainerAction
  state: string
  completed: boolean
  containers: DockerProjectContainerActionResult[]
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

export interface RealtimeSnapshot {
  collectedAt: string
  summary?: SystemSummary
  docker?: DockerInventory
  errors?: string[]
}

export interface ServiceListResponse {
  collectedAt: string
  services: DockerProject[]
}

export type ContainerAction = 'start' | 'stop' | 'restart'

export interface ContainerActionResult {
  containerId: string
  name: string
  action: ContainerAction
  state: string
}

export interface ContainerLogEntry {
  timestamp: string
  level: 'error' | 'warning' | 'info' | 'debug'
  stream: 'stdout' | 'stderr'
  message: string
}

export interface ContainerLogsResult {
  containerId: string
  tail: number
  collectedAt: string
  entries: ContainerLogEntry[]
}

export interface DockerImageSummary {
  id: string
  repoTags: string[]
  repoDigests: string[]
  sizeBytes: number
  createdAt: string
  containers: number
}

export interface DockerImageInventory {
  collectedAt: string
  images: DockerImageSummary[]
}

interface DockerImageWireSummary extends Omit<DockerImageSummary, 'repoTags' | 'repoDigests'> {
  repoTags: string[] | null
  repoDigests: string[] | null
}

interface DockerImageWireInventory {
  collectedAt: string
  images: DockerImageWireSummary[]
}

export interface JobSnapshot {
  id: string
  type: string
  status: 'queued' | 'running' | 'completed' | 'failed' | 'interrupted' | 'cancelled'
  artifactState: 'present' | 'deleted' | 'unknown'
  reference?: string
  message: string
  progress: number
  error?: string
  createdAt: string
  updatedAt: string
  completedAt?: string
  downloadedBytes: number
  totalBytes: number
  speedBytes: number
  layers: Record<string, { id: string; status: string; current: number; total: number }>
}

export interface JobListResult {
  jobs: JobSnapshot[]
  invalidCount: number
}

export interface DockerImageRemoveResult {
  imageId: string
  removed: boolean
}

export interface DockerImageRemoveBatchItem {
  imageId: string
  removed: boolean
  errorCode?: string
}

export interface DockerImageRemoveBatchResult {
  items: DockerImageRemoveBatchItem[]
  removedCount: number
  failedCount: number
  completed: boolean
}

export interface DockerContainerMountInput {
  type: 'bind' | 'volume' | 'tmpfs'
  source?: string
  target: string
  readOnly?: boolean
  volumeDriver?: string
  tmpfsSizeBytes?: number
}

export interface DockerContainerNetworkInput {
  name: string
  driver?: string
  subnet?: string
  gateway?: string
  ip?: string
}

export interface DockerContainerPortInput {
  containerPort: number
  hostPort?: number
  hostIp?: string
  protocol?: 'tcp' | 'udp' | 'sctp'
}

export interface DockerContainerDeviceInput {
  hostPath: string
  containerPath: string
  cgroupPermissions?: string
}

export interface DockerContainerGPUInput {
  driver?: string
  count?: number
  deviceIds?: string[]
  capabilities?: string[]
}

export interface DockerContainerCreateInput {
  image: string
  name?: string
  cpu?: number
  memoryBytes?: number
  restartPolicy?: 'no' | 'always' | 'on-failure' | 'unless-stopped'
  restartMaxRetries?: number
  environment?: Record<string, string>
  mounts?: DockerContainerMountInput[]
  network?: DockerContainerNetworkInput
  ports?: DockerContainerPortInput[]
  command?: string[]
  privileged?: boolean
  capAdd?: string[]
  capDrop?: string[]
  devices?: DockerContainerDeviceInput[]
  gpus?: DockerContainerGPUInput[]
  runContainer: boolean
}

export interface DockerContainerCreateResult {
  containerId: string
  name: string
  image: string
  state: string
  created: boolean
  started: boolean
  runContainer: boolean
}

export interface DockerProjectDeleteResult {
  projectId: string
  kind: 'compose'
  completed: boolean
  partial: boolean
  registryDeleted: boolean
  registryRolledBack: boolean
  containers: Array<{
    containerId: string
    name: string
    state: string
    deleted: boolean
    success: boolean
    errorCode?: string
  }>
}

export interface ComposeLifecycleServiceStatus {
  name: string
  containerId?: string
  state: string
  running: boolean
}

export interface ComposeLifecycleResult {
  projectId: string
  action: ContainerAction
  state: string
  services: ComposeLifecycleServiceStatus[]
  output: string
  completed: boolean
}

export interface DockerHubRepository {
  name: string
  namespace: string
  description: string
  starCount: number
  pullCount: number
  official: boolean
  publisher: string
  lastUpdated: string
  repositoryType: string
  statusDescription: string
}

export interface DockerHubSearchResult {
  count: number
  page: number
  pageSize: number
  results: DockerHubRepository[]
}

export interface DockerHubTag {
  name: string
  publishedAt: string
  lastUpdated: string
  fullSize: number
  architectures: string[]
}

export interface DockerHubTagsResult {
  count: number
  page: number
  pageSize: number
  results: DockerHubTag[]
}

interface DockerHubTagWire extends Omit<DockerHubTag, 'publishedAt'> {
  publishedAt?: string
}

interface DockerHubTagsWireResult extends Omit<DockerHubTagsResult, 'results'> {
  results: DockerHubTagWire[]
}

export interface ComposeFileSnapshot {
  path: string
  name: string
  content: string
  size: number
}

export interface ComposeProjectConfig {
  projectId: string
  workingDirectory: string
  files: ComposeFileSnapshot[]
  collectedAt: string
}

export interface ComposeValidationResult {
  valid: boolean
  services: string[]
  normalized: string
}

export interface ComposeDeployInput {
  projectId: string
  workingDirectory: string
  configFiles: string[]
  targetPath: string
  content: string
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

export async function requestSystemDetails(fetcher: typeof fetch = fetch): Promise<SystemDetails> {
  return requestJson('/api/v1/system/details', {}, isSystemDetails, fetcher, 'SYSTEM_DETAILS_RESPONSE_INVALID')
}

export async function requestDNSCapability(fetcher: typeof fetch = fetch): Promise<DNSCapability> {
  return requestJson('/api/v1/system/dns/capability', {}, isDNSCapability, fetcher, 'SYSTEM_DNS_RESPONSE_INVALID')
}

export interface DNSChangeRequest {
  interface?: string
  connectionId?: string
  nameservers: string[]
  searchDomains?: string[]
}

export interface DNSChangePreview {
  previewId: string
  backend: string
  before: { interface: string; connectionId: string; nameservers: string[]; searchDomains: string[] }
  after: { interface: string; connectionId: string; nameservers: string[]; searchDomains: string[] }
  requiresConfirm: boolean
  rollbackAvailable: boolean
  expiresAt: string
  errorCode: string
}

export interface DNSChangeConfirmation {
  previewId: string
  confirmed: boolean
}

export interface DNSChangeResult {
  changeId: string
  backend: string
  applied: boolean
  rollbackAvailable: boolean
  appliedAt: string
  errorCode: string
}

export async function previewDNSChange(input: DNSChangeRequest, fetcher: typeof fetch = fetch): Promise<DNSChangePreview> {
  return requestJson('/api/v1/system/dns/preview', jsonRequest(input), isDNSChangePreview, fetcher, 'SYSTEM_DNS_PREVIEW_RESPONSE_INVALID')
}

export async function confirmDNSChange(input: DNSChangeConfirmation, fetcher: typeof fetch = fetch): Promise<DNSChangeResult> {
  return requestJson('/api/v1/system/dns/confirm', jsonRequest(input), isDNSChangeResult, fetcher, 'SYSTEM_DNS_CONFIRM_RESPONSE_INVALID')
}

export async function rollbackDNSChange(changeId: string, fetcher: typeof fetch = fetch): Promise<DNSChangeResult> {
  return requestJson('/api/v1/system/dns/rollback', jsonRequest({ changeId }), isDNSChangeResult, fetcher, 'SYSTEM_DNS_ROLLBACK_RESPONSE_INVALID')
}

export async function requestMihomoCapability(fetcher: typeof fetch = fetch): Promise<MihomoCapability> {
  return requestJson('/api/v1/proxy/mihomo/capability', {}, isMihomoCapability, fetcher, 'PROXY_MIHOMO_RESPONSE_INVALID')
}

export async function invokeMihomo(
  input: { operation: string; group?: string; proxy?: string },
  fetcher: typeof fetch = fetch,
): Promise<{ operation: string; statusCode: number; data: unknown }> {
  return requestJson('/api/v1/proxy/mihomo/invoke', jsonRequest(input), isMihomoInvokeResult, fetcher, 'PROXY_MIHOMO_RESPONSE_INVALID')
}

export async function requestPublicEgressCapability(fetcher: typeof fetch = fetch): Promise<PublicEgressCapability> {
  return requestJson('/api/v1/system/public-egress/capability', {}, isPublicEgressCapability, fetcher, 'PUBLIC_EGRESS_RESPONSE_INVALID')
}

export async function detectPublicEgress(fetcher: typeof fetch = fetch): Promise<PublicEgressResult> {
  return requestJson('/api/v1/system/public-egress/detect', { method: 'POST' }, isPublicEgressResult, fetcher, 'PUBLIC_EGRESS_RESPONSE_INVALID')
}

export async function requestDockerInventory(fetcher: typeof fetch = fetch): Promise<DockerInventory> {
  return requestJson('/api/v1/docker/inventory', {}, isDockerInventory, fetcher, 'DOCKER_INVENTORY_RESPONSE_INVALID')
}

export function subscribeSystemEvents(
  scopes: Array<'summary' | 'docker'>,
  intervalSeconds: number,
  handlers: {
    open: () => void
    snapshot: (snapshot: RealtimeSnapshot) => void
    error: () => void
  },
): EventSource {
  const parameters = new URLSearchParams({ interval: String(intervalSeconds) })
  for (const scope of scopes) parameters.append('scope', scope)
  const source = new EventSource(`/api/v1/system/events?${parameters}`)
  source.onopen = handlers.open
  source.addEventListener('snapshot', (event) => {
    try {
      const payload: unknown = JSON.parse((event as MessageEvent<string>).data)
      if (!isRealtimeSnapshot(payload)) throw new Error('实时快照格式无效')
      handlers.snapshot(payload)
    } catch {
      handlers.error()
      source.close()
    }
  })
  source.onerror = handlers.error
  return source
}

export async function requestServices(fetcher: typeof fetch = fetch): Promise<ServiceListResponse> {
  return requestJson('/api/v1/services', {}, isServiceListResponse, fetcher, 'SERVICES_RESPONSE_INVALID')
}

export async function requestContainerAction(
  containerId: string,
  action: ContainerAction,
  fetcher: typeof fetch = fetch,
): Promise<ContainerActionResult> {
  return requestJson(
    `/api/v1/docker/containers/${encodeURIComponent(containerId)}/actions/${action}`,
    { method: 'POST' },
    isContainerActionResult,
    fetcher,
    'DOCKER_CONTAINER_ACTION_RESPONSE_INVALID',
  )
}

export async function requestDockerProjectAction(
  project: Pick<DockerProject, 'id' | 'kind' | 'workingDirectory' | 'configFiles'> & { containerIds?: string[] },
  action: ContainerAction,
  fetcher: typeof fetch = fetch,
): Promise<DockerProjectActionResult | ComposeLifecycleResult> {
  if (project.kind === 'compose') {
    return requestJson(
      `/api/v1/docker/compose/projects/${encodeURIComponent(project.id)}/actions/${action}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          projectId: project.id,
          workingDirectory: project.workingDirectory,
          configFiles: project.configFiles,
          action,
        }),
      },
      isComposeLifecycleResult,
      fetcher,
      'DOCKER_PROJECT_ACTION_RESPONSE_INVALID',
    )
  }
  return requestJson(
    `/api/v1/docker/projects/${encodeURIComponent(project.id)}/actions/${action}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        projectId: project.id,
        kind: 'standalone',
        containerIds: project.containerIds ?? [],
        action,
      }),
    },
    isDockerProjectActionResult,
    fetcher,
    'DOCKER_PROJECT_ACTION_RESPONSE_INVALID',
  )
}

export async function requestContainerLogs(
  containerId: string,
  tail = 120,
  fetcher: typeof fetch = fetch,
): Promise<ContainerLogsResult> {
  return requestJson(
    `/api/v1/docker/containers/${encodeURIComponent(containerId)}/logs?tail=${tail}`,
    {},
    isContainerLogsResult,
    fetcher,
    'DOCKER_LOGS_RESPONSE_INVALID',
  )
}

export async function requestDockerImages(fetcher: typeof fetch = fetch): Promise<DockerImageInventory> {
  const payload = await requestJson(
    '/api/v1/docker/images',
    {},
    isDockerImageWireInventory,
    fetcher,
    'DOCKER_IMAGE_LIST_RESPONSE_INVALID',
  )
  return {
    collectedAt: payload.collectedAt,
    images: payload.images.map((image) => ({
      ...image,
      repoTags: image.repoTags ?? [],
      repoDigests: image.repoDigests ?? [],
    })),
  }
}

export async function pullDockerImage(
  reference: string,
  expectedBytes = 0,
  fetcher: typeof fetch = fetch,
): Promise<JobSnapshot> {
  return requestJobSnapshot(
    '/api/v1/docker/images/pull',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reference, expectedBytes }),
    },
    fetcher,
    'DOCKER_IMAGE_PULL_RESPONSE_INVALID',
  )
}

export function followJob(jobId: string, onProgress: (job: JobSnapshot) => void): Promise<JobSnapshot> {
  return new Promise((resolve, reject) => {
    const source = new EventSource(`/api/v1/jobs/${encodeURIComponent(jobId)}/events`)
    source.addEventListener('progress', (event) => {
      try {
        const payload: unknown = JSON.parse((event as MessageEvent<string>).data)
        const job = parseJobSnapshot(payload)
        if (!job) throw new Error('任务进度格式无效')
        onProgress(job)
        if (job.status === 'completed' || job.status === 'failed' || job.status === 'interrupted' || job.status === 'cancelled') {
          source.close()
          resolve(job)
        }
      } catch (error) {
        source.close()
        reject(error)
      }
    })
    source.onerror = () => {
      source.close()
      reject(new NcpApiError('JOB_STREAM_FAILED', '任务进度连接已中断。'))
    }
  })
}

export async function requestJobs(type = '', fetcher: typeof fetch = fetch): Promise<JobListResult> {
  const parameters = new URLSearchParams()
  if (type) parameters.set('type', type)
  const payload = await requestJson(
    `/api/v1/jobs${parameters.size ? `?${parameters}` : ''}`,
    {},
    (value): value is { jobs: unknown[] } => isRecord(value) && Array.isArray(value.jobs),
    fetcher,
    'JOB_LIST_RESPONSE_INVALID',
  )
  const jobs: JobSnapshot[] = []
  let invalidCount = 0
  for (const value of payload.jobs) {
    const job = parseJobSnapshot(value)
    if (job) jobs.push(job)
    else invalidCount += 1
  }
  if (invalidCount > 0) {
    console.warn(`[NCP] 已忽略 ${invalidCount} 条无法解析的任务记录。`)
  }
  return { jobs, invalidCount }
}

export async function retryJob(jobId: string, fetcher: typeof fetch = fetch): Promise<JobSnapshot> {
  return requestJobSnapshot(
    `/api/v1/jobs/${encodeURIComponent(jobId)}/retry`,
    { method: 'POST' },
    fetcher,
    'JOB_RETRY_RESPONSE_INVALID',
  )
}

export async function cancelJob(jobId: string, fetcher: typeof fetch = fetch): Promise<JobSnapshot> {
  return requestJobSnapshot(
    `/api/v1/jobs/${encodeURIComponent(jobId)}/cancel`,
    { method: 'POST' },
    fetcher,
    'JOB_CANCEL_RESPONSE_INVALID',
  )
}

export async function deleteJob(jobId: string, fetcher: typeof fetch = fetch): Promise<void> {
  const response = await fetcher(`/api/v1/jobs/${encodeURIComponent(jobId)}`, requestOptions({ method: 'DELETE' }))
  if (!response.ok) throw await responseError(response)
}

export async function removeDockerImage(
  imageId: string,
  fetcher: typeof fetch = fetch,
): Promise<DockerImageRemoveResult> {
  return requestJson(
    '/api/v1/docker/images/remove',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ imageId }),
    },
    isDockerImageRemoveResult,
    fetcher,
    'DOCKER_IMAGE_REMOVE_RESPONSE_INVALID',
  )
}

export async function removeDockerImages(
  imageIds: string[],
  fetcher: typeof fetch = fetch,
): Promise<DockerImageRemoveBatchResult> {
  return requestJson(
    '/api/v1/docker/images/remove-batch',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ imageIds }),
    },
    isDockerImageRemoveBatchResult,
    fetcher,
    'DOCKER_IMAGE_REMOVE_BATCH_RESPONSE_INVALID',
  )
}

export async function createDockerContainer(
  input: DockerContainerCreateInput,
  fetcher: typeof fetch = fetch,
): Promise<DockerContainerCreateResult> {
  return requestJson(
    '/api/v1/docker/containers',
    jsonRequest(input),
    isDockerContainerCreateResult,
    fetcher,
    'DOCKER_CONTAINER_CREATE_RESPONSE_INVALID',
  )
}

export async function deleteDockerProject(
  project: Pick<DockerProject, 'id' | 'name' | 'kind'>,
  fetcher: typeof fetch = fetch,
): Promise<DockerProjectDeleteResult> {
  return requestJson(
    `/api/v1/docker/compose/projects/${encodeURIComponent(project.id)}`,
    {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: project.id, kind: project.kind, registryName: project.name }),
    },
    isDockerProjectDeleteResult,
    fetcher,
    'DOCKER_PROJECT_DELETE_RESPONSE_INVALID',
  )
}

export async function searchDockerHub(
  query: string,
  page = 1,
  pageSize = 20,
  sort = 'relevance',
  fetcher: typeof fetch = fetch,
): Promise<DockerHubSearchResult> {
  const parameters = new URLSearchParams({ query, page: String(page), pageSize: String(pageSize), sort })
  return requestJson(
    `/api/v1/docker/hub/search?${parameters}`,
    {},
    isDockerHubSearchResult,
    fetcher,
    'DOCKER_HUB_SEARCH_RESPONSE_INVALID',
  )
}

export async function requestDockerHubTags(
  namespace: string,
  repository: string,
  page = 1,
  pageSize = 25,
  fetcher: typeof fetch = fetch,
): Promise<DockerHubTagsResult> {
  const parameters = new URLSearchParams({ namespace, repository, page: String(page), pageSize: String(pageSize) })
  const payload = await requestJson(
    `/api/v1/docker/hub/tags?${parameters}`,
    {},
    isDockerHubTagsWireResult,
    fetcher,
    'DOCKER_HUB_TAGS_RESPONSE_INVALID',
  )
  return {
    ...payload,
    results: payload.results.map((tag) => ({
      ...tag,
      publishedAt: tag.publishedAt?.trim() || tag.lastUpdated,
    })),
  }
}

export async function readComposeConfig(
  project: Pick<DockerProject, 'id' | 'workingDirectory' | 'configFiles'>,
  fetcher: typeof fetch = fetch,
): Promise<ComposeProjectConfig> {
  return requestJson(
    '/api/v1/docker/compose/config/read',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        projectId: project.id,
        workingDirectory: project.workingDirectory,
        configFiles: project.configFiles,
      }),
    },
    isComposeProjectConfig,
    fetcher,
    'COMPOSE_CONFIG_RESPONSE_INVALID',
  )
}

export async function validateComposeConfig(
  path: string,
  content: string,
  fetcher: typeof fetch = fetch,
): Promise<ComposeValidationResult> {
  return requestJson(
    '/api/v1/docker/compose/config/validate',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, content }),
    },
    isComposeValidationResult,
    fetcher,
    'COMPOSE_VALIDATION_RESPONSE_INVALID',
  )
}

export async function deployComposeConfig(
  input: ComposeDeployInput,
  fetcher: typeof fetch = fetch,
): Promise<JobSnapshot> {
  return requestJobSnapshot(
    '/api/v1/docker/compose/deploy',
    { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) },
    fetcher,
    'COMPOSE_DEPLOY_RESPONSE_INVALID',
  )
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

async function requestJobSnapshot(
  path: string,
  init: RequestInit,
  fetcher: typeof fetch,
  invalidCode: string,
): Promise<JobSnapshot> {
  const payload = await requestJson(path, init, isRecord, fetcher, invalidCode)
  const job = parseJobSnapshot(payload)
  if (!job) {
    throw new NcpApiError(invalidCode, 'NCP 服务返回了无法识别的数据。')
  }
  return job
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

function jsonRequest(body: unknown): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
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

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
}

function isCapabilityEvidence(value: unknown): value is CapabilityEvidence {
  return isRecord(value) && typeof value.source === 'string' && typeof value.status === 'string' && typeof value.detail === 'string'
}

function isDNSCapability(value: unknown): value is DNSCapability {
  return (
    isRecord(value) &&
    typeof value.backend === 'string' &&
    typeof value.detected === 'boolean' &&
    typeof value.state === 'string' &&
    typeof value.readOnly === 'boolean' &&
    typeof value.canRead === 'boolean' &&
    typeof value.canPreview === 'boolean' &&
    typeof value.canConfirm === 'boolean' &&
    typeof value.canRollback === 'boolean' &&
    isStringArray(value.nameservers) &&
    typeof value.detectionSource === 'string' &&
    typeof value.errorCode === 'string'
  )
}

function isDNSChangeEndpoint(value: unknown): value is DNSChangePreview['before'] {
  return (
    isRecord(value) &&
    typeof value.interface === 'string' &&
    typeof value.connectionId === 'string' &&
    isStringArray(value.nameservers) &&
    isStringArray(value.searchDomains)
  )
}

function isDNSChangePreview(value: unknown): value is DNSChangePreview {
  return (
    isRecord(value) &&
    typeof value.previewId === 'string' &&
    typeof value.backend === 'string' &&
    isDNSChangeEndpoint(value.before) &&
    isDNSChangeEndpoint(value.after) &&
    typeof value.requiresConfirm === 'boolean' &&
    typeof value.rollbackAvailable === 'boolean' &&
    typeof value.expiresAt === 'string' &&
    typeof value.errorCode === 'string'
  )
}

function isDNSChangeResult(value: unknown): value is DNSChangeResult {
  return (
    isRecord(value) &&
    typeof value.changeId === 'string' &&
    typeof value.backend === 'string' &&
    typeof value.applied === 'boolean' &&
    typeof value.rollbackAvailable === 'boolean' &&
    typeof value.appliedAt === 'string' &&
    typeof value.errorCode === 'string'
  )
}

function isTailscaleCapability(value: unknown): value is TailscaleCapability {
  return (
    isRecord(value) &&
    typeof value.detected === 'boolean' &&
    typeof value.state === 'string' &&
    typeof value.backendState === 'string' &&
    typeof value.version === 'string' &&
    typeof value.interface === 'string' &&
    isStringArray(value.overlayIps) &&
    typeof value.online === 'boolean' &&
    typeof value.linkState === 'string' &&
    typeof value.heartbeatState === 'string' &&
    typeof value.reachable === 'boolean' &&
    Array.isArray(value.evidence) && value.evidence.every(isCapabilityEvidence) &&
    Array.isArray(value.warnings)
  )
}

function isMihomoCapability(value: unknown): value is MihomoCapability {
  if (
    !isRecord(value) ||
    typeof value.detected !== 'boolean' ||
    typeof value.state !== 'string' ||
    typeof value.processName !== 'string' ||
    typeof value.executable !== 'string' ||
    typeof value.version !== 'string' ||
    !isRecord(value.controller) ||
    !Array.isArray(value.evidence) ||
    !value.evidence.every(isCapabilityEvidence) ||
    !Array.isArray(value.warnings)
  ) return false
  return (
    typeof value.controller.detected === 'boolean' &&
    typeof value.controller.endpoint === 'string' &&
    typeof value.controller.reachable === 'boolean' &&
    typeof value.controller.authRequired === 'boolean' &&
    typeof value.controller.tokenConfigured === 'boolean' &&
    isStringArray(value.controller.operations) &&
    typeof value.controller.detectionSource === 'string'
  )
}

function isMihomoInvokeResult(value: unknown): value is { operation: string; statusCode: number; data: unknown } {
  return isRecord(value) && typeof value.operation === 'string' && typeof value.statusCode === 'number' && 'data' in value
}

function isPublicEgressCapability(value: unknown): value is PublicEgressCapability {
  return (
    isRecord(value) &&
    typeof value.configured === 'boolean' &&
    typeof value.status === 'string' &&
    typeof value.endpoint === 'string' &&
    typeof value.requiresUserAction === 'boolean' &&
    typeof value.detectionSource === 'string' &&
    typeof value.errorCode === 'string'
  )
}

function isPublicEgressResult(value: unknown): value is PublicEgressResult {
  return (
    isRecord(value) &&
    typeof value.status === 'string' &&
    typeof value.address === 'string' &&
    (value.country === undefined || typeof value.country === 'string') &&
    (value.region === undefined || typeof value.region === 'string') &&
    (value.isp === undefined || typeof value.isp === 'string') &&
    typeof value.checkedAt === 'string' &&
    typeof value.detectionSource === 'string' &&
    typeof value.errorCode === 'string'
  )
}

function isContainerAction(value: unknown): value is ContainerAction {
  return value === 'start' || value === 'stop' || value === 'restart'
}

function isDockerProjectActionResult(value: unknown): value is DockerProjectActionResult {
  return (
    isRecord(value) &&
    typeof value.projectId === 'string' &&
    (value.kind === 'standalone' || value.kind === 'compose') &&
    isContainerAction(value.action) &&
    typeof value.state === 'string' &&
    typeof value.completed === 'boolean' &&
    Array.isArray(value.containers) &&
    value.containers.every(
      (item) =>
        isRecord(item) &&
        typeof item.containerId === 'string' &&
        typeof item.name === 'string' &&
        isContainerAction(item.action) &&
        typeof item.state === 'string' &&
        typeof item.success === 'boolean' &&
        (item.errorCode === undefined || typeof item.errorCode === 'string'),
    )
  )
}

function isComposeLifecycleResult(value: unknown): value is ComposeLifecycleResult {
  return (
    isRecord(value) &&
    typeof value.projectId === 'string' &&
    isContainerAction(value.action) &&
    typeof value.state === 'string' &&
    typeof value.output === 'string' &&
    typeof value.completed === 'boolean' &&
    Array.isArray(value.services) &&
    value.services.every(
      (service) =>
        isRecord(service) &&
        typeof service.name === 'string' &&
        (service.containerId === undefined || typeof service.containerId === 'string') &&
        typeof service.state === 'string' &&
        typeof service.running === 'boolean',
    )
  )
}

function isDockerImageRemoveBatchResult(value: unknown): value is DockerImageRemoveBatchResult {
  return (
    isRecord(value) &&
    typeof value.removedCount === 'number' &&
    typeof value.failedCount === 'number' &&
    typeof value.completed === 'boolean' &&
    Array.isArray(value.items) &&
    value.items.every(
      (item) =>
        isRecord(item) &&
        typeof item.imageId === 'string' &&
        typeof item.removed === 'boolean' &&
        (item.errorCode === undefined || typeof item.errorCode === 'string'),
    )
  )
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
    typeof value.cgroupVersion === 'number' &&
    (value.tailscale === undefined || isTailscaleCapability(value.tailscale)) &&
    (value.mihomo === undefined || isMihomoCapability(value.mihomo)) &&
    (value.dns === undefined || isDNSCapability(value.dns)) &&
    (value.publicEgress === undefined || isPublicEgressCapability(value.publicEgress))
  )
}

function isSystemSummary(value: unknown): value is SystemSummary {
  return isRecord(value) && typeof value.collectedAt === 'string' && isRecord(value.host) && isRecord(value.cpu) && isRecord(value.memory) && Array.isArray(value.storage) && Array.isArray(value.network) && Array.isArray(value.warnings)
}

function isSystemDetails(value: unknown): value is SystemDetails {
  if (
    !isRecord(value) ||
    typeof value.collectedAt !== 'string' ||
    !Array.isArray(value.warnings) ||
    !isRecord(value.device) ||
    !isRecord(value.hardware) ||
    !isRecord(value.network) ||
    !isRecord(value.storage) ||
    !isRecord(value.proxy) ||
    !isRecord(value.control)
  ) return false

  return (
    isRecord(value.hardware.cpu) &&
    isRecord(value.hardware.memory) &&
    Array.isArray(value.hardware.sensors) &&
    Array.isArray(value.network.interfaces) &&
    Array.isArray(value.network.routes) &&
    Array.isArray(value.network.dnsServers) &&
    Array.isArray(value.network.listeningPorts) &&
    Array.isArray(value.storage.mounts) &&
    Array.isArray(value.storage.disks) &&
    Array.isArray(value.storage.raid) &&
    isRecord(value.proxy.mihomo) &&
    Array.isArray(value.proxy.system) &&
    Array.isArray(value.proxy.associations) &&
    Array.isArray(value.control.nodes)
  )
}

function isDockerInventory(value: unknown): value is DockerInventory {
  return isRecord(value) && typeof value.collectedAt === 'string' && isRecord(value.engine) && Array.isArray(value.containers) && Array.isArray(value.projects)
}

function isRealtimeSnapshot(value: unknown): value is RealtimeSnapshot {
  return isRecord(value) &&
    typeof value.collectedAt === 'string' &&
    (value.summary === undefined || isSystemSummary(value.summary)) &&
    (value.docker === undefined || isDockerInventory(value.docker)) &&
    (value.errors === undefined || (Array.isArray(value.errors) && value.errors.every((item) => typeof item === 'string')))
}

function isServiceListResponse(value: unknown): value is ServiceListResponse {
  return isRecord(value) && typeof value.collectedAt === 'string' && Array.isArray(value.services)
}

function isContainerActionResult(value: unknown): value is ContainerActionResult {
  return (
    isRecord(value) &&
    typeof value.containerId === 'string' &&
    typeof value.name === 'string' &&
    (value.action === 'start' || value.action === 'stop' || value.action === 'restart') &&
    typeof value.state === 'string'
  )
}

function isContainerLogsResult(value: unknown): value is ContainerLogsResult {
  return (
    isRecord(value) &&
    typeof value.containerId === 'string' &&
    typeof value.tail === 'number' &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.entries) &&
    value.entries.every(
      (entry) =>
        isRecord(entry) &&
        typeof entry.timestamp === 'string' &&
        (entry.level === 'error' || entry.level === 'warning' || entry.level === 'info' || entry.level === 'debug') &&
        (entry.stream === 'stdout' || entry.stream === 'stderr') &&
        typeof entry.message === 'string',
    )
  )
}

function isStringArrayOrNull(value: unknown): value is string[] | null {
  return value === null || (Array.isArray(value) && value.every((item) => typeof item === 'string'))
}

function isDockerImageWireInventory(value: unknown): value is DockerImageWireInventory {
  return (
    isRecord(value) &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.images) &&
    value.images.every(
      (image) =>
        isRecord(image) &&
        typeof image.id === 'string' &&
        isStringArrayOrNull(image.repoTags) &&
        isStringArrayOrNull(image.repoDigests) &&
        typeof image.sizeBytes === 'number' &&
        typeof image.createdAt === 'string' &&
        typeof image.containers === 'number',
    )
  )
}

function isDockerContainerCreateResult(value: unknown): value is DockerContainerCreateResult {
  return (
    isRecord(value) &&
    typeof value.containerId === 'string' &&
    typeof value.name === 'string' &&
    typeof value.image === 'string' &&
    typeof value.state === 'string' &&
    value.created === true &&
    typeof value.started === 'boolean' &&
    typeof value.runContainer === 'boolean'
  )
}

function isDockerProjectDeleteResult(value: unknown): value is DockerProjectDeleteResult {
  return (
    isRecord(value) &&
    typeof value.projectId === 'string' &&
    value.kind === 'compose' &&
    value.completed === true &&
    typeof value.partial === 'boolean' &&
    typeof value.registryDeleted === 'boolean' &&
    typeof value.registryRolledBack === 'boolean' &&
    Array.isArray(value.containers) &&
    value.containers.every((container) =>
      isRecord(container) &&
      typeof container.containerId === 'string' &&
      typeof container.name === 'string' &&
      typeof container.state === 'string' &&
      typeof container.deleted === 'boolean' &&
      typeof container.success === 'boolean' &&
      (container.errorCode === undefined || typeof container.errorCode === 'string'),
    )
  )
}

function parseJobSnapshot(value: unknown): JobSnapshot | null {
  if (!(isRecord(value) &&
    typeof value.id === 'string' &&
    typeof value.type === 'string' &&
    (value.status === 'queued' || value.status === 'running' || value.status === 'completed' || value.status === 'failed' || value.status === 'interrupted' || value.status === 'cancelled') &&
    typeof value.message === 'string' &&
    typeof value.progress === 'number' &&
    typeof value.createdAt === 'string' &&
    typeof value.updatedAt === 'string' &&
    typeof value.downloadedBytes === 'number' &&
    typeof value.totalBytes === 'number' &&
    typeof value.speedBytes === 'number' &&
    isRecord(value.layers))) {
    return null
  }
  const artifactState =
    value.artifactState === 'present' || value.artifactState === 'deleted' || value.artifactState === 'unknown'
      ? value.artifactState
      : 'unknown'
  return { ...value, artifactState } as unknown as JobSnapshot
}

function isDockerImageRemoveResult(value: unknown): value is DockerImageRemoveResult {
  return isRecord(value) && typeof value.imageId === 'string' && value.removed === true
}

function isDockerHubRepository(value: unknown): value is DockerHubRepository {
  return isRecord(value) &&
    typeof value.name === 'string' &&
    typeof value.namespace === 'string' &&
    typeof value.description === 'string' &&
    typeof value.starCount === 'number' &&
    typeof value.pullCount === 'number' &&
    typeof value.official === 'boolean'
}

function isDockerHubSearchResult(value: unknown): value is DockerHubSearchResult {
  return isRecord(value) &&
    typeof value.count === 'number' &&
    typeof value.page === 'number' &&
    typeof value.pageSize === 'number' &&
    Array.isArray(value.results) &&
    value.results.every(isDockerHubRepository)
}

function isDockerHubTagWire(value: unknown): value is DockerHubTagWire {
  return isRecord(value) &&
    typeof value.name === 'string' &&
    (value.publishedAt === undefined || typeof value.publishedAt === 'string') &&
    typeof value.lastUpdated === 'string' &&
    typeof value.fullSize === 'number' &&
    Array.isArray(value.architectures) &&
    value.architectures.every((item) => typeof item === 'string')
}

function isDockerHubTagsWireResult(value: unknown): value is DockerHubTagsWireResult {
  return isRecord(value) &&
    typeof value.count === 'number' &&
    typeof value.page === 'number' &&
    typeof value.pageSize === 'number' &&
    Array.isArray(value.results) &&
    value.results.every(isDockerHubTagWire)
}

function isComposeProjectConfig(value: unknown): value is ComposeProjectConfig {
  return isRecord(value) &&
    typeof value.projectId === 'string' &&
    typeof value.workingDirectory === 'string' &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.files) &&
    value.files.every((file) => isRecord(file) && typeof file.path === 'string' && typeof file.name === 'string' && typeof file.content === 'string' && typeof file.size === 'number')
}

function isComposeValidationResult(value: unknown): value is ComposeValidationResult {
  return isRecord(value) &&
    value.valid === true &&
    Array.isArray(value.services) &&
    value.services.every((service) => typeof service === 'string') &&
    typeof value.normalized === 'string'
}
