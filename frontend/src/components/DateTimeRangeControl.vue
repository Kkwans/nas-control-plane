<script setup lang="ts">
import { computed } from 'vue'
import { Check, Clock3, X } from '@lucide/vue'
import { ElDatePicker, ElTooltip } from 'element-plus'

import ActionButton from '@/components/ActionButton.vue'

const props = defineProps<{
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

const rangeValue = computed<[Date, Date] | null>({
  get: () => props.from && props.to ? [props.from, props.to] as [Date, Date] : null,
  set: (value: [Date, Date] | null) => {
    emit('update:from', value?.[0] ?? null)
    emit('update:to', value?.[1] ?? null)
  },
})
</script>

<template>
  <div class="date-time-range" role="group" aria-label="精确时间范围" :aria-busy="loading ? 'true' : 'false'">
    <div class="date-time-range__picker-frame">
      <ElDatePicker
        v-model="rangeValue"
        class="date-time-range__picker"
        type="datetimerange"
        format="YYYY/MM/DD HH:mm"
        range-separator="至"
        start-placeholder="开始时间"
        end-placeholder="结束时间"
        aria-label="选择开始和结束时间"
        :disabled="loading"
        :editable="false"
        :clearable="false"
        :teleported="true"
        unlink-panels
      />
    </div>
    <div class="date-time-range__actions" role="group" aria-label="时间范围操作">
      <ElTooltip content="将结束时间设为现在" placement="bottom">
        <ActionButton :icon="Clock3" :disabled="loading" aria-label="将结束时间设为现在" @click="emit('now')">现在</ActionButton>
      </ElTooltip>
      <ElTooltip content="清除精确时间范围" placement="bottom">
        <ActionButton :icon="X" :disabled="loading" aria-label="清除精确时间范围" @click="emit('clear')">清除</ActionButton>
      </ElTooltip>
      <ActionButton variant="primary" :icon="Check" :loading="loading" :disabled="loading || !from || !to" aria-label="应用精确时间范围" @click="emit('apply')">应用</ActionButton>
    </div>
  </div>
</template>

<style scoped>
.date-time-range {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--ncp-space-2);
}

.date-time-range__picker-frame {
  width: clamp(340px, 28vw, 430px);
  min-width: 0;
  max-width: 100%;
  container: monitoring-date-picker / inline-size;
}

/* ElDatePicker 转发 class，不转发当前组件的 scope 属性；从原生容器穿透。 */
.date-time-range__picker-frame :deep(.date-time-range__picker) {
  box-sizing: border-box;
  width: 100% !important;
  min-width: 0;
  max-width: 100%;
  height: 40px;
  min-height: 40px;
  padding: 0 var(--ncp-space-3) !important;
  border: 1px solid var(--ncp-control-border);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-control-surface);
  box-shadow: var(--ncp-shadow-control) !important;
  transition:
    border-color var(--ncp-duration-fast) var(--ncp-ease-out),
    background-color var(--ncp-duration-fast) var(--ncp-ease-out),
    box-shadow var(--ncp-duration-fast) var(--ncp-ease-out);
}

.date-time-range__picker-frame :deep(.date-time-range__picker:hover) {
  border-color: var(--ncp-control-border-hover);
  background: var(--ncp-control-hover);
}

.date-time-range__picker-frame :deep(.date-time-range__picker:focus-within) {
  border-color: var(--ncp-control-border-focus);
  background: var(--ncp-control-surface);
  box-shadow: 0 0 0 3px var(--ncp-focus-ring) !important;
}

.date-time-range__picker-frame :deep(.el-range__icon),
.date-time-range__picker-frame :deep(.el-range__close-icon) {
  width: 1em;
  height: 1em;
  margin-left: 0;
  font-size: max(14px, .72rem);
  line-height: 1;
}

.date-time-range__picker-frame :deep(.el-range__icon) {
  margin-right: var(--ncp-space-2);
  color: var(--ncp-primary-strong);
}

.date-time-range__picker-frame :deep(.el-range-input) {
  min-width: 0;
  color: var(--ncp-text);
  font-family: var(--ncp-font-mono);
  font-size: .76rem;
  line-height: 1.5;
  text-align: center;
}

.date-time-range__picker-frame :deep(.el-range-input::placeholder) {
  color: var(--ncp-text-subtle);
  font-family: var(--ncp-font-ui);
  text-align: center;
}

.date-time-range__picker-frame :deep(.el-range-separator) {
  width: 24px;
  height: auto;
  padding: 0;
  flex: 0 0 24px;
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  font-weight: 700;
  line-height: 1.5;
  text-align: center;
}

.date-time-range__actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: var(--ncp-space-2);
}

.date-time-range__actions > .action-button {
  min-width: max-content;
  height: auto;
  min-height: 40px;
  padding-block: .35em;
}

.date-time-range__actions :deep(.action-button__label) {
  line-height: 1.3;
}

@media (max-width: 1260px) {
  .date-time-range {
    width: 100%;
  }

  .date-time-range__picker-frame {
    width: auto;
    flex: 1 1 360px;
  }
}

@media (max-width: 760px) {
  .date-time-range {
    align-items: stretch;
    flex-direction: column;
  }

  .date-time-range__picker-frame {
    width: 100%;
    flex: 0 0 auto;
  }

  .date-time-range__actions {
    width: 100%;
    flex-wrap: wrap;
  }

  .date-time-range__actions > .action-button {
    flex: 1 0 auto;
  }
}

/* 完整日期值随字号变宽；空间不足时逐行展示，不缩小字体或隐藏图标。 */
@container monitoring-date-picker (max-width: 26rem) {
  .date-time-range__picker-frame :deep(.date-time-range__picker) {
    display: grid;
    height: auto;
    grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
    row-gap: 2px;
    padding-block: var(--ncp-space-2) !important;
  }

  .date-time-range__picker-frame :deep(.el-range-input) {
    width: 100%;
    height: 1.5em;
    grid-column: 1 / -1;
  }

  .date-time-range__picker-frame :deep(.el-range-input:first-of-type) { grid-row: 1; }
  .date-time-range__picker-frame :deep(.el-range-input:last-of-type) { grid-row: 3; }
  .date-time-range__picker-frame :deep(.el-range__icon) { grid-column: 1; grid-row: 2; justify-self: end; }
  .date-time-range__picker-frame :deep(.el-range-separator) { grid-column: 2; grid-row: 2; }
  .date-time-range__picker-frame :deep(.el-range__close-icon) { grid-column: 3; grid-row: 2; }
}
</style>
