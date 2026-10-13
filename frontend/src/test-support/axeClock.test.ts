import { EventEmitter } from 'node:events'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { withClockFrames } from './axeClock'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}
function setup(tick: (elapsed: number) => void | Promise<void> = () => {}) {
  const scan = deferred<unknown>()
  let closed = false
  let connected = true
  let elapsed = 0
  const ticks: number[] = []
  const context = Object.assign(new EventEmitter(), { browser: () => ({ isConnected: () => connected }) })
  const page = {
    context: () => context,
    isClosed: () => closed,
    clock: { async runFor(ms: number) { ticks.push(ms); elapsed += ms; await tick(elapsed) } },
  }
  return {
    page, scan, ticks, context,
    closePage() { closed = true },
    disconnect() { connected = false },
    run() { return withClockFrames(page, () => scan.promise) },
    expectClean() {
      expect(vi.getTimerCount()).toBe(0)
      expect(context.listenerCount('page')).toBe(0)
      expect(context.listenerCount('close')).toBe(0)
    },
  }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('完整 axe 的真实调度时限与页面时钟预算', () => {
  it('立即到达的完整结果不需要推进页面时间，也不丢弃违规和其他规则结果', async () => {
    const fixture = setup()
    const result = { violations: [{ id: 'color-contrast' }], passes: [{ id: 'document-title' }], incomplete: ['review'], inapplicable: ['other'] }
    fixture.scan.resolve(result)
    await expect(fixture.run()).resolves.toBe(result)
    expect(fixture.ticks).toEqual([])
    fixture.expectClean()
  })

  it('普通 20 毫秒调度延迟不会在紧循环中耗尽 2000 毫秒页面时间', async () => {
    const fixture = setup()
    const result = { violations: [], passes: ['full result'] }
    setTimeout(() => fixture.scan.resolve(result), 20)
    const pending = expect(fixture.run()).resolves.toBe(result)
    await vi.advanceTimersByTimeAsync(15)
    expect(fixture.ticks).toEqual([])
    await vi.advanceTimersByTimeAsync(1)
    expect(fixture.ticks).toEqual([16])
    await vi.advanceTimersByTimeAsync(4)
    await pending
    fixture.expectClean()
  })

  it('异步初始化完成后仍驱动规则所需的后续帧', async () => {
    let ready = false
    const result = { violations: [], passes: ['late rule'] }
    const fixture = setup(() => { if (ready) fixture.scan.resolve(result) })
    setTimeout(() => { ready = true }, 120)
    const pending = expect(fixture.run()).resolves.toBe(result)
    await vi.advanceTimersByTimeAsync(119)
    expect(fixture.ticks).toHaveLength(7)
    await vi.advanceTimersByTimeAsync(9)
    await pending
    expect(fixture.ticks).toEqual(Array(8).fill(16))
    fixture.expectClean()
  })

  it('页面预算耗尽后仅等待 Node 侧结果，不继续增加快照年龄', async () => {
    const fixture = setup()
    const result = { violations: [], passes: ['complete after clock budget'] }
    setTimeout(() => fixture.scan.resolve(result), 2500)
    const pending = expect(fixture.run()).resolves.toBe(result)
    await vi.advanceTimersByTimeAsync(2000)
    expect(fixture.ticks).toEqual(Array(125).fill(16))
    await vi.advanceTimersByTimeAsync(500)
    await pending
    expect(fixture.ticks.reduce((sum, tick) => sum + tick, 0)).toBe(2000)
    fixture.expectClean()
  })

  it('真实 5 秒到期必须失败；不能把挂起分析变成无违规结果', async () => {
    const fixture = setup()
    let settled = false
    const pending = expect(fixture.run()).rejects.toThrow('axe 未在真实 5 秒时限内结束（受控时钟已推进 2000/2000 毫秒）').then(() => { settled = true })
    await vi.advanceTimersByTimeAsync(4999)
    expect(settled).toBe(false)
    expect(fixture.ticks).toHaveLength(125)
    await vi.advanceTimersByTimeAsync(1)
    await pending
    fixture.expectClean()
  })

  it('被动收尾期间的 axe 拒绝保留原错误', async () => {
    const fixture = setup()
    const failure = new Error('aggregation failed')
    setTimeout(() => fixture.scan.reject(failure), 2200)
    const pending = expect(fixture.run()).rejects.toBe(failure)
    await vi.advanceTimersByTimeAsync(2200)
    await pending
    expect(fixture.ticks).toHaveLength(125)
    fixture.expectClean()
  })

  it('真实超时后消费迟到 axe 拒绝，不重启调度', async () => {
    const fixture = setup()
    const pending = expect(fixture.run()).rejects.toThrow('真实 5 秒时限')
    await vi.advanceTimersByTimeAsync(5000)
    await pending
    fixture.scan.reject(new Error('late axe failure'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(fixture.ticks).toHaveLength(125)
    fixture.expectClean()
  })

  it('单次 runFor 卡住也受真实时限约束，迟到 tick 拒绝被消费且不重试', async () => {
    const stalledTick = deferred<void>()
    const fixture = setup(() => stalledTick.promise)
    const pending = expect(fixture.run()).rejects.toThrow('受控时钟已推进 16/2000 毫秒')
    await vi.advanceTimersByTimeAsync(5000)
    await pending
    stalledTick.reject(new Error('late tick failure'))
    fixture.scan.reject(new Error('late scan failure'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(fixture.ticks).toEqual([16])
    fixture.expectClean()
  })

  it('超时后迟到的成功不能重新开始调度或改变失败结论', async () => {
    const stalledTick = deferred<void>()
    const fixture = setup(() => stalledTick.promise)
    const pending = expect(fixture.run()).rejects.toThrow('真实 5 秒时限')
    await vi.advanceTimersByTimeAsync(5000)
    await pending
    stalledTick.resolve()
    fixture.scan.resolve({ violations: [] })
    await vi.advanceTimersByTimeAsync(5000)
    expect(fixture.ticks).toEqual([16])
    fixture.expectClean()
  })

  for (const kind of ['page', 'context', 'browser'] as const) {
    it(`没有时钟异常时也不能把 ${kind} 关闭后的分析当成成功`, async () => {
      const fixture = setup()
      const pending = expect(fixture.run()).rejects.toThrow('axe 分析结束时主页面、上下文或浏览器已关闭')
      if (kind === 'page') fixture.closePage()
      if (kind === 'context') fixture.context.emit('close')
      if (kind === 'browser') fixture.disconnect()
      fixture.scan.resolve({ violations: [] })
      await pending
      expect(fixture.ticks).toEqual([])
      fixture.expectClean()
    })
  }
})
