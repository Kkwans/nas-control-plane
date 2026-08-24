<script setup lang="ts">
import type { Component } from 'vue'

import type { SystemDetails } from '@/api/system'
import SectionHeader from '@/components/SectionHeader.vue'

interface CapabilityItem {
  name: string
  enabled: boolean
  detail: string
  icon: Component
  type: string
}

defineProps<{
  details: SystemDetails
  capabilityItems: readonly CapabilityItem[]
  formatTime: (value: string) => string
  controlChainIcon: Component
}>()
</script>

<template>
  <section class="services-layout">
    <article class="panel detail-card services-layout__full">
      <SectionHeader class="detail-card__section-header" title="控制链路" description="Web 控制台至 Root Agent 的真实请求路径" :icon="controlChainIcon" />
      <ol class="control-chain">
        <li v-for="(node, index) in details.control.nodes" :key="node.id">
          <span class="control-chain__index">{{ index + 1 }}</span>
          <div><strong>{{ node.name }}</strong><small>{{ node.detail }}</small></div>
          <div class="control-chain__meta">
            <span :class="`status-${node.status}`">{{ node.status === 'ready' ? '正常' : node.status || '未知' }}</span>
            <small>{{ node.version || '版本不可用' }} · {{ formatTime(node.lastSeen) }}</small>
          </div>
        </li>
      </ol>
    </article>

    <article v-for="item in capabilityItems" :key="item.name" class="panel capability-card">
      <span :class="['capability-card__icon', `type-${item.type}`]"><component :is="item.icon" :size="21" /></span>
      <div><strong>{{ item.name }}</strong><small>{{ item.detail }}</small></div>
      <span :class="['capability-state', { off: !item.enabled }]"><i></i>{{ item.enabled ? '可用' : '不可用' }}</span>
    </article>
  </section>
</template>

<style scoped>
.services-layout {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.services-layout__full {
  grid-column: 1 / -1;
}

.detail-card {
  overflow: hidden;
}

.detail-card__section-header {
  padding: 17px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: linear-gradient(120deg, var(--ncp-surface), var(--ncp-surface-quiet));
}

.detail-card__section-header :deep(.section-header__icon) {
  border-color: color-mix(in srgb, var(--ncp-object-system) 20%, var(--ncp-line));
  background: var(--ncp-object-system-soft);
  color: var(--ncp-object-system);
}

.control-chain {
  display: flex;
  align-items: stretch;
  gap: 0;
  margin: 0;
  padding: 20px;
  list-style: none;
}

.control-chain li {
  position: relative;
  display: grid;
  min-width: 0;
  flex: 1;
  grid-template-columns: 36px minmax(0, 1fr);
  align-items: start;
  gap: 10px;
  padding-right: 22px;
}

.control-chain li:not(:last-child)::after {
  position: absolute;
  top: 17px;
  right: 4px;
  left: 46px;
  height: 1px;
  background: var(--ncp-line-strong);
  content: '';
}

.control-chain__index {
  position: relative;
  z-index: 1;
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border: 1px solid var(--ncp-primary-border);
  border-radius: 10px;
  background: var(--ncp-surface);
  color: var(--ncp-primary-strong);
  font-family: var(--ncp-font-mono);
  font-weight: 750;
}

.control-chain li > div:nth-child(2) {
  position: relative;
  z-index: 1;
  display: grid;
  gap: 3px;
  background: var(--ncp-surface);
}

.control-chain li small {
  color: var(--ncp-text-subtle);
  font-size: .72rem;
}

.control-chain__meta {
  display: grid;
  grid-column: 2;
  gap: 3px;
  margin-top: 7px;
}

.control-chain__meta > span {
  width: max-content;
  color: var(--ncp-neutral-strong);
  font-size: .7rem;
  font-weight: 700;
}

.control-chain__meta > .status-ready {
  color: var(--ncp-success-strong);
}

.capability-card {
  display: grid;
  grid-template-columns: 44px minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 16px;
}

.capability-card__icon {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border-radius: 12px;
}

.type-docker { background: var(--ncp-object-docker-soft); color: var(--ncp-object-docker); }
.type-database { background: var(--ncp-engine-sqlite-soft); color: var(--ncp-engine-sqlite); }
.type-system { background: var(--ncp-object-system-soft); color: var(--ncp-object-system); }
.type-storage { background: var(--ncp-object-storage-soft); color: var(--ncp-object-storage); }
.type-network { background: var(--ncp-object-network-soft); color: var(--ncp-object-network); }

.capability-card > div {
  display: grid;
  min-width: 0;
  gap: 3px;
}

.capability-card small {
  overflow: hidden;
  color: var(--ncp-text-subtle);
  font-size: .75rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.capability-state {
  display: inline-flex;
  width: max-content;
  align-items: center;
  gap: 6px;
  color: var(--ncp-success-strong);
  font-size: .72rem;
  font-weight: 700;
}

.capability-state i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}

.capability-state.off {
  color: var(--ncp-neutral-strong);
}

@media (max-width: 1050px) {
  .control-chain {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px;
  }

  .control-chain li {
    padding: 0;
  }

  .control-chain li::after {
    display: none !important;
  }
}

@media (max-width: 760px) {
  .services-layout {
    grid-template-columns: 1fr;
  }

  .control-chain {
    grid-template-columns: 1fr;
  }
}
</style>
