<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
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
  Route,
  Router,
  Search,
  Server,
  Waypoints,
  Wifi,
} from '@lucide/vue'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import ActionButton from '@/components/ActionButton.vue'
import InfrastructureOverviewPanel from '@/components/InfrastructureOverviewPanel.vue'
import InfrastructureDnsPanel from '@/components/InfrastructureDnsPanel.vue'
import InfrastructureNetworkInterfacesPanel from '@/components/InfrastructureNetworkInterfacesPanel.vue'
import InfrastructureProxyPanel from '@/components/InfrastructureProxyPanel.vue'
import { type InfrastructureSignal } from '@/components/InfrastructureSignalSummary.vue'
import InfrastructureServicesPanel from '@/components/InfrastructureServicesPanel.vue'
import InfrastructureStoragePanel from '@/components/InfrastructureStoragePanel.vue'
import { useManualRefreshRegistry } from '@/composables/manualRefresh'
import NcpSelect from '@/components/NcpSelect.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { createRequestSequenceGate } from '@/domain/requestSequence'
import {
  blockDeviceDescription,
  blockDeviceKindLabel,
  blockDeviceTransport,
  formatBytes,
  formatDuration,
  formatTime,
  listeningSourceLabel,
  proxyStateLabel,
} from '@/domain/infrastructurePresentation'
import { editableDNSNameservers } from '@/domain/network'
import { useInfrastructureNetwork } from '@/composables/useInfrastructureNetwork'
import {
  confirmDNSChange,
  inspectMihomo,
  previewDNSChange,
  requestDNSCapability,
  requestSystemDetails,
  rollbackDNSChange,
  type DNSCapability,
  type DNSChangePreview,
  type MihomoInspection,
  type SystemDetails,
  type TailscaleCapability,
} from '@/api/system'
import { useSystemStore } from '@/stores/system'

type DetailTab = 'overview' | 'network' | 'storage' | 'services'

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
const detailsRequestGate = createRequestSequenceGate()
const mihomoRequestGate = createRequestSequenceGate()
const manualRefreshRegistry = useManualRefreshRegistry()
let unregisterManualRefresh: (() => void) | undefined

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
const {
  listenerQuery,
  listenerProtocol,
  listenerScope,
  listenerProtocolOptions,
  listenerScopeOptions,
  primaryInterfaces,
  secondaryInterfaces,
  primaryActiveInterfaceCount,
  listeningPortGroups,
  filteredListeningPortGroups,
  visibleListeningPortGroups,
  exposedListenerCount,
  localListenerCount,
  listenerResultLabel,
  showMoreListeningPorts,
} = useInfrastructureNetwork(details, tailscaleDetails)
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
const capabilityItems = computed(() => [
  { name: 'Docker Engine', enabled: Boolean(systemStore.capabilities?.docker), detail: systemStore.inventory?.engine.serverVersion ? `版本 ${systemStore.inventory.engine.serverVersion}` : '等待检测', icon: Boxes, type: 'docker' },
  { name: 'Docker Compose', enabled: Boolean(systemStore.capabilities?.compose), detail: `已发现 ${systemStore.services.length} 个项目`, icon: Database, type: 'database' },
  { name: 'systemd', enabled: Boolean(systemStore.capabilities?.systemd), detail: '宿主机服务管理', icon: Server, type: 'system' },
  { name: '系统日志服务（journald）', enabled: Boolean(systemStore.capabilities?.journald), detail: 'Linux 系统与服务日志', icon: Activity, type: 'system' },
  { name: '数据卷', enabled: Boolean(systemStore.capabilities?.dataVolumes?.length), detail: systemStore.capabilities?.dataVolumes?.join('、') || '未发现', icon: HardDrive, type: 'storage' },
  { name: '网络接口', enabled: Boolean(systemStore.capabilities?.networkInterfaces?.length), detail: `${systemStore.capabilities?.networkInterfaces?.length ?? 0} 个接口`, icon: Network, type: 'network' },
])

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

onMounted(() => {
  unregisterManualRefresh = manualRefreshRegistry?.register(handleManualRefresh)
  void loadDetails()
})

onBeforeUnmount(() => unregisterManualRefresh?.())
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

      <InfrastructureOverviewPanel
        v-if="activeTab === 'overview'"
        :details="details"
        :infrastructure-signals="infrastructureSignals"
        :primary-active-interface-count="primaryActiveInterfaceCount"
        :volume-count="volumeMounts.length"
        :format-bytes="formatBytes"
        :format-duration="formatDuration"
        :icons="{ cpu: Cpu, gauge: Gauge, hardDrive: HardDrive, memoryStick: MemoryStick, server: Server, wifi: Wifi }"
      />

      <section v-else-if="activeTab === 'network'" class="network-layout">
        <InfrastructureNetworkInterfacesPanel
          :details="details"
          :primary-interfaces="primaryInterfaces"
          :secondary-interfaces="secondaryInterfaces"
          :primary-active-interface-count="primaryActiveInterfaceCount"
          :listening-port-count="listeningPortGroups.length"
          :exposed-listener-count="exposedListenerCount"
          :local-listener-count="localListenerCount"
          :icons="{ gauge: Gauge, network: Network, route: Route, wifi: Wifi }"
        />

        <InfrastructureDnsPanel
          :details="details"
          :dns-details="dnsDetails"
          :route-icon="Route"
          @edit="openDNSEditor"
        />

        <InfrastructureProxyPanel
          :details="details"
          :tailscale-details="tailscaleDetails"
          :tailscale-status="tailscaleStatusLabel()"
          :tailscale-evidence="tailscaleEvidenceLabel()"
          :mihomo-inspection="mihomoInspection"
          :public-egress-details="publicEgressDetails"
          :public-egress-result="publicEgressResult"
          :public-egress-loading="publicEgressLoading"
          :public-egress-message="publicEgressMessage"
          :icons="{ activity: Activity, router: Router }"
          @refresh="checkMihomo(true)"
        />

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

      <InfrastructureStoragePanel
        v-else-if="activeTab === 'storage'"
        :details="details"
        :volume-mounts="volumeMounts"
        :auxiliary-mounts="auxiliaryMounts"
        :volume-total-bytes="volumeTotalBytes"
        :volume-used-bytes="volumeUsedBytes"
        :volume-used-percent="volumeUsedPercent"
        :physical-disks="physicalDisks"
        :auxiliary-disks="auxiliaryDisks"
        :format-bytes="formatBytes"
        :block-device-description="blockDeviceDescription"
        :block-device-kind-label="blockDeviceKindLabel"
        :block-device-transport="blockDeviceTransport"
        :icons="{ boxes: Boxes, database: Database, gauge: Gauge, hardDrive: HardDrive }"
      />

      <InfrastructureServicesPanel
        v-else
        :details="details"
        :capability-items="capabilityItems"
        :format-time="formatTime"
        :control-chain-icon="Waypoints"
      />
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
