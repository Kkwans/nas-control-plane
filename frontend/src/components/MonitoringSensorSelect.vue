<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElOption, ElSelect } from 'element-plus'

const props = defineProps<{
  modelValue: string[]
  names: string[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const query = ref('')
const nameSet = computed(() => new Set(props.names))
const matches = computed(() => {
  const normalized = query.value.trim().toLocaleLowerCase()
  const first: string[] = []
  let count = 0
  for (const name of props.names) {
    if (normalized && !name.toLocaleLowerCase().includes(normalized)) continue
    count += 1
    if (first.length < 50) first.push(name)
  }
  return { first, count }
})
// 保留已选项的标签，但不为完整传感器目录创建 option DOM。
const options = computed(() => [...new Set([...props.modelValue.slice(0, 8), ...matches.value.first])])
function update(value: unknown) {
  if (!Array.isArray(value)) return
  emit('update:modelValue', [...new Set(value.filter((name): name is string => typeof name === 'string' && nameSet.value.has(name)))].slice(0, 8))
}
</script>

<template>
  <div class="monitor-sensor-select">
    <p class="monitor-sensor-select__count" aria-live="polite">已显示 {{ modelValue.length }} / {{ names.length }} 个传感器，最多同时显示 8 个</p>
    <ElSelect
      v-if="names.length > 8 || modelValue.length !== names.length"
      :model-value="modelValue"
      multiple
      filterable
      clearable
      collapse-tags
      fit-input-width
      popper-class="monitor-sensor-select-popper"
      :max-collapse-tags="2"
      :multiple-limit="8"
      :filter-method="(value: string) => query = value"
      aria-label="选择温度传感器，最多 8 个"
      placeholder="搜索并选择传感器"
      @update:model-value="update"
    >
      <ElOption v-for="name in options" :key="name" :label="name" :value="name" :title="name" />
    </ElSelect>
    <p v-if="matches.count > 50" class="monitor-sensor-select__hint" role="status">匹配 {{ matches.count }} 项，仅显示前 50 项，请继续搜索。</p>
    <p v-if="modelValue.length >= 8 && names.length > 8" class="monitor-sensor-select__hint">已达到 8 个上限，请先取消一个再选择。</p>
  </div>
</template>

<style scoped>
.monitor-sensor-select { display:grid; gap:6px; margin-bottom:10px; }
.monitor-sensor-select__count, .monitor-sensor-select__hint { margin:0; color:var(--ncp-text-subtle); font-size:.7rem; line-height:1.5; }
.monitor-sensor-select :deep(.el-select) { min-width:0; width:100%; }
</style>


<style>
/* 下拉面板挂载到 body，不能依赖父卡片或 scoped 后代约束宽度。 */
.monitor-sensor-select-popper { max-width: calc(100vw - 24px); }
.monitor-sensor-select-popper .el-select-dropdown__item { height: auto; min-height: 34px; padding-top: 8px; padding-bottom: 8px; line-height: 1.5; white-space: normal; overflow-wrap: anywhere; }
</style>
