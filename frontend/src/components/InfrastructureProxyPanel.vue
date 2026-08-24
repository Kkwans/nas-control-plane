<script setup lang="ts">
import { computed, type Component } from 'vue'

import type {
  MihomoCapability,
  MihomoInspection,
  PublicEgressCapability,
  PublicEgressResult,
  SystemDetails,
  TailscaleCapability,
} from '@/api/system'
import ActionButton from '@/components/ActionButton.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import { formatTime, mihomoModeLabel, proxyStateLabel } from '@/domain/infrastructurePresentation'
import { isSubscriptionStatusNodeName } from '@/domain/network'

const props = defineProps<{
  details: SystemDetails
  tailscaleDetails: TailscaleCapability
  tailscaleStatus: string
  tailscaleEvidence: string
  mihomoInspection: MihomoInspection | null
  publicEgressDetails: PublicEgressCapability
  publicEgressResult: PublicEgressResult | null
  publicEgressLoading: boolean
  publicEgressMessage: string
  icons: {
    activity: Component
    router: Component
  }
}>()

const emit = defineEmits<{
  (event: 'refresh'): void
}>()

const mihomoCapability = computed<MihomoCapability | null>(() => props.mihomoInspection?.capability ?? props.details.proxy.mihomoCapability ?? null)
const mihomoController = computed(() => mihomoCapability.value?.controller ?? null)
const mihomoOperations = computed(() => mihomoController.value?.operations ?? [])
const mihomoRulesEvidence = computed(() => mihomoCapability.value?.evidence.find((item) => item.source === 'controller-api' && item.detail === '/rules'))
const mihomoRulesReadable = computed(() => mihomoRulesEvidence.value?.status === 'reachable')

function hasMihomoOperation(operation: string) {
  return mihomoOperations.value.includes(operation)
}

function mihomoControllerHealth() {
  if (!mihomoController.value?.detected) return '未确认'
  if (mihomoController.value.authRequired) return mihomoController.value.tokenConfigured ? '认证未通过' : '需要认证'
  return mihomoController.value.reachable ? '已连接' : '不可达'
}

function mihomoRulesStatus() {
  return mihomoRulesReadable.value ? '读取 API 已确认' : '未确认'
}

function publicEgressAddressLabel() {
  if (props.publicEgressLoading) return '正在检测…'
  if (!props.publicEgressResult) return props.publicEgressDetails.configured ? '待手动检测' : '未配置探针'
  return props.publicEgressResult.address || '探针未返回公网 IP'
}

function nodeLocationLabel() {
  const node = props.mihomoInspection?.node
  if (!node) return '等待检查'
  return [node.country, node.region].filter(Boolean).join(' · ') || (node.server ? '入口地区需解析后确认' : '等待检查')
}

function strategySelectionDetail() {
  const strategy = props.mihomoInspection?.strategy
  if (!strategy) return '策略组待确认'
  const parts = [strategy.group, strategy.provider, strategy.nodeType ? strategy.nodeType.toUpperCase() : ''].filter(Boolean)
  if (strategy.selectedNode && isSubscriptionStatusNodeName(strategy.selectedNode)) parts.push('名称来自订阅状态提示')
  return parts.join(' · ') || '策略组待确认'
}

function nodeEndpointEvidence() {
  const node = props.mihomoInspection?.node
  if (!node?.server) return '入口 IP 与地区待确认'
  const address = node.resolvedIp || '由 Mihomo 连接时解析'
  return `${address} · ${nodeLocationLabel()}`
}

function proxyRouteExplanation() {
  if (props.mihomoInspection?.localProxy.mode === 'rule') {
    return '公网出口来自本次真实代理请求；规则模式下，不同域名或应用可能命中不同策略，因此“默认策略选择”不代表所有连接。'
  }
  return '公网出口来自本次真实代理请求；节点入口是连接代理节点所用的地址，两者不是同一个概念。'
}

function egressLocationLabel() {
  const value = props.publicEgressResult
  if (!value) return '等待检查'
  return [value.country, value.region].filter(Boolean).join(' · ') || '地区未返回'
}
</script>

<template>
  <article class="panel detail-card proxy-workspace network-layout__full">
    <SectionHeader class="detail-card__section-header" title="代理链路" description="区分 Overlay、本地代理、当前节点和真实公网出口" :icon="icons.router">
      <template #actions>
        <ActionButton size="sm" :icon="icons.activity" :loading="publicEgressLoading" @click="emit('refresh')">刷新链路</ActionButton>
      </template>
    </SectionHeader>
    <div class="proxy-summary">
      <div class="proxy-identity">
        <span :class="['proxy-state', { active: details.proxy.mihomo.detected }]"><i></i>{{ proxyStateLabel(details.proxy.mihomo.state, details.proxy.mihomo.detected) }}</span>
        <div><strong>{{ details.proxy.mihomo.detected ? `Mihomo ${mihomoCapability?.version || ''}` : '未发现 Mihomo / Clash' }}</strong><p>{{ publicEgressLoading ? '正在读取 Controller、当前节点与公网出口…' : `最近检查 ${mihomoInspection ? formatTime(mihomoInspection.checkedAt) : '尚未完成'}` }}</p></div>
      </div>
      <div class="proxy-overlay-note">
        <span>Tailscale Overlay</span>
        <strong>{{ tailscaleStatus }}</strong>
        <code :title="tailscaleEvidence">{{ tailscaleDetails.overlayIps.join('、') || tailscaleEvidence }}</code>
      </div>
      <div class="proxy-route-chain" aria-label="当前代理链路">
        <div class="proxy-route-node">
          <small>NAS 主机</small>
          <strong>{{ details.device.hostname || '本机' }}</strong>
          <code>发起连接</code>
        </div>
        <div class="proxy-route-node">
          <small>本地代理入口</small>
          <strong>{{ mihomoInspection?.localProxy.address || '监听地址待确认' }}</strong>
          <code>{{ mihomoModeLabel(mihomoInspection?.localProxy.mode) }}</code>
        </div>
        <div class="proxy-route-node">
          <small>默认策略选择</small>
          <strong :title="mihomoInspection?.strategy.selectedNode || ''">{{ mihomoInspection?.strategy.selectedNode || '节点待确认' }}</strong>
          <code :title="strategySelectionDetail()">{{ strategySelectionDetail() }}</code>
        </div>
        <div class="proxy-route-node">
          <small>节点入口</small>
          <strong>{{ mihomoInspection?.node.server ? `${mihomoInspection.node.server}:${mihomoInspection.node.port}` : '入口待确认' }}</strong>
          <code :title="nodeEndpointEvidence()">{{ nodeEndpointEvidence() }}</code>
        </div>
        <div class="proxy-route-node proxy-route-node--egress">
          <small>本次检测公网出口</small>
          <strong>{{ publicEgressAddressLabel() }}</strong>
          <code>{{ egressLocationLabel() }}<template v-if="publicEgressResult?.isp"> · {{ publicEgressResult.isp }}</template><template v-if="publicEgressResult?.asn"> · {{ publicEgressResult.asn }}</template></code>
        </div>
      </div>
      <p class="proxy-route-note">{{ proxyRouteExplanation() }}</p>
      <small v-if="publicEgressMessage" class="proxy-message" role="status">{{ publicEgressMessage }}</small>
      <details class="proxy-capabilities">
        <summary>控制器与分流能力 <span>展开技术明细</span></summary>
        <div class="proxy-facts proxy-facts--capabilities">
          <div><small>Controller 健康</small><strong>{{ mihomoControllerHealth() }}</strong><code>{{ mihomoController?.endpoint || '端点未确认' }}</code></div>
          <div><small>连接能力</small><strong>{{ hasMihomoOperation('connections') ? 'API 已确认' : '未确认' }}</strong><code>{{ mihomoController?.authRequired ? '需要认证' : '受控读取能力' }}</code></div>
          <div><small>节点 / 代理组</small><strong>{{ hasMihomoOperation('proxies') ? '读取 API 已确认' : '未确认' }}</strong><code>切换操作需经过写入安全确认</code></div>
          <div><small>规则能力</small><strong>{{ mihomoRulesStatus() }}</strong><code>{{ mihomoRulesReadable ? '当前仅证明读取' : '写入、备份、校验、回滚未开放' }}</code></div>
        </div>
        <p>{{ mihomoRulesReadable ? '已确认控制器可读取规则；域名规则写入将在具备应用前备份、校验和回滚契约后开放。' : '规则 API 未确认；进程和 Docker 容器分流没有可靠归属证据，当前不开放。' }}</p>
      </details>
    </div>
  </article>
</template>

<style scoped>
.network-layout__full { grid-column: 1 / -1; }
.detail-card { align-self: start; overflow: hidden; }
.detail-card__section-header { padding: 17px 18px; border-bottom: 1px solid var(--ncp-line); background: linear-gradient(120deg, var(--ncp-surface), var(--ncp-surface-quiet)); }
.detail-card__section-header :deep(.section-header__icon) { border-color: color-mix(in srgb, var(--ncp-object-network) 20%, var(--ncp-line)); background: var(--ncp-object-network-soft); color: var(--ncp-object-network); }
.proxy-summary { display: grid; gap: 14px; padding: 22px 18px; }
.proxy-identity { display: flex; align-items: center; gap: 12px; }
.proxy-identity > div { display: grid; min-width: 0; gap: 2px; }
.proxy-identity p { overflow: hidden; margin: 0; color: var(--ncp-text-muted); font-size: .82rem; text-overflow: ellipsis; white-space: nowrap; }
.proxy-state { display: inline-flex; width: max-content; flex: 0 0 auto; align-items: center; gap: 7px; padding: 6px 10px; border: 1px solid var(--ncp-neutral-border); border-radius: 999px; background: var(--ncp-neutral-soft); color: var(--ncp-neutral-strong); font-size: .72rem; font-weight: 750; }
.proxy-state.active { border-color: var(--ncp-success-border); background: var(--ncp-success-soft); color: var(--ncp-success-strong); }
.proxy-state i { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
.proxy-overlay-note { display: grid; min-width: 0; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 4px 10px; padding: 10px 12px; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.proxy-overlay-note span { color: var(--ncp-text-subtle); font-size: .68rem; }
.proxy-overlay-note strong { color: var(--ncp-success-strong); font-size: .76rem; text-align: right; }
.proxy-overlay-note code { overflow: hidden; grid-column: 1 / -1; color: var(--ncp-text-muted); font-family: var(--ncp-font-mono); font-size: .7rem; text-overflow: ellipsis; white-space: nowrap; }
.proxy-route-chain { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); counter-reset: proxy-route; gap: 9px; }
.proxy-route-node { position: relative; display: grid; min-width: 0; min-height: 88px; counter-increment: proxy-route; align-content: center; gap: 3px; padding: 11px 13px 11px 48px; border: 1px solid var(--ncp-line); border-radius: 11px; background: var(--ncp-surface); box-shadow: 0 1px 2px rgb(20 42 73 / 3%); }
.proxy-route-node::before { position: absolute; top: 50%; left: 14px; display: grid; width: 24px; height: 24px; place-items: center; border: 1px solid var(--ncp-primary-border); border-radius: 8px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); content: counter(proxy-route); font-family: var(--ncp-font-mono); font-size: .68rem; font-weight: 750; transform: translateY(-50%); }
.proxy-route-node:not(:last-child)::after { position: absolute; top: 50%; right: -10px; width: 9px; height: 1px; background: var(--ncp-primary-border); content: ''; }
.proxy-route-node--egress { border-color: var(--ncp-success-border); background: linear-gradient(135deg, var(--ncp-surface), var(--ncp-success-soft)); }
.proxy-route-node--egress::before { border-color: var(--ncp-success-border); background: var(--ncp-success-soft); color: var(--ncp-success-strong); }
.proxy-route-node small { color: var(--ncp-text-subtle); font-size: .67rem; }
.proxy-route-node strong, .proxy-route-node code { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.proxy-route-node strong { color: var(--ncp-text); font-size: .8rem; }
.proxy-route-node code { color: var(--ncp-text-muted); font-family: var(--ncp-font-mono); font-size: .69rem; }
.proxy-route-note { margin: 0; padding: 10px 12px; border: 1px solid var(--ncp-info-border); border-radius: 10px; background: var(--ncp-info-soft); color: var(--ncp-text-muted); font-size: .7rem; line-height: 1.55; }
.proxy-message { display: block; padding: 9px 11px; border: 1px solid var(--ncp-warning-border); border-radius: 9px; background: var(--ncp-warning-soft); color: var(--ncp-warning-strong); font-size: .7rem; line-height: 1.45; }
.proxy-capabilities { overflow: hidden; border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.proxy-capabilities > summary { display: flex; min-height: 42px; align-items: center; gap: 9px; padding: 0 12px; cursor: pointer; color: var(--ncp-text-muted); font-size: .73rem; font-weight: 750; list-style: none; }
.proxy-capabilities > summary::-webkit-details-marker { display: none; }
.proxy-capabilities > summary::before { content: '+'; display: grid; width: 20px; height: 20px; place-items: center; border: 1px solid var(--ncp-line-strong); border-radius: 6px; background: var(--ncp-surface); color: var(--ncp-primary-strong); font-size: .9rem; }
.proxy-capabilities[open] > summary::before { content: '−'; }
.proxy-capabilities > summary span { margin-left: auto; color: var(--ncp-text-subtle); font-size: .68rem; font-weight: 600; }
.proxy-facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.proxy-capabilities .proxy-facts { padding: 0 10px 10px; }
.proxy-facts > div { display: grid; min-width: 0; gap: 3px; min-height: 68px; align-content: center; padding: 10px; border: 1px solid var(--ncp-line); border-radius: 9px; background: var(--ncp-surface); }
.proxy-facts small { color: var(--ncp-text-subtle); font-size: .68rem; }
.proxy-facts strong, .proxy-facts code { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.proxy-facts strong { color: var(--ncp-text); font-family: var(--ncp-font-latin); font-size: .78rem; }
.proxy-facts code { color: var(--ncp-text-muted); font-family: var(--ncp-font-mono); font-size: .7rem; letter-spacing: 0; }
.proxy-capabilities > p { margin: 0; padding: 0 12px 12px; color: var(--ncp-text-subtle); font-size: .69rem; line-height: 1.5; }
@media (max-width: 1050px) { .proxy-route-chain { grid-template-columns: 1fr; } .proxy-route-node { min-height: auto; } .proxy-route-node:not(:last-child)::after { top: auto; right: auto; bottom: -10px; left: 25px; width: 1px; height: 9px; } }
@media (max-width: 760px) { .proxy-identity { align-items: flex-start; flex-direction: column; gap: 8px; } .proxy-capabilities .proxy-facts { grid-template-columns: 1fr; } }
</style>
