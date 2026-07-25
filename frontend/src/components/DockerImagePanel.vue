<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { BadgeCheck, Box, ChevronLeft, ChevronRight, Download, HardDrive, Image as ImageIcon, RefreshCw, Search, Star, Trash2 } from '@lucide/vue'
import { ElInput, ElMessage, ElMessageBox, ElTooltip } from 'element-plus'

import {
  NcpApiError,
  pullDockerImage,
  removeDockerImage,
  requestDockerHubTags,
  requestDockerImages,
  searchDockerHub,
  type DockerHubRepository,
  type DockerHubTag,
  type DockerImageSummary,
  type DockerInventory,
} from '@/api/system'
import { formatBytes } from '@/domain/overview'

const props = defineProps<{ query: string; containers: DockerInventory['containers'] }>()
type ImageMode = 'local' | 'hub'

const mode = ref<ImageMode>('local')
const images = ref<DockerImageSummary[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const removePending = ref<string | null>(null)
const hubQuery = ref('')
const hubLoading = ref(false)
const hubError = ref<string | null>(null)
const repositories = ref<DockerHubRepository[]>([])
const hubPage = ref(1)
const hubCount = ref(0)
const selectedRepository = ref<DockerHubRepository | null>(null)
const tags = ref<DockerHubTag[]>([])
const tagsLoading = ref(false)
const selectedTag = ref('latest')
const pullPending = ref(false)

const filteredImages = computed(() => {
  const term = props.query.trim().toLowerCase()
  if (!term) return images.value
  return images.value.filter((image) => [image.id, ...image.repoTags, ...image.repoDigests].some((value) => value.toLowerCase().includes(term)))
})
const totalPages = computed(() => Math.max(1, Math.ceil(hubCount.value / 20)))
const pullReference = computed(() => {
  if (!selectedRepository.value) return ''
  const prefix = selectedRepository.value.namespace === 'library' ? '' : `${selectedRepository.value.namespace}/`
  return `${prefix}${selectedRepository.value.name}:${selectedTag.value || 'latest'}`
})

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
    const result = await searchDockerHub(term, page)
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
    tags.value = result.results
    selectedTag.value = result.results.find((tag) => tag.name === 'latest')?.name ?? result.results[0]?.name ?? 'latest'
  } catch (caught) {
    hubError.value = caught instanceof NcpApiError ? caught.message : '镜像标签读取失败。'
  } finally {
    tagsLoading.value = false
  }
}

async function submitPull() {
  if (!pullReference.value || pullPending.value) return
  pullPending.value = true
  try {
    await pullDockerImage(pullReference.value)
    ElMessage.success(`镜像 ${pullReference.value} 已拉取`)
    await refresh()
  } catch (caught) {
    ElMessage.error(caught instanceof NcpApiError ? caught.message : '镜像拉取失败。')
  } finally {
    pullPending.value = false
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

onMounted(refresh)
</script>

<template>
  <section class="image-workspace">
    <header class="image-modebar">
      <nav aria-label="镜像来源">
        <button :class="{ active: mode === 'local' }" type="button" @click="mode = 'local'"><HardDrive :size="17" />本地镜像 <span>{{ images.length }}</span></button>
        <button :class="{ active: mode === 'hub' }" type="button" @click="mode = 'hub'"><Box :size="17" />线上仓库</button>
      </nav>
      <div v-if="mode === 'local'" class="mode-actions">
        <span>由 Docker Engine 实时读取</span>
        <ElTooltip content="刷新本地镜像"><button class="icon-button" type="button" :disabled="loading" @click="refresh"><RefreshCw :class="{ spin: loading }" :size="17" /></button></ElTooltip>
      </div>
      <form v-else class="hub-search" @submit.prevent="runHubSearch(1)">
        <ElInput v-model="hubQuery" clearable placeholder="搜索 Docker Hub 镜像，例如 nginx、postgres">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
        <button class="primary-button" type="submit" :disabled="!hubQuery.trim() || hubLoading">搜索镜像</button>
      </form>
    </header>

    <div v-if="mode === 'local'">
      <div v-if="error" class="inline-error"><span>{{ error }}</span><button type="button" @click="refresh">重新加载</button></div>
      <section class="resource-table panel">
        <div class="resource-head"><span>镜像</span><span>镜像 ID</span><span>大小</span><span>创建日期</span><span>容器引用</span><span>操作</span></div>
        <template v-if="loading && !images.length">
          <div v-for="row in 7" :key="row" class="resource-row skeleton-row"><i v-for="cell in 6" :key="cell" class="ncp-skeleton"></i></div>
        </template>
        <div v-for="image in filteredImages" :key="image.id" class="resource-row">
          <div class="resource-name"><span><ImageIcon :size="18" /></span><div><strong>{{ displayName(image) }}</strong><small>{{ image.repoDigests[0] ?? '本地构建镜像' }}</small></div></div>
          <code>{{ shortId(image.id) }}</code><span>{{ formatBytes(image.sizeBytes) }}</span><span>{{ dateLabel(image.createdAt) }}</span>
          <span>{{ usageCount(image) ? `${usageCount(image)} 个容器` : '未被引用' }}</span>
          <ElTooltip content="删除未使用的本地镜像"><button class="danger-button" type="button" :disabled="removePending === image.id" @click="confirmRemove(image)"><Trash2 :size="15" />删除</button></ElTooltip>
        </div>
        <div v-if="!loading && !filteredImages.length" class="empty-state">没有匹配的本地镜像</div>
      </section>
    </div>

    <div v-else class="hub-workspace">
      <aside class="repository-list panel">
        <div v-if="hubLoading" class="repository-loading"><div v-for="row in 7" :key="row"><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></div></div>
        <button v-for="repository in repositories" :key="`${repository.namespace}/${repository.name}`" type="button" :class="{ active: selectedRepository?.name === repository.name && selectedRepository?.namespace === repository.namespace }" @click="selectRepository(repository)">
          <span class="repo-icon"><ImageIcon :size="18" /></span>
          <span class="repo-copy"><strong>{{ repository.namespace === 'library' ? repository.name : `${repository.namespace}/${repository.name}` }} <BadgeCheck v-if="repository.official" :size="14" /></strong><small>{{ repository.description || '暂无仓库简介' }}</small></span>
          <span class="repo-stars"><Star :size="13" />{{ compactNumber(repository.starCount) }}</span>
        </button>
        <div v-if="hubError" class="empty-state">{{ hubError }}</div>
        <div v-else-if="!hubLoading && !repositories.length" class="empty-state">输入关键字，搜索 Docker Hub 公共镜像</div>
        <footer v-if="hubCount > 20"><button :disabled="hubPage <= 1" @click="runHubSearch(hubPage - 1)"><ChevronLeft :size="16" /></button><span>{{ hubPage }} / {{ totalPages }}</span><button :disabled="hubPage >= totalPages" @click="runHubSearch(hubPage + 1)"><ChevronRight :size="16" /></button></footer>
      </aside>

      <section class="repository-detail panel">
        <template v-if="selectedRepository">
          <header><div><span class="detail-icon"><ImageIcon :size="22" /></span><div><strong>{{ selectedRepository.namespace === 'library' ? selectedRepository.name : `${selectedRepository.namespace}/${selectedRepository.name}` }}</strong><small>{{ selectedRepository.official ? 'Docker 官方镜像' : `发布者：${selectedRepository.publisher}` }}</small></div></div><span class="pull-count">{{ compactNumber(selectedRepository.pullCount) }} 次拉取</span></header>
          <p>{{ selectedRepository.description || '该仓库暂未提供简介。' }}</p>
          <div class="tag-title"><div><strong>选择版本</strong><span>{{ tags.length }} 个最近更新的标签</span></div><code>{{ pullReference }}</code></div>
          <div v-if="tagsLoading" class="tag-grid"><i v-for="row in 8" :key="row" class="ncp-skeleton"></i></div>
          <div v-else class="tag-grid">
            <button v-for="tag in tags" :key="tag.name" type="button" :class="{ active: selectedTag === tag.name }" @click="selectedTag = tag.name"><strong>{{ tag.name }}</strong><small>{{ formatBytes(tag.fullSize) }} · {{ tag.architectures.slice(0, 2).join('、') || '架构未知' }}</small></button>
          </div>
          <footer><span>拉取完成后会自动出现在本地镜像列表</span><button class="primary-button" type="button" :disabled="pullPending || !selectedTag" @click="submitPull"><Download :size="17" />{{ pullPending ? '正在拉取…' : '一键拉取' }}</button></footer>
        </template>
        <div v-else class="detail-empty"><Search :size="28" /><strong>选择一个镜像仓库</strong><span>查看标签、架构并直接拉取到 NAS</span></div>
      </section>
    </div>
  </section>
</template>

<style scoped>
.image-workspace{display:grid;gap:12px}.image-modebar{display:flex;min-height:58px;align-items:center;justify-content:space-between;gap:16px}.image-modebar nav{display:flex;gap:4px;padding:4px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.image-modebar nav button{display:flex;min-height:38px;align-items:center;gap:7px;padding:0 13px;border-radius:8px;color:var(--ncp-text-muted);font-size:.82rem;font-weight:720}.image-modebar nav button.active{background:#fff;box-shadow:0 3px 10px rgba(24,42,72,.08);color:var(--ncp-primary-strong)}.image-modebar nav span{padding:1px 6px;border-radius:10px;background:var(--ncp-primary-soft);font-size:.68rem}.mode-actions,.hub-search{display:flex;align-items:center;gap:8px}.mode-actions>span{color:var(--ncp-text-subtle);font-size:.78rem}.hub-search{width:min(560px,55vw)}.hub-search :deep(.el-input){flex:1}.icon-button,.primary-button,.danger-button{display:inline-flex;min-height:40px;align-items:center;justify-content:center;gap:6px;border-radius:9px;font-size:.8rem;font-weight:720}.icon-button{width:40px;border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.primary-button{padding:0 15px;background:var(--ncp-primary);box-shadow:0 6px 16px rgba(36,104,216,.16);color:#fff}.primary-button:disabled{opacity:.5}.inline-error{display:flex;min-height:44px;align-items:center;justify-content:space-between;padding:8px 12px;border:1px solid rgba(212,81,93,.18);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.8rem}.inline-error button{font-weight:720}.resource-table{overflow:hidden}.resource-head,.resource-row{display:grid;grid-template-columns:minmax(260px,1.5fr) 130px 100px 120px 120px 86px;align-items:center;gap:12px}.resource-head{min-height:44px;padding:0 16px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.8rem;font-weight:720}.resource-row{min-height:68px;padding:0 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.82rem}.resource-row:hover{background:var(--ncp-surface-hover)}.resource-name{display:flex;min-width:0;align-items:center;gap:10px}.resource-name>span,.repo-icon,.detail-icon{display:grid;flex:0 0 auto;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.resource-name>span{width:36px;height:36px}.resource-name>div{display:grid;min-width:0;gap:2px}.resource-name strong,.resource-name small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.resource-name strong{color:var(--ncp-text);font-size:.88rem}.resource-name small{color:var(--ncp-text-subtle);font-size:.73rem}.resource-row code{font-family:var(--ncp-font-mono);font-size:.76rem}.danger-button{min-height:34px;padding:0 9px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.skeleton-row i{width:76%;height:12px}.empty-state{display:grid;min-height:170px;place-items:center;align-content:center;padding:24px;color:var(--ncp-text-subtle);font-size:.82rem;text-align:center}
.hub-workspace{display:grid;grid-template-columns:minmax(340px,.85fr) minmax(500px,1.35fr);gap:12px;min-height:590px}.repository-list{display:flex;overflow:hidden;flex-direction:column}.repository-list>button{display:grid;grid-template-columns:38px minmax(0,1fr) auto;align-items:center;gap:10px;min-height:70px;padding:10px 14px;border-bottom:1px solid var(--ncp-line);text-align:left;transition:background-color .16s ease,box-shadow .16s ease}.repository-list>button:hover,.repository-list>button.active{background:var(--ncp-surface-hover)}.repository-list>button.active{box-shadow:inset 3px 0 0 var(--ncp-primary)}.repo-icon{width:36px;height:36px}.repo-copy{display:grid;min-width:0;gap:3px}.repo-copy strong{display:flex;align-items:center;gap:4px;overflow:hidden;color:var(--ncp-text);font-size:.85rem;text-overflow:ellipsis;white-space:nowrap}.repo-copy small{display:-webkit-box;overflow:hidden;color:var(--ncp-text-subtle);font-size:.72rem;line-height:1.35;-webkit-box-orient:vertical;-webkit-line-clamp:2}.repo-stars{display:flex;align-items:center;gap:3px;color:#a66b08;font-size:.7rem}.repository-list>footer{display:flex;min-height:50px;align-items:center;justify-content:center;gap:14px;margin-top:auto;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.75rem}.repository-list>footer button{display:grid;width:32px;height:32px;place-items:center;border:1px solid var(--ncp-line);border-radius:8px}.repository-loading{display:grid}.repository-loading>div{display:grid;gap:8px;padding:15px;border-bottom:1px solid var(--ncp-line)}.repository-loading i:first-child{width:45%;height:12px}.repository-loading i:last-child{width:82%;height:9px}
.repository-detail{display:flex;flex-direction:column;padding:20px}.repository-detail>header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.repository-detail>header>div{display:flex;align-items:center;gap:11px}.repository-detail>header>div>div{display:grid;gap:3px}.repository-detail header strong{font-size:1.05rem}.repository-detail header small,.pull-count{color:var(--ncp-text-subtle);font-size:.76rem}.detail-icon{width:44px;height:44px}.repository-detail>p{min-height:46px;margin:18px 0;color:var(--ncp-text-muted);font-size:.84rem;line-height:1.7}.tag-title{display:flex;align-items:flex-end;justify-content:space-between;gap:12px;padding-top:14px;border-top:1px solid var(--ncp-line)}.tag-title>div{display:grid;gap:2px}.tag-title strong{font-size:.88rem}.tag-title span{color:var(--ncp-text-subtle);font-size:.72rem}.tag-title code{max-width:55%;overflow:hidden;padding:5px 8px;border-radius:7px;background:var(--ncp-surface-quiet);color:var(--ncp-primary-strong);font-family:var(--ncp-font-mono);font-size:.72rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:12px}.tag-grid>button{display:grid;gap:3px;padding:11px 12px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;text-align:left}.tag-grid>button:hover,.tag-grid>button.active{border-color:rgba(36,104,216,.42);background:var(--ncp-primary-soft)}.tag-grid strong{overflow:hidden;font-size:.8rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}.tag-grid>i{height:54px}.repository-detail>footer{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:auto;padding-top:18px;border-top:1px solid var(--ncp-line)}.repository-detail>footer>span{color:var(--ncp-text-subtle);font-size:.74rem}.detail-empty{display:grid;flex:1;place-items:center;align-content:center;gap:8px;color:var(--ncp-text-subtle)}.detail-empty strong{color:var(--ncp-text);font-size:.92rem}.detail-empty span{font-size:.78rem}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}
@media(max-width:1050px){.resource-head,.resource-row{grid-template-columns:minmax(220px,1.4fr) 110px 85px 105px 105px 78px;gap:8px}.hub-workspace{grid-template-columns:minmax(300px,.8fr) minmax(430px,1.2fr)}}
@media(max-width:820px){.image-modebar{align-items:stretch;flex-direction:column}.image-modebar nav{width:100%}.image-modebar nav button{flex:1;justify-content:center}.hub-search{width:100%}.resource-table{overflow-x:auto}.resource-head,.resource-row{min-width:780px}.hub-workspace{grid-template-columns:1fr}.repository-list{max-height:430px}.repository-detail{min-height:520px}.tag-grid{grid-template-columns:1fr}}
</style>
