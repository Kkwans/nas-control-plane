<script setup lang="ts">
import { ElOption, ElSelect } from 'element-plus'

export interface NcpSelectOption {
  label: string
  value: string
  disabled?: boolean
}

withDefaults(defineProps<{
  modelValue: string
  options: NcpSelectOption[]
  accessibleLabel: string
  placeholder?: string
  filterable?: boolean
  clearable?: boolean
  disabled?: boolean
  loading?: boolean
  size?: 'large' | 'default' | 'small'
  popperClass?: string
}>(), {
  placeholder: '请选择',
  filterable: false,
  clearable: false,
  disabled: false,
  loading: false,
  size: 'default',
  popperClass: '',
})

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <ElSelect
    class="ncp-select"
    :model-value="modelValue"
    :placeholder="placeholder"
    :filterable="filterable"
    :clearable="clearable"
    :disabled="disabled"
    :loading="loading"
    :size="size"
    :popper-class="['ncp-select-popper', popperClass].filter(Boolean).join(' ')"
    :aria-label="accessibleLabel"
    :aria-busy="loading || undefined"
    @update:model-value="emit('update:modelValue', String($event))"
  >
    <ElOption
      v-for="option in options"
      :key="option.value"
      :label="option.label"
      :value="option.value"
      :disabled="option.disabled"
    />
  </ElSelect>
</template>

<style scoped>
.ncp-select {
  min-width: 140px;
  max-width: 100%;
}

.ncp-select :deep(.el-select__wrapper) {
  min-height: var(--ncp-control-height);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-control-surface);
  box-shadow: 0 0 0 1px var(--ncp-control-border) inset;
  transition:
    background-color var(--ncp-duration-fast) var(--ncp-ease-out),
    box-shadow var(--ncp-duration-fast) var(--ncp-ease-out);
}

.ncp-select :deep(.el-select__wrapper:hover) {
  background: var(--ncp-control-hover);
  box-shadow: 0 0 0 1px var(--ncp-control-border-hover) inset;
}

.ncp-select :deep(.el-select__wrapper.is-focused) {
  background: var(--ncp-control-surface);
  box-shadow: 0 0 0 2px var(--ncp-focus-ring), 0 0 0 1px var(--ncp-control-border-focus) inset;
}

.ncp-select :deep(.el-select__wrapper.is-disabled) {
  background: var(--ncp-control-disabled);
  box-shadow: 0 0 0 1px var(--ncp-line) inset;
  cursor: not-allowed;
}

.ncp-select :deep(.el-select__selected-item),
.ncp-select :deep(.el-select__placeholder) {
  color: var(--ncp-text);
  font-size: .84rem;
  font-weight: 650;
}

.ncp-select :deep(.el-select__placeholder) {
  color: var(--ncp-text-subtle);
}

.ncp-select :deep(.el-select__caret),
.ncp-select :deep(.el-select__clear) {
  color: var(--ncp-text-subtle);
}

.ncp-select :deep(.el-select__input),
.ncp-select :deep(.el-select__input:focus) {
  outline: 0 !important;
  box-shadow: none !important;
}

.ncp-select :deep(.el-select__loading) {
  color: var(--ncp-primary);
}
</style>
