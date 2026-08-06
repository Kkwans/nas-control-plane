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
  <div class="date-time-range" role="group" aria-label="精确时间范围" :aria-busy="loading ? 'true' : 'false'">
    <div class="date-time-range__fields">
      <label class="date-time-range__field">
        <span class="date-time-range__field-label"><CalendarClock :size="13" aria-hidden="true" />开始</span>
        <ElDatePicker
          :model-value="from"
          name="monitoring-range-from"
          type="datetime"
          format="YYYY/MM/DD HH:mm"
          placeholder="选择开始时间"
          aria-label="开始时间"
          :disabled="loading"
          :editable="false"
          :clearable="false"
          :teleported="true"
          @update:model-value="emit('update:from', $event as Date | null)"
        />
      </label>
      <span class="date-time-range__separator" aria-hidden="true">至</span>
      <label class="date-time-range__field">
        <span class="date-time-range__field-label"><CalendarClock :size="13" aria-hidden="true" />结束</span>
        <ElDatePicker
          :model-value="to"
          name="monitoring-range-to"
          type="datetime"
          format="YYYY/MM/DD HH:mm"
          placeholder="选择结束时间"
          aria-label="结束时间"
          :disabled="loading"
          :editable="false"
          :clearable="false"
          :teleported="true"
          @update:model-value="emit('update:to', $event as Date | null)"
        />
      </label>
    </div>
    <div class="date-time-range__actions" role="group" aria-label="时间范围操作">
      <ElTooltip content="将结束时间设为现在" placement="bottom">
        <ElButton class="date-time-range__button" :disabled="loading" aria-label="将结束时间设为现在" @click="emit('now')"><Clock3 :size="15" aria-hidden="true" /><span>现在</span></ElButton>
      </ElTooltip>
      <ElTooltip content="清除精确时间范围" placement="bottom">
        <ElButton class="date-time-range__button" :disabled="loading" aria-label="清除精确时间范围" @click="emit('clear')"><X :size="15" aria-hidden="true" /><span>清除</span></ElButton>
      </ElTooltip>
      <ElButton class="date-time-range__button date-time-range__button--apply" type="primary" :loading="loading" :disabled="loading || !from || !to" aria-label="应用精确时间范围" @click="emit('apply')">
        <Check :size="15" aria-hidden="true" /><span>应用</span>
      </ElButton>
    </div>
  </div>
</template>

<style scoped>
.date-time-range { display:flex; min-width:0; align-items:center; gap:10px; }
.date-time-range__fields { display:flex; min-width:0; align-items:center; gap:8px; }
.date-time-range__field {
  display: flex;
  min-width: 0;
  height: 34px;
  align-items: center;
  gap: 5px;
  padding: 0;
}
.date-time-range__field-label {
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  gap: 4px;
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  font-weight: 700;
  line-height: 1;
}
.date-time-range__field-label svg { color:var(--ncp-primary); }
.date-time-range__field :deep(.el-date-editor) {
  flex: 0 1 auto;
  width: 148px;
  height: 34px;
  min-height: 0;
  border-bottom: 1px solid var(--ncp-control-border);
  border-radius: 0;
  background: transparent;
  transition: border-color var(--ncp-duration-fast), background-color var(--ncp-duration-fast);
}
.date-time-range__field :deep(.el-date-editor:hover) {
  border-bottom-color: var(--ncp-control-border-hover);
  background: var(--ncp-control-hover);
}
.date-time-range__field:focus-within :deep(.el-date-editor) {
  border-bottom-color: var(--ncp-control-border-focus);
  background: var(--ncp-control-surface);
}
.date-time-range__field :deep(.el-input__wrapper) {
  height: 100%;
  min-height: 0;
  padding: 0;
  background: transparent !important;
  border-radius: 0;
  box-shadow: none !important;
}
.date-time-range__field :deep(.el-input__prefix) { display: none; }
.date-time-range__field :deep(.el-input__inner) {
  height: 100%;
  color: var(--ncp-text);
  font-family: var(--ncp-font-mono);
  font-size: .76rem;
  line-height: 34px;
}
.date-time-range__separator {
  flex:0 0 auto;
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  font-weight: 700;
}
.date-time-range__actions {
  display: flex;
  align-items: center;
  gap: 4px;
}
.date-time-range__button {
  display: inline-flex !important;
  min-width: 0;
  min-height: 34px !important;
  align-items: center;
  justify-content: center;
  gap: 5px;
  margin: 0;
  padding: 0 9px !important;
  line-height: 1;
  white-space: nowrap;
}
.date-time-range__button :deep(svg) { display:block; }
.date-time-range__button :deep(span) { display:inline-flex; align-items:center; line-height:1; }
.date-time-range__button--apply { min-width:64px; }
@media (max-width: 1180px) {
  .date-time-range { flex-wrap:wrap; justify-content:flex-end; }
  .date-time-range__fields { flex: 1 1 auto; }
}
@media (max-width: 760px) {
  .date-time-range { width:100%; align-items:stretch; flex-direction:column; gap:10px; }
  .date-time-range__fields { width:100%; align-items:stretch; gap:6px; }
  .date-time-range__field { flex:1 1 0; }
  .date-time-range__field :deep(.el-date-editor) { flex:1 1 auto; width:100%; }
  .date-time-range__field-label { font-size:.7rem; }
  .date-time-range__actions { width:100%; display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:8px; }
  .date-time-range__button { width:100%; min-height:44px !important; justify-content:center; padding-inline:8px!important; }
  .date-time-range__button--apply { min-width:0; }
}
@media (max-width: 480px) {
  .date-time-range__fields { flex-direction:column; }
  .date-time-range__field { width:100%; }
  .date-time-range__separator { display:none; }
}
</style>
