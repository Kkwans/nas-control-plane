import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  requestDatabaseProjectPreferences,
  updateDatabaseProjectPreference,
} from '@/api/control'
import {
  discoverDatabases,
  loadDatabaseCatalog,
  type DatabaseCatalog,
  type DatabaseConnection,
  type DatabaseCredentials,
  type DatabaseSource,
} from '@/api/database'

export function databaseProjectKey(source: DatabaseSource) {
  const project = source.project?.trim() || source.module?.trim() || '未关联项目'
  return `${source.category}:${project}`
}

export const useDatabaseStore = defineStore('database', () => {
  const sources = ref<DatabaseSource[]>([])
  const catalogs = ref<Record<string, DatabaseCatalog>>({})
  const credentials = ref<Record<string, DatabaseCredentials>>({})
  const loading = ref(false)
  const collectedAt = ref('')
  const archivedProjectKeys = ref<string[]>([])

  const systemCount = computed(() => sources.value.filter((source) => source.category === 'system').length)
  const projectCount = computed(() => sources.value.filter((source) => source.category === 'project').length)
  const connectedCount = computed(() => sources.value.filter((source) => Boolean(catalogs.value[source.id])).length)

  function source(sourceId: string) {
    return sources.value.find((item) => item.id === sourceId) ?? null
  }

  function connection(sourceId: string): DatabaseConnection {
    return {
      sourceId,
      credentials: credentials.value[sourceId],
    }
  }

  function isProjectArchived(projectKey: string) {
    return archivedProjectKeys.value.includes(projectKey)
  }

  async function loadProjectPreferences() {
    const preferences = await requestDatabaseProjectPreferences()
    archivedProjectKeys.value = preferences.filter((item) => item.archived).map((item) => item.projectKey)
  }

  async function setProjectArchived(projectKey: string, archived: boolean) {
    const previous = archivedProjectKeys.value
    const keys = new Set(archivedProjectKeys.value)
    if (archived) keys.add(projectKey)
    else keys.delete(projectKey)
    archivedProjectKeys.value = [...keys]
    try {
      await updateDatabaseProjectPreference({ projectKey, archived })
    } catch (error) {
      archivedProjectKeys.value = previous
      throw error
    }
  }

  async function refreshDiscovery() {
    loading.value = true
    try {
      const [result] = await Promise.all([
        discoverDatabases(),
        loadProjectPreferences(),
      ])
      sources.value = result.sources
      collectedAt.value = result.collectedAt
    } finally {
      loading.value = false
    }
  }

  async function loadCatalog(sourceId: string) {
    const catalog = await loadDatabaseCatalog(connection(sourceId))
    catalogs.value = { ...catalogs.value, [sourceId]: catalog }
    return catalog
  }

  async function connect(sourceId: string, input: DatabaseCredentials) {
    credentials.value = { ...credentials.value, [sourceId]: { ...input } }
    try {
      return await loadCatalog(sourceId)
    } catch (error) {
      const next = { ...credentials.value }
      delete next[sourceId]
      credentials.value = next
      throw error
    }
  }

  return {
    sources,
    catalogs,
    credentials,
    loading,
    collectedAt,
    systemCount,
    projectCount,
    connectedCount,
    archivedProjectKeys,
    source,
    connection,
    isProjectArchived,
    loadProjectPreferences,
    setProjectArchived,
    refreshDiscovery,
    loadCatalog,
    connect,
  }
})
