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

  const visibleSites = computed(() => sites.value.filter((site) => !site.hidden))

  async function refresh() {
    loading.value = true
    error.value = null
    try {
      const [result, ignored] = await Promise.all([requestSites(), requestIgnoredSites()])
      sites.value = result.sites
      discovery.value = result.discovery
      ignoredSites.value = ignored
      collectedAt.value = result.collectedAt
    } catch {
      error.value = '站点识别失败，请确认 Root Agent 与 Docker Engine 正常运行。'
    } finally {
      loading.value = false
    }
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
    await deleteSite(siteId)
    sites.value = sites.value.filter((site) => site.id !== siteId)
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
    const result = await recordSiteVisit(siteId)
    const site = sites.value.find((item) => item.id === siteId)
    if (site) site.lastVisitedAt = result.lastVisitedAt
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
  }
})
