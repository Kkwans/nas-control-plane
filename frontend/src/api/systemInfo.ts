import {
  isApiErrorResponse,
  jsonRequest,
  NcpApiError,
  requestJson,
  requestOptions,
} from './systemTransport'
import type {
  CapabilityEvidence,
  DNSCapability,
  DockerInventory,
  MihomoCapability,
  MihomoInspection,
  PublicEgressCapability,
  PublicEgressResult,
  RealtimeSnapshot,
  SystemCapabilities,
  SystemDetails,
  SystemSummary,
  TailscaleCapability,
} from './system'

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

export async function previewDNSChange(input: DNSChangeRequest, fetcher: typeof fetch = fetch): Promise<DNSChangePreview> {
  return requestJson(
    '/api/v1/system/dns/preview',
    jsonRequest(input),
    isDNSChangePreview,
    fetcher,
    'SYSTEM_DNS_PREVIEW_RESPONSE_INVALID',
  )
}

export async function confirmDNSChange(input: DNSChangeConfirmation, fetcher: typeof fetch = fetch): Promise<DNSChangeResult> {
  return requestJson(
    '/api/v1/system/dns/confirm',
    jsonRequest(input),
    isDNSChangeResult,
    fetcher,
    'SYSTEM_DNS_CONFIRM_RESPONSE_INVALID',
  )
}

export async function rollbackDNSChange(changeId: string, fetcher: typeof fetch = fetch): Promise<DNSChangeResult> {
  return requestJson(
    '/api/v1/system/dns/rollback',
    jsonRequest({ changeId }),
    isDNSChangeResult,
    fetcher,
    'SYSTEM_DNS_ROLLBACK_RESPONSE_INVALID',
  )
}

export async function requestMihomoCapability(fetcher: typeof fetch = fetch): Promise<MihomoCapability> {
  return requestJson('/api/v1/proxy/mihomo/capability', {}, isMihomoCapability, fetcher, 'PROXY_MIHOMO_RESPONSE_INVALID')
}

export async function invokeMihomo(
  input: { operation: string; group?: string; proxy?: string },
  fetcher: typeof fetch = fetch,
): Promise<{ operation: string; statusCode: number; data: unknown }> {
  return requestJson(
    '/api/v1/proxy/mihomo/invoke',
    jsonRequest(input),
    isMihomoInvokeResult,
    fetcher,
    'PROXY_MIHOMO_RESPONSE_INVALID',
  )
}

export async function inspectMihomo(force = false, fetcher: typeof fetch = fetch): Promise<MihomoInspection> {
  return requestJson(
    '/api/v1/proxy/mihomo/inspect',
    jsonRequest({ force }),
    isMihomoInspection,
    fetcher,
    'PROXY_MIHOMO_INSPECTION_RESPONSE_INVALID',
  )
}

export async function requestPublicEgressCapability(fetcher: typeof fetch = fetch): Promise<PublicEgressCapability> {
  return requestJson(
    '/api/v1/system/public-egress/capability',
    {},
    isPublicEgressCapability,
    fetcher,
    'PUBLIC_EGRESS_RESPONSE_INVALID',
  )
}

export async function detectPublicEgress(fetcher: typeof fetch = fetch): Promise<PublicEgressResult> {
  const response = await fetcher('/api/v1/system/public-egress/detect', requestOptions({ method: 'POST' }))
  let payload: unknown
  try {
    payload = await response.json()
  } catch {
    throw new NcpApiError('PUBLIC_EGRESS_RESPONSE_INVALID', '公网出口检测返回了无法识别的数据。')
  }
  // A configured probe can legitimately return an explicit unavailable
  // result with HTTP 503. Preserve its stable errorCode instead of replacing
  // it with a generic service-unavailable message.
  if (isPublicEgressResult(payload)) return payload
  if (!response.ok && isApiErrorResponse(payload)) {
    throw new NcpApiError(payload.code, payload.message, payload.requestId)
  }
  throw new NcpApiError('PUBLIC_EGRESS_RESPONSE_INVALID', '公网出口检测返回了无法识别的数据。')
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
    (value.configuredNameservers === undefined || isStringArray(value.configuredNameservers)) &&
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
    Array.isArray(value.evidence) &&
    value.evidence.every(isCapabilityEvidence) &&
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
    (value.asn === undefined || typeof value.asn === 'string') &&
    typeof value.checkedAt === 'string' &&
    typeof value.detectionSource === 'string' &&
    typeof value.errorCode === 'string'
  )
}

function isMihomoInspection(value: unknown): value is MihomoInspection {
  return (
    isRecord(value) &&
    typeof value.status === 'string' &&
    isMihomoCapability(value.capability) &&
    isRecord(value.localProxy) &&
    typeof value.localProxy.address === 'string' &&
    typeof value.localProxy.mode === 'string' &&
    isRecord(value.strategy) &&
    typeof value.strategy.group === 'string' &&
    typeof value.strategy.selectedNode === 'string' &&
    typeof value.strategy.nodeType === 'string' &&
    typeof value.strategy.provider === 'string' &&
    isRecord(value.node) &&
    typeof value.node.server === 'string' &&
    typeof value.node.port === 'number' &&
    typeof value.node.resolvedIp === 'string' &&
    typeof value.node.country === 'string' &&
    typeof value.node.region === 'string' &&
    typeof value.node.isp === 'string' &&
    typeof value.node.asn === 'string' &&
    isPublicEgressResult(value.publicEgress) &&
    typeof value.checkedAt === 'string' &&
    typeof value.expiresAt === 'string' &&
    typeof value.errorCode === 'string'
  )
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
  return (
    isRecord(value) &&
    typeof value.collectedAt === 'string' &&
    isRecord(value.host) &&
    isRecord(value.cpu) &&
    isRecord(value.memory) &&
    Array.isArray(value.storage) &&
    Array.isArray(value.network) &&
    Array.isArray(value.warnings)
  )
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
  return (
    isRecord(value) &&
    typeof value.collectedAt === 'string' &&
    (value.summary === undefined || isSystemSummary(value.summary)) &&
    (value.docker === undefined || isDockerInventory(value.docker)) &&
    (value.errors === undefined || (Array.isArray(value.errors) && value.errors.every((item) => typeof item === 'string')))
  )
}
