import { describe, expect, it, vi } from 'vitest'
import { requestDockerResources } from './docker'
import { requestPathEntries } from './filesystem'

describe('创建抽屉只读取消参数', () => {
  it.each([false, true])('资源清单保持 fetcher 注入并转发可选 signal=%s', async (withSignal) => {
    const controller = new AbortController()
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ networks: [], volumes: [], collectedAt: '2026-10-13T00:00:00Z' })))
    await requestDockerResources(fetcher, withSignal ? controller.signal : undefined)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/docker/resources', expect.objectContaining({ credentials: 'same-origin' }))
    expect(fetcher.mock.calls[0]![1]?.signal).toBe(withSignal ? controller.signal : undefined)
  })
  it.each([false, true])('路径清单保持第四参数 fetcher 并转发可选 signal=%s', async (withSignal) => {
    const controller = new AbortController()
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ path: '/volume/data', parent: '/volume', entries: [], collectedAt: '2026-10-13T00:00:00Z' })))
    await requestPathEntries('/volume/data', 'next', 100, fetcher, withSignal ? controller.signal : undefined)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/files/entries?path=%2Fvolume%2Fdata&cursor=next&limit=100', expect.objectContaining({ credentials: 'same-origin' }))
    expect(fetcher.mock.calls[0]![1]?.signal).toBe(withSignal ? controller.signal : undefined)
  })
})
