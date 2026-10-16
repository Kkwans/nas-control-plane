import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { captureDockerMutationFence } from './dockerMutationFence'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { requestComposeActionJobs, requestComposeActionJob, requestComposeActionJobStatus, type ComposeActionJobSnapshot } from '@/api/composeActionJob'
import { composeActionCanRelease, useComposeActionJobs } from './useComposeActionJobs'
vi.mock('@/api/composeActionJob', () => ({ normalizeComposeProjectId: (value: string) => value.trim(), requestComposeActionJobs: vi.fn(), requestComposeActionJob: vi.fn(), requestComposeActionJobStatus: vi.fn(), isComposeActionAdmissionRejected: (error: unknown) => error === 'rejected' }))
const stamp = '2026-10-16T00:00:00Z'
const input = { projectId: 'media', workingDirectory: '/vol1/media', configFiles: ['/vol1/media/compose.yaml', '/vol1/media/override.yaml'] }
function job(changes: Partial<ComposeActionJobSnapshot> = {}): ComposeActionJobSnapshot { return { id: 'job-1', type: 'compose-start', reference: 'media', status: 'queued', artifactState: 'unknown', cancellable: false, message: 'PRIVATE_MESSAGE', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...changes } }
function complete() { return job({ status: 'completed', progress: 100, completedAt: stamp, composeActionResult: { projectId: 'media', action: 'start', state: 'running', serviceCount: 2 } }) }
const scopes: EffectScope[] = []
function mount() { const scope = effectScope(); scopes.push(scope); return { scope, state: scope.run(() => useComposeActionJobs())! } }
function deferred<T>() { let resolve!: (value: T) => void, reject!: (error: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const refresh = () => Promise.resolve(true)
async function accepted() { const mounted = mount(), entry = mounted.state.reserve(input, 'start')!; await mounted.state.dispatch(entry.token); return { ...mounted, entry } }
async function unknown() { vi.mocked(requestComposeActionJob).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); return accepted() }
beforeEach(() => { beginSession(); vi.resetAllMocks(); vi.mocked(requestComposeActionJob).mockResolvedValue(job()); vi.mocked(requestComposeActionJobStatus).mockResolvedValue(job()); vi.mocked(requestComposeActionJobs).mockResolvedValue({ jobs: [], invalidCount: 0 }) })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); invalidateSession() })
describe('项目操作会话单槽与安全结果消费', () => {
  it('跨scope单槽，只有拥有scope可以取消/派发，卸载仅释放自己的确认', async () => {
    const a = mount(), b = mount(), entry = a.state.reserve(input, 'start')!
    expect(Object.isFrozen(entry)).toBe(true); expect(b.state.reserve(input, 'stop')).toBeNull()
    b.state.cancel(entry); await b.state.dispatch(entry); expect(requestComposeActionJob).not.toHaveBeenCalled()
    b.scope.stop(); expect(a.state.blocked.value).toBe(true)
    a.scope.stop(); expect(a.state.entry.value).toBeNull()
  })
  it('冻结意图，双击仅POST一次，submitting永远不能解除', async () => {
    const state = mount().state, entry = state.reserve(input, 'start')!, pending = deferred<ComposeActionJobSnapshot>()
    vi.mocked(requestComposeActionJob).mockReturnValueOnce(pending.promise)
    const sent = state.dispatch(entry); await state.dispatch(entry)
    state.cancel(entry); expect(state.release(entry, 0, true)).toBe(false); expect(state.blocked.value).toBe(true)
    await state.check(entry, refresh); expect(requestComposeActionJobs).not.toHaveBeenCalled(); expect(requestComposeActionJobStatus).not.toHaveBeenCalled()
    pending.resolve(job()); await sent; expect(requestComposeActionJob).toHaveBeenCalledOnce()
  })
  it.each(['accepted', 'unknown'] as const)('卸载后晚%s回原会话', async outcome => {
    const pending = deferred<ComposeActionJobSnapshot>(); vi.mocked(requestComposeActionJob).mockReturnValueOnce(pending.promise)
    const a = mount(), entry = a.state.reserve(input, 'start')!, sent = a.state.dispatch(entry); a.scope.stop()
    const b = mount().state; expect(b.entry.value?.phase).toBe('submitting')
    if (outcome === 'accepted') pending.resolve(job()); else pending.reject(new Error('PRIVATE_ERROR'))
    await sent; expect(b.entry.value?.phase).toBe(outcome === 'accepted' ? 'known' : 'unknown'); expect(b.reserve(input, 'start')).toBeNull()
  })
  it.each(['accepted', 'unknown', 'rejected'] as const)('会话失效丢弃晚%s且不污染新槽', async outcome => {
    const pending = deferred<ComposeActionJobSnapshot>(); vi.mocked(requestComposeActionJob).mockReturnValueOnce(pending.promise)
    const a = mount().state, old = a.reserve(input, 'start')!, sent = a.dispatch(old)
    beginSession(); const b = mount().state, next = b.reserve(input, 'stop')!
    if (outcome === 'accepted') pending.resolve(job()); else pending.reject(outcome === 'rejected' ? 'rejected' : new Error('unknown'))
    await sent; expect(a.entry.value).toBeNull(); expect(b.entry.value?.token).toBe(next.token); expect(b.entry.value?.phase).toBe('confirming')
  })
  it.each(['queued', 'running', 'pending'] as const)('%s精确查询且不可解除', async status => {
    const { state, entry } = await accepted()
    vi.mocked(requestComposeActionJobStatus).mockResolvedValue(job(status === 'pending' ? { status: 'completed', persistenceStatus: 'pending' } : { status }))
    await state.check(entry, refresh)
    expect(requestComposeActionJobStatus).toHaveBeenCalledWith('job-1', 'media', 'start', expect.any(AbortSignal))
    expect(state.release(entry, 1, true)).toBe(false); expect(state.consumeCompletion(entry, 1)).toBeNull()
  })
  it('完成保留结果，清单失败不影响当前scope同步领取一次', async () => {
    const { state, entry } = await accepted(); vi.mocked(requestComposeActionJobStatus).mockResolvedValue(complete())
    await state.check(entry, async () => false)
    expect(state.entry.value?.phase).toBe('resolved'); expect(state.reserve(input, 'stop')).toBeNull(); expect(state.release(entry, 1, true)).toBe(false)
    expect(state.consumeCompletion(entry, 0)).toBeNull()
    const b = mount().state; expect(b.consumeCompletion(entry.token, 1)).toEqual(complete().composeActionResult)
    expect(state.consumeCompletion(entry, 1)).toBeNull(); expect(state.reserve(input, 'stop')).not.toBeNull()
  })
  it('旧scope卸载后GET完成仍可供新scope领取，不调用旧清单回调', async () => {
    const { state, scope, entry } = await accepted(), pending = deferred<ComposeActionJobSnapshot>(), read = vi.fn(refresh)
    vi.mocked(requestComposeActionJobStatus).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, read); scope.stop(); const b = mount().state
    pending.resolve(complete()); await checking
    expect(read).not.toHaveBeenCalled(); expect(state.consumeCompletion(entry, 1)).toBeNull()
    expect(b.entry.value?.phase).toBe('resolved'); expect(b.consumeCompletion(entry, 1)?.serviceCount).toBe(2)
  })
  it('领取后旧清单晚结果不污染新slot', async () => {
    const { state, entry } = await accepted(), pending = deferred<boolean>()
    vi.mocked(requestComposeActionJobStatus).mockResolvedValue(complete())
    const checking = state.check(entry, () => pending.promise); await Promise.resolve(); await Promise.resolve()
    expect(state.consumeCompletion(entry, 1)?.serviceCount).toBe(2)
    const next = state.reserve(input, 'stop')!; pending.resolve(true); await checking
    expect(state.entry.value?.token).toBe(next.token); expect(state.entry.value?.phase).toBe('confirming'); expect(state.entry.value?.checked).toBe(false)
  })
  it.each(['failed', 'interrupted'] as const)('%s要求当轮精确成功+清单成功，过期确认与失败重读不能解除', async status => {
    const { state, entry } = await accepted(); vi.mocked(requestComposeActionJobStatus).mockResolvedValue(job({ status, completedAt: stamp }))
    await state.check(entry, async () => false); expect(state.release(entry, 1, true)).toBe(false)
    await state.check(entry, refresh); expect(composeActionCanRelease(state.entry.value!)).toBe(true)
    vi.mocked(requestComposeActionJobStatus).mockRejectedValueOnce(new Error('404'))
    await state.check(entry, refresh); expect(state.entry.value?.checked).toBe(true); expect(state.entry.value?.lastReadSucceeded).toBe(false)
    expect(state.release(entry, 2, true)).toBe(false); expect(state.release(entry, 3, true)).toBe(false)
    await state.check(entry, refresh); expect(state.release(entry, 4, true)).toBe(true); expect(requestComposeActionJob).toHaveBeenCalledOnce()
  })
  it('坏completed或错误jobId保留保护', async () => {
    const { state, entry } = await accepted()
    for (const invalid of [job({ status: 'completed' }), { ...complete(), id: 'other' }, { ...complete(), composeActionResult: { ...complete().composeActionResult!, action: 'stop' as const } }]) {
      vi.mocked(requestComposeActionJobStatus).mockResolvedValueOnce(invalid)
      await state.check(entry, refresh); expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.entry.value?.phase).toBe('known'); expect(state.consumeCompletion(entry, state.entry.value!.checkVersion)).toBeNull()
    }
  })
  it('unknown不认领任务；任意Compose活动和损坏列表保留阻塞证据', async () => {
    const { state, entry } = await unknown()
    vi.mocked(requestComposeActionJobs).mockResolvedValueOnce({ jobs: [job({ id: 'unrelated' })], invalidCount: 0 })
    await state.check(entry, refresh); expect(state.entry.value?.job).toBeUndefined(); expect(state.entry.value?.observedActive).toBe(true)
    vi.mocked(requestComposeActionJobs).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true)
    vi.mocked(requestComposeActionJobs).mockResolvedValueOnce({ jobs: [], invalidCount: 1 }); await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true); expect(state.release(entry, 3, true)).toBe(false)
    vi.mocked(requestComposeActionJobs).mockResolvedValueOnce({ jobs: [complete()], invalidCount: 0 }); await state.check(entry, async () => false)
    expect(state.entry.value?.job).toBeUndefined(); expect(state.release(entry, 4, true)).toBe(false)
    await state.check(entry, refresh); expect(state.release(entry, 4, true)).toBe(false); expect(state.release(entry, 5, true)).toBe(true)
    expect(requestComposeActionJobStatus).not.toHaveBeenCalled(); expect(requestComposeActionJob).toHaveBeenCalledOnce()
  })
  it('首次损坏列表的pending活动证据保留', async () => {
    const { state, entry } = await unknown()
    vi.mocked(requestComposeActionJobs).mockResolvedValueOnce({ jobs: [job({ status: 'completed', persistenceStatus: 'pending' })], invalidCount: 1 })
    await state.check(entry, refresh); expect(state.entry.value?.observedActive).toBe(true); expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.release(entry, 1, true)).toBe(false)
  })
  it('并发核对只发起一轮，核对期间旧确认不能解除', async () => {
    const { state, entry } = await unknown(); await state.check(entry, refresh)
    const pending = deferred<{ jobs: ComposeActionJobSnapshot[]; invalidCount: number }>(); vi.mocked(requestComposeActionJobs).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, refresh); await state.check(entry, refresh)
    expect(state.release(entry, 1, true)).toBe(false); expect(state.release(entry, 2, true)).toBe(false); expect(requestComposeActionJobs).toHaveBeenCalledTimes(2)
    pending.resolve({ jobs: [], invalidCount: 0 }); await checking; expect(state.release(entry, 2, true)).toBe(true)
  })
  it('核对中session abort不调用旧清单不污染新槽', async () => {
    const { state, entry } = await accepted(), pending = deferred<ComposeActionJobSnapshot>(), read = vi.fn(refresh)
    vi.mocked(requestComposeActionJobStatus).mockReturnValueOnce(pending.promise)
    const checking = state.check(entry, read); const signal = vi.mocked(requestComposeActionJobStatus).mock.calls[0]![3]!
    beginSession(); const b = mount().state, next = b.reserve(input, 'stop')!
    expect(signal.aborted).toBe(true); pending.resolve(complete()); await checking
    expect(read).not.toHaveBeenCalled(); expect(state.entry.value).toBeNull(); expect(b.entry.value?.token).toBe(next.token)
  })
  it('明确拒绝释放；registry不保存表单、任意文案或异常', async () => {
    const { state, entry } = await accepted(); expect(JSON.stringify(state.entry.value)).not.toContain('PRIVATE_')
    vi.mocked(requestComposeActionJobStatus).mockRejectedValueOnce(new Error('PRIVATE_ERROR')); await state.check(entry, refresh)
    expect(JSON.stringify(state.entry.value)).not.toContain('PRIVATE_')
    beginSession(); const next = mount().state, token = next.reserve(input, 'start')!
    vi.mocked(requestComposeActionJob).mockRejectedValueOnce('rejected'); await next.dispatch(token); expect(next.entry.value).toBeNull()
  })
})

describe('Compose 冻结意图、最小证据和共享围栏', () => {
  it('冻结规范项目、显示名、路径和文件顺序；伪造 handle 不改变派发', async () => {
    const state = mount().state, source = { ...input, projectId: ' media ', configFiles: [...input.configFiles] }
    const entry = state.reserve(source, 'restart', '媒体项目')!
    source.projectId = 'other'; source.workingDirectory = '/vol1/other'; source.configFiles.reverse()
    await state.dispatch({ ...entry, action: 'stop', input: source })
    expect(requestComposeActionJob).toHaveBeenCalledExactlyOnceWith(input, 'restart')
    expect(entry.projectId).toBe('media'); expect(entry.displayName).toBe('媒体项目')
    expect(entry.configFiles).toEqual(input.configFiles); expect(Object.isFrozen(entry.input)).toBe(true); expect(Object.isFrozen(entry.configFiles)).toBe(true)
  })
  it('拒绝空、稀疏、重复、过量路径和非法身份', () => {
    const state = mount().state
    for (const configFiles of [[], Array<string>(1), [input.configFiles[0]!, ...Array<string>(1)], [''], ['x', 'x'], Array.from({ length: 33 }, (_, i) => `${i}.yaml`)]) expect(state.reserve({ ...input, configFiles }, 'start')).toBeNull()
    for (const projectId of ['', ' ', 'x\n', '\ud800', '中'.repeat(86)]) expect(state.reserve({ ...input, projectId }, 'start')).toBeNull()
    expect(state.reserve(input, 'remove' as never)).toBeNull()
  })
  it.each(['start', 'stop', 'restart'] as const)('未知接收观察所有 %s 项目而不认领', async action => {
    const { state, entry } = await unknown()
    vi.mocked(requestComposeActionJobs).mockResolvedValueOnce({ jobs: [{ id: 'foreign', type: `compose-${action}`, reference: 'different-project', status: 'running', persistenceStatus: 'pending' }], invalidCount: 0 })
    await state.check(entry, refresh)
    expect(state.entry.value?.job).toBeUndefined(); expect(state.entry.value?.observedActive).toBe(true); expect(state.release(entry, 1, true)).toBe(false)
  })
  it('聚合完成证据深复制，不保留任意文案或额外服务数据', async () => {
    const { state, entry } = await accepted(), snapshot = complete()
    Object.assign(snapshot.composeActionResult!, { services: ['PRIVATE_SERVICE'], output: 'PRIVATE_OUTPUT' })
    vi.mocked(requestComposeActionJobStatus).mockResolvedValueOnce(snapshot); await state.check(entry, refresh)
    snapshot.composeActionResult!.serviceCount = 999
    const result = state.consumeCompletion(entry, 1)!
    expect(result).toEqual({ projectId: 'media', action: 'start', state: 'running', serviceCount: 2 }); expect(Object.isFrozen(result)).toBe(true)
    expect(JSON.stringify(result)).not.toContain('PRIVATE_'); expect(state.consumeCompletion(entry, 1)).toBeNull()
  })
  it('聚合回执身份、动作、状态与数量坏值均保留防重复保护', async () => {
    const { state, entry } = await accepted()
    for (const changes of [{ projectId: 'other' }, { action: 'stop' as const }, { state: 'stopped' as const }, { serviceCount: 0 }, { serviceCount: 1025 }, { serviceCount: 1.5 }]) {
      vi.mocked(requestComposeActionJobStatus).mockResolvedValueOnce(job({ status: 'completed', composeActionResult: { ...complete().composeActionResult!, ...changes } }))
      await state.check(entry, refresh); expect(state.entry.value?.lastReadSucceeded).toBe(false); expect(state.blocked.value).toBe(true)
    }
  })
  it('整个登记生命期持有compose租约，其他普通变更继续共享', async () => {
    const fence = captureDockerMutationFence(), state = mount().state, entry = state.reserve(input, 'start')!, other = Symbol()
    expect(fence.reserve('standalone', Symbol())).toBe(false)
    expect(fence.reserve('container', other)).toBe(true); expect(fence.release('container', other)).toBe(true)
    vi.mocked(requestComposeActionJob).mockRejectedValueOnce(new Error('unknown')); await state.dispatch(entry)
    expect(fence.reserve('standalone', Symbol())).toBe(false)
    await state.check(entry, refresh); expect(state.release(entry, 1, false)).toBe(false); expect(fence.standaloneBlocked.value).toBe(true)
    expect(state.release(entry, 1, true)).toBe(true); expect(fence.standaloneBlocked.value).toBe(false)
  })
  it('独立项目租约阻止准入，普通租约不阻止Compose', () => {
    const fence = captureDockerMutationFence(), token = Symbol(), state = mount().state
    expect(fence.reserve('standalone', token)).toBe(true); expect(state.blocked.value).toBe(true); expect(state.reserve(input, 'start')).toBeNull()
    fence.release('standalone', token); expect(fence.reserve('delete', token)).toBe(true)
    const entry = state.reserve(input, 'start')!; expect(entry).not.toBeNull(); state.cancel(entry)
    expect(fence.owns('delete', token)).toBe(true); expect(fence.otherMutationsBlocked.value).toBe(false)
  })
})
