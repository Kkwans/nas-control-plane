<script setup lang="ts">
import { computed } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { AriaComponent, GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsOption } from 'echarts'
import { Activity } from '@lucide/vue'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent, AriaComponent])

export interface TrendSeries {
  name: string
  color: string
  values: number[]
}

const props = withDefaults(defineProps<{
  timestamps: string[]
  series: TrendSeries[]
  unit: string
  decimals?: number
  emptyMessage?: string
}>(), {
  decimals: 1,
  emptyMessage: '',
})

const reducedMotion = typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
const availableSeries = computed(() => props.series.filter((item) => item.values.length > 0))
const hasFiniteValue = computed(() => availableSeries.value.some((item) => item.values.some((value) => Number.isFinite(value))))
const enoughData = computed(() => props.timestamps.length >= 2 && hasFiniteValue.value)
const stateMessage = computed(() => {
  if (!props.timestamps.length) return props.emptyMessage || '当前范围暂无监控样本。'
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

function formatFullTimestamp(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return value || '时间未知'
  return `${String(date.getFullYear()).padStart(4, '0')}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function timestampSpan(values: string[]) {
  const timestamps = values
    .map((value) => new Date(value).valueOf())
    .filter((value) => Number.isFinite(value))
  if (timestamps.length < 2) return 0
  return Math.max(...timestamps) - Math.min(...timestamps)
}

function formatAxisLabels(values: string[]) {
  const dates = values.map((value) => new Date(value))
  const span = timestampSpan(values)
  const spansDays = dates.some((date, index) => {
    const previous = dates[index - 1]
    return Boolean(previous && !Number.isNaN(date.valueOf()) && !Number.isNaN(previous.valueOf()) && dateKey(date) !== dateKey(previous))
  })
  return dates.map((date, index) => {
    if (Number.isNaN(date.valueOf())) return values[index] ?? ''
    const time = `${pad(date.getHours())}:${pad(date.getMinutes())}`
    if (!spansDays) return span > 0 && span < 2 * 60 * 60 * 1000 ? `${time}:${pad(date.getSeconds())}` : time
    const previous = dates[index - 1]
    const isBoundary = !previous || Number.isNaN(previous.valueOf()) || dateKey(date) !== dateKey(previous)
    const isEndpoint = index === 0 || index === dates.length - 1
    return isBoundary || isEndpoint ? `${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${time}` : time
  })
}

function shouldShowAxisLabel(index: number, values: string[]) {
  if (index === 0 || index === values.length - 1) return true
  const date = new Date(values[index] ?? '')
  const previous = new Date(values[index - 1] ?? '')
  if (!Number.isNaN(date.valueOf()) && !Number.isNaN(previous.valueOf()) && dateKey(date) !== dateKey(previous)) return true
  const step = Math.max(Math.ceil((values.length - 1) / 5), 1)
  return index % step === 0
}

function formatValue(value: number) {
  if (!Number.isFinite(value)) return '暂无数据'
  const formatted = new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: props.decimals,
    maximumFractionDigits: props.decimals,
  }).format(value)
  return props.unit ? `${formatted} ${props.unit}` : formatted
}

function latestFiniteValue(values: number[]) {
  for (let index = values.length - 1; index >= 0; index -= 1) {
    const value = values[index]
    if (value !== undefined && Number.isFinite(value)) return value
  }
  return Number.NaN
}

type TooltipEntry = {
  dataIndex?: number
  marker?: string
  seriesName?: string
  value?: unknown
}

function tooltipEntries(value: unknown): TooltipEntry[] {
  const entries = Array.isArray(value) ? value : [value]
  return entries.filter((entry): entry is TooltipEntry => typeof entry === 'object' && entry !== null)
}

function tooltipNumber(value: unknown) {
  if (Array.isArray(value)) return Number(value[1])
  return Number(value)
}

function formatTooltip(value: unknown) {
  const entries = tooltipEntries(value)
  const dataIndex = entries.find((entry) => Number.isInteger(entry.dataIndex))?.dataIndex
  const timestamp = props.timestamps[dataIndex ?? 0]
  const title = formatFullTimestamp(timestamp ?? '')
  const lines = entries.map((entry) => `${entry.marker ?? ''}${entry.seriesName ?? '监控指标'}：${formatValue(tooltipNumber(entry.value))}`)
  return [title, ...lines].join('<br/>')
}

const legendRows = computed(() => {
  const names = availableSeries.value.map((item) => item.name)
  if (names.length <= 3) return [names]
  const midpoint = Math.ceil(names.length / 2)
  return [names.slice(0, midpoint), names.slice(midpoint)]
})

const option = computed<EChartsOption>(() => ({
  animation: !reducedMotion,
  animationDuration: 320,
  aria: {
    enabled: true,
    description: `${chartLabel.value}，包含 ${availableSeries.value.map((item) => item.name).join('、')}。`,
  },
  color: availableSeries.value.map((item) => item.color),
  grid: { top: legendRows.value.length > 1 ? 58 : 42, right: 12, bottom: 26, left: 45, containLabel: false },
  legend: legendRows.value.map((names, index) => ({
    data: names,
    top: 3 + index * 20,
    left: 0,
    right: 0,
    width: '100%',
    itemWidth: 16,
    itemHeight: 3,
    itemGap: 12,
    textStyle: { color: '#53627a', fontSize: 10, fontFamily: 'Manrope Variable' },
  })),
  tooltip: {
    trigger: 'axis',
    confine: true,
    backgroundColor: 'rgba(20, 33, 58, .94)',
    borderWidth: 0,
    padding: [9, 11],
    textStyle: { color: '#fff', fontSize: 11 },
    formatter: formatTooltip,
  },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    data: formatAxisLabels(props.timestamps),
    axisLine: { lineStyle: { color: '#dce4ee' } },
    axisTick: { show: false },
    axisLabel: {
      color: '#7b8aa1',
      fontSize: 10,
      hideOverlap: true,
      interval: (index: number) => shouldShowAxisLabel(index, props.timestamps),
    },
  },
  yAxis: {
    type: 'value',
    min: 0,
    axisLabel: {
      color: '#7b8aa1',
      fontSize: 10,
      formatter: (value: number) => `${value}${props.unit === '%' ? '%' : ''}`,
    },
    splitLine: { lineStyle: { color: '#e7edf4', type: 'dashed' } },
  },
  series: availableSeries.value.map((item) => ({
    name: item.name,
    type: 'line',
    data: item.values,
    showSymbol: false,
    symbolSize: 7,
    smooth: 0.24,
    connectNulls: false,
    lineStyle: { width: 2.2 },
    areaStyle: { opacity: .07 },
    emphasis: { focus: 'series', showSymbol: true },
  })),
}))
</script>

<template>
  <div class="trend-chart" role="group" :aria-label="chartLabel" :aria-busy="!enoughData">
    <VChart v-if="enoughData" class="trend-chart__canvas" :option="option" autoresize />
    <div v-else class="trend-chart__pending" role="status" aria-live="polite">
      <span><Activity :size="22" aria-hidden="true" /></span>
      <strong>{{ timestamps.length ? '趋势数据未就绪' : '当前范围暂无样本' }}</strong>
      <p>{{ stateMessage }}</p>
    </div>
    <ul class="sr-only">
      <li v-for="item in availableSeries" :key="item.name">
        {{ item.name }}当前值：{{ formatValue(latestFiniteValue(item.values)) }}
      </li>
    </ul>
  </div>
</template>

<style scoped>
.trend-chart { min-width: 0; height: 224px; }
.trend-chart__canvas { width: 100%; height: 100%; }
.trend-chart__pending { display: grid; height: 100%; place-items: center; align-content: center; gap: 5px; border: 1px dashed var(--ncp-line-strong); border-radius: 11px; background: var(--ncp-surface-quiet); text-align: center; }
.trend-chart__pending>span { display: grid; width: 42px; height: 42px; place-items: center; border-radius: 12px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.trend-chart__pending strong { font-size: .75rem; }
.trend-chart__pending p { max-width: 42rem; margin: 0; padding: 0 14px; color: var(--ncp-text-subtle); font-size: .64rem; }
@media(max-width: 600px) { .trend-chart { height: 210px; } }
</style>
