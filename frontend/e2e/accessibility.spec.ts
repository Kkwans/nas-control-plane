import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

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

for (const route of routes) {
  test(`${route.path} 关键可访问性规则通过`, async ({ page }) => {
    await installMockApi(page)
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(route.path, { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('heading', { name: route.heading, exact: true })).toBeVisible()

    const results = await new AxeBuilder({ page }).analyze()
    const blockingViolations = results.violations.filter((violation) => violation.impact === 'critical' || violation.impact === 'serious')
    expect(blockingViolations, JSON.stringify(blockingViolations, null, 2)).toEqual([])
  })
}
