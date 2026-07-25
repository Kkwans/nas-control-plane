import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  requestSites,
  recordSiteVisit,
  updateSite,
  type Site,
  type SiteProfileInput,
} from '@/api/control'

export const useSitesStore = defineStore('sites', () => {
  const sites = ref<Site[]>([])
  const loading = ref(false)
  const collectedAt = ref('')
  const error = ref<string | null>(null)

  const visibleSites = computed(() => sites.value.filter((site) => !site.hidden))

  async function refresh() {
    loading.value = true
    error.value = null
    try {
      const result = await requestSites()
      sites.value = result.sites
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

  async function visit(projectId: string) {
    const result = await recordSiteVisit(projectId)
    const site = sites.value.find((item) => item.projectId === projectId)
    if (site) site.lastVisitedAt = result.lastVisitedAt
  }

  return {
    sites,
    visibleSites,
    loading,
    collectedAt,
    error,
    refresh,
    save,
    visit,
  }
})
