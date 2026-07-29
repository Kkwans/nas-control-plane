<script setup lang="ts">
import { computed } from 'vue'

export type StatusTone = 'healthy' | 'info' | 'degraded' | 'attention' | 'pending' | 'neutral'

const props = defineProps<{
  label: string
  tone: StatusTone
}>()

const toneClass = computed(() => `status-pill--${props.tone}`)
</script>

<template>
  <span class="status-pill" :class="toneClass" role="status">
    <span class="status-pill__dot" aria-hidden="true"></span>
    {{ label }}
  </span>
</template>

<style scoped>
.status-pill {
  display: inline-flex;
  width: max-content;
  max-width: 100%;
  flex: 0 0 auto;
  justify-self: center;
  align-items: center;
  gap: 7px;
  min-height: 27px;
  padding: 0 10px;
  border: 1px solid transparent;
  border-radius: var(--ncp-radius-pill);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.74rem;
  font-weight: 720;
  letter-spacing: 0.01em;
  white-space: nowrap;
}

.status-pill__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--ncp-radius-pill);
  background: currentColor;
}

.status-pill--healthy {
  border-color: color-mix(in srgb, var(--ncp-success) 22%, transparent);
  background: var(--ncp-success-soft);
  color: var(--ncp-success-strong);
}

.status-pill--info {
  border-color: color-mix(in srgb, var(--ncp-info) 24%, transparent);
  background: var(--ncp-info-soft);
  color: var(--ncp-info-strong);
}

.status-pill--degraded,
.status-pill--pending {
  border-color: color-mix(in srgb, var(--ncp-warning) 24%, transparent);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
}

.status-pill--attention {
  border-color: color-mix(in srgb, var(--ncp-danger) 24%, transparent);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.status-pill--neutral {
  border-color: color-mix(in srgb, var(--ncp-neutral) 22%, transparent);
  background: var(--ncp-neutral-soft);
  color: var(--ncp-neutral-strong);
}

:global(html[data-density='compact']) .status-pill {
  min-height: 24px;
  padding-inline: 8px;
  font-size: .71rem;
}
</style>
