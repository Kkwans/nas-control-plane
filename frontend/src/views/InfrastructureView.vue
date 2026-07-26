<script setup lang="ts">
import { computed } from 'vue'
import { Boxes, CheckCircle2, CircleAlert, Database, FileClock, HardDrive, Network, Server } from '@lucide/vue'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const capabilities = computed(() => systemStore.capabilities)
const capabilityItems = computed(() => [
  { name: 'Docker Engine', enabled: Boolean(capabilities.value?.docker), detail: systemStore.inventory?.engine.serverVersion || '等待检测', icon: Boxes },
  { name: 'Docker Compose', enabled: Boolean(capabilities.value?.compose), detail: `${systemStore.services.length} 个项目`, icon: Database },
  { name: 'systemd', enabled: Boolean(capabilities.value?.systemd), detail: '宿主机服务管理', icon: Server },
  { name: 'journald', enabled: Boolean(capabilities.value?.journald), detail: '系统日志来源', icon: FileClock },
  { name: '数据卷', enabled: Boolean(capabilities.value?.dataVolumes?.length), detail: capabilities.value?.dataVolumes?.join('、') || '等待检测', icon: HardDrive },
  { name: '网络接口', enabled: Boolean(capabilities.value?.networkInterfaces?.length), detail: `${capabilities.value?.networkInterfaces?.length ?? 0} 个接口`, icon: Network },
])
const headerStats = computed<WorkspaceStat[]>(() => [
  { label: '系统架构', value: systemStore.summary?.host.architecture ?? capabilities.value?.architecture ?? '—' },
  { label: 'Docker 版本', value: systemStore.inventory?.engine.serverVersion ?? '—' },
  { label: '数据卷', value: capabilities.value?.dataVolumes?.length ?? 0 },
])
</script>

<template>
  <div class="page workspace-page system-page">
    <WorkspaceHeader title="系统信息" description="设备信息、Root Agent 能力与实时数据来源" :icon="Server" :stats="headerStats" />
    <section class="device-summary panel">
      <div class="device-summary__identity">
        <span><Server :size="24" /></span>
        <div><strong>{{ systemStore.summary?.host.hostname ?? 'NAS 主机' }}</strong><small>{{ systemStore.summary?.host.operatingSystem ?? '等待系统信息' }} · {{ systemStore.summary?.host.architecture ?? capabilities?.architecture ?? '—' }}</small></div>
      </div>
      <dl>
        <div><dt>内核</dt><dd>{{ systemStore.summary?.host.kernelVersion ?? '—' }}</dd></div>
        <div><dt>cgroup</dt><dd>v{{ capabilities?.cgroupVersion ?? '—' }}</dd></div>
        <div><dt>Docker</dt><dd>{{ systemStore.inventory?.engine.serverVersion ?? '—' }}</dd></div>
        <div><dt>网络接口</dt><dd>{{ capabilities?.networkInterfaces?.length ?? 0 }}</dd></div>
      </dl>
    </section>
    <section class="system-layout">
      <article class="panel info-panel">
        <header><h2>设备信息</h2><span>实时快照</span></header>
        <dl>
          <div><dt>主机名</dt><dd>{{ systemStore.summary?.host.hostname ?? '—' }}</dd></div>
          <div><dt>操作系统</dt><dd>{{ systemStore.summary?.host.operatingSystem ?? '—' }}</dd></div>
          <div><dt>内核版本</dt><dd>{{ systemStore.summary?.host.kernelVersion ?? '—' }}</dd></div>
          <div><dt>系统架构</dt><dd>{{ systemStore.summary?.host.architecture ?? capabilities?.architecture ?? '—' }}</dd></div>
          <div><dt>cgroup</dt><dd>v{{ capabilities?.cgroupVersion ?? '—' }}</dd></div>
          <div><dt>Docker 版本</dt><dd>{{ systemStore.inventory?.engine.serverVersion ?? '—' }}</dd></div>
        </dl>
      </article>
      <article class="panel agent-panel">
        <header><h2>控制链路</h2><span>浏览器 → Server → Root Agent → NAS</span></header>
        <ol>
          <li><span>1</span><div><strong>浏览器控制台</strong><small>Root 登录会话与实时 SSE</small></div></li>
          <li><span>2</span><div><strong>NCP Server</strong><small>HTTP API、SQLite 与任务协调</small></div></li>
          <li><span>3</span><div><strong>Root Agent</strong><small>Unix Socket 强类型 RPC</small></div></li>
          <li><span>4</span><div><strong>NAS 资源</strong><small>系统、Docker、日志与终端</small></div></li>
        </ol>
      </article>
    </section>
    <section class="capability-table panel">
      <header><h2>能力检测</h2><span>来自 Root Agent</span></header>
      <div class="capability-table__head"><span>模块</span><span>状态</span><span>说明</span></div>
      <div v-for="item in capabilityItems" :key="item.name" class="capability-row">
        <div data-label="模块"><component :is="item.icon" :size="18" /><strong>{{ item.name }}</strong></div>
        <span data-label="状态" :class="{ enabled: item.enabled }"><CheckCircle2 v-if="item.enabled" :size="16" /><CircleAlert v-else :size="16" />{{ item.enabled ? '可用' : '待检测' }}</span>
        <span data-label="说明">{{ item.detail }}</span>
      </div>
    </section>
  </div>
</template>

<style scoped>
.device-summary{display:flex;min-height:92px;align-items:center;justify-content:space-between;gap:24px;padding:18px 20px;background:radial-gradient(circle at 12% -80%,rgba(52,116,212,.13),transparent 35%),#fff}.device-summary__identity{display:flex;align-items:center;gap:13px}.device-summary__identity>span{display:grid;width:50px;height:50px;place-items:center;border-radius:14px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.device-summary__identity>div{display:grid;gap:3px}.device-summary__identity strong{font-size:1.08rem}.device-summary__identity small{color:var(--ncp-text-muted);font-size:.84rem}.device-summary dl{display:flex;margin:0}.device-summary dl div{display:grid;min-width:112px;gap:2px;padding:0 18px;border-left:1px solid var(--ncp-line)}.device-summary dt{color:var(--ncp-text-subtle);font-size:.78rem}.device-summary dd{order:-1;margin:0;color:var(--ncp-text);font-family:var(--ncp-font-latin);font-size:.98rem;font-weight:750}.system-layout{display:grid;grid-template-columns:1.08fr .92fr;gap:14px}.info-panel,.agent-panel,.capability-table{padding:20px}.info-panel header,.agent-panel header,.capability-table header{display:flex;align-items:center;justify-content:space-between;padding-bottom:14px;border-bottom:1px solid var(--ncp-line)}header h2{margin:0;font-size:1rem}header span{color:var(--ncp-text-subtle);font-size:.8rem}.info-panel dl{display:grid;grid-template-columns:repeat(2,1fr);gap:0;margin:0}.info-panel dl div{padding:15px 4px;border-bottom:1px solid var(--ncp-line)}.info-panel dt{color:var(--ncp-text-subtle);font-size:.8rem}.info-panel dd{margin:4px 0 0;font-size:.86rem;font-weight:700}.agent-panel ol{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:0;padding:18px 0 4px;margin:0;list-style:none}.agent-panel li{position:relative;display:grid;align-content:start;gap:8px;min-height:112px;padding:0 10px}.agent-panel li:not(:last-child)::after{position:absolute;top:15px;right:-8px;width:16px;height:1px;background:var(--ncp-line-strong);content:''}.agent-panel li>span{display:grid;width:32px;height:32px;place-items:center;border-radius:9px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-family:var(--ncp-font-mono);font-size:.76rem;font-weight:750}.agent-panel li div{display:grid;gap:3px}.agent-panel strong{font-size:.82rem}.agent-panel small{color:var(--ncp-text-subtle);font-size:.76rem;line-height:1.45}.capability-table{margin-top:0}.capability-table__head,.capability-row{display:grid;grid-template-columns:1fr 150px 1.4fr;align-items:center;min-height:52px;padding:0 10px}.capability-table__head{color:var(--ncp-text-muted);font-size:.8rem;font-weight:700}.capability-row{border-top:1px solid var(--ncp-line);font-size:.82rem;transition:background-color var(--ncp-duration-fast)}.capability-row:hover{background:var(--ncp-surface-hover)}.capability-row>div,.capability-row>span:nth-child(2){display:flex;align-items:center;gap:8px}.capability-row>div svg{color:var(--ncp-primary-strong)}.capability-row>span:nth-child(2){color:var(--ncp-warning-strong)}.capability-row>span.enabled{color:var(--ncp-success)}.capability-row>span:last-child{color:var(--ncp-text-muted)}
@media(max-width:1100px){.device-summary{align-items:flex-start;flex-direction:column}.device-summary dl{width:100%}.device-summary dl div{min-width:0;flex:1}.system-layout{grid-template-columns:1fr}}@media(max-width:700px){.device-summary dl{display:grid;grid-template-columns:1fr 1fr}.device-summary dl div{padding:10px;border:0;border-top:1px solid var(--ncp-line)}.agent-panel ol{grid-template-columns:1fr 1fr}.agent-panel li:not(:last-child)::after{display:none}}@media(max-width:600px){.info-panel,.agent-panel,.capability-table{padding:16px}.info-panel dl{grid-template-columns:1fr}.capability-table__head{display:none}.capability-row{display:grid;grid-template-columns:1fr auto;gap:10px;min-height:0;padding:14px 2px}.capability-row>span:last-child{grid-column:1/-1}.capability-row>[data-label]::before{display:block;margin-bottom:3px;color:var(--ncp-text-subtle);font-size:.72rem;font-weight:600;content:attr(data-label)}.capability-row>div[data-label]::before{display:none}}
</style>
