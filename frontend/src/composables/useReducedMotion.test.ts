// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { useReducedMotion } from './useReducedMotion'
import { mockReducedMotion } from '@/test/reducedMotion'

afterEach(() => vi.unstubAllGlobals())

describe('动态减少动效偏好', () => {
  it.each([true, false])('同步读取初始偏好 %s，双向更新并在作用域释放时注销监听', (initial) => {
    const mock = mockReducedMotion(initial)
    const scope = effectScope()
    const reduced = scope.run(useReducedMotion)!
    expect(reduced.value).toBe(initial)
    expect(window.matchMedia).toHaveBeenCalledWith('(prefers-reduced-motion: reduce)')
    mock.change(!initial)
    expect(reduced.value).toBe(!initial)
    mock.change(initial)
    expect(reduced.value).toBe(initial)
    scope.stop()
    expect(mock.media.removeEventListener).toHaveBeenCalledWith('change', mock.media.addEventListener.mock.calls[0]![1])
    mock.change(!initial)
    expect(reduced.value).toBe(initial)
  })

  it.each(['window', 'matchMedia'])('缺少 %s 时安全回退，不注册监听', (name) => {
    vi.stubGlobal(name, undefined)
    const scope = effectScope()
    expect(scope.run(useReducedMotion)!.value).toBe(false)
    scope.stop()
  })
})
