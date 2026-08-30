import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

import { installMockApi } from './mockApi'

const viewports = [
  { name: 'desktop-wide', width: 1440, height: 900 },
  { name: 'desktop-compact', width: 1280, height: 800 },
  { name: 'tablet', width: 768, height: 1024 },
  { name: 'mobile', width: 390, height: 844 },
]
// NAS-local Chromium may spend more than one minute compiling the first lazy
// route on a cold Vite server. Keep the assertion honest while avoiding a
// false failure that leaves the next viewport with an unfinished boot.
const routeNavigationTimeout = 120_000

test('总览在四档目标 viewport 可用并保留视觉基线', async ({ page }) => {
  test.setTimeout(300_000)
  const browserErrors: string[] = []
  page.on('pageerror', (error) => browserErrors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error') browserErrors.push(`console.error: ${message.text()}`)
  })
  await installMockApi(page)

  for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.goto('/', { waitUntil: 'domcontentloaded', timeout: routeNavigationTimeout })

    await expect(page.getByRole('heading', { name: '系统总览' })).toBeVisible({ timeout: routeNavigationTimeout })
    await expect(page.getByText('Jellyfin')).toBeVisible()
    await expect(page.getByText('收藏站点')).toBeVisible()
    await expect(page.getByText('CPU 使用率')).toBeVisible()

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

    await expect(page).toHaveScreenshot(`overview-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })
  }

  expect(browserErrors).toEqual([])
})
