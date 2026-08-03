<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Activity,
  AlertTriangle,
  Boxes,
  CheckCircle2,
  Cpu,
  Database,
  Gauge,
  HardDrive,
  MemoryStick,
  Network,
  RefreshCw,
  Route,
  Router,
  Server,
  ShieldCheck,
  Waypoints,
  Wifi,
} from '@lucide/vue'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import {
  confirmDNSChange,
  detectPublicEgress,
  previewDNSChange,
  requestSystemDetails,
  rollbackDNSChange,
  type DNSCapability,
  type DNSChangePreview,
  type PublicEgressResult,
  type SystemDetails,
  type TailscaleCapability,
} from '@/api/system'
import { useSystemStore } from '@/stores/system'

type DetailTab = 'overview' | 'network' | 'storage' | 'services'

const systemStore = useSystemStore()
const details = ref<SystemDetails | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const activeTab = ref<DetailTab>('overview')
const dnsDraft = ref('')
const dnsPreview = ref<DNSChangePreview | null>(null)
const dnsChangeId = ref('')
const dnsMessage = ref('')
const publicEgressResult = ref<PublicEgressResult | null>(null)
const publicEgressMessage = ref('')

const dnsDetails = computed<DNSCapability>(() => details.value?.dns ?? {
  backend: 'unknown', detected: false, state: 'unknown', readOnly: true, canRead: false,
  canPreview: false, canConfirm: false, canRollback: false, nameservers: [],
  detectionSource: '', errorCode: '',
})
const tailscaleDetails = computed<TailscaleCapability>(() => details.value?.tailscale ?? {
  detected: false, state: 'not-found', backendState: 'unknown', version: '', interface: '',
  overlayIps: [], online: false, linkState: 'unknown', heartbeatState: 'unknown', reachable: false,
  evidence: [], warnings: [],
})
const publicEgressDetails = computed(() => details.value?.publicEgress ?? {
  configured: false, status: 'unavailable', endpoint: '', requiresUserAction: true,
  detectionSource: '', errorCode: 'PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED',
})

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
const primaryInterfaces = computed(() => networkInterfaces.value
  .filter((item) => item.name !== 'lo' && !isVirtualInterface(item.name))
  .sort((left, right) => Number(right.state === 'up') - Number(left.state === 'up'))
  .slice(0, 4))
const primaryInterfaceNames = computed(() => new Set(primaryInterfaces.value.map((item) => item.name)))
const secondaryInterfaces = computed(() => networkInterfaces.value.filter((item) => !primaryInterfaceNames.value.has(item.name)))
const activeInterfaceCount = computed(() => networkInterfaces.value.filter((item) => item.name !== 'lo' && item.state === 'up').length)
const volumeMounts = computed(() => (details.value?.storage.mounts ?? []).filter((item) => item.path === '/' || item.path.startsWith('/volume')))
const auxiliaryMounts = computed(() => (details.value?.storage.mounts ?? []).filter((item) => !volumeMounts.value.some((volume) => volume.path === item.path)))
const volumeTotalBytes = computed(() => volumeMounts.value.reduce((total, item) => total + item.totalBytes, 0))
const volumeUsedBytes = computed(() => volumeMounts.value.reduce((total, item) => total + item.usedBytes, 0))
const volumeUsedPercent = computed(() => volumeTotalBytes.value ? volumeUsedBytes.value / volumeTotalBytes.value * 100 : 0)
const physicalDisks = computed(() => (details.value?.storage.disks ?? []).filter((item) => !/^(loop|zram|mmcblk\d+boot)/i.test(item.name)))
const auxiliaryDisks = computed(() => (details.value?.storage.disks ?? []).filter((item) => !physicalDisks.value.some((disk) => disk.name === item.name)))
const listeningPortGroups = computed(() => {
  const groups = new Map<string, { port: number; protocol: string; addresses: string[]; pids: number[]; services: Array<{ label: string; detail: string }> }>()
  for (const item of details.value?.network.listeningPorts ?? []) {
    const key = `${item.protocol}:${item.port}`
    const group = groups.get(key) ?? { port: item.port, protocol: item.protocol, addresses: [], pids: [], services: [] }
    if (item.address && !group.addresses.includes(item.address)) group.addresses.push(item.address)
    if (item.pid && !group.pids.includes(item.pid)) group.pids.push(item.pid)
    const label = item.service || item.containerName || item.systemdUnit || item.processName || item.executable
    const detail = [item.containerName, item.systemdUnit, item.processName, item.executable]
      .filter((value, index, values): value is string => Boolean(value) && values.indexOf(value) === index && value !== label)
      .join(' · ')
    if (label && !group.services.some((service) => service.label === label)) group.services.push({ label, detail })
    groups.set(key, group)
  }
  return [...groups.values()].sort((left, right) => left.port - right.port)
})

const capabilityItems = computed(() => [
  { name: 'Docker Engine', enabled: Boolean(systemStore.capabilities?.docker), detail: systemStore.inventory?.engine.serverVersion ? `版本 ${systemStore.inventory.engine.serverVersion}` : '等待检测', icon: Boxes, type: 'docker' },
  { name: 'Docker Compose', enabled: Boolean(systemStore.capabilities?.compose), detail: `已发现 ${systemStore.services.length} 个项目`, icon: Database, type: 'database' },
  { name: 'systemd', enabled: Boolean(systemStore.capabilities?.systemd), detail: '宿主机服务管理', icon: Server, type: 'system' },
  { name: '系统日志服务（journald）', enabled: Boolean(systemStore.capabilities?.journald), detail: 'Linux 系统与服务日志', icon: Activity, type: 'system' },
  { name: '数据卷', enabled: Boolean(systemStore.capabilities?.dataVolumes?.length), detail: systemStore.capabilities?.dataVolumes?.join('、') || '未发现', icon: HardDrive, type: 'storage' },
  { name: '网络接口', enabled: Boolean(systemStore.capabilities?.networkInterfaces?.length), detail: `${systemStore.capabilities?.networkInterfaces?.length ?? 0} 个接口`, icon: Network, type: 'network' },
])

async function loadDetails() {
  loading.value = true
  errorMessage.value = ''
  try {
    details.value = await requestSystemDetails()
    dnsDraft.value = dnsDetails.value.nameservers.join(', ')
    dnsPreview.value = null
    dnsChangeId.value = ''
    dnsMessage.value = ''
    publicEgressResult.value = null
    publicEgressMessage.value = ''
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '系统详情暂不可用'
  } finally {
    loading.value = false
  }
}

function dnsServersFromDraft() {
  return [...new Set(dnsDraft.value.split(/[\s,，]+/).map((value) => value.trim()).filter(Boolean))]
}

async function previewDNS() {
  if (!dnsDetails.value.canPreview || dnsDetails.value.readOnly) return
  const nameservers = dnsServersFromDraft()
  if (!nameservers.length) {
    dnsMessage.value = '至少填写一个 DNS 地址。'
    return
  }
  dnsMessage.value = ''
  try {
    dnsPreview.value = await previewDNSChange({ nameservers })
    dnsMessage.value = dnsPreview.value.requiresConfirm ? '预览已生成，确认后才会应用。' : '当前后端不要求二次确认。'
  } catch (error) {
    dnsMessage.value = error instanceof Error ? error.message : 'DNS 预览失败。'
  }
}

async function confirmDNS() {
  if (!dnsPreview.value) return
  try {
    const result = await confirmDNSChange({ previewId: dnsPreview.value.previewId, confirmed: true })
    const rollbackId = result.applied && result.rollbackAvailable ? result.changeId : ''
    await loadDetails()
    dnsChangeId.value = rollbackId
    dnsMessage.value = result.applied ? 'DNS 已应用。' : `DNS 未应用（${result.errorCode || '未知原因'}）。`
  } catch (error) {
    dnsMessage.value = error instanceof Error ? error.message : 'DNS 应用失败。'
  }
}

async function rollbackDNS() {
  if (!dnsChangeId.value) return
  try {
    const result = await rollbackDNSChange(dnsChangeId.value)
    await loadDetails()
    dnsMessage.value = result.applied ? 'DNS 已回滚。' : `DNS 未回滚（${result.errorCode || '未知原因'}）。`
  } catch (error) {
    dnsMessage.value = error instanceof Error ? error.message : 'DNS 回滚失败。'
  }
}

async function checkPublicEgress() {
  if (!publicEgressDetails.value.configured) {
    publicEgressMessage.value = '未配置公网出口探针；不会把 Tailscale Overlay IP 当作公网 IP。'
    return
  }
  publicEgressMessage.value = ''
  try {
    publicEgressResult.value = await detectPublicEgress()
    if (publicEgressResult.value.errorCode) publicEgressMessage.value = publicEgressResult.value.errorCode
  } catch (error) {
    publicEgressMessage.value = error instanceof Error ? error.message : '公网出口检测失败。'
  }
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

function isVirtualInterface(name: string) {
  return /^(br-|docker|veth|virbr|tun|tap|tailscale)/i.test(name)
}

function interfaceAddress(item: SystemDetails['network']['interfaces'][number]) {
  return item.addresses.find((address) => address.family === 'ipv4') ?? item.addresses[0]
}

function interfaceKind(name: string) {
  if (/^(br-|docker|veth)/i.test(name)) return 'Docker 虚拟接口'
  if (/^tailscale/i.test(name)) return 'Tailscale Overlay 接口'
  if (/^(tun|tap)/i.test(name)) return '代理虚拟接口'
  if (name === 'lo') return '本机回环'
  return '主机网络接口'
}

function proxyStateLabel(value: string, detected: boolean) {
  if (detected && value === 'running') return '代理核心运行中'
  if (value === 'not-found') return '未发现代理核心'
  return detected ? '已发现代理核心' : '代理状态未知'
}

function diskKind(rotational: boolean) {
  return rotational ? '机械硬盘' : '固态 / 闪存'
}

function formatTime(value: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

onMounted(loadDetails)
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
          <div class="collection-meta">
            <span class="collection-time">原采集于 {{ formatTime(details?.collectedAt ?? '') }}</span>
            <el-button class="system-refresh" :loading="loading" @click="loadDetails"><RefreshCw :size="16" /><span>刷新</span></el-button>
          </div>
        </div>
      </template>
    </WorkspaceHeader>

    <section v-if="loading && !details" class="details-skeleton panel" aria-label="正在加载系统详情">
      <span v-for="index in 8" :key="index"></span>
    </section>

    <section v-else-if="errorMessage && !details" class="details-error panel">
      <AlertTriangle :size="24" />
      <div><strong>系统详情暂不可用</strong><p>{{ errorMessage }}</p></div>
      <el-button @click="loadDetails">重试</el-button>
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

        <article class="panel overview-facts">
          <div><span>系统版本</span><strong>{{ details.device.operatingSystem || '不可用' }}</strong></div>
          <div><span>内核版本</span><strong>{{ details.device.kernelVersion || '不可用' }}</strong></div>
          <div><span>系统架构</span><strong>{{ details.device.architecture || '不可用' }}</strong></div>
          <div><span>运行时间</span><strong>{{ formatDuration(details.device.uptimeSeconds) }}</strong></div>
          <div><span>运行进程</span><strong>{{ details.device.processCount.toLocaleString('zh-CN') }}</strong></div>
          <div class="overview-fact--cgroup"><span>资源隔离（cgroup）</span><strong>{{ details.device.cgroupVersion || '不可用' }}</strong><small>Linux 控制组版本，用于统计与限制进程资源</small></div>
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
            <div><Wifi :size="18" /><span>已连接接口</span><strong>{{ activeInterfaceCount }}</strong></div>
            <div><HardDrive :size="18" /><span>存储卷</span><strong>{{ volumeMounts.length }}</strong></div>
          </div>
        </article>
      </section>

      <section v-else-if="activeTab === 'network'" class="network-layout">
        <article class="network-summary-grid network-layout__full">
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Wifi :size="18" /></span><div><small>当前联网</small><strong>{{ activeInterfaceCount }} 个接口</strong><p>{{ primaryInterfaces.map((item) => item.name).join('、') || '未发现主网络接口' }}</p></div></div>
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Route :size="18" /></span><div><small>默认出口</small><strong>{{ details.network.gateway || '未发现' }}</strong><p>路由 {{ details.network.routes.length }} 条</p></div></div>
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Network :size="18" /></span><div><small>DNS 服务</small><strong>{{ details.network.dnsServers.length || 0 }} 个</strong><p>{{ details.network.dnsServers.slice(0, 2).join('、') || '未发现解析服务' }}</p></div></div>
          <div class="network-summary-card panel"><span class="network-summary-card__icon"><Gauge :size="18" /></span><div><small>监听端点</small><strong>{{ listeningPortGroups.length }} 个端口</strong><p>仅展示宿主机服务入口</p></div></div>
        </article>

        <article class="panel detail-card network-layout__full">
          <header class="detail-card__header type-network"><Network :size="20" /><div><h2>主要网络连接</h2><p>只展示影响 NAS 联网的真实接口；Docker 和代理虚拟接口收进下方明细</p></div></header>
          <div v-if="primaryInterfaces.length" class="primary-interface-list">
            <div v-for="item in primaryInterfaces" :key="item.name" class="primary-interface-row">
              <span class="primary-interface-row__status" :class="{ online: item.state === 'up' }"><i></i>{{ item.state === 'up' ? '已连接' : '未连接' }}</span>
              <div class="primary-interface-row__name"><strong>{{ item.name }}</strong><small>{{ interfaceKind(item.name) }}</small></div>
              <div><span>IP 地址</span><strong>{{ interfaceAddress(item) ? `${interfaceAddress(item)?.address}/${interfaceAddress(item)?.prefixLength}` : '未分配' }}</strong></div>
              <div><span>链路</span><strong>{{ item.speedMbps > 0 ? `${item.speedMbps} Mbps` : '速率未知' }}<template v-if="item.duplex"> · {{ item.duplex }}</template></strong></div>
            </div>
          </div>
          <div v-else class="inline-empty">未发现可用于联网的主机接口。</div>
          <details v-if="secondaryInterfaces.length" class="network-details">
            <summary>查看所有接口明细 <span>{{ secondaryInterfaces.length }} 个虚拟 / 辅助接口</span></summary>
            <div class="network-details__list">
              <div v-for="item in secondaryInterfaces" :key="item.name" class="network-detail-row">
                <div><strong>{{ item.name }}</strong><small>{{ interfaceKind(item.name) }}</small></div>
                <span :class="{ 'is-online': item.state === 'up' }">{{ item.state === 'up' ? '已连接' : '未连接' }}</span>
                <span>{{ item.addresses.length }} 个地址</span>
                <code>{{ item.hardwareAddress || '无 MAC' }}</code>
              </div>
            </div>
          </details>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-network"><Route :size="20" /><div><h2>路由与 DNS</h2><p>默认网关和解析服务</p></div></header>
          <dl class="definition-grid">
            <div class="definition-grid__wide"><dt>默认网关</dt><dd>{{ details.network.gateway || '未发现' }}</dd></div>
            <div class="definition-grid__wide"><dt>DNS</dt><dd>{{ details.network.dnsServers.join('、') || '未发现' }}</dd></div>
            <div><dt>路由数量</dt><dd>{{ details.network.routes.length }}</dd></div>
            <div><dt>默认出口</dt><dd>{{ details.network.routes.find((route) => route.destination === '0.0.0.0/0')?.interface || '未识别' }}</dd></div>
          </dl>
          <div class="dns-management">
            <div class="dns-management__state">
              <span :class="['capability-state', { off: !dnsDetails.detected || dnsDetails.readOnly }]"><i></i>{{ dnsDetails.readOnly ? '只读展示' : dnsDetails.detected ? '支持安全修改' : '未检测到可管理后端' }}</span>
              <small>{{ dnsDetails.readOnly ? '当前使用静态 /etc/resolv.conf，NCP 不会直接覆盖它。' : dnsDetails.detectionSource || 'DNS 能力未报告' }}</small>
            </div>
            <template v-if="dnsDetails.canPreview && dnsDetails.canConfirm && !dnsDetails.readOnly">
              <label class="dns-management__input"><span>DNS 服务器</span><input v-model="dnsDraft" type="text" placeholder="例如 223.5.5.5, 1.1.1.1" /></label>
              <div class="dns-management__actions"><button type="button" class="text-button" @click="previewDNS">预览修改</button><button v-if="dnsPreview" type="button" class="text-button text-button--primary" @click="confirmDNS">确认应用</button><button v-if="dnsChangeId" type="button" class="text-button" @click="rollbackDNS">回滚</button></div>
            </template>
            <small v-if="dnsMessage" class="dns-management__message" role="status">{{ dnsMessage }}</small>
          </div>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-site"><Router :size="20" /><div><h2>代理状态</h2><p>基于配置、进程、网卡和环境的证据扫描</p></div></header>
          <div class="proxy-summary">
            <span :class="{ active: details.proxy.mihomo.detected }"><i></i>{{ proxyStateLabel(details.proxy.mihomo.state, details.proxy.mihomo.detected) }}</span>
            <strong>{{ details.proxy.mihomo.detected ? 'Mihomo / Clash' : '未发现 Mihomo / Clash' }}</strong>
            <p>{{ details.proxy.mihomo.detail || '当前未取得更多服务信息' }}</p>
            <div class="proxy-facts">
              <div><small>Tailscale Overlay</small><strong>{{ tailscaleDetails.reachable ? '已连接' : tailscaleDetails.detected ? '已发现，链路未确认' : '未发现' }}</strong><code>{{ tailscaleDetails.overlayIps.join('、') || '无 Overlay IP' }}</code></div>
              <div><small>公网出口</small><strong>{{ publicEgressResult?.address || (publicEgressDetails.configured ? '待手动检测' : '未配置探针') }}</strong><code>{{ publicEgressResult?.country || publicEgressResult?.region || '不把 Tailscale IP 当公网 IP' }}</code></div>
            </div>
            <small>{{ details.proxy.mihomoCapability?.controller.operations?.length ? `已开放：${details.proxy.mihomoCapability.controller.operations.join('、')}` : '当前只提供进程与控制器状态；域名、进程和容器分流规则未开放。' }}</small>
            <button v-if="publicEgressDetails.configured" type="button" class="text-button text-button--primary" @click="checkPublicEgress">检测公网出口</button>
            <small v-if="publicEgressMessage" class="proxy-message" role="status">{{ publicEgressMessage }}</small>
          </div>
        </article>

        <article class="panel detail-card proxy-rules-card network-layout__full">
          <header class="detail-card__header type-site"><ShieldCheck :size="20" /><div><h2>代理分流能力</h2><p>只在控制器真实开放对应 API 时提供管理入口</p></div></header>
          <div class="proxy-rules-grid">
            <div><strong>域名规则</strong><span>未开放</span><small>当前 Agent 不读取或修改 Mihomo 配置文件。</small></div>
            <div><strong>应用进程规则</strong><span>未开放</span><small>没有可靠的进程级流量归属接口，不显示猜测结果。</small></div>
            <div><strong>Docker 容器规则</strong><span>未开放</span><small>容器网络命名空间与控制器规则尚未建立安全映射。</small></div>
          </div>
          <p class="proxy-rules-note">可用的控制器操作：{{ details.proxy.mihomoCapability?.controller.operations?.join('、') || '无' }}。后续只有完成能力探测、规则校验和回滚设计后，才会开放修改。</p>
        </article>

        <details class="panel network-details ports-disclosure network-layout__full">
          <summary>
            <span class="ports-disclosure__title"><Gauge :size="19" /><span><strong>监听服务</strong><small>按端口合并显示，仅在需要排查入口时展开</small></span></span>
            <span>{{ listeningPortGroups.length }} 个端口</span>
          </summary>
            <div v-if="listeningPortGroups.length" class="port-grid">
            <span v-for="item in listeningPortGroups" :key="`${item.protocol}-${item.port}`">
              <b>{{ item.port }}</b><small>{{ item.protocol.toUpperCase() }} · {{ item.addresses.join('、') || '*' }}</small><em v-if="item.services.length" :title="item.services.map((service) => service.detail ? `${service.label} · ${service.detail}` : service.label).join('、')">应用 {{ item.services.map((service) => service.label).join('、') }}</em><em v-else-if="item.pids.length">PID {{ item.pids.join('、') }}</em>
            </span>
          </div>
          <div v-else class="inline-empty">未取得监听服务信息。</div>
        </details>
      </section>

      <section v-else-if="activeTab === 'storage'" class="storage-layout">
        <article class="storage-summary-grid storage-layout__full">
          <div class="storage-summary-card panel"><span class="storage-summary-card__icon"><HardDrive :size="18" /></span><div><small>可管理存储卷</small><strong>{{ volumeMounts.length }}</strong><p>{{ volumeMounts.map((item) => item.path).join('、') || '未发现 /volume 存储卷' }}</p></div></div>
          <div class="storage-summary-card panel"><span class="storage-summary-card__icon"><Gauge :size="18" /></span><div><small>合计已用</small><strong>{{ formatBytes(volumeUsedBytes) }}</strong><p>总容量 {{ formatBytes(volumeTotalBytes) }}</p></div></div>
          <div class="storage-summary-card panel"><span class="storage-summary-card__icon"><Database :size="18" /></span><div><small>使用率</small><strong>{{ volumeTotalBytes ? `${volumeUsedPercent.toFixed(1)}%` : '—' }}</strong><p>按 NAS 存储卷合计计算</p></div></div>
        </article>

        <article class="panel detail-card storage-layout__full">
          <header class="detail-card__header type-storage"><HardDrive :size="20" /><div><h2>存储卷</h2><p>仅展示用户真正关心的根目录和 /volume 数据卷；系统镜像挂载点收进明细</p></div></header>
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
          <header class="detail-card__header type-storage"><Database :size="20" /><div><h2>物理磁盘</h2><p>只展示可识别的物理设备，zram、启动分区等不再占据主视图</p></div></header>
          <div v-if="physicalDisks.length" class="disk-list">
            <div v-for="disk in physicalDisks" :key="disk.name" class="disk-row">
              <span class="disk-row__icon"><HardDrive :size="16" /></span>
              <div><strong>{{ disk.name }}</strong><small>{{ disk.model || '型号未知' }} · {{ diskKind(disk.rotational) }}</small></div>
              <span class="disk-row__size">{{ formatBytes(disk.sizeBytes) }}</span>
              <span :class="['disk-health', { unknown: !disk.health || disk.health === 'unknown' }]">{{ disk.health && disk.health !== 'unknown' ? disk.health : '健康状态未知' }}</span>
            </div>
          </div>
          <div v-else class="inline-empty">系统未暴露物理磁盘信息。</div>
          <details v-if="auxiliaryDisks.length" class="storage-details"><summary>查看辅助设备 <span>{{ auxiliaryDisks.length }} 个</span></summary><div class="storage-details__list"><div v-for="disk in auxiliaryDisks" :key="disk.name"><strong>{{ disk.name }}</strong><span>{{ diskKind(disk.rotational) }}</span><span>{{ formatBytes(disk.sizeBytes) }}</span></div></div></details>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-system"><Boxes :size="20" /><div><h2>存储阵列</h2><p>阵列级别、状态与成员设备</p></div></header>
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
          <header class="detail-card__header type-system"><Waypoints :size="20" /><div><h2>控制链路</h2><p>Web 控制台至 Root Agent 的真实请求路径</p></div></header>
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
  </div>
</template>

<style scoped>
.system-details-page{display:grid;gap:16px}.detail-tabs{display:flex;align-items:center;gap:3px}.detail-tabs button{display:inline-flex;min-height:38px;align-items:center;gap:7px;padding:0 13px;border:1px solid transparent;border-radius:9px;background:transparent;color:var(--ncp-text-muted);font-weight:680;transition:background var(--ncp-duration-fast),color var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast)}.detail-tabs button:hover{background:var(--ncp-surface);color:var(--ncp-text)}.detail-tabs button.active{border-color:var(--ncp-line);background:var(--ncp-surface);color:var(--ncp-primary-strong);box-shadow:var(--ncp-shadow-control)}.collection-time{color:var(--ncp-text-subtle);font-size:.78rem;white-space:nowrap}.details-skeleton{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;padding:24px}.details-skeleton span{height:108px;border-radius:13px;background:linear-gradient(90deg,var(--ncp-surface-quiet),#fff,var(--ncp-surface-quiet));background-size:200% 100%;animation:skeleton 1.4s linear infinite}.details-error{display:flex;min-height:220px;align-items:center;justify-content:center;gap:14px;padding:24px;color:var(--ncp-danger)}.details-error div{max-width:520px}.details-error p{margin:4px 0;color:var(--ncp-text-muted)}.warning-strip{display:flex;align-items:flex-start;gap:9px;padding:11px 14px;border:1px solid var(--ncp-warning-border);border-radius:11px;background:var(--ncp-warning-soft);color:var(--ncp-warning-strong);font-size:.82rem}.warning-strip svg{flex:0 0 auto;margin-top:1px}.overview-layout,.content-grid,.network-layout,.storage-layout,.services-layout{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.device-summary{display:flex;grid-column:1/-1;align-items:center;gap:16px;padding:22px;background:radial-gradient(circle at 6% 0,rgba(52,116,212,.08),transparent 30%),linear-gradient(115deg,#fff 64%,var(--ncp-surface-quiet))}.device-summary__icon{display:grid;width:60px;height:60px;flex:0 0 auto;place-items:center;border:1px solid color-mix(in srgb,var(--ncp-primary) 18%,transparent);border-radius:17px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.device-summary__identity{min-width:0}.device-summary__identity>span{color:var(--ncp-text-subtle);font-size:.75rem;font-weight:700}.device-summary__identity h2{margin:2px 0;font-size:1.4rem;letter-spacing:-.03em}.device-summary__identity p{margin:0;color:var(--ncp-text-muted);font-size:.86rem}.device-summary__health{display:grid;margin-left:auto;justify-items:end;gap:5px}.device-summary__health>span,.proxy-summary>span{display:inline-flex;width:max-content;align-items:center;gap:7px;padding:6px 10px;border:1px solid var(--ncp-success-border);border-radius:999px;background:var(--ncp-success-soft);color:var(--ncp-success-strong);font-size:.78rem;font-weight:700}.device-summary__health i,.proxy-summary i,.capability-state i{width:7px;height:7px;border-radius:50%;background:currentColor}.device-summary__health small{color:var(--ncp-text-subtle);font-size:.75rem}.overview-facts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));grid-column:1/-1;padding:6px 18px}.overview-facts>div{min-width:0;padding:15px 12px;border-bottom:1px solid var(--ncp-line)}.overview-facts>div:nth-child(n+4){border-bottom:0}.overview-facts span,.definition-grid dt{color:var(--ncp-text-subtle);font-size:.75rem}.overview-facts strong,.definition-grid dd{display:block;overflow:hidden;margin-top:5px;color:var(--ncp-text);font-family:var(--ncp-font-latin);font-size:.88rem;text-overflow:ellipsis;white-space:nowrap}.overview-section{grid-column:1/-1;padding:18px}.overview-section>header,.detail-card__header{display:flex;align-items:center;gap:11px}.overview-section>header>svg{color:var(--ncp-primary)}.overview-section h2,.detail-card__header h2{margin:0;font-size:1rem}.overview-section p,.detail-card__header p{margin:3px 0 0;color:var(--ncp-text-subtle);font-size:.78rem}.resource-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin-top:14px}.resource-summary>div{display:grid;grid-template-columns:auto 1fr;align-items:center;gap:2px 9px;padding:13px;border:1px solid var(--ncp-line);border-radius:12px;background:var(--ncp-surface-quiet)}.resource-summary svg{grid-row:1/3;color:var(--ncp-primary)}.resource-summary span{color:var(--ncp-text-subtle);font-size:.73rem}.resource-summary strong{font-family:var(--ncp-font-latin);font-size:.95rem}.detail-card{overflow:hidden}.content-grid__full,.network-layout__full,.storage-layout__full,.services-layout__full{grid-column:1/-1}.detail-card__header{padding:17px 18px;border-bottom:1px solid var(--ncp-line);background:linear-gradient(135deg,var(--ncp-surface),var(--ncp-surface-quiet))}.detail-card__header>svg{box-sizing:content-box;padding:9px;border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary)}.detail-card__header.type-network>svg{background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.detail-card__header.type-storage>svg{background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.detail-card__header.type-system>svg{background:var(--ncp-object-system-soft);color:var(--ncp-object-system)}.detail-card__header.type-site>svg{background:var(--ncp-object-site-soft);color:var(--ncp-object-site)}.definition-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));margin:0;padding:8px 18px 16px}.definition-grid>div{min-width:0;padding:13px 4px;border-bottom:1px solid var(--ncp-line)}.definition-grid__wide{grid-column:1/-1}.definition-grid dd{margin:5px 0 0}.memory-meter{display:grid;gap:12px;padding:25px 20px}.memory-meter>div:first-child{display:flex;align-items:baseline;gap:6px}.memory-meter strong{font-family:var(--ncp-font-latin);font-size:1.45rem}.memory-meter span,.memory-meter small{color:var(--ncp-text-subtle)}.meter,.usage-cell i{overflow:hidden;height:7px;border-radius:999px;background:var(--ncp-surface-sunken)}.meter i,.usage-cell b{display:block;height:100%;border-radius:inherit;background:var(--ncp-object-storage)}.sensor-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;padding:18px}.sensor-grid>div{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.sensor-grid span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.sensor-grid strong{font-family:var(--ncp-font-latin)}.inline-empty{padding:28px 18px;color:var(--ncp-text-subtle);text-align:center}.interface-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;padding:16px}.interface-card{overflow:hidden;border:1px solid var(--ncp-line);border-radius:13px;background:var(--ncp-surface)}.interface-card>header{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px 14px;border-bottom:1px solid var(--ncp-line);background:var(--ncp-surface-quiet)}.interface-card>header>div{display:flex;align-items:center;gap:8px;color:var(--ncp-object-network)}.interface-card>header span{color:var(--ncp-neutral-strong);font-size:.72rem;font-weight:700}.interface-card>header span.online{color:var(--ncp-success-strong)}.interface-card dl{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));margin:0;padding:4px 14px 12px}.interface-card dl>div{min-width:0;padding:10px 4px}.interface-card dt{color:var(--ncp-text-subtle);font-size:.7rem}.interface-card dd{overflow:hidden;margin:4px 0 0;font-family:var(--ncp-font-latin);font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.proxy-summary{display:grid;gap:12px;padding:22px 18px}.proxy-summary>span{border-color:var(--ncp-neutral-border);background:var(--ncp-neutral-soft);color:var(--ncp-neutral-strong)}.proxy-summary>span.active{border-color:var(--ncp-success-border);background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.proxy-summary p{margin:0;color:var(--ncp-text-muted);font-size:.82rem}.evidence-table,.resource-table{display:grid}.evidence-table__header,.evidence-table>div,.resource-table__header,.resource-table>div{display:grid;grid-template-columns:1.1fr .55fr .55fr 1.8fr;align-items:center;gap:12px;min-height:48px;padding:8px 18px;border-bottom:1px solid var(--ncp-line)}.evidence-table__header,.resource-table__header{min-height:42px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle);font-size:.73rem;font-weight:700}.evidence-table>div:last-child,.resource-table>div:last-child{border-bottom:0}.evidence-table strong{display:grid;gap:2px}.evidence-table strong small{color:var(--ncp-text-subtle);font-weight:500}.evidence-table code{overflow:hidden;color:var(--ncp-text-muted);font-size:.75rem;text-overflow:ellipsis;white-space:nowrap}.evidence-confirmed{color:var(--ncp-success-strong);font-weight:700}.evidence-inferred{color:var(--ncp-warning-strong);font-weight:700}.evidence-unknown{color:var(--ncp-neutral-strong)}.port-grid{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:8px;padding:16px}.port-grid>span{display:grid;gap:2px;padding:10px 12px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.port-grid b{color:var(--ncp-object-network);font-family:var(--ncp-font-mono)}.port-grid small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.resource-table__header,.resource-table>div{grid-template-columns:1.2fr 1.2fr 1fr .8fr}.resource-table>div>span,.resource-table>div>strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.resource-table>div>span{color:var(--ncp-text-muted);font-size:.8rem}.usage-cell{display:grid!important;grid-template-columns:minmax(70px,1fr) auto;align-items:center;gap:8px}.usage-cell i{display:block;width:100%}.compact-list{display:grid;padding:6px 18px 14px}.compact-list>div{display:grid;grid-template-columns:1.2fr .8fr 1fr;align-items:center;gap:12px;min-height:58px;border-bottom:1px solid var(--ncp-line)}.compact-list>div:last-child{border-bottom:0}.compact-list>div>span:first-child{display:grid;gap:2px}.compact-list small{color:var(--ncp-text-subtle)}.control-chain{display:flex;align-items:stretch;gap:0;margin:0;padding:20px;list-style:none}.control-chain li{position:relative;display:grid;min-width:0;flex:1;grid-template-columns:36px minmax(0,1fr);align-items:start;gap:10px;padding-right:22px}.control-chain li:not(:last-child)::after{position:absolute;top:17px;right:4px;left:46px;height:1px;background:var(--ncp-line-strong);content:''}.control-chain__index{position:relative;z-index:1;display:grid;width:36px;height:36px;place-items:center;border:1px solid var(--ncp-primary-border);border-radius:10px;background:var(--ncp-surface);color:var(--ncp-primary-strong);font-family:var(--ncp-font-mono);font-weight:750}.control-chain li>div:nth-child(2){position:relative;z-index:1;display:grid;gap:3px;background:var(--ncp-surface)}.control-chain li small{color:var(--ncp-text-subtle);font-size:.72rem}.control-chain__meta{display:grid;grid-column:2;gap:3px;margin-top:7px}.control-chain__meta>span{width:max-content;color:var(--ncp-neutral-strong);font-size:.7rem;font-weight:700}.control-chain__meta>.status-ready{color:var(--ncp-success-strong)}.capability-card{display:grid;grid-template-columns:44px minmax(0,1fr) auto;align-items:center;gap:12px;padding:16px}.capability-card__icon{display:grid;width:44px;height:44px;place-items:center;border-radius:12px}.type-docker{background:var(--ncp-object-docker-soft);color:var(--ncp-object-docker)}.type-database{background:var(--ncp-engine-sqlite-soft);color:var(--ncp-engine-sqlite)}.type-system{background:var(--ncp-object-system-soft);color:var(--ncp-object-system)}.type-storage{background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.type-network{background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.capability-card>div{display:grid;min-width:0;gap:3px}.capability-card small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.75rem;text-overflow:ellipsis;white-space:nowrap}.capability-state{display:inline-flex;width:max-content;align-items:center;gap:6px;color:var(--ncp-success-strong);font-size:.72rem;font-weight:700}.capability-state.off{color:var(--ncp-neutral-strong)}@keyframes skeleton{to{background-position:-200% 0}}@media(max-width:1050px){.detail-tabs{overflow:auto;max-width:100%;padding-bottom:2px}.content-grid,.network-layout,.storage-layout{grid-template-columns:1fr}.resource-summary{grid-template-columns:repeat(2,minmax(0,1fr))}.control-chain{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.control-chain li{padding:0}.control-chain li::after{display:none!important}.port-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}@media(max-width:760px){.overview-facts{grid-template-columns:repeat(2,minmax(0,1fr))}.overview-facts>div:nth-child(n+3){border-bottom:0}.device-summary{align-items:flex-start;flex-wrap:wrap}.device-summary__health{width:100%;margin-left:76px;justify-items:start}.interface-grid{grid-template-columns:1fr}.sensor-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.evidence-table__header{display:none}.evidence-table>div{grid-template-columns:1fr 1fr;padding-block:12px}.resource-table__header{display:none}.resource-table>div{grid-template-columns:1fr 1fr;padding-block:12px}.compact-list>div{grid-template-columns:1fr}.port-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.services-layout{grid-template-columns:1fr}.control-chain{grid-template-columns:1fr}}@media(max-width:520px){.detail-tabs button{padding-inline:10px}.detail-tabs button svg{display:none}.collection-time{display:none}.overview-facts,.resource-summary,.definition-grid{grid-template-columns:1fr}.overview-facts>div{border-bottom:1px solid var(--ncp-line)!important}.overview-facts>div:last-child{border-bottom:0!important}.definition-grid__wide{grid-column:auto}.device-summary__health{margin-left:0}.sensor-grid{grid-template-columns:1fr}.interface-card dl{grid-template-columns:1fr}.port-grid{grid-template-columns:1fr}.resource-table>div,.evidence-table>div{grid-template-columns:1fr}.usage-cell{grid-template-columns:minmax(90px,1fr) auto!important}.details-skeleton{grid-template-columns:1fr}}
@media(prefers-reduced-motion:reduce){.details-skeleton span{animation:none}}
.system-tabs-toolbar{display:flex;min-width:0;width:100%;align-items:center;justify-content:space-between;gap:14px}.system-tabs-toolbar .detail-tabs{min-width:0;flex:1}.collection-meta{display:flex;flex:0 0 auto;align-items:center;gap:10px}.collection-time{display:inline-flex;align-items:center;color:var(--ncp-text-subtle);font-size:.76rem;white-space:nowrap}.system-refresh{min-height:var(--ncp-control-height)!important;margin:0!important;gap:7px!important;padding-inline:12px!important}.overview-section--processor,.overview-section--resources{grid-column:span 1}.processor-facts{display:grid;grid-template-columns:1.6fr 1fr 1fr;gap:1px;margin-top:14px;overflow:hidden;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-line)}.processor-facts>div{display:grid;min-width:0;gap:5px;padding:12px;background:var(--ncp-surface-quiet)}.processor-facts span{color:var(--ncp-text-subtle);font-size:.72rem}.processor-facts strong{overflow:hidden;font-family:var(--ncp-font-latin);font-size:.86rem;text-overflow:ellipsis;white-space:nowrap}.resource-summary{grid-template-columns:repeat(3,minmax(0,1fr))}.network-summary-grid,.storage-summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.storage-summary-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.network-summary-card,.storage-summary-card{display:grid;min-width:0;grid-template-columns:auto minmax(0,1fr);align-items:start;gap:10px;padding:15px}.network-summary-card__icon,.storage-summary-card__icon{display:grid;width:36px;height:36px;place-items:center;border-radius:10px;background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.storage-summary-card__icon{background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.network-summary-card>div,.storage-summary-card>div{display:grid;min-width:0;gap:3px}.network-summary-card small,.storage-summary-card small{color:var(--ncp-text-subtle);font-size:.7rem}.network-summary-card strong,.storage-summary-card strong{overflow:hidden;color:var(--ncp-text);font-family:var(--ncp-font-latin);font-size:.94rem;text-overflow:ellipsis;white-space:nowrap}.network-summary-card p,.storage-summary-card p{overflow:hidden;margin:0;color:var(--ncp-text-muted);font-size:.72rem;text-overflow:ellipsis;white-space:nowrap}.primary-interface-list{display:grid;padding:6px 18px}.primary-interface-row{display:grid;grid-template-columns:118px minmax(140px,1.1fr) minmax(180px,1.4fr) minmax(150px,1fr);align-items:center;gap:14px;min-height:72px;border-bottom:1px solid var(--ncp-line)}.primary-interface-row:last-child{border-bottom:0}.primary-interface-row__status{display:inline-flex;width:max-content;align-items:center;gap:6px;padding:5px 8px;border:1px solid var(--ncp-line);border-radius:999px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle);font-size:.7rem;font-weight:720}.primary-interface-row__status.online{border-color:var(--ncp-success-border);background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.primary-interface-row__status i{width:6px;height:6px;border-radius:50%;background:currentColor}.primary-interface-row>div:not(.primary-interface-row__name){display:grid;min-width:0;gap:3px}.primary-interface-row__name{display:grid;min-width:0;gap:2px}.primary-interface-row strong{overflow:hidden;font-family:var(--ncp-font-latin);font-size:.83rem;text-overflow:ellipsis;white-space:nowrap}.primary-interface-row small,.primary-interface-row>div:not(.primary-interface-row__name)>span{color:var(--ncp-text-subtle);font-size:.7rem}.primary-interface-row>div:not(.primary-interface-row__name)>strong{font-family:var(--ncp-font-mono);font-size:.76rem;font-weight:600}.network-details,.storage-details{border-top:1px solid var(--ncp-line);background:var(--ncp-surface-quiet)}.network-details summary,.storage-details summary,.evidence-disclosure summary{display:flex;min-height:48px;align-items:center;justify-content:space-between;gap:12px;padding:0 18px;cursor:pointer;color:var(--ncp-primary-strong);font-size:.78rem;font-weight:750;list-style:none}.network-details summary::-webkit-details-marker,.storage-details summary::-webkit-details-marker,.evidence-disclosure summary::-webkit-details-marker{display:none}.network-details summary::before,.storage-details summary::before,.evidence-disclosure summary::before{content:'+';display:grid;width:22px;height:22px;flex:0 0 auto;place-items:center;border:1px solid var(--ncp-primary-border);border-radius:6px;background:var(--ncp-primary-soft);font-size:1rem;line-height:1}.network-details[open] summary::before,.storage-details[open] summary::before,.evidence-disclosure[open] summary::before{content:'−'}.network-details summary span,.storage-details summary span,.evidence-disclosure summary>span:last-child{margin-left:auto;color:var(--ncp-text-subtle);font-size:.7rem;font-weight:600}.network-details__list,.storage-details__list{display:grid;border-top:1px solid var(--ncp-line)}.network-detail-row{display:grid;grid-template-columns:1.2fr 90px 90px 1fr;align-items:center;gap:12px;min-height:54px;padding:8px 18px;border-bottom:1px solid var(--ncp-line)}.network-detail-row:last-child,.storage-details__list>div:last-child{border-bottom:0}.network-detail-row>div{display:grid;min-width:0;gap:2px}.network-detail-row strong,.network-detail-row code{overflow:hidden;font-family:var(--ncp-font-mono);font-size:.75rem;text-overflow:ellipsis;white-space:nowrap}.network-detail-row small,.network-detail-row>span{color:var(--ncp-text-subtle);font-size:.7rem}.network-detail-row>span.is-online{color:var(--ncp-success-strong);font-weight:700}.network-detail-row code{color:var(--ncp-text-muted)}.proxy-summary{padding:18px}.proxy-summary strong{font-size:.9rem}.proxy-summary small{color:var(--ncp-text-subtle);font-size:.72rem}.evidence-disclosure{overflow:hidden}.evidence-disclosure>summary{min-height:68px;padding:0 18px;background:linear-gradient(120deg,var(--ncp-surface),var(--ncp-surface-quiet))}.evidence-disclosure__title{display:flex!important;align-items:center;gap:10px;margin-left:0!important;color:var(--ncp-object-site)}.evidence-disclosure__title>span{display:grid;gap:2px}.evidence-disclosure__title strong{color:var(--ncp-text);font-size:.9rem}.evidence-disclosure__title small{color:var(--ncp-text-subtle);font-size:.72rem;font-weight:500}.evidence-disclosure .evidence-table{border-top:1px solid var(--ncp-line)}.evidence-disclosure .evidence-table__header{background:var(--ncp-surface-quiet)}.port-grid{grid-template-columns:repeat(4,minmax(0,1fr));padding:14px 18px}.port-grid>span{min-width:0;gap:4px}.port-grid b{font-size:.92rem}.port-grid small,.port-grid em{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;font-style:normal;text-overflow:ellipsis;white-space:nowrap}.port-grid em{color:var(--ncp-text-muted);font-family:var(--ncp-font-mono)}.volume-list{display:grid;padding:7px 18px}.volume-row{display:grid;grid-template-columns:minmax(180px,.8fr) minmax(280px,1.2fr);align-items:center;gap:24px;min-height:76px;border-bottom:1px solid var(--ncp-line)}.volume-row:last-child{border-bottom:0}.volume-row__name{display:grid;min-width:0;gap:3px}.volume-row__name strong{font-family:var(--ncp-font-mono);font-size:.85rem}.volume-row__name small,.volume-row__usage small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.7rem;text-overflow:ellipsis;white-space:nowrap}.volume-row__usage{display:grid;grid-template-columns:minmax(90px,1fr) auto;align-items:center;gap:7px}.volume-row__usage .meter{grid-column:1/-1}.volume-row__usage strong{font-family:var(--ncp-font-latin);font-size:.78rem}.volume-row__usage small{text-align:right}.storage-details__list>div{display:grid;grid-template-columns:1.3fr 1fr 90px;align-items:center;gap:12px;min-height:46px;padding:7px 18px;border-bottom:1px solid var(--ncp-line);font-size:.75rem}.storage-details__list span{color:var(--ncp-text-subtle)}.disk-list{display:grid;padding:7px 18px}.disk-row{display:grid;grid-template-columns:30px minmax(0,1fr) auto auto;align-items:center;gap:10px;min-height:62px;border-bottom:1px solid var(--ncp-line)}.disk-row:last-child{border-bottom:0}.disk-row__icon{display:grid;width:28px;height:28px;place-items:center;border-radius:8px;background:var(--ncp-object-storage-soft);color:var(--ncp-object-storage)}.disk-row>div{display:grid;min-width:0;gap:2px}.disk-row strong{overflow:hidden;font-family:var(--ncp-font-mono);font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.disk-row small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.disk-row__size{color:var(--ncp-text-muted);font-family:var(--ncp-font-latin);font-size:.75rem}.disk-health{color:var(--ncp-success-strong);font-size:.72rem;font-weight:700}.disk-health.unknown{color:var(--ncp-text-subtle);font-weight:600}.raid-state--active{color:var(--ncp-success-strong);font-weight:700}
@media(max-width:1050px){.overview-section--processor,.overview-section--resources{grid-column:1/-1}.network-summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.storage-summary-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.primary-interface-row{grid-template-columns:118px minmax(140px,1fr) minmax(150px,1fr)}.primary-interface-row>div:last-child{grid-column:2/-1}.port-grid{grid-template-columns:repeat(3,minmax(0,1fr))}}
@media(max-width:760px){.system-tabs-toolbar{align-items:stretch;flex-direction:column;gap:9px}.system-tabs-toolbar .detail-tabs{width:100%;flex:none}.collection-meta{justify-content:space-between}.overview-section--processor,.overview-section--resources{grid-column:1/-1}.network-summary-grid,.storage-summary-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.primary-interface-list{padding-inline:15px}.primary-interface-row{grid-template-columns:1fr 1fr;gap:9px;padding-block:12px}.primary-interface-row__status{grid-column:1/-1}.primary-interface-row>div:last-child{grid-column:auto}.network-detail-row{grid-template-columns:minmax(0,1fr) auto}.network-detail-row>span:nth-child(3){display:none}.network-detail-row code{grid-column:1/-1}.volume-list,.disk-list{padding-inline:15px}.volume-row{grid-template-columns:1fr;gap:8px;padding-block:12px}.volume-row__usage{grid-template-columns:minmax(100px,1fr) auto}.volume-row__usage .meter{grid-column:1/-1}.disk-row{grid-template-columns:30px minmax(0,1fr) auto}.disk-health{grid-column:2/-1}.port-grid{grid-template-columns:repeat(2,minmax(0,1fr));padding-inline:15px}.evidence-disclosure>summary{padding-inline:15px}.evidence-table>div{padding-inline:15px}}
@media(max-width:520px){.collection-time{display:inline-flex;font-size:.7rem}.collection-meta{width:100%}.collection-meta .system-refresh{min-width:76px}.processor-facts{grid-template-columns:1fr}.resource-summary{grid-template-columns:1fr}.network-summary-grid,.storage-summary-grid{grid-template-columns:1fr}.network-summary-card,.storage-summary-card{padding:13px}.network-details summary,.storage-details summary{padding-inline:15px}.network-detail-row{padding-inline:15px}.port-grid{grid-template-columns:1fr}.storage-details__list>div{grid-template-columns:1fr auto;padding-inline:15px}.storage-details__list>div span:last-child{grid-column:2;grid-row:1}.disk-row__size{font-size:.7rem}}
.ports-disclosure{overflow:hidden}.ports-disclosure>summary{min-height:76px;background:linear-gradient(120deg,var(--ncp-surface),var(--ncp-surface-quiet))}.ports-disclosure__title{display:flex!important;align-items:center;gap:10px;margin-left:0!important}.ports-disclosure__title>svg{box-sizing:content-box;padding:9px;border-radius:11px;background:var(--ncp-object-network-soft);color:var(--ncp-object-network)}.ports-disclosure__title>span{display:grid;gap:2px}.ports-disclosure__title strong{color:var(--ncp-text);font-size:.9rem}.ports-disclosure__title small{color:var(--ncp-text-subtle);font-size:.72rem;font-weight:500}.overview-fact--cgroup small{margin-top:4px;color:var(--ncp-text-subtle);font-size:.66rem;line-height:1.35}
.dns-management{display:grid;gap:10px;padding:0 18px 18px;border-top:1px solid var(--ncp-line)}.dns-management__state{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-top:14px}.dns-management__state small,.dns-management__message{color:var(--ncp-text-subtle);font-size:.7rem}.dns-management__input{display:grid;gap:5px}.dns-management__input span{color:var(--ncp-text-subtle);font-size:.7rem}.dns-management__input input{min-height:36px;padding:0 10px;border:1px solid var(--ncp-line);border-radius:8px;background:var(--ncp-surface);color:var(--ncp-text);font:inherit;font-size:.76rem;outline:none}.dns-management__input input:focus{border-color:var(--ncp-primary);box-shadow:0 0 0 3px var(--ncp-primary-soft)}.dns-management__actions{display:flex;flex-wrap:wrap;gap:7px}.text-button{min-height:32px;padding:0 10px;border:1px solid var(--ncp-line);border-radius:8px;background:var(--ncp-surface);color:var(--ncp-text-muted);font:inherit;font-size:.72rem;font-weight:700;cursor:pointer}.text-button:hover,.text-button:focus-visible{border-color:var(--ncp-primary-border);background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);outline:none}.text-button--primary{border-color:var(--ncp-primary);background:var(--ncp-primary);color:#fff}.text-button--primary:hover,.text-button--primary:focus-visible{background:var(--ncp-primary-strong);color:#fff}.proxy-facts{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}.proxy-facts>div{display:grid;min-width:0;gap:3px;padding:10px;border:1px solid var(--ncp-line);border-radius:9px;background:var(--ncp-surface-quiet)}.proxy-facts small{color:var(--ncp-text-subtle);font-size:.68rem}.proxy-facts strong{overflow:hidden;font-family:var(--ncp-font-latin);font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.proxy-facts code{overflow:hidden;color:var(--ncp-text-muted);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.proxy-message{color:var(--ncp-warning-strong);font-size:.7rem}.proxy-rules-card{overflow:hidden}.proxy-rules-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;padding:16px 18px}.proxy-rules-grid>div{display:grid;gap:4px;padding:13px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.proxy-rules-grid strong{font-size:.78rem}.proxy-rules-grid span{width:max-content;padding:3px 6px;border-radius:5px;background:var(--ncp-neutral-soft);color:var(--ncp-neutral-strong);font-size:.68rem;font-weight:700}.proxy-rules-grid small,.proxy-rules-note{color:var(--ncp-text-subtle);font-size:.7rem;line-height:1.45}.proxy-rules-note{margin:0;padding:0 18px 17px}
@media(max-width:760px){.dns-management__state{align-items:flex-start;flex-direction:column;gap:5px}.proxy-facts,.proxy-rules-grid{grid-template-columns:1fr}}
</style>
