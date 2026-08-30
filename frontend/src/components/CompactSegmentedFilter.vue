<script setup lang="ts">
export interface FilterOption { value: string; label: string; disabled?: boolean }
withDefaults(defineProps<{ modelValue: string; options: FilterOption[]; accessibleLabel?: string }>(), { accessibleLabel: '筛选' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>
<template>
  <div class="compact-filter" role="group" :aria-label="accessibleLabel">
    <button v-for="option in options" :key="option.value" type="button" :disabled="option.disabled" :aria-pressed="modelValue === option.value" :class="{ active: modelValue === option.value }" @click="emit('update:modelValue', option.value)">{{ option.label }}</button>
  </div>
</template>
<style scoped>
.compact-filter{display:flex;max-width:100%;align-items:center;gap:3px;padding:3px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet);overflow:auto}.compact-filter button{min-height:36px;padding:0 12px;border-radius:7px;background:transparent;color:var(--ncp-text-muted);font-size:.78rem;font-weight:700;white-space:nowrap}.compact-filter button.active{background:var(--ncp-surface);box-shadow:var(--ncp-shadow-control);color:var(--ncp-primary-strong)}.compact-filter button:disabled{cursor:not-allowed;opacity:.45}
</style>
