type AuxiliaryPage = { url: () => string; isClosed: () => boolean }
type ClockContext = {
  on(event: 'page', listener: (page: AuxiliaryPage) => void): unknown
  on(event: 'close', listener: () => void): unknown
  off(event: 'page', listener: (page: AuxiliaryPage) => void): unknown
  off(event: 'close', listener: () => void): unknown
  browser: () => { isConnected: () => boolean } | null
}
type ClockPage = {
  context: () => ClockContext
  isClosed: () => boolean
  clock: { runFor: (milliseconds: number) => Promise<void> }
}

// 页面时钟只负责规则的定时器；注入、跨进程结果回传和汇总页收尾另需真实调度时间。
// 每次推进前最多等待一个真实帧；总 fake 时间仍不超过 2 秒，不恢复/重置页面时钟。
// 整个操作另有真实 5 秒上限，包括卡住的 runFor；超时后不再发起任何新 tick。
export async function withClockFrames<T>(page: ClockPage, operation: () => Promise<T>): Promise<T> {
  const context = page.context()
  const auxiliaryPages = new Set<AuxiliaryPage>()
  let contextClosed = false
  const onPage = (auxiliary: AuxiliaryPage) => auxiliaryPages.add(auxiliary)
  const onContextClose = () => { contextClosed = true }
  const originalPageIsAlive = () => !contextClosed && !page.isClosed() && context.browser()?.isConnected() === true
  context.on('page', onPage)
  context.on('close', onContextClose)
  let elapsed = 0
  let finished = false
  let rejected = false
  let result!: T
  let failure: unknown
  let deadlineTimer: ReturnType<typeof setTimeout> | undefined
  try {
    const deadline = new Promise<never>((_, reject) => {
      deadlineTimer = setTimeout(() => reject(new Error(`axe 未在真实 5 秒时限内结束（受控时钟已推进 ${elapsed}/2000 毫秒）`)), 5000)
    })
    // 同时接收成功和失败；超时后的迟到 rejection 也必须被消费，不会改写本次失败。
    const pending = operation().then((value) => { result = value; finished = true }, (error: unknown) => { failure = error; rejected = true; finished = true })
    while (!finished && elapsed < 2000) {
      let frameTimer: ReturnType<typeof setTimeout> | undefined
      try {
        await Promise.race([pending, deadline, new Promise<void>((resolve) => { frameTimer = setTimeout(resolve, 16) })])
      } finally { clearTimeout(frameTimer) }
      if (finished) break
      // 先记账，失败的 tick 不重试；即使某些页面已推进也不会突破预算。
      elapsed += 16
      try {
        await Promise.race([page.clock.runFor(16), deadline])
      } catch (error) {
        // 完整 axe 会新建 about:blank 汇总页并在 finishRun 后关闭。只容忍这个
        // 已确认的竞争；主页面/context 关闭、其他时钟异常和 axe 失败仍须失败。
        const closedSummaryPage = auxiliaryPages.size === 1 && [...auxiliaryPages].every((auxiliary) =>
          auxiliary.url() === 'about:blank' && auxiliary.isClosed())
        if (!(error instanceof Error) || !/^clock\.runFor: Target page, context or browser has been closed(?:\n|$)/.test(error.message) ||
          !closedSummaryPage || !originalPageIsAlive()) throw error
        let closeTimer: ReturnType<typeof setTimeout> | undefined
        try {
          await Promise.race([pending, deadline, new Promise<never>((_, reject) => { closeTimer = setTimeout(() => reject(error), 1000) })])
        } finally { clearTimeout(closeTimer) }
        if (rejected) throw failure
        if (!originalPageIsAlive()) throw error
      }
    }
    // fake 预算耗尽只停止驱动页面，不把尚未到达 Node 的完整结果判成失败。
    // 这里只被动等收尾，真实时限仍生效；不会继续增加快照年龄或绕过 axe 规则。
    if (!finished) await Promise.race([pending, deadline])
    if (rejected) throw failure
    if (!originalPageIsAlive()) throw new Error('axe 分析结束时主页面、上下文或浏览器已关闭')
    return result
  } finally {
    clearTimeout(deadlineTimer)
    context.off('page', onPage)
    context.off('close', onContextClose)
  }
}
