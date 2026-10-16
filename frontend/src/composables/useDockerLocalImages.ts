import { computed, onScopeDispose, ref, watch, type ComputedRef, type Ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import {
  NcpApiError,
  type DockerImageSummary,
  type DockerInventory,
} from '@/api/system'
import { DockerImageRemoveAdmissionUnknownError, requestDockerImageRemoveJob, removeDockerImages, requestDockerImages } from '@/api/docker'
import { captureSession } from '@/session/sessionLifecycle'
import { useListPreference } from '@/composables/useListPreference'

type ReadonlyRef<T> = Ref<T> | ComputedRef<T>
export type ImageRemoveFailure = { id: string; name: string; message: string }
type RemovalAdmission = { kind?: 'single-job' | 'bulk'; name: string; state: 'accepted' | 'unknown'; jobId?: string }
// 页面重挂载不等于重新获得写入许可；同一登录会话保留不确定提交。
let sessionBulkReservation = ref<{ token: symbol; ids: string[] } | null>(null)
let admissionSession = -1
let sessionAdmissions = ref<Record<string, RemovalAdmission>>({})
function removalAdmissionsForSession(session: ReturnType<typeof captureSession>) {
  if (admissionSession !== session.generation) {
    admissionSession = session.generation
    sessionAdmissions = ref<Record<string, RemovalAdmission>>({})
    sessionBulkReservation = ref(null)
    const reservation = sessionBulkReservation
    session.signal.addEventListener('abort', () => { reservation.value = null }, { once: true })
    const owned = sessionAdmissions
    session.signal.addEventListener('abort', () => { owned.value = {} }, { once: true })
  }
  return sessionAdmissions
}

interface UseDockerLocalImagesOptions {
  active?: ReadonlyRef<boolean>
  query: ReadonlyRef<string>
  containers: ReadonlyRef<DockerInventory['containers']>
  pageSize: ReadonlyRef<number>
}

export function useDockerLocalImages(options: UseDockerLocalImagesOptions) {
  const session = captureSession()
  let disposed = false
  const generation = ref(0)
  let readSequence = 0
  const alive = (owner = generation.value) => !disposed && !session.signal.aborted && owner === generation.value
  const active = () => alive() && options.active?.value !== false
  if (options.active) watch(options.active, () => { generation.value += 1 }, { flush: 'sync' })
  onScopeDispose(() => { disposed = true; generation.value += 1 })
  const removeAdmissions = removalAdmissionsForSession(session)
  const bulkReservation = sessionBulkReservation
  const singlePendingIds = ref<string[]>([])
  const images = ref<DockerImageSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const removePendingIds = computed(() => bulkReservation.value?.ids ?? [])
  const removeFailures = ref<ImageRemoveFailure[]>([])
  const bulkRemovalSummary = ref<{ total: number; removed: number; inUse: number; unknown: number; unsent: number; sending: number; running: boolean } | null>(null)
  const summaryOwner = ref(-1)
  const summaryToken = ref<symbol | null>(null)
  const bulkRemovalRunning = computed(() => !!bulkRemovalSummary.value?.running && alive(summaryOwner.value) && bulkReservation.value?.token === summaryToken.value)
  const selectedImageIds = ref<string[]>([])
  const localPage = ref(1)
  const { preference: localPreference, setSort: setLocalSort } = useListPreference('docker.images.local')

  const filteredImages = computed(() => {
    const term = options.query.value.trim().toLowerCase()
    const matching = term
      ? images.value.filter((image) => [image.id, ...image.repoTags, ...image.repoDigests].some((value) => value.toLowerCase().includes(term)))
      : images.value
    const key = localPreference.value.sortKey
    if (!key) return matching
    const direction = localPreference.value.sortDirection === 'desc' ? -1 : 1
    return [...matching].sort((left, right) => {
      if (key === 'size') return (left.sizeBytes - right.sizeBytes) * direction
      if (key === 'created') return (new Date(left.createdAt).valueOf() - new Date(right.createdAt).valueOf()) * direction
      if (key === 'containers') return (containerReferenceCount(left) - containerReferenceCount(right)) * direction
      return displayName(left).localeCompare(displayName(right), 'zh-CN') * direction
    })
  })
  const localPageCount = computed(() => Math.max(1, Math.ceil(filteredImages.value.length / options.pageSize.value)))
  const pagedImages = computed(() => {
    const start = (localPage.value - 1) * options.pageSize.value
    return filteredImages.value.slice(start, start + options.pageSize.value)
  })
  const visibleImageIds = computed(() => pagedImages.value.map((image) => image.id))
  const allVisibleSelected = computed(() => visibleImageIds.value.length > 0 && visibleImageIds.value.every((id) => selectedImageIds.value.includes(id)))
  const someVisibleSelected = computed(() => visibleImageIds.value.some((id) => selectedImageIds.value.includes(id)))
  const selectedImageCount = computed(() => selectedImageIds.value.length)
  const bulkRemovePending = computed(() => bulkReservation.value !== null)

  watch(() => [options.query.value, options.pageSize.value], () => { localPage.value = 1 })
  watch(localPageCount, (count) => {
    if (localPage.value > count) localPage.value = count
  })

  function toggleVisibleSelection(checked: boolean) {
    const visible = new Set(visibleImageIds.value)
    if (checked) {
      selectedImageIds.value = [...new Set([...selectedImageIds.value, ...visible])]
    } else {
      selectedImageIds.value = selectedImageIds.value.filter((id) => !visible.has(id))
    }
  }

  function toggleImageSelection(imageId: string, checked: boolean) {
    if (checked) {
      if (!selectedImageIds.value.includes(imageId)) selectedImageIds.value = [...selectedImageIds.value, imageId]
    } else {
      selectedImageIds.value = selectedImageIds.value.filter((id) => id !== imageId)
    }
  }

  function handleVisibleSelectionChange(event: Event) {
    toggleVisibleSelection((event.currentTarget as HTMLInputElement).checked)
  }

  function handleImageSelectionChange(imageId: string, event: Event) {
    toggleImageSelection(imageId, (event.currentTarget as HTMLInputElement).checked)
  }

  function isRemovePending(imageId: string) {
    return !!bulkReservation.value?.ids.includes(imageId) || removePendingIds.value.includes(imageId) || singlePendingIds.value.includes(imageId) || !!removeAdmissions.value[imageId]
  }

  async function toggleLocalSort(key: 'name' | 'size' | 'created' | 'containers') {
    if (localPreference.value.sortKey !== key) {
      await setLocalSort(key, 'asc')
      return
    }
    if (localPreference.value.sortDirection === 'asc') {
      await setLocalSort(key, 'desc')
      return
    }
    await setLocalSort('', 'asc')
  }

  async function refresh() {
    if (!alive()) return
    const sequence = ++readSequence
    const valid = () => alive() && sequence === readSequence
    loading.value = true
    error.value = null
    try {
      const result = await requestDockerImages()
      if (!valid()) return
      images.value = result.images
      const availableIds = new Set(images.value.map((image) => image.id))
      selectedImageIds.value = selectedImageIds.value.filter((id) => availableIds.has(id))
    } catch (caught) {
      if (!valid()) return
      error.value = caught instanceof NcpApiError ? caught.message : '本地镜像读取失败。'
    } finally {
      if (valid()) loading.value = false
    }
  }

  function containerReferenceCount(image: DockerImageSummary) {
    const inventoryCount = options.containers.value.filter((container) => image.repoTags.includes(container.image) || container.image === image.id).length
    return Math.max(Number.isFinite(image.containers) ? image.containers : 0, inventoryCount)
  }

  async function confirmRemove(image: DockerImageSummary) {
    if (!active() || isRemovePending(image.id)) return
    const id = image.id
    const name = displayName(image)
    const tags = JSON.stringify(image.repoTags)
    const owner = generation.value
    const currentTarget = () => images.value.find((item) => item.id === id && JSON.stringify(item.repoTags) === tags)
    if (!currentTarget() || containerReferenceCount(image) > 0) return
    // 在确认框前锁定目标，避免双击产生两个确认或重复写入。
    singlePendingIds.value.push(id)
    try {
      try {
        await ElMessageBox.confirm(`将从 NAS 删除“${name}”（${id}）。提交后由后台任务执行，不会强制删除。`, '删除本地镜像', {
          confirmButtonText: '提交删除任务', cancelButtonText: '取消', type: 'warning',
        })
      } catch { return }
      const target = currentTarget()
      if (!active() || !alive(owner) || !target || containerReferenceCount(target) > 0 || target.id !== id || removeAdmissions.value[id] || bulkReservation.value?.ids.includes(id)) return
      removeAdmissions.value[id] = { name, state: 'unknown' }
      try {
        const job = await requestDockerImageRemoveJob(id)
        if (!active() || !alive(owner)) return
        removeAdmissions.value[id] = { name, state: 'accepted', jobId: job.id }
        ElMessage.success('删除任务已接收，请在最近任务中查看实际结果。')
      } catch (caught) {
        if (!active() || !alive(owner)) return
        if (caught instanceof DockerImageRemoveAdmissionUnknownError || !(caught instanceof NcpApiError)) {
          removeAdmissions.value[id] = { name, state: 'unknown' }
        } else {
          delete removeAdmissions.value[id]
          removeFailures.value = [{ id, name, message: caught.message }]
        }
      }
    } finally {
      singlePendingIds.value = singlePendingIds.value.filter((pending) => pending !== id)
    }
  }

  async function confirmRemoveSelected() {
    const selected = images.value.filter((image) => selectedImageIds.value.includes(image.id) && !isRemovePending(image.id))
    await confirmRemoveImages(selected)
  }

  async function confirmRemoveImages(requested: DockerImageSummary[]) {
    if (!active() || bulkReservation.value || !requested.length) return
    const owner = generation.value
    const inUse = requested.filter((image) => containerReferenceCount(image) > 0)
    const removable = requested.filter((image) => containerReferenceCount(image) === 0)
      .map((image) => ({ id: image.id, name: displayName(image), tags: JSON.stringify(image.repoTags) }))
    if (!removable.length) {
      ElMessage.warning('所选镜像都正在被容器引用，已跳过删除；不会强制删除。')
      return
    }
    const ids = removable.map((image) => image.id)
    if (new Set(ids).size !== ids.length || ids.some((id) => !/^sha256:[a-f0-9]{64}$/.test(id) || isRemovePending(id))) return
    const token = Symbol('bulk-removal')
    bulkReservation.value = { token, ids }
    const valid = () => active() && alive(owner) && bulkReservation.value?.token === token
    const currentTargets = (batch: typeof removable) => batch.every((snapshot) => {
      const matches = images.value.filter((image) => image.id === snapshot.id)
      const target = matches[0]
      return matches.length === 1 && target && JSON.stringify(target.repoTags) === snapshot.tags &&
        containerReferenceCount(target) === 0 && !singlePendingIds.value.includes(snapshot.id) && !removeAdmissions.value[snapshot.id]
    })
    try {
      if (!currentTargets(removable)) return
      const skipped = inUse.length ? `其中 ${inUse.length} 个镜像正在被容器引用，会跳过且不会 force 删除。` : ''
      const names = removable.length === 1 ? `“${removable[0]!.name}”（${removable[0]!.id}）` : `${removable.length} 个未被引用的镜像`
      try {
        await ElMessageBox.confirm(`将从 NAS 删除 ${names}。${skipped}`, '删除本地镜像', {
          confirmButtonText: removable.length === 1 ? '确认删除' : `删除 ${removable.length} 个`, cancelButtonText: '取消', type: 'warning',
        })
      } catch { return }
      if (!valid()) return
      removeFailures.value = []
      const summary = { total: removable.length, removed: 0, inUse: 0, unknown: 0, unsent: removable.length, sending: 0, running: true }
      summaryOwner.value = owner
      summaryToken.value = token
      const publish = () => { bulkRemovalSummary.value = { ...summary } }
      const retained = new Set(ids)
      publish()
      // 整个选择保持预留，但只有即将发出的批次才获得不确定提交记录。
      // 每次仅检查下一批冻结目标，前一批的未知结果不能阻塞独立目标。
      for (let index = 0; index < removable.length; index += 50) {
        const batch = removable.slice(index, index + 50)
        if (!valid()) return
        if (!currentTargets(batch)) break
        for (const image of batch) removeAdmissions.value[image.id] = { kind: 'bulk', name: image.name, state: 'unknown' }
        summary.unsent -= batch.length
        summary.unknown += batch.length
        summary.sending = batch.length
        publish()
        try {
          const result = await removeDockerImages(batch.map((image) => image.id))
          if (!valid()) return
          for (const item of result.items) {
            const image = batch.find((image) => image.id === item.imageId)!
            if (item.removed) {
              summary.removed += 1
              summary.unknown -= 1
              retained.delete(image.id)
              delete removeAdmissions.value[image.id]
            } else if (item.errorCode === 'DOCKER_IMAGE_IN_USE') {
              summary.inUse += 1
              summary.unknown -= 1
              delete removeAdmissions.value[image.id]
              removeFailures.value.push({ id: image.id, name: image.name, message: '镜像正在被容器引用，已跳过删除；不会强制删除。' })
            }
          }
        } catch {
          // 拒绝或响应丢失均不能证明未执行；不重试，不清除已发出目标的记录。
          if (!valid()) return
        }
        summary.sending = 0
        publish()
      }
      if (!valid()) return
      summary.running = false
      publish()
      if (summary.removed) ElMessage.success(`已删除 ${summary.removed} 个本地镜像`)
      if (summary.inUse) ElMessage.error(`${summary.inUse} 个镜像仍被引用，已跳过删除。`)
      selectedImageIds.value = ids.filter((id) => retained.has(id))
      await refresh()
    } finally {
      if (bulkReservation.value?.token === token) bulkReservation.value = null
    }
  }

  function displayName(image: DockerImageSummary) { return image.repoTags[0] ?? '未标记镜像' }
  function shortId(imageId: string) { return imageId.replace(/^sha256:/, '').slice(0, 12) }
  function dateLabel(value: string) {
    const date = new Date(value)
    return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
  }

  return {
    removeAdmissions,
    images,
    loading,
    error,
    removePendingIds,
    removeFailures,
    bulkRemovalSummary,
    bulkRemovalRunning,
    selectedImageIds,
    localPage,
    localPreference,
    filteredImages,
    localPageCount,
    pagedImages,
    visibleImageIds,
    allVisibleSelected,
    someVisibleSelected,
    selectedImageCount,
    bulkRemovePending,
    toggleLocalSort,
    refresh,
    handleVisibleSelectionChange,
    handleImageSelectionChange,
    isRemovePending,
    confirmRemove,
    confirmRemoveSelected,
    containerReferenceCount,
    displayName,
    shortId,
    dateLabel,
  }
}
