// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { DockerInventory, SystemSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { useSystemStore } from './system'

const collectedAt = '2026-10-13T00:00:00.000Z'
function summary(offset = 0): SystemSummary {
  return {
    collectedAt: new Date(Date.parse(collectedAt) + offset).toISOString(),
    host: { hostname: 'fixture', operatingSystem: 'Linux', kernelVersion: 'test', architecture: 'amd64', uptimeSeconds: 1, processCount: 1 },
    cpu: { usagePercent: 10, logicalCores: 4, load1: 1, load5: 1, load15: 1 },
    memory: { totalBytes: 100, usedBytes: 40, availableBytes: 60 },
    storage: [{ mountpoint: '/', totalBytes: 100, usedBytes: 20, freeBytes: 80 }],
    network: [{ name: 'eth0', receiveBytes: 100, transmitBytes: 50 }], warnings: [],
  }
}
function inventory(offset = 0): DockerInventory {
  return {
    collectedAt: summary(offset).collectedAt, projects: [], containers: [],
    engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'amd64', containers: 0, containersRunning: 0, containersStopped: 0, images: 0 },
  }
}
function snapshot(offset = 0) { return { collectedAt: summary(offset).collectedAt, summary: summary(offset) } }
function response(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } }) }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
class FakeEventSource extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  static sources: FakeEventSource[] = []
  readyState = FakeEventSource.CONNECTING
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn(() => { this.readyState = FakeEventSource.CLOSED })
  constructor(readonly url: string) { super(); FakeEventSource.sources.push(this) }
  open() { this.readyState = FakeEventSource.OPEN; this.onopen?.() }
  transientError() { this.readyState = FakeEventSource.CONNECTING; this.onerror?.() }
  raw(data: string) {
    if (this.readyState !== FakeEventSource.CLOSED) this.dispatchEvent(new MessageEvent('snapshot', { data }))
  }
  snapshot(value: unknown) { this.raw(JSON.stringify(value)) }
  queuedSnapshot(value: unknown) { this.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify(value) })) }
}
let fetcher: ReturnType<typeof vi.fn<typeof fetch>>
let store: ReturnType<typeof useSystemStore>
function source() { return FakeEventSource.sources.at(-1)! }
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date(collectedAt))
  beginSession()
  setActivePinia(createPinia())
  FakeEventSource.sources = []
  vi.stubGlobal('EventSource', FakeEventSource)
  fetcher = vi.fn<typeof fetch>()
  vi.stubGlobal('fetch', fetcher)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  store = useSystemStore()
})
afterEach(() => { store.clear(); store.$dispose(); invalidateSession(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('CLOSED 实时流只由显式刷新恢复', () => {
  it.each([
    ['坏 JSON', '{bad'],
    ['坏信封', JSON.stringify({ collectedAt, errors: [7] })],
    ['坏摘要', JSON.stringify({ collectedAt, summary: {} })],
  ])('%s 关闭流，GET/时钟/同作用域不重连；显式重试仅创建一个源且保留缓存', async (_name, invalid) => {
    store.startRealtime(['summary'])
    const old = source()
    old.open(); old.snapshot(snapshot())
    old.raw(invalid!)
    expect(old.readyState).toBe(FakeEventSource.CLOSED)
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    expect(store.summary).toEqual(summary())
    vi.advanceTimersByTime(16_000)
    document.dispatchEvent(new Event('visibilitychange'))
    store.startRealtime(['summary'])
    expect(store.summaryStale).toBe(true)
    expect(FakeEventSource.sources).toHaveLength(1)
    expect(fetcher).not.toHaveBeenCalled()
    fetcher.mockResolvedValueOnce(response(summary(16_000)))
    expect((await store.refresh()).succeeded).toEqual(['summary'])
    expect(store.summaryStale).toBe(false)
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    expect(store.realtimeState).toBe('offline')
    expect(FakeEventSource.sources).toHaveLength(1)

    const cached = store.summary
    expect(store.retryClosedRealtime()).toBe(true)
    expect(store.retryClosedRealtime()).toBe(false)
    expect(store.summary).toBe(cached)
    expect(source().url).toBe(old.url)
    expect(FakeEventSource.sources).toHaveLength(2)
    expect(vi.getTimerCount()).toBe(1)
    expect(store.realtimeState).toBe('connecting')
    source().open(); source().snapshot(snapshot(16_000))
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    vi.advanceTimersByTime(1000)
    source().snapshot(snapshot(17_000))
    expect(store.errorCode).toBeNull()
    expect(store.realtimeState).toBe('streaming')
    expect(store.connectionState).toBe('connected')
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0]?.[1]?.method ?? 'GET').toBe('GET')
  })

  it('OPEN 与原生 CONNECTING 不重建，临时断线在同一源收到新快照后恢复', () => {
    store.startRealtime(['summary'])
    const first = source()
    expect(store.retryClosedRealtime()).toBe(false)
    first.open(); first.snapshot(snapshot())
    expect(store.retryClosedRealtime()).toBe(false)
    first.transientError()
    expect(store.retryClosedRealtime()).toBe(false)
    expect(first.close).not.toHaveBeenCalled()
    first.open()
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    vi.advanceTimersByTime(1000)
    first.snapshot(snapshot(1000))
    expect(store.errorCode).toBeNull()
    expect(FakeEventSource.sources).toHaveLength(1)
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('重连后的过期、逆序或无效未来样本不能证明恢复，只有更新且新鲜的 SSE 清除断流', () => {
    store.startRealtime(['summary']); source().snapshot(snapshot()); source().raw('{bad')
    store.retryClosedRealtime(); source().open()
    vi.advanceTimersByTime(17_000)
    source().snapshot(snapshot(1000))
    expect(store.summaryStale).toBe(true)
    expect(store.errorCodes).toContain('REALTIME_STREAM_FAILED')
    source().snapshot(snapshot())
    source().snapshot(snapshot(60_000))
    expect(store.summary?.collectedAt).toBe(summary(1000).collectedAt)
    expect(store.errorCodes).toContain('REALTIME_STREAM_FAILED')
    source().snapshot(snapshot(17_000))
    expect(store.summaryStale).toBe(false)
    expect(store.errorCodes).toEqual([])
    expect(store.connectionState).toBe('connected')
  })

  it('浏览器因终止错误进入 CLOSED 时同样可显式重试，不要求解析器先调用 close', () => {
    store.startRealtime(['summary'])
    const old = source()
    old.open()
    old.readyState = FakeEventSource.CLOSED
    old.onerror?.()
    expect(old.close).not.toHaveBeenCalled()
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    expect(store.retryClosedRealtime()).toBe(true)
    expect(FakeEventSource.sources).toHaveLength(2)
    expect(vi.getTimerCount()).toBe(1)
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('M29 的坏 Docker 切片继续保留共享源，下一合法切片独立恢复', () => {
    store.startRealtime(['summary', 'docker'])
    source().open(); source().snapshot({ ...snapshot(), docker: inventory() })
    vi.advanceTimersByTime(1000)
    source().snapshot({ ...snapshot(1000), docker: { ...inventory(1000), containers: [null] } })
    expect(store.inventoryErrorCode).toBe('DOCKER_INVENTORY_RESPONSE_INVALID')
    expect(store.summary?.collectedAt).toBe(summary(1000).collectedAt)
    expect(store.retryClosedRealtime()).toBe(false)
    expect(source().close).not.toHaveBeenCalled()
    source().snapshot({ ...snapshot(1000), docker: inventory(1000) })
    expect(store.inventoryErrorCode).toBeNull()
    expect(FakeEventSource.sources).toHaveLength(1)
  })

  it('隐藏时不重连，恢复可见不自动重连，后续显式刷新才重建', () => {
    store.startRealtime(['summary']); source().raw('{bad')
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    expect(store.retryClosedRealtime()).toBe(false)
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(FakeEventSource.sources).toHaveLength(1)
    expect(store.retryClosedRealtime()).toBe(true)
    expect(FakeEventSource.sources).toHaveLength(2)
    expect(vi.getTimerCount()).toBe(1)
  })

  it.each(['stop', 'clear', 'logout', 'dispose'] as const)('%s 同步释放所有权，旧流和恢复入口不得复活源或时钟', (action) => {
    store.startRealtime(['summary'])
    const old = source()
    old.raw('{bad')
    if (action === 'stop') store.stopRealtime()
    if (action === 'clear') store.clear()
    if (action === 'logout') invalidateSession()
    if (action === 'dispose') store.$dispose()
    expect(store.retryClosedRealtime()).toBe(false)
    old.onopen?.(); old.onerror?.(); old.queuedSnapshot(snapshot())
    expect(store.summary).toBeNull()
    expect(FakeEventSource.sources).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('空作用域仅保留 M29 的本地过期时钟，不因显式恢复建立订阅', async () => {
    store.startRealtime([])
    fetcher.mockResolvedValueOnce(response(inventory()))
    await store.refresh({ inventory: true })
    expect(store.retryClosedRealtime()).toBe(false)
    vi.advanceTimersByTime(16_000)
    expect(store.inventoryStale).toBe(true)
    expect(vi.getTimerCount()).toBe(1)
    expect(FakeEventSource.sources).toHaveLength(0)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('替换后的旧源回调不能清除错误、覆盖新数据或把新源标记为断线', () => {
    store.startRealtime(['summary'])
    const old = source()
    old.raw('{bad')
    store.retryClosedRealtime()
    old.onopen?.(); old.queuedSnapshot(snapshot())
    expect(store.realtimeState).toBe('connecting')
    expect(store.summary).toBeNull()
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    source().open(); source().snapshot(snapshot())
    old.onerror?.(); old.queuedSnapshot(snapshot(1000))
    expect(store.realtimeState).toBe('streaming')
    expect(store.errorCode).toBeNull()
    expect(store.summary).toEqual(summary())
    expect(FakeEventSource.sources).toHaveLength(2)
  })

  it('重试后的下一条坏帧仍关闭源，不自动循环；下一次明确刷新才再次重建', () => {
    store.startRealtime(['summary']); source().raw('{bad')
    expect(store.retryClosedRealtime()).toBe(true)
    source().raw('{bad')
    vi.advanceTimersByTime(60_000)
    expect(FakeEventSource.sources).toHaveLength(2)
    expect(fetcher).not.toHaveBeenCalled()
    expect(store.retryClosedRealtime()).toBe(true)
    expect(store.retryClosedRealtime()).toBe(false)
    expect(FakeEventSource.sources).toHaveLength(3)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('重连及新 SSE 不释放操作后强刷 pending、不吞掉强刷失败，普通读取仍复用请求', async () => {
    store.startRealtime(['summary']); source().raw('{bad')
    const before = deferred<Response>()
    const after = deferred<Response>()
    fetcher.mockReturnValueOnce(before.promise).mockReturnValueOnce(after.promise)
    const oldRefresh = store.refresh({ summary: true })
    const coalesced = store.refresh({ summary: true })
    store.retryClosedRealtime()
    const forced = store.refresh({ summary: true, force: true })
    expect(fetcher).toHaveBeenCalledTimes(2)
    vi.advanceTimersByTime(1000)
    source().open(); source().snapshot(snapshot(1000))
    before.resolve(response(summary()))
    await Promise.all([oldRefresh, coalesced])
    expect(store.isRefreshing).toBe(true)
    expect(store.summary).toEqual(summary(1000))
    after.reject(new Error('forced read failed'))
    expect((await forced).failed).toEqual([{ resource: 'summary', error: expect.any(Error) }])
    expect(store.isRefreshing).toBe(false)
    expect(store.errorCode).toBe('NETWORK_UNAVAILABLE')
    expect(FakeEventSource.sources).toHaveLength(2)
    expect(fetcher.mock.calls.every(([, options]) => options?.method === undefined || options.method === 'GET')).toBe(true)
  })
})
