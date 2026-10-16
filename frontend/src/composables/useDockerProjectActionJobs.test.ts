import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { requestDockerProjectActionJobs, requestDockerProjectActionJob, requestDockerProjectActionJobStatus, type DockerProjectActionJobSnapshot } from '@/api/dockerProjectActionJob'
import { projectActionCanRelease, useDockerProjectActionJobs } from './useDockerProjectActionJobs'
vi.mock('@/api/dockerProjectActionJob', () => ({ requestDockerProjectActionJobs: vi.fn(), requestDockerProjectActionJob: vi.fn(), requestDockerProjectActionJobStatus: vi.fn(), isDockerProjectActionAdmissionRejected: (error: unknown) => error === 'rejected' }))
const stamp = '2026-10-16T00:00:00Z', id = 'a'.repeat(64)
const ids = [id, 'b'.repeat(64)]
function job(changes: Partial<DockerProjectActionJobSnapshot> = {}): DockerProjectActionJobSnapshot { return { id: 'job-1', type: 'docker-project-start', reference: 'standalone', status: 'queued', artifactState: 'unknown', cancellable: false, message: 'PRIVATE_MESSAGE', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...changes } }
function complete() { return job({ status: 'completed', progress: 100, completedAt: stamp, projectActionResult: { projectId: 'standalone', kind: 'standalone', action: 'start', items: ids.map(containerId => ({ containerId, outcome: 'succeeded', state: 'running' })) } }) }
const scopes: EffectScope[] = []
function mount() { const scope = effectScope(); scopes.push(scope); return { scope, state: scope.run(() => useDockerProjectActionJobs())! } }
function deferred<T>() { let resolve!: (value: T) => void, reject!: (error: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const refresh = () => Promise.resolve(true)
async function accepted() { const mounted = mount(), entry = mounted.state.reserve(ids, 'start')!; await mounted.state.dispatch(entry.token); return { ...mounted, entry } }
async function unknown() { vi.mocked(requestDockerProjectActionJob).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); return accepted() }
beforeEach(() => { beginSession(); vi.resetAllMocks(); vi.mocked(requestDockerProjectActionJob).mockResolvedValue(job()); vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValue(job()); vi.mocked(requestDockerProjectActionJobs).mockResolvedValue({ jobs: [], invalidCount: 0 }) })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); invalidateSession() })
describe('项目操作会话单槽与安全结果消费', () => {
  it('跨scope单槽，只有拥有scope可以取消/派发，卸载仅释放自己的确认', async () => {
    const a = mount(), b = mount(), entry = a.state.reserve(ids, 'start')!
    expect(Object.isFrozen(entry)).toBe(true); expect(b.state.reserve(ids, 'stop')).toBeNull()
    b.state.cancel(entry); await b.state.dispatch(entry); expect(requestDockerProjectActionJob).not.toHaveBeenCalled()
    b.scope.stop(); expect(a.state.blocked.value).toBe(true)
    a.scope.stop(); expect(a.state.entry.value).toBeNull()
  })
  it('冻结意图，双击仅POST一次，submitting永远不能解除', async () => {
    const state = mount().state, entry = state.reserve(ids, 'start')!, pending = deferred<DockerProjectActionJobSnapshot>()
    vi.mocked(requestDockerProjectActionJob).mockReturnValueOnce(pending.promise)
    const sent = state.dispatch(entry); await state.dispatch(entry)
    state.cancel(entry); expect(state.release(entry, 0, true)).toBe(false); expect(state.blocked.value).toBe(true)
    await state.check(entry, refresh); expect(requestDockerProjectActionJobs).not.toHaveBeenCalled(); expect(requestDockerProjectActionJobStatus).not.toHaveBeenCalled()
    pending.resolve(job()); await sent; expect(requestDockerProjectActionJob).toHaveBeenCalledOnce()
  })
  it.each(['accepted', 'unknown'] as const)('卸载后晚%s回原会话', async outcome => {
    const pending = deferred<DockerProjectActionJobSnapshot>(); vi.mocked(requestDockerProjectActionJob).mockReturnValueOnce(pending.promise)
    const a = mount(), entry = a.state.reserve(ids, 'start')!, sent = a.state.dispatch(entry); a.scope.stop()
    const b = mount().state; expect(b.entry.value?.phase).toBe('submitting')
    if (outcome === 'accepted') pending.resolve(job()); else pending.reject(new Error('PRIVATE_ERROR'))
    await sent; expect(b.entry.value?.phase).toBe(outcome === 'accepted' ? 'known' : 'unknown'); expect(b.reserve(ids, 'start')).toBeNull()
  })
  it.each(['accepted', 'unknown', 'rejected'] as const)('会话失效丢弃晚%s且不污染新槽', async outcome => {
    const pending = deferred<DockerProjectActionJobSnapshot>(); vi.mocked(requestDockerProjectActionJob).mockReturnValueOnce(pending.promise)
    const a = mount().state, old = a.reserve(ids, 'start')!, sent = a.dispatch(old)
    beginSession(); const b = mount().state, next = b.reserve(ids, 'stop')!
    if (outcome === 'accepted') pending.resolve(job()); else pending.reject(outcome === 'rejected' ? 'rejected' : new Error('unknown'))
    await sent; expect(a.entry.value).toBeNull(); expect(b.entry.value?.token).toBe(next.token); expect(b.entry.value?.phase).toBe('confirming')
  })
  it.each(['queued', 'running', 'pending'] as const)('%s精确查询且不可解除', async status => {
    const { state, entry } = await accepted()
    vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValue(job(status === 'pending' ? { status: 'completed', persistenceStatus: 'pending' } : { status }))
    await state.check(entry, refresh)
    expect(requestDockerProjectActionJobStatus).toHaveBeenCalledWith('job-1', ids, 'start', expect.any(AbortSignal))
    expect(state.release(entry, 1, true)).toBe(false); expect(state.consumeCompletion(entry, 1)).toBeNull()
  })
  it('完成保留结果，清单失败不影响当前scope同步领取一次', async () => {
    const { state, entry } = await accepted(); vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValue(complete())
    await state.check(entry, async () => false)
    expect(state.entry.value?.phase).toBe('resolved'); expect(state.reserve(ids, 'stop')).toBeNull(); expect(state.release(entry, 1, true)).toBe(false)
    expect(state.consumeCompletion(entry, 0)).toBeNull()
    const b = mount().state; expect(b.consumeCompletion(entry.token, 1)).toEqual(complete().projectActionResult)
    expect(state.consumeCompletion(entry, 1)).toBeNull(); expect(state.reserve(ids, 'stop')).not.toBeNull()
  })
  it('旧scope卸载后GET完成仍可供新scope领取，不调用旧清单回调', async () => {
    const { state, scope, entry } = await accepted(), pending = deferred<DockerProjectActionJobSnapshot>(), read = vi.fn(refresh)
    vi.mocked(requestDockerProjectActionJobStatus).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, read); scope.stop(); const b = mount().state
    pending.resolve(complete()); await checking
    expect(read).not.toHaveBeenCalled(); expect(state.consumeCompletion(entry, 1)).toBeNull()
    expect(b.entry.value?.phase).toBe('resolved'); expect(b.consumeCompletion(entry, 1)?.items[0]?.containerId).toBe(id)
  })
  it('领取后旧清单晚结果不污染新slot', async () => {
    const { state, entry } = await accepted(), pending = deferred<boolean>()
    vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValue(complete())
    const checking = state.check(entry, () => pending.promise); await Promise.resolve(); await Promise.resolve()
    expect(state.consumeCompletion(entry, 1)?.items[0]?.containerId).toBe(id)
    const next = state.reserve(ids, 'stop')!; pending.resolve(true); await checking
    expect(state.entry.value?.token).toBe(next.token); expect(state.entry.value?.phase).toBe('confirming'); expect(state.entry.value?.checked).toBe(false)
  })
  it.each(['failed', 'interrupted'] as const)('%s要求当轮精确成功+清单成功，过期确认与失败重读不能解除', async status => {
    const { state, entry } = await accepted(); vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValue(job({ status, completedAt: stamp }))
    await state.check(entry, async () => false); expect(state.release(entry, 1, true)).toBe(false)
    await state.check(entry, refresh); expect(projectActionCanRelease(state.entry.value!)).toBe(true)
    vi.mocked(requestDockerProjectActionJobStatus).mockRejectedValueOnce(new Error('404'))
    await state.check(entry, refresh); expect(state.entry.value?.checked).toBe(true); expect(state.entry.value?.lastReadSucceeded).toBe(false)
    expect(state.release(entry, 2, true)).toBe(false); expect(state.release(entry, 3, true)).toBe(false)
    await state.check(entry, refresh); expect(state.release(entry, 4, true)).toBe(true); expect(requestDockerProjectActionJob).toHaveBeenCalledOnce()
  })
  it('坏completed或错误jobId保留保护', async () => {
    const { state, entry } = await accepted()
    for (const invalid of [job({ status: 'completed' }), { ...complete(), id: 'other' }, { ...complete(), projectActionResult: { ...complete().projectActionResult!, action: 'stop' as const } }]) {
      vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValueOnce(invalid)
      await state.check(entry, refresh); expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.entry.value?.phase).toBe('known'); expect(state.consumeCompletion(entry, state.entry.value!.checkVersion)).toBeNull()
    }
  })
  it('unknown不认领任务；任意create活动和损坏列表保留阻塞证据', async () => {
    const { state, entry } = await unknown()
    vi.mocked(requestDockerProjectActionJobs).mockResolvedValueOnce({ jobs: [job({ id: 'unrelated' })], invalidCount: 0 })
    await state.check(entry, refresh); expect(state.entry.value?.job).toBeUndefined(); expect(state.entry.value?.observedActive).toBe(true)
    vi.mocked(requestDockerProjectActionJobs).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true)
    vi.mocked(requestDockerProjectActionJobs).mockResolvedValueOnce({ jobs: [], invalidCount: 1 }); await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true); expect(state.release(entry, 3, true)).toBe(false)
    vi.mocked(requestDockerProjectActionJobs).mockResolvedValueOnce({ jobs: [complete()], invalidCount: 0 }); await state.check(entry, async () => false)
    expect(state.entry.value?.job).toBeUndefined(); expect(state.release(entry, 4, true)).toBe(false)
    await state.check(entry, refresh); expect(state.release(entry, 4, true)).toBe(false); expect(state.release(entry, 5, true)).toBe(true)
    expect(requestDockerProjectActionJobStatus).not.toHaveBeenCalled(); expect(requestDockerProjectActionJob).toHaveBeenCalledOnce()
  })
  it('首次损坏列表的pending活动证据保留', async () => {
    const { state, entry } = await unknown()
    vi.mocked(requestDockerProjectActionJobs).mockResolvedValueOnce({ jobs: [job({ status: 'completed', persistenceStatus: 'pending' })], invalidCount: 1 })
    await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true); expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.release(entry, 1, true)).toBe(false)
  })
  it('并发核对只发起一轮，核对期间旧确认不能解除', async () => {
    const { state, entry } = await unknown(); await state.check(entry, refresh)
    const pending = deferred<{ jobs: DockerProjectActionJobSnapshot[]; invalidCount: number }>(); vi.mocked(requestDockerProjectActionJobs).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, refresh); await state.check(entry, refresh)
    expect(state.release(entry, 1, true)).toBe(false); expect(state.release(entry, 2, true)).toBe(false); expect(requestDockerProjectActionJobs).toHaveBeenCalledTimes(2)
    pending.resolve({ jobs: [], invalidCount: 0 }); await checking; expect(state.release(entry, 2, true)).toBe(true)
  })
  it('核对中session abort不调用旧清单不污染新槽', async () => {
    const { state, entry } = await accepted(), pending = deferred<DockerProjectActionJobSnapshot>(), read = vi.fn(refresh)
    vi.mocked(requestDockerProjectActionJobStatus).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, read); const signal = vi.mocked(requestDockerProjectActionJobStatus).mock.calls[0]![3]!
    beginSession(); const b = mount().state, next = b.reserve(ids, 'stop')!
    expect(signal.aborted).toBe(true); pending.resolve(complete()); await checking
    expect(read).not.toHaveBeenCalled(); expect(state.entry.value).toBeNull(); expect(b.entry.value?.token).toBe(next.token)
  })
  it('明确拒绝释放；registry不保存表单、任意文案或异常', async () => {
    const { state, entry } = await accepted(); expect(JSON.stringify(state.entry.value)).not.toContain('PRIVATE_')
    vi.mocked(requestDockerProjectActionJobStatus).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); await state.check(entry, refresh)
    expect(JSON.stringify(state.entry.value)).not.toContain('PRIVATE_')
    beginSession(); const next = mount().state, token = next.reserve(ids, 'start')!
    vi.mocked(requestDockerProjectActionJob).mockRejectedValueOnce('rejected'); await next.dispatch(token); expect(next.entry.value).toBeNull()
  })
})

describe('冻结项目子集与安全最小证据', () => {
  it('校验完整唯一集合及动作；原数组或伪造 handle 不能更换冻结意图', async () => {
    const state = mount().state
    for (const invalid of [[], Array<string>(1), [id, ...Array<string>(1)], ['short'], [id, id], Array.from({ length: 129 }, (_, i) => i.toString(16).padStart(64, '0'))]) expect(state.reserve(invalid, 'start')).toBeNull()
    expect(state.reserve(ids, 'remove' as never)).toBeNull()
    const source = [...ids], entry = state.reserve(source, 'start')!
    source.reverse(); source.push('c'.repeat(64))
    expect(Object.isFrozen(entry.containerIds)).toBe(true)
    await state.dispatch({ ...entry, action: 'stop', containerIds: ['c'.repeat(64)] })
    expect(requestDockerProjectActionJob).toHaveBeenCalledExactlyOnceWith(ids, 'start')
    expect(state.entry.value?.containerIds).toEqual(ids)
  })
  it.each(['start', 'stop', 'restart'] as const)('unknown观察所有动作：%s 不可认领且 pending 阻止解除', async action => {
    const { state, entry } = await unknown()
    vi.mocked(requestDockerProjectActionJobs).mockResolvedValueOnce({ jobs: [{ id: 'other', type: `docker-project-${action}`, status: 'running', persistenceStatus: 'pending' }], invalidCount: 0 })
    await state.check(entry, refresh)
    expect(state.entry.value?.observedActive).toBe(true); expect(state.entry.value?.job).toBeUndefined()
    expect(state.release(entry, 1, true)).toBe(false)
  })
  it.each(['failed', 'interrupted'] as const)('%s的有效完整回执只展示最小项，不能当全成功领取', async status => {
    const { state, entry } = await accepted(), result = complete().projectActionResult!
    result.items[1] = status === 'failed'
      ? { containerId: ids[1]!, outcome: 'state-mismatch', state: 'stopped', errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }
      : { containerId: ids[1]!, outcome: 'unknown', state: 'unknown', errorCode: 'JOB_EXECUTION_INTERRUPTED' }
    Object.assign(result.items[0]!, { message: 'PRIVATE_MESSAGE', error: 'PRIVATE_ERROR' })
    result.items.reverse()
    vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValueOnce(job({ status, projectActionResult: result }))
    await state.check(entry, refresh)
    const stored = state.entry.value!.job!.projectActionResult!
    expect(stored.items.map(item => item.containerId)).toEqual(ids)
    expect(stored.items[0]!.outcome).toBe('succeeded')
    expect(JSON.stringify(stored)).not.toContain('PRIVATE_')
    expect(Object.isFrozen(stored)).toBe(true); expect(Object.isFrozen(stored.items)).toBe(true); expect(Object.isFrozen(stored.items[0])).toBe(true)
    result.items[0]!.state = 'running'; result.items.length = 0
    expect(stored.items).toHaveLength(2)
    expect(state.consumeCompletion(entry, 1)).toBeNull(); expect(state.blocked.value).toBe(true)
    expect(state.release(entry, 1, false)).toBe(false)
    expect(state.release(entry, 1, true)).toBe(true)
  })
  it('完整成功必须精确集合与每项结果一致，拒绝缺失、重复、额外、坏状态项', async () => {
    const { state, entry } = await accepted()
    const valid = complete().projectActionResult!
    const invalidItems = [valid.items.slice(0, 1), [valid.items[0]!, valid.items[0]!], [...valid.items, { ...valid.items[0]!, containerId: 'c'.repeat(64) }], [valid.items[0]!, { ...valid.items[1]!, state: 'stopped' as const }]]
    for (const items of invalidItems) {
      vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValueOnce(job({ status: 'completed', projectActionResult: { ...valid, items } }))
      await state.check(entry, refresh)
      expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.entry.value?.phase).toBe('known')
      expect(state.entry.value?.job?.projectActionResult).toBeUndefined(); expect(state.blocked.value).toBe(true)
    }
  })
  it('成功结果深复制，外部回执变更不影响一次领取', async () => {
    const { state, entry } = await accepted(), snapshot = complete()
    vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValueOnce(snapshot)
    await state.check(entry, refresh)
    snapshot.projectActionResult!.items[0]!.containerId = 'c'.repeat(64)
    snapshot.projectActionResult!.items.length = 0
    const consumed = state.consumeCompletion(entry, 1)!
    expect(consumed.items.map(item => item.containerId)).toEqual(ids)
    expect(Object.isFrozen(consumed.items[0])).toBe(true)
    expect(state.consumeCompletion(entry, 1)).toBeNull()
  })
  it('已观察活动的已知任务读失败不能清除活动证据或原任务', async () => {
    const { state, entry } = await accepted()
    await state.check(entry, refresh)
    vi.mocked(requestDockerProjectActionJobStatus).mockRejectedValueOnce(new Error('PRIVATE_ERROR'))
    await state.check(entry, refresh)
    expect(state.entry.value?.observedActive).toBe(true); expect(state.entry.value?.job?.id).toBe('job-1')
    expect(state.release(entry, 2, true)).toBe(false)
  })
})
