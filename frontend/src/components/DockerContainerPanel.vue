<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Info, Play, RotateCcw, Square, Box } from '@lucide/vue'
import { ElTooltip } from 'element-plus'

import type { ContainerAction, DockerInventory } from '@/api/system'
import ListIconButton from '@/components/ListIconButton.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import StatusPill from '@/components/StatusPill.vue'
import ResourcePagination from '@/components/ResourcePagination.vue'
import { dockerContainerStateDetail, dockerContainerStateLabel, dockerContainerTimingLabel } from '@/domain/docker'
import { presentDockerPorts } from '@/domain/dockerPorts'

type DockerContainer = DockerInventory['containers'][number]
type ContainerStateFilter = 'all' | 'running' | 'stopped'

const props = defineProps<{
  containers: DockerContainer[]
  query: string
  stateFilter: ContainerStateFilter
  actionPending: string | null
  pageSize: number
}>()

const emit = defineEmits<{
  action: [containerId: string, action: ContainerAction]
  details: [container: { id: string; name: string }]
}>()

const filteredContainers = computed(() => {
  const term = props.query.trim().toLowerCase()
  return props.containers.filter((container) => {
    const running = container.state === 'running'
    const matchesState =
      props.stateFilter === 'all' ||
      (props.stateFilter === 'running' && running) ||
      (props.stateFilter === 'stopped' && !running)
    const matchesQuery =
      !term ||
      container.name.toLowerCase().includes(term) ||
      container.image.toLowerCase().includes(term) ||
      container.projectName.toLowerCase().includes(term)
    return matchesState && matchesQuery
  })
})
const page = ref(1)
const pageCount = computed(() => Math.max(1, Math.ceil(filteredContainers.value.length / props.pageSize)))
const pagedContainers = computed(() => {
  const start = (page.value - 1) * props.pageSize
  return filteredContainers.value.slice(start, start + props.pageSize)
})

watch(() => [props.query, props.stateFilter, props.pageSize], () => { page.value = 1 })
watch(pageCount, (count) => {
  if (page.value > count) page.value = count
})

function publicPorts(container: DockerContainer) {
  return presentDockerPorts(container.ports)
}

function pending(containerId: string, action: ContainerAction) {
  return props.actionPending === `${containerId}:${action}`
}

function stateTone(container: DockerContainer) {
  if (container.state === 'running') return container.health === 'unhealthy' ? 'attention' : 'healthy'
  if (container.state === 'dead') return 'attention'
  return 'pending'
}
</script>

<template>
  <section class="container-panel panel" aria-label="Docker 容器列表">
    <div class="container-table__head">
      <span>容器</span><span>状态</span><span>所属项目</span><span>镜像</span><span>公开端口</span><span>操作</span>
    </div>
    <div v-for="container in pagedContainers" :key="container.id" class="container-row">
      <div class="container-name">
        <span><Box :size="18" /></span>
        <div><strong>{{ container.name }}</strong><small :title="dockerContainerTimingLabel(container)">{{ dockerContainerTimingLabel(container) }}</small></div>
      </div>
      <div class="container-state"><StatusPill :label="dockerContainerStateLabel(container.state)" :tone="stateTone(container)" /><small class="status-detail">{{ dockerContainerStateDetail(container) }}</small></div>
      <span class="cell-ellipsis">{{ container.projectName || '独立容器' }}</span>
      <span class="cell-ellipsis mono">{{ container.image }}</span>
      <div class="port-list">
        <span v-for="port in publicPorts(container).slice(0, 3)" :key="port.key" :title="port.label">{{ port.hostPort }}</span>
        <small v-if="!publicPorts(container).length">无公开端口</small>
      </div>
      <div class="container-actions">
        <ElTooltip v-if="container.state !== 'running'" content="启动容器" placement="top">
          <ListIconButton :icon="Play" label="启动容器" :loading="pending(container.id, 'start')" :disabled="Boolean(actionPending)" @click="emit('action', container.id, 'start')" />
        </ElTooltip>
        <ElTooltip v-else content="停止容器" placement="top">
          <ListIconButton :icon="Square" label="停止容器" tone="danger" :loading="pending(container.id, 'stop')" :disabled="Boolean(actionPending)" @click="emit('action', container.id, 'stop')" />
        </ElTooltip>
        <ElTooltip content="重启容器" placement="top">
          <ListIconButton :icon="RotateCcw" label="重启容器" :loading="pending(container.id, 'restart')" :disabled="Boolean(actionPending) || container.state !== 'running'" @click="emit('action', container.id, 'restart')" />
        </ElTooltip>
        <ElTooltip content="查看容器详情与日志" placement="top">
          <ListIconButton :icon="Info" label="查看容器详情与日志" @click="emit('details', container)" />
        </ElTooltip>
      </div>
    </div>
    <div v-if="!filteredContainers.length" class="table-empty">没有匹配的 Docker 容器。</div>
    <footer v-else class="container-pagination">
      <ListPageSizeControl list-key="docker.containers" />
      <ResourcePagination v-model:page="page" :page-count="pageCount" />
    </footer>
  </section>

  <section class="container-mobile-list" aria-label="Docker 容器列表">
    <article v-for="container in pagedContainers" :key="container.id" class="container-card panel">
      <header>
        <div class="container-name">
          <span><Box :size="18" /></span>
          <div><strong>{{ container.name }}</strong><small>{{ dockerContainerTimingLabel(container) }}</small></div>
        </div>
        <StatusPill :label="dockerContainerStateLabel(container.state)" :tone="stateTone(container)" />
      </header>
      <dl>
        <div><dt>镜像</dt><dd>{{ container.image }}</dd></div>
        <div><dt>状态</dt><dd>{{ dockerContainerStateDetail(container) }}</dd></div>
        <div><dt>公开端口</dt><dd>{{ publicPorts(container).map((port) => port.label).join('、') || '无' }}</dd></div>
      </dl>
      <div class="mobile-actions">
        <button v-if="container.state !== 'running'" type="button" :disabled="Boolean(actionPending)" @click="emit('action', container.id, 'start')"><Play :size="16" />启动</button>
        <button v-else class="danger" type="button" :disabled="Boolean(actionPending)" @click="emit('action', container.id, 'stop')"><Square :size="15" />停止</button>
        <button type="button" :disabled="Boolean(actionPending) || container.state !== 'running'" @click="emit('action', container.id, 'restart')"><RotateCcw :size="16" />重启</button>
        <button type="button" @click="emit('details', container)"><Info :size="16" />详情</button>
      </div>
    </article>
    <p v-if="!filteredContainers.length" class="table-empty panel">没有匹配的 Docker 容器。</p>
    <footer v-else class="container-pagination panel">
      <ListPageSizeControl list-key="docker.containers" />
      <ResourcePagination v-model:page="page" :page-count="pageCount" />
    </footer>
  </section>
</template>

<style scoped>
.container-panel { overflow: hidden; }
.container-table__head,.container-row { display:grid; grid-template-columns:minmax(180px,1.15fr) 130px minmax(120px,.8fr) minmax(170px,1.1fr) 150px 168px; align-items:center; gap:12px; }
.container-table__head { min-height:46px; padding:0 18px; background:var(--ncp-surface-quiet); color:var(--ncp-text-muted); font-size:.82rem; font-weight:720; }
.container-row { min-height:70px; padding:0 18px; border-top:1px solid var(--ncp-line); background:#fff; color:var(--ncp-text-muted); font-size:.86rem; transition:background-color var(--ncp-duration-fast); }
.container-row:hover { background:var(--ncp-surface-hover); }
.container-table__head>span:nth-child(2),.container-table__head>span:nth-child(5),.container-table__head>span:nth-child(6),.container-row>:nth-child(2),.container-row>:nth-child(5),.container-row>:nth-child(6){justify-self:center;text-align:center}
.container-name { display:flex; min-width:0; align-items:center; gap:9px; }
.container-name>span { display:grid; width:36px; height:36px; flex:0 0 auto; place-items:center; border-radius:10px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.container-name>div { display:grid; min-width:0; gap:1px; }
.container-name strong,.cell-ellipsis { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.container-name strong { color:var(--ncp-text); font-size:.92rem; }
.container-name small,.status-detail { color:var(--ncp-text-subtle); font-size:.76rem; }
.container-state { display:grid; min-width:0; justify-items:center; gap:5px; }
.status-detail { overflow:hidden; max-width:100%; line-height:1.25; text-overflow:ellipsis; white-space:nowrap; }
.mono { font-family:'JetBrains Mono Variable',monospace; font-size:.78rem; }
.port-list { display:flex; align-items:center; gap:5px; }
.port-list span { padding:4px 7px; border-radius:6px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); font-family:'JetBrains Mono Variable',monospace; font-size:.72rem; }
.port-list small { color:var(--ncp-text-subtle); font-size:.72rem; }
.container-actions { display:flex; justify-content:flex-end; gap:5px; }
.table-empty { padding:36px; color:var(--ncp-text-subtle); font-size:.72rem; text-align:center; }
.container-pagination { display:flex; min-height:52px; align-items:center; justify-content:space-between; gap:12px; padding:8px 16px; border-top:1px solid var(--ncp-line); color:var(--ncp-text-subtle); font-size:.82rem; }
.container-pagination div { display:flex; align-items:center; gap:8px; }
.container-pagination button { min-height:34px; padding:0 11px; border:1px solid var(--ncp-line); border-radius:8px; background:#fff; color:var(--ncp-text-muted); font-weight:680; }
.container-pagination button:disabled { cursor:not-allowed; opacity:.42; }
.container-pagination strong { min-width:62px; color:var(--ncp-text); text-align:center; }
.container-mobile-list { display:none; }
@media(max-width:1100px){.container-table__head,.container-row{grid-template-columns:minmax(170px,1fr) 120px minmax(110px,.7fr) minmax(150px,1fr) 120px 152px;gap:8px}.container-actions{gap:3px}}
@media(max-width:820px){
  .container-panel{display:none}.container-mobile-list{display:grid;gap:10px}.container-card{display:grid;gap:13px;padding:15px}.container-card header{display:flex;align-items:center;justify-content:space-between;gap:10px}
  .container-card dl{display:grid;gap:8px;margin:0;padding:11px;border-radius:10px;background:var(--ncp-surface-quiet)}.container-card dl>div{display:grid;grid-template-columns:68px minmax(0,1fr);gap:8px}.container-card dt{color:var(--ncp-text-subtle);font-size:.65rem}.container-card dd{overflow:hidden;margin:0;color:var(--ncp-text-muted);font-size:.68rem;text-overflow:ellipsis;white-space:nowrap}
  .mobile-actions{display:grid;grid-template-columns:repeat(2,1fr);gap:7px}.mobile-actions button{display:flex;min-height:42px;align-items:center;justify-content:center;gap:5px;border-radius:9px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.68rem;font-weight:720}.mobile-actions button.danger{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.mobile-actions button:disabled{opacity:.42}
}
</style>
