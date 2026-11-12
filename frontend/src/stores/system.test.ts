// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import type { RealtimeSnapshot, SystemSummary, DockerInventory } from '@/api/system'
import { RefreshFailureError, useSystemStore } from './system'

const apiMocks = vi.hoisted(() => ({
  requestCapabilities: vi.fn(),
  requestDockerInventory: vi.fn(),
  requestSystemSummary: vi.fn(),
  subscribeSystemEvents: vi.fn(),
}))

vi.mock('@/api/control', () => ({
  requestListPreference: vi.fn(),
  requestPreferences: vi.fn(),
  updateListPreference: vi.fn(),
  updatePreferences: vi.fn(),
}))

vi.mock('@/api/system', () => ({
  NcpApiError: class NcpApiError extends Error {
    constructor(readonly code: string, message: string) { super(message) }
  },
  ...apiMocks,
}))

const summary = {
  collectedAt: '2026-08-24T00:00:00.000Z',
  host: { hostname: 'ncp-test', operatingSystem: 'Linux', kernelVersion: 'test', architecture: 'amd64', uptimeSeconds: 100, processCount: 1 },
  cpu: { usagePercent: 10, logicalCores: 4, load1: 0.5, load5: 0.4, load15: 0.3 },
  memory: { totalBytes: 100, usedBytes: 40, availableBytes: 60 },
  network: [{ name: 'eth0', receiveBytes: 1000, transmitBytes: 500 }],
  storage: [{ mountpoint: '/', totalBytes: 100, usedBytes: 20, freeBytes: 80 }],
  warnings: [],
} satisfies SystemSummary

const inventory = {
  collectedAt: '2026-08-24T00:00:00.000Z',
  projects: [],
  containers: [],
  engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'amd64', containers: 0, containersRunning: 0, containersStopped: 0, images: 0 },
} satisfies DockerInventory

afterEach(() => { invalidateSession(); vi.useRealTimers() })

describe('useSystemStore.refresh', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(summary.collectedAt))
    beginSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
    apiMocks.subscribeSystemEvents.mockReturnValue({ close: vi.fn() })
    apiMocks.requestSystemSummary.mockResolvedValue(summary)
    apiMocks.requestDockerInventory.mockResolvedValue(inventory)
    apiMocks.requestCapabilities.mockResolvedValue({})
  })

  it('does not drop an inventory refresh while summary is in flight', async () => {
    let resolveSummary: ((value: typeof summary) => void) | undefined
    apiMocks.requestSystemSummary.mockReturnValueOnce(new Promise((resolve) => {
      resolveSummary = resolve
    }))

    const store = useSystemStore()
    const summaryRefresh = store.refresh({ summary: true })
    await Promise.resolve()
    const inventoryRefresh = store.refresh({ inventory: true })

    expect(apiMocks.requestSystemSummary).toHaveBeenCalledTimes(1)
    expect(apiMocks.requestDockerInventory).toHaveBeenCalledTimes(1)

    resolveSummary?.(summary)
    await Promise.all([summaryRefresh, inventoryRefresh])
    expect(store.summary).toEqual(summary)
    expect(store.inventory).toEqual(inventory)
  })

  it('coalesces concurrent refreshes for the same resource', async () => {
    let resolveInventory: ((value: typeof inventory) => void) | undefined
    apiMocks.requestDockerInventory.mockReturnValueOnce(new Promise((resolve) => {
      resolveInventory = resolve
    }))

    const store = useSystemStore()
    const first = store.refresh({ inventory: true })
    const second = store.refresh({ inventory: true })

    expect(apiMocks.requestDockerInventory).toHaveBeenCalledTimes(1)
    resolveInventory?.(inventory)
    await Promise.all([first, second])
  })

  it('操作后强刷不能被操作前请求满足，普通刷新复用新的请求', async () => {
    const before = deferred<DockerInventory>()
    const after = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(before.promise).mockReturnValueOnce(after.promise)
    const store = useSystemStore()
    const oldRefresh = store.refresh({ inventory: true })
    const strongRefresh = store.refresh({ inventory: true, force: true })
    expect(apiMocks.requestDockerInventory).toHaveBeenCalledTimes(2)

    before.resolve(inventory)
    expect(await oldRefresh).toEqual({ requested: ['inventory'], succeeded: [], failed: [] })
    expect(store.inventory).toBeNull()
    expect(store.isRefreshing).toBe(true)
    const ordinaryRefresh = store.refresh({ inventory: true })
    expect(apiMocks.requestDockerInventory).toHaveBeenCalledTimes(2)

    const fresh = nextInventory(1000)
    after.resolve(fresh)
    const results = await Promise.all([strongRefresh, ordinaryRefresh])
    expect(results.every((result) => result.succeeded.includes('inventory'))).toBe(true)
    expect(store.inventory).toEqual(fresh)
    expect(store.isRefreshing).toBe(false)
  })

  it.each(['success', 'failure'])('强刷完成后，旧请求迟到的 %s 不覆盖数据或错误', async (outcome) => {
    const before = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(before.promise)
    const store = useSystemStore()
    const oldRefresh = store.refresh({ inventory: true })
    await store.refresh({ inventory: true, force: true })
    if (outcome === 'success') before.resolve(nextInventory(1000))
    else before.reject(new Error('old failure'))
    await oldRefresh
    expect(store.inventory).toEqual(inventory)
    expect(store.errorCode).toBeNull()
    expect(store.connectionState).toBe('connected')
    expect(store.isRefreshing).toBe(false)
  })

  it('连续操作各自新发强刷，前一次 finally 不释放后一次的刷新状态', async () => {
    const first = deferred<DockerInventory>()
    const second = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const store = useSystemStore()
    const firstRefresh = store.refresh({ inventory: true, force: true })
    const secondRefresh = store.refresh({ inventory: true, force: true })
    expect(apiMocks.requestDockerInventory).toHaveBeenCalledTimes(2)
    first.resolve(inventory)
    expect((await firstRefresh).succeeded).toEqual(['inventory'])
    expect(store.isRefreshing).toBe(true)
    second.resolve(nextInventory(1000))
    await secondRefresh
    expect(store.inventory?.collectedAt).toBe(nextInventory(1000).collectedAt)
    expect(store.isRefreshing).toBe(false)
  })

  it.each(['pending', 'completed'])('后一次强刷 %s 时，前一次强刷失败仍通知其调用方但不写入展示状态', async (status) => {
    const first = deferred<DockerInventory>()
    const second = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const store = useSystemStore()
    const firstRefresh = store.refresh({ inventory: true, force: true })
    const secondRefresh = store.refresh({ inventory: true, force: true })
    if (status === 'completed') {
      second.resolve(inventory)
      await secondRefresh
    }
    const error = new Error('first operation refresh failed')
    first.reject(error)
    expect((await firstRefresh).failed).toEqual([{ resource: 'inventory', error }])
    expect(store.errorCode).toBeNull()
    expect(store.isRefreshing).toBe(status === 'pending')
    if (status === 'pending') {
      second.resolve(inventory)
      await secondRefresh
    }
    expect(store.inventory).toEqual(inventory)
    expect(store.connectionState).toBe('connected')
  })

  it('注销后的强刷失败不再归因于新会话', async () => {
    const pending = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(pending.promise)
    const store = useSystemStore()
    const strongRefresh = store.refresh({ inventory: true, force: true })
    invalidateSession()
    store.clear()
    beginSession()
    pending.reject(new Error('old session failure'))
    expect(await strongRefresh).toEqual({ requested: ['inventory'], succeeded: [], failed: [] })
    expect(store.errorCode).toBeNull()
  })

  it.each(['invalid', 'future'])('被后一次强刷替代后仍校验 %s 采集时间，不向调用方误报成功', async (timestamp) => {
    const first = deferred<DockerInventory>()
    const second = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const store = useSystemStore()
    const firstRefresh = store.refresh({ inventory: true, force: true })
    const secondRefresh = store.refresh({ inventory: true, force: true })
    first.resolve({ ...inventory, collectedAt: timestamp === 'invalid' ? 'invalid' : nextInventory(60_000).collectedAt })
    expect((await firstRefresh).failed).toEqual([{ resource: 'inventory', error: expect.objectContaining({ code: 'DOCKER_INVENTORY_RESPONSE_INVALID' }) }])
    expect(store.inventory).toBeNull()
    expect(store.errorCode).toBeNull()
    expect(store.isRefreshing).toBe(true)
    second.resolve(inventory)
    await secondRefresh
    expect(store.inventory).toEqual(inventory)
  })

  it('inventory 强刷不替换其他资源的请求或提前清除全局 busy', async () => {
    const pendingSummary = deferred<SystemSummary>()
    apiMocks.requestSystemSummary.mockReturnValueOnce(pendingSummary.promise)
    const store = useSystemStore()
    const summaryRefresh = store.refresh({ summary: true })
    await store.refresh({ inventory: true, force: true })
    expect(apiMocks.requestSystemSummary).toHaveBeenCalledTimes(1)
    expect(apiMocks.requestCapabilities).not.toHaveBeenCalled()
    expect(store.isRefreshing).toBe(true)
    pendingSummary.resolve(summary)
    await summaryRefresh
    expect(store.summary).toEqual(summary)
    expect(store.isRefreshing).toBe(false)
  })

  it('强刷失败不会被旧请求的迟到成功清除', async () => {
    const before = deferred<DockerInventory>()
    const error = new Error('strong refresh failed')
    apiMocks.requestDockerInventory.mockReturnValueOnce(before.promise).mockRejectedValueOnce(error)
    const store = useSystemStore()
    const oldRefresh = store.refresh({ inventory: true })
    const result = await store.refresh({ inventory: true, force: true })
    expect(result.failed).toEqual([{ resource: 'inventory', error }])
    before.resolve(inventory)
    await oldRefresh
    expect(store.inventory).toBeNull()
    expect(store.errorCode).toBe('NETWORK_UNAVAILABLE')
  })

  it('强刷保留共享 SSE 和时间门控，不能用旧 HTTP 覆盖更新的推送', async () => {
    const after = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(after.promise)
    const store = useSystemStore()
    store.startRealtime(['docker'])
    stream().snapshot({ collectedAt: inventory.collectedAt, docker: inventory })
    const strongRefresh = store.refresh({ force: true })
    const fresh = nextInventory(1000)
    stream().snapshot({ collectedAt: fresh.collectedAt, docker: fresh })
    after.resolve(inventory)
    await strongRefresh
    expect(store.inventory).toEqual(fresh)
    expect(apiMocks.subscribeSystemEvents).toHaveBeenCalledTimes(1)
    expect(apiMocks.subscribeSystemEvents.mock.results[0]?.value.close).not.toHaveBeenCalled()
    expect(apiMocks.requestSystemSummary).not.toHaveBeenCalled()
  })

  it('新 SSE 不能替代强刷自身的失败结果', async () => {
    const after = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(after.promise)
    const store = useSystemStore()
    store.startRealtime(['docker'])
    const strongRefresh = store.refreshStrict({ inventory: true, force: true })
    stream().snapshot({ collectedAt: inventory.collectedAt, docker: inventory })
    after.reject(new Error('post-operation refresh failed'))
    await expect(strongRefresh).rejects.toBeInstanceOf(RefreshFailureError)
    expect(store.inventory).toEqual(inventory)
    expect(store.connectionState).toBe('degraded')
  })

  it.each(['clear', 'logout'])('%s 隔离全部强刷；旧 finally 不能释放新请求', async (action) => {
    const before = deferred<DockerInventory>()
    const after = deferred<DockerInventory>()
    const next = deferred<DockerInventory>()
    apiMocks.requestDockerInventory.mockReturnValueOnce(before.promise).mockReturnValueOnce(after.promise).mockReturnValueOnce(next.promise)
    const store = useSystemStore()
    const oldRefresh = store.refresh({ inventory: true })
    const strongRefresh = store.refresh({ inventory: true, force: true })
    const signal = apiMocks.requestDockerInventory.mock.calls[1]![1] as AbortSignal
    if (action === 'logout') invalidateSession()
    store.clear()
    if (action === 'logout') {
      expect(signal.aborted).toBe(true)
      beginSession()
    }
    const currentRefresh = store.refresh({ inventory: true })
    before.resolve(inventory)
    after.resolve(nextInventory(1000))
    await Promise.all([oldRefresh, strongRefresh])
    expect(store.inventory).toBeNull()
    expect(store.isRefreshing).toBe(true)
    const fresh = nextInventory(2000)
    next.resolve(fresh)
    await currentRefresh
    expect(store.inventory).toEqual(fresh)
    expect(store.isRefreshing).toBe(false)
  })

  it('keeps successful data when another requested resource fails', async () => {
    apiMocks.requestDockerInventory.mockRejectedValueOnce(new Error('inventory unavailable'))

    const store = useSystemStore()
    const result = await store.refresh({ summary: true, inventory: true })

    expect(store.summary).toEqual(summary)
    expect(store.inventory).toBeNull()
    expect(store.connectionState).toBe('degraded')
    expect(store.errorCode).toBe('NETWORK_UNAVAILABLE')
    expect(result.failed).toHaveLength(1)
    apiMocks.requestDockerInventory.mockRejectedValueOnce(new Error('inventory unavailable'))
    await expect(store.refreshStrict({ inventory: true })).rejects.toBeInstanceOf(RefreshFailureError)
  })
})

type StreamHandlers = { open: () => void; snapshot: (snapshot: RealtimeSnapshot) => void; error: () => void }
function stream(index = -1): StreamHandlers { return apiMocks.subscribeSystemEvents.mock.calls.at(index)![2] }
function nextSummary(offset = 0, updates: Partial<SystemSummary> = {}): SystemSummary {
  return { ...summary, collectedAt: new Date(Date.now() + offset).toISOString(), ...updates }
}
function snapshot(summaryValue: SystemSummary = nextSummary(), errors: string[] = []) {
  return { collectedAt: new Date().toISOString(), summary: summaryValue, errors }
}
function nextInventory(offset = 0): DockerInventory {
  return { ...inventory, collectedAt: new Date(Date.now() + offset).toISOString() }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

describe('共享快照新鲜度与会话生命周期', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(summary.collectedAt))
    beginSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
    apiMocks.subscribeSystemEvents.mockReturnValue({ close: vi.fn() })
    apiMocks.requestSystemSummary.mockResolvedValue(summary)
    apiMocks.requestDockerInventory.mockResolvedValue(inventory)
    apiMocks.requestCapabilities.mockResolvedValue({ hostname: 'test' })
  })

  it('冷启动、15 秒边界、静默过期和新快照恢复不依赖新事件', () => {
    const store = useSystemStore()
    store.refreshIntervalSeconds = 300
    store.startRealtime(['summary'])
    expect(store.connectionState).toBe('loading')
    stream().open()
    expect(store.connectionState).not.toBe('connected')
    stream().snapshot(snapshot())
    expect(store.connectionState).toBe('connected')
    vi.advanceTimersByTime(15_000)
    expect(store.connectionState).toBe('connected')
    vi.advanceTimersByTime(1000)
    expect(store.connectionState).toBe('degraded')
    expect(store.summaryStale).toBe(true)
    expect(store.connectionLabel).toBe('显示上次数据')
    expect(store.summary?.collectedAt).toBe(summary.collectedAt)
    stream().snapshot(snapshot())
    expect(store.connectionState).toBe('connected')
    expect(store.summaryStale).toBe(false)
  })

  it('新外层时间、重复样本、open 均不为陈旧数据续期，也不追加历史', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot(summary))
    vi.advanceTimersByTime(10_000)
    stream().snapshot(snapshot(summary))
    stream().open()
    vi.advanceTimersByTime(6000)
    expect(store.errorCode).toBe('SYSTEM_SUMMARY_STALE')
    expect(store.resourceHistory).toHaveLength(1)
    expect(store.summary).toEqual(summary)
  })

  it('断流后重新 open 或重复快照不能清除失败，新采集快照才恢复', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot())
    stream().error()
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    stream().open()
    stream().snapshot(snapshot(summary))
    expect(store.connectionState).toBe('degraded')
    expect(store.errorCode).toBe('REALTIME_STREAM_FAILED')
    vi.advanceTimersByTime(5000)
    stream().snapshot(snapshot())
    expect(store.connectionState).toBe('connected')
    expect(store.errorCode).toBeNull()
  })

  it('源失败保留本地数据，能力或 Docker 成功不会把 summary 错误标绿', async () => {
    const store = useSystemStore()
    store.startRealtime(['summary', 'docker'])
    stream().snapshot({ ...snapshot(), docker: nextInventory() })
    stream().snapshot({ collectedAt: new Date().toISOString(), docker: nextInventory(), errors: ['SYSTEM_SUMMARY_UNAVAILABLE', 'MONITORING_WRITE_FAILED'] })
    await store.refresh({ capabilities: true })
    expect(store.summary).toEqual(summary)
    expect(store.errorCodes).toContain('SYSTEM_SUMMARY_UNAVAILABLE')
    expect(store.errorCodes).toContain('MONITORING_WRITE_FAILED')
    expect(store.connectionState).toBe('degraded')
    vi.advanceTimersByTime(5000)
    stream().snapshot({ ...snapshot(), docker: nextInventory() })
    expect(store.connectionState).toBe('connected')
    expect(store.errorCodes).toEqual([])
  })

  it('当前路由没有 summary 时，旧能力和 inventory 不能冒充所需数据', async () => {
    const store = useSystemStore()
    await store.refresh({ capabilities: true, inventory: true })
    store.startRealtime(['summary'])
    stream().snapshot({ collectedAt: new Date().toISOString(), errors: ['SYSTEM_SUMMARY_UNAVAILABLE'] })
    expect(store.connectionState).toBe('unavailable')
    expect(store.summary).toBeNull()
    store.startRealtime(['docker'])
    stream().snapshot({ collectedAt: new Date().toISOString(), docker: nextInventory(1000) })
    expect(store.connectionState).toBe('connected')
  })

  it('有效新 SSE 清理同资源的手动刷新失败，但不清理其他资源错误', async () => {
    const store = useSystemStore()
    store.startRealtime(['summary', 'docker'])
    stream().snapshot({ ...snapshot(), docker: nextInventory() })
    apiMocks.requestSystemSummary.mockRejectedValueOnce(new Error('offline'))
    apiMocks.requestDockerInventory.mockRejectedValueOnce(new Error('offline'))
    await store.refresh({ summary: true, inventory: true })
    vi.advanceTimersByTime(5000)
    stream().snapshot({ ...snapshot(), docker: inventory })
    expect(store.connectionState).toBe('degraded')
    stream().snapshot({ ...snapshot(), docker: nextInventory() })
    expect(store.connectionState).toBe('connected')
  })

  it('晚到 HTTP 与逆序 SSE 都不能覆盖已收到的新 summary', async () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    const pending = deferred<SystemSummary>()
    apiMocks.requestSystemSummary.mockReturnValueOnce(pending.promise)
    const refresh = store.refresh({ summary: true })
    vi.advanceTimersByTime(5000)
    const newest = nextSummary(0, { cpu: { ...summary.cpu, usagePercent: 42 } })
    stream().snapshot(snapshot(newest))
    pending.resolve(summary)
    await refresh
    stream().snapshot(snapshot(summary))
    expect(store.summary).toEqual(newest)
    expect(store.resourceHistory).toHaveLength(1)
    expect(store.connectionState).toBe('connected')
  })

  it('已有更新的 SSE 成功时，旧 HTTP 请求晚到失败也不能降级', async () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    const pending = deferred<SystemSummary>()
    apiMocks.requestSystemSummary.mockReturnValueOnce(pending.promise)
    const refresh = store.refresh({ summary: true })
    stream().snapshot(snapshot())
    pending.reject(new Error('old failure'))
    await refresh
    expect(store.connectionState).toBe('connected')
  })

  it('警告保留真实部分数据并用缺值表示受影响指标，不生成健康零值', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    const warned = nextSummary(0, {
      warnings: [{ source: 'cpu', code: 'CPU_UNAVAILABLE' }, { source: 'memory', code: 'MEMORY_UNAVAILABLE' }, { source: 'network', code: 'NETWORK_UNAVAILABLE' }],
    })
    stream().snapshot(snapshot(warned))
    expect(store.summary).toEqual(warned)
    expect(store.connectionState).toBe('degraded')
    const sample = store.resourceHistory[0]!
    expect(sample.cpuPercent).toBeNaN()
    expect(sample.memoryPercent).toBeNaN()
    expect(sample.load1).toBeNaN()
    expect(sample.networkReceiveBps).toBeNaN()
  })

  it('温度源警告不会抹去 CPU 或实际读到的温度子集', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot(nextSummary(0, { warnings: [{ source: 'temperature', code: 'TEMPERATURE_UNAVAILABLE' }], sensors: [{ name: 'cpu', temperatureCelsius: 50 }] })))
    expect(store.resourceHistory[0]?.cpuPercent).toBe(10)
    expect(store.summary?.sensors).toHaveLength(1)
    expect(store.connectionState).toBe('degraded')
  })

  it.each(['MONITORING_WRITE_FAILED', 'MONITORING_SAMPLE_INCOMPLETE', 'MONITORING_SAMPLE_INVALID', 'MONITORING_SAMPLE_LIMIT_EXCEEDED'])('历史错误 %s 不把真实快照抹去或误报 Agent 失联', (code) => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot(summary, [code]))
    expect(store.summary).toEqual(summary)
    expect(store.summaryStale).toBe(false)
    expect(store.connectionState).toBe('degraded')
    expect(store.connectionDetail).not.toContain('Agent')
    expect(store.errorCode).toBe(code)
  })

  it('无效或过远的未来时间不覆盖缓存、不以现在捏造历史', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot())
    stream().snapshot(snapshot(nextSummary(0, { collectedAt: 'invalid' })))
    stream().snapshot(snapshot(nextSummary(60_000)))
    expect(store.summary).toEqual(summary)
    expect(store.resourceHistory).toHaveLength(1)
    expect(store.connectionState).toBe('degraded')
  })

  it('接收时已过期的合法时间只保留为旧数据，不注入实时历史', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot(nextSummary(-16_000)))
    expect(store.summary).not.toBeNull()
    expect(store.summaryStale).toBe(true)
    expect(store.resourceHistory).toHaveLength(0)
    expect(store.connectionState).toBe('degraded')
  })

  it('未来容差和客户端时钟回拨不能延长接收后的新鲜度', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot(nextSummary(5000)))
    vi.setSystemTime(Date.now() - 60_000)
    vi.advanceTimersByTime(16_000)
    expect(store.summaryStale).toBe(true)
  })

  it('长时间后台静默后恢复可见时立即重新计算新鲜度', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot())
    vi.setSystemTime(Date.now() + 60_000)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(store.summaryStale).toBe(true)
  })

  it('空作用域保留本地时钟，停止、会话失效和销毁均回收时钟，旧流回调失效', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    const old = stream()
    expect(vi.getTimerCount()).toBe(1)
    store.startRealtime(['docker'])
    expect(vi.getTimerCount()).toBe(1)
    old.snapshot(snapshot())
    expect(store.summary).toBeNull()
    store.startRealtime([])
    expect(vi.getTimerCount()).toBe(1)
    store.stopRealtime()
    expect(vi.getTimerCount()).toBe(0)
    store.startRealtime(['summary'])
    invalidateSession()
    expect(vi.getTimerCount()).toBe(0)
    stream().snapshot(snapshot())
    expect(store.summary).toBeNull()
    beginSession()
    store.clear()
    store.startRealtime(['summary'])
    store.$dispose()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('logout/clear 后旧请求和旧 SSE 不得污染新会话，刷新可重新发起', async () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    const oldStream = stream()
    const pending = deferred<SystemSummary>()
    apiMocks.requestSystemSummary.mockReturnValueOnce(pending.promise)
    const oldRefresh = store.refresh({ summary: true })
    invalidateSession()
    store.clear()
    beginSession()
    store.startRealtime(['summary'])
    const current = nextSummary(1000)
    stream().snapshot(snapshot(current))
    oldStream.snapshot(snapshot(summary))
    pending.resolve(summary)
    await oldRefresh
    expect(store.summary).toEqual(current)
    expect(store.resourceHistory).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(1)
    expect(store.isRefreshing).toBe(false)
  })

  it('网络预热、计数器重置和长缺口产生缺值，连续样本才计算速率', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot())
    expect(store.resourceHistory.at(-1)?.networkReceiveBps).toBeNaN()
    vi.advanceTimersByTime(5000)
    stream().snapshot(snapshot(nextSummary(0, { network: [{ name: 'eth0', receiveBytes: 1500, transmitBytes: 1000 }] })))
    expect(store.resourceHistory.at(-1)?.networkReceiveBps).toBe(100)
    vi.advanceTimersByTime(5000)
    stream().snapshot(snapshot())
    expect(store.resourceHistory.at(-1)?.networkReceiveBps).toBeNaN()
    vi.advanceTimersByTime(20_000)
    stream().snapshot(snapshot(nextSummary(0, { network: [{ name: 'eth0', receiveBytes: 2000, transmitBytes: 1000 }] })))
    expect(store.resourceHistory.at(-1)?.networkReceiveBps).toBeNaN()
  })
})

describe('实时历史边界', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(summary.collectedAt))
    beginSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
    apiMocks.subscribeSystemEvents.mockReturnValue({ close: vi.fn() })
  })
  it('空网络接口不生成零速率，并明确部分数据不可用', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot(nextSummary(0, { network: [] })))
    expect(store.resourceHistory.at(-1)?.networkReceiveBps).toBeNaN()
    expect(store.connectionState).toBe('degraded')
    expect(store.errorCode).toBe('SYSTEM_SUMMARY_PARTIAL')
  })
  it.each<{ name: string; updates: Partial<SystemSummary> }>([
    { name: '空存储', updates: { storage: [] } },
    { name: '缺失存储', updates: { storage: undefined } },
    { name: '存储容量为零', updates: { storage: [{ mountpoint: '/', totalBytes: 0, usedBytes: 0, freeBytes: 0 }] } },
    { name: '存储已用超出容量', updates: { storage: [{ mountpoint: '/', totalBytes: 10, usedBytes: 20, freeBytes: 0 }] } },
    { name: '无效磁盘 I/O', updates: { diskIO: { readBytes: Number.NaN, writeBytes: 0 } } },
    { name: '负值磁盘 I/O', updates: { diskIO: { readBytes: 0, writeBytes: -1 } } },
  ])('$name 即使没有源 warning 也必须降级，并在有效新快照后恢复', ({ updates }) => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    stream().snapshot(snapshot(nextSummary(0, updates)))
    expect(store.connectionState).toBe('degraded')
    expect(store.errorCode).toBe('SYSTEM_SUMMARY_PARTIAL')
    expect(store.resourceHistory.at(-1)?.cpuPercent).toBe(10)
    vi.advanceTimersByTime(5000)
    stream().snapshot(snapshot())
    expect(store.connectionState).toBe('connected')
  })
  it('历史始终最多 60 点，重复和逆序快照不占用新的点位', () => {
    const store = useSystemStore()
    store.startRealtime(['summary'])
    for (let i = 0; i < 65; i++) {
      vi.advanceTimersByTime(5000)
      stream().snapshot(snapshot())
      stream().snapshot(snapshot())
    }
    expect(store.resourceHistory).toHaveLength(60)
    const current = store.summary?.collectedAt
    stream().snapshot(snapshot(summary))
    expect(store.resourceHistory).toHaveLength(60)
    expect(store.summary?.collectedAt).toBe(current)
  })
  it('clear 本身也使同会话晚到的请求失效', async () => {
    const store = useSystemStore()
    const pending = deferred<SystemSummary>()
    apiMocks.requestSystemSummary.mockReturnValueOnce(pending.promise)
    const refresh = store.refresh({ summary: true })
    store.clear()
    pending.resolve(summary)
    await refresh
    expect(store.summary).toBeNull()
    expect(store.resourceHistory).toHaveLength(0)
    expect(store.isRefreshing).toBe(false)
  })
})
