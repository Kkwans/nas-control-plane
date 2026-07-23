<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronDown, ChevronRight, ExternalLink, FileText, LoaderCircle, Play, RotateCw, Search, Square } from '@lucide/vue'
import { ElDrawer, ElInput, ElTooltip } from 'element-plus'

import { NcpApiError, requestContainerAction, requestContainerLogs, type ContainerAction, type ContainerLogsResult } from '@/api/system'
import StatusPill from '@/components/StatusPill.vue'
import { projectStateTone } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const hostName = window.location.hostname
const query = ref('')
const expanded = ref(new Set<string>())
const actionPending = ref<string | null>(null)
const actionError = ref<string | null>(null)
const logOpen = ref(false)
const logLoading = ref(false)
const logContainerName = ref('')
const logs = ref<ContainerLogsResult | null>(null)

const projects = computed(() => {
  const term = query.value.trim().toLowerCase()
  return systemStore.services.filter((project) => !term || project.name.toLowerCase().includes(term))
})
const inventory = computed(() => systemStore.inventory)

function containersFor(projectId: string) {
  return inventory.value?.containers.filter((container) => container.projectId === projectId) ?? []
}
function portsFor(projectId: string) {
  return [...new Set(containersFor(projectId).flatMap((container) => container.ports).filter((port) => port.publicPort > 0).map((port) => port.publicPort))]
}
function toggleProject(projectId: string) {
  const next = new Set(expanded.value)
  next.has(projectId) ? next.delete(projectId) : next.add(projectId)
  expanded.value = next
}
function stateLabel(state: 'running' | 'stopped' | 'degraded') {
  return state === 'running' ? '运行中' : state === 'degraded' ? '需关注' : '已停止'
}
function actionPendingFor(containerId: string, action: ContainerAction) {
  return actionPending.value === `${containerId}:${action}`
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
</script>

<template>
  <div class="page docker-page">
    <header class="page-toolbar">
      <div><h1>Docker 管理</h1><p>项目、容器、端口和日志</p></div>
      <ElInput v-model="query" class="docker-search" clearable placeholder="搜索项目" aria-label="搜索 Docker 项目">
        <template #prefix><Search :size="16" /></template>
      </ElInput>
    </header>

    <section class="docker-summary panel">
      <div><span>Docker 版本</span><strong>{{ inventory?.engine.serverVersion || '—' }}</strong></div>
      <div><span>运行容器</span><strong>{{ inventory?.engine.containersRunning ?? '—' }}</strong></div>
      <div><span>停止容器</span><strong>{{ inventory?.engine.containersStopped ?? '—' }}</strong></div>
      <div><span>本地镜像</span><strong>{{ inventory?.engine.images ?? '—' }}</strong></div>
      <div><span>项目数量</span><strong>{{ projects.length }}</strong></div>
    </section>

    <div v-if="actionError" class="action-error" role="alert"><span>{{ actionError }}</span><button type="button" @click="actionError = null">关闭</button></div>

    <section class="docker-table panel" aria-label="Docker 项目列表">
      <div class="docker-table__head">
        <span>项目</span><span>状态</span><span>容器</span><span>公开端口</span><span>工作目录</span><span>操作</span>
      </div>
      <template v-for="project in projects" :key="project.id">
        <div class="project-row" @click="toggleProject(project.id)">
          <button class="expand-button" type="button" :aria-label="expanded.has(project.id) ? `收起 ${project.name}` : `展开 ${project.name}`">
            <ChevronDown v-if="expanded.has(project.id)" :size="17" /><ChevronRight v-else :size="17" />
          </button>
          <div class="project-name"><strong>{{ project.name }}</strong><small>{{ project.kind === 'compose' ? 'Compose 项目' : '独立容器组' }}</small></div>
          <StatusPill :label="stateLabel(project.state)" :tone="projectStateTone(project.state)" />
          <span class="mono">{{ project.runningCount }}/{{ project.containerCount }}</span>
          <div class="port-cell">
            <ElTooltip v-for="port in portsFor(project.id).slice(0, 3)" :key="port" :content="`打开端口 ${port}`">
              <a :href="`http://${hostName}:${port}`" target="_blank" rel="noreferrer" @click.stop>{{ port }}<ExternalLink :size="12" /></a>
            </ElTooltip>
            <span v-if="!portsFor(project.id).length">无公开端口</span>
          </div>
          <ElTooltip :content="project.workingDirectory || 'Docker 自动发现项目'" placement="top">
            <span class="path-cell">{{ project.workingDirectory || '自动发现' }}</span>
          </ElTooltip>
          <button class="detail-button" type="button" @click.stop="toggleProject(project.id)">容器详情</button>
        </div>

        <div v-if="expanded.has(project.id)" class="container-detail">
          <div class="container-detail__head"><span>容器</span><span>镜像</span><span>状态</span><span>端口</span><span>操作</span></div>
          <div v-for="container in containersFor(project.id)" :key="container.id" class="container-row">
            <div><strong>{{ container.name }}</strong><small>{{ container.id.slice(0, 12) }}</small></div>
            <span class="image-cell">{{ container.image }}</span>
            <span :class="['container-state', { 'container-state--running': container.state === 'running' }]">{{ container.state === 'running' ? '运行中' : container.state }}</span>
            <span class="mono">{{ container.ports.filter((port) => port.publicPort > 0).map((port) => port.publicPort).join('、') || '—' }}</span>
            <div class="container-actions">
              <ElTooltip :content="container.state === 'running' ? '停止容器' : '启动容器'">
                <button :class="{ 'action-button--danger': container.state === 'running' }" class="action-button" type="button" :disabled="Boolean(actionPending)" @click="performAction(container.id, container.state === 'running' ? 'stop' : 'start')">
                  <LoaderCircle v-if="actionPendingFor(container.id, container.state === 'running' ? 'stop' : 'start')" class="spin" :size="16" />
                  <Square v-else-if="container.state === 'running'" :size="16" /><Play v-else :size="16" />
                </button>
              </ElTooltip>
              <ElTooltip content="重启容器">
                <button class="action-button" type="button" :disabled="Boolean(actionPending)" @click="performAction(container.id, 'restart')">
                  <LoaderCircle v-if="actionPendingFor(container.id, 'restart')" class="spin" :size="16" /><RotateCw v-else :size="16" />
                </button>
              </ElTooltip>
              <ElTooltip content="查看日志">
                <button class="action-button" type="button" @click="openLogs(container)"><FileText :size="16" /></button>
              </ElTooltip>
            </div>
          </div>
          <p v-if="!containersFor(project.id).length" class="no-containers">当前项目没有容器。</p>
        </div>
      </template>
      <div v-if="!projects.length" class="table-empty">没有匹配的 Docker 项目。</div>
    </section>

    <ElDrawer v-model="logOpen" :title="`${logContainerName} · 容器日志`" size="min(760px, 92vw)">
      <div v-if="logLoading" class="log-loading"><LoaderCircle class="spin" :size="18" />正在读取日志</div>
      <ol v-else-if="logs?.entries.length" class="log-list">
        <li v-for="(entry, index) in logs.entries" :key="index"><span :class="`log-stream--${entry.stream}`">{{ entry.stream }}</span><code>{{ entry.message }}</code></li>
      </ol>
      <p v-else class="table-empty">当前没有可显示的日志。</p>
    </ElDrawer>
  </div>
</template>

<style scoped>
.docker-search{width:min(320px,42vw)}.docker-summary{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));margin-bottom:12px}.docker-summary div{display:grid;gap:4px;padding:15px 18px;border-right:1px solid var(--ncp-line)}.docker-summary div:last-child{border-right:0}.docker-summary span{color:var(--ncp-text-subtle);font-size:.64rem}.docker-summary strong{font-family:'JetBrains Mono Variable',monospace;font-size:1rem}
.action-error{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;padding:11px 14px;border:1px solid rgba(212,81,93,.2);border-radius:9px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.72rem}.action-error button{background:transparent;color:inherit;font-weight:700}
.docker-table{overflow:hidden}.docker-table__head,.project-row{display:grid;grid-template-columns:minmax(220px,1.3fr) 110px 78px 180px minmax(180px,1fr) 96px;align-items:center}.docker-table__head{min-height:42px;padding:0 18px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.67rem;font-weight:700}.project-row{position:relative;min-height:66px;padding:0 18px;border-top:1px solid var(--ncp-line);font-size:.72rem;cursor:pointer;transition:background-color var(--ncp-duration-fast)}.project-row:hover{background:#fbfcff}.expand-button{position:absolute;left:5px;display:grid;width:32px;height:40px;place-items:center;background:transparent;color:var(--ncp-text-subtle)}.project-name{display:grid;gap:2px;padding-left:18px;min-width:0}.project-name strong{overflow:hidden;font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.project-name small{color:var(--ncp-text-subtle);font-size:.61rem}.mono{font-family:'JetBrains Mono Variable',monospace}.port-cell{display:flex;align-items:center;gap:5px;min-width:0}.port-cell a{display:flex;min-height:30px;align-items:center;gap:3px;padding:0 7px;border-radius:6px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-family:'JetBrains Mono Variable',monospace;font-size:.62rem}.port-cell>span{color:var(--ncp-text-subtle);font-size:.64rem}.path-cell{overflow:hidden;padding-right:16px;color:var(--ncp-text-subtle);text-overflow:ellipsis;white-space:nowrap}.detail-button{min-height:36px;border:1px solid var(--ncp-line);border-radius:7px;background:#fff;color:var(--ncp-text-muted);font-size:.66rem;font-weight:700}
.container-detail{padding:8px 18px 14px 54px;border-top:1px solid var(--ncp-line);background:#f9fbfd}.container-detail__head,.container-row{display:grid;grid-template-columns:minmax(180px,1fr) minmax(220px,1.2fr) 90px 110px 150px;align-items:center;gap:12px}.container-detail__head{min-height:34px;color:var(--ncp-text-subtle);font-size:.61rem;font-weight:700}.container-row{min-height:58px;border-top:1px solid var(--ncp-line);font-size:.68rem}.container-row>div:first-child{display:grid;gap:2px}.container-row small{color:var(--ncp-text-subtle);font-family:'JetBrains Mono Variable',monospace}.image-cell{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.container-state{color:var(--ncp-text-muted)}.container-state--running{color:#148265;font-weight:700}.container-actions{display:flex;gap:7px}.action-button{display:grid;width:40px;height:40px;place-items:center;border:1px solid rgba(44,111,223,.2);border-radius:8px;background:#fff;color:var(--ncp-primary-strong);transition:background-color var(--ncp-duration-fast)}.action-button:hover:not(:disabled){background:var(--ncp-primary-soft)}.action-button--danger{border-color:rgba(212,81,93,.2);color:var(--ncp-danger-strong)}.action-button:disabled{cursor:wait;opacity:.5}.no-containers,.table-empty{padding:24px;color:var(--ncp-text-subtle);font-size:.72rem;text-align:center}.log-loading{display:flex;align-items:center;gap:8px;color:var(--ncp-text-muted);font-size:.75rem}.log-list{display:grid;gap:4px;padding:0;margin:0;list-style:none}.log-list li{display:grid;grid-template-columns:48px minmax(0,1fr);gap:9px;padding:5px 0;border-bottom:1px solid var(--ncp-line);font-size:.67rem;line-height:1.5}.log-list code{overflow-wrap:anywhere;white-space:pre-wrap;font-family:'JetBrains Mono Variable',monospace}.log-list span{font-family:'JetBrains Mono Variable',monospace;font-size:.57rem;font-weight:700}.log-stream--stdout{color:var(--ncp-primary-strong)}.log-stream--stderr{color:var(--ncp-danger-strong)}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}
@media(max-width:1100px){.docker-table{overflow-x:auto}.docker-table__head,.project-row{min-width:1050px}.container-detail{min-width:1050px}.docker-summary{grid-template-columns:repeat(3,1fr)}.docker-summary div:nth-child(3){border-right:0}.docker-summary div:nth-child(n+4){border-top:1px solid var(--ncp-line)}}@media(max-width:650px){.docker-summary{grid-template-columns:repeat(2,1fr)}.docker-summary div{border-top:1px solid var(--ncp-line)}.docker-search{width:48vw}}
</style>
