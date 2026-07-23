<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowDown, ArrowUp, ArrowUpRight, Boxes, Cpu, Gauge, HardDrive, MemoryStick, Server } from '@lucide/vue'
import { ElTooltip } from 'element-plus'

import RealtimeTrendChart, { type TrendSeries } from '@/components/RealtimeTrendChart.vue'
import StatusPill from '@/components/StatusPill.vue'
import { deriveOverviewState, formatBytes, formatOneDecimal, formatUptime, totalStorage, usagePercent } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const hostName = window.location.hostname
const overviewState = computed(() => deriveOverviewState(systemStore.summary, systemStore.inventory))
const storage = computed(() => totalStorage(systemStore.summary))
const projects = computed(() => systemStore.services)
const runningProjects = computed(() => projects.value.filter((project) => project.state === 'running').length)
const timestamps = computed(() => systemStore.resourceHistory.map((sample) => sample.timestamp))
const publicServices = computed(() => {
  const inventory = systemStore.inventory
  if (!inventory) return []
  return projects.value.map((project) => {
    const ports = inventory.containers
      .filter((container) => container.projectId === project.id)
      .flatMap((container) => container.ports)
      .filter((port) => port.publicPort > 0)
    return { ...project, ports: [...new Set(ports.map((port) => port.publicPort))] }
  }).filter((project) => project.ports.length)
})

const metrics = computed(() => {
  const summary = systemStore.summary
  const inventory = systemStore.inventory
  const cpuValue = summary?.cpu.usagePercent ?? 0
  const memoryValue = summary ? usagePercent(summary.memory.usedBytes, summary.memory.totalBytes) : 0
  const storageValue = storage.value.capacity ? usagePercent(storage.value.used, storage.value.capacity) : 0
  const dockerValue = inventory?.engine.containers ? (inventory.engine.containersRunning / inventory.engine.containers) * 100 : 0
  return [
    { label: 'CPU 使用率', value: summary ? `${formatOneDecimal(cpuValue)}%` : '—', detail: summary ? `${summary.cpu.logicalCores} 核 · 负载 ${formatOneDecimal(summary.cpu.load1)}` : '等待数据', icon: Cpu, progress: cpuValue, tone: 'blue' },
    { label: '内存使用', value: summary ? `${formatOneDecimal(memoryValue)}%` : '—', detail: summary ? `${formatBytes(summary.memory.usedBytes)} / ${formatBytes(summary.memory.totalBytes)}` : '等待数据', icon: MemoryStick, progress: memoryValue, tone: 'violet' },
    { label: '存储使用', value: storage.value.capacity ? `${formatOneDecimal(storageValue)}%` : '—', detail: storage.value.capacity ? `${formatBytes(storage.value.used)} / ${formatBytes(storage.value.capacity)}` : '等待数据', icon: HardDrive, progress: storageValue, tone: 'amber' },
    { label: 'Docker 容器', value: inventory ? `${inventory.engine.containersRunning}/${inventory.engine.containers}` : '—', detail: inventory ? `${runningProjects.value}/${projects.value.length} 个项目运行` : '等待数据', icon: Boxes, progress: dockerValue, tone: 'green' },
  ]
})

const resourceSeries = computed<TrendSeries[]>(() => [
  { name: 'CPU', color: '#2468d8', values: systemStore.resourceHistory.map((sample) => sample.cpuPercent) },
  { name: '内存', color: '#7657d6', values: systemStore.resourceHistory.map((sample) => sample.memoryPercent) },
])
const loadSeries = computed<TrendSeries[]>(() => [
  { name: '1 分钟负载', color: '#d48113', values: systemStore.resourceHistory.map((sample) => sample.load1) },
])
const networkSeries = computed<TrendSeries[]>(() => [
  { name: '下载', color: '#16866a', values: systemStore.resourceHistory.map((sample) => sample.networkReceiveBps / 1024) },
  { name: '上传', color: '#2468d8', values: systemStore.resourceHistory.map((sample) => sample.networkTransmitBps / 1024) },
])

function formatTimestamp(value: string | null) {
  if (!value) return '等待首次同步'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '刚刚' : new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date)
}
</script>

<template>
  <div class="page workspace-page overview-page">
    <header class="overview-heading">
      <div>
        <span class="overview-heading__eyebrow"><Server :size="15" />{{ systemStore.deviceName }}</span>
        <h1>系统总览</h1>
        <p>资源、服务和 Docker 状态每次实时快照自动更新</p>
      </div>
      <div class="overview-heading__state">
        <StatusPill :label="overviewState.label" :tone="overviewState.status" />
        <span>更新于 {{ formatTimestamp(systemStore.lastUpdated) }}</span>
      </div>
    </header>

    <section class="metric-grid" aria-label="核心资源指标">
      <article v-for="metric in metrics" :key="metric.label" class="metric-card panel">
        <div class="metric-card__head">
          <span :class="['metric-card__icon', `metric-card__icon--${metric.tone}`]"><component :is="metric.icon" :size="19" /></span>
          <span>{{ metric.label }}</span>
        </div>
        <strong>{{ metric.value }}</strong>
        <p>{{ metric.detail }}</p>
        <div class="metric-card__track" aria-hidden="true"><span :class="`metric-card__bar--${metric.tone}`" :style="{ width: `${Math.min(metric.progress, 100)}%` }"></span></div>
      </article>
    </section>

    <section class="trend-grid" aria-label="实时资源趋势">
      <article class="trend-panel panel">
        <header><div><h2>CPU 与内存趋势</h2><p>本次会话 · 最近 {{ systemStore.resourceHistory.length }} 个样本</p></div><Gauge :size="19" /></header>
        <RealtimeTrendChart :timestamps="timestamps" :series="resourceSeries" unit="%" />
      </article>
      <article class="trend-panel panel">
        <header><div><h2>系统负载</h2><p>1 分钟平均负载</p></div><Cpu :size="19" /></header>
        <RealtimeTrendChart :timestamps="timestamps" :series="loadSeries" unit="" :decimals="2" />
      </article>
      <article class="trend-panel panel">
        <header><div><h2>网络吞吐</h2><p>全部接口实时速率</p></div><span class="network-legend"><ArrowDown :size="15" />下载 <ArrowUp :size="15" />上传</span></header>
        <RealtimeTrendChart :timestamps="timestamps" :series="networkSeries" unit="KB/s" />
      </article>
    </section>

    <section class="detail-grid">
      <article class="panel info-panel">
        <header><div><h2>主机信息</h2><p>Root Agent 实时快照</p></div><Server :size="19" /></header>
        <dl class="host-facts">
          <div><dt>主机名</dt><dd>{{ systemStore.summary?.host.hostname ?? '—' }}</dd></div>
          <div><dt>操作系统</dt><dd>{{ systemStore.summary?.host.operatingSystem ?? '—' }}</dd></div>
          <div><dt>内核</dt><dd>{{ systemStore.summary?.host.kernelVersion ?? '—' }}</dd></div>
          <div><dt>架构</dt><dd>{{ systemStore.summary?.host.architecture ?? '—' }}</dd></div>
          <div><dt>运行时间</dt><dd>{{ systemStore.summary ? formatUptime(systemStore.summary.host.uptimeSeconds) : '—' }}</dd></div>
          <div><dt>进程</dt><dd>{{ systemStore.summary?.host.processCount ?? '—' }}</dd></div>
        </dl>
      </article>

      <article class="panel info-panel">
        <header>
          <div><h2>常用服务</h2><p>{{ publicServices.length }} 个项目具有公开入口</p></div>
          <RouterLink to="/services">全部服务<ArrowUpRight :size="15" /></RouterLink>
        </header>
        <ul v-if="publicServices.length" class="service-list">
          <li v-for="project in publicServices.slice(0, 5)" :key="project.id">
            <span><Boxes :size="16" /></span>
            <div><strong>{{ project.name }}</strong><small>{{ project.runningCount }}/{{ project.containerCount }} 运行</small></div>
            <ElTooltip v-for="port in project.ports.slice(0, 2)" :key="port" :content="`打开端口 ${port}`">
              <a :href="`http://${hostName}:${port}`" target="_blank" rel="noreferrer">{{ port }}</a>
            </ElTooltip>
          </li>
        </ul>
        <p v-else class="empty-state">尚未发现可访问端口。</p>
      </article>

      <article class="panel info-panel">
        <header><div><h2>存储空间</h2><p>数据卷与挂载点</p></div><HardDrive :size="19" /></header>
        <div class="storage-list">
          <div v-for="disk in systemStore.summary?.storage ?? []" :key="disk.mountpoint" class="storage-row">
            <div><strong>{{ disk.mountpoint }}</strong><span>{{ formatBytes(disk.usedBytes) }} / {{ formatBytes(disk.totalBytes) }}</span></div>
            <div class="storage-track"><span :style="{ width: `${usagePercent(disk.usedBytes, disk.totalBytes)}%` }"></span></div>
          </div>
          <p v-if="!systemStore.summary?.storage.length" class="empty-state">等待存储数据。</p>
        </div>
      </article>
    </section>
  </div>
</template>

<style scoped>
.overview-heading { display: flex; min-height: 68px; align-items: flex-end; justify-content: space-between; gap: 20px; }
.overview-heading__eyebrow { display: flex; align-items: center; gap: 6px; margin-bottom: 4px; color: var(--ncp-primary-strong); font-size: .66rem; font-weight: 730; }
.overview-heading h1 { margin: 0; font-size: clamp(1.35rem,2.2vw,1.7rem); font-weight: 790; letter-spacing: -.045em; }
.overview-heading p { margin: 4px 0 0; color: var(--ncp-text-subtle); font-size: .72rem; }
.overview-heading__state { display: flex; align-items: center; gap: 9px; color: var(--ncp-text-subtle); font-size: .65rem; }
.metric-grid { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: 12px; }
.metric-card { display: grid; min-width: 0; gap: 4px; padding: 16px; }
.metric-card__head { display: flex; align-items: center; gap: 8px; color: var(--ncp-text-muted); font-size: .68rem; font-weight: 720; }
.metric-card__icon { display: grid; width: 32px; height: 32px; place-items: center; border-radius: 9px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.metric-card__icon--violet { background: #f0edff; color: #6855c7; }.metric-card__icon--amber { background: var(--ncp-warning-soft); color: var(--ncp-warning-strong); }.metric-card__icon--green { background: var(--ncp-success-soft); color: var(--ncp-success); }
.metric-card>strong { margin-top: 5px; font-family: 'JetBrains Mono Variable', monospace; font-size: 1.4rem; letter-spacing: -.05em; }
.metric-card>p { overflow: hidden; margin: 0; color: var(--ncp-text-subtle); font-size: .63rem; text-overflow: ellipsis; white-space: nowrap; }
.metric-card__track { height: 4px; margin-top: 7px; overflow: hidden; border-radius: 9px; background: var(--ncp-surface-quiet); }
.metric-card__track span { display: block; height: 100%; border-radius: inherit; background: var(--ncp-primary); transition: width var(--ncp-duration-base) var(--ncp-ease-out); }
.metric-card__track .metric-card__bar--violet { background: #7657d6; }.metric-card__track .metric-card__bar--amber { background: var(--ncp-warning); }.metric-card__track .metric-card__bar--green { background: var(--ncp-success); }
.trend-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 12px; }
.trend-panel { min-width: 0; padding: 16px; }
.trend-panel>header, .info-panel>header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.trend-panel h2, .info-panel h2 { margin: 0; font-size: .83rem; }
.trend-panel p, .info-panel header p { margin: 3px 0 0; color: var(--ncp-text-subtle); font-size: .63rem; }
.trend-panel>header>svg, .info-panel>header>svg { color: var(--ncp-primary-strong); }
.network-legend { display: flex; align-items: center; gap: 4px; color: var(--ncp-text-subtle); font-size: .59rem; }
.detail-grid { display: grid; grid-template-columns: 1.05fr 1fr 1fr; gap: 12px; }
.info-panel { min-width: 0; padding: 17px; }
.info-panel header>a { display: flex; min-height: 32px; align-items: center; gap: 3px; color: var(--ncp-primary-strong); font-size: .64rem; font-weight: 730; }
.host-facts { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 14px 18px; margin: 18px 0 0; }
.host-facts div { min-width: 0; }
.host-facts dt { color: var(--ncp-text-subtle); font-size: .61rem; }
.host-facts dd { overflow: hidden; margin: 3px 0 0; font-size: .7rem; font-weight: 680; text-overflow: ellipsis; white-space: nowrap; }
.service-list { display: grid; gap: 0; padding: 0; margin: 12px 0 0; list-style: none; }
.service-list li { display: grid; min-height: 48px; grid-template-columns: auto minmax(0,1fr) auto auto; align-items: center; gap: 8px; border-top: 1px solid var(--ncp-line); }
.service-list li>span { display: grid; width: 30px; height: 30px; place-items: center; border-radius: 8px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.service-list li>div { display: grid; min-width: 0; }
.service-list strong { overflow: hidden; font-size: .67rem; text-overflow: ellipsis; white-space: nowrap; }
.service-list small { color: var(--ncp-text-subtle); font-size: .56rem; }
.service-list a { padding: 4px 6px; border-radius: 6px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', monospace; font-size: .58rem; font-weight: 700; }
.storage-list { display: grid; gap: 13px; margin-top: 16px; }
.storage-row { display: grid; gap: 7px; }
.storage-row>div:first-child { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.storage-row strong { font-size: .68rem; }.storage-row span { color: var(--ncp-text-subtle); font-size: .58rem; }
.storage-track { height: 7px; overflow: hidden; border-radius: 8px; background: var(--ncp-surface-quiet); }
.storage-track span { display: block; height: 100%; border-radius: inherit; background: var(--ncp-primary); }
.empty-state { margin: 18px 0 0; color: var(--ncp-text-subtle); font-size: .66rem; }
@media(max-width: 1200px) {
  .metric-grid { grid-template-columns: repeat(2,1fr); }
  .trend-grid { grid-template-columns: repeat(2,1fr); }
  .trend-panel:last-child { grid-column: 1/-1; }
  .detail-grid { grid-template-columns: repeat(2,1fr); }
}
@media(max-width: 720px) {
  .overview-heading { min-height: 0; align-items: flex-start; flex-direction: column; gap: 10px; }
  .metric-grid, .trend-grid, .detail-grid { grid-template-columns: 1fr; }
  .trend-panel:last-child { grid-column: auto; }
  .metric-card { min-height: 118px; }
  .overview-heading__state { width: 100%; justify-content: space-between; }
}
</style>
