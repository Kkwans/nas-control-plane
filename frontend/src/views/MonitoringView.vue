<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Activity, Cpu, Gauge, HardDrive, MemoryStick, RefreshCw } from '@lucide/vue'
import { ElButton, ElSegmented } from 'element-plus'

import { requestMetricSamples, type MetricSample } from '@/api/control'
import RealtimeTrendChart from '@/components/RealtimeTrendChart.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

type TimeRange = '1h' | '6h' | '24h' | '7d'
const range = ref<TimeRange>('6h')
const customFrom = ref('')
const customTo = ref('')
const systemStore = useSystemStore()
const samples = ref<MetricSample[]>([])
const loading = ref(false)
const error = ref('')
const latest = computed(() => samples.value.at(-1))
const stats = computed<WorkspaceStat[]>(() => [
  { label: '采样数量', value: samples.value.length },
  { label: 'CPU', value: latest.value ? `${latest.value.cpuPercent.toFixed(1)}%` : '—' },
  { label: '内存', value: latest.value ? `${latest.value.memoryPercent.toFixed(1)}%` : '—' },
])
const timestamps = computed(() => samples.value.map((item) => item.collectedAt))
const networkRates = computed(() => samples.value.map((item, index) => {
  const previous = samples.value[index - 1]
  if (!previous) return { receive: 0, transmit: 0 }
  const seconds = Math.max((new Date(item.collectedAt).valueOf() - new Date(previous.collectedAt).valueOf()) / 1000, 1)
  return {
    receive: Math.max((item.networkReceiveBytes - previous.networkReceiveBytes) / seconds / 1024, 0),
    transmit: Math.max((item.networkTransmitBytes - previous.networkTransmitBytes) / seconds / 1024, 0),
  }
}))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const preciseRange = customFrom.value && customTo.value
      ? { from: new Date(customFrom.value).toISOString(), to: new Date(customTo.value).toISOString() }
      : range.value
    samples.value = await requestMetricSamples(preciseRange)
  }
  catch { error.value = '监控历史加载失败，请稍后重试。' }
  finally { loading.value = false }
}
function selectQuickRange(value: TimeRange) {
  range.value = value
  customFrom.value = ''
  customTo.value = ''
  void load()
}

function applyCustomRange() {
  if (!customFrom.value || !customTo.value) return
  const from = new Date(customFrom.value)
  const to = new Date(customTo.value)
  const now = new Date()
  if (from >= to || to > now || to.valueOf() - from.valueOf() > 7 * 24 * 60 * 60 * 1000) {
    error.value = '请选择不超过 7 天、且结束时间不晚于当前时间的范围。'
    return
  }
  void load()
}
watch(() => systemStore.summary?.collectedAt, (next, previous) => {
  if (next && previous && next !== previous) void load()
})
onMounted(() => void load())
</script>

<template>
  <div class="page workspace-page">
    <WorkspaceHeader title="系统监控" description="查看 CPU、内存、负载、磁盘与网络的历史运行趋势" :icon="Gauge" :stats="stats">
      <template #filters><ElSegmented :model-value="range" :options="[{label:'1 小时',value:'1h'},{label:'6 小时',value:'6h'},{label:'24 小时',value:'24h'},{label:'7 天',value:'7d'}]" @change="selectQuickRange($event as TimeRange)" /></template>
      <template #tools>
        <div class="precise-range">
          <label><span>开始</span><input v-model="customFrom" type="datetime-local" :max="customTo || undefined" /></label>
          <span>至</span>
          <label><span>结束</span><input v-model="customTo" type="datetime-local" :min="customFrom || undefined" @change="applyCustomRange" /></label>
          <ElButton :disabled="!customFrom || !customTo" @click="applyCustomRange">应用</ElButton>
        </div>
      </template>
      <template #actions><ElButton :loading="loading" @click="load"><RefreshCw :size="16" />刷新</ElButton></template>
    </WorkspaceHeader>
    <div v-if="error" class="monitor-error">{{ error }}</div>
    <section v-if="loading && !samples.length" class="monitor-grid">
      <article v-for="item in 4" :key="item" class="chart-card panel"><i v-for="line in 8" :key="line" class="ncp-skeleton"></i></article>
    </section>
    <section v-else class="monitor-grid">
      <article class="chart-card panel"><header><Cpu :size="18" /><div><strong>处理器与负载</strong><small>CPU 使用率与 1 分钟负载</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'CPU',color:'#2468d8',values:samples.map(i=>i.cpuPercent)},{name:'负载',color:'#d28a1b',values:samples.map(i=>i.load1)}]" /></article>
      <article class="chart-card panel"><header><MemoryStick :size="18" /><div><strong>内存使用</strong><small>已用内存占总容量比例</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'内存',color:'#16866a',values:samples.map(i=>i.memoryPercent)}]" /></article>
      <article class="chart-card panel"><header><HardDrive :size="18" /><div><strong>存储使用</strong><small>所有已采集挂载点的合计占用</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="%" :series="[{name:'磁盘',color:'#7a5bd0',values:samples.map(i=>i.diskPercent)}]" /></article>
      <article class="chart-card panel"><header><Activity :size="18" /><div><strong>网络吞吐</strong><small>接收与发送速率</small></div></header><RealtimeTrendChart :timestamps="timestamps" unit="KB/s" :series="[{name:'接收',color:'#16866a',values:networkRates.map(i=>i.receive)},{name:'发送',color:'#2468d8',values:networkRates.map(i=>i.transmit)}]" /></article>
    </section>
  </div>
</template>

<style scoped>
.monitor-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; }
.chart-card { min-width:0; padding:20px; transition:border-color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.chart-card:hover { border-color:rgba(52,116,212,.22); box-shadow:0 15px 34px rgba(44,66,94,.08); transform:translateY(-1px); }
.chart-card>header { display:flex; align-items:center; gap:11px; margin-bottom:12px; color:var(--ncp-primary-strong); }
.chart-card>header>svg { padding:8px; box-sizing:content-box; border-radius:10px; background:var(--ncp-primary-soft); }
.chart-card>header div { display:grid; gap:2px; }
.chart-card strong { color:var(--ncp-text); font-size:1rem; }
.chart-card small { color:var(--ncp-text-muted); font-size:.82rem; }
.chart-card>.ncp-skeleton { display:block; width:100%; height:18px; margin:10px 0; }
.monitor-error { margin-bottom:14px; padding:12px 14px; border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.86rem; }
.precise-range { display:flex; align-items:center; gap:8px; }
.precise-range>span { color:var(--ncp-text-subtle); font-size:.76rem; }
.precise-range label { display:flex; min-height:40px; align-items:center; gap:7px; padding:0 10px; border:1px solid var(--ncp-line); border-radius:10px; background:#fff; }
.precise-range label>span { color:var(--ncp-text-subtle); font-size:.72rem; font-weight:700; }
.precise-range input { width:150px; border:0; outline:0; background:transparent; color:var(--ncp-text); font-family:var(--ncp-font-mono); font-size:.75rem; }
@media(max-width:920px) { .monitor-grid { grid-template-columns:1fr; } }
@media(max-width:760px) { .precise-range { width:100%; flex-wrap:wrap; }.precise-range label { flex:1; }.precise-range input { width:100%; } }
@media(max-width:640px) { .chart-card { padding:16px; } }
</style>
