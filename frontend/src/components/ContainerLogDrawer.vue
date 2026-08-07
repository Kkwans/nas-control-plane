<script setup lang="ts">
import { FileText, LoaderCircle } from '@lucide/vue'
import { ElDrawer } from 'element-plus'

import type { ContainerLogsResult } from '@/api/system'
import { formatLocalTimestamp } from '@/lib/datetime'
import { logTokens } from '@/utils/logTokens'

defineProps<{
  modelValue: boolean
  containerName: string
  loading: boolean
  logs: ContainerLogsResult | null
}>()

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

function levelLabel(level: ContainerLogsResult['entries'][number]['level']) {
  return level === 'error' ? 'ERROR' : level === 'warning' ? 'WARN' : level === 'debug' ? 'DEBUG' : 'INFO'
}
</script>

<template>
  <ElDrawer
    :model-value="modelValue"
    :title="`${containerName} · 容器日志`"
    size="min(760px, 100%)"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div v-if="loading" class="log-loading"><LoaderCircle class="spin" :size="18" />正在读取日志</div>
    <ol v-else-if="logs?.entries.length" class="log-list">
      <li v-for="(entry, index) in logs.entries" :key="`${entry.timestamp}-${index}`">
        <header class="log-entry-meta">
          <time>{{ formatLocalTimestamp(entry.timestamp, { fractional: true }) }}</time>
          <span :class="['log-level', `log-level--${entry.level}`]">{{ levelLabel(entry.level) }}</span>
          <span :class="['log-stream', `log-stream--${entry.stream}`]">{{ entry.stream }}</span>
        </header>
        <p class="log-entry-message"><span v-for="(token, tokenIndex) in logTokens(entry.message)" :key="tokenIndex" :class="token.tone ? `log-token--${token.tone}` : undefined">{{ token.text }}</span></p>
      </li>
    </ol>
    <div v-else class="log-empty"><FileText :size="24" /><span>当前没有可显示的日志。</span></div>
  </ElDrawer>
</template>

<style scoped>
.log-loading { display: flex; align-items: center; gap: 8px; color: var(--ncp-text-muted); font-size: .82rem; }
.log-list { display: grid; gap: 7px; padding: 0; margin: 0; list-style: none; }
.log-list li { display: grid; gap: 7px; padding: 11px 0; border-bottom: 1px solid var(--ncp-line); font-size: .78rem; line-height: 1.65; }
.log-entry-meta { display: flex; min-width: 0; align-items: center; gap: 8px; }
.log-entry-meta time { overflow: hidden; color: var(--ncp-text-subtle); font-family: var(--ncp-font-mono); font-size: .72rem; text-overflow: ellipsis; white-space: nowrap; }
.log-level, .log-stream { display: inline-flex; min-height: 22px; align-items: center; padding: 0 7px; border-radius: 6px; font-size: .65rem; font-weight: 760; letter-spacing: .02em; }
.log-level--info { background: var(--ncp-info-soft); color: var(--ncp-info); }
.log-level--warning { background: var(--ncp-warning-soft); color: var(--ncp-warning-strong); }
.log-level--error { background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); }
.log-level--debug { background: var(--ncp-surface-quiet); color: var(--ncp-text-subtle); }
.log-stream { background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); font-family: var(--ncp-font-mono); }
.log-stream--stderr { color: var(--ncp-text-muted); }
.log-entry-message { overflow-wrap: anywhere; margin: 0; color: var(--ncp-text); font-family: var(--ncp-font-mono); font-size: .81rem; white-space: pre-wrap; }
.log-entry-message .log-token--method { color: #2769ba; font-weight: 700; }
.log-entry-message .log-token--success { color: #23866f; font-weight: 700; }
.log-entry-message .log-token--danger { color: #c95361; font-weight: 700; }
.log-entry-message .log-token--warning { color: var(--ncp-warning-strong); font-weight: 700; }
.log-entry-message .log-token--string { color: #7b5ba7; }
.log-entry-message .log-token--path { color: #25798a; }
.log-entry-message .log-token--field { color: #44658c; font-weight: 650; }
.log-entry-message .log-token--punctuation { color: #7b8798; }
.log-empty { display: grid; min-height: 180px; place-items: center; align-content: center; gap: 8px; color: var(--ncp-text-subtle); font-size: .78rem; }
.spin { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
