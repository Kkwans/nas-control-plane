<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import {
  ArrowLeft,
  ArrowRight,
  Columns3,
  Database,
  HardDrive,
  KeyRound,
  RefreshCw,
  Rows3,
  Search,
  Table2,
} from '@lucide/vue'
import { ElButton, ElForm, ElFormItem, ElInput } from 'element-plus'

import { NcpApiError } from '@/api/system'
import type { DatabaseCredentials, DatabaseTable } from '@/api/database'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useDatabaseStore } from '@/stores/database'

const route = useRoute()
const databaseStore = useDatabaseStore()
const sourceId = computed(() => String(route.params.sourceId ?? ''))
const source = computed(() => databaseStore.source(sourceId.value))
const catalog = computed(() => databaseStore.catalogs[sourceId.value] ?? null)
const query = ref('')
const loading = ref(false)
const errorMessage = ref('')
const credentialDraft = ref<DatabaseCredentials>({})

const tables = computed(() => {
  const term = query.value.trim().toLowerCase()
  return (catalog.value?.tables ?? []).filter((table) => !term || `${table.schema} ${table.name} ${table.type}`.toLowerCase().includes(term))
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '数据表', value: catalog.value?.tables.length ?? '—' },
  { label: '统计行数', value: formatNumber(sumMetric('rowCount')), tone: 'success' },
  { label: '数据大小', value: formatBytes(sumMetric('sizeBytes')), tone: 'warning' },
])

onMounted(() => void initialize())
watch(sourceId, () => void initialize())

async function initialize() {
  errorMessage.value = ''
  if (!databaseStore.sources.length) await databaseStore.refreshDiscovery()
  const current = source.value
  if (!current || catalog.value || current.requiresLogin) return
  await reloadCatalog()
}

async function reloadCatalog() {
  if (!source.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    await databaseStore.loadCatalog(sourceId.value)
  } catch (error) {
    errorMessage.value = error instanceof NcpApiError ? error.message : '数据库信息读取失败。'
  } finally {
    loading.value = false
  }
}

async function connectDatabase() {
  loading.value = true
  errorMessage.value = ''
  try {
    await databaseStore.connect(sourceId.value, credentialDraft.value)
  } catch (error) {
    errorMessage.value = error instanceof NcpApiError ? error.message : '数据库连接失败，请检查登录信息。'
  } finally {
    loading.value = false
  }
}

function sumMetric(metric: 'rowCount' | 'sizeBytes') {
  if (!catalog.value) return null
  const values = catalog.value.tables.map((table) => table[metric]).filter((value): value is number => typeof value === 'number')
  return values.length ? values.reduce((total, value) => total + value, 0) : null
}

function formatNumber(value: number | null) {
  return value === null ? '—' : new Intl.NumberFormat('zh-CN').format(value)
}

function formatBytes(value: number | null | undefined) {
  if (value === null || value === undefined) return '—'
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MB`
  return `${(value / 1024 ** 3).toFixed(1)} GB`
}

function primaryKeys(table: DatabaseTable) {
  return table.columns.filter((column) => column.primaryKey).map((column) => column.name)
}
</script>

<template>
  <div v-if="source" class="page workspace-page database-detail">
    <WorkspaceHeader :title="source.name" :description="`${source.project} · ${source.module}`" :icon="Database" :stats="stats">
      <template #actions>
        <ElButton tag="a" href="/databases"><ArrowLeft :size="16" />返回数据库</ElButton>
        <ElButton v-if="catalog" :loading="loading" @click="reloadCatalog"><RefreshCw :size="16" />刷新结构</ElButton>
      </template>
      <template #tools>
        <ElInput v-if="catalog" v-model="query" class="table-search" clearable placeholder="搜索数据表" aria-label="搜索数据表">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="errorMessage" class="database-error" role="alert">{{ errorMessage }}</div>

    <dl class="source-summary panel">
      <div><dt>数据库类型</dt><dd>{{ source.driver === 'sqlite' ? 'SQLite' : source.driver === 'mysql' ? 'MySQL / MariaDB' : 'PostgreSQL' }}</dd></div>
      <div><dt>来源分类</dt><dd>{{ source.category === 'system' ? '系统数据库' : '项目数据库' }}</dd></div>
      <div><dt>关联项目</dt><dd>{{ source.project }}</dd></div>
      <div><dt>连接位置</dt><dd :title="source.location">{{ source.location }}</dd></div>
    </dl>

    <section v-if="loading && !catalog" class="table-list panel" aria-label="正在读取数据库结构">
      <div class="table-list__head"><span>数据表</span><span>类型</span><span>字段</span><span>数据行</span><span>大小</span><span>操作</span></div>
      <div v-for="row in 7" :key="row" class="table-row table-row--skeleton">
        <span><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></span>
        <i v-for="cell in 5" :key="cell" class="ncp-skeleton"></i>
      </div>
    </section>

    <section v-else-if="source.requiresLogin && !catalog" class="connection-panel panel">
      <div class="connection-intro">
        <span><Database :size="24" /></span>
        <div><h2>连接数据库</h2><p>实例和所属项目已自动识别；输入数据库账号后加载数据表。登录框不会再自动弹出。</p></div>
      </div>
      <ElForm class="connection-form" label-position="top" @submit.prevent="connectDatabase">
        <ElFormItem label="用户名"><ElInput v-model="credentialDraft.username" autocomplete="username" /></ElFormItem>
        <ElFormItem label="密码"><ElInput v-model="credentialDraft.password" type="password" show-password autocomplete="current-password" /></ElFormItem>
        <ElFormItem label="数据库名"><ElInput v-model="credentialDraft.database" :placeholder="source.defaultDatabase || '请输入数据库名称'" /></ElFormItem>
        <ElButton type="primary" :loading="loading" @click="connectDatabase">连接并读取数据表<ArrowRight :size="16" /></ElButton>
      </ElForm>
    </section>

    <template v-else-if="catalog">
      <section class="table-list panel" aria-label="数据表列表">
        <div class="table-list__head">
          <span>数据表</span><span>类型</span><span>字段</span><span>数据行</span><span>大小</span><span>操作</span>
        </div>
        <RouterLink
          v-for="table in tables"
          :key="`${table.schema}.${table.name}`"
          class="table-row"
          :to="{
            name: 'database-table',
            params: { sourceId, table: table.name },
            query: { sourceName: source.name, tableName: table.name, schema: table.schema || undefined },
          }"
        >
          <div class="table-name">
            <span><Table2 :size="18" /></span>
            <div>
              <strong>{{ table.name }}</strong>
              <small>{{ table.schema || 'SQLite 主库' }}</small>
              <div class="table-row__details">
                <span v-for="key in primaryKeys(table)" :key="key" class="primary-key-badge"><KeyRound :size="12" />{{ key }}</span>
                <span v-if="!primaryKeys(table).length" class="no-primary-key">无主键</span>
              </div>
            </div>
          </div>
          <span>{{ table.type === 'view' ? '视图' : '数据表' }}</span>
          <span><Columns3 :size="14" />{{ table.columns.length }}</span>
          <span><Rows3 :size="14" />{{ formatNumber(table.rowCount ?? null) }}</span>
          <span><HardDrive :size="14" />{{ formatBytes(table.sizeBytes) }}</span>
          <span class="open-table">打开<ArrowRight :size="16" /></span>
        </RouterLink>
        <div v-if="!tables.length" class="empty-table">没有匹配的数据表。</div>
      </section>
    </template>
  </div>

  <div v-else class="page"><section class="missing-source panel"><Database :size="26" /><h1>数据库来源不存在</h1><a href="/databases">返回数据库列表</a></section></div>
</template>

<style scoped>
.table-search { width:min(250px,24vw); }
.source-summary { display:grid; grid-template-columns:160px 160px minmax(160px,.7fr) minmax(260px,1.3fr); min-height:62px; margin:0; overflow:hidden; }
.source-summary>div { display:grid; min-width:0; align-content:center; gap:2px; padding:9px 15px; border-right:1px solid var(--ncp-line); }
.source-summary>div:last-child { border-right:0; }
.source-summary dt { color:var(--ncp-text-subtle); font-size:.7rem; }
.source-summary dd { overflow:hidden; margin:0; color:var(--ncp-text); font-size:.8rem; font-weight:680; text-overflow:ellipsis; white-space:nowrap; }
.database-error { padding:10px 13px; border:1px solid rgba(212,81,93,.2); border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.82rem; }
.connection-panel { display:grid; grid-template-columns:minmax(280px,.8fr) minmax(420px,1.2fr); gap:28px; padding:24px; }
.connection-intro { display:flex; align-items:flex-start; gap:13px; padding:4px; }
.connection-intro>span { display:grid; width:50px; height:50px; flex:0 0 auto; place-items:center; border-radius:14px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.connection-intro h2 { margin:2px 0 5px; font-size:1.05rem; }.connection-intro p { margin:0; color:var(--ncp-text-subtle); font-size:.82rem; line-height:1.65; }
.connection-form { display:grid; grid-template-columns:1fr 1fr; gap:0 14px; }
.connection-form :deep(.el-form-item:last-of-type) { grid-column:1/-1; }
.connection-form>.el-button { grid-column:1/-1; min-height:42px; justify-self:end; }
.table-list { overflow:hidden; }
.table-list__head,.table-row { display:grid; grid-template-columns:minmax(250px,1.6fr) 100px 90px 110px 110px 74px; align-items:center; gap:12px; }
.table-list__head { min-height:42px; padding:0 16px; background:var(--ncp-surface-quiet); color:var(--ncp-text-subtle); font-size:.74rem; font-weight:700; }
.table-row { position:relative; min-height:92px; padding:0 16px; border-top:1px solid var(--ncp-line); color:var(--ncp-text-muted); font-size:.78rem; transition:background var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast); }
.table-row:hover { background:var(--ncp-surface-hover); box-shadow:inset 3px 0 0 var(--ncp-primary); }
.table-row--skeleton>span{display:grid;gap:7px}.table-row--skeleton>span i:first-child{width:58%;height:12px}.table-row--skeleton>span i:last-child{width:34%;height:9px}.table-row--skeleton>i{width:68%;height:11px}
.table-row>span { display:flex; align-items:center; gap:5px; }
.table-name { display:flex; min-width:0; align-self:center; align-items:center; gap:10px; }
.table-name>span { display:grid; width:38px; height:38px; flex:0 0 auto; place-items:center; border-radius:10px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.table-name>div { display:grid; min-width:0; gap:1px; }.table-name strong { overflow:hidden; color:var(--ncp-text); font-size:.84rem; text-overflow:ellipsis; white-space:nowrap; }.table-name small { color:var(--ncp-text-subtle); font-size:.7rem; }
.open-table { justify-content:flex-end; color:var(--ncp-primary-strong); font-weight:700; }
.table-row__details { display:flex; align-items:center; gap:6px; min-height:22px; color:var(--ncp-text-subtle); font-size:.7rem; }
.primary-key-badge { display:inline-flex; min-height:22px; align-items:center; gap:4px; padding:0 7px; border:1px solid rgba(36,104,216,.2); border-radius:6px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); font-family:'JetBrains Mono Variable',monospace; line-height:1; }
.primary-key-badge svg { flex:0 0 auto; }
.no-primary-key { color:var(--ncp-text-subtle); }
.empty-table { padding:40px; color:var(--ncp-text-subtle); text-align:center; }
.missing-source { display:grid; min-height:260px; place-content:center; gap:8px; text-align:center; }.missing-source h1{margin:0;font-size:1rem}.missing-source a{color:var(--ncp-primary-strong)}
@media(max-width:1100px){.table-list__head,.table-row{grid-template-columns:minmax(220px,1.4fr) 78px 72px 92px 92px 64px;gap:8px}.source-summary{grid-template-columns:140px 140px 1fr 1.4fr}}
@media(max-width:800px){.connection-panel{grid-template-columns:1fr}.table-search{width:100%}.source-summary{grid-template-columns:1fr 1fr}.source-summary>div:nth-child(2){border-right:0}.source-summary>div:nth-child(-n+2){border-bottom:1px solid var(--ncp-line)}.table-list__head{display:none}.table-row{grid-template-columns:minmax(0,1fr) auto auto; gap:10px; min-height:108px; padding:14px}.table-row>span:nth-of-type(2),.table-row>span:nth-of-type(4){display:none}}
@media(max-width:560px){.connection-form{grid-template-columns:1fr}.connection-form :deep(.el-form-item:last-of-type),.connection-form>.el-button{grid-column:1}.connection-form>.el-button{width:100%}}
</style>
