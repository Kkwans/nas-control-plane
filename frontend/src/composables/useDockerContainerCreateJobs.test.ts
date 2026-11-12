import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { requestDockerContainerCreateJobs, requestDockerContainerCreateJob, requestDockerContainerCreateJobStatus, type DockerContainerCreateJobSnapshot } from '@/api/dockerContainerCreateJob'
import { containerCreateCanRelease, useDockerContainerCreateJobs } from './useDockerContainerCreateJobs'
vi.mock('@/api/dockerContainerCreateJob', () => ({ requestDockerContainerCreateJobs: vi.fn(), requestDockerContainerCreateJob: vi.fn(), requestDockerContainerCreateJobStatus: vi.fn(), isDockerContainerCreateAdmissionRejected: (error: unknown) => error === 'rejected' }))
const stamp = '2026-10-16T00:00:00Z', id = 'a'.repeat(64)
const input = { image: 'PRIVATE_IMAGE', name: 'PRIVATE_NAME', environment: { PASSWORD: 'PRIVATE_SECRET' }, runContainer: true }
function job(changes: Partial<DockerContainerCreateJobSnapshot> = {}): DockerContainerCreateJobSnapshot { return { id: 'job-1', type: 'docker-container-create', reference: '', status: 'queued', artifactState: 'unknown', cancellable: false, message: 'PRIVATE_MESSAGE', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...changes } }
function complete() { return job({ status: 'completed', progress: 100, completedAt: stamp, createResult: { containerId: id, created: true, started: true } }) }
const scopes: EffectScope[] = []
function mount() { const scope = effectScope(); scopes.push(scope); return { scope, state: scope.run(() => useDockerContainerCreateJobs())! } }
function deferred<T>() { let resolve!: (value: T) => void, reject!: (error: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const refresh = () => Promise.resolve(true)
async function accepted() { const mounted = mount(), entry = mounted.state.reserve(true)!; await mounted.state.dispatch(entry.token, input); return { ...mounted, entry } }
async function unknown() { vi.mocked(requestDockerContainerCreateJob).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); return accepted() }
beforeEach(() => { beginSession(); vi.resetAllMocks(); vi.mocked(requestDockerContainerCreateJob).mockResolvedValue(job()); vi.mocked(requestDockerContainerCreateJobStatus).mockResolvedValue(job()); vi.mocked(requestDockerContainerCreateJobs).mockResolvedValue({ jobs: [], invalidCount: 0 }) })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); invalidateSession() })
describe('创建会话单槽与安全结果消费', () => {
  it('跨scope单槽，只有拥有scope可以取消/派发，卸载仅释放自己的确认', async () => {
    const a = mount(), b = mount(), entry = a.state.reserve(true)!
    expect(Object.isFrozen(entry)).toBe(true); expect(b.state.reserve(false)).toBeNull()
    b.state.cancel(entry); await b.state.dispatch(entry, input); expect(requestDockerContainerCreateJob).not.toHaveBeenCalled()
    b.scope.stop(); expect(a.state.blocked.value).toBe(true)
    a.scope.stop(); expect(a.state.entry.value).toBeNull()
  })
  it('意图不匹配不发送，双击仅POST一次，submitting永远不能解除', async () => {
    const state = mount().state, entry = state.reserve(true)!, pending = deferred<DockerContainerCreateJobSnapshot>()
    await state.dispatch(entry, { ...input, runContainer: false }); expect(requestDockerContainerCreateJob).not.toHaveBeenCalled()
    vi.mocked(requestDockerContainerCreateJob).mockReturnValueOnce(pending.promise)
    const sent = state.dispatch(entry, input); await state.dispatch(entry, input)
    state.cancel(entry); expect(state.release(entry, 0)).toBe(false); expect(state.blocked.value).toBe(true)
    await state.check(entry, refresh); expect(requestDockerContainerCreateJobs).not.toHaveBeenCalled(); expect(requestDockerContainerCreateJobStatus).not.toHaveBeenCalled()
    pending.resolve(job()); await sent; expect(requestDockerContainerCreateJob).toHaveBeenCalledOnce()
  })
  it.each(['accepted', 'unknown'] as const)('卸载后晚%s回原会话', async outcome => {
    const pending = deferred<DockerContainerCreateJobSnapshot>(); vi.mocked(requestDockerContainerCreateJob).mockReturnValueOnce(pending.promise)
    const a = mount(), entry = a.state.reserve(true)!, sent = a.state.dispatch(entry, input); a.scope.stop()
    const b = mount().state; expect(b.entry.value?.phase).toBe('submitting')
    if (outcome === 'accepted') pending.resolve(job()); else pending.reject(new Error('PRIVATE_ERROR'))
    await sent; expect(b.entry.value?.phase).toBe(outcome === 'accepted' ? 'known' : 'unknown'); expect(b.reserve(true)).toBeNull()
  })
  it.each(['accepted', 'unknown', 'rejected'] as const)('会话失效丢弃晚%s且不污染新槽', async outcome => {
    const pending = deferred<DockerContainerCreateJobSnapshot>(); vi.mocked(requestDockerContainerCreateJob).mockReturnValueOnce(pending.promise)
    const a = mount().state, old = a.reserve(true)!, sent = a.dispatch(old, input)
    beginSession(); const b = mount().state, next = b.reserve(false)!
    if (outcome === 'accepted') pending.resolve(job()); else pending.reject(outcome === 'rejected' ? 'rejected' : new Error('unknown'))
    await sent; expect(a.entry.value).toBeNull(); expect(b.entry.value?.token).toBe(next.token); expect(b.entry.value?.phase).toBe('confirming')
  })
  it.each(['queued', 'running', 'pending'] as const)('%s精确查询且不可解除', async status => {
    const { state, entry } = await accepted()
    vi.mocked(requestDockerContainerCreateJobStatus).mockResolvedValue(job(status === 'pending' ? { status: 'completed', persistenceStatus: 'pending' } : { status }))
    await state.check(entry, refresh)
    expect(requestDockerContainerCreateJobStatus).toHaveBeenCalledWith('job-1', true, expect.any(AbortSignal))
    expect(state.release(entry, 1)).toBe(false); expect(state.consumeCompletion(entry, 1)).toBeNull()
  })
  it('完成保留结果，清单失败不影响当前scope同步领取一次', async () => {
    const { state, entry } = await accepted(); vi.mocked(requestDockerContainerCreateJobStatus).mockResolvedValue(complete())
    await state.check(entry, async () => false)
    expect(state.entry.value?.phase).toBe('resolved'); expect(state.reserve(false)).toBeNull(); expect(state.release(entry, 1)).toBe(false)
    expect(state.consumeCompletion(entry, 0)).toBeNull()
    const b = mount().state; expect(b.consumeCompletion(entry.token, 1)).toEqual({ containerId: id, created: true, started: true })
    expect(state.consumeCompletion(entry, 1)).toBeNull(); expect(state.reserve(false)).not.toBeNull()
  })
  it('旧scope卸载后GET完成仍可供新scope领取，不调用旧清单回调', async () => {
    const { state, scope, entry } = await accepted(), pending = deferred<DockerContainerCreateJobSnapshot>(), read = vi.fn(refresh)
    vi.mocked(requestDockerContainerCreateJobStatus).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, read); scope.stop(); const b = mount().state
    pending.resolve(complete()); await checking
    expect(read).not.toHaveBeenCalled(); expect(state.consumeCompletion(entry, 1)).toBeNull()
    expect(b.entry.value?.phase).toBe('resolved'); expect(b.consumeCompletion(entry, 1)?.containerId).toBe(id)
  })
  it('领取后旧清单晚结果不污染新slot', async () => {
    const { state, entry } = await accepted(), pending = deferred<boolean>()
    vi.mocked(requestDockerContainerCreateJobStatus).mockResolvedValue(complete())
    const checking = state.check(entry, () => pending.promise); await Promise.resolve(); await Promise.resolve()
    expect(state.consumeCompletion(entry, 1)?.containerId).toBe(id)
    const next = state.reserve(false)!; pending.resolve(true); await checking
    expect(state.entry.value?.token).toBe(next.token); expect(state.entry.value?.phase).toBe('confirming'); expect(state.entry.value?.checked).toBe(false)
  })
  it.each(['failed', 'interrupted'] as const)('%s要求当轮精确成功+清单成功，过期确认与失败重读不能解除', async status => {
    const { state, entry } = await accepted(); vi.mocked(requestDockerContainerCreateJobStatus).mockResolvedValue(job({ status, completedAt: stamp }))
    await state.check(entry, async () => false); expect(state.release(entry, 1)).toBe(false)
    await state.check(entry, refresh); expect(containerCreateCanRelease(state.entry.value!)).toBe(true)
    vi.mocked(requestDockerContainerCreateJobStatus).mockRejectedValueOnce(new Error('404'))
    await state.check(entry, refresh); expect(state.entry.value?.checked).toBe(true); expect(state.entry.value?.lastReadSucceeded).toBe(false)
    expect(state.release(entry, 2)).toBe(false); expect(state.release(entry, 3)).toBe(false)
    await state.check(entry, refresh); expect(state.release(entry, 4)).toBe(true); expect(requestDockerContainerCreateJob).toHaveBeenCalledOnce()
  })
  it('坏completed或错误jobId保留保护', async () => {
    const { state, entry } = await accepted()
    for (const invalid of [job({ status: 'completed' }), { ...complete(), id: 'other' }, { ...complete(), createResult: { containerId: id, created: true as const, started: false } }]) {
      vi.mocked(requestDockerContainerCreateJobStatus).mockResolvedValueOnce(invalid)
      await state.check(entry, refresh); expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.entry.value?.phase).toBe('known'); expect(state.consumeCompletion(entry, state.entry.value!.checkVersion)).toBeNull()
    }
  })
  it('unknown不认领任务；任意create活动和损坏列表保留阻塞证据', async () => {
    const { state, entry } = await unknown()
    vi.mocked(requestDockerContainerCreateJobs).mockResolvedValueOnce({ jobs: [job({ id: 'unrelated' })], invalidCount: 0 })
    await state.check(entry, refresh); expect(state.entry.value?.job).toBeUndefined(); expect(state.entry.value?.observedActive).toBe(true)
    vi.mocked(requestDockerContainerCreateJobs).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true)
    vi.mocked(requestDockerContainerCreateJobs).mockResolvedValueOnce({ jobs: [], invalidCount: 1 }); await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true); expect(state.release(entry, 3)).toBe(false)
    vi.mocked(requestDockerContainerCreateJobs).mockResolvedValueOnce({ jobs: [complete()], invalidCount: 0 }); await state.check(entry, async () => false)
    expect(state.entry.value?.job).toBeUndefined(); expect(state.release(entry, 4)).toBe(false)
    await state.check(entry, refresh); expect(state.release(entry, 4)).toBe(false); expect(state.release(entry, 5)).toBe(true)
    expect(requestDockerContainerCreateJobStatus).not.toHaveBeenCalled(); expect(requestDockerContainerCreateJob).toHaveBeenCalledOnce()
  })
  it('首次损坏列表的pending活动证据保留', async () => {
    const { state, entry } = await unknown()
    vi.mocked(requestDockerContainerCreateJobs).mockResolvedValueOnce({ jobs: [job({ status: 'completed', persistenceStatus: 'pending' })], invalidCount: 1 })
    await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true); expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.release(entry, 1)).toBe(false)
  })
  it('并发核对只发起一轮，核对期间旧确认不能解除', async () => {
    const { state, entry } = await unknown(); await state.check(entry, refresh)
    const pending = deferred<{ jobs: DockerContainerCreateJobSnapshot[]; invalidCount: number }>(); vi.mocked(requestDockerContainerCreateJobs).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, refresh); await state.check(entry, refresh)
    expect(state.release(entry, 1)).toBe(false); expect(state.release(entry, 2)).toBe(false); expect(requestDockerContainerCreateJobs).toHaveBeenCalledTimes(2)
    pending.resolve({ jobs: [], invalidCount: 0 }); await checking; expect(state.release(entry, 2)).toBe(true)
  })
  it('核对中session abort不调用旧清单不污染新槽', async () => {
    const { state, entry } = await accepted(), pending = deferred<DockerContainerCreateJobSnapshot>(), read = vi.fn(refresh)
    vi.mocked(requestDockerContainerCreateJobStatus).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, read); const signal = vi.mocked(requestDockerContainerCreateJobStatus).mock.calls[0]![2]!
    beginSession(); const b = mount().state, next = b.reserve(false)!
    expect(signal.aborted).toBe(true); pending.resolve(complete()); await checking
    expect(read).not.toHaveBeenCalled(); expect(state.entry.value).toBeNull(); expect(b.entry.value?.token).toBe(next.token)
  })
  it('明确拒绝释放；registry不保存表单、任意文案或异常', async () => {
    const { state, entry } = await accepted(); expect(JSON.stringify(state.entry.value)).not.toContain('PRIVATE_')
    vi.mocked(requestDockerContainerCreateJobStatus).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); await state.check(entry, refresh)
    expect(JSON.stringify(state.entry.value)).not.toContain('PRIVATE_')
    beginSession(); const next = mount().state, token = next.reserve(true)!
    vi.mocked(requestDockerContainerCreateJob).mockRejectedValueOnce('rejected'); await next.dispatch(token, input); expect(next.entry.value).toBeNull()
  })
})
