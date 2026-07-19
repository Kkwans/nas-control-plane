<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  points: number[]
  label: string
}>()

const chartPoints = computed(() => {
  if (props.points.length === 0) {
    return []
  }

  const minimum = Math.min(...props.points)
  const maximum = Math.max(...props.points)
  const range = maximum - minimum || 1
  const lastIndex = Math.max(props.points.length - 1, 1)

  return props.points.map((value, index) => ({
    x: 12 + (index / lastIndex) * 296,
    y: 100 - ((value - minimum) / range) * 76,
    value,
  }))
})

const polylinePoints = computed(() => chartPoints.value.map((point) => `${point.x},${point.y}`).join(' '))

const areaPath = computed(() => {
  const points = chartPoints.value
  if (points.length === 0) {
    return ''
  }

  return `M ${points[0]?.x} 108 L ${points.map((point) => `${point.x} ${point.y}`).join(' L ')} L ${points.at(-1)?.x} 108 Z`
})

const latestValue = computed(() => chartPoints.value.at(-1)?.value ?? 0)
</script>

<template>
  <div class="signal-chart">
    <svg viewBox="0 0 320 120" role="img" :aria-label="`${label}趋势图，当前 ${latestValue.toFixed(1)}%`">
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
        v-if="chartPoints.length"
        class="signal-chart__point"
        :cx="chartPoints.at(-1)?.x"
        :cy="chartPoints.at(-1)?.y"
        r="4"
      />
    </svg>
    <div class="signal-chart__axis" aria-hidden="true">
      <span>08:00</span>
      <span>10:00</span>
      <span>12:00</span>
    </div>
    <ul class="sr-only">
      <li v-for="(point, index) in points" :key="index">第 {{ index + 1 }} 个采样点：{{ point }}%</li>
    </ul>
  </div>
</template>

<style scoped>
.signal-chart {
  min-width: 0;
}

svg {
  display: block;
  width: 100%;
  overflow: visible;
}

.signal-chart__grid {
  stroke: var(--ncp-line);
  stroke-width: 1;
  stroke-dasharray: 2 6;
}

.signal-chart__area {
  fill: var(--ncp-primary-soft);
}

.signal-chart__line {
  stroke: var(--ncp-primary);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.4;
}

.signal-chart__point {
  fill: var(--ncp-surface-raised);
  stroke: var(--ncp-primary);
  stroke-width: 2.5;
}

.signal-chart__axis {
  display: flex;
  justify-content: space-between;
  margin: 4px 10px 0;
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.64rem;
}
</style>
