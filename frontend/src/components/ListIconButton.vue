<script setup lang="ts">
import type { Component } from 'vue'
import { LoaderCircle } from '@lucide/vue'

withDefaults(defineProps<{
  icon: Component
  label: string
  tone?: 'default' | 'danger'
  disabled?: boolean
  loading?: boolean
}>(), {
  tone: 'default',
  disabled: false,
  loading: false,
})
</script>

<template>
  <button
    type="button"
    class="list-icon-button"
    :class="{ 'list-icon-button--danger': tone === 'danger' }"
    :disabled="disabled || loading"
    :aria-label="label"
    :aria-busy="loading || undefined"
  >
    <LoaderCircle v-if="loading" class="list-icon-button__spinner" :size="16" aria-hidden="true" />
    <component :is="icon" v-else :size="16" :stroke-width="1.9" aria-hidden="true" />
  </button>
</template>

<style scoped>
.list-icon-button {
  display: grid;
  width: 34px;
  min-width: 34px;
  height: 34px;
  min-height: 34px;
  place-items: center;
  padding: 0;
  border: 1px solid var(--ncp-line);
  border-radius: 8px;
  background: var(--ncp-surface);
  color: var(--ncp-primary-strong);
  box-shadow: none;
  transition:
    transform var(--ncp-duration-fast) var(--ncp-ease-out),
    border-color var(--ncp-duration-fast) var(--ncp-ease-out),
    background-color var(--ncp-duration-fast) var(--ncp-ease-out),
    color var(--ncp-duration-fast) var(--ncp-ease-out);
}

.list-icon-button:hover:not(:disabled),
.list-icon-button:focus-visible {
  border-color: var(--ncp-primary-border);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
  outline: none;
  transform: translateY(-1px);
}

.list-icon-button--danger {
  color: var(--ncp-danger-strong);
}

.list-icon-button--danger:hover:not(:disabled),
.list-icon-button--danger:focus-visible {
  border-color: var(--ncp-danger-border);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.list-icon-button:disabled {
  cursor: not-allowed;
  border-color: var(--ncp-line);
  background: var(--ncp-control-disabled);
  color: var(--ncp-text-disabled);
  opacity: .72;
  transform: none;
}

.list-icon-button__spinner {
  animation: list-icon-button-spin 760ms linear infinite;
}

@keyframes list-icon-button-spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .list-icon-button,
  .list-icon-button__spinner { animation: none; transition: none; }
}
</style>
