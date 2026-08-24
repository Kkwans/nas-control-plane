<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElButton, ElDialog, ElInput } from 'element-plus'
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
  Server,
  Waypoints,
  Wifi,
} from '@lucide/vue'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import ActionButton from '@/components/ActionButton.vue'
import InfrastructureOverviewPanel from '@/components/InfrastructureOverviewPanel.vue'
import InfrastructureDnsPanel from '@/components/InfrastructureDnsPanel.vue'
import InfrastructureListenersPanel from '@/components/InfrastructureListenersPanel.vue'
import InfrastructureNetworkInterfacesPanel from '@/components/InfrastructureNetworkInterfacesPanel.vue'
import InfrastructureProxyPanel from '@/components/InfrastructureProxyPanel.vue'
import { type InfrastructureSignal } from '@/components/InfrastructureSignalSummary.vue'
import InfrastructureServicesPanel from '@/components/InfrastructureServicesPanel.vue'
import InfrastructureStoragePanel from '@/components/InfrastructureStoragePanel.vue'
import { useManualRefreshRegistry } from '@/composables/manualRefresh'
import { createRequestSequenceGate } from '@/domain/requestSequence'
import {
  blockDeviceDescription,
  blockDeviceKindLabel,
  blockDeviceTransport,
  formatBytes,
  formatDuration,
  formatTime,
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

        <InfrastructureListenersPanel
          :listener-query="listenerQuery"
          :listener-protocol="listenerProtocol"
          :listener-scope="listenerScope"
          :listener-protocol-options="listenerProtocolOptions"
          :listener-scope-options="listenerScopeOptions"
          :listening-port-groups="listeningPortGroups"
          :filtered-listening-port-groups="filteredListeningPortGroups"
          :visible-listening-port-groups="visibleListeningPortGroups"
          :exposed-listener-count="exposedListenerCount"
          :local-listener-count="localListenerCount"
          :listener-result-label="listenerResultLabel"
          :icon="Gauge"
          @update:listener-query="listenerQuery = $event"
          @update:listener-protocol="listenerProtocol = $event"
          @update:listener-scope="listenerScope = $event"
          @show-more="showMoreListeningPorts"
        />
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
.system-details-page { display: grid; gap: 16px; }
.system-tabs-toolbar { display: flex; min-width: 0; width: 100%; align-items: center; justify-content: space-between; gap: 14px; }
.system-tabs-toolbar .detail-tabs { min-width: 0; flex: 1; }
.detail-tabs { display: flex; align-items: center; gap: 3px; max-width: 100%; overflow: auto; padding-bottom: 2px; }
.detail-tabs button { display: inline-flex; min-height: 38px; flex: 0 0 auto; align-items: center; gap: 7px; padding: 0 13px; border: 1px solid transparent; border-radius: 9px; background: transparent; color: var(--ncp-text-muted); font-weight: 680; white-space: nowrap; transition: background var(--ncp-duration-fast), color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
.detail-tabs button:hover { background: var(--ncp-surface); color: var(--ncp-text); }
.detail-tabs button.active { border-color: var(--ncp-line); background: var(--ncp-surface); color: var(--ncp-primary-strong); box-shadow: var(--ncp-shadow-control); }
.details-skeleton { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; padding: 24px; }
.details-skeleton span { height: 108px; border-radius: 13px; background: linear-gradient(90deg, var(--ncp-surface-quiet), #fff, var(--ncp-surface-quiet)); background-size: 200% 100%; animation: skeleton 1.4s linear infinite; }
.details-error { display: flex; min-height: 220px; align-items: center; justify-content: center; gap: 14px; padding: 24px; color: var(--ncp-danger); }
.details-error div { max-width: 520px; }
.details-error p { margin: 4px 0; color: var(--ncp-text-muted); }
.warning-strip { display: flex; align-items: flex-start; gap: 9px; padding: 11px 14px; border: 1px solid var(--ncp-warning-border); border-radius: 11px; background: var(--ncp-warning-soft); color: var(--ncp-warning-strong); font-size: .82rem; }
.warning-strip svg { flex: 0 0 auto; margin-top: 1px; }
.network-layout { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; gap: 16px; }
.network-layout > * { min-width: 0; }
@keyframes skeleton { to { background-position: -200% 0; } }

:global(.dns-editor-dialog.el-dialog) { overflow: hidden; border: 1px solid var(--ncp-line); border-radius: 16px; background: var(--ncp-surface); box-shadow: var(--ncp-shadow-overlay); }
:global(.dns-editor-dialog .el-dialog__header) { margin: 0; padding: 20px 22px 16px; border-bottom: 1px solid var(--ncp-line); }
:global(.dns-editor-dialog .el-dialog__title) { color: var(--ncp-text); font-size: 1.02rem; font-weight: 750; }
:global(.dns-editor-dialog .el-dialog__body) { padding: 20px 22px; }
:global(.dns-editor-dialog .el-dialog__footer) { padding: 15px 22px; border-top: 1px solid var(--ncp-line); background: var(--ncp-surface-quiet); }
.dns-editor { display: grid; gap: 16px; }
.dns-editor__notice { display: flex; align-items: flex-start; gap: 10px; padding: 12px 13px; border: 1px solid var(--ncp-primary-border); border-radius: 11px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.dns-editor__notice > span { display: grid; gap: 3px; }
.dns-editor__notice strong { font-size: .8rem; }
.dns-editor__notice small { color: var(--ncp-text-muted); font-size: .71rem; line-height: 1.5; }
.dns-editor__field { display: grid; gap: 7px; }
.dns-editor__field > span { color: var(--ncp-text); font-size: .78rem; font-weight: 700; }
.dns-editor__field > small { color: var(--ncp-text-subtle); font-size: .69rem; }
.dns-editor__field :deep(.el-input__wrapper) { min-height: 42px; border-radius: var(--ncp-radius-control); background: var(--ncp-control-surface); box-shadow: 0 0 0 1px var(--ncp-control-border) inset; }
.dns-editor__field :deep(.el-input__wrapper.is-focus) { box-shadow: 0 0 0 1px var(--ncp-primary) inset, 0 0 0 3px var(--ncp-focus-ring); }
.dns-editor__message { margin: 0; padding: 9px 11px; border-radius: 9px; background: var(--ncp-neutral-soft); color: var(--ncp-text-muted); font-size: .72rem; line-height: 1.5; }
.dns-editor__footer { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 9px; }
.dns-preview { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); overflow: hidden; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-line); }
.dns-preview > div { display: grid; min-width: 0; gap: 4px; padding: 10px 12px; background: var(--ncp-surface-quiet); }
.dns-preview span { color: var(--ncp-text-subtle); font-size: .68rem; }
.dns-preview strong { overflow: hidden; font-family: var(--ncp-font-mono); font-size: .74rem; text-overflow: ellipsis; white-space: nowrap; }

@media (max-width: 1050px) {
  .network-layout { grid-template-columns: 1fr; }
}
@media (max-width: 760px) {
  .system-tabs-toolbar { align-items: stretch; flex-direction: column; gap: 9px; }
  .system-tabs-toolbar .detail-tabs { width: 100%; flex: none; }
}
@media (max-width: 620px) {
  .dns-preview { grid-template-columns: 1fr; }
}
@media (max-width: 520px) {
  .detail-tabs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); overflow: visible; }
  .detail-tabs button { padding-inline: 10px; }
  .detail-tabs button { justify-content: center; }
  .detail-tabs button svg { display: none; }
  .details-skeleton { grid-template-columns: 1fr; }
}
@media (prefers-reduced-motion: reduce) {
  .details-skeleton span { animation: none; }
}
</style>
