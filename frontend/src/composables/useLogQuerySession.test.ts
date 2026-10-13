import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useLogQuerySession } from './useLogQuerySession'
import type { followLogs, LogEntry, LogResponse } from '../api/control'
import { beginSession, invalidateSession } from '../session/sessionLifecycle'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function entry(id: string, message = id): LogEntry {
  return { id, message, source: 'system', unit: 'fixture.service', level: 'info', timestamp: '2026-10-13T00:00:00Z' }
}
function response(entries: LogEntry[], nextCursor = ''): LogResponse {
  return { entries, nextCursor, collectedAt: '2026-10-13T00:00:00Z' }
}
function setup() {
  const requests: Array<ReturnType<typeof deferred<LogResponse>>> = []
  const signals: AbortSignal[] = []
  const callbacks: Array<{ logs: Parameters<typeof followLogs>[2]; error: Parameters<typeof followLogs>[3]; close: ReturnType<typeof vi.fn> }> = []
  const requestLogs = vi.fn((_input: Parameters<typeof import('../api/control').requestLogs>[0], signal?: AbortSignal) => {
    const request = deferred<LogResponse>()
    requests.push(request)
    signals.push(signal!)
    return request.promise
  })
  const session = useLogQuerySession({
    requestLogs,
    followLogs: (_input, _interval, logs, error) => {
      const close = vi.fn()
      callbacks.push({ logs, error, close })
      return { close } as unknown as EventSource
    },
  })
  return { session, requests, signals, callbacks, requestLogs }
}
const query = { source: 'system' as const, query: '', hours: 6, limit: 200 }
beforeEach(() => beginSession())

describe('日志查询会话', () => {
  it('分页中切换条件释放 busy，旧分页成功不能覆盖新数据或游标', async () => {
    const { session, requests, signals } = setup()
    const initial = session.load(query)
    requests[0]!.resolve(response([entry('A')], 'cursor-A'))
    await initial
    const older = session.loadMore()
    expect(session.loadingMore.value).toBe(true)
    const newer = session.load({ ...query, hours: 1 })
    expect(signals[1]!.aborted).toBe(true)
    expect(session.loadingMore.value).toBe(false)
    requests[2]!.resolve(response([entry('B')], 'cursor-B'))
    await newer
    const page = session.loadMore()
    requests[1]!.resolve(response([entry('stale')], 'stale-cursor'))
    await older
    expect(session.loadingMore.value).toBe(true)
    expect(session.nextCursor.value).toBe('cursor-B')
    requests[3]!.resolve(response([entry('C')]))
    await page
    expect(session.entries.value.map((item) => item.id)).toEqual(['C', 'B'])
    expect(session.loadingMore.value).toBe(false)
  })

  it('历史响应不丢失先到 SSE，跨资源同 id 保留，同资源重复去重', async () => {
    const { session, requests, callbacks } = setup()
    const load = session.load(query)
    session.syncFollow(true, 5, 200)
    callbacks[0]!.logs(response([entry('same', '实时版本'), entry('live')]))
    requests[0]!.resolve(response([entry('same', '历史版本'), { ...entry('same'), unit: 'other.service' }]))
    await load
    expect(session.entries.value).toHaveLength(3)
    expect(session.entries.value.find((item) => item.id === 'same' && item.unit === 'fixture.service')?.message).toBe('实时版本')
    callbacks[0]!.logs(response([entry('live')]))
    expect(session.entries.value).toHaveLength(3)
  })

  it('新查询拒绝旧流数据及错误，查询输入采用不可变快照', async () => {
    const { session, requests, callbacks, requestLogs } = setup()
    const input = { ...query }
    const old = session.load(input)
    session.syncFollow(true, 5, 200)
    input.query = '未提交编辑'
    requests[0]!.resolve(response([entry('A')], 'next'))
    await old
    const more = session.loadMore()
    expect(requestLogs.mock.calls[1]![0].query).toBe('')
    const current = session.load({ ...query, source: 'agent' })
    session.syncFollow(true, 5, 200)
    callbacks[0]!.logs(response([entry('stale')]))
    callbacks[0]!.error('failed')
    expect(session.followState.value).toBe('connecting')
    expect(session.error.value).toBe('')
    requests[1]!.reject(new Error('旧分页失败'))
    requests[2]!.resolve(response([entry('B')]))
    await Promise.all([more, current])
    expect(session.entries.value.map((item) => item.id)).toEqual(['B'])
    expect(session.error.value).toBe('')
  })

  it('当前分页失败或取消均释放 busy，卸载后不再写入', async () => {
    const { session, requests, callbacks } = setup()
    const load = session.load(query)
    requests[0]!.resolve(response([entry('A')], 'next'))
    await load
    const failure = session.loadMore()
    requests[1]!.reject(new Error('故障'))
    await failure
    expect(session.loadingMore.value).toBe(false)
    expect(session.error.value).toContain('更多日志读取失败')
    const abort = session.loadMore()
    requests[2]!.reject(new DOMException('取消', 'AbortError'))
    await abort
    expect(session.loadingMore.value).toBe(false)
    session.syncFollow(true, 5, 200)
    const pending = session.loadMore()
    session.dispose()
    callbacks[0]!.logs(response([entry('stale')]))
    requests[3]!.resolve(response([entry('stale')]))
    await pending
    expect(session.loadingMore.value).toBe(false)
    expect(session.entries.value.map((item) => item.id)).toEqual(['A'])
  })

  it('会话失效关闭实时连接并忽略未完成历史', async () => {
    const { session, requests, callbacks, signals } = setup()
    const pending = session.load(query)
    session.syncFollow(true, 5, 200)
    invalidateSession()
    expect(callbacks[0]!.close).toHaveBeenCalledOnce()
    expect(signals[0]!.aborted).toBe(true)
    callbacks[0]!.error('failed')
    requests[0]!.resolve(response([entry('stale')]))
    await pending
    expect(session.entries.value).toEqual([])
    expect(session.error.value).toBe('')
    session.dispose()
  })
})
