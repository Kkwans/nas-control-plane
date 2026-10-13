import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'
import { cancelJob, requestJobs } from '@/api/docker'
import { NcpApiError, type JobSnapshot } from '@/api/system'
import { captureSession } from '@/session/sessionLifecycle'
import { createRequestSequenceGate } from '@/domain/requestSequence'
import { canRequestJobCancellation, jobState, jobStatusOptions, jobTypeLabel, mergeJobSnapshot, sortJobs } from '@/domain/jobPresentation'

export interface TaskCancelError { code: string; message: string; reference: string }

export function useTaskCenter(open: Ref<boolean> = ref(true)) {
  const jobs = ref<JobSnapshot[]>([])
  const invalidCount = ref(0)
  const hasLoaded = ref(false)
  const loading = ref(false)
  const refreshing = ref(false)
  const stale = ref(true)
  const error = ref('')
  const lastFetchedAt = ref<Date | null>(null)
  const typeFilter = ref('all')
  const statusFilter = ref('all')
  const query = ref('')
  const page = ref(1)
  const pageSize = ref(20)
  const cancellingIds = ref(new Set<string>())
  const cancelErrors = ref<Record<string, TaskCancelError>>({})
  const statusOptions = jobStatusOptions
  const typeOptions = computed(() => [{ value: 'all', label: '全部类型' }, ...Array.from(new Set(jobs.value.map((job) => job.type))).sort().map((type) => ({ value: type, label: jobTypeLabel(type) }))])
  const filteredJobs = computed(() => {
    const term = query.value.trim().toLocaleLowerCase()
    return jobs.value.filter((job) => (typeFilter.value === 'all' || job.type === typeFilter.value) &&
      (statusFilter.value === 'all' || jobState(job) === statusFilter.value) &&
      (!term || [job.id, job.type, jobTypeLabel(job.type), job.reference, job.message, job.error, job.errorCode].join(' ').toLocaleLowerCase().includes(term)))
  })
  const pageCount = computed(() => Math.max(1, Math.ceil(filteredJobs.value.length / pageSize.value)))
  const pagedJobs = computed(() => filteredJobs.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
  watch([typeFilter, statusFilter, query, pageSize], () => { page.value = 1 }, { flush: 'sync' })
  watch(pageCount, (count) => { page.value = Math.min(page.value, count) }, { flush: 'sync' })

  const session = captureSession()
  const reads = createRequestSequenceGate()
  const visible = ref(typeof document === 'undefined' || document.visibilityState !== 'hidden')
  let disposed = false
  let generation = 0
  let mutation = 0
  let failures = 0
  let inFlight = false
  let pendingRefresh = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const versions = new Map<string, number>()
  const alive = (owner = generation) => !disposed && !session.signal.aborted && open.value && owner === generation
  const observing = () => alive() && visible.value
  function clearTimer() { if (timer !== undefined) clearTimeout(timer); timer = undefined }
  function schedule() {
    clearTimer()
    if (observing()) timer = setTimeout(() => { void refresh() }, failures ? Math.min(60_000, 30_000 * 2 ** (failures - 1)) : 15_000)
  }
  async function refresh() {
    if (!observing() || inFlight) return
    clearTimer()
    inFlight = true
    pendingRefresh = false
    const owner = generation
    const sequence = reads.begin()
    const startedAt = mutation
    const valid = () => alive(owner) && visible.value && reads.isLatest(sequence)
    loading.value = !hasLoaded.value
    refreshing.value = hasLoaded.value
    try {
      const result = await requestJobs()
      if (!valid()) return
      const current = new Map(jobs.value.map((job) => [job.id, job]))
      const listed = new Set(result.jobs.map((job) => job.id))
      const next = result.jobs.map((job) => (versions.get(job.id) ?? 0) > startedAt ? current.get(job.id) ?? job : mergeJobSnapshot(current.get(job.id), job))
      // 只临时保护读取开始后改变的记录；下一份快照缺席即移出当前列表。
      for (const job of jobs.value) if (!listed.has(job.id) && (versions.get(job.id) ?? 0) > startedAt) next.push(job)
      jobs.value = sortJobs(next)
      for (const id of versions.keys()) if (!jobs.value.some((job) => job.id === id)) versions.delete(id)
      invalidCount.value = result.invalidCount
      hasLoaded.value = true
      lastFetchedAt.value = new Date()
      stale.value = false
      error.value = ''
      failures = 0
    } catch (caught) {
      if (!valid()) return
      failures += 1
      stale.value = true
      error.value = caught instanceof NcpApiError ? caught.message : '任务列表读取失败，请重试。'
    } finally {
      inFlight = false
      if (valid()) { loading.value = false; refreshing.value = false }
      if (pendingRefresh && observing()) { pendingRefresh = false; void refresh() }
      else if (valid()) schedule()
    }
  }
  function canCancel(job: JobSnapshot): boolean {
    const current = jobs.value.find((item) => item.id === job.id)
    return observing() && !stale.value && !!current && canRequestJobCancellation(current) && !cancellingIds.value.has(job.id)
  }
  async function requestCancel(job: JobSnapshot) {
    if (!canCancel(job)) return
    const owner = generation
    const id = job.id
    const target = jobs.value.find((item) => item.id === id)!
    const reference = target.reference || id
    cancellingIds.value.add(id)
    delete cancelErrors.value[id]
    try {
      const next = await cancelJob(id)
      if (!alive(owner)) return
      if (next.id !== id || next.type !== target.type || next.reference !== target.reference) throw new NcpApiError('JOB_RESPONSE_INVALID', '取消响应与请求的任务不一致，请刷新后核验。')
      const index = jobs.value.findIndex((item) => item.id === id)
      if (index >= 0) {
        jobs.value[index] = mergeJobSnapshot(jobs.value[index], next)
        versions.set(id, ++mutation)
      }
    } catch (caught) {
      if (!alive(owner)) return
      cancelErrors.value[id] = { reference, code: caught instanceof NcpApiError ? caught.code : 'NETWORK_UNAVAILABLE', message: caught instanceof NcpApiError ? caught.message : '取消请求提交失败，请刷新后核验任务状态。' }
      stale.value = true
      // 多个失败只合并成一次校准读取，不丢弃正在进行的读取。
      if (inFlight) { reads.invalidate(); pendingRefresh = true }
      else void refresh()
    } finally {
      if (alive(owner)) cancellingIds.value.delete(id)
    }
  }
  function visibilityChanged() {
    const nextVisible = document.visibilityState !== 'hidden'
    if (nextVisible === visible.value) return
    visible.value = nextVisible
    clearTimer()
    reads.invalidate()
    loading.value = false
    refreshing.value = false
    stale.value = true
    if (observing()) { if (inFlight) pendingRefresh = true; else void refresh() }
  }
  function resetObservation() {
    generation += 1
    reads.invalidate()
    clearTimer()
    pendingRefresh = false
    jobs.value = []
    versions.clear()
    invalidCount.value = 0
    hasLoaded.value = false
    loading.value = false
    refreshing.value = false
    stale.value = true
    error.value = ''
    lastFetchedAt.value = null
    cancellingIds.value = new Set()
    cancelErrors.value = {}
    failures = 0
    if (observing()) { if (inFlight) { pendingRefresh = true; loading.value = true } else void refresh() }
  }
  function dispose() {
    disposed = true
    resetObservation()
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', visibilityChanged)
    session.signal.removeEventListener('abort', dispose)
  }
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', visibilityChanged)
  session.signal.addEventListener('abort', dispose, { once: true })
  watch(open, resetObservation, { immediate: true, flush: 'sync' })
  onScopeDispose(dispose)
  return { jobs, invalidCount, hasLoaded, loading, refreshing, stale, error, lastFetchedAt, typeFilter, statusFilter, query, page, pageSize, typeOptions, statusOptions, filteredJobs, pagedJobs, pageCount, cancellingIds, cancelErrors, refresh, requestCancel, canCancel }
}
