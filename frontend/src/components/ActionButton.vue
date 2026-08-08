<script setup lang="ts">
import { computed } from 'vue'
import type { Component } from 'vue'
import { LoaderCircle } from '@lucide/vue'

export type ActionButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
export type ActionButtonSize = 'sm' | 'md' | 'lg'

const props = withDefaults(defineProps<{
  variant?: ActionButtonVariant
  size?: ActionButtonSize
  type?: 'button' | 'submit' | 'reset'
  icon?: Component
  disabled?: boolean
  loading?: boolean
  block?: boolean
  iconOnly?: boolean
  ariaLabel?: string
  ariaPressed?: boolean
}>(), {
  variant: 'secondary',
  size: 'md',
  type: 'button',
  icon: undefined,
  disabled: false,
  loading: false,
  block: false,
  iconOnly: false,
  ariaLabel: undefined,
  ariaPressed: undefined,
})

const buttonClass = computed(() => [
  `action-button--${props.variant}`,
  `action-button--${props.size}`,
  { 'action-button--block': props.block, 'action-button--icon-only': props.iconOnly },
])
</script>

<template>
  <button
    class="action-button"
    :class="buttonClass"
    :type="type"
    :disabled="disabled || loading"
    :aria-label="ariaLabel"
    :aria-pressed="ariaPressed"
    :aria-busy="loading || undefined"
  >
    <LoaderCircle v-if="loading" class="action-button__spinner" :size="17" aria-hidden="true" />
    <component :is="icon" v-else-if="icon" :size="17" :stroke-width="1.9" aria-hidden="true" />
    <span class="action-button__label"><slot /></span>
  </button>
</template>

<style scoped>
.action-button {
  display: inline-flex;
  min-width: max-content;
  align-items: center;
  justify-content: center;
  gap: var(--ncp-space-2);
  border: 1px solid transparent;
  border-radius: var(--ncp-radius-control);
  font-weight: 700;
  line-height: 1;
  white-space: nowrap;
  transition:
    color var(--ncp-duration-fast) var(--ncp-ease-out),
    background-color var(--ncp-duration-fast) var(--ncp-ease-out),
    border-color var(--ncp-duration-fast) var(--ncp-ease-out),
    box-shadow var(--ncp-duration-fast) var(--ncp-ease-out),
    transform var(--ncp-duration-fast) var(--ncp-ease-out);
}

.action-button--sm {
  height: 40px;
  min-height: 40px;
  padding: 0 var(--ncp-space-3);
  font-size: .78rem;
}

.action-button--md {
  height: 40px;
  min-height: 40px;
  padding: 0 var(--ncp-space-4);
  font-size: .84rem;
}

.action-button--lg {
  height: 44px;
  min-height: 44px;
  padding: 0 var(--ncp-space-5);
  font-size: .92rem;
}

.action-button--block {
  width: 100%;
}

.action-button--icon-only {
  width: 40px;
  min-width: 40px;
  height: 40px;
  min-height: 40px;
  padding: 0;
  gap: 0;
}

.action-button--icon-only .action-button__label {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

.action-button--primary {
  border-color: var(--ncp-primary);
  background: var(--ncp-primary);
  color: var(--ncp-on-primary);
  box-shadow: var(--ncp-shadow-control);
}

.action-button--primary:hover:not(:disabled) {
  border-color: var(--ncp-primary-strong);
  background: var(--ncp-primary-strong);
  box-shadow: var(--ncp-shadow-hover);
}

.action-button--primary:active:not(:disabled) {
  border-color: var(--ncp-primary-strong);
  background: var(--ncp-primary-strong);
}

.action-button--secondary {
  border-color: var(--ncp-control-border);
  background: var(--ncp-control-surface);
  color: var(--ncp-text-muted);
  box-shadow: var(--ncp-shadow-control);
}

.action-button--secondary:hover:not(:disabled) {
  border-color: var(--ncp-control-border-hover);
  background: var(--ncp-control-hover);
  color: var(--ncp-text);
  box-shadow: var(--ncp-shadow-hover);
}

.action-button--ghost {
  background: transparent;
  color: var(--ncp-primary-strong);
}

.action-button--ghost:hover:not(:disabled) {
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.action-button--danger {
  border-color: var(--ncp-danger-border);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.action-button--danger:hover:not(:disabled) {
  border-color: var(--ncp-danger);
  background: var(--ncp-danger);
  color: var(--ncp-on-primary);
  box-shadow: var(--ncp-shadow-control);
}

.action-button:active:not(:disabled) {
  transform: translateY(1px) scale(.99);
}

.action-button:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px var(--ncp-focus-ring);
}

.action-button:disabled {
  border-color: var(--ncp-line);
  background: var(--ncp-control-disabled);
  color: var(--ncp-text-disabled);
  box-shadow: none;
  opacity: .82;
}

.action-button__label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.action-button__spinner {
  animation: action-button-spin 760ms linear infinite;
}

@keyframes action-button-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 420px) {
  .action-button--lg {
    min-height: var(--ncp-touch-target);
  }
}

@media (prefers-reduced-motion: reduce) {
  .action-button,
  .action-button__spinner { animation: none; transition: none; }
}
</style>
