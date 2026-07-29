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
  Thermometer,
  Waypoints,
  Wifi,
} from '@lucide/vue'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { requestSystemDetails, type SystemDetails } from '@/api/system'
import { useSystemStore } from '@/stores/system'

type DetailTab = 'overview' | 'hardware' | 'network' | 'storage' | 'services'

const systemStore = useSystemStore()
const details = ref<SystemDetails | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const activeTab = ref<DetailTab>('overview')

const tabs: Array<{ id: DetailTab; label: string; icon: typeof Server }> = [
  { id: 'overview', label: '设备概览', icon: Server },
  { id: 'hardware', label: '硬件信息', icon: Cpu },
  { id: 'network', label: '网络与代理', icon: Network },
  { id: 'storage', label: '存储与磁盘', icon: HardDrive },
  { id: 'services', label: '服务与能力', icon: Waypoints },
]

const headerStats = computed<WorkspaceStat[]>(() => [
  { label: '系统架构', value: details.value?.device.architecture || systemStore.summary?.host.architecture || '—' },
  { label: '网络接口', value: details.value?.network.interfaces.length ?? '—' },
  { label: '挂载点', value: details.value?.storage.mounts.length ?? '—' },
])

const memoryUsedBytes = computed(() => {
  const memory = details.value?.hardware.memory
  return memory ? Math.max(0, memory.totalBytes - memory.availableBytes) : 0
})

const memoryUsedPercent = computed(() => {
  const memory = details.value?.hardware.memory
  if (!memory?.totalBytes) return 0
  return Math.min(100, Math.max(0, memoryUsedBytes.value / memory.totalBytes * 100))
})

const capabilityItems = computed(() => [
  { name: 'Docker Engine', enabled: Boolean(systemStore.capabilities?.docker), detail: systemStore.inventory?.engine.serverVersion ? `版本 ${systemStore.inventory.engine.serverVersion}` : '等待检测', icon: Boxes, type: 'docker' },
  { name: 'Docker Compose', enabled: Boolean(systemStore.capabilities?.compose), detail: `已发现 ${systemStore.services.length} 个项目`, icon: Database, type: 'database' },
  { name: 'systemd', enabled: Boolean(systemStore.capabilities?.systemd), detail: '宿主机服务管理', icon: Server, type: 'system' },
  { name: 'journald', enabled: Boolean(systemStore.capabilities?.journald), detail: '系统与服务日志', icon: Activity, type: 'system' },
  { name: '数据卷', enabled: Boolean(systemStore.capabilities?.dataVolumes?.length), detail: systemStore.capabilities?.dataVolumes?.join('、') || '未发现', icon: HardDrive, type: 'storage' },
  { name: '网络接口', enabled: Boolean(systemStore.capabilities?.networkInterfaces?.length), detail: `${systemStore.capabilities?.networkInterfaces?.length ?? 0} 个接口`, icon: Network, type: 'network' },
])

async function loadDetails() {
  loading.value = true
  errorMessage.value = ''
  try {
    details.value = await requestSystemDetails()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '系统详情暂不可用'
  } finally {
    loading.value = false
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

function formatTime(value: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function evidenceLabel(value: string) {
  return (({ confirmed: '已确认', inferred: '推断', unknown: '未知' } as Record<string, string>)[value] ?? value) || '未知'
}

function methodLabel(value: string) {
  return (({ http: 'HTTP', socks: 'SOCKS', tun: 'TUN', transparent: '透明代理', unknown: '未知' } as Record<string, string>)[value] ?? value) || '未知'
}

onMounted(loadDetails)
</script>

<template>
  <div class="page workspace-page system-details-page">
    <WorkspaceHeader title="系统信息" description="集中查看设备、硬件、网络、存储与控制链路" :icon="Server" :stats="headerStats">
      <template #actions>
        <el-button :loading="loading" @click="loadDetails"><RefreshCw :size="16" />刷新详情</el-button>
      </template>
      <template #filters>
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
      </template>
      <template #tools>
        <span class="collection-time">采集于 {{ formatTime(details?.collectedAt ?? '') }}</span>
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
          <div><span>cgroup</span><strong>{{ details.device.cgroupVersion || '不可用' }}</strong></div>
          <div><span>采集时间</span><strong>{{ formatTime(details.collectedAt) }}</strong></div>
        </article>

        <article class="panel overview-section">
          <header><Cpu :size="18" /><div><h2>资源摘要</h2><p>CPU、内存、网络与存储的当前静态信息</p></div></header>
          <div class="resource-summary">
            <div><Cpu :size="18" /><span>逻辑核心</span><strong>{{ details.hardware.cpu.logicalCores || '—' }}</strong></div>
            <div><MemoryStick :size="18" /><span>内存容量</span><strong>{{ formatBytes(details.hardware.memory.totalBytes) }}</strong></div>
            <div><Wifi :size="18" /><span>网络接口</span><strong>{{ details.network.interfaces.length }}</strong></div>
            <div><HardDrive :size="18" /><span>挂载点</span><strong>{{ details.storage.mounts.length }}</strong></div>
          </div>
        </article>
      </section>

      <section v-else-if="activeTab === 'hardware'" class="content-grid">
        <article class="panel detail-card">
          <header class="detail-card__header type-network"><Cpu :size="20" /><div><h2>处理器</h2><p>型号、核心、频率与温度</p></div></header>
          <dl class="definition-grid">
            <div class="definition-grid__wide"><dt>型号</dt><dd>{{ details.hardware.cpu.model || '不可用' }}</dd></div>
            <div><dt>物理核心</dt><dd>{{ details.hardware.cpu.physicalCores || '不可用' }}</dd></div>
            <div><dt>逻辑核心</dt><dd>{{ details.hardware.cpu.logicalCores || '不可用' }}</dd></div>
            <div><dt>当前频率</dt><dd>{{ details.hardware.cpu.frequencyMHz ? `${details.hardware.cpu.frequencyMHz.toFixed(0)} MHz` : '不可用' }}</dd></div>
            <div><dt>CPU 温度</dt><dd>{{ formatTemperature(details.hardware.cpu.temperatureCelsius) }}</dd></div>
          </dl>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-storage"><MemoryStick :size="20" /><div><h2>内存</h2><p>容量与可用空间</p></div></header>
          <div class="memory-meter">
            <div><strong>{{ formatBytes(memoryUsedBytes) }}</strong><span>/ {{ formatBytes(details.hardware.memory.totalBytes) }}</span></div>
            <div class="meter"><i :style="{ width: `${memoryUsedPercent}%` }"></i></div>
            <small>可用 {{ formatBytes(details.hardware.memory.availableBytes) }} · 已使用 {{ memoryUsedPercent.toFixed(1) }}%</small>
          </div>
        </article>

        <article class="panel detail-card content-grid__full">
          <header class="detail-card__header type-system"><Thermometer :size="20" /><div><h2>温度传感器</h2><p>来自宿主机可用的硬件传感器</p></div></header>
          <div v-if="details.hardware.sensors.length" class="sensor-grid">
            <div v-for="sensor in details.hardware.sensors" :key="sensor.name"><span>{{ sensor.name }}</span><strong>{{ formatTemperature(sensor.temperatureCelsius) }}</strong></div>
          </div>
          <div v-else class="inline-empty">当前系统未暴露可读取的温度传感器。</div>
        </article>
      </section>

      <section v-else-if="activeTab === 'network'" class="network-layout">
        <article class="panel detail-card network-layout__full">
          <header class="detail-card__header type-network"><Network :size="20" /><div><h2>网络接口</h2><p>地址、链路状态、MTU 与速率</p></div></header>
          <div class="interface-grid">
            <article v-for="item in details.network.interfaces" :key="item.name" class="interface-card">
              <header><div><Wifi :size="17" /><strong>{{ item.name }}</strong></div><span :class="{ online: item.state === 'up' }">{{ item.state === 'up' ? '已连接' : item.state || '未知' }}</span></header>
              <dl>
                <div><dt>地址</dt><dd>{{ item.addresses.map((address) => `${address.address}/${address.prefixLength}`).join('、') || '未分配' }}</dd></div>
                <div><dt>MAC</dt><dd>{{ item.hardwareAddress || '不可用' }}</dd></div>
                <div><dt>链路</dt><dd>{{ item.speedMbps ? `${item.speedMbps} Mbps` : '速率未知' }} · {{ item.duplex || '双工未知' }}</dd></div>
                <div><dt>MTU</dt><dd>{{ item.mtu || '不可用' }}</dd></div>
              </dl>
            </article>
          </div>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-network"><Route :size="20" /><div><h2>路由与 DNS</h2><p>默认网关和解析服务</p></div></header>
          <dl class="definition-grid">
            <div class="definition-grid__wide"><dt>默认网关</dt><dd>{{ details.network.gateway || '未发现' }}</dd></div>
            <div class="definition-grid__wide"><dt>DNS</dt><dd>{{ details.network.dnsServers.join('、') || '未发现' }}</dd></div>
            <div class="definition-grid__wide"><dt>路由数量</dt><dd>{{ details.network.routes.length }}</dd></div>
          </dl>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-site"><Router :size="20" /><div><h2>代理状态</h2><p>基于配置、进程、网卡和环境的证据扫描</p></div></header>
          <div class="proxy-summary">
            <span :class="{ active: details.proxy.mihomo.detected }"><i></i>{{ details.proxy.mihomo.detected ? '发现 Mihomo' : '未发现 Mihomo' }}</span>
            <p>{{ details.proxy.mihomo.detail || '当前未取得更多服务信息' }}</p>
          </div>
        </article>

        <article class="panel detail-card network-layout__full">
          <header class="detail-card__header type-site"><ShieldCheck :size="20" /><div><h2>代理关联证据</h2><p>不会通过抓包或 eBPF 推断流量关系</p></div></header>
          <div v-if="details.proxy.system.length || details.proxy.associations.length" class="evidence-table">
            <div class="evidence-table__header"><span>来源 / 对象</span><span>方式</span><span>证据</span><span>端点与说明</span></div>
            <div v-for="item in details.proxy.system" :key="`${item.source}-${item.address}`">
              <strong>{{ item.source }}</strong><span>{{ methodLabel(item.method) }}</span><span :class="`evidence-${item.evidence}`">{{ evidenceLabel(item.evidence) }}</span><code>{{ item.address || item.detail || '—' }}</code>
            </div>
            <div v-for="item in details.proxy.associations" :key="`${item.subject}-${item.endpoint}`">
              <strong>{{ item.subject }}<small>{{ item.kind }}</small></strong><span>{{ methodLabel(item.method) }}</span><span :class="`evidence-${item.evidence}`">{{ evidenceLabel(item.evidence) }}</span><code>{{ item.endpoint || item.detail || '—' }}</code>
            </div>
          </div>
          <div v-else class="inline-empty">未发现系统代理、TUN 网卡或服务代理关联。</div>
        </article>

        <article class="panel detail-card network-layout__full">
          <header class="detail-card__header type-network"><Gauge :size="20" /><div><h2>监听端口</h2><p>当前采集到的宿主机监听端点</p></div></header>
          <div v-if="details.network.listeningPorts.length" class="port-grid">
            <span v-for="item in details.network.listeningPorts" :key="`${item.protocol}-${item.address}-${item.port}`">
              <b>{{ item.port }}</b><small>{{ item.protocol.toUpperCase() }} · {{ item.address || '*' }}<template v-if="item.pid"> · PID {{ item.pid }}</template></small>
            </span>
          </div>
          <div v-else class="inline-empty">未取得监听端口信息。</div>
        </article>
      </section>

      <section v-else-if="activeTab === 'storage'" class="storage-layout">
        <article class="panel detail-card storage-layout__full">
          <header class="detail-card__header type-storage"><HardDrive :size="20" /><div><h2>挂载点</h2><p>文件系统容量与使用率</p></div></header>
          <div class="resource-table">
            <div class="resource-table__header"><span>挂载点</span><span>设备 / 文件系统</span><span>已用 / 总量</span><span>使用率</span></div>
            <div v-for="mount in details.storage.mounts" :key="mount.path">
              <strong>{{ mount.path }}</strong>
              <span>{{ mount.device || '未知设备' }} · {{ mount.filesystem || '未知格式' }}</span>
              <span>{{ formatBytes(mount.usedBytes) }} / {{ formatBytes(mount.totalBytes) }}</span>
              <span class="usage-cell"><i><b :style="{ width: `${Math.min(100, mount.usedPercent)}%` }"></b></i>{{ mount.usedPercent.toFixed(1) }}%</span>
            </div>
          </div>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-storage"><Database :size="20" /><div><h2>物理磁盘</h2><p>型号、容量、介质与健康状态</p></div></header>
          <div v-if="details.storage.disks.length" class="compact-list">
            <div v-for="disk in details.storage.disks" :key="disk.name">
              <span><strong>{{ disk.name }}</strong><small>{{ disk.model || '型号未知' }}</small></span>
              <span>{{ formatBytes(disk.sizeBytes) }} · {{ disk.rotational ? '机械硬盘' : '固态设备' }}</span>
              <span>{{ disk.health || '健康状态未知' }} · {{ formatTemperature(disk.temperatureCelsius) }}</span>
            </div>
          </div>
          <div v-else class="inline-empty">系统未暴露物理磁盘健康信息。</div>
        </article>

        <article class="panel detail-card">
          <header class="detail-card__header type-system"><Boxes :size="20" /><div><h2>RAID</h2><p>阵列级别、状态与成员设备</p></div></header>
          <div v-if="details.storage.raid.length" class="compact-list">
            <div v-for="raid in details.storage.raid" :key="raid.name">
              <span><strong>{{ raid.name }}</strong><small>{{ raid.level || '级别未知' }}</small></span>
              <span>{{ raid.state || '状态未知' }}</span>
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
</style>
