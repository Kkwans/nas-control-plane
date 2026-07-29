<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Activity, Cpu, Gauge, HardDrive, MemoryStick } from '@lucide/vue'
import { ElSegmented } from 'element-plus'

import { requestMetricSamples, type MetricSample } from '@/api/control'
import DateTimeRangeControl from '@/components/DateTimeRangeControl.vue'
import RealtimeTrendChart from '@/components/RealtimeTrendChart.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

type TimeRange = '1h' | '6h' | '24h' | '7d'
const rangeMilliseconds: Record<TimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
}
const range = ref<TimeRange>('6h')
const customFrom = ref<Date | null>(null)
const customTo = ref<Date | null>(null)
const customFollowsNow = ref(false)
const systemStore = useSystemStore()
const samples = ref<MetricSample[]>([])
const loading = ref(false)
const error = ref('')
const latest = computed(() => samples.value.at(-1))
const stats = computed<WorkspaceStat[]>(() => [
  { label: '采样数量', value: samples.value.length },
  { label: 'CPU', value: latest.value ? `${latest.value.cpuPercent.toFixed(1)}%` : '—' },
  { label: '内存', value: latest.value ? `${latest.value.memoryPercent.toFixed(1)}%` : '—' },
])
const timestamps = computed(() => samples.value.map((item) => item.collectedAt))
const networkRates = computed(() => samples.value.map((item, index) => {
  const previous = samples.value[index - 1]
  if (!previous) return { receive: 0, transmit: 0 }
  const seconds = Math.max((new Date(item.collectedAt).valueOf() - new Date(previous.collectedAt).valueOf()) / 1000, 1)
  return {
    receive: Math.max((item.networkReceiveBytes - previous.networkReceiveBytes) / seconds / 1024, 0),
    transmit: Math.max((item.networkTransmitBytes - previous.networkTransmitBytes) / seconds / 1024, 0),
  }
}))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const preciseRange = customFrom.value && customTo.value
      ? { from: customFrom.value.toISOString(), to: customTo.value.toISOString() }
      : range.value
    samples.value = await requestMetricSamples(preciseRange)
  }
  catch { error.value = '监控历史加载失败，请稍后重试。' }
  finally { loading.value = false }
}
function selectQuickRange(value: TimeRange) {
  range.value = value
  customFrom.value = null
  customTo.value = null
  customFollowsNow.value = false
  void load()
}

function applyCustomRange() {
  if (!customFrom.value || !customTo.value) return
  const from = customFrom.value
  const to = customTo.value
  const now = new Date()
  if (from >= to || to > now || to.valueOf() - from.valueOf() > 7 * 24 * 60 * 60 * 1000) {
    error.value = '请选择不超过 7 天、且结束时间不晚于当前时间的范围。'
    return
  }
  customFollowsNow.value = now.valueOf() - to.valueOf() <= 60_000
  void load()
}

function clearCustomRange() {
  customFrom.value = null
  customTo.value = null
  customFollowsNow.value = false
  error.value = ''
  void load()
}

function useNow() {
  const now = new Date()
  customTo.value = now
  customFrom.value ??= new Date(now.valueOf() - rangeMilliseconds[range.value])
  customFollowsNow.value = true
}

function summaryToSample(): MetricSample | null {
  const summary = systemStore.summary
  if (!summary) return null
  const storageTotal = summary.storage.reduce((total, item) => total + item.totalBytes, 0)
  const storageUsed = summary.storage.reduce((total, item) => total + item.usedBytes, 0)
  return {
    collectedAt: summary.collectedAt,
    cpuPercent: summary.cpu.usagePercent,
    memoryPercent: summary.memory.totalBytes > 0 ? summary.memory.usedBytes / summary.memory.totalBytes * 100 : 0,
    load1: summary.cpu.load1,
    diskPercent: storageTotal > 0 ? storageUsed / storageTotal * 100 : 0,
    networkReceiveBytes: summary.network.reduce((total, item) => total + item.receiveBytes, 0),
    networkTransmitBytes: summary.network.reduce((total, item) => total + item.transmitBytes, 0),
  }
}

function mergeRealtimeSample() {
  const sample = summaryToSample()
  if (!sample) return
  const timestamp = new Date(sample.collectedAt).valueOf()
  if (!Number.isFinite(timestamp)) return

  let lowerBound: number
  let upperBound = Number.POSITIVE_INFINITY
  if (customFrom.value && customTo.value) {
    if (!customFollowsNow.value) return
    lowerBound = customFrom.value.valueOf()
    customTo.value = new Date(timestamp)
  }
  else {
    lowerBound = timestamp - rangeMilliseconds[range.value]
  }
  const merged = new Map<number, MetricSample>()
  for (const item of samples.value) {
    const itemTimestamp = new Date(item.collectedAt).valueOf()
    if (itemTimestamp >= lowerBound && itemTimestamp <= upperBound) merged.set(itemTimestamp, item)
  }
  merged.set(timestamp, sample)
  samples.value = [...merged.entries()].sort(([left], [right]) => left - right).map(([, item]) => item)
}

function handleManualRefresh() {
  void load()
}

watch(() => systemStore.summary?.collectedAt, (next, previous) => {
  if (next && next !== previous) mergeRealtimeSample()
})
onMounted(() => {
  window.addEventListener('ncp:manual-refresh', handleManualRefresh)
  void load()
})
onBeforeUnmount(() => window.removeEventListener('ncp:manual-refresh', handleManualRefresh))
</script>

<template>
  <div class="page workspace-page">
    <WorkspaceHeader title="系统监控" description="查看 CPU、内存、负载、磁盘与网络的历史运行趋势" :icon="Gauge" :stats="stats">
      <template #filters><ElSegmented :model-value="customFrom && customTo ? '' : range" :options="[{label:'1 小时',value:'1h'},{label:'6 小时',value:'6h'},{label:'24 小时',value:'24h'},{label:'7 天',value:'7d'}]" @change="selectQuickRange($event as TimeRange)" /></template>
      <template #tools>
        <DateTimeRangeControl
          v-model:from="customFrom"
          v-model:to="customTo"
          :loading="loading"
          @now="useNow"
          @clear="clearCustomRange"
          @apply="applyCustomRange"
        />
      </template>
    </WorkspaceHeader>
    <div v-if="error" class="monitor-error">{{ error }}</div>
    <section v-if="loading && !samples.length" class="monitor-grid">
      <article v-for="item in 4" :key="item" class="chart-card panel"><i v-for="line in 8" :key="line" class="ncp-skeleton"></i></article>
    </section>
    <section v-else class="monitor-grid">
      <article class="chart-card panel"><header><Cpu :size="18" /><div><strong>处理器与负载</strong><small>CPU 使用率与 1 分钟负载</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'CPU',color:'#2468d8',values:samples.map(i=>i.cpuPercent)},{name:'负载',color:'#d28a1b',values:samples.map(i=>i.load1)}]" /></article>
      <article class="chart-card panel"><header><MemoryStick :size="18" /><div><strong>内存使用</strong><small>已用内存占总容量比例</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'内存',color:'#16866a',values:samples.map(i=>i.memoryPercent)}]" /></article>
      <article class="chart-card panel"><header><HardDrive :size="18" /><div><strong>存储使用</strong><small>所有已采集挂载点的合计占用</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'磁盘',color:'#7a5bd0',values:samples.map(i=>i.diskPercent)}]" /></article>
      <article class="chart-card panel"><header><Activity :size="18" /><div><strong>网络吞吐</strong><small>接收与发送速率</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="KB/s" :series="[{name:'接收',color:'#16866a',values:networkRates.map(i=>i.receive)},{name:'发送',color:'#2468d8',values:networkRates.map(i=>i.transmit)}]" /></article>
    </section>
  </div>
</template>

<style scoped>
.monitor-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; }
.chart-card { min-width:0; padding:20px; transition:border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.chart-card:hover { border-color:rgba(52,116,212,.22); box-shadow:0 15px 34px rgba(44,66,94,.08); transform:translateY(-1px); }
.chart-card>header { display:flex; align-items:center; gap:11px; margin-bottom:12px; color:var(--ncp-primary-strong); }
.chart-card>header>svg { padding:8px; box-sizing:content-box; border-radius:10px; background:var(--ncp-primary-soft); }
.chart-card>header div { display:grid; gap:2px; }
.chart-card strong { color:var(--ncp-text); font-size:1rem; }
.chart-card small { color:var(--ncp-text-muted); font-size:.82rem; }
.chart-card>.ncp-skeleton { display:block; width:100%; height:18px; margin:10px 0; }
.monitor-error { margin-bottom:14px; padding:12px 14px; border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.86rem; }
@media(max-width:920px) { .monitor-grid { grid-template-columns:1fr; } }
@media(max-width:640px) { .chart-card { padding:16px; } }
</style>
