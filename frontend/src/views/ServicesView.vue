<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowUpRight, Boxes, ChevronRight, Search, SlidersHorizontal } from '@lucide/vue'
import { ElInput, ElTooltip } from 'element-plus'

import ProjectDetailDrawer from '@/components/ProjectDetailDrawer.vue'
import StatusPill from '@/components/StatusPill.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { projectStateTone } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'
import type { DockerProject } from '@/api/system'

type StateFilter = 'all' | DockerProject['state']

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()
const hostName = window.location.hostname
const query = ref('')
const stateFilter = ref<StateFilter>('all')

const enrichedProjects = computed(() => systemStore.services.map((project) => ({
  ...project,
  ports: [...new Set((systemStore.inventory?.containers ?? [])
    .filter((container) => container.projectId === project.id)
    .flatMap((container) => container.ports)
    .filter((port) => port.publicPort > 0)
    .map((port) => port.publicPort))],
})))
const projects = computed(() => {
  const term = query.value.trim().toLowerCase()
  return enrichedProjects.value.filter((project) => {
    const matchesState = stateFilter.value === 'all' || project.state === stateFilter.value
    const matchesQuery = !term || project.name.toLowerCase().includes(term) || project.ports.some((port) => String(port).includes(term))
    return matchesState && matchesQuery
  })
})
const selectedProject = computed(() => enrichedProjects.value.find((project) => project.id === route.query.project) ?? null)
const selectedContainers = computed(() => systemStore.inventory?.containers.filter((container) => container.projectId === selectedProject.value?.id) ?? [])
const detailOpen = computed({
  get: () => Boolean(selectedProject.value),
  set: (open) => {
    if (!open) void updateSelectedProject(null)
  },
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '全部项目', value: enrichedProjects.value.length },
  { label: '运行中', value: enrichedProjects.value.filter((item) => item.state === 'running').length, tone: 'success' },
  { label: '公开入口', value: enrichedProjects.value.reduce((total, item) => total + item.ports.length, 0) },
])

function stateLabel(state: DockerProject['state']) {
  return state === 'running' ? '运行中' : state === 'degraded' ? '需关注' : '已停止'
}

async function updateSelectedProject(projectId: string | null) {
  const queryParameters = { ...route.query }
  if (projectId) queryParameters.project = projectId
  else delete queryParameters.project
  await router.replace({ query: queryParameters })
}

watch([() => route.query.project, enrichedProjects], ([projectId, items]) => {
  if (projectId && items.length && !items.some((project) => project.id === projectId)) void updateSelectedProject(null)
})
</script>

<template>
  <div class="page workspace-page services-page">
    <WorkspaceHeader title="服务入口" description="快速访问 NAS 上对局域网开放的项目" :icon="Boxes" :stats="stats">
      <template #tools>
        <div class="state-filter" aria-label="项目状态筛选">
          <button v-for="item in [{ value: 'all', label: '全部' }, { value: 'running', label: '运行中' }, { value: 'stopped', label: '已停止' }]" :key="item.value" type="button" :class="{ active: stateFilter === item.value }" @click="stateFilter = item.value as StateFilter">
            {{ item.label }}
          </button>
        </div>
        <ElInput v-model="query" class="service-search" clearable placeholder="搜索项目或端口" aria-label="搜索项目或端口">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div class="result-line">
      <span><SlidersHorizontal :size="15" />当前显示 <strong>{{ projects.length }}</strong> 个项目</span>
      <button v-if="query || stateFilter !== 'all'" type="button" @click="query = ''; stateFilter = 'all'">清除筛选</button>
    </div>

    <section v-if="projects.length" class="service-grid" aria-label="服务入口列表">
      <article v-for="project in projects" :key="project.id" class="service-card panel interactive-surface">
        <header>
          <span class="service-card__icon"><Boxes :size="20" /></span>
          <button class="service-card__title" type="button" @click="updateSelectedProject(project.id)">
            <strong>{{ project.name }}</strong>
            <small>{{ project.runningCount }}/{{ project.containerCount }} 个容器运行</small>
          </button>
          <StatusPill :label="stateLabel(project.state)" :tone="projectStateTone(project.state)" />
        </header>

        <div class="service-card__meta">
          <span>工作目录</span>
          <strong :title="project.workingDirectory">{{ project.workingDirectory || 'Docker 自动发现项目' }}</strong>
        </div>

        <footer>
          <div class="service-card__ports">
            <template v-if="project.ports.length">
              <ElTooltip v-for="port in project.ports.slice(0, 4)" :key="port" :content="`打开端口 ${port}`" placement="top">
                <a :href="`http://${hostName}:${port}`" target="_blank" rel="noreferrer">
                  {{ port }}<ArrowUpRight :size="14" />
                </a>
              </ElTooltip>
              <span v-if="project.ports.length > 4">+{{ project.ports.length - 4 }}</span>
            </template>
            <span v-else class="no-port">无公开端口</span>
          </div>
          <ElTooltip content="查看项目详情" placement="top">
            <button class="detail-button" type="button" :aria-label="`查看 ${project.name} 详情`" @click="updateSelectedProject(project.id)">
              <span>详情</span><ChevronRight :size="17" />
            </button>
          </ElTooltip>
        </footer>
      </article>
    </section>

    <section v-else class="empty-panel panel">
      <span><Search :size="24" /></span>
      <div><h2>没有匹配的服务</h2><p>调整状态或关键词，实时清单更新后结果会自动出现。</p></div>
      <button type="button" @click="query = ''; stateFilter = 'all'">清除筛选</button>
    </section>

    <ProjectDetailDrawer
      v-model="detailOpen"
      :project="selectedProject"
      :containers="selectedContainers"
      :host-name="hostName"
    />
  </div>
</template>

<style scoped>
.service-search { width: min(310px, 38vw); }
.state-filter { display: flex; flex: 0 0 auto; gap: 3px; padding: 3px; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.state-filter button { min-height: 36px; padding: 0 12px; border-radius: 7px; background: transparent; color: var(--ncp-text-muted); font-size: .8rem; font-weight: 700; transition: color var(--ncp-duration-fast), background-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
.state-filter button.active { background: #fff; box-shadow: 0 2px 8px rgba(28,45,75,.08); color: var(--ncp-primary-strong); }
.result-line { display: flex; min-height: 28px; align-items: center; justify-content: space-between; color: var(--ncp-text-subtle); font-size: .78rem; }
.result-line span { display: flex; align-items: center; gap: 6px; }
.result-line strong { color: var(--ncp-text); font-family: 'JetBrains Mono Variable', monospace; }
.result-line button { background: transparent; color: var(--ncp-primary-strong); font-size: .66rem; font-weight: 700; }
.service-grid { display: grid; grid-template-columns: repeat(3, minmax(0,1fr)); gap: 12px; }
.service-card { display: grid; min-width: 0; min-height: 188px; grid-template-rows: auto 1fr auto; gap: 15px; padding: 16px; }
.service-card header { display: grid; grid-template-columns: auto minmax(0,1fr) auto; align-items: center; gap: 10px; }
.service-card__icon { display: grid; width: 40px; height: 40px; place-items: center; border-radius: 11px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.service-card__title { display: grid; min-width: 0; gap: 2px; background: transparent; text-align: left; }
.service-card__title strong { overflow: hidden; color: var(--ncp-text); font-size: .9rem; text-overflow: ellipsis; white-space: nowrap; }
.service-card__title small { color: var(--ncp-text-subtle); font-size: .76rem; }
.service-card__meta { display: grid; min-width: 0; align-content: start; gap: 4px; padding: 10px 11px; border-radius: 9px; background: var(--ncp-surface-quiet); }
.service-card__meta span { color: var(--ncp-text-subtle); font-size: .75rem; font-weight: 650; }
.service-card__meta strong { overflow: hidden; color: var(--ncp-text-muted); font-size: .78rem; font-weight: 550; text-overflow: ellipsis; white-space: nowrap; }
.service-card footer, .service-card__ports { display: flex; align-items: center; }
.service-card footer { min-width: 0; justify-content: space-between; gap: 10px; }
.service-card__ports { min-width: 0; flex-wrap: wrap; gap: 6px; }
.service-card__ports a { display: flex; min-height: 36px; align-items: center; gap: 4px; padding: 0 9px; border: 1px solid rgba(36,104,216,.15); border-radius: 8px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', monospace; font-size: .73rem; font-weight: 750; transition: background-color var(--ncp-duration-fast); }
.service-card__ports a:hover { background: var(--ncp-primary-hover); }
.service-card__ports>span { color: var(--ncp-text-subtle); font-size: .62rem; }
.no-port { display: flex; min-height: 36px; align-items: center; }
.detail-button { display: flex; min-width: 68px; min-height: 44px; align-items: center; justify-content: center; gap: 3px; border-radius: 9px; background: transparent; color: var(--ncp-text-muted); font-size: .65rem; font-weight: 700; transition: color var(--ncp-duration-fast), background-color var(--ncp-duration-fast); }
.detail-button:hover { background: var(--ncp-surface-quiet); color: var(--ncp-primary-strong); }
.empty-panel { display: flex; min-height: 180px; align-items: center; justify-content: center; gap: 14px; padding: 28px; text-align: left; }
.empty-panel>span { display: grid; width: 48px; height: 48px; place-items: center; border-radius: 13px; background: var(--ncp-surface-quiet); color: var(--ncp-text-subtle); }
.empty-panel h2 { margin: 0; font-size: .9rem; }
.empty-panel p { margin: 4px 0 0; color: var(--ncp-text-subtle); font-size: .7rem; }
.empty-panel button { min-height: 40px; margin-left: 14px; padding: 0 13px; border-radius: 9px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-size: .68rem; font-weight: 700; }
@media(max-width: 1240px) { .service-grid { grid-template-columns: repeat(2,minmax(0,1fr)); } }
@media(max-width: 900px) { .service-search { min-width: 0; width: 100%; } }
@media(max-width: 680px) {
  .service-grid { grid-template-columns: 1fr; }
  .state-filter { order: 2; width: 100%; }
  .state-filter button { flex: 1; min-height: 40px; }
  .service-card { min-height: 176px; }
  .empty-panel { align-items: center; flex-direction: column; text-align: center; }
  .empty-panel button { min-height: 44px; margin-left: 0; }
}
</style>
