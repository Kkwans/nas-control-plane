import { test, expect, type Locator, type Page, type TestInfo } from '@playwright/test'
import type { MetricSample } from '../src/api/control'
import { monitoringChartTokens } from '../src/domain/monitoring/chartTokens'
import { installMockApi, summary } from './mockApi'

test.use({
  viewport: { width: 1440, height: 900 },
  timezoneId: 'UTC',
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
  await page.clock.setFixedTime(new Date(summary.collectedAt))
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
    await select.scrollIntoViewIfNeeded()
    await select.click()
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
    await select.fill('sensor-63')
    const searched = dropdown.getByRole('option', { name: longSensorNames[63]!, exact: true })
    await expect(searched).toBeVisible()
    await expect(searched).toHaveAttribute('aria-disabled', 'true')
    await expect(dropdown.getByRole('option')).toHaveCount(9)
    await captureViewport(page, testInfo, `${width}px-bounded-sensor-search`)
    await select.press('Escape')
    await expect(dropdown).toHaveCount(0)
    await expectSeparatedChartLayout(temperature)
    const overflow = await page.evaluate(() => Math.max(document.body.scrollWidth, document.documentElement.scrollWidth) - window.innerWidth)
    expect(overflow, '传感器名称、标签与已关闭的下拉面板不能撑宽页面').toBeLessThanOrEqual(0)
  })
}
