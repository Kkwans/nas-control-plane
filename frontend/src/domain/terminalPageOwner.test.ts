import { beforeEach, describe, expect, it, vi } from 'vitest'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { createTerminalPageOwner } from './terminalPageOwner'

beforeEach(() => beginSession())
function panel(page: ReturnType<typeof createTerminalPageOwner>, id: string) {
  const callbacks = { select: vi.fn(), clear: vi.fn() }
  return { ...callbacks, detach: page.register(id, callbacks) }
}
describe('终端页面准确 lease 所有权', () => {
  it('opening/live/closing 共享本页上限；删除不会释放 socket lease', () => {
    const page = createTerminalPageOwner(2)
    const first = panel(page, 'a'); panel(page, 'b'); panel(page, 'c')
    const retire = vi.fn()
    const a = page.reserve('a', retire)!
    const b = page.reserve('b', vi.fn())!
    const socket = {}
    page.bindSocket(a, socket)
    expect(page.occupiedCount).toBe(2)
    expect(page.reserve('c', vi.fn())).toBeNull()
    first.detach()
    page.release(a)
    page.release(a, {})
    expect(page.occupiedCount).toBe(2)
    page.invalidate('停止')
    expect(retire).toHaveBeenCalledWith('停止')
    expect(page.occupiedCount).toBe(2)
    page.release(a, socket)
    page.release(a, socket)
    expect(page.occupiedCount).toBe(1)
    page.release(b)
    expect(page.occupiedCount).toBe(0)
  })
  it('切换只撤销交互且推进 epoch；旧 lease 不释放新 attempt', () => {
    const page = createTerminalPageOwner()
    const a = panel(page, 'a'); const b = panel(page, 'b')
    page.select('a')
    const epoch = page.selectionEpoch
    const old = page.reserve('a', vi.fn())!
    page.release(old)
    const fresh = page.reserve('a', vi.fn())!
    expect(fresh.attemptId).toBeGreaterThan(old.attemptId)
    expect(fresh.tabId).toBe(old.tabId)
    page.release(old)
    expect(page.occupiedCount).toBe(1)
    page.select('b')
    expect(page.selectionEpoch).toBeGreaterThan(epoch)
    expect(page.selected('a')).toBe(false)
    expect(page.selected('b')).toBe(true)
    expect(a.select).toHaveBeenCalledTimes(2)
    expect(b.select).toHaveBeenCalledTimes(1)
  })
  it('认证失效立即禁止交互与准入；pageshow 不恢复旧认证', () => {
    const page = createTerminalPageOwner()
    panel(page, 'a'); page.select('a')
    invalidateSession()
    expect(page.current()).toBe(false)
    expect(page.selected('a')).toBe(false)
    expect(page.reserve('a', vi.fn())).toBeNull()
    page.show()
    expect(page.current()).toBe(false)
  })
})
