<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowDown, ArrowUp, ArrowUpRight, Boxes, Cpu, Gauge, Globe2, HardDrive, MemoryStick, Server } from '@lucide/vue'

import RealtimeTrendChart, { type TrendSeries } from '@/components/RealtimeTrendChart.vue'
import StatusPill from '@/components/StatusPill.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { deriveOverviewState, formatBytes, formatOneDecimal, formatUptime, totalStorage, usagePercent } from '@/domain/overview'
import { useSitesStore } from '@/stores/sites'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const sitesStore = useSitesStore()
const hostName = window.location.hostname
const overviewState = computed(() => deriveOverviewState(systemStore.summary, systemStore.inventory))
const storage = computed(() => totalStorage(systemStore.summary))
const projects = computed(() => systemStore.services)
const runningProjects = computed(() => projects.value.filter((project) => project.state === 'running').length)
const timestamps = computed(() => systemStore.resourceHistory.map((sample) => sample.timestamp))
const failedIcons = ref(new Set<string>())
const favoriteSites = computed(() => sitesStore.visibleSites
  .filter((site) => site.favorite)
  .sort((left, right) => left.sortOrder - right.sortOrder || left.name.localeCompare(right.name, 'zh-CN'))
  .slice(0, 6))
const linkTarget = computed(() => systemStore.preferences.linkOpenMode === 'new-tab' ? '_blank' : '_self')
const headerStats = computed<WorkspaceStat[]>(() => [
  { label: '运行项目', value: `${runningProjects.value}/${projects.value.length}`, tone: 'success' },
  { label: '运行时间', value: systemStore.summary ? formatUptime(systemStore.summary.host.uptimeSeconds) : '—' },
  { label: '最近更新', value: formatTimestamp(systemStore.lastUpdated) },
])

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

function siteURL(site: (typeof favoriteSites.value)[number]) {
  if (site.launchUrl) return site.launchUrl
  return `${systemStore.preferences.siteDefaultProtocol}://${hostName}:${site.primaryPort}`
}

function markIconFailed(siteId: string) {
  failedIcons.value = new Set(failedIcons.value).add(siteId)
}

onMounted(() => {
  void sitesStore.refresh()
})
</script>

<template>
  <div class="page workspace-page overview-page">
    <WorkspaceHeader title="系统总览" description="资源、服务和 Docker 状态随实时快照自动更新" :icon="Server" :stats="headerStats">
      <template #actions>
        <StatusPill :label="overviewState.label" :tone="overviewState.status" />
      </template>
    </WorkspaceHeader>

    <section class="favorite-launcher panel" aria-labelledby="favorite-launcher-title">
      <header class="favorite-launcher__header">
        <div class="favorite-launcher__title">
          <span><Globe2 :size="18" /></span>
          <div>
            <h2 id="favorite-launcher-title">收藏站点</h2>
            <p>快速进入最常使用的 NAS 应用</p>
          </div>
        </div>
        <RouterLink to="/sites">管理站点<ArrowUpRight :size="16" /></RouterLink>
      </header>
      <div v-if="favoriteSites.length" class="favorite-grid">
        <a v-for="site in favoriteSites" :key="site.id" class="favorite-site" :href="siteURL(site)" :target="linkTarget" rel="noreferrer" @click="sitesStore.visit(site.projectId)">
          <span class="favorite-site__logo">
            <img v-if="site.iconUrl && !failedIcons.has(site.id)" :src="site.iconUrl" alt="" @error="markIconFailed(site.id)" />
            <b v-else>{{ site.name.slice(0, 1).toUpperCase() }}</b>
          </span>
          <span class="favorite-site__copy">
            <strong>{{ site.name }}</strong>
            <small>{{ site.description }}</small>
          </span>
          <span :class="['favorite-site__state', { 'favorite-site__state--offline': site.state !== 'running' }]">{{ site.state === 'running' ? '运行中' : '未运行' }}</span>
          <ArrowUpRight class="favorite-site__arrow" :size="17" />
        </a>
      </div>
      <div v-else class="favorite-empty">
        <Globe2 :size="26" />
        <div><strong>还没有收藏站点</strong><span>在站点中心点击星标，入口会固定显示在这里。</span></div>
        <RouterLink to="/sites">前往站点中心</RouterLink>
      </div>
    </section>

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
.favorite-launcher.favorite-launcher{gap:14px;padding:17px 20px 20px}.favorite-launcher__header.favorite-launcher__header{min-height:42px;align-items:center}.favorite-launcher__title{display:flex;align-items:center;gap:10px}.favorite-launcher__title>span{display:grid;width:36px;height:36px;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.favorite-launcher__header .favorite-launcher__title h2{margin:0;font-size:1.04rem}.favorite-launcher__header .favorite-launcher__title p{margin:2px 0 0;font-size:.78rem}.detail-grid.detail-grid{grid-template-columns:1fr}
.favorite-launcher{display:grid;gap:17px;padding:20px;overflow:hidden;background:radial-gradient(circle at 88% -30%,rgba(52,116,212,.1),transparent 32%),#fff}.favorite-launcher__header{display:flex;align-items:flex-end;justify-content:space-between;gap:18px}.favorite-launcher__header h2{margin:4px 0 0;font-size:1.25rem;letter-spacing:-.025em}.favorite-launcher__header p{margin:3px 0 0;color:var(--ncp-text-muted);font-size:.86rem}.favorite-launcher__eyebrow{display:flex;align-items:center;gap:6px;color:var(--ncp-primary-strong);font-size:.8rem;font-weight:730}.favorite-launcher__header>a{display:flex;min-height:38px;align-items:center;gap:5px;padding:0 11px;border-radius:9px;color:var(--ncp-primary-strong);font-size:.84rem;font-weight:700}.favorite-launcher__header>a:hover{background:var(--ncp-primary-soft)}.favorite-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px}.favorite-site{position:relative;display:grid;min-height:92px;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:12px;padding:14px;border:1px solid var(--ncp-line);border-radius:13px;background:rgba(255,255,255,.88);transition:border-color var(--ncp-duration-fast),box-shadow var(--ncp-duration-base),transform var(--ncp-duration-fast)}.favorite-site:hover{border-color:rgba(52,116,212,.28);box-shadow:var(--ncp-shadow-hover);transform:translateY(-2px)}.favorite-site__logo{display:grid;width:48px;height:48px;place-items:center;overflow:hidden;border-radius:13px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-family:var(--ncp-font-latin);font-size:1.05rem}.favorite-site__logo img{width:100%;height:100%;object-fit:cover}.favorite-site__copy{display:grid;min-width:0;gap:4px}.favorite-site__copy strong{font-size:.94rem}.favorite-site__copy small{overflow:hidden;color:var(--ncp-text-muted);font-size:.8rem;text-overflow:ellipsis;white-space:nowrap}.favorite-site__state{align-self:start;padding:3px 7px;border-radius:7px;background:var(--ncp-success-soft);color:var(--ncp-success);font-size:.72rem;font-weight:700}.favorite-site__state--offline{background:var(--ncp-warning-soft);color:var(--ncp-warning-strong)}.favorite-site__arrow{position:absolute;right:12px;bottom:11px;color:var(--ncp-text-subtle);opacity:0;transform:translate(-3px,3px);transition:opacity var(--ncp-duration-fast),transform var(--ncp-duration-fast)}.favorite-site:hover .favorite-site__arrow{opacity:1;transform:none}.favorite-empty{display:flex;min-height:96px;align-items:center;gap:12px;padding:15px;border:1px dashed var(--ncp-line-strong);border-radius:13px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.favorite-empty>div{display:grid;gap:2px}.favorite-empty strong{color:var(--ncp-text)}.favorite-empty span{font-size:.82rem}.favorite-empty>a{margin-left:auto;padding:9px 12px;border-radius:9px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.82rem;font-weight:700}
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
.detail-grid { display: grid; grid-template-columns: 1.08fr .92fr; gap: 12px; }
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
  .favorite-grid { grid-template-columns:repeat(2,minmax(0,1fr)); }
  .metric-grid { grid-template-columns: repeat(2,1fr); }
  .trend-grid { grid-template-columns: repeat(2,1fr); }
  .trend-panel:last-child { grid-column: 1/-1; }
  .detail-grid { grid-template-columns: repeat(2,1fr); }
}
@media(max-width: 720px) {
  .favorite-launcher{padding:16px}.favorite-launcher__header{align-items:flex-start}.favorite-grid{grid-template-columns:1fr}.favorite-site{min-height:84px}.favorite-empty{align-items:flex-start;flex-direction:column}.favorite-empty>a{margin-left:0}
  .metric-grid, .trend-grid, .detail-grid { grid-template-columns: 1fr; }
  .trend-panel:last-child { grid-column: auto; }
  .metric-card { min-height: 118px; }
}
</style>
