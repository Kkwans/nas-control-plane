import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  discoverDatabases,
  loadDatabaseCatalog,
  type DatabaseCatalog,
  type DatabaseConnection,
  type DatabaseCredentials,
  type DatabaseSource,
} from '@/api/database'

export const useDatabaseStore = defineStore('database', () => {
  const sources = ref<DatabaseSource[]>([])
  const catalogs = ref<Record<string, DatabaseCatalog>>({})
  const credentials = ref<Record<string, DatabaseCredentials>>({})
  const loading = ref(false)
  const collectedAt = ref('')

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

  async function refreshDiscovery() {
    loading.value = true
    try {
      const result = await discoverDatabases()
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
    source,
    connection,
    refreshDiscovery,
    loadCatalog,
    connect,
  }
})
