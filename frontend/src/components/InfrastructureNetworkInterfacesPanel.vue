<script setup lang="ts">
import type { Component } from 'vue'

import type { SystemDetails } from '@/api/system'
import {
  interfaceAddress,
  interfaceIsOnline,
  interfaceStateLabel,
} from '@/domain/infrastructurePresentation'
import { networkInterfaceKindLabel } from '@/domain/network'

type NetworkInterface = SystemDetails['network']['interfaces'][number]

defineProps<{
  details: SystemDetails
  primaryInterfaces: readonly NetworkInterface[]
  secondaryInterfaces: readonly NetworkInterface[]
  primaryActiveInterfaceCount: number
  listeningPortCount: number
  exposedListenerCount: number
  localListenerCount: number
  icons: {
    gauge: Component
    network: Component
    route: Component
    wifi: Component
  }
}>()
</script>

<template>
  <article class="network-summary-grid network-layout__full">
    <div class="network-summary-card panel">
      <span class="network-summary-card__icon"><component :is="icons.wifi" :size="18" /></span>
      <div><small>当前联网</small><strong>{{ primaryActiveInterfaceCount }} 个主接口</strong><p>{{ primaryInterfaces.map((item) => item.name).join('、') || '未发现主网络接口' }}</p></div>
    </div>
    <div class="network-summary-card panel">
      <span class="network-summary-card__icon"><component :is="icons.route" :size="18" /></span>
      <div><small>默认出口</small><strong>{{ details.network.gateway || '未发现' }}</strong><p>路由 {{ details.network.routes.length }} 条</p></div>
    </div>
    <div class="network-summary-card panel">
      <span class="network-summary-card__icon"><component :is="icons.network" :size="18" /></span>
      <div><small>DNS 服务</small><strong>{{ details.network.dnsServers.length || 0 }} 个</strong><p>{{ details.network.dnsServers.slice(0, 2).join('、') || '未发现解析服务' }}</p></div>
    </div>
    <div class="network-summary-card panel">
      <span class="network-summary-card__icon"><component :is="icons.gauge" :size="18" /></span>
      <div><small>监听服务</small><strong>{{ listeningPortCount }} 个端口</strong><p>{{ exposedListenerCount }} 个可能对外可达 · {{ localListenerCount }} 个仅本机</p></div>
    </div>
  </article>

  <article class="panel detail-card network-layout__full">
    <header class="detail-card__header type-site">
      <component :is="icons.network" :size="20" />
      <div><h2>主要网络连接</h2><p>展示主机和 Tailscale Overlay 接口；Docker 等内部接口收进下方明细</p></div>
    </header>
    <div v-if="primaryInterfaces.length" class="primary-interface-list">
      <div v-for="item in primaryInterfaces" :key="item.name" class="primary-interface-row">
        <span class="primary-interface-row__status" :class="{ online: interfaceIsOnline(item) }"><i></i>{{ interfaceStateLabel(item) }}</span>
        <div class="primary-interface-row__name"><strong>{{ item.name }}</strong><small>{{ networkInterfaceKindLabel(item.name) }}</small></div>
        <div><span>IP 地址</span><strong>{{ interfaceAddress(item) ? `${interfaceAddress(item)?.address}/${interfaceAddress(item)?.prefixLength}` : '未分配' }}</strong></div>
        <div><span>链路</span><strong>{{ item.speedMbps > 0 ? `${item.speedMbps} Mbps` : '速率未知' }}<template v-if="item.duplex"> · {{ item.duplex }}</template></strong></div>
      </div>
    </div>
    <div v-else class="inline-empty">未发现可用于联网的主机接口。</div>
    <details v-if="secondaryInterfaces.length" class="network-details">
      <summary>查看所有接口明细 <span>{{ secondaryInterfaces.length }} 个虚拟 / 辅助接口</span></summary>
      <div class="network-details__list">
        <div v-for="item in secondaryInterfaces" :key="item.name" class="network-detail-row">
          <div><strong>{{ item.name }}</strong><small>{{ networkInterfaceKindLabel(item.name) }}</small></div>
          <span :class="{ 'is-online': interfaceIsOnline(item) }">{{ interfaceStateLabel(item) }}</span>
          <span>{{ item.addresses.length }} 个地址</span>
          <code>{{ item.hardwareAddress || '无 MAC' }}</code>
        </div>
      </div>
    </details>
  </article>
</template>

<style scoped>
.network-layout__full {
  grid-column: 1 / -1;
}

.detail-card {
  overflow: hidden;
}

.network-summary-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.network-summary-card {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: 10px;
  padding: 15px;
}

.network-summary-card__icon {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border-radius: 10px;
  background: var(--ncp-object-network-soft);
  color: var(--ncp-object-network);
}

.network-summary-card > div {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.network-summary-card small {
  color: var(--ncp-text-subtle);
  font-size: .7rem;
}

.network-summary-card strong {
  overflow: hidden;
  color: var(--ncp-text);
  font-family: var(--ncp-font-latin);
  font-size: .94rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.network-summary-card p {
  overflow: hidden;
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: .72rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-card__header {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 17px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: linear-gradient(135deg, var(--ncp-surface), var(--ncp-surface-quiet));
}

.detail-card__header > svg {
  box-sizing: content-box;
  padding: 9px;
  border-radius: 11px;
  background: var(--ncp-object-site-soft);
  color: var(--ncp-object-site);
}

.detail-card__header h2 {
  margin: 0;
  font-size: 1rem;
}

.detail-card__header p {
  margin: 3px 0 0;
  color: var(--ncp-text-subtle);
  font-size: .78rem;
}

.primary-interface-list {
  display: grid;
  padding: 6px 18px;
}

.primary-interface-row {
  display: grid;
  grid-template-columns: 118px minmax(140px, 1.1fr) minmax(180px, 1.4fr) minmax(150px, 1fr);
  align-items: center;
  gap: 14px;
  min-height: 72px;
  border-bottom: 1px solid var(--ncp-line);
}

.primary-interface-row:last-child {
  border-bottom: 0;
}

.primary-interface-row__status {
  display: inline-flex;
  width: max-content;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  border: 1px solid var(--ncp-line);
  border-radius: 999px;
  background: var(--ncp-surface-quiet);
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  font-weight: 720;
}

.primary-interface-row__status.online {
  border-color: var(--ncp-success-border);
  background: var(--ncp-success-soft);
  color: var(--ncp-success-strong);
}

.primary-interface-row__status i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.primary-interface-row > div:not(.primary-interface-row__name) {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.primary-interface-row__name {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.primary-interface-row strong {
  overflow: hidden;
  font-family: var(--ncp-font-latin);
  font-size: .83rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.primary-interface-row small,
.primary-interface-row > div:not(.primary-interface-row__name) > span {
  color: var(--ncp-text-subtle);
  font-size: .7rem;
}

.primary-interface-row > div:not(.primary-interface-row__name) > strong {
  font-family: var(--ncp-font-mono);
  font-size: .76rem;
  font-weight: 600;
}

.network-details {
  border-top: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.network-details summary {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 18px;
  cursor: pointer;
  color: var(--ncp-primary-strong);
  font-size: .78rem;
  font-weight: 750;
  list-style: none;
}

.network-details summary::-webkit-details-marker {
  display: none;
}

.network-details summary::before {
  display: grid;
  width: 22px;
  height: 22px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--ncp-primary-border);
  border-radius: 6px;
  background: var(--ncp-primary-soft);
  content: '+';
  font-size: 1rem;
  line-height: 1;
}

.network-details[open] summary::before {
  content: '−';
}

.network-details summary span {
  margin-left: auto;
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  font-weight: 600;
}

.network-details__list {
  display: grid;
  border-top: 1px solid var(--ncp-line);
}

.network-detail-row {
  display: grid;
  grid-template-columns: 1.2fr 90px 90px 1fr;
  align-items: center;
  gap: 12px;
  min-height: 54px;
  padding: 8px 18px;
  border-bottom: 1px solid var(--ncp-line);
}

.network-detail-row:last-child {
  border-bottom: 0;
}

.network-detail-row > div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.network-detail-row strong,
.network-detail-row code {
  overflow: hidden;
  font-family: var(--ncp-font-mono);
  font-size: .75rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.network-detail-row small,
.network-detail-row > span {
  color: var(--ncp-text-subtle);
  font-size: .7rem;
}

.network-detail-row > span.is-online {
  color: var(--ncp-success-strong);
  font-weight: 700;
}

.network-detail-row code {
  color: var(--ncp-text-muted);
}

.inline-empty {
  padding: 28px 18px;
  color: var(--ncp-text-subtle);
  text-align: center;
}

@media (max-width: 1050px) {
  .network-summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 760px) {
  .primary-interface-row {
    grid-template-columns: 118px minmax(140px, 1.1fr) minmax(180px, 1.4fr) minmax(150px, 1fr);
  }

  .network-detail-row {
    grid-template-columns: 1.2fr 90px 90px 1fr;
  }
}

@media (max-width: 520px) {
  .network-summary-grid {
    grid-template-columns: 1fr;
  }

  .detail-card__header {
    align-items: flex-start;
  }

  .primary-interface-row,
  .network-detail-row {
    grid-template-columns: 1fr;
    gap: 5px;
    padding-block: 12px;
  }

  .primary-interface-row__status,
  .network-detail-row > span,
  .network-detail-row code {
    grid-column: 1;
  }
}
</style>
