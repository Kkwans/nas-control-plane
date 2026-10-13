import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import {
  NcpApiError,
  type DockerImageSummary,
  type JobSnapshot,
} from '@/api/system'
import {
  cancelJob,
  deleteJob,
  requestJobs,
  retryJob,
  subscribeJob,
  type JobSubscription,
} from '@/api/docker'
import { captureSession } from '@/session/sessionLifecycle'
import { useListPreference } from '@/composables/useListPreference'

export function useDockerImageDownloads(images: Ref<DockerImageSummary[]>) {
  const downloadJobs = ref<JobSnapshot[]>([])
  const invalidDownloadJobCount = ref(0)
  const downloadStatus = ref('all')
  const downloadQuery = ref('')
  const downloadPage = ref(1)
  const downloadStatusOptions = [
    { label: '全部状态', value: 'all' },
    { label: '排队中', value: 'queued' },
    { label: '下载中', value: 'running' },
    { label: '取消中', value: 'cancelling' },
    { label: '结果保存中', value: 'persisting' },
    { label: '已完成', value: 'completed' },
    { label: '已停止', value: 'cancelled' },
    { label: '已删除', value: 'deleted' },
    { label: '失败', value: 'failed' },
    { label: '结果未确认', value: 'interrupted' },
  ]
  const { pageSize: downloadPageSize } = useListPreference('docker.image-downloads')
  const subscriptions = new Map<string, JobSubscription>()

  const session = captureSession()
  let disposed = false
  let listSequence = 0
  let mutation = 0
  const versions = new Map<string, number>()
  const deletedJobs = new Set<string>()
  const alive = () => !disposed && !session.signal.aborted
  const retryingJobs = ref(new Set<string>())
  const deletingJobs = ref(new Set<string>())
  const cancellingJobs = ref(new Set<string>())
  const isLiveJob = (job: JobSnapshot) => job.status === 'queued' || job.status === 'running' || job.persistenceStatus === 'pending'
  const canCancelJob = (job: JobSnapshot) => alive() && isLiveJob(job) && job.persistenceStatus !== 'pending' && job.cancellable !== false && job.cancellationStatus !== 'requested' && !cancellingJobs.value.has(job.id)
  const canDeleteJob = (job: JobSnapshot) => alive() && !isLiveJob(job) && !deletingJobs.value.has(job.id)
  const canRetryJob = (job: JobSnapshot) => alive() && !isLiveJob(job) && !retryingJobs.value.has(job.id)
  const activePullCount = computed(() => downloadJobs.value.filter(isLiveJob).length)
  const filteredDownloadJobs = computed(() => {
    const term = downloadQuery.value.trim().toLowerCase()
    return downloadJobs.value.filter((job) =>
      (downloadStatus.value === 'all' || downloadState(job) === downloadStatus.value) &&
      (!term || `${job.reference ?? ''} ${job.message} ${job.error ?? ''}`.toLowerCase().includes(term)),
    )
  })
  const downloadPageCount = computed(() => Math.max(1, Math.ceil(filteredDownloadJobs.value.length / downloadPageSize.value)))
  const pagedDownloadJobs = computed(() => {
    const start = (downloadPage.value - 1) * downloadPageSize.value
    return filteredDownloadJobs.value.slice(start, start + downloadPageSize.value)
  })

  const resetPage = () => { downloadPage.value = 1 }
  // Keep filter/pagination coupling explicit so a changed filter never leaves
  // the user on a now-empty page.
  watch([downloadStatus, downloadPageSize, downloadQuery], resetPage)
  watch(downloadPageCount, (count) => {
    if (downloadPage.value > count) downloadPage.value = count
  })

  function upsertDownloadJob(job: JobSnapshot): JobSnapshot | undefined {
    if (!alive() || deletedJobs.has(job.id)) return
    const index = downloadJobs.value.findIndex((item) => item.id === job.id)
    const current = downloadJobs.value[index]
    // 终态不可倒退；旧 HTTP 响应不能覆盖较新的 SSE 状态。
    if (current && ((!isLiveJob(current) && (isLiveJob(job) || current.status !== job.status)) ||
      Date.parse(current.updatedAt) > Date.parse(job.updatedAt) ||
      (isLiveJob(job) && ((current.persistenceStatus === 'pending' && job.persistenceStatus !== 'pending') ||
        (current.cancellationStatus === 'requested' && job.cancellationStatus !== 'requested') ||
        (current.status === 'running' && job.status === 'queued'))))) return current
    if (index >= 0) downloadJobs.value[index] = job
    else downloadJobs.value.unshift(job)
    versions.set(job.id, ++mutation)
    downloadJobs.value = [...downloadJobs.value]
    return job
  }

  function localImageForJob(job: JobSnapshot) {
    const reference = (job.reference ?? '').replace(/^docker\.io\//, '')
    return images.value.find((image) => image.repoTags.some((tag) => tag === reference || tag.replace(/^docker\.io\//, '') === reference))
  }

  function downloadState(job: JobSnapshot) {
    if (job.persistenceStatus === 'pending') return 'persisting'
    if (isLiveJob(job) && job.cancellationStatus === 'requested') return 'cancelling'
    if (job.cancellationStatus === 'unknown' || job.status === 'interrupted') return 'interrupted'
    if (job.artifactState === 'deleted') return 'deleted'
    if (job.status === 'completed' && job.artifactState !== 'present' && !localImageForJob(job)) return 'deleted'
    return job.status
  }
  function downloadStateLabel(job: JobSnapshot) {
    const state = downloadState(job)
    return state === 'persisting' ? '结果保存中' : state === 'cancelling' ? '取消中' : state === 'queued' ? '排队中' : state === 'running' ? '下载中' : state === 'completed' ? '已完成' : state === 'deleted' ? '已删除' : state === 'cancelled' ? '已停止' : state === 'interrupted' ? '结果未确认' : '失败'
  }
  function effectiveTotal(job: JobSnapshot) {
    return job.totalBytes || localImageForJob(job)?.sizeBytes || 0
  }
  function dateLabel(value: string) {
    const date = new Date(value)
    return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
  }

  function closeSubscription(jobId: string) {
    const subscription = subscriptions.get(jobId)
    if (!subscription) return
    subscriptions.delete(jobId)
    subscription.close()
  }

  function followDownloadJob(job: JobSnapshot) {
    if (!alive() || deletedJobs.has(job.id)) return
    if (!isLiveJob(job)) {
      closeSubscription(job.id)
      return
    }
    if (subscriptions.has(job.id)) return
    const subscription = subscribeJob(job.id, upsertDownloadJob)
    subscriptions.set(job.id, subscription)
    void subscription.done.catch(() => undefined).finally(() => {
      if (subscriptions.get(job.id) === subscription) subscriptions.delete(job.id)
    })
  }

  async function loadDownloadJobs() {
    if (!alive()) return
    const sequence = ++listSequence
    const startedAt = mutation
    try {
      const result = await requestJobs('docker-image-pull')
      if (!alive() || sequence !== listSequence) return
      const listedIds = new Set(result.jobs.map((job) => job.id))
      downloadJobs.value = downloadJobs.value.filter((job) => listedIds.has(job.id) || isLiveJob(job) || (versions.get(job.id) ?? 0) > startedAt)
      for (const job of result.jobs) {
        if ((versions.get(job.id) ?? 0) <= startedAt) upsertDownloadJob(job)
      }
      invalidDownloadJobCount.value = result.invalidCount
      const activeIds = new Set(downloadJobs.value.filter(isLiveJob).map((item) => item.id))
      for (const jobId of subscriptions.keys()) if (!activeIds.has(jobId)) closeSubscription(jobId)
      for (const job of downloadJobs.value) followDownloadJob(job)
    } catch {
      // 历史刷新失败不清除已确认的快照，也不关闭仍有效的进度流。
    }
  }

  async function retryDownload(job: JobSnapshot) {
    if (!canRetryJob(job)) return
    retryingJobs.value.add(job.id)
    try {
      if (job.status === 'interrupted' || job.cancellationStatus === 'unknown') {
        try {
          await ElMessageBox.confirm('远端执行结果尚未确认，操作可能已经生效。请先检查本地镜像状态，再决定是否重新下载。', '确认重试', { confirmButtonText: '重新下载', cancelButtonText: '返回检查', type: 'warning' })
        } catch { return }
      }
      if (!alive()) return
      const next = await retryJob(job.id)
      const current = upsertDownloadJob(next)
      if (current) followDownloadJob(current)
    } catch (caught) {
      if (alive()) ElMessage.error(caught instanceof NcpApiError ? caught.message : '任务重试失败。')
    } finally {
      retryingJobs.value.delete(job.id)
    }
  }

  async function stopDownload(job: JobSnapshot) {
    if (!canCancelJob(job)) return
    cancellingJobs.value.add(job.id)
    try {
      const next = await cancelJob(job.id)
      const current = upsertDownloadJob(next)
      if (!current) return
      followDownloadJob(current)
      if (current.status === 'cancelled') ElMessage.success('任务已取消')
      else if (isLiveJob(current)) ElMessage.info('取消请求已接收，远端执行可能仍在继续。')
    } catch (caught) {
      if (alive()) ElMessage.error(caught instanceof NcpApiError ? caught.message : '停止下载失败。')
    } finally {
      cancellingJobs.value.delete(job.id)
    }
  }

  async function confirmDeleteJob(job: JobSnapshot) {
    if (!canDeleteJob(job)) return
    deletingJobs.value.add(job.id)
    try {
      try {
        await ElMessageBox.confirm(`仅删除“${job.reference || '未命名镜像'}”的下载记录，不会删除本地镜像。`, '删除下载记录', {
          confirmButtonText: '删除记录', cancelButtonText: '取消', type: 'warning',
        })
      } catch { return }
      if (!alive()) return
      await deleteJob(job.id)
      if (!alive()) return
      deletedJobs.add(job.id)
      versions.set(job.id, ++mutation)
      closeSubscription(job.id)
      downloadJobs.value = downloadJobs.value.filter((item) => item.id !== job.id)
      ElMessage.success('下载记录已删除')
    } catch (caught) {
      if (alive()) ElMessage.error(caught instanceof NcpApiError ? caught.message : '下载记录删除失败。')
    } finally {
      deletingJobs.value.delete(job.id)
    }
  }

  function dispose() {
    disposed = true
    for (const subscription of subscriptions.values()) subscription.close()
    subscriptions.clear()
    session.signal.removeEventListener('abort', dispose)
  }
  session.signal.addEventListener('abort', dispose, { once: true })
  onScopeDispose(dispose)

  return {
    downloadJobs,
    invalidDownloadJobCount,
    downloadStatus,
    downloadQuery,
    downloadPage,
    downloadPageSize,
    downloadStatusOptions,
    activePullCount,
    filteredDownloadJobs,
    downloadPageCount,
    pagedDownloadJobs,
    resetPage,
    upsertDownloadJob,
    loadDownloadJobs,
    retryDownload,
    stopDownload,
    confirmDeleteJob,
    isLiveJob,
    canCancelJob,
    canDeleteJob,
    canRetryJob,
    downloadState,
    downloadStateLabel,
    effectiveTotal,
    dateLabel,
  }
}
