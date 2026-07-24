<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  ArrowUpRight,
  EyeOff,
  Globe2,
  Image,
  Pencil,
  RefreshCw,
  Search,
  Settings2,
  Sparkles,
} from '@lucide/vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElOption,
  ElSelect,
  ElSwitch,
  ElTooltip,
} from 'element-plus'

import type { Site, SiteProfileInput } from '@/api/control'
import StatusPill from '@/components/StatusPill.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSitesStore } from '@/stores/sites'
import { useSystemStore } from '@/stores/system'

type SiteFilter = 'running' | 'all' | 'hidden'

const sitesStore = useSitesStore()
const systemStore = useSystemStore()
const hostName = window.location.hostname
const query = ref('')
const siteFilter = ref<SiteFilter>('running')
const editOpen = ref(false)
const selectedSite = ref<Site | null>(null)
const saving = ref(false)
const failedIcons = ref(new Set<string>())
const draft = ref<SiteProfileInput>({
  name: '',
  description: '',
  iconUrl: '',
  category: '',
  primaryPort: 0,
  hidden: false,
})

const sites = computed(() => sitesStore.sites.map((site) => {
  const project = systemStore.services.find((item) => item.id === site.projectId)
  return project ? { ...site, state: project.state } : site
}))
const visibleSites = computed(() => {
  const term = query.value.trim().toLowerCase()
  return sites.value.filter((site) => {
    const matchesFilter = siteFilter.value === 'hidden'
      ? site.hidden
      : !site.hidden && (siteFilter.value === 'all' || site.state === 'running')
    const matchesQuery = !term || `${site.name} ${site.description} ${site.category} ${site.ports.join(' ')}`.toLowerCase().includes(term)
    return matchesFilter && matchesQuery
  })
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '已发现站点', value: sites.value.filter((site) => !site.hidden).length },
  { label: '正在运行', value: sites.value.filter((site) => !site.hidden && site.state === 'running').length, tone: 'success' },
  { label: '分类', value: new Set(sites.value.filter((site) => !site.hidden).map((site) => site.category)).size },
])
const categoryOptions = computed(() => [...new Set([
  '管理工具',
  '文件与 NAS',
  'AI 工具',
  '影音服务',
  '网络服务',
  ...sites.value.map((site) => site.category).filter(Boolean),
])])

onMounted(() => void sitesStore.refresh())

function siteURL(site: Site, port = site.primaryPort) {
  return `http://${hostName}:${port}`
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

function openEditor(site: Site) {
  selectedSite.value = site
  draft.value = {
    name: site.name,
    description: site.description,
    iconUrl: site.iconUrl,
    category: site.category,
    primaryPort: site.primaryPort,
    hidden: site.hidden,
  }
  editOpen.value = true
}

async function saveSite() {
  if (!selectedSite.value) return
  saving.value = true
  try {
    await sitesStore.save(selectedSite.value.projectId, draft.value)
    editOpen.value = false
    ElMessage.success('站点资料已保存')
  } catch {
    ElMessage.error('站点资料保存失败')
  } finally {
    saving.value = false
  }
}

function markIconFailed(site: Site) {
  failedIcons.value = new Set(failedIcons.value).add(site.id)
}
</script>

<template>
  <div class="page workspace-page sites-page">
    <WorkspaceHeader title="站点中心" description="统一访问和维护 NAS 上正在运行的 Web 站点" :icon="Globe2" :stats="stats">
      <template #actions>
        <ElButton :loading="sitesStore.loading" @click="sitesStore.refresh"><RefreshCw :size="16" />重新识别</ElButton>
      </template>
      <template #filters>
        <div class="state-filter" aria-label="站点状态筛选">
          <button v-for="item in [{ value: 'running', label: '运行中' }, { value: 'all', label: '全部站点' }, { value: 'hidden', label: '已隐藏' }]" :key="item.value" type="button" :class="{ active: siteFilter === item.value }" @click="siteFilter = item.value as SiteFilter">
            {{ item.label }}
          </button>
        </div>
      </template>
      <template #tools>
        <ElInput v-model="query" class="site-search" clearable placeholder="搜索站点、分类或端口" aria-label="搜索站点">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="sitesStore.error" class="site-error" role="alert">{{ sitesStore.error }}</div>

    <section v-if="visibleSites.length" class="site-grid" aria-label="NAS 站点列表">
      <article v-for="site in visibleSites" :key="site.id" class="site-card panel">
        <div class="site-card__accent" :style="siteHue(site)" aria-hidden="true"></div>
        <header>
          <div class="site-logo" :style="siteHue(site)">
            <img v-if="site.iconUrl && !failedIcons.has(site.id)" :src="site.iconUrl" alt="" @error="markIconFailed(site)" />
            <span v-else>{{ site.name.slice(0, 1).toUpperCase() }}</span>
          </div>
          <div class="site-identity">
            <strong>{{ site.name }}</strong>
            <span>{{ site.category || '未分类' }}</span>
          </div>
          <StatusPill :label="siteStateLabel(site.state)" :tone="siteTone(site.state)" />
        </header>

        <p class="site-description">{{ site.description || '尚未填写站点简介。' }}</p>

        <div class="site-meta">
          <span><Sparkles :size="14" />{{ site.source === 'edited' ? '资料已编辑' : 'Docker 自动发现' }}</span>
          <span>{{ site.ports.length }} 个 Web 入口</span>
        </div>

        <footer>
          <div class="site-ports">
            <ElTooltip v-for="port in site.ports.slice(0, 3)" :key="port" :content="port === site.primaryPort ? '主入口' : `打开端口 ${port}`" placement="top">
              <a :class="{ primary: port === site.primaryPort }" :href="siteURL(site, port)" target="_blank" rel="noreferrer">{{ port }}</a>
            </ElTooltip>
            <span v-if="site.ports.length > 3">+{{ site.ports.length - 3 }}</span>
          </div>
          <div class="site-actions">
            <ElTooltip content="编辑名称、简介、图标和主入口" placement="top">
              <button type="button" :aria-label="`编辑 ${site.name}`" @click="openEditor(site)"><Pencil :size="16" /></button>
            </ElTooltip>
            <a class="open-site" :class="{ disabled: site.state !== 'running' }" :href="site.state === 'running' ? siteURL(site) : undefined" target="_blank" rel="noreferrer">
              打开站点<ArrowUpRight :size="16" />
            </a>
          </div>
        </footer>
      </article>
    </section>

    <section v-else class="empty-sites panel">
      <Globe2 :size="28" />
      <div>
        <h2>{{ siteFilter === 'hidden' ? '没有隐藏的站点' : '没有匹配的运行站点' }}</h2>
        <p>站点中心只识别具有 Web 端口的 Docker 项目，可调整筛选或重新识别。</p>
      </div>
    </section>

    <ElDialog v-model="editOpen" class="site-editor" title="编辑站点资料" width="min(620px, calc(100vw - 28px))">
      <div v-if="selectedSite" class="editor-context">
        <div class="site-logo" :style="siteHue(selectedSite)">
          <img v-if="draft.iconUrl && !failedIcons.has(selectedSite.id)" :src="draft.iconUrl" alt="" />
          <span v-else>{{ draft.name.slice(0, 1).toUpperCase() }}</span>
        </div>
        <div><strong>{{ selectedSite.projectId }}</strong><span>Docker 项目关联保持不变</span></div>
      </div>
      <ElForm label-position="top">
        <div class="editor-grid">
          <ElFormItem label="站点名称"><ElInput v-model="draft.name" maxlength="120" /></ElFormItem>
          <ElFormItem label="站点分类">
            <ElSelect v-model="draft.category" filterable allow-create default-first-option>
              <ElOption v-for="category in categoryOptions" :key="category" :label="category" :value="category" />
            </ElSelect>
          </ElFormItem>
        </div>
        <ElFormItem label="简介"><ElInput v-model="draft.description" type="textarea" :rows="3" maxlength="500" show-word-limit /></ElFormItem>
        <ElFormItem>
          <template #label><span class="field-label"><Image :size="15" />图标地址</span></template>
          <ElInput v-model="draft.iconUrl" placeholder="可填写站点 Logo 或 favicon 地址；留空使用自动字标" />
        </ElFormItem>
        <div class="editor-grid">
          <ElFormItem label="主入口端口">
            <ElSelect v-model="draft.primaryPort">
              <ElOption v-for="port in selectedSite?.ports ?? []" :key="port" :label="String(port)" :value="port" />
            </ElSelect>
          </ElFormItem>
          <ElFormItem label="列表可见性">
            <div class="visibility-control"><ElSwitch v-model="draft.hidden" /><span><EyeOff :size="14" />隐藏此站点</span></div>
          </ElFormItem>
        </div>
      </ElForm>
      <template #footer>
        <ElButton @click="editOpen = false">取消</ElButton>
        <ElButton type="primary" :loading="saving" @click="saveSite"><Settings2 :size="16" />保存站点资料</ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<style scoped>
.site-search { width:min(360px,36vw); }
.state-filter { display:flex; flex:0 0 auto; gap:3px; padding:3px; border:1px solid var(--ncp-line); border-radius:10px; background:var(--ncp-surface-quiet); }
.state-filter button { min-height:36px; padding:0 14px; border-radius:7px; background:transparent; color:var(--ncp-text-muted); font-size:.8rem; font-weight:700; transition:color var(--ncp-duration-fast),background var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast); }
.state-filter button.active { background:#fff; box-shadow:0 2px 8px rgba(28,45,75,.08); color:var(--ncp-primary-strong); }
.site-error { padding:10px 13px; border:1px solid rgba(212,81,93,.2); border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.8rem; }
.site-grid { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:14px; }
.site-card { position:relative; display:grid; min-width:0; min-height:252px; grid-template-rows:auto 1fr auto auto; gap:14px; padding:18px; overflow:hidden; transition:border-color var(--ncp-duration-fast),box-shadow var(--ncp-duration-base),transform var(--ncp-duration-fast); }
.site-card:hover { border-color:rgba(36,104,216,.25); box-shadow:var(--ncp-shadow-hover); transform:translateY(-2px); }
.site-card__accent { position:absolute; top:0; left:18px; width:58px; height:3px; border-radius:0 0 8px 8px; background:hsl(var(--site-hue) 72% 53%); }
.site-card header { display:grid; grid-template-columns:auto minmax(0,1fr) auto; align-items:center; gap:11px; }
.site-logo { display:grid; width:44px; height:44px; flex:0 0 auto; place-items:center; overflow:hidden; border:1px solid hsl(var(--site-hue) 65% 85%); border-radius:13px; background:hsl(var(--site-hue) 78% 95%); color:hsl(var(--site-hue) 67% 40%); font-size:1rem; font-weight:800; }
.site-logo img { width:100%; height:100%; object-fit:cover; }
.site-identity { display:grid; min-width:0; gap:2px; }
.site-identity strong { overflow:hidden; font-size:.93rem; text-overflow:ellipsis; white-space:nowrap; }
.site-identity span { color:var(--ncp-text-subtle); font-size:.73rem; }
.site-description { display:-webkit-box; min-height:48px; margin:0; overflow:hidden; color:var(--ncp-text-muted); font-size:.8rem; line-height:1.65; -webkit-box-orient:vertical; -webkit-line-clamp:2; }
.site-meta { display:flex; align-items:center; justify-content:space-between; gap:10px; padding:9px 10px; border-radius:9px; background:var(--ncp-surface-quiet); color:var(--ncp-text-subtle); font-size:.7rem; }
.site-meta span { display:flex; align-items:center; gap:5px; }
.site-card footer,.site-ports,.site-actions { display:flex; align-items:center; }
.site-card footer { justify-content:space-between; gap:12px; }
.site-ports { min-width:0; gap:5px; }
.site-ports a { display:grid; min-width:46px; min-height:34px; place-items:center; border:1px solid var(--ncp-line); border-radius:8px; background:#fff; color:var(--ncp-text-muted); font-family:'JetBrains Mono Variable',monospace; font-size:.7rem; font-weight:700; }
.site-ports a.primary { border-color:rgba(36,104,216,.2); background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.site-ports>span { color:var(--ncp-text-subtle); font-size:.68rem; }
.site-actions { gap:6px; }
.site-actions button { display:grid; width:40px; height:40px; place-items:center; border-radius:9px; background:transparent; color:var(--ncp-text-muted); transition:background var(--ncp-duration-fast),color var(--ncp-duration-fast); }
.site-actions button:hover { background:var(--ncp-surface-quiet); color:var(--ncp-primary-strong); }
.open-site { display:flex; min-height:40px; align-items:center; gap:5px; padding:0 12px; border-radius:9px; background:var(--ncp-primary); color:#fff; font-size:.73rem; font-weight:720; }
.open-site.disabled { pointer-events:none; background:var(--ncp-surface-quiet); color:var(--ncp-text-subtle); }
.empty-sites { display:flex; min-height:220px; align-items:center; justify-content:center; gap:14px; color:var(--ncp-text-subtle); }
.empty-sites h2 { margin:0; color:var(--ncp-text); font-size:.95rem; }.empty-sites p { margin:4px 0 0; font-size:.78rem; }
.editor-context { display:flex; align-items:center; gap:11px; margin:-5px 0 18px; padding:12px; border-radius:11px; background:var(--ncp-surface-quiet); }
.editor-context>div:last-child { display:grid; min-width:0; gap:2px; }.editor-context strong { overflow:hidden; font-size:.78rem; text-overflow:ellipsis; white-space:nowrap; }.editor-context span { color:var(--ncp-text-subtle); font-size:.7rem; }
.editor-grid { display:grid; grid-template-columns:1fr 1fr; gap:14px; }
.editor-grid :deep(.el-select) { width:100%; }
.field-label,.visibility-control,.visibility-control span { display:flex; align-items:center; gap:6px; }
.visibility-control { min-height:40px; }.visibility-control span { color:var(--ncp-text-muted); font-size:.78rem; }
@media(max-width:1240px){.site-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:900px){.site-search{width:100%}}
@media(max-width:680px){.site-grid{grid-template-columns:1fr}.state-filter{width:100%}.state-filter button{flex:1;padding-inline:8px}.site-card{min-height:238px}.editor-grid{grid-template-columns:1fr}.empty-sites{padding:28px 18px;text-align:center;flex-direction:column}}
</style>
