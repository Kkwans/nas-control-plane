<script setup lang="ts">
import { computed } from 'vue'

type StatusTone = 'healthy' | 'degraded' | 'attention' | 'pending'

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
  align-items: center;
  gap: 7px;
  min-height: 27px;
  padding: 0 10px;
  border: 1px solid transparent;
  border-radius: var(--ncp-radius-pill);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.67rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  white-space: nowrap;
}

.status-pill__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--ncp-radius-pill);
  background: currentColor;
}

.status-pill--healthy {
  border-color: rgba(140, 226, 190, 0.22);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary);
}

.status-pill--degraded,
.status-pill--pending {
  border-color: rgba(241, 198, 117, 0.23);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning);
}

.status-pill--attention {
  border-color: rgba(239, 141, 137, 0.24);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger);
}
</style>
