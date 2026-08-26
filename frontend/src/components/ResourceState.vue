<script setup lang="ts">
import { CircleAlert, Inbox, LoaderCircle } from '@lucide/vue'

withDefaults(defineProps<{
  state: 'loading' | 'empty' | 'error'
  title: string
  message?: string
  nextStep?: string
  code?: string
  retryLabel?: string
}>(), {
  message: '',
  nextStep: '',
  code: '',
  retryLabel: '重试',
})

defineEmits<{ retry: [] }>()
</script>

<template>
  <section
    :class="['resource-state', `resource-state--${state}`]"
    :role="state === 'error' ? 'alert' : 'status'"
    :aria-live="state === 'error' ? 'assertive' : 'polite'"
  >
    <LoaderCircle v-if="state === 'loading'" class="resource-state__icon resource-state__icon--spin" :size="24" aria-hidden="true" />
    <CircleAlert v-else-if="state === 'error'" class="resource-state__icon" :size="24" aria-hidden="true" />
    <Inbox v-else class="resource-state__icon" :size="24" aria-hidden="true" />
    <strong>{{ title }}</strong>
    <p v-if="message">{{ message }}</p>
    <small v-if="nextStep">下一步：{{ nextStep }}</small>
    <code v-if="code">代码 {{ code }}</code>
    <button v-if="state === 'error'" type="button" @click="$emit('retry')">{{ retryLabel }}</button>
  </section>
</template>

<style scoped>
.resource-state {
  display: grid;
  min-height: 170px;
  place-items: center;
  align-content: center;
  gap: 8px;
  padding: 24px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-md);
  background: var(--ncp-surface);
  color: var(--ncp-text-subtle);
  text-align: center;
}

.resource-state__icon { color: var(--ncp-text-subtle); }
.resource-state--error {
  min-height: 74px;
  grid-template-columns: auto minmax(0, 1fr) auto;
  place-items: start;
  align-content: center;
  gap: 4px 10px;
  padding: 12px 14px;
  border-color: var(--ncp-danger-border);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
  text-align: left;
}
.resource-state--error .resource-state__icon { grid-row: span 3; margin-top: 1px; color: var(--ncp-danger-strong); }
.resource-state--error strong { align-self: end; font-size: .82rem; }
.resource-state p { margin: 0; color: inherit; font-size: .8rem; line-height: 1.45; }
.resource-state--error p { grid-column: 2; }
.resource-state small { color: inherit; font-size: .72rem; line-height: 1.4; }
.resource-state--error small { grid-column: 2; }
.resource-state code {
  padding: 2px 6px;
  border: 1px solid currentColor;
  border-radius: var(--ncp-radius-xs);
  font-family: var(--ncp-font-mono);
  font-size: .67rem;
  white-space: nowrap;
}
.resource-state--error code { grid-column: 2; }
.resource-state button {
  min-height: 34px;
  padding: 0 11px;
  border: 1px solid currentColor;
  border-radius: var(--ncp-radius-sm);
  background: var(--ncp-surface);
  color: inherit;
  font: inherit;
  font-size: .76rem;
  font-weight: 700;
  cursor: pointer;
}
.resource-state--error button { grid-column: 3; grid-row: 1 / span 3; }
.resource-state button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
.resource-state__icon--spin { animation: resource-state-spin .9s linear infinite; color: var(--ncp-primary); }
@keyframes resource-state-spin { to { transform: rotate(360deg); } }
@media (max-width: 560px) {
  .resource-state--error { grid-template-columns: auto minmax(0, 1fr); }
  .resource-state--error button { grid-column: 2; grid-row: auto; justify-self: start; margin-top: 4px; }
}
</style>
