<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ArrowUpRight,
  Boxes,
  CalendarClock,
  CircleStop,
  Code2,
  FileCode2,
  Info,
  Folder,
  Image,
  LoaderCircle,
  Play,
  RotateCw,
} from '@lucide/vue'
import { ElDrawer, ElTooltip } from 'element-plus'

import ActionButton from '@/components/ActionButton.vue'
import StatusPill from '@/components/StatusPill.vue'
import { dockerContainerStateDetail, dockerContainerStateLabel, dockerContainerTimingLabel } from '@/domain/docker'
import { projectStateTone } from '@/domain/overview'
import type { ContainerAction, DockerInventory, DockerProject } from '@/api/system'
import { presentDockerPorts } from '@/domain/dockerPorts'

type DockerContainer = DockerInventory['containers'][number]
type ProjectActionError = { containerId: string; name: string; message: string; scope?: 'project' | 'container' }

const props = withDefaults(defineProps<{
  modelValue: boolean
  project: DockerProject | null
  containers: DockerContainer[]
  hostName: string
  verifiedWebUrls?: Record<string, string>
  allowOperations?: boolean
  actionPending?: string | null
  projectActionPending?: ContainerAction | null
  projectActionErrors?: ProjectActionError[]
  containerActionError?: string
}>(), {
  allowOperations: false,
  actionPending: null,
  projectActionPending: null,
  projectActionErrors: () => [],
  containerActionError: '',
  verifiedWebUrls: () => ({}),
})

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  action: [containerId: string, action: ContainerAction]
  details: [container: { id: string; name: string }]
  compose: []
  'project-action': [action: ContainerAction]
}>()

const isMobile = ref(window.innerWidth < 768)
const drawerSize = computed(() => isMobile.value ? '100%' : 'min(720px, 94vw)')
const publicPorts = computed(() => presentDockerPorts(
  props.containers.flatMap((container) => container.ports),
  { webUrls: props.verifiedWebUrls },
))

function stateLabel(state: DockerProject['state']) {
  return state === 'running' ? '运行中' : state === 'degraded' ? '需关注' : '已停止'
}
function actionLabel(action: ContainerAction) {
  return action === 'start' ? '启动' : action === 'stop' ? '停止' : '重启'
}
const currentStateLabel = computed(() => {
  if (props.projectActionPending) return `正在${actionLabel(props.projectActionPending)}`
  return props.project ? stateLabel(props.project.state) : ''
})
const currentStateTone = computed(() => props.projectActionPending ? 'pending' : props.project ? projectStateTone(props.project.state) : 'neutral')
const projectActionErrorTitle = computed(() => props.projectActionErrors.some((failure) => failure.scope === 'project') ? '项目操作失败' : '部分容器操作失败')
function projectActionDisabled(action: ContainerAction) {
  if (!props.project || props.projectActionPending || props.actionPending || !props.containers.length) return true
  const runningCount = props.containers.filter((container) => container.state === 'running').length
  if (action === 'start') return runningCount === props.containers.length
  return runningCount === 0
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value || '—' : new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

function pendingFor(containerId: string, action: ContainerAction) {
  return props.actionPending === `${containerId}:${action}`
}

function updateViewport() {
  isMobile.value = window.innerWidth < 768
}

onMounted(() => window.addEventListener('resize', updateViewport, { passive: true }))
onBeforeUnmount(() => window.removeEventListener('resize', updateViewport))
</script>

<template>
  <ElDrawer
    :model-value="modelValue"
    :size="drawerSize"
    :show-close="true"
    class="project-drawer"
    modal-class="project-drawer-modal"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div v-if="project" class="drawer-title">
        <span class="drawer-title__icon"><Boxes :size="21" /></span>
        <div><strong>{{ project.name }}</strong><span>{{ project.kind === 'compose' ? 'Compose 项目' : '独立容器组' }}</span></div>
        <StatusPill :label="currentStateLabel" :tone="currentStateTone" />
      </div>
    </template>

    <div v-if="project" class="project-detail">
      <section class="detail-overview">
        <div><Folder :size="17" /><span>工作目录</span><strong :title="project.workingDirectory">{{ project.workingDirectory || 'Docker 自动发现项目' }}</strong></div>
        <div><FileCode2 :size="17" /><span>Compose 配置</span><strong :title="project.configFiles.join('、')">{{ project.configFiles.length ? `${project.configFiles.length} 个配置文件` : '等待定位配置' }}</strong></div>
        <div><Boxes :size="17" /><span>容器状态</span><strong>{{ project.runningCount }}/{{ project.containerCount }} 正在运行</strong></div>
      </section>
      <button v-if="project.kind === 'compose' && project.configFiles.length" class="compose-open-button" type="button" @click="emit('compose')">
        <Code2 :size="17" /><span><strong>打开 Compose 配置工作台</strong><small>读取真实 YAML、切换配置文件并执行校验</small></span><ArrowUpRight :size="16" />
      </button>

      <section v-if="allowOperations" class="project-lifecycle" aria-label="Docker 项目操作">
        <header>
          <div><h3>项目操作</h3><p>按容器逐项执行，失败项会保留在这里。</p></div>
          <span>{{ containers.length }} 个容器</span>
        </header>
        <div class="project-lifecycle__actions">
          <ActionButton variant="ghost" size="sm" :icon="Play" :loading="projectActionPending === 'start'" :disabled="projectActionDisabled('start')" @click="emit('project-action', 'start')">启动</ActionButton>
          <ActionButton variant="danger" size="sm" :icon="CircleStop" :loading="projectActionPending === 'stop'" :disabled="projectActionDisabled('stop')" @click="emit('project-action', 'stop')">停止</ActionButton>
          <ActionButton variant="secondary" size="sm" :icon="RotateCw" :loading="projectActionPending === 'restart'" :disabled="projectActionDisabled('restart')" @click="emit('project-action', 'restart')">重启</ActionButton>
        </div>
        <div v-if="projectActionErrors.length" class="project-action-errors" role="alert">
          <strong>{{ projectActionErrorTitle }}</strong>
          <ul><li v-for="failure in projectActionErrors" :key="failure.containerId">{{ failure.name }}：{{ failure.message }}</li></ul>
        </div>
      </section>

      <section class="detail-section">
        <header><div><h3>访问入口</h3><p>在新的浏览器标签页打开服务</p></div><span>{{ publicPorts.length }} 个端口</span></header>
        <div v-if="publicPorts.length" class="port-links">
          <ElTooltip v-for="port in publicPorts" :key="port.key" :content="port.webUrl ? '打开已验证站点入口' : '端口信息（仅复制使用）'">
            <a v-if="port.webUrl" :href="port.webUrl" target="_blank" rel="noreferrer">
              <span>{{ port.label }}</span><ArrowUpRight :size="15" />
            </a>
            <span v-else class="port-text"><span>{{ port.label }}</span></span>
          </ElTooltip>
        </div>
        <p v-else class="detail-empty">此项目没有对局域网公开端口。</p>
      </section>

      <section class="detail-section">
        <header><div><h3>容器</h3><p>镜像、状态、端口和运行操作</p></div><span>{{ containers.length }} 个</span></header>
        <div v-if="containerActionError" class="project-action-errors" role="alert">
          <strong>容器操作失败</strong>
          <p>{{ containerActionError }}</p>
        </div>
        <div class="container-cards">
          <article v-for="container in containers" :key="container.id" class="container-card">
            <div class="container-card__top">
              <div class="container-card__name">
                <span><Boxes :size="17" /></span>
                <div><strong>{{ container.name }}</strong><small>{{ dockerContainerTimingLabel(container) }}</small></div>
              </div>
              <span :class="['container-card__state', { 'container-card__state--running': container.state === 'running' }]">
                {{ dockerContainerStateLabel(container.state) }} · {{ dockerContainerStateDetail(container) }}
              </span>
            </div>
            <dl>
              <div><dt><Image :size="14" />镜像</dt><dd :title="container.image">{{ container.image }}</dd></div>
              <div><dt><CalendarClock :size="14" />创建时间</dt><dd>{{ formatTime(container.createdAt) }}</dd></div>
              <div><dt><ArrowUpRight :size="14" />端口</dt><dd>{{ presentDockerPorts(container.ports).map((port) => port.label).join('、') || '无公开端口' }}</dd></div>
            </dl>
            <div v-if="allowOperations" class="container-card__actions">
              <ElTooltip :content="container.state === 'running' ? '停止容器' : '启动容器'">
                <button
                  :class="{ 'operation-button--danger': container.state === 'running' }"
                  class="operation-button"
                  type="button"
                  :disabled="Boolean(actionPending)"
                  :aria-label="container.state === 'running' ? `停止 ${container.name}` : `启动 ${container.name}`"
                  @click="emit('action', container.id, container.state === 'running' ? 'stop' : 'start')"
                >
                  <LoaderCircle v-if="pendingFor(container.id, container.state === 'running' ? 'stop' : 'start')" class="spin" :size="17" />
                  <CircleStop v-else-if="container.state === 'running'" :size="17" />
                  <Play v-else :size="17" />
                </button>
              </ElTooltip>
              <ElTooltip content="重启容器">
                <button class="operation-button" type="button" :disabled="Boolean(actionPending)" :aria-label="`重启 ${container.name}`" @click="emit('action', container.id, 'restart')">
                  <LoaderCircle v-if="pendingFor(container.id, 'restart')" class="spin" :size="17" /><RotateCw v-else :size="17" />
                </button>
              </ElTooltip>
              <ElTooltip content="查看容器详情与日志">
                <button class="operation-button" type="button" :aria-label="`查看 ${container.name} 详情与日志`" @click="emit('details', container)">
                  <Info :size="17" />
                </button>
              </ElTooltip>
            </div>
          </article>
          <p v-if="!containers.length" class="detail-empty">当前项目没有容器。</p>
        </div>
      </section>
    </div>
  </ElDrawer>
</template>

<style scoped>
.drawer-title { display: grid; grid-template-columns: auto minmax(0,1fr) auto; align-items: center; gap: 11px; min-width: 0; padding-right: 10px; }
.drawer-title__icon { display: grid; width: 40px; height: 40px; place-items: center; border-radius: 11px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.drawer-title>div { display: grid; min-width: 0; }
.drawer-title strong { overflow: hidden; color: var(--ncp-text); font-size: .94rem; text-overflow: ellipsis; white-space: nowrap; }
.drawer-title span:not(.drawer-title__icon) { color: var(--ncp-text-subtle); font-size: .72rem; }
.project-detail { display: grid; gap: 18px; padding-bottom: calc(20px + env(safe-area-inset-bottom)); }
.detail-overview { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.detail-overview>div { display: grid; grid-template-columns: auto minmax(0,1fr); gap: 2px 8px; padding: 14px; border: 1px solid var(--ncp-line); border-radius: 12px; background: var(--ncp-surface-quiet); }
.detail-overview svg { grid-row: 1/3; color: var(--ncp-primary-strong); }
.detail-overview span { color: var(--ncp-text-subtle); font-size: .7rem; }
.detail-overview strong { overflow: hidden; font-size: .75rem; text-overflow: ellipsis; white-space: nowrap; }
.compose-open-button{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:10px;min-height:54px;padding:9px 13px;border:1px solid rgba(36,104,216,.18);border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);text-align:left;transition:transform var(--ncp-duration-fast),border-color var(--ncp-duration-fast)}.compose-open-button:hover{border-color:rgba(36,104,216,.38);transform:translateY(-1px)}.compose-open-button>span{display:grid;gap:2px}.compose-open-button strong{font-size:.78rem}.compose-open-button small{color:var(--ncp-text-muted);font-size:.68rem}
.project-lifecycle { display: grid; gap: 11px; padding: 13px; border: 1px solid var(--ncp-line); border-radius: 12px; background: var(--ncp-surface-quiet); }
.project-lifecycle>header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.project-lifecycle>header>div { display: grid; gap: 2px; }
.project-lifecycle h3 { margin: 0; font-size: .86rem; }
.project-lifecycle p { margin: 0; color: var(--ncp-text-subtle); font-size: .7rem; }
.project-lifecycle>header>span { color: var(--ncp-text-subtle); font-size: .7rem; }
.project-lifecycle__actions { display: flex; flex-wrap: wrap; gap: 7px; }
.project-action-errors { padding: 9px 10px; border: 1px solid var(--ncp-danger-border); border-radius: 9px; background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); font-size: .7rem; }
.project-action-errors ul { display: grid; gap: 3px; margin: 4px 0 0; padding-left: 17px; }
.detail-section { display: grid; gap: 12px; }
.detail-section>header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.detail-section h3 { margin: 0; font-size: .88rem; }
.detail-section p { margin: 3px 0 0; color: var(--ncp-text-subtle); font-size: .72rem; }
.detail-section>header>span { padding: 4px 8px; border-radius: 99px; background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); font-size: .7rem; font-weight: 700; }
.port-links { display: flex; flex-wrap: wrap; gap: 8px; }
.port-links .port-text { display: flex; min-height: 44px; align-items: center; padding: 0 14px; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); font-family: 'JetBrains Mono Variable', monospace; font-size: .72rem; }
.port-links a { display: flex; min-height: 44px; align-items: center; gap: 7px; padding: 0 14px; border: 1px solid rgba(36,104,216,.16); border-radius: 10px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', monospace; font-size: .72rem; font-weight: 750; transition: background-color var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.port-links a:hover { background: var(--ncp-primary-hover); transform: translateY(-1px); }
.container-cards { display: grid; gap: 10px; }
.container-card { padding: 14px; border: 1px solid var(--ncp-line); border-radius: 13px; background: #fff; }
.container-card__top, .container-card__name, .container-card__actions { display: flex; align-items: center; }
.container-card__top { justify-content: space-between; gap: 12px; }
.container-card__name { min-width: 0; gap: 9px; }
.container-card__name>span { display: grid; width: 34px; height: 34px; flex: 0 0 auto; place-items: center; border-radius: 9px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.container-card__name>div { display: grid; min-width: 0; }
.container-card__name strong { overflow: hidden; font-size: .78rem; text-overflow: ellipsis; white-space: nowrap; }
.container-card__name small { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', monospace; font-size: .68rem; }
.container-card__state { flex: 0 0 auto; color: var(--ncp-text-muted); font-size: .7rem; font-weight: 700; }
.container-card__state--running { color: var(--ncp-success); }
.container-card dl { display: grid; gap: 7px; margin: 13px 0 0; padding: 11px 0 0; border-top: 1px solid var(--ncp-line); }
.container-card dl>div { display: grid; grid-template-columns: 92px minmax(0,1fr); gap: 10px; }
.container-card dt { display: flex; align-items: center; gap: 5px; color: var(--ncp-text-subtle); font-size: .7rem; }
.container-card dd { overflow: hidden; margin: 0; color: var(--ncp-text-muted); font-size: .72rem; text-overflow: ellipsis; white-space: nowrap; }
.container-card__actions { justify-content: flex-end; gap: 8px; margin-top: 12px; }
.operation-button { display: grid; width: 44px; height: 44px; place-items: center; border: 1px solid rgba(36,104,216,.18); border-radius: 10px; background: #fff; color: var(--ncp-primary-strong); transition: background-color var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.operation-button:hover:not(:disabled) { background: var(--ncp-primary-soft); transform: translateY(-1px); }
.operation-button--danger { border-color: rgba(212,81,93,.2); color: var(--ncp-danger-strong); }
.operation-button:disabled { cursor: wait; opacity: .5; }
.detail-empty { padding: 22px; border: 1px dashed var(--ncp-line-strong); border-radius: 12px; text-align: center; }
.spin { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media(max-width: 560px) {
  .detail-overview { grid-template-columns: 1fr; }
  .drawer-title { grid-template-columns: auto minmax(0,1fr); }
  .drawer-title>:last-child { grid-column: 2; justify-self: start; }
  .container-card { padding: 13px; }
  .container-card dl>div { grid-template-columns: 78px minmax(0,1fr); }
}
@media(max-width: 900px) and (min-width: 561px) {
  .detail-overview { grid-template-columns: 1fr 1fr; }
  .detail-overview>div:last-child { grid-column: 1 / -1; }
}
</style>

<style>
.project-drawer .el-drawer__header { margin-bottom: 0; padding: 18px 20px 14px; border-bottom: 1px solid var(--ncp-line); }
.project-drawer .el-drawer__body { padding: 18px 20px; }
@media(max-width: 767px) {
  .project-drawer .el-drawer__header { padding-top: calc(14px + env(safe-area-inset-top)); }
  .project-drawer .el-drawer__body { padding: 16px 14px; }
}
</style>
