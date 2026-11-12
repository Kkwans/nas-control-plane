import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { useJobHistory } from './useJobHistory'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { NcpApiError } from '@/api/systemTransport'
import type { JobHistoryPage } from '@/api/jobHistory'
const api = vi.hoisted(() => ({ read: vi.fn() }))
vi.mock('@/api/jobHistory', () => ({ requestJobHistory: api.read }))
const page = (id = 'a', nextCursor: string | undefined = 'YWJj'): JobHistoryPage => ({ source: 'persisted', items: [{ id, type: 'future', status: 'running', message: '', createdAt: '2026-10-15T00:00:00Z', updatedAt: '2026-10-15T00:00:00Z' }], nextCursor })
function deferred() { let resolve!: (v: JobHistoryPage) => void; let reject!: (e: Error) => void; const promise = new Promise<JobHistoryPage>((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const scopes: ReturnType<typeof effectScope>[] = []
const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve() }
function setup() { const scope = effectScope(); scopes.push(scope); const unauthorized = vi.fn(); const state = scope.run(() => useJobHistory(unauthorized))!; return { state, scope, unauthorized } }
beforeEach(() => { vi.resetAllMocks(); beginSession(); api.read.mockResolvedValue(page()) })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); vi.useRealTimers() })
describe('历史请求所有权与有界分页', () => {
  it('默认 50，只有当前页，下一页单飞且冻结请求', async () => {
    const { state } = setup(); await flush(); expect(api.read.mock.calls[0]?.[0]).toEqual({ type: '', limit: 50 })
    const next = deferred(); api.read.mockReturnValue(next.promise); state.next(); state.next(); state.retry()
    expect(api.read).toHaveBeenCalledTimes(2); expect(Object.isFrozen(api.read.mock.calls[1]?.[0])).toBe(true)
    expect(state.page.value?.items[0]?.id).toBe('a'); next.resolve({ ...page('b'), nextCursor: undefined }); await flush()
    expect(state.page.value?.items.map(x => x.id)).toEqual(['b']); state.next(); expect(api.read).toHaveBeenCalledTimes(2)
  })
  it('下一页失败保留原页/标签/游标，只重试相同请求', async () => {
    const { state } = setup(); await flush(); const original = state.page.value
    api.read.mockRejectedValueOnce(new Error('offline')); state.next(); await flush()
    expect(state.page.value).toBe(original); expect(state.query.value.cursor).toBeUndefined(); state.next(); expect(api.read).toHaveBeenCalledTimes(2)
    state.retry(); await flush(); expect(api.read.mock.calls[2]?.[0]).toEqual(api.read.mock.calls[1]?.[0]); expect(state.error.value).toBeNull()
  })
  it('慢 A 快 B 及旧 finally 不清除新请求 loading', async () => {
    const a = deferred(), b = deferred(); api.read.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const { state } = setup(); const signal = api.read.mock.calls[0]?.[1] as AbortSignal
    state.latest('other', 20); expect(signal.aborted).toBe(true); expect(state.page.value).toBeNull()
    a.resolve(page('old')); await flush(); expect(state.loading.value).toBe(true); expect(state.page.value).toBeNull()
    b.resolve(page('new')); await flush(); expect(state.page.value?.items[0]?.id).toBe('new'); expect(state.query.value).toEqual({ type: 'other', limit: 20 })
  })
  it('快 B 后慢 A 的成功与失败都不覆盖 B', async () => {
    for (const fail of [false, true]) {
      const a = deferred(); api.read.mockReturnValueOnce(a.promise).mockResolvedValueOnce(page('new'))
      const { state } = setup(); state.latest('new'); await flush()
      if (fail) a.reject(new Error('old')); else a.resolve(page('old'))
      await flush(); expect(state.page.value?.items[0]?.id).toBe('new'); expect(state.error.value).toBeNull()
    }
  })
  it.each(['scope', 'session'] as const)('%s 结束中止请求并禁止迟到写入与 401 回调', async action => {
    const pending = deferred(); api.read.mockReturnValueOnce(pending.promise); const { state, scope, unauthorized } = setup()
    const signal = api.read.mock.calls[0]?.[1] as AbortSignal
    if (action === 'scope') scope.stop(); else invalidateSession()
    expect(signal.aborted).toBe(true); pending.reject(new NcpApiError('AUTH_UNAUTHORIZED', 'invalid')); await flush()
    expect(state.page.value).toBeNull(); expect(state.error.value).toBeNull(); expect(unauthorized).not.toHaveBeenCalled()
  })
  it('旧筛选 401 不刷新会话；当前 401 只通知一次且不能重试', async () => {
    const old = deferred(); api.read.mockReturnValueOnce(old.promise); const { state, unauthorized } = setup()
    state.latest('new'); await flush(); old.reject(new NcpApiError('AUTH_UNAUTHORIZED', 'invalid')); await flush(); expect(unauthorized).not.toHaveBeenCalled()
    api.read.mockRejectedValueOnce(new NcpApiError('AUTH_UNAUTHORIZED', 'invalid')); state.latest(); await flush(); expect(unauthorized).toHaveBeenCalledOnce()
    state.retry(); expect(unauthorized).toHaveBeenCalledOnce(); expect(api.read).toHaveBeenCalledTimes(3)
  })
  it('修改页大小/回最新清空旧页与游标；不累计页面或定时读取', async () => {
    vi.useFakeTimers(); const { state } = setup(); await flush(); state.next(); await flush(); expect(state.query.value.cursor).toBe('YWJj')
    state.latest(state.query.value.type, 100); expect(state.page.value).toBeNull(); await flush(); expect(state.query.value).toEqual({ type: '', limit: 100 })
    const calls = api.read.mock.calls.length; await vi.advanceTimersByTimeAsync(600_000); expect(api.read).toHaveBeenCalledTimes(calls)
  })
  it.each(['JOB_HISTORY_UNSUPPORTED', 'JOB_HISTORY_TOO_LARGE', 'JOB_HISTORY_CORRUPT', 'JOB_HISTORY_RESPONSE_INVALID', 'JOB_HISTORY_UNAVAILABLE'])('错误 %s 不当作空页', async code => {
    api.read.mockRejectedValueOnce(new NcpApiError(code, 'failure')); const { state } = setup(); await flush()
    expect(state.page.value).toBeNull(); expect(state.error.value?.code).toBe(code)
    state.retry(); await flush(); expect(api.read).toHaveBeenCalledTimes(code === 'JOB_HISTORY_UNSUPPORTED' ? 1 : 2)
  })
})
