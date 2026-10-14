<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowLeft,
  Braces,
  CircleAlert,
  CirclePlus,
  Columns3,
  Eye,
  FileCode2,
  Info,
  KeyRound,
  Pencil,
  Play,
  RefreshCw,
  Rows3,
  Table2,
  Trash2,
} from '@lucide/vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElMessage,
  ElSwitch,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'

import {
  deleteDatabaseRow,
  executeDatabaseSQL,
  insertDatabaseRow,
  loadDatabaseRows,
  updateDatabaseRow,
  type DatabaseColumn,
  type DatabaseConnection,
  type DatabaseQueryResultMode,
  type DatabaseRows,
  type DatabaseValue,
  type QueryResult,
} from '@/api/database'
import { NcpApiError } from '@/api/system'
import { DatabaseInsertOutcomeUnknownError } from '@/api/databaseInsertOutcome'
import { DatabaseRowWriteOutcomeUnknownError, type DatabaseRowWriteOperation } from '@/api/databaseRowWriteOutcome'
import { DatabaseValueError, databaseEditorKind, databaseEditorValue, databaseWriteMode, resolveDatabaseValue } from '@/domain/database/valueConversion'
import { databaseColumnContract } from '@/domain/database/columnContract'
import { classifySqlRisk } from '@/domain/database/sqlRisk'
import DatabaseErrorPanel, { type DatabaseErrorState } from '@/components/DatabaseErrorPanel.vue'
import DatabaseCellEditor from '@/components/DatabaseCellEditor.vue'
import ConfirmDangerDialog from '@/components/ConfirmDangerDialog.vue'
import SqlEditor from '@/components/SqlEditor.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useDatabaseStore } from '@/stores/database'
import { useListPreference } from '@/composables/useListPreference'
import { captureSession, isAbortError, isCurrentSession, type SessionContext } from '@/session/sessionLifecycle'

type TableMode = 'data' | 'overview' | 'structure' | 'definition' | 'sql'
type RowDialogMode = 'insert' | 'edit'
type RowsRefreshOutcome = { status: 'success' | 'stale' } | { status: 'failed'; error: DatabaseErrorState }

interface SQLEditorScope {
  targetKey: string
  routeGeneration: number
  session: SessionContext
}

interface SQLExecutionSnapshot {
  scope: SQLEditorScope
  connection: DatabaseConnection
  sql: string
  resultMode: DatabaseQueryResultMode
}

const resultModeLabels: Record<DatabaseQueryResultMode, string> = {
  auto: '自动',
  rows: '返回行',
  exec: '执行（不展示返回行）',
}

interface DatabaseErrorCopy {
  message: string
  nextStep: string
}

const databaseErrorCopy: Record<string, DatabaseErrorCopy> = {
  credentials_required: { message: '连接需要数据库凭据。', nextStep: '返回数据源详情页，输入凭据后重新测试连接。' },
  auth_failed: { message: '数据库认证失败，请核对用户名、密码和数据库名。', nextStep: '核对连接信息后重新测试；错误提示不会显示或保存密码。' },
  unreachable: { message: '数据库服务不可达，请检查地址和端口。', nextStep: '确认数据库服务运行且主机、端口可访问后重试。' },
  database_not_found: { message: '目标数据库或数据表不存在。', nextStep: '确认数据库名、schema 或表名后重新读取目录。' },
  permission_denied: { message: '当前账号没有执行该操作的权限。', nextStep: '改用具备目录读取或操作权限的账号后重试。' },
  sql_invalid: { message: 'SQL 或数据库请求无效。', nextStep: '检查 SQL 语句、表名和参数后重试。' },
  constraint_failed: { message: '数据约束未满足，请检查提交内容。', nextStep: '检查必填字段、唯一键和外键约束后重试。' },
  result_too_large: { message: '结果超过 1 MiB 响应预算，无法展示。', nextStep: '减少每页行数；若单行仍超预算，请在 SQL 中只选择必要字段或预览大字段的部分内容。' },
  agent_unavailable: { message: 'Root Agent 暂不可用，请确认代理服务状态。', nextStep: '确认 Root Agent 正常运行后重新读取数据表。' },
  timeout: { message: '数据库操作超时，请稍后重试。', nextStep: '检查网络和数据库负载，稍后重新读取。' },
  credential_store_unavailable: { message: '数据库凭据服务暂不可用。', nextStep: '确认凭据服务正常后返回详情页重新测试连接。' },
  credential_corrupt: { message: '保存的数据库凭据不可用，请重新连接。', nextStep: '返回数据源详情页重新输入凭据。' },
  key_unavailable: { message: '数据库凭据加密服务不可用。', nextStep: '确认凭据加密服务正常后重试。' },
  key_rotation_failed: { message: '数据库凭据密钥更新失败。', nextStep: '稍后重试；若持续失败，请记录错误码联系管理员。' },
  migration_failed: { message: '数据库凭据存储迁移失败。', nextStep: '稍后重试；若持续失败，请记录错误码联系管理员。' },
  DATABASE_OPERATION_FAILED: { message: '数据库操作失败，请稍后重试。', nextStep: '稍后重试；若持续失败，请记录错误码联系管理员。' },
  DATABASE_VALUE_INVALID: { message: '字段值格式不正确。', nextStep: '按字段类型填写值；空字符串不会自动转换成 NULL。' },
}

const route = useRoute()
const router = useRouter()
const databaseStore = useDatabaseStore()
const { pageSize } = useListPreference('database.table.rows')
const sourceId = computed(() => String(route.params.sourceId ?? ''))
const tableName = computed(() => String(route.params.table ?? ''))
const schema = computed(() => String(route.query.schema ?? ''))
const source = computed(() => databaseStore.source(sourceId.value))
const catalog = computed(() => databaseStore.catalogs[sourceId.value] ?? null)
const table = computed(() => catalog.value?.tables.find((item) => item.name === tableName.value && item.schema === schema.value) ?? null)
const mode = ref<TableMode>('data')
const tableRows = ref<DatabaseRows | null>(null)
// 可见行归属于最后一次成功读取的目标；路由已变但旧 DOM 尚未重绘时不能借用旧行。
const loadedRowsTargetKey = ref<string | null>(null)
const acceptedRowsContract = ref<{ targetKey: string; columns: string | null } | null>(null)
// 结构保护与写结果未知各自独立；普通刷新、关闭草稿、切换后返回均不能解锁。
const schemaDriftTargets = ref(new Set<string>())
const schemaReloadPending = ref(false)
const schemaReloadError = ref<DatabaseErrorState | null>(null)
let schemaReloadSequence = 0
let schemaReloadController: AbortController | null = null
const rowsLoading = ref(false)
const rowsInitialized = ref(false)
const errorState = ref<DatabaseErrorState | null>(null)
const offset = ref(0)
const sortColumn = ref('')
const sortDirection = ref('')
const rowDialogOpen = ref(false)
const rowDialogMode = ref<RowDialogMode>('insert')
const rowForm = ref<Record<string, string>>({})
// 草稿保留打开时的编辑合同，不能被共享目录的后续读取静默重解释。
const rowDraftColumns = ref<DatabaseColumn[]>([])
const rowNullFields = ref<Record<string, boolean>>({})
const originalKeys = ref<Record<string, DatabaseValue>>({})
const mutationPending = ref(false)
const rowDraftGeneration = ref(0)
const rowDraftRevision = ref(0)
const rowDraftTargetKey = ref<string | null>(null)
const deleteConfirmationTargetKey = ref<string | null>(null)
const rowWriteDraftGeneration = ref<number | null>(null)
const dangerGeneration = ref(0)
// 按整表目标保护 UPDATE/DELETE，绝不把可能敏感的主键或行值写进 ledger。
const rowWriteAttempts = ref(new Map<string, { operation: DatabaseRowWriteOperation; status: 'pending' | 'unknown' }>())
const rowWriteRefreshError = ref<{ operation: DatabaseRowWriteOperation; error: DatabaseErrorState } | null>(null)
let rowWriteController: AbortController | null = null
const insertDraftGeneration = ref<number | null>(null)
// 仅在当前页面实例内保留目标及状态，不保存提交值、连接凭据或原始异常。
// 这不是后端幂等：完整重载/页面卸载后不能依靠此锁判断此前是否已写入。
const insertAttempts = ref(new Map<string, 'pending' | 'unknown'>())
const insertRefreshError = ref<DatabaseErrorState | null>(null)
let insertRefreshSequence = 0
let insertController: { key: string; controller: AbortController } | null = null
const sql = ref('')
const resultMode = ref<DatabaseQueryResultMode>('auto')
const queryResult = ref<QueryResult | null>(null)
const queryPending = ref(false)
const queryError = ref<DatabaseErrorState | null>(null)
const queryRefreshError = ref<DatabaseErrorState | null>(null)
const dangerOpen = ref(false)
const dangerBusy = ref(false)
const dangerAction = ref<'delete-row' | 'sql'>('delete-row')
const pendingRow = ref<Record<string, DatabaseValue> | null>(null)
const pendingQuery = ref<SQLExecutionSnapshot | null>(null)
const routeRevision = ref(0)
let routeSequence = 0
let rowsSequence = 0
let querySequence = 0
let mutationSequence = 0
let rowsController: AbortController | null = null
let initializeController: AbortController | null = null
let queryController: AbortController | null = null
let mutationController: AbortController | null = null
// SQL 编辑器归属不依赖行读取是否成功，也不能借用下一会话或尚未重绘的新路由。
let sqlEditorScope: SQLEditorScope | null = null
let releaseSQLEditorScope: (() => void) | null = null

const columns = computed(() => table.value?.columns ?? [])
const primaryKeyColumns = computed(() => columns.value.filter((column) => column.primaryKey))
// schema / 表名可合法包含分隔符，使用元组编码避免不同目标共享草稿归属。
const routeKey = computed(() => JSON.stringify([sourceId.value, schema.value, tableName.value]))
const columnsContract = computed(() => databaseColumnContract(columns.value))
const rowsContractMismatch = computed(() => acceptedRowsContract.value?.targetKey === routeKey.value
  && (columnsContract.value === null || acceptedRowsContract.value.columns !== columnsContract.value))
const draftContractMismatch = computed(() => rowDialogOpen.value && rowDraftTargetKey.value === routeKey.value
  && (columnsContract.value === null || databaseColumnContract(rowDraftColumns.value) !== columnsContract.value))
const schemaDrift = computed(() => schemaDriftTargets.value.has(routeKey.value) || rowsContractMismatch.value || draftContractMismatch.value)
const rowSchemaReady = computed(() => !schemaDrift.value && !schemaReloadPending.value
  && loadedRowsTargetKey.value === routeKey.value && columnsContract.value !== null)
const insertTargetKey = routeKey
const insertStatus = computed(() => insertAttempts.value.get(insertTargetKey.value))
const insertPending = computed(() => insertStatus.value === 'pending')
const insertUnknown = computed(() => insertStatus.value === 'unknown')
const rowWriteAttempt = computed(() => rowWriteAttempts.value.get(routeKey.value))
const rowWriteUnknown = computed(() => rowWriteAttempt.value?.status === 'unknown')
const rowWritePending = computed(() => rowWriteAttempt.value?.status === 'pending')
const rowDraftPending = computed(() => rowDialogMode.value === 'insert'
  ? insertPending.value && insertDraftGeneration.value === rowDraftGeneration.value
  : rowWritePending.value && rowWriteAttempt.value?.operation === 'update' && rowWriteDraftGeneration.value === rowDraftGeneration.value)
const stablePagination = computed(() => tableRows.value?.ordering?.stable !== false)
const isView = computed(() => table.value?.type.toLowerCase() === 'view')
const canInsertRows = computed(() => Boolean(table.value) && !isView.value && rowSchemaReady.value)
const canEditRows = computed(() => canInsertRows.value && primaryKeyColumns.value.length > 0)
const stats = computed<WorkspaceStat[]>(() => [
  { label: '数据行', value: formatNumber(table.value?.rowCount), tone: 'success' },
  { label: '表大小', value: formatBytes(table.value?.sizeBytes), tone: 'warning' },
  { label: '字段', value: columns.value.length },
])
// SQL 列名只作标签；重复、空白或带点的别名不能作为取值键。
const queryColumns = computed(() => queryResult.value?.columns.map((label, index) => ({ key: `column_${index}`, label })) ?? [])
const queryRows = computed(() => {
  if (!queryResult.value) return []
  return queryResult.value.rows.map((row) =>
    Object.fromEntries(queryColumns.value.map((column, index) => [column.key, row[index]])))
})
const sqlRisk = computed(() => classifySqlRisk(sql.value))
const affectedSummary = computed(() => queryResult.value?.rowsAffectedKnown === true
  ? `执行完成；影响 ${queryResult.value.rowsAffected} 行（驱动报告）。`
  : '执行完成；数据库未提供可用的影响行数。')
const queryResultTitle = computed(() => {
  const result = queryResult.value
  if (!result?.columns.length) return '执行结果'
  if (result.truncated && !result.rows.length) return '结果行未能展示'
  return `返回 ${result.rows.length} 行`
})
const queryTruncationSummary = computed(() => {
  const result = queryResult.value
  if (!result?.truncated) return ''
  const reason = result.truncationReason === 'byte_limit' ? '受 1 MiB 结果字节预算限制。'
    : result.truncationReason === 'row_limit' ? '达到 500 行展示上限。'
      : '返回结果已截断。'
  const retained = result.rows.length ? `仅展示前 ${result.rows.length} 行完整结果，单元内容未被裁剪。`
    : '没有可展示的完整行，不代表查询无匹配。'
  return `${reason}${retained}截断不限制写入范围，也不代表写入未生效或已回滚。`
})
const queryEmptyText = computed(() => queryResult.value?.truncated ? '完整结果行未能展示。' : '无返回行')
const dangerTitle = computed(() => dangerAction.value === 'sql' ? (classifySqlRisk(pendingQuery.value?.sql ?? '')?.title ?? '确认执行 SQL') : '确认删除数据行')
const dangerDescription = computed(() => dangerAction.value === 'sql'
  ? (classifySqlRisk(pendingQuery.value?.sql ?? '')?.confirmation ?? '请确认 SQL 的目标数据库和影响范围。')
  : '将永久删除当前数据行并立即写入数据库；删除本身无法在控制面撤销。')
const dangerImpact = computed(() => dangerAction.value === 'sql'
  ? [`结果模式：${resultModeLabels[pendingQuery.value?.resultMode ?? 'auto']}`, '可能影响多行数据、其他表或数据库结构；返回行模式也会执行写操作']
  : ['永久删除选中的数据行'])
const dangerRetained = computed(() => dangerAction.value === 'sql' ? [] : ['其他数据行不会被删除'])

async function initialize() {
  const sequence = ++routeSequence
  const session = captureSession()
  const requestKey = routeKey.value
  resetRouteState()
  initializeController?.abort()
  const localController = new AbortController()
  const signal = AbortSignal.any([localController.signal, session.signal])
  const ownsInitialization = () => sequence === routeSequence && requestKey === routeKey.value
    && !signal.aborted && isCurrentSession(session.generation)
  initializeController = localController
  try {
    if (!databaseStore.sources.length) await databaseStore.refreshDiscovery()
    if (!ownsInitialization()) return
    if (!catalog.value && source.value && !source.value.requiresLogin) await databaseStore.loadCatalog(sourceId.value, signal)
  } catch (error) {
    if (!ownsInitialization() || isAbortError(error)) return
    setError(error, '数据库对象目录读取失败。')
    return
  }
  if (!ownsInitialization()) return
  if (!catalog.value && source.value?.requiresLogin) {
    await router.replace({ name: 'database-detail', params: { sourceId: sourceId.value }, query: { sourceName: source.value.name } })
    return
  }
  if (!table.value) return
  sqlEditorScope = Object.freeze({ targetKey: requestKey, routeGeneration: sequence, session })
  const revoke = () => invalidateSQLScope()
  session.signal.addEventListener('abort', revoke, { once: true })
  releaseSQLEditorScope = () => session.signal.removeEventListener('abort', revoke)
  sql.value = `SELECT * FROM ${sqlTableName()} LIMIT ${pageSize.value}`
  await refreshRows(true, offset.value, session)
}

function resetRouteState() {
  invalidateSQLScope()
  schemaReloadSequence += 1
  schemaReloadController?.abort()
  schemaReloadController = null
  schemaReloadPending.value = false
  schemaReloadError.value = null
  interruptInsert()
  rowWriteController?.abort()
  rowWriteController = null
  rowWriteRefreshError.value = null
  clearRowDraft()
  insertRefreshSequence += 1
  insertRefreshError.value = null
  rowsSequence += 1
  querySequence += 1
  mutationSequence += 1
  rowsController?.abort()
  initializeController?.abort()
  queryController?.abort()
  mutationController?.abort()
  initializeController = null
  rowsController = null
  queryController = null
  mutationController = null
  tableRows.value = null
  loadedRowsTargetKey.value = null
  acceptedRowsContract.value = null
  rowsLoading.value = false
  rowsInitialized.value = false
  errorState.value = null
  queryResult.value = null
  queryPending.value = false
  queryError.value = null
  queryRefreshError.value = null
  resultMode.value = 'auto'
  pendingQuery.value = null
  pendingRow.value = null
  offset.value = 0
  sortColumn.value = ''
  sortDirection.value = ''
  mode.value = 'data'
  rowDialogOpen.value = false
  dangerOpen.value = false
  dangerBusy.value = false
  mutationPending.value = false
}

function connection() {
  return databaseStore.connection(sourceId.value)
}

function ownsSQLScope(scope: SQLEditorScope) {
  return scope === sqlEditorScope && scope.targetKey === routeKey.value && scope.routeGeneration === routeSequence
    && !scope.session.signal.aborted && isCurrentSession(scope.session.generation)
}

function invalidateSQLScope() {
  releaseSQLEditorScope?.()
  releaseSQLEditorScope = null
  sqlEditorScope = null
  querySequence += 1
  // 仅撤销客户端观察；中止信号不表示已经派发的 SQL 未执行或已经回滚。
  queryController?.abort()
  queryController = null
  queryPending.value = false
  queryResult.value = null
  queryError.value = null
  queryRefreshError.value = null
  pendingQuery.value = null
  sql.value = ''
  if (dangerAction.value === 'sql') {
    dangerOpen.value = false
    dangerBusy.value = false
  }
}

async function refreshRows(reportError = true, requestedOffset = offset.value, session = captureSession(), schemaReadSequence?: number): Promise<RowsRefreshOutcome> {
  if (!table.value || schemaReloadPending.value && schemaReadSequence !== schemaReloadSequence) return { status: 'stale' }
  const sequence = ++rowsSequence
  const requestKey = routeKey.value
  rowsController?.abort()
  const controller = new AbortController()
  const signal = AbortSignal.any([controller.signal, session.signal])
  rowsController = controller
  rowsLoading.value = true
  clearError()
  try {
    const result = await loadDatabaseRows({
      ...connection(),
      schema: table.value.schema,
      table: table.value.name,
      limit: pageSize.value,
      offset: requestedOffset,
      sortColumn: sortColumn.value,
      sortDirection: sortDirection.value,
    }, signal)
    if (sequence !== rowsSequence || routeKey.value !== requestKey || signal.aborted || !isCurrentSession(session.generation)) return { status: 'stale' }
    // 先核对最新目录合同，再授予行编辑归属；不接管、不覆盖已经打开的草稿。
    acceptedRowsContract.value = { targetKey: requestKey, columns: databaseColumnContract(result.table?.columns) }
    tableRows.value = result
    loadedRowsTargetKey.value = rowsContractMismatch.value ? null : requestKey
    offset.value = requestedOffset
    if (reportError) {
      queryRefreshError.value = null
      insertRefreshError.value = null
      rowWriteRefreshError.value = null
    }
    return { status: 'success' }
  } catch (error) {
    if (sequence !== rowsSequence || routeKey.value !== requestKey || signal.aborted || !isCurrentSession(session.generation) || isAbortError(error)) return { status: 'stale' }
    const failure = toErrorState(error, '表数据读取失败。')
    if (reportError) errorState.value = failure
    return { status: 'failed', error: failure }
  } finally {
    if (sequence === rowsSequence) {
      rowsLoading.value = false
      rowsInitialized.value = true
    }
  }
}

async function reloadTableSchema() {
  if (!schemaDrift.value || schemaReloadPending.value || rowDialogOpen.value || dangerOpen.value || mutationPending.value
    || dangerBusy.value || queryPending.value || insertPending.value || rowWritePending.value) return
  const key = routeKey.value
  const requestedSource = sourceId.value
  const routeGeneration = routeSequence
  const sequence = ++schemaReloadSequence
  const session = captureSession()
  const controller = new AbortController()
  const signal = AbortSignal.any([controller.signal, session.signal])
  const ownsRead = () => sequence === schemaReloadSequence && routeGeneration === routeSequence
    && key === routeKey.value && !signal.aborted && isCurrentSession(session.generation)
  schemaReloadController = controller
  schemaReloadPending.value = true
  schemaReloadError.value = null
  // 在目录 await 前终止旧只读结果的归属；不会中止任何已经发出的写入。
  rowsSequence += 1
  rowsController?.abort()
  rowsLoading.value = false
  loadedRowsTargetKey.value = null
  try {
    await databaseStore.loadCatalog(requestedSource, signal)
    if (!ownsRead()) return
    if (!table.value) {
      schemaDriftTargets.value.add(key)
      schemaReloadError.value = { code: 'database_not_found', message: '当前目录中未找到此表。', nextStep: '返回数据源详情核对表名与结构。' }
      return
    }
    const catalogContract = columnsContract.value
    if (sortColumn.value && !columns.value.some((column) => column.name === sortColumn.value)) {
      sortColumn.value = ''
      sortDirection.value = ''
    }
    const refresh = await refreshRows(false, 0, session, sequence)
    if (!ownsRead()) return
    if (refresh.status === 'failed') schemaReloadError.value = refresh.error
    if (refresh.status === 'success' && catalogContract !== null && columnsContract.value === catalogContract
      && acceptedRowsContract.value?.targetKey === key && acceptedRowsContract.value.columns === catalogContract) {
      schemaDriftTargets.value.delete(key)
    }
  } catch (error) {
    if (ownsRead() && !isAbortError(error)) schemaReloadError.value = toErrorState(error, '表结构重新读取失败。')
  } finally {
    if (sequence === schemaReloadSequence) {
      schemaReloadPending.value = false
      if (schemaReloadController === controller) schemaReloadController = null
    }
  }
}

async function changePage(direction: -1 | 1) {
  if (rowsLoading.value || !tableRows.value || !stablePagination.value) return
  if (direction === 1 && !tableRows.value.hasMore || direction === -1 && offset.value === 0) return
  // 失败页不推进游标，避免下次翻页跳过超预算但尚未展示的数据。
  await refreshRows(true, Math.max(0, offset.value + direction * pageSize.value))
}

function invalidateRowsPage() {
  // 新排序或页大小必须先成功读取首屏，旧页不能作为新设置的翻页依据。
  tableRows.value = null
  loadedRowsTargetKey.value = null
  offset.value = 0
}

async function onSortChange(event: { prop?: string | null; order?: 'ascending' | 'descending' | null }) {
  sortColumn.value = event.prop ?? ''
  sortDirection.value = event.order === 'descending' ? 'desc' : event.order === 'ascending' ? 'asc' : ''
  invalidateRowsPage()
  await refreshRows()
}

function openInsert() {
  if (!canInsertRows.value) return
  rowDraftTargetKey.value = routeKey.value
  rowDraftGeneration.value += 1
  rowDialogMode.value = 'insert'
  rowDraftColumns.value = columns.value.map((column) => ({ ...column }))
  rowForm.value = Object.fromEntries(columns.value.map((column) => [column.name, '']))
  rowNullFields.value = Object.fromEntries(columns.value.map((column) => [column.name, false]))
  originalKeys.value = {}
  rowDialogOpen.value = true
}

function clearRowDraft() {
  rowDraftGeneration.value += 1
  rowDraftTargetKey.value = null
  rowForm.value = {}
  rowDraftColumns.value = []
  rowNullFields.value = {}
  originalKeys.value = {}
}

function openEdit(row: Record<string, DatabaseValue>) {
  if (!canEditRows.value || loadedRowsTargetKey.value !== routeKey.value) return
  rowDraftTargetKey.value = routeKey.value
  rowDraftGeneration.value += 1
  rowDialogMode.value = 'edit'
  rowDraftColumns.value = columns.value.map((column) => ({ ...column }))
  rowForm.value = Object.fromEntries(columns.value.map((column) => [
    column.name,
    databaseEditorValue(row[column.name], column),
  ]))
  rowNullFields.value = Object.fromEntries(columns.value.map((column) => [
    column.name,
    row[column.name] === null || row[column.name] === undefined,
  ]))
  originalKeys.value = Object.fromEntries(primaryKeyColumns.value.map((column) => [column.name, row[column.name] ?? null]))
  rowDialogOpen.value = true
}

function rowValues(dialogMode: RowDialogMode) {
  return Object.fromEntries(rowDraftColumns.value.flatMap((column) => {
    const rawValue = rowForm.value[column.name] ?? ''
    const nullSelected = rowNullFields.value[column.name] === true
    const writeMode = databaseWriteMode(column)
    if (dialogMode === 'insert' && rawValue.trim() === '' && writeMode === 'required' && !nullSelected) {
      throw new DatabaseValueError(`字段「${column.name}」为必填项，请填写值或显式选择 NULL。`)
    }
    if (dialogMode === 'insert' && rawValue === '' && writeMode !== 'required') return []
    return [[column.name, resolveDatabaseValue(rawValue, column, nullSelected)]]
  }))
}

function interruptInsert() {
  if (!insertController) return
  // 中止只结束客户端观察，不证明服务端没有提交；返回该目标时仍须阻止重放。
  insertAttempts.value.set(insertController.key, 'unknown')
  insertController.controller.abort()
  insertController = null
}

async function submitInsert() {
  if (!table.value || !canInsertRows.value || !rowDialogOpen.value || rowDraftTargetKey.value !== routeKey.value || insertStatus.value) return
  const target = Object.freeze({ sourceId: sourceId.value, schema: table.value.schema, table: table.value.name })
  const key = insertTargetKey.value
  const routeGeneration = routeSequence
  const draftGeneration = rowDraftGeneration.value
  const refreshSequence = ++insertRefreshSequence
  const session = captureSession()
  const controller = new AbortController()
  const signal = AbortSignal.any([controller.signal, session.signal])
  const markUnknown = () => { insertAttempts.value.set(key, 'unknown') }
  // 在首个 await 前占用目标；按钮尚未重绘时的重复调用也不能发出第二次写入。
  insertAttempts.value.set(key, 'pending')
  insertDraftGeneration.value = draftGeneration
  insertController = { key, controller }
  signal.addEventListener('abort', markUnknown, { once: true })
  insertRefreshError.value = null
  clearError()
  try {
    const values = rowValues('insert')
    await insertDatabaseRow({ ...databaseStore.connection(target.sourceId), ...target, values }, signal)
  } catch (error) {
    if (error instanceof DatabaseInsertOutcomeUnknownError) {
      insertAttempts.value.set(key, 'unknown')
    } else {
      // 发送后的不确定异常已由 INSERT transport 归类；稍后的中止不能把本地失败改成已发送。
      insertAttempts.value.delete(key)
      if (isCurrentSession(session.generation) && routeGeneration === routeSequence && key === insertTargetKey.value && draftGeneration === rowDraftGeneration.value) {
        errorState.value = error instanceof NcpApiError
          ? { code: error.code, message: error.message, nextStep: '新增请求未提交到数据库执行；请处理请求或登录问题后重新提交。' }
          : toErrorState(error, '新增请求未发送，请检查字段值。')
      }
    }
    return
  } finally {
    signal.removeEventListener('abort', markUnknown)
    if (insertController?.controller === controller) insertController = null
  }
  if (signal.aborted || !isCurrentSession(session.generation)) return
  insertAttempts.value.delete(key)
  if (routeGeneration !== routeSequence || key !== insertTargetKey.value) return
  ElMessage.success('新增请求已执行')
  if (draftGeneration === rowDraftGeneration.value) rowDialogOpen.value = false
  // 确认写入与后续只读刷新分别报告；读失败不降格为未知，也不能重放 INSERT。
  const refresh = await refreshRows(false, offset.value, session)
  if (isCurrentSession(session.generation) && routeGeneration === routeSequence && key === insertTargetKey.value && refreshSequence === insertRefreshSequence && refresh.status === 'failed') {
    insertRefreshError.value = refresh.error
  }
}

async function submitRow() {
  if (!table.value || !canInsertRows.value || !rowDialogOpen.value || mutationPending.value) return
  if (rowDialogMode.value === 'insert') {
    await submitInsert()
    return
  }
  // 保留同步准入；新草稿、关闭对话框均不能抢占已派发写入。
  if (!canEditRows.value || dangerBusy.value || rowWriteAttempt.value || rowDraftTargetKey.value !== routeKey.value) return
  await submitRowWrite('update')
}

async function submitRowWrite(operation: DatabaseRowWriteOperation) {
  if (!table.value || !canEditRows.value || rowWriteAttempt.value || mutationPending.value || dangerBusy.value) return
  const target = Object.freeze({ sourceId: sourceId.value, schema: table.value.schema, table: table.value.name })
  const key = JSON.stringify([target.sourceId, target.schema, target.table])
  const routeGeneration = routeSequence
  const sequence = ++mutationSequence
  const draftGeneration = rowDraftGeneration.value
  const draftRevision = rowDraftRevision.value
  const confirmationGeneration = dangerGeneration.value
  const session = captureSession()
  const controller = new AbortController()
  const signal = AbortSignal.any([controller.signal, session.signal])
  let confirmed = false
  const ownsPage = () => routeGeneration === routeSequence && key === routeKey.value && isCurrentSession(session.generation)
  const ownsDialog = () => operation === 'update'
    ? draftGeneration === rowDraftGeneration.value && draftRevision === rowDraftRevision.value
    : confirmationGeneration === dangerGeneration.value
  const releaseBusy = () => {
    if (sequence !== mutationSequence) return
    if (operation === 'update') mutationPending.value = false
    else dangerBusy.value = false
  }
  const interrupt = () => {
    // 中止观察不证明未执行；已确认写后的读取中止则不降格为未知。
    if (!confirmed) rowWriteAttempts.value.set(key, { operation, status: 'unknown' })
    releaseBusy()
  }
  rowWriteAttempts.value.set(key, { operation, status: 'pending' })
  if (operation === 'update') {
    mutationPending.value = true
    rowWriteDraftGeneration.value = draftGeneration
  } else dangerBusy.value = true
  rowWriteController = controller
  mutationController = controller
  signal.addEventListener('abort', interrupt, { once: true })
  rowWriteRefreshError.value = null
  clearError()
  try {
    try {
      // 仅当前请求瞬时持有发送数据；ledger/controller 均不复制主键、草稿或凭据。
      if (operation === 'update') {
        const values = rowValues('edit')
        await updateDatabaseRow({ ...databaseStore.connection(target.sourceId), ...target, values, keys: { ...originalKeys.value } }, signal)
      } else {
        await deleteDatabaseRow({ ...databaseStore.connection(target.sourceId), ...target, keys: { ...pendingRow.value } }, signal)
      }
    } catch (error) {
      if (error instanceof DatabaseRowWriteOutcomeUnknownError) {
        rowWriteAttempts.value.set(key, { operation, status: 'unknown' })
        if (operation === 'delete' && ownsPage() && ownsDialog()) {
          dangerOpen.value = false
          pendingRow.value = null
        }
      } else {
        // 转换/序列化零派发，或 transport 已验证的准确派发前拒绝。
        rowWriteAttempts.value.delete(key)
        if (ownsPage() && ownsDialog()) {
          errorState.value = error instanceof NcpApiError
            ? { code: error.code, message: error.message, nextStep: '行修改请求未提交到数据库执行；请处理请求或登录问题后重新提交。' }
            : toErrorState(error, '行修改请求未发送，请检查字段值。')
        }
      }
      return
    }
    if (signal.aborted || !ownsPage()) return
    confirmed = true
    rowWriteAttempts.value.delete(key)
    ElMessage.success(operation === 'update' ? '更新请求已执行' : '删除请求已执行')
    if (ownsDialog()) {
      if (operation === 'update') rowDialogOpen.value = false
      else dangerOpen.value = false
    }
    // 行数为 0 同样是已执行；后续读取绝不重发写入或改变确认结论。
    const refresh = await refreshRows(false, offset.value, session)
    if (ownsPage() && sequence === mutationSequence && refresh.status === 'failed') {
      rowWriteRefreshError.value = { operation, error: refresh.error }
    }
  } finally {
    signal.removeEventListener('abort', interrupt)
    if (rowWriteController === controller) rowWriteController = null
    if (mutationController === controller) mutationController = null
    releaseBusy()
  }
}

async function removeRow(row: Record<string, DatabaseValue>) {
  if (!table.value || !canEditRows.value || loadedRowsTargetKey.value !== routeKey.value || rowWriteAttempt.value || mutationPending.value || queryPending.value || dangerBusy.value || dangerOpen.value) return
  deleteConfirmationTargetKey.value = routeKey.value
  // 确认仅需主键，不把整行的其他敏感字段留在隐藏确认状态。
  pendingRow.value = Object.fromEntries(primaryKeyColumns.value.map((column) => [column.name, row[column.name] ?? null]))
  pendingQuery.value = null
  dangerGeneration.value += 1
  dangerAction.value = 'delete-row'
  dangerOpen.value = true
}

async function executeSQL(snapshot: SQLExecutionSnapshot) {
  if (queryPending.value || !ownsSQLScope(snapshot.scope)) return false
  const sequence = ++querySequence
  queryController?.abort()
  const controller = new AbortController()
  const session = snapshot.scope.session
  const signal = AbortSignal.any([controller.signal, session.signal])
  const ownsExecution = () => sequence === querySequence && ownsSQLScope(snapshot.scope) && !signal.aborted
  queryController = controller
  queryPending.value = true
  queryResult.value = null
  queryError.value = null
  queryRefreshError.value = null
  try {
    const result = await executeDatabaseSQL({
      ...snapshot.connection,
      sql: snapshot.sql,
      ...(snapshot.resultMode === 'auto' ? {} : { resultMode: snapshot.resultMode }),
    }, signal)
    if (!ownsExecution()) return false
    queryResult.value = result
    // 返回行不代表只读；SQL 成功后只刷新数据，绝不重放 SQL。
    const refresh = await refreshRows(false, offset.value, session)
    if (!ownsExecution()) return false
    if (refresh.status === 'failed') queryRefreshError.value = refresh.error
    return true
  } catch (error) {
    if (!ownsExecution() || isAbortError(error)) return false
    queryResult.value = null
    queryError.value = toQueryErrorState(error)
    return false
  } finally {
    if (queryController === controller) queryController = null
    if (ownsExecution()) queryPending.value = false
  }
}

async function confirmDanger() {
  if (!table.value || !dangerOpen.value || mutationPending.value || dangerBusy.value || queryPending.value) return
  if (dangerAction.value === 'delete-row') {
    if (!canEditRows.value || !pendingRow.value || rowWriteAttempt.value || deleteConfirmationTargetKey.value !== routeKey.value) return
    await submitRowWrite('delete')
    return
  }
  // SQL 保持独立明确执行的合同，不作为行修改未知结果的自动核对或重放。
  const snapshot = pendingQuery.value
  if (!snapshot || !ownsSQLScope(snapshot.scope)) return
  const sequence = ++mutationSequence
  const confirmationGeneration = dangerGeneration.value
  const ownsExecution = () => sequence === mutationSequence && ownsSQLScope(snapshot.scope)
  mutationController?.abort()
  dangerBusy.value = true
  try {
    const succeeded = await executeSQL(snapshot)
    if (succeeded && ownsExecution()) ElMessage.success('SQL 执行完成')
  } finally {
    if (ownsExecution()) dangerBusy.value = false
    if (ownsExecution() && confirmationGeneration === dangerGeneration.value) {
      dangerOpen.value = false
      pendingRow.value = null
      pendingQuery.value = null
    }
  }
}

async function runSQL() {
  const scope = sqlEditorScope
  if (!scope || !ownsSQLScope(scope) || !sql.value.trim() || queryPending.value || dangerBusy.value || dangerOpen.value) return
  const currentConnection = connection()
  // 凭据仅属于这次确认/执行；后续编辑连接不能悄悄改变已确认 SQL 的目标数据库。
  const snapshot = Object.freeze({
    scope,
    connection: Object.freeze({
      ...currentConnection,
      ...(currentConnection.credentials ? { credentials: Object.freeze({ ...currentConnection.credentials }) } : {}),
    }),
    sql: sql.value,
    resultMode: resultMode.value,
  })
  if (sqlRisk.value) {
    dangerGeneration.value += 1
    pendingQuery.value = snapshot
    pendingRow.value = null
    dangerAction.value = 'sql'
    dangerOpen.value = true
    return
  }
  await executeSQL(snapshot)
}

function isSensitiveColumn(name: string) {
  return /(password|passwd|token|secret|cookie|api[_-]?key|private[_-]?key)/i.test(name)
}

function isBooleanColumn(column: DatabaseColumn) {
  return databaseEditorKind(column.dataType) === 'boolean'
}

function displayValue(column: string, value: DatabaseValue | undefined) {
  if (isSensitiveColumn(column) && value !== null && value !== undefined) return '••••••••'
  return value
}

function sqlTableName() {
  if (!table.value) return ''
  const quote = source.value?.driver === 'mysql' ? '`' : '"'
  const name = `${quote}${table.value.name.replaceAll(quote, quote + quote)}${quote}`
  if (!table.value.schema || source.value?.driver === 'sqlite') return name
  return `${quote}${table.value.schema.replaceAll(quote, quote + quote)}${quote}.${name}`
}

function formatNumber(value?: number) {
  return value === undefined ? '未统计' : new Intl.NumberFormat('zh-CN').format(value)
}

function formatBytes(value?: number) {
  if (value === undefined) return '未统计'
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MB`
  return `${(value / 1024 ** 3).toFixed(1)} GB`
}

function tableTypeLabel(value: string) {
  return value.toLowerCase() === 'view' ? '视图' : '数据表'
}

function tableTypeIcon(value: string) {
  return value.toLowerCase() === 'view' ? Eye : Table2
}

function schemaLabel(value: string) {
  return value.trim() || '未提供 schema'
}

function errorCode(error: unknown) {
  if (error instanceof DatabaseValueError) return error.code
  if (error instanceof NcpApiError) return error.code
  if (error instanceof Error && databaseErrorCopy[error.message]) return error.message
  return 'DATABASE_OPERATION_FAILED'
}

function errorMessage(error: unknown, fallback: string) {
  const code = errorCode(error)
  return databaseErrorCopy[code]?.message || fallback
}

function errorNextStep(error: unknown, fallback: string) {
  return databaseErrorCopy[errorCode(error)]?.nextStep || fallback
}

function toErrorState(error: unknown, fallback: string): DatabaseErrorState {
  return {
    code: errorCode(error),
    message: errorMessage(error, fallback),
    nextStep: errorNextStep(error, '稍后重试；若持续失败，请记录错误码联系管理员。'),
  }
}

function toQueryErrorState(error: unknown): DatabaseErrorState {
  const code = errorCode(error)
  if (code === 'result_mode_unsupported') {
    return { code, message: '当前 Agent 不支持结果模式，请先升级 Agent。', nextStep: 'SQL 未提交执行；升级 Agent 后再主动执行。' }
  }
  if (code === 'DATABASE_INPUT_INVALID') {
    return { code, message: 'SQL 请求未通过服务端校验。', nextStep: 'SQL 未提交执行；请核对请求格式及 Server 是否支持所选结果模式。' }
  }
  if (code === 'result_too_large') {
    return { code, message: 'SQL 结果超过 1 MiB 响应预算，无法展示。', nextStep: '写操作可能已生效；请先核对数据库状态，再决定是否重试。读取时可减少返回列或预览大字段的部分内容。' }
  }
  const message = code === 'timeout' ? 'SQL 执行超时。'
    : code === 'DATABASE_OPERATION_FAILED' ? '未能确认 SQL 执行结果。'
      : errorMessage(error, 'SQL 执行失败。')
  return { code, message, nextStep: '写操作可能已生效；请先核对数据库状态，再决定是否重试。' }
}

function setError(error: unknown, fallback: string) {
  errorState.value = toErrorState(error, fallback)
}

function clearError() {
  errorState.value = null
}

watch([rowsContractMismatch, draftContractMismatch], ([rowsChanged, draftChanged]) => {
  if (!rowsChanged && !draftChanged) return
  schemaDriftTargets.value.add(routeKey.value)
  if (dangerOpen.value && dangerAction.value === 'delete-row' && !dangerBusy.value) dangerOpen.value = false
}, { flush: 'sync' })
// 同 tick 的旧按钮事件（包括离开后立刻返回）必须先失去 SQL 派发权。
watch(routeKey, () => {
  invalidateSQLScope()
  routeRevision.value += 1
}, { flush: 'sync' })
// 即使同 tick 离开再返回原目标，也在重绘前重建有效编辑器，不能永久失去执行入口。
watch(routeRevision, () => { void initialize() }, { immediate: true })
watch(rowDialogOpen, (open) => { if (!open) clearRowDraft() }, { flush: 'sync' })
watch([rowForm, rowNullFields], () => { rowDraftRevision.value += 1 }, { deep: true, flush: 'sync' })
watch(dangerOpen, (open) => {
  if (!open) {
    dangerGeneration.value += 1
    deleteConfirmationTargetKey.value = null
    pendingRow.value = null
    pendingQuery.value = null
  }
}, { flush: 'sync' })
watch(pageSize, (next, previous) => {
  if (next === previous) return
  invalidateRowsPage()
  sql.value = table.value ? `SELECT * FROM ${sqlTableName()} LIMIT ${next}` : sql.value
  void refreshRows()
})
onBeforeUnmount(() => {
  invalidateSQLScope()
  schemaReloadSequence += 1
  schemaReloadController?.abort()
  schemaReloadController = null
  interruptInsert()
  rowWriteController?.abort()
  rowWriteController = null
  pendingRow.value = null
  pendingQuery.value = null
  clearRowDraft()
  routeSequence += 1
  rowsSequence += 1
  querySequence += 1
  mutationSequence += 1
  rowsController?.abort()
  initializeController?.abort()
  queryController?.abort()
  mutationController?.abort()
  initializeController = null
})
</script>

<template>
  <div v-if="table && source" class="page workspace-page table-workbench-page">
    <WorkspaceHeader :title="table.name" :description="`${tableTypeLabel(table.type)} / ${source.name}${table.schema ? ` / ${table.schema}` : ''}`" :icon="tableTypeIcon(table.type)" :stats="stats">
      <template #actions>
        <ElButton tag="a" :href="`/databases/${sourceId}?sourceName=${encodeURIComponent(source.name)}`" class="database-header-action"><ArrowLeft :size="16" />返回数据表</ElButton>
      </template>
    </WorkspaceHeader>

    <DatabaseErrorPanel v-if="errorState" title="数据表操作失败" :error="errorState" />
    <div v-if="schemaDrift" class="insert-outcome-warning" role="alert">
      <strong>表结构已变化，新增、编辑和删除已暂停。</strong>
      <span>读取到的字段信息与编辑器不一致。请先关闭草稿或确认框，再重新读取表结构；普通刷新不会解除保护。</span>
      <span v-if="schemaReloadError">重新读取未完成：{{ schemaReloadError.message }}（代码 {{ schemaReloadError.code }}）</span>
      <ElButton :loading="schemaReloadPending" :disabled="rowDialogOpen || dangerOpen || mutationPending || dangerBusy || queryPending || insertPending || rowWritePending" @click="reloadTableSchema"><RefreshCw :size="15" />重新读取表结构</ElButton>
      <small>重新读取不会解除写结果未确认的保护。</small>
    </div>
    <div v-if="insertUnknown" class="insert-outcome-warning" role="alert">
      <strong>新增结果未确认，可能已生效。</strong>
      <span>已暂停当前目标的再次新增。草稿仍可查看；刷新仅重新读取数据，不能证明此次新增成功或失败，也不会解除保护。</span>
      <small>请独立核对数据库状态，勿重复提交。保护仅保留在当前页面实例；完整重载或离开此页面后不再保留，服务端尚无幂等保障。</small>
    </div>
    <div v-if="insertRefreshError" class="insert-outcome-warning" role="alert">
      <strong>新增请求已执行，表数据刷新失败。</strong>
      <span>{{ insertRefreshError.message }} 请只刷新表数据，勿重复新增。</span>
      <small>代码 {{ insertRefreshError.code }}</small>
    </div>

    <div v-if="rowWriteUnknown" class="insert-outcome-warning" role="alert">
      <strong>{{ rowWriteAttempt?.operation === 'update' ? '更新' : '删除' }}结果未确认，可能已生效。</strong>
      <span>已暂停当前数据源、schema 和表的所有行编辑与删除，包括其他行。刷新仅重新读取数据，不能证明此次请求成功或失败，也不会解除保护。</span>
      <small>请独立只读核对，勿重复修改或删除。SQL 仍需单独明确执行，不会自动核对或重放。保护仅保留在当前页面实例；完整重载或离开此页面后不再保留，服务端尚无幂等保障。</small>
    </div>
    <div v-if="rowWriteRefreshError" class="insert-outcome-warning" role="alert">
      <strong>{{ rowWriteRefreshError.operation === 'update' ? '更新' : '删除' }}请求已执行，表数据刷新失败。</strong>
      <span>{{ rowWriteRefreshError.error.message }} 请只刷新表数据，勿重复修改或删除。</span>
      <small>代码 {{ rowWriteRefreshError.error.code }}</small>
    </div>

    <div class="table-connection-note" role="note">
      <Info :size="16" aria-hidden="true" />
      <span><strong>连接说明</strong>当前连接已完成认证并读取对象目录，表数据操作沿用此连接上下文。</span>
    </div>

    <nav class="table-tabs panel" role="tablist" aria-label="数据表工作区">
      <button v-for="item in [
        { value: 'data', label: '表数据', icon: Rows3 },
        { value: 'overview', label: '表信息', icon: Info },
        { value: 'structure', label: '表结构', icon: Columns3 },
        { value: 'definition', label: 'SQL 定义', icon: FileCode2 },
        { value: 'sql', label: '执行 SQL', icon: Braces },
      ]" :key="item.value" type="button" role="tab" :class="{ active: mode === item.value }" :aria-selected="mode === item.value" @click="mode = item.value as TableMode">
        <component :is="item.icon" :size="16" />{{ item.label }}
      </button>
    </nav>

    <section v-if="mode === 'data'" class="data-workbench panel">
      <div class="data-toolbar">
        <div>
          <span :class="['object-kind', { 'object-kind--view': isView }]">{{ tableTypeLabel(table.type) }}</span>
          <ElTag v-for="column in primaryKeyColumns" :key="column.name" effect="plain"><KeyRound :size="12" />{{ column.name }}</ElTag>
          <span>{{ columns.length }} 个字段 / 当前加载 {{ tableRows?.rows.length ?? 0 }} 行</span>
        </div>
        <div>
          <ElButton class="database-header-action" :loading="rowsLoading" @click="refreshRows()"><RefreshCw :size="15" />刷新</ElButton>
          <ElButton type="primary" :disabled="!canInsertRows" @click="openInsert"><CirclePlus :size="16" />新增行</ElButton>
        </div>
      </div>
      <div v-if="isView" class="object-warning">当前对象是视图，只读展示，新增、编辑和删除已停用。</div>
      <div v-else-if="!primaryKeyColumns.length" class="object-warning">当前表没有主键，为避免误改多行，编辑和删除已停用；同时禁用跨页浏览，首批数据仅供查看。</div>
      <div v-if="tableRows && !stablePagination" class="object-warning">当前表缺少稳定分页键，仅显示首批数据；如需完整浏览，请在 SQL Workbench 中使用自定义排序查询。</div>
      <div class="data-table-hint"><span>表数据</span><small>左右滑动查看字段，编号和行操作已固定</small></div>
      <div class="data-table" role="region" :aria-label="`${table.name} 表数据`">
        <div v-if="rowsLoading && !rowsInitialized" class="data-table-skeleton">
          <div class="data-table-skeleton__head"><i v-for="cell in Math.min(columns.length + 2, 8)" :key="cell" class="ncp-skeleton"></i></div>
          <div v-for="row in 9" :key="row" class="data-table-skeleton__row"><i v-for="cell in Math.min(columns.length + 2, 8)" :key="cell" class="ncp-skeleton"></i></div>
        </div>
        <ElTable v-else v-loading="rowsLoading" :data="tableRows?.rows ?? []" height="100%" :scrollbar-tabindex="0" border stripe highlight-current-row @sort-change="onSortChange">
          <ElTableColumn type="index" label="#" width="56" fixed="left" align="center" />
          <ElTableColumn v-for="column in columns" :key="column.name" :prop="column.name" :min-width="column.primaryKey ? 120 : Math.min(360, Math.max(160, column.name.length * 12 + 72))" sortable="custom" :sort-orders="['ascending', 'descending', null]" show-overflow-tooltip>
            <template #header><span class="column-heading"><KeyRound v-if="column.primaryKey" :size="12" />{{ column.name }}</span></template>
            <template #default="{ row }"><span v-if="row[column.name] === null" class="null-value">NULL</span><span v-else class="cell-value">{{ displayValue(column.name, row[column.name]) }}</span></template>
          </ElTableColumn>
          <ElTableColumn label="行操作" fixed="right" width="156" align="center">
            <template #default="{ row }">
              <div class="row-actions">
                <button type="button" :disabled="!canEditRows || rowWriteUnknown" aria-label="编辑当前行" @click.stop="openEdit(row)"><Pencil :size="14" />编辑</button>
                <button class="danger" type="button" :disabled="!canEditRows || Boolean(rowWriteAttempt) || mutationPending || queryPending || dangerBusy || dangerOpen" aria-label="删除当前行" @click.stop="removeRow(row)"><Trash2 :size="14" />删除</button>
              </div>
            </template>
          </ElTableColumn>
        </ElTable>
      </div>
      <footer class="data-pagination">
        <ListPageSizeControl list-key="database.table.rows" />
        <div><ElButton :disabled="offset === 0 || rowsLoading || !stablePagination" @click="changePage(-1)">上一页</ElButton><ElButton :disabled="!tableRows?.hasMore || rowsLoading || !stablePagination" @click="changePage(1)">下一页</ElButton></div>
      </footer>
    </section>

    <section v-else-if="mode === 'overview'" class="info-workbench">
      <div class="table-profile panel">
        <div><span>数据库</span><strong>{{ source.name }}</strong></div>
        <div><span>Schema</span><strong>{{ schemaLabel(table.schema) }}</strong></div>
        <div><span>对象类型</span><strong><span :class="['object-kind', { 'object-kind--view': isView }]">{{ tableTypeLabel(table.type) }}</span></strong></div>
        <div><span>字段数量</span><strong>{{ columns.length }}</strong></div>
        <div><span>主键字段</span><strong>{{ primaryKeyColumns.map((column) => column.name).join('、') || '无主键' }}</strong></div>
        <div><span>统计行数</span><strong>{{ formatNumber(table.rowCount) }}</strong></div>
        <div><span>占用空间</span><strong>{{ formatBytes(table.sizeBytes) }}</strong></div>
      </div>
    </section>

    <section v-else-if="mode === 'structure'" class="structure-workbench panel" role="region" :aria-label="`${table.name} 表结构`">
      <ElTable :data="columns" height="100%" :scrollbar-tabindex="0" border stripe>
        <ElTableColumn prop="position" label="#" width="62" align="center" />
        <ElTableColumn prop="name" label="字段名" min-width="180"><template #default="{ row }"><span class="structure-name"><KeyRound v-if="row.primaryKey" :size="13" />{{ row.name }}</span></template></ElTableColumn>
        <ElTableColumn prop="dataType" label="数据类型" min-width="170" align="center" />
        <ElTableColumn label="允许 NULL" width="120" align="center"><template #default="{ row }">{{ row.nullable ? '是' : '否' }}</template></ElTableColumn>
        <ElTableColumn label="主键" width="100" align="center"><template #default="{ row }">{{ row.primaryKey ? '是' : '否' }}</template></ElTableColumn>
        <ElTableColumn prop="default" label="默认值" min-width="200" align="center"><template #default="{ row }"><span v-if="row.default === undefined" class="null-value">无</span><span v-else class="cell-value">{{ row.default }}</span></template></ElTableColumn>
      </ElTable>
    </section>

    <section v-else-if="mode === 'definition'" class="definition-workbench panel">
      <header><div><FileCode2 :size="18" /><span><strong>SQL 定义</strong><small>数据库返回的原始对象定义</small></span></div></header>
      <div v-if="table.definition" class="definition-editor"><SqlEditor :model-value="table.definition" disabled /></div>
      <div v-else class="definition-empty">当前数据库类型没有提供该对象的完整 SQL 定义，可在“表结构”中查看字段信息。</div>
    </section>

    <section v-else class="sql-workbench panel">
      <div class="sql-toolbar">
        <div><strong>执行 SQL</strong><span>{{ tableTypeLabel(table.type) }} / {{ source.name }} / {{ table.name }}</span></div>
        <div class="sql-toolbar__actions">
          <label class="sql-result-mode"><span>结果模式</span><select v-model="resultMode" class="ncp-select" :disabled="queryPending" aria-label="SQL 结果模式"><option v-for="(label, value) in resultModeLabels" :key="value" :value="value">{{ label }}</option></select></label>
          <small v-if="sqlRisk" class="sql-risk-hint" role="status"><CircleAlert :size="14" />{{ sqlRisk.hint }}</small>
          <small v-else>最多 500 行 / 1 MiB</small>
          <ElButton type="primary" :loading="queryPending" :disabled="!sql.trim() || queryPending || dangerOpen || dangerBusy" @click="runSQL"><Play :size="15" />执行<kbd>Ctrl ↵</kbd></ElButton>
        </div>
      </div>
      <div class="sql-mode-note" role="note">新版 Agent 的自动模式可识别 SQLite/PostgreSQL 部分无 RETURNING 的 CTE 写入，复杂语法仍可能无法识别。RETURNING 请选“返回行”；需要明确获取无返回行写入的驱动计数时请选择“执行”。返回行模式也会执行写操作；500 行与 1 MiB 仅限制展示结果，不限制写入范围。</div>
      <div class="sql-editor"><SqlEditor v-model="sql" :disabled="queryPending" @execute="runSQL" /></div>
      <div class="query-result">
        <header><strong>{{ queryResultTitle }}</strong><span v-if="queryResult">{{ queryResult.durationMs }} ms<template v-if="queryResult.truncated"> / 返回结果已截断</template></span><span v-else>{{ queryError ? '执行失败' : queryPending ? '正在执行' : '等待执行' }}</span></header>
        <div v-if="queryRefreshError" class="query-refresh-warning" role="alert"><strong>SQL 已执行，表数据刷新失败。</strong><span>{{ queryRefreshError.message }} 请在“表数据”中重新读取，勿为刷新而重跑 SQL。</span><small>代码 {{ queryRefreshError.code }}</small></div>
        <div v-if="queryTruncationSummary" class="query-truncation-warning" role="status">{{ queryTruncationSummary }}</div>
        <div v-if="queryResult?.columns.length" class="query-affected-summary">{{ affectedSummary }}</div>
        <div v-if="queryError" class="query-message query-message--error">
          <CircleAlert :size="22" />
          <div class="query-message__body"><strong>执行失败</strong><span>{{ queryError.message }}</span><small>下一步：{{ queryError.nextStep }}</small></div>
          <code>代码 {{ queryError.code }}</code>
        </div>
        <div v-else-if="queryResult?.columns.length" class="query-table" role="region" :aria-label="`${table.name} SQL 查询结果`">
          <ElTable :data="queryRows" :empty-text="queryEmptyText" height="100%" :scrollbar-tabindex="0" border stripe>
            <ElTableColumn v-for="column in queryColumns" :key="column.key" :prop="column.key" :label="column.label" min-width="150" show-overflow-tooltip>
              <template #default="{ row }"><span v-if="row[column.key] === null" class="null-value">NULL</span><span v-else class="cell-value">{{ displayValue(column.label, row[column.key]) }}</span></template>
            </ElTableColumn>
          </ElTable>
        </div>
        <div v-else-if="queryResult" class="query-message query-message--success"><strong>执行成功</strong><span>{{ affectedSummary }}</span></div>
        <div v-else class="query-message"><Braces :size="24" /><span>输入 SQL 后按 Ctrl + Enter 执行</span></div>
      </div>
    </section>

    <ElDialog v-model="rowDialogOpen" :title="rowDialogMode === 'insert' ? '新增数据行' : '编辑数据行'" width="min(760px, calc(100vw - 28px))">
      <div class="row-dialog-context"><component :is="tableTypeIcon(table.type)" :size="18" /><span><strong>{{ table.name }}</strong><small>保存后将直接写入数据库</small></span></div>
      <div v-if="schemaDrift" class="insert-outcome-warning" role="alert"><strong>表结构已变化，草稿不能提交。</strong><span>如需保留，请先复制草稿；关闭后重新读取表结构，再建立新草稿。</span></div>
      <div v-if="rowDialogMode === 'insert' && insertUnknown" class="insert-outcome-warning" role="alert"><strong>新增结果未确认，可能已生效。</strong><span>当前目标已暂停再次新增。请先只读核对数据库状态，刷新不会解除保护。</span></div>
      <div v-else-if="rowDialogMode === 'insert' && insertPending" class="object-warning" role="status">当前目标的新增请求正在等待结果，请勿重复提交。</div>
      <div v-if="rowDialogMode === 'edit' && rowWriteUnknown" class="insert-outcome-warning" role="alert"><strong>行修改结果未确认，可能已生效。</strong><span>当前表所有行的编辑与删除已暂停。只读刷新不会解除保护。</span></div>
      <ElForm class="row-form" label-position="top">
        <ElFormItem v-for="column in rowDraftColumns" :key="column.name">
          <template #label><span class="form-label"><span>{{ column.name }}<small>{{ column.dataType }}<template v-if="column.primaryKey"> / 主键</template></small></span><ElSwitch v-if="column.nullable && !isBooleanColumn(column)" v-model="rowNullFields[column.name]" :disabled="rowDraftPending" size="small" inline-prompt active-text="NULL" inactive-text="值" /></span></template>
          <DatabaseCellEditor :model-value="rowForm[column.name] ?? ''" :null-selected="rowNullFields[column.name] === true" :column="column" :disabled="rowDraftPending || (rowNullFields[column.name] && !isBooleanColumn(column)) || (rowDialogMode === 'edit' && column.primaryKey)" :sensitive="isSensitiveColumn(column.name)" @update:model-value="rowForm[column.name] = $event" @update:null-selected="rowNullFields[column.name] = $event" />
        </ElFormItem>
      </ElForm>
      <template #footer><ElButton @click="rowDialogOpen = false">取消</ElButton><ElButton type="primary" :loading="mutationPending || (rowDialogMode === 'insert' && insertPending)" :disabled="!canInsertRows || (rowDialogMode === 'insert' ? insertUnknown : dangerBusy || rowWriteUnknown)" @click="submitRow">{{ rowDialogMode === 'insert' ? '新增数据行' : '保存修改' }}</ElButton></template>
    </ElDialog>

    <ConfirmDangerDialog
      v-model="dangerOpen"
      :title="dangerTitle"
      :description="dangerDescription"
      :impact="dangerImpact"
      :retained="dangerRetained"
      :action-label="dangerAction === 'sql' ? '确认执行' : '永久删除'"
      :busy="mutationPending || dangerBusy || queryPending"
      @confirm="confirmDanger"
    >
      <template v-if="dangerAction === 'sql' && pendingQuery" #details>
        <section class="sql-confirmation-snapshot" aria-label="待执行 SQL 快照">
          <strong>待执行 SQL（原文）</strong>
          <pre class="sql-confirmation-preview">{{ pendingQuery.sql }}</pre>
        </section>
      </template>
    </ConfirmDangerDialog>
  </div>

  <div v-else class="page"><section class="missing-table panel"><Table2 :size="26" /><h1>数据表不存在或数据库尚未连接</h1><a href="/databases">返回数据库列表</a></section></div>
</template>

<style scoped>
.insert-outcome-warning {
  display: grid;
  gap: 4px;
  padding: 12px 14px;
  border: 1px solid var(--ncp-warning-border);
  border-radius: var(--ncp-radius-md);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
  font-size: .8rem;
  line-height: 1.5;
}

.sql-confirmation-snapshot {
  display: grid;
  min-width: 0;
  gap: 6px;
  font-size: max(12px, .875rem);
}

.sql-confirmation-preview {
  min-width: 0;
  max-height: min(40vh, 420px);
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-surface-quiet);
  color: var(--ncp-text);
  font-family: var(--ncp-font-mono);
  font-size: inherit;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.table-connection-note {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  padding: 0 2px;
  color: var(--ncp-text-subtle);
  font-size: .74rem;
}

.table-connection-note svg {
  flex: 0 0 auto;
  color: var(--ncp-primary-strong);
}

.table-connection-note strong {
  margin-right: 6px;
  color: var(--ncp-text-muted);
  font-weight: 740;
}

.database-header-action {
  --el-button-hover-text-color: var(--ncp-primary-strong);
  --el-button-active-text-color: var(--ncp-primary-strong);
  min-height: var(--ncp-control-height);
}

.table-tabs {
  display: flex;
  min-width: 0;
  min-height: 50px;
  align-items: center;
  gap: 4px;
  padding: 5px;
  overflow: auto;
  scrollbar-width: none;
}

.table-tabs::-webkit-scrollbar {
  display: none;
}

.table-tabs button {
  display: flex;
  min-width: 112px;
  min-height: 38px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 0 13px;
  border-radius: var(--ncp-radius-sm);
  background: transparent;
  color: var(--ncp-text-muted);
  font-size: .77rem;
  font-weight: 700;
  white-space: nowrap;
  transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out), color var(--ncp-duration-fast) var(--ncp-ease-out);
}

.table-tabs button:hover,
.table-tabs button.active {
  background: var(--ncp-surface-quiet);
  color: var(--ncp-primary-strong);
}

.table-tabs button.active {
  background: var(--ncp-surface);
  box-shadow: var(--ncp-shadow-control);
}

.data-workbench,
.structure-workbench,
.definition-workbench {
  height: calc(100dvh - 278px);
  min-height: 560px;
  overflow: hidden;
}

.data-workbench {
  display: flex;
  min-width: 0;
  flex-direction: column;
}

.data-toolbar {
  display: flex;
  min-height: 56px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--ncp-line);
}

.data-toolbar > div {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.data-toolbar > div:first-child {
  color: var(--ncp-text-subtle);
  font-size: .76rem;
}

.data-toolbar > div:first-child > span:last-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.data-toolbar :deep(.el-tag),
.data-toolbar :deep(.el-tag__content) {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.object-kind {
  display: inline-flex;
  min-height: 24px;
  align-items: center;
  padding: 0 8px;
  border-radius: var(--ncp-radius-pill);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
  font-size: .69rem;
  font-weight: 740;
  line-height: 1;
  white-space: nowrap;
}

.object-kind--view {
  background: var(--ncp-info-soft);
  color: var(--ncp-info-strong);
}

.object-warning {
  padding: 8px 13px;
  border-bottom: 1px solid var(--ncp-warning-border);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
  font-size: .75rem;
}

.data-table-hint {
  display: none;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 13px;
  border-bottom: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
  color: var(--ncp-text-muted);
  font-size: .72rem;
}

.data-table-hint small {
  color: var(--ncp-text-subtle);
  font-size: .68rem;
}

.data-table {
  min-height: 0;
  flex: 1;
}

.data-table :deep(.el-table),
.structure-workbench :deep(.el-table),
.query-table :deep(.el-table) {
  --el-table-border-color: var(--ncp-line);
  --el-table-header-bg-color: var(--ncp-table-head);
  --el-table-row-hover-bg-color: var(--ncp-table-row-hover);
  font-size: .78rem;
}

.data-table :deep(.el-scrollbar__wrap:focus-visible),
.structure-workbench :deep(.el-scrollbar__wrap:focus-visible),
.query-table :deep(.el-scrollbar__wrap:focus-visible) {
  outline: 2px solid var(--ncp-primary);
  outline-offset: -2px;
}

.data-table :deep(.el-table__header th.el-table__cell) {
  height: 44px;
  padding: 0;
}

.data-table :deep(.el-table__body td.el-table__cell) {
  height: 46px;
  padding: 0;
}

.data-table :deep(.el-table .cell),
.structure-workbench :deep(.el-table .cell),
.query-table :deep(.el-table .cell) {
  padding: 0 11px;
}

.column-heading {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-weight: 720;
}

.cell-value {
  display: block;
  overflow: hidden;
  color: #344054;
  font-family: var(--ncp-font-mono);
  font-size: .72rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.null-value {
  display: inline-flex;
  padding: 1px 5px;
  border-radius: var(--ncp-radius-xs);
  background: var(--ncp-neutral-soft);
  color: var(--ncp-text-subtle);
  font-family: var(--ncp-font-mono);
  font-size: .69rem;
  font-style: italic;
}

.data-table-skeleton {
  height: 100%;
  overflow: hidden;
  border: 1px solid var(--ncp-line);
}

.data-table-skeleton__head,
.data-table-skeleton__row {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: minmax(130px, 1fr);
  align-items: center;
  gap: 18px;
  padding: 0 14px;
}

.data-table-skeleton__head {
  height: 44px;
  background: var(--ncp-surface-quiet);
}

.data-table-skeleton__row {
  height: 46px;
  border-top: 1px solid var(--ncp-line);
}

.data-table-skeleton i {
  width: 72%;
  height: 11px;
}

.row-actions {
  display: flex;
  justify-content: center;
  gap: 5px;
}

.row-actions button {
  display: inline-flex;
  height: 31px;
  align-items: center;
  gap: 4px;
  padding: 0 8px;
  border: 1px solid var(--ncp-primary-border);
  border-radius: var(--ncp-radius-sm);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
  font-size: .69rem;
  font-weight: 700;
  transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out), border-color var(--ncp-duration-fast) var(--ncp-ease-out);
}

.row-actions button:hover:not(:disabled) {
  border-color: var(--ncp-primary);
  background: var(--ncp-primary-hover);
}

.row-actions button.danger {
  border-color: var(--ncp-danger-border);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.row-actions button:disabled {
  cursor: not-allowed;
  opacity: .4;
}

.data-pagination {
  display: flex;
  min-height: 52px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 7px 12px;
  border-top: 1px solid var(--ncp-line);
  color: var(--ncp-text-subtle);
  font-size: .74rem;
}

.data-pagination > div {
  display: flex;
  gap: 7px;
}

.info-workbench {
  min-width: 0;
}

.table-profile {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1px;
  padding: 1px;
  overflow: hidden;
  background: var(--ncp-line);
}

.table-profile > div {
  display: grid;
  min-width: 0;
  min-height: 108px;
  align-content: center;
  gap: 5px;
  padding: 18px;
  background: var(--ncp-surface);
}

.table-profile span {
  color: var(--ncp-text-subtle);
  font-size: .74rem;
}

.table-profile strong {
  overflow: hidden;
  color: var(--ncp-text);
  font-size: .86rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.table-profile strong .object-kind {
  color: var(--ncp-primary-strong);
  font-size: .69rem;
  font-weight: 740;
}

.table-profile strong .object-kind--view {
  color: var(--ncp-info-strong);
}

.structure-name {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-weight: 700;
}

.definition-workbench {
  display: flex;
  flex-direction: column;
  background: var(--ncp-surface);
}

.definition-workbench header {
  display: flex;
  min-height: 60px;
  align-items: center;
  padding: 0 16px;
  border-bottom: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.definition-workbench header > div {
  display: flex;
  align-items: center;
  gap: 9px;
}

.definition-workbench header span {
  display: grid;
}

.definition-workbench header strong {
  font-size: .84rem;
}

.definition-workbench header small {
  color: var(--ncp-text-subtle);
  font-size: .72rem;
}

.definition-editor {
  min-height: 0;
  flex: 1;
  overflow: hidden;
}

.definition-empty {
  display: grid;
  min-height: 300px;
  place-content: center;
  padding: 24px;
  color: var(--ncp-text-subtle);
  font-size: .8rem;
  text-align: center;
}

.sql-workbench {
  display: grid;
  min-width: 0;
  overflow: hidden;
  grid-template-rows: auto auto minmax(240px, 16rem) minmax(360px, 24rem);
}

.sql-toolbar {
  display: flex;
  min-height: 54px;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 12px;
  padding: 7px 12px;
  border-bottom: 1px solid var(--ncp-line);
}

.sql-toolbar > div {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 9px;
}

.sql-toolbar__actions {
  max-width: 100%;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.sql-result-mode {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.sql-result-mode select {
  min-width: 0;
  min-height: var(--ncp-control-height);
  width: auto;
  max-width: 100%;
  padding: 5px 8px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-surface);
  color: var(--ncp-text);
  font-size: max(12px, .875rem);
}

.sql-mode-note,
.query-affected-summary {
  min-width: 0;
  flex-shrink: 0;
  padding: 8px 12px;
  border-bottom: 1px solid var(--ncp-line);
  color: var(--ncp-text-muted);
  font-size: max(12px, .875rem);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.query-refresh-warning,
.query-truncation-warning {
  display: grid;
  flex-shrink: 0;
  gap: 3px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--ncp-warning-border);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
  font-size: max(12px, .875rem);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.sql-risk-hint {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
  color: var(--ncp-warning-strong);
}

.sql-risk-hint svg {
  flex: 0 0 auto;
}

.sql-toolbar strong {
  font-size: .82rem;
}

.sql-toolbar span,
.sql-toolbar small {
  overflow: hidden;
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sql-toolbar kbd {
  margin-left: 4px;
  padding: 1px 5px;
  border: 1px solid rgba(255, 255, 255, .35);
  border-radius: var(--ncp-radius-xs);
  background: rgba(255, 255, 255, .14);
  color: inherit;
  font-size: .58rem;
}

.sql-toolbar .sql-risk-hint {
  white-space: normal;
  overflow-wrap: anywhere;
}

.sql-editor {
  min-height: 0;
  overflow: hidden;
  border-bottom: 1px solid var(--ncp-line);
}

.query-result {
  display: flex;
  min-height: 0;
  flex-direction: column;
  overflow: auto;
}

.query-result > header {
  display: flex;
  min-height: 42px;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
  font-size: .75rem;
  flex-shrink: 0;
}

.query-result > header span {
  min-width: 0;
  color: var(--ncp-text-subtle);
  font-family: var(--ncp-font-mono);
  font-size: .68rem;
  overflow-wrap: anywhere;
}

.query-table {
  min-height: 120px;
  flex: 1;
}

.query-message {
  display: grid;
  min-width: 0;
  min-height: 150px;
  flex: 1;
  place-content: center;
  justify-items: center;
  gap: 6px;
  padding: 12px;
  color: var(--ncp-text-subtle);
  font-size: .78rem;
  text-align: center;
  overflow-wrap: anywhere;
}

.query-message--error {
  color: var(--ncp-danger-strong);
}

.query-message--error code {
  padding: 2px 6px;
  border: 1px solid var(--ncp-danger-border);
  border-radius: var(--ncp-radius-xs);
  color: var(--ncp-danger-strong);
  font-family: var(--ncp-font-mono);
  font-size: .67rem;
}

.query-message--error span {
  max-width: 52ch;
}

.query-message__body {
  display: grid;
  gap: 3px;
  max-width: min(52ch, 100%);
}

.query-message__body small {
  color: var(--ncp-danger-strong);
  font-size: .7rem;
  line-height: 1.4;
}

.query-message--success strong {
  color: var(--ncp-success-strong);
}

.row-dialog-context {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: -4px 0 16px;
  padding: 10px 12px;
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.row-dialog-context > span {
  display: grid;
}

.row-dialog-context small {
  color: var(--ncp-text-muted);
  font-size: .7rem;
}

.row-form {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 2px 16px;
  max-height: 58vh;
  overflow: auto;
  padding-right: 5px;
}

.form-label {
  display: flex;
  width: 100%;
  align-items: baseline;
  justify-content: space-between;
}

.form-label > span {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.form-label small {
  color: var(--ncp-text-subtle);
  font-family: var(--ncp-font-mono);
  font-size: .66rem;
}

.missing-table {
  display: grid;
  min-height: 260px;
  place-content: center;
  gap: 8px;
  text-align: center;
}

.missing-table h1 {
  margin: 0;
  font-size: 1rem;
}

.missing-table a {
  color: var(--ncp-primary-strong);
}

@media (max-width: 1000px) {
  .table-profile {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .data-workbench,
  .structure-workbench,
  .definition-workbench {
    height: calc(100dvh - 312px);
  }
}

@media (max-width: 680px) {
  .table-tabs button {
    min-width: 100px;
  }

  .table-tabs button svg {
    display: none;
  }

  .data-workbench,
  .structure-workbench,
  .definition-workbench {
    height: calc(100dvh - 370px);
    min-height: 540px;
  }

  .data-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .data-toolbar > div:last-child {
    width: 100%;
  }

  .data-toolbar > div:last-child :deep(.el-button) {
    flex: 1;
  }

  .data-table-hint {
    display: flex;
    align-items: flex-start;
    flex-direction: column;
    gap: 2px;
  }

  .data-pagination {
    align-items: stretch;
    flex-direction: column;
    gap: 8px;
  }

  .data-pagination > div :deep(.el-button) {
    flex: 1;
  }

  .table-profile {
    grid-template-columns: 1fr 1fr;
  }

  .table-profile > div {
    min-height: 96px;
    padding: 14px;
  }

  .row-form {
    grid-template-columns: 1fr;
  }

  .sql-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .sql-toolbar > div {
    width: 100%;
    justify-content: space-between;
  }

  .sql-toolbar kbd {
    display: none;
  }

  .sql-result-mode {
    width: 100%;
  }

  .sql-result-mode select {
    flex: 1 1 190px;
    width: 100%;
  }
}
</style>
