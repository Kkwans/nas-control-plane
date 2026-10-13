import { EventEmitter } from 'node:events'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { afterEach, describe, expect, it, vi } from 'vitest'

type SummaryPage = { url: () => string; isClosed: () => boolean }
type FixtureContext = EventEmitter & { browser: () => { isConnected: () => boolean } }
type FixturePage = {
  context: () => FixtureContext
  isClosed: () => boolean
  clock: { runFor: (milliseconds: number) => Promise<void> }
}
type Fixture = {
  context: FixtureContext
  page: FixturePage
  ticks: number[]
  resolveScan: (value: unknown) => void
  rejectScan: (error: Error) => void
  closeSummary: () => void
  closeMain: () => void
  disconnect: () => void
  start: () => Promise<unknown>
  checkCleanup: () => void
}
type FixtureOptions = {
  auxiliary?: boolean
  summaryURL?: string
  tick?: (fixture: Fixture) => void | Promise<void>
}

// 只提取当前 E2E 源文件的纯调度函数，不加载 Playwright 测试入口或启动浏览器。
// AST 按名称定位，避免维护另一份可能与真正采集逻辑脱节的测试实现。
const source = readFileSync(new URL('../../e2e/docker-inventory-response.spec.ts', import.meta.url), 'utf8')
const ast = ts.createSourceFile('docker-inventory-response.spec.ts', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const helpers = ast.statements.filter((node): node is ts.FunctionDeclaration =>
  ts.isFunctionDeclaration(node) && node.name?.text === 'withClockFrames')
if (helpers.length !== 1) throw new Error('必须找到唯一的 withClockFrames 采集函数')
const compiled = ts.transpileModule(helpers[0]!.getText(ast), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText
const withClockFrames = new Function('expect', `${compiled}; return withClockFrames;`)(expect) as
  <T>(page: FixturePage, operation: () => Promise<T>) => Promise<T>
const closedError = () => new Error('clock.runFor: Target page, context or browser has been closed')

function setup(options: FixtureOptions = {}): Fixture {
  let summaryClosed = false
  let mainClosed = false
  let connected = true
  let resolveScan!: (value: unknown) => void
  let rejectScan!: (error: Error) => void
  const scan = new Promise((resolve, reject) => { resolveScan = resolve; rejectScan = reject })
  const context = Object.assign(new EventEmitter(), { browser: () => ({ isConnected: () => connected }) })
  const summary: SummaryPage = { url: () => options.summaryURL ?? 'about:blank', isClosed: () => summaryClosed }
  const ticks: number[] = []
  const page: FixturePage = {
    context: () => context,
    isClosed: () => mainClosed,
    clock: { async runFor(tick) { ticks.push(tick); await options.tick?.(fixture) } },
  }
  const fixture: Fixture = {
    context, page, ticks, resolveScan, rejectScan,
    closeSummary() { summaryClosed = true },
    closeMain() { mainClosed = true },
    disconnect() { connected = false },
    start() { if (options.auxiliary !== false) context.emit('page', summary); return scan },
    checkCleanup() {
      expect(context.listenerCount('page')).toBe(0)
      expect(context.listenerCount('close')).toBe(0)
    },
  }
  return fixture
}

afterEach(() => vi.useRealTimers())

describe('Docker 清单完整 axe 的受控时钟生命周期', () => {
  it('成功分析保持完整结果且只推进一帧', async () => {
    const expected = { violations: [], passes: ['all rules'] }
    const fixture = setup({ tick(current) { current.closeSummary(); current.resolveScan(expected) } })
    await expect(withClockFrames(fixture.page, fixture.start)).resolves.toBe(expected)
    expect(fixture.ticks).toEqual([16])
    fixture.checkCleanup()
  })

  it('仅汇总页关闭竞争可等待成功分析，不重放已经推进的时钟', async () => {
    const expected = { violations: [], passes: ['all rules'] }
    const failure = closedError()
    const fixture = setup({ tick(current) {
      current.closeSummary()
      setImmediate(() => current.resolveScan(expected))
      throw failure
    } })
    await expect(withClockFrames(fixture.page, fixture.start)).resolves.toBe(expected)
    expect(fixture.ticks).toEqual([16])
    fixture.checkCleanup()
  })

  it('汇总页关闭后的分析失败仍向上传递', async () => {
    const failure = new Error('full axe scan failed')
    const fixture = setup({ tick(current) {
      current.closeSummary()
      setImmediate(() => current.rejectScan(failure))
      throw closedError()
    } })
    await expect(withClockFrames(fixture.page, fixture.start)).rejects.toBe(failure)
    expect(fixture.ticks).toEqual([16])
    fixture.checkCleanup()
  })

  const unsafeClosures: Array<{ name: string; mutate?: (fixture: Fixture) => void; options?: FixtureOptions }> = [
    { name: '主页面已关闭', mutate: (fixture) => fixture.closeMain() },
    { name: 'context 已关闭', mutate: (fixture) => { fixture.context.emit('close') } },
    { name: '浏览器已断开', mutate: (fixture) => fixture.disconnect() },
    { name: '没有跟踪到汇总页', options: { auxiliary: false } },
    { name: '存在多个辅助页', mutate: (fixture) => { fixture.context.emit('page', { url: () => 'about:blank', isClosed: () => true }) } },
    { name: '关闭的页面不是空白汇总页', options: { summaryURL: 'https://example.invalid/' } },
  ]
  for (const { name, mutate, options } of unsafeClosures) {
    it(`${name}时，即使分析已成功也不能吞掉关闭异常`, async () => {
      const failure = closedError()
      const fixture = setup({ ...options, tick(current) {
        current.closeSummary()
        mutate?.(current)
        current.resolveScan({ violations: [] })
        throw failure
      } })
      await expect(withClockFrames(fixture.page, fixture.start)).rejects.toBe(failure)
      expect(fixture.ticks).toEqual([16])
      fixture.checkCleanup()
    })
  }

  it('辅助页尚未关闭不足以证明生命周期竞争', async () => {
    const failure = closedError()
    const fixture = setup({ tick(current) { current.resolveScan({ violations: [] }); throw failure } })
    await expect(withClockFrames(fixture.page, fixture.start)).rejects.toBe(failure)
    fixture.checkCleanup()
  })

  for (const [name, failure] of [
    ['其他时钟异常', new Error('clock.runFor: Unexpected JavaScript error')],
    ['非 Error 对象', 'clock.runFor: Target page, context or browser has been closed'],
    ['不同 API 的关闭异常', new Error('page.evaluate: Target page, context or browser has been closed')],
    ['具有不同后缀的异常', new Error('clock.runFor: Target page, context or browser has been closed unexpectedly')],
  ] as const) {
    it(`${name}必须失败`, async () => {
      const fixture = setup({ tick(current) { current.closeSummary(); current.resolveScan({ violations: [] }); throw failure } })
      await expect(withClockFrames(fixture.page, fixture.start)).rejects.toBe(failure)
      fixture.checkCleanup()
    })
  }

  for (const { name, mutate } of unsafeClosures.slice(0, 3)) {
    it(`等待分析收尾时${name}仍必须失败`, async () => {
      const failure = closedError()
      const fixture = setup({ tick(current) {
        current.closeSummary()
        setImmediate(() => { mutate!(current); current.resolveScan({ violations: [] }) })
        throw failure
      } })
      await expect(withClockFrames(fixture.page, fixture.start)).rejects.toBe(failure)
      fixture.checkCleanup()
    })
  }

  it('汇总页关闭后卡死的分析最多等待真实调度器一秒', async () => {
    vi.useFakeTimers()
    const failure = closedError()
    const fixture = setup({ tick(current) { current.closeSummary(); throw failure } })
    const pending = expect(withClockFrames(fixture.page, fixture.start)).rejects.toBe(failure)
    await vi.advanceTimersByTimeAsync(999)
    expect(vi.getTimerCount()).toBe(1)
    await vi.advanceTimersByTimeAsync(1)
    await pending
    expect(vi.getTimerCount()).toBe(0)
    expect(fixture.ticks).toEqual([16])
    fixture.checkCleanup()
  })

  it('普通分析拒绝仍必须失败', async () => {
    const failure = new Error('scan failed independently')
    const fixture = setup({ tick(current) { current.rejectScan(failure) } })
    await expect(withClockFrames(fixture.page, fixture.start)).rejects.toBe(failure)
    fixture.checkCleanup()
  })

  it('同步分析初始化失败也会移除生命周期监听器', async () => {
    const failure = new Error('operation setup failed')
    const fixture = setup()
    await expect(withClockFrames(fixture.page, () => { throw failure })).rejects.toBe(failure)
    fixture.checkCleanup()
  })

  it('未完成的分析仍受累计 2000 毫秒时钟上限约束', async () => {
    const fixture = setup()
    await expect(withClockFrames(fixture.page, fixture.start)).rejects.toThrow('axe 未在受控 2 秒时钟预算内结束')
    expect(fixture.ticks).toHaveLength(125)
    expect(fixture.ticks.reduce((sum, tick) => sum + tick, 0)).toBe(2000)
    fixture.checkCleanup()
  })
})
