<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Braces,
  ChevronLeft,
  ChevronRight,
  CirclePlus,
  Database,
  KeyRound,
  LoaderCircle,
  Pencil,
  Play,
  RefreshCw,
  Search,
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
  ElOption,
  ElSelect,
  ElSwitch,
  ElTable,
  ElTableColumn,
  ElTag,
  ElTooltip,
} from 'element-plus'

import {
  deleteDatabaseRow,
  discoverDatabases,
  executeDatabaseSQL,
  insertDatabaseRow,
  loadDatabaseCatalog,
  loadDatabaseRows,
  updateDatabaseRow,
  type DatabaseCatalog,
  type DatabaseColumn,
  type DatabaseConnection,
  type DatabaseCredentials,
  type DatabaseRows,
  type DatabaseSource,
  type DatabaseTable,
  type DatabaseValue,
  type QueryResult,
} from '@/api/database'
import { NcpApiError } from '@/api/system'
import SqlEditor from '@/components/SqlEditor.vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'

type WorkspaceMode = 'data' | 'sql'
type RowDialogMode = 'insert' | 'edit'

const sources = ref<DatabaseSource[]>([])
const selectedSourceId = ref('')
const catalog = ref<DatabaseCatalog | null>(null)
const selectedTableKey = ref('')
const tableRows = ref<DatabaseRows | null>(null)
const credentialsBySource = ref<Record<string, DatabaseCredentials>>({})
const sourceFilter = ref('')
const tableFilter = ref('')
const mode = ref<WorkspaceMode>('data')
const loading = ref(false)
const rowsLoading = ref(false)
const errorMessage = ref('')
const offset = ref(0)
const sortColumn = ref('')
const sortDirection = ref('')
const credentialDialogOpen = ref(false)
const credentialDraft = ref<DatabaseCredentials>({})
const rowDialogOpen = ref(false)
const rowDialogMode = ref<RowDialogMode>('insert')
const rowForm = ref<Record<string, string>>({})
const rowNullFields = ref<Record<string, boolean>>({})
const originalKeys = ref<Record<string, DatabaseValue>>({})
const mutationPending = ref(false)
const sql = ref('SELECT * FROM ')
const queryResult = ref<QueryResult | null>(null)
const queryPending = ref(false)
const queryError = ref('')

const selectedSource = computed(() => sources.value.find((source) => source.id === selectedSourceId.value) ?? null)
const selectedTable = computed(() => {
  const [schema, name] = selectedTableKey.value.split('\u0000')
  return catalog.value?.tables.find((table) => table.schema === schema && table.name === name) ?? null
})
const filteredSources = computed(() => {
  const term = sourceFilter.value.trim().toLowerCase()
  return sources.value.filter((source) => !term ||
    `${source.name} ${source.project} ${source.module} ${source.driver}`.toLowerCase().includes(term))
})
const filteredTables = computed(() => {
  const term = tableFilter.value.trim().toLowerCase()
  return (catalog.value?.tables ?? []).filter((table) =>
    !term || `${table.schema} ${table.name}`.toLowerCase().includes(term))
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '已发现来源', value: sources.value.length },
  { label: '系统数据库', value: sources.value.filter((source) => source.category === 'system').length, tone: 'warning' },
  { label: '当前数据表', value: catalog.value?.tables.length ?? '—' },
])
const editableColumns = computed(() => selectedTable.value?.columns ?? [])
const primaryKeyColumns = computed(() => editableColumns.value.filter((column) => column.primaryKey))
const queryRows = computed(() => {
  if (!queryResult.value) return []
  return queryResult.value.rows.map((row) =>
    Object.fromEntries(queryResult.value!.columns.map((column, index) => [column, row[index]])))
})

onMounted(() => void refreshDiscovery())

function connection(): DatabaseConnection {
  return {
    sourceId: selectedSourceId.value,
    credentials: credentialsBySource.value[selectedSourceId.value],
  }
}

function tableKey(table: DatabaseTable) {
  return `${table.schema}\u0000${table.name}`
}

function driverLabel(source: DatabaseSource) {
  return source.driver === 'sqlite' ? 'SQLite' : source.driver === 'mysql' ? 'MySQL' : 'PostgreSQL'
}

async function refreshDiscovery() {
  loading.value = true
  errorMessage.value = ''
  try {
    const discovery = await discoverDatabases()
    sources.value = discovery.sources
    if (!selectedSourceId.value && sources.value.length) await chooseSource(sources.value[0]!.id)
  } catch (error) {
    showError(error, '数据库来源发现失败。')
  } finally {
    loading.value = false
  }
}

async function chooseSource(sourceId: string) {
  selectedSourceId.value = sourceId
  catalog.value = null
  selectedTableKey.value = ''
  tableRows.value = null
  queryResult.value = null
  offset.value = 0
  const source = selectedSource.value
  if (!source) return
  if (source.requiresLogin && !credentialsBySource.value[source.id]) {
    credentialDraft.value = { database: source.defaultDatabase ?? '' }
    credentialDialogOpen.value = true
    return
  }
  await connectSource()
}

async function saveCredentials() {
  if (!selectedSource.value) return
  credentialsBySource.value = {
    ...credentialsBySource.value,
    [selectedSource.value.id]: { ...credentialDraft.value },
  }
  credentialDialogOpen.value = false
  await connectSource()
}

async function connectSource() {
  loading.value = true
  errorMessage.value = ''
  try {
    catalog.value = await loadDatabaseCatalog(connection())
    const firstTable = catalog.value.tables.find((table) =>
      !table.columns.some((column) => isSensitiveColumn(column.name))) ?? catalog.value.tables[0]
    if (firstTable) await chooseTable(tableKey(firstTable))
  } catch (error) {
    showError(error, '数据库连接失败，请检查登录信息。')
  } finally {
    loading.value = false
  }
}

async function chooseTable(key: string) {
  selectedTableKey.value = key
  offset.value = 0
  sortColumn.value = ''
  sortDirection.value = ''
  queryResult.value = null
  const table = selectedTable.value
  if (table) sql.value = `SELECT * FROM ${sqlTableName(table)} LIMIT 100`
  await refreshRows()
}

async function refreshRows() {
  const table = selectedTable.value
  if (!table) return
  rowsLoading.value = true
  errorMessage.value = ''
  try {
    tableRows.value = await loadDatabaseRows({
      ...connection(),
      schema: table.schema,
      table: table.name,
      limit: 50,
      offset: offset.value,
      sortColumn: sortColumn.value,
      sortDirection: sortDirection.value,
    })
  } catch (error) {
    showError(error, '表数据读取失败。')
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
  rowForm.value = Object.fromEntries(editableColumns.value.map((column) => [column.name, '']))
  rowNullFields.value = Object.fromEntries(editableColumns.value.map((column) => [column.name, false]))
  originalKeys.value = {}
  rowDialogOpen.value = true
}

function openEdit(row: Record<string, DatabaseValue>) {
  rowDialogMode.value = 'edit'
  rowForm.value = Object.fromEntries(editableColumns.value.map((column) => [
    column.name,
    row[column.name] === null || row[column.name] === undefined ? '' : String(row[column.name]),
  ]))
  rowNullFields.value = Object.fromEntries(editableColumns.value.map((column) => [
    column.name,
    row[column.name] === null || row[column.name] === undefined,
  ]))
  originalKeys.value = Object.fromEntries(primaryKeyColumns.value.map((column) => [column.name, row[column.name] ?? null]))
  rowDialogOpen.value = true
}

async function submitRow() {
  const table = selectedTable.value
  if (!table) return
  mutationPending.value = true
  try {
    const values = Object.fromEntries(editableColumns.value.flatMap((column) => {
      const rawValue = rowForm.value[column.name] ?? ''
      if (rowDialogMode.value === 'insert' && rawValue === '' && (column.primaryKey || column.default !== undefined)) return []
      return [[column.name, rowNullFields.value[column.name] ? null : convertValue(rawValue, column)]]
    }))
    if (rowDialogMode.value === 'insert') {
      await insertDatabaseRow({ ...connection(), schema: table.schema, table: table.name, values })
      ElMessage.success('数据已新增')
    } else {
      await updateDatabaseRow({
        ...connection(), schema: table.schema, table: table.name, values, keys: originalKeys.value,
      })
      ElMessage.success('数据已更新')
    }
    rowDialogOpen.value = false
    await refreshRows()
  } catch (error) {
    showError(error, '数据写入失败。')
  } finally {
    mutationPending.value = false
  }
}

async function removeRow(row: Record<string, DatabaseValue>) {
  const table = selectedTable.value
  if (!table || !primaryKeyColumns.value.length) return
  await ElMessageBox.confirm('将删除当前数据行，此操作会立即写入数据库。', '确认删除', {
    confirmButtonText: '删除',
    cancelButtonText: '取消',
    type: 'warning',
  })
  const keys = Object.fromEntries(primaryKeyColumns.value.map((column) => [column.name, row[column.name] ?? null]))
  try {
    await deleteDatabaseRow({ ...connection(), schema: table.schema, table: table.name, keys })
    ElMessage.success('数据已删除')
    await refreshRows()
  } catch (error) {
    showError(error, '数据删除失败。')
  }
}

async function runSQL() {
  if (!sql.value.trim()) return
  queryPending.value = true
  queryError.value = ''
  try {
    queryResult.value = await executeDatabaseSQL({ ...connection(), sql: sql.value })
    if (!queryResult.value.columns.length) {
      ElMessage.success(`SQL 已执行，影响 ${queryResult.value.rowsAffected} 行`)
      await refreshRows()
    }
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
  if (/(bool)/.test(type)) return value === 'true' || value === '1'
  return value
}

function isSensitiveColumn(name: string) {
  return /(password|passwd|token|secret|cookie|api[_-]?key|private[_-]?key)/i.test(name)
}

function displayValue(column: string, value: DatabaseValue | undefined) {
  if (isSensitiveColumn(column) && value !== null && value !== undefined) return '••••••••'
  return value
}

function sqlTableName(table: DatabaseTable) {
  const quote = selectedSource.value?.driver === 'mysql' ? '`' : '"'
  const name = `${quote}${table.name.replaceAll(quote, quote + quote)}${quote}`
  if (!table.schema || selectedSource.value?.driver === 'sqlite') return name
  return `${quote}${table.schema.replaceAll(quote, quote + quote)}${quote}.${name}`
}

function showError(error: unknown, fallback: string) {
  errorMessage.value = error instanceof NcpApiError ? error.message : fallback
}
</script>

<template>
  <div class="page workspace-page database-page">
    <WorkspaceHeader title="数据库" description="发现 NAS 与项目数据库，直接管理表数据和执行 SQL" :icon="Database" :stats="stats">
      <template #actions>
        <ElTooltip content="重新扫描数据库来源">
          <ElButton :loading="loading" @click="refreshDiscovery"><RefreshCw :size="16" />重新发现</ElButton>
        </ElTooltip>
      </template>
      <template #tools>
        <ElSelect class="mobile-source-select" :model-value="selectedSourceId" placeholder="选择数据库来源" @change="chooseSource">
          <ElOption v-for="source in sources" :key="source.id" :value="source.id" :label="source.name" />
        </ElSelect>
        <ElSelect class="mobile-table-select" :model-value="selectedTableKey" placeholder="选择数据表" :disabled="!catalog" @change="chooseTable">
          <ElOption v-for="table in catalog?.tables ?? []" :key="tableKey(table)" :value="tableKey(table)" :label="table.schema ? `${table.schema}.${table.name}` : table.name" />
        </ElSelect>
      </template>
    </WorkspaceHeader>

    <div v-if="errorMessage" class="database-error" role="alert">
      <span>{{ errorMessage }}</span><button type="button" @click="errorMessage = ''">关闭</button>
    </div>

    <section class="database-workspace panel">
      <aside class="source-pane">
        <div class="pane-heading">
          <div><strong>数据库来源</strong><span>{{ sources.length }} 个已发现</span></div>
          <LoaderCircle v-if="loading" class="spin" :size="16" />
        </div>
        <ElInput v-model="sourceFilter" clearable placeholder="搜索来源或项目">
          <template #prefix><Search :size="15" /></template>
        </ElInput>
        <div class="source-list">
          <button
            v-for="source in filteredSources"
            :key="source.id"
            type="button"
            :class="['source-item', { active: source.id === selectedSourceId }]"
            @click="chooseSource(source.id)"
          >
            <span class="source-item__icon"><Database :size="18" /></span>
            <span class="source-item__body">
              <strong>{{ source.name }}</strong>
              <small>{{ source.project }} · {{ source.module }}</small>
              <span><b>{{ driverLabel(source) }}</b><em>{{ source.category === 'system' ? '系统' : '项目' }}</em></span>
            </span>
            <ChevronRight :size="15" />
          </button>
          <p v-if="!filteredSources.length" class="pane-empty">没有匹配的数据库来源</p>
        </div>
      </aside>

      <aside class="table-pane">
        <div class="pane-heading">
          <div><strong>数据表</strong><span>{{ catalog?.tables.length ?? 0 }} 张</span></div>
        </div>
        <ElInput v-model="tableFilter" clearable placeholder="搜索数据表">
          <template #prefix><Search :size="15" /></template>
        </ElInput>
        <div class="table-list">
          <button
            v-for="table in filteredTables"
            :key="tableKey(table)"
            type="button"
            :class="['table-item', { active: tableKey(table) === selectedTableKey }]"
            @click="chooseTable(tableKey(table))"
          >
            <Table2 :size="16" />
            <span><strong>{{ table.name }}</strong><small>{{ table.schema || 'SQLite' }} · {{ table.columns.length }} 字段</small></span>
          </button>
          <p v-if="catalog && !filteredTables.length" class="pane-empty">没有匹配的数据表</p>
          <p v-else-if="!catalog" class="pane-empty">选择数据库后加载数据表</p>
        </div>
      </aside>

      <main class="data-pane">
        <template v-if="selectedTable">
          <header class="data-heading">
            <div>
              <span class="data-heading__icon"><Table2 :size="19" /></span>
              <div>
                <h2>{{ selectedTable.name }}</h2>
                <p>{{ selectedSource?.name }}<template v-if="selectedTable.schema"> · {{ selectedTable.schema }}</template></p>
              </div>
            </div>
            <nav class="mode-switch" aria-label="数据库工作模式">
              <button type="button" :class="{ active: mode === 'data' }" @click="mode = 'data'"><Table2 :size="15" />表数据</button>
              <button type="button" :class="{ active: mode === 'sql' }" @click="mode = 'sql'"><Braces :size="15" />SQL</button>
            </nav>
          </header>

          <template v-if="mode === 'data'">
            <div class="data-toolbar">
              <div class="field-summary">
                <ElTag v-for="column in primaryKeyColumns" :key="column.name" effect="plain">
                  <KeyRound :size="12" />{{ column.name }}
                </ElTag>
                <span>{{ selectedTable.columns.length }} 个字段 · 当前加载 {{ tableRows?.rows.length ?? 0 }} 行</span>
              </div>
              <div class="table-actions">
                <ElButton :loading="rowsLoading" aria-label="重新读取当前页" @click="refreshRows">
                  <RefreshCw :size="15" />刷新
                </ElButton>
                <ElButton type="primary" @click="openInsert">
                  <CirclePlus :size="16" />新增行
                </ElButton>
              </div>
            </div>
            <div v-if="!primaryKeyColumns.length" class="primary-key-warning">
              当前表没有主键，可以查看和新增数据；为避免误改多行，编辑与删除已停用。
            </div>

            <div class="data-table-wrap">
              <ElTable
                v-loading="rowsLoading"
                :data="tableRows?.rows ?? []"
                height="100%"
                border
                stripe
                highlight-current-row
                @sort-change="onSortChange"
              >
                <ElTableColumn type="index" label="#" width="56" fixed="left" align="center" />
                <ElTableColumn
                  v-for="column in selectedTable.columns"
                  :key="column.name"
                  :prop="column.name"
                  :min-width="column.primaryKey ? 110 : Math.min(320, Math.max(150, column.name.length * 12 + 64))"
                  sortable="custom"
                  show-overflow-tooltip
                >
                  <template #header>
                    <span class="column-heading">
                      <KeyRound v-if="column.primaryKey" :size="12" />
                      {{ column.name }}<small>{{ column.dataType || '未声明类型' }}</small>
                    </span>
                  </template>
                  <template #default="{ row }">
                    <span v-if="row[column.name] === null" class="null-value">NULL</span>
                    <span v-else class="cell-value">{{ displayValue(column.name, row[column.name]) }}</span>
                  </template>
                </ElTableColumn>
                <ElTableColumn label="行操作" fixed="right" width="150" align="center">
                  <template #default="{ row }">
                    <div class="row-actions">
                      <button type="button" :disabled="!primaryKeyColumns.length" aria-label="编辑当前行" @click.stop="openEdit(row)">
                        <Pencil :size="14" />编辑
                      </button>
                      <button class="danger" type="button" :disabled="!primaryKeyColumns.length" aria-label="删除当前行" @click.stop="removeRow(row)">
                        <Trash2 :size="14" />删除
                      </button>
                    </div>
                  </template>
                </ElTableColumn>
                <template #empty><span class="table-empty">当前表没有数据</span></template>
              </ElTable>
            </div>
            <footer class="data-pagination">
              <span>第 {{ Math.floor(offset / 50) + 1 }} 页 · 每页 50 行</span>
              <div>
                <ElButton :disabled="offset === 0 || rowsLoading" @click="offset = Math.max(0, offset - 50); refreshRows()"><ChevronLeft :size="15" />上一页</ElButton>
                <ElButton :disabled="!tableRows?.hasMore || rowsLoading" @click="offset += 50; refreshRows()">下一页<ChevronRight :size="15" /></ElButton>
              </div>
            </footer>
          </template>

          <template v-else>
            <section class="sql-workspace">
              <div class="sql-toolbar">
                <div>
                  <strong>SQL 查询</strong>
                  <span>{{ selectedSource?.name }} · {{ selectedTable.name }}</span>
                </div>
                <div>
                  <small>最多返回 500 行</small>
                  <ElButton type="primary" :loading="queryPending" :disabled="!sql.trim()" @click="runSQL">
                    <Play :size="15" />执行
                    <kbd>Ctrl ↵</kbd>
                  </ElButton>
                </div>
              </div>
              <div class="sql-editor">
                <SqlEditor v-model="sql" :disabled="queryPending" @execute="runSQL" />
              </div>
              <div class="query-result">
                <header>
                  <strong>执行结果</strong>
                  <span v-if="queryResult">{{ queryResult.rows.length }} 行 · {{ queryResult.durationMs }} ms<template v-if="queryResult.truncated"> · 已截断</template></span>
                  <span v-else>等待执行</span>
                </header>
                <div v-if="queryError" class="query-message query-message--error" role="alert">
                  <strong>执行失败</strong>
                  <span>{{ queryError }}</span>
                </div>
                <div v-else-if="queryResult?.columns.length" class="query-table">
                  <ElTable :data="queryRows" height="100%" border stripe>
                    <ElTableColumn v-for="column in queryResult.columns" :key="column" :prop="column" :label="column" min-width="140" show-overflow-tooltip>
                      <template #default="{ row }">
                        <span v-if="row[column] === null" class="null-value">NULL</span>
                        <span v-else class="cell-value">{{ displayValue(column, row[column]) }}</span>
                      </template>
                    </ElTableColumn>
                  </ElTable>
                </div>
                <div v-else-if="queryResult" class="query-message query-message--success">
                  <strong>执行成功</strong>
                  <span>影响 {{ queryResult.rowsAffected }} 行，用时 {{ queryResult.durationMs }} ms。</span>
                </div>
                <div v-else class="query-placeholder">
                  <Braces :size="24" />
                  <span>输入 SQL 后按 Ctrl + Enter 执行</span>
                </div>
              </div>
            </section>
          </template>
        </template>
        <div v-else class="workspace-empty">
          <span><Database :size="28" /></span>
          <strong>选择一个数据库和数据表</strong>
          <p>这里将显示实时表数据、字段结构和 SQL 工作区。</p>
        </div>
      </main>
    </section>

    <ElDialog v-model="credentialDialogOpen" :title="`连接 ${selectedSource?.name ?? '数据库'}`" width="min(460px, calc(100vw - 28px))" :close-on-click-modal="false">
      <p class="dialog-note">登录信息只保留在当前页面内存中，不会保存到项目配置。</p>
      <ElForm label-position="top" @submit.prevent="saveCredentials">
        <ElFormItem label="用户名"><ElInput v-model="credentialDraft.username" autocomplete="username" /></ElFormItem>
        <ElFormItem label="密码"><ElInput v-model="credentialDraft.password" type="password" show-password autocomplete="current-password" /></ElFormItem>
        <ElFormItem label="数据库名"><ElInput v-model="credentialDraft.database" placeholder="例如 nasdb" /></ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="credentialDialogOpen = false">取消</ElButton>
        <ElButton type="primary" @click="saveCredentials">连接数据库</ElButton>
      </template>
    </ElDialog>

    <ElDialog v-model="rowDialogOpen" :title="rowDialogMode === 'insert' ? '新增数据行' : '编辑数据行'" width="min(760px, calc(100vw - 28px))">
      <div class="row-dialog-context">
        <Table2 :size="18" />
        <span><strong>{{ selectedTable?.name }}</strong><small>保存后将直接写入数据库</small></span>
      </div>
      <ElForm class="row-form" label-position="top">
        <ElFormItem v-for="column in editableColumns" :key="column.name">
          <template #label>
            <span class="form-label">
              <span>{{ column.name }}<small>{{ column.dataType }}<template v-if="column.primaryKey"> · 主键</template></small></span>
              <ElSwitch
                v-if="column.nullable"
                v-model="rowNullFields[column.name]"
                size="small"
                inline-prompt
                active-text="NULL"
                inactive-text="值"
              />
            </span>
          </template>
          <ElInput
            v-model="rowForm[column.name]"
            :disabled="rowNullFields[column.name] || (rowDialogMode === 'edit' && column.primaryKey)"
            :type="isSensitiveColumn(column.name) ? 'password' : /text|json|blob/i.test(column.dataType) ? 'textarea' : 'text'"
            :autosize="{ minRows: 2, maxRows: 5 }"
            :placeholder="rowNullFields[column.name] ? '将写入 NULL' : column.default !== undefined ? `留空使用默认值 ${column.default}` : '请输入字段值'"
          />
        </ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="rowDialogOpen = false">取消</ElButton>
        <ElButton type="primary" :loading="mutationPending" @click="submitRow">{{ rowDialogMode === 'insert' ? '新增数据行' : '保存修改' }}</ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<style scoped>
.database-page { min-height: calc(100dvh - var(--ncp-topbar-height)); }
.mobile-source-select,.mobile-table-select { display: none; }
.database-error { display:flex; min-height:44px; align-items:center; justify-content:space-between; padding:9px 13px; border:1px solid rgba(212,81,93,.2); border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.72rem; }
.database-error button { min-height:34px; padding:0 10px; border-radius:8px; background:#fff8; color:inherit; font-weight:700; }
.database-workspace { display:grid; grid-template-columns:270px 220px minmax(0,1fr); min-height:660px; height:calc(100dvh - 250px); overflow:hidden; }
.source-pane,.table-pane { display:flex; min-width:0; flex-direction:column; gap:10px; padding:14px 12px; border-right:1px solid var(--ncp-line); background:#fff; }
.table-pane { background:var(--ncp-surface-quiet); }
.source-pane>.el-input,.table-pane>.el-input { height:32px; min-height:32px; max-height:32px; flex:0 0 32px; }
.pane-heading { display:flex; min-height:34px; align-items:center; justify-content:space-between; padding:0 3px; }
.pane-heading>div { display:grid; gap:1px; }
.pane-heading strong { font-size:.75rem; }
.pane-heading span { color:var(--ncp-text-subtle); font-size:.61rem; }
.source-list,.table-list { display:grid; align-content:start; flex:1; gap:5px; min-height:0; overflow:auto; padding:1px; }
.source-item { display:grid; grid-template-columns:36px minmax(0,1fr) 15px; align-items:center; gap:9px; width:100%; min-height:72px; padding:9px; border:1px solid transparent; border-radius:10px; background:transparent; color:var(--ncp-text-subtle); text-align:left; transition:all var(--ncp-duration-fast); }
.source-item:hover { background:var(--ncp-surface-hover); color:var(--ncp-primary-strong); transform:translateX(2px); }
.source-item.active { border-color:rgba(36,104,216,.18); background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.source-item__icon { display:grid; width:36px; height:36px; place-items:center; border-radius:10px; background:#fff; box-shadow:0 2px 8px rgba(28,45,75,.07); }
.source-item__body { display:grid; min-width:0; gap:2px; }
.source-item__body strong,.source-item__body small { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.source-item__body strong { color:var(--ncp-text); font-size:.7rem; }
.source-item__body small { font-size:.58rem; }
.source-item__body>span { display:flex; gap:5px; margin-top:2px; }
.source-item__body b,.source-item__body em { padding:1px 5px; border-radius:4px; background:#fff; color:var(--ncp-text-muted); font-size:.52rem; font-style:normal; font-weight:700; }
.source-item__body em { background:var(--ncp-warning-soft); color:var(--ncp-warning-strong); }
.table-item { display:flex; align-items:center; gap:8px; min-height:50px; padding:7px 9px; border:1px solid transparent; border-radius:9px; background:transparent; color:var(--ncp-text-subtle); text-align:left; transition:all var(--ncp-duration-fast); }
.table-item:hover,.table-item.active { background:#fff; color:var(--ncp-primary-strong); }
.table-item.active { border-color:rgba(36,104,216,.17); box-shadow:0 3px 10px rgba(28,45,75,.05); }
.table-item span { display:grid; min-width:0; }
.table-item strong,.table-item small { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.table-item strong { color:var(--ncp-text); font-size:.67rem; }.table-item small { font-size:.55rem; }
.pane-empty { padding:24px 8px; color:var(--ncp-text-subtle); font-size:.65rem; text-align:center; }
.data-pane { display:flex; min-width:0; min-height:0; flex-direction:column; background:#fff; }
.data-heading { display:flex; min-height:72px; align-items:center; justify-content:space-between; gap:14px; padding:12px 16px; border-bottom:1px solid var(--ncp-line); }
.data-heading>div { display:flex; min-width:0; align-items:center; gap:10px; }
.data-heading__icon { display:grid; width:40px; height:40px; flex:0 0 auto; place-items:center; border-radius:11px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.data-heading h2 { margin:0; font-size:1rem; letter-spacing:-.03em; }.data-heading p { margin:2px 0 0; color:var(--ncp-text-subtle); font-size:.62rem; }
.mode-switch { display:flex; gap:3px; padding:3px; border-radius:10px; background:var(--ncp-surface-quiet); }
.mode-switch button { display:flex; min-height:36px; align-items:center; gap:6px; padding:0 11px; border-radius:7px; background:transparent; color:var(--ncp-text-muted); font-size:.65rem; font-weight:700; }
.mode-switch button.active { background:#fff; box-shadow:0 2px 8px rgba(28,45,75,.08); color:var(--ncp-primary-strong); }
.data-toolbar { display:flex; min-height:54px; align-items:center; justify-content:space-between; gap:10px; padding:8px 14px; border-bottom:1px solid var(--ncp-line); }
.data-toolbar>div { display:flex; align-items:center; gap:7px; }
.field-summary { color:var(--ncp-text-subtle); font-size:.62rem; }.field-summary :deep(.el-tag) { gap:3px; }
.data-table-wrap { min-height:0; flex:1; }
.data-table-wrap :deep(.el-table) { --el-table-header-bg-color:var(--ncp-surface-quiet); --el-table-row-hover-bg-color:var(--ncp-surface-hover); font-size:.67rem; }
.column-heading { display:flex; align-items:center; gap:4px; color:var(--ncp-text); font-weight:730; }
.column-heading small { margin-left:3px; color:var(--ncp-text-subtle); font-family:'JetBrains Mono Variable',monospace; font-size:.5rem; font-weight:500; }
.cell-value { font-family:'JetBrains Mono Variable',monospace; font-size:.61rem; }.null-value { color:var(--ncp-text-subtle); font-family:'JetBrains Mono Variable',monospace; font-size:.58rem; font-style:italic; }
.row-actions { display:flex; gap:4px; }.row-actions button { display:grid; width:34px; height:34px; place-items:center; border-radius:8px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }.row-actions button.danger { background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); }.row-actions button:disabled { cursor:not-allowed; opacity:.35; }
.table-empty { color:var(--ncp-text-subtle); font-size:.68rem; }
.data-pagination { display:flex; min-height:54px; align-items:center; justify-content:space-between; gap:10px; padding:8px 14px; border-top:1px solid var(--ncp-line); color:var(--ncp-text-subtle); font-size:.62rem; }.data-pagination>div { display:flex; gap:7px; }
.sql-workspace { display:grid; grid-template-rows:auto minmax(180px,1fr); min-height:0; flex:1; gap:12px; padding:14px; overflow:auto; background:var(--ncp-surface-quiet); }
.sql-editor,.query-result { display:grid; align-content:start; gap:10px; padding:14px; border:1px solid var(--ncp-line); border-radius:11px; background:#fff; }
.sql-editor>div,.query-result header { display:flex; align-items:center; justify-content:space-between; }.sql-editor span,.query-result strong { font-size:.72rem; font-weight:750; }.sql-editor small,.query-result header span { color:var(--ncp-text-subtle); font-size:.58rem; }
.sql-editor :deep(textarea) { font-family:'JetBrains Mono Variable',monospace; font-size:.68rem; line-height:1.7; }
.sql-editor>.el-button { justify-self:end; }
.query-result { min-height:220px; }.query-table { min-height:180px; height:100%; }.query-result>p { color:var(--ncp-success); font-size:.7rem; }
.workspace-empty { display:grid; min-height:100%; place-items:center; align-content:center; gap:7px; color:var(--ncp-text-subtle); text-align:center; }.workspace-empty>span { display:grid; width:58px; height:58px; place-items:center; border-radius:16px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }.workspace-empty strong { color:var(--ncp-text); font-size:.8rem; }.workspace-empty p { margin:0; font-size:.65rem; }
.dialog-note { margin:-4px 0 16px; padding:9px 11px; border-radius:8px; background:var(--ncp-primary-soft); color:var(--ncp-text-muted); font-size:.65rem; }
.row-form { display:grid; grid-template-columns:1fr 1fr; gap:0 14px; max-height:58vh; overflow:auto; padding-right:5px; }
.form-label { display:flex; align-items:baseline; gap:6px; }.form-label small { color:var(--ncp-text-subtle); font-family:'JetBrains Mono Variable',monospace; font-size:.53rem; }
.spin { animation:spin .8s linear infinite; }@keyframes spin { to { transform:rotate(360deg); } }
@media(max-width:1180px) { .database-workspace { grid-template-columns:230px 190px minmax(0,1fr); } }
@media(max-width:900px) {
  .mobile-source-select,.mobile-table-select { display:block; width:100%; }
  .database-workspace { grid-template-columns:1fr; height:calc(100dvh - 300px); min-height:620px; }
  .source-pane,.table-pane { display:none; }
}
@media(max-width:640px) {
  .database-workspace { height:calc(100dvh - 330px); min-height:560px; border-radius:10px; }
  .data-heading { align-items:flex-start; flex-direction:column; min-height:auto; }.mode-switch { width:100%; }.mode-switch button { flex:1; justify-content:center; min-height:40px; }
  .data-toolbar { align-items:flex-start; flex-direction:column; }.data-toolbar>div:last-child { width:100%; justify-content:flex-end; }
  .data-pagination { align-items:flex-start; flex-direction:column; }.data-pagination>div { width:100%; }.data-pagination :deep(.el-button) { flex:1; }
  .row-form { grid-template-columns:1fr; }
  .field-summary :deep(.el-tag:nth-of-type(n+3)) { display:none; }
}

/* Database workbench: compact, data-first interaction */
.database-workspace {
  grid-template-columns: 264px 220px minmax(0, 1fr);
  height: calc(100dvh - 220px);
  min-height: 620px;
  border: 1px solid var(--ncp-line);
  box-shadow: 0 12px 32px rgba(22, 38, 66, .07);
}

.source-pane,
.table-pane {
  padding: 12px 10px;
}

.source-item {
  min-height: 64px;
}

.table-item {
  min-height: 44px;
}

.data-heading {
  min-height: 66px;
  padding: 10px 14px;
}

.data-toolbar {
  min-height: 52px;
  padding: 7px 12px;
}

.table-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.primary-key-warning {
  padding: 8px 13px;
  border-bottom: 1px solid rgba(183, 116, 13, .15);
  background: var(--ncp-warning-soft);
  color: var(--ncp-warning-strong);
  font-size: .64rem;
}

.data-table-wrap {
  position: relative;
  overflow: hidden;
}

.data-table-wrap :deep(.el-table) {
  --el-table-border-color: #e7ecf3;
  --el-table-header-bg-color: #f7f9fc;
  --el-table-row-hover-bg-color: #f1f6ff;
  --el-table-current-row-bg-color: #edf4ff;
  color: var(--ncp-text);
  font-size: .66rem;
}

.data-table-wrap :deep(.el-table__header th.el-table__cell) {
  height: 42px;
  padding: 0;
  color: var(--ncp-text-muted);
  font-weight: 750;
}

.data-table-wrap :deep(.el-table__body td.el-table__cell) {
  height: 44px;
  padding: 0;
}

.data-table-wrap :deep(.el-table .cell) {
  padding: 0 11px;
  line-height: 1.35;
}

.data-table-wrap :deep(.el-table-fixed-column--right) {
  box-shadow: -8px 0 18px rgba(23, 37, 61, .04);
}

.column-heading {
  min-width: 0;
}

.column-heading small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cell-value {
  display: block;
  overflow: hidden;
  color: #344054;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.null-value {
  display: inline-flex;
  padding: 1px 5px;
  border-radius: 4px;
  background: #f0f2f6;
}

.row-actions {
  justify-content: center;
  gap: 5px;
}

.row-actions button {
  display: inline-flex;
  width: auto;
  height: 30px;
  align-items: center;
  gap: 4px;
  padding: 0 8px;
  border: 1px solid rgba(36, 104, 216, .13);
  border-radius: 7px;
  font-size: .6rem;
  font-weight: 700;
  transition: transform var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast), background var(--ncp-duration-fast);
}

.row-actions button:hover:not(:disabled) {
  background: #dfeaff;
  box-shadow: 0 3px 10px rgba(36, 104, 216, .12);
  transform: translateY(-1px);
}

.row-actions button.danger {
  border-color: rgba(212, 81, 93, .14);
}

.row-actions button.danger:hover:not(:disabled) {
  background: #ffe8eb;
  box-shadow: 0 3px 10px rgba(212, 81, 93, .1);
}

.data-pagination {
  min-height: 50px;
  padding: 7px 12px;
}

.sql-workspace {
  grid-template-rows: 52px minmax(220px, .9fr) minmax(220px, 1.1fr);
  gap: 0;
  padding: 0;
  overflow: hidden;
  background: #fff;
}

.sql-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 7px 12px;
  border-bottom: 1px solid var(--ncp-line);
  background: #fff;
}

.sql-toolbar > div {
  display: flex;
  align-items: center;
  gap: 9px;
}

.sql-toolbar strong {
  font-size: .72rem;
}

.sql-toolbar span,
.sql-toolbar small {
  color: var(--ncp-text-subtle);
  font-size: .59rem;
}

.sql-toolbar kbd {
  margin-left: 4px;
  padding: 1px 5px;
  border: 1px solid rgba(255, 255, 255, .35);
  border-radius: 4px;
  background: rgba(255, 255, 255, .14);
  color: inherit;
  font-family: 'JetBrains Mono Variable', monospace;
  font-size: .5rem;
}

.sql-editor {
  min-height: 0;
  padding: 0;
  border: 0;
  border-bottom: 1px solid var(--ncp-line);
  border-radius: 0;
  overflow: hidden;
}

.query-result {
  display: flex;
  min-height: 0;
  flex-direction: column;
  gap: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  overflow: hidden;
}

.query-result header {
  min-height: 42px;
  flex: 0 0 42px;
  padding: 0 12px;
  border-bottom: 1px solid var(--ncp-line);
  background: var(--ncp-surface-quiet);
}

.query-table {
  min-height: 0;
  height: auto;
  flex: 1;
}

.query-table :deep(.el-table) {
  --el-table-border-color: #e7ecf3;
  --el-table-header-bg-color: #f7f9fc;
  font-size: .64rem;
}

.query-table :deep(.el-table__cell) {
  padding: 7px 0;
}

.query-message,
.query-placeholder {
  display: grid;
  min-height: 150px;
  flex: 1;
  place-content: center;
  gap: 6px;
  padding: 20px;
  color: var(--ncp-text-subtle);
  text-align: center;
}

.query-message strong {
  font-size: .76rem;
}

.query-message span,
.query-placeholder span {
  font-size: .64rem;
}

.query-message--success strong {
  color: var(--ncp-success);
}

.query-message--error strong {
  color: var(--ncp-danger-strong);
}

.query-message--error {
  color: var(--ncp-danger-strong);
}

.row-dialog-context {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: -4px 0 16px;
  padding: 10px 12px;
  border-radius: 9px;
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
}

.row-dialog-context > span {
  display: grid;
  gap: 1px;
}

.row-dialog-context strong {
  font-size: .7rem;
}

.row-dialog-context small {
  color: var(--ncp-text-muted);
  font-size: .57rem;
}

.row-form {
  gap: 2px 16px;
}

.form-label {
  width: 100%;
  justify-content: space-between;
}

.form-label > span {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

@media(max-width:1180px) {
  .database-workspace {
    grid-template-columns: 224px 188px minmax(0, 1fr);
  }
}

@media(max-width:900px) {
  .database-workspace {
    grid-template-columns: 1fr;
    height: calc(100dvh - 280px);
    min-height: 600px;
  }
}

@media(max-width:640px) {
  .database-workspace {
    height: calc(100dvh - 310px);
    min-height: 560px;
  }

  .data-heading {
    gap: 9px;
  }

  .data-toolbar {
    align-items: stretch;
    padding: 8px 10px;
  }

  .field-summary {
    min-height: 24px;
  }

  .table-actions {
    justify-content: stretch !important;
  }

  .table-actions :deep(.el-button) {
    flex: 1;
  }

  .sql-workspace {
    grid-template-rows: auto minmax(240px, 1fr) minmax(220px, 1fr);
  }

  .sql-toolbar {
    align-items: flex-start;
    flex-direction: column;
    padding: 9px 10px;
  }

  .sql-toolbar > div {
    width: 100%;
    justify-content: space-between;
  }

  .sql-toolbar kbd {
    display: none;
  }

  .row-dialog-context {
    margin-top: 0;
  }
}
</style>
