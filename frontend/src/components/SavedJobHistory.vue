<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { ArrowRight, RefreshCw } from '@lucide/vue'
import { useJobHistory } from '@/composables/useJobHistory'
import { validHistoryType } from '@/api/jobHistory'
import { containerCreateStateDescription, jobTypeLabel, projectActionStateDescription } from '@/domain/jobPresentation'
import { formatLocalTimestamp } from '@/lib/datetime'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ active: () => boolean }>()
const auth = useAuthStore()
const { page, query, loading, error, latest, next, retry } = useJobHistory(() => { void auth.refresh() }, props.active)
const panel = ref<HTMLElement | null>(null)
watch(page, async value => { if (value) { await nextTick(); if (panel.value) panel.value.scrollTop = 0 } })
function focusPanel() { panel.value?.focus({ preventScroll: true }) }
const id = useId()
const typed = ref('')
const inputError = ref('')
const known = ['docker-image-pull', 'docker-image-remove', 'compose-deploy', 'docker-container-create', 'docker-container-start', 'docker-container-stop', 'docker-container-restart', 'docker-project-start', 'docker-project-stop', 'docker-project-restart']
const types = computed(() => [...new Set([...known, ...(query.value.type ? [query.value.type] : []), ...(page.value?.items.map(item => item.type) ?? [])])])
const unsupported = computed(() => error.value?.code === 'JOB_HISTORY_UNSUPPORTED')
const unauthorized = computed(() => error.value?.code === 'AUTH_UNAUTHORIZED')
function filter(type: string) { inputError.value = ''; typed.value = type; latest(type) }
function applyType() {
  if (!validHistoryType(typed.value)) { inputError.value = '类型最多 128 UTF-8 字节，不能含控制字符。'; return }
  filter(typed.value)
}
const stateLabels = new Map([['queued', '排队中'], ['running', '执行中'], ['completed', '已完成'], ['failed', '失败'], ['cancelled', '已取消'], ['interrupted', '结果未确认']])
</script>

<template>
  <section ref="panel" class="history" aria-label="已保存任务历史" tabindex="-1">
    <div class="history-toolbar">
      <p class="history-notice">仅显示最后保存的记录，可能落后于当前执行；不代表当前资源状态</p>
      <div class="history-filters">
        <label><span :id="`${id}-type`">已保存任务类型</span><select :aria-labelledby="`${id}-type`" :value="query.type" @change="filter(($event.target as HTMLSelectElement).value)"><option value="">全部类型</option><option v-for="type in types" :key="type" :value="type">{{ jobTypeLabel(type) }}</option></select></label>
        <label><span :id="`${id}-limit`">每页条数</span><select :aria-labelledby="`${id}-limit`" :value="query.limit" @change="latest(query.type, Number(($event.target as HTMLSelectElement).value))"><option :value="20">20</option><option :value="50">50</option><option :value="100">100</option></select></label>
      </div>
      <form class="history-exact" @submit.prevent="applyType"><label><span :id="`${id}-exact`">其他类型精确查询（保留空格）</span><input v-model="typed" :aria-labelledby="`${id}-exact`" :aria-invalid="!!inputError" :aria-describedby="inputError ? `${id}-invalid` : undefined" type="text" autocomplete="off" /></label><button type="submit">查询类型</button></form>
      <p v-if="inputError" :id="`${id}-invalid`" class="history-error" role="alert">{{ inputError }}</p>
      <p class="history-scope">当前查询：{{ query.type ? `类型「${query.type}」` : '全部类型' }} · 每页最多 {{ query.limit }} 条</p>
      <div role="status" aria-live="polite" aria-atomic="true" class="history-feedback">
        <p v-if="loading">{{ page ? '正在读取下一页，保留当前页…' : '正在读取已保存历史…' }}</p>
        <template v-else-if="error"><p class="history-error">{{ error.message }}</p><p v-if="page">读取失败，未切页；仍显示上次成功读取的当前页。</p></template>
        <p v-else-if="page">本页 {{ page.items.length }} 条{{ query.cursor ? ' · 较早记录' : ' · 最新页' }}</p>
      </div>
      <button v-if="error && !unsupported && !unauthorized" type="button" :disabled="loading" @click="focusPanel(); retry()">重试同一读取</button>
    </div>
    <section class="history-list" :aria-busy="loading" aria-label="已保存历史列表">
      <p v-if="page && !page.items.length" class="history-empty">本次查询没有更多已保存记录。可回到最新页重新读取。</p>
      <article v-for="job in page?.items" :key="job.id" class="history-record" :aria-label="job.reference || job.id">
        <div class="history-top"><span>{{ jobTypeLabel(job.type) }}</span><span class="history-status">保存状态：{{ stateLabels.get(job.status) ?? job.status }}</span></div>
        <h3>{{ job.reference || job.id }}</h3>
        <p v-if="job.type === 'docker-container-create' && containerCreateStateDescription(job.status)" class="history-notice">保存记录：{{ containerCreateStateDescription(job.status) }}</p>
        <p v-if="projectActionStateDescription(job.type, job.status)" class="history-notice">保存记录：{{ projectActionStateDescription(job.type, job.status) }}</p>
        <p class="history-updated"><time :datetime="job.updatedAt">记录更新时间 {{ formatLocalTimestamp(job.updatedAt) }}</time></p>
        <details><summary>查看保存详情</summary><dl>
          <div><dt>完整 ID</dt><dd class="history-mono">{{ job.id }}</dd></div>
          <div><dt>任务类型</dt><dd>{{ job.type }}</dd></div>
          <div><dt>创建时间</dt><dd>{{ formatLocalTimestamp(job.createdAt) }}</dd></div>
          <div><dt>保存的完成时间</dt><dd>{{ job.completedAt ? formatLocalTimestamp(job.completedAt) : '未保存完成时间' }}</dd></div>
          <div><dt>消息</dt><dd>{{ job.message || '无' }}</dd></div>
          <div v-if="job.error"><dt>错误</dt><dd>{{ job.error }}</dd></div>
          <div v-if="job.errorCode"><dt>错误代码</dt><dd>{{ job.errorCode }}</dd></div>
          <div v-if="job.cancellationStatus"><dt>保存的取消状态</dt><dd>{{ job.cancellationStatus }}</dd></div>
        </dl></details>
      </article>
    </section>
    <div class="history-pagination">
      <p>逐页读取，不保证跨页快照；新记录请回到最新页查看。</p>
      <div><button type="button" :disabled="loading" @click="focusPanel(); latest()"><RefreshCw :size="16" aria-hidden="true" />回到最新页</button><button type="button" :disabled="loading || !!error || !page?.nextCursor" @click="focusPanel(); next()">下一页<ArrowRight :size="16" aria-hidden="true" /></button></div>
    </div>
  </section>
</template>

<style scoped>
.history{display:flex;flex:1;flex-direction:column;min-width:0;min-height:0;overflow:auto;overscroll-behavior:contain;color:var(--ncp-text)}.history-toolbar{padding:14px 20px;border-bottom:1px solid var(--ncp-line);flex-shrink:0}.history-notice{margin:0 0 12px;font-size:.8rem;line-height:1.6;color:var(--ncp-warning-strong)}.history-filters{display:grid;grid-template-columns:minmax(0,2fr) minmax(0,1fr);gap:10px}.history label{display:grid;min-width:0;gap:6px;font-size:.78rem;color:var(--ncp-text-muted)}.history input,.history select{min-height:44px;min-width:0;width:100%;padding:8px 10px;border:1px solid var(--ncp-control-border);border-radius:var(--ncp-radius-control);background:var(--ncp-control-surface);color:var(--ncp-text);font:inherit}.history-exact{display:flex;align-items:end;gap:10px;margin-top:10px}.history-exact label{flex:1}.history button{display:inline-flex;align-items:center;justify-content:center;gap:6px;min-height:44px;min-width:44px;padding:8px 12px;border:1px solid var(--ncp-line);border-radius:var(--ncp-radius-control);background:var(--ncp-surface);color:var(--ncp-text-muted);font:inherit;font-size:.8rem}.history button:disabled{opacity:.55;cursor:not-allowed}.history button:hover:not(:disabled){background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.history button:focus-visible,.history input:focus-visible,.history select:focus-visible,.history summary:focus-visible{outline:2px solid var(--ncp-primary);outline-offset:2px}.history-scope,.history-feedback,.history-empty{font-size:.78rem;line-height:1.6;overflow-wrap:anywhere}.history-scope{color:var(--ncp-text-subtle);margin-bottom:4px}.history-feedback p{margin:6px 0}.history-error{color:var(--ncp-danger-strong);font-size:.78rem}.history-list{flex:1 0 auto;min-height:0;padding:16px 20px;background:var(--ncp-surface-quiet)}.history-record{min-width:0;padding:14px 16px;margin-bottom:10px;border:1px solid var(--ncp-line);border-radius:var(--ncp-radius-md);background:var(--ncp-surface);overflow-wrap:anywhere}.history-top{display:flex;justify-content:space-between;flex-wrap:wrap;gap:8px;font-size:.75rem;color:var(--ncp-text-subtle)}.history-status{padding:4px 8px;border-radius:var(--ncp-radius-pill);background:var(--ncp-neutral-soft);color:var(--ncp-neutral-strong)}.history-record h3{margin:10px 0;font-size:.9rem;line-height:1.5}.history-updated{font-size:.75rem;color:var(--ncp-text-subtle)}.history summary{min-height:44px;padding:12px 0;font-size:.8rem;color:var(--ncp-primary-strong);cursor:pointer}.history dl{display:grid;gap:10px;margin:8px 0}.history dl>div{display:grid;grid-template-columns:110px minmax(0,1fr);gap:10px;font-size:.78rem;line-height:1.6}.history dt{color:var(--ncp-text-subtle)}.history dd{margin:0;white-space:pre-wrap}.history-mono{font-family:var(--ncp-font-mono)}.history-pagination{padding:10px 20px;border-top:1px solid var(--ncp-line);flex-shrink:0}.history-pagination p{font-size:.75rem;color:var(--ncp-text-subtle);line-height:1.5;margin:0 0 8px}.history-pagination>div{display:flex;justify-content:space-between;gap:8px}
@media(max-width:560px){.history-toolbar,.history-list{padding:12px 14px}.history-exact{flex-wrap:wrap}.history-exact label{flex-basis:100%}.history dl>div{grid-template-columns:1fr;gap:2px}.history-pagination{padding:10px 14px}}
@media(max-height:640px){.history{display:block}.history-list{overflow:visible}}
</style>
