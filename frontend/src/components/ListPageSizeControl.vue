<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElInputNumber, ElTooltip } from 'element-plus'

import NcpSelect, { type NcpSelectOption } from '@/components/NcpSelect.vue'
import { useListPreference } from '@/composables/useListPreference'

const props = defineProps<{ listKey: string }>()
const presets = [5, 10, 15, 20, 30, 50]
const options: NcpSelectOption[] = [
  ...presets.map((value) => ({ label: `${value} 条`, value: String(value) })),
  { label: '自定义', value: 'custom' },
]
const { pageSize, saving, error, setPageSize } = useListPreference(props.listKey)
const selected = ref('10')
const customValue = ref(10)

watch(pageSize, (value) => {
  selected.value = presets.includes(value) ? String(value) : 'custom'
  customValue.value = value
}, { immediate: true })

async function updatePreset(value: string) {
  if (value !== 'custom') await setPageSize(Number(value))
}

async function updateCustom(value: number | undefined) {
  if (value) await setPageSize(value)
}
</script>

<template>
  <ElTooltip :content="error || '该设置只作用于当前列表，并会保存到账号偏好中。'" placement="top">
    <div class="page-size-control" :class="{ saving }">
      <span>每页</span>
      <NcpSelect v-model="selected" :options="options" accessible-label="选择每页数量" @update:model-value="updatePreset" />
      <ElInputNumber
        v-if="selected === 'custom'"
        v-model="customValue"
        :min="1"
        :max="200"
        :controls="false"
        aria-label="自定义每页数量"
        @change="updateCustom"
      />
    </div>
  </ElTooltip>
</template>

<style scoped>
.page-size-control {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  gap: 7px;
  color: var(--ncp-text-subtle);
  font-size: .8rem;
  transition: opacity 160ms ease;
}
.page-size-control.saving { opacity: .64; }
.page-size-control :deep(.ncp-select) { width: 94px; min-width: 94px; }
.page-size-control :deep(.el-input-number) { width: 76px; }
.page-size-control :deep(.el-input__wrapper) {
  min-height: var(--ncp-control-height);
  border-radius: 8px;
  box-shadow: 0 0 0 1px var(--ncp-line) inset;
}
</style>
