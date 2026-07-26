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
}>(), {
  placeholder: '请选择',
  filterable: false,
})

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <ElSelect
    class="ncp-select"
    :model-value="modelValue"
    :placeholder="placeholder"
    :filterable="filterable"
    :aria-label="accessibleLabel"
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
.ncp-select{min-width:140px}
.ncp-select :deep(.el-select__wrapper){min-height:40px;border-radius:9px;background:#fff;box-shadow:0 0 0 1px var(--ncp-line) inset;transition:box-shadow var(--ncp-duration-fast),background-color var(--ncp-duration-fast)}
.ncp-select :deep(.el-select__wrapper:hover){box-shadow:0 0 0 1px var(--ncp-line-strong) inset}
.ncp-select :deep(.el-select__wrapper.is-focused){box-shadow:0 0 0 2px rgba(52,116,212,.2),0 0 0 1px var(--ncp-primary) inset}
.ncp-select :deep(.el-select__selected-item){color:var(--ncp-text-muted);font-size:.8rem;font-weight:650}
</style>
