<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CirclePause, Eye, FileClock, Play, RefreshCw, Search } from '@lucide/vue'
import { ElButton, ElDialog, ElInput, ElOption, ElSelect, ElSwitch } from 'element-plus'

import { followLogs, requestLogs, type LogEntry } from '@/api/control'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'
import { useListPreference } from '@/composables/useListPreference'

type LogSource = 'system' | 'agent' | 'container'
const systemStore = useSystemStore()
const { pageSize } = useListPreference('logs.events')
const source = ref<LogSource>('system')
const containerId = ref('')
const level = ref('all')
const hours = ref(6)
const query = ref('')
const following = ref(false)
const loading = ref(false)
const error = ref('')
const entries = ref<LogEntry[]>([])
const selectedEntry = ref<LogEntry | null>(null)
const page = ref(1)
const showSkeleton = ref(false)
let logSource: EventSource | null = null
let loadController: AbortController | null = null
let skeletonTimer: ReturnType<typeof setTimeout> | null = null
let loadSequence = 0

const containers = computed(() => systemStore.inventory?.containers ?? [])
const stats = computed<WorkspaceStat[]>(() => [
  { label: '当前记录', value: entries.value.length },
  { label: '错误', value: entries.value.filter((item) => item.level === 'error').length, tone: 'warning' },
  { label: '实时跟随', value: following.value ? '已开启' : '已暂停', tone: following.value ? 'success' : undefined },
])
const sortedEntries = computed(() => [...entries.value].sort((left, right) => new Date(right.timestamp).valueOf() - new Date(left.timestamp).valueOf()))
const pageCount = computed(() => Math.max(1, Math.ceil(sortedEntries.value.length / pageSize.value)))
const pagedEntries = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return sortedEntries.value.slice(start, start + pageSize.value)
})

async function load(silent = false) {
  const requestSequence = ++loadSequence
  loadController?.abort()
  if (source.value === 'container' && !containerId.value) {
    entries.value = []
    loading.value = false
    showSkeleton.value = false
    return
  }
  if (!silent) {
    loading.value = true
    if (!entries.value.length) {
      if (skeletonTimer) clearTimeout(skeletonTimer)
      skeletonTimer = setTimeout(() => { showSkeleton.value = true }, 180)
    }
  }
  error.value = ''
  loadController = new AbortController()
  try {
    const result = await requestLogs({
      source: source.value, containerId: containerId.value, level: level.value,
      query: query.value, hours: hours.value, limit: 200,
    }, loadController.signal)
    if (requestSequence !== loadSequence) return
    entries.value = result.entries
    page.value = 1
  } catch (caught) {
    if (caught instanceof DOMException && caught.name === 'AbortError') return
    if (requestSequence !== loadSequence) return
    error.value = '日志读取失败，请确认目标服务正在运行。'
  } finally {
    if (requestSequence !== loadSequence) return
    if (skeletonTimer) clearTimeout(skeletonTimer)
    skeletonTimer = null
    showSkeleton.value = false
    loading.value = false
  }
}

function syncFollowStream() {
  logSource?.close()
  logSource = null
  if (!following.value || (source.value === 'container' && !containerId.value)) return
  logSource = followLogs({
    source: source.value, containerId: containerId.value, level: level.value,
    query: query.value, hours: hours.value, limit: pageSize.value,
  }, systemStore.refreshIntervalSeconds, (result) => {
    const merged = new Map(entries.value.map((entry) => [entry.id, entry]))
    for (const entry of result.entries) merged.set(entry.id, entry)
    entries.value = [...merged.values()].sort((left, right) => new Date(right.timestamp).valueOf() - new Date(left.timestamp).valueOf()).slice(0, 500)
    error.value = ''
  }, () => {
    error.value = '实时日志连接已中断，可暂停后重新开启。'
  })
}

function refreshLogs() {
  void load()
  if (following.value) syncFollowStream()
}

watch([source, containerId, level, hours], () => {
  void load()
  if (following.value) syncFollowStream()
})
watch(pageSize, () => { page.value = 1 })
watch(pageCount, (count) => {
  if (page.value > count) page.value = count
})
watch(following, syncFollowStream)
onMounted(() => {
  void systemStore.refresh({ inventory: true })
  void load()
})
onBeforeUnmount(() => {
  loadController?.abort()
  logSource?.close()
  if (skeletonTimer) clearTimeout(skeletonTimer)
})

function levelLabel(value: LogEntry['level']) {
  return value === 'error' ? '错误' : value === 'warning' ? '警告' : value === 'debug' ? '调试' : '信息'
}

function timestampLabel(value: string) {
  const timestamp = new Date(value)
  return Number.isNaN(timestamp.valueOf()) || timestamp.valueOf() <= 0 ? '时间未知' : timestamp.toLocaleString('zh-CN', { hour12: false })
}

function preciseTimestampLabel(value: string) {
  return value.replace('T', ' ').replace(/Z$/, '').replace(/([+-]\d{2}):?(\d{2})$/, ' $1:$2')
}

function logTokens(message: string) {
  const pattern = /(HTTP\/\d(?:\.\d)?\s+[1-5]\d{2}|\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b|\b(?:error|warn(?:ing)?|fatal|panic|exception|timeout|success|ready)\b|"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|(?:\/[\w.%~-]+)+|[A-Za-z_][\w.-]*(?==)|[{}[\],:])/gi
  return message.split(pattern).filter(Boolean).map((text) => {
    const normalized = text.toLowerCase()
    const tone = /^(get|post|put|patch|delete|head|options)$/.test(normalized) ? 'method'
      : /^http\/\d(?:\.\d)?\s+[1-5]\d{2}$/i.test(text) ? (/\s[23]\d{2}$/.test(text) ? 'success' : 'danger')
        : /^(error|warning|warn|fatal|panic|exception)$/.test(normalized) ? 'danger'
          : /^(success|ready)$/.test(normalized) ? 'success'
            : /^timeout$/.test(normalized) ? 'warning'
              : /^["']/.test(text) ? 'string'
                : /^\//.test(text) ? 'path'
                  : /^[A-Za-z_][\w.-]*$/.test(text) ? 'field'
                    : /^[{}[\],:]$/.test(text) ? 'punctuation' : ''
    return { text, tone }
  })
}
</script>

<template>
  <div class="page workspace-page logs-page">
    <WorkspaceHeader title="日志中心" description="统一检索系统、NCP Agent 与 Docker 容器日志" :icon="FileClock" :stats="stats" />

    <section class="log-toolbar panel">
      <div class="log-filters">
        <ElSelect v-model="source" aria-label="日志来源">
          <ElOption label="系统 journald" value="system" /><ElOption label="NCP Root Agent" value="agent" /><ElOption label="Docker 容器" value="container" />
        </ElSelect>
        <ElSelect v-if="source === 'container'" v-model="containerId" filterable placeholder="选择容器" aria-label="Docker 容器">
          <ElOption v-for="container in containers" :key="container.id" :label="container.name" :value="container.id" />
        </ElSelect>
        <ElSelect v-model="level" aria-label="日志级别"><ElOption label="全部级别" value="all" /><ElOption label="错误" value="error" /><ElOption label="警告" value="warning" /><ElOption label="信息" value="info" /><ElOption label="调试" value="debug" /></ElSelect>
        <ElSelect v-model="hours" aria-label="时间范围"><ElOption label="最近 1 小时" :value="1" /><ElOption label="最近 6 小时" :value="6" /><ElOption label="最近 24 小时" :value="24" /><ElOption label="最近 7 天" :value="168" /></ElSelect>
      </div>
      <div class="log-tools">
        <ListPageSizeControl list-key="logs.events" />
        <ElInput v-model="query" clearable placeholder="搜索消息或服务" @keyup.enter="refreshLogs"><template #prefix><Search :size="16" /></template></ElInput>
        <label class="follow-switch"><component :is="following ? Play : CirclePause" :size="16" /><span>实时跟随</span><ElSwitch v-model="following" /></label>
        <ElButton class="log-refresh-button" :loading="loading" title="刷新当前日志" @click="refreshLogs"><RefreshCw :size="16" />刷新</ElButton>
      </div>
    </section>

    <div v-if="error" class="log-error">{{ error }}</div>
    <section class="log-console panel">
      <div class="log-head"><span>时间</span><span>级别</span><span>消息</span><span>操作</span></div>
      <template v-if="showSkeleton && !entries.length">
        <div v-for="row in 12" :key="row" class="log-row log-row--skeleton"><i v-for="cell in 4" :key="cell" class="ncp-skeleton"></i></div>
      </template>
      <div v-for="entry in pagedEntries" v-else :key="entry.id" class="log-row">
        <time>{{ timestampLabel(entry.timestamp) }}</time>
        <span :class="['level-badge', `level-badge--${entry.level}`]">{{ levelLabel(entry.level) }}</span>
        <p class="log-message" :title="entry.message"><span v-for="(token, index) in logTokens(entry.message)" :key="index" :class="token.tone ? `log-token--${token.tone}` : undefined">{{ token.text }}</span></p>
        <button class="log-detail-button" type="button" title="查看日志详情" @click="selectedEntry = entry"><Eye :size="16" /></button>
      </div>
      <div v-if="!loading && !entries.length" class="log-empty">当前筛选条件下没有日志记录</div>
      <footer v-if="entries.length" class="log-pagination">
        <span>共 {{ entries.length }} 条</span>
        <div><button type="button" :disabled="page <= 1" @click="page -= 1">上一页</button><strong>{{ page }} / {{ pageCount }}</strong><button type="button" :disabled="page >= pageCount" @click="page += 1">下一页</button></div>
      </footer>
    </section>
    <ElDialog :model-value="Boolean(selectedEntry)" title="日志详情" width="min(920px, 92vw)" align-center destroy-on-close @close="selectedEntry = null">
      <div class="log-detail-dialog">
        <dl v-if="selectedEntry" class="log-detail-meta">
          <div><dt>时间</dt><dd>{{ preciseTimestampLabel(selectedEntry.timestamp) }}</dd></div>
          <div><dt>级别</dt><dd><span :class="['level-badge', `level-badge--${selectedEntry.level}`]">{{ levelLabel(selectedEntry.level) }}</span></dd></div>
          <div><dt>来源</dt><dd>{{ selectedEntry.source }}</dd></div>
          <div><dt>服务 / 容器</dt><dd>{{ selectedEntry.unit }}</dd></div>
        </dl>
        <pre v-if="selectedEntry" class="log-detail-message"><span v-for="(token, index) in logTokens(selectedEntry.message)" :key="index" :class="token.tone ? `log-token--${token.tone}` : undefined">{{ token.text }}</span></pre>
        <details v-if="selectedEntry?.rawMessage" class="log-raw-message">
          <summary>查看原始日志</summary>
          <pre>{{ selectedEntry.rawMessage }}</pre>
        </details>
      </div>
    </ElDialog>
  </div>
</template>

<style scoped>
.log-toolbar{display:flex;min-height:66px;align-items:center;justify-content:space-between;gap:14px;padding:11px 14px}.log-filters,.log-tools{display:flex;align-items:center;gap:8px}.log-filters :deep(.el-select){width:148px}.log-filters :deep(.el-select:nth-child(3)),.log-filters :deep(.el-select:nth-child(4)){width:126px}.log-tools :deep(.el-input){width:min(300px,24vw)}.log-toolbar :deep(.el-select__wrapper),.log-toolbar :deep(.el-input__wrapper){min-height:42px;border-radius:10px}.follow-switch{display:flex;min-height:42px;align-items:center;gap:7px;padding:0 11px;border:1px solid var(--ncp-line);border-radius:10px;color:var(--ncp-text-muted);font-size:.82rem}.log-refresh-button{min-width:86px}.log-console{min-height:610px;overflow:hidden}.log-head,.log-row{display:grid;grid-template-columns:188px 78px 200px minmax(360px,1fr);align-items:start;gap:13px}.log-head{min-height:46px;align-items:center;padding:0 16px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.8rem;font-weight:720}.log-row{min-height:52px;padding:10px 16px;border-top:1px solid var(--ncp-line);font-size:.8rem}.log-row:hover{background:var(--ncp-surface-hover)}.log-row time,.log-row code{overflow:hidden;color:var(--ncp-text-subtle);font-family:var(--ncp-font-mono);font-size:.76rem;text-overflow:ellipsis;white-space:nowrap}.log-row pre{margin:0;overflow-wrap:anywhere;color:var(--ncp-text);font-family:var(--ncp-font-mono);font-size:.78rem;line-height:1.55;white-space:pre-wrap}.level-badge{justify-self:start;padding:3px 8px;border-radius:7px;background:var(--ncp-info-soft);color:var(--ncp-info);font-size:.74rem;font-weight:720}.level-badge--error{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.level-badge--warning{background:var(--ncp-warning-soft);color:var(--ncp-warning-strong)}.level-badge--debug{background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.log-row--skeleton i{width:75%;height:10px}.log-empty{display:grid;min-height:560px;place-items:center;color:var(--ncp-text-subtle);font-size:.84rem}.log-error{padding:11px 14px;border-radius:9px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.82rem}@media(max-width:1180px){.log-toolbar{align-items:stretch;flex-direction:column}.log-filters,.log-tools{width:100%}.log-filters :deep(.el-select){flex:1;width:auto}.log-tools :deep(.el-input){min-width:0;flex:1}.log-head,.log-row{grid-template-columns:155px 68px 150px minmax(280px,1fr)}}@media(max-width:700px){.log-filters{display:grid;grid-template-columns:1fr 1fr}.log-filters :deep(.el-select){width:100%}.log-tools{flex-wrap:wrap}.log-tools :deep(.el-input){order:-1;flex-basis:100%}.log-head{display:none}.log-row{grid-template-columns:1fr auto}.log-row code{grid-column:1}.log-row pre{grid-column:1/-1}.log-row time{grid-row:1;grid-column:1}}
.log-toolbar :deep(.el-select__input),.log-toolbar :deep(.el-select__input-wrapper){border:0!important;outline:0!important;box-shadow:none!important}.log-head,.log-row{grid-template-columns:188px 78px minmax(360px,1fr) 64px;align-items:center}.log-head>span:nth-child(2),.log-head>span:last-child{text-align:center}.level-badge{justify-self:center}.log-message{overflow:hidden;margin:0;color:var(--ncp-text);font-family:var(--ncp-font-mono);font-size:.78rem;line-height:1.55;text-overflow:ellipsis;white-space:nowrap}.log-detail-button{display:grid;width:34px;height:34px;place-items:center;justify-self:center;border:1px solid transparent;border-radius:8px;color:var(--ncp-text-subtle);transition:background-color .18s ease,border-color .18s ease,color .18s ease}.log-row:hover .log-detail-button,.log-detail-button:focus-visible{border-color:var(--ncp-line);background:#fff;color:var(--ncp-primary-strong)}.log-token--method{color:#2769ba;font-weight:700}.log-token--success{color:#23866f;font-weight:700}.log-token--danger{color:#c95361;font-weight:700}.log-token--punctuation{color:#7b8798}.log-detail-dialog{display:grid;gap:16px}.log-detail-meta{display:grid;grid-template-columns:1fr 1fr;margin:0;border:1px solid var(--ncp-line);border-radius:12px;background:#fff}.log-detail-meta>div{display:grid;min-width:0;gap:5px;padding:12px}.log-detail-meta dt{color:var(--ncp-text-subtle);font-size:.75rem}.log-detail-meta dd{overflow-wrap:anywhere;margin:0;color:var(--ncp-text);font-family:var(--ncp-font-mono);font-size:.82rem}.log-detail-message{max-height:52vh;overflow:auto;margin:0;padding:18px;border:1px solid var(--ncp-line);border-radius:12px;background:var(--ncp-surface-quiet);color:var(--ncp-text);font-family:var(--ncp-font-mono);font-size:.84rem;line-height:1.7;white-space:pre-wrap;word-break:break-word}.log-pagination{display:flex;min-height:52px;align-items:center;justify-content:space-between;padding:8px 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.78rem}.log-pagination div{display:flex;align-items:center;gap:8px}.log-pagination button{min-height:34px;padding:0 11px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-text-muted);font-weight:680}.log-pagination button:disabled{opacity:.4}.log-pagination strong{min-width:56px;color:var(--ncp-text);text-align:center}@media(max-width:1050px){.log-head,.log-row{grid-template-columns:155px 68px minmax(280px,1fr) 58px}}@media(max-width:700px){.log-row{grid-template-columns:1fr auto auto}.log-message{grid-column:1/-1}.log-row time{grid-row:1;grid-column:1}.log-detail-meta{grid-template-columns:1fr}.log-console{min-height:520px}}
.log-token--warning{color:var(--ncp-warning-strong);font-weight:700}.log-token--string{color:#7b5ba7}.log-token--path{color:#25798a}.log-token--field{color:#44658c;font-weight:650}.log-detail-meta .level-badge{justify-self:start}.log-detail-message{tab-size:4}.log-raw-message{border:1px solid var(--ncp-line);border-radius:10px;background:#fff}.log-raw-message summary{cursor:pointer;padding:11px 14px;color:var(--ncp-text-muted);font-size:.8rem;font-weight:700}.log-raw-message pre{max-height:240px;overflow:auto;margin:0;padding:14px;border-top:1px solid var(--ncp-line);font-family:var(--ncp-font-mono);font-size:.78rem;line-height:1.65;tab-size:4;white-space:pre-wrap;word-break:break-word}
</style>
