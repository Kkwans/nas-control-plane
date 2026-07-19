<script setup lang="ts">
import type { Component } from 'vue'

type MetricAccent = 'primary' | 'info' | 'warning'

defineProps<{
  label: string
  value: string
  unit: string
  note: string
  trend?: string
  accent: MetricAccent
  icon: Component
}>()
</script>

<template>
  <article class="metric-card" :class="`metric-card--${accent}`">
    <div class="metric-card__header">
      <span>{{ label }}</span>
      <span class="metric-card__icon" aria-hidden="true">
        <component :is="icon" :size="18" :stroke-width="1.8" />
      </span>
    </div>
    <div class="metric-card__value">
      <strong>{{ value }}</strong>
      <span>{{ unit }}</span>
    </div>
    <div class="metric-card__footer">
      <span>{{ note }}</span>
      <span v-if="trend" class="metric-card__trend">{{ trend }}</span>
    </div>
  </article>
</template>

<style scoped>
.metric-card {
  min-height: 172px;
  padding: 18px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-md);
  background: rgba(18, 28, 29, 0.84);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.13);
  transition:
    border-color var(--ncp-duration-base) var(--ncp-ease-out),
    background-color var(--ncp-duration-base) var(--ncp-ease-out),
    transform var(--ncp-duration-base) var(--ncp-ease-out),
    box-shadow var(--ncp-duration-base) var(--ncp-ease-out);
}

.metric-card:hover {
  border-color: var(--ncp-line-strong);
  background: var(--ncp-surface-hover);
  box-shadow: var(--ncp-shadow-panel);
  transform: translateY(-3px);
}

.metric-card__header,
.metric-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--ncp-space-3);
}

.metric-card__header > span:first-child {
  color: var(--ncp-text-muted);
  font-size: 0.78rem;
  font-weight: 700;
}

.metric-card__icon {
  display: grid;
  width: 34px;
  height: 34px;
  place-items: center;
  border: 1px solid currentColor;
  border-radius: 9px;
}

.metric-card__value {
  display: flex;
  align-items: baseline;
  gap: 7px;
  margin-top: 26px;
}

.metric-card__value strong {
  color: var(--ncp-text);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: clamp(1.8rem, 2.7vw, 2.32rem);
  font-weight: 650;
  letter-spacing: -0.08em;
  line-height: 1;
}

.metric-card__value span {
  color: var(--ncp-text-subtle);
  font-size: 0.74rem;
  font-weight: 700;
}

.metric-card__footer {
  margin-top: 22px;
  color: var(--ncp-text-subtle);
  font-size: 0.7rem;
}

.metric-card__trend {
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-weight: 700;
  white-space: nowrap;
}

.metric-card--primary .metric-card__icon,
.metric-card--primary .metric-card__trend {
  color: var(--ncp-primary);
}

.metric-card--info .metric-card__icon,
.metric-card--info .metric-card__trend {
  color: var(--ncp-info);
}

.metric-card--warning .metric-card__icon,
.metric-card--warning .metric-card__trend {
  color: var(--ncp-warning);
}
</style>
