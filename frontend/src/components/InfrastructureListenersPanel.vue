<script setup lang="ts">
import { type Component } from 'vue'
import { ElInput, ElTooltip } from 'element-plus'
import { Search } from '@lucide/vue'

import ActionButton from '@/components/ActionButton.vue'
import NcpSelect, { type NcpSelectOption } from '@/components/NcpSelect.vue'
import SectionHeader from '@/components/SectionHeader.vue'
import {
  listeningSourceLabel,
} from '@/domain/infrastructurePresentation'
import type { ListenerScopeFilter, ListeningPortGroup } from '@/composables/useInfrastructureNetwork'

defineProps<{
  listenerQuery: string
  listenerProtocol: string
  listenerScope: ListenerScopeFilter
  listenerProtocolOptions: NcpSelectOption[]
  listenerScopeOptions: NcpSelectOption[]
  listeningPortGroups: ListeningPortGroup[]
  filteredListeningPortGroups: ListeningPortGroup[]
  visibleListeningPortGroups: ListeningPortGroup[]
  exposedListenerCount: number
  localListenerCount: number
  listenerResultLabel: string
  icon: Component
}>()

const emit = defineEmits<{
  (event: 'update:listenerQuery', value: string): void
  (event: 'update:listenerProtocol', value: string): void
  (event: 'update:listenerScope', value: ListenerScopeFilter): void
  (event: 'show-more'): void
}>()

function updateListenerScope(value: string) {
  emit('update:listenerScope', value as ListenerScopeFilter)
}
</script>

<template>
  <article class="panel detail-card ports-workspace-card network-layout__full">
    <SectionHeader class="detail-card__section-header" title="监听服务" description="按端口合并归属信息，并优先展示对外监听的服务" :icon="icon">
      <template #actions><span class="listener-count">{{ listenerResultLabel }}</span></template>
    </SectionHeader>
    <div v-if="listeningPortGroups.length" class="port-workspace">
      <div class="port-toolbar">
        <ElInput
          :model-value="listenerQuery"
          clearable
          aria-label="搜索监听服务"
          placeholder="搜索端口、进程、容器或地址"
          @update:model-value="emit('update:listenerQuery', String($event))"
        >
          <template #prefix><Search :size="16" /></template>
        </ElInput>
        <NcpSelect :model-value="listenerProtocol" :options="listenerProtocolOptions" accessible-label="筛选监听协议" @update:model-value="emit('update:listenerProtocol', $event)" />
        <NcpSelect :model-value="listenerScope" :options="listenerScopeOptions" accessible-label="筛选监听范围" @update:model-value="updateListenerScope" />
      </div>
      <div class="port-risk-summary" role="note">
        <span class="port-risk-summary__item port-risk-summary__item--warning"><i></i><strong>{{ exposedListenerCount }}</strong> 个端口可能对外可达</span>
        <span class="port-risk-summary__item"><i></i><strong>{{ localListenerCount }}</strong> 个端口仅允许本机访问</span>
        <small>“公网地址”或“所有接口”需要重点检查；实际可达范围仍受路由、防火墙与端口转发控制。</small>
      </div>
      <div v-if="visibleListeningPortGroups.length" class="port-grid">
        <article v-for="item in visibleListeningPortGroups" :key="`${item.protocol}-${item.port}`" class="port-card">
          <div class="port-card__endpoint">
            <b>{{ item.port }}</b>
            <span>{{ item.protocol.toUpperCase() }}</span>
          </div>
          <div class="port-card__fact">
            <small>进程 / 服务 / 容器</small>
            <ElTooltip
              :content="item.owners.map((owner) => `${owner.label} · ${owner.detail}`).join('；') || `PID ${item.pids.join('、') || '未知'}`"
              placement="top"
              :show-after="350"
            >
              <strong>{{ item.owners.map((owner) => owner.label).join('、') || (item.pids.length ? `PID ${item.pids.join('、')}` : '未识别') }}</strong>
            </ElTooltip>
            <span>{{ item.owners.map((owner) => owner.detail).join('、') || '未取得进程归属' }}</span>
          </div>
          <div class="port-card__fact">
            <small>监听地址</small>
            <ElTooltip :content="item.addresses.join('、') || '*'" placement="top" :show-after="350">
              <code>{{ item.addresses.join('、') || '*' }}</code>
            </ElTooltip>
            <span>{{ item.addresses.some((address) => /^(0\.0\.0\.0|::|\[::\])/.test(address)) ? '所有网络接口' : '指定网络接口' }}</span>
          </div>
          <div class="port-card__fact">
            <small>访问范围</small>
            <span class="listener-scope" :class="`listener-scope--${item.scope.tone}`"><i></i>{{ item.scope.label }}</span>
            <ElTooltip :content="`${item.scope.description}；识别证据：${listeningSourceLabel(item.sources)}`" placement="top" :show-after="350">
              <span>{{ item.scope.description }}</span>
            </ElTooltip>
          </div>
        </article>
      </div>
      <div v-else class="inline-empty">没有符合当前搜索和协议条件的监听服务。</div>
      <div v-if="visibleListeningPortGroups.length" class="port-results-footer">
        <span>已显示 {{ visibleListeningPortGroups.length }} / {{ filteredListeningPortGroups.length }} 个端口</span>
        <ActionButton v-if="visibleListeningPortGroups.length < filteredListeningPortGroups.length" size="sm" @click="emit('show-more')">再显示 {{ Math.min(24, filteredListeningPortGroups.length - visibleListeningPortGroups.length) }} 个</ActionButton>
      </div>
    </div>
    <div v-else class="inline-empty">未取得监听服务信息。</div>
  </article>
</template>

<style scoped>
.network-layout__full { grid-column: 1 / -1; }
.detail-card { align-self: start; overflow: hidden; }
.detail-card__section-header { padding: 17px 18px; border-bottom: 1px solid var(--ncp-line); background: linear-gradient(120deg, var(--ncp-surface), var(--ncp-surface-quiet)); }
.detail-card__section-header :deep(.section-header__icon) { border-color: color-mix(in srgb, var(--ncp-object-network) 20%, var(--ncp-line)); background: var(--ncp-object-network-soft); color: var(--ncp-object-network); }
.port-workspace { border-top: 1px solid var(--ncp-line); }
.port-toolbar { display: grid; grid-template-columns: minmax(240px, 1fr) 150px 190px; align-items: center; gap: 10px; padding: 13px 18px; border-bottom: 1px solid var(--ncp-line); background: var(--ncp-surface); }
.listener-count { color: var(--ncp-text-subtle); font-size: .72rem; font-weight: 650; white-space: nowrap; }
.port-risk-summary { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 14px; padding: 10px 18px; border-bottom: 1px solid var(--ncp-line); background: var(--ncp-surface-quiet); }
.port-risk-summary__item { display: inline-flex; align-items: center; gap: 6px; color: var(--ncp-text-muted); font-size: .72rem; }
.port-risk-summary__item i, .listener-scope i { width: 6px; height: 6px; flex: 0 0 auto; border-radius: 50%; background: currentColor; }
.port-risk-summary__item strong { color: var(--ncp-text); font-family: var(--ncp-font-data); }
.port-risk-summary__item--warning { color: var(--ncp-warning-strong); }
.port-risk-summary > small { margin-left: auto; color: var(--ncp-text-subtle); font-size: .67rem; }
.port-toolbar :deep(.el-input__wrapper) { min-height: var(--ncp-control-height); border-radius: var(--ncp-radius-control); background: var(--ncp-control-surface); box-shadow: 0 0 0 1px var(--ncp-control-border) inset; }
.port-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; padding: 14px 18px 18px; background: var(--ncp-surface-quiet); }
.port-card { display: grid; min-width: 0; grid-template-columns: 72px minmax(150px, 1.3fr) minmax(130px, 1fr) minmax(120px, .9fr); align-items: center; gap: 14px; padding: 14px; border: 1px solid var(--ncp-line); border-radius: 12px; background: var(--ncp-surface); box-shadow: 0 1px 2px rgb(20 42 73 / 3%); transition: border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.port-card:hover { border-color: var(--ncp-primary-border); box-shadow: var(--ncp-shadow-control); transform: translateY(-1px); }
.port-card__endpoint { display: grid; min-width: 0; justify-items: start; gap: 5px; }
.port-card__endpoint b { color: var(--ncp-object-network); font-family: var(--ncp-font-mono); font-size: 1rem; }
.port-card__endpoint span { padding: 3px 6px; border-radius: 6px; background: var(--ncp-object-network-soft); color: var(--ncp-object-network); font-family: var(--ncp-font-mono); font-size: .65rem; font-weight: 750; }
.port-card__fact { display: grid; min-width: 0; gap: 3px; }
.port-card__fact small, .port-card__fact span { overflow: hidden; color: var(--ncp-text-subtle); font-size: .68rem; text-overflow: ellipsis; white-space: nowrap; }
.port-card__fact strong, .port-card__fact code { display: block; overflow: hidden; color: var(--ncp-text); font-family: var(--ncp-font-latin); font-size: .78rem; font-weight: 700; letter-spacing: 0; text-overflow: ellipsis; white-space: nowrap; }
.listener-scope { display: inline-flex !important; width: max-content; max-width: 100%; min-height: 24px; align-items: center; gap: 6px; padding: 3px 8px; border: 1px solid var(--ncp-neutral-border); border-radius: var(--ncp-radius-pill); background: var(--ncp-neutral-soft); color: var(--ncp-neutral-strong) !important; font-size: .68rem !important; font-weight: 720; }
.listener-scope--danger { border-color: var(--ncp-danger-border); background: var(--ncp-danger-soft); color: var(--ncp-danger-strong) !important; }
.listener-scope--warning { border-color: var(--ncp-warning-border); background: var(--ncp-warning-soft); color: var(--ncp-warning-strong) !important; }
.listener-scope--info { border-color: var(--ncp-info-border); background: var(--ncp-info-soft); color: var(--ncp-info-strong) !important; }
.listener-scope--success { border-color: var(--ncp-success-border); background: var(--ncp-success-soft); color: var(--ncp-success-strong) !important; }
.port-results-footer { display: flex; min-height: 58px; align-items: center; justify-content: space-between; gap: var(--ncp-space-3); padding: 8px 18px; border-top: 1px solid var(--ncp-line); background: var(--ncp-surface); }
.port-results-footer > span { color: var(--ncp-text-subtle); font-size: .72rem; }
.port-card__fact code { color: var(--ncp-text-muted); font-family: var(--ncp-font-mono); font-size: .72rem; font-weight: 600; }
.inline-empty { padding: 28px 18px; color: var(--ncp-text-subtle); text-align: center; }
@media (max-width: 1480px) { .port-grid { grid-template-columns: 1fr; } }
@media (max-width: 760px) { .port-toolbar { grid-template-columns: minmax(0, 1fr) 140px 170px; padding-inline: 15px; } .port-grid { grid-template-columns: 1fr; padding-inline: 15px; } .port-card { grid-template-columns: 64px minmax(0, 1fr) minmax(0, 1fr); } .port-card__fact:last-child { grid-column: 2 / -1; } }
@media (max-width: 520px) { .detail-card__section-header { padding-inline: 15px; } .port-toolbar { grid-template-columns: 1fr; } .port-risk-summary { align-items: flex-start; flex-direction: column; } .port-risk-summary > small { margin-left: 0; line-height: 1.5; } .port-results-footer { align-items: stretch; flex-direction: column; padding-block: 12px; } .port-card { grid-template-columns: 58px minmax(0, 1fr); } .port-card__fact { grid-column: 2; } }
@media (prefers-reduced-motion: reduce) { .port-card { transition: none; } .port-card:hover { transform: none; } }
</style>
