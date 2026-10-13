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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

const list = (name = 'Demo') => ({ sites: [{ id: 'manual:demo', ...input, name }], discovery: { status: 'complete', issues: [] }, collectedAt: name })

describe('站点写入后的列表同步', () => {
  beforeEach(() => {
    invalidateSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
    apiMocks.requestIgnoredSites.mockResolvedValue([])
    apiMocks.requestSites.mockResolvedValue(list())
    apiMocks.createSite.mockResolvedValue({ ...input, projectId: 'manual:demo' })
  })

  it.each(['save', 'create', 'remove', 'restore', 'uploadIcon', 'removeIcon'] as const)('%s 写入成功、延迟读取失败明确返回同步错误且不重放写入', async (action) => {
    const store = useSitesStore()
    await store.refresh()
    const read = deferred<never>()
    apiMocks.requestSites.mockReturnValueOnce(read.promise)
    const file = new File(['icon'], 'icon.png', { type: 'image/png' })
    const operation = action === 'save' ? store.save('manual:demo', input)
      : action === 'create' ? store.create(input)
        : action === 'uploadIcon' ? store.uploadIcon('manual:demo', file)
          : store[action]('manual:demo')
    const result = expect(operation).rejects.toMatchObject({ code: 'SITES_SYNC_FAILED', siteId: 'manual:demo' })
    await Promise.resolve()
    read.reject(new Error('offline'))
    await result
    expect(store.sites[0]?.name).toBe('Demo')
    expect(store.error).toBeTruthy()
    expect(store.loading).toBe(false)
    await store.refresh()
    const mutation = { save: apiMocks.updateSite, create: apiMocks.createSite, remove: apiMocks.deleteSite, restore: apiMocks.restoreSite, uploadIcon: apiMocks.uploadSiteIcon, removeIcon: apiMocks.deleteSiteIcon }[action]
    expect(mutation).toHaveBeenCalledTimes(1)
    if (action === 'create') expect(apiMocks.deleteSite).not.toHaveBeenCalled()
  })

  it('提交失败保留原始错误，不发列表同步或重放', async () => {
    const failure = new Error('write failed')
    apiMocks.updateSite.mockRejectedValue(failure)
    await expect(useSitesStore().save('manual:demo', input)).rejects.toBe(failure)
    expect(apiMocks.requestSites).not.toHaveBeenCalled()
    expect(apiMocks.updateSite).toHaveBeenCalledTimes(1)
  })

  it('较新读取覆盖较旧写后读取，旧失败不污染列表和错误', async () => {
    const store = useSitesStore()
    const old = deferred<never>()
    apiMocks.requestSites.mockReturnValueOnce(old.promise)
    const write = store.save('manual:demo', input)
    const result = expect(write).resolves.toBeUndefined()
    await Promise.resolve()
    apiMocks.requestSites.mockResolvedValueOnce(list('New'))
    await store.refresh()
    old.reject(new Error('old'))
    await result
    expect(store.sites[0]?.name).toBe('New')
    expect(store.error).toBeNull()
    expect(store.loading).toBe(false)
  })

  it('写后刷新期间退出，不回写错误或抛出旧会话同步提示', async () => {
    const store = useSitesStore()
    const read = deferred<never>()
    apiMocks.requestSites.mockReturnValueOnce(read.promise)
    const operation = store.save('manual:demo', input)
    await Promise.resolve()
    invalidateSession()
    store.resetSessionState()
    read.reject(new Error('late failure'))
    await expect(operation).resolves.toBeUndefined()
    expect(store.sites).toEqual([])
    expect(store.error).toBeNull()
    expect(store.loading).toBe(false)
  })

  it('创建响应跨会话到达时不上传图标或刷新', async () => {
    const created = deferred<{ projectId: string }>()
    apiMocks.createSite.mockReturnValueOnce(created.promise)
    const operation = useSitesStore().create(input, new File(['icon'], 'icon.png'))
    invalidateSession()
    created.resolve({ projectId: 'manual:demo' })
    await operation
    expect(apiMocks.uploadSiteIcon).not.toHaveBeenCalled()
    expect(apiMocks.requestSites).not.toHaveBeenCalled()
  })
})
