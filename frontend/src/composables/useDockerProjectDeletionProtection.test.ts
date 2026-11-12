import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { deleteDockerProject } from '@/api/docker'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { projectDeletionCanRelease, useDockerProjectDeletionProtection } from './useDockerProjectDeletionProtection'
vi.mock('@/api/docker', () => ({ deleteDockerProject: vi.fn(), isDockerProjectDeleteRejected: (error: unknown) => error === 'rejected' }))
const project = { id: '__proto__', name: 'demo', kind: 'compose' as const }
const scopes: EffectScope[] = []
function mount() { const scope = effectScope(); scopes.push(scope); return { scope, state: scope.run(useDockerProjectDeletionProtection)! } }
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
beforeEach(() => { beginSession(); vi.resetAllMocks(); vi.mocked(deleteDockerProject).mockRejectedValue(new Error('secret raw transport error')) })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); invalidateSession() })
describe('项目删除会话防重复保护', () => {
  it('精确 Map 身份不受原型键影响；跨实例确认去重，只能撤销自己的确认', () => {
    const a = mount(), b = mount(); const entry = a.state.reserve(project)!
    expect(entry.projectId).toBe('__proto__'); expect(Object.isFrozen(entry)).toBe(true)
    expect(b.state.reserve(project)).toBeNull(); b.state.cancel(entry); b.scope.stop()
    expect(a.state.blockedIds.value).toEqual([project.id])
    a.scope.stop(); expect(a.state.entries.value).toEqual([])
    expect(deleteDockerProject).not.toHaveBeenCalled()
  })
  it('身份冻结且仅派发一次；派发后取消与卸载不解锁，晚异常写回原会话', async () => {
    const pending = deferred<Awaited<ReturnType<typeof deleteDockerProject>>>()
    vi.mocked(deleteDockerProject).mockReturnValue(pending.promise)
    const a = mount(), mutable = { ...project, configFiles: ['secret'] }, entry = a.state.reserve(mutable)!
    mutable.id = 'changed'; mutable.name = 'changed'
    const sent = a.state.dispatch(entry)
    await a.state.dispatch(entry); a.state.cancel(entry); a.scope.stop()
    const b = mount().state
    expect(b.reserve(project)).toBeNull(); expect(b.entries.value[0]?.phase).toBe('submitting')
    expect(deleteDockerProject).toHaveBeenCalledExactlyOnceWith(project)
    pending.reject(new Error('raw secret')); await sent
    expect(b.entries.value[0]?.phase).toBe('unknown')
    expect(JSON.stringify(b.entries.value)).not.toContain('secret')
    expect(Object.keys(b.entries.value[0]!)).not.toContain('configFiles')
  })
  it('旧会话迟到结果不能污染新会话同 ID 锁', async () => {
    const pending = deferred<Awaited<ReturnType<typeof deleteDockerProject>>>()
    vi.mocked(deleteDockerProject).mockReturnValueOnce(pending.promise)
    const a = mount().state, old = a.reserve(project)!; const sent = a.dispatch(old)
    beginSession(); const b = mount().state, fresh = b.reserve(project)!
    pending.reject(new Error('late')); await sent
    expect(a.entries.value).toEqual([]); expect(b.entries.value[0]?.token).toBe(fresh.token); expect(b.entries.value[0]?.phase).toBe('confirming')
  })
  it.each(['deleted', 'rejected'] as const)('只有严格 %s 释放保护', async outcome => {
    if (outcome === 'deleted') vi.mocked(deleteDockerProject).mockResolvedValue({} as never)
    else vi.mocked(deleteDockerProject).mockRejectedValue('rejected')
    const state = mount().state, entry = state.reserve(project)!
    expect(await state.dispatch(entry)).toBe(outcome); expect(state.entries.value).toEqual([])
  })
  it('只读成功不自动解锁；失败禁止解除；风险解除绑定 token 和本轮 version', async () => {
    const state = mount().state, entry = state.reserve(project)!; await state.dispatch(entry)
    expect(state.release(entry, 0)).toBe(false)
    await state.check(entry, async () => false); expect(projectDeletionCanRelease(state.entries.value[0]!)).toBe(false)
    await state.check(entry, async () => true); const version = state.entries.value[0]!.checkVersion
    expect(state.entries.value[0]?.notice).toContain('项目注册状态可能仍需人工核对')
    expect(state.blockedIds.value).toEqual([project.id])
    await state.check(entry, async () => true)
    expect(state.release(entry, version)).toBe(false)
    expect(state.release(entry, state.entries.value[0]!.checkVersion)).toBe(true)
    const next = state.reserve(project)!; state.cancel(entry); expect(state.entries.value[0]?.token).toBe(next.token)
    expect(deleteDockerProject).toHaveBeenCalledOnce()
  })
  it('核对 singleflight，进行中立即作废上次解除资格', async () => {
    const state = mount().state, entry = state.reserve(project)!; await state.dispatch(entry)
    await state.check(entry, async () => true)
    const pending = deferred<boolean>(), read = vi.fn(() => pending.promise)
    const checking = state.check(entry, read); await state.check(entry, read)
    expect(read).toHaveBeenCalledOnce(); expect(projectDeletionCanRelease(state.entries.value[0]!)).toBe(false)
    pending.resolve(false); await checking; expect(state.release(entry, 2)).toBe(false)
  })
  it.each([undefined, 'true', 1])('只读非布尔真值 %s 不能开放风险解除', async value => {
    const state = mount().state, entry = state.reserve(project)!; await state.dispatch(entry)
    await state.check(entry, async () => value as unknown as boolean)
    expect(state.entries.value[0]?.inventoryChecked).toBe(false)
    expect(state.release(entry, 1)).toBe(false)
  })

})
