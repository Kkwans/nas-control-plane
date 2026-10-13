import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  createSite,
  deleteSite,
  deleteSiteIcon,
  requestIgnoredSites,
  requestSites,
  recordSiteVisit,
  restoreSite,
  updateSite,
  uploadSiteIcon,
  type Site,
  type SiteDiscoverySummary,
  type SiteProfileInput,
} from '@/api/control'
import { SiteWriteUnknownError } from '@/api/siteWriteOutcome'
import { NcpApiError } from '@/api/system'
import { captureSession, isAbortError, isCurrentSession, type SessionContext } from '@/session/sessionLifecycle'

export class SiteSyncError extends Error {
  readonly code = 'SITES_SYNC_FAILED'
  constructor(readonly siteId?: string) {
    super('站点操作已完成，但列表尚未同步。')
    this.name = 'SiteSyncError'
  }
}

export const useSitesStore = defineStore('sites', () => {
  const sites = ref<Site[]>([])
  const loading = ref(false)
  const collectedAt = ref('')
  const error = ref<string | null>(null)
  const discovery = ref<SiteDiscoverySummary>({
    status: 'unavailable',
    probeAvailable: false,
    candidateCount: 0,
    verifiedCount: 0,
    failedCount: 0,
    issues: [],
  })
  const ignoredSites = ref<Array<SiteProfileInput & { projectId: string }>>([])
  let refreshSequence = 0
  let syncedSequence = 0

  const visibleSites = computed(() => sites.value.filter((site) => !site.hidden))

  async function refresh(existingSession?: SessionContext): Promise<boolean> {
    const sequence = ++refreshSequence
    const session = existingSession ?? captureSession()
    loading.value = true
    error.value = null
    try {
      const [result, ignored] = await Promise.all([requestSites(session.signal), requestIgnoredSites(session.signal)])
      if (sequence !== refreshSequence || !isCurrentSession(session.generation)) return false
      syncedSequence = sequence
      sites.value = result.sites
      discovery.value = result.discovery
      ignoredSites.value = ignored
      collectedAt.value = result.collectedAt
    } catch (caught) {
      if (isAbortError(caught) || sequence !== refreshSequence || !isCurrentSession(session.generation)) return false
      error.value =
        caught instanceof NcpApiError ? caught.message : '无法读取站点目录，请检查 NCP Server 与 Root Agent 的连接。'
      return false
    } finally {
      if (sequence === refreshSequence && isCurrentSession(session.generation)) loading.value = false
    }
    return true
  }

  async function syncAfterWrite(session: SessionContext, siteId: string) {
    if (!isCurrentSession(session.generation)) return
    const pending = refresh(session)
    const sequence = refreshSequence
    const synced = await pending
    if (!synced && syncedSequence < sequence && isCurrentSession(session.generation)) throw new SiteSyncError(siteId)
  }

  async function save(siteId: string, input: SiteProfileInput) {
    const session = captureSession()
    await updateSite(siteId, input, session.signal)
    await syncAfterWrite(session, siteId)
  }

  async function create(input: SiteProfileInput, icon?: File | null) {
    const session = captureSession()
    const result = await createSite(input, session.signal)
    try {
      if (icon && isCurrentSession(session.generation)) await uploadSiteIcon(result.projectId, icon, session.signal)
    } catch (error) {
      // 仅保留既有的一次补偿；失败可能是确认丢失，不能重试删除或创建。
      // 即使 DELETE 已确认，未完成的服务端图标请求仍可能迟到写入。
      if (isCurrentSession(session.generation)) {
        try {
          await deleteSite(result.projectId, session.signal)
        } catch {
          throw new SiteWriteUnknownError('icon-compensation', result.projectId)
        }
        if (isCurrentSession(session.generation)) await refresh(session)
      }
      throw error
    }
    await syncAfterWrite(session, result.projectId)
    return result
  }

  async function remove(siteId: string) {
    const session = captureSession()
    await deleteSite(siteId, session.signal)
    await syncAfterWrite(session, siteId)
  }

  async function restore(siteId: string) {
    const session = captureSession()
    await restoreSite(siteId, session.signal)
    await syncAfterWrite(session, siteId)
  }

  async function uploadIcon(siteId: string, icon: File) {
    const session = captureSession()
    const result = await uploadSiteIcon(siteId, icon, session.signal)
    await syncAfterWrite(session, siteId)
    return result
  }

  async function removeIcon(siteId: string) {
    const session = captureSession()
    await deleteSiteIcon(siteId, session.signal)
    await syncAfterWrite(session, siteId)
  }

  async function visit(siteId: string) {
    const session = captureSession()
    const result = await recordSiteVisit(siteId, session.signal)
    const site = sites.value.find((item) => item.id === siteId)
    if (site && isCurrentSession(session.generation)) site.lastVisitedAt = result.lastVisitedAt
  }

  function resetSessionState() {
    refreshSequence += 1
    sites.value = []
    loading.value = false
    collectedAt.value = ''
    error.value = null
    discovery.value = {
      status: 'unavailable',
      probeAvailable: false,
      candidateCount: 0,
      verifiedCount: 0,
      failedCount: 0,
      issues: [],
    }
    ignoredSites.value = []
  }

  return {
    sites,
    visibleSites,
    ignoredSites,
    loading,
    collectedAt,
    error,
    discovery,
    refresh,
    save,
    create,
    remove,
    restore,
    uploadIcon,
    removeIcon,
    visit,
    resetSessionState,
  }
})
