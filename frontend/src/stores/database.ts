import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import { requestDatabaseProjectPreferences, updateDatabaseProjectPreference } from '@/api/control'
import {
  connectDatabase,
  discoverDatabases,
  loadDatabaseCatalog,
  type DatabaseCatalog,
  type DatabaseConnection,
  type DatabaseCredentials,
  type DatabaseSource,
} from '@/api/database'
import { captureSession, isAbortError, isCurrentSession, type SessionContext } from '@/session/sessionLifecycle'

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
  let discoverySequence = 0

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

  async function loadProjectPreferences(existingSession?: SessionContext) {
    const session = existingSession ?? captureSession()
    const preferences = await requestDatabaseProjectPreferences(session.signal)
    if (!isCurrentSession(session.generation)) return
    archivedProjectKeys.value = preferences.filter((item) => item.archived).map((item) => item.projectKey)
  }

  async function setProjectArchived(projectKey: string, archived: boolean) {
    const session = captureSession()
    const previous = archivedProjectKeys.value
    const keys = new Set(archivedProjectKeys.value)
    if (archived) keys.add(projectKey)
    else keys.delete(projectKey)
    archivedProjectKeys.value = [...keys]
    try {
      await updateDatabaseProjectPreference({ projectKey, archived }, session.signal)
    } catch (error) {
      if (isCurrentSession(session.generation)) archivedProjectKeys.value = previous
      if (isAbortError(error) || !isCurrentSession(session.generation)) return
      throw error
    }
  }

  async function refreshDiscovery(force = false) {
    const sequence = ++discoverySequence
    const session = captureSession()
    loading.value = true
    try {
      const [result] = await Promise.all([discoverDatabases(force, session.signal), loadProjectPreferences(session)])
      if (sequence !== discoverySequence || !isCurrentSession(session.generation)) return
      sources.value = result.sources
      collectedAt.value = result.collectedAt
    } catch (error) {
      if (!isAbortError(error) && sequence === discoverySequence && isCurrentSession(session.generation)) throw error
    } finally {
      if (sequence === discoverySequence && isCurrentSession(session.generation)) loading.value = false
    }
  }

  async function loadCatalog(sourceId: string, signal?: AbortSignal) {
    const session = captureSession()
    const requestSignal = signal ?? session.signal
    const catalog = await loadDatabaseCatalog(connection(sourceId), requestSignal)
    if (requestSignal.aborted || !isCurrentSession(session.generation))
      throw new DOMException('Session invalidated', 'AbortError')
    catalogs.value = { ...catalogs.value, [sourceId]: catalog }
    return catalog
  }

  async function connect(sourceId: string, input: DatabaseCredentials) {
    const session = captureSession()
    credentials.value = { ...credentials.value, [sourceId]: { ...input } }
    try {
      const hasCredentials = Object.values(input).some((value) => typeof value === 'string' && value.trim() !== '')
      const diagnostic = await connectDatabase(
        hasCredentials ? { sourceId, credentials: input } : { sourceId },
        session.signal,
      )
      if (!isCurrentSession(session.generation)) throw new DOMException('Session invalidated', 'AbortError')
      if (!diagnostic.connected) {
        throw new Error(diagnostic.code || 'DATABASE_CONNECTION_FAILED')
      }
      const catalog = await loadCatalog(sourceId)
      return { catalog, diagnostic }
    } catch (error) {
      if (isCurrentSession(session.generation)) {
        const next = { ...credentials.value }
        delete next[sourceId]
        credentials.value = next
      }
      throw error
    }
  }

  function resetSessionState() {
    discoverySequence += 1
    sources.value = []
    catalogs.value = {}
    credentials.value = {}
    loading.value = false
    collectedAt.value = ''
    archivedProjectKeys.value = []
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
    resetSessionState,
  }
})
