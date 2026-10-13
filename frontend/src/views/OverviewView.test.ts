// @vitest-environment jsdom

import { createApp, h, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Site } from '@/api/control'
import { NcpApiError } from '@/api/system'
import { invalidateSession } from '@/session/sessionLifecycle'
import { useSitesStore } from '@/stores/sites'
import OverviewView from './OverviewView.vue'

const mocks = vi.hoisted(() => ({ requestSites: vi.fn(), requestIgnoredSites: vi.fn() }))
vi.mock('@/api/control', async (original) => ({ ...await original<object>(), ...mocks }))
vi.mock('vue-router', () => ({ RouterLink: { render: () => null } }))
vi.mock('@/components/RealtimeTrendChart.vue', () => ({ default: { render: () => null } }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ summary: null, inventory: null, connectionState: 'loading', connectionLabel: '等待实时数据', connectionDetail: '正在等待实时数据。', services: [], resourceHistory: [], lastUpdated: null, preferences: { linkOpenMode: 'new-tab', siteDefaultProtocol: 'http' } }) }))

const collectedAt = '2026-10-13T08:00:00Z'
const site: Site = {
  id: 'media', projectId: 'media', name: '媒体库', description: '家庭媒体', iconUrl: '', category: '媒体',
  state: 'running', primaryPort: 8096, ports: [8096], launchUrl: 'http://nas:8096', favorite: true,
  sortOrder: 1, lastVisitedAt: null, hidden: false, source: 'auto',
}
function result(sites: Site[] = []) {
  return { sites, collectedAt, discovery: { status: 'complete', probeAvailable: true, candidateCount: sites.length, verifiedCount: sites.length, failedCount: 0, issues: [] } }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
async function settle() { for (let i = 0; i < 12; i++) await nextTick() }
let app: App | undefined
let host: HTMLDivElement
function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(OverviewView) })
  app.mount(host)
}
function launcher() { return host.querySelector('.favorite-launcher')! }

describe('Overview favorites resource states', () => {
  beforeEach(() => {
    invalidateSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
    mocks.requestIgnoredSites.mockResolvedValue([])
  })
  afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

  it('does not show an empty state before the first catalog response', async () => {
    const pending = deferred<ReturnType<typeof result>>()
    mocks.requestSites.mockReturnValue(pending.promise)
    mount()
    expect(launcher().textContent).toContain('正在加载收藏站点')
    expect(launcher().textContent).not.toContain('还没有收藏站点')
    expect(launcher().querySelector('[aria-busy="true"]')).not.toBeNull()
    pending.resolve(result())
    await settle()
    expect(launcher().textContent).toContain('还没有收藏站点')
    expect(launcher().querySelector('[aria-busy="true"]')).toBeNull()
  })

  it('shows an initial error and retries once even with repeated clicks', async () => {
    mocks.requestSites.mockRejectedValueOnce(new NcpApiError('unavailable', '站点服务不可用'))
    mount()
    await settle()
    expect(launcher().textContent).toContain('收藏站点加载失败')
    expect(launcher().textContent).not.toContain('还没有收藏站点')
    const pending = deferred<ReturnType<typeof result>>()
    mocks.requestSites.mockReturnValue(pending.promise)
    const retry = launcher().querySelector('button')!
    retry.click()
    retry.click()
    await settle()
    expect(mocks.requestSites).toHaveBeenCalledTimes(2)
    expect(launcher().textContent).toContain('正在加载收藏站点')
    pending.resolve(result([site]))
    await settle()
    expect(launcher().querySelector<HTMLAnchorElement>('.favorite-site')?.href).toBe('http://nas:8096/')
    expect(launcher().textContent).not.toContain('失败')
  })

  it('preserves cached links and labels their state as historical through refresh and failure', async () => {
    const store = useSitesStore()
    store.sites = [site]
    store.collectedAt = collectedAt
    const pending = deferred<ReturnType<typeof result>>()
    mocks.requestSites.mockReturnValue(pending.promise)
    mount()
    await settle()
    expect(launcher().querySelector('.favorite-site')).not.toBeNull()
    expect(launcher().textContent).toContain('正在更新收藏站点')
    expect(launcher().textContent).toContain('上次运行中')
    pending.reject(new NcpApiError('unavailable', '站点服务不可用'))
    await settle()
    expect(launcher().textContent).toContain('收藏站点刷新失败')
    expect(launcher().textContent).toContain('上次同步：')
    expect(launcher().textContent).toContain('上次运行中')
    expect(launcher().querySelector('[aria-busy="true"]')).toBeNull()
    mocks.requestSites.mockResolvedValue(result([{ ...site, state: 'stopped' }]))
    launcher().querySelector('button')!.click()
    await settle()
    expect(launcher().textContent).toContain('未运行')
    expect(launcher().textContent).not.toContain('上次')
    expect(launcher().textContent).not.toContain('失败')
  })

  it('does not present cached empty data as a confirmed empty state during refresh failure', async () => {
    useSitesStore().collectedAt = collectedAt
    const pending = deferred<ReturnType<typeof result>>()
    mocks.requestSites.mockReturnValue(pending.promise)
    mount()
    await settle()
    expect(launcher().textContent).toContain('正在更新收藏站点')
    expect(launcher().textContent).not.toContain('还没有收藏站点')
    pending.reject(new Error('offline'))
    await settle()
    expect(launcher().textContent).toContain('收藏站点刷新失败')
    expect(launcher().textContent).not.toContain('还没有收藏站点')
  })

  it('shows a true empty state when the successful catalog contains no visible favorites', async () => {
    mocks.requestSites.mockResolvedValue(result([{ ...site, favorite: false }, { ...site, id: 'hidden', hidden: true }]))
    mount()
    await settle()
    expect(launcher().textContent).toContain('还没有收藏站点')
    expect(launcher().querySelector('.favorite-site')).toBeNull()
  })

  it('joins a store refresh already in progress instead of replacing it', async () => {
    const pending = deferred<ReturnType<typeof result>>()
    mocks.requestSites.mockReturnValue(pending.promise)
    const refresh = useSitesStore().refresh()
    mount()
    expect(mocks.requestSites).toHaveBeenCalledTimes(1)
    pending.resolve(result([site]))
    await refresh
    await settle()
    expect(launcher().textContent).toContain('媒体库')
  })
})
