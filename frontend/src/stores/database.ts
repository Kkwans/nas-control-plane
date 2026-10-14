import { computed, ref, watch } from 'vue'
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
import { cloneDatabaseCredentials, databaseSourceBinding } from '@/domain/database/connectionDraft'
import { NcpApiError } from '@/api/systemTransport'
import { DatabaseConnectionCatalogError } from '@/api/databaseConnectionResponse'
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
  const connectionSequences = new Map<string, number>()
  watch(() => new Map(sources.value.map(item => [item.id, databaseSourceBinding(item)])), (next, previous) => {
    for (const [id, binding] of previous) {
      if (next.get(id) === binding) continue
      connectionSequences.set(id, (connectionSequences.get(id) ?? 0) + 1)
      delete credentials.value[id]
      delete catalogs.value[id]
    }
  }, { flush: 'sync' })

  function captureConnectionOwner(sourceId: string) {
    const sequence = (connectionSequences.get(sourceId) ?? 0) + 1
    connectionSequences.set(sourceId, sequence)
    const binding = databaseSourceBinding(source(sourceId))
    return () => connectionSequences.get(sourceId) === sequence && databaseSourceBinding(source(sourceId)) === binding
  }

  const systemCount = computed(() => sources.value.filter((source) => source.category === 'system').length)
  const projectCount = computed(() => sources.value.filter((source) => source.category === 'project').length)
  const connectedCount = computed(() => sources.value.filter((source) => Boolean(catalogs.value[source.id])).length)

  function source(sourceId: string) {
    return sources.value.find((item) => item.id === sourceId) ?? null
  }

  function connection(sourceId: string): DatabaseConnection {
    return {
      sourceId,
      credentials: credentials.value[sourceId] ? cloneDatabaseCredentials(credentials.value[sourceId]) : undefined,
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
    const requestSignal = signal ? AbortSignal.any([signal, session.signal]) : session.signal
    const ownsTarget = captureConnectionOwner(sourceId)
    const catalog = await loadDatabaseCatalog(connection(sourceId), requestSignal)
    if (requestSignal.aborted || !isCurrentSession(session.generation) || !ownsTarget())
      throw new DOMException('Session invalidated', 'AbortError')
    catalogs.value = { ...catalogs.value, [sourceId]: catalog }
    return catalog
  }

  async function connect(sourceId: string, input: DatabaseCredentials, signal?: AbortSignal) {
    const session = captureSession()
    const requestSignal = signal ? AbortSignal.any([signal, session.signal]) : session.signal
    const ownsTarget = captureConnectionOwner(sourceId)
    const snapshot = cloneDatabaseCredentials(input)
    if (snapshot.tls && (!snapshot.username?.trim() || (!snapshot.database?.trim() && !source(sourceId)?.defaultDatabase?.trim()))) {
      throw new NcpApiError('credentials_required', '请填写本次连接的用户名和数据库名。')
    }
    const hasCredentials = snapshot.tls !== undefined || Object.values(snapshot).some((value) => typeof value === 'string' && value.trim() !== '')
    const connectionInput = hasCredentials ? { sourceId, credentials: snapshot } : { sourceId }
    const diagnostic = await connectDatabase({ ...connectionInput, ...(hasCredentials ? { credentials: cloneDatabaseCredentials(snapshot) } : {}) }, requestSignal)
    if (requestSignal.aborted || !isCurrentSession(session.generation) || !ownsTarget())
      throw new DOMException('Session invalidated', 'AbortError')
    if (diagnostic.connected !== true) throw new Error(diagnostic.code || 'DATABASE_CONNECTION_FAILED')
    let catalog: DatabaseCatalog
    try { catalog = await loadDatabaseCatalog(connectionInput, requestSignal) } catch (error) {
      if (requestSignal.aborted || !isCurrentSession(session.generation) || !ownsTarget() || isAbortError(error))
        throw new DOMException('Session invalidated', 'AbortError')
      throw new DatabaseConnectionCatalogError()
    }
    if (requestSignal.aborted || !isCurrentSession(session.generation) || !ownsTarget())
      throw new DOMException('Session invalidated', 'AbortError')
    credentials.value = { ...credentials.value, [sourceId]: cloneDatabaseCredentials(snapshot) }
    catalogs.value = { ...catalogs.value, [sourceId]: catalog }
    return { catalog, diagnostic }
  }

  function resetSessionState() {
    discoverySequence += 1
    connectionSequences.clear()
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
