import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { test, expect, type Locator, type Page, type TestInfo } from '@playwright/test'
import type { MetricSample } from '../src/api/control'
import { monitoringChartTokens } from '../src/domain/monitoring/chartTokens'
import { installMockApi, summary } from './mockApi'

test.use({
  viewport: { width: 1440, height: 900 },
  timezoneId: 'UTC',
  serviceWorkers: 'block',
  contextOptions: { reducedMotion: 'reduce' },
})

const minuteMs = 60_000
const historyEnd = Date.parse(summary.collectedAt)
const historyStart = historyEnd - 60 * minuteMs
// 与 RealtimeTrendChart 的 canvas grid 保持同步；图例和单位在 canvas 外，不使用页面绝对坐标。
const plotGrid = { left: 45, right: 12, top: 10, bottom: 26 }
const regions = { left: [0.015, 0.075], center: [0.2, 0.8], right: [0.925, 0.985] } as const
type RegionName = keyof typeof regions
type RGB = readonly [number, number, number]
type HistoryReply = { status: number; body: unknown }

function rgb(hex: string): RGB {
  expect(hex).toMatch(/^#[0-9a-f]{6}$/i)
  return [Number.parseInt(hex.slice(1, 3), 16), Number.parseInt(hex.slice(3, 5), 16), Number.parseInt(hex.slice(5, 7), 16)]
}

function sampleAt(minute: number, start = historyStart): MetricSample {
  return {
    collectedAt: new Date(start + minute * minuteMs).toISOString(),
    cpuPercent: 24,
    memoryPercent: minute <= 5 ? 37 : minute >= 55 ? 43 : 37 + (minute - 5) * 6 / 50,
    load1: 1.2,
    diskPercent: 28,
    networkReceiveBytes: 1_000_000 + minute * 60 * 1024 * 4,
    networkTransmitBytes: 800_000 + minute * 60 * 1024 * 2,
    diskReadBytes: 2_000_000 + minute * 60 * 1024 ** 2,
    diskWriteBytes: 1_000_000 + minute * 60 * 1024 ** 2 / 2,
  }
}

function hourSamples(gap = false) {
  return Array.from({ length: 61 }, (_, minute) => sampleAt(minute))
    .filter((_, minute) => !gap || minute <= 5 || minute >= 55)
}

function sensorSamples(gap = false): MetricSample[] {
  return Array.from({ length: 11 }, (_, minute) => ({
    ...sampleAt(minute, historyEnd - 10 * minuteMs),
    temperatures: [
      { name: 'fixture-sensor-a', temperatureCelsius: 37 },
      ...(!gap || minute <= 2 || minute >= 8 ? [{ name: 'fixture-sensor-b', temperatureCelsius: 43 }] : []),
    ],
  }))
}

async function startHistory(page: Page, initial: HistoryReply) {
  await page.clock.install({ time: new Date(summary.collectedAt) })
  // 样本时间固定，UI 时钟继续前进；冻结 Date.now 会让 Vue 丢弃标签向选择框冒泡的 click。
  await installMockApi(page, { clock: false, manualRealtime: true })
  let reply = initial
  // 后注册的精确 fixture 覆盖通用 mock；其余 /api/v1/** 也只由 installMockApi 响应。
  await page.route('**/api/v1/monitoring/samples**', (route) => route.fulfill({
    status: reply.status,
    contentType: 'application/json',
    body: JSON.stringify(reply.body),
  }))
  await page.goto('/monitoring', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: '系统监控', exact: true })).toBeVisible()
  return (next: HistoryReply) => { reply = next }
}

function chartCard(page: Page, title: string) {
  return page.locator(`article[aria-labelledby="${title}-chart-title"]`)
}

async function readPlotPixels(card: Locator, color: RGB, center: readonly [number, number] = regions.center) {
  const canvas = card.locator('.trend-chart__canvas canvas').first()
  await expect(canvas).toBeVisible()
  return canvas.evaluate((element, args) => {
    if (!(element instanceof HTMLCanvasElement)) throw new Error('趋势图未使用 canvas')
    const context = element.getContext('2d')
    if (!context) throw new Error('无法读取趋势图的实际像素')
    const bounds = element.getBoundingClientRect()
    const scaleX = element.width / bounds.width
    const scaleY = element.height / bounds.height
    const plotWidth = bounds.width - args.grid.left - args.grid.right
    if (!Number.isFinite(scaleX) || !Number.isFinite(scaleY) || plotWidth <= 0) throw new Error('趋势图没有有效绘图区')
    const pixels = context.getImageData(0, 0, element.width, element.height).data
    // 排除图例、坐标标签；包含绘图区底边，使伪造的 0 速率贴底线也会失败。
    const top = Math.max(0, Math.floor(args.grid.top * scaleY))
    const bottom = Math.min(element.height, Math.ceil((bounds.height - args.grid.bottom) * scaleY) + 1)
    const result = {} as Record<RegionName, { pixels: number; columns: number; width: number; coverage: number }>
    for (const name of ['left', 'center', 'right'] as const) {
      const interval = name === 'center' ? args.center : args.regions[name]
      const left = Math.max(0, Math.ceil((args.grid.left + plotWidth * interval[0]) * scaleX))
      const right = Math.min(element.width, Math.floor((args.grid.left + plotWidth * interval[1]) * scaleX))
      let count = 0
      let columns = 0
      for (let x = left; x < right; x += 1) {
        let columnHasLine = false
        for (let y = top; y < bottom; y += 1) {
          const offset = (y * element.width + x) * 4
          // 明确 series token 的近不透明像素；7% area、灰色网格及文字不算折线。
          if ((pixels[offset + 3] ?? 0) >= 230 && args.color.every((channel, index) => Math.abs((pixels[offset + index] ?? -255) - channel) <= 8)) {
            count += 1
            columnHasLine = true
          }
        }
        if (columnHasLine) columns += 1
      }
      result[name] = { pixels: count, columns, width: right - left, coverage: columns / (right - left) }
    }
    return {
      width: element.width,
      height: element.height,
      cssWidth: bounds.width,
      cssHeight: bounds.height,
      scaleX,
      scaleY,
      dpr: window.devicePixelRatio,
      regions: result,
    }
  }, { grid: plotGrid, regions, color, center })
}

async function expectSeriesPixels(card: Locator, color: RGB, visible: RegionName[], center: readonly [number, number] = regions.center) {
  // 正对照与缺口两端都必须覆盖大部分预期横向区间，不能让空白画布通过。
  await expect.poll(async () => {
    const probe = await readPlotPixels(card, color, center)
    return Math.min(...visible.map((name) => probe.regions[name].coverage))
  }, { message: '实际 canvas 中应有连续的 series 折线像素' }).toBeGreaterThan(0.7)
  const probe = await readPlotPixels(card, color, center)
  expect(probe.cssWidth).toBeGreaterThan(plotGrid.left + plotGrid.right)
  expect(probe.cssHeight).toBeGreaterThan(plotGrid.top + plotGrid.bottom)
  expect(Math.abs(probe.scaleX - probe.dpr)).toBeLessThan(0.02)
  expect(Math.abs(probe.scaleY - probe.dpr)).toBeLessThan(0.02)
  return probe
}

async function capture(card: Locator, testInfo: TestInfo, state: string) {
  if (process.env.NCP_CAPTURE_UI !== '1') return
  const name = `fixture-monitoring-history-${state}`
  const path = testInfo.outputPath('ui-captures', `${name}.png`)
  await card.screenshot({ path, animations: 'disabled' })
  await testInfo.attach(name, { path, contentType: 'image/png' })
}

test('真实 time axis 保留一小时缺口，内存与网络的中央绘图区没有跨段或贴底折线', async ({ page }, testInfo) => {
  expect(monitoringChartTokens.memory).toBe('#16866a')
  const changeReply = await startHistory(page, { status: 200, body: hourSamples() })
  const memory = chartCard(page, 'memory')
  const network = chartCard(page, 'network')
  const memoryColor = rgb(monitoringChartTokens.memory)
  const receiveColor = rgb(monitoringChartTokens.receive)
  const transmitColor = rgb(monitoringChartTokens.transmit)

  // 同样的首尾时间及 37%..43% 值域；先证明颜色检测确实能找到真实折线。
  const control = await expectSeriesPixels(memory, memoryColor, ['left', 'center', 'right'])
  await expectSeriesPixels(network, receiveColor, ['left', 'center', 'right'])
  await expectSeriesPixels(network, transmitColor, ['left', 'center', 'right'])
  await capture(memory, testInfo, 'memory-continuous-control')
  await capture(network, testInfo, 'network-continuous-control')

  changeReply({ status: 200, body: hourSamples(true) })
  await page.reload({ waitUntil: 'domcontentloaded' })
  const gap = await expectSeriesPixels(memory, memoryColor, ['left', 'right'])
  const receiveGap = await expectSeriesPixels(network, receiveColor, ['left', 'right'])
  const transmitGap = await expectSeriesPixels(network, transmitColor, ['left', 'right'])
  await capture(memory, testInfo, 'memory-gap')
  await capture(network, testInfo, 'network-gap')
  expect([gap.width, gap.height]).toEqual([control.width, control.height])
  // 0..5 和 55..60 分钟只占首尾各 8.3%；category axis 会拉宽至中央而失败。
  expect(gap.regions.center.pixels, '内存的 20%..80% 时间范围必须保持空白').toBe(0)
  expect(receiveGap.regions.center.pixels, '缺口中不得出现平均接收速率或伪零线').toBe(0)
  expect(transmitGap.regions.center.pixels, '缺口中不得出现平均发送速率或伪零线').toBe(0)
})

test('单个温度传感器缺读数时独立断线，另一传感器保持连续', async ({ page }, testInfo) => {
  const changeReply = await startHistory(page, { status: 200, body: sensorSamples() })
  const temperature = chartCard(page, 'temperature')
  const sensorAColor = rgb(monitoringChartTokens.temperature[0])
  const sensorBColor = rgb(monitoringChartTokens.temperature[1])
  const center = [0.3, 0.7] as const
  await expectSeriesPixels(temperature, sensorAColor, ['left', 'center', 'right'], center)
  await expectSeriesPixels(temperature, sensorBColor, ['left', 'center', 'right'], center)
  await capture(temperature, testInfo, 'sensors-continuous-control')

  changeReply({ status: 200, body: sensorSamples(true) })
  await page.reload({ waitUntil: 'domcontentloaded' })
  await expectSeriesPixels(temperature, sensorAColor, ['left', 'center', 'right'], center)
  const sensorB = await expectSeriesPixels(temperature, sensorBColor, ['left', 'right'], center)
  await capture(temperature, testInfo, 'sensors-independent-gap')
  expect(sensorB.regions.center.pixels, '缺失的 B 传感器不能沿用旧值或借用 A 的读数').toBe(0)
})

for (const failure of [
  { status: 422, code: 'MONITORING_HISTORY_LIMIT_EXCEEDED', message: /超过.*上限|读取上限/ },
  { status: 500, code: 'MONITORING_HISTORY_INVALID', message: /无效数据/ },
  { status: 500, code: 'MONITORING_READ_FAILED', message: /读取失败|加载失败/ },
]) {
  test(`${failure.status} ${failure.code} 显示读取错误而非无历史，重试可恢复`, async ({ page }, testInfo) => {
    const changeReply = await startHistory(page, {
      status: failure.status,
      body: { code: failure.code, message: 'Fixture history request failed', requestId: `fixture-${failure.code}` },
    })
    const error = page.locator('.resource-state--error')
    await expect(error).toBeVisible()
    await expect(error).toContainText('监控历史加载失败')
    await expect(error).toContainText(failure.code)
    await expect(error).toContainText(failure.message)
    await expect(error).not.toContainText('确认 Agent')
    await expect(page.locator('.resource-state--empty')).toHaveCount(0)
    await expect(page.locator('.monitor-grid')).toHaveCount(0)
    await expect(page.getByText('当前范围暂无样本', { exact: true })).toHaveCount(0)
    await capture(error, testInfo, `error-${failure.code.toLowerCase()}`)

    changeReply({ status: 200, body: hourSamples(true) })
    await error.getByRole('button', { name: '重试', exact: true }).click()
    await expect(chartCard(page, 'memory').locator('canvas').first()).toBeVisible()
    await expect(page.locator('.resource-state--error')).toHaveCount(0)
    await expect(page.locator('.resource-state--empty')).toHaveCount(0)
  })
}

test('只有成功的空数组呈现单一历史空态', async ({ page }, testInfo) => {
  await startHistory(page, { status: 200, body: [] })
  const empty = page.locator('.resource-state--empty')
  await expect(empty).toHaveCount(1)
  await expect(empty).toContainText('当前范围暂无样本')
  await expect(page.locator('.resource-state--error')).toHaveCount(0)
  await expect(page.locator('.monitor-grid')).toHaveCount(0)
  await expect(page.locator('.trend-chart')).toHaveCount(0)
  await capture(empty, testInfo, 'successful-empty')
})

const longSensorNames = Array.from({ length: 64 }, (_, index) => `sensor-${String(index).padStart(2, '0')}-机箱内部温度探针-coretemp:Package-id-${index}-<img src=x onerror=alert(1)>-very-long-sensor-name`)

function manySensorSamples(): MetricSample[] {
  return Array.from({ length: 6 }, (_, minute) => ({
    ...sampleAt(minute, historyEnd - 5 * minuteMs),
    temperatures: longSensorNames.map((name, index) => ({ name, temperatureCelsius: 30 + index + minute })),
  }))
}

async function expectSeparatedChartLayout(card: Locator) {
  await expect(card.locator('.trend-chart__canvas canvas').first()).toBeVisible()
  const layout = await card.evaluate((element) => {
    const chart = element.querySelector('.trend-chart')!
    const chartBounds = chart.getBoundingClientRect()
    const legend = chart.querySelector('.trend-chart__legend')!.getBoundingClientRect()
    const units = chart.querySelector('.trend-chart__axis-units')!.getBoundingClientRect()
    const canvas = chart.querySelector('.trend-chart__canvas')!.getBoundingClientRect()
    const items = [...chart.querySelectorAll('.trend-chart__legend-item')].map((item) => item.getBoundingClientRect())
    return {
      legendToUnits: units.top - legend.bottom,
      unitsToCanvas: canvas.top - units.bottom,
      canvasHeight: canvas.height,
      rightEdge: chartBounds.right,
      leftEdge: chartBounds.left,
      viewport: window.innerWidth,
      contained: items.every((item) => item.width > 0 && item.left >= chartBounds.left - 1 && item.right <= chartBounds.right + 1 && item.bottom <= units.top + 1),
      overlap: items.some((item, index) => items.slice(index + 1).some((other) => Math.min(item.right, other.right) - Math.max(item.left, other.left) > 1 && Math.min(item.bottom, other.bottom) - Math.max(item.top, other.top) > 1)),
    }
  })
  expect(layout.legendToUnits, '图例与单位使用独立的布局行').toBeGreaterThanOrEqual(-1)
  expect(layout.unitsToCanvas, '单位行不会覆盖实际绘图区').toBeGreaterThanOrEqual(-1)
  expect(layout.canvasHeight, '长图例不能压扁趋势绘图区').toBeGreaterThanOrEqual(170)
  expect(layout.leftEdge).toBeGreaterThanOrEqual(0)
  expect(layout.rightEdge).toBeLessThanOrEqual(layout.viewport)
  expect(layout.contained).toBe(true)
  expect(layout.overlap).toBe(false)
}

async function captureViewport(page: Page, testInfo: TestInfo, state: string) {
  if (process.env.NCP_CAPTURE_UI !== '1') return
  const name = `fixture-monitoring-history-${state}`
  const path = testInfo.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path, animations: 'disabled' })
  await testInfo.attach(name, { path, contentType: 'image/png' })
}

for (const width of [320, 390]) {
  test(`${width}px 下六张监控图表分隔单位与图例，八个长传感器名称和真实下拉菜单不溢出`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    await startHistory(page, { status: 200, body: manySensorSamples() })
    for (const title of ['cpu', 'memory', 'storage', 'network', 'disk-io', 'temperature']) {
      const card = chartCard(page, title)
      await expectSeparatedChartLayout(card)
      await capture(card, testInfo, `${width}px-${title}-separated-labels`)
    }
    const temperature = chartCard(page, 'temperature')
    const legends = temperature.locator('.trend-chart__legend-item')
    await expect(legends).toHaveCount(8)
    await expect(temperature.locator('img')).toHaveCount(0)
    await expect(temperature.locator('.monitor-sensor-select__count')).toHaveText('已显示 8 / 64 个传感器，最多同时显示 8 个')
    for (let index = 0; index < 8; index += 1) {
      await expect(legends.nth(index)).toHaveAttribute('title', longSensorNames[index]!)
      await expect(legends.nth(index)).toHaveAccessibleName(longSensorNames[index]!)
    }
    // HTML 图例保留键盘切换行为，并用真实 canvas 证明隐藏/恢复曲线生效。
    const memory = chartCard(page, 'memory')
    const memoryLegend = memory.getByRole('button', { name: '内存', exact: true })
    await expectSeriesPixels(memory, rgb(monitoringChartTokens.memory), ['left', 'center', 'right'])
    await memoryLegend.focus()
    await memoryLegend.press('Space')
    await expect(memoryLegend).toHaveAttribute('aria-pressed', 'false')
    await expect.poll(async () => (await readPlotPixels(memory, rgb(monitoringChartTokens.memory))).regions.center.pixels).toBe(0)
    await memoryLegend.press('Space')
    await expect(memoryLegend).toHaveAttribute('aria-pressed', 'true')
    await expectSeriesPixels(memory, rgb(monitoringChartTokens.memory), ['left', 'center', 'right'])

    const select = temperature.getByRole('combobox', { name: '选择温度传感器，最多 8 个' })
    // 多选收起时，Element Plus 会把空搜索 input 隐藏在标签后；用户从可见控件进入。
    const selectControl = temperature.locator('.monitor-sensor-select .el-select__wrapper')
    await selectControl.click()
    await expect(select).toBeFocused()
    await expect(select).toHaveAttribute('aria-expanded', 'true')
    await expect(select.locator('..')).not.toHaveClass(/\bis-hidden\b/)
    expect(await select.evaluate((input) => {
      const bounds = input.getBoundingClientRect()
      return bounds.width > 0 && bounds.height > 0 && document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2) === input
    }), '展开后搜索 input 必须拥有真实、未被标签遮挡的命中区域').toBe(true)
    const dropdown = page.locator('.el-popper.monitor-sensor-select-popper:visible')
    await expect(dropdown).toBeVisible()
    await expect(dropdown.getByRole('option')).toHaveCount(50)
    await expect(dropdown.locator('img')).toHaveCount(0)
    const ninth = dropdown.getByRole('option', { name: longSensorNames[8]!, exact: true })
    await expect(ninth).toHaveAttribute('aria-disabled', 'true')
    const dropdownBox = await dropdown.boundingBox()
    expect(dropdownBox).not.toBeNull()
    expect(dropdownBox!.x).toBeGreaterThanOrEqual(0)
    expect(dropdownBox!.x + dropdownBox!.width).toBeLessThanOrEqual(width)
    await captureViewport(page, testInfo, `${width}px-eight-long-sensors-dropdown`)

    await dropdown.getByRole('option', { name: longSensorNames[0]!, exact: true }).click()
    await expect(temperature.locator('.monitor-sensor-select__count')).toContainText('已显示 7 / 64')
    await expect(ninth).not.toHaveAttribute('aria-disabled', 'true')
    await ninth.click()
    await expect(temperature.locator('.monitor-sensor-select__count')).toContainText('已显示 8 / 64')
    await expect(legends).toHaveCount(8)
    await expect(temperature.getByRole('button', { name: longSensorNames[0]!, exact: true })).toHaveCount(0)
    await expect(temperature.getByRole('button', { name: longSensorNames[8]!, exact: true })).toBeVisible()
    await expect(select).toBeFocused()
    await select.pressSequentially('sensor-63')
    await expect(select).toHaveValue('sensor-63')
    const searched = dropdown.getByRole('option', { name: longSensorNames[63]!, exact: true })
    await expect(searched).toBeVisible()
    await expect(searched).toHaveAttribute('aria-disabled', 'true')
    await expect(dropdown.getByRole('option')).toHaveCount(9)
    await captureViewport(page, testInfo, `${width}px-bounded-sensor-search`)
    // Element Plus 的第一次 Escape 清空搜索，第二次关闭；用真实按键覆盖这两步。
    await select.press('Escape')
    await expect(select).toHaveValue('')
    await expect(dropdown).toBeVisible()
    await select.press('Escape')
    await expect(dropdown).toHaveCount(0)
    await expect(select).toHaveAttribute('aria-expanded', 'false')
    await select.press('ArrowDown')
    await expect(dropdown).toBeVisible()
    await expect(select).toHaveAttribute('aria-expanded', 'true')
    await expect(dropdown.getByRole('option')).toHaveCount(50)
    await expect(temperature.locator('.monitor-sensor-select__count')).toContainText('已显示 8 / 64')
    await select.press('Escape')
    await expect(dropdown).toHaveCount(0)
    await expectSeparatedChartLayout(temperature)
    const overflow = await page.evaluate(() => Math.max(document.body.scrollWidth, document.documentElement.scrollWidth) - window.innerWidth)
    expect(overflow, '传感器名称、标签与已关闭的下拉面板不能撑宽页面').toBeLessThanOrEqual(0)
  })
}

// F10：所有历史请求均来自严格只读 fixture；实际浏览器与证据仅由 CI 执行。
const recoveryEndpoint = '/api/v1/monitoring/samples'
const recoveryHash = (value: string | Buffer) => createHash('sha256').update(value).digest('hex')
function recoveryBarrier() {
  let release!: () => void
  const promise = new Promise<void>(resolve => { release = resolve })
  return { promise, release }
}
type RecoveryReply = {
  range: '6h' | '1h' | 'precise'
  outcome: 'success' | 'error' | 'empty'
  value: number
  held?: ReturnType<typeof recoveryBarrier>
}
async function recoveryFixture(page: Page, origin: string) {
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  await page.clock.install({ time: new Date(summary.collectedAt) })
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: string[] = [], denied: string[] = [], writes: string[] = [], errors: string[] = []
  const replies: RecoveryReply[] = [], gates: ReturnType<typeof recoveryBarrier>[] = []
  const history: Array<{ target: string; browserAt: string; from: string | null; to: string | null; outcome: string; samples: MetricSample[]; delivered: boolean }> = []
  const captures: Array<{ file: string; sha256: string }> = []
  const reads = new Set(['/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities', '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/monitoring/alerts'])
  page.on('pageerror', error => errors.push(error.message))
  await page.routeWebSocket('**', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), target = `${request.method()} ${url.pathname}${url.search}`
    if (url.pathname.startsWith('/api/')) requests.push(target)
    if (request.method() !== 'GET') writes.push(target)
    if (url.origin !== origin || request.method() !== 'GET') { denied.push(target); return route.abort('blockedbyclient') }
    if (url.pathname === recoveryEndpoint) {
      const next = replies.shift(), from = url.searchParams.get('from'), to = url.searchParams.get('to')
      const precise = from !== null && to !== null && [...url.searchParams.keys()].sort().join(',') === 'from,to' && Number.isFinite(Date.parse(to)) && Date.parse(to) - Date.parse(from) === 6 * 60 * minuteMs
      const valid = next && (next.range === 'precise' ? precise : url.search === `?range=${next.range}`)
      if (!valid || !next) { denied.push(target); return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED' } }) }
      const browserAt = await page.evaluate(() => new Date().toISOString())
      const end = Date.parse(to ?? browserAt)
      const samples = next.outcome === 'success' ? Array.from({ length: 61 }, (_, minute) => ({
        ...sampleAt(minute, end - 60 * minuteMs), cpuPercent: next.value, memoryPercent: next.value + minute / 10,
        temperatures: [{ name: 'fixture-recovery-sensor', temperatureCelsius: next.value + 10 }],
      })) : []
      const entry = { target, browserAt, from, to, outcome: next.outcome, samples, delivered: false }
      history.push(entry)
      if (next.held) await next.held.promise
      await route.fulfill({ status: next.outcome === 'error' ? 503 : 200, headers: { 'Cache-Control': 'no-store' }, json: next.outcome === 'error' ? { code: 'MONITORING_READ_FAILED', message: 'Fixture refresh failed', requestId: 'fixture-monitoring-refresh' } : samples })
      entry.delivered = true
      return
    }
    if (url.pathname === '/api/v1/jobs' && !url.search) return route.fulfill({ json: { jobs: [] } })
    if (reads.has(url.pathname + url.search)) return route.fallback()
    if (!url.search && (['/', '/monitoring', '/favicon.svg'].includes(url.pathname) || /^\/assets\/[A-Za-z0-9_.-]+$/.test(url.pathname))) return route.fallback()
    denied.push(target); return route.abort('blockedbyclient')
  })
  return { requests, denied, writes, errors, history, captures,
    enqueue(range: RecoveryReply['range'], outcome: RecoveryReply['outcome'], value = 24, held = false) {
      const gate = held ? recoveryBarrier() : undefined
      if (gate) gates.push(gate)
      replies.push({ range, outcome, value, held: gate })
      return gate
    },
    release() { gates.forEach(gate => gate.release()) },
    assert() { expect(replies).toHaveLength(0); expect(denied).toEqual([]); expect(writes).toEqual([]); expect(errors).toEqual([]); expect(history.every(item => item.delivered)).toBe(true) },
  }
}
type RecoveryFixture = Awaited<ReturnType<typeof recoveryFixture>>
async function recoveryProvenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async file => [file, recoveryHash(await readFile(resolve(root, file)))]))) }
}
const recoveryNote = (page: Page) => page.locator('.monitor-history-note').filter({ has: page.locator('time') })
async function recoveryCharts(page: Page) {
  const cards = await page.locator('.monitor-grid:not(.monitor-grid--loading) .chart-card').evaluateAll(elements => elements.map(element => ({
    title: element.getAttribute('aria-labelledby'),
    latest: element.querySelector('.sr-only')?.textContent,
    // 包含时间轴文字和真实折线；不是仅比较 fixture 输入或节点数量。
    canvases: [...element.querySelectorAll('canvas')].map(canvas => canvas.toDataURL()),
  })))
  return cards.map(card => ({ ...card, canvases: card.canvases.map(recoveryHash) }))
}
async function stableRecoveryCharts(page: Page) {
  await page.evaluate(() => document.fonts.ready)
  await expect.poll(async () => {
    const before = await recoveryCharts(page)
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    return JSON.stringify(await recoveryCharts(page)) === JSON.stringify(before)
  }, { message: '真实图表在字体就绪和两个绘制帧后稳定' }).toBe(true)
}
async function recoveryGeometry(page: Page) {
  return page.locator('.workspace-page').evaluate(root => ({
    browserAt: new Date().toISOString(),
    focus: { tag: document.activeElement?.tagName, id: document.activeElement?.id, text: document.activeElement?.matches('button,input') ? document.activeElement.textContent : null, label: document.activeElement?.getAttribute('aria-label') },
    scroll: [...document.querySelectorAll<HTMLElement>('*')].filter(element => element.scrollTop || element.scrollLeft).map(element => ({ tag: element.tagName, className: element.className, top: element.scrollTop, left: element.scrollLeft })),
    heading: root.querySelector('h1')?.getBoundingClientRect().toJSON(),
    viewport: { width: innerWidth, height: innerHeight },
    root: { width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth },
    note: root.querySelector('.monitor-history-note:has(time)')?.textContent ?? null,
    readAt: root.querySelector('.monitor-history-note time')?.getAttribute('datetime') ?? null,
    fields: [...root.querySelectorAll('.monitor-history-note,.resource-state--error,.resource-state--empty,.chart-card')].map(element => {
      const box = element.getBoundingClientRect()
      return { text: element.textContent, bounds: box.toJSON(), overflow: element.scrollWidth - element.clientWidth }
    }),
    controls: [...root.querySelectorAll('.resource-state button')].map(element => {
      const box = element.getBoundingClientRect(), hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
      return { text: element.textContent, bounds: box.toJSON(), centerHit: !!hit && element.contains(hit) }
    }),
  }))
}
async function captureRecovery(page: Page, info: TestInfo, api: RecoveryFixture, state: string) {
  await page.evaluate(() => document.fonts.ready)
  await page.mouse.move(1, 10)
  await expect(page.getByRole('tooltip')).toHaveCount(0)
  await expect(page.locator('.el-message')).toHaveCount(0)
  const controlReachability = []
  for (const button of await page.locator('.resource-state--error').getByRole('button').all()) {
    await button.scrollIntoViewIfNeeded()
    controlReachability.push(await button.evaluate(element => {
      const box = element.getBoundingClientRect()
      const points = [[box.left + 5, box.top + 5], [box.right - 5, box.top + 5], [box.left + 5, box.bottom - 5], [box.right - 5, box.bottom - 5], [box.left + box.width / 2, box.top + box.height / 2]]
      return { text: element.textContent, bounds: box.toJSON(), hits: points.map(([x, y]) => { const hit = document.elementFromPoint(x!, y!); return Boolean(hit && element.contains(hit)) }) }
    }))
  }
  const reachableState = await recoveryGeometry(page)
  // 先验证实际可达性，再还原滚动，避免 fullPage 的 sticky 页头覆盖中部证据。
  await page.evaluate(() => {
    for (const element of document.querySelectorAll<HTMLElement>('*')) if (element.scrollTop || element.scrollLeft) element.scrollTo({ top: 0, left: 0, behavior: 'instant' })
    window.scrollTo({ top: 0, left: 0, behavior: 'instant' })
  })
  await stableRecoveryCharts(page)
  const before = await recoveryGeometry(page), chartsBefore = await recoveryCharts(page), requestsBefore = [...api.requests]
  const name = `monitoring-refresh-${page.viewportSize()!.width}-${state}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  const screenshot = await page.screenshot({ path: png, animations: 'disabled', fullPage: true })
  const afterCapture = await recoveryGeometry(page), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await recoveryGeometry(page)
  const file = `${name}-capture-state.json`, path = info.outputPath('ui-captures', file)
  await writeFile(path, JSON.stringify({ ...await recoveryProvenance(), fixtureOnly: true, nativeNASAcceptance: false, executionScope: 'CI browser with strict static fixture only', state, testTitle: info.title, screenshot: `${name}.png`, screenshotSha256: recoveryHash(screenshot), fullPage: true, scrollNormalization: 'all scrollers to 0,0 after reachability check', retryTargetBaseline: 'shared ResourceState 44px design token; no fixture style override', controlReachability, reachableState, before, afterCapture, afterAxe, chartsBefore, chartsAfter: await recoveryCharts(page), requestsBefore, requestsAfter: [...api.requests], allowedBackgroundPollCounts: Object.fromEntries(['GET /api/v1/monitoring/alerts', 'GET /api/v1/jobs'].map(target => [target, api.requests.slice(requestsBefore.length).filter(item => item === target).length])), history: api.history, denied: api.denied, writes: api.writes, errors: api.errors, axe }, null, 2))
  api.captures.push({ file, sha256: recoveryHash(await readFile(path)) })
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path, contentType: 'application/json' })
  expect(axe.violations).toEqual([])
  for (const control of controlReachability) {
    expect(control.bounds.width).toBeGreaterThanOrEqual(44)
    expect(control.bounds.height).toBeGreaterThanOrEqual(44)
    expect(control.hits).toEqual([true, true, true, true, true])
  }
  const incidental = api.requests.slice(requestsBefore.length)
  // 提示与任务的已有只读轮询继续运行；历史刷新、其他读取与所有写入不能由捕获触发。
  expect(incidental.every(target => ['GET /api/v1/monitoring/alerts', 'GET /api/v1/jobs'].includes(target))).toBe(true)
  expect(api.denied).toEqual([]); expect(api.writes).toEqual([]); expect(api.errors).toEqual([])
  for (const frame of [before, afterCapture, afterAxe]) {
    expect(frame.root.scrollWidth).toBeLessThanOrEqual(frame.root.width + 1)
    if (frame.heading) {
      expect(frame.heading.top).toBeGreaterThanOrEqual(0)
      expect(frame.heading.bottom).toBeLessThanOrEqual(frame.viewport.height)
    }
    for (const field of frame.fields) {
      expect(field.bounds.left).toBeGreaterThanOrEqual(-1)
      expect(field.bounds.right).toBeLessThanOrEqual(frame.viewport.width + 1)
      expect(field.overflow).toBeLessThanOrEqual(1)
    }
  }
  expect(afterCapture.focus).toEqual(before.focus); expect(afterAxe.focus).toEqual(before.focus)
  expect(afterCapture.note).toBe(before.note); expect(afterAxe.note).toBe(before.note)
  expect(await recoveryCharts(page)).toEqual(chartsBefore)
}

for (const width of [390, 1440]) {
  test(`${width}px F10 同范围刷新保留图表和实际读取范围，失败可重试，空结果及切换范围清空`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    const api = await recoveryFixture(page, new URL(baseURL!).origin)
    let journeyCompleted = false
    try {
      api.enqueue('6h', 'success')
      await page.goto('/monitoring', { waitUntil: 'domcontentloaded' })
      await expect(chartCard(page, 'memory').locator('canvas')).toBeVisible()
      api.enqueue('precise', 'success')
      await page.getByRole('button', { name: '将结束时间设为现在', exact: true }).click()
      await page.getByRole('button', { name: '应用精确时间范围', exact: true }).click()
      await expect.poll(() => api.history.length).toBe(2)
      await expect(page.getByRole('button', { name: '应用精确时间范围', exact: true })).toBeEnabled()
      await expectSeriesPixels(chartCard(page, 'memory'), rgb(monitoringChartTokens.memory), ['left', 'center', 'right'])
      await page.evaluate(() => document.fonts.ready)
      await expect(page.locator('.monitor-grid .chart-card canvas')).toHaveCount(6)
      await page.mouse.move(1, 10)
      await stableRecoveryCharts(page)
      const baseline = await recoveryCharts(page), note = await recoveryNote(page).innerText(), readAt = await recoveryNote(page).locator('time').getAttribute('datetime')
      expect(baseline).toHaveLength(6)
      expect(baseline.every(card => card.canvases.length > 0)).toBe(true)
      const successfulRange = api.history[1]!
      const rangeLabel = await page.evaluate(({ from, to }) => `${new Date(from!).toLocaleString('zh-CN', { hour12: false })} 至 ${new Date(to!).toLocaleString('zh-CN', { hour12: false })}`, successfulRange)
      expect(note).toContain(rangeLabel)
      await captureRecovery(page, info, api, 'successful-precise-range')

      // 浏览器时钟继续流动；前进一分钟让移动范围变化可见，绝不使用宿主机时间。
      await page.clock.fastForward(minuteMs)
      const failure = api.enqueue('precise', 'error', 24, true)!
      await page.getByRole('button', { name: '立即刷新数据', exact: true }).click()
      await expect.poll(() => api.history.length).toBe(3)
      expect(Date.parse(api.history[2]!.to!)).toBeGreaterThan(Date.parse(successfulRange.to!))
      const retained = async () => {
        await expect(recoveryNote(page)).toHaveText(note)
        await expect(recoveryNote(page).locator('time')).toHaveAttribute('datetime', readAt!)
        await expect(page.locator('.monitor-grid--loading')).toHaveCount(0)
        await expect(page.locator('.resource-state--empty')).toHaveCount(0)
        await page.mouse.move(1, 10)
        expect(await recoveryCharts(page)).toEqual(baseline)
      }
      await expect(page.locator('.monitor-history-note[role="status"]')).toHaveText('正在刷新，暂时显示上次成功读取的数据。')
      await retained()
      await captureRecovery(page, info, api, 'held-refresh-retained')
      failure.release()
      const error = page.locator('.resource-state--error')
      await expect(error).toContainText('MONITORING_READ_FAILED')
      await expect(page.locator('.monitor-history-note[role="status"]')).toHaveText('刷新失败，当前显示上次成功读取的旧数据。')
      await retained()
      await error.getByRole('button', { name: '重试', exact: true }).focus()
      await captureRecovery(page, info, api, 'failed-refresh-retained')

      api.enqueue('precise', 'success', 54)
      await error.getByRole('button', { name: '重试', exact: true }).press('Enter')
      await expect(error).toHaveCount(0)
      await expect(chartCard(page, 'memory').locator('.sr-only')).toContainText('60.0 %')
      await expect(recoveryNote(page).locator('time')).not.toHaveAttribute('datetime', readAt!)
      await expect(recoveryNote(page)).not.toHaveText(note)
      await expect(page.locator('.monitor-history-note[role="status"]')).toHaveCount(0)
      expect(await recoveryCharts(page)).not.toEqual(baseline)
      await captureRecovery(page, info, api, 'retry-replaced-snapshot')

      // 显式换范围必须立即移除旧图，即便新请求仍在等待。
      const switched = api.enqueue('1h', 'success', 64, true)!
      await page.getByRole('button', { name: '1 小时', exact: true }).click()
      await expect.poll(() => api.history.length).toBe(5)
      await expect(page.locator('.monitor-grid--loading')).toBeVisible()
      await expect(page.locator('.trend-chart')).toHaveCount(0)
      await expect(recoveryNote(page)).toHaveCount(0)
      await captureRecovery(page, info, api, 'explicit-range-clears')
      switched.release()
      await expect(chartCard(page, 'memory').locator('.sr-only')).toContainText('70.0 %')
      await expect(recoveryNote(page)).toContainText('已读取范围：1 小时')
      api.enqueue('1h', 'empty')
      await page.getByRole('button', { name: '立即刷新数据', exact: true }).click()
      await expect(page.locator('.resource-state--empty')).toHaveCount(1)
      await expect(page.locator('.trend-chart')).toHaveCount(0)
      await expect(page.locator('.resource-state--error')).toHaveCount(0)
      await expect(page.locator('.monitor-history-note[role="status"]')).toHaveCount(0)
      await captureRecovery(page, info, api, 'successful-empty-replaces')
      api.assert()
      journeyCompleted = true
    } finally {
      api.release()
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      const path = info.outputPath('ui-captures', 'monitoring-refresh-final-request-ledger-capture-state.json')
      await writeFile(path, JSON.stringify({ ...await recoveryProvenance(), fixtureOnly: true, nativeNASAcceptance: false, journeyCompleted, clockMode: 'installed-running-browser-time', linkedCaptures: api.captures, requests: api.requests, requestsSha256: recoveryHash(JSON.stringify(api.requests)), history: api.history, denied: api.denied, writes: api.writes, errors: api.errors }, null, 2))
      await info.attach('monitoring-refresh-final-request-ledger', { path, contentType: 'application/json' })
    }
  })
}
