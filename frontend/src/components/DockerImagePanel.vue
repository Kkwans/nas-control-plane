<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ArrowDown, ArrowUp, ArrowUpDown, Box, Download, Eye, HardDrive, Image as ImageIcon, PackagePlus, RefreshCw, RotateCcw, Search, Square, Trash2 } from '@lucide/vue'
import { ElInput, ElMessage, ElTooltip } from 'element-plus'

import {
  type DockerImageSummary,
  type DockerInventory,
  type JobSnapshot,
} from '@/api/system'
import TaskCenterDrawer from '@/components/TaskCenterDrawer.vue'
import ActionButton from '@/components/ActionButton.vue'
import CreateContainerDrawer from '@/components/CreateContainerDrawer.vue'
import DockerHubBrowser from '@/components/DockerHubBrowser.vue'
import ListIconButton from '@/components/ListIconButton.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import NcpSelect from '@/components/NcpSelect.vue'
import SectionTabs, { type SectionTab } from '@/components/SectionTabs.vue'
import { useDockerImageDownloads } from '@/composables/useDockerImageDownloads'
import { useDockerLocalImages } from '@/composables/useDockerLocalImages'
import { formatBytes } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'

const props = defineProps<{ query: string; containers: DockerInventory['containers']; pageSize: number }>()
const emit = defineEmits<{ 'update:query': [value: string] }>()
type ImageMode = 'local' | 'hub' | 'downloads'
const systemStore = useSystemStore()
let alive = true
onBeforeUnmount(() => { alive = false })

const mode = ref<ImageMode>('local')
const removalTasksOpen = ref(false)
const createImage = ref<DockerImageSummary | null>(null)
const createDrawerOpen = ref(false)
const localImages = useDockerLocalImages({ active: computed(() => mode.value === 'local'), query: computed(() => props.query), containers: computed(() => props.containers), pageSize: computed(() => props.pageSize) })
const downloads = useDockerImageDownloads(localImages.images, { onCreatedDownloadCompleted: () => localImages.refresh() })
const {
  images, loading, error, removeAdmissions, removeFailures, selectedImageIds, localPage, localPreference,
  filteredImages, localPageCount, pagedImages, allVisibleSelected,
  someVisibleSelected, selectedImageCount, bulkRemovePending, toggleLocalSort, refresh,
  handleVisibleSelectionChange, handleImageSelectionChange, isRemovePending, confirmRemove,
  confirmRemoveSelected, containerReferenceCount, displayName, shortId, dateLabel,
} = localImages
const {
  downloadJobs, downloadJobsLoading, downloadJobsLoaded, downloadJobsError, downloadJobsLastSuccessAt,
  isDownloadObservationInterrupted, downloadLastObservedAt, canShowDownloadSpeed,
  invalidDownloadJobCount, downloadStatus, downloadQuery, downloadPage,
  downloadStatusOptions, activePullCount, filteredDownloadJobs, downloadPageCount,
  pagedDownloadJobs, observeCreatedDownload, loadDownloadJobs, retryDownload, stopDownload,
  confirmDeleteJob, isLiveJob, canCancelJob, canDeleteJob, canRetryJob, downloadState, downloadStateLabel, downloadArtifactLabel, effectiveTotal,
} = downloads
const imageModeTabs = computed<SectionTab[]>(() => [
  { value: 'local', label: '本地镜像', icon: HardDrive, count: images.value.length },
  { value: 'hub', label: '线上仓库', icon: Box },
  { value: 'downloads', label: '下载任务', icon: Download, count: activePullCount.value },
])

function openCreateDrawer(image: DockerImageSummary) {
  createImage.value = image
  createDrawerOpen.value = true
}

async function handleContainerCreated() {
  if (!alive) return
  const session = captureSession()
  const refreshResult = await systemStore.refresh({ inventory: true, force: true })
  // 创建对账跨两个读取阶段；页面或会话离开后不再提示或启动下一次读取。
  if (!alive || !isCurrentSession(session.generation)) return
  if (refreshResult.failed.length) {
    ElMessage.warning('容器已创建，但状态刷新失败，请手动重新加载。')
  }
  await refresh()
}

function handleHubJobCreated(job: JobSnapshot) {
  observeCreatedDownload(job)
}

function showDownloadedImage(job: JobSnapshot) {
  mode.value = 'local'
  emit('update:query', (job.reference ?? '').split('@')[0] ?? '')
}

function observationTime(value: string | null) {
  if (!value) return '尚未获取'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

function sortIcon(key: 'name' | 'size' | 'created' | 'containers') {
  if (localPreference.value.sortKey !== key) return ArrowUpDown
  return localPreference.value.sortDirection === 'desc' ? ArrowDown : ArrowUp
}

onMounted(() => {
  void refresh()
  void loadDownloadJobs()
})
</script>

<template>
  <TaskCenterDrawer v-if="removalTasksOpen" @close="removalTasksOpen = false" />
  <section class="image-workspace">
    <header class="image-modebar">
      <SectionTabs v-model="mode" :tabs="imageModeTabs" accessible-label="镜像来源" />
      <div v-if="mode === 'local'" class="mode-actions">
        <ElInput :model-value="query" clearable aria-label="搜索本地镜像" placeholder="搜索镜像名称、标签或 ID" @update:model-value="emit('update:query', String($event))">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
        <span v-if="selectedImageCount" class="selection-count">已选 {{ selectedImageCount }} 个</span>
        <ActionButton v-if="selectedImageCount" variant="danger" size="sm" :icon="Trash2" :loading="bulkRemovePending" @click="confirmRemoveSelected">批量删除</ActionButton>
        <ElTooltip content="刷新本地镜像"><button class="icon-button" type="button" :disabled="loading" aria-label="刷新本地镜像" @click="refresh"><RefreshCw :class="{ spin: loading }" :size="17" /></button></ElTooltip>
      </div>
      <div v-else-if="mode === 'downloads'" class="mode-actions download-mode-actions">
        <ElInput v-model="downloadQuery" clearable aria-label="搜索镜像下载任务" placeholder="搜索镜像或任务消息">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
        <NcpSelect v-model="downloadStatus" :options="downloadStatusOptions" accessible-label="下载任务状态" />
        <ElTooltip content="刷新下载任务"><button class="icon-button" type="button" :disabled="downloadJobsLoading" :aria-busy="downloadJobsLoading" aria-label="刷新下载任务" @click="loadDownloadJobs"><RefreshCw :class="{ spin: downloadJobsLoading }" :size="17" /></button></ElTooltip>
      </div>
    </header>

    <div v-if="mode === 'local'">
      <div v-if="error" class="inline-error"><span>{{ error }}</span><button type="button" @click="refresh">重新加载</button></div>
      <div v-if="removeFailures.length" class="inline-error bulk-remove-error" role="alert">
        <div><strong>部分镜像删除失败</strong><ul><li v-for="failure in removeFailures" :key="failure.id">{{ failure.name }}：{{ failure.message }}</li></ul></div>
        <button type="button" @click="removeFailures = []">关闭</button>
      </div>
      <div v-for="(admission, id) in removeAdmissions" :key="id" class="inline-error removal-admission" :class="{ 'removal-admission--unknown': admission.state === 'unknown' }" role="status">
        <span>{{ admission.name }}（{{ id }}）：{{ admission.state === 'accepted' ? `删除任务已接收（${admission.jobId}），尚未确认删除完成。` : '删除任务可能已接收，结果尚未确认，请勿重复提交。' }}</span>
        <button type="button" @click="removalTasksOpen = true">查看最近任务</button>
        <button type="button" :disabled="loading" @click="refresh">只读刷新镜像</button>
      </div>
      <section class="resource-table panel">
        <div class="resource-head">
          <div class="resource-select-head">
            <input type="checkbox" :checked="allVisibleSelected" :indeterminate="someVisibleSelected && !allVisibleSelected" :aria-label="allVisibleSelected ? '取消选择当前页镜像' : '选择当前页镜像'" @change="handleVisibleSelectionChange">
            <button :class="{ active: localPreference.sortKey === 'name' }" type="button" @click="toggleLocalSort('name')">镜像<component :is="sortIcon('name')" :size="14" /></button>
          </div>
          <span>镜像 ID</span>
          <button :class="{ active: localPreference.sortKey === 'size' }" type="button" @click="toggleLocalSort('size')">大小<component :is="sortIcon('size')" :size="14" /></button>
          <button :class="{ active: localPreference.sortKey === 'created' }" type="button" @click="toggleLocalSort('created')">创建日期<component :is="sortIcon('created')" :size="14" /></button>
          <button :class="{ active: localPreference.sortKey === 'containers' }" type="button" @click="toggleLocalSort('containers')">容器引用<component :is="sortIcon('containers')" :size="14" /></button><span>操作</span>
        </div>
        <template v-if="loading && !images.length">
          <div v-for="row in 7" :key="row" class="resource-row skeleton-row"><i v-for="cell in 6" :key="cell" class="ncp-skeleton"></i></div>
        </template>
        <div v-for="image in pagedImages" :key="image.id" class="resource-row">
          <div class="resource-name-cell">
            <input type="checkbox" :checked="selectedImageIds.includes(image.id)" :aria-label="`选择镜像 ${displayName(image)}`" @change="handleImageSelectionChange(image.id, $event)">
            <div class="resource-name"><span><ImageIcon :size="18" /></span><div><strong>{{ displayName(image) }}</strong><small>{{ image.repoDigests[0] ?? '本地构建镜像' }}</small></div></div>
          </div>
          <code>{{ shortId(image.id) }}</code><span>{{ formatBytes(image.sizeBytes) }}</span><span>{{ dateLabel(image.createdAt) }}</span>
          <ElTooltip :content="containerReferenceCount(image) ? `正在被 ${containerReferenceCount(image)} 个容器引用，不能强制删除` : '删除未使用的本地镜像'">
            <span class="usage-cell">
              <span v-if="containerReferenceCount(image)" class="usage-warning">{{ containerReferenceCount(image) }} 个容器使用中</span>
              <span v-else>未被引用</span>
            </span>
          </ElTooltip>
          <div class="image-row-actions">
            <ElTooltip content="使用此镜像创建容器"><ListIconButton :icon="PackagePlus" :label="`从镜像 ${displayName(image)} 创建容器`" @click="openCreateDrawer(image)" /></ElTooltip>
            <ElTooltip :content="containerReferenceCount(image) ? '镜像正在被容器引用，先停止并移除引用' : '删除未使用的本地镜像'"><ListIconButton :icon="Trash2" tone="danger" :loading="isRemovePending(image.id)" :disabled="bulkRemovePending || containerReferenceCount(image) > 0" :label="`删除镜像 ${displayName(image)}`" @click="confirmRemove(image)" /></ElTooltip>
          </div>
        </div>
        <div v-if="!loading && !filteredImages.length" class="empty-state">没有匹配的本地镜像</div>
        <footer v-else class="resource-pagination">
          <ListPageSizeControl list-key="docker.images.local" />
          <div>
            <button type="button" :disabled="localPage <= 1" @click="localPage -= 1">上一页</button>
            <strong>{{ localPage }} / {{ localPageCount }}</strong>
            <button type="button" :disabled="localPage >= localPageCount" @click="localPage += 1">下一页</button>
          </div>
        </footer>
      </section>
    </div>

    <DockerHubBrowser
      v-show="mode === 'hub'"
      @job-created="handleHubJobCreated"
    />

    <section v-if="mode === 'downloads'" class="download-workspace panel">
      <header>
        <div>
          <strong>镜像下载任务</strong>
          <span>最多同时拉取 3 个镜像，任务进度与历史记录由服务端持久化。</span>
        </div>
        <span v-if="invalidDownloadJobCount" class="task-parse-warning">
          已忽略 {{ invalidDownloadJobCount }} 条格式异常的历史记录
        </span>
      </header>
      <div v-if="downloadJobsError" class="inline-error download-read-state" role="alert">
        <div>
          <strong>{{ downloadJobsLoaded ? '下载任务刷新失败' : '下载任务读取失败' }}</strong>
          <span>{{ downloadJobsError }}</span>
          <span v-if="downloadJobs.length">已保留上次获取的任务；当前状态可能已变化。</span>
          <span v-if="downloadJobsLastSuccessAt">上次成功获取：<time :datetime="downloadJobsLastSuccessAt">{{ observationTime(downloadJobsLastSuccessAt) }}</time></span>
        </div>
        <button type="button" :disabled="downloadJobsLoading" @click="loadDownloadJobs">{{ downloadJobsLoading ? '正在重试…' : '重新加载下载任务' }}</button>
      </div>
      <div v-else-if="downloadJobsLoading" class="download-read-state download-loading" role="status">{{ downloadJobsLoaded ? '正在刷新下载任务…' : '正在读取下载任务…' }}</div>
      <div class="download-head"><span>镜像</span><span>状态</span><span>进度</span><span>速度</span><span>更新时间</span><span>操作</span></div>
      <article v-for="job in pagedDownloadJobs" :key="job.id" class="download-row" :aria-label="`${job.reference} 下载任务`">
        <div class="job-identity"><strong>{{ job.reference }}</strong><small>{{ job.message }}</small><small v-if="job.status === 'completed'">{{ downloadArtifactLabel(job) }}</small></div>
        <div class="job-observation">
          <span :class="['job-state', `job-state--${downloadState(job)}`]">{{ downloadStateLabel(job) }}</span>
          <span v-if="isDownloadObservationInterrupted(job)" class="job-observation-warning">进度连接中断</span>
        </div>
        <div class="job-progress"><span role="progressbar" :aria-label="`${job.reference} ${isLiveJob(job) && !canShowDownloadSpeed(job) ? '上次报告进度' : '下载进度'}`" :aria-valuenow="job.progress" :aria-valuemin="0" :aria-valuemax="100"><i :style="{ width: `${job.progress}%` }"></i></span><small><template v-if="isLiveJob(job) && !canShowDownloadSpeed(job)">上次报告：</template>{{ job.progress }}% · {{ formatBytes(job.downloadedBytes || (job.progress === 100 ? effectiveTotal(job) : 0)) }} / {{ effectiveTotal(job) ? formatBytes(effectiveTotal(job)) : '未知' }}</small></div>
        <span class="job-speed"><span class="job-mobile-label">速度：</span>{{ canShowDownloadSpeed(job) && job.speedBytes > 0 ? `${formatBytes(job.speedBytes)}/s` : '—' }}</span>
        <div class="job-observed-time">
          <span><span class="job-mobile-label">更新时间：</span><time :datetime="job.updatedAt">{{ dateLabel(job.updatedAt) }}</time></span>
          <small v-if="isLiveJob(job) && !canShowDownloadSpeed(job)">上次获取：{{ observationTime(downloadLastObservedAt(job)) }}</small>
        </div>
        <div class="job-actions">
          <button v-if="isLiveJob(job)" :disabled="!canCancelJob(job)" type="button" title="请求取消下载（不保证远端立即停止）" aria-label="请求取消下载（不保证远端立即停止）" @click="stopDownload(job)"><Square :size="14" /><span class="job-mobile-label">请求取消下载</span></button>
          <button v-else-if="job.status === 'failed' || job.status === 'interrupted' || job.status === 'cancelled' || downloadState(job) === 'deleted'" type="button" :disabled="!canRetryJob(job)" title="重新下载" aria-label="重新下载" @click="retryDownload(job)"><RotateCcw :size="15" /><span class="job-mobile-label">重新下载</span></button>
          <button v-else-if="downloadState(job) === 'completed'" type="button" title="查看本地镜像" aria-label="查看本地镜像" @click="showDownloadedImage(job)"><Eye :size="15" /><span class="job-mobile-label">查看本地镜像</span></button>
          <button v-if="canDeleteJob(job)" class="job-delete" type="button" title="删除下载记录" aria-label="删除下载记录" @click="confirmDeleteJob(job)"><Trash2 :size="15" /><span class="job-mobile-label">删除下载记录</span></button>
        </div>
        <div v-if="isDownloadObservationInterrupted(job)" class="job-observation-recovery">
          <span>显示上次报告的进度，任务可能仍在执行。</span>
          <button type="button" :disabled="downloadJobsLoading" :aria-label="`重新获取 ${job.reference} 的下载进度`" @click="loadDownloadJobs">{{ downloadJobsLoading ? '正在获取…' : '重新获取进度' }}</button>
        </div>
      </article>
      <div v-if="downloadJobsLoaded && !downloadJobsLoading && !downloadJobsError && !filteredDownloadJobs.length" class="empty-state">
        <template v-if="!downloadJobs.length && invalidDownloadJobCount">下载记录无法读取，请刷新后重试。</template>
        <template v-else-if="!downloadJobs.length">暂无镜像下载任务</template>
        <template v-else>没有匹配的镜像下载任务</template>
      </div>
      <footer v-if="filteredDownloadJobs.length" class="resource-pagination">
        <ListPageSizeControl list-key="docker.image-downloads" />
        <div><button type="button" :disabled="downloadPage <= 1" @click="downloadPage -= 1">上一页</button><strong>{{ downloadPage }} / {{ downloadPageCount }}</strong><button type="button" :disabled="downloadPage >= downloadPageCount" @click="downloadPage += 1">下一页</button></div>
      </footer>
    </section>
    <CreateContainerDrawer v-model="createDrawerOpen" :image="createImage" @created="handleContainerCreated" />
  </section>
</template>

<style scoped>
.image-workspace{display:grid;grid-template-columns:minmax(0,1fr);gap:12px}.image-modebar{display:flex;min-height:58px;align-items:center;justify-content:space-between;gap:16px}.image-modebar .section-tabs{display:flex;gap:4px;padding:4px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.image-modebar .section-tabs button{display:flex;min-height:38px;align-items:center;gap:7px;padding:0 13px;border-radius:8px;color:var(--ncp-text-muted);font-size:.82rem;font-weight:720}.image-modebar .section-tabs button.active{background:#fff;box-shadow:0 3px 10px rgba(24,42,72,.08);color:var(--ncp-primary-strong)}.image-modebar .section-tabs span{padding:1px 6px;border-radius:10px;background:var(--ncp-primary-soft);font-size:.68rem}.mode-actions,.hub-search{display:flex;align-items:center;gap:8px}.mode-actions>span{color:var(--ncp-text-subtle);font-size:.78rem}.hub-search{width:min(560px,55vw)}.hub-search :deep(.el-input){flex:1}.hub-search :deep(.el-input__inner:focus){outline:0!important;box-shadow:none!important}.icon-button,.primary-button,.danger-button{display:inline-flex;min-height:40px;align-items:center;justify-content:center;gap:6px;border-radius:9px;font-size:.8rem;font-weight:720}.icon-button{width:40px;border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.primary-button{padding:0 15px;background:var(--ncp-primary);box-shadow:0 6px 16px rgba(36,104,216,.16);color:#fff}.primary-button:disabled{opacity:.5}.inline-error{display:flex;min-height:44px;align-items:center;justify-content:space-between;padding:8px 12px;border:1px solid rgba(212,81,93,.18);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.8rem}.inline-error button{font-weight:720}.resource-table{overflow:hidden}.resource-head,.resource-row{display:grid;grid-template-columns:minmax(260px,1.5fr) 130px 100px 120px 120px 100px;align-items:center;gap:12px}.resource-head{min-height:44px;padding:0 16px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.8rem;font-weight:720}.resource-head button{display:inline-flex;align-items:center;gap:5px;padding:5px 8px;border-radius:7px;color:inherit;font:inherit;transition:color .16s ease,background-color .16s ease}.resource-head button:hover{background:rgba(52,116,212,.06);color:var(--ncp-primary-strong)}.resource-head button.active{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.resource-head>:nth-child(3),.resource-head>:nth-child(4){justify-self:center}.resource-row{min-height:68px;padding:0 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.82rem}.resource-row:hover{background:var(--ncp-surface-hover)}.resource-name{display:flex;min-width:0;align-items:center;gap:10px}.resource-name>span,.repo-icon,.detail-icon{display:grid;flex:0 0 auto;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.resource-name>span{width:36px;height:36px}.resource-name>div{display:grid;min-width:0;gap:2px}.resource-name strong,.resource-name small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.resource-name strong{color:var(--ncp-text);font-size:.88rem}.resource-name small{color:var(--ncp-text-subtle);font-size:.73rem}.resource-row code{font-family:var(--ncp-font-mono);font-size:.76rem}.image-row-actions{display:flex;align-items:center;justify-content:center;gap:4px}.danger-button{min-height:34px;padding:0 9px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.skeleton-row i{width:76%;height:12px}.empty-state{display:grid;min-height:170px;place-items:center;align-content:center;padding:24px;color:var(--ncp-text-subtle);font-size:.82rem;text-align:center}
.resource-pagination{display:flex;min-height:52px;align-items:center;justify-content:space-between;gap:12px;padding:8px 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.82rem}.resource-pagination div{display:flex;align-items:center;gap:8px}.resource-pagination button{min-height:34px;padding:0 11px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-text-muted);font-weight:680}.resource-pagination button:disabled{cursor:not-allowed;opacity:.42}.resource-pagination strong{min-width:62px;color:var(--ncp-text);text-align:center}
.download-workspace{overflow:hidden}.download-workspace>header{display:flex;min-height:64px;align-items:center;justify-content:space-between;gap:16px;padding:10px 16px;border-bottom:1px solid var(--ncp-line)}.download-workspace>header>div{display:grid;gap:3px}.download-workspace>header strong{font-size:.92rem}.download-workspace>header span{color:var(--ncp-text-subtle);font-size:.75rem}.download-head,.download-row{display:grid;grid-template-columns:minmax(240px,1.25fr) 112px minmax(250px,1.1fr) 100px 120px 132px;align-items:center;gap:14px;padding:0 16px}.download-head{min-height:44px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.78rem;font-weight:720}.download-row{min-height:72px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.8rem;transition:background-color .18s ease}.download-row:hover{background:var(--ncp-surface-hover)}.download-row>div:first-child{display:grid;min-width:0;gap:3px}.download-row strong,.download-row small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.download-row strong{color:var(--ncp-text);font-size:.84rem}.download-row small{color:var(--ncp-text-subtle);font-size:.72rem}.job-state{display:inline-flex;min-height:26px;align-items:center;justify-content:center;padding:3px 10px;border:1px solid transparent;border-radius:999px;background:var(--ncp-surface-quiet);font-size:.72rem;font-weight:700;white-space:nowrap}.job-state--running{border-color:rgba(52,116,212,.14);background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.job-state--completed{border-color:rgba(35,134,111,.15);background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.job-state--queued{background:#f0edff;color:#6c55b5}.job-state--deleted{background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.job-state--failed,.job-state--interrupted{border-color:rgba(201,83,97,.14);background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.job-progress{display:grid;width:100%;min-width:0;gap:5px}.job-progress>span{overflow:hidden;width:100%;height:7px;border-radius:999px;background:#e8eef6}.job-progress i{display:block;height:100%;border-radius:inherit;background:linear-gradient(90deg,#5a8fe0,var(--ncp-primary));transition:width .2s linear}.job-progress small{font-variant-numeric:tabular-nums}.job-actions{display:flex;align-items:center;justify-content:center;gap:6px}.job-actions button{display:inline-flex;min-height:32px;align-items:center;justify-content:center;gap:5px;padding:0 9px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-primary-strong);font-weight:700;white-space:nowrap}.job-actions .job-delete{width:32px;padding:0;color:var(--ncp-danger-strong)}
.download-workspace>header .download-actions{display:flex;align-items:center;gap:8px}.download-actions select{min-height:40px;padding:0 30px 0 11px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;color:var(--ncp-text-muted);font-size:.78rem}
.mode-actions{width:min(460px,45vw)}.mode-actions :deep(.el-input){width:0;min-width:0;flex:1 1 auto}.download-mode-actions{width:min(620px,58vw)}.download-mode-actions :deep(.ncp-select){width:150px;flex:0 0 150px}
.resource-head button{width:100%;min-height:32px;justify-content:flex-start;padding:0;border:0;background:transparent;box-shadow:none}.resource-head>button:nth-child(3),.resource-head>button:nth-child(4){justify-content:center}.resource-head>:nth-child(3),.resource-head>:nth-child(4),.resource-head>:nth-child(5),.resource-head>:nth-child(6),.resource-row>:nth-child(3),.resource-row>:nth-child(4),.resource-row>:nth-child(5),.resource-row>:nth-child(6){justify-self:center;text-align:center}.resource-head>span,.resource-head>button{background:transparent}
.download-head>:nth-child(n+2),.download-row>:nth-child(n+2){justify-self:center;text-align:center}.download-row .job-state{justify-self:center}.job-live{color:var(--ncp-success-strong);font-size:.72rem;font-weight:680}
.resource-head button.active{background:transparent;color:var(--ncp-primary-strong);font-weight:800}
.tag-title :deep(.ncp-select){width:min(310px,48%)}
.tag-picker{overflow:auto;padding-right:2px}
.download-head,.download-row{grid-template-columns:minmax(240px,1.25fr) 112px minmax(190px,260px) 100px 120px 112px}
.job-state--queued{border-color:#cbd5e1;background:#eef2f7;color:#52657d}
.job-state--cancelled{border-color:#ead1a7;background:#fff4e3;color:#9a5b13}
.job-state--deleted{border-color:#d5dce6;background:#edf1f5;color:#5f6f83}
.job-state--failed{border-color:rgba(201,83,97,.28);background:#fcecef;color:#a83d4b}
.job-state--interrupted{border-color:#e8c98b;background:#fff3d9;color:#996117}
.resource-select-head{display:flex;min-width:0;align-items:center;gap:8px}.resource-name-cell{display:flex;min-width:0;align-items:center;gap:8px}.resource-select-head input,.resource-name-cell>input{width:16px;height:16px;flex:0 0 auto;accent-color:var(--ncp-primary)}.usage-cell{display:grid;justify-items:center;gap:2px;line-height:1.25}.usage-warning{color:var(--ncp-danger-strong);font-size:.72rem;font-weight:720}.bulk-remove-error>div{min-width:0}.bulk-remove-error ul{margin:4px 0 0;padding-left:17px}.selection-count{white-space:nowrap;font-variant-numeric:tabular-nums}
@media(max-width:1050px){.resource-head,.resource-row{grid-template-columns:minmax(220px,1.4fr) 110px 85px 105px 105px 100px;gap:8px}}
@media(max-width:820px){.resource-table{overflow-x:auto}.resource-head,.resource-row{min-width:780px}.tag-title{align-items:stretch;flex-direction:column}.tag-title :deep(.ncp-select){width:100%}.tag-picker dl{grid-template-columns:1fr}.tag-picker dl>div:nth-child(even){border-left:0}.tag-picker dl>div:nth-child(n+2){border-top:1px solid var(--ncp-line)}.tag-grid{grid-template-columns:1fr}}
</style>

<style scoped>
.inline-error.removal-admission{flex-wrap:wrap;gap:8px;margin-bottom:10px;background:var(--ncp-info-soft);color:var(--ncp-info-strong);border-color:var(--ncp-line)}.removal-admission>span{flex:1 1 320px;min-width:0;overflow-wrap:anywhere}.removal-admission button{min-height:44px;padding:6px 10px}.inline-error.removal-admission--unknown{background:var(--ncp-warning-soft);color:var(--ncp-warning-strong)}
.image-workspace { container: image-workspace / inline-size; }
/* 工具栏根据可用面板宽度和字号换行，标签不能依赖横向滚动才能读全。 */
.image-modebar { min-width: 0; flex-wrap: wrap; }
.image-modebar .section-tabs { min-width: 0; flex-wrap: wrap; overflow: visible; }
.image-modebar :deep(.section-tabs button) { min-width: 0; max-width: 100%; white-space: normal; }
.image-modebar :deep(.section-tabs button>span) { min-width: 0; overflow-wrap: anywhere; }
.image-modebar :deep(.section-tabs button>svg),.image-modebar :deep(.section-tabs button>small) { flex: 0 0 auto; }
.mode-actions { min-width: 0; max-width: 100%; flex-wrap: wrap; }
.mode-actions :deep(.el-input) { min-width: min(100%, 11.25rem); max-width: 100%; flex: 1 1 11.25rem; }
.mode-actions .icon-button { flex: 0 0 40px; }
.download-mode-actions :deep(.ncp-select) { width: max(150px, 9.375rem); min-width: 0; flex: 0 1 max(150px, 9.375rem); }
.download-mode-actions :deep(.el-select__wrapper) { line-height: 1.4; }
.download-mode-actions :deep(.el-select__placeholder) { position: static; transform: none; white-space: normal; overflow: visible; overflow-wrap: anywhere; text-overflow: clip; }
@container image-workspace (max-width:980px) or (max-width:61.25rem) {
  .image-modebar { align-items: stretch; flex-direction: column; gap: 8px; }
  .image-modebar .section-tabs { width: 100%; }
  .image-modebar :deep(.section-tabs button) { min-height: 44px; flex: 1 1 auto; }
  .mode-actions,.download-mode-actions { width: 100%; }
  .mode-actions .icon-button { width: 44px; min-height: 44px; flex-basis: 44px; }
  .mode-actions :deep(.el-input__wrapper),.download-mode-actions :deep(.el-select__wrapper) { min-height: 44px; }
}
.download-workspace { container: image-downloads / inline-size; }
.download-read-state { margin: 12px 16px; gap: 12px; }
.download-read-state>div { display: grid; min-width: 0; gap: 4px; overflow-wrap: anywhere; }
.download-read-state button,.job-observation-recovery button { min-height: 40px; flex: 0 0 auto; padding: 6px 10px; border: 1px solid currentColor; border-radius: 8px; font-weight: 700; }
.download-loading { color: var(--ncp-text-muted); font-size: .82rem; }
.download-mode-actions button:disabled,.download-read-state button:disabled,.job-observation-recovery button:disabled { cursor: not-allowed; opacity: .5; }
.job-observation,.job-observed-time { display: grid; gap: 4px; justify-items: center; }
.job-observation-warning { color: var(--ncp-danger-strong); font-size: .75rem; }
.download-row .job-observed-time small { white-space: normal; overflow-wrap: anywhere; }
.download-row .job-observation-recovery { grid-column: 1 / -1; justify-self: stretch; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 0 0 12px; color: var(--ncp-text-muted); text-align: left; }
.job-observation-recovery button { color: var(--ncp-primary-strong); }
.job-mobile-label { display: none; }
/* 同时保护固定列最小宽度与放大文字；按面板实际空间切换，不依赖侧栏状态。 */
@container image-downloads (max-width:980px) or (max-width:61.25rem) {
  .download-workspace>header { min-width: 0; align-items: flex-start; flex-direction: column; gap: 6px; overflow-wrap: anywhere; }
  .download-workspace>header>div { min-width: 0; }
  .download-read-state { align-items: stretch; flex-direction: column; }
  .download-head { display: none; }
  .download-row { min-width: 0; grid-template-columns: minmax(0, 1fr); gap: 10px; padding: 16px; }
  .download-row>:nth-child(n) { min-width: 0; max-width: 100%; justify-self: stretch; text-align: left; }
  .download-row strong,.download-row small { overflow: visible; white-space: normal; overflow-wrap: anywhere; }
  .download-row .job-state { justify-self: start; white-space: normal; }
  .job-observation { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
  .job-observed-time { justify-items: start; overflow-wrap: anywhere; }
  .job-mobile-label { display: inline; }
  .job-speed { overflow-wrap: anywhere; }
  .job-actions { justify-content: flex-start; flex-wrap: wrap; }
  .job-actions button,.job-actions .job-delete { width: auto; min-width: 44px; min-height: 44px; padding: 8px 12px; white-space: normal; }
  .job-actions button svg { flex: 0 0 auto; }
  .download-row .job-observation-recovery { align-items: stretch; flex-direction: column; gap: 8px; padding: 12px; border-radius: var(--ncp-radius-md); background: var(--ncp-surface-quiet); overflow-wrap: anywhere; }
  .download-read-state button,.job-observation-recovery button { min-height: 44px; white-space: normal; overflow-wrap: anywhere; }
  .download-workspace .resource-pagination,.download-workspace .resource-pagination>div { flex-wrap: wrap; }
  .download-workspace .resource-pagination button { min-height: 44px; }
}
.resource-select-head button { width: auto; min-width: 0; flex: 1; }
.resource-head>button:nth-child(5) { justify-content: center; }
.resource-name-cell .resource-name { flex: 1; }
.mode-actions :deep(.action-button) { flex: 0 0 auto; }
</style>
