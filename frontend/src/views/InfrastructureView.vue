<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElButton, ElDialog, ElInput, ElTooltip } from 'element-plus'
import {
  Activity,
  AlertTriangle,
  Boxes,
  Cpu,
  Database,
  Gauge,
  HardDrive,
  MemoryStick,
  Network,
  Pencil,
  Route,
  Router,
  Search,
  Server,
  Waypoints,
  Wifi,
} from '@lucide/vue'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import ActionButton from '@/components/ActionButton.vue'
import InfrastructureSignalSummary, { type InfrastructureSignal } from '@/components/InfrastructureSignalSummary.vue'
import NcpSelect from '@/components/NcpSelect.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { createRequestSequenceGate } from '@/domain/requestSequence'
import {
  classifyListenerScope,
  editableDNSNameservers,
  isAuxiliaryNetworkInterface,
  isSubscriptionStatusNodeName,
  networkInterfaceKindLabel,
  type ListenerScope,
  type ListenerScopePresentation,
} from '@/domain/network'
import {
  confirmDNSChange,
  inspectMihomo,
  previewDNSChange,
  requestDNSCapability,
  requestSystemDetails,
  rollbackDNSChange,
  type DNSCapability,
  type DNSChangePreview,
  type MihomoCapability,
  type MihomoInspection,
  type SystemDetails,
  type TailscaleCapability,
} from '@/api/system'
import { useSystemStore } from '@/stores/system'

type DetailTab = 'overview' | 'network' | 'storage' | 'services'
type ListenerScopeFilter = 'all' | 'exposed' | ListenerScope

interface ListeningPortGroup {
  port: number
  protocol: string
  addresses: string[]
  pids: number[]
  owners: Array<{ label: string; detail: string }>
  sources: string[]
  scope: ListenerScopePresentation
}

const systemStore = useSystemStore()
const details = ref<SystemDetails | null>(null)
const dnsCapability = ref<DNSCapability | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const activeTab = ref<DetailTab>('overview')
const dnsDraft = ref('')
const dnsPreview = ref<DNSChangePreview | null>(null)
const dnsChangeId = ref('')
const dnsMessage = ref('')
const dnsDialogOpen = ref(false)
const dnsPending = ref(false)
const mihomoInspection = ref<MihomoInspection | null>(null)
const publicEgressMessage = ref('')
const publicEgressLoading = ref(false)
const listenerQuery = ref('')
const listenerProtocol = ref('all')
const listenerScope = ref<ListenerScopeFilter>('all')
const listenerVisibleLimit = ref(24)
const detailsRequestGate = createRequestSequenceGate()
const mihomoRequestGate = createRequestSequenceGate()

const listenerProtocolOptions = [
  { label: '全部协议', value: 'all' },
  { label: 'TCP', value: 'tcp' },
  { label: 'UDP', value: 'udp' },
]
const listenerScopeOptions = computed(() => [
  { label: '全部范围', value: 'all' },
  { label: `对外监听 ${exposedListenerCount.value}`, value: 'exposed' },
  { label: `局域网 ${listenerScopeCount('lan')}`, value: 'lan' },
  { label: `仅本机 ${listenerScopeCount('loopback')}`, value: 'loopback' },
  { label: `Tailscale ${listenerScopeCount('overlay')}`, value: 'overlay' },
  { label: `容器网络 ${listenerScopeCount('container')}`, value: 'container' },
])

const dnsDetails = computed<DNSCapability>(() => dnsCapability.value ?? details.value?.dns ?? {
  backend: 'unknown', detected: false, state: 'unknown', readOnly: true, canRead: false,
  canPreview: false, canConfirm: false, canRollback: false, nameservers: [],
  configuredNameservers: [],
  detectionSource: '', errorCode: '',
})
const editableDNS = computed(() => editableDNSNameservers(dnsDetails.value))
const tailscaleDetails = computed<TailscaleCapability>(() => details.value?.tailscale ?? {
  detected: false, state: 'not-found', backendState: 'unknown', version: '', interface: '',
  overlayIps: [], online: false, linkState: 'unknown', heartbeatState: 'unknown', reachable: false,
  evidence: [], warnings: [],
})
const publicEgressDetails = computed(() => details.value?.publicEgress ?? {
  configured: false, status: 'unavailable', endpoint: '', requiresUserAction: true,
  detectionSource: '', errorCode: 'PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED',
})
const publicEgressResult = computed(() => mihomoInspection.value?.publicEgress ?? null)

const tabs: Array<{ id: DetailTab; label: string; icon: typeof Server }> = [
  { id: 'overview', label: '设备概览', icon: Server },
  { id: 'network', label: '网络与代理', icon: Network },
  { id: 'storage', label: '存储与磁盘', icon: HardDrive },
  { id: 'services', label: '服务与能力', icon: Waypoints },
]

const headerStats = computed<WorkspaceStat[]>(() => [
  { label: '系统架构', value: details.value?.device.architecture || systemStore.summary?.host.architecture || '—' },
  { label: '网络接口', value: details.value?.network.interfaces.length ?? '—' },
  { label: '挂载点', value: details.value?.storage.mounts.length ?? '—' },
])

const networkInterfaces = computed(() => details.value?.network.interfaces ?? [])
const primaryInterfaces = computed(() => {
  const candidates = networkInterfaces.value.filter((item) => item.name !== 'lo' && !isAuxiliaryNetworkInterface(item.name))
  const tailscale = candidates.filter((item) => /^tailscale/i.test(item.name))
  const physical = candidates
    .filter((item) => !/^tailscale/i.test(item.name))
    .sort((left, right) => Number(interfaceIsOnline(right)) - Number(interfaceIsOnline(left)))
  const reserved = Math.min(tailscale.length, 1)
  return [...physical.slice(0, 4 - reserved), ...tailscale.slice(0, reserved)]
})
const primaryInterfaceNames = computed(() => new Set(primaryInterfaces.value.map((item) => item.name)))
const secondaryInterfaces = computed(() => networkInterfaces.value.filter((item) => !primaryInterfaceNames.value.has(item.name)))
const primaryActiveInterfaceCount = computed(() => primaryInterfaces.value.filter(interfaceIsOnline).length)
const infrastructureSignals = computed<InfrastructureSignal[]>(() => [
  {
    label: '主网络',
    value: primaryActiveInterfaceCount.value ? `${primaryActiveInterfaceCount.value} 个接口在线` : '未连接',
    detail: details.value?.network.gateway ? `默认出口 ${details.value.network.gateway}` : '默认出口未发现',
    icon: Wifi,
    tone: primaryActiveInterfaceCount.value ? 'success' : 'warning',
  },
  {
    label: 'DNS 服务',
    value: dnsDetails.value.nameservers.length ? `${dnsDetails.value.nameservers.length} 个服务` : '未读取',
    detail: dnsDetails.value.readOnly ? '当前只读展示' : '支持预览与回滚',
    icon: Network,
    tone: dnsDetails.value.nameservers.length ? 'success' : 'warning',
  },
  {
    label: 'Tailscale',
    value: tailscaleStatusLabel(),
    detail: tailscaleDetails.value.overlayIps.join('、') || tailscaleEvidenceLabel(),
    icon: Waypoints,
    tone: tailscaleDetails.value.reachable ? 'success' : tailscaleDetails.value.detected ? 'warning' : 'neutral',
  },
  {
    label: 'Mihomo / 公网出口',
    value: proxyStateLabel(details.value?.proxy.mihomo.state ?? 'unknown', details.value?.proxy.mihomo.detected ?? false),
    detail: publicEgressResult.value?.address || (publicEgressDetails.value.configured ? '公网出口待检查' : '未配置探针'),
    icon: Router,
    tone: details.value?.proxy.mihomo.detected ? 'success' : 'neutral',
  },
])
const volumeMounts = computed(() => (details.value?.storage.mounts ?? []).filter((item) => item.path === '/' || item.path.startsWith('/volume')))
const auxiliaryMounts = computed(() => (details.value?.storage.mounts ?? []).filter((item) => !volumeMounts.value.some((volume) => volume.path === item.path)))
const volumeTotalBytes = computed(() => volumeMounts.value.reduce((total, item) => total + item.totalBytes, 0))
const volumeUsedBytes = computed(() => volumeMounts.value.reduce((total, item) => total + item.usedBytes, 0))
const volumeUsedPercent = computed(() => volumeTotalBytes.value ? volumeUsedBytes.value / volumeTotalBytes.value * 100 : 0)
const physicalDisks = computed(() => (details.value?.storage.disks ?? []).filter((item) => (
  item.kind ? item.kind === 'physical' : /^(sd[a-z]|hd[a-z]|nvme\d+n\d+)$/i.test(item.name)
)))
const auxiliaryDisks = computed(() => (details.value?.storage.disks ?? []).filter((item) => (
  !/^md\d+$/i.test(item.name) && !physicalDisks.value.some((disk) => disk.name === item.name)
)))
const listeningPortGroups = computed(() => {
  const groups = new Map<string, Omit<ListeningPortGroup, 'scope'>>()
  for (const item of details.value?.network.listeningPorts ?? []) {
    const key = `${item.protocol}:${item.port}`
    const group = groups.get(key) ?? { port: item.port, protocol: item.protocol, addresses: [], pids: [], owners: [], sources: [] }
    if (item.address && !group.addresses.includes(item.address)) group.addresses.push(item.address)
    if (item.pid && !group.pids.includes(item.pid)) group.pids.push(item.pid)
    for (const source of [...(item.detectionSources ?? []), item.detectionSource ?? ''].filter(Boolean)) {
      if (!group.sources.includes(source)) group.sources.push(source)
    }
    for (const owner of listeningPortOwners(item)) {
      if (!group.owners.some((current) => current.label === owner.label && current.detail === owner.detail)) group.owners.push(owner)
    }
    groups.set(key, group)
  }
  return [...groups.values()]
    .map<ListeningPortGroup>((group) => ({
      ...group,
      scope: classifyListenerScope(group.addresses, tailscaleDetails.value.overlayIps),
    }))
    .sort((left, right) => left.scope.rank - right.scope.rank || left.port - right.port)
})
const filteredListeningPortGroups = computed(() => {
  const query = listenerQuery.value.trim().toLocaleLowerCase('zh-CN')
  return listeningPortGroups.value.filter((item) => {
    if (listenerProtocol.value !== 'all' && item.protocol.toLowerCase() !== listenerProtocol.value) return false
    if (listenerScope.value === 'exposed' && !['public', 'all-interfaces'].includes(item.scope.value)) return false
    if (listenerScope.value !== 'all' && listenerScope.value !== 'exposed' && item.scope.value !== listenerScope.value) return false
    if (!query) return true
    return [
      String(item.port), item.protocol, ...item.addresses, ...item.sources,
      ...item.owners.flatMap((owner) => [owner.label, owner.detail]),
    ].some((value) => value.toLocaleLowerCase('zh-CN').includes(query))
  })
})
const visibleListeningPortGroups = computed(() => filteredListeningPortGroups.value.slice(0, listenerVisibleLimit.value))
const exposedListenerCount = computed(() => listeningPortGroups.value.filter((item) => ['public', 'all-interfaces'].includes(item.scope.value)).length)
const localListenerCount = computed(() => listeningPortGroups.value.filter((item) => item.scope.value === 'loopback').length)
const listenerResultLabel = computed(() => filteredListeningPortGroups.value.length === listeningPortGroups.value.length
  ? `共 ${listeningPortGroups.value.length} 个监听端口`
  : `筛选出 ${filteredListeningPortGroups.value.length} / ${listeningPortGroups.value.length} 个端口`)

watch([listenerQuery, listenerProtocol, listenerScope], () => { listenerVisibleLimit.value = 24 })

const capabilityItems = computed(() => [
  { name: 'Docker Engine', enabled: Boolean(systemStore.capabilities?.docker), detail: systemStore.inventory?.engine.serverVersion ? `版本 ${systemStore.inventory.engine.serverVersion}` : '等待检测', icon: Boxes, type: 'docker' },
  { name: 'Docker Compose', enabled: Boolean(systemStore.capabilities?.compose), detail: `已发现 ${systemStore.services.length} 个项目`, icon: Database, type: 'database' },
  { name: 'systemd', enabled: Boolean(systemStore.capabilities?.systemd), detail: '宿主机服务管理', icon: Server, type: 'system' },
  { name: '系统日志服务（journald）', enabled: Boolean(systemStore.capabilities?.journald), detail: 'Linux 系统与服务日志', icon: Activity, type: 'system' },
  { name: '数据卷', enabled: Boolean(systemStore.capabilities?.dataVolumes?.length), detail: systemStore.capabilities?.dataVolumes?.join('、') || '未发现', icon: HardDrive, type: 'storage' },
  { name: '网络接口', enabled: Boolean(systemStore.capabilities?.networkInterfaces?.length), detail: `${systemStore.capabilities?.networkInterfaces?.length ?? 0} 个接口`, icon: Network, type: 'network' },
])

const mihomoCapability = computed<MihomoCapability | null>(() => mihomoInspection.value?.capability ?? details.value?.proxy.mihomoCapability ?? null)
const mihomoController = computed(() => mihomoCapability.value?.controller ?? null)
const mihomoOperations = computed(() => mihomoController.value?.operations ?? [])
const mihomoRulesEvidence = computed(() => mihomoCapability.value?.evidence.find((item) => item.source === 'controller-api' && item.detail === '/rules'))
const mihomoRulesReadable = computed(() => mihomoRulesEvidence.value?.status === 'reachable')

async function loadDetails() {
  const requestSequence = detailsRequestGate.begin()
  mihomoRequestGate.invalidate()
  publicEgressLoading.value = false
  mihomoInspection.value = null
  loading.value = true
  errorMessage.value = ''
  try {
    const [systemDetails, liveDNSCapability] = await Promise.all([
      requestSystemDetails(),
      requestDNSCapability().catch(() => null),
    ])
    if (!detailsRequestGate.isLatest(requestSequence)) return
    details.value = systemDetails
    dnsCapability.value = liveDNSCapability
    dnsDraft.value = editableDNS.value.join(', ')
    dnsPreview.value = null
    dnsMessage.value = ''
    publicEgressMessage.value = ''
    mihomoRequestGate.invalidate()
    void checkMihomo(false)
  } catch (error) {
    if (detailsRequestGate.isLatest(requestSequence)) {
      errorMessage.value = error instanceof Error ? error.message : '系统详情暂不可用'
    }
  } finally {
    if (detailsRequestGate.isLatest(requestSequence)) loading.value = false
  }
}

function dnsServersFromDraft() {
  return [...new Set(dnsDraft.value.split(/[\s,，]+/).map((value) => value.trim()).filter(Boolean))]
}

async function previewDNS() {
  if (!dnsDetails.value.canPreview || dnsDetails.value.readOnly || dnsPending.value) return
  const nameservers = dnsServersFromDraft()
  if (!nameservers.length) {
    dnsMessage.value = '至少填写一个 DNS 地址。'
    return
  }
  if (dnsDetails.value.backend === 'ugos-network-service' && nameservers.length > 2) {
    dnsMessage.value = 'UGOS 最多支持配置 2 个 DNS 服务器。'
    return
  }
  dnsMessage.value = ''
  dnsPending.value = true
  try {
    dnsPreview.value = await previewDNSChange({ nameservers })
    dnsMessage.value = dnsPreview.value.requiresConfirm ? '预览已生成，确认后才会应用。' : '当前后端不要求二次确认。'
  } catch (error) {
    dnsMessage.value = error instanceof Error ? error.message : 'DNS 预览失败。'
  } finally {
    dnsPending.value = false
  }
}

async function confirmDNS() {
  if (!dnsPreview.value || dnsPending.value) return
  const previewId = dnsPreview.value.previewId
  dnsPending.value = true
  try {
    const result = await confirmDNSChange({ previewId, confirmed: true })
    const rollbackId = result.applied && result.rollbackAvailable ? result.changeId : ''
    await loadDetails()
    dnsChangeId.value = rollbackId
    dnsMessage.value = result.applied ? 'DNS 已应用。' : `DNS 未应用（${result.errorCode || '未知原因'}）。`
  } catch (error) {
    dnsMessage.value = error instanceof Error ? error.message : 'DNS 应用失败。'
  } finally {
    dnsPending.value = false
  }
}

async function rollbackDNS() {
  if (!dnsChangeId.value || dnsPending.value) return
  dnsPending.value = true
  try {
    const result = await rollbackDNSChange(dnsChangeId.value)
    await loadDetails()
    dnsChangeId.value = ''
    dnsMessage.value = !result.applied && !result.rollbackAvailable ? 'DNS 已回滚。' : `DNS 未回滚（${result.errorCode || '未知原因'}）。`
  } catch (error) {
    dnsMessage.value = error instanceof Error ? error.message : 'DNS 回滚失败。'
  } finally {
    dnsPending.value = false
  }
}

function openDNSEditor() {
  dnsDraft.value = editableDNS.value.join(', ')
  dnsPreview.value = null
  dnsMessage.value = ''
  dnsDialogOpen.value = true
}

function invalidateDNSPreview() {
  dnsPreview.value = null
  dnsMessage.value = ''
}

async function checkMihomo(force: boolean) {
  if (publicEgressLoading.value) return
  const requestSequence = mihomoRequestGate.begin()
  publicEgressMessage.value = ''
  publicEgressLoading.value = true
  try {
    const inspection = await inspectMihomo(force)
    if (!mihomoRequestGate.isLatest(requestSequence)) return
    mihomoInspection.value = inspection
    if (inspection.errorCode) publicEgressMessage.value = mihomoInspectionErrorMessage(inspection.errorCode)
  } catch (error) {
    if (!mihomoRequestGate.isLatest(requestSequence)) return
    const code = typeof error === 'object' && error && 'code' in error ? String(error.code) : ''
    publicEgressMessage.value = code ? mihomoInspectionErrorMessage(code) : error instanceof Error ? error.message : '代理链路检查失败。'
  } finally {
    if (mihomoRequestGate.isLatest(requestSequence)) publicEgressLoading.value = false
  }
}

function handleManualRefresh() {
  void loadDetails()
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size >= 100 || index === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[index]}`
}

function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  return [days ? `${days} 天` : '', hours ? `${hours} 小时` : '', !days && minutes ? `${minutes} 分钟` : ''].filter(Boolean).join(' ')
}

function formatTemperature(value: number) {
  return Number.isFinite(value) && value > 0 ? `${value.toFixed(1)} °C` : '不可用'
}

function interfaceIsOnline(item: SystemDetails['network']['interfaces'][number]) {
  if (item.lowerUpKnown === true) return item.lowerUp === true && (!item.state || item.state === 'up' || item.state === 'unknown')
  return item.state === 'up'
}

function interfaceStateLabel(item: SystemDetails['network']['interfaces'][number]) {
  if (interfaceIsOnline(item) && /^tailscale/i.test(item.name)) return 'Overlay 可用'
  return interfaceIsOnline(item) ? '已连接' : '未连接'
}

function interfaceAddress(item: SystemDetails['network']['interfaces'][number]) {
  return item.addresses.find((address) => address.family === 'ipv4') ?? item.addresses[0]
}

function listeningPortOwners(item: SystemDetails['network']['listeningPorts'][number]) {
  if (item.containerName) {
    return [{ label: item.containerName, detail: item.processName ? `Docker 容器 · ${item.processName}` : 'Docker 容器' }]
  }
  if (item.containerId) {
    return [{ label: shortIdentifier(item.containerId), detail: `Docker 容器名未知 · ${portDetectionReason(item)}` }]
  }
  if (item.systemdUnit) {
    return [{ label: item.systemdUnit.replace(/\.service$/, ''), detail: item.processName ? `系统服务 · ${item.processName}` : 'systemd 服务' }]
  }
  if (item.processName) return [{ label: item.processName, detail: item.service && item.service !== item.processName ? item.service : '进程' }]
  if (item.executable) return [{ label: item.executable, detail: '可执行文件' }]
  return [{ label: item.pid > 0 ? `PID ${item.pid}` : '未知监听者', detail: portDetectionReason(item) }]
}

function shortIdentifier(value: string) {
  return value.length > 12 ? value.slice(0, 12) : value
}

function portDetectionReason(item: SystemDetails['network']['listeningPorts'][number]) {
  switch (item.detectionErrorCode) {
    case 'LISTENING_PORT_PID_UNAVAILABLE': return 'PID 不可用'
    case 'LISTENING_PORT_CONTAINER_NAME_UNAVAILABLE': return 'Docker CLI 未返回容器名称'
    case 'LISTENING_PORT_PROCESS_METADATA_UNAVAILABLE': return 'proc 元数据不可读'
    case 'LISTENING_PORT_PROCESS_METADATA_EMPTY': return 'proc 元数据为空'
    case 'LISTENING_PORT_PROCESS_METADATA_PARTIAL': return 'proc 元数据不完整'
    default: return item.detectionErrorCode || '映射原因未知'
  }
}

function hasMihomoOperation(operation: string) {
  return mihomoOperations.value.includes(operation)
}

function mihomoControllerHealth() {
  if (!mihomoController.value?.detected) return '未确认'
  if (mihomoController.value.authRequired) return mihomoController.value.tokenConfigured ? '认证未通过' : '需要认证'
  return mihomoController.value.reachable ? '已连接' : '不可达'
}

function mihomoRulesStatus() {
  if (mihomoRulesReadable.value) return '读取 API 已确认'
  return '未确认'
}

function tailscaleStatusLabel() {
  if (tailscaleDetails.value.reachable) return 'Overlay 已连接'
  if (!tailscaleDetails.value.detected) return '未发现 Tailscale'
  if (/stopped|needslogin|nostate/i.test(tailscaleDetails.value.backendState)) return '控制面未运行或需要登录'
  if (!tailscaleDetails.value.online) return '节点未在线'
  if (!tailscaleDetails.value.overlayIps.length) return '未取得 Overlay IP'
  if (tailscaleDetails.value.linkState === 'down') return '主机链路未建立'
  return '证据不足，未判定为已连接'
}

function tailscaleEvidenceLabel() {
  const parts = [
    tailscaleDetails.value.backendState ? `控制面 ${tailscaleDetails.value.backendState}` : '',
    tailscaleDetails.value.linkState ? `链路 ${tailscaleDetails.value.linkState}` : '',
    tailscaleDetails.value.heartbeatState ? `心跳 ${tailscaleDetails.value.heartbeatState}` : '',
  ].filter(Boolean)
  return parts.join(' · ') || '未取得控制面、链路或心跳证据'
}

function dnsCapabilityExplanation() {
  if (!dnsDetails.value.readOnly && dnsDetails.value.backend === 'ugos-network-service') return '通过 UGOS 官方网络服务预览和应用；修改前保存完整配置，且支持并发变更保护与一键回滚。'
  if (!dnsDetails.value.readOnly && dnsDetails.value.backend === 'static-resolv-conf') return '修改前自动备份，确认后原子应用；若配置未被其他进程改动，可一键回滚。'
  if (dnsDetails.value.errorCode === 'DNS_BACKEND_READ_ONLY') return '检测到静态 /etc/resolv.conf，未发现可管理的 systemd-resolved 或 NetworkManager；保持只读。'
  if (dnsDetails.value.errorCode === 'UGOS_DNS_WRITE_UNCONFIRMED') return '已检测到 UGOS 网络服务，但当前固件拒绝了受控写入；为避免伪成功，DNS 保持只读，请在 UGOS 网络设置中修改。'
  if (dnsDetails.value.errorCode === 'DNS_WRITE_ADAPTER_UNAVAILABLE') return '检测到 DNS 后端，但未接入安全的预览、应用和回滚适配器；保持只读。'
  if (dnsDetails.value.readOnly) return '当前 DNS 后端只提供读取能力；NCP 不会直接覆盖 /etc/resolv.conf。'
  return dnsDetails.value.detectionSource || 'DNS 能力未报告'
}

function publicEgressErrorMessage(code: string) {
  switch (code) {
    case 'PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED': return '尚未配置公网出口检测端点。'
    case 'PUBLIC_EGRESS_ENDPOINT_INVALID': return '公网出口检测端点配置无效。'
    case 'PUBLIC_EGRESS_ENDPOINT_UNAVAILABLE': return 'Root Agent 无法连接公网出口检测端点，请检查 Mihomo 出站代理。'
    case 'PUBLIC_EGRESS_RESPONSE_INVALID': return '检测端点没有返回有效的公网 IP。'
    case 'PUBLIC_EGRESS_CHECK_CANCELED': return '公网出口检测已取消。'
    default: return '公网出口检测失败，请稍后重试。'
  }
}

function mihomoInspectionErrorMessage(code: string) {
  switch (code) {
    case 'MIHOMO_CONTROLLER_UNAVAILABLE': return 'Mihomo Controller 当前不可达，已保留进程和配置检测结果。'
    case 'MIHOMO_PROXIES_UNAVAILABLE': return 'Controller 可达，但未能读取当前策略组与节点。'
    case 'MIHOMO_STRATEGY_UNAVAILABLE': return '未能从 Controller 响应中确定当前策略组。'
    case 'MIHOMO_NODE_ADDRESS_UNRESOLVED': return '已识别当前节点，但节点服务器地址暂时无法解析。'
    case 'PROXY_MIHOMO_INSPECTION_UNAVAILABLE': return 'Root Agent 暂未提供代理链路检查能力。'
    default: return publicEgressErrorMessage(code)
  }
}

function publicEgressAddressLabel() {
  if (publicEgressLoading.value) return '正在检测…'
  if (!publicEgressResult.value) return publicEgressDetails.value.configured ? '待手动检测' : '未配置探针'
  return publicEgressResult.value.address || '探针未返回公网 IP'
}

function mihomoModeLabel(value: string | undefined) {
  if (value === 'rule') return '规则模式'
  if (value === 'global') return '全局模式'
  if (value === 'direct') return '直连模式'
  return '模式未确认'
}

function nodeLocationLabel() {
  const node = mihomoInspection.value?.node
  if (!node) return '等待检查'
  return [node.country, node.region].filter(Boolean).join(' · ') || (node.server ? '入口地区需解析后确认' : '等待检查')
}

function strategySelectionDetail() {
  const strategy = mihomoInspection.value?.strategy
  if (!strategy) return '策略组待确认'
  const parts = [strategy.group, strategy.provider, strategy.nodeType ? strategy.nodeType.toUpperCase() : ''].filter(Boolean)
  if (strategy.selectedNode && isSubscriptionStatusNodeName(strategy.selectedNode)) parts.push('名称来自订阅状态提示')
  return parts.join(' · ') || '策略组待确认'
}

function nodeEndpointEvidence() {
  const node = mihomoInspection.value?.node
  if (!node?.server) return '入口 IP 与地区待确认'
  const address = node.resolvedIp || '由 Mihomo 连接时解析'
  return `${address} · ${nodeLocationLabel()}`
}

function proxyRouteExplanation() {
  if (mihomoInspection.value?.localProxy.mode === 'rule') {
    return '公网出口来自本次真实代理请求；规则模式下，不同域名或应用可能命中不同策略，因此“默认策略选择”不代表所有连接。'
  }
  return '公网出口来自本次真实代理请求；节点入口是连接代理节点所用的地址，两者不是同一个概念。'
}

function egressLocationLabel() {
  const value = publicEgressResult.value
  if (!value) return '等待检查'
  return [value.country, value.region].filter(Boolean).join(' · ') || '地区未返回'
}

function proxyStateLabel(value: string, detected: boolean) {
  if (detected && value === 'running') return '代理核心运行中'
  if (value === 'not-found') return '未发现代理核心'
  return detected ? '已发现代理核心' : '代理状态未知'
}

function diskKind(rotational: boolean) {
  return rotational ? '机械硬盘' : '固态 / 闪存'
}

function blockDeviceKindLabel(disk: SystemDetails['storage']['disks'][number]) {
  switch (disk.kind) {
    case 'physical': return disk.rotational ? '机械数据盘' : '固态数据盘'
    case 'emmc': return '系统 eMMC'
    case 'emmc-boot': return 'eMMC 启动区'
    case 'compressed-memory': return '压缩内存交换设备'
    case 'virtual': return '系统虚拟设备'
    default: return diskKind(disk.rotational)
  }
}

function blockDeviceDescription(disk: SystemDetails['storage']['disks'][number]) {
  return disk.description || `${blockDeviceKindLabel(disk)}，用途由系统管理`
}

function blockDeviceTransport(disk: SystemDetails['storage']['disks'][number]) {
  if (!disk.transport) return '接口未知'
  if (disk.transport === 'memory') return '内存'
  if (disk.transport === 'emmc') return 'eMMC'
  if (disk.transport === 'block') return '块设备'
  return disk.transport.toUpperCase()
}

function listeningSourceLabel(sources: string[]) {
  const labels = sources.map((source) => {
    if (/docker/i.test(source)) return 'Docker 容器映射'
    if (/systemd|proc|cgroup/i.test(source)) return '进程与系统服务'
    if (/gopsutil|connection|socket/i.test(source)) return '系统连接表'
    return source
  }).filter(Boolean)
  return [...new Set(labels)].join('、') || '系统监听信息'
}

function listenerScopeCount(scope: ListenerScope) {
  return listeningPortGroups.value.filter((item) => item.scope.value === scope).length
}

function showMoreListeningPorts() {
  listenerVisibleLimit.value += 24
}

function formatTime(value: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

onMounted(() => {
  void loadDetails()
  window.addEventListener('ncp:manual-refresh', handleManualRefresh)
})

onBeforeUnmount(() => window.removeEventListener('ncp:manual-refresh', handleManualRefresh))
</script>

<template>
  <div class="page workspace-page system-details-page">
    <WorkspaceHeader title="系统信息" description="集中查看设备、硬件、网络、存储与控制链路" :icon="Server" :stats="headerStats">
      <template #filters>
        <div class="system-tabs-toolbar">
          <nav class="detail-tabs" aria-label="系统信息分类">
            <button
              v-for="tab in tabs"
              :key="tab.id"
              type="button"
              :class="{ active: activeTab === tab.id }"
              @click="activeTab = tab.id"
            >
              <component :is="tab.icon" :size="16" />
              {{ tab.label }}
            </button>
          </nav>
        </div>
      </template>
    </WorkspaceHeader>

    <section v-if="loading && !details" class="details-skeleton panel" aria-label="正在加载系统详情">
      <span v-for="index in 8" :key="index"></span>
    </section>

    <section v-else-if="errorMessage && !details" class="details-error panel">
      <AlertTriangle :size="24" />
      <div><strong>系统详情暂不可用</strong><p>{{ errorMessage }}</p></div>
      <ElButton @click="loadDetails">重试</ElButton>
    </section>

    <template v-else-if="details">
      <div v-if="details.warnings.length" class="warning-strip">
        <AlertTriangle :size="17" />
        <span>{{ details.warnings.join('；') }}</span>
      </div>

      <section v-if="activeTab === 'overview'" class="overview-layout">
        <article class="device-summary panel">
          <div class="device-summary__icon"><Server :size="31" /></div>
          <div class="device-summary__identity">
            <span>当前设备</span>
            <h2>{{ details.device.model || details.device.hostname || 'NAS 主机' }}</h2>
            <p>{{ details.device.hostname }} · {{ details.device.operatingSystem }} · {{ details.device.architecture }}</p>
          </div>
          <div class="device-summary__health">
            <span><i></i>控制链路正常</span>
            <small>{{ details.control.nodes.length }} 个节点已纳入检测</small>
          </div>
        </article>

        <InfrastructureSignalSummary :signals="infrastructureSignals" />

        <article class="panel overview-facts">
          <div><span>系统版本</span><strong>{{ details.device.operatingSystem || '不可用' }}</strong></div>
          <div><span>内核版本</span><strong>{{ details.device.kernelVersion || '不可用' }}</strong></div>
          <div><span>系统架构</span><strong>{{ details.device.architecture || '不可用' }}</strong></div>
          <div><span>运行时间</span><strong>{{ formatDuration(details.device.uptimeSeconds) }}</strong></div>
          <div><span>运行进程</span><strong>{{ details.device.processCount.toLocaleString('zh-CN') }}</strong></div>
          <div class="overview-fact--cgroup"><span>资源控制（cgroup）</span><strong>{{ details.device.cgroupVersion || '不可用' }}</strong><small>{{ details.device.cgroupVersion === 'v2' ? 'v2 使用统一层级和控制器接口，负责统计、限制与委派进程资源。' : 'Linux 控制组用于统计和限制进程资源；当前版本决定可用的控制器接口。' }}</small></div>
        </article>

        <article class="panel overview-section overview-section--processor">
          <header><Cpu :size="18" /><div><h2>处理器信息</h2><p>设备识别到的静态 CPU 信息</p></div></header>
          <div class="processor-facts">
            <div class="processor-facts__model"><span>型号</span><strong>{{ details.hardware.cpu.model || '不可用' }}</strong></div>
            <div><span>核心</span><strong>{{ details.hardware.cpu.physicalCores || '—' }} 物理 / {{ details.hardware.cpu.logicalCores || '—' }} 逻辑</strong></div>
            <div><span>频率</span><strong>{{ details.hardware.cpu.frequencyMHz ? `${details.hardware.cpu.frequencyMHz.toFixed(0)} MHz` : '不可用' }}</strong></div>
          </div>
        </article>

        <article class="panel overview-section overview-section--resources">
          <header><Gauge :size="18" /><div><h2>资源概览</h2><p>容量类数据放在这里，实时变化请前往系统监控</p></div></header>
          <div class="resource-summary">
            <div><MemoryStick :size="18" /><span>内存容量</span><strong>{{ formatBytes(details.hardware.memory.totalBytes) }}</strong></div>
            <div><Wifi :size="18" /><span>主网络连接</span><strong>{{ primaryActiveInterfaceCount }}</strong></div>
            <div><HardDrive :size="18" /><span>存储卷</span><strong>{{ volumeMounts.length }}</strong></div>
          </div>
        </article>
      </section>

      <section v-else-if="activeTab === 'network'" class="network-layout">
        <article class="network-summary-grid network-layout__full">
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Wifi :size="18" /></span><div><small>当前联网</small><strong>{{ primaryActiveInterfaceCount }} 个主接口</strong><p>{{ primaryInterfaces.map((item) => item.name).join('、') || '未发现主网络接口' }}</p></div></div>
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Route :size="18" /></span><div><small>默认出口</small><strong>{{ details.network.gateway || '未发现' }}</strong><p>路由 {{ details.network.routes.length }} 条</p></div></div>
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Network :size="18" /></span><div><small>DNS 服务</small><strong>{{ details.network.dnsServers.length || 0 }} 个</strong><p>{{ details.network.dnsServers.slice(0, 2).join('、') || '未发现解析服务' }}</p></div></div>
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Gauge :size="18" /></span><div><small>监听服务</small><strong>{{ listeningPortGroups.length }} 个端口</strong><p>{{ exposedListenerCount }} 个可能对外可达 · {{ localListenerCount }} 个仅本机</p></div></div>
        </article>

        <article class="panel detail-card network-layout__full">
          <header class="detail-card__header type-site"><Network :size="20" /><div><h2>主要网络连接</h2><p>展示主机和 Tailscale Overlay 接口；Docker 等内部接口收进下方明细</p></div></header>
          <div v-if="primaryInterfaces.length" class="primary-interface-list">
            <div v-for="item in primaryInterfaces" :key="item.name" class="primary-interface-row">
              <span class="primary-interface-row__status" :class="{ online: interfaceIsOnline(item) }"><i></i>{{ interfaceStateLabel(item) }}</span>
              <div class="primary-interface-row__name"><strong>{{ item.name }}</strong><small>{{ networkInterfaceKindLabel(item.name) }}</small></div>
              <div><span>IP 地址</span><strong>{{ interfaceAddress(item) ? `${interfaceAddress(item)?.address}/${interfaceAddress(item)?.prefixLength}` : '未分配' }}</strong></div>
              <div><span>链路</span><strong>{{ item.speedMbps > 0 ? `${item.speedMbps} Mbps` : '速率未知' }}<template v-if="item.duplex"> · {{ item.duplex }}</template></strong></div>
            </div>
          </div>
          <div v-else class="inline-empty">未发现可用于联网的主机接口。</div>
          <details v-if="secondaryInterfaces.length" class="network-details">
            <summary>查看所有接口明细 <span>{{ secondaryInterfaces.length }} 个虚拟 / 辅助接口</span></summary>
            <div class="network-details__list">
              <div v-for="item in secondaryInterfaces" :key="item.name" class="network-detail-row">
                <div><strong>{{ item.name }}</strong><small>{{ networkInterfaceKindLabel(item.name) }}</small></div>
                <span :class="{ 'is-online': interfaceIsOnline(item) }">{{ interfaceStateLabel(item) }}</span>
                <span>{{ item.addresses.length }} 个地址</span>
                <code>{{ item.hardwareAddress || '无 MAC' }}</code>
              </div>
            </div>
          </details>
        </article>

        <article class="panel detail-card network-layout__full dns-workspace">
          <SectionHeader class="detail-card__section-header" title="路由与 DNS" description="默认网关保持只读；DNS 由已确认的系统后端安全管理" :icon="Route">
            <template #actions>
              <ActionButton
                v-if="dnsDetails.canPreview && dnsDetails.canConfirm && dnsDetails.canRollback && !dnsDetails.readOnly"
                size="sm"
                :icon="Pencil"
                @click="openDNSEditor"
              >编辑 DNS</ActionButton>
            </template>
          </SectionHeader>
          <dl class="definition-grid">
            <div class="definition-grid__wide"><dt>默认网关</dt><dd>{{ details.network.gateway || '未发现' }}</dd></div>
            <div class="definition-grid__wide"><dt>DNS</dt><dd>{{ details.network.dnsServers.join('、') || '未发现' }}</dd></div>
            <div><dt>路由数量</dt><dd>{{ details.network.routes.length }}</dd></div>
            <div><dt>默认出口</dt><dd>{{ details.network.routes.find((route) => route.destination === '0.0.0.0/0')?.interface || '未识别' }}</dd></div>
          </dl>
          <div class="dns-management">
            <div class="dns-management__state">
              <span :class="['capability-state', { off: !dnsDetails.detected || dnsDetails.readOnly }]"><i></i>{{ dnsDetails.readOnly ? '只读展示' : dnsDetails.detected ? '支持安全修改' : '未检测到可管理后端' }}</span>
              <small>{{ dnsCapabilityExplanation() }}</small>
            </div>
            <div class="dns-current-grid">
              <div class="dns-current-value"><span>当前生效 DNS</span><code>{{ dnsDetails.nameservers.join('、') || '未读取到 DNS 地址' }}</code></div>
              <div v-if="!dnsDetails.readOnly" class="dns-current-value dns-current-value--managed"><span>UGOS 手动配置</span><code>{{ dnsDetails.configuredNameservers?.join('、') || '未读取到可编辑配置' }}</code></div>
            </div>
          </div>
        </article>

        <article class="panel detail-card proxy-workspace network-layout__full">
          <SectionHeader class="detail-card__section-header" title="代理链路" description="区分 Overlay、本地代理、当前节点和真实公网出口" :icon="Router">
            <template #actions>
              <ActionButton size="sm" :icon="Activity" :loading="publicEgressLoading" @click="checkMihomo(true)">刷新链路</ActionButton>
            </template>
          </SectionHeader>
          <div class="proxy-summary">
            <div class="proxy-identity">
              <span :class="['proxy-state', { active: details.proxy.mihomo.detected }]"><i></i>{{ proxyStateLabel(details.proxy.mihomo.state, details.proxy.mihomo.detected) }}</span>
              <div><strong>{{ details.proxy.mihomo.detected ? `Mihomo ${mihomoCapability?.version || ''}` : '未发现 Mihomo / Clash' }}</strong><p>{{ publicEgressLoading ? '正在读取 Controller、当前节点与公网出口…' : `最近检查 ${mihomoInspection ? formatTime(mihomoInspection.checkedAt) : '尚未完成'}` }}</p></div>
            </div>
            <div class="proxy-overlay-note">
              <span>Tailscale Overlay</span>
              <strong>{{ tailscaleStatusLabel() }}</strong>
              <code :title="tailscaleEvidenceLabel()">{{ tailscaleDetails.overlayIps.join('、') || tailscaleEvidenceLabel() }}</code>
            </div>
            <div class="proxy-route-chain" aria-label="当前代理链路">
              <div class="proxy-route-node">
                <small>NAS 主机</small>
                <strong>{{ details.device.hostname || '本机' }}</strong>
                <code>发起连接</code>
              </div>
              <div class="proxy-route-node">
                <small>本地代理入口</small>
                <strong>{{ mihomoInspection?.localProxy.address || '监听地址待确认' }}</strong>
                <code>{{ mihomoModeLabel(mihomoInspection?.localProxy.mode) }}</code>
              </div>
              <div class="proxy-route-node">
                <small>默认策略选择</small>
                <strong :title="mihomoInspection?.strategy.selectedNode || ''">{{ mihomoInspection?.strategy.selectedNode || '节点待确认' }}</strong>
                <code :title="strategySelectionDetail()">{{ strategySelectionDetail() }}</code>
              </div>
              <div class="proxy-route-node">
                <small>节点入口</small>
                <strong>{{ mihomoInspection?.node.server ? `${mihomoInspection.node.server}:${mihomoInspection.node.port}` : '入口待确认' }}</strong>
                <code :title="nodeEndpointEvidence()">{{ nodeEndpointEvidence() }}</code>
              </div>
              <div class="proxy-route-node proxy-route-node--egress">
                <small>本次检测公网出口</small>
                <strong>{{ publicEgressAddressLabel() }}</strong>
                <code>{{ egressLocationLabel() }}<template v-if="publicEgressResult?.isp"> · {{ publicEgressResult.isp }}</template><template v-if="publicEgressResult?.asn"> · {{ publicEgressResult.asn }}</template></code>
              </div>
            </div>
            <p class="proxy-route-note">{{ proxyRouteExplanation() }}</p>
            <small v-if="publicEgressMessage" class="proxy-message" role="status">{{ publicEgressMessage }}</small>
            <details class="proxy-capabilities">
              <summary>控制器与分流能力 <span>展开技术明细</span></summary>
              <div class="proxy-facts proxy-facts--capabilities">
                <div><small>Controller 健康</small><strong>{{ mihomoControllerHealth() }}</strong><code>{{ mihomoController?.endpoint || '端点未确认' }}</code></div>
                <div><small>连接能力</small><strong>{{ hasMihomoOperation('connections') ? 'API 已确认' : '未确认' }}</strong><code>{{ mihomoController?.authRequired ? '需要认证' : '受控读取能力' }}</code></div>
                <div><small>节点 / 代理组</small><strong>{{ hasMihomoOperation('proxies') ? '读取 API 已确认' : '未确认' }}</strong><code>切换操作需经过写入安全确认</code></div>
                <div><small>规则能力</small><strong>{{ mihomoRulesStatus() }}</strong><code>{{ mihomoRulesReadable ? '当前仅证明读取' : '写入、备份、校验、回滚未开放' }}</code></div>
              </div>
              <p>{{ mihomoRulesReadable ? '已确认控制器可读取规则；域名规则写入将在具备应用前备份、校验和回滚契约后开放。' : '规则 API 未确认；进程和 Docker 容器分流没有可靠归属证据，当前不开放。' }}</p>
            </details>
          </div>
        </article>

        <article class="panel detail-card ports-workspace-card network-layout__full">
          <SectionHeader class="detail-card__section-header" title="监听服务" description="按端口合并归属信息，并优先展示对外监听的服务" :icon="Gauge">
            <template #actions><span class="listener-count">{{ listenerResultLabel }}</span></template>
          </SectionHeader>
          <div v-if="listeningPortGroups.length" class="port-workspace">
            <div class="port-toolbar">
              <ElInput v-model="listenerQuery" clearable aria-label="搜索监听服务" placeholder="搜索端口、进程、容器或地址">
                <template #prefix><Search :size="16" /></template>
              </ElInput>
              <NcpSelect v-model="listenerProtocol" :options="listenerProtocolOptions" accessible-label="筛选监听协议" />
              <NcpSelect v-model="listenerScope" :options="listenerScopeOptions" accessible-label="筛选监听范围" />
            </div>
            <div class="port-risk-summary" role="note">
              <span class="port-risk-summary__item port-risk-summary__item--warning"><i></i><strong>{{ exposedListenerCount }}</strong> 个端口可能对外可达</span>
              <span class="port-risk-summary__item"><i></i><strong>{{ localListenerCount }}</strong> 个端口仅允许本机访问</span>
              <small>“公网地址”或“所有接口”需要重点检查；实际可达范围仍受路由、防火墙与端口转发控制。</small>
            </div>
            <div v-if="visibleListeningPortGroups.length" class="port-grid">
              <article v-for="item in visibleListeningPortGroups" :key="`${item.protocol}-${item.port}`" class="port-card">
                <div class="port-card__endpoint">
                  <b>{{ item.port }}</b>
                  <span>{{ item.protocol.toUpperCase() }}</span>
                </div>
                <div class="port-card__fact">
                  <small>进程 / 服务 / 容器</small>
                  <ElTooltip
                    :content="item.owners.map((owner) => `${owner.label} · ${owner.detail}`).join('；') || `PID ${item.pids.join('、') || '未知'}`"
                    placement="top"
                    :show-after="350"
                  >
                    <strong>{{ item.owners.map((owner) => owner.label).join('、') || (item.pids.length ? `PID ${item.pids.join('、')}` : '未识别') }}</strong>
                  </ElTooltip>
                  <span>{{ item.owners.map((owner) => owner.detail).join('、') || '未取得进程归属' }}</span>
                </div>
                <div class="port-card__fact">
                  <small>监听地址</small>
                  <ElTooltip :content="item.addresses.join('、') || '*'" placement="top" :show-after="350">
                    <code>{{ item.addresses.join('、') || '*' }}</code>
                  </ElTooltip>
                  <span>{{ item.addresses.some((address) => /^(0\.0\.0\.0|::|\[::\])/.test(address)) ? '所有网络接口' : '指定网络接口' }}</span>
                </div>
                <div class="port-card__fact">
                  <small>访问范围</small>
                  <span class="listener-scope" :class="`listener-scope--${item.scope.tone}`"><i></i>{{ item.scope.label }}</span>
                  <ElTooltip :content="`${item.scope.description}；识别证据：${listeningSourceLabel(item.sources)}`" placement="top" :show-after="350">
                    <span>{{ item.scope.description }}</span>
                  </ElTooltip>
                </div>
              </article>
            </div>
            <div v-else class="inline-empty">没有符合当前搜索和协议条件的监听服务。</div>
            <div v-if="visibleListeningPortGroups.length" class="port-results-footer">
              <span>已显示 {{ visibleListeningPortGroups.length }} / {{ filteredListeningPortGroups.length }} 个端口</span>
              <ActionButton v-if="visibleListeningPortGroups.length < filteredListeningPortGroups.length" size="sm" @click="showMoreListeningPorts">再显示 {{ Math.min(24, filteredListeningPortGroups.length - visibleListeningPortGroups.length) }} 个</ActionButton>
            </div>
          </div>
          <div v-else class="inline-empty">未取得监听服务信息。</div>
        </article>
      </section>

      <section v-else-if="activeTab === 'storage'" class="storage-layout">
        <article class="storage-summary-grid storage-layout__full">
          <div class="storage-summary-card panel"><span class="storage-summary-card__icon"><HardDrive :size="18" /></span><div><small>可管理存储卷</small><strong>{{ volumeMounts.length }}</strong><p>{{ volumeMounts.map((item) => item.path).join('、') || '未发现 /volume 存储卷' }}</p></div></div>
          <div class="storage-summary-card panel"><span class="storage-summary-card__icon"><Gauge :size="18" /></span><div><small>合计已用</small><strong>{{ formatBytes(volumeUsedBytes) }}</strong><p>总容量 {{ formatBytes(volumeTotalBytes) }}</p></div></div>
          <div class="storage-summary-card panel"><span class="storage-summary-card__icon"><Database :size="18" /></span><div><small>使用率</small><strong>{{ volumeTotalBytes ? `${volumeUsedPercent.toFixed(1)}%` : '—' }}</strong><p>按 NAS 存储卷合计计算</p></div></div>
        </article>

        <article class="panel detail-card storage-layout__full">
          <SectionHeader class="detail-card__section-header" title="存储卷" description="仅展示系统根目录和 /volume 数据卷；系统镜像挂载点收进明细" :icon="HardDrive" />
          <div class="volume-list">
            <div v-for="mount in volumeMounts" :key="mount.path" class="volume-row">
              <div class="volume-row__name"><strong>{{ mount.path === '/' ? '系统根目录' : mount.path }}</strong><small>{{ mount.filesystem || '未知文件系统' }} · {{ mount.device || '未知设备' }}</small></div>
              <div class="volume-row__usage"><div class="meter"><i :style="{ width: `${Math.min(100, mount.usedPercent)}%` }"></i></div><strong>{{ mount.usedPercent.toFixed(1) }}%</strong><small>{{ formatBytes(mount.usedBytes) }} / {{ formatBytes(mount.totalBytes) }}</small></div>
            </div>
          </div>
          <details v-if="auxiliaryMounts.length" class="storage-details">
            <summary>查看系统挂载点 <span>{{ auxiliaryMounts.length }} 个系统 / 镜像挂载点</span></summary>
            <div class="storage-details__list"><div v-for="mount in auxiliaryMounts" :key="mount.path"><strong>{{ mount.path }}</strong><span>{{ mount.filesystem || '未知' }}</span><span>{{ mount.usedPercent.toFixed(1) }}%</span></div></div>
          </details>
          <div v-if="!volumeMounts.length" class="inline-empty">未发现可管理的存储卷。</div>
        </article>

        <article class="panel detail-card">
          <SectionHeader class="detail-card__section-header" title="物理磁盘" description="只展示可独立更换的数据盘；RAID、系统 eMMC 与内存设备不混入此处" :icon="Database" />
          <div v-if="physicalDisks.length" class="disk-list">
            <div v-for="disk in physicalDisks" :key="disk.name" class="disk-row">
              <span class="disk-row__icon"><HardDrive :size="16" /></span>
              <div><strong>{{ disk.name }}</strong><small>{{ disk.model || '型号未知' }} · {{ blockDeviceKindLabel(disk) }} · {{ blockDeviceTransport(disk) }}</small></div>
              <span class="disk-row__size">{{ formatBytes(disk.sizeBytes) }}</span>
              <span :class="['disk-health', { unknown: !disk.health || disk.health === 'unknown' }]">{{ disk.health && disk.health !== 'unknown' ? disk.health : '健康状态未知' }}</span>
            </div>
          </div>
          <div v-else class="inline-empty">系统未暴露物理磁盘信息。</div>
          <details v-if="auxiliaryDisks.length" class="storage-details">
            <summary>查看系统与内存设备 <span>{{ auxiliaryDisks.length }} 个，均不是数据盘</span></summary>
            <div class="storage-details__list storage-details__list--devices">
              <div v-for="disk in auxiliaryDisks" :key="disk.name">
                <span class="auxiliary-device__identity"><strong>{{ disk.name }}</strong><small>{{ blockDeviceKindLabel(disk) }} · {{ blockDeviceTransport(disk) }}</small></span>
                <ElTooltip :content="blockDeviceDescription(disk)" placement="top" :show-after="350"><span class="auxiliary-device__description">{{ blockDeviceDescription(disk) }}</span></ElTooltip>
                <span>{{ formatBytes(disk.sizeBytes) }}</span>
              </div>
            </div>
          </details>
        </article>

        <article class="panel detail-card">
          <SectionHeader class="detail-card__section-header" title="存储阵列" description="阵列级别、运行状态与成员设备；md1、md2 只在这里展示" :icon="Boxes" />
          <div v-if="details.storage.raid.length" class="compact-list">
            <div v-for="raid in details.storage.raid" :key="raid.name">
              <span><strong>{{ raid.name }}</strong><small>{{ raid.level || '级别未知' }}</small></span>
              <span :class="{ 'raid-state--active': raid.state === 'active' }">{{ raid.state || '状态未知' }}</span>
              <span>{{ raid.devices.join('、') || '成员未知' }}</span>
            </div>
          </div>
          <div v-else class="inline-empty">未发现可读取的软件 RAID 信息。</div>
        </article>
      </section>

      <section v-else class="services-layout">
        <article class="panel detail-card services-layout__full">
          <SectionHeader class="detail-card__section-header" title="控制链路" description="Web 控制台至 Root Agent 的真实请求路径" :icon="Waypoints" />
          <ol class="control-chain">
            <li v-for="(node, index) in details.control.nodes" :key="node.id">
              <span class="control-chain__index">{{ index + 1 }}</span>
              <div><strong>{{ node.name }}</strong><small>{{ node.detail }}</small></div>
              <div class="control-chain__meta"><span :class="`status-${node.status}`">{{ node.status === 'ready' ? '正常' : node.status || '未知' }}</span><small>{{ node.version || '版本不可用' }} · {{ formatTime(node.lastSeen) }}</small></div>
            </li>
          </ol>
        </article>

        <article
          v-for="item in capabilityItems"
          :key="item.name"
          class="panel capability-card"
        >
          <span :class="['capability-card__icon', `type-${item.type}`]"><component :is="item.icon" :size="21" /></span>
          <div><strong>{{ item.name }}</strong><small>{{ item.detail }}</small></div>
          <span :class="['capability-state', { off: !item.enabled }]"><i></i>{{ item.enabled ? '可用' : '不可用' }}</span>
        </article>
      </section>
    </template>

    <ElDialog
      v-model="dnsDialogOpen"
      class="dns-editor-dialog"
      title="编辑 DNS 服务器"
      width="min(580px, calc(100vw - 28px))"
      append-to-body
      destroy-on-close
      :close-on-click-modal="!dnsPending"
      :close-on-press-escape="!dnsPending"
      :show-close="!dnsPending"
    >
      <div class="dns-editor">
        <div class="dns-editor__notice"><Route :size="18" /><span><strong>安全修改</strong><small>先生成差异预览，确认后才调用 UGOS 网络服务；应用前保存完整配置，可立即回滚。</small></span></div>
        <label class="dns-editor__field">
          <span>DNS 服务器</span>
          <ElInput v-model="dnsDraft" placeholder="例如 223.5.5.5, 1.1.1.1" clearable @input="invalidateDNSPreview" />
          <small>支持 IPv4 或 IPv6，多个地址可用逗号或空格分隔；UGOS 最多保存 2 个地址。</small>
        </label>
        <div v-if="dnsPreview" class="dns-preview" aria-label="DNS 修改预览">
          <div><span>当前配置</span><strong>{{ dnsPreview.before.nameservers.join('、') || '未配置' }}</strong></div>
          <div><span>应用后</span><strong>{{ dnsPreview.after.nameservers.join('、') || '未配置' }}</strong></div>
        </div>
        <p v-if="dnsMessage" class="dns-editor__message" role="status">{{ dnsMessage }}</p>
      </div>
      <template #footer>
        <div class="dns-editor__footer">
          <ActionButton :disabled="dnsPending" @click="dnsDialogOpen = false">取消</ActionButton>
          <ActionButton v-if="dnsChangeId" variant="danger" :loading="dnsPending" @click="rollbackDNS">回滚上次修改</ActionButton>
          <ActionButton v-if="!dnsPreview" variant="primary" :loading="dnsPending" @click="previewDNS">预览修改</ActionButton>
          <ActionButton v-else variant="primary" :loading="dnsPending" @click="confirmDNS">确认应用</ActionButton>
        </div>
      </template>
    </ElDialog>
  </div>
</template>

<style scoped>
.system-details-page{display:grid;gap:16px}.detail-tabs{display:flex;align-items:center;gap:3px}.detail-tabs button{display:inline-flex;min-height:38px;flex:0 0 auto;align-items:center;gap:7px;padding:0 13px;border:1px solid transparent;border-radius:9px;background:transparent;color:var(--ncp-text-muted);font-weight:680;white-space:nowrap;transition:background var(--ncp-duration-fast),color var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast)}.detail-tabs button:hover{background:var(--ncp-surface);color:var(--ncp-text)}.detail-tabs button.active{border-color:var(--ncp-line);background:var(--ncp-surface);color:var(--ncp-primary-strong);box-shadow:var(--ncp-shadow-control)}.collection-time{color:var(--ncp-text-subtle);font-size:.78rem;white-space:nowrap}.details-skeleton{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;padding:24px}.details-skeleton span{height:108px;border-radius:13px;background:linear-gradient(90deg,var(--ncp-surface-quiet),#fff,var(--ncp-surface-quiet));background-size:200% 100%;animation:skeleton 1.4s linear infinite}.details-error{display:flex;min-height:220px;align-items:center;justify-content:center;gap:14px;padding:24px;color:var(--ncp-danger)}.details-error div{max-width:520px}.details-error p{margin:4px 0;color:var(--ncp-text-muted)}.warning-strip{display:flex;align-items:flex-start;gap:9px;padding:11px 14px;border:1px solid var(--ncp-warning-border);border-radius:11px;background:var(--ncp-warning-soft);color:var(--ncp-warning-strong);font-size:.82rem}.warning-strip svg{flex:0 0 auto;margin-top:1px}.overview-layout,.content-grid,.network-layout,.storage-layout,.services-layout{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.device-summary{display:flex;grid-column:1/-1;align-items:center;gap:16px;padding:22px;background:radial-gradient(circle at 6% 0,rgba(52,116,212,.08),transparent 30%),linear-gradient(115deg,#fff 64%,var(--ncp-surface-quiet))}.device-summary__icon{display:grid;width:60px;height:60px;flex:0 0 auto;place-items:center;border:1px solid color-mix(in srgb,var(--ncp-primary) 18%,transparent);border-radius:17px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.device-summary__identity{min-width:0}.device-summary__identity>span{color:var(--ncp-text-subtle);font-size:.75rem;font-weight:700}.device-summary__identity h2{margin:2px 0;font-size:1.4rem;letter-spacing:-.03em}.device-summary__identity p{margin:0;color:var(--ncp-text-muted);font-size:.86rem}.device-summary__health{display:grid;margin-left:auto;justify-items:end;gap:5px}.device-summary__health>span,.proxy-summary>span{display:inline-flex;width:max-content;align-items:center;gap:7px;padding:6px 10px;border:1px solid var(--ncp-success-border);border-radius:999px;background:var(--ncp-success-soft);color:var(--ncp-success-strong);font-size:.78rem;font-weight:700}.device-summary__health i,.proxy-summary i,.capability-state i{width:7px;height:7px;border-radius:50%;background:currentColor}.device-summary__health small{color:var(--ncp-text-subtle);font-size:.75rem}.overview-facts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));grid-column:1/-1;padding:6px 18px}.overview-facts>div{min-width:0;padding:15px 12px;border-bottom:1px solid var(--ncp-line)}.overview-facts>div:nth-child(n+4){border-bottom:0}.overview-facts span,.definition-grid dt{color:var(--ncp-text-subtle);font-size:.75rem}.overview-facts strong,.definition-grid dd{display:block;overflow:hidden;margin-top:5px;color:var(--ncp-text);font-family:var(--ncp-font-latin);font-size:.88rem;text-overflow:ellipsis;white-space:nowrap}.overview-section{grid-column:1/-1;padding:18px}.overview-section>header,.detail-card__header{display:flex;align-items:center;gap:11px}.overview-section>header>svg{color:var(--ncp-primary)}.overview-section h2,.detail-card__header h2{margin:0;font-size:1rem}.overview-section p,.detail-card__header p{margin:3px 0 0;color:var(--ncp-text-subtle);font-size:.78rem}.resource-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin-top:14px}.resource-summary>div{display:grid;grid-template-columns:auto 1fr;align-items:center;gap:2px 9px;padding:13px;border:1px solid var(--ncp-line);border-radius:12px;background:var(--ncp-surface-quiet)}.resource-summary svg{grid-row:1/3;color:var(--ncp-primary)}.resource-summary span{color:var(--ncp-text-subtle);font-size:.73rem}.resource-summary strong{font-family:var(--ncp-font-latin);font-size:.95rem}.detail-card{overflow:hidden}.content-grid__full,.network-layout__full,.storage-layout__full,.services-layout__full{grid-column:1/-1}.detail-card__header{padding:17px 18px;border-bottom:1px solid var(--ncp-line);background:linear-gradient(135deg,var(--ncp-surface),var(--ncp-surface-quiet))}.detail-card__header>svg{box-sizing:content-box;padding:9px;border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary)}.detail-card__header.type-network>svg{background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.detail-card__header.type-storage>svg{background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.detail-card__header.type-system>svg{background:var(--ncp-object-system-soft);color:var(--ncp-object-system)}.detail-card__header.type-site>svg{background:var(--ncp-object-site-soft);color:var(--ncp-object-site)}.definition-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));margin:0;padding:8px 18px 16px}.definition-grid>div{min-width:0;padding:13px 4px;border-bottom:1px solid var(--ncp-line)}.definition-grid__wide{grid-column:1/-1}.definition-grid dd{margin:5px 0 0}.memory-meter{display:grid;gap:12px;padding:25px 20px}.memory-meter>div:first-child{display:flex;align-items:baseline;gap:6px}.memory-meter strong{font-family:var(--ncp-font-latin);font-size:1.45rem}.memory-meter span,.memory-meter small{color:var(--ncp-text-subtle)}.meter,.usage-cell i{overflow:hidden;height:7px;border-radius:999px;background:var(--ncp-surface-sunken)}.meter i,.usage-cell b{display:block;height:100%;border-radius:inherit;background:var(--ncp-object-storage)}.sensor-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;padding:18px}.sensor-grid>div{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.sensor-grid span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.sensor-grid strong{font-family:var(--ncp-font-latin)}.inline-empty{padding:28px 18px;color:var(--ncp-text-subtle);text-align:center}.interface-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;padding:16px}.interface-card{overflow:hidden;border:1px solid var(--ncp-line);border-radius:13px;background:var(--ncp-surface)}.interface-card>header{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px 14px;border-bottom:1px solid var(--ncp-line);background:var(--ncp-surface-quiet)}.interface-card>header>div{display:flex;align-items:center;gap:8px;color:var(--ncp-object-network)}.interface-card>header span{color:var(--ncp-neutral-strong);font-size:.72rem;font-weight:700}.interface-card>header span.online{color:var(--ncp-success-strong)}.interface-card dl{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));margin:0;padding:4px 14px 12px}.interface-card dl>div{min-width:0;padding:10px 4px}.interface-card dt{color:var(--ncp-text-subtle);font-size:.7rem}.interface-card dd{overflow:hidden;margin:4px 0 0;font-family:var(--ncp-font-latin);font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.proxy-summary{display:grid;gap:12px;padding:22px 18px}.proxy-summary>span{border-color:var(--ncp-neutral-border);background:var(--ncp-neutral-soft);color:var(--ncp-neutral-strong)}.proxy-summary>span.active{border-color:var(--ncp-success-border);background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.proxy-summary p{margin:0;color:var(--ncp-text-muted);font-size:.82rem}.evidence-table,.resource-table{display:grid}.evidence-table__header,.evidence-table>div,.resource-table__header,.resource-table>div{display:grid;grid-template-columns:1.1fr .55fr .55fr 1.8fr;align-items:center;gap:12px;min-height:48px;padding:8px 18px;border-bottom:1px solid var(--ncp-line)}.evidence-table__header,.resource-table__header{min-height:42px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle);font-size:.73rem;font-weight:700}.evidence-table>div:last-child,.resource-table>div:last-child{border-bottom:0}.evidence-table strong{display:grid;gap:2px}.evidence-table strong small{color:var(--ncp-text-subtle);font-weight:500}.evidence-table code{overflow:hidden;color:var(--ncp-text-muted);font-size:.75rem;text-overflow:ellipsis;white-space:nowrap}.evidence-confirmed{color:var(--ncp-success-strong);font-weight:700}.evidence-inferred{color:var(--ncp-warning-strong);font-weight:700}.evidence-unknown{color:var(--ncp-neutral-strong)}.port-grid{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:8px;padding:16px}.port-grid>span{display:grid;gap:2px;padding:10px 12px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.port-grid b{color:var(--ncp-object-network);font-family:var(--ncp-font-mono)}.port-grid small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.resource-table__header,.resource-table>div{grid-template-columns:1.2fr 1.2fr 1fr .8fr}.resource-table>div>span,.resource-table>div>strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.resource-table>div>span{color:var(--ncp-text-muted);font-size:.8rem}.usage-cell{display:grid!important;grid-template-columns:minmax(70px,1fr) auto;align-items:center;gap:8px}.usage-cell i{display:block;width:100%}.compact-list{display:grid;padding:6px 18px 14px}.compact-list>div{display:grid;grid-template-columns:1.2fr .8fr 1fr;align-items:center;gap:12px;min-height:58px;border-bottom:1px solid var(--ncp-line)}.compact-list>div:last-child{border-bottom:0}.compact-list>div>span:first-child{display:grid;gap:2px}.compact-list small{color:var(--ncp-text-subtle)}.control-chain{display:flex;align-items:stretch;gap:0;margin:0;padding:20px;list-style:none}.control-chain li{position:relative;display:grid;min-width:0;flex:1;grid-template-columns:36px minmax(0,1fr);align-items:start;gap:10px;padding-right:22px}.control-chain li:not(:last-child)::after{position:absolute;top:17px;right:4px;left:46px;height:1px;background:var(--ncp-line-strong);content:''}.control-chain__index{position:relative;z-index:1;display:grid;width:36px;height:36px;place-items:center;border:1px solid var(--ncp-primary-border);border-radius:10px;background:var(--ncp-surface);color:var(--ncp-primary-strong);font-family:var(--ncp-font-mono);font-weight:750}.control-chain li>div:nth-child(2){position:relative;z-index:1;display:grid;gap:3px;background:var(--ncp-surface)}.control-chain li small{color:var(--ncp-text-subtle);font-size:.72rem}.control-chain__meta{display:grid;grid-column:2;gap:3px;margin-top:7px}.control-chain__meta>span{width:max-content;color:var(--ncp-neutral-strong);font-size:.7rem;font-weight:700}.control-chain__meta>.status-ready{color:var(--ncp-success-strong)}.capability-card{display:grid;grid-template-columns:44px minmax(0,1fr) auto;align-items:center;gap:12px;padding:16px}.capability-card__icon{display:grid;width:44px;height:44px;place-items:center;border-radius:12px}.type-docker{background:var(--ncp-object-docker-soft);color:var(--ncp-object-docker)}.type-database{background:var(--ncp-engine-sqlite-soft);color:var(--ncp-engine-sqlite)}.type-system{background:var(--ncp-object-system-soft);color:var(--ncp-object-system)}.type-storage{background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.type-network{background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.capability-card>div{display:grid;min-width:0;gap:3px}.capability-card small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.75rem;text-overflow:ellipsis;white-space:nowrap}.capability-state{display:inline-flex;width:max-content;align-items:center;gap:6px;color:var(--ncp-success-strong);font-size:.72rem;font-weight:700}.capability-state.off{color:var(--ncp-neutral-strong)}@keyframes skeleton{to{background-position:-200% 0}}@media(max-width:1050px){.detail-tabs{overflow:auto;max-width:100%;padding-bottom:2px}.content-grid,.network-layout,.storage-layout{grid-template-columns:1fr}.resource-summary{grid-template-columns:repeat(2,minmax(0,1fr))}.control-chain{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.control-chain li{padding:0}.control-chain li::after{display:none!important}.port-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}@media(max-width:760px){.overview-facts{grid-template-columns:repeat(2,minmax(0,1fr))}.overview-facts>div:nth-child(n+3){border-bottom:0}.device-summary{align-items:flex-start;flex-wrap:wrap}.device-summary__health{width:100%;margin-left:76px;justify-items:start}.interface-grid{grid-template-columns:1fr}.sensor-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.evidence-table__header{display:none}.evidence-table>div{grid-template-columns:1fr 1fr;padding-block:12px}.resource-table__header{display:none}.resource-table>div{grid-template-columns:1fr 1fr;padding-block:12px}.compact-list>div{grid-template-columns:1fr}.port-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.services-layout{grid-template-columns:1fr}.control-chain{grid-template-columns:1fr}}@media(max-width:520px){.detail-tabs button{padding-inline:10px}.detail-tabs button svg{display:none}.collection-time{display:none}.overview-facts,.resource-summary,.definition-grid{grid-template-columns:1fr}.overview-facts>div{border-bottom:1px solid var(--ncp-line)!important}.overview-facts>div:last-child{border-bottom:0!important}.definition-grid__wide{grid-column:auto}.device-summary__health{margin-left:0}.sensor-grid{grid-template-columns:1fr}.interface-card dl{grid-template-columns:1fr}.port-grid{grid-template-columns:1fr}.resource-table>div,.evidence-table>div{grid-template-columns:1fr}.usage-cell{grid-template-columns:minmax(90px,1fr) auto!important}.details-skeleton{grid-template-columns:1fr}}
@media(prefers-reduced-motion:reduce){.details-skeleton span{animation:none}}
.system-tabs-toolbar{display:flex;min-width:0;width:100%;align-items:center;justify-content:space-between;gap:14px}.system-tabs-toolbar .detail-tabs{min-width:0;flex:1}.collection-meta{display:flex;flex:0 0 auto;align-items:center;gap:10px}.collection-time{display:inline-flex;align-items:center;color:var(--ncp-text-subtle);font-size:.76rem;white-space:nowrap}.system-refresh{min-height:var(--ncp-control-height)!important;margin:0!important;gap:7px!important;padding-inline:12px!important}.overview-section--processor,.overview-section--resources{grid-column:span 1}.processor-facts{display:grid;grid-template-columns:1.6fr 1fr 1fr;gap:1px;margin-top:14px;overflow:hidden;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-line)}.processor-facts>div{display:grid;min-width:0;gap:5px;padding:12px;background:var(--ncp-surface-quiet)}.processor-facts span{color:var(--ncp-text-subtle);font-size:.72rem}.processor-facts strong{overflow:hidden;font-family:var(--ncp-font-latin);font-size:.86rem;text-overflow:ellipsis;white-space:nowrap}.resource-summary{grid-template-columns:repeat(3,minmax(0,1fr))}.network-summary-grid,.storage-summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.storage-summary-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.network-summary-card,.storage-summary-card{display:grid;min-width:0;grid-template-columns:auto minmax(0,1fr);align-items:start;gap:10px;padding:15px}.network-summary-card__icon,.storage-summary-card__icon{display:grid;width:36px;height:36px;place-items:center;border-radius:10px;background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.storage-summary-card__icon{background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.network-summary-card>div,.storage-summary-card>div{display:grid;min-width:0;gap:3px}.network-summary-card small,.storage-summary-card small{color:var(--ncp-text-subtle);font-size:.7rem}.network-summary-card strong,.storage-summary-card strong{overflow:hidden;color:var(--ncp-text);font-family:var(--ncp-font-latin);font-size:.94rem;text-overflow:ellipsis;white-space:nowrap}.network-summary-card p,.storage-summary-card p{overflow:hidden;margin:0;color:var(--ncp-text-muted);font-size:.72rem;text-overflow:ellipsis;white-space:nowrap}.primary-interface-list{display:grid;padding:6px 18px}.primary-interface-row{display:grid;grid-template-columns:118px minmax(140px,1.1fr) minmax(180px,1.4fr) minmax(150px,1fr);align-items:center;gap:14px;min-height:72px;border-bottom:1px solid var(--ncp-line)}.primary-interface-row:last-child{border-bottom:0}.primary-interface-row__status{display:inline-flex;width:max-content;align-items:center;gap:6px;padding:5px 8px;border:1px solid var(--ncp-line);border-radius:999px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle);font-size:.7rem;font-weight:720}.primary-interface-row__status.online{border-color:var(--ncp-success-border);background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.primary-interface-row__status i{width:6px;height:6px;border-radius:50%;background:currentColor}.primary-interface-row>div:not(.primary-interface-row__name){display:grid;min-width:0;gap:3px}.primary-interface-row__name{display:grid;min-width:0;gap:2px}.primary-interface-row strong{overflow:hidden;font-family:var(--ncp-font-latin);font-size:.83rem;text-overflow:ellipsis;white-space:nowrap}.primary-interface-row small,.primary-interface-row>div:not(.primary-interface-row__name)>span{color:var(--ncp-text-subtle);font-size:.7rem}.primary-interface-row>div:not(.primary-interface-row__name)>strong{font-family:var(--ncp-font-mono);font-size:.76rem;font-weight:600}.network-details,.storage-details{border-top:1px solid var(--ncp-line);background:var(--ncp-surface-quiet)}.network-details summary,.storage-details summary,.evidence-disclosure summary{display:flex;min-height:48px;align-items:center;justify-content:space-between;gap:12px;padding:0 18px;cursor:pointer;color:var(--ncp-primary-strong);font-size:.78rem;font-weight:750;list-style:none}.network-details summary::-webkit-details-marker,.storage-details summary::-webkit-details-marker,.evidence-disclosure summary::-webkit-details-marker{display:none}.network-details summary::before,.storage-details summary::before,.evidence-disclosure summary::before{content:'+';display:grid;width:22px;height:22px;flex:0 0 auto;place-items:center;border:1px solid var(--ncp-primary-border);border-radius:6px;background:var(--ncp-primary-soft);font-size:1rem;line-height:1}.network-details[open] summary::before,.storage-details[open] summary::before,.evidence-disclosure[open] summary::before{content:'−'}.network-details summary span,.storage-details summary span,.evidence-disclosure summary>span:last-child{margin-left:auto;color:var(--ncp-text-subtle);font-size:.7rem;font-weight:600}.network-details__list,.storage-details__list{display:grid;border-top:1px solid var(--ncp-line)}.network-detail-row{display:grid;grid-template-columns:1.2fr 90px 90px 1fr;align-items:center;gap:12px;min-height:54px;padding:8px 18px;border-bottom:1px solid var(--ncp-line)}.network-detail-row:last-child,.storage-details__list>div:last-child{border-bottom:0}.network-detail-row>div{display:grid;min-width:0;gap:2px}.network-detail-row strong,.network-detail-row code{overflow:hidden;font-family:var(--ncp-font-mono);font-size:.75rem;text-overflow:ellipsis;white-space:nowrap}.network-detail-row small,.network-detail-row>span{color:var(--ncp-text-subtle);font-size:.7rem}.network-detail-row>span.is-online{color:var(--ncp-success-strong);font-weight:700}.network-detail-row code{color:var(--ncp-text-muted)}.proxy-summary{padding:18px}.proxy-summary strong{font-size:.9rem}.proxy-summary small{color:var(--ncp-text-subtle);font-size:.72rem}.evidence-disclosure{overflow:hidden}.evidence-disclosure>summary{min-height:68px;padding:0 18px;background:linear-gradient(120deg,var(--ncp-surface),var(--ncp-surface-quiet))}.evidence-disclosure__title{display:flex!important;align-items:center;gap:10px;margin-left:0!important;color:var(--ncp-object-site)}.evidence-disclosure__title>span{display:grid;gap:2px}.evidence-disclosure__title strong{color:var(--ncp-text);font-size:.9rem}.evidence-disclosure__title small{color:var(--ncp-text-subtle);font-size:.72rem;font-weight:500}.evidence-disclosure .evidence-table{border-top:1px solid var(--ncp-line)}.evidence-disclosure .evidence-table__header{background:var(--ncp-surface-quiet)}.port-grid{grid-template-columns:repeat(4,minmax(0,1fr));padding:14px 18px}.port-grid>span{min-width:0;gap:4px}.port-grid b{font-size:.92rem}.port-grid small,.port-grid em{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;font-style:normal;text-overflow:ellipsis;white-space:nowrap}.port-grid em{color:var(--ncp-text-muted);font-family:var(--ncp-font-mono)}.volume-list{display:grid;padding:7px 18px}.volume-row{display:grid;grid-template-columns:minmax(180px,.8fr) minmax(280px,1.2fr);align-items:center;gap:24px;min-height:76px;border-bottom:1px solid var(--ncp-line)}.volume-row:last-child{border-bottom:0}.volume-row__name{display:grid;min-width:0;gap:3px}.volume-row__name strong{font-family:var(--ncp-font-mono);font-size:.85rem}.volume-row__name small,.volume-row__usage small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.7rem;text-overflow:ellipsis;white-space:nowrap}.volume-row__usage{display:grid;grid-template-columns:minmax(90px,1fr) auto;align-items:center;gap:7px}.volume-row__usage .meter{grid-column:1/-1}.volume-row__usage strong{font-family:var(--ncp-font-latin);font-size:.78rem}.volume-row__usage small{text-align:right}.storage-details__list>div{display:grid;grid-template-columns:1.3fr 1fr 90px;align-items:center;gap:12px;min-height:46px;padding:7px 18px;border-bottom:1px solid var(--ncp-line);font-size:.75rem}.storage-details__list span{color:var(--ncp-text-subtle)}.disk-list{display:grid;padding:7px 18px}.disk-row{display:grid;grid-template-columns:30px minmax(0,1fr) auto auto;align-items:center;gap:10px;min-height:62px;border-bottom:1px solid var(--ncp-line)}.disk-row:last-child{border-bottom:0}.disk-row__icon{display:grid;width:28px;height:28px;place-items:center;border-radius:8px;background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.disk-row>div{display:grid;min-width:0;gap:2px}.disk-row strong{overflow:hidden;font-family:var(--ncp-font-mono);font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.disk-row small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.disk-row__size{color:var(--ncp-text-muted);font-family:var(--ncp-font-latin);font-size:.75rem}.disk-health{color:var(--ncp-success-strong);font-size:.72rem;font-weight:700}.disk-health.unknown{color:var(--ncp-text-subtle);font-weight:600}.raid-state--active{color:var(--ncp-success-strong);font-weight:700}
@media(max-width:1050px){.overview-section--processor,.overview-section--resources{grid-column:1/-1}.network-summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.storage-summary-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.primary-interface-row{grid-template-columns:118px minmax(140px,1fr) minmax(150px,1fr)}.primary-interface-row>div:last-child{grid-column:2/-1}.port-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}
@media(max-width:760px){.system-tabs-toolbar{align-items:stretch;flex-direction:column;gap:9px}.system-tabs-toolbar .detail-tabs{width:100%;flex:none}.collection-meta{justify-content:space-between}.overview-section--processor,.overview-section--resources{grid-column:1/-1}.network-summary-grid,.storage-summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.primary-interface-list{padding-inline:15px}.primary-interface-row{grid-template-columns:1fr 1fr;gap:9px;padding-block:12px}.primary-interface-row__status{grid-column:1/-1}.primary-interface-row>div:last-child{grid-column:auto}.network-detail-row{grid-template-columns:minmax(0,1fr) auto}.network-detail-row>span:nth-child(3){display:none}.network-detail-row code{grid-column:1/-1}.volume-list,.disk-list{padding-inline:15px}.volume-row{grid-template-columns:1fr;gap:8px;padding-block:12px}.volume-row__usage{grid-template-columns:minmax(100px,1fr) auto}.volume-row__usage .meter{grid-column:1/-1}.disk-row{grid-template-columns:30px minmax(0,1fr) auto}.disk-health{grid-column:2/-1}.port-grid{grid-template-columns:repeat(2,minmax(0,1fr));padding-inline:15px}.evidence-disclosure>summary{padding-inline:15px}.evidence-table>div{padding-inline:15px}}
@media(max-width:520px){.collection-time{display:inline-flex;font-size:.7rem}.collection-meta{width:100%}.collection-meta .system-refresh{min-width:76px}.processor-facts{grid-template-columns:1fr}.resource-summary{grid-template-columns:1fr}.network-summary-grid,.storage-summary-grid{grid-template-columns:1fr}.network-summary-card,.storage-summary-card{padding:13px}.network-details summary,.storage-details summary{padding-inline:15px}.network-detail-row{padding-inline:15px}.port-grid{grid-template-columns:1fr}.storage-details__list>div{grid-template-columns:1fr auto;padding-inline:15px}.storage-details__list>div span:last-child{grid-column:2;grid-row:1}.disk-row__size{font-size:.7rem}}
.ports-disclosure{overflow:hidden}.ports-disclosure>summary{min-height:76px;background:linear-gradient(120deg,var(--ncp-surface),var(--ncp-surface-quiet))}.ports-disclosure__title{display:flex!important;align-items:center;gap:10px;margin-left:0!important}.ports-disclosure__title>svg{box-sizing:content-box;padding:9px;border-radius:11px;background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.ports-disclosure__title>span{display:grid;gap:2px}.ports-disclosure__title strong{color:var(--ncp-text);font-size:.9rem}.ports-disclosure__title small{color:var(--ncp-text-subtle);font-size:.72rem;font-weight:500}.overview-fact--cgroup small{margin-top:4px;color:var(--ncp-text-subtle);font-size:.66rem;line-height:1.35}
.dns-management{display:grid;gap:10px;padding:0 18px 18px;border-top:1px solid var(--ncp-line)}.dns-management__state{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-top:14px}.dns-management__state small,.dns-management__message{color:var(--ncp-text-subtle);font-size:.7rem}.dns-management__input{display:grid;gap:5px}.dns-management__input span{color:var(--ncp-text-subtle);font-size:.7rem}.dns-management__input input{min-height:36px;padding:0 10px;border:1px solid var(--ncp-line);border-radius:8px;background:var(--ncp-surface);color:var(--ncp-text);font:inherit;font-size:.76rem;outline:none}.dns-management__input input:focus{border-color:var(--ncp-primary);box-shadow:0 0 0 3px var(--ncp-primary-soft)}.dns-management__actions{display:flex;flex-wrap:wrap;gap:7px}.text-button{min-height:32px;padding:0 10px;border:1px solid var(--ncp-line);border-radius:8px;background:var(--ncp-surface);color:var(--ncp-text-muted);font:inherit;font-size:.72rem;font-weight:700;cursor:pointer}.text-button:hover,.text-button:focus-visible{border-color:var(--ncp-primary-border);background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);outline:none}.text-button--primary{border-color:var(--ncp-primary);background:var(--ncp-primary);color:#fff}.text-button--primary:hover,.text-button--primary:focus-visible{background:var(--ncp-primary-strong);color:#fff}.proxy-facts{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}.proxy-facts>div{display:grid;min-width:0;gap:3px;padding:10px;border:1px solid var(--ncp-line);border-radius:9px;background:var(--ncp-surface-quiet)}.proxy-facts small{color:var(--ncp-text-subtle);font-size:.68rem}.proxy-facts strong{overflow:hidden;font-family:var(--ncp-font-latin);font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.proxy-facts code{overflow:hidden;color:var(--ncp-text-muted);font-family:var(--ncp-font-latin);font-size:.7rem;letter-spacing:0;text-overflow:ellipsis;white-space:nowrap}.proxy-message{color:var(--ncp-warning-strong);font-size:.7rem}.proxy-rules-card{overflow:hidden}.proxy-rules-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;padding:16px 18px}.proxy-rules-grid>div{display:grid;gap:4px;padding:13px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.proxy-rules-grid strong{font-size:.78rem}.proxy-rules-grid span{width:max-content;padding:3px 6px;border-radius:5px;background:var(--ncp-neutral-soft);color:var(--ncp-neutral-strong);font-size:.68rem;font-weight:700}.proxy-rules-grid small,.proxy-rules-note{color:var(--ncp-text-subtle);font-size:.7rem;line-height:1.45}.proxy-rules-note{margin:0;padding:0 18px 17px}
@media(max-width:760px){.dns-management__state{align-items:flex-start;flex-direction:column;gap:5px}.proxy-facts,.proxy-rules-grid{grid-template-columns:1fr}}
.overview-facts{grid-auto-rows:minmax(82px,1fr);align-items:stretch}.overview-facts>div{display:grid;align-content:center}.proxy-facts>div{min-height:68px;align-content:center}.port-service{overflow:hidden;font-family:var(--ncp-font-latin)!important;text-overflow:ellipsis;white-space:nowrap}
.network-layout{align-items:start}.network-layout>.detail-card{align-self:start}.dns-management{padding-top:14px}.dns-management__state{align-items:flex-start;padding-top:0}.dns-management__state small{max-width:68%;line-height:1.55;text-align:right}.dns-management__input input{min-height:40px;border-color:var(--ncp-line-strong);border-radius:10px;font-size:.8rem}.dns-management__actions{align-items:center}.dns-preview{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));overflow:hidden;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-line)}.dns-preview>div{display:grid;min-width:0;gap:4px;padding:10px 12px;background:var(--ncp-surface-quiet)}.dns-preview span{color:var(--ncp-text-subtle);font-size:.68rem}.dns-preview strong{overflow:hidden;font-family:var(--ncp-font-mono);font-size:.74rem;text-overflow:ellipsis;white-space:nowrap}.proxy-summary{gap:14px}.proxy-identity{display:flex;align-items:center;gap:12px}.proxy-identity>div{display:grid;min-width:0;gap:2px}.proxy-identity p{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.proxy-state{display:inline-flex;width:max-content;flex:0 0 auto;align-items:center;gap:7px;padding:6px 10px;border:1px solid var(--ncp-neutral-border);border-radius:999px;background:var(--ncp-neutral-soft);color:var(--ncp-neutral-strong);font-size:.72rem;font-weight:750}.proxy-state.active{border-color:var(--ncp-success-border);background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.proxy-state i{width:7px;height:7px;border-radius:50%;background:currentColor}.proxy-facts--primary>div{min-height:76px;padding:12px}.proxy-facts--primary strong{font-size:.84rem}.proxy-actions{display:flex;min-height:34px;align-items:center;gap:10px}.proxy-actions .text-button{display:inline-flex;flex:0 0 auto;align-items:center;justify-content:center;gap:7px}.proxy-actions .text-button:disabled{cursor:wait;opacity:.64}.proxy-message{line-height:1.45}.proxy-capabilities{overflow:hidden;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.proxy-capabilities>summary{display:flex;min-height:42px;align-items:center;gap:9px;padding:0 12px;cursor:pointer;color:var(--ncp-text-muted);font-size:.73rem;font-weight:750;list-style:none}.proxy-capabilities>summary::-webkit-details-marker{display:none}.proxy-capabilities>summary::before{content:'+';display:grid;width:20px;height:20px;place-items:center;border:1px solid var(--ncp-line-strong);border-radius:6px;background:var(--ncp-surface);color:var(--ncp-primary-strong);font-size:.9rem}.proxy-capabilities[open]>summary::before{content:'−'}.proxy-capabilities>summary span{margin-left:auto;color:var(--ncp-text-subtle);font-size:.68rem;font-weight:600}.proxy-capabilities .proxy-facts{padding:0 10px 10px}.proxy-capabilities>p{margin:0;padding:0 12px 12px;color:var(--ncp-text-subtle);font-size:.69rem;line-height:1.5}.proxy-facts--capabilities>div{background:var(--ncp-surface)}
@media(max-width:760px){.dns-management__state small{max-width:none;text-align:left}.dns-preview{grid-template-columns:1fr}.proxy-identity{align-items:flex-start;flex-direction:column;gap:8px}.proxy-actions{align-items:flex-start;flex-direction:column}.proxy-capabilities .proxy-facts{grid-template-columns:1fr}}

.detail-card__section-header {
  padding: 17px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: linear-gradient(120deg, var(--ncp-surface), var(--ncp-surface-quiet));
}

.detail-card__section-header :deep(.section-header__icon) {
  border-color: color-mix(in srgb, var(--ncp-object-storage) 20%, var(--ncp-line));
  background: var(--ncp-object-storage-soft);
  color: var(--ncp-object-storage);
}

.services-layout .detail-card__section-header :deep(.section-header__icon) {
  border-color: color-mix(in srgb, var(--ncp-object-system) 20%, var(--ncp-line));
  background: var(--ncp-object-system-soft);
  color: var(--ncp-object-system);
}

.port-workspace {
  border-top: 1px solid var(--ncp-line);
}

.port-toolbar {
  display: grid;
  grid-template-columns: minmax(240px, 1fr) 150px 190px;
  align-items: center;
  gap: 10px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: var(--ncp-surface);
}

.listener-count {
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  font-weight: 650;
  white-space: nowrap;
}

.port-risk-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 14px;
  padding: 10px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.port-risk-summary__item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--ncp-text-muted);
  font-size: .72rem;
}

.port-risk-summary__item i,
.listener-scope i {
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: currentColor;
}

.port-risk-summary__item strong { color: var(--ncp-text); font-family: var(--ncp-font-data); }
.port-risk-summary__item--warning { color: var(--ncp-warning-strong); }
.port-risk-summary > small { margin-left: auto; color: var(--ncp-text-subtle); font-size: .67rem; }

.port-toolbar :deep(.el-input__wrapper) {
  min-height: var(--ncp-control-height);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-control-surface);
  box-shadow: 0 0 0 1px var(--ncp-control-border) inset;
}

.port-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  padding: 14px 18px 18px;
  background: var(--ncp-surface-quiet);
}

.port-card {
  display: grid;
  min-width: 0;
  grid-template-columns: 72px minmax(150px, 1.3fr) minmax(130px, 1fr) minmax(120px, .9fr);
  align-items: center;
  gap: 14px;
  padding: 14px;
  border: 1px solid var(--ncp-line);
  border-radius: 12px;
  background: var(--ncp-surface);
  box-shadow: 0 1px 2px rgb(20 42 73 / 3%);
  transition: border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast);
}

.port-card:hover {
  border-color: var(--ncp-primary-border);
  box-shadow: var(--ncp-shadow-control);
  transform: translateY(-1px);
}

.port-card__endpoint {
  display: grid;
  min-width: 0;
  justify-items: start;
  gap: 5px;
}

.port-card__endpoint b {
  color: var(--ncp-object-network);
  font-family: var(--ncp-font-mono);
  font-size: 1rem;
}

.port-card__endpoint span {
  padding: 3px 6px;
  border-radius: 6px;
  background: var(--ncp-object-network-soft);
  color: var(--ncp-object-network);
  font-family: var(--ncp-font-mono);
  font-size: .65rem;
  font-weight: 750;
}

.port-card__fact {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.port-card__fact small,
.port-card__fact span {
  overflow: hidden;
  color: var(--ncp-text-subtle);
  font-size: .68rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.port-card__fact strong,
.port-card__fact code {
  display: block;
  overflow: hidden;
  color: var(--ncp-text);
  font-family: var(--ncp-font-latin);
  font-size: .78rem;
  font-weight: 700;
  letter-spacing: 0;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.listener-scope {
  display: inline-flex !important;
  width: max-content;
  max-width: 100%;
  min-height: 24px;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  border: 1px solid var(--ncp-neutral-border);
  border-radius: var(--ncp-radius-pill);
  background: var(--ncp-neutral-soft);
  color: var(--ncp-neutral-strong) !important;
  font-size: .68rem !important;
  font-weight: 720;
}

.listener-scope--danger { border-color: var(--ncp-danger-border); background: var(--ncp-danger-soft); color: var(--ncp-danger-strong) !important; }
.listener-scope--warning { border-color: var(--ncp-warning-border); background: var(--ncp-warning-soft); color: var(--ncp-warning-strong) !important; }
.listener-scope--info { border-color: var(--ncp-info-border); background: var(--ncp-info-soft); color: var(--ncp-info-strong) !important; }
.listener-scope--success { border-color: var(--ncp-success-border); background: var(--ncp-success-soft); color: var(--ncp-success-strong) !important; }

.port-results-footer {
  display: flex;
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: var(--ncp-space-3);
  padding: 8px 18px;
  border-top: 1px solid var(--ncp-line);
  background: var(--ncp-surface);
}

.port-results-footer > span {
  color: var(--ncp-text-subtle);
  font-size: .72rem;
}

.port-card__fact code {
  color: var(--ncp-text-muted);
  font-family: var(--ncp-font-mono);
  font-size: .72rem;
  font-weight: 600;
}

.storage-details__list--devices > div {
  grid-template-columns: minmax(140px, .8fr) minmax(220px, 1.5fr) 90px;
}

.auxiliary-device__identity {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.auxiliary-device__identity strong,
.auxiliary-device__identity small,
.auxiliary-device__description {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.auxiliary-device__identity small {
  color: var(--ncp-text-subtle);
  font-size: .67rem;
}

.network-layout .detail-card__section-header :deep(.section-header__icon) {
  border-color: color-mix(in srgb, var(--ncp-object-network) 20%, var(--ncp-line));
  background: var(--ncp-object-network-soft);
  color: var(--ncp-object-network);
}

.dns-management {
  gap: 12px;
}

.dns-management__state {
  align-items: flex-start;
  flex-direction: column;
  gap: 7px;
}

.dns-management__state small {
  max-width: none;
  line-height: 1.55;
  text-align: left;
}

.dns-current-value {
  display: grid;
  min-width: 0;
  gap: 5px;
  padding: 12px 13px;
  border: 1px solid var(--ncp-line);
  border-radius: 10px;
  background: var(--ncp-surface-quiet);
}

.dns-current-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 9px;
}

.dns-current-grid > .dns-current-value:only-child {
  grid-column: 1 / -1;
}

.dns-current-value--managed {
  border-color: var(--ncp-primary-border);
  background: color-mix(in srgb, var(--ncp-primary-soft) 62%, var(--ncp-surface));
}

.dns-current-value span {
  color: var(--ncp-text-subtle);
  font-size: .68rem;
}

.dns-current-value code {
  overflow-wrap: anywhere;
  color: var(--ncp-text);
  font-family: var(--ncp-font-mono);
  font-size: .76rem;
  line-height: 1.5;
}

:global(.dns-editor-dialog.el-dialog) {
  overflow: hidden;
  border: 1px solid var(--ncp-line);
  border-radius: 16px;
  background: var(--ncp-surface);
  box-shadow: var(--ncp-shadow-overlay);
}

:global(.dns-editor-dialog .el-dialog__header) {
  margin: 0;
  padding: 20px 22px 16px;
  border-bottom: 1px solid var(--ncp-line);
}

:global(.dns-editor-dialog .el-dialog__title) {
  color: var(--ncp-text);
  font-size: 1.02rem;
  font-weight: 750;
}

:global(.dns-editor-dialog .el-dialog__body) {
  padding: 20px 22px;
}

:global(.dns-editor-dialog .el-dialog__footer) {
  padding: 15px 22px;
  border-top: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.dns-editor {
  display: grid;
  gap: 16px;
}

.dns-editor__notice {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 13px;
  border: 1px solid var(--ncp-primary-border);
  border-radius: 11px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.dns-editor__notice > span {
  display: grid;
  gap: 3px;
}

.dns-editor__notice strong {
  font-size: .8rem;
}

.dns-editor__notice small {
  color: var(--ncp-text-muted);
  font-size: .71rem;
  line-height: 1.5;
}

.dns-editor__field {
  display: grid;
  gap: 7px;
}

.dns-editor__field > span {
  color: var(--ncp-text);
  font-size: .78rem;
  font-weight: 700;
}

.dns-editor__field > small {
  color: var(--ncp-text-subtle);
  font-size: .69rem;
}

.dns-editor__field :deep(.el-input__wrapper) {
  min-height: 42px;
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-control-surface);
  box-shadow: 0 0 0 1px var(--ncp-control-border) inset;
}

.dns-editor__field :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 1px var(--ncp-primary) inset, 0 0 0 3px var(--ncp-focus-ring);
}

.dns-editor__message {
  margin: 0;
  padding: 9px 11px;
  border-radius: 9px;
  background: var(--ncp-neutral-soft);
  color: var(--ncp-text-muted);
  font-size: .72rem;
  line-height: 1.5;
}

.dns-editor__footer {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 9px;
}

@media (max-width: 620px) {
  .dns-current-grid {
    grid-template-columns: 1fr;
  }
}

.proxy-workspace {
  align-self: start;
}

.proxy-overlay-note {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: 4px 10px;
  padding: 10px 12px;
  border: 1px solid var(--ncp-line);
  border-radius: 10px;
  background: var(--ncp-surface-quiet);
}

.proxy-overlay-note span {
  color: var(--ncp-text-subtle);
  font-size: .68rem;
}

.proxy-overlay-note strong {
  color: var(--ncp-success-strong);
  font-size: .76rem;
  text-align: right;
}

.proxy-overlay-note code {
  overflow: hidden;
  grid-column: 1 / -1;
  color: var(--ncp-text-muted);
  font-family: var(--ncp-font-mono);
  font-size: .7rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.proxy-route-chain {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  counter-reset: proxy-route;
  gap: 9px;
}

.proxy-route-node {
  position: relative;
  display: grid;
  min-width: 0;
  min-height: 88px;
  counter-increment: proxy-route;
  align-content: center;
  gap: 3px;
  padding: 11px 13px 11px 48px;
  border: 1px solid var(--ncp-line);
  border-radius: 11px;
  background: var(--ncp-surface);
  box-shadow: 0 1px 2px rgb(20 42 73 / 3%);
}

.proxy-route-node::before {
  position: absolute;
  top: 50%;
  left: 14px;
  display: grid;
  width: 24px;
  height: 24px;
  place-items: center;
  border: 1px solid var(--ncp-primary-border);
  border-radius: 8px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
  content: counter(proxy-route);
  font-family: var(--ncp-font-mono);
  font-size: .68rem;
  font-weight: 750;
  transform: translateY(-50%);
}

.proxy-route-node:not(:last-child)::after {
  position: absolute;
  top: 50%;
  right: -10px;
  width: 9px;
  height: 1px;
  background: var(--ncp-primary-border);
  content: '';
}

.proxy-route-node--egress {
  border-color: var(--ncp-success-border);
  background: linear-gradient(135deg, var(--ncp-surface), var(--ncp-success-soft));
}

.proxy-route-node--egress::before {
  border-color: var(--ncp-success-border);
  background: var(--ncp-success-soft);
  color: var(--ncp-success-strong);
}

.proxy-route-node small {
  color: var(--ncp-text-subtle);
  font-size: .67rem;
}

.proxy-route-node strong,
.proxy-route-node code {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.proxy-route-node strong {
  color: var(--ncp-text);
  font-size: .8rem;
}

.proxy-route-node code {
  color: var(--ncp-text-muted);
  font-family: var(--ncp-font-mono);
  font-size: .69rem;
}

.proxy-route-note {
  margin: 0;
  padding: 10px 12px;
  border: 1px solid var(--ncp-info-border);
  border-radius: 10px;
  background: var(--ncp-info-soft);
  color: var(--ncp-text-muted);
  font-size: .7rem !important;
  line-height: 1.55;
}

.proxy-message {
  padding: 9px 11px;
  border: 1px solid var(--ncp-warning-border);
  border-radius: 9px;
  background: var(--ncp-warning-soft);
}

@media (max-width: 1480px) {
  .port-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 1050px) {
  .proxy-route-chain {
    grid-template-columns: 1fr;
  }

  .proxy-route-node {
    min-height: auto;
  }

  .proxy-route-node:not(:last-child)::after {
    top: auto;
    right: auto;
    bottom: -10px;
    left: 25px;
    width: 1px;
    height: 9px;
  }
}

@media (max-width: 760px) {
  .port-toolbar {
    grid-template-columns: minmax(0, 1fr) 140px 170px;
    padding-inline: 15px;
  }

  .port-grid {
    grid-template-columns: 1fr;
    padding-inline: 15px;
  }

  .port-card {
    grid-template-columns: 64px minmax(0, 1fr) minmax(0, 1fr);
  }

  .port-card__fact:last-child {
    grid-column: 2 / -1;
  }

  .storage-details__list--devices > div {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .auxiliary-device__description {
    grid-column: 1 / -1;
    grid-row: auto !important;
  }
}

@media (max-width: 520px) {
  .detail-card__section-header {
    padding-inline: 15px;
  }

  .detail-tabs {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    overflow: visible;
  }

  .detail-tabs button {
    justify-content: center;
  }

  .port-toolbar {
    grid-template-columns: 1fr;
  }

  .port-risk-summary { align-items: flex-start; flex-direction: column; }
  .port-risk-summary > small { margin-left: 0; line-height: 1.5; }
  .port-results-footer { align-items: stretch; flex-direction: column; padding-block: 12px; }

  .port-card {
    grid-template-columns: 58px minmax(0, 1fr);
  }

  .port-card__fact {
    grid-column: 2;
  }

  .proxy-route-node {
    padding-right: 10px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .port-card {
    transition: none;
  }

  .port-card:hover {
    transform: none;
  }
}
</style>
