<script setup lang="ts">
import { CalendarClock, Check, Clock3, X } from '@lucide/vue'
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
    <div class="date-time-range__fields">
      <label class="date-time-range__field">
        <span class="date-time-range__field-label"><CalendarClock :size="14" />开始时间</span>
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
      <span class="date-time-range__separator" aria-hidden="true">至</span>
      <label class="date-time-range__field">
        <span class="date-time-range__field-label"><CalendarClock :size="14" />结束时间</span>
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
    </div>
    <div class="date-time-range__actions" role="group" aria-label="时间范围操作">
      <ElTooltip content="将结束时间设为现在" placement="bottom">
        <ElButton class="date-time-range__button" aria-label="使用现在时间" @click="emit('now')"><Clock3 :size="15" /><span>现在</span></ElButton>
      </ElTooltip>
      <ElTooltip content="清除精确时间范围" placement="bottom">
        <ElButton class="date-time-range__button" aria-label="清除时间范围" @click="emit('clear')"><X :size="15" /><span>清除</span></ElButton>
      </ElTooltip>
      <ElButton class="date-time-range__button date-time-range__button--apply" type="primary" :loading="loading" :disabled="!from || !to" @click="emit('apply')">
        <Check :size="15" /><span>应用范围</span>
      </ElButton>
    </div>
  </div>
</template>

<style scoped>
.date-time-range { display:flex; min-width:0; align-items:center; gap:10px; }
.date-time-range__fields { display:flex; min-width:0; align-items:center; gap:8px; }
.date-time-range__field {
  display: grid;
  min-width: 0;
  min-height: 60px;
  align-content: center;
  gap: 3px;
  padding: 7px 10px 6px;
  border: 1px solid var(--ncp-line);
  border-radius: 11px;
  background: var(--ncp-control-surface);
  transition: border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast);
}
.date-time-range__field:focus-within {
  border-color: color-mix(in srgb, var(--ncp-primary) 68%, white);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--ncp-primary) 12%, transparent);
}
.date-time-range__field-label {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  font-weight: 700;
}
.date-time-range__field-label svg { color:var(--ncp-primary); }
.date-time-range__field :deep(.el-date-editor) {
  width: 168px;
  height: 28px;
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
  font-size: .76rem;
}
.date-time-range__separator {
  flex:0 0 auto;
  color: var(--ncp-text-subtle);
  font-size: .76rem;
  font-weight: 700;
}
.date-time-range__actions {
  display: flex;
  align-items: center;
  gap: 7px;
}
.date-time-range__button { min-height:var(--ncp-control-height); margin:0; gap:8px; padding-inline:13px!important; }
.date-time-range__button :deep(span) { display:inline-flex; align-items:center; line-height:1; }
.date-time-range__button--apply { min-width:94px; }
@media (max-width: 1180px) {
  .date-time-range { flex-wrap:wrap; justify-content:flex-end; }
}
@media (max-width: 760px) {
  .date-time-range { width:100%; align-items:stretch; flex-direction:column; gap:10px; }
  .date-time-range__fields { width:100%; align-items:stretch; flex-direction:column; gap:7px; }
  .date-time-range__field { width:100%; min-height:62px; }
  .date-time-range__field :deep(.el-date-editor) { width:100%; }
  .date-time-range__separator { display:none; }
  .date-time-range__actions { width:100%; display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:8px; }
  .date-time-range__button { width:100%; min-height:44px; justify-content:center; padding-inline:8px!important; }
  .date-time-range__button--apply { min-width:0; }
}
</style>
