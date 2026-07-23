<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowUpRight, Boxes, Search } from '@lucide/vue'
import { ElInput, ElTooltip } from 'element-plus'

import StatusPill from '@/components/StatusPill.vue'
import { projectStateTone } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const hostName = window.location.hostname
const query = ref('')
const projects = computed(() => {
  const term = query.value.trim().toLowerCase()
  return systemStore.services
    .map((project) => ({
      ...project,
      ports: [...new Set((systemStore.inventory?.containers ?? [])
        .filter((container) => container.projectId === project.id)
        .flatMap((container) => container.ports)
        .filter((port) => port.publicPort > 0)
        .map((port) => port.publicPort))],
    }))
    .filter((project) => !term || project.name.toLowerCase().includes(term) || project.ports.some((port) => String(port).includes(term)))
})

function stateLabel(state: 'running' | 'stopped' | 'degraded') {
  return state === 'running' ? '运行中' : state === 'degraded' ? '需关注' : '已停止'
}
</script>

<template>
  <div class="page services-page">
    <header class="page-toolbar">
      <div><h1>服务入口</h1><p>集中打开 NAS 上已发现的 Docker 服务</p></div>
      <ElInput v-model="query" class="service-search" clearable placeholder="搜索项目或端口" aria-label="搜索项目或端口">
        <template #prefix><Search :size="16" /></template>
      </ElInput>
    </header>

    <div class="summary-strip panel">
      <div><strong>{{ projects.length }}</strong><span>已发现项目</span></div>
      <div><strong>{{ projects.filter((item) => item.state === 'running').length }}</strong><span>运行中</span></div>
      <div><strong>{{ projects.reduce((total, item) => total + item.ports.length, 0) }}</strong><span>公开入口</span></div>
      <RouterLink to="/docker">进入 Docker 管理 <ArrowUpRight :size="15" /></RouterLink>
    </div>

    <section v-if="projects.length" class="service-grid" aria-label="服务入口列表">
      <article v-for="project in projects" :key="project.id" class="service-entry panel">
        <div class="service-entry__head">
          <span class="service-entry__icon"><Boxes :size="20" /></span>
          <div><h2>{{ project.name }}</h2><p>{{ project.runningCount }}/{{ project.containerCount }} 个容器运行</p></div>
          <StatusPill :label="stateLabel(project.state)" :tone="projectStateTone(project.state)" />
        </div>
        <p class="service-entry__path" :title="project.workingDirectory">{{ project.workingDirectory || 'Docker 自动发现项目' }}</p>
        <div class="service-entry__ports">
          <template v-if="project.ports.length">
            <ElTooltip v-for="port in project.ports" :key="port" :content="`打开端口 ${port}`" placement="top">
              <a :href="`http://${hostName}:${port}`" target="_blank" rel="noreferrer">
                <span>{{ port }}</span><ArrowUpRight :size="15" />
              </a>
            </ElTooltip>
          </template>
          <span v-else class="no-port">无公开端口</span>
        </div>
      </article>
    </section>
    <section v-else class="empty-panel panel"><Boxes :size="28" /><div><h2>没有匹配的服务</h2><p>清除搜索条件，或等待 Docker 实时清单更新。</p></div></section>
  </div>
</template>

<style scoped>
.service-search { width: min(320px, 42vw); }
.summary-strip { display:flex; align-items:center; gap:0; min-height:72px; padding:12px 18px; }.summary-strip>div { display:grid; min-width:130px; gap:2px; padding:0 22px; border-right:1px solid var(--ncp-line); }.summary-strip>div:first-child{padding-left:0}.summary-strip strong{font-family:'JetBrains Mono Variable',monospace;font-size:1.18rem}.summary-strip span{color:var(--ncp-text-subtle);font-size:.65rem}.summary-strip>a{display:flex;align-items:center;gap:5px;margin-left:auto;color:var(--ncp-primary-strong);font-size:.72rem;font-weight:700}
.service-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin-top:12px}.service-entry{display:flex;min-height:182px;flex-direction:column;padding:17px;transition:border-color var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast)}.service-entry:hover{border-color:rgba(44,111,223,.25);box-shadow:0 10px 28px rgba(34,60,100,.08)}.service-entry__head{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:10px}.service-entry__icon{display:grid;width:38px;height:38px;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.service-entry h2{overflow:hidden;margin:0;font-size:.85rem;text-overflow:ellipsis;white-space:nowrap}.service-entry__head p{margin:2px 0 0;color:var(--ncp-text-subtle);font-size:.62rem}.service-entry__path{overflow:hidden;margin:16px 0 13px;color:var(--ncp-text-subtle);font-size:.64rem;text-overflow:ellipsis;white-space:nowrap}.service-entry__ports{display:flex;flex-wrap:wrap;gap:7px;margin-top:auto}.service-entry__ports a{display:flex;min-height:40px;align-items:center;justify-content:center;gap:5px;padding:0 12px;border:1px solid rgba(44,111,223,.18);border-radius:8px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-family:'JetBrains Mono Variable',monospace;font-size:.7rem;font-weight:750}.service-entry__ports a:hover{background:rgba(44,111,223,.16)}.no-port{display:flex;min-height:40px;align-items:center;color:var(--ncp-text-subtle);font-size:.67rem}.empty-panel{display:flex;align-items:center;gap:14px;margin-top:12px;padding:28px;color:var(--ncp-text-subtle)}.empty-panel h2{margin:0;color:var(--ncp-text);font-size:.9rem}.empty-panel p{margin:4px 0 0;font-size:.7rem}
@media(max-width:1200px){.service-grid{grid-template-columns:repeat(2,1fr)}}@media(max-width:720px){.service-grid{grid-template-columns:1fr}.summary-strip>div{min-width:0;flex:1;padding:0 10px}.summary-strip>a{display:none}.service-search{width:48vw}}
</style>
