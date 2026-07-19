<script setup lang="ts">
import { computed } from 'vue'
import {
  Activity,
  ArrowUpRight,
  Boxes,
  Cable,
  Cpu,
  HardDrive,
  MemoryStick,
  Network,
  ShieldCheck,
} from '@lucide/vue'

import MetricCard from '@/components/MetricCard.vue'
import SignalChart from '@/components/SignalChart.vue'
import StatusPill from '@/components/StatusPill.vue'
import { previewServices, previewSnapshot } from '@/data/overview-snapshot'
import {
  deriveOverviewState,
  formatOneDecimal,
  memoryUsagePercent,
  storageUsagePercent,
} from '@/domain/overview'

const overviewState = computed(() => deriveOverviewState(previewSnapshot))
const lastUpdated = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
}).format(new Date(previewSnapshot.updatedAt))

const metrics = computed(() => [
  {
    label: 'CPU 负载',
    value: formatOneDecimal(previewSnapshot.cpu.usage),
    unit: '%',
    note: '过去 4 小时峰值 42.7%',
    trend: '+4.2%',
    accent: 'primary' as const,
    icon: Cpu,
  },
  {
    label: '内存使用',
    value: formatOneDecimal(memoryUsagePercent(previewSnapshot)),
    unit: '%',
    note: `${formatOneDecimal(previewSnapshot.memory.usedGiB)} / ${formatOneDecimal(previewSnapshot.memory.totalGiB)} GiB`,
    trend: '稳定',
    accent: 'info' as const,
    icon: MemoryStick,
  },
  {
    label: '存储容量',
    value: formatOneDecimal(storageUsagePercent(previewSnapshot)),
    unit: '%',
    note: `${formatOneDecimal(previewSnapshot.storage.usedTiB)} / ${formatOneDecimal(previewSnapshot.storage.totalTiB)} TiB`,
    trend: '健康',
    accent: 'warning' as const,
    icon: HardDrive,
  },
  {
    label: '下行速率',
    value: formatOneDecimal(previewSnapshot.network.downMbps),
    unit: 'Mb/s',
    note: `上行 ${formatOneDecimal(previewSnapshot.network.upMbps)} Mb/s`,
    trend: '实时',
    accent: 'info' as const,
    icon: Network,
  },
])
</script>

<template>
  <div class="page overview-page">
    <header class="page-header reveal">
      <div>
        <p class="eyebrow"><Activity :size="14" aria-hidden="true" /> 控制室总览</p>
        <h1>把 NAS 的日常运行，收进一眼可读的节奏。</h1>
        <p class="page-header__description">
          这是基于 Phase 0 核验边界建立的控制台预览。接入真实 API 后，结构与状态语言保持不变。
        </p>
      </div>
      <div class="header-state">
        <StatusPill :label="overviewState.label" :tone="overviewState.status" />
        <span>最后同步 {{ lastUpdated }}</span>
      </div>
    </header>

    <section class="overview-hero panel reveal" style="--reveal-index: 1" aria-labelledby="overview-health-title">
      <div class="overview-hero__copy">
        <span class="overview-hero__eyebrow">SYSTEM RHYTHM</span>
        <h2 id="overview-health-title">{{ overviewState.label }}</h2>
        <p>{{ overviewState.detail }}</p>
        <div class="overview-hero__actions">
          <RouterLink class="quiet-link" to="/infrastructure">
            查看能力地图
            <ArrowUpRight :size="15" aria-hidden="true" />
          </RouterLink>
          <span>当前仅展示模拟采样</span>
        </div>
      </div>

      <div class="availability-orbit" aria-label="系统可用性 99.98%">
        <span class="availability-orbit__ring availability-orbit__ring--outer" aria-hidden="true"></span>
        <span class="availability-orbit__ring availability-orbit__ring--middle" aria-hidden="true"></span>
        <div class="availability-orbit__core">
          <strong>99.98<span>%</span></strong>
          <small>可用性</small>
          <span>24h 观测窗口</span>
        </div>
        <span class="availability-orbit__spark availability-orbit__spark--one" aria-hidden="true"></span>
        <span class="availability-orbit__spark availability-orbit__spark--two" aria-hidden="true"></span>
      </div>
    </section>

    <section class="metric-grid" aria-label="核心运行指标">
      <MetricCard
        v-for="(metric, index) in metrics"
        :key="metric.label"
        class="reveal"
        :style="{ '--reveal-index': index + 2 }"
        v-bind="metric"
      />
    </section>

    <section class="overview-grid">
      <article class="signal-panel panel reveal" style="--reveal-index: 3">
        <header class="panel-header">
          <div>
            <h2>处理器节奏</h2>
            <p>基于 4 小时窗口的预览采样</p>
          </div>
          <span class="data-note">平均 32.1%</span>
        </header>
        <div class="signal-panel__stat">
          <strong>{{ formatOneDecimal(previewSnapshot.cpu.usage) }}<span>%</span></strong>
          <span>当前使用率</span>
        </div>
        <SignalChart label="CPU 使用率" :points="previewSnapshot.cpu.trend" />
      </article>

      <article class="attention-panel panel reveal" style="--reveal-index: 4">
        <header class="panel-header">
          <div>
            <h2>今天的关注点</h2>
            <p>将需要主动判断的事项保留在这里</p>
          </div>
          <ShieldCheck :size="19" :stroke-width="1.7" aria-hidden="true" />
        </header>
        <div class="attention-panel__body">
          <span class="attention-marker" aria-hidden="true"></span>
          <div>
            <strong>没有待处理的系统风险</strong>
            <p>后续会接入 Job、审计事件与阈值告警，保持同一套信息密度与状态语言。</p>
          </div>
        </div>
        <div class="attention-panel__footer">
          <span>下一次自动刷新</span>
          <strong>00:02</strong>
        </div>
      </article>
    </section>

    <section class="service-panel panel reveal" style="--reveal-index: 5" aria-labelledby="service-snapshot-title">
      <header class="panel-header service-panel__header">
        <div>
          <h2 id="service-snapshot-title">服务快照</h2>
          <p>明确区分已核验能力和待完成工作，避免把开发状态伪装成线上状态。</p>
        </div>
        <RouterLink class="quiet-link quiet-link--compact" to="/services">
          服务中心
          <ArrowUpRight :size="15" aria-hidden="true" />
        </RouterLink>
      </header>

      <ul class="service-list">
        <li v-for="service in previewServices" :key="service.name" class="service-list__item">
          <span class="service-list__icon" aria-hidden="true">
            <Boxes v-if="service.name === 'Docker Engine'" :size="18" :stroke-width="1.75" />
            <Cable v-else :size="18" :stroke-width="1.75" />
          </span>
          <div class="service-list__copy">
            <strong>{{ service.name }}</strong>
            <span>{{ service.detail }}</span>
          </div>
          <span class="service-list__value">{{ service.value }}</span>
          <StatusPill
            :label="service.state === 'pending' ? '待验证' : '已核验'"
            :tone="service.state"
          />
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.header-state {
  display: grid;
  justify-items: end;
  gap: 9px;
  min-width: max-content;
}

.header-state > span:last-child {
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.67rem;
}

.overview-hero {
  display: grid;
  grid-template-columns: minmax(0, 1.08fr) minmax(300px, 0.92fr);
  min-height: 308px;
  overflow: hidden;
  padding: clamp(24px, 4vw, 42px);
  background:
    radial-gradient(circle at 75% 55%, rgba(140, 226, 190, 0.12), transparent 14rem),
    linear-gradient(135deg, rgba(28, 48, 46, 0.95), rgba(14, 23, 24, 0.98));
}

.overview-hero__copy {
  position: relative;
  z-index: 1;
  align-self: center;
}

.overview-hero__eyebrow {
  color: var(--ncp-primary);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.69rem;
  font-weight: 700;
  letter-spacing: 0.12em;
}

.overview-hero h2 {
  max-width: 520px;
  margin: 12px 0 10px;
  font-size: clamp(2rem, 3.7vw, 3.45rem);
  font-weight: 650;
  letter-spacing: -0.065em;
  line-height: 1.03;
}

.overview-hero p {
  max-width: 450px;
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: 0.91rem;
}

.overview-hero__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 15px 20px;
  margin-top: 28px;
}

.overview-hero__actions > span {
  color: var(--ncp-text-subtle);
  font-size: 0.72rem;
}

.quiet-link {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  padding: 0 14px;
  border: 1px solid rgba(140, 226, 190, 0.25);
  border-radius: var(--ncp-radius-sm);
  background: rgba(140, 226, 190, 0.08);
  color: var(--ncp-primary);
  font-size: 0.78rem;
  font-weight: 750;
  transition:
    background-color var(--ncp-duration-fast) var(--ncp-ease-out),
    border-color var(--ncp-duration-fast) var(--ncp-ease-out),
    transform var(--ncp-duration-fast) var(--ncp-ease-out);
}

.quiet-link:hover {
  border-color: rgba(140, 226, 190, 0.48);
  background: rgba(140, 226, 190, 0.16);
  transform: translateY(-2px);
}

.quiet-link--compact {
  min-height: 34px;
  padding: 0 10px;
}

.availability-orbit {
  position: relative;
  display: grid;
  width: min(100%, 278px);
  aspect-ratio: 1;
  place-self: center;
  place-items: center;
}

.availability-orbit__ring {
  position: absolute;
  border-radius: 50%;
}

.availability-orbit__ring--outer {
  inset: 3px;
  border: 1px solid rgba(140, 226, 190, 0.18);
}

.availability-orbit__ring--middle {
  inset: 30px;
  border: 1px dashed rgba(140, 226, 190, 0.3);
}

.availability-orbit__core {
  position: relative;
  display: grid;
  width: 139px;
  height: 139px;
  place-content: center;
  justify-items: center;
  border: 1px solid rgba(140, 226, 190, 0.37);
  border-radius: 50%;
  background: radial-gradient(circle at 34% 25%, rgba(140, 226, 190, 0.2), rgba(15, 34, 31, 0.78));
  box-shadow: 0 0 0 14px rgba(140, 226, 190, 0.03), 0 16px 40px rgba(0, 0, 0, 0.24);
}

.availability-orbit__core strong {
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 1.88rem;
  font-weight: 650;
  letter-spacing: -0.11em;
}

.availability-orbit__core strong span {
  font-size: 0.87rem;
  letter-spacing: -0.05em;
}

.availability-orbit__core small {
  margin-top: 2px;
  color: var(--ncp-primary);
  font-size: 0.7rem;
  font-weight: 800;
}

.availability-orbit__core > span {
  margin-top: 7px;
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.56rem;
}

.availability-orbit__spark {
  position: absolute;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ncp-primary);
  box-shadow: 0 0 0 5px rgba(140, 226, 190, 0.1);
}

.availability-orbit__spark--one {
  top: 16%;
  right: 12%;
}

.availability-orbit__spark--two {
  bottom: 19%;
  left: 8%;
  width: 5px;
  height: 5px;
  background: var(--ncp-info);
  box-shadow: 0 0 0 4px rgba(145, 199, 243, 0.09);
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;
  margin-top: 14px;
}

.overview-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) minmax(300px, 0.7fr);
  gap: 14px;
  margin-top: 14px;
}

.signal-panel,
.attention-panel {
  min-height: 286px;
  padding: 24px;
}

.signal-panel__stat {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin: 27px 0 15px;
}

.signal-panel__stat strong {
  color: var(--ncp-text);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 2.5rem;
  font-weight: 650;
  letter-spacing: -0.1em;
  line-height: 1;
}

.signal-panel__stat strong span {
  color: var(--ncp-primary);
  font-size: 1rem;
  letter-spacing: -0.04em;
}

.signal-panel__stat > span {
  color: var(--ncp-text-subtle);
  font-size: 0.73rem;
}

.attention-panel {
  display: flex;
  flex-direction: column;
  background:
    linear-gradient(145deg, rgba(27, 43, 40, 0.88), rgba(15, 24, 25, 0.94)),
    var(--ncp-surface);
}

.attention-panel :deep(.lucide) {
  color: var(--ncp-primary);
}

.attention-panel__body {
  display: flex;
  gap: 13px;
  margin-top: auto;
  padding: 24px 0;
}

.attention-marker {
  flex: 0 0 auto;
  width: 10px;
  height: 10px;
  margin-top: 6px;
  border-radius: 50%;
  background: var(--ncp-primary);
  box-shadow: 0 0 0 6px var(--ncp-primary-soft);
}

.attention-panel__body strong {
  display: block;
  font-size: 0.88rem;
}

.attention-panel__body p {
  margin: 6px 0 0;
  color: var(--ncp-text-muted);
  font-size: 0.78rem;
  line-height: 1.65;
}

.attention-panel__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-top: 16px;
  border-top: 1px solid var(--ncp-line);
  color: var(--ncp-text-subtle);
  font-size: 0.7rem;
}

.attention-panel__footer strong {
  color: var(--ncp-text-muted);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.76rem;
}

.service-panel {
  margin-top: 14px;
  padding: 24px;
}

.service-panel__header {
  align-items: center;
}

.service-list {
  display: grid;
  padding: 0;
  margin: 23px 0 0;
  list-style: none;
}

.service-list__item {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) minmax(130px, auto) auto;
  align-items: center;
  gap: 15px;
  min-height: 76px;
  padding: 12px 0;
  border-top: 1px solid var(--ncp-line);
}

.service-list__icon {
  display: grid;
  width: 38px;
  height: 38px;
  place-items: center;
  border-radius: 10px;
  background: var(--ncp-surface-quiet);
  color: var(--ncp-primary);
}

.service-list__copy {
  display: grid;
  gap: 3px;
}

.service-list__copy strong {
  font-size: 0.82rem;
}

.service-list__copy span {
  color: var(--ncp-text-subtle);
  font-size: 0.72rem;
}

.service-list__value {
  color: var(--ncp-text-muted);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.68rem;
  text-align: right;
}

@media (max-width: 1120px) {
  .metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 860px) {
  .overview-hero,
  .overview-grid {
    grid-template-columns: 1fr;
  }

  .availability-orbit {
    margin-top: -10px;
  }
}

@media (max-width: 640px) {
  .header-state {
    display: flex;
    justify-content: flex-start;
    margin-top: 18px;
  }

  .header-state > span:last-child {
    display: none;
  }

  .overview-hero,
  .signal-panel,
  .attention-panel,
  .service-panel {
    padding: 20px;
  }

  .metric-grid {
    grid-template-columns: 1fr;
  }

  .service-panel__header {
    align-items: flex-start;
  }

  .service-list__item {
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 11px;
  }

  .service-list__value {
    display: none;
  }

  .service-list__item :deep(.status-pill) {
    grid-column: 2 / -1;
    justify-self: start;
    margin-top: -6px;
  }
}
</style>
