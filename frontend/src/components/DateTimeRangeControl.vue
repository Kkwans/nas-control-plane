<script setup lang="ts">
import { Clock3, RotateCcw, X } from '@lucide/vue'
import { ElButton, ElDatePicker, ElTooltip } from 'element-plus'

defineProps<{
  from: Date | null
  to: Date | null
  loading?: boolean
}>()

const emit = defineEmits<{
  'update:from': [value: Date | null]
  'update:to': [value: Date | null]
  apply: []
  clear: []
  now: []
}>()
</script>

<template>
  <div class="date-time-range" aria-label="精确时间范围">
    <label class="date-time-range__field">
      <span>开始</span>
      <ElDatePicker
        :model-value="from"
        type="datetime"
        format="YYYY/MM/DD HH:mm"
        placeholder="选择开始时间"
        :editable="false"
        :clearable="false"
        :teleported="true"
        @update:model-value="emit('update:from', $event as Date | null)"
      />
    </label>
    <span class="date-time-range__separator">至</span>
    <label class="date-time-range__field">
      <span>结束</span>
      <ElDatePicker
        :model-value="to"
        type="datetime"
        format="YYYY/MM/DD HH:mm"
        placeholder="选择结束时间"
        :editable="false"
        :clearable="false"
        :teleported="true"
        @update:model-value="emit('update:to', $event as Date | null)"
      />
    </label>
    <div class="date-time-range__actions">
      <ElTooltip content="将结束时间设为现在" placement="bottom">
        <ElButton aria-label="使用现在时间" @click="emit('now')"><Clock3 :size="15" />现在</ElButton>
      </ElTooltip>
      <ElTooltip content="清除精确时间范围" placement="bottom">
        <ElButton aria-label="清除时间范围" @click="emit('clear')"><X :size="15" />清除</ElButton>
      </ElTooltip>
      <ElButton type="primary" :loading="loading" :disabled="!from || !to" @click="emit('apply')">
        <RotateCcw :size="15" />应用
      </ElButton>
    </div>
  </div>
</template>

<style scoped>
.date-time-range {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
}
.date-time-range__field {
  display: flex;
  min-width: 0;
  height: var(--ncp-control-height);
  align-items: center;
  gap: 7px;
  padding-left: 10px;
  border: 1px solid var(--ncp-line);
  border-radius: 10px;
  background: var(--ncp-control-surface);
  transition: border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast);
}
.date-time-range__field:focus-within {
  border-color: color-mix(in srgb, var(--ncp-primary) 68%, white);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--ncp-primary) 12%, transparent);
}
.date-time-range__field > span {
  flex: 0 0 auto;
  color: var(--ncp-text-subtle);
  font-size: .75rem;
  font-weight: 700;
}
.date-time-range__field :deep(.el-date-editor) {
  width: 156px;
  height: calc(var(--ncp-control-height) - 2px);
}
.date-time-range__field :deep(.el-input__wrapper) {
  min-height: 0;
  padding: 0 9px 0 0;
  background: transparent;
  box-shadow: none !important;
}
.date-time-range__field :deep(.el-input__prefix) { display: none; }
.date-time-range__field :deep(.el-input__inner) {
  color: var(--ncp-text);
  font-family: var(--ncp-font-mono);
  font-size: .78rem;
}
.date-time-range__separator {
  color: var(--ncp-text-subtle);
  font-size: .78rem;
}
.date-time-range__actions {
  display: flex;
  align-items: center;
  gap: 6px;
}
.date-time-range__actions :deep(.el-button) {
  min-height: var(--ncp-control-height);
  margin: 0;
}
@media (max-width: 1180px) {
  .date-time-range { flex-wrap: wrap; justify-content: flex-end; }
}
@media (max-width: 760px) {
  .date-time-range,
  .date-time-range__actions { width: 100%; }
  .date-time-range__field { flex: 1 1 calc(50% - 14px); }
  .date-time-range__field :deep(.el-date-editor) { width: 100%; }
  .date-time-range__actions :deep(.el-button) { flex: 1; }
}
</style>
