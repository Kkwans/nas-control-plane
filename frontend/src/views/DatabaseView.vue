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
import { ElButton, ElInput, ElTag, ElTooltip } from 'element-plus'

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
</script>

<template>
  <div class="page workspace-page database-overview">
    <WorkspaceHeader title="数据库" description="自动发现 NAS 系统与项目数据库，按来源进入管理" :icon="Database" :stats="stats">
      <template #actions>
        <ElButton :loading="databaseStore.loading" @click="refreshDiscovery"><RefreshCw :size="16" />重新发现</ElButton>
      </template>
      <template #tools>
        <div class="source-filter" aria-label="数据库来源筛选">
          <button v-for="item in [{ value: 'all', label: '全部' }, { value: 'project', label: '项目' }, { value: 'system', label: '系统' }, { value: 'archived', label: '已归档' }]" :key="item.value" type="button" :class="{ active: sourceFilter === item.value }" @click="sourceFilter = item.value as SourceFilter">
            {{ item.label }}
          </button>
        </div>
        <ElInput v-model="query" class="database-search" clearable placeholder="搜索数据库、项目或模块" aria-label="搜索数据库">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="errorMessage" class="database-error" role="alert">{{ errorMessage }}</div>

    <section v-if="filteredGroups.length" class="database-groups" aria-label="按项目分组的数据库来源">
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
            <button class="archive-button" type="button" @click="databaseStore.setProjectArchived(group.key, !databaseStore.isProjectArchived(group.key))">
              <ArchiveRestore v-if="databaseStore.isProjectArchived(group.key)" :size="16" />
              <Archive v-else :size="16" />
              {{ databaseStore.isProjectArchived(group.key) ? '恢复' : '归档' }}
            </button>
          </ElTooltip>
        </header>
        <div class="database-grid">
          <RouterLink
            v-for="source in group.sources"
            :key="source.id"
            class="database-card"
            :to="{ name: 'database-detail', params: { sourceId: source.id }, query: { sourceName: source.name } }"
          >
            <header>
              <span :class="['database-card__icon', `database-card__icon--${source.driver}`]">
                <component :is="driverIcon(source)" :size="20" />
              </span>
              <div>
                <strong>{{ source.name }}</strong>
                <small>{{ driverLabel(source.driver) }}</small>
              </div>
            </header>
            <dl>
              <div><dt>用途模块</dt><dd>{{ source.module }}</dd></div>
              <div><dt>位置</dt><dd :title="source.location">{{ source.location }}</dd></div>
            </dl>
            <footer>
              <div class="source-tags">
                <span v-for="tag in source.tags.slice(0, 3)" :key="tag">{{ tag }}</span>
              </div>
              <span class="enter-database">{{ source.requiresLogin ? '连接并查看' : '查看数据库' }}<ArrowRight :size="16" /></span>
            </footer>
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
.database-groups { display:grid; gap:12px; }
.database-project { overflow:hidden; }
.database-project__header { display:grid; min-height:62px; grid-template-columns:auto minmax(0,1fr) auto auto; align-items:center; gap:10px; padding:10px 14px; border-bottom:1px solid var(--ncp-line); background:#f8fafc; }
.database-project__icon { display:grid; width:38px; height:38px; place-items:center; border-radius:10px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.database-project__header>div { display:grid; min-width:0; gap:2px; }
.database-project__header strong { overflow:hidden; font-size:.9rem; text-overflow:ellipsis; white-space:nowrap; }
.database-project__header small { color:var(--ncp-text-subtle); font-size:.72rem; }
.archive-button { display:flex; min-height:36px; align-items:center; gap:5px; padding:0 10px; border:1px solid var(--ncp-line); border-radius:8px; background:#fff; color:var(--ncp-text-muted); font-size:.72rem; font-weight:700; transition:border-color var(--ncp-duration-fast),color var(--ncp-duration-fast),background var(--ncp-duration-fast); }
.archive-button:hover { border-color:rgba(36,104,216,.25); background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.database-grid { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); }
.database-card { display:grid; min-width:0; min-height:190px; grid-template-rows:auto 1fr auto; gap:13px; padding:15px; border-right:1px solid var(--ncp-line); border-bottom:1px solid var(--ncp-line); transition:background var(--ncp-duration-fast),box-shadow var(--ncp-duration-base); }
.database-card:nth-child(3n) { border-right:0; }
.database-card:hover { position:relative; z-index:1; background:var(--ncp-surface-hover); box-shadow:inset 3px 0 0 var(--ncp-primary); }
.database-card header { display:grid; grid-template-columns:auto minmax(0,1fr) auto; align-items:center; gap:11px; }
.database-card__icon { display:grid; width:42px; height:42px; place-items:center; border-radius:12px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.database-card__icon--mysql { background:#fff3e2; color:#b66a0a; }
.database-card__icon--postgresql { background:#eaf4ff; color:#27689f; }
.database-card header>div { display:grid; min-width:0; gap:2px; }
.database-card header strong { overflow:hidden; font-size:.9rem; text-overflow:ellipsis; white-space:nowrap; }
.database-card header small { color:var(--ncp-text-subtle); font-size:.76rem; }
.database-card dl { display:grid; gap:8px; margin:0; padding:11px 12px; border-radius:10px; background:var(--ncp-surface-quiet); }
.database-card dl>div { display:grid; grid-template-columns:64px minmax(0,1fr); gap:8px; }
.database-card dt { color:var(--ncp-text-subtle); font-size:.75rem; }
.database-card dd { display:flex; min-width:0; align-items:center; gap:5px; overflow:hidden; margin:0; color:var(--ncp-text-muted); font-size:.78rem; text-overflow:ellipsis; white-space:nowrap; }
.database-card footer { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:10px; }
.source-tags { display:flex; min-width:0; gap:5px; overflow:hidden; }
.source-tags span { flex:0 0 auto; padding:3px 6px; border-radius:5px; background:var(--ncp-surface-quiet); color:var(--ncp-text-subtle); font-size:.68rem; }
.enter-database { display:flex; flex:0 0 auto; align-items:center; gap:4px; color:var(--ncp-primary-strong); font-size:.77rem; font-weight:720; }
.empty-panel { display:flex; min-height:180px; align-items:center; justify-content:center; gap:12px; color:var(--ncp-text-subtle); }
.empty-panel h2 { margin:0; color:var(--ncp-text); font-size:.92rem; }.empty-panel p { margin:3px 0 0; font-size:.8rem; }
@media(max-width:1220px){.database-grid{grid-template-columns:repeat(2,minmax(0,1fr));}.database-card:nth-child(3n){border-right:1px solid var(--ncp-line)}.database-card:nth-child(2n){border-right:0}.database-search{width:min(300px,32vw);}}
@media(max-width:900px){.database-search{width:100%;}.source-filter{width:100%;}.source-filter button{flex:1;}}
@media(max-width:680px){.database-project__header{grid-template-columns:auto minmax(0,1fr) auto}.database-project__header :deep(.el-tag){display:none}.archive-button{width:40px;justify-content:center;padding:0}.archive-button{font-size:0}.database-grid{grid-template-columns:1fr}.database-card,.database-card:nth-child(2n),.database-card:nth-child(3n){min-height:185px;border-right:0}.source-filter{overflow:auto}.source-filter button{min-width:68px}}
</style>
