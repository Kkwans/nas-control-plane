<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CircleAlert,
  Columns3,
  Database,
  Eye,
  HardDrive,
  KeyRound,
  RefreshCw,
  Rows3,
  Search,
  Table2,
} from '@lucide/vue'
import { ElButton, ElForm, ElFormItem, ElInput } from 'element-plus'

import { NcpApiError } from '@/api/system'
import { testDatabaseConnection } from '@/api/database'
import type { DatabaseConnectionDiagnostic, DatabaseCredentials, DatabaseTable } from '@/api/database'
import DatabaseErrorPanel, { type DatabaseErrorState } from '@/components/DatabaseErrorPanel.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useDatabaseStore } from '@/stores/database'

type ConnectionDiagnostic = DatabaseConnectionDiagnostic

type StatusTone = 'success' | 'warning' | 'danger' | 'neutral'

const databaseErrorCopy: Record<string, string> = {
  credentials_required: '连接需要数据库凭据，请填写表单后重试。',
  auth_failed: '数据库认证失败，请核对用户名和密码。',
  unreachable: '数据库服务不可达，请检查地址和端口。',
  database_not_found: '目标数据库或数据表不存在，请检查数据库名。',
  permission_denied: '当前账号没有执行该操作的权限。',
  sql_invalid: 'SQL 或数据库请求无效。',
  constraint_failed: '数据约束未满足，请检查提交内容。',
  agent_unavailable: 'Root Agent 暂不可用，请确认代理服务状态。',
  timeout: '数据库操作超时，请稍后重试。',
  credential_store_unavailable: '数据库凭据服务暂不可用。',
  credential_corrupt: '保存的数据库凭据不可用，请重新连接。',
  key_unavailable: '数据库凭据加密服务不可用。',
  key_rotation_failed: '数据库凭据密钥更新失败。',
  migration_failed: '数据库凭据存储迁移失败。',
  DATABASE_OPERATION_FAILED: '数据库操作失败，请稍后重试。',
}

const route = useRoute()
const databaseStore = useDatabaseStore()
const sourceId = computed(() => String(route.params.sourceId ?? ''))
const source = computed(() => databaseStore.source(sourceId.value))
const catalog = computed(() => databaseStore.catalogs[sourceId.value] ?? null)
const query = ref('')
const loading = ref(false)
const diagnosticLoading = ref(false)
const errorState = ref<DatabaseErrorState | null>(null)
const credentialDraft = ref<DatabaseCredentials>({})
const diagnostic = ref<ConnectionDiagnostic | null>(null)

const tables = computed(() => {
  const term = query.value.trim().toLowerCase()
  return (catalog.value?.tables ?? []).filter((table) => !term || `${table.schema} ${table.name} ${table.type}`.toLowerCase().includes(term))
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '数据表与视图', value: catalog.value?.tables.length ?? '待加载' },
  { label: '统计行数', value: formatNumber(sumMetric('rowCount')), tone: 'success' },
  { label: '数据大小', value: formatBytes(sumMetric('sizeBytes')), tone: 'warning' },
])
const connectionState = computed(() => {
  if (catalog.value) return { label: '已连接', tone: 'success' as StatusTone, detail: '已读取对象目录，可打开数据表或视图。' }
  if (diagnostic.value?.connected) return { label: '诊断通过', tone: 'success' as StatusTone, detail: '凭据可用，提交连接后读取对象目录。' }
  if (source.value?.status === 'credentials_required' || source.value?.requiresLogin) {
    return { label: '需要凭据', tone: 'warning' as StatusTone, detail: '输入凭据后测试连接或读取对象目录。' }
  }
  if (source.value?.status === 'available') return { label: '可用', tone: 'success' as StatusTone, detail: '可以读取对象目录。' }
  if (source.value?.status === 'unreachable') return { label: '不可达', tone: 'danger' as StatusTone, detail: '请检查数据库服务状态。' }
  return { label: source.value?.status || '未知', tone: 'neutral' as StatusTone, detail: '连接状态尚未确认。' }
})

onMounted(() => void initialize())
watch(sourceId, () => void initialize())

async function initialize() {
  clearError()
  diagnostic.value = null
  try {
    if (!databaseStore.sources.length) await databaseStore.refreshDiscovery()
  } catch (error) {
    setError(error, '数据库发现失败，请确认 Root Agent 正常运行。')
    return
  }
  const current = source.value
  if (!current || catalog.value) return
  if (current.requiresLogin) {
    loading.value = true
    try {
      const result = await databaseStore.connect(sourceId.value, {})
      diagnostic.value = result.diagnostic
    } catch (error) {
      if (errorCode(error) !== 'credentials_required') {
        diagnostic.value = diagnosticFromError(error)
        setError(error, '自动连接失败，请检查保存的凭据或重新输入。')
      }
    } finally {
      loading.value = false
    }
    return
  }
  await reloadCatalog()
}

async function reloadCatalog() {
  if (!source.value) return
  loading.value = true
  clearError()
  try {
    await databaseStore.loadCatalog(sourceId.value)
  } catch (error) {
    setError(error, '数据库对象目录读取失败。')
  } finally {
    loading.value = false
  }
}

async function connectDatabase() {
  if (!source.value) return
  loading.value = true
  clearError()
  try {
    const result = await databaseStore.connect(sourceId.value, credentialDraft.value)
    diagnostic.value = result.diagnostic
  } catch (error) {
    diagnostic.value = diagnosticFromError(error)
    setError(error, '数据库连接失败，请检查登录信息。')
  } finally {
    loading.value = false
  }
}

async function testConnection() {
  if (!source.value) return
  diagnosticLoading.value = true
  diagnostic.value = null
  clearError()
  try {
    const result = await requestConnectionDiagnostic({
      sourceId: sourceId.value,
      credentials: credentialDraft.value,
    })
    diagnostic.value = result
    if (!result.connected) {
      const code = result.code || 'unreachable'
      setError(new NcpApiError(code, databaseErrorCopy[code] || '连接诊断未通过。'), '连接诊断未通过。')
    }
  } catch (error) {
    diagnostic.value = diagnosticFromError(error)
    setError(error, '连接诊断失败，请稍后重试。')
  } finally {
    diagnosticLoading.value = false
  }
}

async function requestConnectionDiagnostic(input: { sourceId: string; credentials: DatabaseCredentials }) {
  return testDatabaseConnection(input)
}

function diagnosticFromError(error: unknown): ConnectionDiagnostic {
  return {
    connected: false,
    code: errorCode(error),
    driver: source.value?.driver || 'sqlite',
    endpoint: source.value?.location || '',
    database: credentialDraft.value.database || source.value?.defaultDatabase,
    operation: 'test_connection',
    durationMs: 0,
  }
}

function sumMetric(metric: 'rowCount' | 'sizeBytes') {
  if (!catalog.value) return null
  const values = catalog.value.tables.map((table) => table[metric]).filter((value): value is number => typeof value === 'number')
  return values.length ? values.reduce((total, value) => total + value, 0) : null
}

function formatNumber(value: number | null | undefined) {
  return value === null || value === undefined ? '未统计' : new Intl.NumberFormat('zh-CN').format(value)
}

function formatBytes(value: number | null | undefined) {
  if (value === null || value === undefined) return '未统计'
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MB`
  return `${(value / 1024 ** 3).toFixed(1)} GB`
}

function primaryKeys(table: DatabaseTable) {
  return table.columns.filter((column) => column.primaryKey).map((column) => column.name)
}

function tableTypeLabel(table: DatabaseTable) {
  return table.type.toLowerCase() === 'view' ? '视图' : '数据表'
}

function tableTypeIcon(table: DatabaseTable) {
  return table.type.toLowerCase() === 'view' ? Eye : Table2
}

function schemaLabel(schema: string) {
  return schema.trim() || '未提供 schema'
}

function driverLabel(driver: string) {
  return driver === 'sqlite' ? 'SQLite' : driver === 'mysql' ? 'MySQL / MariaDB' : 'PostgreSQL'
}

function errorCode(error: unknown) {
  if (error instanceof NcpApiError) return error.code
  if (error instanceof Error && databaseErrorCopy[error.message]) return error.message
  return 'DATABASE_OPERATION_FAILED'
}

function errorMessage(error: unknown, fallback: string) {
  const code = errorCode(error)
  return databaseErrorCopy[code] || (error instanceof NcpApiError ? error.message : fallback)
}

function errorNextStep(error: unknown) {
  switch (errorCode(error)) {
    case 'credentials_required': return '输入凭据后重新测试连接。'
    case 'auth_failed': return '核对用户名、密码和数据库名；密码不会显示在错误信息中。'
    case 'unreachable': return '确认数据库服务运行且地址、端口可访问。'
    case 'database_not_found': return '确认数据库名、schema 或对象名称。'
    case 'permission_denied': return '改用具备目录读取权限的账号。'
    case 'sql_invalid': return '检查 SQL 语句和参数后重试。'
    case 'constraint_failed': return '检查必填字段、唯一键和外键约束。'
    default: return '稍后重试；若持续失败，请记录错误码联系管理员。'
  }
}

function setError(error: unknown, fallback: string) {
  const code = errorCode(error)
  errorState.value = { code, message: errorMessage(error, fallback), nextStep: errorNextStep(error) }
}

function clearError() {
  errorState.value = null
}
</script>

<template>
  <div v-if="source" class="page workspace-page database-detail">
    <WorkspaceHeader :title="source.name" :description="`${source.project || '未关联项目'} / ${source.module || '未标注用途'}`" :icon="Database" :stats="stats">
      <template #actions>
        <ElButton tag="a" href="/databases" class="database-header-action"><ArrowLeft :size="16" />返回列表</ElButton>
        <ElButton v-if="catalog" class="database-header-action" :loading="loading" @click="reloadCatalog"><RefreshCw :size="16" />刷新结构</ElButton>
      </template>
      <template #tools>
        <ElInput v-if="catalog" v-model="query" class="table-search" clearable placeholder="搜索数据表或视图" aria-label="搜索数据表或视图">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <DatabaseErrorPanel v-if="errorState" title="数据库操作失败" :error="errorState" />

    <dl class="source-summary panel">
      <div class="source-summary__status">
        <dt>连接状态</dt>
        <dd>
          <span :class="['connection-pill', `connection-pill--${connectionState.tone}`]">{{ connectionState.label }}</span>
          <small>{{ connectionState.detail }}</small>
        </dd>
      </div>
      <div><dt>数据库类型</dt><dd>{{ driverLabel(source.driver) }}</dd></div>
      <div><dt>来源分类</dt><dd>{{ source.category === 'system' ? '系统数据库' : '项目数据库' }}</dd></div>
      <div><dt>关联项目</dt><dd>{{ source.project || '未关联项目' }}</dd></div>
      <div><dt>连接位置</dt><dd :title="source.location">{{ source.location || '未提供' }}</dd></div>
    </dl>

    <section v-if="loading && !catalog" class="table-list panel" aria-label="正在读取数据库结构">
      <div class="table-list__head"><span>数据表或视图</span><span>类型</span><span>字段</span><span>数据行</span><span>大小</span><span>操作</span></div>
      <div v-for="row in 7" :key="row" class="table-row table-row--skeleton">
        <span><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></span>
        <i v-for="cell in 5" :key="cell" class="ncp-skeleton"></i>
      </div>
    </section>

    <section v-else-if="source.requiresLogin && !catalog" class="connection-panel panel">
      <div class="connection-intro">
        <span class="connection-intro__icon" aria-hidden="true"><Database :size="24" /></span>
        <div>
          <span class="section-kicker">连接设置</span>
          <h2>输入数据库凭据</h2>
          <p>先测试连接确认凭据有效，再读取数据库对象目录。诊断结果只返回稳定状态码和安全的连接上下文。</p>
          <div class="connection-facts">
            <span><small>类型</small><strong>{{ driverLabel(source.driver) }}</strong></span>
            <span><small>位置</small><strong>{{ source.location || '未提供' }}</strong></span>
          </div>
        </div>
      </div>
      <ElForm class="connection-form" label-position="top" @submit.prevent="connectDatabase">
        <ElFormItem label="用户名"><ElInput v-model="credentialDraft.username" autocomplete="username" placeholder="输入数据库用户名" /></ElFormItem>
        <ElFormItem label="密码"><ElInput v-model="credentialDraft.password" type="password" show-password autocomplete="current-password" placeholder="输入数据库密码" /></ElFormItem>
        <ElFormItem label="数据库名"><ElInput v-model="credentialDraft.database" :placeholder="source.defaultDatabase || '输入数据库名称'" /></ElFormItem>
        <div class="connection-form__actions">
          <ElButton class="database-header-action" :loading="diagnosticLoading" @click="testConnection"><CircleAlert :size="16" />测试连接</ElButton>
          <ElButton type="primary" native-type="submit" :loading="loading"><ArrowRight :size="16" />连接并读取数据表</ElButton>
        </div>
        <div v-if="diagnostic" :class="['diagnostic-result', { 'diagnostic-result--success': diagnostic.connected, 'diagnostic-result--failure': !diagnostic.connected }]" role="status">
          <CheckCircle2 v-if="diagnostic.connected" :size="18" />
          <CircleAlert v-else :size="18" />
          <div><strong>{{ diagnostic.connected ? '连接诊断通过' : '连接诊断未通过' }}</strong><span>{{ diagnostic.endpoint || source.location }}<template v-if="diagnostic.database"> / {{ diagnostic.database }}</template></span></div>
          <code>代码 {{ diagnostic.code || 'unknown' }}</code>
        </div>
      </ElForm>
    </section>

    <template v-else-if="catalog">
      <section class="table-list panel" aria-label="数据表和视图列表">
        <div class="table-list__head">
          <span>数据表或视图</span><span>类型</span><span>字段</span><span>数据行</span><span>大小</span><span>详情</span>
        </div>
        <RouterLink
          v-for="table in tables"
          :key="`${table.schema}.${table.name}`"
          class="table-row"
          :to="{ name: 'database-table', params: { sourceId, table: table.name }, query: { sourceName: source.name, tableName: table.name, schema: table.schema || undefined } }"
          :aria-label="`打开 ${table.name} ${tableTypeLabel(table)}详情`"
        >
          <div class="table-name">
            <span :class="['table-name__icon', { 'table-name__icon--view': table.type.toLowerCase() === 'view' }]" aria-hidden="true"><component :is="tableTypeIcon(table)" :size="18" /></span>
            <div>
              <strong>{{ table.name }}</strong>
              <small>{{ schemaLabel(table.schema) }}</small>
              <div class="table-row__details">
                <span :class="['object-kind', { 'object-kind--view': table.type.toLowerCase() === 'view' }]">{{ tableTypeLabel(table) }}</span>
                <span v-for="key in primaryKeys(table)" :key="key" class="primary-key-badge"><KeyRound :size="12" />{{ key }}</span>
                <span v-if="!primaryKeys(table).length" class="no-primary-key">无主键</span>
              </div>
            </div>
          </div>
          <span class="table-row__metric"><small>类型</small><span :class="['object-kind', { 'object-kind--view': table.type.toLowerCase() === 'view' }]">{{ tableTypeLabel(table) }}</span></span>
          <span class="table-row__metric"><small>字段</small><Columns3 :size="14" />{{ table.columns.length }}</span>
          <span class="table-row__metric"><small>数据行</small><Rows3 :size="14" />{{ formatNumber(table.rowCount) }}</span>
          <span class="table-row__metric"><small>大小</small><HardDrive :size="14" />{{ formatBytes(table.sizeBytes) }}</span>
          <span class="table-row__affordance" aria-hidden="true"><ArrowRight :size="17" /></span>
        </RouterLink>
        <div v-if="!tables.length" class="empty-table"><Search :size="22" /><strong>没有匹配的数据表或视图</strong><span>调整搜索词后重试。</span></div>
      </section>
    </template>
  </div>

  <div v-else class="page"><section class="missing-source panel"><Database :size="26" /><h1>数据库来源不存在</h1><a href="/databases">返回数据库列表</a></section></div>
</template>

<style scoped>
.table-search {
  width: min(280px, 26vw);
}

.diagnostic-result {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border-radius: var(--ncp-radius-md);
}

.diagnostic-result > svg {
  flex: 0 0 auto;
  margin-top: 1px;
}

.diagnostic-result > div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.diagnostic-result strong {
  font-size: .8rem;
}

.diagnostic-result span {
  font-size: .77rem;
  line-height: 1.45;
}

.diagnostic-result code {
  margin-left: auto;
  padding: 2px 6px;
  border-radius: var(--ncp-radius-xs);
  font-family: var(--ncp-font-mono);
  font-size: .67rem;
  white-space: nowrap;
}

.source-summary {
  display: grid;
  grid-template-columns: minmax(180px, 1.2fr) repeat(3, minmax(135px, .8fr)) minmax(230px, 1.4fr);
  min-height: 76px;
  margin: 0;
  overflow: hidden;
}

.source-summary > div {
  display: grid;
  min-width: 0;
  align-content: center;
  gap: 3px;
  padding: 10px 15px;
  border-right: 1px solid var(--ncp-line);
}

.source-summary > div:last-child {
  border-right: 0;
}

.source-summary dt {
  color: var(--ncp-text-subtle);
  font-size: .69rem;
}

.source-summary dd {
  overflow: hidden;
  margin: 0;
  color: var(--ncp-text);
  font-size: .8rem;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source-summary__status dd {
  display: grid;
  justify-items: start;
  gap: 3px;
}

.source-summary small {
  overflow: hidden;
  color: var(--ncp-text-subtle);
  font-size: .68rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-pill,
.object-kind {
  display: inline-flex;
  width: max-content;
  min-height: 24px;
  align-items: center;
  padding: 0 8px;
  border-radius: var(--ncp-radius-pill);
  font-size: .69rem;
  font-weight: 740;
  line-height: 1;
  white-space: nowrap;
}

.connection-pill--success {
  color: var(--ncp-success-strong);
  background: var(--ncp-success-soft);
}

.connection-pill--warning {
  color: var(--ncp-warning-strong);
  background: var(--ncp-warning-soft);
}

.connection-pill--danger {
  color: var(--ncp-danger-strong);
  background: var(--ncp-danger-soft);
}

.connection-pill--neutral {
  color: var(--ncp-neutral-strong);
  background: var(--ncp-neutral-soft);
}

.connection-panel {
  display: grid;
  grid-template-columns: minmax(280px, .82fr) minmax(420px, 1.18fr);
  gap: 28px;
  padding: 24px;
}

.connection-intro {
  display: flex;
  align-items: flex-start;
  gap: 13px;
  padding: 4px;
}

.connection-intro__icon {
  display: grid;
  width: 50px;
  height: 50px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--ncp-primary-border);
  border-radius: var(--ncp-radius-lg);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.connection-intro h2 {
  margin: 3px 0 5px;
  font-size: 1.06rem;
}

.connection-intro p {
  max-width: 48ch;
  margin: 0;
  color: var(--ncp-text-muted);
  font-size: .8rem;
  line-height: 1.65;
}

.section-kicker {
  color: var(--ncp-primary-strong);
  font-size: .68rem;
  font-weight: 760;
  letter-spacing: .08em;
}

.connection-facts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin-top: 16px;
}

.connection-facts span {
  display: grid;
  gap: 2px;
}

.connection-facts small {
  color: var(--ncp-text-subtle);
  font-size: .68rem;
}

.connection-facts strong {
  max-width: 220px;
  overflow: hidden;
  color: var(--ncp-text-muted);
  font-family: var(--ncp-font-mono);
  font-size: .7rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.connection-form {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 14px;
}

.connection-form :deep(.el-form-item:last-of-type) {
  grid-column: 1 / -1;
}

.connection-form__actions {
  display: flex;
  grid-column: 1 / -1;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 2px;
}

.diagnostic-result {
  grid-column: 1 / -1;
  align-items: center;
  margin-top: 14px;
}

.diagnostic-result--success {
  border: 1px solid var(--ncp-success-border);
  background: var(--ncp-success-soft);
  color: var(--ncp-success-strong);
}

.diagnostic-result--failure {
  border: 1px solid var(--ncp-danger-border);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.diagnostic-result--success span,
.diagnostic-result--success code {
  color: var(--ncp-success-strong);
}

.diagnostic-result--failure span,
.diagnostic-result--failure code {
  color: var(--ncp-danger-strong);
}

.diagnostic-result code {
  border: 1px solid currentColor;
  opacity: .82;
}

.table-list {
  overflow: hidden;
}

.table-list__head,
.table-row {
  display: grid;
  grid-template-columns: minmax(250px, 1.55fr) 112px 90px 112px 112px 92px;
  align-items: center;
  gap: 14px;
  padding-inline: 16px;
}

.table-list__head {
  min-height: 44px;
  background: var(--ncp-table-head);
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  font-weight: 740;
}

.table-list__head span:not(:first-child) {
  text-align: center;
}

.table-row {
  position: relative;
  min-height: 92px;
  border-top: 1px solid var(--ncp-line);
  color: var(--ncp-text-muted);
  font-size: .77rem;
  text-decoration: none;
  cursor: pointer;
  transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out), box-shadow var(--ncp-duration-fast) var(--ncp-ease-out);
}

.table-row:hover,
.table-row:focus-visible {
  background: var(--ncp-table-row-hover);
  box-shadow: inset 3px 0 0 var(--ncp-primary);
}

.table-row:focus-visible {
  outline: 2px solid var(--ncp-primary);
  outline-offset: -2px;
}

.table-row > :not(.table-name):not(.table-row--skeleton) {
  justify-content: center;
  text-align: center;
}

.table-row--skeleton > span {
  display: grid;
  gap: 7px;
}

.table-row--skeleton > span i:first-child {
  width: 58%;
  height: 12px;
}

.table-row--skeleton > span i:last-child {
  width: 34%;
  height: 9px;
}

.table-row--skeleton > i {
  width: 68%;
  height: 11px;
}

.table-name {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.table-name__icon {
  display: grid;
  width: 38px;
  height: 38px;
  flex: 0 0 auto;
  place-items: center;
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.table-name__icon--view {
  background: var(--ncp-info-soft);
  color: var(--ncp-info-strong);
}

.table-name > div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.table-name strong,
.table-name small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.table-name strong {
  color: var(--ncp-text);
  font-size: .84rem;
}

.table-name small {
  color: var(--ncp-text-subtle);
  font-size: .69rem;
}

.table-row__metric {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
}

.table-row__metric small {
  display: none;
  color: var(--ncp-text-subtle);
  font-size: .68rem;
  font-weight: 700;
}

.object-kind {
  color: var(--ncp-primary-strong);
  background: var(--ncp-primary-soft);
}

.object-kind--view {
  color: var(--ncp-info-strong);
  background: var(--ncp-info-soft);
}

.table-row__details {
  display: flex;
  min-height: 22px;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
}

.primary-key-badge {
  display: inline-flex;
  min-height: 22px;
  align-items: center;
  gap: 4px;
  padding: 0 7px;
  border: 1px solid var(--ncp-primary-border);
  border-radius: var(--ncp-radius-xs);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
  font-family: var(--ncp-font-mono);
  font-size: .66rem;
  line-height: 1;
}

.no-primary-key {
  color: var(--ncp-text-subtle);
  font-size: .68rem;
}

.table-row__affordance {
  display: grid;
  width: 30px;
  height: 30px;
  place-items: center;
  justify-self: center;
  border: 1px solid transparent;
  border-radius: 50%;
  color: var(--ncp-text-subtle);
  transition: color var(--ncp-duration-fast) var(--ncp-ease-out), background-color var(--ncp-duration-fast) var(--ncp-ease-out), border-color var(--ncp-duration-fast) var(--ncp-ease-out), transform var(--ncp-duration-fast) var(--ncp-ease-out);
}

.table-row:hover .table-row__affordance,
.table-row:focus-visible .table-row__affordance {
  border-color: var(--ncp-primary-border);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
  transform: translateX(2px);
}

.empty-table {
  display: grid;
  min-height: 190px;
  place-content: center;
  justify-items: center;
  gap: 6px;
  color: var(--ncp-text-subtle);
  text-align: center;
}

.empty-table strong {
  color: var(--ncp-text-muted);
  font-size: .82rem;
}

.empty-table span {
  font-size: .75rem;
}

.missing-source {
  display: grid;
  min-height: 260px;
  place-content: center;
  gap: 8px;
  text-align: center;
}

.missing-source h1 {
  margin: 0;
  font-size: 1rem;
}

.missing-source a {
  color: var(--ncp-primary-strong);
}

@media (max-width: 1160px) {
  .source-summary {
    grid-template-columns: minmax(170px, 1.1fr) repeat(3, minmax(120px, .8fr)) minmax(190px, 1.2fr);
  }

  .table-list__head,
  .table-row {
    grid-template-columns: minmax(230px, 1.4fr) 96px 80px 100px 100px 84px;
    gap: 9px;
  }
}

@media (max-width: 900px) {
  .connection-panel {
    grid-template-columns: 1fr;
  }

  .table-search {
    width: 100%;
  }

  .source-summary {
    grid-template-columns: repeat(3, 1fr);
  }

  .source-summary > div:nth-child(3n) {
    border-right: 0;
  }

  .source-summary > div:nth-child(-n + 3) {
    border-bottom: 1px solid var(--ncp-line);
  }

  .table-list__head {
    display: none;
  }

  .table-row {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 9px 10px;
    min-height: 0;
    padding: 15px 14px;
  }

  .table-name {
    grid-column: 1;
  }

  .table-row__metric {
    justify-content: flex-start;
    color: var(--ncp-text-muted);
  }

  .table-row__metric small {
    display: inline-flex;
  }

  .table-row__metric:nth-of-type(2) {
    grid-column: 1;
  }

  .table-row__metric:nth-of-type(3) {
    grid-column: 2;
  }

  .table-row__metric:nth-of-type(4) {
    grid-column: 1;
  }

  .table-row__metric:nth-of-type(5) {
    grid-column: 2;
  }

  .table-row__affordance {
    grid-column: 2;
    grid-row: 1;
    align-self: start;
  }
}

@media (max-width: 600px) {
  .source-summary {
    grid-template-columns: 1fr 1fr;
  }

  .source-summary > div:nth-child(3n) {
    border-right: 1px solid var(--ncp-line);
  }

  .source-summary > div:nth-child(2n) {
    border-right: 0;
  }

  .source-summary > div:nth-child(-n + 4) {
    border-bottom: 1px solid var(--ncp-line);
  }

  .connection-panel {
    padding: 18px 15px;
  }

  .connection-form {
    grid-template-columns: 1fr;
  }

  .connection-form :deep(.el-form-item:last-of-type),
  .connection-form__actions,
  .diagnostic-result {
    grid-column: 1;
  }

  .connection-form__actions {
    align-items: stretch;
    flex-direction: column-reverse;
  }

  .connection-form__actions :deep(.el-button) {
    width: 100%;
    margin-left: 0;
  }

  .diagnostic-result {
    flex-wrap: wrap;
  }

  .diagnostic-result code {
    width: 100%;
    margin-left: 28px;
  }
}
</style>
