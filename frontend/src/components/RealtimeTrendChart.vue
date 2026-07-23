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
}>(), {
  decimals: 1,
})

const enoughData = computed(() => props.timestamps.length >= 2)
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value : new Intl.DateTimeFormat('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date)
}

function formatValue(value: number) {
  return `${new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: props.decimals,
    maximumFractionDigits: props.decimals,
  }).format(value)} ${props.unit}`
}

const option = computed<EChartsOption>(() => ({
  animation: !reducedMotion,
  animationDuration: 320,
  aria: {
    enabled: true,
    description: `实时趋势图，包含 ${props.series.map((item) => item.name).join('、')}。`,
  },
  color: props.series.map((item) => item.color),
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
    data: props.timestamps.map(formatTime),
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
  series: props.series.map((item) => ({
    name: item.name,
    type: 'line',
    data: item.values,
    showSymbol: false,
    symbolSize: 7,
    smooth: 0.24,
    lineStyle: { width: 2.2 },
    areaStyle: { opacity: .07 },
    emphasis: { focus: 'series', showSymbol: true },
  })),
}))
</script>

<template>
  <div class="trend-chart">
    <VChart v-if="enoughData" class="trend-chart__canvas" :option="option" autoresize />
    <div v-else class="trend-chart__pending">
      <span><Activity :size="22" /></span>
      <strong>正在积累实时样本</strong>
      <p>收到第二个快照后开始绘制趋势，当前 {{ timestamps.length }}/2。</p>
    </div>
    <ul class="sr-only">
      <li v-for="item in series" :key="item.name">
        {{ item.name }}当前值：{{ formatValue(item.values.at(-1) ?? 0) }}
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
.trend-chart__pending p { margin: 0; color: var(--ncp-text-subtle); font-size: .64rem; }
@media(max-width: 600px) { .trend-chart { height: 210px; } }
</style>
