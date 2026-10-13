<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { Activity, Cpu, Gauge, HardDrive, MemoryStick, Thermometer } from '@lucide/vue'

import { requestMetricSamples, type MetricSample } from '@/api/control'
import { NcpApiError } from '@/api/system'
import DateTimeRangeControl from '@/components/DateTimeRangeControl.vue'
import MonitoringSensorSelect from '@/components/MonitoringSensorSelect.vue'
import RealtimeTrendChart, { type TrendSeries } from '@/components/RealtimeTrendChart.vue'
import ResourceState from '@/components/ResourceState.vue'
import ResourceAlertsPanel from '@/components/ResourceAlertsPanel.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import CompactSegmentedFilter from '@/components/CompactSegmentedFilter.vue'
import { useManualRefreshRegistry } from '@/composables/manualRefresh'
import { monitoringChartTokens } from '@/domain/monitoring/chartTokens'
import { buildHistoryCounterRates, buildHistoryTimeline, buildTemperatureIndex, historyValue, isHistoryCounter, projectHistoryGauge, projectTemperatureHistory } from '@/domain/monitoring/series'
import { isAbortError } from '@/session/sessionLifecycle'

type TimeRange = '1h' | '6h' | '24h' | '7d'
type HistoryRange = TimeRange | { from: string; to: string }
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
const appliedRange = ref<HistoryRange>('6h')
const appliedDuration = ref<number | null>(null)
const loadedRangeLabel = ref('')
const samples = shallowRef<MetricSample[]>([])
const status = ref<'loading' | 'error' | 'success'>('loading')
const loading = computed(() => status.value === 'loading')
const rangeError = ref('')
const loadError = ref({ message: '', code: '', nextStep: '' })
const manualRefreshRegistry = useManualRefreshRegistry()
let unregisterManualRefresh: (() => void) | undefined
let loadSequence = 0
let loadController: AbortController | null = null
const latest = computed(() => samples.value.at(-1))
const timeline = computed(() => buildHistoryTimeline(samples.value))
const timestamps = computed(() => timeline.value.valid ? timeline.value.timestamps : [])
const selectedSensors = ref<string[]>([])
let sensorSelectionInitialized = false
const temperatureIndex = computed(() => buildTemperatureIndex(samples.value))

const stats = computed<WorkspaceStat[]>(() => [
  { label: '采样数量', value: status.value === 'success' ? samples.value.length : '—' },
  { label: 'CPU', value: latest.value && historyValue(latest.value.cpuPercent) !== null ? `${latest.value.cpuPercent.toFixed(1)}%` : '—' },
  { label: '内存', value: latest.value && historyValue(latest.value.memoryPercent) !== null ? `${latest.value.memoryPercent.toFixed(1)}%` : '—' },
])

function gauge(key: 'cpuPercent' | 'load1' | 'memoryPercent' | 'diskPercent') {
  return timeline.value.valid ? projectHistoryGauge(timeline.value, samples.value, (sample) => sample[key]) : []
}
function rate(key: 'networkReceiveBytes' | 'networkTransmitBytes' | 'diskReadBytes' | 'diskWriteBytes', divisor: number) {
  return timeline.value.valid ? buildHistoryCounterRates(samples.value, timeline.value, key, divisor) : []
}
const cpuSeries = computed<TrendSeries[]>(() => [
  { name: 'CPU', color: monitoringChartTokens.cpu, values: gauge('cpuPercent'), unit: '%', axis: 'left' },
  { name: '负载', color: monitoringChartTokens.load, values: gauge('load1'), unit: '', decimals: 2, axis: 'right' },
])
const memorySeries = computed<TrendSeries[]>(() => [{ name: '内存', color: monitoringChartTokens.memory, values: gauge('memoryPercent'), unit: '%' }])
const storageSeries = computed<TrendSeries[]>(() => [{ name: '磁盘', color: monitoringChartTokens.storage, values: gauge('diskPercent'), unit: '%' }])
const networkSeries = computed<TrendSeries[]>(() => [
  { name: '接收', color: monitoringChartTokens.receive, values: rate('networkReceiveBytes', 1024), unit: 'KB/s', decimals: 1 },
  { name: '发送', color: monitoringChartTokens.transmit, values: rate('networkTransmitBytes', 1024), unit: 'KB/s', decimals: 1 },
])
const diskIOSeries = computed<TrendSeries[]>(() => [
  { name: '读取', color: monitoringChartTokens.storage, values: rate('diskReadBytes', 1024 * 1024), unit: 'MB/s', decimals: 2 },
  { name: '写入', color: monitoringChartTokens.load, values: rate('diskWriteBytes', 1024 * 1024), unit: 'MB/s', decimals: 2 },
])
const rateMessage = computed(() => samples.value.length === 1 ? '一条已保存样本，暂不能计算速率。' : '尚无可计算的相邻分钟样本。')
const diskIOMessage = computed(() => samples.value.some((sample) => isHistoryCounter(sample.diskReadBytes) || isHistoryCounter(sample.diskWriteBytes))
  ? rateMessage.value
  : '当前范围没有可用的磁盘 I/O 计数。')
const temperatureSeries = computed<TrendSeries[]>(() => {
  const currentTimeline = timeline.value
  if (!currentTimeline.valid) return []
  return selectedSensors.value.slice(0, 8).map((name, index) => ({
    name,
    color: monitoringChartTokens.temperature[index % monitoringChartTokens.temperature.length] ?? monitoringChartTokens.load,
    values: projectTemperatureHistory(currentTimeline, temperatureIndex.value, name),
  }))
})
const temperatureTrendMessage = computed(() => {
  if (!temperatureIndex.value.hasData) return '当前范围没有可用温度读数。'
  if (!selectedSensors.value.length) return '请选择传感器。'
  return '所选传感器在当前范围没有可用温度读数。'
})

function describeRange(value: HistoryRange) {
  if (typeof value === 'string') return quickRanges.find((item) => item.value === value)?.label ?? value
  const format = (timestamp: string) => new Date(timestamp).toLocaleString('zh-CN', { hour12: false })
  return `${format(value.from)} 至 ${format(value.to)}`
}
function describeLoadError(caught: unknown) {
  const code = caught instanceof NcpApiError ? caught.code : 'MONITORING_READ_FAILED'
  if (code === 'MONITORING_HISTORY_LIMIT_EXCEEDED') return { code, message: '所选范围的历史数据超过读取上限，请缩小时间范围后重试。', nextStep: '选择更短的时间范围。' }
  if (code === 'MONITORING_HISTORY_INVALID') return { code, message: '所选范围的历史包含无效数据，无法读取。', nextStep: '请检查服务端历史数据读取错误。' }
  if (code === 'MONITORING_RANGE_INVALID') return { code, message: '服务端无法读取此时间范围。', nextStep: '请选择不超过 7 天的有效时间范围。' }
  return { code, message: '监控历史读取失败，请稍后重试。', nextStep: '确认 Server 可用后重试。' }
}
async function load() {
  let preciseRange = appliedRange.value
  if (typeof preciseRange !== 'string' && appliedDuration.value !== null) {
    const to = new Date()
    preciseRange = { from: new Date(to.valueOf() - appliedDuration.value).toISOString(), to: to.toISOString() }
    appliedRange.value = preciseRange
  }
  const sequence = ++loadSequence
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  samples.value = []
  status.value = 'loading'
  loadError.value = { message: '', code: '', nextStep: '' }
  try {
    const result = await requestMetricSamples(preciseRange, controller.signal)
    if (sequence !== loadSequence || controller.signal.aborted) return
    samples.value = result
    if (!timeline.value.valid) throw new NcpApiError('MONITORING_HISTORY_INVALID', '历史时间戳无效。')
    const names = temperatureIndex.value.names
    if (!sensorSelectionInitialized && names.length) {
      selectedSensors.value = names.slice(0, 8)
      sensorSelectionInitialized = true
    } else {
      const available = new Set(names)
      selectedSensors.value = selectedSensors.value.filter((name) => available.has(name)).slice(0, 8)
    }
    loadedRangeLabel.value = describeRange(preciseRange)
    status.value = 'success'
  } catch (caught) {
    if (sequence !== loadSequence || controller.signal.aborted || isAbortError(caught)) return
    samples.value = []
    loadError.value = describeLoadError(caught)
    status.value = 'error'
  }
}

function selectQuickRange(value: TimeRange) {
  range.value = value
  customFrom.value = null
  customTo.value = null
  appliedRange.value = value
  appliedDuration.value = null
  rangeError.value = ''
  void load()
}
function applyCustomRange() {
  const from = customFrom.value?.valueOf()
  const to = customTo.value?.valueOf()
  const now = Date.now()
  if (from === undefined || to === undefined || !Number.isFinite(from) || !Number.isFinite(to) || from >= to || to > now || to - from > rangeMilliseconds['7d']) {
    rangeError.value = '请选择不超过 7 天、且结束时间不晚于当前时间的范围。'
    return
  }
  appliedRange.value = { from: new Date(from).toISOString(), to: new Date(to).toISOString() }
  appliedDuration.value = now - to <= 60_000 ? to - from : null
  rangeError.value = ''
  void load()
}
function clearCustomRange() { selectQuickRange(range.value) }
function useNow() {
  customTo.value = new Date()
  customFrom.value ??= new Date(customTo.value.valueOf() - rangeMilliseconds[range.value])
}
async function handleManualRefresh() {
  await load()
  if (status.value === 'error') throw new Error(loadError.value.message)
}

onMounted(() => {
  unregisterManualRefresh = manualRefreshRegistry?.register(handleManualRefresh)
  void load()
})
onBeforeUnmount(() => {
  unregisterManualRefresh?.()
  loadSequence += 1
  loadController?.abort()
})
</script>

<template>
  <div class="page workspace-page">
    <WorkspaceHeader title="系统监控" description="当前资源提示独立评估；历史图表每分钟记录首个有效样本，时间范围仅作用于历史" :icon="Gauge" :stats="stats">
      <template #filters>
        <div class="monitor-range-filter" role="group" aria-label="快速时间范围">
          <CompactSegmentedFilter :model-value="typeof appliedRange === 'string' ? appliedRange : ''" :options="quickRanges" accessible-label="快速时间范围" @update:model-value="selectQuickRange($event as TimeRange)" />
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

    <ResourceAlertsPanel />

    <p v-if="rangeError" class="monitor-error" role="alert">{{ rangeError }}</p>
    <p v-if="status === 'success'" class="monitor-history-note">已读取范围：{{ loadedRangeLabel }}。折线空白表示缺少可用样本；网络和磁盘为相邻分钟样本间的平均速率。</p>
    <ResourceState v-if="status === 'error'" state="error" title="监控历史加载失败" :message="loadError.message" :code="loadError.code" :next-step="loadError.nextStep" @retry="load" />
    <section v-else-if="loading" class="monitor-grid monitor-grid--loading" role="status" aria-live="polite" aria-label="正在加载监控图表">
      <article v-for="item in 6" :key="item" class="chart-card panel" aria-hidden="true"><i v-for="line in 8" :key="line" class="ncp-skeleton"></i></article>
    </section>
    <ResourceState v-else-if="!samples.length" state="empty" title="当前范围暂无样本" message="所选范围没有已保存的监控历史，请调整时间范围或稍后刷新。" />
    <section v-else class="monitor-grid" aria-label="监控趋势图表">
      <article class="chart-card panel" aria-labelledby="cpu-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><Cpu :size="18" /></span>
          <div><h2 id="cpu-chart-title">处理器与负载</h2><p>CPU 使用率与 1 分钟负载</p></div>
        </header>
        <RealtimeTrendChart history :timestamps="timestamps" :series="cpuSeries" />
      </article>
      <article class="chart-card panel" aria-labelledby="memory-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><MemoryStick :size="18" /></span>
          <div><h2 id="memory-chart-title">内存使用</h2><p>已用内存占总容量比例</p></div>
        </header>
        <RealtimeTrendChart history :timestamps="timestamps" :series="memorySeries" />
      </article>
      <article class="chart-card panel" aria-labelledby="storage-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><HardDrive :size="18" /></span>
          <div><h2 id="storage-chart-title">存储使用</h2><p>所有已采集挂载点的合计占用</p></div>
        </header>
        <RealtimeTrendChart history :timestamps="timestamps" :series="storageSeries" />
      </article>
      <article class="chart-card panel" aria-labelledby="network-chart-title">
        <header>
          <span class="chart-card__icon" aria-hidden="true"><Activity :size="18" /></span>
          <div><h2 id="network-chart-title">网络吞吐</h2><p>接收与发送速率</p></div>
        </header>
        <RealtimeTrendChart history :timestamps="timestamps" :series="networkSeries" :empty-message="rateMessage" />
      </article>
      <article class="chart-card panel" aria-labelledby="disk-io-chart-title">
        <header>
          <span class="chart-card__icon chart-card__icon--violet" aria-hidden="true"><HardDrive :size="18" /></span>
          <div><h2 id="disk-io-chart-title">磁盘 I/O</h2><p>宿主机磁盘读写速率</p></div>
        </header>
        <RealtimeTrendChart history :timestamps="timestamps" :series="diskIOSeries" :empty-message="diskIOMessage" />
      </article>
      <article class="chart-card panel" aria-labelledby="temperature-chart-title">
        <header>
          <span class="chart-card__icon chart-card__icon--amber" aria-hidden="true"><Thermometer :size="18" /></span>
          <div><h2 id="temperature-chart-title">温度趋势</h2><p>按传感器显示历史温度趋势</p></div>
        </header>
        <MonitoringSensorSelect v-if="temperatureIndex.names.length" v-model="selectedSensors" :names="temperatureIndex.names" />
        <RealtimeTrendChart history :timestamps="timestamps" :series="temperatureSeries" unit="°C" :empty-message="temperatureTrendMessage" />
      </article>
    </section>
  </div>
</template>

<style scoped>
.monitor-range-filter { display:flex; min-width:0; align-items:center; }
.monitor-range-filter :deep(.el-segmented) { max-width:100%; }
.monitor-error { margin-bottom:14px; padding:12px 14px; border:1px solid color-mix(in srgb, var(--ncp-danger) 20%, transparent); border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.86rem; }
.monitor-history-note { margin:0 0 14px; color:var(--ncp-text-subtle); font-size:.76rem; line-height:1.6; }
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
@media(max-width:1100px) {
  .monitor-grid { grid-template-columns:repeat(2,minmax(0,1fr)); }
}
@media(max-width:640px) {
  .monitor-grid { grid-template-columns:1fr; }
  .chart-card { padding:16px; }
}
@media(prefers-reduced-motion: reduce) {
  .chart-card { transition:border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
  .chart-card:hover { transform:none; }
}
</style>
