<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ArrowUpDown, BadgeCheck, Box, ChevronLeft, ChevronRight, Download, HardDrive, Image as ImageIcon, RefreshCw, Search, Star, Trash2 } from '@lucide/vue'
import { ElInput, ElMessage, ElMessageBox, ElTooltip } from 'element-plus'

import {
  NcpApiError,
  followJob,
  pullDockerImage,
  requestJobs,
  retryJob,
  removeDockerImage,
  requestDockerHubTags,
  requestDockerImages,
  searchDockerHub,
  type DockerHubRepository,
  type DockerHubTag,
  type DockerImageSummary,
  type DockerInventory,
  type JobSnapshot,
} from '@/api/system'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import { useListPreference } from '@/composables/useListPreference'
import { formatBytes } from '@/domain/overview'

const props = defineProps<{ query: string; containers: DockerInventory['containers']; pageSize: number }>()
type ImageMode = 'local' | 'hub' | 'downloads'

const mode = ref<ImageMode>('local')
const images = ref<DockerImageSummary[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const removePending = ref<string | null>(null)
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
const downloadStatus = ref('all')
const downloadPage = ref(1)
const { preference: localPreference, setSort: setLocalSort } = useListPreference('docker.images.local')
const { pageSize: downloadPageSize } = useListPreference('docker.image-downloads')

const filteredImages = computed(() => {
  const term = props.query.trim().toLowerCase()
  const matching = term
    ? images.value.filter((image) => [image.id, ...image.repoTags, ...image.repoDigests].some((value) => value.toLowerCase().includes(term)))
    : images.value
  const key = localPreference.value.sortKey || 'name'
  const direction = localPreference.value.sortDirection === 'desc' ? -1 : 1
  return [...matching].sort((left, right) => {
    if (key === 'size') return (left.sizeBytes - right.sizeBytes) * direction
    if (key === 'created') return (new Date(left.createdAt).valueOf() - new Date(right.createdAt).valueOf()) * direction
    return displayName(left).localeCompare(displayName(right), 'zh-CN') * direction
  })
})
const localPageCount = computed(() => Math.max(1, Math.ceil(filteredImages.value.length / props.pageSize)))
const pagedImages = computed(() => {
  const start = (localPage.value - 1) * props.pageSize
  return filteredImages.value.slice(start, start + props.pageSize)
})
const totalPages = computed(() => Math.max(1, Math.ceil(hubCount.value / 20)))
const pullReference = computed(() => {
  if (!selectedRepository.value) return ''
  const prefix = selectedRepository.value.namespace === 'library' ? '' : `${selectedRepository.value.namespace}/`
  return `${prefix}${selectedRepository.value.name}:${selectedTag.value || 'latest'}`
})
const selectedTagDetails = computed(() => tags.value.find((tag) => tag.name === selectedTag.value) ?? null)
const recommendedTag = computed(() => tags.value.find((tag) => tag.name !== 'latest' && tag.architectures.some((architecture) => architecture.includes('arm64')))?.name ?? tags.value.find((tag) => tag.name === 'latest')?.name ?? '')
const activePullCount = computed(() => downloadJobs.value.filter((job) => job.status === 'queued' || job.status === 'running').length)
const filteredDownloadJobs = computed(() => downloadStatus.value === 'all' ? downloadJobs.value : downloadJobs.value.filter((job) => job.status === downloadStatus.value))
const downloadPageCount = computed(() => Math.max(1, Math.ceil(filteredDownloadJobs.value.length / downloadPageSize.value)))
const pagedDownloadJobs = computed(() => {
  const start = (downloadPage.value - 1) * downloadPageSize.value
  return filteredDownloadJobs.value.slice(start, start + downloadPageSize.value)
})

watch(() => [props.query, props.pageSize], () => { localPage.value = 1 })
watch(localPageCount, (count) => {
  if (localPage.value > count) localPage.value = count
})
watch([downloadStatus, downloadPageSize], () => { downloadPage.value = 1 })
watch(downloadPageCount, (count) => {
  if (downloadPage.value > count) downloadPage.value = count
})

async function toggleLocalSort(key: 'name' | 'size' | 'created') {
  const direction = localPreference.value.sortKey === key && localPreference.value.sortDirection === 'asc' ? 'desc' : 'asc'
  await setLocalSort(key, direction)
}

async function refresh() {
  loading.value = true
  error.value = null
  try {
    images.value = (await requestDockerImages()).images
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
  try {
    const result = await searchDockerHub(term, page, 20, hubSort.value)
    repositories.value = result.results
    hubCount.value = result.count
    hubPage.value = result.page
    const firstRepository = result.results[0]
    if (firstRepository) await selectRepository(firstRepository)
    else selectedRepository.value = null
  } catch (caught) {
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
    const job = await pullDockerImage(reference)
    upsertDownloadJob(job)
    mode.value = 'downloads'
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
    downloadJobs.value = await requestJobs('docker-image-pull')
    for (const job of downloadJobs.value.filter((item) => item.status === 'queued' || item.status === 'running')) {
      void followJob(job.id, upsertDownloadJob).catch(() => undefined)
    }
  } catch {
    downloadJobs.value = []
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

async function confirmRemove(image: DockerImageSummary) {
  const name = displayName(image)
  try {
    await ElMessageBox.confirm(`将从 NAS 删除本地镜像“${name}”。正在被容器使用的镜像会被 Docker 拒绝删除。`, '删除本地镜像', {
      confirmButtonText: '确认删除', cancelButtonText: '取消', type: 'warning',
    })
  } catch { return }
  removePending.value = image.id
  try {
    await removeDockerImage(image.id)
    ElMessage.success(`已删除镜像 ${name}`)
    await refresh()
  } catch (caught) {
    ElMessage.error(caught instanceof NcpApiError ? caught.message : '镜像删除失败。')
  } finally {
    removePending.value = null
  }
}

function displayName(image: DockerImageSummary) { return image.repoTags[0] ?? '未标记镜像' }
function shortId(imageId: string) { return imageId.replace(/^sha256:/, '').slice(0, 12) }
function usageCount(image: DockerImageSummary) {
  return props.containers.filter((container) => image.repoTags.includes(container.image) || container.image === image.id).length
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
        <span>由 Docker Engine 实时读取</span>
        <ElTooltip content="刷新本地镜像"><button class="icon-button" type="button" :disabled="loading" @click="refresh"><RefreshCw :class="{ spin: loading }" :size="17" /></button></ElTooltip>
      </div>
      <form v-else-if="mode === 'hub'" class="hub-search" @submit.prevent="runHubSearch(1)">
        <ElInput v-model="hubQuery" clearable placeholder="搜索 Docker Hub 镜像，例如 nginx、postgres">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
        <select v-model="hubSort" aria-label="搜索结果排序" @change="repositories.length && runHubSearch(1)">
          <option value="relevance">最佳匹配</option>
          <option value="pulls">拉取量</option>
          <option value="stars">收藏数</option>
          <option value="updated">最近更新</option>
        </select>
        <button class="primary-button" type="submit" :disabled="!hubQuery.trim() || hubLoading">搜索镜像</button>
      </form>
    </header>

    <div v-if="mode === 'local'">
      <div v-if="error" class="inline-error"><span>{{ error }}</span><button type="button" @click="refresh">重新加载</button></div>
      <section class="resource-table panel">
        <div class="resource-head">
          <button type="button" @click="toggleLocalSort('name')">镜像<ArrowUpDown :size="13" /></button>
          <span>镜像 ID</span>
          <button type="button" @click="toggleLocalSort('size')">大小<ArrowUpDown :size="13" /></button>
          <button type="button" @click="toggleLocalSort('created')">创建日期<ArrowUpDown :size="13" /></button>
          <span>容器引用</span><span>操作</span>
        </div>
        <template v-if="loading && !images.length">
          <div v-for="row in 7" :key="row" class="resource-row skeleton-row"><i v-for="cell in 6" :key="cell" class="ncp-skeleton"></i></div>
        </template>
        <div v-for="image in pagedImages" :key="image.id" class="resource-row">
          <div class="resource-name"><span><ImageIcon :size="18" /></span><div><strong>{{ displayName(image) }}</strong><small>{{ image.repoDigests[0] ?? '本地构建镜像' }}</small></div></div>
          <code>{{ shortId(image.id) }}</code><span>{{ formatBytes(image.sizeBytes) }}</span><span>{{ dateLabel(image.createdAt) }}</span>
          <span>{{ usageCount(image) ? `${usageCount(image)} 个容器` : '未被引用' }}</span>
          <ElTooltip content="删除未使用的本地镜像"><button class="danger-button" type="button" :disabled="removePending === image.id" @click="confirmRemove(image)"><Trash2 :size="15" />删除</button></ElTooltip>
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
          <span class="repo-icon" :style="{ '--repo-hue': repositoryHue(repository) }">{{ repositoryInitial(repository) }}</span>
          <span class="repo-copy"><strong>{{ repository.namespace === 'library' ? repository.name : `${repository.namespace}/${repository.name}` }} <BadgeCheck v-if="repository.official" :size="14" /></strong><small>{{ repository.description || '暂无仓库简介' }}</small></span>
          <span class="repo-stats"><small><Download :size="12" />{{ compactNumber(repository.pullCount) }}</small><small><Star :size="12" />{{ compactNumber(repository.starCount) }}</small><time>{{ dateLabel(repository.lastUpdated) }}</time></span>
        </button>
        <div v-if="hubError" class="empty-state">{{ hubError }}</div>
        <div v-else-if="!hubLoading && !repositories.length" class="empty-state">输入关键字，搜索 Docker Hub 公共镜像</div>
        <footer v-if="hubCount > 20"><button :disabled="hubPage <= 1" @click="runHubSearch(hubPage - 1)"><ChevronLeft :size="16" /></button><span>{{ hubPage }} / {{ totalPages }}</span><button :disabled="hubPage >= totalPages" @click="runHubSearch(hubPage + 1)"><ChevronRight :size="16" /></button></footer>
      </aside>

      <section class="repository-detail panel">
        <template v-if="selectedRepository">
          <header><div><span class="detail-icon repo-icon" :style="{ '--repo-hue': repositoryHue(selectedRepository) }">{{ repositoryInitial(selectedRepository) }}</span><div><strong>{{ selectedRepository.namespace === 'library' ? selectedRepository.name : `${selectedRepository.namespace}/${selectedRepository.name}` }}</strong><small>{{ selectedRepository.official ? 'Docker 官方镜像' : `发布者：${selectedRepository.publisher}` }}</small></div></div><span class="pull-count">{{ compactNumber(selectedRepository.pullCount) }} 次拉取</span></header>
          <p>{{ selectedRepository.description || '该仓库暂未提供简介。' }}</p>
          <div class="tag-title"><div><strong>选择版本</strong><span>{{ tags.length }} 个最近更新的标签</span></div><code>{{ pullReference }}</code></div>
          <div v-if="tagsLoading" class="tag-picker-loading ncp-skeleton"></div>
          <div v-else class="tag-picker">
            <label><span>镜像标签</span><select v-model="selectedTag"><option v-for="tag in tags" :key="tag.name" :value="tag.name">{{ tag.name }}{{ tag.name === 'latest' ? ' · latest' : tag.name === recommendedTag ? ' · 推荐' : '' }}</option></select></label>
            <dl v-if="selectedTagDetails">
              <div><dt>大小</dt><dd>{{ formatBytes(selectedTagDetails.fullSize) }}</dd></div>
              <div><dt>架构</dt><dd>{{ selectedTagDetails.architectures.join('、') || '未知' }}</dd></div>
              <div><dt>最近更新</dt><dd>{{ dateLabel(selectedTagDetails.lastUpdated) }}</dd></div>
            </dl>
          </div>
          <footer><span>任务将进入后台下载队列，最多并行拉取 3 个镜像</span><button class="primary-button" type="button" :disabled="submittingReference === pullReference || !selectedTag" @click="submitPull"><Download :size="17" />{{ submittingReference === pullReference ? '正在提交' : '加入下载队列' }}</button></footer>
        </template>
        <div v-else class="detail-empty"><Search :size="28" /><strong>选择一个镜像仓库</strong><span>查看标签、架构并直接拉取到 NAS</span></div>
      </section>
    </div>

    <section v-else class="download-workspace panel">
      <header><div><strong>镜像下载任务</strong><span>最多同时拉取 3 个镜像，刷新页面后记录仍会保留。</span></div><div class="download-actions"><select v-model="downloadStatus" aria-label="下载任务状态"><option value="all">全部状态</option><option value="queued">排队中</option><option value="running">下载中</option><option value="completed">已完成</option><option value="failed">失败</option><option value="interrupted">已中断</option></select><button class="icon-button" type="button" @click="loadDownloadJobs"><RefreshCw :size="17" /></button></div></header>
      <div class="download-head"><span>镜像</span><span>状态</span><span>进度</span><span>速度</span><span>更新时间</span><span>操作</span></div>
      <article v-for="job in pagedDownloadJobs" :key="job.id" class="download-row">
        <div><strong>{{ job.reference }}</strong><small>{{ job.message }}</small></div>
        <span :class="['job-state', `job-state--${job.status}`]">{{ job.status === 'queued' ? '排队中' : job.status === 'running' ? '下载中' : job.status === 'completed' ? '已完成' : job.status === 'interrupted' ? '已中断' : '失败' }}</span>
        <div class="job-progress"><span><i :style="{ width: `${job.progress}%` }"></i></span><small>{{ job.progress }}% · {{ formatBytes(job.downloadedBytes) }} / {{ job.totalBytes ? formatBytes(job.totalBytes) : '未知' }}</small></div>
        <span>{{ job.speedBytes > 0 ? `${formatBytes(job.speedBytes)}/s` : '—' }}</span>
        <time>{{ dateLabel(job.updatedAt) }}</time>
        <button v-if="job.status === 'failed' || job.status === 'interrupted'" type="button" @click="retryDownload(job)">重试</button>
        <span v-else>—</span>
      </article>
      <div v-if="!filteredDownloadJobs.length" class="empty-state">没有匹配的镜像下载任务</div>
      <footer v-else class="resource-pagination">
        <ListPageSizeControl list-key="docker.image-downloads" />
        <div><button type="button" :disabled="downloadPage <= 1" @click="downloadPage -= 1">上一页</button><strong>{{ downloadPage }} / {{ downloadPageCount }}</strong><button type="button" :disabled="downloadPage >= downloadPageCount" @click="downloadPage += 1">下一页</button></div>
      </footer>
    </section>
  </section>
</template>

<style scoped>
.image-workspace{display:grid;gap:12px}.image-modebar{display:flex;min-height:58px;align-items:center;justify-content:space-between;gap:16px}.image-modebar nav{display:flex;gap:4px;padding:4px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.image-modebar nav button{display:flex;min-height:38px;align-items:center;gap:7px;padding:0 13px;border-radius:8px;color:var(--ncp-text-muted);font-size:.82rem;font-weight:720}.image-modebar nav button.active{background:#fff;box-shadow:0 3px 10px rgba(24,42,72,.08);color:var(--ncp-primary-strong)}.image-modebar nav span{padding:1px 6px;border-radius:10px;background:var(--ncp-primary-soft);font-size:.68rem}.mode-actions,.hub-search{display:flex;align-items:center;gap:8px}.mode-actions>span{color:var(--ncp-text-subtle);font-size:.78rem}.hub-search{width:min(560px,55vw)}.hub-search :deep(.el-input){flex:1}.icon-button,.primary-button,.danger-button{display:inline-flex;min-height:40px;align-items:center;justify-content:center;gap:6px;border-radius:9px;font-size:.8rem;font-weight:720}.icon-button{width:40px;border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.primary-button{padding:0 15px;background:var(--ncp-primary);box-shadow:0 6px 16px rgba(36,104,216,.16);color:#fff}.primary-button:disabled{opacity:.5}.inline-error{display:flex;min-height:44px;align-items:center;justify-content:space-between;padding:8px 12px;border:1px solid rgba(212,81,93,.18);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.8rem}.inline-error button{font-weight:720}.resource-table{overflow:hidden}.resource-head,.resource-row{display:grid;grid-template-columns:minmax(260px,1.5fr) 130px 100px 120px 120px 86px;align-items:center;gap:12px}.resource-head{min-height:44px;padding:0 16px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.8rem;font-weight:720}.resource-head button{display:inline-flex;align-items:center;gap:5px;color:inherit;font:inherit}.resource-head button:hover{color:var(--ncp-primary-strong)}.resource-row{min-height:68px;padding:0 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.82rem}.resource-row:hover{background:var(--ncp-surface-hover)}.resource-name{display:flex;min-width:0;align-items:center;gap:10px}.resource-name>span,.repo-icon,.detail-icon{display:grid;flex:0 0 auto;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.resource-name>span{width:36px;height:36px}.resource-name>div{display:grid;min-width:0;gap:2px}.resource-name strong,.resource-name small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.resource-name strong{color:var(--ncp-text);font-size:.88rem}.resource-name small{color:var(--ncp-text-subtle);font-size:.73rem}.resource-row code{font-family:var(--ncp-font-mono);font-size:.76rem}.danger-button{min-height:34px;padding:0 9px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.skeleton-row i{width:76%;height:12px}.empty-state{display:grid;min-height:170px;place-items:center;align-content:center;padding:24px;color:var(--ncp-text-subtle);font-size:.82rem;text-align:center}
.hub-workspace{display:grid;grid-template-columns:minmax(340px,.85fr) minmax(500px,1.35fr);gap:12px;min-height:590px}.repository-list{display:flex;overflow:hidden;flex-direction:column}.repository-list>button{display:grid;grid-template-columns:38px minmax(0,1fr) auto;align-items:center;gap:10px;min-height:70px;padding:10px 14px;border-bottom:1px solid var(--ncp-line);text-align:left;transition:background-color .16s ease,box-shadow .16s ease}.repository-list>button:hover,.repository-list>button.active{background:var(--ncp-surface-hover)}.repository-list>button.active{box-shadow:inset 3px 0 0 var(--ncp-primary)}.repo-icon{width:36px;height:36px}.repo-copy{display:grid;min-width:0;gap:3px}.repo-copy strong{display:flex;align-items:center;gap:4px;overflow:hidden;color:var(--ncp-text);font-size:.85rem;text-overflow:ellipsis;white-space:nowrap}.repo-copy small{display:-webkit-box;overflow:hidden;color:var(--ncp-text-subtle);font-size:.72rem;line-height:1.35;-webkit-box-orient:vertical;-webkit-line-clamp:2}.repo-stars{display:flex;align-items:center;gap:3px;color:#a66b08;font-size:.7rem}.repository-list>footer{display:flex;min-height:50px;align-items:center;justify-content:center;gap:14px;margin-top:auto;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.75rem}.repository-list>footer button{display:grid;width:32px;height:32px;place-items:center;border:1px solid var(--ncp-line);border-radius:8px}.repository-loading{display:grid}.repository-loading>div{display:grid;gap:8px;padding:15px;border-bottom:1px solid var(--ncp-line)}.repository-loading i:first-child{width:45%;height:12px}.repository-loading i:last-child{width:82%;height:9px}
.resource-pagination{display:flex;min-height:52px;align-items:center;justify-content:space-between;gap:12px;padding:8px 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.82rem}.resource-pagination div{display:flex;align-items:center;gap:8px}.resource-pagination button{min-height:34px;padding:0 11px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-text-muted);font-weight:680}.resource-pagination button:disabled{cursor:not-allowed;opacity:.42}.resource-pagination strong{min-width:62px;color:var(--ncp-text);text-align:center}
.repository-detail{display:flex;flex-direction:column;padding:20px}.repository-detail>header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.repository-detail>header>div{display:flex;align-items:center;gap:11px}.repository-detail>header>div>div{display:grid;gap:3px}.repository-detail header strong{font-size:1.05rem}.repository-detail header small,.pull-count{color:var(--ncp-text-subtle);font-size:.76rem}.detail-icon{width:44px;height:44px}.repository-detail>p{min-height:46px;margin:18px 0;color:var(--ncp-text-muted);font-size:.84rem;line-height:1.7}.tag-title{display:flex;align-items:flex-end;justify-content:space-between;gap:12px;padding-top:14px;border-top:1px solid var(--ncp-line)}.tag-title>div{display:grid;gap:2px}.tag-title strong{font-size:.88rem}.tag-title span{color:var(--ncp-text-subtle);font-size:.72rem}.tag-title code{max-width:55%;overflow:hidden;padding:5px 8px;border-radius:7px;background:var(--ncp-surface-quiet);color:var(--ncp-primary-strong);font-family:var(--ncp-font-mono);font-size:.72rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:12px}.tag-grid>button{display:grid;gap:3px;padding:11px 12px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;text-align:left}.tag-grid>button:hover,.tag-grid>button.active{border-color:rgba(36,104,216,.42);background:var(--ncp-primary-soft)}.tag-grid strong{overflow:hidden;font-size:.8rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid>i{height:54px}.repository-detail>footer{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:auto;padding-top:18px;border-top:1px solid var(--ncp-line)}.repository-detail>footer>span{color:var(--ncp-text-subtle);font-size:.74rem}.detail-empty{display:grid;flex:1;place-items:center;align-content:center;gap:8px;color:var(--ncp-text-subtle)}.detail-empty strong{color:var(--ncp-text);font-size:.92rem}.detail-empty span{font-size:.78rem}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}
.repository-list>button.active{box-shadow:inset 0 0 0 1px rgba(52,116,212,.2)}
.hub-search{width:min(680px,65vw)}.hub-search select{min-height:40px;padding:0 30px 0 11px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;color:var(--ncp-text-muted);font-size:.78rem}
.repository-list{position:relative;max-height:650px;overflow-y:auto}.repository-list>button{grid-template-columns:38px minmax(0,1fr) 86px;background:#fff}.repository-refreshing{position:sticky;z-index:2;top:0;display:flex;min-height:36px;align-items:center;justify-content:center;gap:7px;border-bottom:1px solid var(--ncp-line);background:rgba(255,255,255,.94);color:var(--ncp-text-muted);font-size:.75rem;backdrop-filter:blur(8px)}.repo-icon{background:hsl(var(--repo-hue,215) 65% 94%);color:hsl(var(--repo-hue,215) 56% 38%);font-size:.9rem;font-weight:800}.repo-stats{display:grid;justify-items:end;gap:3px;color:var(--ncp-text-subtle)}.repo-stats small{display:flex;align-items:center;gap:3px;font-size:.67rem}.repo-stats time{font-size:.65rem}.tag-picker{display:grid;gap:14px;margin-top:14px}.tag-picker label{display:grid;gap:7px}.tag-picker label>span{color:var(--ncp-text-muted);font-size:.75rem;font-weight:700}.tag-picker select{min-height:42px;padding:0 12px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;color:var(--ncp-text);font-size:.82rem}.tag-picker dl{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));margin:0;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.tag-picker dl>div{display:grid;gap:4px;padding:12px}.tag-picker dl>div+div{border-left:1px solid var(--ncp-line)}.tag-picker dt{color:var(--ncp-text-subtle);font-size:.69rem}.tag-picker dd{overflow:hidden;margin:0;color:var(--ncp-text);font-size:.76rem;text-overflow:ellipsis;white-space:nowrap}.tag-picker-loading{height:96px;margin-top:14px}
.download-workspace{overflow:hidden}.download-workspace>header{display:flex;min-height:64px;align-items:center;justify-content:space-between;gap:16px;padding:10px 16px;border-bottom:1px solid var(--ncp-line)}.download-workspace>header>div{display:grid;gap:3px}.download-workspace>header strong{font-size:.92rem}.download-workspace>header span{color:var(--ncp-text-subtle);font-size:.75rem}.download-head,.download-row{display:grid;grid-template-columns:minmax(240px,1.4fr) 90px minmax(220px,1fr) 100px 120px 70px;align-items:center;gap:14px;padding:0 16px}.download-head{min-height:44px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.78rem;font-weight:720}.download-row{min-height:72px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.8rem;transition:background-color .18s ease}.download-row:hover{background:var(--ncp-surface-hover)}.download-row>div:first-child{display:grid;min-width:0;gap:3px}.download-row strong,.download-row small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.download-row strong{color:var(--ncp-text);font-size:.84rem}.download-row small{color:var(--ncp-text-subtle);font-size:.72rem}.job-state{justify-self:start;padding:4px 9px;border-radius:999px;background:var(--ncp-surface-quiet);font-size:.72rem;font-weight:700}.job-state--running,.job-state--completed{background:var(--ncp-success-soft);color:var(--ncp-success-strong)}.job-state--queued{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.job-state--failed,.job-state--interrupted{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.job-progress{display:grid;gap:5px}.job-progress>span{overflow:hidden;height:6px;border-radius:999px;background:var(--ncp-surface-quiet)}.job-progress i{display:block;height:100%;border-radius:inherit;background:var(--ncp-primary);transition:width .2s ease}.job-progress small{font-variant-numeric:tabular-nums}.download-row>button{justify-self:start;padding:6px 10px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-primary-strong);font-weight:700}
.download-workspace>header .download-actions{display:flex;align-items:center;gap:8px}.download-actions select{min-height:40px;padding:0 30px 0 11px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;color:var(--ncp-text-muted);font-size:.78rem}
@media(max-width:1050px){.resource-head,.resource-row{grid-template-columns:minmax(220px,1.4fr) 110px 85px 105px 105px 78px;gap:8px}.hub-workspace{grid-template-columns:minmax(300px,.8fr) minmax(430px,1.2fr)}}
@media(max-width:820px){.image-modebar{align-items:stretch;flex-direction:column}.image-modebar nav{width:100%}.image-modebar nav button{flex:1;justify-content:center}.hub-search{width:100%}.resource-table,.download-workspace{overflow-x:auto}.resource-head,.resource-row{min-width:780px}.download-workspace>header{min-width:760px}.download-head,.download-row{min-width:900px}.hub-workspace{grid-template-columns:1fr}.repository-list{max-height:430px}.repository-detail{min-height:520px}.tag-grid{grid-template-columns:1fr}}
</style>
