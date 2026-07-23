<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  ArrowRight,
  Boxes,
  Database,
  FolderKanban,
  HardDrive,
  RefreshCw,
  Search,
  Server,
} from '@lucide/vue'
import { ElButton, ElInput, ElTag } from 'element-plus'

import type { DatabaseDriver, DatabaseSource } from '@/api/database'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useDatabaseStore } from '@/stores/database'

type SourceFilter = 'all' | 'project' | 'system'

const databaseStore = useDatabaseStore()
const query = ref('')
const sourceFilter = ref<SourceFilter>('all')
const errorMessage = ref('')

const stats = computed<WorkspaceStat[]>(() => [
  { label: '数据库来源', value: databaseStore.sources.length },
  { label: '项目数据库', value: databaseStore.projectCount, tone: 'success' },
  { label: '系统数据库', value: databaseStore.systemCount, tone: 'warning' },
])
const filteredSources = computed(() => {
  const term = query.value.trim().toLowerCase()
  return databaseStore.sources.filter((source) => {
    const matchesType = sourceFilter.value === 'all' || source.category === sourceFilter.value
    const matchesQuery = !term || `${source.name} ${source.project} ${source.module} ${source.driver}`.toLowerCase().includes(term)
    return matchesType && matchesQuery
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
          <button v-for="item in [{ value: 'all', label: '全部' }, { value: 'project', label: '项目' }, { value: 'system', label: '系统' }]" :key="item.value" type="button" :class="{ active: sourceFilter === item.value }" @click="sourceFilter = item.value as SourceFilter">
            {{ item.label }}
          </button>
        </div>
        <ElInput v-model="query" class="database-search" clearable placeholder="搜索数据库、项目或模块" aria-label="搜索数据库">
          <template #prefix><Search :size="17" /></template>
        </ElInput>
      </template>
    </WorkspaceHeader>

    <div v-if="errorMessage" class="database-error" role="alert">{{ errorMessage }}</div>

    <div class="section-heading">
      <div><h2>数据库来源</h2><p>已识别 {{ databaseStore.sources.length }} 个来源，当前显示 {{ filteredSources.length }} 个</p></div>
      <span><Boxes :size="15" />来自文件扫描、Docker 挂载与项目配置</span>
    </div>

    <section v-if="filteredSources.length" class="database-grid" aria-label="数据库来源列表">
      <RouterLink
        v-for="source in filteredSources"
        :key="source.id"
        class="database-card panel"
        :to="{ name: 'database-detail', params: { sourceId: source.id }, query: { sourceName: source.name } }"
      >
        <header>
          <span :class="['database-card__icon', `database-card__icon--${source.driver}`]">
            <component :is="driverIcon(source)" :size="21" />
          </span>
          <div>
            <strong>{{ source.name }}</strong>
            <small>{{ driverLabel(source.driver) }}</small>
          </div>
          <ElTag :type="source.category === 'system' ? 'warning' : 'success'" effect="light" size="small">
            {{ source.category === 'system' ? '系统' : '项目' }}
          </ElTag>
        </header>
        <dl>
          <div><dt>关联项目</dt><dd><FolderKanban :size="14" />{{ source.project }}</dd></div>
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
.section-heading { display:flex; align-items:end; justify-content:space-between; gap:18px; min-height:44px; }
.section-heading h2 { margin:0; font-size:1rem; letter-spacing:-.02em; }
.section-heading p { margin:3px 0 0; color:var(--ncp-text-subtle); font-size:.8rem; }
.section-heading>span { display:flex; align-items:center; gap:6px; color:var(--ncp-text-subtle); font-size:.76rem; }
.database-grid { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:12px; }
.database-card { display:grid; min-width:0; min-height:218px; grid-template-rows:auto 1fr auto; gap:16px; padding:17px; transition:border-color var(--ncp-duration-fast),box-shadow var(--ncp-duration-base),transform var(--ncp-duration-fast); }
.database-card:hover { border-color:rgba(36,104,216,.26); box-shadow:var(--ncp-shadow-hover); transform:translateY(-2px); }
.database-card header { display:grid; grid-template-columns:auto minmax(0,1fr) auto; align-items:center; gap:11px; }
.database-card__icon { display:grid; width:42px; height:42px; place-items:center; border-radius:12px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.database-card__icon--mysql { background:#fff3e2; color:#b66a0a; }
.database-card__icon--postgresql { background:#eaf4ff; color:#27689f; }
.database-card header>div { display:grid; min-width:0; gap:2px; }
.database-card header strong { overflow:hidden; font-size:.9rem; text-overflow:ellipsis; white-space:nowrap; }
.database-card header small { color:var(--ncp-text-subtle); font-size:.76rem; }
.database-card dl { display:grid; gap:8px; margin:0; padding:11px 12px; border-radius:10px; background:var(--ncp-surface-quiet); }
.database-card dl>div { display:grid; grid-template-columns:68px minmax(0,1fr); gap:8px; }
.database-card dt { color:var(--ncp-text-subtle); font-size:.75rem; }
.database-card dd { display:flex; min-width:0; align-items:center; gap:5px; overflow:hidden; margin:0; color:var(--ncp-text-muted); font-size:.78rem; text-overflow:ellipsis; white-space:nowrap; }
.database-card footer { display:flex; min-width:0; align-items:center; justify-content:space-between; gap:10px; }
.source-tags { display:flex; min-width:0; gap:5px; overflow:hidden; }
.source-tags span { flex:0 0 auto; padding:3px 6px; border-radius:5px; background:var(--ncp-surface-quiet); color:var(--ncp-text-subtle); font-size:.68rem; }
.enter-database { display:flex; flex:0 0 auto; align-items:center; gap:4px; color:var(--ncp-primary-strong); font-size:.77rem; font-weight:720; }
.empty-panel { display:flex; min-height:180px; align-items:center; justify-content:center; gap:12px; color:var(--ncp-text-subtle); }
.empty-panel h2 { margin:0; color:var(--ncp-text); font-size:.92rem; }.empty-panel p { margin:3px 0 0; font-size:.8rem; }
@media(max-width:1220px){.database-grid{grid-template-columns:repeat(2,minmax(0,1fr));}.database-search{width:min(300px,32vw);}}
@media(max-width:900px){.database-search{width:100%;}.source-filter{width:100%;}.source-filter button{flex:1;}}
@media(max-width:680px){.database-grid{grid-template-columns:1fr;}.section-heading>span{display:none;}.database-card{min-height:205px;}}
</style>
