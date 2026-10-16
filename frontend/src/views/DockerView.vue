<script setup lang="ts">
import { computed, h, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Boxes, Box, ExternalLink, Images, LoaderCircle, Play, RotateCcw, Search, Square, Trash2 } from '@lucide/vue'
import { ElInput, ElMessage, ElMessageBox, ElTooltip } from 'element-plus'

import { composeActionStateMatches, dockerActionStateMatches, DockerActionOutcomeUnknownError } from '@/api/dockerActionOutcome'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'
import { NcpApiError, requestContainerDetails, requestContainerLogs, requestDockerProjectAction, type ContainerAction, type ContainerDetails, type ContainerLogsResult, type ComposeLifecycleResult, type DockerInventory, type DockerProject, type DockerProjectActionResult } from '@/api/system'
import ActionButton from '@/components/ActionButton.vue'
import ContainerLogDrawer from '@/components/ContainerLogDrawer.vue'
import DockerContainerPanel from '@/components/DockerContainerPanel.vue'
import ContainerCreateProtectionPanel from '@/components/ContainerCreateProtectionPanel.vue'
import { useDockerContainerCreateJobs, containerCreateCanRelease, type ContainerCreateAdmission } from '@/composables/useDockerContainerCreateJobs'
import ProjectDeletionProtectionPanel from '@/components/ProjectDeletionProtectionPanel.vue'
import { useDockerProjectDeletionProtection, projectDeletionCanRelease, type ProjectDeletionAdmission } from '@/composables/useDockerProjectDeletionProtection'
import ProjectActionProtectionPanel from '@/components/ProjectActionProtectionPanel.vue'
import { useDockerProjectActionJobs, projectActionCanRelease, type ProjectActionAdmission, type ProjectActionCompletion } from '@/composables/useDockerProjectActionJobs'
import { captureDockerMutationFence } from '@/composables/dockerMutationFence'
import ContainerActionProtectionPanel from '@/components/ContainerActionProtectionPanel.vue'
import { useDockerContainerActionJobs, containerActionCanRelease, type ContainerActionAdmission } from '@/composables/useDockerContainerActionJobs'
import DockerImagePanel from '@/components/DockerImagePanel.vue'
import ListIconButton from '@/components/ListIconButton.vue'
import ComposeEditorDrawer from '@/components/ComposeEditorDrawer.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import ProjectDetailDrawer from '@/components/ProjectDetailDrawer.vue'
import StatusPill from '@/components/StatusPill.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import SectionTabs, { type SectionTab } from '@/components/SectionTabs.vue'
import CompactSegmentedFilter from '@/components/CompactSegmentedFilter.vue'
import ResourcePagination from '@/components/ResourcePagination.vue'
import { useManualRefreshRegistry } from '@/composables/manualRefresh'
import { useListPreference } from '@/composables/useListPreference'
import {
  dockerActionLabel,
  dockerActionNeedsConfirmation,
  dockerContainerActionJobConfirmation,
  dockerProjectActionConfirmation,
  dockerProjectActionDisabled,
  dockerProjectActionTargets,
  dockerProjectDeleteDisabledReason,
} from '@/domain/dockerLifecycle'
import { projectStateTone as projectStateToneBase } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'
import { useSitesStore } from '@/stores/sites'
import { createScopedReadRequest } from '@/domain/scopedReadRequest'
import { presentDockerPorts } from '@/domain/dockerPorts'

type StateFilter = 'all' | DockerProject['state']
type ContainerStateFilter = 'all' | 'running' | 'stopped'
type DockerViewMode = 'projects' | 'containers' | 'images'
type DockerContainer = DockerInventory['containers'][number]
type ProjectActionError = { containerId: string; name: string; message: string; scope?: 'project' | 'container' }

const pageRoot = useTemplateRef<HTMLElement>('pageRoot')
const tooltipTarget = shallowRef<HTMLElement>()
const route = useRoute()
const manualRefreshRegistry = useManualRefreshRegistry()
const router = useRouter()
const systemStore = useSystemStore()
const sitesStore = useSitesStore()
const hostName = window.location.hostname
const query = ref('')
const stateFilter = ref<StateFilter>('all')
const containerStateFilter = ref<ContainerStateFilter>('all')
const activeView = ref<DockerViewMode>('projects')
const actionPending = ref<string | null>(null)
const containerJobs = useDockerContainerActionJobs()
const { entries: containerAdmissions, blockedIds: blockedContainerIds } = containerJobs
let containerActionOwner: symbol | null = null
const projectActionPending = ref<{ projectId: string; action: ContainerAction } | null>(null)
const projectDeletion = useDockerProjectDeletionProtection()
const { entries: projectDeletionEntries, blockedIds: blockedProjectDeletionIds } = projectDeletion
const projectDeletePending = computed(() => projectDeletionEntries.value.find(entry => entry.phase === 'submitting')?.projectId ?? null)
const projectActionErrors = ref<Record<string, ProjectActionError[]>>({})
const projectContainerActionErrors = ref<Record<string, string>>({})
const actionError = ref<string | null>(null)
let viewActive = true
const createJobs = useDockerContainerCreateJobs()
const { entry: createAdmission } = createJobs
const createResult = shallowRef<{ containerId: string; created: true; started: boolean } | null>(null)
const creationSession = captureSession()
const mutationFence = captureDockerMutationFence(creationSession)
const projectJobs = useDockerProjectActionJobs()
const { entry: projectAdmission } = projectJobs
const projectResult = shallowRef<ProjectActionCompletion | null>(null)
const composeReservations = new Map<symbol, ReturnType<typeof captureDockerMutationFence>>()
const clearProjectResult = () => { projectResult.value = null }
creationSession.signal.addEventListener('abort', clearProjectResult, { once: true })
watch(projectAdmission, (entry, previous) => {
  if (entry && entry.token !== previous?.token) projectResult.value = null
  if (!projectJobs.alive()) { projectResult.value = null; return }
  if (!viewActive || !entry || !entry.completionVersion) return
  const result = projectJobs.consumeCompletion(entry, entry.completionVersion)
  if (!result) return
  projectResult.value = result
  void refreshContainerInventory()
}, { immediate: true, flush: 'sync' })
const clearCreateResult = () => { createResult.value = null }
creationSession.signal.addEventListener('abort', clearCreateResult, { once: true })
watch(createAdmission, entry => {
  if (!createJobs.alive()) { createResult.value = null; return }
  if (!viewActive || !entry || !entry.completionVersion) return
  const result = createJobs.consumeCompletion(entry, entry.completionVersion)
  if (!result) return
  createResult.value = result
  // 必须先同步消费并保留精确结果，再进行独立的只读状态刷新。
  void refreshContainerInventory()
}, { immediate: true, flush: 'sync' })
const detailRequest = createScopedReadRequest()
const logOpen = ref(false)
const logLoading = ref(false)
const logContainerName = ref('')
const logs = ref<ContainerLogsResult | null>(null)
const containerDetails = ref<ContainerDetails | null>(null)
const containerDrawerError = ref('')
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
const inventoryLoading = computed(() => !inventory.value && (systemStore.inventoryRefreshing || !systemStore.inventoryErrorCode))
const inventoryUnavailable = computed(() => !inventory.value && !systemStore.inventoryRefreshing && Boolean(systemStore.inventoryErrorCode))
const effectiveActionPending = computed(() => {
  if (actionPending.value) return actionPending.value
  if (projectActionPending.value) return `project:${projectActionPending.value.projectId}:${projectActionPending.value.action}`
  if (projectDeletePending.value) return `project:${projectDeletePending.value}:delete`
  return null
})
const selectedProject = computed(() => allProjects.value.find((project) => project.id === route.query.project) ?? null)
const selectedContainers = computed(() => containersFor(selectedProject.value?.id ?? ''))
const selectedProjectContainerActionError = computed(() => selectedProject.value ? projectContainerActionErrors.value[selectedProject.value.id] ?? '' : '')
const detailOpen = computed({
  get: () => Boolean(selectedProject.value),
  set: (open) => {
    if (!open) void updateSelectedProject(null)
  },
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '全部项目', value: inventory.value ? allProjects.value.length : '—' },
  { label: '运行容器', value: inventory.value?.engine.containersRunning ?? '—', tone: 'success' },
  { label: '停止容器', value: inventory.value?.engine.containersStopped ?? '—', tone: 'warning' },
  { label: '本地镜像', value: inventory.value?.engine.images ?? '—' },
])
const searchPlaceholder = computed(() => {
  if (activeView.value === 'containers') return '搜索容器、镜像或所属项目'
  if (activeView.value === 'images') return '搜索镜像名称、标签或 ID'
  return '搜索项目或工作目录'
})
const dockerViewTabs: SectionTab[] = [
  { value: 'projects', label: '项目', icon: Boxes },
  { value: 'containers', label: '容器', icon: Box },
  { value: 'images', label: '镜像', icon: Images },
]
const dockerStateFilters = [
  { value: 'all', label: '全部' },
  { value: 'running', label: '运行中' },
  { value: 'stopped', label: '已停止' },
]

function containersFor(projectId: string) {
  return inventory.value?.containers.filter((container) => container.projectId === projectId) ?? []
}
function portsFor(projectId: string) {
  const site = sitesStore.visibleSites.find((candidate) => candidate.projectId === projectId && candidate.launchUrl)
  const webUrls: Record<string, string> = {}
  const mappings = containersFor(projectId).flatMap((container) => container.ports)
  if (site?.launchUrl && site.primaryPort > 0) {
    for (const port of mappings) {
      if (port.publicPort === site.primaryPort && (port.protocol || 'tcp').toLowerCase() === 'tcp') {
        webUrls[`${port.hostIp || '0.0.0.0'}:${port.publicPort}/tcp->${port.privatePort}`] = site.launchUrl
      }
    }
  }
  return presentDockerPorts(mappings, { webUrls })
}
function portLabelsFor(projectId: string) {
  return portsFor(projectId).map((port) => port.label)
}
function verifiedWebUrlsFor(projectId: string) {
  const site = sitesStore.visibleSites.find((candidate) => candidate.projectId === projectId && candidate.launchUrl)
  if (!site?.launchUrl || site.primaryPort <= 0) return {}
  const urls: Record<string, string> = {}
  for (const port of portsFor(projectId)) {
    if (port.protocol === 'tcp' && port.hostPort === site.primaryPort) urls[port.key] = site.launchUrl
  }
  return urls
}
function stateLabel(state: DockerProject['state']) {
  return state === 'running' ? '运行中' : state === 'degraded' ? '需关注' : '已停止'
}
function projectActionFor(projectId: string) {
  return projectActionPending.value?.projectId === projectId ? projectActionPending.value.action : null
}
function projectStateLabel(project: DockerProject) {
  const action = projectActionFor(project.id)
  return action ? `正在${dockerActionLabel(action)}` : stateLabel(project.state)
}
function projectStateTone(project: DockerProject) {
  return projectActionFor(project.id) ? 'pending' : projectStateToneBase(project.state)
}
function projectErrorsFor(projectId: string) {
  return projectActionErrors.value[projectId] ?? []
}
function projectHasProtectedContainer(project: DockerProject) {
  return containersFor(project.id).some(container => blockedContainerIds.value.includes(container.id))
}
function projectActionDisabled(project: DockerProject, action: ContainerAction) {
  if (project.kind === 'standalone' ? projectJobs.blocked.value : mutationFence.otherMutationsBlocked.value) return true
  if (projectHasProtectedContainer(project)) return true
  return dockerProjectActionDisabled(
    project,
    action,
    Boolean(actionPending.value) || Boolean(projectActionPending.value) || Boolean(projectDeletePending.value),
  )
}
function projectDeleteDisabledReason(project: DockerProject) {
  if (mutationFence.otherMutationsBlocked.value) return '独立项目操作结果待核对，请先保留防重复保护。'
  if (blockedProjectDeletionIds.value.includes(project.id)) return '该项目删除等待确认或结果待核对，请保留防重复保护。'
  if (projectHasProtectedContainer(project)) return '项目含有操作结果待核对的容器，请先核对。'
  return dockerProjectDeleteDisabledReason(
    project,
    Boolean(actionPending.value) || Boolean(projectActionPending.value) || Boolean(projectDeletePending.value),
  )
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
      .filter((item) => !item.success || !dockerActionStateMatches(action, item.state))
      .map((item) => ({
        containerId: item.containerId,
        name: item.name || targets.find((container) => container.id === item.containerId)?.name || item.containerId.slice(0, 12),
        message: item.success ? '命令已受理，读回时尚未达到目标状态；请先只读核对。' : '容器操作结果未确认，可能已经生效；请先只读核对，勿重复提交。',
      }))
    if (result.completed || failures.length) return failures
    return targets.map((container) => ({
      containerId: container.id,
      name: container.name || container.id.slice(0, 12),
      message: '项目回执未确认完成，请先只读核对状态，勿重复提交。',
    }))
  }
  const failures = result.services
    .map((service, index) => ({ service, index }))
    .filter(({ service }) => !composeActionStateMatches(action, service.state))
    .map(({ service, index }) => ({
      containerId: `compose-service:${index}`,
      name: service.name || service.containerId || '未命名服务',
      message: `状态复核尚未达到目标；请先只读核对，勿重复提交。`,
    }))
  if (!result.completed && !failures.length) {
    return [{ containerId: `project:${result.projectId}`, name: '项目操作', scope: 'project' as const, message: '项目回执未确认完成，请先只读核对状态，勿重复提交。' }]
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
  if (mutationFence.otherMutationsBlocked.value || actionPending.value || projectActionPending.value || projectDeletePending.value) return null
  const session = captureSession()
  const current = () => viewActive && isCurrentSession(session.generation)
  const container = inventory.value?.containers.find((item) => item.id === containerId)
  if (!container) return null
  const admission = containerJobs.reserve(containerId, action, container.name)
  if (!admission) return null
  // 在禁用按钮前保存触发点；私有所有者避免相同容器动作的旧调用释放新锁。
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  let dismissed = false
  const pendingOwner = Symbol()
  containerActionOwner = pendingOwner
  // 在确认框之前占用本次操作，避免多个确认框依次触发同一写操作。
  actionPending.value = `${containerId}:${action}`
  let failure: string | null = null
  try {
    if (dockerActionNeedsConfirmation(action)) {
      try {
        await ElMessageBox.confirm(
          dockerContainerActionJobConfirmation({ id: admission.containerId, name: admission.name, image: container.image }, admission.action),
          `确认${dockerActionLabel(action)}容器`,
          { confirmButtonText: `确认${dockerActionLabel(action)}`, cancelButtonText: '取消', type: action === 'stop' ? 'warning' : 'info', customClass: 'ncp-container-action-confirmation' },
        )
      } catch (decision) {
        dismissed = decision === 'cancel' || decision === 'close'
        return null
      }
    }
    if (!current()) return null
    actionError.value = null
    const outcome = await containerJobs.dispatch(admission)
    if (!current()) return null
    if (outcome === 'accepted') {
      ElMessage.info('容器操作任务已接收，尚未确认完成。可在防重复保护面板只读核对。')
      void containerJobs.check(admission, refreshContainerInventory)
    } else if (outcome) {
      failure = outcome === 'rejected' ? '服务器明确拒绝接收容器操作，请检查会话或稍后重试。' : '容器操作接收未知，可能已经执行。请先只读核对，勿重复提交。'
      actionError.value = failure
      ElMessage.warning(failure)
    }
  } finally {
    containerJobs.cancel(admission)
    if (viewActive && containerActionOwner === pendingOwner) {
      containerActionOwner = null
      actionPending.value = null
      if (dismissed) {
        // 等按钮重新启用后再恢复；旧会话或新操作不能抢回焦点。
        await nextTick()
        if (current() && !effectiveActionPending.value && returnFocus?.isConnected && !returnFocus.matches(':disabled')) {
          returnFocus.focus({ preventScroll: true })
        }
      }
    }
  }
  return failure
}
async function refreshContainerInventory() {
  const result = await systemStore.refresh({ inventory: true, force: true })
  return result.succeeded.includes('inventory')
}
async function refreshDockerPage() {
  const inventoryRead = systemStore.refresh({ inventory: true }).then(result => result.succeeded.includes('inventory'))
  await Promise.all(containerAdmissions.value.filter(entry => entry.phase === 'known').map(entry => containerJobs.check(entry, () => inventoryRead)))
  const createEntry = createAdmission.value
  if (createEntry && !['confirming', 'submitting', 'resolved'].includes(createEntry.phase)) await createJobs.check(createEntry, () => inventoryRead)
  const projectEntry = projectAdmission.value
  if (projectEntry && ['known', 'unknown'].includes(projectEntry.phase)) await projectJobs.check(projectEntry, () => inventoryRead)
  await inventoryRead
}
const unregisterManualRefresh = manualRefreshRegistry?.register(refreshDockerPage)
async function releaseCreateProtection(entry: ContainerCreateAdmission) {
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  await createJobs.check(entry, refreshContainerInventory)
  if (!viewActive || !createJobs.alive()) return
  const checked = createAdmission.value
  if (!checked || checked.token !== entry.token || !containerCreateCanRelease(checked)) return
  const version = checked.checkVersion
  try {
    await ElMessageBox.confirm('仅解除本登录会话防重复保护，不会取消、重试或重发创建任务。之前创建可能已经执行，再次提交可能重复创建。整页刷新与其他标签页不受此保护。确认自行承担重复创建风险并解除？', '解除创建防重复保护', { confirmButtonText: '确认风险并解除', cancelButtonText: '保留保护', type: 'warning', customClass: 'ncp-container-action-confirmation' })
    if (viewActive && createJobs.alive()) createJobs.release(checked, version)
  } catch (decision) {
    if (decision !== 'cancel' && decision !== 'close') return
    await nextTick()
    if (viewActive && createJobs.alive() && createAdmission.value?.token === checked.token && createAdmission.value.checkVersion === version && returnFocus?.isConnected && !returnFocus.matches(':disabled')) returnFocus.focus({ preventScroll: true })
  }
}
async function releaseContainerProtection(entry: ContainerActionAdmission) {
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  // 每次解除前重新只读核对；确认框等待期间的新观察不能被旧确认覆盖。
  await containerJobs.check(entry, refreshContainerInventory)
  if (!viewActive || !containerJobs.alive()) return
  const checked = containerAdmissions.value.find(item => item.token === entry.token)
  if (!checked || !containerActionCanRelease(checked)) return
  const version = checked.checkVersion
  try {
    await ElMessageBox.confirm('仅解除本登录会话的本页防重复保护，不会取消、撤销或重发任务。之前操作可能仍已执行；当前状态或核对失败都不能证明未接收。之后再次操作可能重复执行。确认自行承担此风险并解除？', '解除本页防重复保护', { confirmButtonText: '确认风险并解除', cancelButtonText: '保留保护', type: 'warning', customClass: 'ncp-container-action-confirmation' })
    if (viewActive) containerJobs.release(checked, version)
  } catch (decision) {
    if (decision !== 'cancel' && decision !== 'close') return
    await nextTick()
    if (viewActive && containerJobs.alive() && containerAdmissions.value.some(item => item.token === checked.token && item.checkVersion === version) && returnFocus?.isConnected && !returnFocus.matches(':disabled')) returnFocus.focus({ preventScroll: true })
  }
}
async function performDetailAction(containerId: string, action: ContainerAction) {
  const projectId = selectedProject.value?.id
  const session = captureSession()
  if (!projectId) return
  projectContainerActionErrors.value = { ...projectContainerActionErrors.value, [projectId]: '' }
  const failure = await performAction(containerId, action)
  if (failure && viewActive && isCurrentSession(session.generation)) {
    projectContainerActionErrors.value = { ...projectContainerActionErrors.value, [projectId]: failure }
  }
}
async function releaseProjectActionProtection(entry: ProjectActionAdmission) {
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  await projectJobs.check(entry, refreshContainerInventory)
  if (!viewActive || !projectJobs.alive()) return
  const checked = projectAdmission.value
  if (!checked || checked.token !== entry.token || !projectActionCanRelease(checked)) return
  const version = checked.checkVersion
  try {
    await ElMessageBox.confirm('仅解除本登录会话的防重复保护，不会取消、重试或重发任务。先前项目操作可能已经执行，再次提交可能重复执行。当前容器状态不能证明先前未执行。确认自行承担重复执行风险并解除？', '解除项目操作防重复保护', { confirmButtonText: '确认风险并解除', cancelButtonText: '保留保护', type: 'warning', customClass: 'ncp-container-action-confirmation' })
    if (viewActive && projectJobs.alive()) projectJobs.release(checked, version, true)
  } catch (decision) {
    if (decision !== 'cancel' && decision !== 'close') return
    await nextTick()
    if (viewActive && projectJobs.alive() && projectAdmission.value?.token === checked.token && projectAdmission.value.checkVersion === version && returnFocus?.isConnected && !returnFocus.matches(':disabled')) returnFocus.focus({ preventScroll: true })
  }
}
async function performProjectAction(project: DockerProject, action: ContainerAction) {
  const session = captureSession()
  const mutationFence = captureDockerMutationFence(session)
  if (project.kind === 'standalone' ? projectJobs.blocked.value : mutationFence.otherMutationsBlocked.value) return
  if (projectHasProtectedContainer(project)) return
  if (actionPending.value || projectActionPending.value || projectDeletePending.value) return
  const current = () => viewActive && isCurrentSession(session.generation)
  // 确认框和请求等待期间，清单可能刷新；确认及回执只属于这一份快照。
  project = { ...project, configFiles: [...project.configFiles] }
  const actionTargets = dockerProjectActionTargets(project, inventory.value?.containers ?? [], action).map((container) => ({ ...container }))
  if (!actionTargets.length) {
    ElMessage.info(`项目“${project.name}”已经${action === 'start' ? '全部运行' : action === 'stop' ? '全部停止' : '没有可重启的容器'}`)
    return
  }
  // 原生 disabled 会先移走焦点，需在占用操作前保存真实键盘触发点。
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const admission = project.kind === 'standalone' ? projectJobs.reserve(actionTargets.map(container => container.id), action) : null
  const composeToken = project.kind === 'compose' ? Symbol() : null
  if (project.kind === 'standalone' && !admission) {
    ElMessage.warning('项目操作仅支持 1–128 个唯一完整容器 ID；请先核对范围。')
    return
  }
  if (composeToken && !mutationFence.reserve('compose', composeToken)) return
  if (composeToken) composeReservations.set(composeToken, mutationFence)
  let dismissed = false
  projectActionPending.value = { projectId: project.id, action }
  const pendingOwner = projectActionPending.value
  const failures: ProjectActionError[] = []
  try {
    if (dockerActionNeedsConfirmation(action)) {
      try {
        const confirmationText = dockerProjectActionConfirmation(project, action, actionTargets.length) + (admission ? `\n冻结操作：${dockerActionLabel(admission.action)}；范围：${admission.containerIds.length} 个容器。\n${admission.containerIds.join('\n')}\n接收后在后台执行，离开页面不会取消；不支持取消或重试，已接收不代表完成。` : '')
        const confirmationMessage = admission && admission.containerIds.length > 8
          ? h('div', { tabindex: 0, role: 'region', 'aria-label': '冻结项目操作范围', class: 'ncp-project-action-scope' }, confirmationText)
          : confirmationText
        await ElMessageBox.confirm(
          confirmationMessage,
          `确认${dockerActionLabel(action)}项目`,
          {
            confirmButtonText: `确认${dockerActionLabel(action)}`,
            cancelButtonText: '取消',
            type: action === 'stop' ? 'warning' : 'info',
            customClass: project.kind === 'compose' ? 'ncp-compose-action-confirmation' : 'ncp-container-action-confirmation',
          },
        )
      } catch (decision) {
        dismissed = decision === 'cancel' || decision === 'close'
        return
      }
    }
    if (!current() || projectHasProtectedContainer(project) || actionTargets.some(container => blockedContainerIds.value.includes(container.id))) return
    projectActionErrors.value = { ...projectActionErrors.value, [project.id]: [] }
    actionError.value = null
    if (admission) {
      const outcome = await projectJobs.dispatch(admission)
      if (!current()) return
      if (outcome === 'accepted') {
        ElMessage.info('项目操作任务已接收，尚未确认完成。请在防重复保护面板只读核对。')
        void projectJobs.check(admission, refreshContainerInventory)
      } else if (outcome) {
        actionError.value = outcome === 'rejected' ? '服务器明确拒绝接收项目操作，请检查会话或稍后重试。' : '项目操作接收未知，可能已经执行。请先只读核对，勿重复提交。'
        ElMessage.warning(actionError.value)
      }
      return
    }
    if (!composeToken || !mutationFence.owns('compose', composeToken)) return
    composeReservations.delete(composeToken)
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
      if (!current()) return
      failures.push({
        containerId: `project:${project.id}`,
        name: '项目操作',
        message: errorMessage(error, new DockerActionOutcomeUnknownError(project.kind).message),
        scope: 'project',
      })
    }
    if (!current()) return
    if (!failures.length && result) failures.push(...projectActionFailures(result, actionTargets, action))
    if (failures.length) {
      projectActionErrors.value = { ...projectActionErrors.value, [project.id]: failures }
      actionError.value = failures.some((failure) => failure.scope === 'project') ? failures[0]!.message : (project.kind === 'standalone' ? `项目${dockerActionLabel(action)}回执中有 ${failures.length} 个容器尚未确认达到目标；请先只读核对。` : `项目${dockerActionLabel(action)}回执中有 ${failures.length} 个服务尚未确认达到目标；请先只读核对，勿重复提交。`)
      ElMessage.warning(actionError.value)
    } else {
      projectActionErrors.value = { ...projectActionErrors.value, [project.id]: [] }
      ElMessage.success(project.kind === 'standalone'
        ? `项目“${project.name}”${dockerActionLabel(action)}命令已受理，读回时目标容器均${action === 'stop' ? '已停止' : '运行中'}。`
        : `项目“${project.name}”${dockerActionLabel(action)}命令已受理，读回时服务均${action === 'stop' ? '处于非运行状态' : '运行中'}。`)
    }
    try {
      const refreshResult = await systemStore.refresh({ inventory: true, force: true })
      if (current() && refreshResult.failed.length) actionError.value = [actionError.value, 'Docker 状态刷新失败，请手动重新加载。'].filter(Boolean).join(' ')
    } catch {
      if (current()) actionError.value = [actionError.value, 'Docker 状态刷新失败，请手动重新加载。'].filter(Boolean).join(' ')
    }
  } finally {
    if (admission) projectJobs.cancel(admission)
    if (composeToken) { composeReservations.delete(composeToken); mutationFence.release('compose', composeToken) }
    if (viewActive && projectActionPending.value === pendingOwner) {
      projectActionPending.value = null
      if (dismissed) {
        // 等按钮重新启用后再恢复；旧会话或新操作不能抢回焦点。
        await nextTick()
        if (current() && !effectiveActionPending.value && returnFocus?.isConnected && !returnFocus.matches(':disabled')) {
          returnFocus.focus({ preventScroll: true })
        }
      }
    }
  }
}
async function releaseProjectDeletion(entry: ProjectDeletionAdmission) {
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  await projectDeletion.check(entry, refreshContainerInventory)
  if (!viewActive || !projectDeletion.alive()) return
  const checked = projectDeletionEntries.value.find(item => item.token === entry.token)
  if (!checked || !projectDeletionCanRelease(checked)) return
  const version = checked.checkVersion
  try {
    await ElMessageBox.confirm('本轮清单读取成功不证明删除结果，项目注册状态可能仍需人工核对。解除仅允许本会话再次删除，不会补偿、重发或取消原操作；再次删除可能重复执行。确认自行承担风险并解除？', '解除项目删除保护', { confirmButtonText: '确认风险并解除', cancelButtonText: '保留保护', type: 'warning', customClass: 'ncp-container-action-confirmation' })
    if (viewActive) projectDeletion.release(checked, version)
  } catch (decision) {
    if (decision !== 'cancel' && decision !== 'close') return
    await nextTick()
    if (viewActive && projectDeletion.alive() && projectDeletionEntries.value.some(item => item.token === checked.token && item.checkVersion === version) && returnFocus?.isConnected && !returnFocus.matches(':disabled')) returnFocus.focus({ preventScroll: true })
  }
}
async function confirmDeleteProject(project: DockerProject) {
  const disabledReason = projectDeleteDisabledReason(project)
  if (disabledReason) { ElMessage.info(disabledReason); return }
  const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const entry = projectDeletion.reserve(project)
  if (!entry) return
  const current = () => viewActive && projectDeletion.alive()
  try {
    await ElMessageBox.confirm(
      `将删除项目“${entry.name}”的已停止容器及 UGREEN 项目注册记录。镜像、卷、Compose 文件和工作目录不会删除。`,
      '确认删除 Docker 项目',
      { confirmButtonText: '删除项目', cancelButtonText: '取消', type: 'warning', customClass: 'ncp-container-action-confirmation' },
    )
  } catch {
    projectDeletion.cancel(entry)
    await nextTick()
    if (current() && returnFocus?.isConnected && !returnFocus.matches(':disabled')) returnFocus.focus({ preventScroll: true })
    return
  }
  if (!current()) { projectDeletion.cancel(entry); return }
  const latest = allProjects.value.find(item => item.id === entry.projectId)
  if (!latest || projectHasProtectedContainer(latest) || dockerProjectDeleteDisabledReason(latest, Boolean(actionPending.value) || Boolean(projectActionPending.value) || Boolean(projectDeletePending.value))) { projectDeletion.cancel(entry); return }
  actionError.value = null
  const outcome = await projectDeletion.dispatch(entry)
  if (!current() || !outcome) return
  if (outcome === 'deleted') {
    // 先保留已确认事实；路由等待或刷新失败不能改写删除回执。
    ElMessage.success(`项目“${entry.name}”已删除；镜像、卷和配置文件均已保留。`)
    if (selectedProject.value?.id === entry.projectId) {
      try { await updateSelectedProject(null) } catch { /* 路由失败不改写删除事实。 */ }
    }
    if (!current()) return
    let refreshed = false
    try { refreshed = await refreshContainerInventory() } catch { /* 独立只读刷新。 */ }
    if (current() && !refreshed) ElMessage.warning(`项目“${entry.name}”已删除，但状态刷新失败，请手动重新加载。`)
  } else {
    const message = outcome === 'rejected' ? '删除请求在执行前被明确拒绝，未执行本次删除。请核对项目状态后再操作。' : '项目删除结果未知，可能已删除部分或全部资源。防重复保护已保留，请先只读核对。'
    actionError.value = message
    ElMessage.error(message)
  }
}
function performSelectedProjectAction(action: ContainerAction) {
  if (selectedProject.value) void performProjectAction(selectedProject.value, action)
}
async function openContainerDetails(container: { id: string; name: string }) {
  const request = detailRequest.begin()
  const fetchDetails: typeof fetch = (input, init) => fetch(input, { ...init, signal: request.signal })
  logOpen.value = true
  logLoading.value = true
  logContainerName.value = container.name
  logs.value = null
  containerDetails.value = null
  containerDrawerError.value = ''
  const [detailsResult, logsResult] = await Promise.allSettled([
    requestContainerDetails(container.id, fetchDetails),
    requestContainerLogs(container.id, 200, fetchDetails),
  ])
  request.finish()
  if (!request.isCurrent() || !logOpen.value) return
  if (detailsResult.status === 'fulfilled') containerDetails.value = detailsResult.value
  else containerDrawerError.value = errorMessage(detailsResult.reason, '容器详情读取失败。')
  if (logsResult.status === 'fulfilled') logs.value = logsResult.value
  else containerDrawerError.value = [containerDrawerError.value, errorMessage(logsResult.reason, '容器日志读取失败。')].filter(Boolean).join(' ')
  logLoading.value = false
}

watch(logOpen, (open) => {
  if (!open) {
    detailRequest.invalidate()
    logLoading.value = false
    logs.value = null
    containerDetails.value = null
    containerDrawerError.value = ''
  }
}, { flush: 'sync' })
onBeforeUnmount(() => {
  for (const [token, fence] of composeReservations) fence.release('compose', token)
  composeReservations.clear()
  creationSession.signal.removeEventListener('abort', clearProjectResult)
  creationSession.signal.removeEventListener('abort', clearCreateResult); viewActive = false; detailRequest.invalidate(); unregisterManualRefresh?.() })

watch([() => route.query.project, allProjects], ([projectId, items]) => {
  if (projectId && items.length && !items.some((project) => project.id === projectId)) void updateSelectedProject(null)
})
watch([query, stateFilter, projectPageSize], () => { projectPage.value = 1 })
watch(projectPageCount, (count) => {
  if (projectPage.value > count) projectPage.value = count
})
onMounted(() => {
  // 提示仍传送到页面主地标内，避免表格裁切及 body 外悬浮内容。
  tooltipTarget.value = pageRoot.value?.closest<HTMLElement>('main, [role="main"]') ?? pageRoot.value ?? undefined
  void refreshDockerPage()
})
</script>

<template>
  <div ref="pageRoot" class="page workspace-page docker-page">
    <WorkspaceHeader title="Docker 管理" description="统一查看项目、容器、镜像和运行状态" :icon="Boxes" :stats="stats">
      <template #filters>
        <SectionTabs v-model="activeView" :tabs="dockerViewTabs" accessible-label="Docker 管理视图" />
        <CompactSegmentedFilter v-if="activeView === 'projects'" v-model="stateFilter" :options="dockerStateFilters" accessible-label="Docker 项目状态筛选" />
        <CompactSegmentedFilter v-else-if="activeView === 'containers'" v-model="containerStateFilter" :options="dockerStateFilters" accessible-label="Docker 容器状态筛选" />
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

    <ProjectActionProtectionPanel :entry="projectAdmission" :result="projectResult" @check="entry => projectJobs.check(entry, refreshContainerInventory)" @release="releaseProjectActionProtection" />

    <ContainerCreateProtectionPanel :entry="createAdmission" :result="createResult" @check="entry => createJobs.check(entry, refreshContainerInventory)" @release="releaseCreateProtection" />

    <ProjectDeletionProtectionPanel :entries="projectDeletionEntries" @check="entry => projectDeletion.check(entry, refreshContainerInventory)" @release="releaseProjectDeletion" />
    <ContainerActionProtectionPanel :entries="containerAdmissions" @check="entry => containerJobs.check(entry, refreshContainerInventory)" @release="releaseContainerProtection" />

    <section v-if="inventory && (systemStore.inventoryErrorCode || systemStore.inventoryStale)" class="inventory-recovery panel" role="status">
      <div>
        <strong>{{ systemStore.inventoryErrorCode ? 'Docker 状态更新失败，显示上次数据' : 'Docker 状态尚未更新，显示上次数据' }}</strong>
        <span>上次更新：{{ inventory.collectedAt }}{{ systemStore.inventoryErrorCode ? `（${systemStore.inventoryErrorCode}）` : '' }}。项目和容器状态可能已变化。</span>
      </div>
      <ActionButton variant="secondary" size="sm" :icon="RotateCcw" :loading="systemStore.inventoryRefreshing" @click="refreshDockerPage">重新加载</ActionButton>
    </section>

    <section v-if="inventoryLoading" class="resource-state panel" aria-live="polite" aria-busy="true">
      <LoaderCircle class="spin" :size="22" />
      <strong>正在读取 Docker 状态</strong>
      <span>项目和容器列表加载完成后会显示在这里。</span>
    </section>

    <section v-else-if="inventoryUnavailable" class="resource-state resource-state--error panel" role="alert">
      <strong>Docker 状态读取失败</strong>
      <span>无法读取项目和容器列表{{ systemStore.inventoryErrorCode ? `（${systemStore.inventoryErrorCode}）` : '' }}。</span>
      <ActionButton variant="secondary" size="sm" :icon="RotateCcw" :loading="systemStore.inventoryRefreshing" @click="refreshDockerPage">重新加载</ActionButton>
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
        <div v-for="project in pagedProjects" :key="project.id" class="project-row">
          <button class="project-name project-identity" type="button" :aria-label="`查看 Docker 项目 ${project.name}`" aria-haspopup="dialog" @click="updateSelectedProject(project.id)">
            <span><Boxes :size="18" /></span>
            <div><strong>{{ project.name }}</strong><small>{{ project.kind === 'compose' ? `Compose · ${project.configFiles.length || 1} 个配置文件` : '独立容器组' }}</small></div>
          </button>
          <StatusPill :label="projectStateLabel(project)" :tone="projectStateTone(project)" />
          <span class="mono">{{ project.runningCount }}/{{ project.containerCount }}</span>
          <div class="port-cell">
            <template v-if="portsFor(project.id).length">
              <ElTooltip :append-to="tooltipTarget" v-for="port in portsFor(project.id).slice(0, 3)" :key="port.key" :content="port.webUrl ? `打开已验证站点入口` : `复制端口 ${port.label}`">
                <a v-if="port.webUrl" :href="port.webUrl" target="_blank" rel="noreferrer" @click.stop>{{ port.hostPort }}<ExternalLink :size="12" /></a>
                <span v-else class="port-text" :title="port.label">{{ port.hostPort }}</span>
              </ElTooltip>
              <span v-if="portsFor(project.id).length > 3">+{{ portsFor(project.id).length - 3 }}</span>
            </template>
            <span v-else>无公开端口</span>
          </div>
          <ElTooltip :append-to="tooltipTarget" :content="project.workingDirectory || 'Docker 自动发现项目'" placement="top">
            <span class="path-cell">{{ project.workingDirectory || '自动发现' }}</span>
          </ElTooltip>
          <div class="project-actions" @click.stop>
            <ElTooltip :trigger-keys="[]" :disabled="Boolean(actionPending || projectActionPending || projectDeletePending)" :append-to="tooltipTarget" content="启动项目" placement="top">
              <ListIconButton :icon="Play" :loading="projectActionFor(project.id) === 'start'" :disabled="projectActionDisabled(project, 'start')" :label="`启动项目 ${project.name}`" @click="performProjectAction(project, 'start')" />
            </ElTooltip>
            <ElTooltip :trigger-keys="[]" :disabled="Boolean(actionPending || projectActionPending || projectDeletePending)" :append-to="tooltipTarget" content="停止项目" placement="top">
              <ListIconButton :icon="Square" tone="danger" :loading="projectActionFor(project.id) === 'stop'" :disabled="projectActionDisabled(project, 'stop')" :label="`停止项目 ${project.name}`" @click="performProjectAction(project, 'stop')" />
            </ElTooltip>
            <ElTooltip :trigger-keys="[]" :disabled="Boolean(actionPending || projectActionPending || projectDeletePending)" :append-to="tooltipTarget" content="重启项目" placement="top">
              <ListIconButton :icon="RotateCcw" :loading="projectActionFor(project.id) === 'restart'" :disabled="projectActionDisabled(project, 'restart')" :label="`重启项目 ${project.name}`" @click="performProjectAction(project, 'restart')" />
            </ElTooltip>
            <ElTooltip :trigger-keys="[]" :append-to="tooltipTarget" :content="projectDeleteDisabledReason(project) || '删除已停止项目；保留镜像、卷和配置文件'" placement="top">
              <span class="project-action-tooltip-target">
                <ListIconButton :icon="Trash2" tone="danger" :loading="projectDeletePending === project.id" :disabled="Boolean(projectDeleteDisabledReason(project))" :label="`删除项目 ${project.name}`" @click="confirmDeleteProject(project)" />
              </span>
            </ElTooltip>
          </div>
          <div v-if="projectErrorsFor(project.id).length" class="project-row-feedback" role="alert">
            <strong>项目操作需核对</strong>
            <ul><li v-for="failure in projectErrorsFor(project.id)" :key="failure.containerId">{{ failure.name }}：{{ failure.message }}</li></ul>
          </div>
        </div>
        <div v-if="!projects.length" class="table-empty">没有匹配的 Docker 项目。</div>
        <footer v-else class="resource-pagination">
          <ListPageSizeControl list-key="docker.projects" />
          <ResourcePagination v-model:page="projectPage" :page-count="projectPageCount" />
        </footer>
      </section>

      <section v-if="activeView === 'projects'" class="docker-mobile-list" aria-label="Docker 项目列表">
        <article v-for="project in pagedProjects" :key="project.id" class="mobile-project panel">
          <header>
            <button class="project-name project-identity" type="button" :aria-label="`查看 Docker 项目 ${project.name}`" aria-haspopup="dialog" @click="updateSelectedProject(project.id)">
              <span><Boxes :size="18" /></span>
              <div><strong>{{ project.name }}</strong><small>{{ project.kind === 'compose' ? `Compose · ${project.configFiles.length || 1} 个配置文件` : '独立容器组' }}</small></div>
            </button>
            <StatusPill :label="projectStateLabel(project)" :tone="projectStateTone(project)" />
          </header>
          <dl>
            <div><dt>容器</dt><dd>{{ project.runningCount }}/{{ project.containerCount }} 运行</dd></div>
            <div><dt>公开端口</dt><dd>{{ portLabelsFor(project.id).join('、') || '无' }}</dd></div>
            <div><dt>工作目录</dt><dd :title="project.workingDirectory">{{ project.workingDirectory || '自动发现' }}</dd></div>
          </dl>
          <div class="mobile-project-actions" @click.stop>
            <ActionButton variant="ghost" size="sm" :icon="Play" :loading="projectActionFor(project.id) === 'start'" :disabled="projectActionDisabled(project, 'start')" @click="performProjectAction(project, 'start')">启动</ActionButton>
            <ActionButton variant="danger" size="sm" :icon="Square" :loading="projectActionFor(project.id) === 'stop'" :disabled="projectActionDisabled(project, 'stop')" @click="performProjectAction(project, 'stop')">停止</ActionButton>
            <ActionButton variant="secondary" size="sm" :icon="RotateCcw" :loading="projectActionFor(project.id) === 'restart'" :disabled="projectActionDisabled(project, 'restart')" @click="performProjectAction(project, 'restart')">重启</ActionButton>
            <ElTooltip :trigger-keys="[]" :append-to="tooltipTarget" :content="projectDeleteDisabledReason(project) || '删除已停止项目；保留镜像、卷和配置文件'">
              <span class="project-action-tooltip-target project-action-tooltip-target--mobile">
                <ActionButton variant="danger" size="sm" :icon="Trash2" :loading="projectDeletePending === project.id" :disabled="Boolean(projectDeleteDisabledReason(project))" @click="confirmDeleteProject(project)">删除</ActionButton>
              </span>
            </ElTooltip>
          </div>
          <div v-if="projectErrorsFor(project.id).length" class="project-action-errors" role="alert">
            <strong>项目操作需核对</strong>
            <ul><li v-for="failure in projectErrorsFor(project.id)" :key="failure.containerId">{{ failure.name }}：{{ failure.message }}</li></ul>
          </div>
        </article>
        <p v-if="!projects.length" class="table-empty panel">没有匹配的 Docker 项目。</p>
        <footer v-else class="resource-pagination panel">
          <ListPageSizeControl list-key="docker.projects" />
          <ResourcePagination v-model:page="projectPage" :page-count="projectPageCount" />
        </footer>
      </section>

      <DockerContainerPanel
        :tooltip-append-to="tooltipTarget"
        v-else
        :containers="inventory?.containers ?? []"
        :query="query"
        :state-filter="containerStateFilter"
        :action-pending="effectiveActionPending"
        :blocked-container-ids="blockedContainerIds"
        :mutations-blocked="mutationFence.otherMutationsBlocked.value"
        :page-size="containerPageSize"
        @action="performAction"
        @details="openContainerDetails"
      />
    </template>

    <ProjectDetailDrawer
      v-model="detailOpen"
      :project="selectedProject"
      :containers="selectedContainers"
      :host-name="hostName"
      :verified-web-urls="verifiedWebUrlsFor(selectedProject?.id ?? '')"
      :allow-operations="true"
      :action-pending="effectiveActionPending"
      :blocked-container-ids="blockedContainerIds"
      :mutations-blocked="mutationFence.otherMutationsBlocked.value"
      :project-action-pending="projectActionFor(selectedProject?.id ?? '')"
      :project-mutations-blocked="selectedProject?.kind === 'standalone' ? projectJobs.blocked.value : mutationFence.otherMutationsBlocked.value"
      :project-action-errors="projectErrorsFor(selectedProject?.id ?? '')"
      :container-action-error="selectedProjectContainerActionError"
      @action="performDetailAction"
      @details="openContainerDetails"
      @compose="composeEditorOpen = true"
      @project-action="performSelectedProjectAction"
    />
    <ComposeEditorDrawer v-model="composeEditorOpen" :project="selectedProject" />

    <ContainerLogDrawer v-model="logOpen" :container-name="logContainerName" :loading="logLoading" :details="containerDetails" :logs="logs" :error="containerDrawerError" />
  </div>
</template>

<style scoped>
/* MessageBox 传送到页面根部；每条规则仅限容器或 Compose 专属类。 */
:global(.ncp-container-action-confirmation .el-message-box__message) { overflow-wrap: anywhere; white-space: pre-line; max-height: min(50vh, 420px); overflow-y: auto; }
:global(.ncp-container-action-confirmation .el-message-box__message:focus-visible),
:global(.ncp-container-action-confirmation .ncp-project-action-scope:focus-visible) { outline: 2px solid var(--ncp-primary); outline-offset: -2px; }
:global(.ncp-container-action-confirmation .el-message-box__header),
:global(.ncp-compose-action-confirmation .el-message-box__header) { min-height: var(--ncp-touch-target); padding-right: calc(var(--ncp-touch-target) + 8px); }
:global(.ncp-container-action-confirmation .el-message-box__headerbtn),
:global(.ncp-compose-action-confirmation .el-message-box__headerbtn) { top: 4px; right: 4px; width: var(--ncp-touch-target); height: var(--ncp-touch-target); border-radius: 4px; }
:global(.ncp-container-action-confirmation .el-message-box__btns .el-button),
:global(.ncp-compose-action-confirmation .el-message-box__btns .el-button) { min-width: var(--ncp-touch-target); min-height: var(--ncp-touch-target); }
:global(.ncp-container-action-confirmation .el-message-box__headerbtn:focus-visible),
:global(.ncp-compose-action-confirmation .el-message-box__headerbtn:focus-visible),
:global(.ncp-container-action-confirmation .el-message-box__btns .el-button:focus-visible),
:global(.ncp-compose-action-confirmation .el-message-box__btns .el-button:focus-visible) { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }

.docker-search { width: min(360px, 38vw); }
.docker-view-tabs { display: flex; gap: 3px; padding: 3px; border: 1px solid var(--ncp-line); border-radius: 10px; background: #fff; }
.docker-view-tabs button { display: flex; min-height: 38px; align-items: center; gap: 6px; padding: 0 13px; border-radius: 7px; background: transparent; color: var(--ncp-text-muted); font-size: .82rem; font-weight: 720; white-space: nowrap; }
.docker-view-tabs button.active { background: var(--ncp-primary); box-shadow: 0 5px 14px rgba(52,116,212,.18); color: #fff; }
.state-filter { display: flex; flex: 0 0 auto; gap: 3px; padding: 3px; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.state-filter button { min-height: 36px; padding: 0 12px; border-radius: 7px; background: transparent; color: var(--ncp-text-muted); font-size: .8rem; font-weight: 700; }
.state-filter button.active { background: #fff; box-shadow: 0 2px 8px rgba(28,45,75,.08); color: var(--ncp-primary-strong); }
.action-error { display: flex; gap: 8px; min-height: 44px; align-items: center; justify-content: space-between; padding: 9px 13px; border: 1px solid rgba(212,81,93,.2); border-radius: 10px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .7rem; }
.action-error button { flex: 0 0 auto; min-width: 44px; min-height: 44px; white-space: nowrap; padding: 0 10px; border-radius: 8px; background: rgba(255,255,255,.65); color: inherit; font-weight: 700; }
.resource-state { display: grid; min-height: 220px; place-items: center; align-content: center; gap: 8px; padding: 28px; color: var(--ncp-text-subtle); text-align: center; }
.resource-state strong { color: var(--ncp-text); font-size: .9rem; }
.resource-state span { font-size: .75rem; }
.resource-state--error { border-color: var(--ncp-danger-border); color: var(--ncp-danger-strong); }
.resource-state--error strong { color: var(--ncp-danger-strong); }
.inventory-recovery { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 18px; border-color: var(--ncp-warning-border); background: var(--ncp-warning-soft); }
.inventory-recovery>div { display: grid; flex: 1 1 240px; min-width: 0; gap: 4px; }
.inventory-recovery strong { color: var(--ncp-text); font-size: .85rem; }
.inventory-recovery span { color: var(--ncp-text-muted); font-size: .78rem; overflow-wrap: anywhere; }
.docker-table { overflow: hidden; border-color:rgba(203,214,228,.9); }
.docker-table__head, .project-row { display: grid; grid-template-columns: minmax(0,1.3fr) 108px 74px minmax(80px,180px) minmax(0,1fr) 188px; align-items: center; gap: 12px; }
.docker-table__head { min-height: 46px; padding: 0 18px; background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); font-size: .82rem; font-weight: 720; }
.project-row { width: 100%; min-height: 70px; padding: 10px 18px; border-top: 1px solid var(--ncp-line); background: #fff; color: var(--ncp-text-muted); font-size: .86rem; text-align: left; transition: background-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.project-row:hover { position: relative; z-index: 1; background: var(--ncp-surface-hover); }
.docker-table__head>span:nth-child(2),.docker-table__head>span:nth-child(3),.docker-table__head>span:nth-child(4),.docker-table__head>span:nth-child(6),.project-row>:nth-child(2),.project-row>:nth-child(3),.project-row>:nth-child(4),.project-row>:nth-child(6){justify-self:center;text-align:center}
.project-name { display: flex; min-width: 0; align-items: center; gap: 9px; }
.project-name>span { display: grid; width: 36px; height: 36px; flex: 0 0 auto; place-items: center; border-radius: 10px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.project-name>div { display: grid; min-width: 0; gap: 1px; }
.project-name strong { overflow: hidden; color: var(--ncp-text); font-size: .92rem; text-overflow: ellipsis; white-space: nowrap; }
.project-name small { color: var(--ncp-text-subtle); font-size: .8rem; }
.project-identity { width: 100%; min-width: 0; padding: 0; border: 0; background: transparent; color: inherit; cursor: pointer; font: inherit; text-align: left; }
.project-identity:hover strong { color: var(--ncp-primary-strong); }
.project-identity:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 3px; border-radius: 8px; }
.mono { font-family: 'JetBrains Mono Variable', monospace; }
.port-cell { display: flex; min-width: 0; align-items: center; gap: 5px; }
.port-cell a { display: flex; min-height: 32px; align-items: center; gap: 3px; padding: 0 8px; border-radius: 7px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', monospace; font-size: .72rem; }
.port-cell>span { color: var(--ncp-text-subtle); font-size: .72rem; }
.path-cell { overflow: hidden; padding-right: 8px; color: var(--ncp-text-muted); font-family:var(--ncp-font-mono); font-size:.78rem; text-overflow: ellipsis; white-space: nowrap; }
.project-actions { display: flex; align-items: center; justify-content: flex-end; gap: 4px; }
.project-action-tooltip-target { display: inline-flex; }
.project-action-tooltip-target--mobile, .project-action-tooltip-target--mobile :deep(.action-button) { width: 100%; }
.project-row-feedback { grid-column: 1 / -1; align-self: stretch; padding: 8px 10px; border: 1px solid var(--ncp-danger-border); border-radius: 8px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .72rem; }
.project-row-feedback strong, .project-action-errors strong { font-weight: 760; }
.project-row-feedback ul, .project-action-errors ul { display: grid; gap: 3px; margin: 4px 0 0; padding-left: 17px; }
.table-empty { padding: 36px; color: var(--ncp-text-subtle); font-size: .82rem; text-align: center; }
footer.resource-pagination { display:flex; flex-wrap:wrap; min-height:52px; align-items:center; justify-content:space-between; gap:12px 16px; padding:8px 18px; border-top:1px solid var(--ncp-line); color:var(--ncp-text-subtle); font-size:.82rem; }
footer.resource-pagination > .page-size-control { flex-shrink:0; white-space:nowrap; }
footer.resource-pagination > .resource-pagination { margin-left:auto; }
footer.resource-pagination :deep(.el-select__wrapper) { min-height:44px; }
.docker-mobile-list { display: none; }
.mobile-project-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 7px; }
.mobile-project-actions :deep(.action-button) { width: 100%; }
.project-action-errors { padding: 9px 10px; border: 1px solid var(--ncp-danger-border); border-radius: 9px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .7rem; }
.spin { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media(max-width: 1120px) {
  .docker-table__head, .project-row { grid-template-columns: minmax(0,1.2fr) 100px 65px minmax(60px,150px) minmax(0,1fr) 188px; gap: 8px; }
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
  .mobile-project header .project-identity { flex: 1; }
  .mobile-project dl { display: grid; gap: 8px; margin: 0; padding: 11px; border-radius: 10px; background: var(--ncp-surface-quiet); }
  .mobile-project dl>div { display: grid; grid-template-columns: 74px minmax(0,1fr); gap: 8px; }
  .mobile-project dt { color: var(--ncp-text-subtle); font-size: max(12px, .875rem); line-height: 1.5; }
  .mobile-project dd { min-width: 0; margin: 0; color: var(--ncp-text-muted); font-size: max(12px, .875rem); line-height: 1.5; overflow-wrap: anywhere; white-space: normal; }
}
</style>
