<script setup lang="ts">
import type { Component } from 'vue'

export type InfrastructureSignalTone = 'success' | 'warning' | 'neutral'

export interface InfrastructureSignal {
  label: string
  value: string
  detail: string
  icon: Component
  tone: InfrastructureSignalTone
}

defineProps<{
  signals: readonly InfrastructureSignal[]
}>()
</script>

<template>
  <section class="infrastructure-signals panel" aria-labelledby="infrastructure-signals-title">
    <header class="infrastructure-signals__header">
      <div>
        <span class="infrastructure-signals__eyebrow">运行状态</span>
        <h2 id="infrastructure-signals-title">网络与控制链路</h2>
      </div>
      <p>先看当前是否可用，再进入网络与代理查看证据和操作。</p>
    </header>

    <div class="infrastructure-signals__grid">
      <article
        v-for="signal in signals"
        :key="signal.label"
        class="infrastructure-signal"
        :class="`infrastructure-signal--${signal.tone}`"
      >
        <span class="infrastructure-signal__icon" aria-hidden="true">
          <component :is="signal.icon" :size="18" :stroke-width="1.9" />
        </span>
        <div class="infrastructure-signal__content">
          <span class="infrastructure-signal__label">{{ signal.label }}</span>
          <strong :title="signal.value">{{ signal.value }}</strong>
          <p :title="signal.detail">{{ signal.detail }}</p>
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.infrastructure-signals {
  display: grid;
  grid-column: 1 / -1;
  gap: 14px;
  padding: 18px;
}

.infrastructure-signals__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
}

.infrastructure-signals__eyebrow {
  color: var(--ncp-primary-strong);
  font-family: var(--ncp-font-mono);
  font-size: .68rem;
  font-weight: 750;
  letter-spacing: .08em;
  text-transform: uppercase;
}

.infrastructure-signals h2 {
  margin: 3px 0 0;
  color: var(--ncp-text);
  font-size: 1rem;
  letter-spacing: -.025em;
}

.infrastructure-signals__header p {
  max-width: 40ch;
  margin: 2px 0 0;
  color: var(--ncp-text-subtle);
  font-size: .76rem;
  line-height: 1.5;
  text-align: right;
}

.infrastructure-signals__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}

.infrastructure-signal {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: 10px;
  padding: 13px;
  border: 1px solid var(--ncp-line);
  border-radius: 12px;
  background: var(--ncp-surface-quiet);
}

.infrastructure-signal__icon {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
  border-radius: 10px;
  background: color-mix(in srgb, currentColor 8%, transparent);
}

.infrastructure-signal--success .infrastructure-signal__icon {
  color: var(--ncp-success-strong);
}

.infrastructure-signal--warning .infrastructure-signal__icon {
  color: var(--ncp-warning-strong);
}

.infrastructure-signal--neutral .infrastructure-signal__icon {
  color: var(--ncp-neutral-strong);
}

.infrastructure-signal__content {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.infrastructure-signal__label {
  overflow: hidden;
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.infrastructure-signal strong,
.infrastructure-signal p {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.infrastructure-signal strong {
  color: var(--ncp-text);
  font-size: .86rem;
}

.infrastructure-signal p {
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: .72rem;
}

@media (max-width: 1050px) {
  .infrastructure-signals__grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 520px) {
  .infrastructure-signals {
    padding: 15px;
  }

  .infrastructure-signals__header {
    flex-direction: column;
    gap: 5px;
  }

  .infrastructure-signals__header p {
    max-width: none;
    text-align: left;
  }

  .infrastructure-signals__grid {
    grid-template-columns: 1fr;
  }
}
</style>
