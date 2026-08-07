<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ArrowDown, ArrowUp, ArrowUpDown, BadgeCheck, Box, ChevronLeft, ChevronRight, Download, Eye, HardDrive, Image as ImageIcon, PackagePlus, RefreshCw, RotateCcw, Search, Square, Star, Trash2 } from '@lucide/vue'
import { ElInput, ElMessage, ElMessageBox, ElTooltip } from 'element-plus'

import {
  NcpApiError,
  cancelJob,
  deleteJob,
  followJob,
  pullDockerImage,
  requestJobs,
  retryJob,
  removeDockerImages,
  requestDockerHubTags,
  requestDockerImages,
  searchDockerHub,
  type DockerHubRepository,
  type DockerHubTag,
  type DockerImageSummary,
  type DockerInventory,
  type JobSnapshot,
} from '@/api/system'
import ActionButton from '@/components/ActionButton.vue'
import CreateContainerDrawer from '@/components/CreateContainerDrawer.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import NcpSelect, { type NcpSelectOption } from '@/components/NcpSelect.vue'
import { useListPreference } from '@/composables/useListPreference'
import { formatBytes } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const props = defineProps<{ query: string; containers: DockerInventory['containers']; pageSize: number }>()
const emit = defineEmits<{ 'update:query': [value: string] }>()
type ImageMode = 'local' | 'hub' | 'downloads'
const systemStore = useSystemStore()

const mode = ref<ImageMode>('local')
const images = ref<DockerImageSummary[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const removePendingIds = ref<string[]>([])
const removeFailures = ref<Array<{ id: string; name: string; message: string }>>([])
const createImage = ref<DockerImageSummary | null>(null)
const createDrawerOpen = ref(false)
const selectedImageIds = ref<string[]>([])
const hubQuery = ref('')
const hubSort = ref('relevance')
const hubLoading = ref(false)
const hubError = ref<string | null>(null)
const repositories = ref<DockerHubRepository[]>([])
const hubPage = ref(1)
const hubCount = ref(0)
const selectedRepository = ref<DockerHubRepository | null>(null)
const tags = ref<DockerHubTag[]>([])
const tagsLoading = ref(false)
const selectedTag = ref('latest')
const submittingReference = ref('')
const localPage = ref(1)
const downloadJobs = ref<JobSnapshot[]>([])
const invalidDownloadJobCount = ref(0)
const downloadStatus = ref('all')
const downloadQuery = ref('')
const downloadPage = ref(1)
let hubRequestSequence = 0
const hubSortOptions: NcpSelectOption[] = [
  { label: '最佳匹配', value: 'relevance' },
  { label: '拉取量', value: 'pulls' },
  { label: '收藏数', value: 'stars' },
  { label: '最近更新', value: 'updated' },
]
const downloadStatusOptions: NcpSelectOption[] = [
  { label: '全部状态', value: 'all' },
  { label: '排队中', value: 'queued' },
  { label: '下载中', value: 'running' },
  { label: '已完成', value: 'completed' },
  { label: '已停止', value: 'cancelled' },
  { label: '已删除', value: 'deleted' },
  { label: '失败', value: 'failed' },
  { label: '已中断', value: 'interrupted' },
]
const { preference: localPreference, setSort: setLocalSort } = useListPreference('docker.images.local')
const { pageSize: downloadPageSize } = useListPreference('docker.image-downloads')

const filteredImages = computed(() => {
  const term = props.query.trim().toLowerCase()
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
const localPageCount = computed(() => Math.max(1, Math.ceil(filteredImages.value.length / props.pageSize)))
const pagedImages = computed(() => {
  const start = (localPage.value - 1) * props.pageSize
  return filteredImages.value.slice(start, start + props.pageSize)
})
const visibleImageIds = computed(() => pagedImages.value.map((image) => image.id))
const allVisibleSelected = computed(() => visibleImageIds.value.length > 0 && visibleImageIds.value.every((id) => selectedImageIds.value.includes(id)))
const someVisibleSelected = computed(() => visibleImageIds.value.some((id) => selectedImageIds.value.includes(id)))
const selectedImageCount = computed(() => selectedImageIds.value.length)
const bulkRemovePending = computed(() => removePendingIds.value.length > 0)
const totalPages = computed(() => Math.max(1, Math.ceil(hubCount.value / 20)))
const pullReference = computed(() => {
  if (!selectedRepository.value) return ''
  const prefix = selectedRepository.value.namespace === 'library' ? '' : `${selectedRepository.value.namespace}/`
  return `${prefix}${selectedRepository.value.name}:${selectedTag.value || 'latest'}`
})
const selectedTagDetails = computed(() => tags.value.find((tag) => tag.name === selectedTag.value) ?? null)
const hostArchitecture = computed(() => normalizeArchitecture(
  systemStore.summary?.host.architecture || systemStore.capabilities?.architecture || '',
))
const recommendedTag = computed(() => tags.value.find((tag) => tag.name !== 'latest' && tagSupportsHost(tag))?.name
  ?? tags.value.find((tag) => tag.name === 'latest' && tagSupportsHost(tag))?.name
  ?? tags.value[0]?.name
  ?? '')
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

watch(() => [props.query, props.pageSize], () => { localPage.value = 1 })
watch(localPageCount, (count) => {
  if (localPage.value > count) localPage.value = count
})
watch([downloadStatus, downloadPageSize, downloadQuery], () => { downloadPage.value = 1 })
watch(downloadPageCount, (count) => {
  if (downloadPage.value > count) downloadPage.value = count
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

function openCreateDrawer(image: DockerImageSummary) {
  createImage.value = image
  createDrawerOpen.value = true
}

async function handleContainerCreated() {
  await systemStore.refresh({ inventory: true })
  await refresh()
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

function normalizeArchitecture(value: string) {
  const normalized = value.trim().toLowerCase().replaceAll('_', '-')
  if (normalized.includes('aarch64') || normalized.includes('arm64')) return 'arm64'
  if (normalized.includes('x86-64') || normalized.includes('amd64')) return 'amd64'
  if (normalized.includes('armv7l') || normalized.includes('arm/v7')) return 'arm/v7'
  return normalized.replace(/^linux\//, '')
}

function tagSupportsHost(tag: DockerHubTag) {
  if (!hostArchitecture.value) return false
  return tag.architectures.some((architecture) => normalizeArchitecture(architecture) === hostArchitecture.value)
}

function hostCompatibilityLabel(tag: DockerHubTag) {
  if (!hostArchitecture.value) return '无法识别本机架构'
  return tagSupportsHost(tag) ? `支持本机 ${hostArchitecture.value}` : `不支持本机 ${hostArchitecture.value}`
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

async function runHubSearch(page = 1) {
  const term = hubQuery.value.trim()
  if (!term) return
  hubLoading.value = true
  hubError.value = null
  const requestSequence = ++hubRequestSequence
  try {
    const result = await searchDockerHub(term, page, 20, hubSort.value)
    if (requestSequence !== hubRequestSequence) return
    repositories.value = result.results
    hubCount.value = result.count
    hubPage.value = result.page
    const firstRepository = result.results[0]
    if (firstRepository) await selectRepository(firstRepository)
    else selectedRepository.value = null
  } catch (caught) {
    if (requestSequence !== hubRequestSequence) return
    hubError.value = caught instanceof NcpApiError ? caught.message : 'Docker Hub 搜索失败。'
  } finally {
    hubLoading.value = false
  }
}

async function selectRepository(repository: DockerHubRepository) {
  selectedRepository.value = repository
  tagsLoading.value = true
  tags.value = []
  selectedTag.value = 'latest'
  try {
    const result = await requestDockerHubTags(repository.namespace, repository.name)
    tags.value = [...result.results].sort(compareTags)
    selectedTag.value = tags.value.find((tag) => tag.name === 'latest')?.name ?? tags.value[0]?.name ?? 'latest'
  } catch (caught) {
    hubError.value = caught instanceof NcpApiError ? caught.message : '镜像标签读取失败。'
  } finally {
    tagsLoading.value = false
  }
}

async function submitPull() {
  if (!pullReference.value) return
  const reference = pullReference.value
  if (submittingReference.value === reference) return
  submittingReference.value = reference
  try {
    const job = await pullDockerImage(reference, selectedTagDetails.value?.fullSize ?? 0)
    upsertDownloadJob(job)
    void followJob(job.id, upsertDownloadJob).then(async (completed) => {
      if (completed.status === 'completed') {
        ElMessage.success(`镜像 ${reference} 已拉取`)
        await refresh()
      } else if (completed.status === 'failed') {
        ElMessage.error(completed.error || completed.message || `镜像 ${reference} 拉取失败`)
      }
    }).catch(() => undefined)
  } catch (caught) {
    ElMessage.error(caught instanceof NcpApiError ? caught.message : '镜像拉取失败。')
  } finally {
    submittingReference.value = ''
  }
}

function upsertDownloadJob(job: JobSnapshot) {
  const index = downloadJobs.value.findIndex((item) => item.id === job.id)
  if (index >= 0) downloadJobs.value[index] = job
  else downloadJobs.value.unshift(job)
  downloadJobs.value = [...downloadJobs.value]
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

function containerReferenceCount(image: DockerImageSummary) {
  const inventoryCount = props.containers.filter((container) => image.repoTags.includes(container.image) || container.image === image.id).length
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
    const failures: Array<{ id: string; name: string; message: string }> = []
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
function sortIcon(key: 'name' | 'size' | 'created' | 'containers') {
  if (localPreference.value.sortKey !== key) return ArrowUpDown
  return localPreference.value.sortDirection === 'desc' ? ArrowDown : ArrowUp
}
function compactNumber(value: number) {
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}
function dateLabel(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}

function compareTags(left: DockerHubTag, right: DockerHubTag) {
  if (left.name === 'latest') return -1
  if (right.name === 'latest') return 1
  const leftStable = /^\d+(?:\.\d+){1,3}(?:[-.][a-z0-9]+)*$/i.test(left.name)
  const rightStable = /^\d+(?:\.\d+){1,3}(?:[-.][a-z0-9]+)*$/i.test(right.name)
  if (leftStable !== rightStable) return leftStable ? -1 : 1
  return new Date(right.lastUpdated).valueOf() - new Date(left.lastUpdated).valueOf()
}

function repositoryInitial(repository: DockerHubRepository) {
  return repository.name.slice(0, 1).toUpperCase() || 'D'
}

function repositoryHue(repository: DockerHubRepository) {
  return [...`${repository.namespace}/${repository.name}`].reduce((sum, character) => sum + character.charCodeAt(0), 0) % 360
}

function repositoryLogo(repository: DockerHubRepository) {
  const name = repository.name.toLowerCase()
  const logos: Record<string, string> = {
    redis: 'https://cdn.simpleicons.org/redis/FF4438',
    nginx: 'https://cdn.simpleicons.org/nginx/009639',
    postgres: 'https://cdn.simpleicons.org/postgresql/4169E1',
    postgresql: 'https://cdn.simpleicons.org/postgresql/4169E1',
    mysql: 'https://cdn.simpleicons.org/mysql/4479A1',
    mariadb: 'https://cdn.simpleicons.org/mariadb/003545',
    mongo: 'https://cdn.simpleicons.org/mongodb/47A248',
    mongodb: 'https://cdn.simpleicons.org/mongodb/47A248',
    node: 'https://cdn.simpleicons.org/nodedotjs/5FA04E',
    python: 'https://cdn.simpleicons.org/python/3776AB',
    alpine: 'https://cdn.simpleicons.org/alpinelinux/0D597F',
    ubuntu: 'https://cdn.simpleicons.org/ubuntu/E95420',
  }
  return logos[name] ?? ''
}

function tagOptions(): NcpSelectOption[] {
  return tags.value.map((tag) => ({
    value: tag.name,
    label: `${tag.name}${tag.name === 'latest' ? ' · latest' : tag.name === recommendedTag.value ? ' · 推荐' : ''}`,
  }))
}

function showDownloadedImage(job: JobSnapshot) {
  mode.value = 'local'
  emit('update:query', (job.reference ?? '').split('@')[0] ?? '')
}

onMounted(() => {
  void refresh()
  void loadDownloadJobs()
})
</script>

<template>
  <section class="image-workspace">
    <header class="image-modebar">
      <nav aria-label="镜像来源">
        <button :class="{ active: mode === 'local' }" type="button" @click="mode = 'local'"><HardDrive :size="17" />本地镜像 <span>{{ images.length }}</span></button>
        <button :class="{ active: mode === 'hub' }" type="button" @click="mode = 'hub'"><Box :size="17" />线上仓库</button>
        <button :class="{ active: mode === 'downloads' }" type="button" @click="mode = 'downloads'"><Download :size="17" />下载任务 <span>{{ activePullCount }}</span></button>
      </nav>
      <div v-if="mode === 'local'" class="mode-actions">
        <ElInput :model-value="query" clearable aria-label="搜索本地镜像" placeholder="搜索镜像名称、标签或 ID" @update:model-value="emit('update:query', String($event))">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
        <span v-if="selectedImageCount" class="selection-count">已选 {{ selectedImageCount }} 个</span>
        <ActionButton v-if="selectedImageCount" variant="danger" size="sm" :icon="Trash2" :loading="bulkRemovePending" @click="confirmRemoveSelected">批量删除</ActionButton>
        <ElTooltip content="刷新本地镜像"><button class="icon-button" type="button" :disabled="loading" aria-label="刷新本地镜像" @click="refresh"><RefreshCw :class="{ spin: loading }" :size="17" /></button></ElTooltip>
      </div>
      <form v-else-if="mode === 'hub'" class="hub-search" @submit.prevent="runHubSearch(1)">
        <ElInput v-model="hubQuery" clearable aria-label="搜索 Docker Hub 镜像" placeholder="搜索 Docker Hub 镜像，例如 nginx、postgres">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
        <NcpSelect v-model="hubSort" :options="hubSortOptions" accessible-label="搜索结果排序" @update:model-value="repositories.length && runHubSearch(1)" />
        <button class="primary-button" type="submit" :disabled="!hubQuery.trim() || hubLoading">搜索镜像</button>
      </form>
      <div v-else class="mode-actions download-mode-actions">
        <ElInput v-model="downloadQuery" clearable aria-label="搜索镜像下载任务" placeholder="搜索镜像或任务消息">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
        <NcpSelect v-model="downloadStatus" :options="downloadStatusOptions" accessible-label="下载任务状态" />
        <ElTooltip content="刷新下载任务"><button class="icon-button" type="button" @click="loadDownloadJobs"><RefreshCw :size="17" /></button></ElTooltip>
      </div>
    </header>

    <div v-if="mode === 'local'">
      <div v-if="error" class="inline-error"><span>{{ error }}</span><button type="button" @click="refresh">重新加载</button></div>
      <div v-if="removeFailures.length" class="inline-error bulk-remove-error" role="alert">
        <div><strong>部分镜像删除失败</strong><ul><li v-for="failure in removeFailures" :key="failure.id">{{ failure.name }}：{{ failure.message }}</li></ul></div>
        <button type="button" @click="removeFailures = []">关闭</button>
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
            <ElTooltip content="使用此镜像创建容器"><ActionButton variant="ghost" size="sm" icon-only :icon="PackagePlus" :aria-label="`从镜像 ${displayName(image)} 创建容器`" @click="openCreateDrawer(image)"><span>创建容器</span></ActionButton></ElTooltip>
            <ElTooltip :content="containerReferenceCount(image) ? '镜像正在被容器引用，先停止并移除引用' : '删除未使用的本地镜像'"><ActionButton variant="danger" size="sm" icon-only :icon="Trash2" :loading="isRemovePending(image.id)" :disabled="bulkRemovePending || containerReferenceCount(image) > 0" :aria-label="`删除镜像 ${displayName(image)}`" @click="confirmRemove(image)"><span>删除镜像</span></ActionButton></ElTooltip>
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

    <div v-else-if="mode === 'hub'" class="hub-workspace">
      <aside class="repository-list panel">
        <div v-if="hubLoading && !repositories.length" class="repository-loading"><div v-for="row in 7" :key="row"><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></div></div>
        <div v-else-if="hubLoading" class="repository-refreshing"><RefreshCw class="spin" :size="15" />正在更新搜索结果</div>
        <button v-for="repository in repositories" :key="`${repository.namespace}/${repository.name}`" type="button" :class="{ active: selectedRepository?.name === repository.name && selectedRepository?.namespace === repository.namespace }" @click="selectRepository(repository)">
          <span class="repo-icon" :style="{ '--repo-hue': repositoryHue(repository) }"><img v-if="repositoryLogo(repository)" :src="repositoryLogo(repository)" alt="" /><b v-else>{{ repositoryInitial(repository) }}</b></span>
          <span class="repo-copy"><strong>{{ repository.namespace === 'library' ? repository.name : `${repository.namespace}/${repository.name}` }} <BadgeCheck v-if="repository.official" class="official-badge" :size="13" /></strong><small>{{ repository.description || '暂无仓库简介' }}</small></span>
          <span class="repo-stats"><small><Download :size="12" />{{ compactNumber(repository.pullCount) }}</small><small><Star :size="12" />{{ compactNumber(repository.starCount) }}</small><time>{{ dateLabel(repository.lastUpdated) }}</time></span>
        </button>
        <div v-if="hubError" class="empty-state">{{ hubError }}</div>
        <div v-else-if="!hubLoading && !repositories.length" class="empty-state">输入关键字，搜索 Docker Hub 公共镜像</div>
        <footer v-if="hubCount > 20"><button :disabled="hubPage <= 1" @click="runHubSearch(hubPage - 1)"><ChevronLeft :size="16" /></button><span>{{ hubPage }} / {{ totalPages }}</span><button :disabled="hubPage >= totalPages" @click="runHubSearch(hubPage + 1)"><ChevronRight :size="16" /></button></footer>
      </aside>

      <section class="repository-detail panel">
        <template v-if="selectedRepository">
          <header><div><span class="detail-icon repo-icon" :style="{ '--repo-hue': repositoryHue(selectedRepository) }"><img v-if="repositoryLogo(selectedRepository)" :src="repositoryLogo(selectedRepository)" alt="" /><b v-else>{{ repositoryInitial(selectedRepository) }}</b></span><div><strong>{{ selectedRepository.namespace === 'library' ? selectedRepository.name : `${selectedRepository.namespace}/${selectedRepository.name}` }}</strong><small>{{ selectedRepository.official ? 'Docker 官方镜像' : `发布者：${selectedRepository.publisher}` }}</small></div></div><span class="pull-count">{{ compactNumber(selectedRepository.pullCount) }} 次拉取</span></header>
          <p>{{ selectedRepository.description || '该仓库暂未提供简介。' }}</p>
          <dl class="repository-facts">
            <div><dt>发布者</dt><dd>{{ selectedRepository.publisher || selectedRepository.namespace }}</dd></div>
            <div><dt>拉取量</dt><dd>{{ compactNumber(selectedRepository.pullCount) }}</dd></div>
            <div><dt>收藏数</dt><dd>{{ compactNumber(selectedRepository.starCount) }}</dd></div>
            <div><dt>仓库类型</dt><dd>{{ selectedRepository.official ? '官方镜像' : (selectedRepository.repositoryType || '公共镜像') }}</dd></div>
            <div><dt>仓库状态</dt><dd>{{ selectedRepository.statusDescription || '可用' }}</dd></div>
            <div><dt>最近更新</dt><dd>{{ dateLabel(selectedRepository.lastUpdated) }}</dd></div>
          </dl>
          <div class="tag-title">
            <div><strong>选择版本</strong><span>{{ tags.length }} 个最近更新的标签</span></div>
            <NcpSelect v-if="!tagsLoading" v-model="selectedTag" :options="tagOptions()" accessible-label="镜像标签" filterable />
          </div>
          <div v-if="tagsLoading" class="tag-picker-loading ncp-skeleton"></div>
          <div v-else class="tag-picker">
            <dl v-if="selectedTagDetails">
              <div v-if="selectedTagDetails.publishedAt">
                <dt>
                  <ElTooltip content="Docker Hub 标签的发布时间或最近更新时间，不代表镜像内部构建时间。" placement="top">
                    <span>版本发布时间</span>
                  </ElTooltip>
                </dt>
                <dd>{{ dateLabel(selectedTagDetails.publishedAt) }}</dd>
              </div>
              <div v-else><dt>拉取命令</dt><dd><code>docker pull {{ pullReference }}</code></dd></div>
              <div><dt>镜像大小</dt><dd>{{ formatBytes(selectedTagDetails.fullSize) }}</dd></div>
              <div><dt>兼容架构</dt><dd>{{ selectedTagDetails.architectures.join('、') || '未知' }}</dd></div>
              <div><dt>本机兼容性</dt><dd>{{ hostCompatibilityLabel(selectedTagDetails) }}</dd></div>
            </dl>
          </div>
          <footer><span>任务将进入后台下载队列，最多并行拉取 3 个镜像</span><button class="primary-button" type="button" :disabled="submittingReference === pullReference || !selectedTag" @click="submitPull"><Download :size="17" />{{ submittingReference === pullReference ? '正在提交' : '加入下载队列' }}</button></footer>
        </template>
        <div v-else class="detail-empty"><Search :size="28" /><strong>选择一个镜像仓库</strong><span>查看标签、架构并直接拉取到 NAS</span></div>
      </section>
    </div>

    <section v-else class="download-workspace panel">
      <header>
        <div>
          <strong>镜像下载任务</strong>
          <span>最多同时拉取 3 个镜像，任务进度与历史记录由服务端持久化。</span>
        </div>
        <span v-if="invalidDownloadJobCount" class="task-parse-warning">
          已忽略 {{ invalidDownloadJobCount }} 条格式异常的历史记录
        </span>
      </header>
      <div class="download-head"><span>镜像</span><span>状态</span><span>进度</span><span>速度</span><span>更新时间</span><span>操作</span></div>
      <article v-for="job in pagedDownloadJobs" :key="job.id" class="download-row">
        <div><strong>{{ job.reference }}</strong><small>{{ job.message }}</small></div>
        <span :class="['job-state', `job-state--${downloadState(job)}`]">{{ downloadStateLabel(job) }}</span>
        <div class="job-progress"><span><i :style="{ width: `${job.progress}%` }"></i></span><small>{{ job.progress }}% · {{ formatBytes(job.downloadedBytes || (job.progress === 100 ? effectiveTotal(job) : 0)) }} / {{ effectiveTotal(job) ? formatBytes(effectiveTotal(job)) : '未知' }}</small></div>
        <span>{{ job.speedBytes > 0 ? `${formatBytes(job.speedBytes)}/s` : '—' }}</span>
        <time>{{ dateLabel(job.updatedAt) }}</time>
        <div class="job-actions">
          <button v-if="job.status === 'queued' || job.status === 'running'" type="button" title="停止下载" @click="stopDownload(job)"><Square :size="14" /></button>
          <button v-else-if="job.status === 'failed' || job.status === 'interrupted' || job.status === 'cancelled' || downloadState(job) === 'deleted'" type="button" title="重新下载" @click="retryDownload(job)"><RotateCcw :size="15" /></button>
          <button v-else-if="downloadState(job) === 'completed'" type="button" title="查看本地镜像" @click="showDownloadedImage(job)"><Eye :size="15" /></button>
          <button v-if="job.status !== 'queued' && job.status !== 'running'" class="job-delete" type="button" title="删除下载记录" @click="confirmDeleteJob(job)"><Trash2 :size="15" /></button>
        </div>
      </article>
      <div v-if="!filteredDownloadJobs.length" class="empty-state">没有匹配的镜像下载任务</div>
      <footer v-else class="resource-pagination">
        <ListPageSizeControl list-key="docker.image-downloads" />
        <div><button type="button" :disabled="downloadPage <= 1" @click="downloadPage -= 1">上一页</button><strong>{{ downloadPage }} / {{ downloadPageCount }}</strong><button type="button" :disabled="downloadPage >= downloadPageCount" @click="downloadPage += 1">下一页</button></div>
      </footer>
    </section>
    <CreateContainerDrawer v-model="createDrawerOpen" :image="createImage" @created="handleContainerCreated" />
  </section>
</template>

<style scoped>
.image-workspace{display:grid;gap:12px}.image-modebar{display:flex;min-height:58px;align-items:center;justify-content:space-between;gap:16px}.image-modebar nav{display:flex;gap:4px;padding:4px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.image-modebar nav button{display:flex;min-height:38px;align-items:center;gap:7px;padding:0 13px;border-radius:8px;color:var(--ncp-text-muted);font-size:.82rem;font-weight:720}.image-modebar nav button.active{background:#fff;box-shadow:0 3px 10px rgba(24,42,72,.08);color:var(--ncp-primary-strong)}.image-modebar nav span{padding:1px 6px;border-radius:10px;background:var(--ncp-primary-soft);font-size:.68rem}.mode-actions,.hub-search{display:flex;align-items:center;gap:8px}.mode-actions>span{color:var(--ncp-text-subtle);font-size:.78rem}.hub-search{width:min(560px,55vw)}.hub-search :deep(.el-input){flex:1}.hub-search :deep(.el-input__inner:focus){outline:0!important;box-shadow:none!important}.icon-button,.primary-button,.danger-button{display:inline-flex;min-height:40px;align-items:center;justify-content:center;gap:6px;border-radius:9px;font-size:.8rem;font-weight:720}.icon-button{width:40px;border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.primary-button{padding:0 15px;background:var(--ncp-primary);box-shadow:0 6px 16px rgba(36,104,216,.16);color:#fff}.primary-button:disabled{opacity:.5}.inline-error{display:flex;min-height:44px;align-items:center;justify-content:space-between;padding:8px 12px;border:1px solid rgba(212,81,93,.18);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.8rem}.inline-error button{font-weight:720}.resource-table{overflow:hidden}.resource-head,.resource-row{display:grid;grid-template-columns:minmax(260px,1.5fr) 130px 100px 120px 120px 100px;align-items:center;gap:12px}.resource-head{min-height:44px;padding:0 16px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.8rem;font-weight:720}.resource-head button{display:inline-flex;align-items:center;gap:5px;padding:5px 8px;border-radius:7px;color:inherit;font:inherit;transition:color .16s ease,background-color .16s ease}.resource-head button:hover{background:rgba(52,116,212,.06);color:var(--ncp-primary-strong)}.resource-head button.active{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.resource-head>:nth-child(3),.resource-head>:nth-child(4){justify-self:center}.resource-row{min-height:68px;padding:0 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.82rem}.resource-row:hover{background:var(--ncp-surface-hover)}.resource-name{display:flex;min-width:0;align-items:center;gap:10px}.resource-name>span,.repo-icon,.detail-icon{display:grid;flex:0 0 auto;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.resource-name>span{width:36px;height:36px}.resource-name>div{display:grid;min-width:0;gap:2px}.resource-name strong,.resource-name small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.resource-name strong{color:var(--ncp-text);font-size:.88rem}.resource-name small{color:var(--ncp-text-subtle);font-size:.73rem}.resource-row code{font-family:var(--ncp-font-mono);font-size:.76rem}.image-row-actions{display:flex;align-items:center;justify-content:center;gap:4px}.danger-button{min-height:34px;padding:0 9px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.skeleton-row i{width:76%;height:12px}.empty-state{display:grid;min-height:170px;place-items:center;align-content:center;padding:24px;color:var(--ncp-text-subtle);font-size:.82rem;text-align:center}
.hub-workspace{display:grid;grid-template-columns:minmax(340px,.85fr) minmax(500px,1.35fr);gap:12px;height:clamp(470px,calc(100dvh - 352px),570px);min-height:0}.repository-list{display:flex;min-height:0;overflow-y:auto;flex-direction:column}.repository-list>button{display:grid;grid-template-columns:38px minmax(0,1fr) auto;align-items:center;gap:10px;min-height:66px;padding:8px 14px;border-bottom:1px solid var(--ncp-line);text-align:left;transition:background-color .16s ease,box-shadow .16s ease}.repository-list>button:hover,.repository-list>button.active{background:var(--ncp-surface-hover)}.repo-icon{width:36px;height:36px;align-self:center}.repo-icon img{display:block;width:72%;height:72%;object-fit:contain}.repo-copy{display:grid;min-width:0;gap:3px}.repo-copy strong{display:flex;align-items:center;gap:4px;overflow:hidden;color:var(--ncp-text);font-size:.85rem;text-overflow:ellipsis;white-space:nowrap}.official-badge{flex:0 0 auto;color:#2772d7}.repo-copy small{display:-webkit-box;overflow:hidden;color:var(--ncp-text-subtle);font-size:.72rem;line-height:1.35;-webkit-box-orient:vertical;-webkit-line-clamp:2}.repo-stars{display:flex;align-items:center;gap:3px;color:#a66b08;font-size:.7rem}.repository-list>footer{display:flex;min-height:46px;align-items:center;justify-content:center;gap:14px;margin-top:auto;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.75rem}.repository-list>footer button{display:grid;width:32px;height:32px;place-items:center;border:1px solid var(--ncp-line);border-radius:8px}.repository-loading{display:grid}.repository-loading>div{display:grid;gap:8px;padding:15px;border-bottom:1px solid var(--ncp-line)}.repository-loading i:first-child{width:45%;height:12px}.repository-loading i:last-child{width:82%;height:9px}
.resource-pagination{display:flex;min-height:52px;align-items:center;justify-content:space-between;gap:12px;padding:8px 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.82rem}.resource-pagination div{display:flex;align-items:center;gap:8px}.resource-pagination button{min-height:34px;padding:0 11px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-text-muted);font-weight:680}.resource-pagination button:disabled{cursor:not-allowed;opacity:.42}.resource-pagination strong{min-width:62px;color:var(--ncp-text);text-align:center}
.repository-detail{display:flex;min-height:0;overflow-y:auto;flex-direction:column;padding:16px}.repository-detail>header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.repository-detail>header>div{display:flex;align-items:center;gap:11px}.repository-detail>header>div>div{display:grid;gap:3px}.repository-detail header strong{font-size:1.05rem}.repository-detail header small,.pull-count{color:var(--ncp-text-subtle);font-size:.76rem}.detail-icon{width:44px;height:44px}.repository-detail>p{min-height:0;margin:12px 0;color:var(--ncp-text-muted);font-size:.82rem;line-height:1.55}.tag-title{display:flex;align-items:flex-end;justify-content:space-between;gap:12px;padding-top:11px;border-top:1px solid var(--ncp-line)}.tag-title>div{display:grid;gap:2px}.tag-title strong{font-size:.88rem}.tag-title span{color:var(--ncp-text-subtle);font-size:.72rem}.tag-title code{max-width:55%;overflow:hidden;padding:5px 8px;border-radius:7px;background:var(--ncp-surface-quiet);color:var(--ncp-primary-strong);font-family:var(--ncp-font-mono);font-size:.72rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:12px}.tag-grid>button{display:grid;gap:3px;padding:11px 12px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;text-align:left}.tag-grid>button:hover,.tag-grid>button.active{border-color:rgba(36,104,216,.42);background:var(--ncp-primary-soft)}.tag-grid strong{overflow:hidden;font-size:.8rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid>i{height:54px}.repository-detail>footer{display:flex;position:sticky;bottom:-16px;align-items:center;justify-content:space-between;gap:12px;margin-top:auto;padding:12px 0 0;border-top:1px solid var(--ncp-line);background:#fff}.repository-detail>footer>span{color:var(--ncp-text-subtle);font-size:.74rem}.detail-empty{display:grid;flex:1;place-items:center;align-content:center;gap:8px;color:var(--ncp-text-subtle)}.detail-empty strong{color:var(--ncp-text);font-size:.92rem}.detail-empty span{font-size:.78rem}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}
.repository-list>button.active{box-shadow:inset 0 0 0 1px rgba(52,116,212,.2)}
.hub-search{width:min(680px,65vw)}.hub-search :deep(.el-input){width:0;min-width:0;flex:1 1 auto}.hub-search :deep(.ncp-select){width:140px;flex:0 0 140px}
.repository-list{position:relative;max-height:650px;overflow-y:auto}.repository-list>button{grid-template-columns:38px minmax(0,1fr) 86px;background:#fff}.repository-refreshing{position:sticky;z-index:2;top:0;display:flex;min-height:36px;align-items:center;justify-content:center;gap:7px;border-bottom:1px solid var(--ncp-line);background:rgba(255,255,255,.94);color:var(--ncp-text-muted);font-size:.75rem;backdrop-filter:blur(8px)}.repo-icon{background:hsl(var(--repo-hue,215) 65% 94%);color:hsl(var(--repo-hue,215) 56% 38%);font-size:.9rem;font-weight:800}.repo-stats{display:grid;justify-items:end;gap:3px;color:var(--ncp-text-subtle)}.repo-stats small{display:flex;align-items:center;gap:3px;font-size:.67rem}.repo-stats time{font-size:.65rem}.tag-picker{display:grid;gap:14px;margin-top:14px}.tag-picker label{display:grid;gap:7px}.tag-picker label>span{color:var(--ncp-text-muted);font-size:.75rem;font-weight:700}.tag-picker select{min-height:42px;padding:0 12px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;color:var(--ncp-text);font-size:.82rem}.tag-picker dl{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));margin:0;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.tag-picker dl>div{display:grid;gap:4px;padding:12px}.tag-picker dl>div+div{border-left:1px solid var(--ncp-line)}.tag-picker dt{color:var(--ncp-text-subtle);font-size:.69rem}.tag-picker dd{overflow:hidden;margin:0;color:var(--ncp-text);font-size:.76rem;text-overflow:ellipsis;white-space:nowrap}.tag-picker-loading{height:96px;margin-top:14px}
.download-workspace{overflow:hidden}.download-workspace>header{display:flex;min-height:64px;align-items:center;justify-content:space-between;gap:16px;padding:10px 16px;border-bottom:1px solid var(--ncp-line)}.download-workspace>header>div{display:grid;gap:3px}.download-workspace>header strong{font-size:.92rem}.download-workspace>header span{color:var(--ncp-text-subtle);font-size:.75rem}.download-head,.download-row{display:grid;grid-template-columns:minmax(240px,1.25fr) 112px minmax(250px,1.1fr) 100px 120px 132px;align-items:center;gap:14px;padding:0 16px}.download-head{min-height:44px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.78rem;font-weight:720}.download-row{min-height:72px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.8rem;transition:background-color .18s ease}.download-row:hover{background:var(--ncp-surface-hover)}.download-row>div:first-child{display:grid;min-width:0;gap:3px}.download-row strong,.download-row small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.download-row strong{color:var(--ncp-text);font-size:.84rem}.download-row small{color:var(--ncp-text-subtle);font-size:.72rem}.job-state{display:inline-flex;min-height:26px;align-items:center;justify-content:center;padding:3px 10px;border:1px solid transparent;border-radius:999px;background:var(--ncp-surface-quiet);font-size:.72rem;font-weight:700;white-space:nowrap}.job-state--running{border-color:rgba(52,116,212,.14);background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.job-state--completed{border-color:rgba(35,134,111,.15);background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.job-state--queued{background:#f0edff;color:#6c55b5}.job-state--deleted{background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.job-state--failed,.job-state--interrupted{border-color:rgba(201,83,97,.14);background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.job-progress{display:grid;width:100%;min-width:0;gap:5px}.job-progress>span{overflow:hidden;width:100%;height:7px;border-radius:999px;background:#e8eef6}.job-progress i{display:block;height:100%;border-radius:inherit;background:linear-gradient(90deg,#5a8fe0,var(--ncp-primary));transition:width .2s linear}.job-progress small{font-variant-numeric:tabular-nums}.job-actions{display:flex;align-items:center;justify-content:center;gap:6px}.job-actions button{display:inline-flex;min-height:32px;align-items:center;justify-content:center;gap:5px;padding:0 9px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-primary-strong);font-weight:700;white-space:nowrap}.job-actions .job-delete{width:32px;padding:0;color:var(--ncp-danger-strong)}
.download-workspace>header .download-actions{display:flex;align-items:center;gap:8px}.download-actions select{min-height:40px;padding:0 30px 0 11px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;color:var(--ncp-text-muted);font-size:.78rem}
.mode-actions{width:min(460px,45vw)}.mode-actions :deep(.el-input){width:0;min-width:0;flex:1 1 auto}.download-mode-actions{width:min(620px,58vw)}.download-mode-actions :deep(.ncp-select){width:150px;flex:0 0 150px}
.resource-head button{width:100%;min-height:32px;justify-content:flex-start;padding:0;border:0;background:transparent;box-shadow:none}.resource-head>button:nth-child(3),.resource-head>button:nth-child(4){justify-content:center}.resource-head>:nth-child(3),.resource-head>:nth-child(4),.resource-head>:nth-child(5),.resource-head>:nth-child(6),.resource-row>:nth-child(3),.resource-row>:nth-child(4),.resource-row>:nth-child(5),.resource-row>:nth-child(6){justify-self:center;text-align:center}.resource-head>span,.resource-head>button{background:transparent}
.hub-workspace{height:clamp(340px,calc(100dvh - 380px),650px);min-height:0;overflow:hidden}.repository-list{height:100%;max-height:none;border-radius:14px;background:#fff}.repository-list>button{margin:4px 6px 0;border:1px solid transparent;border-radius:10px;background:#fff}.repository-list>button:hover{border-color:var(--ncp-line);background:var(--ncp-surface-hover)}.repository-list>button.active{border-color:rgba(52,116,212,.34);background:linear-gradient(90deg,rgba(234,242,253,.9),rgba(248,250,253,.9));box-shadow:0 5px 16px rgba(42,83,140,.08)}.repo-icon img{width:24px;height:24px;object-fit:contain}.repo-icon b{font-size:.9rem}
.repository-detail{height:100%;min-height:0}.repository-detail>p{min-height:0;margin:10px 0;line-height:1.5}.repository-facts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));margin:0 0 10px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.repository-facts>div{display:grid;min-width:0;gap:3px;padding:8px 10px}.repository-facts>div:nth-child(n+4){border-top:1px solid var(--ncp-line)}.repository-facts>div:not(:nth-child(3n+1)){border-left:1px solid var(--ncp-line)}.repository-facts dt{color:var(--ncp-text-subtle);font-size:.68rem}.repository-facts dd{overflow:hidden;margin:0;color:var(--ncp-text);font-size:.76rem;font-weight:650;text-overflow:ellipsis;white-space:nowrap}.tag-picker{min-height:0;gap:10px;margin-top:10px}.tag-picker :deep(.ncp-select){width:100%}.tag-picker dl{grid-template-columns:repeat(2,minmax(0,1fr));background:#fff}.tag-picker dl>div{padding:9px 11px}.tag-picker dl>div+div{border-left:0}.tag-picker dl>div:nth-child(even){border-left:1px solid var(--ncp-line)}.tag-picker dl>div:nth-child(n+3){border-top:1px solid var(--ncp-line)}.tag-picker dd{white-space:normal;overflow-wrap:anywhere}.repository-detail>footer .primary-button{flex:0 0 auto}
.download-head>:nth-child(n+2),.download-row>:nth-child(n+2){justify-self:center;text-align:center}.download-row .job-state{justify-self:center}.job-live{color:var(--ncp-success-strong);font-size:.72rem;font-weight:680}
.resource-head button.active{background:transparent;color:var(--ncp-primary-strong);font-weight:800}
.hub-search{width:min(680px,48vw)}
.repository-list>button{min-height:76px;margin:0 6px}
.repository-detail{overflow:hidden}
.tag-title :deep(.ncp-select){width:min(310px,48%)}
.tag-picker{overflow:auto;padding-right:2px}
.repository-detail>footer{position:static;bottom:auto}
.download-head,.download-row{grid-template-columns:minmax(240px,1.25fr) 112px minmax(190px,260px) 100px 120px 112px}
.job-state--queued{border-color:#cbd5e1;background:#eef2f7;color:#52657d}
.job-state--cancelled{border-color:#ead1a7;background:#fff4e3;color:#9a5b13}
.job-state--deleted{border-color:#d5dce6;background:#edf1f5;color:#5f6f83}
.job-state--failed{border-color:rgba(201,83,97,.28);background:#fcecef;color:#a83d4b}
.job-state--interrupted{border-color:#e8c98b;background:#fff3d9;color:#996117}
.resource-select-head{display:flex;min-width:0;align-items:center;gap:8px}.resource-name-cell{display:flex;min-width:0;align-items:center;gap:8px}.resource-select-head input,.resource-name-cell>input{width:16px;height:16px;flex:0 0 auto;accent-color:var(--ncp-primary)}.usage-cell{display:grid;justify-items:center;gap:2px;line-height:1.25}.usage-warning{color:var(--ncp-danger-strong);font-size:.72rem;font-weight:720}.bulk-remove-error>div{min-width:0}.bulk-remove-error ul{margin:4px 0 0;padding-left:17px}.selection-count{white-space:nowrap;font-variant-numeric:tabular-nums}
@media(max-width:1050px){.resource-head,.resource-row{grid-template-columns:minmax(220px,1.4fr) 110px 85px 105px 105px 100px;gap:8px}.hub-workspace{grid-template-columns:minmax(300px,.8fr) minmax(430px,1.2fr)}}
@media(max-width:820px){.image-modebar{align-items:stretch;flex-direction:column}.image-modebar nav{width:100%}.image-modebar nav button{flex:1;justify-content:center}.mode-actions,.download-mode-actions{width:100%;flex-wrap:wrap}.mode-actions :deep(.el-input){min-width:160px;flex:1 1 180px}.hub-search{width:100%}.resource-table,.download-workspace{overflow-x:auto}.resource-head,.resource-row{min-width:780px}.download-workspace>header{min-width:760px}.download-head,.download-row{min-width:900px}.hub-workspace{grid-template-columns:1fr}.repository-list{max-height:430px}.repository-detail{min-height:520px}.tag-title{align-items:stretch;flex-direction:column}.tag-title :deep(.ncp-select){width:100%}.tag-picker dl{grid-template-columns:1fr}.tag-picker dl>div:nth-child(even){border-left:0}.tag-picker dl>div:nth-child(n+2){border-top:1px solid var(--ncp-line)}.tag-grid{grid-template-columns:1fr}}
</style>

<style scoped>
.resource-select-head button { width: auto; min-width: 0; flex: 1; }
.resource-head>button:nth-child(5) { justify-content: center; }
.resource-name-cell .resource-name { flex: 1; }
.mode-actions :deep(.action-button) { flex: 0 0 auto; }
</style>
