import { test, expect } from '@playwright/test'

import { installMockApi } from './mockApi'

const routes = [
  { path: '/', heading: '系统总览' },
  { path: '/sites', heading: '站点中心' },
  { path: '/docker', heading: 'Docker 管理' },
  { path: '/databases', heading: '数据库' },
  { path: '/databases/sqlite-main', heading: 'NCP 数据库' },
  { path: '/databases/sqlite-main/tables/settings', heading: 'settings' },
  { path: '/logs', heading: '日志中心' },
  { path: '/monitoring', heading: '系统监控' },
  { path: '/system', heading: '系统信息' },
  { path: '/terminal', heading: '终端' },
  { path: '/users', heading: '用户管理' },
  { path: '/settings', heading: '系统设置' },
]

const routeNavigationTimeout = 30_000

const viewports = [
  { name: 'desktop-wide', width: 1440, height: 900 },
  { name: 'desktop-compact', width: 1280, height: 800 },
  { name: 'tablet', width: 768, height: 1024 },
  { name: 'mobile', width: 390, height: 844 },
]

for (const viewport of viewports) {
  test(`主要页面在 ${viewport.name} 可进入且无横向失控`, async ({ page }) => {
    const browserErrors: string[] = []
    page.on('pageerror', (error) => browserErrors.push(`pageerror: ${error.message}`))
    page.on('console', (message) => {
      if (message.type() === 'error') browserErrors.push(`console.error: ${message.text()}`)
    })
    await installMockApi(page)
    await page.setViewportSize({ width: viewport.width, height: viewport.height })

    for (const route of routes) {
      await page.goto(route.path, { waitUntil: 'domcontentloaded', timeout: routeNavigationTimeout })
      await expect(page.getByRole('heading', { name: route.heading, exact: true })).toBeVisible({ timeout: routeNavigationTimeout })
      const layout = await page.evaluate(() => ({
        bodyWidth: document.body.scrollWidth,
        viewportWidth: window.innerWidth,
        mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
      }))
      expect(layout.bodyWidth, `${route.path} body 横向溢出`).toBeLessThanOrEqual(layout.viewportWidth)
      expect(layout.mainWidth, `${route.path} main 横向溢出`).toBeLessThanOrEqual(layout.viewportWidth)
    }

    expect(browserErrors).toEqual([])
  })
}
