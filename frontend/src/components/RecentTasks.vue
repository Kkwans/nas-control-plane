<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { ChevronDown, RefreshCw } from '@lucide/vue'
import ResourcePagination from '@/components/ResourcePagination.vue'
import ResourceState from '@/components/ResourceState.vue'
import { useTaskCenter } from '@/composables/useTaskCenter'
import { jobArtifactLabel, jobProgressLimitedWarning, jobState, jobStateDescription, jobStateLabel, jobTypeLabel } from '@/domain/jobPresentation'
import { formatLocalTimestamp } from '@/lib/datetime'
import type { JobSnapshot } from '@/api/system'

const props = defineProps<{ active: () => boolean }>()
const emit = defineEmits<{ focusClose: [] }>()
const open = computed(props.active)
const titleId = useId()
const expanded = ref(new Set<string>())
const {
  jobs, invalidCount, loading, refreshing, hasLoaded, stale, error, lastFetchedAt,
  typeFilter, statusFilter, query, page, typeOptions, statusOptions,
  filteredJobs, pagedJobs, pageCount, cancellingIds, cancelErrors,
  refresh, requestCancel, canCancel,
} = useTaskCenter(open)
const snapshotTime = computed(() => lastFetchedAt.value ? formatLocalTimestamp(lastFetchedAt.value.toISOString()) : '')
function toggleDetails(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}
function clearFilters() { query.value = ''; typeFilter.value = 'all'; statusFilter.value = 'all' }
function focusRowDetails(target: Element | null) {
  target?.closest('article')?.querySelector<HTMLButtonElement>('.task-detail-toggle')?.focus({ preventScroll: true })
}
function cancelTask(job: JobSnapshot, event: MouseEvent) {
  // 命令会立即禁用按钮；先保留同一行的有效焦点，避免浏览器把 Tab 起点退回 body。
  focusRowDetails(event.currentTarget as Element)
  void requestCancel(job)
}
watch([jobs, stale, cancellingIds], () => {
  const active = document.activeElement
  if (!(active instanceof HTMLButtonElement) || !active.matches('.task-cancel')) return
  const job = jobs.value.find(item => item.id === active.dataset.jobId)
  if (!job) emit('focusClose')
  else if (!canCancel(job) || cancellingIds.value.has(job.id)) focusRowDetails(active)
})
function progress(job: JobSnapshot) {
  // 只有服务端提供明确的 0–100 数值时显示；Compose 的占位 0 不代表可测量进度。
  return job.progressLimited !== true && job.type === 'docker-image-pull' && Number.isFinite(job.progress) && job.progress >= 0 && job.progress <= 100
    ? Math.round(job.progress) : null
}
</script>

<template>
    <div class="task-center">
      <div class="task-toolbar">
        <p class="task-scope">显示控制面当前可提供的最近记录，可能不包含更早历史。关闭面板不会停止任务。</p>
        <div class="task-refresh-row">
          <div class="task-observation"><strong>当前列表 {{ jobs.length }} 条</strong><span>定期刷新 · 页面可见且正常时每 15 秒</span></div>
          <button class="task-button" type="button" :disabled="loading || refreshing" @click="refresh"><RefreshCw :size="16" :class="{ spin: loading || refreshing }" aria-hidden="true" />刷新任务</button>
        </div>
        <div class="task-filters">
          <label class="task-search"><span :id="`${titleId}-search`">搜索已加载记录</span><input :aria-labelledby="`${titleId}-search`" v-model="query" type="search" placeholder="对象、ID 或消息" /></label>
          <label><span :id="`${titleId}-type`">任务类型</span><select :aria-labelledby="`${titleId}-type`" v-model="typeFilter"><option v-for="option in typeOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></label>
          <label><span :id="`${titleId}-status`">任务状态</span><select :aria-labelledby="`${titleId}-status`" v-model="statusFilter"><option v-for="option in statusOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></label>
        </div>
        <div class="task-feedback" role="status" aria-live="polite" aria-atomic="true">
          <span v-if="refreshing">正在刷新，保留上次记录…</span>
          <span v-else-if="stale && hasLoaded" class="task-warning">刷新失败，当前记录可能已过期。取消操作暂不可用。{{ error }}</span>
          <span v-else-if="hasLoaded">已获取当前可提供的最近记录</span>
          <span v-if="snapshotTime" class="task-snapshot">上次成功获取 {{ snapshotTime }}</span>
        </div>
        <p v-if="invalidCount" class="task-warning task-invalid">有 {{ invalidCount }} 条记录无法解析</p>
      </div>
      <!-- 仅作滚动布局；最近任务由外层对话框标题命名，状态由各自的 live region 通知。 -->
      <div class="task-list-region">
        <ResourceState v-if="loading && !hasLoaded" state="loading" title="正在读取最近任务" />
        <ResourceState v-else-if="error && !hasLoaded" state="error" title="最近任务读取失败" :message="error" @retry="refresh" />
        <ResourceState v-else-if="!jobs.length && invalidCount" state="error" title="记录无法读取" message="返回的记录暂时无法解析，请刷新重试。" @retry="refresh" />
        <ResourceState v-else-if="hasLoaded && !jobs.length" state="empty" title="尚无可展示记录" message="这里仅显示控制面当前可提供的最近任务。" />
        <div v-else-if="hasLoaded && !filteredJobs.length" class="task-empty-filter"><ResourceState state="empty" title="没有匹配的记录" message="筛选范围仅为已加载记录。" /><button class="task-button" type="button" @click="clearFilters">清空筛选</button></div>
        <div v-else class="task-list">
          <article v-for="job in pagedJobs" :key="job.id" class="task-row" :aria-label="job.reference || job.id">
            <div class="task-row__top"><span class="task-type">{{ jobTypeLabel(job.type) }}</span><span class="task-state" :class="`task-state--${jobState(job)}`">{{ jobStateLabel(job) }}</span></div>
            <h3>{{ job.reference || job.id }}</h3>
            <p v-if="jobStateDescription(job)" class="task-description">{{ jobStateDescription(job) }}</p>
            <p v-if="job.progressLimited" class="task-description task-warning">{{ jobProgressLimitedWarning }}</p>
            <div class="task-progress-row">
              <template v-if="progress(job) !== null"><progress :value="progress(job)!" max="100" :aria-label="`${job.reference || job.id} 进度`" /><span>{{ progress(job) }}%</span></template>
              <span v-else class="task-progress-unknown">{{ job.progressLimited ? '完整进度未提供' : '进度未提供' }}</span>
            </div>
            <div class="task-row__footer"><time :datetime="job.updatedAt">更新 {{ formatLocalTimestamp(job.updatedAt) }}</time><button class="task-detail-toggle" type="button" :aria-expanded="expanded.has(job.id)" @click="toggleDetails(job.id)">{{ expanded.has(job.id) ? '收起详情' : '查看详情' }}<ChevronDown :size="16" :class="{ expanded: expanded.has(job.id) }" aria-hidden="true" /></button></div>
            <div v-if="expanded.has(job.id)" class="task-details">
              <dl><div><dt>完整 ID</dt><dd class="task-mono">{{ job.id }}</dd></div><div><dt>对象</dt><dd>{{ job.reference || '未提供' }}</dd></div><div><dt>任务类型</dt><dd>{{ job.type }}</dd></div><div><dt>创建时间</dt><dd>{{ formatLocalTimestamp(job.createdAt) }}</dd></div><div><dt>完成时间</dt><dd>{{ job.completedAt ? formatLocalTimestamp(job.completedAt) : '尚无确认时间' }}</dd></div><div v-if="job.type === 'docker-image-pull'"><dt>镜像当前状态</dt><dd>{{ jobArtifactLabel(job) }}</dd></div><div><dt>消息</dt><dd>{{ job.message || '无' }}</dd></div><div v-if="job.error"><dt>错误</dt><dd>{{ job.error }}</dd></div><div v-if="job.errorCode"><dt>错误代码</dt><dd class="task-mono">{{ job.errorCode }}</dd></div></dl>
              <p v-if="job.type === 'compose-deploy'" class="task-description">部署任务完成仅表示命令已完成，请另行核验配置与实际运行状态。</p>
              <button v-if="job.type === 'docker-image-pull' && job.cancellable === true" class="task-button task-cancel" :data-job-id="job.id" type="button" :disabled="!canCancel(job) || cancellingIds.has(job.id)" @click="cancelTask(job, $event)">{{ cancellingIds.has(job.id) ? '正在提交' : '请求取消' }}</button>
              <p v-else class="task-description">此任务不支持取消</p>
              <p v-if="cancelErrors[job.id]" class="task-cancel-error" role="alert">{{ cancelErrors[job.id]!.reference }}：{{ cancelErrors[job.id]!.message }}（{{ cancelErrors[job.id]!.code }}）</p>
            </div>
          </article>
        </div>
      </div>
      <div v-if="filteredJobs.length" class="task-pagination"><span>匹配 {{ filteredJobs.length }} 条 · 每页 20 条</span><ResourcePagination v-model:page="page" :page-count="pageCount" /></div>
    </div>
</template>

<style scoped>
.task-center{display:flex;min-width:0;min-height:0;width:100%;flex:1;flex-direction:column;color:var(--ncp-text)}.task-toolbar{flex-shrink:0;padding:16px 20px 12px;border-bottom:1px solid var(--ncp-line);background:var(--ncp-surface)}.task-scope{margin:0 0 14px;color:var(--ncp-text-subtle);font-size:.78rem;line-height:1.6}.task-refresh-row{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:10px}.task-observation{display:grid;gap:4px}.task-observation strong{font-size:.87rem}.task-observation span,.task-snapshot{font-size:.75rem;color:var(--ncp-text-subtle)}.task-button{display:inline-flex;min-width:44px;min-height:44px;align-items:center;justify-content:center;gap:7px;padding:8px 12px;border:1px solid var(--ncp-line);border-radius:var(--ncp-radius-control);background:var(--ncp-surface);color:var(--ncp-text-muted);font:inherit;font-size:.8rem;font-weight:650}.task-button:disabled{opacity:.55;cursor:not-allowed}.task-button:hover:not(:disabled),.task-icon-button:hover,.task-detail-toggle:hover{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}
.task-filters{display:grid;grid-template-columns:minmax(0,1.5fr) repeat(2,minmax(0,1fr));gap:10px;margin-top:14px}.task-filters label{display:grid;min-width:0;gap:6px;font-size:.75rem;color:var(--ncp-text-muted)}.task-filters input,.task-filters select{width:100%;min-width:0;min-height:44px;padding:8px 10px;border:1px solid var(--ncp-control-border);border-radius:var(--ncp-radius-control);background:var(--ncp-control-surface);color:var(--ncp-text);font:inherit;font-size:.8rem}.task-feedback{display:grid;gap:4px;margin-top:12px;font-size:.75rem;line-height:1.5}.task-warning{color:var(--ncp-warning-strong)}.task-invalid{margin:8px 0 0;font-size:.78rem}.task-list-region{overflow:auto;overscroll-behavior:contain;min-height:0;flex:1;padding:16px 20px;background:var(--ncp-surface-quiet)}.task-list{display:grid;gap:10px}.task-row{min-width:0;padding:14px 16px;border:1px solid var(--ncp-line);border-radius:var(--ncp-radius-md);background:var(--ncp-surface)}.task-row__top{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px}.task-type{font-size:.75rem;color:var(--ncp-text-subtle);overflow-wrap:anywhere}.task-state{padding:4px 9px;border-radius:var(--ncp-radius-pill);background:var(--ncp-neutral-soft);color:var(--ncp-neutral-strong);font-size:.75rem;font-weight:700}.task-state--running{background:var(--ncp-info-soft);color:var(--ncp-info-strong)}.task-state--completed{background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.task-state--failed{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.task-state--persisting,.task-state--cancelling,.task-state--interrupted{background:var(--ncp-warning-soft);color:var(--ncp-warning-strong)}.task-row h3{margin:10px 0 8px;overflow-wrap:anywhere;font-size:.9rem;font-weight:700;line-height:1.5}.task-description{margin:8px 0;color:var(--ncp-text-subtle);font-size:.78rem;line-height:1.6}.task-progress-row{display:flex;align-items:center;gap:10px;margin:8px 0;font-size:.75rem;color:var(--ncp-text-muted);font-variant-numeric:tabular-nums}.task-progress-row progress{width:100%;height:6px;accent-color:var(--ncp-primary);border:0;border-radius:var(--ncp-radius-pill);overflow:hidden}.task-progress-row progress::-webkit-progress-bar{background:var(--ncp-surface-sunken)}.task-progress-row progress::-webkit-progress-value{background:var(--ncp-primary)}.task-row__footer{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:4px 8px}.task-row time{font-size:.75rem;font-variant-numeric:tabular-nums;color:var(--ncp-text-subtle)}.task-detail-toggle{display:flex;min-height:44px;align-items:center;gap:5px;padding:6px 8px;border-radius:var(--ncp-radius-sm);background:transparent;color:var(--ncp-primary-strong);font-size:.78rem}.task-detail-toggle svg{transition:transform var(--ncp-duration-fast)}.task-detail-toggle .expanded{transform:rotate(180deg)}.task-details{padding-top:12px;border-top:1px solid var(--ncp-line-subtle)}.task-details dl{display:grid;gap:10px;margin:0}.task-details dl>div{display:grid;grid-template-columns:90px minmax(0,1fr);gap:10px;font-size:.78rem;line-height:1.6}.task-details dt{color:var(--ncp-text-subtle)}.task-details dd{min-width:0;margin:0;white-space:pre-wrap;overflow-wrap:anywhere}.task-mono{font-family:var(--ncp-font-mono)}.task-cancel{margin-top:12px}.task-cancel-error{margin:10px 0 0;color:var(--ncp-danger-strong);font-size:.78rem;overflow-wrap:anywhere;white-space:pre-wrap}.task-pagination{display:flex;flex-shrink:0;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:8px;padding:12px 20px;border-top:1px solid var(--ncp-line);font-size:.75rem;color:var(--ncp-text-subtle);font-variant-numeric:tabular-nums}.task-empty-filter{display:grid;justify-items:center;gap:12px}.task-empty-filter>.resource-state{width:100%}.task-list-region :deep(.resource-state button){min-height:44px}.task-center button:focus-visible,.task-center input:focus-visible,.task-center select:focus-visible{outline:2px solid var(--ncp-primary);outline-offset:2px}.spin{animation:task-spin 1s linear infinite}@keyframes task-spin{to{transform:rotate(360deg)}}
.task-description.task-warning{color:var(--ncp-warning-strong)}
@media(max-width:560px){.task-filters{grid-template-columns:repeat(2,minmax(0,1fr))}.task-search{grid-column:1/-1}.task-toolbar,.task-list-region{padding:12px 14px}.task-row{padding:12px}.task-details dl>div{grid-template-columns:1fr;gap:2px}.task-pagination{padding:10px 14px}}
@media(max-height:640px){:global(.task-center-drawer > .task-center-drawer__body){display:block;overflow:auto;overscroll-behavior:contain}.task-center{display:block;overflow:visible}.task-list-region{overflow:visible}.task-pagination{flex-shrink:0}}
@media(prefers-reduced-motion:reduce){.spin{animation:none}.task-detail-toggle svg{transition:none}.task-center-drawer{transition:none}}
</style>
