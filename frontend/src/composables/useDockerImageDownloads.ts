import { computed, ref, watch, type Ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import {
  NcpApiError,
  type DockerImageSummary,
  type JobSnapshot,
} from '@/api/system'
import {
  cancelJob,
  deleteJob,
  followJob,
  requestJobs,
  retryJob,
} from '@/api/docker'
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
    { label: '已完成', value: 'completed' },
    { label: '已停止', value: 'cancelled' },
    { label: '已删除', value: 'deleted' },
    { label: '失败', value: 'failed' },
    { label: '已中断', value: 'interrupted' },
  ]
  const { pageSize: downloadPageSize } = useListPreference('docker.image-downloads')

  const activePullCount = computed(() => downloadJobs.value.filter((job) => job.status === 'queued' || job.status === 'running').length)
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

  function upsertDownloadJob(job: JobSnapshot) {
    const index = downloadJobs.value.findIndex((item) => item.id === job.id)
    if (index >= 0) downloadJobs.value[index] = job
    else downloadJobs.value.unshift(job)
    downloadJobs.value = [...downloadJobs.value]
  }

  function localImageForJob(job: JobSnapshot) {
    const reference = (job.reference ?? '').replace(/^docker\.io\//, '')
    return images.value.find((image) => image.repoTags.some((tag) => tag === reference || tag.replace(/^docker\.io\//, '') === reference))
  }

  function downloadState(job: JobSnapshot) {
    if (job.artifactState === 'deleted') return 'deleted'
    if (job.status === 'completed' && job.artifactState !== 'present' && !localImageForJob(job)) return 'deleted'
    return job.status
  }
  function downloadStateLabel(job: JobSnapshot) {
    const state = downloadState(job)
    return state === 'queued' ? '排队中' : state === 'running' ? '下载中' : state === 'completed' ? '已完成' : state === 'deleted' ? '已删除' : state === 'cancelled' ? '已停止' : state === 'interrupted' ? '已中断' : '失败'
  }
  function effectiveTotal(job: JobSnapshot) {
    return job.totalBytes || localImageForJob(job)?.sizeBytes || 0
  }
  function dateLabel(value: string) {
    const date = new Date(value)
    return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
  }

  async function loadDownloadJobs() {
    try {
      const result = await requestJobs('docker-image-pull')
      downloadJobs.value = result.jobs
      invalidDownloadJobCount.value = result.invalidCount
      for (const job of downloadJobs.value.filter((item) => item.status === 'queued' || item.status === 'running')) {
        void followJob(job.id, upsertDownloadJob).catch(() => undefined)
      }
    } catch {
      downloadJobs.value = []
      invalidDownloadJobCount.value = 0
    }
  }

  async function retryDownload(job: JobSnapshot) {
    try {
      const next = await retryJob(job.id)
      upsertDownloadJob(next)
      void followJob(next.id, upsertDownloadJob).catch(() => undefined)
    } catch (caught) {
      ElMessage.error(caught instanceof NcpApiError ? caught.message : '任务重试失败。')
    }
  }

  async function stopDownload(job: JobSnapshot) {
    try {
      await cancelJob(job.id)
      ElMessage.success('已发送停止下载请求')
    } catch (caught) {
      ElMessage.error(caught instanceof NcpApiError ? caught.message : '停止下载失败。')
    }
  }

  async function confirmDeleteJob(job: JobSnapshot) {
    try {
      await ElMessageBox.confirm(`仅删除“${job.reference || '未命名镜像'}”的下载记录，不会删除本地镜像。`, '删除下载记录', {
        confirmButtonText: '删除记录', cancelButtonText: '取消', type: 'warning',
      })
    } catch { return }
    try {
      await deleteJob(job.id)
      downloadJobs.value = downloadJobs.value.filter((item) => item.id !== job.id)
      ElMessage.success('下载记录已删除')
    } catch (caught) {
      ElMessage.error(caught instanceof NcpApiError ? caught.message : '下载记录删除失败。')
    }
  }

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
    downloadState,
    downloadStateLabel,
    effectiveTotal,
    dateLabel,
  }
}
