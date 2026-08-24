import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

import { installMockApi, systemDetails } from './mockApi'

const viewports = [
  { name: 'desktop-wide', width: 1440, height: 900 },
  { name: 'desktop-compact', width: 1280, height: 800 },
  { name: 'tablet', width: 768, height: 1024 },
  { name: 'mobile', width: 390, height: 844 },
]
const routeNavigationTimeout = 60_000

test('系统概览在四档目标 viewport 可读并保留视觉基线', async ({ page }) => {
  test.setTimeout(120_000)
  const browserErrors: string[] = []
  page.on('pageerror', (error) => browserErrors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error') browserErrors.push(`console.error: ${message.text()}`)
  })
  await installMockApi(page)

  for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.goto('/system', { waitUntil: 'domcontentloaded', timeout: routeNavigationTimeout })

    await expect(page.getByRole('heading', { name: '系统信息' })).toBeVisible({ timeout: routeNavigationTimeout })
    await expect(page.getByRole('heading', { name: '网络与控制链路' })).toBeVisible()
    await expect(page.getByText('主网络', { exact: true })).toBeVisible()
    await expect(page.getByText('Tailscale')).toBeVisible()

    const layout = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      viewportWidth: window.innerWidth,
      mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
    }))
    expect(layout.bodyWidth, `${viewport.name} body 横向溢出`).toBeLessThanOrEqual(layout.viewportWidth)
    expect(layout.mainWidth, `${viewport.name} main 横向溢出`).toBeLessThanOrEqual(layout.viewportWidth)

    if (viewport.name === 'desktop-wide') {
      const accessibility = await new AxeBuilder({ page }).analyze()
      const blockingViolations = accessibility.violations.filter((violation) => violation.impact === 'critical' || violation.impact === 'serious')
      expect(blockingViolations, JSON.stringify(blockingViolations, null, 2)).toEqual([])
    }

    await expect(page).toHaveScreenshot(`system-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })
  }

  expect(browserErrors).toEqual([])
})

test('系统详情刷新不会被较早响应覆盖', async ({ page }) => {
  await installMockApi(page)
  let detailsRequests = 0
  await page.route('**/api/v1/system/details', async (route) => {
    const requestNumber = ++detailsRequests
    if (requestNumber === 1) await new Promise((resolve) => setTimeout(resolve, 500))
    const model = requestNumber === 1 ? '旧响应 NAS' : '新响应 NAS'
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...systemDetails, device: { ...systemDetails.device, model } }),
    })
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/system', { waitUntil: 'domcontentloaded', timeout: 15_000 })
  await expect(page.getByRole('heading', { name: '系统信息' })).toBeVisible({ timeout: 15_000 })
  await page.getByRole('button', { name: '立即刷新数据' }).click()
  await expect(page.getByText('新响应 NAS', { exact: true })).toBeVisible({ timeout: 15_000 })
  await new Promise((resolve) => setTimeout(resolve, 650))
  await expect(page.getByText('旧响应 NAS', { exact: true })).toHaveCount(0)
  expect(detailsRequests).toBeGreaterThanOrEqual(2)
})

test('系统详情加载完成后可打开并取消 DNS 编辑 Dialog', async ({ page }) => {
  await installMockApi(page)
  const editableDns = {
    ...systemDetails.dns,
    readOnly: false,
    canPreview: true,
    canConfirm: true,
    canRollback: true,
    configuredNameservers: ['192.168.5.1'],
    errorCode: '',
  }
  await page.route('**/api/v1/system/details', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 300))
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(systemDetails) })
  })
  await page.route('**/api/v1/system/dns/capability', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(editableDns) })
  })

  await page.goto('/system', { waitUntil: 'domcontentloaded', timeout: 30_000 })
  await expect(page.locator('.details-skeleton')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole('heading', { name: '网络与控制链路' })).toBeVisible({ timeout: 30_000 })
  await page.getByRole('button', { name: '网络与代理', exact: true }).click()
  await page.getByRole('button', { name: '编辑 DNS', exact: true }).click()

  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('先生成差异预览，确认后才调用 UGOS 网络服务')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await expect(dialog).toHaveCount(0)
})

test('系统详情失败时显示恢复入口并可重试', async ({ page }) => {
  await installMockApi(page)
  let shouldFail = true
  await page.route('**/api/v1/system/details', async (route) => {
    if (shouldFail) {
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ code: 'SYSTEM_DETAILS_UNAVAILABLE', message: 'Fixture system details unavailable', requestId: 'fixture-system-error' }),
      })
      return
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(systemDetails) })
  })

  await page.goto('/system', { waitUntil: 'domcontentloaded', timeout: 30_000 })
  await expect(page.getByText('系统详情暂不可用', { exact: true })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('Fixture system details unavailable', { exact: true })).toBeVisible()
  shouldFail = false
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.getByRole('heading', { name: '网络与控制链路' })).toBeVisible({ timeout: 30_000 })
})

test('系统信息的存储与服务 tab 在四档 viewport 可读', async ({ page }) => {
  test.setTimeout(120_000)
  const browserErrors: string[] = []
  page.on('pageerror', (error) => browserErrors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error') browserErrors.push(`console.error: ${message.text()}`)
  })
  await installMockApi(page)

  for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.goto('/system', { waitUntil: 'domcontentloaded', timeout: routeNavigationTimeout })
    await expect(page.getByRole('heading', { name: '系统信息' })).toBeVisible({ timeout: routeNavigationTimeout })

    await page.getByRole('button', { name: '网络与代理' }).click()
    await expect(page.getByRole('heading', { name: '主要网络连接' })).toBeVisible()
    await expect(page.getByText('当前联网', { exact: true })).toBeVisible()
    const networkLayout = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      viewportWidth: window.innerWidth,
      mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
    }))
    expect(networkLayout.bodyWidth, `${viewport.name} network body 横向溢出`).toBeLessThanOrEqual(networkLayout.viewportWidth)
    expect(networkLayout.mainWidth, `${viewport.name} network main 横向溢出`).toBeLessThanOrEqual(networkLayout.viewportWidth)
    await expect(page).toHaveScreenshot(`system-network-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })

    const proxyHeading = page.getByRole('heading', { name: '代理链路', exact: true })
    await proxyHeading.scrollIntoViewIfNeeded()
    await expect(proxyHeading).toBeVisible()
    await expect(page.getByRole('button', { name: '刷新链路', exact: true })).toBeVisible()
    await expect(page).toHaveScreenshot(`system-proxy-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })

    const listenersHeading = page.getByRole('heading', { name: '监听服务', exact: true })
    await listenersHeading.scrollIntoViewIfNeeded()
    await expect(listenersHeading).toBeVisible()
    await expect(page.getByRole('textbox', { name: '搜索监听服务' })).toBeVisible()
    await expect(page).toHaveScreenshot(`system-listeners-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })

    await page.getByRole('button', { name: '存储与磁盘' }).click()
    await expect(page.getByRole('heading', { name: '存储卷' })).toBeVisible()
    await expect(page.getByRole('heading', { name: '物理磁盘' })).toBeVisible()
    await expect(page.getByRole('heading', { name: '存储阵列' })).toBeVisible()
    const storageLayout = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      viewportWidth: window.innerWidth,
      mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
    }))
    expect(storageLayout.bodyWidth, `${viewport.name} storage body 横向溢出`).toBeLessThanOrEqual(storageLayout.viewportWidth)
    expect(storageLayout.mainWidth, `${viewport.name} storage main 横向溢出`).toBeLessThanOrEqual(storageLayout.viewportWidth)
    await expect(page).toHaveScreenshot(`system-storage-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })

    await page.getByRole('button', { name: '服务与能力' }).click()
    await expect(page.getByRole('heading', { name: '控制链路' })).toBeVisible()
    await expect(page.getByText('Docker Engine', { exact: true })).toBeVisible()
    const servicesLayout = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      viewportWidth: window.innerWidth,
      mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
    }))
    expect(servicesLayout.bodyWidth, `${viewport.name} services body 横向溢出`).toBeLessThanOrEqual(servicesLayout.viewportWidth)
    expect(servicesLayout.mainWidth, `${viewport.name} services main 横向溢出`).toBeLessThanOrEqual(servicesLayout.viewportWidth)
    await expect(page).toHaveScreenshot(`system-services-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })
  }

  expect(browserErrors).toEqual([])
})
