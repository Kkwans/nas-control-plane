<script setup lang="ts">
import { Box, CalendarClock, FileText, Gauge, HardDrive, LoaderCircle, Network } from '@lucide/vue'
import { ElDrawer } from 'element-plus'

import type { ContainerDetails, ContainerLogsResult } from '@/api/system'
import { dockerContainerStateDetail, dockerContainerStateLabel } from '@/domain/docker'
import { formatLocalTimestamp } from '@/lib/datetime'
import { logTokens } from '@/utils/logTokens'

defineProps<{
  modelValue: boolean
  containerName: string
  loading: boolean
  details: ContainerDetails | null
  logs: ContainerLogsResult | null
  error: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

function levelLabel(level: ContainerLogsResult['entries'][number]['level']) {
  return level === 'error' ? 'ERROR' : level === 'warning' ? 'WARN' : level === 'debug' ? 'DEBUG' : 'INFO'
}

function formatBytes(value: number) {
  if (!value) return '无限制'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit++
  }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}

function formatCPU(nanoCpus: number) {
  return nanoCpus > 0 ? `${(nanoCpus / 1_000_000_000).toFixed(2).replace(/\.00$/, '')} 核` : '无限制'
}

function restartPolicyLabel(value?: string) {
  const labels: Record<string, string> = { no: '不自动重启', always: '始终重启', 'on-failure': '失败时重启', 'unless-stopped': '手动停止前重启' }
  return labels[value ?? ''] ?? value ?? '未设置'
}
</script>

<template>
  <ElDrawer
    :model-value="modelValue"
    size="min(860px, 100%)"
    append-to-body
    class="container-detail-drawer"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #header>
      <div class="drawer-heading">
        <span><Box :size="21" /></span>
        <div><strong>{{ containerName }}</strong><small>容器详情与运行日志</small></div>
      </div>
    </template>

    <div v-if="loading" class="drawer-loading"><LoaderCircle class="spin" :size="20" /><strong>正在读取容器信息</strong><span>详情和日志会同时加载。</span></div>
    <div v-else class="drawer-content">
      <div v-if="error" class="drawer-error" role="alert">{{ error }}</div>

      <template v-if="details">
        <section class="facts-grid" aria-label="容器运行事实">
          <article><Box :size="17" /><span>当前状态</span><strong>{{ dockerContainerStateLabel(details.state) }}</strong><small>{{ dockerContainerStateDetail(details) }}</small></article>
          <article><CalendarClock :size="17" /><span>创建时间</span><strong>{{ details.createdAt ? formatLocalTimestamp(details.createdAt) : '未知' }}</strong><small>{{ details.startedAt ? `启动 ${formatLocalTimestamp(details.startedAt)}` : '尚未启动' }}</small></article>
          <article><Gauge :size="17" /><span>资源限制</span><strong>{{ formatCPU(details.nanoCpus) }}</strong><small>内存 {{ formatBytes(details.memoryBytes) }}</small></article>
          <article><HardDrive :size="17" /><span>重启策略</span><strong>{{ restartPolicyLabel(details.restartPolicy) }}</strong><small>累计重启 {{ details.restartCount }} 次</small></article>
        </section>

        <section class="detail-block">
          <header><Network :size="17" /><div><strong>网络与端口</strong><span>{{ details.networkMode || '默认网络' }}</span></div></header>
          <div class="network-grid">
            <article v-for="network in details.networks" :key="network.name">
              <strong>{{ network.name }}</strong><span :title="network.ipAddress || network.ipv6Address">{{ network.ipAddress || network.ipv6Address || '未分配地址' }}</span><small>{{ network.gateway ? `网关 ${network.gateway}` : '无独立网关' }}</small>
            </article>
            <article v-for="port in details.ports" :key="`${port.privatePort}-${port.publicPort}-${port.protocol}`">
              <strong>{{ port.publicPort || '未发布' }} → {{ port.privatePort }}/{{ port.protocol }}</strong><span>{{ port.hostIp || '全部主机地址' }}</span><small>{{ port.publicPort ? '主机端口映射' : '仅容器内部开放' }}</small>
            </article>
            <p v-if="!details.networks.length && !details.ports.length" class="detail-empty">没有可显示的网络或端口信息。</p>
          </div>
        </section>

        <section class="detail-block">
          <header><HardDrive :size="17" /><div><strong>存储挂载</strong><span>{{ details.mounts.length }} 项</span></div></header>
          <div v-if="details.mounts.length" class="mount-list">
            <article v-for="mount in details.mounts" :key="`${mount.type}-${mount.destination}`">
              <span>{{ mount.type }}</span><div><strong :title="mount.destination">{{ mount.destination }}</strong><small :title="mount.source || mount.name">{{ mount.source || mount.name || '临时文件系统' }}</small></div><em>{{ mount.readOnly ? '只读' : '读写' }}</em>
            </article>
          </div>
          <p v-else class="detail-empty">该容器没有额外挂载。</p>
        </section>
      </template>

      <section class="logs-block">
        <header><FileText :size="17" /><div><strong>运行日志</strong><span>最近 {{ logs?.tail ?? 0 }} 条 stdout / stderr</span></div></header>
        <ol v-if="logs?.entries.length" class="log-list">
          <li v-for="(entry, index) in logs.entries" :key="`${entry.timestamp}-${index}`">
            <header class="log-entry-meta">
              <time>{{ formatLocalTimestamp(entry.timestamp, { fractional: true }) }}</time>
              <span :class="['log-level', `log-level--${entry.level}`]">{{ levelLabel(entry.level) }}</span>
              <span :class="['log-stream', `log-stream--${entry.stream}`]">{{ entry.stream }}</span>
            </header>
            <p class="log-entry-message"><span v-for="(token, tokenIndex) in logTokens(entry.message)" :key="tokenIndex" :class="token.tone ? `log-token--${token.tone}` : undefined">{{ token.text }}</span></p>
            <details v-if="entry.rawMessage" class="log-entry-raw">
              <summary>查看原始日志</summary>
              <pre>{{ entry.rawMessage }}</pre>
            </details>
          </li>
        </ol>
        <div v-else class="log-empty"><FileText :size="24" /><span>当前没有可显示的日志。</span></div>
      </section>
    </div>
  </ElDrawer>
</template>

<style scoped>
.drawer-heading{display:flex;min-width:0;align-items:center;gap:11px}.drawer-heading>span{display:grid;width:42px;height:42px;flex:0 0 auto;place-items:center;border-radius:12px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.drawer-heading>div{display:grid;min-width:0;gap:2px}.drawer-heading strong{overflow:hidden;color:var(--ncp-text);font-size:.95rem;text-overflow:ellipsis;white-space:nowrap}.drawer-heading small{color:var(--ncp-text-subtle);font-size:.72rem}
.drawer-loading{display:grid;min-height:260px;place-items:center;align-content:center;gap:8px;color:var(--ncp-text-subtle)}.drawer-loading strong{color:var(--ncp-text);font-size:.85rem}.drawer-loading span{font-size:.72rem}.drawer-content{display:grid;gap:18px;padding-bottom:20px}.drawer-error{padding:10px 12px;border:1px solid var(--ncp-danger-border);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.74rem;line-height:1.55}
.facts-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:9px}.facts-grid article{display:grid;grid-template-columns:auto minmax(0,1fr);gap:2px 8px;padding:12px;border:1px solid var(--ncp-line);border-radius:12px;background:var(--ncp-surface-quiet)}.facts-grid svg{grid-row:1/4;color:var(--ncp-primary-strong)}.facts-grid span{color:var(--ncp-text-subtle);font-size:.67rem}.facts-grid strong,.facts-grid small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.facts-grid strong{color:var(--ncp-text);font-size:.76rem}.facts-grid small{color:var(--ncp-text-muted);font-size:.65rem}
.detail-block,.logs-block{display:grid;gap:11px}.detail-block>header,.logs-block>header{display:flex;align-items:center;gap:8px;color:var(--ncp-primary-strong)}.detail-block>header>div,.logs-block>header>div{display:grid;gap:1px}.detail-block>header strong,.logs-block>header strong{color:var(--ncp-text);font-size:.84rem}.detail-block>header span,.logs-block>header span{color:var(--ncp-text-subtle);font-size:.68rem}.network-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}.network-grid article{display:grid;gap:3px;padding:11px 12px;border:1px solid var(--ncp-line);border-radius:10px;background:#fff}.network-grid strong{font-size:.74rem}.network-grid span,.network-grid small{overflow:hidden;color:var(--ncp-text-muted);font-family:var(--ncp-font-mono);font-size:.66rem;text-overflow:ellipsis;white-space:nowrap}.network-grid small{color:var(--ncp-text-subtle)}
.mount-list{display:grid;border:1px solid var(--ncp-line);border-radius:11px;overflow:hidden}.mount-list article{display:grid;grid-template-columns:54px minmax(0,1fr) auto;align-items:center;gap:10px;min-height:54px;padding:8px 11px;border-top:1px solid var(--ncp-line)}.mount-list article:first-child{border-top:0}.mount-list>article>span,.mount-list em{padding:3px 6px;border-radius:6px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.63rem;font-style:normal;text-align:center}.mount-list div{display:grid;min-width:0;gap:2px}.mount-list strong,.mount-list small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.mount-list strong{font-family:var(--ncp-font-mono);font-size:.7rem}.mount-list small{color:var(--ncp-text-subtle);font-family:var(--ncp-font-mono);font-size:.63rem}.detail-empty{grid-column:1/-1;margin:0;padding:18px;border:1px dashed var(--ncp-line-strong);border-radius:10px;color:var(--ncp-text-subtle);font-size:.7rem;text-align:center}
.logs-block{padding-top:2px}.log-list{display:grid;gap:0;padding:0;margin:0;border-top:1px solid var(--ncp-line);list-style:none}.log-list li{display:grid;gap:7px;padding:12px 2px;border-bottom:1px solid var(--ncp-line);font-size:.8rem;line-height:1.65}.log-entry-meta{display:flex;min-width:0;align-items:center;gap:8px}.log-entry-meta time{overflow:hidden;color:var(--ncp-text-subtle);font-family:var(--ncp-font-mono);font-size:.72rem;text-overflow:ellipsis;white-space:nowrap}.log-level,.log-stream{display:inline-flex;min-height:22px;align-items:center;padding:0 7px;border-radius:6px;font-size:.65rem;font-weight:760;letter-spacing:.02em}.log-level--info{background:var(--ncp-info-soft);color:var(--ncp-info)}.log-level--warning{background:var(--ncp-warning-soft);color:var(--ncp-warning-strong)}.log-level--error{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.log-level--debug{background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.log-stream{background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-family:var(--ncp-font-mono)}.log-entry-message{overflow-wrap:anywhere;margin:0;color:var(--ncp-text);font-family:var(--ncp-font-mono);font-size:.81rem;white-space:pre-wrap}.log-entry-message .log-token--method{color:#2769ba;font-weight:700}.log-entry-message .log-token--success{color:#23866f;font-weight:700}.log-entry-message .log-token--danger{color:#c95361;font-weight:700}.log-entry-message .log-token--warning{color:var(--ncp-warning-strong);font-weight:700}.log-entry-message .log-token--string{color:#7b5ba7}.log-entry-message .log-token--path{color:#25798a}.log-entry-message .log-token--field{color:#44658c;font-weight:650}.log-entry-message .log-token--punctuation{color:#7b8798}.log-empty{display:grid;min-height:140px;place-items:center;align-content:center;gap:8px;border:1px dashed var(--ncp-line-strong);border-radius:11px;color:var(--ncp-text-subtle);font-size:.75rem}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}
.log-entry-raw{border-radius:8px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted)}.log-entry-raw summary{cursor:pointer;padding:7px 9px;font-size:.68rem;font-weight:700}.log-entry-raw pre{overflow-wrap:anywhere;margin:0;padding:0 9px 9px;font-family:var(--ncp-font-mono);font-size:.7rem;line-height:1.55;white-space:pre-wrap}
@media(max-width:700px){.facts-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.network-grid{grid-template-columns:1fr}.mount-list article{grid-template-columns:48px minmax(0,1fr)}.mount-list em{grid-column:2;justify-self:start}.log-entry-meta{flex-wrap:wrap}}
</style>

<style>
.container-detail-drawer .el-drawer__header{margin-bottom:0;padding:17px 20px 14px;border-bottom:1px solid var(--ncp-line)}.container-detail-drawer .el-drawer__body{padding:18px 20px}@media(max-width:700px){.container-detail-drawer .el-drawer__body{padding:15px 14px}}
</style>
