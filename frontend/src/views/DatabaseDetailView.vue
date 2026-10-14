<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
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
import type { DatabaseConnectionDiagnostic, DatabaseCredentials, DatabaseSource, DatabaseTable } from '@/api/database'
import DatabaseErrorPanel, { type DatabaseErrorState } from '@/components/DatabaseErrorPanel.vue'
import NcpSelect from '@/components/NcpSelect.vue'
import { buildDatabaseConnectionDraft, cloneDatabaseCredentials, databaseSourceBinding, emptyDatabaseTLSDraft, type DatabaseConnectionErrors, type DatabaseConnectionField } from '@/domain/database/connectionDraft'
import ResourceState from '@/components/ResourceState.vue'
import { createRequestSequenceGate } from '@/domain/requestSequence'
import { captureSession, isAbortError, isCurrentSession } from '@/session/sessionLifecycle'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useDatabaseStore } from '@/stores/database'

type ConnectionDiagnostic = DatabaseConnectionDiagnostic

type StatusTone = 'success' | 'warning' | 'danger' | 'neutral'

const databaseErrorCopy: Record<string, string> = {
  credentials_required: '连接需要数据库凭据，请填写表单后重试。',
  auth_failed: '数据库认证失败，请核对用户名和密码。',
  credential_conflict: '数据库凭据发生冲突，请重新确认后重试。',
  tls_configuration_invalid: 'TLS 设置无效，请检查 CA 证书和服务器名。',
  tls_policy_unsupported: 'Root Agent 尚不支持此 TLS 策略，请升级后重试。',
  tls_verification_failed: '证书或服务器身份校验失败，请核对 CA、服务器名和证书有效期。',
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
  DATABASE_RESPONSE_INVALID: '数据库服务返回了无法识别的数据，诊断结果尚未确认。',
  DATABASE_CONNECTION_OUTCOME_UNKNOWN: '连接结果尚未确认，服务端可能已保存本次凭据。请核对后再决定是否重新连接。',
  DATABASE_CONNECTION_CATALOG_FAILED: '连接已确认，但数据库对象目录读取失败。服务端可能已保存本次凭据。',
}

const route = useRoute()
const databaseStore = useDatabaseStore()
const sourceId = computed(() => String(route.params.sourceId ?? ''))
const source = computed(() => databaseStore.source(sourceId.value))
const catalog = computed(() => databaseStore.catalogs[sourceId.value] ?? null)
const query = ref('')
const discovering = ref(true)
const loading = ref(false)
const diagnosticLoading = ref(false)
const errorState = ref<DatabaseErrorState | null>(null)
const errorOperation = ref<'discovery' | 'catalog' | 'connection' | 'diagnostic' | null>(null)
const credentialDraft = ref<DatabaseCredentials>({})
const diagnostic = ref<ConnectionDiagnostic | null>(null)
const diagnosticRevision = ref<number | null>(null)
const draftRevision = ref(0)
const tlsDraft = ref(emptyDatabaseTLSDraft())
const fieldErrors = ref<DatabaseConnectionErrors>({})
const settingsOpen = ref(false)
const tlsEntry = ref<InstanceType<typeof ElButton>>()
const formElement = ref<InstanceType<typeof ElForm>>()
const sourceBinding = computed(() => databaseSourceBinding(source.value))
const busy = computed(() => loading.value || diagnosticLoading.value)
const networkSource = computed(() => source.value?.driver === 'mysql' || source.value?.driver === 'postgresql')
const showSettings = computed(() => networkSource.value && (settingsOpen.value || (!catalog.value && !loading.value)))
const diagnosticIsCurrent = computed(() => diagnosticRevision.value === draftRevision.value)
const trustOptions = [{ value: 'system', label: 'Root Agent 系统根证书' }, { value: 'custom', label: '自定义 PEM CA' }]
watch([credentialDraft, tlsDraft], () => {
  draftRevision.value += 1
  fieldErrors.value = {}
}, { deep: true, flush: 'sync' })
watch(() => tlsDraft.value.trust, () => { tlsDraft.value.caPEM = '' }, { flush: 'sync' })
// Element Plus 把透传描述放在选择器包装层；将本表单的说明关联到实际聚焦输入。
// 自有错误文本与字段同一轮渲染，不等待 ElFormItem 的错误插槽防抖。
watch([formElement, showSettings, () => tlsDraft.value.active, () => tlsDraft.value.trust, () => fieldErrors.value.trust], () => {
  const input = (formElement.value?.$el as HTMLElement | undefined)?.querySelector<HTMLInputElement>('[data-connection-field="trust"] input[role="combobox"]')
  if (!input) return
  input.setAttribute('aria-describedby', fieldErrors.value.trust ? 'tls-trust-help tls-trust-error' : 'tls-trust-help')
  input.setAttribute('aria-invalid', String(Boolean(fieldErrors.value.trust)))
}, { flush: 'post' })

const connectionCredentialConflict = computed(() => errorOperation.value === 'connection' && errorState.value?.code === 'credential_conflict')
const connectionOutcomeUnknown = computed(() => errorState.value?.code === 'DATABASE_CONNECTION_OUTCOME_UNKNOWN')
const connectionCatalogFailed = computed(() => errorState.value?.code === 'DATABASE_CONNECTION_CATALOG_FAILED')
const diagnosticResponseInvalid = computed(() => errorOperation.value === 'diagnostic' && errorState.value?.code === 'DATABASE_RESPONSE_INVALID')
const errorTitle = computed(() => connectionCredentialConflict.value ? '连接凭据未保存'
  : connectionOutcomeUnknown.value ? '数据库连接结果未确认'
    : connectionCatalogFailed.value ? '数据库连接已确认，目录读取失败'
      : diagnosticResponseInvalid.value ? '数据库连接诊断结果未确认'
        : errorOperation.value === 'diagnostic' ? '数据库连接诊断失败' : '数据库连接失败')

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
  if (connectionCredentialConflict.value) return { label: '连接凭据未保存', tone: 'warning' as StatusTone, detail: '其他操作已修改保存的凭据，请重新确认后再连接。' }
  if (connectionOutcomeUnknown.value) return { label: '连接结果未确认', tone: 'warning' as StatusTone, detail: '服务端可能已保存凭据，请核对后再决定是否重新连接。' }
  if (connectionCatalogFailed.value) return { label: '目录读取失败', tone: 'warning' as StatusTone, detail: '连接已确认，尚未读取到可靠的对象目录。' }
  if (diagnosticResponseInvalid.value) return { label: '诊断结果未确认', tone: 'warning' as StatusTone, detail: '未收到有效的连接诊断结果。' }
  if (errorState.value) return { label: '本次操作失败', tone: 'warning' as StatusTone, detail: '请查看本次操作的错误信息；已有目录仅供参考。' }
  if (catalog.value) return { label: '已连接', tone: 'success' as StatusTone, detail: '已读取对象目录，可打开数据表或视图。' }
  if (source.value?.reachability === 'container-internal' || source.value?.reachability === 'unreachable') {
    return { label: '主机不可达', tone: 'danger' as StatusTone, detail: '该数据库仅在容器内部可见，未发现可由 Root Agent 访问的宿主端口。' }
  }
  if (diagnostic.value?.connected && diagnosticIsCurrent.value) return { label: '诊断通过', tone: 'success' as StatusTone, detail: '凭据可用，提交连接后读取对象目录。' }
  if (source.value?.status === 'credentials_required' || source.value?.requiresLogin) {
    return { label: '需要凭据', tone: 'warning' as StatusTone, detail: '输入凭据后测试连接或读取对象目录。' }
  }
  if (source.value?.status === 'available') return { label: '可用', tone: 'success' as StatusTone, detail: '可以读取对象目录。' }
  if (source.value?.status === 'unreachable') return { label: '不可达', tone: 'danger' as StatusTone, detail: '请检查数据库服务状态。' }
  return { label: source.value?.status || '未知', tone: 'neutral' as StatusTone, detail: '连接状态尚未确认。' }
})
const sourceReachabilityBlocked = computed(() => source.value?.reachability === 'container-internal' || source.value?.reachability === 'unreachable')

const requests = createRequestSequenceGate()
const session = captureSession()
let controller: AbortController | null = null

function resetLocalState() {
  requests.invalidate()
  controller?.abort()
  controller = null
  credentialDraft.value = {}
  tlsDraft.value = emptyDatabaseTLSDraft()
  fieldErrors.value = {}
  settingsOpen.value = false
  diagnosticRevision.value = null
  diagnostic.value = null
  clearError()
  query.value = ''
  loading.value = false
  diagnosticLoading.value = false
  discovering.value = false
}

function beginRequest() {
  controller?.abort()
  controller = new AbortController()
  const sequence = requests.begin()
  const targetId = sourceId.value
  const binding = sourceBinding.value
  const signal = AbortSignal.any([controller.signal, session.signal])
  loading.value = false
  diagnosticLoading.value = false
  clearError()
  return {
    targetId,
    signal,
    current: () => !signal.aborted && requests.isLatest(sequence) &&
      isCurrentSession(session.generation) && sourceId.value === targetId && sourceBinding.value === binding,
  }
}

session.signal.addEventListener('abort', resetLocalState, { once: true })
watch([sourceId, sourceBinding], () => {
  resetLocalState()
  void initialize()
}, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => {
  resetLocalState()
  session.signal.removeEventListener('abort', resetLocalState)
})

async function initialize() {
  if (!isCurrentSession(session.generation)) return
  const request = beginRequest()
  discovering.value = true
  try {
    if (!databaseStore.source(request.targetId)) await databaseStore.refreshDiscovery()
    if (!request.current()) return
    discovering.value = false
    const current = databaseStore.source(request.targetId)
    if (!current || databaseStore.catalogs[request.targetId]) return
    loading.value = true
    if (current.requiresLogin) {
      try {
        const result = await databaseStore.connect(request.targetId, {}, request.signal)
        if (request.current()) diagnostic.value = result.diagnostic
      } catch (error) {
        if (!request.current() || isAbortError(error)) return
        if (errorCode(error) !== 'credentials_required') {
          diagnostic.value = connectionDiagnosticUnavailable(error) ? null : diagnosticFromError(error, current, {})
          setError(error, '自动连接失败，请检查保存的凭据或重新输入。', 'connection')
        }
      }
    } else {
      try {
        await databaseStore.loadCatalog(request.targetId, request.signal)
      } catch (error) {
        if (request.current() && !isAbortError(error)) setError(error, '数据库对象目录读取失败。', 'catalog')
      }
    }
  } catch (error) {
    if (request.current() && !isAbortError(error)) setError(error, '数据库发现失败，请确认 Root Agent 正常运行。', 'discovery')
  } finally {
    if (request.current()) {
      discovering.value = false
      loading.value = false
    }
  }
}

async function reloadCatalog() {
  if (!source.value || busy.value) return
  const request = beginRequest()
  loading.value = true
  try {
    await databaseStore.loadCatalog(request.targetId, request.signal)
  } catch (error) {
    if (request.current() && !isAbortError(error)) setError(error, '数据库对象目录读取失败。', 'catalog')
  } finally {
    if (request.current()) loading.value = false
  }
}

async function submitSnapshot() {
  if (!source.value || busy.value) return null
  const result = buildDatabaseConnectionDraft(credentialDraft.value, tlsDraft.value, source.value)
  fieldErrors.value = result.errors
  const first = (['username', 'database', 'trust', 'caPEM', 'serverName'] as DatabaseConnectionField[]).find(field => result.errors[field])
  if (first) {
    await nextTick()
    formElement.value?.$el.querySelector(`[data-connection-field="${first}"] input, [data-connection-field="${first}"] textarea`)?.focus()
    return null
  }
  return { credentials: cloneDatabaseCredentials(result.credentials), revision: draftRevision.value, binding: sourceBinding.value }
}

async function cancelTLSEdit() {
  tlsDraft.value = emptyDatabaseTLSDraft()
  await nextTick()
  tlsEntry.value?.$el.focus()
}

async function connectDatabase() {
  const target = source.value
  const submitted = await submitSnapshot()
  if (!target || !submitted || busy.value || submitted.binding !== sourceBinding.value) return
  const { credentials, revision } = submitted
  settingsOpen.value = true
  const request = beginRequest()
  loading.value = true
  diagnostic.value = null
  try {
    const result = await databaseStore.connect(request.targetId, credentials, request.signal)
    if (request.current()) {
      diagnostic.value = result.diagnostic
      diagnosticRevision.value = revision
      if (draftRevision.value === revision) {
        credentialDraft.value = {}
        tlsDraft.value = emptyDatabaseTLSDraft()
        settingsOpen.value = false
      }
    }
  } catch (error) {
    if (!request.current() || isAbortError(error)) return
    diagnostic.value = connectionDiagnosticUnavailable(error) ? null : diagnosticFromError(error, target, credentials)
    diagnosticRevision.value = revision
    setError(error, '数据库连接失败，请检查登录信息。', 'connection')
  } finally {
    if (request.current()) loading.value = false
  }
}

async function testConnection() {
  const target = source.value
  const submitted = await submitSnapshot()
  if (!target || !submitted || busy.value || submitted.binding !== sourceBinding.value) return
  const { credentials, revision } = submitted
  const request = beginRequest()
  diagnosticLoading.value = true
  diagnostic.value = null
  try {
    const result = await testDatabaseConnection({ sourceId: request.targetId, credentials }, request.signal)
    if (!request.current()) return
    diagnostic.value = result
    diagnosticRevision.value = revision
    if (!result.connected) {
      const code = result.code || 'unreachable'
      setError(new NcpApiError(code, knownErrorCopy(code) || '连接诊断未通过。'), '连接诊断未通过。', 'diagnostic')
    }
  } catch (error) {
    if (!request.current() || isAbortError(error)) return
    diagnostic.value = errorCode(error) === 'DATABASE_RESPONSE_INVALID' ? null : diagnosticFromError(error, target, credentials)
    diagnosticRevision.value = revision
    setError(error, '连接诊断失败，请稍后重试。', 'diagnostic')
  } finally {
    if (request.current()) diagnosticLoading.value = false
  }
}

function connectionDiagnosticUnavailable(error: unknown) {
  // 保存冲突只证明本次没有写入凭据，不能据此伪造连接诊断失败。
  return ['credential_conflict', 'DATABASE_CONNECTION_OUTCOME_UNKNOWN', 'DATABASE_CONNECTION_CATALOG_FAILED'].includes(errorCode(error))
}

function diagnosticFromError(error: unknown, target: DatabaseSource, credentials: DatabaseCredentials): ConnectionDiagnostic {
  return {
    connected: false,
    code: errorCode(error),
    driver: target.driver,
    endpoint: target.location,
    database: credentials.database || target.defaultDatabase,
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

function knownErrorCopy(code: string) {
  return Object.hasOwn(databaseErrorCopy, code) ? databaseErrorCopy[code] : undefined
}

function errorCode(error: unknown) {
  if (error instanceof NcpApiError) return error.code
  if (error instanceof Error && knownErrorCopy(error.message)) return error.message
  return 'DATABASE_OPERATION_FAILED'
}

function errorMessage(error: unknown, fallback: string) {
  const code = errorCode(error)
  if (errorOperation.value === 'connection' && code === 'credential_conflict') {
    return '其他操作已修改保存的凭据，本次连接未保存凭据。请重新确认后再连接。'
  }
  return knownErrorCopy(code) || fallback
}

function errorNextStep(error: unknown) {
  switch (errorCode(error)) {
    case 'credentials_required': return '输入凭据后重新测试连接。'
    case 'auth_failed': return '核对用户名、密码和数据库名；密码不会显示在错误信息中。'
    case 'credential_conflict': return errorOperation.value === 'connection'
      ? '核对当前凭据后手动重新连接；不会自动重试。' : '核对当前凭据后重试。'
    case 'tls_configuration_invalid':
    case 'tls_verification_failed': return '核对证书与服务器身份后再测试；不会跳过校验或自动降级。'
    case 'tls_policy_unsupported': return '升级 Root Agent 后手动重试；不会移除 TLS 策略重新连接。'
    case 'unreachable': return '确认数据库服务运行且地址、端口可访问。'
    case 'database_not_found': return '确认数据库名、schema 或对象名称。'
    case 'permission_denied': return '改用具备目录读取权限的账号。'
    case 'sql_invalid': return '检查 SQL 语句和参数后重试。'
    case 'constraint_failed': return '检查必填字段、唯一键和外键约束。'
    case 'DATABASE_CONNECTION_OUTCOME_UNKNOWN':
    case 'DATABASE_CONNECTION_CATALOG_FAILED': return '先核对服务端连接与凭据状态；重新连接可能再次保存凭据，不会自动重试。'
    case 'DATABASE_RESPONSE_INVALID': return '重新测试连接以获取有效诊断；测试连接不会保存凭据。'
    default: return '稍后重试；若持续失败，请记录错误码联系管理员。'
  }
}

function setError(error: unknown, fallback: string, operation: NonNullable<typeof errorOperation.value>) {
  errorOperation.value = operation
  const code = errorCode(error)
  errorState.value = { code, message: errorMessage(error, fallback), nextStep: errorNextStep(error) }
}

function clearError() {
  errorOperation.value = null
  errorState.value = null
}
</script>

<template>
  <div v-if="source" class="page workspace-page database-detail">
    <WorkspaceHeader :title="source.name" :description="`${source.project || '未关联项目'} / ${source.module || '未标注用途'}`" :icon="Database" :stats="stats">
      <template #actions>
        <ElButton tag="a" href="/databases" class="database-header-action"><ArrowLeft :size="16" />返回列表</ElButton>
        <ElButton v-if="catalog && networkSource" class="database-header-action" :aria-expanded="settingsOpen" aria-controls="database-connection-settings" @click="settingsOpen = !settingsOpen">连接设置</ElButton>
        <ElButton v-if="catalog" class="database-header-action" :disabled="busy" :loading="loading" @click="reloadCatalog"><RefreshCw :size="16" />刷新结构</ElButton>
      </template>
      <template #tools>
        <ElInput v-if="catalog" v-model="query" class="table-search" clearable placeholder="搜索数据表或视图" aria-label="搜索数据表或视图">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <ResourceState v-if="errorState && errorOperation === 'catalog'" state="error" title="数据库对象目录读取失败" :message="errorState.message" :code="errorState.code" :next-step="errorState.nextStep" retry-label="重新读取目录" @retry="reloadCatalog" />
    <DatabaseErrorPanel v-else-if="errorState" :title="errorTitle" :error="errorState" />

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

    <section v-if="loading && !catalog && !settingsOpen" class="table-list panel" aria-label="正在读取数据库结构">
      <div class="table-list__head"><span>数据表或视图</span><span>类型</span><span>字段</span><span>数据行</span><span>大小</span><span>操作</span></div>
      <div v-for="row in 7" :key="row" class="table-row table-row--skeleton">
        <span><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></span>
        <i v-for="cell in 5" :key="cell" class="ncp-skeleton"></i>
      </div>
    </section>

    <section v-if="showSettings" id="database-connection-settings" class="connection-panel panel" aria-label="连接设置">
      <div class="connection-intro">
        <span class="connection-intro__icon" aria-hidden="true"><Database :size="24" /></span>
        <div>
          <span class="section-kicker">连接设置</span>
          <h2>输入数据库凭据</h2>
          <p>手动凭据测试通过后保存；测试连接本身不保存凭据。每次连接并读取数据表都会重新测试。</p>
          <div class="connection-facts">
            <span><small>类型</small><strong>{{ driverLabel(source.driver) }}</strong></span>
            <span><small>位置</small><strong>{{ source.location || '未提供' }}</strong></span>
          </div>
        </div>
      </div>
      <div v-if="sourceReachabilityBlocked" class="connection-unavailable" role="alert">{{ source.reachability === 'container-internal' ? 'DATABASE_URL 使用了容器内部 service hostname，Root Agent 无法从宿主机直接连接。请发布数据库端口或改用 host network。' : '未发现已发布的数据库端口，NCP 不会伪造宿主连接地址。' }}</div>
      <ElForm v-else ref="formElement" class="connection-form" label-position="top" @submit.prevent="connectDatabase">
        <ElFormItem label="用户名" :error="fieldErrors.username" :show-message="false" data-connection-field="username">
          <ElInput v-model="credentialDraft.username" autocomplete="username" aria-label="用户名" :aria-invalid="Boolean(fieldErrors.username)" :aria-describedby="fieldErrors.username ? 'connection-username-error' : undefined" placeholder="输入数据库用户名" />
          <span v-if="fieldErrors.username" id="connection-username-error" class="field-description field-description--error">{{ fieldErrors.username }}</span>
        </ElFormItem>
        <ElFormItem label="密码"><ElInput v-model="credentialDraft.password" type="password" show-password autocomplete="current-password" aria-label="密码" placeholder="输入数据库密码" /></ElFormItem>
        <ElFormItem label="数据库名" class="connection-form__wide" :error="fieldErrors.database" :show-message="false" data-connection-field="database">
          <ElInput v-model="credentialDraft.database" :placeholder="source.defaultDatabase || '输入数据库名称'" aria-label="数据库名" :aria-invalid="Boolean(fieldErrors.database)" :aria-describedby="fieldErrors.database ? 'connection-database-error' : undefined" />
          <span v-if="fieldErrors.database" id="connection-database-error" class="field-description field-description--error">{{ fieldErrors.database }}</span>
        </ElFormItem>
        <div class="tls-settings connection-form__wide">
          <p class="tls-status">TLS 状态未确认</p>
          <p id="tls-inheritance-help" class="field-description">未提供新的 TLS 设置时，服务端沿用已保存的严格策略；没有已保存策略时，保持原连接行为。</p>
          <ElButton v-if="!tlsDraft.active" ref="tlsEntry" aria-describedby="tls-inheritance-help" @click="tlsDraft.active = true">设置证书校验 TLS</ElButton>
          <fieldset v-else class="tls-fields" aria-label="证书校验 TLS">
            <legend>证书校验 TLS</legend>
            <p class="tls-mode">verify-full · 校验证书链与服务器身份</p>
            <p class="field-description">本次明确设置会替换原 TLS 策略；原 CA 不会回填。请填写本次用户名和数据库名，不会借用已保存密码。</p>
            <ElFormItem label="信任来源" :error="fieldErrors.trust" :show-message="false" data-connection-field="trust">
              <NcpSelect v-model="tlsDraft.trust" :options="trustOptions" accessible-label="信任来源" placeholder="请选择信任来源" :aria-invalid="Boolean(fieldErrors.trust)" :aria-describedby="fieldErrors.trust ? 'tls-trust-help tls-trust-error' : 'tls-trust-help'" />
              <span v-if="fieldErrors.trust" id="tls-trust-error" class="field-description field-description--error">{{ fieldErrors.trust }}</span>
              <span id="tls-trust-help" class="field-description">{{ tlsDraft.trust === 'custom' ? '只信任本次提供的 CA，不与系统根合并。' : tlsDraft.trust === 'system' ? '使用 Root Agent 所在系统的根证书。' : '请明确选择本次连接的信任来源。' }}</span>
            </ElFormItem>
            <ElFormItem v-if="tlsDraft.trust === 'custom'" label="CA 证书（PEM）" :error="fieldErrors.caPEM" :show-message="false" data-connection-field="caPEM">
              <ElInput v-model="tlsDraft.caPEM" type="textarea" :rows="7" resize="vertical" aria-label="CA 证书（PEM）" autocomplete="off" :spellcheck="false" :aria-invalid="Boolean(fieldErrors.caPEM)" :aria-describedby="fieldErrors.caPEM ? 'tls-ca-help tls-ca-error' : 'tls-ca-help'" />
              <span id="tls-ca-help" class="field-description">最多 64 KiB、32 张 CA 证书；只接受 PEM 内容，不接受路径、网址或私钥。证书内容由 Root Agent 校验。</span>
              <span v-if="fieldErrors.caPEM" id="tls-ca-error" class="field-description field-description--error">{{ fieldErrors.caPEM }}</span>
            </ElFormItem>
            <ElFormItem label="服务器名（可选）" :error="fieldErrors.serverName" :show-message="false" data-connection-field="serverName">
              <ElInput v-model="tlsDraft.serverName" aria-label="服务器名（可选）" placeholder="db.example.com" autocomplete="off" :spellcheck="false" :aria-invalid="Boolean(fieldErrors.serverName)" :aria-describedby="fieldErrors.serverName ? 'tls-server-name-help tls-server-name-error' : 'tls-server-name-help'" />
              <span id="tls-server-name-help" class="field-description">默认校验实际连接主机；填写后只改变证书身份校验 / SNI，不改变连接地址。</span>
              <span v-if="fieldErrors.serverName" id="tls-server-name-error" class="field-description field-description--error">{{ fieldErrors.serverName }}</span>
            </ElFormItem>
            <ElButton @click="cancelTLSEdit">撤销本次 TLS 编辑</ElButton>
            <p class="field-description">撤销后仍沿用服务端已保存策略。</p>
          </fieldset>
        </div>
        <div class="connection-form__actions">
          <ElButton class="database-header-action" :disabled="busy" :loading="diagnosticLoading" @click="testConnection"><CircleAlert :size="16" />测试连接</ElButton>
          <ElButton type="primary" native-type="submit" :disabled="busy" :loading="loading"><ArrowRight :size="16" />连接并读取数据表</ElButton>
        </div>
        <div v-if="diagnostic" :class="['diagnostic-result', { 'diagnostic-result--success': diagnostic.connected && diagnosticIsCurrent, 'diagnostic-result--failure': !diagnostic.connected && diagnosticIsCurrent, 'diagnostic-result--previous': !diagnosticIsCurrent }]" role="status" aria-live="polite">
          <CheckCircle2 v-if="diagnostic.connected && diagnosticIsCurrent" :size="18" />
          <CircleAlert v-else :size="18" />
          <div><strong>{{ diagnosticIsCurrent ? (diagnostic.connected ? '连接诊断通过' : '连接诊断未通过') : '上次提交结果' }}</strong><span>{{ diagnostic.endpoint || source.location }}<template v-if="diagnostic.database"> / {{ diagnostic.database }}</template></span></div>
          <code>代码 {{ diagnostic.code || 'unknown' }}</code>
        </div>
      </ElForm>
    </section>

    <template v-if="catalog">
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

  <div v-else-if="discovering" class="page" aria-busy="true"><ResourceState state="loading" title="正在发现数据库来源" message="发现完成后显示连接状态。" /></div>
  <div v-else-if="errorState" class="page"><ResourceState state="error" title="数据库发现失败" :message="errorState.message" :code="errorState.code" :next-step="errorState.nextStep" @retry="initialize" /></div>
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
  white-space: normal;
  overflow-wrap: anywhere;
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
  font-size: max(12px, .875rem);
  line-height: 1.5;
}

.source-summary dd {
  min-width: 0;
  margin: 0;
  color: var(--ncp-text);
  font-size: max(12px, .875rem);
  line-height: 1.5;
  font-weight: 700;
  overflow-wrap: anywhere;
  white-space: normal;
}

.source-summary__status dd {
  display: grid;
  justify-items: start;
  gap: 3px;
}

.source-summary small {
  min-width: 0;
  color: var(--ncp-text-subtle);
  font-size: max(12px, .875rem);
  line-height: 1.5;
  overflow-wrap: anywhere;
  white-space: normal;
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

.source-summary .connection-pill {
  font-size: max(12px, .875rem);
  line-height: 1.5;
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
  grid-template-columns: minmax(0, .82fr) minmax(0, 1.18fr);
  gap: 28px;
  padding: 24px;
}

.connection-unavailable {
  grid-column: 1 / -1;
  padding: 14px 16px;
  border: 1px solid var(--ncp-danger-border);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
  font-size: .82rem;
  line-height: 1.6;
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
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  min-width: 0;
  gap: 0 14px;
}

.connection-form__wide {
  grid-column: 1 / -1;
}

.tls-settings {
  min-width: 0;
  padding: 16px 0;
  border-top: 1px solid var(--ncp-line);
}

.tls-fields {
  min-width: 0;
  margin: 12px 0 0;
  padding: 14px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-md);
}

.tls-fields legend,
.tls-status,
.tls-mode {
  font-size: .875rem;
  line-height: 1.6;
  font-weight: 650;
}

.tls-status,
.tls-mode { margin: 0 0 8px; }

.field-description {
  display: block;
  width: 100%;
  margin: 6px 0 12px;
  color: var(--ncp-text-muted);
  font-size: .8125rem;
  line-height: 1.65;
  overflow-wrap: anywhere;
}

.field-description--error { color: var(--ncp-danger-strong); }

.tls-fields :deep(.el-textarea),
.tls-fields :deep(.el-select) {
  min-width: 0;
  width: 100%;
}

.tls-fields :deep(.el-form-item__error) {
  position: static;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.connection-form__actions {
  display: flex;
  grid-column: 1 / -1;
  justify-content: flex-end;
  flex-wrap: wrap;
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

.diagnostic-result--previous {
  border: 1px solid var(--ncp-line);
  background: var(--ncp-neutral-soft);
  color: var(--ncp-text-muted);
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
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .source-summary > div:last-child {
    grid-column: 1 / -1;
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

  .connection-form__wide,
  .connection-form__actions,
  .diagnostic-result {
    grid-column: 1;
  }

  .connection-form__actions {
    align-items: stretch;
    flex-direction: column;
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
