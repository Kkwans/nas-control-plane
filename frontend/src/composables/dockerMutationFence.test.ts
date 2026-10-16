import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, watchSyncEffect } from 'vue'
import { beginSession, captureSession, invalidateSession } from '@/session/sessionLifecycle'
import { captureDockerMutationFence, type DockerMutationKind } from './dockerMutationFence'

const sharedKinds = ['create', 'container', 'delete', 'compose'] as const
beforeEach(() => { beginSession() })
afterEach(() => { vi.restoreAllMocks(); invalidateSession() })

describe('Docker 会话变更准入', () => {
  it.each(sharedKinds)('独立操作与 %s 双向互斥，跨捕获共享状态', kind => {
    const a = captureDockerMutationFence(), b = captureDockerMutationFence()
    const standalone = Symbol(), shared = Symbol()
    expect(a.reserve('standalone', standalone)).toBe(true)
    expect(b.standaloneBlocked.value).toBe(true)
    expect(b.otherMutationsBlocked.value).toBe(true)
    expect(b.reserve(kind, shared)).toBe(false)
    expect(b.reserve('standalone', Symbol())).toBe(false)
    expect(a.release('standalone', standalone)).toBe(true)
    expect(b.reserve(kind, shared)).toBe(true)
    expect(a.standaloneBlocked.value).toBe(true)
    expect(a.otherMutationsBlocked.value).toBe(false)
    expect(a.reserve('standalone', standalone)).toBe(false)
    expect(b.release(kind, shared)).toBe(true)
    expect(a.reserve('standalone', standalone)).toBe(true)
  })

  it('共享类型及同类型可并发，直到所有共享租约释放才允许独立操作', () => {
    const state = captureDockerMutationFence()
    const tokens = [...sharedKinds, ...sharedKinds].map(kind => ({ kind, token: Symbol() }))
    for (const { kind, token } of tokens) expect(state.reserve(kind, token)).toBe(true)
    for (const [index, { kind, token }] of tokens.entries()) {
      expect(state.owns(kind, token)).toBe(true)
      expect(state.release(kind, token)).toBe(true)
      expect(state.standaloneBlocked.value).toBe(index < tokens.length - 1)
      expect(state.otherMutationsBlocked.value).toBe(false)
    }
    expect(state.reserve('standalone', Symbol())).toBe(true)
  })

  it('重复 token 和重新标记不能替换租约，错误 token 或类型无法释放', () => {
    const state = captureDockerMutationFence(), token = Symbol('same'), wrong = Symbol('same')
    expect(state.reserve('create', token)).toBe(true)
    expect(state.reserve('create', token)).toBe(false)
    for (const kind of ['container', 'delete', 'compose', 'standalone'] as DockerMutationKind[]) {
      expect(state.reserve(kind, token)).toBe(false)
      expect(state.owns(kind, token)).toBe(false)
      expect(state.release(kind, token)).toBe(false)
    }
    expect(state.owns('create', wrong)).toBe(false)
    expect(state.release('create', wrong)).toBe(false)
    expect(state.owns('create', token)).toBe(true)
    expect(state.release('create', token)).toBe(true)
    expect(state.release('create', token)).toBe(false)
  })

  it('运行时非法类型或 token 不产生租约', () => {
    const state = captureDockerMutationFence()
    for (const [kind, token] of [[undefined, undefined], ['other', Symbol()], ['create', 'token'], ['standalone', null]]) {
      expect(state.reserve(kind as DockerMutationKind, token as symbol)).toBe(false)
      expect(state.owns(kind as DockerMutationKind, token as symbol)).toBe(false)
      expect(state.release(kind as DockerMutationKind, token as symbol)).toBe(false)
    }
    expect(state.standaloneBlocked.value).toBe(false)
    expect(state.otherMutationsBlocked.value).toBe(false)
  })

  it('会话中止清空旧租约；旧方法与迟到捕获不能影响新会话的同 token 租约', () => {
    const oldSession = captureSession(), old = captureDockerMutationFence(oldSession), token = Symbol()
    expect(old.reserve('standalone', token)).toBe(true)
    beginSession()
    const fresh = captureDockerMutationFence()
    expect(fresh.reserve('standalone', token)).toBe(true)
    expect(old.standaloneBlocked.value).toBe(false)
    expect(old.otherMutationsBlocked.value).toBe(false)
    expect(old.owns('standalone', token)).toBe(false)
    expect(old.release('standalone', token)).toBe(false)
    expect(old.reserve('create', Symbol())).toBe(false)
    const late = captureDockerMutationFence(oldSession)
    expect(late.reserve('create', Symbol())).toBe(false)
    expect(captureDockerMutationFence().owns('standalone', token)).toBe(true)
    expect(fresh.owns('standalone', token)).toBe(true)
  })

  it('新会话捕获后旧中止回调迟到，只清空旧引用', () => {
    const session = captureSession()
    let delayedAbort!: EventListenerOrEventListenerObject
    vi.spyOn(session.signal, 'addEventListener').mockImplementation((type, listener) => {
      if (type === 'abort' && listener) delayedAbort = listener
    })
    const old = captureDockerMutationFence(session), token = Symbol()
    expect(old.reserve('create', token)).toBe(true)
    beginSession()
    const fresh = captureDockerMutationFence()
    expect(fresh.reserve('standalone', token)).toBe(true)
    expect(old.release('create', token)).toBe(false)
    const event = new Event('abort')
    if (typeof delayedAbort === 'function') delayedAbort.call(session.signal, event)
    else delayedAbort.handleEvent(event)
    expect(old.standaloneBlocked.value).toBe(false)
    expect(fresh.owns('standalone', token)).toBe(true)
    expect(fresh.standaloneBlocked.value).toBe(true)
  })

  it('同代不同信号及伪造未来会话均无准入权限，不替换真实会话引用', () => {
    const session = captureSession(), state = captureDockerMutationFence(), token = Symbol()
    expect(state.reserve('create', token)).toBe(true)
    for (const context of [
      { generation: session.generation, signal: new AbortController().signal },
      { generation: session.generation + 100, signal: session.signal },
    ]) {
      const invalid = captureDockerMutationFence(context)
      expect(invalid.reserve('standalone', Symbol())).toBe(false)
      expect(invalid.owns('create', token)).toBe(false)
      expect(invalid.release('create', token)).toBe(false)
      expect(invalid.standaloneBlocked.value).toBe(false)
      expect(captureDockerMutationFence().owns('create', token)).toBe(true)
    }
    beginSession()
    const fresh = captureDockerMutationFence()
    expect(fresh.reserve('standalone', token)).toBe(true)
    expect(captureDockerMutationFence().owns('standalone', token)).toBe(true)
  })

  it('捕获后外部修改会话描述不能让旧句柄借用新信号', () => {
    const session = captureSession(), old = captureDockerMutationFence(session)
    const fresh = beginSession()
    Object.assign(session, fresh)
    expect(old.reserve('create', Symbol())).toBe(false)
    expect(captureDockerMutationFence().reserve('standalone', Symbol())).toBe(true)
  })

  it('不可变 Map 更新使阻塞计算值同步响应准入、释放及会话中止', () => {
    const state = captureDockerMutationFence(), scope = effectScope(), observed: boolean[][] = []
    scope.run(() => watchSyncEffect(() => { observed.push([state.standaloneBlocked.value, state.otherMutationsBlocked.value]) }))
    const shared = Symbol(), standalone = Symbol()
    state.reserve('compose', shared)
    state.release('compose', shared)
    state.reserve('standalone', standalone)
    invalidateSession()
    expect(observed).toEqual([[false, false], [true, false], [false, false], [true, true], [false, false]])
    scope.stop()
  })
})
