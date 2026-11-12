<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Bell, Cpu, MemoryStick } from '@lucide/vue'
import StatusPill from '@/components/StatusPill.vue'
import { useResourceAlerts } from '@/composables/useResourceAlerts'
import { alertPresentation } from '@/domain/monitoring/alerts'

const { snapshot, loading, errorCode, now, refresh } = useResourceAlerts()
const cards = computed(() => snapshot.value?.enabled ? (['cpu', 'memory'] as const).map((kind) => {
  const rule = snapshot.value!.rules[kind]
  return { kind, name: kind === 'cpu' ? 'CPU 持续高使用率' : '内存持续高使用率', rule, ...alertPresentation(rule, now.value, Boolean(errorCode.value)) }
}) : [])
const stableStatus = computed(() => {
  if (errorCode.value) return snapshot.value ? '当前资源提示刷新失败，显示上次结果。' : '当前资源提示读取失败。'
  if (!snapshot.value) return ''
  if (!snapshot.value.enabled) return '当前资源提示未启用。'
  return cards.value.map((card) => `${card.name}：${card.label}。${card.current ? '' : card.detail}`).join(' ')
})
const announcement = ref('')
// 数值、响应时间和观察起点变化不会重播；仅稳定的状态文本变化时通知。
watch(stableStatus, (value, previous) => { if (value !== previous) announcement.value = value })
function formatTime(value: string | null) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '暂无' }
</script>

<template>
  <section class="resource-alerts panel" aria-labelledby="resource-alerts-title">
    <header class="resource-alerts__header">
      <div><h2 id="resource-alerts-title"><Bell :size="18" aria-hidden="true" />当前资源提示</h2><p>固定 CPU / 内存规则，与下方历史范围无关。</p></div>
      <button type="button" :disabled="loading" @click="refresh">{{ loading ? '读取中…' : '刷新当前提示' }}</button>
    </header>
    <p class="resource-alerts__live" role="status" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <div v-if="errorCode" class="resource-alerts__error">
      <strong>{{ snapshot ? '当前提示刷新失败' : '当前提示读取失败' }}</strong>
      <p>{{ snapshot ? '保留上次读取结果，不能作为当前资源状态。' : '暂时无法读取当前资源提示，请确认 Server 可用后重试。' }}</p>
      <span>代码 {{ errorCode }}</span>
      <button type="button" :disabled="loading" @click="refresh">重试当前提示</button>
    </div>
    <p v-if="!snapshot && loading" class="resource-alerts__notice">正在读取当前资源提示…</p>
    <div v-else-if="snapshot && !snapshot.enabled" class="resource-alerts__notice">
      <StatusPill label="未启用" tone="neutral" role="none" aria-live="off" />
      <p>可选资源提示默认关闭。仅在 Server 启动时配置启用，本页只读；未启用不表示资源正常。</p>
    </div>
    <div v-else-if="snapshot" class="resource-alerts__grid">
      <article v-for="card in cards" :key="card.kind" class="resource-alerts__rule" :aria-labelledby="`alert-${card.kind}-title`">
        <div class="resource-alerts__rule-heading">
          <h3 :id="`alert-${card.kind}-title`"><component :is="card.kind === 'cpu' ? Cpu : MemoryStick" :size="17" aria-hidden="true" />{{ card.name }}</h3>
          <StatusPill :label="card.label" :tone="card.tone" role="none" aria-live="off" />
        </div>
        <p class="resource-alerts__detail">{{ card.detail }}</p>
        <dl>
          <div><dt>{{ card.current ? '最近有效值' : '上次有效值' }}</dt><dd>{{ card.rule.value === null ? '暂无有效值' : `${card.rule.value.toFixed(1)}%` }}</dd></div>
          <div><dt>数据时间</dt><dd>{{ formatTime(card.rule.sampleAt) }}</dd></div>
          <div v-if="card.rule.firingSince"><dt>本次触发</dt><dd>{{ formatTime(card.rule.firingSince) }}</dd></div>
          <div v-if="card.current && card.rule.pendingSince"><dt>触发观察起点</dt><dd>{{ formatTime(card.rule.pendingSince) }}</dd></div>
          <div v-if="card.current && card.rule.recoveringSince"><dt>恢复观察起点</dt><dd>{{ formatTime(card.rule.recoveringSince) }}</dd></div>
          <div v-if="card.rule.lastRecoveredAt"><dt>最近确认恢复</dt><dd>{{ formatTime(card.rule.lastRecoveredAt) }}</dd></div>
        </dl>
        <p class="resource-alerts__rule-note">触发 ≥{{ card.rule.triggerPercent }}% / {{ card.rule.triggerSeconds }} 秒；恢复 ≤{{ card.rule.recoveryPercent }}% / {{ card.rule.recoverySeconds }} 秒。</p>
        <p v-if="card.rule.reasonCode" class="resource-alerts__reason">原因 {{ card.rule.reasonCode }}</p>
      </article>
    </div>
    <footer>按约 5 秒采样判断，采样间的变化不可知。仅本次 Server 运行期，重启后重新观察；仅保留最近 24 小时内的最后恢复时间，不是持久告警历史或 NAS 整体健康证明。</footer>
  </section>
</template>

<style scoped>
.resource-alerts { padding: 1rem; min-width: 0; }
.resource-alerts__header, .resource-alerts__rule-heading { display: flex; align-items: flex-start; justify-content: space-between; flex-wrap: wrap; gap: .65rem 1rem; }
.resource-alerts h2, .resource-alerts h3 { display: flex; align-items: center; gap: .5rem; margin: 0; font-size: .9rem; line-height: 1.5; }
.resource-alerts h3 { font-size: .85rem; }
.resource-alerts h2 svg, .resource-alerts h3 svg { flex: 0 0 auto; color: var(--ncp-text-subtle); }
.resource-alerts p { margin: .4rem 0; font-size: .8rem; line-height: 1.6; }
.resource-alerts__header p, .resource-alerts__rule-note, .resource-alerts__reason, .resource-alerts footer { color: var(--ncp-text-subtle); }
.resource-alerts button { min-height: 2.25rem; padding: .35rem .7rem; border: 1px solid var(--ncp-line); border-radius: var(--ncp-radius-sm); color: var(--ncp-text); background: var(--ncp-surface); font: inherit; font-size: .8rem; cursor: pointer; }
.resource-alerts button:disabled { opacity: .6; cursor: default; }
.resource-alerts button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
.resource-alerts__grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 24rem), 1fr)); gap: .8rem; margin-block: .9rem; }
.resource-alerts__rule { min-width: 0; padding: .9rem; border: 1px solid var(--ncp-line); border-radius: var(--ncp-radius-sm); }
.resource-alerts__detail { min-height: 2.5em; }
.resource-alerts dl { margin: .8rem 0; font-size: .8rem; }
.resource-alerts dl > div { display: flex; flex-wrap: wrap; gap: .25rem .7rem; margin: .4rem 0; }
.resource-alerts dt { color: var(--ncp-text-subtle); }
.resource-alerts dd { margin: 0; font-variant-numeric: tabular-nums; }
.resource-alerts dd, .resource-alerts__reason, .resource-alerts__error { overflow-wrap: anywhere; }
.resource-alerts__error, .resource-alerts__notice { margin: .8rem 0; padding: .8rem; border: 1px solid var(--ncp-neutral-border); border-radius: var(--ncp-radius-sm); background: var(--ncp-neutral-soft); }
.resource-alerts__error { font-size: .8rem; }
.resource-alerts__error span { display: block; margin-bottom: .5rem; }
.resource-alerts footer { font-size: .72rem; line-height: 1.6; }
.resource-alerts__live { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
.resource-alerts :deep(.status-pill) { white-space: normal; height: auto; min-height: 1.75rem; padding-block: .25rem; }
.resource-alerts :deep(.status-pill__label) { overflow: visible; text-overflow: clip; }
</style>
