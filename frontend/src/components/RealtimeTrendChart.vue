<script setup lang="ts">
import { computed, ref } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { AriaComponent, GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsOption } from 'echarts'
import { Activity } from '@lucide/vue'

import { useReducedMotion } from '@/composables/useReducedMotion'
import { monitoringChartTokens } from '@/domain/monitoring/chartTokens'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent, AriaComponent])

export interface TrendSeries {
  name: string
  color: string
  values: (number | null)[]
  unit?: string
  decimals?: number
  axis?: 'left' | 'right'
}

const props = withDefaults(defineProps<{
  timestamps: (string | number)[]
  series: TrendSeries[]
  history?: boolean
  unit?: string
  decimals?: number
  emptyMessage?: string
}>(), {
  decimals: 1,
  emptyMessage: '',
  history: false,
})

const reducedMotion = useReducedMotion()
const availableSeries = computed(() => props.series.filter((item) => item.values.length > 0))
const hasRightSeries = computed(() => availableSeries.value.some((item) => item.axis === 'right'))
const leftAxisUnit = computed(() => availableSeries.value.find((item) => item.axis !== 'right')?.unit ?? props.unit ?? '')
const rightAxisUnit = computed(() => availableSeries.value.find((item) => item.axis === 'right')?.unit ?? '')
const rightAxisLabel = computed(() => rightAxisUnit.value || availableSeries.value.find((item) => item.axis === 'right')?.name || '数值')
const hiddenSeries = ref(new Set<string>())
function toggleSeries(name: string) {
  // 切换时仅保留当前图表的状态，避免反复更换传感器累积历史名称。
  const next = new Set(availableSeries.value.filter((item) => hiddenSeries.value.has(item.name)).map((item) => item.name))
  if (next.has(name)) next.delete(name)
  else next.add(name)
  hiddenSeries.value = next
}
const hasFiniteValue = computed(() => availableSeries.value.some((item) => item.values.some((value) => Number.isFinite(value))))
const enoughData = computed(() => props.timestamps.length >= (props.history ? 1 : 2) && hasFiniteValue.value)
const stateMessage = computed(() => {
  if (!props.timestamps.length) return props.emptyMessage || '当前范围暂无监控样本。'
  if (props.history && props.timestamps.length === 1) return props.emptyMessage || '一条已保存样本，暂不能计算速率。'
  if (!availableSeries.value.length || !hasFiniteValue.value) return props.emptyMessage || '当前范围暂无可用指标数据。'
  return '正在积累实时样本，收到第二个有效快照后开始绘制趋势。'
})
const chartLabel = computed(() => {
  const seriesLabel = availableSeries.value.map((item) => item.name).join('、') || '监控指标'
  return `${seriesLabel}趋势图`
})

function dateKey(date: Date) {
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`
}

function pad(value: number) {
  return String(value).padStart(2, '0')
}

function formatFullTimestamp(value: string | number) {
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return String(value || '时间未知')
  return `${String(date.getFullYear()).padStart(4, '0')}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function timestampSpan(values: (string | number)[]) {
  const timestamps = values
    .map((value) => new Date(value).valueOf())
    .filter((value) => Number.isFinite(value))
  if (timestamps.length < 2) return 0
  return Math.max(...timestamps) - Math.min(...timestamps)
}

function formatAxisLabels(values: (string | number)[]) {
  const dates = values.map((value) => new Date(value))
  const span = timestampSpan(values)
  const spansDays = dates.some((date, index) => {
    const previous = dates[index - 1]
    return Boolean(previous && !Number.isNaN(date.valueOf()) && !Number.isNaN(previous.valueOf()) && dateKey(date) !== dateKey(previous))
  })
  return dates.map((date, index) => {
    if (Number.isNaN(date.valueOf())) return String(values[index] ?? '')
    const time = `${pad(date.getHours())}:${pad(date.getMinutes())}`
    if (!spansDays) return span > 0 && span < 2 * 60 * 60 * 1000 ? `${time}:${pad(date.getSeconds())}` : time
    const previous = dates[index - 1]
    const isBoundary = !previous || Number.isNaN(previous.valueOf()) || dateKey(date) !== dateKey(previous)
    const isEndpoint = index === 0 || index === dates.length - 1
    return isBoundary || isEndpoint ? `${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${time}` : time
  })
}

function shouldShowAxisLabel(index: number, values: (string | number)[]) {
  if (index === 0 || index === values.length - 1) return true
  const date = new Date(values[index] ?? '')
  const previous = new Date(values[index - 1] ?? '')
  if (!Number.isNaN(date.valueOf()) && !Number.isNaN(previous.valueOf()) && dateKey(date) !== dateKey(previous)) return true
  const step = Math.max(Math.ceil((values.length - 1) / 5), 1)
  return index % step === 0
}

const historyTimestamps = computed(() => props.timestamps.map((value) => new Date(value).valueOf()))
const historyRange = computed(() => {
  let min = Number.POSITIVE_INFINITY
  let max = Number.NEGATIVE_INFINITY
  for (const value of historyTimestamps.value) {
    if (!Number.isFinite(value)) continue
    min = Math.min(min, value)
    max = Math.max(max, value)
  }
  if (!Number.isFinite(min)) return {}
  return min === max ? { min: min - 30_000, max: max + 30_000 } : { min, max }
})
const historySpansDays = computed(() => {
  const first = historyTimestamps.value[0]
  const last = historyTimestamps.value.at(-1)
  return first !== undefined && last !== undefined && dateKey(new Date(first)) !== dateKey(new Date(last))
})

function formatHistoryTick(value: number) {
  const date = new Date(value)
  const minuteLabel = `${pad(date.getHours())}:${pad(date.getMinutes())}`
  const time = historyRange.value.min !== undefined && historyRange.value.max !== undefined && historyRange.value.max - historyRange.value.min < 2 * 60_000
    ? `${minuteLabel}:${pad(date.getSeconds())}`
    : minuteLabel
  return historySpansDays.value ? `${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${time}` : time
}

function finiteValue(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function formatSeriesValue(value: number | null | undefined, series?: TrendSeries) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '暂无数据'
  const formatted = new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: series?.decimals ?? props.decimals,
    maximumFractionDigits: series?.decimals ?? props.decimals,
  }).format(value)
  const unit = series?.unit ?? props.unit
  return unit ? `${formatted} ${unit}` : formatted
}

type TooltipEntry = {
  dataIndex?: number
  axisValue?: string | number
  marker?: string
  seriesName?: string
  value?: unknown
}

function tooltipEntries(value: unknown): TooltipEntry[] {
  const entries = Array.isArray(value) ? value : [value]
  return entries.filter((entry): entry is TooltipEntry => typeof entry === 'object' && entry !== null)
}

function tooltipNumber(value: unknown) {
  const reading: unknown = Array.isArray(value) ? value[1] : value
  return typeof reading === 'number' ? reading : Number.NaN
}

function escapeHtml(value: string) {
  return value.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[character]!)
}

function tooltipTimestamp(entries: TooltipEntry[]) {
  if (props.history) {
    for (const entry of entries) {
      const value: unknown = Array.isArray(entry.value) ? entry.value[0] : entry.axisValue
      if ((typeof value === 'number' || typeof value === 'string') && Number.isFinite(new Date(value).valueOf())) return value
    }
  }
  const dataIndex = entries.find((entry) => Number.isInteger(entry.dataIndex))?.dataIndex
  return props.timestamps[dataIndex ?? 0] ?? ''
}

function formatTooltip(value: unknown) {
  const entries = tooltipEntries(value)
  const title = escapeHtml(formatFullTimestamp(tooltipTimestamp(entries)))
  const lines = entries.map((entry) => {
    const series = availableSeries.value.find((item) => item.name === entry.seriesName)
    return `${entry.marker ?? ''}${escapeHtml(entry.seriesName ?? '监控指标')}：${escapeHtml(formatSeriesValue(tooltipNumber(entry.value), series))}`
  })
  return [title, ...lines].join('<br/>')
}

const option = computed<EChartsOption>(() => ({
  animation: !reducedMotion.value,
  animationDuration: 320,
  aria: {
    enabled: true,
    description: `${chartLabel.value}，包含 ${availableSeries.value.map((item) => item.name).join('、')}。`,
  },
  color: availableSeries.value.map((item) => item.color),
  // 图例、轴单位由独立 DOM 行承载；canvas 只为刻度和绘图区留白。
  grid: { top: 10, right: hasRightSeries.value ? 42 : 12, bottom: 26, left: 45, containLabel: false },
  legend: {
    show: false,
    data: availableSeries.value.map((item) => item.name),
    selected: Object.fromEntries(availableSeries.value.map((item) => [item.name, !hiddenSeries.value.has(item.name)])),
  },
  tooltip: {
    trigger: 'axis',
    confine: true,
    backgroundColor: monitoringChartTokens.tooltip,
    borderWidth: 0,
    padding: [9, 11],
    textStyle: { color: monitoringChartTokens.tooltipText, fontSize: 11 },
    formatter: formatTooltip,
    extraCssText: 'max-width: min(360px, calc(100% - 16px)); white-space: normal; overflow-wrap: anywhere;',
  },
  xAxis: props.history ? {
    type: 'time',
    ...historyRange.value,
    axisLine: { lineStyle: { color: monitoringChartTokens.line } },
    axisTick: { show: false },
    axisLabel: {
      color: monitoringChartTokens.axis,
      fontSize: 10,
      hideOverlap: true,
      formatter: formatHistoryTick,
    },
  } : {
    type: 'category',
    boundaryGap: false,
    data: formatAxisLabels(props.timestamps),
    axisLine: { lineStyle: { color: monitoringChartTokens.line } },
    axisTick: { show: false },
    axisLabel: {
      color: monitoringChartTokens.axis,
      fontSize: 10,
      hideOverlap: true,
      interval: (index: number) => shouldShowAxisLabel(index, props.timestamps),
    },
  },
  yAxis: [
    {
      type: 'value', min: props.history ? (range) => Math.min(0, range.min) : 0, name: '',
      axisLabel: { color: monitoringChartTokens.axis, fontSize: 10, formatter: (value: number) => `${value}${leftAxisUnit.value === '%' ? '%' : ''}` },
      splitLine: { lineStyle: { color: monitoringChartTokens.grid, type: 'dashed' } },
    },
    {
      type: 'value', min: props.history ? (range) => Math.min(0, range.min) : 0, show: hasRightSeries.value, name: '',
      axisLabel: { color: monitoringChartTokens.axis, fontSize: 10, formatter: (value: number) => `${value}${rightAxisUnit.value === '%' ? '%' : ''}` },
      splitLine: { show: false },
    },
  ],
  series: availableSeries.value.map((item) => ({
    name: item.name,
    type: 'line',
    data: props.history
      ? historyTimestamps.value.map((timestamp, index) => [timestamp, finiteValue(item.values[index])])
      : item.values.map(finiteValue),
    showSymbol: props.history,
    symbol: props.history ? (_value, params) => {
      const index = params.dataIndex
      if (finiteValue(item.values[index]) === null) return 'none'
      return finiteValue(item.values[index - 1]) === null && finiteValue(item.values[index + 1]) === null ? 'circle' : 'none'
    } : undefined,
    symbolSize: props.history ? 4 : 7,
    smooth: props.history ? false : 0.24,
    connectNulls: false,
    lineStyle: { width: 2.2 },
    areaStyle: { opacity: .07 },
    emphasis: { focus: 'series', showSymbol: true },
    yAxisIndex: item.axis === 'right' ? 1 : 0,
  })),
}))
</script>

<template>
  <div class="trend-chart" role="group" :aria-label="chartLabel" :aria-busy="!history && !enoughData">
    <template v-if="enoughData">
      <div class="trend-chart__legend" role="group" aria-label="图例，点击切换曲线">
        <button
          v-for="item in availableSeries"
          :key="item.name"
          type="button"
          class="trend-chart__legend-item"
          :title="item.name"
          :aria-pressed="!hiddenSeries.has(item.name)"
          @click="toggleSeries(item.name)"
        >
          <span class="trend-chart__legend-swatch" :style="{ backgroundColor: item.color }" aria-hidden="true"></span>
          <span class="trend-chart__legend-name">{{ item.name }}</span>
        </button>
      </div>
      <div class="trend-chart__axis-units" :class="{ 'trend-chart__axis-units--dual': hasRightSeries }" aria-label="纵轴单位">
        <span>{{ hasRightSeries ? '左轴 · ' : '' }}{{ leftAxisUnit || '数值' }}</span>
        <span v-if="hasRightSeries">右轴 · {{ rightAxisLabel }}</span>
      </div>
      <VChart class="trend-chart__canvas" :option="option" autoresize />
    </template>
    <div v-else class="trend-chart__pending" role="status" aria-live="polite">
      <span><Activity :size="22" aria-hidden="true" /></span>
      <strong>{{ timestamps.length ? '趋势数据未就绪' : '当前范围暂无样本' }}</strong>
      <p>{{ stateMessage }}</p>
    </div>
    <ul class="sr-only">
      <li v-for="item in availableSeries" :key="item.name">
        {{ item.name }}最新样本：{{ formatSeriesValue(item.values.at(-1) ?? Number.NaN, item) }}
      </li>
    </ul>
  </div>
</template>

<style scoped>
.trend-chart { min-width: 0; min-height: 224px; }
.trend-chart__legend { display: flex; flex-wrap: wrap; gap: 4px 12px; min-width: 0; padding-bottom: 6px; }
.trend-chart__legend-item { display: inline-flex; align-items: center; gap: 6px; min-width: 0; max-width: 100%; min-height: 24px; padding: 2px 0; border-radius: 3px; background: transparent; color: var(--ncp-text-muted); font-size: 11px; text-align: left; }
.trend-chart__legend-item[aria-pressed='false'] { opacity: .5; }
.trend-chart__legend-item[aria-pressed='false'] .trend-chart__legend-name { text-decoration: line-through; }
.trend-chart__legend-swatch { width: 16px; height: 3px; flex: 0 0 auto; border-radius: 2px; }
.trend-chart__legend-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.trend-chart__axis-units { display: flex; justify-content: space-between; gap: 12px; min-width: 0; min-height: 18px; padding: 0 12px 0 45px; color: var(--ncp-text-subtle); font-size: 10px; line-height: 18px; }
.trend-chart__axis-units--dual { padding-right: 0; }
.trend-chart__axis-units span { min-width: 0; overflow-wrap: anywhere; }
.trend-chart__axis-units span:last-child:not(:first-child) { text-align: right; }
.trend-chart__canvas { width: 100%; height: 184px; }
.trend-chart__pending { display: grid; height: 224px; place-items: center; align-content: center; gap: 5px; border: 1px dashed var(--ncp-line-strong); border-radius: 11px; background: var(--ncp-surface-quiet); text-align: center; }
.trend-chart__pending>span { display: grid; width: 42px; height: 42px; place-items: center; border-radius: 12px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.trend-chart__pending strong { font-size: .75rem; }
.trend-chart__pending p { max-width: 42rem; margin: 0; padding: 0 14px; color: var(--ncp-text-subtle); font-size: .64rem; }
@media(max-width: 600px) { .trend-chart { min-height: 210px; } .trend-chart__canvas { height: 170px; } .trend-chart__pending { height: 210px; } }
</style>
