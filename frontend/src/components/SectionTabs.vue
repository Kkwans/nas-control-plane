<script setup lang="ts">
import type { Component } from 'vue'

export interface SectionTab {
  value: string
  label: string
  icon?: Component
  count?: string | number
  disabled?: boolean
}

const props = withDefaults(defineProps<{
  modelValue: string
  tabs: SectionTab[]
  accessibleLabel?: string
}>(), { accessibleLabel: '分区标签' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function select(tab: SectionTab) {
  if (!tab.disabled) emit('update:modelValue', tab.value)
}
function keydown(event: KeyboardEvent, index: number) {
  if (!['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const enabled = props.tabs.map((tab, itemIndex) => tab.disabled ? -1 : itemIndex).filter((itemIndex) => itemIndex >= 0)
  const position = enabled.indexOf(index)
  if (!enabled.length || position < 0) return
  const nextPosition = event.key === 'Home' ? 0 : event.key === 'End' ? enabled.length - 1 : (position + (event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1) + enabled.length) % enabled.length
  const next = props.tabs[enabled[nextPosition] ?? -1]
  if (!next) return
  event.preventDefault()
  ;(event.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLElement>('button:not(:disabled)')[nextPosition]?.focus()
  select(next)
}
</script>

<template>
  <nav class="section-tabs" role="group" :aria-label="accessibleLabel">
    <button v-for="(tab, index) in tabs" :key="tab.value" type="button" :aria-pressed="modelValue === tab.value" :tabindex="modelValue === tab.value ? 0 : -1" :disabled="tab.disabled" :class="{ active: modelValue === tab.value }" @click="select(tab)" @keydown="keydown($event, index)">
      <component :is="tab.icon" v-if="tab.icon" :size="16" aria-hidden="true" /><span>{{ tab.label }}</span><small v-if="tab.count !== undefined">{{ tab.count }}</small>
    </button>
  </nav>
</template>

<style scoped>
.section-tabs{display:flex;max-width:100%;align-items:center;gap:3px;overflow:auto;padding:3px;border:1px solid var(--ncp-line);border-radius:var(--ncp-radius-control);background:var(--ncp-surface-sunken);scrollbar-width:thin}.section-tabs button{display:inline-flex;min-width:max-content;min-height:40px;align-items:center;justify-content:center;gap:7px;padding:0 13px;border:1px solid transparent;border-radius:var(--ncp-radius-sm);background:transparent;color:var(--ncp-text-muted);font-size:.8rem;font-weight:700;white-space:nowrap}.section-tabs button:hover:not(:disabled){background:var(--ncp-control-hover);color:var(--ncp-text)}.section-tabs button.active{border-color:var(--ncp-line);background:var(--ncp-surface);color:var(--ncp-primary-strong);box-shadow:var(--ncp-shadow-control)}.section-tabs button:disabled{cursor:not-allowed;opacity:.45}.section-tabs small{color:var(--ncp-text-subtle);font-size:.7rem}
</style>
