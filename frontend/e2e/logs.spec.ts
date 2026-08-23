import { test, expect } from '@playwright/test'

import { installMockApi } from './mockApi'

const collectedAt = '2026-08-24T00:00:00Z'

test('日志历史结果明确分页并可加载服务端后续页面', async ({ page }) => {
  await installMockApi(page)
  const cursors: string[] = []

  await page.route('**/api/v1/logs**', async (route) => {
    const url = new URL(route.request().url())
    const cursor = url.searchParams.get('cursor') || ''
    cursors.push(cursor)
    const firstPage = !cursor
    await route.fulfill({
      status: 200,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        collectedAt,
        entries: [{
          id: firstPage ? 'fixture-log-1' : 'fixture-log-2',
          timestamp: firstPage ? '2026-08-24T00:00:01Z' : '2026-08-24T00:00:02Z',
          source: 'system',
          unit: 'ncp-fixture.service',
          level: 'info',
          message: firstPage ? '第一批日志' : '第二批日志',
        }],
        nextCursor: firstPage ? 'fixture-cursor-1' : '',
      }),
    })
  })

  await page.goto('/logs')
  await expect(page.getByRole('heading', { name: '日志中心', exact: true })).toBeVisible()
  await expect(page.getByText('当前已载入 1 条')).toBeVisible()
  await expect(page.getByRole('button', { name: '加载更多结果' })).toBeVisible()

  await page.getByRole('button', { name: '加载更多结果' }).click()
  await expect(page.getByText('第二批日志')).toBeVisible()
  await expect(page.getByText('当前筛选范围已没有更多结果')).toBeVisible()
  await expect(page.getByRole('button', { name: '加载更多结果' })).toHaveCount(0)
  expect(cursors).toEqual(['', 'fixture-cursor-1'])
})
