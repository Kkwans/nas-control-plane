import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import type { JobSnapshot } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { requestJobs } from '@/api/docker'
import { requestDockerContainerActionJob, requestDockerContainerActionJobStatus } from '@/api/dockerContainerActionJob'
import { containerActionCanRelease, useDockerContainerActionJobs } from './useDockerContainerActionJobs'
vi.mock('@/api/docker', () => ({ requestJobs: vi.fn() }))
vi.mock('@/api/dockerContainerActionJob', () => ({ requestDockerContainerActionJob: vi.fn(), requestDockerContainerActionJobStatus: vi.fn(), isDockerContainerActionAdmissionRejected: (error: unknown) => error === 'rejected' }))
const id = 'a'.repeat(64)
const stamp = '2026-10-16T00:00:00Z'
function job(changes: Partial<JobSnapshot> = {}): JobSnapshot { return { id: 'job-1', type: 'docker-container-restart', reference: id, status: 'queued', artifactState: 'unknown', cancellable: false, message: '', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...changes } }
const scopes: EffectScope[] = []
function mount() { const scope = effectScope(); scopes.push(scope); return { scope, state: scope.run(() => useDockerContainerActionJobs())! } }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes }); return { promise, resolve } }
beforeEach(() => { beginSession(); vi.resetAllMocks(); vi.mocked(requestDockerContainerActionJob).mockResolvedValue(job()); vi.mocked(requestJobs).mockResolvedValue({ jobs: [], invalidCount: 0 }); vi.mocked(requestDockerContainerActionJobStatus).mockResolvedValue(job()) })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); invalidateSession() })
const refresh = () => Promise.resolve(true)
describe('登录会话拥有单容器动作保护', () => {
  it('确认前冻结完整目标，全部动作和多实例共享；取消仅释放自己未派发锁', () => {
    const a = mount().state, b = mount().state
    const entry = a.reserve(id, 'restart', 'old name')!
    expect(Object.isFrozen(entry)).toBe(true)
    for (const action of ['start', 'stop', 'restart'] as const) expect(b.reserve(id, action, 'new')).toBeNull()
    expect(b.reserve('short-id', 'restart', 'name')).toBeNull()
    a.cancel(entry); expect(b.blockedIds.value).toEqual([])
    expect(requestDockerContainerActionJob).not.toHaveBeenCalled()
  })
  it('卸载仅取消本实例确认，不释放其他实例确认', () => {
    const a = mount(), b = mount(); const entry = a.state.reserve(id, 'stop', 'name')!
    b.scope.stop(); expect(a.state.blockedIds.value).toEqual([id])
    a.scope.stop(); expect(a.state.entries.value).toEqual([])
    a.state.cancel(entry)
  })
  it('派发后卸载仍接收晚202并跨路由阻止全部动作；取消不能解锁', async () => {
    const pending = deferred<JobSnapshot>(); vi.mocked(requestDockerContainerActionJob).mockReturnValue(pending.promise)
    const a = mount(), entry = a.state.reserve(id, 'restart', 'name')!
    const sent = a.state.dispatch(entry); a.state.cancel(entry); a.scope.stop()
    const b = mount().state
    expect(b.entries.value[0]?.phase).toBe('submitting')
    expect(b.reserve(id, 'start', 'name')).toBeNull()
    pending.resolve(job()); await sent
    expect(b.entries.value[0]?.job?.id).toBe('job-1')
    expect(requestDockerContainerActionJob).toHaveBeenCalledTimes(1)
    expect(requestDockerContainerActionJobStatus).not.toHaveBeenCalled()
  })
  it('会话切换清空旧锁，晚202不能污染新会话同ID新锁', async () => {
    const pending = deferred<JobSnapshot>(); vi.mocked(requestDockerContainerActionJob).mockReturnValueOnce(pending.promise)
    const a = mount().state, old = a.reserve(id, 'restart', 'old')!; const sent = a.dispatch(old)
    beginSession(); const b = mount().state, next = b.reserve(id, 'stop', 'new')!
    pending.resolve(job()); await sent
    expect(a.entries.value).toEqual([]); expect(b.entries.value[0]?.token).toBe(next.token); expect(b.entries.value[0]?.phase).toBe('confirming')
  })
  it.each(['queued', 'running', 'persisting'] as const)('%s不释放且精确jobId核对', async status => {
    const state = mount().state, entry = state.reserve(id, 'restart', 'name')!; await state.dispatch(entry)
    vi.mocked(requestDockerContainerActionJobStatus).mockResolvedValue(job(status === 'persisting' ? { status: 'completed', persistenceStatus: 'pending' } : { status }))
    await state.check(entry, refresh)
    expect(requestDockerContainerActionJobStatus).toHaveBeenCalledWith('job-1', id, 'restart', expect.any(AbortSignal))
    expect(containerActionCanRelease(state.entries.value[0]!)).toBe(false)
    expect(state.release(entry, 1)).toBe(false)
  })
  it('404或核对失败不释放；精确completed读回后才释放并只读刷新', async () => {
    const state = mount().state, entry = state.reserve(id, 'restart', 'name')!; await state.dispatch(entry)
    vi.mocked(requestDockerContainerActionJobStatus).mockRejectedValueOnce(new Error('404'))
    await state.check(entry, refresh); expect(state.blockedIds.value).toEqual([id])
    vi.mocked(requestDockerContainerActionJobStatus).mockResolvedValueOnce(job({ status: 'completed', progress: 100, completedAt: stamp }))
    const read = vi.fn(refresh); await state.check(entry, read)
    expect(state.entries.value).toEqual([]); expect(read).toHaveBeenCalledOnce(); expect(requestDockerContainerActionJob).toHaveBeenCalledOnce()
  })
  it.each(['failed', 'interrupted'] as const)('%s必须清单核对成功并风险确认，过期确认不释放', async status => {
    const state = mount().state, entry = state.reserve(id, 'restart', 'name')!; await state.dispatch(entry)
    vi.mocked(requestDockerContainerActionJobStatus).mockResolvedValue(job({ status, completedAt: stamp, errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }))
    await state.check(entry, async () => false); expect(containerActionCanRelease(state.entries.value[0]!)).toBe(false)
    await state.check(entry, refresh); const version = state.entries.value[0]!.checkVersion
    expect(containerActionCanRelease(state.entries.value[0]!)).toBe(true)
    await state.check(entry, refresh); expect(state.release(entry, version)).toBe(false)
    expect(state.release(entry, state.entries.value[0]!.checkVersion)).toBe(true)
    expect(requestDockerContainerActionJob).toHaveBeenCalledOnce()
  })
  it('未知接收只观察最近任务且不认领，活动任务阻止解除，清单失败不伪装未执行', async () => {
    vi.mocked(requestDockerContainerActionJob).mockRejectedValue(new Error('network secret'))
    const state = mount().state, entry = state.reserve(id, 'restart', 'name')!; await state.dispatch(entry)
    expect(state.release(entry, 0)).toBe(false)
    vi.mocked(requestJobs).mockResolvedValueOnce({ jobs: [job({ id: 'other', type: 'docker-container-stop' })], invalidCount: 0 })
    await state.check(entry, refresh); expect(state.entries.value[0]?.job).toBeUndefined(); expect(state.release(entry, 1)).toBe(false)
    vi.mocked(requestJobs).mockRejectedValueOnce(new Error('offline'))
    await state.check(entry, refresh); expect(state.release(entry, 2)).toBe(false)
    vi.mocked(requestJobs).mockResolvedValueOnce({ jobs: [job({ status: 'completed' })], invalidCount: 0 })
    await state.check(entry, async () => false)
    expect(state.entries.value[0]?.notice).toContain('刷新失败')
    expect(state.release(entry, 3)).toBe(true)
    expect(requestDockerContainerActionJob).toHaveBeenCalledOnce(); expect(requestDockerContainerActionJobStatus).not.toHaveBeenCalled()
  })
  it('最近任务含损坏记录时仍保留已观察活动任务，不能风险解除', async () => {
    vi.mocked(requestDockerContainerActionJob).mockRejectedValue(new Error('unknown'))
    const state = mount().state, entry = state.reserve(id, 'restart', 'name')!; await state.dispatch(entry)
    vi.mocked(requestJobs).mockResolvedValueOnce({ jobs: [job()], invalidCount: 1 })
    await state.check(entry, refresh)
    expect(state.entries.value[0]?.observedActive).toBe(true)
    expect(state.release(entry, 1)).toBe(false)
  })
  it('明确接收拒绝释放，接收未知不自动派发或解锁', async () => {
    const state = mount().state, entry = state.reserve(id, 'start', 'name')!
    vi.mocked(requestDockerContainerActionJob).mockRejectedValueOnce('rejected'); expect(await state.dispatch(entry)).toBe('rejected'); expect(state.entries.value).toEqual([])
    const again = state.reserve(id, 'start', 'name')!
    vi.mocked(requestDockerContainerActionJob).mockRejectedValueOnce(new Error('unknown')); await state.dispatch(again)
    expect(state.entries.value[0]?.phase).toBe('unknown'); state.cancel(again); expect(state.blockedIds.value).toEqual([id])
  })
})
