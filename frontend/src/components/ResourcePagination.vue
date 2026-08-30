<script setup lang="ts">
const props = withDefaults(defineProps<{
  page?: number
  pageCount?: number
  hasNext?: boolean
  loading?: boolean
  mode?: 'page' | 'cursor'
  previousLabel?: string
  nextLabel?: string
}>(), { page: 1, pageCount: 1, hasNext: false, loading: false, mode: 'page', previousLabel: '上一页', nextLabel: '下一页' })
const emit = defineEmits<{ 'update:page': [value: number]; 'load-more': [] }>()
function previous() { if (props.page > 1 && !props.loading) emit('update:page', props.page - 1) }
function next() { if (props.mode === 'cursor') { if (props.hasNext && !props.loading) emit('load-more'); return } if (props.page < props.pageCount && !props.loading) emit('update:page', props.page + 1) }
</script>
<template>
  <div class="resource-pagination">
    <button type="button" :disabled="page <= 1 || loading || mode === 'cursor'" @click="previous">{{ previousLabel }}</button>
    <strong v-if="mode === 'page'">{{ page }} / {{ pageCount }}</strong>
    <button type="button" :disabled="loading || (mode === 'page' ? page >= pageCount : !hasNext)" @click="next">{{ mode === 'cursor' ? '加载更多' : nextLabel }}</button>
  </div>
</template>
<style scoped>
.resource-pagination{display:flex;align-items:center;justify-content:center;gap:8px;color:var(--ncp-text-subtle);font-size:.78rem}.resource-pagination button{min-height:34px;padding:0 11px;border:1px solid var(--ncp-line);border-radius:8px;background:var(--ncp-surface);color:var(--ncp-text-muted);font-weight:680}.resource-pagination button:disabled{cursor:not-allowed;opacity:.42}.resource-pagination strong{min-width:62px;color:var(--ncp-text);text-align:center}
</style>
