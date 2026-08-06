<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  Archive,
  ArchiveRestore,
  ArrowRight,
  CircleAlert,
  Database,
  FolderKanban,
  HardDrive,
  Info,
  RefreshCw,
  Search,
  Server,
} from '@lucide/vue'
import { ElButton, ElInput, ElMessage, ElTooltip } from 'element-plus'

import { NcpApiError } from '@/api/system'
import type { DatabaseDriver, DatabaseSource } from '@/api/database'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { databaseProjectKey, useDatabaseStore } from '@/stores/database'

type SourceFilter = 'all' | 'project' | 'system' | 'archived'
type StatusTone = 'success' | 'warning' | 'danger' | 'neutral'

interface DatabaseProjectGroup {
  key: string
  name: string
  category: DatabaseSource['category']
  sources: DatabaseSource[]
}

interface ErrorState {
  code: string
  message: string
  nextStep: string
}

interface DatabaseErrorCopy {
  message: string
  nextStep: string
}

const databaseErrorCopy: Record<string, DatabaseErrorCopy> = {
  credentials_required: { message: '连接需要数据库凭据。', nextStep: '打开对应数据源，输入凭据后重新测试连接。' },
  auth_failed: { message: '数据库认证失败，请核对用户名、密码和数据库名。', nextStep: '核对连接信息后重新测试；错误提示不会显示或保存密码。' },
  unreachable: { message: '数据库服务不可达，请检查地址和端口。', nextStep: '确认数据库服务运行且主机、端口可访问后重试。' },
  database_not_found: { message: '目标数据库或数据表不存在。', nextStep: '确认数据库名、schema 或表名后重新读取目录。' },
  permission_denied: { message: '当前账号没有执行该操作的权限。', nextStep: '改用具备目录读取或操作权限的账号后重试。' },
  sql_invalid: { message: 'SQL 或数据库请求无效。', nextStep: '检查 SQL 语句、表名和参数后重试。' },
  constraint_failed: { message: '数据约束未满足，请检查提交内容。', nextStep: '检查必填字段、唯一键和外键约束后重试。' },
  agent_unavailable: { message: 'Root Agent 暂不可用，请确认代理服务状态。', nextStep: '确认 Root Agent 正常运行后重新发现数据库。' },
  timeout: { message: '数据库操作超时，请稍后重试。', nextStep: '检查网络和数据库负载，稍后重新发现。' },
  credential_store_unavailable: { message: '数据库凭据服务暂不可用。', nextStep: '确认凭据服务正常后重新测试连接。' },
  credential_corrupt: { message: '保存的数据库凭据不可用，请重新连接。', nextStep: '返回数据源详情页重新输入凭据。' },
  key_unavailable: { message: '数据库凭据加密服务不可用。', nextStep: '确认凭据加密服务正常后重试。' },
  key_rotation_failed: { message: '数据库凭据密钥更新失败。', nextStep: '稍后重试；若持续失败，请记录错误码联系管理员。' },
  migration_failed: { message: '数据库凭据存储迁移失败。', nextStep: '稍后重试；若持续失败，请记录错误码联系管理员。' },
  DATABASE_OPERATION_FAILED: { message: '数据库操作失败，请稍后重试。', nextStep: '稍后重试；若持续失败，请记录错误码联系管理员。' },
}

const databaseStore = useDatabaseStore()
const query = ref('')
const sourceFilter = ref<SourceFilter>('all')
const errorState = ref<ErrorState | null>(null)

const projectGroups = computed<DatabaseProjectGroup[]>(() => {
  const groups = new Map<string, DatabaseProjectGroup>()
  for (const source of databaseStore.sources) {
    const key = databaseProjectKey(source)
    const existing = groups.get(key)
    if (existing) existing.sources.push(source)
    else groups.set(key, {
      key,
      name: source.project?.trim() || source.module?.trim() || '未关联项目',
      category: source.category,
      sources: [source],
    })
  }
  return [...groups.values()].sort((left, right) =>
    Number(left.category === 'system') - Number(right.category === 'system') || left.name.localeCompare(right.name, 'zh-CN'),
  )
})

const activeGroups = computed(() => projectGroups.value.filter((group) => !databaseStore.isProjectArchived(group.key)))
const archivedGroups = computed(() => projectGroups.value.filter((group) => databaseStore.isProjectArchived(group.key)))
const connectedSourceCount = computed(() => databaseStore.sources.filter((source) => Boolean(databaseStore.catalogs[source.id])).length)
const stats = computed<WorkspaceStat[]>(() => [
  { label: '项目分组', value: activeGroups.value.length },
  { label: '数据库来源', value: activeGroups.value.reduce((total, group) => total + group.sources.length, 0), tone: 'success' },
  { label: '已连接', value: connectedSourceCount.value, tone: 'success' },
  { label: '已归档', value: archivedGroups.value.length, tone: 'warning' },
])
const filteredGroups = computed(() => {
  const term = query.value.trim().toLowerCase()
  return projectGroups.value.flatMap((group) => {
    const archived = databaseStore.isProjectArchived(group.key)
    const matchesType = sourceFilter.value === 'archived'
      ? archived
      : !archived && (sourceFilter.value === 'all' || group.category === sourceFilter.value)
    if (!matchesType) return []
    const groupMatches = !term || group.name.toLowerCase().includes(term)
    const sources = group.sources.filter((source) =>
      groupMatches || `${source.name} ${source.project} ${source.module} ${source.driver} ${source.location}`.toLowerCase().includes(term),
    )
    return sources.length ? [{ ...group, sources }] : []
  })
})

function driverLabel(driver: DatabaseDriver) {
  return driver === 'sqlite' ? 'SQLite' : driver === 'mysql' ? 'MySQL / MariaDB' : 'PostgreSQL'
}

function driverIcon(driver: DatabaseDriver) {
  return driver === 'sqlite' ? HardDrive : driver === 'postgresql' ? Server : Database
}

function databaseDisplayName(source: DatabaseSource, projectName: string) {
  const sourceName = source.name.trim()
  const normalizedProjectName = projectName.trim()
  if (!sourceName || !normalizedProjectName) return sourceName

  const escapedProjectName = normalizedProjectName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const prefixPattern = new RegExp(`^${escapedProjectName}\\s*(?:[·•:：/\\\\-]+)\\s*(.+)$`, 'i')
  return sourceName.match(prefixPattern)?.[1]?.trim() || sourceName
}

function sourceStatus(source: DatabaseSource) {
  if (databaseStore.catalogs[source.id]) {
    return { label: '已连接', tone: 'success' as StatusTone, detail: '认证通过 · 目录已读' }
  }
  if (source.status === 'credentials_required' || source.requiresLogin) {
    return { label: '需要凭据', tone: 'warning' as StatusTone, detail: '认证后读取目录' }
  }
  if (source.status === 'available') {
    return { label: '可用', tone: 'success' as StatusTone, detail: '已发现 · 尚未连接' }
  }
  if (source.status === 'unreachable') {
    return { label: '不可达', tone: 'danger' as StatusTone, detail: '检查地址和端口' }
  }
  return { label: source.status || '未知', tone: 'neutral' as StatusTone, detail: '需要检查连接' }
}

function errorCode(error: unknown) {
  return error instanceof NcpApiError ? error.code : 'DATABASE_OPERATION_FAILED'
}

function errorMessage(error: unknown, fallback: string) {
  const code = errorCode(error)
  return databaseErrorCopy[code]?.message || fallback
}

function errorNextStep(error: unknown, fallback: string) {
  return databaseErrorCopy[errorCode(error)]?.nextStep || fallback
}

function setError(error: unknown, fallback: string, nextStep = '稍后重试；若持续失败，请记录错误码联系管理员。') {
  const code = errorCode(error)
  errorState.value = { code, message: errorMessage(error, fallback), nextStep: errorNextStep(error, nextStep) }
}

function clearError() {
  errorState.value = null
}

async function refreshDiscovery() {
  clearError()
  try {
    await databaseStore.refreshDiscovery()
  } catch (error) {
    setError(error, '数据库发现失败，请确认 Root Agent 正常运行。')
  }
}

async function toggleArchive(group: DatabaseProjectGroup) {
  const archived = !databaseStore.isProjectArchived(group.key)
  try {
    await databaseStore.setProjectArchived(group.key, archived)
    ElMessage.success(archived ? `已归档 ${group.name}` : `已恢复 ${group.name}`)
  } catch (error) {
    ElMessage.error(errorMessage(error, '归档状态保存失败。'))
  }
}

onMounted(() => {
  if (!databaseStore.sources.length) void refreshDiscovery()
})
</script>

<template>
  <div class="page workspace-page database-overview">
    <WorkspaceHeader title="数据库" description="自动发现 NAS 系统与项目数据库，集中查看连接状态和可管理对象" :icon="Database" :stats="stats">
      <template #filters>
        <div class="source-filter" aria-label="数据库来源筛选">
          <button
            v-for="item in [{ value: 'all', label: '全部' }, { value: 'project', label: '项目' }, { value: 'system', label: '系统' }, { value: 'archived', label: '已归档' }]"
            :key="item.value"
            type="button"
            :class="{ active: sourceFilter === item.value }"
            :aria-pressed="sourceFilter === item.value"
            @click="sourceFilter = item.value as SourceFilter"
          >{{ item.label }}</button>
        </div>
      </template>
      <template #tools>
        <div class="database-tools">
          <ElInput v-model="query" class="database-search" clearable placeholder="搜索数据库、项目或模块" aria-label="搜索数据库">
            <template #prefix><Search :size="17" /></template>
          </ElInput>
          <ElButton class="database-action" :loading="databaseStore.loading" title="重新发现数据库" aria-label="重新发现数据库" @click="refreshDiscovery"><RefreshCw :size="16" />重新发现</ElButton>
        </div>
      </template>
    </WorkspaceHeader>

    <div v-if="errorState" class="database-error" role="alert">
      <CircleAlert :size="18" />
      <div class="database-error__body">
        <strong>数据库发现失败</strong>
        <span>{{ errorState.message }}</span>
        <small>下一步：{{ errorState.nextStep }}</small>
      </div>
      <code>代码 {{ errorState.code }}</code>
    </div>

    <div class="database-connection-note" role="note">
      <Info :size="16" aria-hidden="true" />
      <span><strong>连接说明</strong>打开数据源会先完成认证，再读取对象目录；目录读取成功后才标记为“已连接”。</span>
    </div>

    <section v-if="databaseStore.loading && !databaseStore.sources.length" class="database-catalog panel" aria-label="正在发现数据库">
      <div v-for="group in 3" :key="group" class="database-skeleton">
        <header><i class="ncp-skeleton"></i><span class="ncp-skeleton"></span></header>
        <div v-for="row in 2" :key="row"><i v-for="cell in 6" :key="cell" class="ncp-skeleton"></i></div>
      </div>
    </section>

    <section v-else-if="filteredGroups.length" class="database-catalog panel" aria-label="按项目分组的数据库来源">
      <div class="database-table__head" aria-hidden="true">
        <span>数据库</span><span>用途模块</span><span>类型</span><span>状态</span><span>连接位置</span><span>操作</span>
      </div>
      <article v-for="group in filteredGroups" :key="group.key" class="database-group">
        <header class="database-group__header">
          <div class="database-group__summary">
            <span :class="['project-icon', { 'project-icon--system': group.category === 'system' }]" aria-hidden="true"><FolderKanban :size="19" /></span>
            <div><strong>{{ group.name }}</strong><small>{{ group.sources.length }} 个数据库来源</small></div>
          </div>
          <div class="database-group__meta">
            <span :class="['project-kind', { 'project-kind--system': group.category === 'system' }]"><span>{{ group.category === 'system' ? '系统模块' : '用户项目' }}</span></span>
            <ElTooltip :content="databaseStore.isProjectArchived(group.key) ? '恢复到默认列表' : '归档后从默认列表隐藏'" placement="top">
              <button class="archive-button" type="button" @click="toggleArchive(group)">
                <ArchiveRestore v-if="databaseStore.isProjectArchived(group.key)" :size="16" />
                <Archive v-else :size="16" />
                <span>{{ databaseStore.isProjectArchived(group.key) ? '恢复' : '归档' }}</span>
              </button>
            </ElTooltip>
          </div>
        </header>

        <div class="database-table">
          <RouterLink
            v-for="source in group.sources"
            :key="source.id"
            class="database-row"
            :to="{ name: 'database-detail', params: { sourceId: source.id }, query: { sourceName: source.name } }"
            :aria-label="`进入 ${databaseDisplayName(source, group.name)} 数据库详情`"
          >
            <div class="database-identity">
              <span :class="['database-type-icon', `database-type-icon--${source.driver}`, { 'database-type-icon--system': source.category === 'system' }]" aria-hidden="true">
                <component :is="driverIcon(source.driver)" :size="19" />
              </span>
              <div>
                <strong>{{ databaseDisplayName(source, group.name) }}</strong>
                <small>{{ source.tags.slice(0, 2).join(' · ') || (source.category === 'system' ? 'NAS 系统数据库' : '项目数据库') }}</small>
              </div>
            </div>
            <div class="database-module database-cell"><small class="database-field-label">用途</small><span>{{ source.module || '未标注用途' }}</span></div>
            <div class="database-cell"><span :class="['driver-badge', `driver-badge--${source.driver}`]"><small class="database-field-label">类型</small>{{ driverLabel(source.driver) }}</span></div>
            <div class="database-cell"><span :class="['source-status', `source-status--${sourceStatus(source).tone}`]"><small class="database-field-label">状态</small><strong>{{ sourceStatus(source).label }}</strong><small>{{ sourceStatus(source).detail }}</small></span></div>
            <code class="database-location database-cell" :title="source.location || '未提供连接位置'"><small class="database-field-label">位置</small><span>{{ source.location || '未提供' }}</span></code>
            <span class="database-row__affordance" aria-hidden="true"><ArrowRight :size="17" /></span>
          </RouterLink>
        </div>
      </article>
    </section>

    <section v-else class="empty-panel panel">
      <span aria-hidden="true"><Search :size="25" /></span>
      <div><h2>没有匹配的数据库</h2><p>调整筛选条件或搜索词，也可以重新执行数据库发现。</p></div>
    </section>
  </div>
</template>

<style scoped>
.database-search {
  width: min(360px, 36vw);
}

.database-tools,
.source-filter {
  display: flex;
  align-items: center;
  gap: 8px;
}

.source-filter {
  flex: 0 0 auto;
  gap: 3px;
  padding: 3px;
  border: 1px solid var(--ncp-line);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-surface-sunken);
}

.source-filter button {
  min-height: 34px;
  padding: 0 13px;
  border-radius: var(--ncp-radius-sm);
  background: transparent;
  color: var(--ncp-text-muted);
  font-size: .78rem;
  font-weight: 700;
  transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out), color var(--ncp-duration-fast) var(--ncp-ease-out);
}

.source-filter button:hover,
.source-filter button.active {
  background: var(--ncp-surface);
  color: var(--ncp-primary-strong);
}

.source-filter button.active {
  box-shadow: var(--ncp-shadow-control);
}

.database-error {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--ncp-danger-border);
  border-radius: var(--ncp-radius-md);
  background: var(--ncp-danger-soft);
  color: var(--ncp-danger-strong);
}

.database-error > svg {
  flex: 0 0 auto;
  margin-top: 1px;
}

.database-error > div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.database-error__body small {
  color: var(--ncp-danger-strong);
  font-size: .72rem;
  line-height: 1.4;
}

.database-error strong {
  font-size: .8rem;
}

.database-error span {
  color: var(--ncp-danger-strong);
  font-size: .78rem;
  line-height: 1.45;
}

.database-error code {
  margin-left: auto;
  padding: 2px 6px;
  border: 1px solid var(--ncp-danger-border);
  border-radius: var(--ncp-radius-xs);
  color: var(--ncp-danger-strong);
  font-family: var(--ncp-font-mono);
  font-size: .67rem;
  white-space: nowrap;
}

.database-connection-note {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  padding: 0 2px;
  color: var(--ncp-text-subtle);
  font-size: .74rem;
}

.database-connection-note svg {
  flex: 0 0 auto;
  color: var(--ncp-primary-strong);
}

.database-connection-note strong {
  margin-right: 6px;
  color: var(--ncp-text-muted);
  font-weight: 740;
}

.database-catalog {
  overflow: hidden;
}

.database-group + .database-group {
  border-top: 1px solid var(--ncp-line-strong);
}

.database-group__header {
  display: flex;
  min-height: 60px;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 8px 18px;
  border-bottom: 1px solid var(--ncp-line);
  background: linear-gradient(120deg, var(--ncp-surface-quiet), var(--ncp-surface));
}

.database-group__summary,
.database-group__meta {
  display: flex;
  min-width: 0;
  align-items: center;
}

.database-group__summary {
  gap: 11px;
}

.database-group__meta {
  flex: 0 0 auto;
  gap: 10px;
}

.project-icon,
.database-type-icon {
  display: grid;
  flex: 0 0 auto;
  place-items: center;
  border-radius: var(--ncp-radius-control);
}

.project-icon {
  width: 36px;
  height: 36px;
  color: var(--ncp-object-project);
  background: var(--ncp-object-project-soft);
}

.project-icon--system {
  color: var(--ncp-object-system);
  background: var(--ncp-object-system-soft);
}

.database-group__summary > div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.database-group__header strong {
  overflow: hidden;
  font-size: .94rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.database-group__header small {
  color: var(--ncp-text-subtle);
  font-size: .74rem;
}

.project-kind,
.source-status,
.driver-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--ncp-radius-pill);
  font-size: .7rem;
  font-weight: 730;
  white-space: nowrap;
}

.project-kind {
  padding: 5px 9px;
  color: var(--ncp-object-project);
  background: var(--ncp-object-project-soft);
}

.project-kind--system {
  color: var(--ncp-object-system);
  background: var(--ncp-object-system-soft);
}

.archive-button {
  display: inline-flex;
  min-height: 34px;
  align-items: center;
  gap: 6px;
  padding: 0 10px;
  border: 1px solid var(--ncp-control-border);
  border-radius: var(--ncp-radius-control);
  background: var(--ncp-control-surface);
  color: var(--ncp-text-muted);
  font-size: .76rem;
  font-weight: 700;
  transition: border-color var(--ncp-duration-fast) var(--ncp-ease-out), color var(--ncp-duration-fast) var(--ncp-ease-out), background-color var(--ncp-duration-fast) var(--ncp-ease-out);
}

.archive-button:hover {
  border-color: var(--ncp-control-border-hover);
  background: var(--ncp-control-hover);
  color: var(--ncp-primary-strong);
}

.database-table__head,
.database-row {
  display: grid;
  grid-template-columns: minmax(250px, 1.3fr) minmax(170px, .8fr) 150px 126px minmax(220px, 1.1fr) 56px;
  align-items: center;
  gap: 16px;
  padding-inline: 18px;
}

.database-table__head {
  min-height: 44px;
  background: var(--ncp-table-head);
  color: var(--ncp-text-subtle);
  font-size: .72rem;
  font-weight: 740;
}

.database-table__head span:not(:first-child) {
  text-align: center;
}

.database-row {
  min-height: 76px;
  border-top: 1px solid var(--ncp-line);
  color: inherit;
  text-decoration: none;
  cursor: pointer;
  transition: background-color var(--ncp-duration-fast) var(--ncp-ease-out), box-shadow var(--ncp-duration-fast) var(--ncp-ease-out);
}

.database-row:hover,
.database-row:focus-visible {
  background: var(--ncp-table-row-hover);
  box-shadow: inset 3px 0 0 var(--ncp-primary);
}

.database-row:focus-visible {
  outline: 2px solid var(--ncp-primary);
  outline-offset: -2px;
}

.database-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 11px;
  justify-self: start;
}

.database-type-icon {
  width: 38px;
  height: 38px;
  color: var(--ncp-object-sqlite);
  background: var(--ncp-object-sqlite-soft);
}

.database-type-icon--mysql {
  color: var(--ncp-object-mysql);
  background: var(--ncp-object-mysql-soft);
}

.database-type-icon--postgresql {
  color: var(--ncp-object-postgresql);
  background: var(--ncp-object-postgresql-soft);
}

.database-type-icon--system {
  color: var(--ncp-object-system);
  background: var(--ncp-object-system-soft);
}

.database-identity > div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.database-identity strong,
.database-identity small,
.database-module span,
.database-location span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.database-identity strong {
  font-size: .86rem;
}

.database-identity small {
  color: var(--ncp-text-subtle);
  font-size: .7rem;
}

.database-cell {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--ncp-text-muted);
  font-size: .76rem;
  text-align: center;
}

.database-module {
  color: var(--ncp-text-muted);
}

.driver-badge {
  min-height: 26px;
  padding: 0 9px;
  color: var(--ncp-object-sqlite);
  background: var(--ncp-object-sqlite-soft);
}

.driver-badge--mysql {
  color: var(--ncp-object-mysql);
  background: var(--ncp-object-mysql-soft);
}

.driver-badge--postgresql {
  color: var(--ncp-object-postgresql);
  background: var(--ncp-object-postgresql-soft);
}

.source-status {
  min-height: 26px;
  padding: 0 9px;
}

.source-status {
  display: grid;
  min-width: 92px;
  gap: 2px;
  padding-block: 4px;
  line-height: 1.1;
}

.source-status > strong {
  font-size: .7rem;
  font-weight: 740;
}

.source-status > small {
  color: currentColor;
  font-size: .62rem;
  font-weight: 600;
  opacity: .82;
}

.source-status--success {
  color: var(--ncp-success-strong);
  background: var(--ncp-success-soft);
}

.source-status--warning {
  color: var(--ncp-warning-strong);
  background: var(--ncp-warning-soft);
}

.source-status--danger {
  color: var(--ncp-danger-strong);
  background: var(--ncp-danger-soft);
}

.source-status--neutral {
  color: var(--ncp-neutral-strong);
  background: var(--ncp-neutral-soft);
}

.database-location {
  max-width: 100%;
  color: var(--ncp-text-subtle);
  font-family: var(--ncp-font-mono);
  font-size: .69rem;
}

.database-row__affordance {
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

.database-row:hover .database-row__affordance,
.database-row:focus-visible .database-row__affordance {
  border-color: var(--ncp-primary-border);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary-strong);
  transform: translateX(2px);
}

.database-row:active .database-row__affordance {
  transform: translateX(2px) scale(.96);
}

.database-row__affordance svg {
  display: block;
}

.empty-panel {
  display: flex;
  min-height: 280px;
  align-items: center;
  justify-content: center;
  gap: 14px;
}

.empty-panel > span {
  display: grid;
  width: 48px;
  height: 48px;
  place-items: center;
  border-radius: var(--ncp-radius-lg);
  background: var(--ncp-surface-sunken);
  color: var(--ncp-text-subtle);
}

.empty-panel h2 {
  margin: 0;
  font-size: .98rem;
}

.empty-panel p {
  margin: 4px 0 0;
  color: var(--ncp-text-subtle);
  font-size: .8rem;
}

.database-skeleton + .database-skeleton {
  border-top: 1px solid var(--ncp-line);
}

.database-skeleton header {
  display: flex;
  min-height: 60px;
  align-items: center;
  gap: 12px;
  padding: 0 18px;
  background: var(--ncp-surface-quiet);
}

.database-skeleton header i {
  width: 36px;
  height: 36px;
}

.database-skeleton header span {
  width: 180px;
  height: 14px;
}

.database-skeleton > div {
  display: grid;
  min-height: 76px;
  grid-template-columns: minmax(220px, 1.3fr) 150px 120px 110px minmax(180px, 1fr) 56px;
  align-items: center;
  gap: 24px;
  padding: 0 18px;
  border-top: 1px solid var(--ncp-line);
}

.database-skeleton > div i {
  height: 11px;
}

.database-field-label {
  display: none;
}

@media (max-width: 1220px) {
  .database-table {
    overflow-x: auto;
  }

  .database-table__head,
  .database-row {
    min-width: 1040px;
  }
}

@media (max-width: 900px) {
  .database-tools {
    width: 100%;
  }

  .database-search {
    width: auto;
    flex: 1;
  }
}

@media (max-width: 700px) {
  .source-filter {
    width: 100%;
  }

  .source-filter button {
    flex: 1;
    padding-inline: 6px;
  }

  .database-search {
    width: 100%;
  }

  .database-tools :deep(.el-button) {
    min-width: 42px;
    width: 42px;
    padding-inline: 0;
    font-size: 0;
  }

  .database-tools :deep(.el-button svg) {
    margin: 0;
  }

  .database-group__header {
    min-height: 62px;
    padding-inline: 14px;
  }

  .database-group__summary {
    gap: 9px;
  }

  .database-group__meta {
    gap: 0;
  }

  .project-kind {
    display: none;
  }

  .database-table {
    overflow: visible;
  }

  .database-table__head {
    display: none;
  }

  .database-row {
    min-width: 0;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 8px 10px;
    padding: 14px;
  }

  .database-identity {
    align-self: start;
  }

  .database-module,
  .database-location {
    grid-column: 1 / -1;
    justify-content: flex-start;
    text-align: left;
  }

  .database-module {
    align-items: baseline;
    gap: 8px;
    line-height: 1.45;
  }

  .database-type-icon {
    width: 36px;
    height: 36px;
  }

  .database-identity strong {
    font-size: .86rem;
  }

  .database-field-label {
    display: inline-flex;
    flex: 0 0 auto;
    color: var(--ncp-text-subtle);
    font-family: var(--ncp-font-ui);
    font-size: .67rem;
    font-weight: 700;
  }

  .database-location {
    justify-content: flex-start;
    line-height: 1.4;
    text-align: left;
  }

  .database-row__affordance {
    grid-column: 2;
    grid-row: 1;
    align-self: start;
  }

  .database-cell:nth-child(3) {
    grid-column: 1;
    justify-content: flex-start;
  }

  .database-cell:nth-child(4) {
    grid-column: 2;
    justify-content: flex-end;
  }

  .database-skeleton > div {
    grid-template-columns: minmax(0, 1fr) 90px;
  }

  .database-skeleton > div i:nth-child(n + 3) {
    display: none;
  }
}

@media (max-width: 500px) {
  .database-error {
    flex-wrap: wrap;
  }

  .database-error code {
    width: 100%;
    margin-left: 28px;
  }

  .archive-button {
    width: 36px;
    justify-content: center;
    padding-inline: 0;
  }

  .archive-button > span {
    display: none;
  }

  .database-row {
    padding-inline: 13px;
  }
}
</style>
