<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  Archive,
  ArchiveRestore,
  ArrowRight,
  Database,
  FolderKanban,
  HardDrive,
  RefreshCw,
  Search,
  Server,
} from '@lucide/vue'
import { ElButton, ElInput, ElMessage, ElTooltip } from 'element-plus'

import type { DatabaseDriver, DatabaseSource } from '@/api/database'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { databaseProjectKey, useDatabaseStore } from '@/stores/database'

type SourceFilter = 'all' | 'project' | 'system' | 'archived'
interface DatabaseProjectGroup {
  key: string
  name: string
  category: DatabaseSource['category']
  sources: DatabaseSource[]
}

const databaseStore = useDatabaseStore()
const query = ref('')
const sourceFilter = ref<SourceFilter>('all')
const errorMessage = ref('')

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
const stats = computed<WorkspaceStat[]>(() => [
  { label: '项目分组', value: activeGroups.value.length },
  { label: '数据库来源', value: activeGroups.value.reduce((total, group) => total + group.sources.length, 0), tone: 'success' },
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

async function refreshDiscovery() {
  errorMessage.value = ''
  try {
    await databaseStore.refreshDiscovery()
  } catch {
    errorMessage.value = '数据库发现失败，请确认 Root Agent 正常运行。'
  }
}

async function toggleArchive(group: DatabaseProjectGroup) {
  const archived = !databaseStore.isProjectArchived(group.key)
  try {
    await databaseStore.setProjectArchived(group.key, archived)
    ElMessage.success(archived ? `已归档 ${group.name}` : `已恢复 ${group.name}`)
  } catch {
    ElMessage.error('归档状态保存失败。')
  }
}

onMounted(() => {
  if (!databaseStore.sources.length) void refreshDiscovery()
})
</script>

<template>
  <div class="page workspace-page database-overview">
    <WorkspaceHeader title="数据库" description="自动发现 NAS 系统与项目数据库，按项目和用途集中管理" :icon="Database" :stats="stats">
      <template #filters>
        <div class="source-filter" aria-label="数据库来源筛选">
          <button
            v-for="item in [{ value: 'all', label: '全部' }, { value: 'project', label: '项目' }, { value: 'system', label: '系统' }, { value: 'archived', label: '已归档' }]"
            :key="item.value"
            type="button"
            :class="{ active: sourceFilter === item.value }"
            @click="sourceFilter = item.value as SourceFilter"
          >{{ item.label }}</button>
        </div>
      </template>
      <template #tools>
        <div class="database-tools">
          <ElInput v-model="query" class="database-search" clearable placeholder="搜索数据库、项目或模块" aria-label="搜索数据库">
            <template #prefix><Search :size="17" /></template>
          </ElInput>
          <ElButton :loading="databaseStore.loading" @click="refreshDiscovery"><RefreshCw :size="16" />重新发现</ElButton>
        </div>
      </template>
    </WorkspaceHeader>

    <div v-if="errorMessage" class="database-error" role="alert">{{ errorMessage }}</div>

    <section v-if="databaseStore.loading && !databaseStore.sources.length" class="database-catalog panel" aria-label="正在发现数据库">
      <div v-for="group in 3" :key="group" class="database-skeleton">
        <header><i class="ncp-skeleton"></i><span class="ncp-skeleton"></span></header>
        <div v-for="row in 2" :key="row"><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></div>
      </div>
    </section>

    <section v-else-if="filteredGroups.length" class="database-catalog panel" aria-label="按项目分组的数据库来源">
      <div class="database-table__head" aria-hidden="true">
        <span>数据库</span><span>用途模块</span><span>类型</span><span>连接位置</span><span>操作</span>
      </div>
      <article v-for="group in filteredGroups" :key="group.key" class="database-group">
        <header class="database-group__header">
          <span :class="['project-icon', { 'project-icon--system': group.category === 'system' }]"><FolderKanban :size="19" /></span>
          <div><strong>{{ group.name }}</strong><small>{{ group.sources.length }} 个数据库来源</small></div>
          <span :class="['project-kind', { 'project-kind--system': group.category === 'system' }]">{{ group.category === 'system' ? '系统模块' : '用户项目' }}</span>
          <ElTooltip :content="databaseStore.isProjectArchived(group.key) ? '恢复到默认列表' : '归档后从默认列表隐藏'" placement="top">
            <button class="archive-button" type="button" @click="toggleArchive(group)">
              <ArchiveRestore v-if="databaseStore.isProjectArchived(group.key)" :size="16" />
              <Archive v-else :size="16" />
              {{ databaseStore.isProjectArchived(group.key) ? '恢复' : '归档' }}
            </button>
          </ElTooltip>
        </header>

        <div class="database-table">
          <RouterLink
            v-for="source in group.sources"
            :key="source.id"
            class="database-row"
            :to="{ name: 'database-detail', params: { sourceId: source.id }, query: { sourceName: source.name } }"
          >
            <div class="database-identity">
              <span :class="['database-type-icon', `database-type-icon--${source.driver}`, { 'database-type-icon--system': source.category === 'system' }]">
                <component :is="driverIcon(source.driver)" :size="19" />
              </span>
              <div>
                  <strong>{{ databaseDisplayName(source, group.name) }}</strong>
                <small>{{ source.tags.slice(0, 2).join(' · ') || (source.category === 'system' ? 'NAS 系统数据库' : '项目数据库') }}</small>
              </div>
            </div>
            <span class="database-module">{{ source.module || '未标注用途' }}</span>
            <span :class="['driver-badge', `driver-badge--${source.driver}`]">{{ driverLabel(source.driver) }}</span>
            <code :title="source.location">{{ source.location }}</code>
            <span class="enter-database">{{ source.requiresLogin ? '连接' : '管理' }}<ArrowRight :size="16" /></span>
          </RouterLink>
        </div>
      </article>
    </section>

    <section v-else class="empty-panel panel">
      <span><Search :size="25" /></span>
      <div><h2>没有匹配的数据库</h2><p>调整筛选条件，或重新执行数据库发现。</p></div>
    </section>
  </div>
</template>

<style scoped>
.database-search{width:min(360px,36vw)}.source-filter{display:flex;flex:0 0 auto;gap:3px;padding:3px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.source-filter button{min-height:36px;padding:0 14px;border-radius:8px;background:transparent;color:var(--ncp-text-muted);font-size:.8rem;font-weight:680}.source-filter button:hover{background:rgba(255,255,255,.66);color:var(--ncp-text)}.source-filter button.active{background:#fff;box-shadow:0 2px 10px rgba(28,45,75,.08);color:var(--ncp-primary-strong)}.database-error{padding:11px 14px;border:1px solid rgba(201,83,97,.18);border-radius:11px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.82rem}.database-catalog{overflow:hidden}.database-group+.database-group{border-top:1px solid var(--ncp-line-strong)}.database-group__header{display:grid;min-height:58px;grid-template-columns:auto minmax(0,1fr) auto auto;align-items:center;gap:11px;padding:8px 18px;border-top:1px solid var(--ncp-line);border-bottom:1px solid var(--ncp-line);background:#f8fafc}.project-icon{display:grid;width:36px;height:36px;place-items:center;border-radius:10px;background:#e8f6f1;color:#23866f}.project-icon--system{background:#fff1e4;color:#b87622}.database-group__header>div{display:grid;min-width:0;gap:1px}.database-group__header strong{overflow:hidden;font-size:.92rem;text-overflow:ellipsis;white-space:nowrap}.database-group__header small{color:var(--ncp-text-subtle);font-size:.74rem}.project-kind{padding:4px 8px;border-radius:7px;background:#ecf6f2;color:#2b816b;font-size:.72rem;font-weight:700}.project-kind--system{background:#fbf1e5;color:#a76922}.archive-button{display:flex;min-height:34px;align-items:center;gap:6px;padding:0 10px;border:1px solid var(--ncp-line);border-radius:9px;background:#fff;color:var(--ncp-text-muted);font-size:.76rem;font-weight:680}.archive-button:hover{border-color:rgba(52,116,212,.24);color:var(--ncp-primary-strong)}.database-table__head,.database-row{display:grid;grid-template-columns:minmax(290px,1.25fr) minmax(180px,.75fr) 150px minmax(260px,1.2fr) 90px;align-items:center;gap:18px;padding-inline:18px}.database-table__head{min-height:46px;background:#eef3f8;color:var(--ncp-text-muted);font-size:.76rem;font-weight:730}.database-table__head span:last-child{text-align:center}.database-row{min-height:72px;border-top:1px solid var(--ncp-line);transition:background-color var(--ncp-duration-fast)}.database-table .database-row:first-child{border-top:0}.database-row:hover{background:#f7faff}.database-identity{display:flex;min-width:0;align-items:center;gap:12px}.database-type-icon{display:grid;width:40px;height:40px;flex:0 0 auto;place-items:center;border-radius:11px;background:#eeeafd;color:#6d55b5}.database-type-icon--mysql{background:#fff1df;color:#b76a13}.database-type-icon--postgresql{background:#e8f1fb;color:#356da9}.database-type-icon--system{background:#f8ecf1;color:#a94d72;box-shadow:none}.database-identity>div{display:grid;min-width:0;gap:2px}.database-identity strong{overflow:hidden;font-size:.88rem;text-overflow:ellipsis;white-space:nowrap}.database-identity small,.database-module{overflow:hidden;color:var(--ncp-text-subtle);font-size:.76rem;text-overflow:ellipsis;white-space:nowrap}.driver-badge{display:inline-flex;width:max-content;padding:5px 8px;border-radius:8px;background:#eeeafd;color:#6d55b5;font-size:.72rem;font-weight:720}.driver-badge--mysql{background:#fff2e3;color:#a9661f}.driver-badge--postgresql{background:#eaf1f9;color:#386d9e}.database-row code{overflow:hidden;color:var(--ncp-text-muted);font-family:var(--ncp-font-mono);font-size:.74rem;text-overflow:ellipsis;white-space:nowrap}.enter-database{display:flex;align-items:center;justify-content:center;gap:5px;color:var(--ncp-primary-strong);font-size:.78rem;font-weight:730}.database-row:hover .enter-database svg{transform:translateX(2px)}.enter-database svg{transition:transform var(--ncp-duration-fast)}.empty-panel{display:flex;min-height:280px;align-items:center;justify-content:center;gap:14px}.empty-panel>span{display:grid;width:48px;height:48px;place-items:center;border-radius:14px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle)}.empty-panel h2{margin:0;font-size:.98rem}.empty-panel p{margin:4px 0 0;font-size:.8rem}.database-skeleton+.database-skeleton{border-top:1px solid var(--ncp-line)}.database-skeleton header{display:flex;min-height:58px;align-items:center;gap:12px;padding:0 18px;background:var(--ncp-surface-quiet)}.database-skeleton header i{width:38px;height:38px}.database-skeleton header span{width:180px;height:15px}.database-skeleton>div{display:grid;min-height:72px;grid-template-columns:260px 160px 120px 1fr;align-items:center;gap:38px;padding:0 18px;border-top:1px solid var(--ncp-line)}.database-skeleton>div i{height:13px}@media(max-width:1180px){.database-table{overflow-x:auto}.database-table__head,.database-row{min-width:980px}}@media(max-width:700px){.database-search{width:100%}.database-group__header{grid-template-columns:auto minmax(0,1fr) auto}.project-kind{display:none}.database-table{overflow:visible}.database-table__head{display:none}.database-row{display:grid;min-width:0;grid-template-columns:1fr auto;gap:10px;padding:14px 16px}.database-module,.database-row code{grid-column:1/-1}.driver-badge{grid-column:1}.enter-database{grid-column:2;grid-row:2}.database-group+.database-group{border-top-width:1px}.database-skeleton>div{grid-template-columns:1fr 90px}.database-skeleton>div i:nth-child(n+3){display:none}}@media(max-width:500px){.database-group__header{padding-inline:14px}.archive-button{width:36px;padding:0;justify-content:center}.archive-button{font-size:0}.source-filter{width:100%}.source-filter button{flex:1;padding-inline:6px}}
.database-tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

.project-icon {
  color: var(--ncp-object-project);
  background: var(--ncp-object-project-soft);
}

.project-icon--system {
  color: var(--ncp-object-system);
  background: var(--ncp-object-system-soft);
}

.project-kind {
  color: var(--ncp-object-project);
  background: var(--ncp-object-project-soft);
}

.project-kind--system {
  color: var(--ncp-object-system);
  background: var(--ncp-object-system-soft);
}

.database-type-icon {
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

.driver-badge {
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

@media (max-width: 700px) {
  .database-tools {
    width: 100%;
  }

  .database-search {
    width: auto;
    flex: 1;
  }
}
</style>
