<script setup lang="ts">
import { computed } from 'vue'
import { ElInput } from 'element-plus'

import type { DatabaseColumn } from '@/api/database'
import { databaseEditorKind } from '@/domain/database/valueConversion'
import NcpSelect, { type NcpSelectOption } from './NcpSelect.vue'

const NULL_VALUE = '__ncp_database_null__'

const props = withDefaults(defineProps<{
  modelValue: string
  column: DatabaseColumn
  nullSelected?: boolean
  disabled?: boolean
  sensitive?: boolean
}>(), {
  nullSelected: false,
  disabled: false,
  sensitive: false,
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
  'update:nullSelected': [value: boolean]
}>()

const kind = computed(() => databaseEditorKind(props.column.dataType))
const inputType = computed(() => {
  if (kind.value === 'datetime') return 'datetime-local'
  if (kind.value === 'date' || kind.value === 'time') return kind.value
  return 'text'
})
const booleanValue = computed(() => props.nullSelected ? NULL_VALUE : props.modelValue)
const booleanOptions = computed<NcpSelectOption[]>(() => [
  { label: 'true', value: 'true' },
  { label: 'false', value: 'false' },
  ...(props.column.nullable ? [{ label: 'NULL（显式）', value: NULL_VALUE }] : []),
])
const placeholder = computed(() => {
  if (props.nullSelected) return '将写入 NULL'
  if (kind.value === 'integer') return '请输入整数（按字符串保存）'
  if (kind.value === 'decimal') return '请输入数字（保留原始精度）'
  if (kind.value === 'json') return '{ "key": "value" }'
  if (kind.value === 'blob') return 'Blob 仅支持只读展示'
  if (props.column.default !== undefined) return `留空使用默认值 ${props.column.default}`
  return '请输入字段值'
})

function updateBoolean(value: string) {
  if (value === NULL_VALUE) {
    emit('update:nullSelected', true)
    return
  }
  emit('update:nullSelected', false)
  emit('update:modelValue', value)
}
</script>

<template>
  <div class="database-cell-editor" :data-field-kind="kind">
    <NcpSelect
      v-if="kind === 'boolean'"
      :model-value="booleanValue"
      :options="booleanOptions"
      :disabled="disabled && !(kind === 'boolean' && nullSelected)"
      accessible-label="布尔字段值"
      placeholder="请选择 true / false"
      @update:model-value="updateBoolean"
    />
    <ElInput
      v-else
      :model-value="modelValue"
      :type="sensitive ? 'password' : kind === 'json' || kind === 'blob' ? 'textarea' : inputType"
      :disabled="disabled"
      :readonly="kind === 'blob'"
      :inputmode="kind === 'integer' ? 'numeric' : kind === 'decimal' ? 'decimal' : undefined"
      :autosize="kind === 'text' || kind === 'json' || kind === 'blob' ? { minRows: 2, maxRows: 5 } : undefined"
      :placeholder="placeholder"
      :aria-label="column.name"
      @update:model-value="emit('update:modelValue', String($event))"
    />
    <small v-if="kind === 'blob'" class="database-cell-editor__hint">Blob 目前只读，避免误改二进制内容。</small>
    <small v-else-if="kind === 'integer' || kind === 'decimal'" class="database-cell-editor__hint">不会经过 JavaScript Number，保持大整数和小数精度。</small>
  </div>
</template>

<style scoped>
.database-cell-editor {
  display: grid;
  min-width: 0;
  gap: 4px;
}

.database-cell-editor :deep(.el-input__wrapper),
.database-cell-editor :deep(.el-textarea__inner) {
  border-radius: var(--ncp-radius-control);
}

.database-cell-editor__hint {
  color: var(--ncp-text-subtle);
  font-size: .67rem;
  line-height: 1.35;
}
</style>
