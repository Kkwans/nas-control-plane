<script setup lang="ts">
import type { Component } from 'vue'

import type { SystemDetails } from '@/api/system'
import InfrastructureSignalSummary, { type InfrastructureSignal } from '@/components/InfrastructureSignalSummary.vue'

defineProps<{
  details: SystemDetails
  infrastructureSignals: readonly InfrastructureSignal[]
  primaryActiveInterfaceCount: number
  volumeCount: number
  formatBytes: (value: number) => string
  formatDuration: (seconds: number) => string
  icons: {
    cpu: Component
    gauge: Component
    hardDrive: Component
    memoryStick: Component
    server: Component
    wifi: Component
  }
}>()
</script>

<template>
  <section class="overview-layout">
    <article class="device-summary panel">
      <div class="device-summary__icon"><component :is="icons.server" :size="31" /></div>
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
      <div class="overview-fact--cgroup">
        <span>资源控制（cgroup）</span>
        <strong>{{ details.device.cgroupVersion || '不可用' }}</strong>
        <small>{{ details.device.cgroupVersion === 'v2' ? 'v2 使用统一层级和控制器接口，负责统计、限制与委派进程资源。' : 'Linux 控制组用于统计和限制进程资源；当前版本决定可用的控制器接口。' }}</small>
      </div>
    </article>

    <article class="panel overview-section overview-section--processor">
      <header>
        <component :is="icons.cpu" :size="18" />
        <div><h2>处理器信息</h2><p>设备识别到的静态 CPU 信息</p></div>
      </header>
      <div class="processor-facts">
        <div class="processor-facts__model"><span>型号</span><strong>{{ details.hardware.cpu.model || '不可用' }}</strong></div>
        <div><span>核心</span><strong>{{ details.hardware.cpu.physicalCores || '—' }} 物理 / {{ details.hardware.cpu.logicalCores || '—' }} 逻辑</strong></div>
        <div><span>频率</span><strong>{{ details.hardware.cpu.frequencyMHz ? `${details.hardware.cpu.frequencyMHz.toFixed(0)} MHz` : '不可用' }}</strong></div>
      </div>
    </article>

    <article class="panel overview-section overview-section--resources">
      <header>
        <component :is="icons.gauge" :size="18" />
        <div><h2>资源概览</h2><p>容量类数据放在这里，实时变化请前往系统监控</p></div>
      </header>
      <div class="resource-summary">
        <div><component :is="icons.memoryStick" :size="18" /><span>内存容量</span><strong>{{ formatBytes(details.hardware.memory.totalBytes) }}</strong></div>
        <div><component :is="icons.wifi" :size="18" /><span>主网络连接</span><strong>{{ primaryActiveInterfaceCount }}</strong></div>
        <div><component :is="icons.hardDrive" :size="18" /><span>存储卷</span><strong>{{ volumeCount }}</strong></div>
      </div>
    </article>
  </section>
</template>

<style scoped>
.overview-layout {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.device-summary {
  display: flex;
  grid-column: 1 / -1;
  align-items: center;
  gap: 16px;
  padding: 22px;
  background: radial-gradient(circle at 6% 0, rgb(52 116 212 / 8%), transparent 30%), linear-gradient(115deg, #fff 64%, var(--ncp-surface-quiet));
}

.device-summary__icon {
  display: grid;
  width: 60px;
  height: 60px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--ncp-primary) 18%, transparent);
  border-radius: 17px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.device-summary__identity {
  min-width: 0;
}

.device-summary__identity > span {
  color: var(--ncp-text-subtle);
  font-size: .75rem;
  font-weight: 700;
}

.device-summary__identity h2 {
  margin: 2px 0;
  font-size: 1.4rem;
  letter-spacing: -.03em;
}

.device-summary__identity p {
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: .86rem;
}

.device-summary__health {
  display: grid;
  margin-left: auto;
  justify-items: end;
  gap: 5px;
}

.device-summary__health > span {
  display: inline-flex;
  width: max-content;
  align-items: center;
  gap: 7px;
  padding: 6px 10px;
  border: 1px solid var(--ncp-success-border);
  border-radius: 999px;
  background: var(--ncp-success-soft);
  color: var(--ncp-success-strong);
  font-size: .78rem;
  font-weight: 700;
}

.device-summary__health i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}

.device-summary__health small {
  color: var(--ncp-text-subtle);
  font-size: .75rem;
}

.overview-facts {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  grid-auto-rows: minmax(82px, 1fr);
  align-items: stretch;
  padding: 6px 18px;
}

.overview-facts > div {
  display: grid;
  min-width: 0;
  align-content: center;
  padding: 15px 12px;
  border-bottom: 1px solid var(--ncp-line);
}

.overview-facts > div:nth-child(n + 4) {
  border-bottom: 0;
}

.overview-facts span {
  color: var(--ncp-text-subtle);
  font-size: .75rem;
}

.overview-facts strong {
  display: block;
  overflow: hidden;
  margin-top: 5px;
  color: var(--ncp-text);
  font-family: var(--ncp-font-latin);
  font-size: .88rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-facts small {
  display: block;
  overflow: hidden;
  margin-top: 4px;
  color: var(--ncp-text-subtle);
  font-size: .68rem;
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.overview-section {
  grid-column: span 1;
  padding: 18px;
}

.overview-section > header {
  display: flex;
  align-items: center;
  gap: 11px;
}

.overview-section > header > svg {
  color: var(--ncp-primary);
}

.overview-section h2 {
  margin: 0;
  font-size: 1rem;
}

.overview-section p {
  margin: 3px 0 0;
  color: var(--ncp-text-subtle);
  font-size: .78rem;
}

.processor-facts {
  display: grid;
  grid-template-columns: 1.6fr 1fr 1fr;
  gap: 1px;
  margin-top: 14px;
  overflow: hidden;
  border: 1px solid var(--ncp-line);
  border-radius: 11px;
  background: var(--ncp-line);
}

.processor-facts > div {
  display: grid;
  min-width: 0;
  gap: 5px;
  padding: 12px;
  background: var(--ncp-surface-quiet);
}

.processor-facts span {
  color: var(--ncp-text-subtle);
  font-size: .72rem;
}

.processor-facts strong {
  overflow: hidden;
  font-family: var(--ncp-font-latin);
  font-size: .86rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.resource-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-top: 14px;
}

.resource-summary > div {
  display: grid;
  grid-template-columns: auto 1fr;
  align-items: center;
  gap: 2px 9px;
  padding: 13px;
  border: 1px solid var(--ncp-line);
  border-radius: 12px;
  background: var(--ncp-surface-quiet);
}

.resource-summary svg {
  grid-row: 1 / 3;
  color: var(--ncp-primary);
}

.resource-summary span {
  color: var(--ncp-text-subtle);
  font-size: .73rem;
}

.resource-summary strong {
  font-family: var(--ncp-font-latin);
  font-size: .95rem;
}

@media (max-width: 1050px) {
  .overview-layout {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 760px) {
  .overview-facts {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .overview-facts > div:nth-child(n + 3) {
    border-bottom: 0;
  }

  .device-summary {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .device-summary__health {
    width: 100%;
    margin-left: 76px;
    justify-items: start;
  }
}

@media (max-width: 520px) {
  .overview-facts,
  .resource-summary {
    grid-template-columns: 1fr;
  }

  .overview-facts > div {
    border-bottom: 1px solid var(--ncp-line) !important;
  }

  .overview-facts > div:last-child {
    border-bottom: 0 !important;
  }

  .device-summary__health {
    margin-left: 0;
  }
}
</style>
