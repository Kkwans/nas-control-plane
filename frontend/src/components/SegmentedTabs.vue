<script setup lang="ts">
import { computed } from 'vue'
import type { Component } from 'vue'

export interface SegmentedTab {
  label: string
  value: string
  disabled?: boolean
  count?: string | number
  icon?: Component
}

const props = withDefaults(defineProps<{
  modelValue: string
  tabs: SegmentedTab[]
  accessibleLabel?: string
  panelId?: string
  disabled?: boolean
}>(), {
  accessibleLabel: '选项卡',
  panelId: undefined,
  disabled: false,
})

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const activeValue = computed(() => {
  if (props.tabs.some((tab) => tab.value === props.modelValue)) return props.modelValue
  return props.tabs.find((tab) => !tab.disabled)?.value ?? ''
})

function isDisabled(tab: SegmentedTab) {
  return props.disabled || Boolean(tab.disabled)
}

function selectTab(tab: SegmentedTab) {
  if (isDisabled(tab)) return
  emit('update:modelValue', tab.value)
}

function onKeydown(event: KeyboardEvent, index: number) {
  const key = event.key
  if (!['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp', 'Home', 'End'].includes(key)) return

  const enabledIndexes = props.tabs
    .map((tab, tabIndex) => (isDisabled(tab) ? -1 : tabIndex))
    .filter((tabIndex) => tabIndex >= 0)
  if (!enabledIndexes.length) return

  const currentPosition = enabledIndexes.indexOf(index)
  const nextPosition = key === 'Home'
    ? 0
    : key === 'End'
      ? enabledIndexes.length - 1
      : (currentPosition + (key === 'ArrowLeft' || key === 'ArrowUp' ? -1 : 1) + enabledIndexes.length) % enabledIndexes.length
  const nextIndex = enabledIndexes[nextPosition]
  const nextTab = nextIndex === undefined ? undefined : props.tabs[nextIndex]
  if (!nextTab || nextIndex === undefined) return

  event.preventDefault()
  const buttons = Array.from(
    (event.currentTarget as HTMLButtonElement).closest('[role="tablist"]')?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [],
  )
  buttons[nextIndex]?.focus()
  selectTab(nextTab)
}
</script>

<template>
  <div class="segmented-tabs" role="tablist" :aria-label="accessibleLabel">
    <button
      v-for="(tab, index) in tabs"
      :key="tab.value"
      class="segmented-tabs__tab"
      :class="{ 'is-active': tab.value === activeValue }"
      type="button"
      role="tab"
      :aria-selected="tab.value === activeValue"
      :aria-controls="panelId || undefined"
      :tabindex="tab.value === activeValue ? 0 : -1"
      :disabled="isDisabled(tab)"
      @click="selectTab(tab)"
      @keydown="onKeydown($event, index)"
    >
      <component :is="tab.icon" v-if="tab.icon" :size="15" :stroke-width="1.9" aria-hidden="true" />
      <span class="segmented-tabs__label">{{ tab.label }}</span>
      <span v-if="tab.count !== undefined" class="segmented-tabs__count tabular-number">{{ tab.count }}</span>
    </button>
  </div>
</template>

<style scoped>
.segmented-tabs {
  display: flex;
  width: max-content;
  max-width: 100%;
  align-items: center;
  gap: 3px;
  overflow-x: auto;
  padding: 3px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-surface-sunken);
  scrollbar-width: thin;
}

.segmented-tabs__tab {
  display: inline-flex;
  min-width: max-content;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  gap: var(--ncp-space-2);
  padding: 0 var(--ncp-space-3);
  border: 1px solid transparent;
  border-radius: var(--ncp-radius-sm);
  background: transparent;
  color: var(--ncp-text-muted);
  font-size: .82rem;
  font-weight: 680;
  white-space: nowrap;
  transition:
    color var(--ncp-duration-fast) var(--ncp-ease-out),
    background-color var(--ncp-duration-fast) var(--ncp-ease-out),
    border-color var(--ncp-duration-fast) var(--ncp-ease-out),
    box-shadow var(--ncp-duration-fast) var(--ncp-ease-out),
    transform var(--ncp-duration-fast) var(--ncp-ease-out);
}

.segmented-tabs__tab:hover:not(:disabled) {
  background: var(--ncp-control-hover);
  color: var(--ncp-text);
}

.segmented-tabs__tab.is-active {
  border-color: var(--ncp-line);
  background: var(--ncp-surface);
  color: var(--ncp-primary-strong);
  box-shadow: var(--ncp-shadow-control);
}

.segmented-tabs__tab:active:not(:disabled) {
  transform: translateY(1px) scale(.99);
}

.segmented-tabs__tab:disabled {
  color: var(--ncp-text-disabled);
  cursor: not-allowed;
}

.segmented-tabs__label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.segmented-tabs__count {
  min-width: 1.25em;
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  text-align: center;
}

.segmented-tabs__tab.is-active .segmented-tabs__count {
  color: var(--ncp-primary-strong);
}

@media (max-width: 390px) {
  .segmented-tabs__tab {
    min-height: var(--ncp-touch-target);
    padding-inline: var(--ncp-space-3);
  }
}
</style>
