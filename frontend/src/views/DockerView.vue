<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Boxes, ChevronRight, ExternalLink, FileText, LoaderCircle, Search, SlidersHorizontal } from '@lucide/vue'
import { ElDrawer, ElInput, ElTooltip } from 'element-plus'

import { NcpApiError, requestContainerAction, requestContainerLogs, type ContainerAction, type ContainerLogsResult, type DockerProject } from '@/api/system'
import ProjectDetailDrawer from '@/components/ProjectDetailDrawer.vue'
import StatusPill from '@/components/StatusPill.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { projectStateTone } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

type StateFilter = 'all' | DockerProject['state']

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()
const hostName = window.location.hostname
const query = ref('')
const stateFilter = ref<StateFilter>('all')
const actionPending = ref<string | null>(null)
const actionError = ref<string | null>(null)
const logOpen = ref(false)
const logLoading = ref(false)
const logContainerName = ref('')
const logs = ref<ContainerLogsResult | null>(null)

const allProjects = computed(() => systemStore.services)
const projects = computed(() => {
  const term = query.value.trim().toLowerCase()
  return allProjects.value.filter((project) => {
    const matchesState = stateFilter.value === 'all' || project.state === stateFilter.value
    return matchesState && (!term || project.name.toLowerCase().includes(term) || project.workingDirectory.toLowerCase().includes(term))
  })
})
const inventory = computed(() => systemStore.inventory)
const selectedProject = computed(() => allProjects.value.find((project) => project.id === route.query.project) ?? null)
const selectedContainers = computed(() => containersFor(selectedProject.value?.id ?? ''))
const detailOpen = computed({
  get: () => Boolean(selectedProject.value),
  set: (open) => {
    if (!open) void updateSelectedProject(null)
  },
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '全部项目', value: allProjects.value.length },
  { label: '运行容器', value: inventory.value?.engine.containersRunning ?? '—', tone: 'success' },
  { label: '停止容器', value: inventory.value?.engine.containersStopped ?? '—', tone: 'warning' },
  { label: '本地镜像', value: inventory.value?.engine.images ?? '—' },
])

function containersFor(projectId: string) {
  return inventory.value?.containers.filter((container) => container.projectId === projectId) ?? []
}
function portsFor(projectId: string) {
  return [...new Set(containersFor(projectId).flatMap((container) => container.ports).filter((port) => port.publicPort > 0).map((port) => port.publicPort))]
}
function stateLabel(state: DockerProject['state']) {
  return state === 'running' ? '运行中' : state === 'degraded' ? '需关注' : '已停止'
}
async function updateSelectedProject(projectId: string | null) {
  const queryParameters = { ...route.query }
  if (projectId) queryParameters.project = projectId
  else delete queryParameters.project
  await router.replace({ query: queryParameters })
}
async function performAction(containerId: string, action: ContainerAction) {
  if (actionPending.value) return
  actionPending.value = `${containerId}:${action}`
  actionError.value = null
  try {
    await requestContainerAction(containerId, action)
    await systemStore.refresh({ includeCapabilities: false })
  } catch (error) {
    actionError.value = error instanceof NcpApiError ? error.message : '容器操作失败，请稍后重试。'
  } finally {
    actionPending.value = null
  }
}
async function openLogs(container: { id: string; name: string }) {
  logOpen.value = true
  logLoading.value = true
  logContainerName.value = container.name
  logs.value = null
  try {
    logs.value = await requestContainerLogs(container.id, 200)
  } catch (error) {
    actionError.value = error instanceof NcpApiError ? error.message : '容器日志读取失败。'
  } finally {
    logLoading.value = false
  }
}

watch([() => route.query.project, allProjects], ([projectId, items]) => {
  if (projectId && items.length && !items.some((project) => project.id === projectId)) void updateSelectedProject(null)
})
</script>

<template>
  <div class="page workspace-page docker-page">
    <WorkspaceHeader title="Docker 管理" description="统一查看项目、容器、端口和运行状态" :icon="Boxes" :stats="stats">
      <template #actions>
        <span class="engine-version">Engine {{ inventory?.engine.serverVersion || '—' }}</span>
      </template>
      <template #tools>
        <div class="state-filter" aria-label="Docker 项目状态筛选">
          <button v-for="item in [{ value: 'all', label: '全部' }, { value: 'running', label: '运行中' }, { value: 'stopped', label: '已停止' }]" :key="item.value" type="button" :class="{ active: stateFilter === item.value }" @click="stateFilter = item.value as StateFilter">
            {{ item.label }}
          </button>
        </div>
        <ElInput v-model="query" class="docker-search" clearable placeholder="搜索项目或工作目录" aria-label="搜索 Docker 项目">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="actionError" class="action-error" role="alert">
      <span>{{ actionError }}</span><button type="button" @click="actionError = null">关闭</button>
    </div>

    <div class="result-line">
      <span><SlidersHorizontal :size="15" />当前显示 <strong>{{ projects.length }}</strong> 个项目</span>
      <button v-if="query || stateFilter !== 'all'" type="button" @click="query = ''; stateFilter = 'all'">清除筛选</button>
    </div>

    <section class="docker-table panel" aria-label="Docker 项目列表">
      <div class="docker-table__head">
        <span>项目</span><span>状态</span><span>容器</span><span>公开端口</span><span>工作目录</span><span>操作</span>
      </div>
      <div v-for="project in projects" :key="project.id" class="project-row" role="button" tabindex="0" @click="updateSelectedProject(project.id)" @keydown.enter="updateSelectedProject(project.id)" @keydown.space.prevent="updateSelectedProject(project.id)">
        <div class="project-name">
          <span><Boxes :size="18" /></span>
          <div><strong>{{ project.name }}</strong><small>{{ project.kind === 'compose' ? 'Compose 项目' : '独立容器组' }}</small></div>
        </div>
        <StatusPill :label="stateLabel(project.state)" :tone="projectStateTone(project.state)" />
        <span class="mono">{{ project.runningCount }}/{{ project.containerCount }}</span>
        <div class="port-cell">
          <template v-if="portsFor(project.id).length">
            <ElTooltip v-for="port in portsFor(project.id).slice(0, 3)" :key="port" :content="`打开端口 ${port}`">
              <a :href="`http://${hostName}:${port}`" target="_blank" rel="noreferrer" @click.stop>{{ port }}<ExternalLink :size="12" /></a>
            </ElTooltip>
            <span v-if="portsFor(project.id).length > 3">+{{ portsFor(project.id).length - 3 }}</span>
          </template>
          <span v-else>无公开端口</span>
        </div>
        <ElTooltip :content="project.workingDirectory || 'Docker 自动发现项目'" placement="top">
          <span class="path-cell">{{ project.workingDirectory || '自动发现' }}</span>
        </ElTooltip>
        <span class="row-detail">详情<ChevronRight :size="17" /></span>
      </div>
      <div v-if="!projects.length" class="table-empty">没有匹配的 Docker 项目。</div>
    </section>

    <section class="docker-mobile-list" aria-label="Docker 项目列表">
      <article v-for="project in projects" :key="project.id" class="mobile-project panel interactive-surface">
        <header>
          <div class="project-name">
            <span><Boxes :size="18" /></span>
            <div><strong>{{ project.name }}</strong><small>{{ project.kind === 'compose' ? 'Compose 项目' : '独立容器组' }}</small></div>
          </div>
          <StatusPill :label="stateLabel(project.state)" :tone="projectStateTone(project.state)" />
        </header>
        <dl>
          <div><dt>容器</dt><dd>{{ project.runningCount }}/{{ project.containerCount }} 运行</dd></div>
          <div><dt>公开端口</dt><dd>{{ portsFor(project.id).join('、') || '无' }}</dd></div>
          <div><dt>工作目录</dt><dd :title="project.workingDirectory">{{ project.workingDirectory || '自动发现' }}</dd></div>
        </dl>
        <button type="button" @click="updateSelectedProject(project.id)">查看项目详情<ChevronRight :size="17" /></button>
      </article>
      <p v-if="!projects.length" class="table-empty panel">没有匹配的 Docker 项目。</p>
    </section>

    <ProjectDetailDrawer
      v-model="detailOpen"
      :project="selectedProject"
      :containers="selectedContainers"
      :host-name="hostName"
      :allow-operations="true"
      :action-pending="actionPending"
      @action="performAction"
      @logs="openLogs"
    />

    <ElDrawer v-model="logOpen" :title="`${logContainerName} · 容器日志`" size="min(760px, 100%)" append-to-body>
      <div v-if="logLoading" class="log-loading"><LoaderCircle class="spin" :size="18" />正在读取日志</div>
      <ol v-else-if="logs?.entries.length" class="log-list">
        <li v-for="(entry, index) in logs.entries" :key="index"><span :class="`log-stream--${entry.stream}`">{{ entry.stream }}</span><code>{{ entry.message }}</code></li>
      </ol>
      <div v-else class="log-empty"><FileText :size="24" /><span>当前没有可显示的日志。</span></div>
    </ElDrawer>
  </div>
</template>

<style scoped>
.engine-version { display: inline-flex; min-height: 36px; align-items: center; padding: 0 11px; border-radius: 9px; background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); font-family: 'JetBrains Mono Variable', monospace; font-size: .63rem; }
.docker-search { width: min(320px, 38vw); }
.state-filter { display: flex; flex: 0 0 auto; gap: 3px; padding: 3px; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.state-filter button { min-height: 36px; padding: 0 12px; border-radius: 7px; background: transparent; color: var(--ncp-text-muted); font-size: .67rem; font-weight: 700; }
.state-filter button.active { background: #fff; box-shadow: 0 2px 8px rgba(28,45,75,.08); color: var(--ncp-primary-strong); }
.action-error { display: flex; min-height: 44px; align-items: center; justify-content: space-between; padding: 9px 13px; border: 1px solid rgba(212,81,93,.2); border-radius: 10px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .7rem; }
.action-error button { min-height: 36px; padding: 0 10px; border-radius: 8px; background: rgba(255,255,255,.65); color: inherit; font-weight: 700; }
.result-line { display: flex; min-height: 28px; align-items: center; justify-content: space-between; color: var(--ncp-text-subtle); font-size: .67rem; }
.result-line span { display: flex; align-items: center; gap: 6px; }
.result-line strong { color: var(--ncp-text); font-family: 'JetBrains Mono Variable', monospace; }
.result-line button { background: transparent; color: var(--ncp-primary-strong); font-size: .66rem; font-weight: 700; }
.docker-table { overflow: hidden; }
.docker-table__head, .project-row { display: grid; grid-template-columns: minmax(210px,1.3fr) 108px 74px 180px minmax(170px,1fr) 78px; align-items: center; gap: 12px; }
.docker-table__head { min-height: 42px; padding: 0 16px; background: var(--ncp-surface-quiet); color: var(--ncp-text-subtle); font-size: .64rem; font-weight: 730; }
.project-row { width: 100%; min-height: 68px; padding: 0 16px; border-top: 1px solid var(--ncp-line); background: #fff; color: var(--ncp-text-muted); font-size: .69rem; text-align: left; transition: background-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
.project-row:hover { position: relative; z-index: 1; background: var(--ncp-surface-hover); box-shadow: inset 3px 0 0 var(--ncp-primary); }
.project-name { display: flex; min-width: 0; align-items: center; gap: 9px; }
.project-name>span { display: grid; width: 36px; height: 36px; flex: 0 0 auto; place-items: center; border-radius: 10px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.project-name>div { display: grid; min-width: 0; gap: 1px; }
.project-name strong { overflow: hidden; color: var(--ncp-text); font-size: .75rem; text-overflow: ellipsis; white-space: nowrap; }
.project-name small { color: var(--ncp-text-subtle); font-size: .59rem; }
.mono { font-family: 'JetBrains Mono Variable', monospace; }
.port-cell { display: flex; min-width: 0; align-items: center; gap: 5px; }
.port-cell a { display: flex; min-height: 32px; align-items: center; gap: 3px; padding: 0 7px; border-radius: 7px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', monospace; font-size: .61rem; }
.port-cell>span { color: var(--ncp-text-subtle); font-size: .62rem; }
.path-cell { overflow: hidden; padding-right: 8px; color: var(--ncp-text-subtle); text-overflow: ellipsis; white-space: nowrap; }
.row-detail { display: flex; min-height: 44px; align-items: center; justify-content: flex-end; gap: 2px; color: var(--ncp-primary-strong); font-size: .65rem; font-weight: 700; }
.table-empty { padding: 36px; color: var(--ncp-text-subtle); font-size: .72rem; text-align: center; }
.docker-mobile-list { display: none; }
.log-loading { display: flex; align-items: center; gap: 8px; color: var(--ncp-text-muted); font-size: .75rem; }
.log-list { display: grid; gap: 4px; padding: 0; margin: 0; list-style: none; }
.log-list li { display: grid; grid-template-columns: 48px minmax(0,1fr); gap: 9px; padding: 6px 0; border-bottom: 1px solid var(--ncp-line); font-size: .67rem; line-height: 1.5; }
.log-list code { overflow-wrap: anywhere; white-space: pre-wrap; font-family: 'JetBrains Mono Variable', monospace; }
.log-list span { font-family: 'JetBrains Mono Variable', monospace; font-size: .57rem; font-weight: 700; }
.log-stream--stdout { color: var(--ncp-primary-strong); }.log-stream--stderr { color: var(--ncp-danger-strong); }
.log-empty { display: grid; min-height: 180px; place-items: center; align-content: center; gap: 8px; color: var(--ncp-text-subtle); font-size: .72rem; }
.spin { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media(max-width: 1120px) {
  .docker-table__head, .project-row { grid-template-columns: minmax(190px,1.2fr) 100px 65px 150px minmax(140px,1fr) 68px; gap: 8px; }
}
@media(max-width: 900px) { .docker-search { min-width: 0; width: 100%; } }
@media(max-width: 767px) {
  .docker-table { display: none; }
  .docker-mobile-list { display: grid; gap: 10px; }
  .state-filter { order: 2; width: 100%; }
  .state-filter button { flex: 1; min-height: 40px; }
  .mobile-project { display: grid; gap: 13px; padding: 15px; }
  .mobile-project header { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
  .mobile-project dl { display: grid; gap: 8px; margin: 0; padding: 11px; border-radius: 10px; background: var(--ncp-surface-quiet); }
  .mobile-project dl>div { display: grid; grid-template-columns: 74px minmax(0,1fr); gap: 8px; }
  .mobile-project dt { color: var(--ncp-text-subtle); font-size: .65rem; }
  .mobile-project dd { overflow: hidden; margin: 0; color: var(--ncp-text-muted); font-size: .68rem; text-overflow: ellipsis; white-space: nowrap; }
  .mobile-project>button { display: flex; min-height: 44px; align-items: center; justify-content: center; gap: 4px; border-radius: 9px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-size: .69rem; font-weight: 730; }
}
</style>
