<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
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
  type DatabaseRows,
  type DatabaseValue,
  type QueryResult,
} from '@/api/database'
import { NcpApiError } from '@/api/system'
import { DatabaseValueError, databaseEditorKind, resolveDatabaseValue } from '@/domain/database/valueConversion'
import { classifySqlRisk } from '@/domain/database/sqlRisk'
import DatabaseErrorPanel, { type DatabaseErrorState } from '@/components/DatabaseErrorPanel.vue'
import DatabaseCellEditor from '@/components/DatabaseCellEditor.vue'
import ConfirmDangerDialog from '@/components/ConfirmDangerDialog.vue'
import SqlEditor from '@/components/SqlEditor.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useDatabaseStore } from '@/stores/database'
import { useListPreference } from '@/composables/useListPreference'

type TableMode = 'data' | 'overview' | 'structure' | 'definition' | 'sql'
type RowDialogMode = 'insert' | 'edit'

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
const rowsLoading = ref(false)
const errorState = ref<DatabaseErrorState | null>(null)
const offset = ref(0)
const sortColumn = ref('')
const sortDirection = ref('')
const rowDialogOpen = ref(false)
const rowDialogMode = ref<RowDialogMode>('insert')
const rowForm = ref<Record<string, string>>({})
const rowNullFields = ref<Record<string, boolean>>({})
const originalKeys = ref<Record<string, DatabaseValue>>({})
const mutationPending = ref(false)
const sql = ref('')
const queryResult = ref<QueryResult | null>(null)
const queryPending = ref(false)
const queryError = ref<DatabaseErrorState | null>(null)
const dangerOpen = ref(false)
const dangerBusy = ref(false)
const dangerAction = ref<'delete-row' | 'sql'>('delete-row')
const pendingRow = ref<Record<string, DatabaseValue> | null>(null)
const pendingSql = ref('')

const columns = computed(() => table.value?.columns ?? [])
const primaryKeyColumns = computed(() => columns.value.filter((column) => column.primaryKey))
const isView = computed(() => table.value?.type.toLowerCase() === 'view')
const canInsertRows = computed(() => Boolean(table.value) && !isView.value)
const canEditRows = computed(() => Boolean(table.value) && !isView.value && primaryKeyColumns.value.length > 0)
const stats = computed<WorkspaceStat[]>(() => [
  { label: '数据行', value: formatNumber(table.value?.rowCount), tone: 'success' },
  { label: '表大小', value: formatBytes(table.value?.sizeBytes), tone: 'warning' },
  { label: '字段', value: columns.value.length },
])
const queryRows = computed(() => {
  if (!queryResult.value) return []
  return queryResult.value.rows.map((row) =>
    Object.fromEntries(queryResult.value!.columns.map((column, index) => [column, row[index]])))
})
const sqlRisk = computed(() => classifySqlRisk(sql.value))
const dangerTitle = computed(() => dangerAction.value === 'sql' ? (classifySqlRisk(pendingSql.value)?.title ?? '确认执行 SQL') : '确认删除数据行')
const dangerDescription = computed(() => dangerAction.value === 'sql'
  ? (classifySqlRisk(pendingSql.value)?.confirmation ?? '请确认 SQL 的目标数据库和影响范围。')
  : '将永久删除当前数据行并立即写入数据库；删除本身无法在控制面撤销。')
const dangerImpact = computed(() => dangerAction.value === 'sql' ? ['执行当前 SQL 语句', '结果可能影响多行数据或表结构'] : ['永久删除选中的数据行'])
const dangerRetained = computed(() => dangerAction.value === 'sql' ? ['NCP 不会限制 Root 的正常 SQL 能力', '其他数据库和表不受影响'] : ['其他数据行不会被删除'])

onMounted(() => void initialize())

async function initialize() {
  try {
    if (!databaseStore.sources.length) await databaseStore.refreshDiscovery()
    if (!catalog.value && source.value && !source.value.requiresLogin) await databaseStore.loadCatalog(sourceId.value)
  } catch (error) {
    setError(error, '数据库对象目录读取失败。')
    return
  }
  if (!catalog.value && source.value?.requiresLogin) {
    await router.replace({ name: 'database-detail', params: { sourceId: sourceId.value }, query: { sourceName: source.value.name } })
    return
  }
  if (!table.value) return
  sql.value = `SELECT * FROM ${sqlTableName()} LIMIT ${pageSize.value}`
  await refreshRows()
}

function connection() {
  return databaseStore.connection(sourceId.value)
}

async function refreshRows() {
  if (!table.value) return
  rowsLoading.value = true
  clearError()
  try {
    tableRows.value = await loadDatabaseRows({
      ...connection(),
      schema: table.value.schema,
      table: table.value.name,
      limit: pageSize.value,
      offset: offset.value,
      sortColumn: sortColumn.value,
      sortDirection: sortDirection.value,
    })
  } catch (error) {
    setError(error, '表数据读取失败。')
  } finally {
    rowsLoading.value = false
  }
}

async function onSortChange(event: { prop?: string | null; order?: 'ascending' | 'descending' | null }) {
  sortColumn.value = event.prop ?? ''
  sortDirection.value = event.order === 'descending' ? 'desc' : event.order === 'ascending' ? 'asc' : ''
  offset.value = 0
  await refreshRows()
}

function openInsert() {
  if (!canInsertRows.value) return
  rowDialogMode.value = 'insert'
  rowForm.value = Object.fromEntries(columns.value.map((column) => [column.name, '']))
  rowNullFields.value = Object.fromEntries(columns.value.map((column) => [column.name, false]))
  originalKeys.value = {}
  rowDialogOpen.value = true
}

function openEdit(row: Record<string, DatabaseValue>) {
  if (!canEditRows.value) return
  rowDialogMode.value = 'edit'
  rowForm.value = Object.fromEntries(columns.value.map((column) => [
    column.name,
    row[column.name] === null || row[column.name] === undefined ? '' : String(row[column.name]),
  ]))
  rowNullFields.value = Object.fromEntries(columns.value.map((column) => [
    column.name,
    row[column.name] === null || row[column.name] === undefined,
  ]))
  originalKeys.value = Object.fromEntries(primaryKeyColumns.value.map((column) => [column.name, row[column.name] ?? null]))
  rowDialogOpen.value = true
}

async function submitRow() {
  if (!table.value || !canInsertRows.value) return
  mutationPending.value = true
  clearError()
  try {
    const values = Object.fromEntries(columns.value.flatMap((column) => {
      const rawValue = rowForm.value[column.name] ?? ''
      if (rowDialogMode.value === 'insert' && rawValue === '' && (column.primaryKey || column.default !== undefined)) return []
      return [[column.name, resolveDatabaseValue(rawValue, column, rowNullFields.value[column.name] === true)]]
    }))
    if (rowDialogMode.value === 'insert') {
      await insertDatabaseRow({ ...connection(), schema: table.value.schema, table: table.value.name, values })
      ElMessage.success('数据行已新增')
    } else {
      await updateDatabaseRow({ ...connection(), schema: table.value.schema, table: table.value.name, values, keys: originalKeys.value })
      ElMessage.success('数据行已更新')
    }
    rowDialogOpen.value = false
    await refreshRows()
  } catch (error) {
    setError(error, '数据写入失败。')
  } finally {
    mutationPending.value = false
  }
}

async function removeRow(row: Record<string, DatabaseValue>) {
  if (!table.value || !canEditRows.value) return
  pendingRow.value = row
  pendingSql.value = ''
  dangerAction.value = 'delete-row'
  dangerOpen.value = true
}

async function executeSQL(statement: string) {
  queryPending.value = true
  queryError.value = null
  try {
    queryResult.value = await executeDatabaseSQL({ ...connection(), sql: statement })
    if (!queryResult.value.columns.length) await refreshRows()
    return true
  } catch (error) {
    queryResult.value = null
    queryError.value = toErrorState(error, 'SQL 执行失败。')
    return false
  } finally {
    queryPending.value = false
  }
}

async function confirmDanger() {
  if (!table.value || dangerBusy.value) return
  dangerBusy.value = true
  let succeeded = false
  try {
    if (dangerAction.value === 'delete-row') {
      const row = pendingRow.value
      if (!row) return
      const keys = Object.fromEntries(primaryKeyColumns.value.map((column) => [column.name, row[column.name] ?? null]))
      clearError()
      await deleteDatabaseRow({ ...connection(), schema: table.value.schema, table: table.value.name, keys })
      ElMessage.success('数据行已删除')
      await refreshRows()
      succeeded = true
    } else {
      succeeded = await executeSQL(pendingSql.value)
      if (succeeded) ElMessage.success('SQL 执行完成')
    }
  } catch (error) {
    if (dangerAction.value === 'delete-row') setError(error, '数据删除失败。')
  } finally {
    dangerBusy.value = false
    if (succeeded) {
      dangerOpen.value = false
      pendingRow.value = null
      pendingSql.value = ''
    }
  }
}

async function runSQL() {
  if (!sql.value.trim()) return
  if (sqlRisk.value) {
    pendingSql.value = sql.value
    pendingRow.value = null
    dangerAction.value = 'sql'
    dangerOpen.value = true
    return
  }
  await executeSQL(sql.value)
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

function setError(error: unknown, fallback: string) {
  errorState.value = toErrorState(error, fallback)
}

function clearError() {
  errorState.value = null
}
</script>

<template>
  <div v-if="table && source" class="page workspace-page table-workbench-page">
    <WorkspaceHeader :title="table.name" :description="`${tableTypeLabel(table.type)} / ${source.name}${table.schema ? ` / ${table.schema}` : ''}`" :icon="tableTypeIcon(table.type)" :stats="stats">
      <template #actions>
        <ElButton tag="a" :href="`/databases/${sourceId}?sourceName=${encodeURIComponent(source.name)}`" class="database-header-action"><ArrowLeft :size="16" />返回数据表</ElButton>
      </template>
    </WorkspaceHeader>

    <DatabaseErrorPanel v-if="errorState" title="数据表操作失败" :error="errorState" />

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
          <ElButton class="database-header-action" :loading="rowsLoading" @click="refreshRows"><RefreshCw :size="15" />刷新</ElButton>
          <ElButton type="primary" :disabled="!canInsertRows" @click="openInsert"><CirclePlus :size="16" />新增行</ElButton>
        </div>
      </div>
      <div v-if="isView" class="object-warning">当前对象是视图，只读展示，新增、编辑和删除已停用。</div>
      <div v-else-if="!primaryKeyColumns.length" class="object-warning">当前表没有主键，为避免误改多行，编辑和删除已停用。</div>
      <div class="data-table-hint"><span>表数据</span><small>左右滑动查看字段，编号和行操作已固定</small></div>
      <div class="data-table">
        <div v-if="rowsLoading && !tableRows" class="data-table-skeleton">
          <div class="data-table-skeleton__head"><i v-for="cell in Math.min(columns.length + 2, 8)" :key="cell" class="ncp-skeleton"></i></div>
          <div v-for="row in 9" :key="row" class="data-table-skeleton__row"><i v-for="cell in Math.min(columns.length + 2, 8)" :key="cell" class="ncp-skeleton"></i></div>
        </div>
        <ElTable v-else v-loading="rowsLoading" :data="tableRows?.rows ?? []" height="100%" border stripe highlight-current-row @sort-change="onSortChange">
          <ElTableColumn type="index" label="#" width="56" fixed="left" align="center" />
          <ElTableColumn v-for="column in columns" :key="column.name" :prop="column.name" :min-width="column.primaryKey ? 120 : Math.min(360, Math.max(160, column.name.length * 12 + 72))" sortable="custom" :sort-orders="['ascending', 'descending', null]" show-overflow-tooltip>
            <template #header><span class="column-heading"><KeyRound v-if="column.primaryKey" :size="12" />{{ column.name }}</span></template>
            <template #default="{ row }"><span v-if="row[column.name] === null" class="null-value">NULL</span><span v-else class="cell-value">{{ displayValue(column.name, row[column.name]) }}</span></template>
          </ElTableColumn>
          <ElTableColumn label="行操作" fixed="right" width="156" align="center">
            <template #default="{ row }">
              <div class="row-actions">
                <button type="button" :disabled="!canEditRows" aria-label="编辑当前行" @click.stop="openEdit(row)"><Pencil :size="14" />编辑</button>
                <button class="danger" type="button" :disabled="!canEditRows" aria-label="删除当前行" @click.stop="removeRow(row)"><Trash2 :size="14" />删除</button>
              </div>
            </template>
          </ElTableColumn>
        </ElTable>
      </div>
      <footer class="data-pagination">
        <ListPageSizeControl list-key="database.table.rows" />
        <div><ElButton :disabled="offset === 0 || rowsLoading" @click="offset = Math.max(0, offset - pageSize); refreshRows()">上一页</ElButton><ElButton :disabled="!tableRows?.hasMore || rowsLoading" @click="offset += pageSize; refreshRows()">下一页</ElButton></div>
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

    <section v-else-if="mode === 'structure'" class="structure-workbench panel">
      <ElTable :data="columns" height="100%" border stripe>
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
        <div><strong>SQL 查询</strong><span>{{ tableTypeLabel(table.type) }} / {{ source.name }} / {{ table.name }}</span></div>
        <div class="sql-toolbar__actions">
          <small v-if="sqlRisk" class="sql-risk-hint" role="status"><CircleAlert :size="14" />{{ sqlRisk.hint }}</small>
          <small v-else>最多返回 500 行</small>
          <ElButton type="primary" :loading="queryPending" :disabled="!sql.trim()" @click="runSQL"><Play :size="15" />执行<kbd>Ctrl ↵</kbd></ElButton>
        </div>
      </div>
      <div class="sql-editor"><SqlEditor v-model="sql" :disabled="queryPending" @execute="runSQL" /></div>
      <div class="query-result">
        <header><strong>执行结果</strong><span v-if="queryResult">{{ queryResult.rows.length }} 行 / {{ queryResult.durationMs }} ms<template v-if="queryResult.truncated"> / 结果已截断</template></span><span v-else>等待执行</span></header>
        <div v-if="queryError" class="query-message query-message--error">
          <CircleAlert :size="22" />
          <div class="query-message__body"><strong>执行失败</strong><span>{{ queryError.message }}</span><small>下一步：{{ queryError.nextStep }}</small></div>
          <code>代码 {{ queryError.code }}</code>
        </div>
        <div v-else-if="queryResult?.columns.length" class="query-table">
          <ElTable :data="queryRows" height="100%" border stripe>
            <ElTableColumn v-for="column in queryResult.columns" :key="column" :prop="column" :label="column" min-width="150" show-overflow-tooltip>
              <template #default="{ row }"><span v-if="row[column] === null" class="null-value">NULL</span><span v-else class="cell-value">{{ displayValue(column, row[column]) }}</span></template>
            </ElTableColumn>
          </ElTable>
        </div>
        <div v-else-if="queryResult" class="query-message query-message--success"><strong>执行成功</strong><span>影响 {{ queryResult.rowsAffected }} 行，用时 {{ queryResult.durationMs }} ms。</span></div>
        <div v-else class="query-message"><Braces :size="24" /><span>输入 SQL 后按 Ctrl + Enter 执行</span></div>
      </div>
    </section>

    <ElDialog v-model="rowDialogOpen" :title="rowDialogMode === 'insert' ? '新增数据行' : '编辑数据行'" width="min(760px, calc(100vw - 28px))">
      <div class="row-dialog-context"><component :is="tableTypeIcon(table.type)" :size="18" /><span><strong>{{ table.name }}</strong><small>保存后将直接写入数据库</small></span></div>
      <ElForm class="row-form" label-position="top">
        <ElFormItem v-for="column in columns" :key="column.name">
          <template #label><span class="form-label"><span>{{ column.name }}<small>{{ column.dataType }}<template v-if="column.primaryKey"> / 主键</template></small></span><ElSwitch v-if="column.nullable && !isBooleanColumn(column)" v-model="rowNullFields[column.name]" size="small" inline-prompt active-text="NULL" inactive-text="值" /></span></template>
          <DatabaseCellEditor :model-value="rowForm[column.name] ?? ''" :null-selected="rowNullFields[column.name] === true" :column="column" :disabled="rowNullFields[column.name] || (rowDialogMode === 'edit' && column.primaryKey)" :sensitive="isSensitiveColumn(column.name)" @update:model-value="rowForm[column.name] = $event" @update:null-selected="rowNullFields[column.name] = $event" />
        </ElFormItem>
      </ElForm>
      <template #footer><ElButton @click="rowDialogOpen = false">取消</ElButton><ElButton type="primary" :loading="mutationPending" @click="submitRow">{{ rowDialogMode === 'insert' ? '新增数据行' : '保存修改' }}</ElButton></template>
    </ElDialog>

    <ConfirmDangerDialog
      v-model="dangerOpen"
      :title="dangerTitle"
      :description="dangerDescription"
      :impact="dangerImpact"
      :retained="dangerRetained"
      :action-label="dangerAction === 'sql' ? '确认执行' : '永久删除'"
      :busy="dangerBusy || queryPending"
      @confirm="confirmDanger"
    />
  </div>

  <div v-else class="page"><section class="missing-table panel"><Table2 :size="26" /><h1>数据表不存在或数据库尚未连接</h1><a href="/databases">返回数据库列表</a></section></div>
</template>

<style scoped>
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
.definition-workbench,
.sql-workbench {
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
  grid-template-rows: 54px minmax(240px, .95fr) minmax(220px, 1.05fr);
}

.sql-toolbar {
  display: flex;
  align-items: center;
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
  flex-wrap: wrap;
  justify-content: flex-end;
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

.sql-editor {
  min-height: 0;
  overflow: hidden;
  border-bottom: 1px solid var(--ncp-line);
}

.query-result {
  display: flex;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
}

.query-result > header {
  display: flex;
  min-height: 42px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 0 12px;
  border-bottom: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
  font-size: .75rem;
}

.query-result > header span {
  overflow: hidden;
  color: var(--ncp-text-subtle);
  font-family: var(--ncp-font-mono);
  font-size: .68rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.query-table {
  min-height: 0;
  flex: 1;
}

.query-message {
  display: grid;
  min-height: 150px;
  flex: 1;
  place-content: center;
  justify-items: center;
  gap: 6px;
  color: var(--ncp-text-subtle);
  font-size: .78rem;
  text-align: center;
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
  .definition-workbench,
  .sql-workbench {
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
  .definition-workbench,
  .sql-workbench {
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
}
</style>
