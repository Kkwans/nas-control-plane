export {
  bootstrapRoot,
  loginRoot,
  logoutRoot,
  requestAuthStatus,
} from './auth'

export {
  confirmDNSChange,
  detectPublicEgress,
  inspectMihomo,
  invokeMihomo,
  previewDNSChange,
  requestCapabilities,
  requestDNSCapability,
  requestMihomoCapability,
  requestPublicEgressCapability,
  requestSystemDetails,
  requestSystemSummary,
  rollbackDNSChange,
  subscribeSystemEvents,
} from './systemInfo'

export type {
  DNSChangeConfirmation,
  DNSChangePreview,
  DNSChangeRequest,
  DNSChangeResult,
} from './systemInfo'

export {
  cancelJob,
  createDockerContainer,
  deleteJob,
  deleteDockerProject,
  deployComposeConfig,
  followJob,
  pullDockerImage,
  readComposeConfig,
  removeDockerImage,
  removeDockerImages,
  requestContainerAction,
  requestContainerDetails,
  requestContainerLogs,
  requestDockerHubTags,
  requestDockerInventory,
  requestDockerResources,
  requestDockerImages,
  requestDockerProjectAction,
  requestJobs,
  requestServices,
  retryJob,
  searchDockerHub,
  validateComposeConfig,
} from './docker'

export { requestPathEntries } from './filesystem'

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
  configuredNameservers?: string[]
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
  asn?: string
  checkedAt: string
  detectionSource: string
  errorCode: string
}

export interface MihomoInspection {
  status: string
  capability: MihomoCapability
  localProxy: {
    address: string
    mode: string
  }
  strategy: {
    group: string
    selectedNode: string
    nodeType: string
    provider: string
  }
  node: {
    server: string
    port: number
    resolvedIp: string
    country: string
    region: string
    isp: string
    asn: string
  }
  publicEgress: PublicEgressResult
  checkedAt: string
  expiresAt: string
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
      lowerUp: boolean
      lowerUpKnown: boolean
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
      kind: 'physical' | 'emmc' | 'emmc-boot' | 'compressed-memory' | 'virtual'
      role: 'data' | 'system' | 'boot' | 'swap' | 'virtual'
      transport: string
      description: string
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
    startedAt?: string
    finishedAt?: string
    exitCode?: number
    restartCount?: number
    health?: 'none' | 'starting' | 'healthy' | 'unhealthy'
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

export interface DockerNetworkResource {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  subnets: string[]
  gateways: string[]
}

export interface DockerVolumeResource {
  name: string
  driver: string
  scope: string
  mountpoint: string
}

export interface DockerResources {
  collectedAt: string
  networks: DockerNetworkResource[]
  volumes: DockerVolumeResource[]
}

export type FileEntryType = 'directory' | 'file' | 'symlink' | 'other'

export interface FileEntry {
  name: string
  path: string
  type: FileEntryType
  readable: boolean
}

export interface FileEntriesPage {
  path: string
  parent: string
  entries: FileEntry[]
  nextCursor?: string
  collectedAt: string
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

export interface ContainerDetails {
  id: string
  name: string
  image: string
  state: string
  health?: 'none' | 'starting' | 'healthy' | 'unhealthy'
  healthFailingStreak: number
  createdAt?: string
  startedAt?: string
  finishedAt?: string
  exitCode: number
  restartCount: number
  oomKilled: boolean
  platform?: string
  driver?: string
  networkMode?: string
  restartPolicy?: string
  restartMaximumRetries: number
  autoRemove: boolean
  privileged: boolean
  readonlyRootfs: boolean
  nanoCpus: number
  memoryBytes: number
  ports: Array<{ hostIp: string; privatePort: number; publicPort: number; protocol: string }>
  mounts: Array<{ type: string; name?: string; source?: string; destination: string; driver?: string; readOnly: boolean }>
  networks: Array<{ name: string; ipAddress?: string; gateway?: string; ipv6Address?: string; macAddress?: string }>
}

export interface ContainerLogEntry {
  timestamp: string
  level: 'error' | 'warning' | 'info' | 'debug'
  stream: 'stdout' | 'stderr'
  message: string
  rawMessage?: string
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

export { NcpApiError } from './systemTransport'
