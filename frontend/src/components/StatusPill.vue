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
    <span class="status-pill__label">{{ label }}</span>
  </span>
</template>

<style scoped>
.status-pill {
  display: inline-flex;
  width: max-content;
  max-width: 100%;
  flex: 0 0 auto;
  align-items: center;
  gap: var(--ncp-space-2);
  min-height: 28px;
  padding: 0 var(--ncp-space-3);
  border: 1px solid transparent;
  border-radius: var(--ncp-radius-pill);
  font-family: var(--ncp-font-mono);
  font-size: .73rem;
  font-weight: 720;
  letter-spacing: .01em;
  line-height: 1.2;
  white-space: nowrap;
}

.status-pill__dot {
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: var(--ncp-radius-pill);
  background: currentColor;
}

.status-pill__label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.status-pill--healthy {
  border-color: var(--ncp-success-border);
  background: var(--ncp-success-soft);
  color: var(--ncp-success-strong);
}

.status-pill--info {
  border-color: var(--ncp-info-border);
  background: var(--ncp-info-soft);
  color: var(--ncp-info-strong);
}

.status-pill--degraded,
.status-pill--pending {
  border-color: var(--ncp-warning-border);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
}

.status-pill--attention {
  border-color: var(--ncp-danger-border);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.status-pill--neutral {
  border-color: var(--ncp-neutral-border);
  background: var(--ncp-neutral-soft);
  color: var(--ncp-neutral-strong);
}

:global(html[data-density='compact']) .status-pill {
  min-height: 24px;
  padding-inline: var(--ncp-space-2);
  font-size: .7rem;
}
</style>
