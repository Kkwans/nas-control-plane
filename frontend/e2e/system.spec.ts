import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

import { installMockApi } from './mockApi'

const viewports = [
  { name: 'desktop-wide', width: 1440, height: 900 },
  { name: 'desktop-compact', width: 1280, height: 800 },
  { name: 'tablet', width: 768, height: 1024 },
  { name: 'mobile', width: 390, height: 844 },
]

test('系统概览在四档目标 viewport 可读并保留视觉基线', async ({ page }) => {
  const browserErrors: string[] = []
  page.on('pageerror', (error) => browserErrors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error') browserErrors.push(`console.error: ${message.text()}`)
  })
  await installMockApi(page)

  for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.goto('/system')

    await expect(page.getByRole('heading', { name: '系统信息' })).toBeVisible({ timeout: 15_000 })
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

    await expect(page).toHaveScreenshot(`system-${viewport.name}.png`, { fullPage: true, animations: 'disabled' })
  }

  expect(browserErrors).toEqual([])
})
