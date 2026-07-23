<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowUpRight, Boxes, Cpu, HardDrive, MemoryStick, Network, Server } from '@lucide/vue'

import StatusPill from '@/components/StatusPill.vue'
import { deriveOverviewState, formatBytes, formatOneDecimal, formatUptime, totalStorage, usagePercent } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const hostName = window.location.hostname
const overviewState = computed(() => deriveOverviewState(systemStore.summary, systemStore.inventory))
const storage = computed(() => totalStorage(systemStore.summary))
const projects = computed(() => systemStore.services)
const runningProjects = computed(() => projects.value.filter((project) => project.state === 'running').length)
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
  return [
    { label: 'CPU', value: summary ? `${formatOneDecimal(summary.cpu.usagePercent)}%` : '—', detail: summary ? `${summary.cpu.logicalCores} 核 · 负载 ${formatOneDecimal(summary.cpu.load1)}` : '等待数据', icon: Cpu, tone: 'blue' },
    { label: '内存', value: summary ? `${formatOneDecimal(usagePercent(summary.memory.usedBytes, summary.memory.totalBytes))}%` : '—', detail: summary ? `${formatBytes(summary.memory.usedBytes)} / ${formatBytes(summary.memory.totalBytes)}` : '等待数据', icon: MemoryStick, tone: 'violet' },
    { label: '存储', value: storage.value.capacity ? `${formatOneDecimal(usagePercent(storage.value.used, storage.value.capacity))}%` : '—', detail: storage.value.capacity ? `${formatBytes(storage.value.used)} / ${formatBytes(storage.value.capacity)}` : '等待数据', icon: HardDrive, tone: 'amber' },
    { label: 'Docker', value: inventory ? `${inventory.engine.containersRunning}/${inventory.engine.containers}` : '—', detail: inventory ? `${runningProjects.value}/${projects.value.length} 个项目运行` : '等待数据', icon: Boxes, tone: 'green' },
  ]
})

function formatTimestamp(value: string | null) {
  if (!value) return '等待首次同步'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '刚刚' : new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date)
}
</script>

<template>
  <div class="page overview-page">
    <header class="page-toolbar">
      <div><h1>总览</h1><p>{{ systemStore.deviceName }} 的实时运行状态</p></div>
      <div class="toolbar-state"><StatusPill :label="overviewState.label" :tone="overviewState.status" /><span>更新于 {{ formatTimestamp(systemStore.lastUpdated) }}</span></div>
    </header>

    <section class="metric-grid" aria-label="核心资源指标">
      <article v-for="metric in metrics" :key="metric.label" class="metric-card panel">
        <span :class="['metric-card__icon', `metric-card__icon--${metric.tone}`]"><component :is="metric.icon" :size="20" aria-hidden="true" /></span>
        <div><p>{{ metric.label }}</p><strong>{{ metric.value }}</strong><small>{{ metric.detail }}</small></div>
      </article>
    </section>

    <section class="overview-layout">
      <article class="panel host-panel">
        <header class="section-header"><div><h2>主机信息</h2><p>Root Agent 实时快照</p></div><Server :size="20" aria-hidden="true" /></header>
        <dl class="host-facts">
          <div><dt>主机名</dt><dd>{{ systemStore.summary?.host.hostname ?? '—' }}</dd></div>
          <div><dt>操作系统</dt><dd>{{ systemStore.summary?.host.operatingSystem ?? '—' }}</dd></div>
          <div><dt>内核</dt><dd>{{ systemStore.summary?.host.kernelVersion ?? '—' }}</dd></div>
          <div><dt>架构</dt><dd>{{ systemStore.summary?.host.architecture ?? '—' }}</dd></div>
          <div><dt>运行时间</dt><dd>{{ systemStore.summary ? formatUptime(systemStore.summary.host.uptimeSeconds) : '—' }}</dd></div>
          <div><dt>进程数量</dt><dd>{{ systemStore.summary?.host.processCount ?? '—' }}</dd></div>
        </dl>
        <div class="load-row">
          <span>系统负载</span>
          <strong>{{ systemStore.summary ? formatOneDecimal(systemStore.summary.cpu.load1) : '—' }}</strong>
          <small>1 分钟</small>
          <strong>{{ systemStore.summary ? formatOneDecimal(systemStore.summary.cpu.load5) : '—' }}</strong>
          <small>5 分钟</small>
          <strong>{{ systemStore.summary ? formatOneDecimal(systemStore.summary.cpu.load15) : '—' }}</strong>
          <small>15 分钟</small>
        </div>
      </article>

      <article class="panel service-panel">
        <header class="section-header">
          <div><h2>服务入口</h2><p>{{ publicServices.length }} 个项目提供公开端口</p></div>
          <RouterLink to="/services">全部入口 <ArrowUpRight :size="15" /></RouterLink>
        </header>
        <ul v-if="publicServices.length" class="service-links">
          <li v-for="project in publicServices.slice(0, 6)" :key="project.id">
            <span class="service-links__icon"><Boxes :size="17" /></span>
            <div><strong>{{ project.name }}</strong><small>{{ project.runningCount }}/{{ project.containerCount }} 个容器运行</small></div>
            <div class="service-links__ports">
              <a v-for="port in project.ports.slice(0, 3)" :key="port" :href="`http://${hostName}:${port}`" target="_blank" rel="noreferrer">{{ port }}</a>
            </div>
          </li>
        </ul>
        <p v-else class="empty-state">尚未发现可访问端口。</p>
      </article>
    </section>

    <section class="overview-layout overview-layout--bottom">
      <article class="panel compact-panel">
        <header class="section-header"><div><h2>存储空间</h2><p>当前挂载点容量</p></div><HardDrive :size="19" /></header>
        <div v-for="disk in systemStore.summary?.storage ?? []" :key="disk.mountpoint" class="progress-row">
          <div><strong>{{ disk.mountpoint }}</strong><span>{{ formatBytes(disk.usedBytes) }} / {{ formatBytes(disk.totalBytes) }}</span></div>
          <div class="progress-track"><span :style="{ width: `${usagePercent(disk.usedBytes, disk.totalBytes)}%` }"></span></div>
        </div>
        <p v-if="!systemStore.summary?.storage.length" class="empty-state">等待存储数据。</p>
      </article>
      <article class="panel compact-panel">
        <header class="section-header"><div><h2>网络累计流量</h2><p>按网络接口统计</p></div><Network :size="19" /></header>
        <ul class="network-list">
          <li v-for="item in systemStore.summary?.network.slice(0, 6) ?? []" :key="item.name"><strong>{{ item.name }}</strong><span>下载 {{ formatBytes(item.receiveBytes) }}</span><span>上传 {{ formatBytes(item.transmitBytes) }}</span></li>
        </ul>
        <p v-if="!systemStore.summary?.network.length" class="empty-state">等待网络数据。</p>
      </article>
    </section>
  </div>
</template>

<style scoped>
.toolbar-state { display: flex; align-items: center; gap: 10px; color: var(--ncp-text-subtle); font-size: .68rem; }
.metric-grid { display: grid; grid-template-columns: repeat(4, minmax(0,1fr)); gap: 12px; }
.metric-card { display: flex; min-height: 116px; align-items: center; gap: 14px; padding: 18px; }
.metric-card__icon { display: grid; width: 42px; height: 42px; flex: 0 0 auto; place-items: center; border-radius: 11px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.metric-card__icon--violet { background: #f0edff; color: #6855c7; }.metric-card__icon--amber { background: var(--ncp-warning-soft); color: var(--ncp-warning-strong); }.metric-card__icon--green { background: #e9f7f2; color: #148265; }
.metric-card p,.metric-card small { margin: 0; color: var(--ncp-text-subtle); }.metric-card p { font-size: .7rem; font-weight: 700; }.metric-card strong { display: block; margin: 2px 0; font-family: 'JetBrains Mono Variable', monospace; font-size: 1.45rem; letter-spacing: -.06em; }.metric-card small { font-size: .65rem; }
.overview-layout { display: grid; grid-template-columns: minmax(0,1.1fr) minmax(0,.9fr); gap: 12px; margin-top: 12px; }.overview-layout--bottom { grid-template-columns: 1fr 1fr; }
.host-panel,.service-panel,.compact-panel { padding: 20px; }.section-header { display:flex; align-items:flex-start; justify-content:space-between; gap:12px; }.section-header h2 { margin:0; font-size:.92rem; }.section-header p { margin:3px 0 0; color:var(--ncp-text-subtle); font-size:.68rem; }.section-header>a { display:flex; align-items:center; gap:4px; color:var(--ncp-primary-strong); font-size:.68rem; font-weight:700; }
.host-facts { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:16px 20px; margin:22px 0; }.host-facts div { min-width:0; }.host-facts dt { color:var(--ncp-text-subtle); font-size:.64rem; }.host-facts dd { overflow:hidden; margin:4px 0 0; color:var(--ncp-text); font-size:.76rem; font-weight:700; text-overflow:ellipsis; white-space:nowrap; }
.load-row { display:flex; align-items:baseline; gap:8px; padding-top:14px; border-top:1px solid var(--ncp-line); color:var(--ncp-text-muted); font-size:.67rem; }.load-row>span { margin-right:auto; font-weight:700; }.load-row strong { font-family:'JetBrains Mono Variable',monospace; font-size:.8rem; }.load-row small { color:var(--ncp-text-subtle); }
.service-links { display:grid; gap:2px; padding:0; margin:14px 0 0; list-style:none; }.service-links li { display:grid; grid-template-columns:auto minmax(0,1fr) auto; align-items:center; gap:10px; min-height:54px; padding:7px 4px; border-bottom:1px solid var(--ncp-line); }.service-links__icon { display:grid; width:34px; height:34px; place-items:center; border-radius:9px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }.service-links strong { display:block; font-size:.75rem; }.service-links small { color:var(--ncp-text-subtle); font-size:.62rem; }.service-links__ports { display:flex; gap:5px; }.service-links__ports a { padding:4px 7px; border-radius:6px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); font-family:'JetBrains Mono Variable',monospace; font-size:.62rem; font-weight:700; }
.progress-row { margin-top:16px; }.progress-row>div:first-child { display:flex; justify-content:space-between; gap:12px; font-size:.68rem; }.progress-row span { color:var(--ncp-text-subtle); }.progress-track { height:6px; margin-top:7px; overflow:hidden; border-radius:99px; background:var(--ncp-surface-quiet); }.progress-track span { display:block; height:100%; border-radius:inherit; background:var(--ncp-primary); }
.network-list { padding:0; margin:12px 0 0; list-style:none; }.network-list li { display:grid; grid-template-columns:minmax(90px,1fr) 1fr 1fr; gap:10px; padding:10px 0; border-bottom:1px solid var(--ncp-line); font-size:.67rem; }.network-list span { color:var(--ncp-text-subtle); }.empty-state { margin:20px 0 0; color:var(--ncp-text-subtle); font-size:.72rem; }
@media(max-width:1100px){.metric-grid{grid-template-columns:repeat(2,1fr)}.overview-layout,.overview-layout--bottom{grid-template-columns:1fr}}@media(max-width:600px){.metric-grid{grid-template-columns:1fr}.host-facts{grid-template-columns:repeat(2,1fr)}.toolbar-state{align-items:flex-end;flex-direction:column}.load-row{flex-wrap:wrap}.load-row>span{width:100%}}
</style>
