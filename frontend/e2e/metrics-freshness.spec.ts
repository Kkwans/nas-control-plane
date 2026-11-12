import { test, expect, type Page, type TestInfo } from '@playwright/test'
import { installMockApi, overviewDocker, summary } from './mockApi'

async function emit(page: Page, detail: { action?: 'open' | 'error'; snapshot?: unknown }) {
  await page.evaluate((value) => window.dispatchEvent(new CustomEvent('ncp:realtime-fixture', { detail: value })), detail)
}
async function start(page: Page, path = '/') {
  await page.clock.install({ time: new Date(summary.collectedAt) })
  await installMockApi(page, { clock: false, manualRealtime: true })
  await page.goto(path, { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: path === '/' ? '系统总览' : '系统监控', exact: true })).toBeVisible()
  await expect(page.locator('.topbar .connection-state')).toHaveClass(/connection-state--connected/)
}

test('已连接的流静默过期、旧事件和重连不标绿，新采集数据恢复一致健康状态', async ({ page }, testInfo) => {
  await start(page)
  const badge = page.locator('.topbar .connection-state')
  await expect(badge).toHaveClass(/connection-state--connected/)
  await expect(page.getByText('运行稳定', { exact: true })).toBeVisible()
  await page.clock.fastForward(16_000)
  await expect(badge).toContainText('显示上次数据')
  await expect(badge).toHaveClass(/connection-state--degraded/)
  await expect(page.getByText('运行稳定', { exact: true })).toHaveCount(0)
  await expect(page.locator('.snapshot-health-note')).toContainText('上次更新')
  await capture(page, testInfo, 'overview-stale')
  await emit(page, { action: 'open' })
  await emit(page, { snapshot: { collectedAt: '2026-08-24T02:00:16.000Z', summary, docker: overviewDocker } })
  await expect(badge).toHaveClass(/connection-state--degraded/)
  const current = await page.evaluate(() => new Date().toISOString())
  await emit(page, { snapshot: { collectedAt: current, summary: { ...summary, collectedAt: current }, docker: { ...overviewDocker, collectedAt: current } } })
  await expect(badge).toHaveClass(/connection-state--connected/)
  await expect(page.getByText('运行稳定', { exact: true })).toBeVisible()
})

test('源错误与部分警告可见，CPU 和内存缺失不会显示健康零值', async ({ page }, testInfo) => {
  await start(page)
  const badge = page.locator('.topbar .connection-state')
  await emit(page, { snapshot: { collectedAt: summary.collectedAt, docker: overviewDocker, errors: ['SYSTEM_SUMMARY_UNAVAILABLE'] } })
  await expect(badge).toContainText('系统数据待恢复')
  await expect(page.locator('.metric-card').filter({ hasText: 'CPU 使用率' }).locator('strong')).toHaveText('24.5%')
  await emit(page, { action: 'open' })
  await expect(badge).toHaveClass(/connection-state--degraded/)
  await page.clock.fastForward(5000)
  const current = await page.evaluate(() => new Date().toISOString())
  await emit(page, { snapshot: {
    collectedAt: current,
    summary: { ...summary, collectedAt: current, cpu: { ...summary.cpu, usagePercent: 0 }, memory: { totalBytes: 0, usedBytes: 0, availableBytes: 0 }, warnings: [{ code: 'CPU_UNAVAILABLE', source: 'cpu' }, { code: 'MEMORY_UNAVAILABLE', source: 'memory' }] },
    docker: { ...overviewDocker, collectedAt: current },
    errors: ['MONITORING_SAMPLE_INCOMPLETE'],
  } })
  await expect(badge).toContainText('部分指标不可用')
  await expect(page.locator('.metric-card').filter({ hasText: 'CPU 使用率' }).locator('strong')).toHaveText('—')
  await expect(page.locator('.metric-card').filter({ hasText: '内存使用' }).locator('strong')).toHaveText('—')
  await expect(page.getByText('运行稳定', { exact: true })).toHaveCount(0)
  await capture(page, testInfo, 'overview-partial')
})

test('持久化历史不会被 5 秒实时尾点或不完整 summary 改写', async ({ page }, testInfo) => {
  await installMockApi(page, { manualRealtime: true })
  let requests = 0
  await page.route('**/api/v1/monitoring/samples**', (route) => {
    requests += 1
    return route.fulfill({ json: [{ collectedAt: summary.collectedAt, cpuPercent: 18, memoryPercent: 30, load1: 1, diskPercent: 20, networkReceiveBytes: 100, networkTransmitBytes: 50 }] })
  })
  await page.goto('/monitoring', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: '系统监控', exact: true })).toBeVisible()
  await expect(page.getByText('18.0%', { exact: true })).toBeVisible()
  await expect(page.locator('.topbar .connection-state')).toHaveClass(/connection-state--connected/)
  await page.clock.fastForward(5000)
  const current = await page.evaluate(() => new Date().toISOString())
  await emit(page, { snapshot: { collectedAt: current, summary: { ...summary, collectedAt: current, warnings: [{ code: 'CPU_UNAVAILABLE', source: 'cpu' }] } } })
  await expect(page.locator('.topbar .connection-state')).toContainText('部分指标不可用')
  await expect(page.getByText('18.0%', { exact: true })).toBeVisible()
  expect(requests).toBe(1)
  await expect(page.getByText('当前资源提示独立评估；历史图表每分钟记录首个有效样本，时间范围仅作用于历史', { exact: true })).toBeVisible()
  await expect(page.locator('.monitor-history-note')).toContainText('折线空白表示缺少可用样本')
  await capture(page, testInfo, 'monitoring-persisted-only')
})


test('普通 fixture 在 15 秒后仍持续采集新快照，日期与单调时钟一致前进', async ({ page }) => {
  await installMockApi(page)
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  const badge = page.locator('.topbar .connection-state')
  await expect(badge).toHaveClass(/connection-state--connected/)
  const before = await page.evaluate(() => Date.now())
  await page.clock.runFor(20_000)
  const after = await page.evaluate(() => Date.now())
  expect(after - before).toBeGreaterThanOrEqual(20_000)
  await expect(badge).toHaveClass(/connection-state--connected/)
  await expect(page.getByText('运行稳定', { exact: true })).toBeVisible()
  await expect(page.locator('.snapshot-health-note')).toHaveCount(0)
})


async function capture(page: Page, testInfo: TestInfo, state: string) {
  if (process.env.NCP_CAPTURE_UI !== '1') return
  const path = testInfo.outputPath('ui-captures', `fixture-metrics-${state}.png`)
  await page.screenshot({ path, fullPage: true, animations: 'disabled' })
  await testInfo.attach(`fixture-metrics-${state}`, { path, contentType: 'image/png' })
}

test('移动监控页在 320、390、768 像素均可见过期与源错误，健康徽标不裁切', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await start(page, '/monitoring')
  const badge = page.locator('.topbar .connection-state')
  await page.clock.fastForward(16_000)
  await expect(badge).toBeVisible()
  await expect(badge).toContainText('显示上次数据')
  const current = await page.evaluate(() => new Date().toISOString())
  await emit(page, { snapshot: { collectedAt: current, summary: { ...summary, collectedAt: current }, errors: ['MONITORING_WRITE_FAILED'] } })
  await expect(badge).toContainText('历史写入失败')
  await expect(badge).toBeVisible()
  await emit(page, { snapshot: { collectedAt: current, errors: ['SYSTEM_SUMMARY_UNAVAILABLE'] } })
  for (const width of [320, 390, 768]) {
    await page.setViewportSize({ width, height: 844 })
    await expect(badge).toBeVisible()
    await expect(badge).toContainText('系统数据待恢复')
    const layout = await badge.evaluate((element) => {
      const bounds = element.getBoundingClientRect()
      return { left: bounds.left, right: bounds.right, content: element.scrollWidth, width: element.clientWidth, body: document.body.scrollWidth, viewport: window.innerWidth }
    })
    expect(layout.left).toBeGreaterThanOrEqual(0)
    expect(layout.right).toBeLessThanOrEqual(layout.viewport)
    expect(layout.content).toBeLessThanOrEqual(layout.width)
    expect(layout.body).toBeLessThanOrEqual(layout.viewport)
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await capture(page, testInfo, 'monitoring-mobile-source-error-390')
})
