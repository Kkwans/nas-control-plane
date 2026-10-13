// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import type { DockerInventory, SystemSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { useSystemStore } from './system'

const collectedAt = '2026-10-13T00:00:00.000Z'
const capabilities = { architecture: 'amd64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2 }
const summary: SystemSummary = {
  collectedAt,
  host: { hostname: 'test', operatingSystem: 'Linux', kernelVersion: 'test', architecture: 'amd64', uptimeSeconds: 1, processCount: 1 },
  cpu: { usagePercent: 10, logicalCores: 4, load1: 1, load5: 1, load15: 1 },
  memory: { totalBytes: 100, usedBytes: 40, availableBytes: 60 },
  storage: [{ mountpoint: '/', totalBytes: 100, usedBytes: 20, freeBytes: 80 }],
  network: [{ name: 'eth0', receiveBytes: 100, transmitBytes: 50 }], warnings: [],
}
function inventory(offset = 0): DockerInventory {
  return {
    collectedAt: new Date(Date.parse(collectedAt) + offset).toISOString(),
    projects: [], containers: [],
    engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'amd64', containers: 0, containersRunning: 0, containersStopped: 0, images: 0 },
  }
}
function response(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } }) }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
class FakeEventSource extends EventTarget {
  static sources: FakeEventSource[] = []
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn()
  constructor(readonly url: string) { super(); FakeEventSource.sources.push(this) }
  snapshot(value: unknown) { this.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify(value) })) }
}
let fetcher: ReturnType<typeof vi.fn<typeof fetch>>
let store: ReturnType<typeof useSystemStore>
function source() { return FakeEventSource.sources.at(-1)! }
function push(docker: unknown, nextSummary?: SystemSummary) {
  source().snapshot({ collectedAt: new Date().toISOString(), docker, ...(nextSummary ? { summary: nextSummary } : {}) })
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(collectedAt))
  beginSession()
  setActivePinia(createPinia())
  FakeEventSource.sources = []
  vi.stubGlobal('EventSource', FakeEventSource)
  fetcher = vi.fn<typeof fetch>()
  vi.stubGlobal('fetch', fetcher)
  store = useSystemStore()
})
afterEach(() => { store.clear(); store.$dispose(); invalidateSession(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('Docker 清单独立加载与恢复状态', () => {
  it('能力先加载后，首次坏清单结束等待；summary 和 capabilities 成功不掩盖失败，只读重试可恢复为空清单', async () => {
    fetcher.mockResolvedValueOnce(response(capabilities))
    await store.refresh({ capabilities: true })
    const pending = deferred<Response>()
    fetcher.mockReturnValueOnce(pending.promise)
    const first = store.refresh({ inventory: true })
    expect(store.inventoryRefreshing).toBe(true)
    expect(store.inventoryErrorCode).toBeNull()
    pending.resolve(response({ ...inventory(), containers: [null] }))
    expect((await first).failed).toEqual([{ resource: 'inventory', error: expect.objectContaining({ code: 'DOCKER_INVENTORY_RESPONSE_INVALID' }) }])
    expect(store.inventory).toBeNull()
    expect(store.inventoryRefreshing).toBe(false)
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    expect(store.connectionState).toBe('degraded')

    fetcher.mockResolvedValueOnce(response(summary)).mockResolvedValueOnce(response(capabilities))
    await store.refresh({ summary: true, capabilities: true })
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    const retry = deferred<Response>()
    fetcher.mockReturnValueOnce(retry.promise)
    const refreshing = store.refresh({ inventory: true })
    expect(store.inventoryRefreshing).toBe(true)
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    retry.resolve(response(inventory()))
    await refreshing
    expect(store.inventory).toEqual(inventory())
    expect(store.inventoryRefreshing).toBe(false)
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.inventoryStale).toBe(false)
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/system/capabilities', '/api/v1/docker/inventory', '/api/v1/system/summary', '/api/v1/system/capabilities', '/api/v1/docker/inventory',
    ])
    expect(fetcher.mock.calls.every(([, options]) => options?.method === undefined || options.method === 'GET')).toBe(true)
  })

  it('坏 HTTP 清单保留缓存和源时间；后续同一条 SSE 的新清单清除资源错误', async () => {
    fetcher.mockResolvedValueOnce(response(inventory())).mockResolvedValueOnce(response({ ...inventory(5000), projects: [null] }))
    await store.refresh({ inventory: true })
    const previous = store.inventory
    await store.refresh({ inventory: true })
    expect(store.inventory).toBe(previous)
    expect(store.inventory?.collectedAt).toBe(collectedAt)
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    store.startRealtime(['docker'])
    push(inventory(1000))
    expect(store.inventory).toEqual(inventory(1000))
    expect(store.inventoryErrorCode).toBeNull()
    expect(FakeEventSource.sources).toHaveLength(1)
    expect(source().close).not.toHaveBeenCalled()
  })

  it('坏 SSE Docker 保留有效 summary 和旧清单，不推进清单新鲜度，也不关闭共享源', () => {
    store.startRealtime(['summary', 'docker'])
    push(inventory(), summary)
    const previous = store.inventory
    push({ ...inventory(5000), containers: [null] }, { ...summary, collectedAt: inventory(5000).collectedAt })
    expect(store.inventory).toBe(previous)
    expect(store.summary?.collectedAt).toBe(inventory(5000).collectedAt)
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    expect(store.realtimeState).toBe('streaming')
    expect(source().close).not.toHaveBeenCalled()
    // 比坏帧更早，但比已接受清单更新，必须仍能恢复。
    push(inventory(1000), summary)
    expect(store.inventory).toEqual(inventory(1000))
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.summary?.collectedAt).toBe(inventory(5000).collectedAt)
  })

  it('首帧坏清单没有可展示缓存，后续有效空清单在同一源恢复', () => {
    store.startRealtime(['docker'])
    push({ ...inventory(5000), engine: null })
    expect(store.inventory).toBeNull()
    expect(store.inventoryRefreshing).toBe(false)
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    push(inventory())
    expect(store.inventory).toEqual(inventory())
    expect(store.inventoryErrorCode).toBeNull()
    expect(source().close).not.toHaveBeenCalled()
  })

  it('重复、逆序、已过期与未来无效清单不清除错误，只有接受的新鲜清单可恢复', () => {
    store.startRealtime(['docker'])
    push(inventory())
    push({ ...inventory(5000), projects: [null] })
    push(inventory())
    push(inventory(-1000))
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    expect(store.inventory?.collectedAt).toBe(collectedAt)
    vi.advanceTimersByTime(20_000)
    push(inventory(1000))
    expect(store.inventory?.collectedAt).toBe(inventory(1000).collectedAt)
    expect(store.inventoryStale).toBe(true)
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    push(inventory(60_000))
    expect(store.inventory?.collectedAt).toBe(inventory(1000).collectedAt)
    push(inventory(20_000))
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.inventoryStale).toBe(false)
  })

  it('普通读取复用同资源请求，操作强刷保留写入屏障与自身 pending 状态', async () => {
    const before = deferred<Response>()
    const after = deferred<Response>()
    fetcher.mockReturnValueOnce(before.promise).mockReturnValueOnce(after.promise)
    const first = store.refresh({ inventory: true })
    const coalesced = store.refresh({ inventory: true })
    expect(fetcher).toHaveBeenCalledTimes(1)
    const forced = store.refresh({ inventory: true, force: true })
    before.resolve(response({ ...inventory(), projects: [null] }))
    await Promise.all([first, coalesced])
    expect(store.inventoryRefreshing).toBe(true)
    expect(store.inventoryErrorCode).toBeNull()
    after.resolve(response(inventory(1000)))
    await forced
    expect(store.inventoryRefreshing).toBe(false)
    expect(store.inventory).toEqual(inventory(1000))
  })

  it('summary 仍在读取时，清单结束后不会继续显示 inventory busy', async () => {
    const pending = deferred<Response>()
    fetcher.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(response(inventory()))
    const summaryRefresh = store.refresh({ summary: true })
    await store.refresh({ inventory: true })
    expect(store.isRefreshing).toBe(true)
    expect(store.inventoryRefreshing).toBe(false)
    pending.resolve(response(summary))
    await summaryRefresh
  })

  it('GET-only 页面只用共享本地时钟让快照过期，不增加请求或事件源', async () => {
    store.startRealtime([])
    fetcher.mockResolvedValueOnce(response(inventory()))
    await store.refresh({ inventory: true })
    expect(store.inventoryStale).toBe(false)
    expect(store.realtimeState).toBe('offline')
    expect(vi.getTimerCount()).toBe(1)
    store.startRealtime([])
    expect(vi.getTimerCount()).toBe(1)
    vi.advanceTimersByTime(16_000)
    expect(store.inventoryStale).toBe(true)
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.inventory?.collectedAt).toBe(collectedAt)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(FakeEventSource.sources).toHaveLength(0)
  })

  it('GET-only 页面后台恢复可见时立即重新核算年龄，并随停止或会话失效回收时钟', async () => {
    store.startRealtime([])
    fetcher.mockResolvedValueOnce(response(inventory()))
    await store.refresh({ inventory: true })
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    vi.setSystemTime(Date.now() + 16_000)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(store.inventoryStale).toBe(false)
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(store.inventoryStale).toBe(true)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(FakeEventSource.sources).toHaveLength(0)
    store.stopRealtime()
    expect(vi.getTimerCount()).toBe(0)
    store.startRealtime([])
    expect(vi.getTimerCount()).toBe(1)
    invalidateSession()
    expect(vi.getTimerCount()).toBe(0)
    beginSession()
    store.startRealtime([])
    store.clear()
    expect(vi.getTimerCount()).toBe(0)
    store.startRealtime([])
    store.$dispose()
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['clear', 'logout'])('%s 清空资源状态，旧请求和源回调不能污染新会话或释放新请求', async (action) => {
    store.startRealtime(['docker'])
    const previousSource = source()
    push({ ...inventory(), containers: [null] })
    const old = deferred<Response>()
    const current = deferred<Response>()
    fetcher.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const oldRefresh = store.refresh({ inventory: true })
    const signal = fetcher.mock.calls[0]![1]!.signal!
    if (action === 'logout') invalidateSession()
    store.clear()
    if (action === 'logout') { expect(signal.aborted).toBe(true); beginSession() }
    expect(store.inventoryRefreshing).toBe(false)
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.inventoryStale).toBe(false)
    const newRefresh = store.refresh({ inventory: true })
    previousSource.snapshot({ collectedAt, docker: inventory() })
    old.resolve(response({ ...inventory(), containers: [null] }))
    await oldRefresh
    expect(store.inventory).toBeNull()
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.inventoryRefreshing).toBe(true)
    current.resolve(response(inventory(1000)))
    await newRefresh
    expect(store.inventory).toEqual(inventory(1000))
    expect(store.inventoryRefreshing).toBe(false)
  })

  it('保留原共享源销毁边界，旧回调不重新引入错误或启动新连接', () => {
    store.startRealtime(['docker'])
    push(inventory())
    const previousSource = source()
    store.$dispose()
    expect(previousSource.close).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
    previousSource.snapshot({ collectedAt, docker: { ...inventory(1000), projects: [null] } })
    expect(store.inventoryErrorCode).toBeNull()
    expect(store.inventory?.collectedAt).toBe(collectedAt)
    expect(FakeEventSource.sources).toHaveLength(1)
  })
})
