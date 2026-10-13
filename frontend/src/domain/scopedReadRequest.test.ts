import { describe, expect, it } from 'vitest'
import { createScopedReadRequest } from './scopedReadRequest'
import { beginSession, invalidateSession } from '../session/sessionLifecycle'

describe('资源读取边界', () => {
  it('A 慢 B 快时仅 B 可写入，A 的清理不取消 B', () => {
    beginSession()
    const scope = createScopedReadRequest()
    const a = scope.begin()
    const b = scope.begin()
    expect(a.signal.aborted).toBe(true)
    expect(a.isCurrent()).toBe(false)
    a.finish()
    expect(b.isCurrent()).toBe(true)
    scope.invalidate()
    expect(b.signal.aborted).toBe(true)
    expect(b.isCurrent()).toBe(false)
  })

  it('关闭后重新打开或登出均使旧回调失效', () => {
    beginSession()
    const scope = createScopedReadRequest()
    const a = scope.begin()
    scope.invalidate()
    const b = scope.begin()
    expect(a.isCurrent()).toBe(false)
    invalidateSession()
    expect(b.isCurrent()).toBe(false)
    expect(b.signal.aborted).toBe(true)
    b.finish()
  })
})
