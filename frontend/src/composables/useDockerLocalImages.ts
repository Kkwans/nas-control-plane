import { computed, ref, watch, type ComputedRef, type Ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

import {
  NcpApiError,
  type DockerImageSummary,
  type DockerInventory,
} from '@/api/system'
import { removeDockerImages, requestDockerImages } from '@/api/docker'
import { useListPreference } from '@/composables/useListPreference'

type ReadonlyRef<T> = Ref<T> | ComputedRef<T>
export type ImageRemoveFailure = { id: string; name: string; message: string }

interface UseDockerLocalImagesOptions {
  query: ReadonlyRef<string>
  containers: ReadonlyRef<DockerInventory['containers']>
  pageSize: ReadonlyRef<number>
}

export function useDockerLocalImages(options: UseDockerLocalImagesOptions) {
  const images = ref<DockerImageSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const removePendingIds = ref<string[]>([])
  const removeFailures = ref<ImageRemoveFailure[]>([])
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
  const bulkRemovePending = computed(() => removePendingIds.value.length > 0)

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
    return removePendingIds.value.includes(imageId)
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
    loading.value = true
    error.value = null
    try {
      images.value = (await requestDockerImages()).images
      const availableIds = new Set(images.value.map((image) => image.id))
      selectedImageIds.value = selectedImageIds.value.filter((id) => availableIds.has(id))
    } catch (caught) {
      error.value = caught instanceof NcpApiError ? caught.message : '本地镜像读取失败。'
    } finally {
      loading.value = false
    }
  }

  function containerReferenceCount(image: DockerImageSummary) {
    const inventoryCount = options.containers.value.filter((container) => image.repoTags.includes(container.image) || container.image === image.id).length
    return Math.max(Number.isFinite(image.containers) ? image.containers : 0, inventoryCount)
  }

  function failureMessage(caught: unknown) {
    return caught instanceof NcpApiError ? caught.message : '镜像删除失败，请稍后重试。'
  }

  async function confirmRemove(image: DockerImageSummary) {
    await confirmRemoveImages([image])
  }

  async function confirmRemoveSelected() {
    const selected = images.value.filter((image) => selectedImageIds.value.includes(image.id))
    await confirmRemoveImages(selected)
  }

  async function confirmRemoveImages(requested: DockerImageSummary[]) {
    if (!requested.length) return
    const inUse = requested.filter((image) => containerReferenceCount(image) > 0)
    const removable = requested.filter((image) => containerReferenceCount(image) === 0)
    if (!removable.length) {
      ElMessage.warning('所选镜像都正在被容器引用，已跳过删除；不会强制删除。')
      return
    }
    const skippedMessage = inUse.length
      ? `其中 ${inUse.length} 个镜像正在被容器引用，会跳过且不会 force 删除。`
      : ''
    const names = removable.length === 1 ? `“${displayName(removable[0] as DockerImageSummary)}”` : `${removable.length} 个未被引用的镜像`
    try {
      await ElMessageBox.confirm(`将从 NAS 删除 ${names}。${skippedMessage}`, '删除本地镜像', {
        confirmButtonText: removable.length === 1 ? '确认删除' : `删除 ${removable.length} 个`, cancelButtonText: '取消', type: 'warning',
      })
    } catch { return }

    removeFailures.value = []
    removePendingIds.value = removable.map((image) => image.id)
    try {
      const batches: DockerImageSummary[][] = []
      for (let index = 0; index < removable.length; index += 50) batches.push(removable.slice(index, index + 50))
      const results = await Promise.allSettled(batches.map((batch) => removeDockerImages(batch.map((image) => image.id))))
      const failures: ImageRemoveFailure[] = []
      let succeededCount = 0
      results.forEach((result, batchIndex) => {
        const batch = batches[batchIndex] ?? []
        if (result.status === 'rejected') {
          failures.push(...batch.map((image) => ({ id: image.id, name: displayName(image), message: failureMessage(result.reason) })))
          return
        }
        succeededCount += result.value.removedCount
        const imageById = new Map(batch.map((image) => [image.id, image]))
        for (const item of result.value.items) {
          if (item.removed) continue
          const image = imageById.get(item.imageId)
          if (!image) continue
          failures.push({ id: image.id, name: displayName(image), message: item.errorCode ? `删除失败（${item.errorCode}）` : '镜像删除失败，请稍后重试。' })
        }
      })
      removeFailures.value = failures
      if (succeededCount) ElMessage.success(`已删除 ${succeededCount} 个本地镜像`)
      if (failures.length) ElMessage.error(`${failures.length} 个镜像删除失败，请查看列表中的错误。`)
      selectedImageIds.value = failures.map((failure) => failure.id)
      await refresh()
    } finally {
      removePendingIds.value = []
    }
  }

  function displayName(image: DockerImageSummary) { return image.repoTags[0] ?? '未标记镜像' }
  function shortId(imageId: string) { return imageId.replace(/^sha256:/, '').slice(0, 12) }
  function dateLabel(value: string) {
    const date = new Date(value)
    return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
  }

  return {
    images,
    loading,
    error,
    removePendingIds,
    removeFailures,
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
