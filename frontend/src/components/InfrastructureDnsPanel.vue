<script setup lang="ts">
import { computed, type Component } from 'vue'

import type { DNSCapability, SystemDetails } from '@/api/system'
import ActionButton from '@/components/ActionButton.vue'
import SectionHeader from '@/components/SectionHeader.vue'

const props = defineProps<{
  details: SystemDetails
  dnsDetails: DNSCapability
  routeIcon: Component
}>()

const emit = defineEmits<{
  (event: 'edit'): void
}>()

const dnsCapabilityExplanation = computed(() => {
  if (!props.dnsDetails.readOnly && props.dnsDetails.backend === 'ugos-network-service') return '通过 UGOS 官方网络服务预览和应用；修改前保存完整配置，且支持并发变更保护与一键回滚。'
  if (!props.dnsDetails.readOnly && props.dnsDetails.backend === 'static-resolv-conf') return '修改前自动备份，确认后原子应用；若配置未被其他进程改动，可一键回滚。'
  if (props.dnsDetails.errorCode === 'DNS_BACKEND_READ_ONLY') return '检测到静态 /etc/resolv.conf，未发现可管理的 systemd-resolved 或 NetworkManager；保持只读。'
  if (props.dnsDetails.errorCode === 'UGOS_DNS_WRITE_UNCONFIRMED') return '已检测到 UGOS 网络服务，但当前固件拒绝了受控写入；为避免伪成功，DNS 保持只读，请在 UGOS 网络设置中修改。'
  if (props.dnsDetails.errorCode === 'DNS_WRITE_ADAPTER_UNAVAILABLE') return '检测到 DNS 后端，但未接入安全的预览、应用和回滚适配器；保持只读。'
  if (props.dnsDetails.readOnly) return '当前 DNS 后端只提供读取能力；NCP 不会直接覆盖 /etc/resolv.conf。'
  return props.dnsDetails.detectionSource || 'DNS 能力未报告'
})
</script>

<template>
  <article class="panel detail-card dns-workspace">
    <SectionHeader class="detail-card__section-header" title="路由与 DNS" description="默认网关保持只读；DNS 由已确认的系统后端安全管理" :icon="routeIcon">
      <template #actions>
        <ActionButton
          v-if="dnsDetails.canPreview && dnsDetails.canConfirm && dnsDetails.canRollback && !dnsDetails.readOnly"
          size="sm"
          @click="emit('edit')"
        >编辑 DNS</ActionButton>
      </template>
    </SectionHeader>
    <dl class="definition-grid">
      <div class="definition-grid__wide"><dt>默认网关</dt><dd>{{ details.network.gateway || '未发现' }}</dd></div>
      <div class="definition-grid__wide"><dt>DNS</dt><dd>{{ details.network.dnsServers.join('、') || '未发现' }}</dd></div>
      <div><dt>路由数量</dt><dd>{{ details.network.routes.length }}</dd></div>
      <div><dt>默认出口</dt><dd>{{ details.network.routes.find((route) => route.destination === '0.0.0.0/0')?.interface || '未识别' }}</dd></div>
    </dl>
    <div class="dns-management">
      <div class="dns-management__state">
        <span :class="['capability-state', { off: !dnsDetails.detected || dnsDetails.readOnly }]"><i></i>{{ dnsDetails.readOnly ? '只读展示' : dnsDetails.detected ? '支持安全修改' : '未检测到可管理后端' }}</span>
        <small>{{ dnsCapabilityExplanation }}</small>
      </div>
      <div class="dns-current-grid">
        <div class="dns-current-value"><span>当前生效 DNS</span><code>{{ dnsDetails.nameservers.join('、') || '未读取到 DNS 地址' }}</code></div>
        <div v-if="!dnsDetails.readOnly" class="dns-current-value dns-current-value--managed"><span>UGOS 手动配置</span><code>{{ dnsDetails.configuredNameservers?.join('、') || '未读取到可编辑配置' }}</code></div>
      </div>
    </div>
  </article>
</template>

<style scoped>
.detail-card {
  overflow: hidden;
}

.detail-card__section-header {
  padding: 17px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: linear-gradient(120deg, var(--ncp-surface), var(--ncp-surface-quiet));
}

.detail-card__section-header :deep(.section-header__icon) {
  border-color: color-mix(in srgb, var(--ncp-object-network) 20%, var(--ncp-line));
  background: var(--ncp-object-network-soft);
  color: var(--ncp-object-network);
}

.definition-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  margin: 0;
  padding: 8px 18px 16px;
}

.definition-grid > div {
  min-width: 0;
  padding: 13px 4px;
  border-bottom: 1px solid var(--ncp-line);
}

.definition-grid__wide {
  grid-column: 1 / -1;
}

.definition-grid dt {
  color: var(--ncp-text-subtle);
  font-size: .75rem;
}

.definition-grid dd {
  display: block;
  overflow: hidden;
  margin: 5px 0 0;
  color: var(--ncp-text);
  font-family: var(--ncp-font-latin);
  font-size: .88rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dns-management {
  display: grid;
  gap: 12px;
  padding: 14px 18px 18px;
  border-top: 1px solid var(--ncp-line);
}

.dns-management__state {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.capability-state {
  display: inline-flex;
  width: max-content;
  flex: 0 0 auto;
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

.dns-management__state small {
  max-width: 68%;
  color: var(--ncp-text-subtle);
  font-size: .7rem;
  line-height: 1.55;
  text-align: right;
}

.dns-current-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 9px;
}

.dns-current-grid > .dns-current-value:only-child {
  grid-column: 1 / -1;
}

.dns-current-value {
  display: grid;
  min-width: 0;
  gap: 5px;
  padding: 12px 13px;
  border: 1px solid var(--ncp-line);
  border-radius: 10px;
  background: var(--ncp-surface-quiet);
}

.dns-current-value--managed {
  border-color: var(--ncp-primary-border);
  background: color-mix(in srgb, var(--ncp-primary-soft) 62%, var(--ncp-surface));
}

.dns-current-value span {
  color: var(--ncp-text-subtle);
  font-size: .68rem;
}

.dns-current-value code {
  overflow-wrap: anywhere;
  color: var(--ncp-text);
  font-family: var(--ncp-font-mono);
  font-size: .76rem;
  line-height: 1.5;
}

@media (max-width: 760px) {
  .dns-management__state {
    flex-direction: column;
    gap: 5px;
  }

  .dns-management__state small {
    max-width: none;
    text-align: left;
  }
}

@media (max-width: 620px) {
  .dns-current-grid,
  .definition-grid {
    grid-template-columns: 1fr;
  }

  .definition-grid__wide {
    grid-column: auto;
  }
}
</style>
