<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Activity, Cpu, Gauge, HardDrive, LoaderCircle, MemoryStick, Thermometer } from '@lucide/vue'
import { ElSegmented } from 'element-plus'

import { requestMetricSamples, type MetricSample } from '@/api/control'
import type { SystemSummary } from '@/api/system'
import DateTimeRangeControl from '@/components/DateTimeRangeControl.vue'
import RealtimeTrendChart, { type TrendSeries } from '@/components/RealtimeTrendChart.vue'
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
const quickRanges = [
  { label: '1 小时', value: '1h' },
  { label: '6 小时', value: '6h' },
  { label: '24 小时', value: '24h' },
  { label: '7 天', value: '7d' },
]

interface MonitoringTemperature {
  name: string
  temperatureCelsius: number
}

/**
 * The history endpoint already returns these fields in the running backend.
 * They stay optional here until the shared frontend API contract is updated.
 */
type MonitoringSample = MetricSample & {
  diskReadBytes?: number
  diskWriteBytes?: number
  temperatures?: MonitoringTemperature[]
}

type MonitoringSummary = SystemSummary & {
  diskIO?: {
    readBytes?: number
    writeBytes?: number
  }
}

const range = ref<TimeRange>('6h')
const customFrom = ref<Date | null>(null)
const customTo = ref<Date | null>(null)
const customFollowsNow = ref(false)
const systemStore = useSystemStore()
const samples = ref<MonitoringSample[]>([])
const loading = ref(false)
const error = ref('')
const latest = computed(() => samples.value.at(-1))
const timestamps = computed(() => samples.value.map((item) => item.collectedAt))

const temperatureSensors = computed(() => (systemStore.summary?.sensors ?? []).map((sensor, index) => ({
  ...sensor,
  label: temperatureLabel(sensor.name, index),
  tone: temperatureTone(sensor.temperatureCelsius),
})))
const temperatureGridStyle = computed(() => ({ '--temperature-columns': temperatureColumns(temperatureSensors.value.length) }))
const hasRealtimeSummary = computed(() => Boolean(systemStore.summary))
const temperatureWarning = computed(() => Boolean(systemStore.summary?.warnings.some((warning) => warning.source === 'temperature')))
const temperatureStatus = computed(() => {
  if (temperatureSensors.value.length) return '实时'
  return hasRealtimeSummary.value ? '无传感器' : '等待数据'
})
const temperatureEmptyMessage = computed(() => {
  if (!hasRealtimeSummary.value) return '等待实时快照，以确认宿主机是否暴露温度传感器。'
  if (temperatureWarning.value) return '宿主机温度采集暂不可用；请检查传感器权限或驱动状态。'
  return '当前宿主机未暴露可用的温度传感器，温度卡片不会使用虚构的 0 °C 填充。'
})
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
  if (diskIORates.value.read.length) series.push({ name: '读取', color: '#7a5bd0', values: diskIORates.value.read })
  if (diskIORates.value.write.length) series.push({ name: '写入', color: '#d28a1b', values: diskIORates.value.write })
  return series
})
const diskIOMessage = computed(() => {
  if (!samples.value.length) return '当前范围暂无磁盘 I/O 历史样本。'
  if (!diskIORates.value.hasData) return '当前接口尚未提供磁盘 I/O 累计计数器；接入 diskReadBytes 与 diskWriteBytes 后显示速率。'
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
  color: ['#d28a1b', '#c64a59', '#7a5bd0', '#16866a'][index % 4] ?? '#d28a1b',
  values: samples.value.map((sample) => {
    const reading = sample.temperatures?.find((item) => item.name === name)?.temperatureCelsius
    return finiteNumber(reading) ? reading : Number.NaN
  }),
})).filter((series) => series.values.some((value) => finiteNumber(value))))
const hasTemperatureHistoryField = computed(() => samples.value.some((sample) => Array.isArray(sample.temperatures)))
const temperatureTrendMessage = computed(() => {
  if (!samples.value.length) return '当前范围暂无温度历史样本。'
  if (!hasTemperatureHistoryField.value) return '当前接口尚未提供 temperatures 历史字段；下方卡片仍显示实时传感器状态。'
  return '当前范围暂无可绘制的温度历史样本。'
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const preciseRange = customFrom.value && customTo.value
      ? { from: customFrom.value.toISOString(), to: customTo.value.toISOString() }
      : range.value
    samples.value = await requestMetricSamples(preciseRange)
  }
  catch {
    error.value = '监控历史加载失败，请稍后重试。'
  }
  finally {
    loading.value = false
  }
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

function summaryToSample(): MonitoringSample | null {
  const summary = systemStore.summary as MonitoringSummary | null
  if (!summary) return null
  const storageTotal = summary.storage.reduce((total, item) => total + item.totalBytes, 0)
  const storageUsed = summary.storage.reduce((total, item) => total + item.usedBytes, 0)
  const sample: MonitoringSample = {
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
  const sample = summaryToSample()
  if (!sample) return
  const timestamp = new Date(sample.collectedAt).valueOf()
  if (!Number.isFinite(timestamp)) return

  let lowerBound: number
  if (customFrom.value && customTo.value) {
    if (!customFollowsNow.value) return
    lowerBound = customFrom.value.valueOf()
    customTo.value = new Date(timestamp)
  }
  else {
    lowerBound = timestamp - rangeMilliseconds[range.value]
  }
  const merged = new Map<number, MonitoringSample>()
  for (const item of samples.value) {
    const itemTimestamp = new Date(item.collectedAt).valueOf()
    if (itemTimestamp >= lowerBound && itemTimestamp <= timestamp) merged.set(itemTimestamp, item)
  }
  merged.set(timestamp, sample)
  samples.value = [...merged.entries()].sort(([left], [right]) => left - right).map(([, item]) => item)
}

function handleManualRefresh() {
  void load()
}

function sampleSeconds(current: MonitoringSample, previous: MonitoringSample) {
  const elapsed = (new Date(current.collectedAt).valueOf() - new Date(previous.collectedAt).valueOf()) / 1000
  return Number.isFinite(elapsed) ? Math.max(elapsed, 1) : 1
}

function finiteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function temperatureColumns(count: number) {
  if (count <= 1) return 1
  const start = Math.ceil(Math.sqrt(count))
  for (let columns = start; columns <= count; columns += 1) {
    if (count % columns !== 1) return columns
  }
  return count
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
    <WorkspaceHeader title="系统监控" description="查看 CPU、内存、负载、磁盘、网络与温度的历史运行趋势" :icon="Gauge" :stats="stats">
      <template #filters>
        <div class="monitor-range-filter" role="group" aria-label="快速时间范围">
          <ElSegmented :model-value="customFrom && customTo ? '' : range" :options="quickRanges" aria-label="快速时间范围" @change="selectQuickRange($event as TimeRange)" />
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

    <div v-if="error" class="monitor-error" role="alert" aria-live="assertive" aria-atomic="true">{{ error }}</div>
    <div v-if="loading && samples.length" class="monitor-loading" role="status" aria-live="polite">
      <LoaderCircle :size="16" aria-hidden="true" />正在更新监控历史…
    </div>

    <section class="temperature-panel panel" aria-labelledby="temperature-title">
      <header class="temperature-panel__header">
        <div class="temperature-panel__title">
          <span class="temperature-panel__icon" aria-hidden="true"><Thermometer :size="18" /></span>
          <div>
            <h2 id="temperature-title">温度监控</h2>
            <p>实时快照 · {{ systemStore.summary?.collectedAt ? formatLocalTimestamp(systemStore.summary.collectedAt) : '等待数据' }}</p>
          </div>
        </div>
        <span :class="['temperature-panel__status', `temperature-panel__status--${temperatureSensors.length ? 'ready' : hasRealtimeSummary ? 'missing' : 'waiting'}`]" role="status" aria-live="polite"><i aria-hidden="true"></i>{{ temperatureStatus }}</span>
      </header>
      <div v-if="temperatureSensors.length" class="temperature-grid" role="list" aria-label="实时温度传感器" :style="temperatureGridStyle">
        <article v-for="(sensor, index) in temperatureSensors" :key="`${sensor.name}-${index}`" :class="['temperature-card', `temperature-card--${sensor.tone}`]" role="listitem" :aria-label="`${sensor.label}：${Number.isFinite(sensor.temperatureCelsius) ? `${sensor.temperatureCelsius.toFixed(1)} °C` : '不可用'}`">
          <span>{{ sensor.label }}</span>
          <strong>{{ Number.isFinite(sensor.temperatureCelsius) ? `${sensor.temperatureCelsius.toFixed(1)} °C` : '不可用' }}</strong>
        </article>
      </div>
      <div v-else class="temperature-empty" role="status" aria-live="polite">{{ temperatureEmptyMessage }}</div>
    </section>

    <section v-if="loading && !samples.length" class="monitor-grid monitor-grid--loading" role="status" aria-live="polite" aria-label="正在加载监控图表">
      <article v-for="item in 6" :key="item" class="chart-card panel" aria-hidden="true"><i v-for="line in 8" :key="line" class="ncp-skeleton"></i></article>
    </section>
    <section v-else class="monitor-grid" aria-label="监控趋势图表" :aria-busy="loading">
      <article class="chart-card panel" aria-labelledby="cpu-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><Cpu :size="18" /></span>
          <div><h2 id="cpu-chart-title">处理器与负载</h2><p>CPU 使用率与 1 分钟负载</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'CPU',color:'#2468d8',values:samples.map(i=>i.cpuPercent)},{name:'负载',color:'#d28a1b',values:samples.map(i=>i.load1)}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="memory-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><MemoryStick :size="18" /></span>
          <div><h2 id="memory-chart-title">内存使用</h2><p>已用内存占总容量比例</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'内存',color:'#16866a',values:samples.map(i=>i.memoryPercent)}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="storage-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><HardDrive :size="18" /></span>
          <div><h2 id="storage-chart-title">存储使用</h2><p>所有已采集挂载点的合计占用</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'磁盘',color:'#7a5bd0',values:samples.map(i=>i.diskPercent)}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="network-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><Activity :size="18" /></span>
          <div><h2 id="network-chart-title">网络吞吐</h2><p>接收与发送速率</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" unit="KB/s" :series="[{name:'接收',color:'#16866a',values:networkRates.map(i=>i.receive)},{name:'发送',color:'#2468d8',values:networkRates.map(i=>i.transmit)}]" />
      </article>
      <article class="chart-card panel" aria-labelledby="disk-io-chart-title">
        <header>
          <span class="chart-card__icon chart-card__icon--violet" aria-hidden="true"><HardDrive :size="18" /></span>
          <div><h2 id="disk-io-chart-title">磁盘 I/O</h2><p>宿主机磁盘读写速率</p></div>
        </header>
        <RealtimeTrendChart :timestamps="timestamps" :series="diskIOSeries" unit="MB/s" :empty-message="diskIOMessage" />
      </article>
      <article class="chart-card panel" aria-labelledby="temperature-chart-title">
        <header>
          <span class="chart-card__icon chart-card__icon--amber" aria-hidden="true"><Thermometer :size="18" /></span>
          <div><h2 id="temperature-chart-title">温度历史</h2><p>按传感器显示历史温度趋势</p></div>
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
.temperature-panel { overflow:hidden; }
.temperature-panel__header { display:flex; min-height:64px; align-items:center; justify-content:space-between; gap:16px; padding:13px 18px; border-bottom:1px solid var(--ncp-line); background:linear-gradient(120deg,var(--ncp-surface),var(--ncp-surface-quiet)); }
.temperature-panel__title { display:flex; min-width:0; align-items:center; gap:10px; }
.temperature-panel__icon { display:grid; width:36px; height:36px; flex:0 0 auto; place-items:center; border-radius:10px; background:var(--ncp-warning-soft); color:var(--ncp-warning-strong); }
.temperature-panel__title div { display:grid; min-width:0; gap:2px; }
.temperature-panel__title h2 { margin:0; color:var(--ncp-text); font-size:.96rem; }
.temperature-panel__title p { margin:0; overflow:hidden; color:var(--ncp-text-subtle); font-size:.75rem; text-overflow:ellipsis; white-space:nowrap; }
.temperature-panel__status { display:inline-flex; align-items:center; gap:6px; padding:5px 9px; border:1px solid var(--ncp-line); border-radius:999px; background:var(--ncp-surface-quiet); color:var(--ncp-text-subtle); font-size:.72rem; font-weight:750; white-space:nowrap; }
.temperature-panel__status i { width:6px; height:6px; border-radius:50%; background:currentColor; }
.temperature-panel__status--ready { border-color:var(--ncp-success-border); background:var(--ncp-success-soft); color:var(--ncp-success-strong); }
.temperature-panel__status--missing { border-color:var(--ncp-warning-border); background:var(--ncp-warning-soft); color:var(--ncp-warning-strong); }
.temperature-panel__status--waiting { color:var(--ncp-text-subtle); }
.temperature-grid { display:grid; grid-template-columns:repeat(var(--temperature-columns),minmax(0,1fr)); gap:10px; padding:14px 18px 18px; }
.temperature-card { display:grid; min-width:0; gap:6px; padding:12px 13px; border:1px solid var(--ncp-line); border-radius:11px; background:var(--ncp-surface-quiet); }
.temperature-card span { overflow:hidden; color:var(--ncp-text-muted); font-size:.76rem; text-overflow:ellipsis; white-space:nowrap; }
.temperature-card strong { color:var(--ncp-text); font-family:var(--ncp-font-latin); font-size:1.05rem; font-variant-numeric:tabular-nums; }
.temperature-card--warning { border-color:rgba(179,110,24,.24); background:var(--ncp-warning-soft); }
.temperature-card--warning strong { color:var(--ncp-warning-strong); }
.temperature-card--danger { border-color:rgba(198,74,89,.24); background:var(--ncp-danger-soft); }
.temperature-card--danger strong { color:var(--ncp-danger-strong); }
.temperature-empty { padding:17px 18px 19px; color:var(--ncp-text-subtle); font-size:.8rem; }
.monitor-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; align-items:stretch; }
.chart-card { min-width:0; padding:20px; transition:border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.chart-card:hover { border-color:rgba(52,116,212,.22); box-shadow:0 15px 34px rgba(44,66,94,.08); transform:translateY(-1px); }
.chart-card>header { display:flex; align-items:center; gap:11px; margin-bottom:12px; }
.chart-card__icon { display:grid; width:34px; height:34px; flex:0 0 auto; place-items:center; border-radius:10px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.chart-card__icon--violet { background:#f0edff; color:#6855c7; }
.chart-card__icon--amber { background:var(--ncp-warning-soft); color:var(--ncp-warning-strong); }
.chart-card>header>div { display:grid; min-width:0; gap:2px; }
.chart-card h2 { margin:0; color:var(--ncp-text); font-size:1rem; }
.chart-card p { margin:0; overflow:hidden; color:var(--ncp-text-muted); font-size:.82rem; text-overflow:ellipsis; white-space:nowrap; }
.chart-card>.ncp-skeleton { display:block; width:100%; height:18px; margin:10px 0; }
.monitor-grid--loading .chart-card { min-height:300px; }
@keyframes monitor-spin { to { transform:rotate(360deg); } }
@media(max-width:920px) {
  .monitor-grid { grid-template-columns:1fr; }
}
@media(max-width:640px) {
  .temperature-panel__header { align-items:flex-start; flex-direction:column; gap:10px; padding:13px 15px; }
  .temperature-panel__status { align-self:flex-start; }
  .temperature-grid { padding:12px 15px 15px; }
  .chart-card { padding:16px; }
}
@media(prefers-reduced-motion: reduce) {
  .monitor-loading svg { animation:none; }
  .chart-card { transition:border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
  .chart-card:hover { transform:none; }
}
</style>
