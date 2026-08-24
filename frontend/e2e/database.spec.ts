import { test, expect } from '@playwright/test'

import { installMockApi } from './mockApi'

test('数据库 SQL 工作台在无范围 DELETE 前提示影响并允许取消', async ({ page }) => {
  await installMockApi(page)
  let queryRequests = 0
  await page.route('**/api/v1/databases/query', async (route) => {
    queryRequests += 1
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ columns: [], rows: [], rowsAffected: 0, truncated: false, durationMs: 1 }),
    })
  })

  await page.goto('/databases/sqlite-main/tables/settings', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'settings', exact: true })).toBeVisible({ timeout: 30_000 })
  await page.getByRole('button', { name: '执行 SQL', exact: true }).click()
  await page.locator('.cm-content').fill('DELETE FROM settings')

  await expect(page.getByText('DELETE 未包含 WHERE，可能影响整张表。')).toBeVisible()
  await page.locator('.sql-toolbar__actions').getByRole('button', { name: /执行/ }).click()
  await expect(page.getByText('该 DELETE 未包含 WHERE 条件')).toBeVisible()
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.getByText('该 DELETE 未包含 WHERE 条件')).toHaveCount(0)
  expect(queryRequests).toBe(0)
})

test('数据库发现失败显示统一错误状态和恢复提示', async ({ page }) => {
  await installMockApi(page)
  await page.route('**/api/v1/databases/discovery', async (route) => {
    await route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'DATABASE_DISCOVERY_FAILED', message: 'Fixture database discovery failed', requestId: 'fixture-db-error' }),
    })
  })

  await page.goto('/databases', { waitUntil: 'domcontentloaded', timeout: 60_000 })
  const error = page.getByRole('alert')
  await expect(error).toBeVisible({ timeout: 60_000 })
  await expect(error).toContainText('数据库发现失败')
  await expect(error).toContainText('代码 DATABASE_DISCOVERY_FAILED')
  await expect(error).toContainText('下一步：')
})
