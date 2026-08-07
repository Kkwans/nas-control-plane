<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Boxes, Box, ExternalLink, Images, LoaderCircle, Play, RotateCcw, Search, Square, Trash2 } from '@lucide/vue'
import { ElInput, ElMessage, ElMessageBox, ElTooltip } from 'element-plus'

import { NcpApiError, deleteDockerProject, requestContainerAction, requestContainerLogs, requestDockerProjectAction, type ContainerAction, type ContainerLogsResult, type ComposeLifecycleResult, type DockerInventory, type DockerProject, type DockerProjectActionResult } from '@/api/system'
import ActionButton from '@/components/ActionButton.vue'
import ContainerLogDrawer from '@/components/ContainerLogDrawer.vue'
import DockerContainerPanel from '@/components/DockerContainerPanel.vue'
import DockerImagePanel from '@/components/DockerImagePanel.vue'
import ComposeEditorDrawer from '@/components/ComposeEditorDrawer.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import ProjectDetailDrawer from '@/components/ProjectDetailDrawer.vue'
import StatusPill from '@/components/StatusPill.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useListPreference } from '@/composables/useListPreference'
import { projectStateTone as projectStateToneBase } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

type StateFilter = 'all' | DockerProject['state']
type ContainerStateFilter = 'all' | 'running' | 'stopped'
type DockerViewMode = 'projects' | 'containers' | 'images'
type DockerContainer = DockerInventory['containers'][number]
type ProjectActionError = { containerId: string; name: string; message: string }

const route = useRoute()
const router = useRouter()
const systemStore = useSystemStore()
const hostName = window.location.hostname
const query = ref('')
const stateFilter = ref<StateFilter>('all')
const containerStateFilter = ref<ContainerStateFilter>('all')
const activeView = ref<DockerViewMode>('projects')
const actionPending = ref<string | null>(null)
const projectActionPending = ref<{ projectId: string; action: ContainerAction } | null>(null)
const projectDeletePending = ref<string | null>(null)
const projectActionErrors = ref<Record<string, ProjectActionError[]>>({})
const actionError = ref<string | null>(null)
const logOpen = ref(false)
const logLoading = ref(false)
const logContainerName = ref('')
const logs = ref<ContainerLogsResult | null>(null)
const composeEditorOpen = ref(false)
const projectPage = ref(1)
const { pageSize: projectPageSize } = useListPreference('docker.projects')
const { pageSize: containerPageSize } = useListPreference('docker.containers')
const { pageSize: imagePageSize } = useListPreference('docker.images.local')

const allProjects = computed(() => systemStore.services)
const projects = computed(() => {
  const term = query.value.trim().toLowerCase()
  return allProjects.value.filter((project) => {
    const matchesState = stateFilter.value === 'all' || project.state === stateFilter.value
    return matchesState && (!term || project.name.toLowerCase().includes(term) || project.workingDirectory.toLowerCase().includes(term))
  })
})
const projectPageCount = computed(() => Math.max(1, Math.ceil(projects.value.length / projectPageSize.value)))
const pagedProjects = computed(() => {
  const start = (projectPage.value - 1) * projectPageSize.value
  return projects.value.slice(start, start + projectPageSize.value)
})
const inventory = computed(() => systemStore.inventory)
const inventoryLoading = computed(() => !inventory.value && systemStore.connectionState !== 'unavailable')
const inventoryUnavailable = computed(() => !inventory.value && systemStore.connectionState === 'unavailable')
const effectiveActionPending = computed(() => {
  if (actionPending.value) return actionPending.value
  if (projectActionPending.value) return `project:${projectActionPending.value.projectId}:${projectActionPending.value.action}`
  if (projectDeletePending.value) return `project:${projectDeletePending.value}:delete`
  return null
})
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
const searchPlaceholder = computed(() => {
  if (activeView.value === 'containers') return '搜索容器、镜像或所属项目'
  if (activeView.value === 'images') return '搜索镜像名称、标签或 ID'
  return '搜索项目或工作目录'
})

function containersFor(projectId: string) {
  return inventory.value?.containers.filter((container) => container.projectId === projectId) ?? []
}
function portsFor(projectId: string) {
  return [...new Set(containersFor(projectId).flatMap((container) => container.ports).filter((port) => port.publicPort > 0).map((port) => port.publicPort))]
}
function stateLabel(state: DockerProject['state']) {
  return state === 'running' ? '运行中' : state === 'degraded' ? '需关注' : '已停止'
}
function actionLabel(action: ContainerAction) {
  return action === 'start' ? '启动' : action === 'stop' ? '停止' : '重启'
}
function projectActionFor(projectId: string) {
  return projectActionPending.value?.projectId === projectId ? projectActionPending.value.action : null
}
function projectStateLabel(project: DockerProject) {
  const action = projectActionFor(project.id)
  return action ? `正在${actionLabel(action)}` : stateLabel(project.state)
}
function projectStateTone(project: DockerProject) {
  return projectActionFor(project.id) ? 'pending' : projectStateToneBase(project.state)
}
function projectErrorsFor(projectId: string) {
  return projectActionErrors.value[projectId] ?? []
}
function projectActionDisabled(project: DockerProject, action: ContainerAction) {
  if (Boolean(actionPending.value) || Boolean(projectActionPending.value) || Boolean(projectDeletePending.value) || project.containerCount === 0) return true
  if (action === 'start') return project.state === 'running'
  if (action === 'stop') return project.state === 'stopped'
  return project.runningCount === 0
}
function projectDeleteDisabledReason(project: DockerProject) {
  if (project.kind !== 'compose') return '独立容器是虚拟分组，不能作为项目删除；请在容器列表中单独管理。'
  if (project.name.toLowerCase() === 'nas-control-plane') return 'NCP 自身项目受保护，不能从当前控制台删除。'
  if (project.state !== 'stopped' || project.runningCount > 0) return '项目仍在运行，请先停止全部容器。'
  if (actionPending.value || projectActionPending.value || projectDeletePending.value) return '其他 Docker 操作正在执行，请稍后重试。'
  return ''
}
function errorMessage(error: unknown, fallback: string) {
  return error instanceof NcpApiError ? error.message : fallback
}
function projectActionFailures(
  result: DockerProjectActionResult | ComposeLifecycleResult,
  targets: DockerContainer[],
  action: ContainerAction,
) {
  if ('containers' in result) {
    const failures = result.containers
      .filter((item) => !item.success)
      .map((item) => ({
        containerId: item.containerId,
        name: item.name || targets.find((container) => container.id === item.containerId)?.name || item.containerId.slice(0, 12),
        message: item.errorCode ? `操作失败（${item.errorCode}）` : '容器操作失败，请稍后重试。',
      }))
    if (result.completed || failures.length) return failures
    return targets.map((container) => ({
      containerId: container.id,
      name: container.name || container.id.slice(0, 12),
      message: '项目操作未完成，请刷新状态后重试。',
    }))
  }
  const expectedRunning = action !== 'stop'
  const failures = result.services
    .filter((service) => service.running !== expectedRunning)
    .map((service) => {
      const container = targets.find((candidate) => candidate.id === service.containerId || candidate.serviceName === service.name || candidate.name === service.name)
      return {
        containerId: service.containerId || container?.id || service.name,
        name: service.name || container?.name || service.containerId || '未命名服务',
        message: `状态复核未达到“${action === 'stop' ? '已停止' : '运行中'}”（${service.state || '未知'}）。`,
      }
    })
  if (!result.completed && !failures.length) {
    return targets.map((container) => ({
      containerId: container.id,
      name: container.name || container.id.slice(0, 12),
      message: '项目操作未完成，请刷新状态后重试。',
    }))
  }
  return failures
}
async function updateSelectedProject(projectId: string | null) {
  const queryParameters = { ...route.query }
  if (projectId) queryParameters.project = projectId
  else delete queryParameters.project
  await router.replace({ query: queryParameters })
}
async function performAction(containerId: string, action: ContainerAction) {
  if (actionPending.value || projectActionPending.value || projectDeletePending.value) return
  actionPending.value = `${containerId}:${action}`
  actionError.value = null
  try {
    await requestContainerAction(containerId, action)
    await systemStore.refresh({ inventory: true })
  } catch (error) {
    actionError.value = errorMessage(error, '容器操作失败，请稍后重试。')
  } finally {
    actionPending.value = null
  }
}
async function performProjectAction(project: DockerProject, action: ContainerAction) {
  if (actionPending.value || projectActionPending.value || projectDeletePending.value) return
  const containers = containersFor(project.id)
  const targets = action === 'start'
    ? containers.filter((container) => container.state !== 'running')
    : action === 'stop'
      ? containers.filter((container) => container.state === 'running')
      : containers.filter((container) => container.state === 'running')
  const actionTargets = project.kind === 'compose' ? containers : targets
  if (!actionTargets.length) {
    ElMessage.info(`项目“${project.name}”已经${action === 'start' ? '全部运行' : action === 'stop' ? '全部停止' : '没有可重启的容器'}`)
    return
  }
  try {
    await ElMessageBox.confirm(
      `将对项目“${project.name}”的 ${actionTargets.length} 个容器执行${actionLabel(action)}。操作会按容器逐项执行。`,
      `确认${actionLabel(action)}项目`,
      { confirmButtonText: `确认${actionLabel(action)}`, cancelButtonText: '取消', type: action === 'stop' ? 'warning' : 'info' },
    )
  } catch { return }

  projectActionPending.value = { projectId: project.id, action }
  projectActionErrors.value = { ...projectActionErrors.value, [project.id]: [] }
  actionError.value = null
  const failures: ProjectActionError[] = []
  try {
    let result: DockerProjectActionResult | ComposeLifecycleResult | null = null
    try {
      result = await requestDockerProjectAction({
        id: project.id,
        kind: project.kind,
        workingDirectory: project.workingDirectory,
        configFiles: project.configFiles,
        containerIds: actionTargets.map((container) => container.id),
      }, action)
    } catch (error) {
      failures.push(...actionTargets.map((container) => ({
        containerId: container.id,
        name: container.name || container.id.slice(0, 12),
        message: errorMessage(error, `项目${actionLabel(action)}失败，请稍后重试。`),
      })))
    }
    if (!failures.length && result) failures.push(...projectActionFailures(result, actionTargets, action))
    if (failures.length) {
      projectActionErrors.value = { ...projectActionErrors.value, [project.id]: failures }
      ElMessage.warning(`项目${actionLabel(action)}完成，但有 ${failures.length} 个容器失败。`)
    } else {
      projectActionErrors.value = { ...projectActionErrors.value, [project.id]: [] }
      ElMessage.success(`项目“${project.name}”已${actionLabel(action)}`)
    }
    try {
      await systemStore.refresh({ inventory: true })
    } catch (error) {
      actionError.value = errorMessage(error, '项目状态刷新失败，请手动重新加载。')
    }
  } catch (error) {
    actionError.value = errorMessage(error, `项目${actionLabel(action)}失败，请稍后重试。`)
  } finally {
    projectActionPending.value = null
  }
}
async function confirmDeleteProject(project: DockerProject) {
  const disabledReason = projectDeleteDisabledReason(project)
  if (disabledReason) {
    ElMessage.info(disabledReason)
    return
  }
  try {
    await ElMessageBox.confirm(
      `将删除项目“${project.name}”的已停止容器及 UGREEN 项目注册记录。镜像、卷、Compose 文件和工作目录不会删除。`,
      '确认删除 Docker 项目',
      { confirmButtonText: '删除项目', cancelButtonText: '取消', type: 'warning' },
    )
  } catch { return }
  projectDeletePending.value = project.id
  actionError.value = null
  try {
    await deleteDockerProject(project)
    if (selectedProject.value?.id === project.id) await updateSelectedProject(null)
    await systemStore.refresh({ inventory: true })
    ElMessage.success(`项目“${project.name}”已删除；镜像、卷和配置文件均已保留。`)
  } catch (error) {
    const message = errorMessage(error, '项目删除失败，未确认的资源不会继续删除。')
    actionError.value = message
    ElMessage.error(message)
    try { await systemStore.refresh({ inventory: true }) } catch { /* 页面错误区保留原始失败信息 */ }
  } finally {
    projectDeletePending.value = null
  }
}
function performSelectedProjectAction(action: ContainerAction) {
  if (selectedProject.value) void performProjectAction(selectedProject.value, action)
}
async function openLogs(container: { id: string; name: string }) {
  logOpen.value = true
  logLoading.value = true
  logContainerName.value = container.name
  logs.value = null
  try {
    logs.value = await requestContainerLogs(container.id, 200)
  } catch (error) {
    actionError.value = errorMessage(error, '容器日志读取失败。')
  } finally {
    logLoading.value = false
  }
}

watch([() => route.query.project, allProjects], ([projectId, items]) => {
  if (projectId && items.length && !items.some((project) => project.id === projectId)) void updateSelectedProject(null)
})
watch([query, stateFilter, projectPageSize], () => { projectPage.value = 1 })
watch(projectPageCount, (count) => {
  if (projectPage.value > count) projectPage.value = count
})
onMounted(() => void systemStore.refresh({ inventory: true }))
</script>

<template>
  <div class="page workspace-page docker-page">
    <WorkspaceHeader title="Docker 管理" description="统一查看项目、容器、镜像和运行状态" :icon="Boxes" :stats="stats">
      <template #filters>
        <div class="docker-view-tabs" aria-label="Docker 管理视图">
          <button type="button" :class="{ active: activeView === 'projects' }" @click="activeView = 'projects'"><Boxes :size="16" />项目</button>
          <button type="button" :class="{ active: activeView === 'containers' }" @click="activeView = 'containers'"><Box :size="16" />容器</button>
          <button type="button" :class="{ active: activeView === 'images' }" @click="activeView = 'images'"><Images :size="16" />镜像</button>
        </div>
        <div v-if="activeView === 'projects'" class="state-filter" aria-label="Docker 项目状态筛选">
          <button v-for="item in [{ value: 'all', label: '全部' }, { value: 'running', label: '运行中' }, { value: 'stopped', label: '已停止' }]" :key="item.value" type="button" :class="{ active: stateFilter === item.value }" @click="stateFilter = item.value as StateFilter">
            {{ item.label }}
          </button>
        </div>
        <div v-else-if="activeView === 'containers'" class="state-filter" aria-label="Docker 容器状态筛选">
          <button v-for="item in [{ value: 'all', label: '全部' }, { value: 'running', label: '运行中' }, { value: 'stopped', label: '已停止' }]" :key="item.value" type="button" :class="{ active: containerStateFilter === item.value }" @click="containerStateFilter = item.value as ContainerStateFilter">
            {{ item.label }}
          </button>
        </div>
      </template>
      <template v-if="activeView !== 'images'" #tools>
        <ElInput v-model="query" class="docker-search" clearable :placeholder="searchPlaceholder" aria-label="搜索 Docker 资源">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="actionError" class="action-error" role="alert">
      <span>{{ actionError }}</span><button type="button" @click="actionError = null">关闭</button>
    </div>

    <section v-if="inventoryLoading" class="resource-state panel" aria-live="polite" aria-busy="true">
      <LoaderCircle class="spin" :size="22" />
      <strong>正在读取 Docker 状态</strong>
      <span>项目和容器列表加载完成后会显示在这里。</span>
    </section>

    <section v-else-if="inventoryUnavailable" class="resource-state resource-state--error panel" role="alert">
      <strong>Docker 状态读取失败</strong>
      <span>无法读取项目和容器列表{{ systemStore.errorCode ? `（${systemStore.errorCode}）` : '' }}。</span>
      <ActionButton variant="secondary" size="sm" :icon="RotateCcw" @click="systemStore.refresh({ inventory: true })">重新加载</ActionButton>
    </section>

    <template v-else-if="activeView === 'images'">
      <DockerImagePanel
        v-model:query="query"
        :containers="inventory?.containers ?? []"
        :page-size="imagePageSize"
      />
    </template>

    <template v-else>
      <section v-if="activeView === 'projects'" class="docker-table panel" aria-label="Docker 项目列表">
        <div class="docker-table__head">
          <span>项目</span><span>状态</span><span>容器</span><span>公开端口</span><span>工作目录</span><span>操作</span>
        </div>
        <div v-for="project in pagedProjects" :key="project.id" class="project-row" @click="updateSelectedProject(project.id)">
          <div class="project-name">
            <span><Boxes :size="18" /></span>
            <div><strong>{{ project.name }}</strong><small>{{ project.kind === 'compose' ? `Compose · ${project.configFiles.length || 1} 个配置文件` : '独立容器组' }}</small></div>
          </div>
          <StatusPill :label="projectStateLabel(project)" :tone="projectStateTone(project)" />
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
          <div class="project-actions" @click.stop>
            <ElTooltip content="启动项目" placement="top">
              <ActionButton variant="ghost" size="sm" icon-only :icon="Play" :loading="projectActionFor(project.id) === 'start'" :disabled="projectActionDisabled(project, 'start')" :aria-label="`启动项目 ${project.name}`" @click="performProjectAction(project, 'start')"><span>启动项目</span></ActionButton>
            </ElTooltip>
            <ElTooltip content="停止项目" placement="top">
              <ActionButton variant="danger" size="sm" icon-only :icon="Square" :loading="projectActionFor(project.id) === 'stop'" :disabled="projectActionDisabled(project, 'stop')" :aria-label="`停止项目 ${project.name}`" @click="performProjectAction(project, 'stop')"><span>停止项目</span></ActionButton>
            </ElTooltip>
            <ElTooltip content="重启项目" placement="top">
              <ActionButton variant="secondary" size="sm" icon-only :icon="RotateCcw" :loading="projectActionFor(project.id) === 'restart'" :disabled="projectActionDisabled(project, 'restart')" :aria-label="`重启项目 ${project.name}`" @click="performProjectAction(project, 'restart')"><span>重启项目</span></ActionButton>
            </ElTooltip>
            <ElTooltip :content="projectDeleteDisabledReason(project) || '删除已停止项目；保留镜像、卷和配置文件'" placement="top">
              <ActionButton variant="danger" size="sm" icon-only :icon="Trash2" :loading="projectDeletePending === project.id" :disabled="Boolean(projectDeleteDisabledReason(project))" :aria-label="`删除项目 ${project.name}`" @click="confirmDeleteProject(project)"><span>删除项目</span></ActionButton>
            </ElTooltip>
          </div>
          <div v-if="projectErrorsFor(project.id).length" class="project-row-feedback" role="alert">
            <strong>部分容器操作失败</strong>
            <ul><li v-for="failure in projectErrorsFor(project.id)" :key="failure.containerId">{{ failure.name }}：{{ failure.message }}</li></ul>
          </div>
        </div>
        <div v-if="!projects.length" class="table-empty">没有匹配的 Docker 项目。</div>
        <footer v-else class="resource-pagination">
          <ListPageSizeControl list-key="docker.projects" />
          <div>
            <button type="button" :disabled="projectPage <= 1" @click="projectPage -= 1">上一页</button>
            <strong>{{ projectPage }} / {{ projectPageCount }}</strong>
            <button type="button" :disabled="projectPage >= projectPageCount" @click="projectPage += 1">下一页</button>
          </div>
        </footer>
      </section>

      <section v-if="activeView === 'projects'" class="docker-mobile-list" aria-label="Docker 项目列表">
        <article v-for="project in pagedProjects" :key="project.id" class="mobile-project panel interactive-surface" @click="updateSelectedProject(project.id)">
          <header>
            <div class="project-name">
              <span><Boxes :size="18" /></span>
              <div><strong>{{ project.name }}</strong><small>{{ project.kind === 'compose' ? `Compose · ${project.configFiles.length || 1} 个配置文件` : '独立容器组' }}</small></div>
            </div>
            <StatusPill :label="projectStateLabel(project)" :tone="projectStateTone(project)" />
          </header>
          <dl>
            <div><dt>容器</dt><dd>{{ project.runningCount }}/{{ project.containerCount }} 运行</dd></div>
            <div><dt>公开端口</dt><dd>{{ portsFor(project.id).join('、') || '无' }}</dd></div>
            <div><dt>工作目录</dt><dd :title="project.workingDirectory">{{ project.workingDirectory || '自动发现' }}</dd></div>
          </dl>
          <div class="mobile-project-actions" @click.stop>
            <ActionButton variant="ghost" size="sm" :icon="Play" :loading="projectActionFor(project.id) === 'start'" :disabled="projectActionDisabled(project, 'start')" @click="performProjectAction(project, 'start')">启动</ActionButton>
            <ActionButton variant="danger" size="sm" :icon="Square" :loading="projectActionFor(project.id) === 'stop'" :disabled="projectActionDisabled(project, 'stop')" @click="performProjectAction(project, 'stop')">停止</ActionButton>
            <ActionButton variant="secondary" size="sm" :icon="RotateCcw" :loading="projectActionFor(project.id) === 'restart'" :disabled="projectActionDisabled(project, 'restart')" @click="performProjectAction(project, 'restart')">重启</ActionButton>
            <ElTooltip :content="projectDeleteDisabledReason(project) || '删除已停止项目；保留镜像、卷和配置文件'">
              <ActionButton variant="danger" size="sm" :icon="Trash2" :loading="projectDeletePending === project.id" :disabled="Boolean(projectDeleteDisabledReason(project))" @click="confirmDeleteProject(project)">删除</ActionButton>
            </ElTooltip>
          </div>
          <div v-if="projectErrorsFor(project.id).length" class="project-action-errors" role="alert">
            <strong>部分容器操作失败</strong>
            <ul><li v-for="failure in projectErrorsFor(project.id)" :key="failure.containerId">{{ failure.name }}：{{ failure.message }}</li></ul>
          </div>
        </article>
        <p v-if="!projects.length" class="table-empty panel">没有匹配的 Docker 项目。</p>
        <footer v-else class="resource-pagination panel">
          <ListPageSizeControl list-key="docker.projects" />
          <div>
            <button type="button" :disabled="projectPage <= 1" @click="projectPage -= 1">上一页</button>
            <button type="button" :disabled="projectPage >= projectPageCount" @click="projectPage += 1">下一页</button>
          </div>
        </footer>
      </section>

      <DockerContainerPanel
        v-else
        :containers="inventory?.containers ?? []"
        :query="query"
        :state-filter="containerStateFilter"
        :action-pending="effectiveActionPending"
        :page-size="containerPageSize"
        @action="performAction"
        @logs="openLogs"
      />
    </template>

    <ProjectDetailDrawer
      v-model="detailOpen"
      :project="selectedProject"
      :containers="selectedContainers"
      :host-name="hostName"
      :allow-operations="true"
      :action-pending="effectiveActionPending"
      :project-action-pending="projectActionFor(selectedProject?.id ?? '')"
      :project-action-errors="projectErrorsFor(selectedProject?.id ?? '')"
      @action="performAction"
      @logs="openLogs"
      @compose="composeEditorOpen = true"
      @project-action="performSelectedProjectAction"
    />
    <ComposeEditorDrawer v-model="composeEditorOpen" :project="selectedProject" />

    <ContainerLogDrawer v-model="logOpen" :container-name="logContainerName" :loading="logLoading" :logs="logs" />
  </div>
</template>

<style scoped>
.docker-search { width: min(360px, 38vw); }
.docker-view-tabs { display: flex; gap: 3px; padding: 3px; border: 1px solid var(--ncp-line); border-radius: 10px; background: #fff; }
.docker-view-tabs button { display: flex; min-height: 38px; align-items: center; gap: 6px; padding: 0 13px; border-radius: 7px; background: transparent; color: var(--ncp-text-muted); font-size: .82rem; font-weight: 720; white-space: nowrap; }
.docker-view-tabs button.active { background: var(--ncp-primary); box-shadow: 0 5px 14px rgba(52,116,212,.18); color: #fff; }
.state-filter { display: flex; flex: 0 0 auto; gap: 3px; padding: 3px; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.state-filter button { min-height: 36px; padding: 0 12px; border-radius: 7px; background: transparent; color: var(--ncp-text-muted); font-size: .8rem; font-weight: 700; }
.state-filter button.active { background: #fff; box-shadow: 0 2px 8px rgba(28,45,75,.08); color: var(--ncp-primary-strong); }
.action-error { display: flex; min-height: 44px; align-items: center; justify-content: space-between; padding: 9px 13px; border: 1px solid rgba(212,81,93,.2); border-radius: 10px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .7rem; }
.action-error button { min-height: 36px; padding: 0 10px; border-radius: 8px; background: rgba(255,255,255,.65); color: inherit; font-weight: 700; }
.resource-state { display: grid; min-height: 220px; place-items: center; align-content: center; gap: 8px; padding: 28px; color: var(--ncp-text-subtle); text-align: center; }
.resource-state strong { color: var(--ncp-text); font-size: .9rem; }
.resource-state span { font-size: .75rem; }
.resource-state--error { border-color: var(--ncp-danger-border); color: var(--ncp-danger-strong); }
.resource-state--error strong { color: var(--ncp-danger-strong); }
.docker-table { overflow: hidden; border-color:rgba(203,214,228,.9); }
.docker-table__head, .project-row { display: grid; grid-template-columns: minmax(210px,1.3fr) 108px 74px 180px minmax(170px,1fr) minmax(180px,auto); align-items: center; gap: 12px; }
.docker-table__head { min-height: 46px; padding: 0 18px; background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); font-size: .82rem; font-weight: 720; }
.project-row { width: 100%; min-height: 70px; padding: 10px 18px; border-top: 1px solid var(--ncp-line); background: #fff; color: var(--ncp-text-muted); cursor: pointer; font-size: .86rem; text-align: left; transition: background-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.project-row:hover { position: relative; z-index: 1; background: var(--ncp-surface-hover); }
.docker-table__head>span:nth-child(2),.docker-table__head>span:nth-child(3),.docker-table__head>span:nth-child(4),.docker-table__head>span:nth-child(6),.project-row>:nth-child(2),.project-row>:nth-child(3),.project-row>:nth-child(4),.project-row>:nth-child(6){justify-self:center;text-align:center}
.project-name { display: flex; min-width: 0; align-items: center; gap: 9px; }
.project-name>span { display: grid; width: 36px; height: 36px; flex: 0 0 auto; place-items: center; border-radius: 10px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.project-name>div { display: grid; min-width: 0; gap: 1px; }
.project-name strong { overflow: hidden; color: var(--ncp-text); font-size: .92rem; text-overflow: ellipsis; white-space: nowrap; }
.project-name small { color: var(--ncp-text-subtle); font-size: .8rem; }
.mono { font-family: 'JetBrains Mono Variable', monospace; }
.port-cell { display: flex; min-width: 0; align-items: center; gap: 5px; }
.port-cell a { display: flex; min-height: 32px; align-items: center; gap: 3px; padding: 0 8px; border-radius: 7px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', monospace; font-size: .72rem; }
.port-cell>span { color: var(--ncp-text-subtle); font-size: .72rem; }
.path-cell { overflow: hidden; padding-right: 8px; color: var(--ncp-text-muted); font-family:var(--ncp-font-mono); font-size:.78rem; text-overflow: ellipsis; white-space: nowrap; }
.project-actions { display: flex; align-items: center; justify-content: flex-end; gap: 4px; }
.project-actions :deep(.action-button) { width: 40px; min-width: 40px; padding: 0; }
.project-row-feedback { grid-column: 1 / -1; align-self: stretch; padding: 8px 10px; border: 1px solid var(--ncp-danger-border); border-radius: 8px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .72rem; }
.project-row-feedback strong, .project-action-errors strong { font-weight: 760; }
.project-row-feedback ul, .project-action-errors ul { display: grid; gap: 3px; margin: 4px 0 0; padding-left: 17px; }
.table-empty { padding: 36px; color: var(--ncp-text-subtle); font-size: .82rem; text-align: center; }
.resource-pagination { display:flex; min-height:52px; align-items:center; justify-content:space-between; gap:16px; padding:8px 18px; border-top:1px solid var(--ncp-line); color:var(--ncp-text-subtle); font-size:.82rem; }
.resource-pagination div { display:flex; align-items:center; gap:8px; }
.resource-pagination button { min-height:34px; padding:0 11px; border:1px solid var(--ncp-line); border-radius:8px; background:#fff; color:var(--ncp-text-muted); font-weight:680; }
.resource-pagination button:disabled { cursor:not-allowed; opacity:.42; }
.resource-pagination strong { min-width:62px; color:var(--ncp-text); text-align:center; }
.docker-mobile-list { display: none; }
.mobile-project-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 7px; }
.mobile-project-actions :deep(.action-button) { width: 100%; }
.project-action-errors { padding: 9px 10px; border: 1px solid var(--ncp-danger-border); border-radius: 9px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .7rem; }
.spin { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media(max-width: 1120px) {
  .docker-table__head, .project-row { grid-template-columns: minmax(190px,1.2fr) 100px 65px 150px minmax(140px,1fr) minmax(180px,auto); gap: 8px; }
}
@media(max-width: 900px) { .docker-search { min-width: 0; width: 100%; } }
@media(max-width: 820px) {
  .docker-table { display: none; }
  .docker-mobile-list { display: grid; gap: 10px; }
  .docker-view-tabs { width: 100%; }
  .docker-view-tabs button { flex: 1; min-height: 40px; justify-content: center; }
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
