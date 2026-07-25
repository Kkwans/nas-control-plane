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
import { ElButton, ElInput, ElMessage, ElTag, ElTooltip } from 'element-plus'

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
    const group = groups.get(key)
    if (group) group.sources.push(source)
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

onMounted(() => {
  if (!databaseStore.sources.length) void refreshDiscovery()
})

async function refreshDiscovery() {
  errorMessage.value = ''
  try {
    await databaseStore.refreshDiscovery()
  } catch {
    errorMessage.value = '数据库发现失败，请确认 Root Agent 正常运行。'
  }
}

function driverLabel(driver: DatabaseDriver) {
  return driver === 'sqlite' ? 'SQLite' : driver === 'mysql' ? 'MySQL / MariaDB' : 'PostgreSQL'
}

function driverIcon(source: DatabaseSource) {
  return source.driver === 'sqlite' ? HardDrive : source.category === 'system' ? Server : Database
}

async function toggleArchive(group: DatabaseProjectGroup) {
  const archived = !databaseStore.isProjectArchived(group.key)
  try {
    await databaseStore.setProjectArchived(group.key, archived)
    ElMessage.success(archived ? `已归档 ${group.name}` : `已恢复 ${group.name}`)
  } catch {
    ElMessage.error('归档状态保存失败')
  }
}
</script>

<template>
  <div class="page workspace-page database-overview">
    <WorkspaceHeader title="数据库" description="自动发现 NAS 系统与项目数据库，按来源进入管理" :icon="Database" :stats="stats">
      <template #actions>
        <ElButton :loading="databaseStore.loading" @click="refreshDiscovery"><RefreshCw :size="16" />重新发现</ElButton>
      </template>
      <template #filters>
        <div class="source-filter" aria-label="数据库来源筛选">
          <button v-for="item in [{ value: 'all', label: '全部' }, { value: 'project', label: '项目' }, { value: 'system', label: '系统' }, { value: 'archived', label: '已归档' }]" :key="item.value" type="button" :class="{ active: sourceFilter === item.value }" @click="sourceFilter = item.value as SourceFilter">
            {{ item.label }}
          </button>
        </div>
      </template>
      <template #tools>
        <ElInput v-model="query" class="database-search" clearable placeholder="搜索数据库、项目或模块" aria-label="搜索数据库">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="errorMessage" class="database-error" role="alert">{{ errorMessage }}</div>

    <section v-if="databaseStore.loading && !databaseStore.sources.length" class="database-loading" aria-label="正在发现数据库">
      <article v-for="group in 3" :key="group" class="database-project panel">
        <header class="database-project__header database-project__header--skeleton">
          <i class="ncp-skeleton"></i><div><i class="ncp-skeleton"></i><i class="ncp-skeleton"></i></div>
        </header>
        <div class="database-table">
          <div v-for="row in 3" :key="row" class="database-row database-row--skeleton">
            <i v-for="cell in 5" :key="cell" class="ncp-skeleton"></i>
          </div>
        </div>
      </article>
    </section>

    <section v-else-if="filteredGroups.length" class="database-groups" aria-label="按项目分组的数据库来源">
      <article v-for="group in filteredGroups" :key="group.key" class="database-project panel">
        <header class="database-project__header">
          <span class="database-project__icon"><FolderKanban :size="20" /></span>
          <div>
            <strong>{{ group.name }}</strong>
            <small>{{ group.sources.length }} 个数据库来源</small>
          </div>
          <ElTag :type="group.category === 'system' ? 'warning' : 'success'" effect="light" size="small">
            {{ group.category === 'system' ? '系统项目' : '用户项目' }}
          </ElTag>
          <ElTooltip :content="databaseStore.isProjectArchived(group.key) ? '恢复后将在默认列表显示' : '归档后从默认列表隐藏'" placement="top">
            <button class="archive-button" type="button" @click="toggleArchive(group)">
              <ArchiveRestore v-if="databaseStore.isProjectArchived(group.key)" :size="16" />
              <Archive v-else :size="16" />
              {{ databaseStore.isProjectArchived(group.key) ? '恢复' : '归档' }}
            </button>
          </ElTooltip>
        </header>
        <div class="database-table">
          <div class="database-table__head" aria-hidden="true">
            <span>数据库</span><span>用途模块</span><span>类型</span><span>连接位置</span><span>操作</span>
          </div>
          <RouterLink
            v-for="source in group.sources"
            :key="source.id"
            class="database-row"
            :to="{ name: 'database-detail', params: { sourceId: source.id }, query: { sourceName: source.name } }"
          >
            <div class="database-identity">
              <span :class="['database-card__icon', `database-card__icon--${source.driver}`]">
                <component :is="driverIcon(source)" :size="20" />
              </span>
              <div>
                <strong>{{ source.name }}</strong>
                <small>{{ source.tags.slice(0, 2).join(' · ') || (source.category === 'system' ? 'NAS 系统数据库' : '项目数据库') }}</small>
              </div>
            </div>
            <span class="database-module">{{ source.module || '未标注用途' }}</span>
            <span class="driver-badge">{{ driverLabel(source.driver) }}</span>
            <code :title="source.location">{{ source.location }}</code>
            <span class="enter-database">{{ source.requiresLogin ? '连接' : '管理' }}<ArrowRight :size="16" /></span>
          </RouterLink>
        </div>
      </article>
    </section>

    <section v-else class="empty-panel panel">
      <Search :size="25" />
      <div><h2>没有匹配的数据库</h2><p>调整筛选条件，或重新执行数据库发现。</p></div>
    </section>
  </div>
</template>

<style scoped>
.database-search { width: min(340px, 34vw); }
.source-filter { display:flex; flex:0 0 auto; gap:3px; padding:3px; border:1px solid var(--ncp-line); border-radius:10px; background:var(--ncp-surface-quiet); }
.source-filter button { min-height:36px; padding:0 13px; border-radius:7px; background:transparent; color:var(--ncp-text-muted); font-size:.8rem; font-weight:680; }
.source-filter button.active { background:#fff; box-shadow:0 2px 9px rgba(28,45,75,.08); color:var(--ncp-primary-strong); }
.database-error { padding:10px 13px; border:1px solid rgba(212,81,93,.2); border-radius:10px; background:var(--ncp-danger-soft); color:var(--ncp-danger-strong); font-size:.82rem; }
.database-groups,.database-loading { display:grid; gap:12px; }
.database-project { overflow:hidden; }
.database-project__header { display:grid; min-height:62px; grid-template-columns:auto minmax(0,1fr) auto auto; align-items:center; gap:10px; padding:10px 14px; border-bottom:1px solid var(--ncp-line); background:#f8fafc; }
.database-project__icon { display:grid; width:38px; height:38px; place-items:center; border-radius:10px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.database-project__header>div { display:grid; min-width:0; gap:2px; }
.database-project__header strong { overflow:hidden; font-size:.9rem; text-overflow:ellipsis; white-space:nowrap; }
.database-project__header small { color:var(--ncp-text-subtle); font-size:.72rem; }
.archive-button { display:flex; min-height:36px; align-items:center; gap:5px; padding:0 10px; border:1px solid var(--ncp-line); border-radius:8px; background:#fff; color:var(--ncp-text-muted); font-size:.72rem; font-weight:700; transition:border-color var(--ncp-duration-fast),color var(--ncp-duration-fast),background var(--ncp-duration-fast); }
.archive-button:hover { border-color:rgba(36,104,216,.25); background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.database-table{overflow:hidden}.database-table__head,.database-row{display:grid;grid-template-columns:minmax(260px,1.25fr) minmax(150px,.7fr) 150px minmax(260px,1.25fr) 82px;align-items:center;gap:14px}.database-table__head{min-height:42px;padding:0 16px;background:#fbfcfe;color:var(--ncp-text-subtle);font-size:.73rem;font-weight:720}.database-row{min-height:72px;padding:10px 16px;border-top:1px solid var(--ncp-line);transition:background var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast)}.database-row:hover{background:var(--ncp-surface-hover);box-shadow:inset 3px 0 0 var(--ncp-primary)}.database-identity{display:flex;min-width:0;align-items:center;gap:11px}.database-identity>div{display:grid;min-width:0;gap:2px}.database-identity strong,.database-identity small,.database-row code{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.database-identity strong{font-size:.86rem}.database-identity small{color:var(--ncp-text-subtle);font-size:.7rem}.database-module{overflow:hidden;color:var(--ncp-text-muted);font-size:.78rem;text-overflow:ellipsis;white-space:nowrap}.driver-badge{justify-self:start;padding:4px 8px;border-radius:7px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);font-size:.7rem;font-weight:720}.database-row code{color:var(--ncp-text-muted);font-family:var(--ncp-font-mono);font-size:.7rem}.database-row--skeleton i{width:75%;height:12px}.database-project__header--skeleton>i{width:38px;height:38px;border-radius:10px}.database-project__header--skeleton>div i:first-child{width:180px;height:12px}.database-project__header--skeleton>div i:last-child{width:90px;height:9px}
.database-card__icon { display:grid; width:42px; height:42px; place-items:center; border-radius:12px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.database-card__icon--mysql { background:#fff3e2; color:#b66a0a; }
.database-card__icon--postgresql { background:#eaf4ff; color:#27689f; }
.enter-database { display:flex; align-items:center; justify-content:flex-end; gap:4px; color:var(--ncp-primary-strong); font-size:.77rem; font-weight:720; }
.empty-panel { display:flex; min-height:180px; align-items:center; justify-content:center; gap:12px; color:var(--ncp-text-subtle); }
.empty-panel h2 { margin:0; color:var(--ncp-text); font-size:.92rem; }.empty-panel p { margin:3px 0 0; font-size:.8rem; }
@media(max-width:1220px){.database-table__head,.database-row{grid-template-columns:minmax(240px,1fr) 140px 130px minmax(210px,1fr) 72px}.database-search{width:min(300px,32vw);}}
@media(max-width:900px){.database-search{width:100%;}.source-filter{width:100%;}.source-filter button{flex:1;}}
@media(max-width:760px){.database-project__header{grid-template-columns:auto minmax(0,1fr) auto}.database-project__header :deep(.el-tag){display:none}.archive-button{width:40px;justify-content:center;padding:0;font-size:0}.database-table__head{display:none}.database-row{grid-template-columns:minmax(0,1fr) auto;gap:8px;min-height:116px}.database-module,.database-row code{grid-column:1/-1}.driver-badge{grid-row:1;grid-column:2}.enter-database{grid-column:2}.source-filter{overflow:auto}.source-filter button{min-width:68px}}
</style>
