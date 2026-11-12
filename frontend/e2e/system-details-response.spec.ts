import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi, systemDetails } from './mockApi'

const invalidMessage = 'NCP 服务返回了无法识别的数据。'
const malformed = { ...systemDetails, storage: { ...systemDetails.storage, mounts: [null] } }

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// GET 由内存 fixture 接管；自动 Mihomo POST 只是读取探测，也明确拦截。
// 其余写请求全部拒绝，绝不落到真实 NAS、DNS、代理或宿主服务。
async function installDetailsFixture(page: Page, initial: unknown) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const pageErrors: string[] = []
  const blockedWrites: string[] = []
  let payload = initial
  let detailsRequests = 0
  let inspectionRequests = 0
  let gate: { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> } | undefined
  page.on('pageerror', (error) => pageErrors.push(error.message))
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/v1/proxy/mihomo/inspect' && request.method() === 'POST') {
      inspectionRequests += 1
      await route.fulfill({ status: 503, json: { code: 'PROXY_MIHOMO_INSPECTION_UNAVAILABLE', message: 'fixture-only proxy probe unavailable' } })
      return
    }
    if (request.method() !== 'GET') {
      blockedWrites.push(`${request.method()} ${path}`)
      await route.fulfill({ status: 503, json: { code: 'FIXTURE_WRITE_BLOCKED', message: 'unexpected fixture write' } })
      return
    }
    if (path !== '/api/v1/system/details') return route.fallback()
    detailsRequests += 1
    const response = payload
    if (gate) {
      const held = gate
      gate = undefined
      held.received.resolve()
      await held.release.promise
    }
    await route.fulfill({ json: response })
  })
  return {
    pageErrors, blockedWrites,
    detailsRequests: () => detailsRequests,
    inspectionRequests: () => inspectionRequests,
    respondWith(value: unknown) { payload = value },
    hold() {
      const held = { received: deferred(), release: deferred() }
      gate = held
      return held
    },
  }
}

async function captureError(page: Page, info: TestInfo, width: number) {
  const panel = page.locator('.details-error')
  await panel.scrollIntoViewIfNeeded()
  await page.evaluate(() => document.fonts.ready)
  const geometry = await panel.evaluate((element) => {
    const box = element.getBoundingClientRect()
    const text = element.querySelector('p')!
    return { left: box.left, right: box.right, textWidth: text.scrollWidth, availableTextWidth: text.clientWidth, documentWidth: document.documentElement.scrollWidth }
  })
  // 保存真实视口证据，不更新现有基线，也不隐藏错误或修改样式。
  const name = `system-details-invalid-${width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const path = info.outputPath('ui-captures', `${name}.png`)
  await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({ fixtureOnly: true, viewport: page.viewportSize(), ...geometry }, null, 2))
  await page.screenshot({ path, fullPage: false, animations: 'disabled' })
  await info.attach(name, { path, contentType: 'image/png' })
  await info.attach(`${name}-geometry`, { body: JSON.stringify(geometry), contentType: 'application/json' })
  expect(geometry.left).toBeGreaterThanOrEqual(0)
  expect(geometry.right).toBeLessThanOrEqual(width + 1)
  expect(geometry.documentWidth).toBeLessThanOrEqual(width + 1)
  expect(geometry.textWidth).toBeLessThanOrEqual(geometry.availableTextWidth + 1)
  const accessibility = await new AxeBuilder({ page }).include('.details-error').analyze()
  expect(accessibility.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical')).toEqual([])
}

for (const width of [390, 1440]) {
  test(`嵌套 mount 为 null 时显示可重试错误且不崩溃（${width}px）`, async ({ page }, info) => {
    const fixture = await installDetailsFixture(page, malformed)
    const held = fixture.hold()
    await page.setViewportSize({ width, height: 900 })
    try {
      await page.goto('/system', { waitUntil: 'domcontentloaded' })
      await held.received.promise
      await expect(page.getByLabel('正在加载系统详情')).toBeVisible()
    } finally {
      held.release.resolve()
    }
    await expect(page.locator('.details-error')).toContainText(invalidMessage)
    await expect(page.getByText('系统详情暂不可用', { exact: true })).toBeVisible()
    expect(fixture.detailsRequests()).toBe(1)
    expect(fixture.inspectionRequests()).toBe(0)
    await captureError(page, info, width)
    fixture.respondWith(systemDetails)
    await page.getByRole('button', { name: '重试系统详情', exact: true }).click()
    await expect(page.getByText(systemDetails.device.model, { exact: true })).toBeVisible()
    await expect(page.locator('.details-error')).toHaveCount(0)
    await expect.poll(fixture.inspectionRequests).toBe(1)
    expect(fixture.detailsRequests()).toBe(2)
    expect(fixture.pageErrors).toEqual([])
    expect(fixture.blockedWrites).toEqual([])
  })
}

test('畸形刷新保留上次设备和存储详情并提示刷新失败', async ({ page }) => {
  const fixture = await installDetailsFixture(page, systemDetails)
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await expect(page.getByText(systemDetails.device.model, { exact: true })).toBeVisible()
  await expect.poll(fixture.inspectionRequests).toBe(1)
  fixture.respondWith({ ...malformed, device: { ...systemDetails.device, model: '不可提交的刷新设备' } })
  const held = fixture.hold()
  const refresh = page.getByRole('button', { name: '立即刷新数据', exact: true })
  try {
    await refresh.click()
    await held.received.promise
    await expect(refresh).toBeDisabled()
    await expect(page.getByText(systemDetails.device.model, { exact: true })).toBeVisible()
  } finally {
    held.release.resolve()
  }
  await expect(page.getByText('1 个资源刷新失败，已保留可用数据。', { exact: true })).toBeVisible()
  await expect(refresh).toBeEnabled()
  await expect(page.getByText(systemDetails.device.model, { exact: true })).toBeVisible()
  await expect(page.getByText('不可提交的刷新设备', { exact: true })).toHaveCount(0)
  await expect(page.locator('.details-error')).toHaveCount(0)
  await page.getByRole('button', { name: '存储与磁盘', exact: true }).click()
  await expect(page.getByRole('heading', { name: '存储卷', exact: true })).toBeVisible()
  await expect(page.locator('.volume-row strong').filter({ hasText: /^\/volume2$/ })).toBeVisible()
  expect(fixture.detailsRequests()).toBe(2)
  expect(fixture.inspectionRequests()).toBe(1)
  expect(fixture.pageErrors).toEqual([])
  expect(fixture.blockedWrites).toEqual([])
})

test('服务端零值、可选字段缺省及嵌套 null 切片可显示各系统分类', async ({ page }) => {
  const compatible = {
    ...systemDetails,
    device: { ...systemDetails.device, model: '零值兼容设备', uptimeSeconds: 0, processCount: 0 },
    hardware: { ...systemDetails.hardware, cpu: { ...systemDetails.hardware.cpu, frequencyMHz: 0, temperatureCelsius: -10 }, memory: { totalBytes: 0, availableBytes: 0 } },
    network: { ...systemDetails.network, interfaces: [{ ...systemDetails.network.interfaces[0], speedMbps: -1, addresses: null }] },
    storage: {
      mounts: [{ path: '/', device: '', filesystem: '', totalBytes: 0, usedBytes: 0, availableBytes: 0, usedPercent: 0 }],
      disks: [{ ...systemDetails.storage.disks[0], sizeBytes: 0, kind: '', role: '', temperatureCelsius: -1 }],
      raid: [{ name: 'fixture-empty-raid', level: '', state: '', devices: null }],
    },
    proxy: { ...systemDetails.proxy, mihomo: { ...systemDetails.proxy.mihomo, operations: null } },
    tailscale: undefined, dns: undefined, publicEgress: undefined,
  }
  const fixture = await installDetailsFixture(page, compatible)
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await expect(page.getByText('零值兼容设备', { exact: true })).toBeVisible()
  await expect.poll(fixture.inspectionRequests).toBe(1)
  for (const [tab, heading] of [['网络与代理', '主要网络连接'], ['存储与磁盘', '存储卷'], ['服务与能力', '控制链路']] as const) {
    await page.getByRole('button', { name: tab, exact: true }).click()
    await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible()
  }
  await expect(page.locator('.details-error')).toHaveCount(0)
  expect(fixture.detailsRequests()).toBe(1)
  expect(fixture.pageErrors).toEqual([])
  expect(fixture.blockedWrites).toEqual([])
})
