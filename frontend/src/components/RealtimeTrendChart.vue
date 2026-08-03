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

function formatAxisLabels(values: string[]) {
  const dates = values.map((value) => new Date(value))
  const spansDays = dates.some((date, index) => {
    const previous = dates[index - 1]
    return Boolean(previous && date.toDateString() !== previous.toDateString())
  })
  return dates.map((date, index) => {
    if (Number.isNaN(date.valueOf())) return values[index] ?? ''
    const time = new Intl.DateTimeFormat('zh-CN', {
      hour: '2-digit',
      minute: '2-digit',
      second: spansDays ? undefined : '2-digit',
    }).format(date)
    if (!spansDays) return time
    const previous = dates[index - 1]
    const isBoundary = !previous || date.toDateString() !== previous.toDateString()
    return isBoundary ? `${String(date.getMonth() + 1).padStart(2, '0')}/${String(date.getDate()).padStart(2, '0')} ${time}` : time
  })
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

const option = computed<EChartsOption>(() => ({
  animation: !reducedMotion,
  animationDuration: 320,
  aria: {
    enabled: true,
    description: `${chartLabel.value}，包含 ${availableSeries.value.map((item) => item.name).join('、')}。`,
  },
  color: availableSeries.value.map((item) => item.color),
  grid: { top: 42, right: 12, bottom: 26, left: 45, containLabel: false },
  legend: {
    top: 4,
    left: 0,
    itemWidth: 16,
    itemHeight: 3,
    textStyle: { color: '#53627a', fontSize: 11, fontFamily: 'Manrope Variable' },
  },
  tooltip: {
    trigger: 'axis',
    confine: true,
    backgroundColor: 'rgba(20, 33, 58, .94)',
    borderWidth: 0,
    padding: [9, 11],
    textStyle: { color: '#fff', fontSize: 11 },
    valueFormatter: (value: unknown) => formatValue(Number(value)),
  },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    data: formatAxisLabels(props.timestamps),
    axisLine: { lineStyle: { color: '#dce4ee' } },
    axisTick: { show: false },
    axisLabel: { color: '#7b8aa1', fontSize: 10, interval: Math.max(Math.floor(props.timestamps.length / 5) - 1, 0) },
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
