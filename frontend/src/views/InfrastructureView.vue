<script setup lang="ts">
import { computed } from 'vue'
import {
  Boxes,
  Check,
  CircleAlert,
  Database,
  FileClock,
  HardDrive,
  MemoryStick,
  Network,
  Server,
  Waypoints,
} from '@lucide/vue'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const capabilities = computed(() => systemStore.capabilities)
const summary = computed(() => systemStore.summary)
const online = computed(() => systemStore.connectionState === 'connected')

const headerStats = computed<WorkspaceStat[]>(() => [
  { label: '系统架构', value: summary.value?.host.architecture ?? capabilities.value?.architecture ?? '—' },
  { label: 'Docker', value: systemStore.inventory?.engine.serverVersion ?? '—' },
  { label: '数据卷', value: capabilities.value?.dataVolumes?.length ?? 0 },
])

const facts = computed(() => [
  { label: '主机名', value: summary.value?.host.hostname ?? '—' },
  { label: '操作系统', value: summary.value?.host.operatingSystem ?? '—' },
  { label: '内核版本', value: summary.value?.host.kernelVersion ?? '—' },
  { label: '系统架构', value: summary.value?.host.architecture ?? capabilities.value?.architecture ?? '—' },
  { label: 'cgroup', value: capabilities.value?.cgroupVersion ? `v${capabilities.value.cgroupVersion}` : '—' },
  { label: 'Docker Engine', value: systemStore.inventory?.engine.serverVersion ?? '—' },
])

const chain = computed(() => [
  { name: 'Web 控制台', detail: '浏览器中的 Root 管理会话', state: 'ready' },
  { name: 'NCP Server', detail: 'HTTP API、SQLite 与任务调度', state: online.value ? 'ready' : 'warning' },
  { name: 'Unix Socket', detail: '本机强类型 RPC 通道', state: online.value ? 'ready' : 'warning' },
  { name: 'Root Agent', detail: '访问宿主机与 Docker Engine', state: online.value ? 'ready' : 'warning' },
])

const capabilityItems = computed(() => [
  { name: 'Docker Engine', enabled: Boolean(capabilities.value?.docker), detail: systemStore.inventory?.engine.serverVersion ? `版本 ${systemStore.inventory.engine.serverVersion}` : '等待检测', icon: Boxes, tone: 'blue' },
  { name: 'Docker Compose', enabled: Boolean(capabilities.value?.compose), detail: `已发现 ${systemStore.services.length} 个项目`, icon: Database, tone: 'violet' },
  { name: 'systemd', enabled: Boolean(capabilities.value?.systemd), detail: '宿主机服务管理', icon: Server, tone: 'cyan' },
  { name: 'journald', enabled: Boolean(capabilities.value?.journald), detail: '系统日志与服务日志', icon: FileClock, tone: 'amber' },
  { name: '数据卷', enabled: Boolean(capabilities.value?.dataVolumes?.length), detail: capabilities.value?.dataVolumes?.join('、') || '等待检测', icon: HardDrive, tone: 'green' },
  { name: '网络接口', enabled: Boolean(capabilities.value?.networkInterfaces?.length), detail: `${capabilities.value?.networkInterfaces?.length ?? 0} 个接口`, icon: Network, tone: 'rose' },
])
</script>

<template>
  <div class="page workspace-page system-page">
    <WorkspaceHeader title="系统信息" description="查看 NAS 设备、控制链路与 Root Agent 能力" :icon="Server" :stats="headerStats" />

    <section class="device-hero panel">
      <div class="device-hero__identity">
        <span><Server :size="28" /></span>
        <div>
          <p>当前设备</p>
          <h2>{{ summary?.host.hostname ?? 'NAS 主机' }}</h2>
          <small>{{ summary?.host.operatingSystem ?? '等待系统信息' }} · {{ summary?.host.architecture ?? capabilities?.architecture ?? '未知架构' }}</small>
        </div>
      </div>
      <div class="device-health">
        <span :class="{ warning: !online }"><i></i>{{ online ? '控制链路正常' : '控制链路待恢复' }}</span>
        <small>{{ systemStore.lastUpdated ? `最近同步 ${new Date(systemStore.lastUpdated).toLocaleTimeString('zh-CN')}` : '等待首次同步' }}</small>
      </div>
    </section>

    <section class="system-grid">
      <article class="facts-panel panel">
        <header class="section-heading">
          <span><MemoryStick :size="19" /></span>
          <div><h2>硬件与系统</h2><p>来自宿主机实时快照和 Docker Engine</p></div>
        </header>
        <dl class="fact-grid">
          <div v-for="item in facts" :key="item.label">
            <dt>{{ item.label }}</dt><dd>{{ item.value }}</dd>
          </div>
        </dl>
      </article>

      <article class="chain-panel panel">
        <header class="section-heading">
          <span><Waypoints :size="19" /></span>
          <div><h2>控制链路</h2><p>请求从浏览器到 Root Agent 的实际路径</p></div>
        </header>
        <ol class="control-chain">
          <li v-for="(node, index) in chain" :key="node.name" :class="`control-node--${node.state}`">
            <span class="control-node__index">{{ index + 1 }}</span>
            <div><strong>{{ node.name }}</strong><small>{{ node.detail }}</small></div>
            <Check v-if="node.state === 'ready'" :size="16" />
            <CircleAlert v-else :size="16" />
          </li>
        </ol>
      </article>
    </section>

    <section class="capabilities-section">
      <div class="section-title">
        <div><h2>能力检测</h2><p>Root Agent 当前可调用的宿主机能力</p></div>
        <span>{{ capabilityItems.filter((item) => item.enabled).length }} / {{ capabilityItems.length }} 可用</span>
      </div>
      <div class="capability-grid">
        <article v-for="item in capabilityItems" :key="item.name" class="capability-card panel">
          <span :class="['capability-card__icon', `capability-card__icon--${item.tone}`]"><component :is="item.icon" :size="21" /></span>
          <div><strong>{{ item.name }}</strong><small :title="item.detail">{{ item.detail }}</small></div>
          <span :class="['capability-state', { 'capability-state--off': !item.enabled }]"><i></i>{{ item.enabled ? '可用' : '待检测' }}</span>
        </article>
      </div>
    </section>
  </div>
</template>

<style scoped>
.device-hero{display:flex;min-height:126px;align-items:center;justify-content:space-between;gap:24px;padding:24px;background:radial-gradient(circle at 0 0,rgba(52,116,212,.09),transparent 34%),linear-gradient(110deg,#fff 58%,#f8fbff)}.device-hero__identity{display:flex;align-items:center;gap:16px}.device-hero__identity>span{display:grid;width:62px;height:62px;place-items:center;border:1px solid rgba(52,116,212,.14);border-radius:18px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);box-shadow:0 8px 24px rgba(52,116,212,.1)}.device-hero__identity p{margin:0;color:var(--ncp-text-subtle);font-size:.76rem;font-weight:700}.device-hero__identity h2{margin:1px 0;font-size:1.45rem;letter-spacing:-.035em}.device-hero__identity small{color:var(--ncp-text-muted);font-size:.86rem}.device-health{display:grid;justify-items:end;gap:6px}.device-health>span{display:inline-flex;align-items:center;gap:8px;padding:7px 11px;border:1px solid rgba(35,134,111,.16);border-radius:999px;background:var(--ncp-success-soft);color:var(--ncp-success);font-size:.8rem;font-weight:720}.device-health>span i,.capability-state i{width:7px;height:7px;border-radius:50%;background:currentColor}.device-health>span.warning{border-color:rgba(184,118,34,.18);background:var(--ncp-warning-soft);color:var(--ncp-warning)}.device-health small{color:var(--ncp-text-subtle);font-size:.76rem}.system-grid{display:grid;grid-template-columns:minmax(0,1.04fr) minmax(0,.96fr);gap:16px}.facts-panel,.chain-panel{overflow:hidden}.section-heading{display:flex;align-items:center;gap:11px;padding:18px 20px;border-bottom:1px solid var(--ncp-line);background:linear-gradient(135deg,#fff,var(--ncp-surface-quiet))}.section-heading>span{display:grid;width:38px;height:38px;place-items:center;border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.section-heading h2,.section-title h2{margin:0;font-size:1rem}.section-heading p,.section-title p{margin:3px 0 0;color:var(--ncp-text-subtle);font-size:.8rem}.fact-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));margin:0;padding:4px 20px 14px}.fact-grid>div{min-width:0;padding:15px 4px;border-bottom:1px solid var(--ncp-line)}.fact-grid dt{color:var(--ncp-text-subtle);font-size:.75rem}.fact-grid dd{overflow:hidden;margin:5px 0 0;color:var(--ncp-text);font-family:var(--ncp-font-latin);font-size:.88rem;font-weight:720;text-overflow:ellipsis;white-space:nowrap}.control-chain{display:grid;gap:0;margin:0;padding:10px 20px 14px;list-style:none}.control-chain li{position:relative;display:grid;min-height:64px;grid-template-columns:34px minmax(0,1fr) auto;align-items:center;gap:11px}.control-chain li:not(:last-child)::after{position:absolute;top:49px;bottom:-15px;left:16px;width:1px;background:var(--ncp-line-strong);content:''}.control-node__index{position:relative;z-index:1;display:grid;width:34px;height:34px;place-items:center;border:1px solid rgba(52,116,212,.16);border-radius:10px;background:#fff;color:var(--ncp-primary-strong);font-family:var(--ncp-font-mono);font-size:.74rem;font-weight:760;box-shadow:0 3px 10px rgba(34,64,105,.06)}.control-chain li>div{display:grid;gap:2px}.control-chain strong{font-size:.86rem}.control-chain small{color:var(--ncp-text-subtle);font-size:.76rem}.control-chain li>svg{color:var(--ncp-success)}.control-chain .control-node--warning>svg{color:var(--ncp-warning)}.section-title{display:flex;align-items:end;justify-content:space-between;gap:16px;margin:2px 2px 10px}.section-title>span{color:var(--ncp-text-subtle);font-size:.78rem;font-weight:680}.capability-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.capability-card{display:grid;min-height:92px;grid-template-columns:44px minmax(0,1fr);align-items:center;gap:12px;padding:16px;transition:border-color var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast),transform var(--ncp-duration-fast)}.capability-card:hover{border-color:rgba(52,116,212,.2);box-shadow:var(--ncp-shadow-hover);transform:translateY(-1px)}.capability-card__icon{display:grid;width:44px;height:44px;place-items:center;border-radius:13px}.capability-card__icon--blue{background:#eaf2fd;color:#3474d4}.capability-card__icon--violet{background:#f1ecfc;color:#7d5dc7}.capability-card__icon--cyan{background:#e8f5f7;color:#2f8792}.capability-card__icon--amber{background:#fbf2e7;color:#ae7026}.capability-card__icon--green{background:#e9f5f1;color:#2d856f}.capability-card__icon--rose{background:#faedf0;color:#b75869}.capability-card>div{display:grid;min-width:0;gap:3px}.capability-card strong{font-size:.88rem}.capability-card small{overflow:hidden;color:var(--ncp-text-subtle);font-size:.76rem;text-overflow:ellipsis;white-space:nowrap}.capability-state{grid-column:2;display:inline-flex;width:max-content;align-items:center;gap:6px;color:var(--ncp-success);font-size:.72rem;font-weight:700}.capability-state--off{color:var(--ncp-warning)}@media(max-width:1050px){.system-grid{grid-template-columns:1fr}.capability-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:700px){.device-hero{align-items:flex-start;flex-direction:column}.device-health{justify-items:start}.capability-grid{grid-template-columns:1fr}}@media(max-width:520px){.device-hero{padding:18px}.device-hero__identity{align-items:flex-start}.device-hero__identity>span{width:50px;height:50px;border-radius:14px}.fact-grid{grid-template-columns:1fr}.capability-card{min-height:82px}}
</style>
