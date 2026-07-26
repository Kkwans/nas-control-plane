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
  type SiteProfileInput,
} from '@/api/control'

export const useSitesStore = defineStore('sites', () => {
  const sites = ref<Site[]>([])
  const loading = ref(false)
  const collectedAt = ref('')
  const error = ref<string | null>(null)
  const ignoredSites = ref<Array<SiteProfileInput & { projectId: string }>>([])

  const visibleSites = computed(() => sites.value.filter((site) => !site.hidden))

  async function refresh() {
    loading.value = true
    error.value = null
    try {
      const [result, ignored] = await Promise.all([requestSites(), requestIgnoredSites()])
      sites.value = result.sites
      ignoredSites.value = ignored
      collectedAt.value = result.collectedAt
    } catch {
      error.value = '站点识别失败，请确认 Root Agent 与 Docker Engine 正常运行。'
    } finally {
      loading.value = false
    }
  }

  async function save(projectId: string, input: SiteProfileInput) {
    await updateSite(projectId, input)
    await refresh()
  }

  async function create(input: SiteProfileInput, icon?: File | null) {
    const result = await createSite(input)
    if (icon) await uploadSiteIcon(result.projectId, icon)
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

  async function visit(projectId: string) {
    const result = await recordSiteVisit(projectId)
    const site = sites.value.find((item) => item.projectId === projectId)
    if (site) site.lastVisitedAt = result.lastVisitedAt
  }

  return {
    sites,
    visibleSites,
    ignoredSites,
    loading,
    collectedAt,
    error,
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
