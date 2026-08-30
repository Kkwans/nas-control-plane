<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  CircleAlert,
  CircleCheck,
  Clock3,
  ArrowUpRight,
  EyeOff,
  Globe2,
  Image,
  Pencil,
  Plus,
  Radar,
  RefreshCw,
  Search,
  Settings2,
  Star,
  Trash2,
  Upload,
} from '@lucide/vue'
import {
  ElButton,
  ElDrawer,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSwitch,
  ElTooltip,
  type FormInstance,
  type FormRules,
} from 'element-plus'

import type { Site, SiteProfileInput } from '@/api/control'
import ActionButton from '@/components/ActionButton.vue'
import GroupedDirectory, { type GroupedDirectoryGroup } from '@/components/GroupedDirectory.vue'
import ListIconButton from '@/components/ListIconButton.vue'
import NcpSelect from '@/components/NcpSelect.vue'
import SegmentedTabs, { type SegmentedTab } from '@/components/SegmentedTabs.vue'
import StatusPill from '@/components/StatusPill.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { SiteSyncError, useSitesStore } from '@/stores/sites'
import { useSystemStore } from '@/stores/system'

type SiteFilter = 'running' | 'all' | 'hidden' | 'ignored'

const sitesStore = useSitesStore()
const systemStore = useSystemStore()
const hostName = window.location.hostname
const query = ref('')
const siteFilter = ref<SiteFilter>('running')
const selectedCategory = ref('全部分类')
const editOpen = ref(false)
const selectedSite = ref<Site | null>(null)
const saving = ref(false)
const failedIcons = ref(new Set<string>())
const favoritePending = ref<string | null>(null)
const draft = ref<SiteProfileInput>(emptyProfile())
const iconInput = ref<HTMLInputElement | null>(null)
const siteForm = ref<FormInstance>()
const pendingIcon = ref<File | null>(null)
const pendingIconPreview = ref('')
const maxSiteIconBytes = 2 * 1024 * 1024
const supportedSiteIconTypes = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/svg+xml'])
const formRules: FormRules<SiteProfileInput> = {
  name: [{ required: true, message: '请输入站点名称', trigger: 'blur' }],
}

const sites = computed(() => sitesStore.sites)
const categoryOptions = computed(() => ['全部分类', ...new Set(sites.value.map((site) => site.category).filter(Boolean))])
const categorySelectOptions = computed(() => categoryOptions.value.map((category) => ({ label: category, value: category })))
const siteFilterTabs = computed<SegmentedTab[]>(() => [
  { value: 'running', label: '运行中', count: sites.value.filter((site) => !site.hidden && site.state === 'running').length },
  { value: 'all', label: '全部站点', count: sites.value.filter((site) => !site.hidden).length },
  { value: 'hidden', label: '已隐藏', count: sites.value.filter((site) => site.hidden).length },
  { value: 'ignored', label: '已忽略', count: sitesStore.ignoredSites.length },
])
const filteredSites = computed(() => {
  const term = query.value.trim().toLowerCase()
  return sites.value.filter((site) => {
    if (siteFilter.value === 'ignored') return false
    const matchesFilter = siteFilter.value === 'hidden'
      ? site.hidden
      : !site.hidden && (siteFilter.value === 'all' || site.state === 'running')
    const matchesCategory = selectedCategory.value === '全部分类' || site.category === selectedCategory.value
    const searchable = `${site.name} ${site.description} ${site.category} ${(site.ports ?? []).join(' ')} ${site.launchUrl}`.toLowerCase()
    return matchesFilter && matchesCategory && (!term || searchable.includes(term))
  })
})
const groupedSites = computed(() => {
  const groups = new Map<string, Site[]>()
  const ordered = [...filteredSites.value].sort((left, right) =>
    Number(right.favorite) - Number(left.favorite) ||
    left.sortOrder - right.sortOrder ||
    left.name.localeCompare(right.name, 'zh-CN'),
  )
  for (const site of ordered) {
    const category = site.category || '未分类'
    groups.set(category, [...(groups.get(category) ?? []), site])
  }
  return [...groups.entries()].map(([category, items]) => ({ category, items }))
})
const siteDirectoryGroups = computed<GroupedDirectoryGroup[]>(() => groupedSites.value.map((group) => ({
  key: group.category,
  title: group.category,
  count: group.items.length,
})))
const stats = computed<WorkspaceStat[]>(() => [
  { label: '全部站点', value: sites.value.filter((site) => !site.hidden).length },
  { label: '正在运行', value: sites.value.filter((site) => !site.hidden && site.state === 'running').length, tone: 'success' },
  { label: '已收藏', value: sites.value.filter((site) => !site.hidden && site.favorite).length },
])
const discoveryTone = computed(() => sitesStore.discovery.status === 'complete'
  ? 'complete'
  : sitesStore.discovery.status === 'partial'
    ? 'partial'
    : 'unavailable')
const discoveryTitle = computed(() => sitesStore.discovery.status === 'complete'
  ? '自动识别已完成'
  : sitesStore.discovery.status === 'partial'
    ? '自动识别完成，部分入口待核验'
    : '网页入口探测暂不可用')
const discoveryDescription = computed(() => {
  const summary = sitesStore.discovery
  if (summary.status === 'unavailable') return '已保留手动站点和明确标注的入口；恢复探测后会自动补齐其余站点。'
  if (summary.failedCount > 0) return `已确认 ${summary.verifiedCount} 个入口，另有 ${summary.failedCount} 个候选端口没有返回可识别网页。`
  return `本次检查了 ${summary.candidateCount} 个候选入口，已确认的网页入口均已加入目录。`
})
const collectedLabel = computed(() => formatCollectedAt(sitesStore.collectedAt))

onMounted(() => void refreshSites(false))

function formatCollectedAt(value: string) {
  const date = new Date(value)
  if (!value || Number.isNaN(date.valueOf())) return '尚未完成采集'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date)
}

async function refreshSites(announce = true) {
  await sitesStore.refresh()
  if (sitesStore.error) {
    if (announce) ElMessage.error(sitesStore.error)
    return
  }
  if (!announce) return
  if (sitesStore.discovery.status === 'complete') ElMessage.success(`识别完成，已确认 ${sitesStore.discovery.verifiedCount} 个站点入口`)
  else if (sitesStore.discovery.status === 'partial') ElMessage.warning(`识别完成，${sitesStore.discovery.failedCount} 个候选入口待核验`)
  else ElMessage.warning('网页入口探测暂不可用，已显示可确认的站点')
}

function emptyProfile(): SiteProfileInput {
  return {
    name: '',
    description: '',
    iconUrl: '',
    category: '',
    primaryPort: 0,
    launchUrl: '',
    favorite: false,
    sortOrder: 0,
    hidden: false,
  }
}

function profileFor(site: Site, overrides: Partial<SiteProfileInput> = {}): SiteProfileInput {
  return {
    name: site.name,
    description: site.description,
    iconUrl: site.iconUrl,
    category: site.category,
    primaryPort: site.primaryPort,
    launchUrl: site.launchUrl,
    favorite: site.favorite,
    sortOrder: site.sortOrder,
    hidden: site.hidden,
    ...overrides,
  }
}

function siteURL(site: Site, port = site.primaryPort) {
  if (port === site.primaryPort && site.launchUrl) return site.launchUrl
  return `${systemStore.preferences.siteDefaultProtocol}://${hostName}:${port}`
}

function siteLaunchable(site: Site) {
  return Boolean(site.launchUrl?.trim()) || site.primaryPort > 0
}

const linkTarget = computed(() => systemStore.preferences.linkOpenMode === 'new-tab' ? '_blank' : '_self')

function trackVisit(site: Site) {
  void sitesStore.visit(site.id)
}

function siteTone(state: Site['state']) {
  return state === 'running' ? 'healthy' : state === 'degraded' ? 'degraded' : 'pending'
}

function siteStateLabel(state: Site['state']) {
  return state === 'running' ? '运行中' : state === 'degraded' ? '部分运行' : '已停止'
}

function siteHue(site: Site) {
  let hash = 0
  for (const character of site.name) hash = (hash * 31 + character.charCodeAt(0)) % 360
  return { '--site-hue': String(hash) }
}

function sourceLabel(site: Site) {
  if (site.source === 'manual') return '手动添加'
  if (site.source === 'edited') return '自定义资料'
  if (site.source === 'built-in') return '内置项目资料'
  if (site.source === 'labels') return '项目标签'
  return '自动识别'
}

function visitedLabel(value: string | null) {
  if (!value) return '尚未访问'
  const date = new Date(value)
  if (Number.isNaN(date.valueOf())) return '尚未访问'
  const elapsed = Date.now() - date.valueOf()
  if (elapsed < 60_000) return '刚刚访问'
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit' }).format(date)
}

function openEditor(site: Site) {
  resetPendingIcon()
  selectedSite.value = site
  draft.value = profileFor(site)
  editOpen.value = true
}

function openCreator() {
  resetPendingIcon()
  selectedSite.value = null
  draft.value = emptyProfile()
  editOpen.value = true
}

async function saveSite() {
  const valid = await siteForm.value?.validate().catch(() => false)
  if (valid === false) return
  if (!draft.value.launchUrl.trim() && draft.value.primaryPort <= 0) {
    ElMessage.warning('请填写完整入口 URL，或设置主入口端口')
    return
  }
  saving.value = true
  try {
    if (selectedSite.value) await sitesStore.save(selectedSite.value.id, draft.value)
    else await sitesStore.create(draft.value, pendingIcon.value)
    editOpen.value = false
    resetPendingIcon()
    ElMessage.success(selectedSite.value ? '站点资料已保存' : '站点已添加')
  } catch {
    ElMessage.error('站点资料保存失败，请检查 URL 和输入内容')
  } finally {
    saving.value = false
  }
}

async function removeSite(site: Site) {
  try {
    await ElMessageBox.confirm(
      site.source === 'manual'
        ? `确定删除手动站点“${site.name}”吗？`
        : `确定忽略自动识别的“${site.name}”吗？之后可在已忽略站点中恢复。`,
      site.source === 'manual' ? '删除站点' : '忽略站点',
      { type: 'warning', confirmButtonText: '确定', cancelButtonText: '取消' },
    )
    await sitesStore.remove(site.id)
    ElMessage.success(site.source === 'manual' ? '站点已删除' : '站点已忽略')
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') {
      if (error instanceof SiteSyncError) ElMessage.warning('站点操作已完成，但列表同步失败，请重新识别。')
      else ElMessage.error('站点删除失败')
    }
  }
}

async function restoreIgnoredSite(siteId: string) {
  try {
    await sitesStore.restore(siteId)
    ElMessage.success('站点已恢复，将在下次识别时重新出现')
  } catch {
    ElMessage.error('站点恢复失败')
  }
}

async function uploadIcon(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (!supportedSiteIconTypes.has(file.type) || file.size > maxSiteIconBytes) {
    ElMessage.error('请选择 2 MB 以内的 PNG、JPEG、WebP 或 SVG 图标')
    if (iconInput.value) iconInput.value.value = ''
    return
  }
  if (!selectedSite.value) {
    pendingIcon.value = file
    if (pendingIconPreview.value) URL.revokeObjectURL(pendingIconPreview.value)
    pendingIconPreview.value = URL.createObjectURL(file)
    if (iconInput.value) iconInput.value.value = ''
    return
  }
  try {
    await sitesStore.uploadIcon(selectedSite.value.id, file)
    failedIcons.value.delete(selectedSite.value.id)
    ElMessage.success('站点图标已上传')
  } catch {
    ElMessage.error('图标上传失败，请使用 2 MB 以内的 PNG、JPEG、WebP 或 SVG 文件')
  } finally {
    if (iconInput.value) iconInput.value.value = ''
  }
}

function resetPendingIcon() {
  pendingIcon.value = null
  if (pendingIconPreview.value) URL.revokeObjectURL(pendingIconPreview.value)
  pendingIconPreview.value = ''
  if (iconInput.value) iconInput.value.value = ''
}

onBeforeUnmount(resetPendingIcon)

async function toggleFavorite(site: Site) {
  if (favoritePending.value) return
  favoritePending.value = site.id
  try {
    await sitesStore.save(site.id, profileFor(site, { favorite: !site.favorite }))
  } catch {
    ElMessage.error('收藏状态保存失败')
  } finally {
    favoritePending.value = null
  }
}

function markIconFailed(site: Site) {
  failedIcons.value = new Set(failedIcons.value).add(site.id)
}

function sitesForCategory(category: string) {
  return groupedSites.value.find((group) => group.category === category)?.items ?? []
}
</script>

<template>
  <div class="page workspace-page sites-page">
    <WorkspaceHeader title="站点管理" description="快速启动 NAS 上的 Web 应用，并维护入口资料" :icon="Globe2" :stats="stats">
      <template #filters>
        <SegmentedTabs v-model="siteFilter" :tabs="siteFilterTabs" accessible-label="站点状态筛选" />
        <NcpSelect v-model="selectedCategory" class="category-filter" :options="categorySelectOptions" accessible-label="按分类筛选站点" />
      </template>
      <template #tools>
        <ElInput v-model="query" class="site-search" clearable placeholder="搜索站点、用途或端口" aria-label="搜索站点">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="sitesStore.error" class="site-error" role="alert">
      <span>{{ sitesStore.error }}</span>
      <ActionButton size="sm" :icon="RefreshCw" @click="refreshSites">重新加载</ActionButton>
    </div>

    <section v-if="!sitesStore.loading || sites.length" class="discovery-status panel" :class="`discovery-status--${discoveryTone}`" aria-labelledby="discovery-status-title">
      <div class="discovery-status__summary">
        <span class="discovery-status__icon" aria-hidden="true">
          <CircleCheck v-if="discoveryTone === 'complete'" :size="21" />
          <CircleAlert v-else-if="discoveryTone === 'partial'" :size="21" />
          <Radar v-else :size="21" />
        </span>
        <div>
          <h2 id="discovery-status-title">{{ discoveryTitle }}</h2>
          <p>{{ discoveryDescription }}</p>
        </div>
      </div>
      <dl class="discovery-status__metrics">
        <div><dt>候选入口</dt><dd>{{ sitesStore.discovery.candidateCount }}</dd></div>
        <div><dt>确认站点</dt><dd>{{ sitesStore.discovery.verifiedCount }}</dd></div>
        <div><dt>待核验</dt><dd>{{ sitesStore.discovery.failedCount }}</dd></div>
      </dl>
      <div class="discovery-status__time"><Clock3 :size="15" /><span>采集于 {{ collectedLabel }}</span></div>
      <details v-if="sitesStore.discovery.issues.length" class="discovery-issues">
        <summary>查看 {{ sitesStore.discovery.issues.length }} 个待核验入口</summary>
        <div class="discovery-issues__list">
          <article v-for="issue in sitesStore.discovery.issues" :key="`${issue.siteId}:${issue.ports.join(',')}`">
            <div><strong>{{ issue.name }}</strong><span>{{ issue.projectId }}</span></div>
            <code>{{ issue.ports.length ? issue.ports.join('、') : '未发现端口' }}</code>
            <p>{{ issue.reason }}</p>
          </article>
        </div>
      </details>
    </section>

    <section v-if="sitesStore.loading && !sites.length" class="directory-panel panel" aria-label="正在加载站点">
      <div v-for="item in 6" :key="item" class="site-row site-row--skeleton">
        <i class="ncp-skeleton"></i><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i>
      </div>
    </section>

    <section v-else-if="groupedSites.length" class="directory-section" aria-labelledby="directory-title">
      <div class="section-heading">
        <div><h2 id="directory-title">站点目录</h2><p>按用途分类，快速查看状态和全部入口</p></div>
        <div class="section-actions">
          <ActionButton variant="primary" :icon="Plus" @click="openCreator">添加站点</ActionButton>
          <ActionButton :icon="RefreshCw" :loading="sitesStore.loading" @click="refreshSites">重新识别</ActionButton>
        </div>
      </div>
      <GroupedDirectory :groups="siteDirectoryGroups" label="按用途分组的站点目录">
        <template #header>
          <div class="site-columns" aria-hidden="true">
            <span>站点</span><span>状态</span><span>入口</span><span>来源</span><span>最近访问</span><span>操作</span>
          </div>
        </template>
        <template #items="{ group }">
          <article v-for="site in sitesForCategory(group.key)" :key="site.id" class="site-row">
            <a v-if="site.state === 'running' && siteLaunchable(site)" class="site-identity site-identity--link" :href="siteURL(site)" :target="linkTarget" rel="noreferrer" @click="trackVisit(site)">
              <div class="site-logo" :class="{ 'site-logo--image': site.iconUrl && !failedIcons.has(site.id) }" :style="siteHue(site)">
                <img v-if="site.iconUrl && !failedIcons.has(site.id)" :src="site.iconUrl" alt="" @error="markIconFailed(site)" />
                <span v-else>{{ site.name.slice(0, 1).toUpperCase() }}</span>
              </div>
              <div><strong>{{ site.name }}</strong><p>{{ site.description }}</p></div>
            </a>
            <div v-else class="site-identity">
              <div class="site-logo" :class="{ 'site-logo--image': site.iconUrl && !failedIcons.has(site.id) }" :style="siteHue(site)">
                <img v-if="site.iconUrl && !failedIcons.has(site.id)" :src="site.iconUrl" alt="" @error="markIconFailed(site)" />
                <span v-else>{{ site.name.slice(0, 1).toUpperCase() }}</span>
              </div>
              <div><strong>{{ site.name }}</strong><p>{{ site.description }}</p></div>
            </div>
            <StatusPill :label="siteStateLabel(site.state)" :tone="siteTone(site.state)" />
            <div class="site-ports">
              <ElTooltip v-for="port in (site.ports ?? []).slice(0, 3)" :key="port" :content="port === site.primaryPort ? '主入口' : `打开端口 ${port}`" placement="top">
                <a :class="{ primary: port === site.primaryPort }" :href="siteURL(site, port)" :target="linkTarget" rel="noreferrer" @click="trackVisit(site)">{{ port }}</a>
              </ElTooltip>
              <span v-if="(site.ports ?? []).length > 3">+{{ site.ports.length - 3 }}</span>
            </div>
            <span class="site-source">{{ sourceLabel(site) }}</span>
            <span class="site-visited">{{ visitedLabel(site.lastVisitedAt) }}</span>
            <div class="row-actions">
              <ListIconButton :icon="Star" :active="site.favorite" :loading="favoritePending === site.id" :label="site.favorite ? `取消收藏 ${site.name}` : `收藏 ${site.name}`" @click="toggleFavorite(site)" />
              <ListIconButton :icon="Pencil" :label="`编辑 ${site.name}`" @click="openEditor(site)" />
              <ListIconButton :icon="Trash2" tone="danger" :label="site.source === 'manual' ? `删除 ${site.name}` : `忽略 ${site.name}`" @click="removeSite(site)" />
              <a class="row-open" :class="{ disabled: site.state !== 'running' || !siteLaunchable(site) }" :href="site.state === 'running' && siteLaunchable(site) ? siteURL(site) : undefined" :target="linkTarget" rel="noreferrer" :aria-disabled="site.state !== 'running' || !siteLaunchable(site)" @click="site.state === 'running' && siteLaunchable(site) && trackVisit(site)">打开<ArrowUpRight :size="16" /></a>
            </div>
          </article>
        </template>
      </GroupedDirectory>
    </section>

    <section v-if="siteFilter === 'ignored' && sitesStore.ignoredSites.length" class="directory-panel panel ignored-sites">
      <article v-for="site in sitesStore.ignoredSites" :key="site.projectId" class="site-row">
        <div class="site-identity"><div class="site-logo"><EyeOff :size="19" /></div><div><strong>{{ site.name || site.projectId }}</strong><p>{{ site.description || '自动识别站点已被忽略' }}</p></div></div>
        <span class="site-source">已忽略</span>
        <div class="row-actions"><ElButton @click="restoreIgnoredSite(site.projectId)">恢复识别</ElButton></div>
      </article>
    </section>

    <section v-if="!sitesStore.loading && !groupedSites.length && (siteFilter !== 'ignored' || !sitesStore.ignoredSites.length)" class="empty-sites panel">
      <Globe2 :size="28" />
      <div>
        <h2>{{ siteFilter === 'hidden' ? '没有隐藏的站点' : siteFilter === 'ignored' ? '没有已忽略站点' : '没有匹配的站点' }}</h2>
        <p>可调整筛选条件，或重新识别具有 Web 入口的 Docker 项目。</p>
      </div>
    </section>

    <ElDrawer v-model="editOpen" class="site-editor" :title="selectedSite ? '编辑站点资料' : '添加站点'" size="min(540px, 100%)" append-to-body>
      <div v-if="selectedSite" class="editor-context">
        <div class="site-logo" :class="{ 'site-logo--image': draft.iconUrl && !failedIcons.has(selectedSite.id) }" :style="siteHue(selectedSite)">
          <img v-if="draft.iconUrl && !failedIcons.has(selectedSite.id)" :src="draft.iconUrl" alt="" />
          <span v-else>{{ draft.name.slice(0, 1).toUpperCase() }}</span>
        </div>
        <div><strong>{{ selectedSite.projectId }}</strong><span>继续关联当前 Docker 项目</span></div>
      </div>
      <ElForm ref="siteForm" :model="draft" :rules="formRules" label-position="top">
        <div class="editor-grid">
          <ElFormItem label="站点名称" prop="name" required><ElInput v-model="draft.name" maxlength="120" placeholder="例如 NAS 文件浏览器" /></ElFormItem>
          <ElFormItem label="站点分类">
            <ElSelect v-model="draft.category" filterable allow-create default-first-option>
              <ElOption v-for="category in categoryOptions.filter((item) => item !== '全部分类')" :key="category" :label="category" :value="category" />
            </ElSelect>
          </ElFormItem>
        </div>
        <ElFormItem label="简介"><ElInput v-model="draft.description" type="textarea" :rows="3" maxlength="500" show-word-limit /></ElFormItem>
        <ElFormItem>
          <template #label><span class="required-group-label">完整入口 URL <small>与端口二选一</small></span></template>
          <ElInput v-model="draft.launchUrl" placeholder="例如 https://nas.example.com/app/；留空按 NAS 地址和端口生成" />
          <span class="field-help">入口 URL 与主入口端口至少填写一项</span>
        </ElFormItem>
        <ElFormItem>
          <template #label><span class="field-label"><Image :size="15" />Logo 或 favicon 地址</span></template>
          <ElInput v-model="draft.iconUrl" placeholder="支持 http:// 或 https:// 完整图片地址" />
          <div class="icon-upload">
            <input ref="iconInput" type="file" accept="image/png,image/jpeg,image/webp,image/svg+xml" @change="uploadIcon" />
            <button class="icon-upload__preview" type="button" @click="iconInput?.click()">
              <img v-if="pendingIconPreview" :src="pendingIconPreview" alt="待上传图标预览" />
              <img v-else-if="selectedSite?.iconUrl && !failedIcons.has(selectedSite.id)" :src="selectedSite.iconUrl" alt="当前站点图标" />
              <Upload v-else :size="20" />
            </button>
            <div>
              <ElButton @click="iconInput?.click()"><Upload :size="15" />{{ selectedSite ? '更换本地图标' : '上传站点图标' }}</ElButton>
              <span>PNG、JPEG、WebP 或 SVG，最大 2 MB；上传图标优先于 URL</span>
            </div>
          </div>
        </ElFormItem>
        <div class="editor-grid">
          <ElFormItem v-if="selectedSite?.ports.length" label="主入口端口">
            <ElSelect v-model="draft.primaryPort">
              <ElOption v-for="port in selectedSite?.ports ?? []" :key="port" :label="String(port)" :value="port" />
            </ElSelect>
          </ElFormItem>
          <ElFormItem v-else>
            <template #label><span class="required-group-label">主入口端口 <small>与 URL 二选一</small></span></template>
            <ElInputNumber v-model="draft.primaryPort" :min="0" :max="65535" controls-position="right" />
          </ElFormItem>
          <ElFormItem label="目录排序">
            <ElInputNumber v-model="draft.sortOrder" :min="-100000" :max="100000" :step="10" controls-position="right" />
          </ElFormItem>
        </div>
        <div class="switch-grid">
          <label><ElSwitch v-model="draft.favorite" /><span><Star :size="15" />加入快捷启动</span></label>
          <label><ElSwitch v-model="draft.hidden" /><span><EyeOff :size="15" />隐藏此站点</span></label>
        </div>
      </ElForm>
      <template #footer>
        <ElButton @click="editOpen = false">取消</ElButton>
        <ElButton type="primary" :loading="saving" @click="saveSite"><Settings2 :size="16" />{{ selectedSite ? '保存站点资料' : '添加站点' }}</ElButton>
      </template>
    </ElDrawer>
  </div>
</template>

<style scoped>
.required-group-label::before{margin-right:4px;color:var(--el-color-danger);content:'*'}.required-group-label small{margin-left:5px;color:var(--ncp-text-subtle);font-size:.68rem;font-weight:500}
.site-search{width:min(360px,36vw)}.category-filter{width:150px}
.site-error{display:flex;min-height:46px;align-items:center;justify-content:space-between;gap:12px;padding:8px 12px;border:1px solid rgba(212,81,93,.2);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.82rem}.site-error button{min-height:34px;padding:0 11px;border-radius:8px;background:#fff;color:inherit;font-weight:700}
.section-heading{display:flex;align-items:end;justify-content:space-between;margin:2px 2px 9px}.section-heading h2{margin:0;font-size:1rem;letter-spacing:-.02em}.section-heading p{margin:2px 0 0;color:var(--ncp-text-subtle);font-size:.78rem}
.site-logo{display:grid;width:42px;height:42px;flex:0 0 auto;place-items:center;overflow:hidden;border:1px solid hsl(var(--site-hue) 60% 84%);border-radius:11px;background:hsl(var(--site-hue) 72% 95%);color:hsl(var(--site-hue) 62% 39%);font-family:var(--ncp-font-latin);font-weight:800}.site-logo--image{border-color:transparent;background:transparent}.site-logo--large{width:52px;height:52px;border-radius:14px;font-size:1.08rem}.site-logo img{width:100%;height:100%;object-fit:contain}
.directory-panel{overflow:hidden}.site-row{display:grid;min-height:78px;grid-template-columns:minmax(250px,1.6fr) 104px 190px 104px 100px 190px;align-items:center;gap:12px;padding:10px 15px;border-top:1px solid var(--ncp-line);transition:background var(--ncp-duration-fast)}.site-row:hover{background:var(--ncp-surface-hover)}.site-identity{display:flex;min-width:0;align-items:center;gap:10px}.site-identity>div:last-child{min-width:0}.site-identity strong{display:block;overflow:hidden;font-size:.9rem;text-overflow:ellipsis;white-space:nowrap}.site-identity p{overflow:hidden;margin:3px 0 0;color:var(--ncp-text-subtle);font-size:.76rem;line-height:1.4;text-overflow:ellipsis;white-space:nowrap}.site-ports,.row-actions{display:flex;align-items:center;gap:5px}.site-ports a{display:grid;min-width:46px;min-height:32px;place-items:center;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-text-muted);font-family:var(--ncp-font-mono);font-size:.72rem}.site-ports a.primary{border-color:rgba(36,104,216,.2);background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.site-ports>span,.site-source,.site-visited{color:var(--ncp-text-subtle);font-size:.75rem}.row-actions{justify-content:flex-end}.row-actions button{display:grid;width:36px;height:36px;place-items:center;border-radius:8px;background:transparent;color:var(--ncp-text-muted)}.row-actions button:hover{background:var(--ncp-surface-quiet);color:var(--ncp-primary-strong)}.row-open{display:flex;min-height:36px;flex:0 0 auto;align-items:center;gap:3px;padding:0 11px;border-radius:8px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.78rem;font-weight:700;white-space:nowrap}.row-open.disabled{pointer-events:none;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}
.site-row--skeleton i{height:12px}.site-row--skeleton i:first-child{width:62%}.site-row--skeleton i:nth-child(2){width:72%}.site-row--skeleton i:nth-child(3){width:58%}.site-row--skeleton i:last-child{width:76%}.site-row:hover .row-actions{opacity:1}.row-actions{opacity:.72;transition:opacity var(--ncp-duration-fast)}
.ignored-sites .site-row{grid-template-columns:minmax(280px,1fr) 100px 140px}
.row-actions button.danger:hover{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}
.empty-sites{display:flex;min-height:220px;align-items:center;justify-content:center;gap:14px;color:var(--ncp-text-subtle)}.empty-sites h2{margin:0;color:var(--ncp-text);font-size:.95rem}.empty-sites p{margin:4px 0 0;font-size:.8rem}.editor-context{display:flex;align-items:center;gap:11px;margin:-5px 0 18px;padding:12px;border-radius:11px;background:var(--ncp-surface-quiet)}.editor-context>div:last-child{display:grid;min-width:0;gap:2px}.editor-context strong{overflow:hidden;font-size:.8rem;text-overflow:ellipsis;white-space:nowrap}.editor-context span{color:var(--ncp-text-subtle);font-size:.74rem}.editor-grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}.editor-grid :deep(.el-select),.editor-grid :deep(.el-input-number){width:100%}.field-label,.switch-grid label,.switch-grid span{display:flex;align-items:center;gap:6px}.switch-grid{display:grid;grid-template-columns:1fr 1fr;gap:10px}.switch-grid label{min-height:48px;padding:0 12px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet)}.switch-grid span{color:var(--ncp-text-muted);font-size:.8rem}
.field-help{display:block;margin-top:5px;color:var(--ncp-text-subtle);font-size:.72rem}.icon-upload{display:flex;align-items:center;gap:11px;margin-top:9px}.icon-upload input{display:none}.icon-upload>div{display:grid;justify-items:start;gap:5px}.icon-upload span{color:var(--ncp-text-subtle);font-size:.72rem}.icon-upload__preview{display:grid;width:58px;height:58px;flex:0 0 auto;place-items:center;overflow:hidden;border:1px dashed var(--ncp-line-strong);border-radius:13px;background:var(--ncp-surface-quiet);color:var(--ncp-primary-strong)}.icon-upload__preview:hover{border-color:rgba(52,116,212,.45);background:var(--ncp-primary-soft)}.icon-upload__preview img{width:100%;height:100%;object-fit:cover}
@media(max-width:900px){.site-search{width:100%}}
@media(max-width:680px){.category-filter{width:100%}.directory-panel{border:0;background:transparent;box-shadow:none}.site-row{display:grid;grid-template-columns:1fr auto;gap:10px;margin-top:9px;padding:14px;border:1px solid var(--ncp-line);border-radius:12px;background:#fff}.site-identity{grid-column:1/-1}.site-ports{grid-column:1}.site-row>.status-pill{grid-column:2;grid-row:2}.row-actions{grid-column:1/-1}.row-actions .row-open{flex:1;justify-content:center}.editor-grid,.switch-grid{grid-template-columns:1fr}.empty-sites{padding:28px 18px;text-align:center;flex-direction:column}}
.section-actions{display:flex;align-items:center;gap:8px}
.section-actions>.action-button{min-width:112px}
.site-row{grid-template-columns:minmax(280px,1.8fr) 92px 170px 104px 100px 190px}
.site-row>.status-pill{justify-self:center}
@media(max-width:680px){.section-heading{align-items:flex-start;flex-direction:column}.section-actions{width:100%}.section-actions>.action-button{flex:1}}
@media(max-width:680px){.site-row{grid-template-columns:minmax(0,1fr) auto}.site-row>.status-pill{justify-self:end}.row-actions{min-width:0}.row-actions .row-open{min-width:0}}

.discovery-status {
  display: grid;
  grid-template-columns: minmax(280px, 1.35fr) auto auto;
  align-items: center;
  gap: var(--ncp-space-5);
  overflow: hidden;
  padding: var(--ncp-space-4) var(--ncp-space-5);
  border-left: 3px solid var(--ncp-success);
  box-shadow: var(--ncp-shadow-panel);
}

.discovery-status--partial { border-left-color: var(--ncp-warning); }
.discovery-status--unavailable { border-left-color: var(--ncp-neutral); }

.discovery-status__summary {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--ncp-space-3);
}

.discovery-status__icon {
  display: grid;
  width: 42px;
  height: 42px;
  flex: 0 0 auto;
  place-items: center;
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-success-soft);
  color: var(--ncp-success-strong);
}

.discovery-status--partial .discovery-status__icon {
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
}

.discovery-status--unavailable .discovery-status__icon {
  background: var(--ncp-neutral-soft);
  color: var(--ncp-neutral-strong);
}

.discovery-status h2 {
  margin: 0;
  color: var(--ncp-text);
  font-size: .92rem;
  font-weight: 760;
  letter-spacing: -.015em;
}

.discovery-status p {
  margin: 3px 0 0;
  color: var(--ncp-text-muted);
  font-size: .76rem;
  line-height: 1.45;
}

.discovery-status__metrics {
  display: flex;
  margin: 0;
  overflow: hidden;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-surface-quiet);
}

.discovery-status__metrics > div {
  display: grid;
  min-width: 76px;
  gap: 1px;
  padding: 8px 12px;
  text-align: center;
}

.discovery-status__metrics > div + div { border-left: 1px solid var(--ncp-line); }
.discovery-status__metrics dt { color: var(--ncp-text-subtle); font-size: .67rem; }
.discovery-status__metrics dd { margin: 0; color: var(--ncp-text); font-family: var(--ncp-font-data); font-size: .86rem; font-weight: 760; }

.discovery-status__time {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  white-space: nowrap;
}

.discovery-issues {
  grid-column: 1 / -1;
  margin: 0 calc(var(--ncp-space-5) * -1) calc(var(--ncp-space-4) * -1);
  border-top: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.discovery-issues summary {
  display: flex;
  min-height: 42px;
  align-items: center;
  padding: 0 var(--ncp-space-5);
  color: var(--ncp-warning-strong);
  font-size: .76rem;
  font-weight: 720;
  list-style: none;
}

.discovery-issues summary::-webkit-details-marker { display: none; }
.discovery-issues summary::after { margin-left: auto; content: '+'; font-size: 1rem; }
.discovery-issues[open] summary::after { content: '−'; }
.discovery-issues__list { display: grid; border-top: 1px solid var(--ncp-line); }

.discovery-issues article {
  display: grid;
  grid-template-columns: minmax(220px, 1.2fr) minmax(100px, .5fr) minmax(260px, 1.5fr);
  align-items: center;
  gap: var(--ncp-space-4);
  min-height: 54px;
  padding: 8px var(--ncp-space-5);
  border-bottom: 1px solid var(--ncp-line);
}

.discovery-issues article:last-child { border-bottom: 0; }
.discovery-issues article > div { display: grid; min-width: 0; gap: 2px; }
.discovery-issues strong, .discovery-issues span, .discovery-issues p { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.discovery-issues strong { font-size: .78rem; }
.discovery-issues span, .discovery-issues code, .discovery-issues p { margin: 0; color: var(--ncp-text-subtle); font-size: .7rem; }
.discovery-issues code { font-family: var(--ncp-font-mono); }

.site-columns,
.site-row {
  grid-template-columns: minmax(280px, 1.8fr) 92px 170px 104px 100px 224px;
}

.site-columns {
  display: grid;
  align-items: center;
  gap: 12px;
  min-height: 42px;
  padding: 0 15px;
}

.site-columns span:not(:first-child) { text-align: center; }
.site-identity--link { border-radius: var(--ncp-radius-sm); }
.site-identity--link:hover strong { color: var(--ncp-primary-strong); }
.site-identity--link:focus-visible { outline-offset: 4px; }
.site-row { min-height: 82px; }
.site-row:hover { background: var(--ncp-table-row-hover); }
.site-row .site-source, .site-row .site-visited { text-align: center; }
.row-actions { gap: 6px; opacity: 1; }
.row-actions :deep(.list-icon-button) { width: 40px; min-width: 40px; height: 40px; }
.row-open { min-height: 40px; align-items: center; gap: 6px; padding: 0 12px; border: 1px solid var(--ncp-primary-border); }
.row-open:hover:not(.disabled) { border-color: var(--ncp-primary); background: var(--ncp-primary-hover); transform: translateY(-1px); }
.row-open.disabled { border-color: var(--ncp-line); }
.site-error :deep(.action-button) { flex: 0 0 auto; background: var(--ncp-surface); }

@media (max-width: 1280px) {
  .site-columns,
  .site-row { grid-template-columns: minmax(240px, 1.5fr) 88px 150px 92px 216px; }
  .site-source,
  .site-columns span:nth-child(4) { display: none; }
}

@media (max-width: 1000px) {
  .discovery-status { grid-template-columns: minmax(260px, 1fr) auto; }
  .discovery-status__time { grid-column: 1 / -1; }
  .site-columns,
  .site-row { grid-template-columns: minmax(220px, 1.4fr) 88px minmax(130px, 1fr) 216px; }
  .site-visited,
  .site-columns span:nth-child(5) { display: none; }
}

@media (max-width: 760px) {
  .discovery-status { grid-template-columns: 1fr; align-items: stretch; padding: var(--ncp-space-4); }
  .discovery-status__metrics { width: 100%; }
  .discovery-status__metrics > div { min-width: 0; flex: 1; }
  .discovery-status__time { grid-column: auto; }
  .discovery-issues { margin-inline: calc(var(--ncp-space-4) * -1); }
  .discovery-issues article { grid-template-columns: 1fr; gap: 4px; padding-block: 11px; }
  .site-row { grid-template-columns: minmax(0, 1fr) auto; }
  .site-source, .site-visited { display: none; }
}

@media (max-width: 520px) {
  .discovery-status__summary { align-items: flex-start; }
  .discovery-status__metrics dt { font-size: .62rem; }
  .discovery-status__metrics > div { padding-inline: 6px; }
  .section-actions { display: grid; grid-template-columns: 1fr 1fr; }
  .row-actions { display: grid; grid-template-columns: repeat(3, 40px) minmax(0, 1fr); width: 100%; }
}
</style>
