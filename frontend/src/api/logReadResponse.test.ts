import { afterEach, describe, expect, it, vi } from 'vitest'
import { followLogs, requestLogs } from './control'

const entry = { id: '', timestamp: '0001-01-01T00:00:00Z', source: 'container', unit: '', level: 'debug', message: '中文\n🙂' }
const page = { collectedAt: '2026-10-16T00:00:00.123456789Z', entries: [entry], nextCursor: ' opaque/+ = ' }
class FakeEventSource {
  static CLOSED = 2
  readyState = 1
  onerror: (() => void) | null = null
  listeners = new Map<string, (event: { data: string }) => void>()
  close = vi.fn(() => { this.readyState = FakeEventSource.CLOSED })
  constructor(readonly url: string) {}
  addEventListener(name: string, callback: (event: { data: string }) => void) { this.listeners.set(name, callback) }
  emit(name: string, value: unknown, raw = false) { this.listeners.get(name)?.({ data: raw ? value as string : JSON.stringify(value) }) }
}
afterEach(() => vi.unstubAllGlobals())

describe('日志 HTTP/SSE 解码边界', () => {
  it('HTTP 一次读取保留查询、取消与凭据，并投影正确结果', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ ...page, secret: 'private' })))
    vi.stubGlobal('fetch', fetcher)
    const controller = new AbortController()
    expect(await requestLogs({ source: 'container', containerId: 'c/1', cursor: page.nextCursor, query: '中文', hours: 1, limit: 200 }, controller.signal)).toEqual(page)
    expect(fetcher).toHaveBeenCalledOnce()
    const [url, init] = fetcher.mock.calls[0]!
    expect(new URL(String(url), 'https://fixture.invalid').searchParams.get('cursor')).toBe(page.nextCursor)
    expect(init).toMatchObject({ signal: controller.signal, credentials: 'same-origin', headers: { Accept: 'application/json' } })
  })

  it.each(['{private', JSON.stringify({ ...page, entries: [entry, {}], nextCursor: 'private', requestId: 'private' }), JSON.stringify({ ...page, collectedAt: 'private' })])('HTTP 坏 JSON/结构只失败一次且不泄漏正文', async (body) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(body))
    vi.stubGlobal('fetch', fetcher)
    const error = await requestLogs({ source: 'system' }).catch((error: unknown) => error)
    expect(error).toMatchObject({ code: 'CONTROL_RESPONSE_INVALID', message: '日志接口返回了无法识别的数据。' })
    expect(JSON.stringify(error)).not.toContain('private')
    expect(error).not.toHaveProperty('cause')
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it('非成功 HTTP 错误与网络取消保持原语义，不重试', async () => {
    const abort = new DOMException('取消', 'AbortError')
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'LOG_QUERY_FAILED', message: '读取失败', requestId: 'fixture' }), { status: 503 }))
      .mockRejectedValueOnce(abort)
    vi.stubGlobal('fetch', fetcher)
    await expect(requestLogs({ source: 'system' })).rejects.toMatchObject({ code: 'LOG_QUERY_FAILED', message: '读取失败', requestId: 'fixture' })
    await expect(requestLogs({ source: 'system' })).rejects.toBe(abort)
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('HTTP 204 不是合法空页，正常空数组才可以发布', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ...page, entries: [] }))))
    await expect(requestLogs({ source: 'system' })).rejects.toMatchObject({ code: 'CONTROL_RESPONSE_INVALID' })
    expect((await requestLogs({ source: 'system' })).entries).toEqual([])
  })

  it.each(['{private', JSON.stringify({ ...page, entries: [entry, {}] }), JSON.stringify({ ...page, nextCursor: false }), JSON.stringify({ ...page, entries: [{ ...entry, level: 'fatal' }] })])('SSE 拒绝整帧，关闭后排队帧也不发布或复活状态', (body) => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const onLogs = vi.fn()
    const onError = vi.fn()
    const source = followLogs({ source: 'system' }, 5, onLogs, onError) as unknown as FakeEventSource
    source.emit('logs', body, true)
    expect(source.close).toHaveBeenCalledOnce()
    expect(onError).toHaveBeenCalledExactlyOnceWith('failed')
    source.emit('logs', page)
    source.emit('unavailable', {})
    source.onerror?.()
    expect(onLogs).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledTimes(1)
  })

  it('SSE 正常投影、空页、unavailable 与网络重连行为保持', () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const onLogs = vi.fn()
    const onError = vi.fn()
    const source = followLogs({ source: 'agent' }, 5, onLogs, onError) as unknown as FakeEventSource
    source.emit('logs', { ...page, entries: [{ ...entry, stream: null, rawMessage: null, secret: 'private' }], secret: 'private' })
    expect(onLogs).toHaveBeenLastCalledWith(page)
    source.emit('logs', { ...page, entries: [] })
    expect(onLogs).toHaveBeenLastCalledWith({ ...page, entries: [] })
    source.emit('unavailable', {})
    source.onerror?.()
    expect(onError.mock.calls).toEqual([['reconnecting'], ['reconnecting']])
    expect(source.close).not.toHaveBeenCalled()
    source.readyState = FakeEventSource.CLOSED
    source.onerror?.()
    expect(onError).toHaveBeenLastCalledWith('failed')
  })
})
