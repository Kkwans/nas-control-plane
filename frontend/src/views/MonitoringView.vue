<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Activity, Cpu, Gauge, HardDrive, LoaderCircle, MemoryStick, Thermometer } from '@lucide/vue'

import { requestMetricSamples, type MetricSample } from '@/api/control'
import DateTimeRangeControl from '@/components/DateTimeRangeControl.vue'
import RealtimeTrendChart, { type TrendSeries } from '@/components/RealtimeTrendChart.vue'
import ResourceState from '@/components/ResourceState.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import CompactSegmentedFilter from '@/components/CompactSegmentedFilter.vue'
import { useManualRefreshRegistry } from '@/composables/manualRefresh'
import { monitoringChartTokens } from '@/domain/monitoring/chartTokens'
import { mergeMetricSampleWindow } from '@/domain/monitoring/series'
import { useSystemStore } from '@/stores/system'

type TimeRange = '1h' | '6h' | '24h' | '7d'
const rangeMilliseconds: Record<TimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
}
const quickRanges = [
  { label: '1 小时', value: '1h' },
  { label: '6 小时', value: '6h' },
  { label: '24 小时', value: '24h' },
  { label: '7 天', value: '7d' },
]

const range = ref<TimeRange>('6h')
const customFrom = ref<Date | null>(null)
const customTo = ref<Date | null>(null)
const customFollowsNow = ref(false)
const systemStore = useSystemStore()
const samples = ref<MetricSample[]>([])
const loading = ref(false)
const error = ref('')
const manualRefreshRegistry = useManualRefreshRegistry()
let unregisterManualRefresh: (() => void) | undefined
let loadSequence = 0
let loadController: AbortController | null = null
const loadedRangeKey = ref('')
const customDuration = ref<number | null>(null)
const latest = computed(() => samples.value.at(-1))
const timestamps = computed(() => samples.value.map((item) => item.collectedAt))

const stats = computed<WorkspaceStat[]>(() => [
  { label: '采样数量', value: samples.value.length },
  { label: 'CPU', value: latest.value && finiteNumber(latest.value.cpuPercent) ? `${latest.value.cpuPercent.toFixed(1)}%` : '—' },
  { label: '内存', value: latest.value && finiteNumber(latest.value.memoryPercent) ? `${latest.value.memoryPercent.toFixed(1)}%` : '—' },
])

const networkRates = computed(() => samples.value.map((item, index) => {
  const previous = samples.value[index - 1]
  if (!previous) return { receive: 0, transmit: 0 }
  const seconds = sampleSeconds(item, previous)
  return {
    receive: Math.max((item.networkReceiveBytes - previous.networkReceiveBytes) / seconds / 1024, 0),
    transmit: Math.max((item.networkTransmitBytes - previous.networkTransmitBytes) / seconds / 1024, 0),
  }
}))

function counterRates(key: 'diskReadBytes' | 'diskWriteBytes') {
  return samples.value.map((item, index) => {
    const current = item[key]
    if (!finiteNumber(current)) return Number.NaN
    const previous = samples.value[index - 1]
    const previousValue = previous?.[key]
    if (!previous || !finiteNumber(previousValue)) return 0
    return Math.max((current - previousValue) / sampleSeconds(item, previous) / 1024 / 1024, 0)
  })
}

const diskIORates = computed(() => {
  const hasRead = samples.value.some((item) => finiteNumber(item.diskReadBytes))
  const hasWrite = samples.value.some((item) => finiteNumber(item.diskWriteBytes))
  return {
    hasData: hasRead || hasWrite,
    read: hasRead ? counterRates('diskReadBytes') : [],
    write: hasWrite ? counterRates('diskWriteBytes') : [],
  }
})
const diskIOSeries = computed<TrendSeries[]>(() => {
  const series: TrendSeries[] = []
  if (diskIORates.value.read.length) series.push({ name: '读取', color: monitoringChartTokens.storage, values: diskIORates.value.read, unit: 'MB/s', decimals: 2 })
  if (diskIORates.value.write.length) series.push({ name: '写入', color: monitoringChartTokens.load, values: diskIORates.value.write, unit: 'MB/s', decimals: 2 })
  return series
})
const diskIOMessage = computed(() => {
  if (!samples.value.length) return '当前范围暂无磁盘 I/O 历史样本。'
  if (!diskIORates.value.hasData) return '当前运行数据暂未提供磁盘读写统计；接入磁盘 I/O 采集后显示速率。'
  return '磁盘 I/O 数据尚未积累到足够样本。'
})

const temperatureNames = computed(() => {
  const names: string[] = []
  for (const sample of samples.value) {
    for (const temperature of sample.temperatures ?? []) {
      if (temperature.name && !names.includes(temperature.name)) names.push(temperature.name)
    }
  }
  return names
})
const temperatureSeries = computed<TrendSeries[]>(() => temperatureNames.value.map((name, index) => ({
  name: temperatureLabel(name, index),
  color: monitoringChartTokens.temperature[index % monitoringChartTokens.temperature.length] ?? monitoringChartTokens.load,
  values: samples.value.map((sample) => {
    const reading = sample.temperatures?.find((item) => item.name === name)?.temperatureCelsius
    return finiteNumber(reading) ? reading : Number.NaN
  }),
})).filter((series) => series.values.some((value) => finiteNumber(value))))
const hasTemperatureHistoryField = computed(() => samples.value.some((sample) => Array.isArray(sample.temperatures)))
const temperatureTrendMessage = computed(() => {
  if (!samples.value.length) return '当前范围暂无温度历史样本。'
  if (!hasTemperatureHistoryField.value) return '当前运行数据暂未提供温度历史；采集到传感器数据后显示趋势。'
  return '当前范围暂无可绘制的温度历史样本。'
})

async function load() {
  const sequence = ++loadSequence
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  const preciseRange = customFrom.value && customTo.value
    ? { from: customFrom.value.toISOString(), to: customTo.value.toISOString() }
    : range.value
  const rangeKey = typeof preciseRange === 'string' ? preciseRange : `${preciseRange.from}|${preciseRange.to}`
  loadedRangeKey.value = ''
  samples.value = []
  loading.value = true
  error.value = ''
  try {
    const result = await requestMetricSamples(preciseRange, controller.signal)
    if (sequence !== loadSequence || controller.signal.aborted) return
    samples.value = result
    loadedRangeKey.value = rangeKey
  } catch (caught) {
    if (sequence !== loadSequence || (caught instanceof DOMException && caught.name === 'AbortError')) return
    error.value = '监控历史加载失败，请稍后重试。'
  } finally {
    if (sequence === loadSequence) loading.value = false
  }
}

function selectQuickRange(value: TimeRange) {
  range.value = value
  customFrom.value = null
  customTo.value = null
  customFollowsNow.value = false
  customDuration.value = null
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
  customDuration.value = to.valueOf() - from.valueOf()
  customFollowsNow.value = now.valueOf() - to.valueOf() <= 60_000
  void load()
}

function clearCustomRange() {
  customFrom.value = null
  customTo.value = null
  customFollowsNow.value = false
  customDuration.value = null
  error.value = ''
  void load()
}

function useNow() {
  const now = new Date()
  customTo.value = now
  customFrom.value ??= new Date(now.valueOf() - rangeMilliseconds[range.value])
  customFollowsNow.value = true
  customDuration.value = customTo.value.valueOf() - customFrom.value.valueOf()
}

function summaryToSample(): MetricSample | null {
  const summary = systemStore.summary
  if (!summary) return null
  const storageTotal = summary.storage.reduce((total, item) => total + item.totalBytes, 0)
  const storageUsed = summary.storage.reduce((total, item) => total + item.usedBytes, 0)
  const sample: MetricSample = {
    collectedAt: summary.collectedAt,
    cpuPercent: summary.cpu.usagePercent,
    memoryPercent: summary.memory.totalBytes > 0 ? summary.memory.usedBytes / summary.memory.totalBytes * 100 : 0,
    load1: summary.cpu.load1,
    diskPercent: storageTotal > 0 ? storageUsed / storageTotal * 100 : 0,
    networkReceiveBytes: summary.network.reduce((total, item) => total + item.receiveBytes, 0),
    networkTransmitBytes: summary.network.reduce((total, item) => total + item.transmitBytes, 0),
    temperatures: (summary.sensors ?? []).map((sensor) => ({
      name: sensor.name,
      temperatureCelsius: sensor.temperatureCelsius,
    })),
  }
  const diskIO = summary.diskIO
  if (diskIO && finiteNumber(diskIO.readBytes)) sample.diskReadBytes = diskIO.readBytes
  if (diskIO && finiteNumber(diskIO.writeBytes)) sample.diskWriteBytes = diskIO.writeBytes
  return sample
}

function mergeRealtimeSample() {
  if (!loadedRangeKey.value) return
  const sample = summaryToSample()
  if (!sample) return
  const timestamp = new Date(sample.collectedAt).valueOf()
  if (!Number.isFinite(timestamp)) return

  let lowerBound: number
  if (customFrom.value && customTo.value) {
    if (!customFollowsNow.value) return
    const duration = customDuration.value ?? (customTo.value.valueOf() - customFrom.value.valueOf())
    customTo.value = new Date(timestamp)
    customFrom.value = new Date(timestamp - duration)
    lowerBound = customFrom.value.valueOf()
  }
  else {
    lowerBound = timestamp - rangeMilliseconds[range.value]
  }
  samples.value = mergeMetricSampleWindow(samples.value, sample, lowerBound, timestamp)
}

function handleManualRefresh() {
  return load().then(() => {
    if (error.value) throw new Error(error.value)
  })
}

function sampleSeconds(current: MetricSample, previous: MetricSample) {
  const elapsed = (new Date(current.collectedAt).valueOf() - new Date(previous.collectedAt).valueOf()) / 1000
  return Number.isFinite(elapsed) ? Math.max(elapsed, 1) : 1
}

function finiteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
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

watch(() => systemStore.summary?.collectedAt, (next, previous) => {
  if (next && next !== previous) mergeRealtimeSample()
})
onMounted(() => {
  unregisterManualRefresh = manualRefreshRegistry?.register(handleManualRefresh)
  void load()
})
onBeforeUnmount(() => {
  unregisterManualRefresh?.()
  loadController?.abort()
})
</script>

<template>
  <div class="page workspace-page">
    <WorkspaceHeader title="系统监控" description="查看 CPU、内存、负载、磁盘、网络与温度的历史运行趋势" :icon="Gauge" :stats="stats">
      <template #filters>
        <div class="monitor-range-filter" role="group" aria-label="快速时间范围">
          <CompactSegmentedFilter :model-value="customFrom && customTo ? '' : range" :options="quickRanges" accessible-label="快速时间范围" @update:model-value="selectQuickRange($event as TimeRange)" />
        </div>
      </template>
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

    <ResourceState v-if="error" state="error" title="监控历史加载失败" :message="error" next-step="确认 Agent 可用后重试。" @retry="load" />
    <div v-if="loading && samples.length" class="monitor-loading" role="status" aria-live="polite">
      <LoaderCircle :size="16" aria-hidden="true" />正在更新监控历史…
    </div>

    <section v-if="loading && !samples.length" class="monitor-grid monitor-grid--loading" role="status" aria-live="polite" aria-label="正在加载监控图表">
      <article v-for="item in 6" :key="item" class="chart-card panel" aria-hidden="true"><i v-for="line in 8" :key="line" class="ncp-skeleton"></i></article>
    </section>
    <section v-else class="monitor-grid" aria-label="监控趋势图表" :aria-busy="loading">
      <article class="chart-card panel" aria-labelledby="cpu-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><Cpu :size="18" /></span>
          <div><h2 id="cpu-chart-title">处理器与负载</h2><p>CPU 使用率与 1 分钟负载</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" :series="[{name:'CPU',color:monitoringChartTokens.cpu,values:samples.map(i=>i.cpuPercent),unit:'%',axis:'left'},{name:'负载',color:monitoringChartTokens.load,values:samples.map(i=>i.load1),unit:'',decimals:2,axis:'right'}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="memory-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><MemoryStick :size="18" /></span>
          <div><h2 id="memory-chart-title">内存使用</h2><p>已用内存占总容量比例</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" :series="[{name:'内存',color:monitoringChartTokens.memory,values:samples.map(i=>i.memoryPercent),unit:'%'}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="storage-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><HardDrive :size="18" /></span>
          <div><h2 id="storage-chart-title">存储使用</h2><p>所有已采集挂载点的合计占用</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" :series="[{name:'磁盘',color:monitoringChartTokens.storage,values:samples.map(i=>i.diskPercent),unit:'%'}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="network-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><Activity :size="18" /></span>
          <div><h2 id="network-chart-title">网络吞吐</h2><p>接收与发送速率</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" :series="[{name:'接收',color:monitoringChartTokens.receive,values:networkRates.map(i=>i.receive),unit:'KB/s',decimals:1},{name:'发送',color:monitoringChartTokens.transmit,values:networkRates.map(i=>i.transmit),unit:'KB/s',decimals:1}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="disk-io-chart-title">
        <header>
          <span class="chart-card__icon chart-card__icon--violet" aria-hidden="true"><HardDrive :size="18" /></span>
          <div><h2 id="disk-io-chart-title">磁盘 I/O</h2><p>宿主机磁盘读写速率</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" :series="diskIOSeries" :empty-message="diskIOMessage" />
      </article>
      <article class="chart-card panel" aria-labelledby="temperature-chart-title">
        <header>
          <span class="chart-card__icon chart-card__icon--amber" aria-hidden="true"><Thermometer :size="18" /></span>
          <div><h2 id="temperature-chart-title">温度趋势</h2><p>按传感器显示历史温度趋势</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" :series="temperatureSeries" unit="°C" :empty-message="temperatureTrendMessage" />
      </article>
    </section>
  </div>
</template>

<style scoped>
.monitor-range-filter { display:flex; min-width:0; align-items:center; }
.monitor-range-filter :deep(.el-segmented) { max-width:100%; }
.monitor-error { margin-bottom:14px; padding:12px 14px; border:1px solid color-mix(in srgb, var(--ncp-danger) 20%, transparent); border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.86rem; }
.monitor-loading { display:flex; min-height:36px; align-items:center; gap:7px; margin-bottom:12px; color:var(--ncp-text-muted); font-size:.76rem; }
.monitor-loading svg { animation:monitor-spin .9s linear infinite; color:var(--ncp-primary); }
.monitor-grid { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:16px; align-items:stretch; }
.chart-card { min-width:0; padding:20px; transition:border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.chart-card:hover { border-color:rgba(52,116,212,.22); box-shadow:0 15px 34px rgba(44,66,94,.08); transform:translateY(-1px); }
.chart-card>header { display:flex; align-items:center; gap:11px; margin-bottom:12px; }
.chart-card__icon { display:grid; width:34px; height:34px; flex:0 0 auto; place-items:center; border-radius:10px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.chart-card__icon--violet { background:color-mix(in srgb, var(--ncp-chart-storage) 12%, transparent); color:var(--ncp-chart-storage); }
.chart-card__icon--amber { background:var(--ncp-warning-soft); color:var(--ncp-warning-strong); }
.chart-card>header>div { display:grid; min-width:0; gap:2px; }
.chart-card h2 { margin:0; color:var(--ncp-text); font-size:1rem; }
.chart-card p { margin:0; overflow:hidden; color:var(--ncp-text-muted); font-size:.82rem; text-overflow:ellipsis; white-space:nowrap; }
.chart-card>.ncp-skeleton { display:block; width:100%; height:18px; margin:10px 0; }
.monitor-grid--loading .chart-card { min-height:300px; }
@keyframes monitor-spin { to { transform:rotate(360deg); } }
@media(max-width:1100px) {
  .monitor-grid { grid-template-columns:repeat(2,minmax(0,1fr)); }
}
@media(max-width:640px) {
  .monitor-grid { grid-template-columns:1fr; }
  .chart-card { padding:16px; }
}
@media(prefers-reduced-motion: reduce) {
  .monitor-loading svg { animation:none; }
  .chart-card { transition:border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
  .chart-card:hover { transform:none; }
}
</style>
