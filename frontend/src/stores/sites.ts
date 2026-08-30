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
import { NcpApiError } from '@/api/system'
import { captureSession, isAbortError, isCurrentSession } from '@/session/sessionLifecycle'

export class SiteSyncError extends Error {
  readonly code = 'SITES_SYNC_FAILED'
  constructor() {
    super('站点操作已完成，但列表同步失败。')
    this.name = 'SiteSyncError'
  }
}

export const useSitesStore = defineStore('sites', () => {
  const sites = ref<Site[]>([])
  const loading = ref(false)
  const collectedAt = ref('')
  const error = ref<string | null>(null)
  const discovery = ref<SiteDiscoverySummary>({
    status: 'unavailable', probeAvailable: false, candidateCount: 0,
    verifiedCount: 0, failedCount: 0, issues: [],
  })
  const ignoredSites = ref<Array<SiteProfileInput & { projectId: string }>>([])
  let refreshSequence = 0

  const visibleSites = computed(() => sites.value.filter((site) => !site.hidden))

  async function refresh(): Promise<boolean> {
    const sequence = ++refreshSequence
    const session = captureSession()
    loading.value = true
    error.value = null
    try {
      const [result, ignored] = await Promise.all([requestSites(session.signal), requestIgnoredSites(session.signal)])
      if (sequence !== refreshSequence || !isCurrentSession(session.generation)) return false
      sites.value = result.sites
      discovery.value = result.discovery
      ignoredSites.value = ignored
      collectedAt.value = result.collectedAt
    } catch (caught) {
      if (isAbortError(caught) || sequence !== refreshSequence || !isCurrentSession(session.generation)) return false
      error.value = caught instanceof NcpApiError
        ? caught.message
        : '无法读取站点目录，请检查 NCP Server 与 Root Agent 的连接。'
      return false
    } finally {
      if (sequence === refreshSequence && isCurrentSession(session.generation)) loading.value = false
    }
    return true
  }

  async function save(siteId: string, input: SiteProfileInput) {
    await updateSite(siteId, input)
    await refresh()
  }

  async function create(input: SiteProfileInput, icon?: File | null) {
    const result = await createSite(input)
    try {
      if (icon) await uploadSiteIcon(result.projectId, icon)
    } catch (error) {
      // Creating a site and uploading its icon are exposed as two HTTP calls.
      // Roll the first call back so a failed icon upload cannot leave a
      // successfully-created record that the user then submits again.
      await deleteSite(result.projectId).catch(() => undefined)
      await refresh()
      throw error
    }
    await refresh()
    return result
  }

  async function remove(siteId: string) {
    const session = captureSession()
    await deleteSite(siteId)
    if (isCurrentSession(session.generation) && !(await refresh())) throw new SiteSyncError()
  }

  async function restore(siteId: string) {
    await restoreSite(siteId)
    await refresh()
  }

  async function uploadIcon(siteId: string, icon: File) {
    const result = await uploadSiteIcon(siteId, icon)
    await refresh()
    return result
  }

  async function removeIcon(siteId: string) {
    await deleteSiteIcon(siteId)
    await refresh()
  }

  async function visit(siteId: string) {
    const session = captureSession()
    const result = await recordSiteVisit(siteId)
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
      status: 'unavailable', probeAvailable: false, candidateCount: 0,
      verifiedCount: 0, failedCount: 0, issues: [],
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
