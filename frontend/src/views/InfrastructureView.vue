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
.system-layout{display:grid;grid-template-columns:1fr 1fr;gap:12px}.info-panel,.agent-panel,.capability-table{padding:20px}.info-panel header,.agent-panel header,.capability-table header{display:flex;align-items:center;justify-content:space-between;padding-bottom:14px;border-bottom:1px solid var(--ncp-line)}header h2{margin:0;font-size:.9rem}header span{color:var(--ncp-text-subtle);font-size:.72rem}.info-panel dl{display:grid;grid-template-columns:repeat(2,1fr);gap:0;margin:0}.info-panel dl div{padding:14px 4px;border-bottom:1px solid var(--ncp-line)}.info-panel dt{color:var(--ncp-text-subtle);font-size:.72rem}.info-panel dd{margin:4px 0 0;font-size:.78rem;font-weight:700}.agent-panel ol{display:grid;gap:0;padding:0;margin:0;list-style:none}.agent-panel li{display:flex;align-items:center;gap:11px;min-height:65px;border-bottom:1px solid var(--ncp-line)}.agent-panel li>span{display:grid;width:30px;height:30px;place-items:center;border-radius:8px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-family:'JetBrains Mono Variable',monospace;font-size:.72rem;font-weight:750}.agent-panel li div{display:grid;gap:2px}.agent-panel strong{font-size:.76rem}.agent-panel small{color:var(--ncp-text-subtle);font-size:.72rem}.capability-table{margin-top:0}.capability-table__head,.capability-row{display:grid;grid-template-columns:1fr 130px 1.4fr;align-items:center;min-height:48px;padding:0 8px}.capability-table__head{color:var(--ncp-text-subtle);font-size:.72rem;font-weight:700}.capability-row{border-top:1px solid var(--ncp-line);font-size:.74rem}.capability-row>div,.capability-row>span:nth-child(2){display:flex;align-items:center;gap:8px}.capability-row>div svg{color:var(--ncp-primary-strong)}.capability-row>span:nth-child(2){color:var(--ncp-warning-strong)}.capability-row>span.enabled{color:var(--ncp-success)}.capability-row>span:last-child{color:var(--ncp-text-muted)}
@media(max-width:900px){.system-layout{grid-template-columns:1fr}}@media(max-width:600px){.info-panel,.agent-panel,.capability-table{padding:16px}.info-panel dl{grid-template-columns:1fr}.capability-table__head{display:none}.capability-row{display:grid;grid-template-columns:1fr auto;gap:10px;min-height:0;padding:14px 2px}.capability-row>span:last-child{grid-column:1/-1}.capability-row>[data-label]::before{display:block;margin-bottom:3px;color:var(--ncp-text-subtle);font-size:.68rem;font-weight:600;content:attr(data-label)}.capability-row>div[data-label]::before{display:none}}
</style>
