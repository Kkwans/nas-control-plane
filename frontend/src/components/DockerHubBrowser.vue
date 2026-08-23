<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { BadgeCheck, ChevronLeft, ChevronRight, Download, RefreshCw, Search, Star } from '@lucide/vue'
import { ElInput, ElMessage, ElTooltip } from 'element-plus'

import {
  NcpApiError,
  type DockerHubRepository,
  type DockerHubTag,
  type JobSnapshot,
} from '@/api/system'
import { followJob, pullDockerImage, requestDockerHubTags, searchDockerHub } from '@/api/docker'
import NcpSelect, { type NcpSelectOption } from '@/components/NcpSelect.vue'
import { formatBytes } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const emit = defineEmits<{
  'job-created': [job: JobSnapshot]
  'job-progress': [job: JobSnapshot]
  'local-refresh': []
}>()

const systemStore = useSystemStore()
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
let hubRequestSequence = 0
let tagRequestSequence = 0

const hubSortOptions: NcpSelectOption[] = [
  { label: '最佳匹配', value: 'relevance' },
  { label: '拉取量', value: 'pulls' },
  { label: '收藏数', value: 'stars' },
  { label: '最近更新', value: 'updated' },
]

const totalPages = computed(() => Math.max(1, Math.ceil(hubCount.value / 20)))
const hostArchitecture = computed(() => normalizeArchitecture(
  systemStore.summary?.host.architecture || systemStore.capabilities?.architecture || '',
))
const recommendedTag = computed(() => tags.value.find((tag) => tag.name !== 'latest' && tagSupportsHost(tag))?.name
  ?? tags.value.find((tag) => tag.name === 'latest' && tagSupportsHost(tag))?.name
  ?? tags.value[0]?.name
  ?? '')
const pullReference = computed(() => {
  if (!selectedRepository.value) return ''
  const prefix = selectedRepository.value.namespace === 'library' ? '' : `${selectedRepository.value.namespace}/`
  return `${prefix}${selectedRepository.value.name}:${selectedTag.value || 'latest'}`
})
const selectedTagDetails = computed(() => tags.value.find((tag) => tag.name === selectedTag.value) ?? null)

onMounted(() => {
  if (!systemStore.summary) void systemStore.refresh({ summary: true })
})

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
    if (requestSequence === hubRequestSequence) hubLoading.value = false
  }
}

function updateHubSort(value: string) {
  hubSort.value = value
  if (repositories.value.length) void runHubSearch(1)
}

async function selectRepository(repository: DockerHubRepository) {
  selectedRepository.value = repository
  tagsLoading.value = true
  tags.value = []
  selectedTag.value = 'latest'
  const requestSequence = ++tagRequestSequence
  try {
    const result = await requestDockerHubTags(repository.namespace, repository.name)
    if (requestSequence !== tagRequestSequence) return
    tags.value = [...result.results].sort(compareTags)
    selectedTag.value = tags.value.find((tag) => tag.name === 'latest')?.name ?? tags.value[0]?.name ?? 'latest'
  } catch (caught) {
    if (requestSequence !== tagRequestSequence) return
    hubError.value = caught instanceof NcpApiError ? caught.message : '镜像标签读取失败。'
  } finally {
    if (requestSequence === tagRequestSequence) tagsLoading.value = false
  }
}

async function submitPull() {
  if (!pullReference.value || submittingReference.value === pullReference.value) return
  const reference = pullReference.value
  submittingReference.value = reference
  try {
    const job = await pullDockerImage(reference, selectedTagDetails.value?.fullSize ?? 0)
    emit('job-created', job)
    void followJob(job.id, (progress) => emit('job-progress', progress)).then((completed) => {
      emit('job-progress', completed)
      if (completed.status === 'completed') {
        ElMessage.success(`镜像 ${reference} 已拉取`)
        emit('local-refresh')
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

function tagOptions(): NcpSelectOption[] {
  return tags.value.map((tag) => ({
    value: tag.name,
    label: `${tag.name}${tag.name === 'latest' ? ' · latest' : tag.name === recommendedTag.value ? ' · 推荐' : ''}`,
  }))
}

function compareTags(left: DockerHubTag, right: DockerHubTag) {
  if (left.name === 'latest') return -1
  if (right.name === 'latest') return 1
  const leftStable = /^\d+(?:\.\d+){1,3}(?:[-.][a-z0-9]+)*$/i.test(left.name)
  const rightStable = /^\d+(?:\.\d+){1,3}(?:[-.][a-z0-9]+)*$/i.test(right.name)
  if (leftStable !== rightStable) return leftStable ? -1 : 1
  return new Date(right.lastUpdated).valueOf() - new Date(left.lastUpdated).valueOf()
}

function compactNumber(value: number) {
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

function dateLabel(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}

function repositoryInitial(repository: DockerHubRepository) {
  return repository.name.slice(0, 1).toUpperCase() || 'D'
}

function repositoryHue(repository: DockerHubRepository) {
  return [...`${repository.namespace}/${repository.name}`].reduce((sum, character) => sum + character.charCodeAt(0), 0) % 360
}

function repositoryLogo(repository: DockerHubRepository) {
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
  return logos[repository.name.toLowerCase()] ?? ''
}
</script>

<template>
  <section class="hub-panel">
    <form class="hub-search" @submit.prevent="runHubSearch(1)">
      <ElInput v-model="hubQuery" clearable aria-label="搜索 Docker Hub 镜像" placeholder="搜索 Docker Hub 镜像，例如 nginx、postgres">
        <template #prefix><Search :size="17" /></template>
      </ElInput>
      <NcpSelect v-model="hubSort" :options="hubSortOptions" accessible-label="搜索结果排序" @update:model-value="updateHubSort(String($event))" />
      <button class="primary-button" type="submit" :disabled="!hubQuery.trim() || hubLoading">搜索镜像</button>
    </form>
    <section class="hub-workspace">
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
            <div v-if="selectedTagDetails.publishedAt"><dt><ElTooltip content="Docker Hub 标签的发布时间或最近更新时间，不代表镜像内部构建时间。" placement="top"><span>版本发布时间</span></ElTooltip></dt><dd>{{ dateLabel(selectedTagDetails.publishedAt) }}</dd></div>
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
    </section>
  </section>
</template>

<style scoped>
.hub-panel { display: grid; min-width: 0; gap: 12px; }
.hub-search { display: flex; width: min(680px, 65vw); align-items: center; gap: 8px; margin-left: auto; }
.hub-search :deep(.el-input) { width: 0; min-width: 0; flex: 1 1 auto; }
.hub-search :deep(.ncp-select) { width: 140px; flex: 0 0 140px; }
.hub-workspace {
  display: grid;
  grid-template-columns: minmax(340px, .85fr) minmax(500px, 1.35fr);
  gap: 12px;
  height: clamp(340px, calc(100dvh - 380px), 650px);
  min-height: 0;
  overflow: hidden;
}
.repository-list {
  position: relative;
  display: flex;
  height: 100%;
  min-height: 0;
  overflow-y: auto;
  flex-direction: column;
  border-radius: 14px;
  background: #fff;
}
.repository-list > button {
  display: grid;
  grid-template-columns: 38px minmax(0, 1fr) 86px;
  align-items: center;
  gap: 10px;
  min-height: 76px;
  margin: 0 6px;
  padding: 8px 14px;
  border: 1px solid transparent;
  border-radius: 10px;
  background: #fff;
  text-align: left;
  transition: background-color .16s ease, box-shadow .16s ease;
}
.repository-list > button:hover {
  border-color: var(--ncp-line);
  background: var(--ncp-surface-hover);
}
.repository-list > button.active {
  border-color: rgba(52,116,212,.34);
  background: linear-gradient(90deg, rgba(234,242,253,.9), rgba(248,250,253,.9));
  box-shadow: 0 5px 16px rgba(42,83,140,.08);
}
.repository-refreshing {
  position: sticky;
  z-index: 2;
  top: 0;
  display: flex;
  min-height: 36px;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border-bottom: 1px solid var(--ncp-line);
  background: rgba(255,255,255,.94);
  color: var(--ncp-text-muted);
  font-size: .75rem;
  backdrop-filter: blur(8px);
}
.repository-loading { display: grid; }
.repository-loading > div { display: grid; gap: 8px; padding: 15px; border-bottom: 1px solid var(--ncp-line); }
.repository-loading i:first-child { width: 45%; height: 12px; }
.repository-loading i:last-child { width: 82%; height: 9px; }
.repo-icon,
.detail-icon {
  display: grid;
  flex: 0 0 auto;
  place-items: center;
  border-radius: 10px;
  background: hsl(var(--repo-hue,215) 65% 94%);
  color: hsl(var(--repo-hue,215) 56% 38%);
  font-size: .9rem;
  font-weight: 800;
}
.repo-icon { width: 36px; height: 36px; }
.repo-icon img { display: block; width: 24px; height: 24px; object-fit: contain; }
.repo-copy { display: grid; min-width: 0; gap: 3px; }
.repo-copy strong { display: flex; align-items: center; gap: 4px; overflow: hidden; color: var(--ncp-text); font-size: .85rem; text-overflow: ellipsis; white-space: nowrap; }
.official-badge { flex: 0 0 auto; color: #2772d7; }
.repo-copy small { display: -webkit-box; overflow: hidden; color: var(--ncp-text-subtle); font-size: .72rem; line-height: 1.35; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.repo-stats { display: grid; justify-items: end; gap: 3px; color: var(--ncp-text-subtle); }
.repo-stats small { display: flex; align-items: center; gap: 3px; font-size: .67rem; }
.repo-stats time { font-size: .65rem; }
.repository-list > footer { display: flex; min-height: 46px; align-items: center; justify-content: center; gap: 14px; margin-top: auto; border-top: 1px solid var(--ncp-line); color: var(--ncp-text-muted); font-size: .75rem; }
.repository-list > footer button { display: grid; width: 32px; height: 32px; place-items: center; border: 1px solid var(--ncp-line); border-radius: 8px; }
.empty-state { display: grid; min-height: 170px; place-items: center; align-content: center; padding: 24px; color: var(--ncp-text-subtle); font-size: .82rem; text-align: center; }
.repository-detail { display: flex; height: 100%; min-height: 0; overflow: hidden; flex-direction: column; padding: 16px; }
.repository-detail > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.repository-detail > header > div { display: flex; align-items: center; gap: 11px; }
.repository-detail > header > div > div { display: grid; gap: 3px; }
.repository-detail header strong { font-size: 1.05rem; }
.repository-detail header small,
.pull-count { color: var(--ncp-text-subtle); font-size: .76rem; }
.detail-icon { width: 44px; height: 44px; }
.repository-detail > p { min-height: 0; margin: 10px 0; color: var(--ncp-text-muted); font-size: .82rem; line-height: 1.5; }
.repository-facts { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); margin: 0 0 10px; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.repository-facts > div { display: grid; min-width: 0; gap: 3px; padding: 8px 10px; }
.repository-facts > div:nth-child(n+4) { border-top: 1px solid var(--ncp-line); }
.repository-facts > div:not(:nth-child(3n+1)) { border-left: 1px solid var(--ncp-line); }
.repository-facts dt { color: var(--ncp-text-subtle); font-size: .68rem; }
.repository-facts dd { overflow: hidden; margin: 0; color: var(--ncp-text); font-size: .76rem; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.tag-title { display: flex; align-items: flex-end; justify-content: space-between; gap: 12px; padding-top: 11px; border-top: 1px solid var(--ncp-line); }
.tag-title > div { display: grid; gap: 2px; }
.tag-title strong { font-size: .88rem; }
.tag-title span { color: var(--ncp-text-subtle); font-size: .72rem; }
.tag-title :deep(.ncp-select) { width: min(310px, 48%); }
.tag-picker { display: grid; min-height: 0; gap: 10px; margin-top: 10px; overflow: auto; padding-right: 2px; }
.tag-picker dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); margin: 0; border: 1px solid var(--ncp-line); border-radius: 10px; background: #fff; }
.tag-picker dl > div { display: grid; gap: 4px; padding: 9px 11px; }
.tag-picker dl > div:nth-child(even) { border-left: 1px solid var(--ncp-line); }
.tag-picker dl > div:nth-child(n+3) { border-top: 1px solid var(--ncp-line); }
.tag-picker dt { color: var(--ncp-text-subtle); font-size: .68rem; }
.tag-picker dd { overflow-wrap: anywhere; margin: 0; color: var(--ncp-text); font-size: .76rem; }
.tag-picker-loading { height: 96px; margin-top: 14px; }
.repository-detail > footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: auto; padding: 12px 0 0; border-top: 1px solid var(--ncp-line); background: #fff; }
.repository-detail > footer > span { color: var(--ncp-text-subtle); font-size: .74rem; }
.primary-button { display: inline-flex; min-height: 40px; align-items: center; justify-content: center; gap: 6px; padding: 0 15px; border-radius: 9px; background: var(--ncp-primary); box-shadow: 0 6px 16px rgba(36,104,216,.16); color: #fff; font-size: .8rem; font-weight: 720; }
.primary-button:disabled { opacity: .5; }
.detail-empty { display: grid; flex: 1; place-items: center; align-content: center; gap: 8px; color: var(--ncp-text-subtle); }
.detail-empty strong { color: var(--ncp-text); font-size: .92rem; }
.detail-empty span { font-size: .78rem; }
.spin { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (max-width: 820px) {
  .hub-search { width: 100%; margin-left: 0; flex-wrap: wrap; }
  .hub-search :deep(.el-input) { min-width: 180px; flex: 1 1 220px; }
  .hub-search :deep(.ncp-select) { flex: 1 1 150px; }
  .hub-search .primary-button { flex: 1 1 100%; }
  .hub-workspace { grid-template-columns: 1fr; height: auto; overflow: visible; }
  .repository-list { max-height: 430px; }
  .repository-detail { min-height: 520px; }
  .tag-title { align-items: stretch; flex-direction: column; }
  .tag-title :deep(.ncp-select) { width: 100%; }
  .tag-picker dl { grid-template-columns: 1fr; }
  .tag-picker dl > div:nth-child(even) { border-left: 0; }
  .tag-picker dl > div:nth-child(n+2) { border-top: 1px solid var(--ncp-line); }
  .repository-detail > footer { align-items: stretch; flex-direction: column; }
  .repository-detail > footer .primary-button { width: 100%; }
}
</style>
