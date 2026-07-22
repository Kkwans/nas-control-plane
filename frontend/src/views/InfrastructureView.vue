<script setup lang="ts">
import { computed } from 'vue'
import { Boxes, Cable, Check, CircleAlert, Database, Gauge, HardDrive, Network, Server, ShieldCheck } from '@lucide/vue'

import StatusPill from '@/components/StatusPill.vue'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const capabilities = computed(() => systemStore.capabilities)

const capabilityItems = computed(() => {
  const value = capabilities.value
  return [
    { title: 'Docker Engine', detail: value?.docker ? 'Root Agent 可读取 Docker Engine 资源清单。' : '等待 Docker Engine 能力确认。', enabled: Boolean(value?.docker), icon: Boxes },
    { title: 'Docker Compose', detail: value?.compose ? 'Compose 项目可通过 Docker 元数据自动归类。' : '等待 Compose 能力确认。', enabled: Boolean(value?.compose), icon: Database },
    { title: 'systemd', detail: value?.systemd ? '已识别 systemd 宿主机运行时。' : '等待 systemd 能力确认。', enabled: Boolean(value?.systemd), icon: Server },
    { title: 'journald', detail: value?.journald ? '日志域可由后续日志切片接入。' : '等待 journald 能力确认。', enabled: Boolean(value?.journald), icon: Gauge },
    { title: '数据卷', detail: value?.dataVolumes?.length ? value.dataVolumes.join(' · ') : '等待可用数据卷信息。', enabled: Boolean(value?.dataVolumes?.length), icon: HardDrive },
    { title: '网络接口', detail: value?.networkInterfaces?.length ? `${value.networkInterfaces.length} 个接口已识别` : '等待网络接口信息。', enabled: Boolean(value?.networkInterfaces?.length), icon: Network },
  ]
})
</script>

<template>
  <div class="page infrastructure-page">
    <header class="page-header reveal">
      <div>
        <p class="eyebrow"><ShieldCheck :size="14" aria-hidden="true" /> Capability map</p>
        <h1>把可用能力和真实数据通道放在同一张图里。</h1>
        <p class="page-header__description">这页读取 Root Agent 的环境能力快照，帮助判断哪些资源域已经可用，哪些仍在等待正式部署。</p>
      </div>
      <StatusPill :label="capabilities ? '能力快照已同步' : '等待能力快照'" :tone="capabilities ? 'healthy' : 'pending'" />
    </header>

    <section class="architecture-frame panel reveal" style="--reveal-index: 1" aria-labelledby="architecture-title">
      <div class="architecture-frame__heading"><span>CONTROL PATH</span><h2 id="architecture-title">浏览器 → NCP Server → Unix Socket → Root Agent → NAS</h2><p>登录会话由 Server 持久化；宿主机与 Docker 资源由 Root Agent 提供强类型数据接口。</p></div>
      <ol class="architecture-path">
        <li><span>01</span><strong>浏览器</strong><small>Root Session</small></li>
        <li><span>02</span><strong>NCP Server</strong><small>API · SQLite · UI</small></li>
        <li><span>03</span><strong>Root Agent</strong><small>Unix Socket RPC</small></li>
        <li><span>04</span><strong>NAS 资源</strong><small>System · Docker</small></li>
      </ol>
    </section>

    <section class="capability-grid" aria-label="实时能力快照">
      <article v-for="(item, index) in capabilityItems" :key="item.title" class="capability-card reveal" :style="{ '--reveal-index': index + 2 }">
        <div class="capability-card__top"><span class="capability-card__icon" :class="{ 'capability-card__icon--pending': !item.enabled }" aria-hidden="true"><component :is="item.icon" :size="20" :stroke-width="1.75" /></span><span class="capability-card__state"><Check v-if="item.enabled" :size="15" aria-hidden="true" /><CircleAlert v-else :size="15" aria-hidden="true" />{{ item.enabled ? '已识别' : '待确认' }}</span></div>
        <h2>{{ item.title }}</h2>
        <p>{{ item.detail }}</p>
      </article>
    </section>

    <section class="agent-note panel reveal" style="--reveal-index: 4">
      <Cable :size="22" aria-hidden="true" />
      <div><h2>实时能力与部署状态分开表达</h2><p>本地开发环境可以完成接口和 ARM64 构建验证；只有 Root Agent 和 ncp-server 正式部署后，页面才会显示 NAS 的实机数据。当前不会以预览样本替代缺失的运行时事实。</p></div>
    </section>
  </div>
</template>

<style scoped>
.architecture-frame { padding: clamp(25px, 4vw, 40px); background: radial-gradient(circle at 88% 20%, rgba(44, 111, 223, 0.12), transparent 17rem), linear-gradient(135deg, #fff, #f3f8ff); }
.architecture-frame__heading > span { color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.65rem; font-weight: 750; letter-spacing: 0.12em; }
.architecture-frame__heading h2 { max-width: 830px; margin: 12px 0 8px; font-size: clamp(1.5rem, 3vw, 2.55rem); font-weight: 720; letter-spacing: -0.06em; line-height: 1.12; }
.architecture-frame__heading p { max-width: 760px; margin: 0; color: var(--ncp-text-muted); font-size: 0.85rem; line-height: 1.7; }
.architecture-path { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; padding: 0; margin: 31px 0 0; list-style: none; }
.architecture-path li { position: relative; display: grid; gap: 6px; min-height: 120px; padding: 17px; border: 1px solid rgba(44, 111, 223, 0.13); border-radius: 13px; background: rgba(255, 255, 255, 0.67); }
.architecture-path li:not(:last-child)::after { position: absolute; top: 50%; right: -7px; z-index: 1; width: 13px; height: 1px; background: var(--ncp-primary); content: ''; }
.architecture-path span { color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.62rem; font-weight: 750; }
.architecture-path strong { margin-top: auto; color: var(--ncp-text); font-size: 0.86rem; }
.architecture-path small { color: var(--ncp-text-subtle); font-size: 0.65rem; }
.capability-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; margin-top: 14px; }
.capability-card { display: flex; flex-direction: column; min-height: 214px; padding: 20px; border: 1px solid var(--ncp-line); border-radius: var(--ncp-radius-md); background: var(--ncp-surface); box-shadow: var(--ncp-shadow-panel); transition: border-color var(--ncp-duration-base) var(--ncp-ease-out), transform var(--ncp-duration-base) var(--ncp-ease-out); }
.capability-card:hover { border-color: rgba(44, 111, 223, 0.24); transform: translateY(-3px); }
.capability-card__top { display: flex; align-items: center; justify-content: space-between; }
.capability-card__icon { display: grid; width: 40px; height: 40px; place-items: center; border: 1px solid rgba(26, 139, 109, 0.16); border-radius: 12px; background: rgba(26, 139, 109, 0.08); color: #187b61; }
.capability-card__icon--pending { border-color: rgba(210, 138, 27, 0.18); background: var(--ncp-warning-soft); color: var(--ncp-warning-strong); }
.capability-card__state { display: inline-flex; align-items: center; gap: 5px; color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.59rem; font-weight: 700; }
.capability-card h2 { margin: 24px 0 6px; color: var(--ncp-text); font-size: 0.95rem; letter-spacing: -0.03em; }
.capability-card p { margin: 0; color: var(--ncp-text-muted); font-size: 0.74rem; line-height: 1.7; }
.agent-note { display: flex; align-items: flex-start; gap: 14px; margin-top: 14px; padding: 23px; background: linear-gradient(135deg, #fff, #f7fbff); }
.agent-note > svg { flex: 0 0 auto; margin-top: 2px; color: var(--ncp-primary-strong); }
.agent-note h2 { margin: 0; font-size: 0.94rem; }
.agent-note p { max-width: 850px; margin: 7px 0 0; color: var(--ncp-text-muted); font-size: 0.77rem; line-height: 1.7; }
@media (max-width: 960px) { .architecture-path { grid-template-columns: repeat(2, minmax(0, 1fr)); } .architecture-path li:nth-child(2)::after { display: none; } .capability-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 660px) { .architecture-frame, .agent-note { padding: 20px; } .architecture-path, .capability-grid { grid-template-columns: 1fr; } .architecture-path li:not(:last-child)::after { display: none; } .agent-note { display: block; } .agent-note > svg { margin-bottom: 12px; } }
</style>
