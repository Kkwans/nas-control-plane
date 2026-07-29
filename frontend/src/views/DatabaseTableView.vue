<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowLeft,
  Braces,
  CirclePlus,
  Columns3,
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
  ElInput,
  ElMessage,
  ElMessageBox,
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
import SqlEditor from '@/components/SqlEditor.vue'
import ListPageSizeControl from '@/components/ListPageSizeControl.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useDatabaseStore } from '@/stores/database'
import { useListPreference } from '@/composables/useListPreference'

type TableMode = 'data' | 'overview' | 'structure' | 'definition' | 'sql'
type RowDialogMode = 'insert' | 'edit'

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
const errorMessage = ref('')
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
const queryError = ref('')

const columns = computed(() => table.value?.columns ?? [])
const primaryKeyColumns = computed(() => columns.value.filter((column) => column.primaryKey))
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

onMounted(() => void initialize())

async function initialize() {
  if (!databaseStore.sources.length) await databaseStore.refreshDiscovery()
  if (!catalog.value && source.value && !source.value.requiresLogin) await databaseStore.loadCatalog(sourceId.value)
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
  errorMessage.value = ''
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
    errorMessage.value = error instanceof NcpApiError ? error.message : '表数据读取失败。'
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
  rowDialogMode.value = 'insert'
  rowForm.value = Object.fromEntries(columns.value.map((column) => [column.name, '']))
  rowNullFields.value = Object.fromEntries(columns.value.map((column) => [column.name, false]))
  originalKeys.value = {}
  rowDialogOpen.value = true
}

function openEdit(row: Record<string, DatabaseValue>) {
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
  if (!table.value) return
  mutationPending.value = true
  try {
    const values = Object.fromEntries(columns.value.flatMap((column) => {
      const rawValue = rowForm.value[column.name] ?? ''
      if (rowDialogMode.value === 'insert' && rawValue === '' && (column.primaryKey || column.default !== undefined)) return []
      return [[column.name, rowNullFields.value[column.name] ? null : convertValue(rawValue, column)]]
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
    errorMessage.value = error instanceof NcpApiError ? error.message : '数据写入失败。'
  } finally {
    mutationPending.value = false
  }
}

async function removeRow(row: Record<string, DatabaseValue>) {
  if (!table.value || !primaryKeyColumns.value.length) return
  await ElMessageBox.confirm('将删除当前数据行，此操作会立即写入数据库。', '确认删除', {
    confirmButtonText: '删除',
    cancelButtonText: '取消',
    type: 'warning',
  })
  const keys = Object.fromEntries(primaryKeyColumns.value.map((column) => [column.name, row[column.name] ?? null]))
  try {
    await deleteDatabaseRow({ ...connection(), schema: table.value.schema, table: table.value.name, keys })
    ElMessage.success('数据行已删除')
    await refreshRows()
  } catch (error) {
    errorMessage.value = error instanceof NcpApiError ? error.message : '数据删除失败。'
  }
}

async function runSQL() {
  if (!sql.value.trim()) return
  queryPending.value = true
  queryError.value = ''
  try {
    queryResult.value = await executeDatabaseSQL({ ...connection(), sql: sql.value })
    if (!queryResult.value.columns.length) await refreshRows()
  } catch (error) {
    queryResult.value = null
    queryError.value = error instanceof NcpApiError ? error.message : 'SQL 执行失败。'
  } finally {
    queryPending.value = false
  }
}

function convertValue(value: string, column: DatabaseColumn): DatabaseValue {
  const type = column.dataType.toLowerCase()
  if (value === '' && column.nullable) return null
  if (/(^|[^a-z])(int|real|float|double|decimal|numeric)/.test(type)) {
    const numeric = Number(value)
    return Number.isFinite(numeric) ? numeric : value
  }
  if (/bool/.test(type)) return value === 'true' || value === '1'
  return value
}

function isSensitiveColumn(name: string) {
  return /(password|passwd|token|secret|cookie|api[_-]?key|private[_-]?key)/i.test(name)
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
  return value === undefined ? '—' : new Intl.NumberFormat('zh-CN').format(value)
}

function formatBytes(value?: number) {
  if (value === undefined) return '—'
  if (value < 1024) return `${value} B`
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MB`
  return `${(value / 1024 ** 3).toFixed(1)} GB`
}
</script>

<template>
  <div v-if="table && source" class="page workspace-page table-workbench-page">
    <WorkspaceHeader :title="table.name" :description="`${source.name}${table.schema ? ` · ${table.schema}` : ''}`" :icon="Table2" :stats="stats">
      <template #actions>
        <ElButton tag="a" :href="`/databases/${sourceId}?sourceName=${encodeURIComponent(source.name)}`"><ArrowLeft :size="16" />返回数据表</ElButton>
      </template>
    </WorkspaceHeader>

    <div v-if="errorMessage" class="database-error" role="alert">{{ errorMessage }}</div>

    <nav class="table-tabs panel" aria-label="数据表工作区">
      <button v-for="item in [
        { value: 'data', label: '表数据', icon: Rows3 },
        { value: 'overview', label: '表信息', icon: Info },
        { value: 'structure', label: '表结构', icon: Columns3 },
        { value: 'definition', label: 'SQL 定义', icon: FileCode2 },
        { value: 'sql', label: '执行 SQL', icon: Braces },
      ]" :key="item.value" type="button" :class="{ active: mode === item.value }" @click="mode = item.value as TableMode">
        <component :is="item.icon" :size="16" />{{ item.label }}
      </button>
    </nav>

    <section v-if="mode === 'data'" class="data-workbench panel">
      <div class="data-toolbar">
        <div>
          <ElTag v-for="column in primaryKeyColumns" :key="column.name" effect="plain"><KeyRound :size="12" />{{ column.name }}</ElTag>
          <span>{{ columns.length }} 个字段 · 当前加载 {{ tableRows?.rows.length ?? 0 }} 行</span>
        </div>
        <div>
          <ElButton :loading="rowsLoading" @click="refreshRows"><RefreshCw :size="15" />刷新</ElButton>
          <ElButton type="primary" @click="openInsert"><CirclePlus :size="16" />新增行</ElButton>
        </div>
      </div>
      <div v-if="!primaryKeyColumns.length" class="primary-key-warning">当前表没有主键，为避免误改多行，编辑和删除已停用。</div>
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
                <button type="button" :disabled="!primaryKeyColumns.length" aria-label="编辑当前行" @click.stop="openEdit(row)"><Pencil :size="14" />编辑</button>
                <button class="danger" type="button" :disabled="!primaryKeyColumns.length" aria-label="删除当前行" @click.stop="removeRow(row)"><Trash2 :size="14" />删除</button>
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
        <div><span>Schema</span><strong>{{ table.schema || 'SQLite 主库' }}</strong></div>
        <div><span>对象类型</span><strong>{{ table.type === 'view' ? '视图' : '数据表' }}</strong></div>
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
        <ElTableColumn prop="dataType" label="数据类型" min-width="170" />
        <ElTableColumn label="允许 NULL" width="120"><template #default="{ row }">{{ row.nullable ? '是' : '否' }}</template></ElTableColumn>
        <ElTableColumn label="主键" width="100"><template #default="{ row }">{{ row.primaryKey ? '是' : '—' }}</template></ElTableColumn>
        <ElTableColumn prop="default" label="默认值" min-width="200"><template #default="{ row }"><span v-if="row.default === undefined" class="null-value">无</span><span v-else class="cell-value">{{ row.default }}</span></template></ElTableColumn>
      </ElTable>
    </section>

    <section v-else-if="mode === 'definition'" class="definition-workbench panel">
      <header><div><FileCode2 :size="18" /><span><strong>SQL 定义</strong><small>数据库返回的原始对象定义</small></span></div></header>
      <div v-if="table.definition" class="definition-editor"><SqlEditor :model-value="table.definition" disabled /></div>
      <div v-else class="definition-empty">当前数据库类型没有提供该数据表的完整 SQL 定义，可在“表结构”中查看字段信息。</div>
    </section>

    <section v-else class="sql-workbench panel">
      <div class="sql-toolbar">
        <div><strong>SQL 查询</strong><span>{{ source.name }} · {{ table.name }}</span></div>
        <div><small>最多返回 500 行</small><ElButton type="primary" :loading="queryPending" :disabled="!sql.trim()" @click="runSQL"><Play :size="15" />执行<kbd>Ctrl ↵</kbd></ElButton></div>
      </div>
      <div class="sql-editor"><SqlEditor v-model="sql" :disabled="queryPending" @execute="runSQL" /></div>
      <div class="query-result">
        <header><strong>执行结果</strong><span v-if="queryResult">{{ queryResult.rows.length }} 行 · {{ queryResult.durationMs }} ms</span><span v-else>等待执行</span></header>
        <div v-if="queryError" class="query-message query-message--error"><strong>执行失败</strong><span>{{ queryError }}</span></div>
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
      <div class="row-dialog-context"><Table2 :size="18" /><span><strong>{{ table.name }}</strong><small>保存后将直接写入数据库</small></span></div>
      <ElForm class="row-form" label-position="top">
        <ElFormItem v-for="column in columns" :key="column.name">
          <template #label><span class="form-label"><span>{{ column.name }}<small>{{ column.dataType }}<template v-if="column.primaryKey"> · 主键</template></small></span><ElSwitch v-if="column.nullable" v-model="rowNullFields[column.name]" size="small" inline-prompt active-text="NULL" inactive-text="值" /></span></template>
          <ElInput v-model="rowForm[column.name]" :disabled="rowNullFields[column.name] || (rowDialogMode === 'edit' && column.primaryKey)" :type="isSensitiveColumn(column.name) ? 'password' : /text|json|blob/i.test(column.dataType) ? 'textarea' : 'text'" :autosize="{ minRows: 2, maxRows: 5 }" :placeholder="rowNullFields[column.name] ? '将写入 NULL' : column.default !== undefined ? `留空使用默认值 ${column.default}` : '请输入字段值'" />
        </ElFormItem>
      </ElForm>
      <template #footer><ElButton @click="rowDialogOpen = false">取消</ElButton><ElButton type="primary" :loading="mutationPending" @click="submitRow">{{ rowDialogMode === 'insert' ? '新增数据行' : '保存修改' }}</ElButton></template>
    </ElDialog>
  </div>

  <div v-else class="page"><section class="missing-table panel"><Table2 :size="26" /><h1>数据表不存在或数据库尚未连接</h1><a href="/databases">返回数据库列表</a></section></div>
</template>

<style scoped>
.table-tabs{display:flex;min-width:0;min-height:50px;align-items:center;gap:4px;padding:5px;background:var(--ncp-surface-quiet);overflow:auto}
.table-tabs button{display:flex;min-width:116px;min-height:38px;align-items:center;justify-content:center;gap:6px;padding:0 14px;border-radius:8px;background:transparent;color:var(--ncp-text-muted);font-size:.78rem;font-weight:680;white-space:nowrap}.table-tabs button.active{background:#fff;box-shadow:0 2px 8px rgba(28,45,75,.08);color:var(--ncp-primary-strong)}
.database-error{padding:10px 13px;border:1px solid rgba(212,81,93,.2);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.82rem}
.data-workbench,.structure-workbench,.definition-workbench,.sql-workbench{height:calc(100dvh - 266px);min-height:560px;overflow:hidden}
.data-workbench{display:flex;min-width:0;flex-direction:column}.data-toolbar{display:flex;min-height:54px;align-items:center;justify-content:space-between;gap:12px;padding:8px 12px;border-bottom:1px solid var(--ncp-line)}.data-toolbar>div{display:flex;align-items:center;gap:8px}.data-toolbar>div:first-child{color:var(--ncp-text-subtle);font-size:.78rem}.data-toolbar :deep(.el-tag),.data-toolbar :deep(.el-tag__content){display:inline-flex;align-items:center;gap:3px}.primary-key-warning{padding:8px 13px;border-bottom:1px solid rgba(183,116,13,.15);background:var(--ncp-warning-soft);color:var(--ncp-warning-strong);font-size:.76rem}
.data-table{min-height:0;flex:1}.data-table :deep(.el-table){--el-table-border-color:#e7ecf3;--el-table-header-bg-color:#f7f9fc;--el-table-row-hover-bg-color:#f1f6ff;font-size:.78rem}.data-table :deep(.el-table__header th.el-table__cell){height:44px;padding:0}.data-table :deep(.el-table__body td.el-table__cell){height:46px;padding:0}.data-table :deep(.el-table .cell){padding:0 11px}.column-heading{display:flex;align-items:center;gap:5px;font-weight:720}.column-heading small{margin-left:3px;color:var(--ncp-text-subtle);font-family:'JetBrains Mono Variable',monospace;font-size:.66rem;font-weight:500}.cell-value{display:block;overflow:hidden;color:#344054;font-family:'JetBrains Mono Variable',monospace;font-size:.73rem;text-overflow:ellipsis;white-space:nowrap}.null-value{display:inline-flex;padding:1px 5px;border-radius:4px;background:#f0f2f6;color:var(--ncp-text-subtle);font-family:'JetBrains Mono Variable',monospace;font-size:.7rem;font-style:italic}
.data-table-skeleton{height:100%;overflow:hidden;border:1px solid var(--ncp-line)}.data-table-skeleton__head,.data-table-skeleton__row{display:grid;grid-auto-flow:column;grid-auto-columns:minmax(130px,1fr);align-items:center;gap:18px;padding:0 14px}.data-table-skeleton__head{height:44px;background:var(--ncp-surface-quiet)}.data-table-skeleton__row{height:46px;border-top:1px solid var(--ncp-line)}.data-table-skeleton i{width:72%;height:11px}
.row-actions{display:flex;justify-content:center;gap:5px}.row-actions button{display:inline-flex;height:31px;align-items:center;gap:4px;padding:0 8px;border:1px solid rgba(36,104,216,.13);border-radius:7px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.7rem;font-weight:700}.row-actions button.danger{border-color:rgba(212,81,93,.14);background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.row-actions button:disabled{cursor:not-allowed;opacity:.35}.data-pagination{display:flex;min-height:52px;align-items:center;justify-content:space-between;padding:7px 12px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.75rem}.data-pagination>div{display:flex;gap:7px}
.info-workbench{display:block}.table-profile{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:1px;padding:1px;overflow:hidden;background:var(--ncp-line)}.table-profile>div{display:grid;gap:5px;min-height:108px;align-content:center;padding:18px;background:#fff}.table-profile span{color:var(--ncp-text-subtle);font-size:.75rem}.table-profile strong{overflow:hidden;font-size:.88rem;text-overflow:ellipsis;white-space:nowrap}
.structure-workbench :deep(.el-table){font-size:.8rem}.structure-name{display:flex;align-items:center;gap:5px;font-weight:700}
.definition-workbench{display:flex;flex-direction:column;background:#fff}.definition-workbench header{display:flex;min-height:60px;align-items:center;padding:0 16px;border-bottom:1px solid var(--ncp-line);background:#fff}.definition-workbench header>div{display:flex;align-items:center;gap:9px}.definition-workbench header span{display:grid}.definition-workbench header strong{font-size:.84rem}.definition-workbench header small{color:var(--ncp-text-subtle);font-size:.72rem}.definition-editor{min-height:0;flex:1;overflow:hidden;background:#fff}.definition-empty{display:grid;min-height:300px;place-content:center;padding:24px;color:var(--ncp-text-subtle);font-size:.82rem;text-align:center}
.sql-workbench{display:grid;grid-template-rows:52px minmax(240px,.95fr) minmax(220px,1.05fr)}.sql-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:7px 12px;border-bottom:1px solid var(--ncp-line)}.sql-toolbar>div{display:flex;align-items:center;gap:9px}.sql-toolbar strong{font-size:.82rem}.sql-toolbar span,.sql-toolbar small{color:var(--ncp-text-subtle);font-size:.73rem}.sql-toolbar kbd{margin-left:4px;padding:1px 5px;border:1px solid rgba(255,255,255,.35);border-radius:4px;background:rgba(255,255,255,.14);color:inherit;font-size:.58rem}.sql-editor{min-height:0;border-bottom:1px solid var(--ncp-line);overflow:hidden}.query-result{display:flex;min-height:0;flex-direction:column;overflow:hidden}.query-result>header{display:flex;min-height:42px;align-items:center;justify-content:space-between;padding:0 12px;border-bottom:1px solid var(--ncp-line);background:var(--ncp-surface-quiet);font-size:.75rem}.query-table{min-height:0;flex:1}.query-message{display:grid;min-height:150px;flex:1;place-content:center;gap:6px;color:var(--ncp-text-subtle);font-size:.78rem;text-align:center}.query-message--error{color:var(--ncp-danger-strong)}.query-message--success strong{color:var(--ncp-success)}
.row-dialog-context{display:flex;align-items:center;gap:9px;margin:-4px 0 16px;padding:10px 12px;border-radius:9px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.row-dialog-context>span{display:grid}.row-dialog-context small{color:var(--ncp-text-muted);font-size:.7rem}.row-form{display:grid;grid-template-columns:1fr 1fr;gap:2px 16px;max-height:58vh;overflow:auto;padding-right:5px}.form-label{display:flex;width:100%;align-items:baseline;justify-content:space-between}.form-label>span{display:flex;align-items:baseline;gap:6px}.form-label small{color:var(--ncp-text-subtle);font-family:'JetBrains Mono Variable',monospace;font-size:.66rem}
.missing-table{display:grid;min-height:260px;place-content:center;gap:8px;text-align:center}.missing-table h1{margin:0;font-size:1rem}.missing-table a{color:var(--ncp-primary-strong)}
@media(max-width:1000px){.table-tabs button{min-width:104px;padding:0 10px}.table-profile{grid-template-columns:repeat(2,minmax(0,1fr))}.data-workbench,.structure-workbench,.definition-workbench,.sql-workbench{height:calc(100dvh - 300px)}}
@media(max-width:680px){.table-tabs{width:100%}.table-tabs button{min-width:100px}.table-tabs button svg{display:none}.data-workbench,.structure-workbench,.definition-workbench,.sql-workbench{height:calc(100dvh - 360px);min-height:540px}.data-toolbar{align-items:stretch;flex-direction:column}.data-toolbar>div:last-child{width:100%}.data-toolbar>div:last-child :deep(.el-button){flex:1}.data-pagination{align-items:stretch;flex-direction:column;gap:8px}.data-pagination>div :deep(.el-button){flex:1}.table-profile{grid-template-columns:1fr 1fr}.row-form{grid-template-columns:1fr}.sql-toolbar{align-items:flex-start;flex-direction:column}.sql-toolbar>div{width:100%;justify-content:space-between}.sql-toolbar kbd{display:none}}
</style>
