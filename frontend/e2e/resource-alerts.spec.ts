import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import type { AlertCondition, ResourceAlertRule, ResourceAlerts } from '../src/domain/monitoring/alerts'
import { installMockApi, summary } from './mockApi'

test.use({ viewport: { width: 1440, height: 1000 }, timezoneId: 'UTC', contextOptions: { reducedMotion: 'reduce' } })
const endpoint = '/api/v1/monitoring/alerts'
const baseTime = Date.parse(summary.collectedAt)
const at = (offset = 0) => new Date(baseTime + offset).toISOString()
const panel = (page: Page) => page.getByRole('region', { name: '当前资源提示', exact: true })
const card = (page: Page, kind: 'cpu' | 'memory') => panel(page).locator(`article[aria-labelledby="alert-${kind}-title"]`)
function rule(kind: 'cpu' | 'memory', condition: AlertCondition = 'normal', overrides: Partial<ResourceAlertRule> = {}): ResourceAlertRule {
  return {
    id: `host.${kind}.high`, severity: 'warning', triggerPercent: 90, recoveryPercent: kind === 'cpu' ? 80 : 85,
    triggerSeconds: 120, recoverySeconds: 60, condition, availability: 'fresh', reasonCode: '',
    sampleAt: at(), receivedAt: at(), value: condition === 'normal' ? 24.5 : condition === 'recovering' ? 72 : 95,
    pendingSince: condition === 'pending' ? at(-30_000) : null,
    firingSince: condition === 'firing' || condition === 'recovering' ? at(-180_000) : null,
    recoveringSince: condition === 'recovering' ? at(-20_000) : null, lastRecoveredAt: null, ...overrides,
  }
}
function snapshot(cpu: ResourceAlertRule = rule('cpu'), memory: ResourceAlertRule = rule('memory')): ResourceAlerts {
  return { enabled: true, observedAt: at(), startedAt: at(-600_000), rules: { cpu, memory } }
}
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
async function fixture(page: Page, initial: unknown = snapshot()) {
  await page.clock.install({ time: new Date(baseTime) })
  await page.clock.pauseAt(new Date(baseTime + 1000))
  await page.clock.setSystemTime(new Date(baseTime))
  await installMockApi(page, { clock: false, manualRealtime: true })
  let body = initial
  let status = 200
  let gate: { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> } | undefined
  const requests: string[] = []
  const writes: string[] = []
  let active = 0
  let maximumActive = 0
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() !== 'GET') {
      writes.push(`${request.method()} ${path}`)
      return route.fulfill({ status: 503, json: { code: 'FIXTURE_WRITE_BLOCKED' } })
    }
    if (path !== endpoint) return route.fallback()
    requests.push(`${request.method()} ${path}`)
    const response = { status, json: structuredClone(body) }
    const held = gate
    gate = undefined
    active += 1
    maximumActive = Math.max(maximumActive, active)
    try {
      if (held) { held.received.resolve(); await held.release.promise }
      await route.fulfill(response)
    } finally { active -= 1; held?.finished.resolve() }
  })
  return {
    requests, writes, maximumActive: () => maximumActive,
    reply(next: unknown, code = 200) { body = next; status = code },
    hold() { gate = { received: deferred(), release: deferred(), finished: deferred() }; return gate },
  }
}
async function open(page: Page) {
  await page.goto('/monitoring', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: '系统监控', exact: true })).toBeVisible()
  await page.clock.runFor(1)
}
async function refresh(page: Page) {
  await page.clock.runFor(1)
  await panel(page).getByRole('button', { name: '刷新当前提示', exact: true }).click()
  await expect(panel(page).getByRole('button', { name: '刷新当前提示', exact: true })).toBeEnabled()
}
async function semanticState(page: Page) {
  return panel(page).evaluate((element) => ({
    rules: [...element.querySelectorAll('article')].map((item) => item.textContent),
    status: element.querySelector('[role="status"]')?.textContent,
    error: element.querySelector('.resource-alerts__error')?.textContent ?? null,
    notice: element.querySelector('.resource-alerts__notice')?.textContent ?? null,
  }))
}
async function fullAxe(page: Page) {
  // axe 依赖运行中的页面计时器。允许只读轮询继续，但不能把过期后的 UI 当作原状态验收。
  // 请求数量的精确断言放在本 helper 之前，或在之后重新取得基线。
  await expect(panel(page).getByRole('button', { name: '刷新当前提示', exact: true })).toBeEnabled()
  const before = await semanticState(page)
  await page.clock.resume()
  let result
  try { result = await new AxeBuilder({ page }).analyze() }
  finally { await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000)) }
  await expect(panel(page).getByRole('button', { name: '刷新当前提示', exact: true })).toBeEnabled()
  expect(await semanticState(page), 'axe 期间规则、读数、原因、恢复时间或错误状态不得变化').toEqual(before)
  return result
}
async function historyControlGeometry(page: Page) {
  await expect(page.locator('.monitor-header .date-time-range')).toHaveAttribute('aria-busy', 'false')
  await page.evaluate(() => document.fonts.ready)
  return page.locator('.monitor-header').evaluate((header) => ({
    fields: [...header.querySelectorAll('.workspace-header__copy p, .workspace-stat dt, .workspace-stat dd, .compact-filter button, .date-time-range__actions .action-button__label')].map((field) => {
      const range = document.createRange()
      range.selectNodeContents(field)
      const bounds = field.getBoundingClientRect()
      const clips = [header.getBoundingClientRect()]
      for (let ancestor: Element | null = field; ancestor && ancestor !== header; ancestor = ancestor.parentElement) {
        const style = getComputedStyle(ancestor)
        if (/(hidden|clip|auto|scroll)/.test(`${style.overflowX} ${style.overflowY}`)) clips.push(ancestor.getBoundingClientRect())
      }
      const lines = [...range.getClientRects()].filter((rect) => rect.width && rect.height)
      return {
        text: field.textContent, bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()),
        scrollWidth: field.scrollWidth, clientWidth: field.clientWidth,
        contained: lines.length > 0 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every((clip) =>
          line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
      }
    }),
    actionBounds: [...header.querySelectorAll('.date-time-range__actions button')].map((button) => button.getBoundingClientRect().toJSON()),
  }))
}
function expectReadableHistoryControls(geometry: Awaited<ReturnType<typeof historyControlGeometry>>) {
  expect(geometry.fields).toHaveLength(14)
  for (const field of geometry.fields) {
    expect(field.contained, `${field.text}: ${JSON.stringify(field)}`).toBe(true)
    expect(field.scrollWidth, field.text ?? '').toBeLessThanOrEqual(field.clientWidth + 1)
  }
}
async function evidence(page: Page, info: TestInfo, state: string) {
  await page.evaluate(() => document.fonts.ready)
  // 先运行 axe，再冻结并采集同一实际 UI 的像素与状态。
  const axe = await fullAxe(page)
  const captureState = await semanticState(page)
  const historyControls = await historyControlGeometry(page)
  const checks = await panel(page).evaluate((element) => {
    const bounds = element.getBoundingClientRect()
    return {
      viewport: { width: innerWidth, height: innerHeight }, rootFontSize: getComputedStyle(document.documentElement).fontSize,
      documentOverflow: document.documentElement.scrollWidth > innerWidth + 1,
      panel: bounds.toJSON(),
      rules: [...element.querySelectorAll('article')].map((item) => ({ text: item.textContent, bounds: item.getBoundingClientRect().toJSON(), overflow: item.scrollWidth > item.clientWidth + 1 })),
      status: element.querySelector('[role="status"]')?.textContent,
    }
  })
  // 完整页面 axe，不排除颜色、标签或对话框规则；保留所有级别结果供复核。
  if (process.env.NCP_CAPTURE_UI === '1') {
    const name = `fixture-resource-alerts-${state}-${page.viewportSize()!.width}`
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    const path = info.outputPath('ui-captures', `${name}.png`)
    const jsonPath = info.outputPath('ui-captures', `${name}-capture-state.json`)
    await writeFile(jsonPath, JSON.stringify({
      sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
      fixtureOnly: true, fixtureSource: 'frontend/e2e/resource-alerts.spec.ts + frontend/e2e/mockApi.ts', state, semanticState: captureState, historyControls, ...checks,
      axe: { violations: axe.violations, incomplete: axe.incomplete, passes: axe.passes.map(({ id }) => id) },
    }, null, 2))
    await page.screenshot({ path, fullPage: true, animations: 'disabled' })
    await info.attach(name, { path, contentType: 'image/png' })
    await info.attach(`${name}-capture-state`, { path: jsonPath, contentType: 'application/json' })
  }
  expect(await semanticState(page), 'PNG 与 capture-state 必须记录同一规则状态').toEqual(captureState)
  expectReadableHistoryControls(historyControls)
  expect(checks.documentOverflow).toBe(false)
  expect(checks.panel.left).toBeGreaterThanOrEqual(0)
  expect(checks.panel.right).toBeLessThanOrEqual(checks.viewport.width)
  expect(checks.rules.every((item) => !item.overflow)).toBe(true)
  expect(axe.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical'), JSON.stringify(axe.violations, null, 2)).toEqual([])
}

test('当前正常与等待持续确认独立于历史范围，数值变化不重复播报', async ({ page }, info) => {
  const api = await fixture(page, snapshot(rule('cpu'), rule('memory', 'pending')))
  await open(page)
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('未达到告警条件')
  await expect(card(page, 'memory').locator('.status-pill')).toHaveText('等待持续确认')
  await expect(panel(page)).toContainText('仅本次 Server 运行期')
  await expect(panel(page)).toContainText('采样间的变化不可知')
  const announcement = await panel(page).getByRole('status').textContent()
  await page.evaluate(() => {
    const target = document.querySelector('.resource-alerts__live')!
    target.setAttribute('data-mutations', '0')
    new MutationObserver((records) => target.setAttribute('data-mutations', String(Number(target.getAttribute('data-mutations')) + records.length)))
      .observe(target, { childList: true, characterData: true, subtree: true })
  })
  api.reply(snapshot(rule('cpu', 'normal', { value: 42 }), rule('memory', 'pending', { value: 96 })))
  await refresh(page)
  await expect(card(page, 'cpu')).toContainText('42.0%')
  await expect(panel(page).getByRole('status')).toHaveText(announcement!)
  await expect(panel(page).getByRole('status')).toHaveAttribute('data-mutations', '0')
  const before = api.requests.length
  await page.getByRole('button', { name: '1 小时', exact: true }).click()
  await expect(card(page, 'memory').locator('.status-pill')).toHaveText('等待持续确认')
  expect(api.requests).toHaveLength(before)
  await evidence(page, info, 'fresh-pending')
  expect(api.writes).toEqual([])
})

test('触发、恢复观察、unknown 与 stale 保留未确认事件，合法恢复才显示恢复时间', async ({ page }, info) => {
  const api = await fixture(page, snapshot(rule('cpu', 'firing'), rule('memory', 'recovering')))
  await open(page)
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('已触发')
  await expect(card(page, 'memory').locator('.status-pill')).toHaveText('恢复观察')
  await evidence(page, info, 'firing-recovering')
  api.reply(snapshot(
    rule('cpu', 'firing', { availability: 'unknown', reasonCode: 'CPU_UNAVAILABLE' }),
    rule('memory', 'firing', { availability: 'stale', reasonCode: 'SAMPLE_STALE', sampleAt: at(-20_000), receivedAt: at(-20_000) }),
  ))
  await refresh(page)
  for (const kind of ['cpu', 'memory'] as const) {
    await expect(card(page, kind)).toContainText('此前已触发，数据不可用，恢复尚未确认。')
    await expect(card(page, kind)).toContainText('上次有效值')
    await expect(card(page, kind)).toContainText('本次触发')
    await expect(card(page, kind).locator('.status-pill')).toHaveClass(/status-pill--neutral/)
  }
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('暂不可评估')
  await expect(card(page, 'memory').locator('.status-pill')).toHaveText('数据已过期')
  await evidence(page, info, 'unknown-stale-retained-event')
  const current = await page.evaluate(() => new Date().toISOString())
  api.reply(snapshot(rule('cpu', 'normal', { lastRecoveredAt: current, sampleAt: current, receivedAt: current }), rule('memory', 'recovering', { sampleAt: current, receivedAt: current })))
  await refresh(page)
  await expect(card(page, 'cpu')).toContainText('最近确认恢复')
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('未达到告警条件')
  await expect(card(page, 'memory').locator('.status-pill')).toHaveText('恢复观察')
  expect(api.writes).toEqual([])
})

test('1440px 默认字号的历史操作保持紧凑且文字完整', async ({ page }) => {
  const api = await fixture(page)
  await open(page)
  const geometry = await historyControlGeometry(page)
  expectReadableHistoryControls(geometry)
  const [first, ...rest] = geometry.actionBounds
  expect(first).toBeDefined()
  for (const button of rest) expect(Math.abs(button.top - first!.top)).toBeLessThanOrEqual(1)
  expect(geometry.actionBounds.every((button) => button.height <= 44)).toBe(true)
  expect(api.writes).toEqual([])
})

for (const width of [320, 390, 768, 1440]) {
  test(`${width}px 中断事件长原因码在 200% 根字号不溢出`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    const api = await fixture(page, snapshot(rule('cpu', 'firing', { availability: 'unknown', reasonCode: 'CPU_UNAVAILABLE_'.repeat(8) }), rule('memory', 'recovering')))
    await open(page)
    await expect(card(page, 'cpu')).toContainText('恢复尚未确认')
    const base = await page.locator('html').evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))
    await page.evaluate((size) => document.documentElement.style.setProperty('font-size', `${size * 2}px`, 'important'), base)
    await expect(page.locator('html')).toHaveCSS('font-size', `${base * 2}px`)
    // 390/1440 保留代表截图，其余尺寸执行同样布局和完整 axe 检查。
    if (width === 390 || width === 1440) await evidence(page, info, 'retained-event-200-percent')
    else {
      expectReadableHistoryControls(await historyControlGeometry(page))
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true)
      const axe = await fullAxe(page)
      expect(axe.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical')).toEqual([])
    }
    expect(api.writes).toEqual([])
  })
}

test('首次 loading、读取错误和坏成功响应可键盘重试；默认关闭不冒充健康', async ({ page }, info) => {
  await page.setViewportSize({ width: 390, height: 1000 })
  const api = await fixture(page)
  api.reply({ code: 'MONITORING_ALERTS_UNAVAILABLE' }, 503)
  const held = api.hold()
  try {
    await open(page)
    await held.received.promise
    await expect(panel(page)).toContainText('正在读取当前资源提示…')
    await expect(page.getByText('当前范围暂无样本', { exact: true })).toBeVisible()
    expect(api.requests).toHaveLength(1)
  } finally { held.release.resolve() }
  await expect(panel(page)).toContainText('当前提示读取失败')
  await evidence(page, info, 'initial-read-error')
  const beforeRetry = api.requests.length
  api.reply({ ...snapshot(), rules: {} })
  const retry = panel(page).getByRole('button', { name: '重试当前提示', exact: true })
  await retry.focus(); await retry.press('Enter')
  await expect(panel(page)).toContainText('MONITORING_ALERTS_RESPONSE_INVALID')
  api.reply({ ...snapshot(), enabled: false })
  await retry.focus(); await retry.press('Enter')
  await expect(panel(page).locator('.status-pill')).toHaveText('未启用')
  await expect(panel(page)).toContainText('未启用不表示资源正常')
  await expect(panel(page).locator('article')).toHaveCount(0)
  await expect(panel(page).locator('.resource-alerts__error')).toHaveCount(0)
  expect(api.requests).toHaveLength(beforeRetry + 2)
  await evidence(page, info, 'disabled')
  expect(api.writes).toEqual([])
})

test('慢轮询无重叠，挂起时本地 stale；刷新失败保留事件且历史错误独立恢复', async ({ page }, info) => {
  const api = await fixture(page, snapshot(rule('cpu', 'firing')))
  let historyFailed = true
  await page.route('**/api/v1/monitoring/samples**', (route) => route.fulfill(historyFailed
    ? { status: 503, json: { code: 'MONITORING_READ_FAILED' } } : { json: [] }))
  await open(page)
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('已触发')
  await expect(page.locator('.resource-state--error')).toContainText('监控历史加载失败')
  api.reply({ code: 'MONITORING_ALERTS_UNAVAILABLE' }, 503)
  const held = api.hold()
  try {
    await page.clock.runFor(5000)
    await held.received.promise
    await page.clock.runFor(16_000)
    await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('数据已过期')
    await expect(card(page, 'cpu')).toContainText('恢复尚未确认')
    await expect(panel(page).getByRole('button', { name: '读取中…' })).toBeDisabled()
    expect(api.requests).toHaveLength(2)
    expect(api.maximumActive()).toBe(1)
  } finally { held.release.resolve() }
  await expect(panel(page)).toContainText('当前提示刷新失败')
  await evidence(page, info, 'stale-refresh-error-history-error')
  historyFailed = false
  await page.locator('.resource-state--error').getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.getByText('当前范围暂无样本', { exact: true })).toBeVisible()
  await expect(panel(page)).toContainText('当前提示刷新失败')
  const current = await page.evaluate(() => new Date().toISOString())
  api.reply({ ...snapshot(rule('cpu', 'recovering', { sampleAt: current, receivedAt: current }), rule('memory', 'normal', { sampleAt: current, receivedAt: current })), observedAt: current })
  await panel(page).getByRole('button', { name: '重试当前提示', exact: true }).click()
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('恢复观察')
  await expect(panel(page).locator('.resource-alerts__error')).toHaveCount(0)
  expect(api.maximumActive()).toBe(1)
  expect(api.writes).toEqual([])
})

test('卸载和退出会话取消轮询，迟到触发结果不会进入登录页', async ({ page }) => {
  const api = await fixture(page, snapshot(rule('cpu', 'firing')))
  const held = api.hold()
  await page.route('**/api/v1/auth/logout', (route) => route.fulfill({ status: 204 }))
  try {
    await open(page)
    await held.received.promise
    await page.getByRole('button', { name: '退出登录', exact: true }).click()
    await expect(page.getByRole('heading', { name: '进入 NAS Control Plane', exact: true })).toBeVisible()
    held.release.resolve()
    await held.finished.promise
    await page.clock.runFor(30_000)
    await expect(panel(page)).toHaveCount(0)
    await expect(page.getByText('CPU 持续高使用率', { exact: true })).toHaveCount(0)
    expect(api.requests).toHaveLength(1)
    expect(api.writes).toEqual([])
  } finally { held.release.resolve() }
})

test('冷启动缺读数不显示零值，重复响应时间不能刷新旧样本', async ({ page }) => {
  const missing = { availability: 'unknown' as const, reasonCode: 'MONITORING_ALERTS_NO_SAMPLE', value: null, sampleAt: null, receivedAt: null }
  const api = await fixture(page, snapshot(rule('cpu', 'normal', missing), rule('memory', 'normal', missing)))
  await open(page)
  for (const kind of ['cpu', 'memory'] as const) {
    await expect(card(page, kind).locator('.status-pill')).toHaveText('暂不可评估')
    await expect(card(page, kind)).toContainText('暂无有效值')
    await expect(card(page, kind)).not.toContainText('0.0%')
  }
  api.reply(snapshot(rule('cpu', 'firing')))
  await refresh(page)
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('已触发')
  await page.clock.runFor(16_000)
  const current = await page.evaluate(() => new Date().toISOString())
  api.reply({ ...snapshot(rule('cpu', 'firing')), observedAt: current })
  await refresh(page)
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('数据已过期')
  await expect(card(page, 'cpu')).toContainText('恢复尚未确认')
  expect(api.writes).toEqual([])
})

test('隐藏暂停读取，恢复可见仅立即 GET 一次，离开监控页停止轮询', async ({ page }) => {
  const api = await fixture(page)
  await open(page)
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('未达到告警条件')
  const before = api.requests.length
  // 确定性的 Page Visibility fixture，不依赖 CI 窗口管理器切换前台标签。
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await page.clock.runFor(30_000)
  expect(api.requests).toHaveLength(before)
  const current = await page.evaluate(() => new Date().toISOString())
  api.reply(snapshot(rule('cpu', 'pending', { sampleAt: current, receivedAt: current }), rule('memory', 'normal', { sampleAt: current, receivedAt: current })))
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await expect(card(page, 'cpu').locator('.status-pill')).toHaveText('等待持续确认')
  expect(api.requests).toHaveLength(before + 1)
  await page.getByRole('link', { name: '总览', exact: true }).click()
  await expect(page.getByRole('heading', { name: '系统总览', exact: true })).toBeVisible()
  await page.clock.runFor(30_000)
  expect(api.requests).toHaveLength(before + 1)
  expect(api.writes).toEqual([])
})
