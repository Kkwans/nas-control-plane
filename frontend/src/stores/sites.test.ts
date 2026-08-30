import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { invalidateSession } from '@/session/sessionLifecycle'

import { useSitesStore } from './sites'

const apiMocks = vi.hoisted(() => ({
  requestSites: vi.fn(),
  requestIgnoredSites: vi.fn(),
  updateSite: vi.fn(),
  createSite: vi.fn(),
  deleteSite: vi.fn(),
  restoreSite: vi.fn(),
  uploadSiteIcon: vi.fn(),
  deleteSiteIcon: vi.fn(),
  recordSiteVisit: vi.fn(),
}))

vi.mock('@/api/control', () => ({
  requestSites: apiMocks.requestSites,
  requestIgnoredSites: apiMocks.requestIgnoredSites,
  updateSite: apiMocks.updateSite,
  createSite: apiMocks.createSite,
  deleteSite: apiMocks.deleteSite,
  restoreSite: apiMocks.restoreSite,
  uploadSiteIcon: apiMocks.uploadSiteIcon,
  deleteSiteIcon: apiMocks.deleteSiteIcon,
  recordSiteVisit: apiMocks.recordSiteVisit,
}))

vi.mock('@/api/system', () => ({
  NcpApiError: class NcpApiError extends Error {
    code = 'TEST_ERROR'
  },
}))

const input = {
  name: 'Demo',
  description: '',
  iconUrl: '',
  category: 'web',
  primaryPort: 8080,
  launchUrl: '',
  favorite: false,
  sortOrder: 0,
  hidden: false,
}

describe('useSitesStore session isolation', () => {
  beforeEach(() => {
    invalidateSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('does not refresh the site list after logout during a save', async () => {
    let resolveUpdate: (() => void) | undefined
    apiMocks.updateSite.mockReturnValueOnce(
      new Promise<void>((resolve) => {
        resolveUpdate = resolve
      }),
    )

    const store = useSitesStore()
    const operation = store.save('manual:demo', input)

    invalidateSession()
    store.resetSessionState()
    resolveUpdate?.()
    await operation

    expect(apiMocks.updateSite).toHaveBeenCalledWith('manual:demo', input, expect.any(AbortSignal))
    expect(apiMocks.requestSites).not.toHaveBeenCalled()
    expect(store.sites).toEqual([])
  })
})
