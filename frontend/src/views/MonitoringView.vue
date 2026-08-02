<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Activity, Cpu, Gauge, HardDrive, MemoryStick, Thermometer } from '@lucide/vue'
import { ElSegmented } from 'element-plus'

import { requestMetricSamples, type MetricSample } from '@/api/control'
import DateTimeRangeControl from '@/components/DateTimeRangeControl.vue'
import RealtimeTrendChart from '@/components/RealtimeTrendChart.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { formatLocalTimestamp } from '@/lib/datetime'
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
const temperatureSensors = computed(() => (systemStore.summary?.sensors ?? []).map((sensor, index) => ({
  ...sensor,
  label: temperatureLabel(sensor.name, index),
  tone: temperatureTone(sensor.temperatureCelsius),
})))
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

function temperatureLabel(name: string, index: number) {
  const normalized = name.trim().toLowerCase()
  const labels: Record<string, string> = {
    'soc-thermal': 'SoC 系统芯片',
    'bigcore0-thermal': '大核心 0',
    'bigcore1-thermal': '大核心 1',
    'littlecore-thermal': '小核心',
    'center-thermal': '中心区域',
    'gpu-thermal': 'GPU 图形处理器',
    'npu-thermal': 'NPU AI 加速器',
  }
  if (labels[normalized]) return labels[normalized]
  if (normalized.includes('cpu') || normalized.includes('core')) return `处理器核心 ${index + 1}`
  if (normalized.includes('gpu')) return 'GPU 图形处理器'
  if (normalized.includes('npu')) return 'NPU AI 加速器'
  return `温度传感器 ${index + 1}`
}

function temperatureTone(value: number) {
  if (!Number.isFinite(value)) return 'unknown'
  if (value >= 80) return 'danger'
  if (value >= 65) return 'warning'
  return 'normal'
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
    <section class="temperature-panel panel">
      <header class="temperature-panel__header">
        <div class="temperature-panel__title">
          <span class="temperature-panel__icon"><Thermometer :size="18" /></span>
          <div><strong>温度监控</strong><small>随实时快照更新 · {{ systemStore.summary?.collectedAt ? formatLocalTimestamp(systemStore.summary.collectedAt) : '等待数据' }}</small></div>
        </div>
        <span class="temperature-panel__status"><i></i>实时</span>
      </header>
      <div v-if="temperatureSensors.length" class="temperature-grid">
        <article v-for="sensor in temperatureSensors" :key="sensor.name" :class="['temperature-card', `temperature-card--${sensor.tone}`]">
          <span>{{ sensor.label }}</span>
          <strong>{{ Number.isFinite(sensor.temperatureCelsius) ? `${sensor.temperatureCelsius.toFixed(1)} °C` : '不可用' }}</strong>
        </article>
      </div>
      <div v-else class="temperature-empty">当前没有读取到可用的温度传感器。温度模块会在宿主机暴露传感器后自动出现。</div>
    </section>
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
.temperature-panel { overflow:hidden; }
.temperature-panel__header { display:flex; min-height:64px; align-items:center; justify-content:space-between; gap:16px; padding:13px 18px; border-bottom:1px solid var(--ncp-line); background:linear-gradient(120deg,var(--ncp-surface),var(--ncp-surface-quiet)); }
.temperature-panel__title { display:flex; min-width:0; align-items:center; gap:10px; }
.temperature-panel__icon { display:grid; width:36px; height:36px; flex:0 0 auto; place-items:center; border-radius:10px; background:var(--ncp-warning-soft); color:var(--ncp-warning-strong); }
.temperature-panel__title div { display:grid; min-width:0; gap:2px; }
.temperature-panel__title strong { font-size:.96rem; }
.temperature-panel__title small { overflow:hidden; color:var(--ncp-text-subtle); font-size:.75rem; text-overflow:ellipsis; white-space:nowrap; }
.temperature-panel__status { display:inline-flex; align-items:center; gap:6px; padding:5px 9px; border:1px solid var(--ncp-success-border); border-radius:999px; background:var(--ncp-success-soft); color:var(--ncp-success-strong); font-size:.72rem; font-weight:750; }
.temperature-panel__status i { width:6px; height:6px; border-radius:50%; background:currentColor; }
.temperature-grid { display:grid; grid-template-columns:repeat(6,minmax(0,1fr)); gap:10px; padding:14px 18px 18px; }
.temperature-card { display:grid; min-width:0; gap:6px; padding:12px 13px; border:1px solid var(--ncp-line); border-radius:11px; background:var(--ncp-surface-quiet); }
.temperature-card span { overflow:hidden; color:var(--ncp-text-muted); font-size:.76rem; text-overflow:ellipsis; white-space:nowrap; }
.temperature-card strong { color:var(--ncp-text); font-family:var(--ncp-font-latin); font-size:1.05rem; }
.temperature-card--warning { border-color:rgba(179,110,24,.24); background:var(--ncp-warning-soft); }
.temperature-card--warning strong { color:var(--ncp-warning-strong); }
.temperature-card--danger { border-color:rgba(198,74,89,.24); background:var(--ncp-danger-soft); }
.temperature-card--danger strong { color:var(--ncp-danger-strong); }
.temperature-empty { padding:17px 18px 19px; color:var(--ncp-text-subtle); font-size:.8rem; }
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
@media(max-width:920px) { .temperature-grid { grid-template-columns:repeat(3,minmax(0,1fr)); } }
@media(max-width:640px) {
  .temperature-panel__header { align-items:flex-start; flex-direction:column; gap:10px; padding:13px 15px; }
  .temperature-panel__status { align-self:flex-start; }
  .temperature-grid { grid-template-columns:repeat(2,minmax(0,1fr)); padding:12px 15px 15px; }
  .chart-card { padding:16px; }
}
</style>
