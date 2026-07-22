<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Activity, ArrowUpRight, Boxes, Cpu, HardDrive, MemoryStick, Network, Server } from '@lucide/vue'

import MetricCard from '@/components/MetricCard.vue'
import StatusPill from '@/components/StatusPill.vue'
import { deriveOverviewState, formatBytes, formatOneDecimal, formatUptime, projectStateTone, totalStorage, usagePercent } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const overviewState = computed(() => deriveOverviewState(systemStore.summary, systemStore.inventory))
const storage = computed(() => totalStorage(systemStore.summary))
const serviceSnapshot = computed(() => systemStore.services?.services ?? systemStore.inventory?.projects ?? [])
const networkSnapshot = computed(() => systemStore.summary?.network.slice(0, 4) ?? [])
const storageSnapshot = computed(() => systemStore.summary?.storage ?? [])

const metrics = computed(() => {
  const summary = systemStore.summary
  const inventory = systemStore.inventory
  return [
    {
      label: 'CPU 使用率',
      value: summary ? formatOneDecimal(summary.cpu.usagePercent) : '—',
      unit: summary ? '%' : '',
      note: summary ? `${summary.cpu.logicalCores} 个逻辑核心 · Load 1 ${formatOneDecimal(summary.cpu.load1)}` : '等待 Root Agent 系统快照',
      trend: summary ? `${formatOneDecimal(summary.cpu.load5)} load` : undefined,
      accent: 'primary' as const,
      icon: Cpu,
    },
    {
      label: '内存使用',
      value: summary ? formatOneDecimal(usagePercent(summary.memory.usedBytes, summary.memory.totalBytes)) : '—',
      unit: summary ? '%' : '',
      note: summary ? `${formatBytes(summary.memory.usedBytes)} / ${formatBytes(summary.memory.totalBytes)}` : '等待 Root Agent 系统快照',
      trend: summary ? `${formatBytes(summary.memory.availableBytes)} 可用` : undefined,
      accent: 'info' as const,
      icon: MemoryStick,
    },
    {
      label: '数据卷容量',
      value: storage.value.capacity ? formatOneDecimal(usagePercent(storage.value.used, storage.value.capacity)) : '—',
      unit: storage.value.capacity ? '%' : '',
      note: storage.value.capacity ? `${formatBytes(storage.value.used)} / ${formatBytes(storage.value.capacity)}` : '未发现可展示的数据卷',
      trend: storageSnapshot.value.length ? `${storageSnapshot.value.length} 个挂载点` : undefined,
      accent: 'warning' as const,
      icon: HardDrive,
    },
    {
      label: 'Docker 容器',
      value: inventory ? String(inventory.engine.containersRunning) : '—',
      unit: inventory ? `/${inventory.engine.containers}` : '',
      note: inventory ? `Engine ${inventory.engine.serverVersion || '已连接'}` : '等待 Docker Engine 清单',
      trend: inventory ? `${inventory.projects.length} 个服务组` : undefined,
      accent: 'info' as const,
      icon: Boxes,
    },
  ]
})

function formatTimestamp(value: string | null) {
  if (!value) return '尚未同步'
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return '刚刚同步'
  return new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date)
}
</script>

<template>
  <div class="page overview-page">
    <header class="page-header reveal">
      <div>
        <p class="eyebrow"><Activity :size="14" aria-hidden="true" /> Root control room</p>
        <h1>让 NAS 的真实运行状态，一眼可读。</h1>
        <p class="page-header__description">
          主机、Docker 和服务信息均通过 Root Agent 采集；没有接收到的数据会明确留空，而不是伪装成在线状态。
        </p>
      </div>
      <div class="header-state">
        <StatusPill :label="overviewState.label" :tone="overviewState.status" />
        <span>最后同步 {{ formatTimestamp(systemStore.lastUpdated) }}</span>
      </div>
    </header>

    <section class="overview-hero panel reveal" style="--reveal-index: 1" aria-labelledby="overview-health-title">
      <div class="overview-hero__copy">
        <span class="overview-hero__eyebrow">LIVE HOST SNAPSHOT</span>
        <h2 id="overview-health-title">{{ systemStore.summary?.host.hostname ?? '等待 Root Agent' }}</h2>
        <p>{{ overviewState.detail }}</p>
        <div class="overview-hero__actions">
          <RouterLink class="quiet-link" to="/services">
            查看服务中心 <ArrowUpRight :size="15" aria-hidden="true" />
          </RouterLink>
          <span v-if="systemStore.errorCode" class="hero-error-code">{{ systemStore.errorCode }}</span>
        </div>
      </div>

      <div class="runtime-plate" aria-label="主机实时信息">
        <div class="runtime-plate__header"><Server :size="17" aria-hidden="true" /><span>HOST RUNTIME</span></div>
        <dl>
          <div><dt>系统</dt><dd>{{ systemStore.summary?.host.operatingSystem || '—' }}</dd></div>
          <div><dt>内核</dt><dd>{{ systemStore.summary?.host.kernelVersion || '—' }}</dd></div>
          <div><dt>运行时间</dt><dd>{{ systemStore.summary ? formatUptime(systemStore.summary.host.uptimeSeconds) : '—' }}</dd></div>
          <div><dt>进程</dt><dd>{{ systemStore.summary?.host.processCount ?? '—' }}</dd></div>
        </dl>
        <div class="runtime-plate__foot"><span></span>{{ systemStore.summary?.host.architecture || 'root agent pending' }}</div>
      </div>
    </section>

    <section class="metric-grid" aria-label="核心运行指标">
      <MetricCard v-for="(metric, index) in metrics" :key="metric.label" class="reveal" :style="{ '--reveal-index': index + 2 }" v-bind="metric" />
    </section>

    <section class="overview-grid">
      <article class="storage-panel panel reveal" style="--reveal-index: 3">
        <header class="panel-header">
          <div><h2>存储布局</h2><p>仅展示根目录与 NAS 数据卷的实时容量。</p></div>
          <HardDrive :size="19" :stroke-width="1.7" aria-hidden="true" />
        </header>
        <div v-if="storageSnapshot.length" class="storage-list">
          <div v-for="disk in storageSnapshot" :key="disk.mountpoint" class="storage-row">
            <div class="storage-row__meta"><strong>{{ disk.mountpoint }}</strong><span>{{ formatBytes(disk.usedBytes) }} / {{ formatBytes(disk.totalBytes) }}</span></div>
            <div class="storage-row__track"><span :style="{ width: `${usagePercent(disk.usedBytes, disk.totalBytes)}%` }"></span></div>
          </div>
        </div>
        <p v-else class="empty-note">等待 Root Agent 返回可展示的文件系统信息。</p>
      </article>

      <article class="signal-panel panel reveal" style="--reveal-index: 4">
        <header class="panel-header">
          <div><h2>系统信号</h2><p>负载与接口累计流量来自当前实时快照。</p></div>
          <Network :size="19" :stroke-width="1.7" aria-hidden="true" />
        </header>
        <div class="load-cluster">
          <div><span>LOAD 1</span><strong>{{ systemStore.summary ? formatOneDecimal(systemStore.summary.cpu.load1) : '—' }}</strong></div>
          <div><span>LOAD 5</span><strong>{{ systemStore.summary ? formatOneDecimal(systemStore.summary.cpu.load5) : '—' }}</strong></div>
          <div><span>LOAD 15</span><strong>{{ systemStore.summary ? formatOneDecimal(systemStore.summary.cpu.load15) : '—' }}</strong></div>
        </div>
        <ul v-if="networkSnapshot.length" class="network-list">
          <li v-for="network in networkSnapshot" :key="network.name"><span>{{ network.name }}</span><small>↓ {{ formatBytes(network.receiveBytes) }} · ↑ {{ formatBytes(network.transmitBytes) }}</small></li>
        </ul>
        <p v-else class="empty-note">等待网络接口计数器。</p>
      </article>
    </section>

    <section class="service-panel panel reveal" style="--reveal-index: 5" aria-labelledby="service-snapshot-title">
      <header class="panel-header service-panel__header">
        <div><h2 id="service-snapshot-title">服务快照</h2><p>Docker Compose 项目和独立容器由 Docker Engine 自动归类。</p></div>
        <RouterLink class="quiet-link quiet-link--compact" to="/services">全部服务 <ArrowUpRight :size="15" aria-hidden="true" /></RouterLink>
      </header>
      <ul v-if="serviceSnapshot.length" class="service-list">
        <li v-for="service in serviceSnapshot.slice(0, 4)" :key="service.id" class="service-list__item">
          <span class="service-list__icon" aria-hidden="true"><Boxes :size="18" :stroke-width="1.75" /></span>
          <div class="service-list__copy"><strong>{{ service.name }}</strong><span>{{ service.kind === 'compose' ? 'Docker Compose 项目' : '独立容器组' }}</span></div>
          <span class="service-list__value">{{ service.runningCount }} / {{ service.containerCount }} running</span>
          <StatusPill :label="service.state === 'running' ? '运行中' : service.state === 'degraded' ? '需关注' : '已停止'" :tone="projectStateTone(service.state)" />
        </li>
      </ul>
      <p v-else class="empty-note empty-note--service">等待 Docker Engine 服务发现结果。</p>
    </section>
  </div>
</template>

<style scoped>
.header-state { display: grid; justify-items: end; gap: 9px; min-width: max-content; }
.header-state > span:last-child { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.65rem; }
.overview-hero { display: grid; grid-template-columns: minmax(0, 1.14fr) minmax(290px, 0.86fr); gap: 24px; min-height: 292px; padding: clamp(25px, 4vw, 42px); overflow: hidden; background: radial-gradient(circle at 83% 56%, rgba(44, 111, 223, 0.11), transparent 17rem), linear-gradient(135deg, #fff, #f4f8ff); }
.overview-hero__copy { align-self: center; }
.overview-hero__eyebrow { color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.66rem; font-weight: 750; letter-spacing: 0.12em; }
.overview-hero h2 { max-width: 560px; margin: 12px 0 10px; color: var(--ncp-text); font-size: clamp(2rem, 3.75vw, 3.55rem); font-weight: 720; letter-spacing: -0.07em; line-height: 1.01; }
.overview-hero p { max-width: 510px; margin: 0; color: var(--ncp-text-muted); font-size: 0.9rem; line-height: 1.7; }
.overview-hero__actions { display: flex; align-items: center; flex-wrap: wrap; gap: 14px 18px; margin-top: 26px; }
.quiet-link { display: inline-flex; align-items: center; gap: 8px; min-height: 40px; padding: 0 14px; border: 1px solid rgba(44, 111, 223, 0.22); border-radius: 11px; background: rgba(44, 111, 223, 0.075); color: var(--ncp-primary-strong); font-size: 0.77rem; font-weight: 750; transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out), border-color var(--ncp-duration-fast) var(--ncp-ease-out), transform var(--ncp-duration-fast) var(--ncp-ease-out); }
.quiet-link:hover { border-color: rgba(44, 111, 223, 0.42); background: rgba(44, 111, 223, 0.13); transform: translateY(-2px); }
.quiet-link--compact { min-height: 35px; padding: 0 10px; }
.hero-error-code { color: var(--ncp-warning-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.63rem; }
.runtime-plate { align-self: stretch; padding: 20px; border: 1px solid rgba(44, 111, 223, 0.14); border-radius: 16px; background: rgba(255, 255, 255, 0.77); box-shadow: 0 16px 36px rgba(33, 70, 140, 0.07); }
.runtime-plate__header { display: flex; align-items: center; gap: 8px; color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.63rem; font-weight: 750; letter-spacing: 0.09em; }
.runtime-plate dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 21px 15px; margin: 30px 0 24px; }
.runtime-plate dl div { display: grid; gap: 5px; }
.runtime-plate dt { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.58rem; font-weight: 700; letter-spacing: 0.06em; }
.runtime-plate dd { margin: 0; overflow: hidden; color: var(--ncp-text); font-size: 0.8rem; font-weight: 720; text-overflow: ellipsis; white-space: nowrap; }
.runtime-plate__foot { display: flex; align-items: center; gap: 8px; color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.6rem; }
.runtime-plate__foot span { width: 6px; height: 6px; border-radius: 50%; background: #1a8b6d; box-shadow: 0 0 0 4px rgba(26, 139, 109, 0.1); }
.metric-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; margin-top: 14px; }
.overview-grid { display: grid; grid-template-columns: minmax(0, 1.2fr) minmax(310px, 0.8fr); gap: 14px; margin-top: 14px; }
.storage-panel, .signal-panel { min-height: 290px; padding: 24px; }
.storage-panel :deep(.lucide), .signal-panel :deep(.lucide) { color: var(--ncp-primary-strong); }
.storage-list { display: grid; gap: 18px; margin-top: 28px; }
.storage-row { display: grid; gap: 9px; }
.storage-row__meta { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.storage-row__meta strong { color: var(--ncp-text); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.7rem; }
.storage-row__meta span { color: var(--ncp-text-subtle); font-size: 0.68rem; }
.storage-row__track { height: 7px; overflow: hidden; border-radius: var(--ncp-radius-pill); background: var(--ncp-surface-quiet); }
.storage-row__track span { display: block; height: 100%; min-width: 3px; border-radius: inherit; background: linear-gradient(90deg, #377ce5, #7ca8f2); }
.load-cluster { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 9px; margin-top: 25px; }
.load-cluster div { display: grid; gap: 7px; padding: 13px; border: 1px solid var(--ncp-line); border-radius: 12px; background: var(--ncp-surface-quiet); }
.load-cluster span { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.57rem; font-weight: 750; letter-spacing: 0.06em; }
.load-cluster strong { color: var(--ncp-text); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 1.15rem; font-weight: 650; letter-spacing: -0.07em; }
.network-list { display: grid; padding: 0; margin: 19px 0 0; list-style: none; }
.network-list li { display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 10px 0; border-top: 1px solid var(--ncp-line); }
.network-list span { color: var(--ncp-text-muted); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.66rem; font-weight: 700; }
.network-list small { color: var(--ncp-text-subtle); font-size: 0.64rem; }
.empty-note { margin: 38px 0 0; color: var(--ncp-text-subtle); font-size: 0.77rem; }
.service-panel { margin-top: 14px; padding: 24px; }
.service-panel__header { align-items: center; }
.service-list { display: grid; padding: 0; margin: 22px 0 0; list-style: none; }
.service-list__item { display: grid; grid-template-columns: auto minmax(0, 1fr) minmax(130px, auto) auto; align-items: center; gap: 15px; min-height: 74px; padding: 12px 0; border-top: 1px solid var(--ncp-line); }
.service-list__icon { display: grid; width: 38px; height: 38px; place-items: center; border-radius: 11px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.service-list__copy { display: grid; gap: 3px; }
.service-list__copy strong { color: var(--ncp-text); font-size: 0.82rem; }
.service-list__copy span { color: var(--ncp-text-subtle); font-size: 0.7rem; }
.service-list__value { color: var(--ncp-text-muted); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.65rem; text-align: right; }
.empty-note--service { margin: 26px 0 4px; }

@media (max-width: 1130px) { .metric-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 870px) { .overview-hero, .overview-grid { grid-template-columns: 1fr; } .runtime-plate { min-height: 220px; } }
@media (max-width: 640px) { .header-state { display: flex; justify-content: flex-start; margin-top: 18px; } .header-state > span:last-child { display: none; } .overview-hero, .storage-panel, .signal-panel, .service-panel { padding: 20px; } .metric-grid { grid-template-columns: 1fr; } .service-panel__header { align-items: flex-start; } .service-list__item { grid-template-columns: auto minmax(0, 1fr) auto; gap: 11px; } .service-list__value { display: none; } .service-list__item :deep(.status-pill) { grid-column: 2 / -1; justify-self: start; margin-top: -7px; } }
</style>
