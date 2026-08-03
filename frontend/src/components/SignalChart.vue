<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  points: number[]
  label: string
  timestamps?: string[]
  unit?: string
  emptyMessage?: string
}>(), {
  timestamps: () => [],
  unit: '%',
  emptyMessage: '当前范围暂无可绘制的数据。',
})

const chartValues = computed(() => props.points
  .map((value, index) => ({ value, index }))
  .filter((point) => Number.isFinite(point.value)))
const chartPoints = computed(() => {
  if (chartValues.value.length === 0) return []

  const values = chartValues.value.map((point) => point.value)
  const minimum = Math.min(...values)
  const maximum = Math.max(...values)
  const range = maximum - minimum || 1
  const lastIndex = Math.max(props.points.length - 1, 1)

  return chartValues.value.map(({ value, index }) => ({
    x: 12 + (index / lastIndex) * 296,
    y: 100 - ((value - minimum) / range) * 76,
    value,
  }))
})

const polylinePoints = computed(() => chartPoints.value.map((point) => `${point.x},${point.y}`).join(' '))

const areaPath = computed(() => {
  const points = chartPoints.value
  if (points.length === 0) return ''

  return `M ${points[0]?.x} 108 L ${points.map((point) => `${point.x} ${point.y}`).join(' L ')} L ${points.at(-1)?.x} 108 Z`
})

const latestValue = computed(() => chartPoints.value.at(-1)?.value)
const chartLabel = computed(() => {
  if (latestValue.value === undefined) return `${props.label}趋势图，暂无数据`
  return `${props.label}趋势图，当前 ${latestValue.value.toFixed(1)}${props.unit}`
})

function formatAxisLabel(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return value
  return new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit' }).format(date)
}

const axisLabels = computed(() => {
  if (props.timestamps.length < 2) return []
  const indexes = [0, Math.floor((props.timestamps.length - 1) / 2), props.timestamps.length - 1]
  return [...new Set(indexes.map((index) => formatAxisLabel(props.timestamps[index] ?? '')))]
})
</script>

<template>
  <div class="signal-chart" role="group" :aria-label="`${label}趋势`">
    <svg v-if="chartPoints.length" viewBox="0 0 320 120" role="img" :aria-label="chartLabel" focusable="false">
      <line class="signal-chart__grid" x1="12" y1="24" x2="308" y2="24" />
      <line class="signal-chart__grid" x1="12" y1="62" x2="308" y2="62" />
      <line class="signal-chart__grid" x1="12" y1="100" x2="308" y2="100" />
      <path v-if="areaPath" class="signal-chart__area" :d="areaPath" />
      <polyline
        v-if="polylinePoints"
        class="signal-chart__line"
        :points="polylinePoints"
        fill="none"
      />
      <circle
        class="signal-chart__point"
        :cx="chartPoints.at(-1)?.x"
        :cy="chartPoints.at(-1)?.y"
        r="4"
      />
    </svg>
    <div v-else class="signal-chart__empty" role="status" aria-live="polite">{{ emptyMessage }}</div>
    <div v-if="axisLabels.length" class="signal-chart__axis" aria-hidden="true">
      <span v-for="value in axisLabels" :key="value">{{ value }}</span>
    </div>
    <ul v-if="chartPoints.length" class="sr-only">
      <li v-for="(point, index) in chartPoints" :key="index">第 {{ index + 1 }} 个采样点：{{ point.value.toFixed(1) }}{{ unit }}</li>
    </ul>
  </div>
</template>

<style scoped>
.signal-chart { min-width: 0; }
svg { display: block; width: 100%; overflow: visible; }
.signal-chart__grid { stroke: var(--ncp-line); stroke-width: 1; stroke-dasharray: 2 6; }
.signal-chart__area { fill: var(--ncp-primary-soft); }
.signal-chart__line { stroke: var(--ncp-primary); stroke-linecap: round; stroke-linejoin: round; stroke-width: 2.4; }
.signal-chart__point { fill: var(--ncp-surface-raised); stroke: var(--ncp-primary); stroke-width: 2.5; }
.signal-chart__empty { display:grid; min-height:120px; place-items:center; border:1px dashed var(--ncp-line-strong); border-radius:11px; background:var(--ncp-surface-quiet); color:var(--ncp-text-subtle); font-size:.74rem; text-align:center; }
.signal-chart__axis { display: flex; justify-content: space-between; margin: 4px 10px 0; color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.64rem; }
</style>
