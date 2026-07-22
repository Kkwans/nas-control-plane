<script setup lang="ts">
import { computed } from 'vue'
import { ArrowUpRight, Boxes, Container, Network, PackageCheck, ServerCog } from '@lucide/vue'

import StatusPill from '@/components/StatusPill.vue'
import { projectStateTone } from '@/domain/overview'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const inventory = computed(() => systemStore.inventory)
const projects = computed(() => systemStore.services?.services ?? inventory.value?.projects ?? [])

function containersFor(projectId: string) {
  return inventory.value?.containers.filter((container) => container.projectId === projectId) ?? []
}

function portsFor(projectId: string) {
  const unique = new Map<string, { publicPort: number; privatePort: number; protocol: string }>()
  for (const container of containersFor(projectId)) {
    for (const port of container.ports) {
      if (port.publicPort > 0) unique.set(`${port.publicPort}/${port.protocol}`, port)
    }
  }
  return [...unique.values()]
}

function portUrl(port: { publicPort: number }) {
  return `http://${window.location.hostname}:${port.publicPort}`
}

function stateLabel(state: 'running' | 'stopped' | 'degraded') {
  if (state === 'running') return '运行中'
  if (state === 'degraded') return '需关注'
  return '已停止'
}
</script>

<template>
  <div class="page services-page">
    <header class="page-header reveal">
      <div>
        <p class="eyebrow"><Boxes :size="14" aria-hidden="true" /> Docker service inventory</p>
        <h1>按项目归类，而不是堆叠容器。</h1>
        <p class="page-header__description">
          服务中心直接读取 Docker Engine 的 Compose 元数据和运行状态；容器控制、日志和任务编排将在后续切片加入。
        </p>
      </div>
      <div class="service-count"><strong>{{ projects.length }}</strong><span>已发现服务组</span></div>
    </header>

    <section class="engine-summary panel reveal" style="--reveal-index: 1" aria-labelledby="engine-summary-title">
      <div>
        <span class="engine-summary__eyebrow">DOCKER ENGINE</span>
        <h2 id="engine-summary-title">{{ inventory ? inventory.engine.operatingSystem || 'Docker Engine 已连接' : '等待 Docker Engine 清单' }}</h2>
        <p v-if="inventory">{{ inventory.engine.serverVersion || '已协商版本' }} · {{ inventory.engine.architecture || '架构待识别' }} · {{ inventory.engine.images }} 个本地镜像</p>
        <p v-else>Root Agent 返回清单后，这里会展示真实容器、Compose 服务组与可访问端口。</p>
      </div>
      <div class="engine-summary__metrics">
        <div><span>RUNNING</span><strong>{{ inventory?.engine.containersRunning ?? '—' }}</strong></div>
        <div><span>STOPPED</span><strong>{{ inventory?.engine.containersStopped ?? '—' }}</strong></div>
        <div><span>PROJECTS</span><strong>{{ projects.length }}</strong></div>
      </div>
    </section>

    <section v-if="projects.length" class="service-board" aria-label="实时服务列表">
      <article v-for="(project, index) in projects" :key="project.id" class="service-card reveal" :style="{ '--reveal-index': index + 2 }">
        <div class="service-card__topline">
          <span>{{ project.kind === 'compose' ? 'COMPOSE PROJECT' : 'STANDALONE GROUP' }}</span>
          <StatusPill :label="stateLabel(project.state)" :tone="projectStateTone(project.state)" />
        </div>
        <div class="service-card__identity"><span class="service-card__icon" aria-hidden="true"><Boxes :size="21" :stroke-width="1.8" /></span><div><h2>{{ project.name }}</h2><p>{{ project.runningCount }} / {{ project.containerCount }} 容器运行中</p></div></div>
        <div class="service-card__meta"><ServerCog :size="15" aria-hidden="true" /><span>{{ project.workingDirectory || (project.kind === 'compose' ? 'Compose 工作目录未提供' : '由 Docker 自动归类') }}</span></div>
        <ul class="container-list">
          <li v-for="container in containersFor(project.id).slice(0, 3)" :key="container.id"><Container :size="14" aria-hidden="true" /><span>{{ container.name }}</span><small>{{ container.state }}</small></li>
          <li v-if="containersFor(project.id).length > 3" class="container-list__more">另有 {{ containersFor(project.id).length - 3 }} 个容器</li>
        </ul>
        <div class="service-card__footer">
          <div v-if="portsFor(project.id).length" class="port-list">
            <a v-for="port in portsFor(project.id)" :key="`${port.publicPort}/${port.protocol}`" :href="portUrl(port)" target="_blank" rel="noreferrer" :aria-label="`打开 ${project.name} 的 ${port.publicPort} 端口`">
              <Network :size="13" aria-hidden="true" /> {{ port.publicPort }} <ArrowUpRight :size="12" aria-hidden="true" />
            </a>
          </div>
          <span v-else class="no-port">无公开端口</span>
          <span class="service-card__kind"><PackageCheck :size="14" aria-hidden="true" /> {{ project.kind }}</span>
        </div>
      </article>
    </section>

    <section v-else class="empty-services panel reveal" style="--reveal-index: 2">
      <span class="empty-services__icon" aria-hidden="true"><Boxes :size="24" /></span>
      <div><h2>尚未收到服务发现结果</h2><p>这不是空的演示列表。请确认 Root Agent 已部署、Docker Engine 正常运行后刷新页面。</p></div>
    </section>
  </div>
</template>

<style scoped>
.service-count { display: grid; min-width: 142px; padding: 14px 16px; border: 1px solid var(--ncp-line); border-radius: var(--ncp-radius-md); background: rgba(255, 255, 255, 0.66); box-shadow: var(--ncp-shadow-panel); }
.service-count strong { color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 1.45rem; font-weight: 680; letter-spacing: -0.08em; line-height: 1; }
.service-count span { margin-top: 7px; color: var(--ncp-text-subtle); font-size: 0.67rem; font-weight: 700; }
.engine-summary { display: flex; align-items: flex-end; justify-content: space-between; gap: 26px; padding: clamp(25px, 4vw, 40px); background: radial-gradient(circle at 88% 14%, rgba(44, 111, 223, 0.12), transparent 18rem), linear-gradient(135deg, #fff, #f3f8ff); }
.engine-summary__eyebrow { color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.65rem; font-weight: 750; letter-spacing: 0.12em; }
.engine-summary h2 { max-width: 690px; margin: 11px 0 8px; font-size: clamp(1.55rem, 3vw, 2.55rem); font-weight: 720; letter-spacing: -0.055em; line-height: 1.1; }
.engine-summary p { max-width: 670px; margin: 0; color: var(--ncp-text-muted); font-size: 0.84rem; line-height: 1.7; }
.engine-summary__metrics { display: grid; grid-template-columns: repeat(3, minmax(72px, 1fr)); gap: 8px; min-width: 290px; }
.engine-summary__metrics div { display: grid; gap: 7px; padding: 14px; border: 1px solid rgba(44, 111, 223, 0.12); border-radius: 12px; background: rgba(255, 255, 255, 0.7); }
.engine-summary__metrics span { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.56rem; font-weight: 750; letter-spacing: 0.07em; }
.engine-summary__metrics strong { color: var(--ncp-text); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 1.28rem; font-weight: 650; letter-spacing: -0.08em; }
.service-board { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; margin-top: 14px; }
.service-card { display: flex; flex-direction: column; min-height: 340px; padding: 21px; border: 1px solid var(--ncp-line); border-radius: var(--ncp-radius-md); background: var(--ncp-surface); box-shadow: var(--ncp-shadow-panel); transition: border-color var(--ncp-duration-base) var(--ncp-ease-out), transform var(--ncp-duration-base) var(--ncp-ease-out), box-shadow var(--ncp-duration-base) var(--ncp-ease-out); }
.service-card:hover { border-color: rgba(44, 111, 223, 0.24); box-shadow: 0 20px 38px rgba(35, 55, 91, 0.09); transform: translateY(-3px); }
.service-card__topline { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.service-card__topline > span { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.57rem; font-weight: 750; letter-spacing: 0.08em; }
.service-card__identity { display: flex; align-items: center; gap: 12px; margin-top: 22px; }
.service-card__icon { display: grid; flex: 0 0 auto; width: 43px; height: 43px; place-items: center; border: 1px solid rgba(44, 111, 223, 0.14); border-radius: 13px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.service-card h2 { margin: 0; color: var(--ncp-text); font-size: 1rem; letter-spacing: -0.035em; }
.service-card__identity p { margin: 4px 0 0; color: var(--ncp-text-subtle); font-size: 0.69rem; }
.service-card__meta { display: flex; gap: 8px; align-items: flex-start; margin-top: 22px; color: var(--ncp-text-subtle); font-size: 0.69rem; line-height: 1.5; }
.service-card__meta svg { flex: 0 0 auto; margin-top: 2px; color: var(--ncp-primary-strong); }
.container-list { display: grid; gap: 8px; padding: 0; margin: 19px 0 0; list-style: none; }
.container-list li { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; gap: 7px; align-items: center; color: var(--ncp-text-muted); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.64rem; }
.container-list li svg { color: var(--ncp-text-subtle); }
.container-list span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.container-list small { color: var(--ncp-text-subtle); font-size: 0.58rem; }
.container-list__more { display: block !important; color: var(--ncp-text-subtle) !important; padding-left: 21px; }
.service-card__footer { display: flex; align-items: flex-end; justify-content: space-between; gap: 10px; margin-top: auto; padding-top: 17px; border-top: 1px solid var(--ncp-line); }
.port-list { display: flex; flex-wrap: wrap; gap: 6px; }
.port-list a { display: inline-flex; align-items: center; gap: 4px; min-height: 29px; padding: 0 8px; border: 1px solid rgba(44, 111, 223, 0.17); border-radius: 8px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.61rem; font-weight: 750; transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out); }
.port-list a:hover { background: rgba(44, 111, 223, 0.16); }
.no-port { color: var(--ncp-text-subtle); font-size: 0.66rem; }
.service-card__kind { display: inline-flex; align-items: center; gap: 5px; color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.6rem; }
.empty-services { display: flex; align-items: center; gap: 18px; min-height: 180px; margin-top: 14px; padding: 28px; }
.empty-services__icon { display: grid; flex: 0 0 auto; width: 48px; height: 48px; place-items: center; border-radius: 15px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.empty-services h2 { margin: 0; font-size: 1rem; }
.empty-services p { max-width: 560px; margin: 7px 0 0; color: var(--ncp-text-muted); font-size: 0.79rem; line-height: 1.7; }
@media (max-width: 940px) { .engine-summary { display: block; } .engine-summary__metrics { margin-top: 24px; } .service-board { grid-template-columns: 1fr; } }
@media (max-width: 640px) { .service-count { display: inline-grid; margin-top: 20px; } .engine-summary { padding: 22px; } .engine-summary__metrics { min-width: 0; } .service-card { padding: 19px; } }
</style>
