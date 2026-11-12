import { test, expect } from '@playwright/test'
import { installMockApi } from './mockApi'

function gate() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => { release = resolve })
  return { promise, release }
}
const emptyCatalog = {
  collectedAt: '2026-10-13T08:00:00Z', sites: [],
  discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] },
}
const failure = { status: 503, contentType: 'application/json', body: JSON.stringify({ code: 'AGENT_UNAVAILABLE', message: '站点目录暂时不可用' }) }

test('收藏首次慢加载、失败、重试与真实空态互不混淆', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await installMockApi(page)
  const pending = gate()
  let requests = 0
  await page.route('**/api/v1/sites', async (route) => {
    requests += 1
    if (requests === 1) {
      await pending.promise
      return route.fulfill(failure)
    }
    return route.fulfill({ json: emptyCatalog })
  })
  try {
    await page.goto('/', { waitUntil: 'domcontentloaded' })
    const favorites = page.getByRole('region', { name: '收藏站点', exact: true })
    await expect(favorites.getByText('正在加载收藏站点…')).toBeVisible()
    await expect(favorites.getByText('还没有收藏站点')).toHaveCount(0)
    pending.release()
    await expect(favorites.getByRole('alert')).toContainText('收藏站点加载失败')
    await expect(favorites.getByText('还没有收藏站点')).toHaveCount(0)
    await favorites.getByRole('button', { name: '重试', exact: true }).click()
    await expect(favorites.getByText('还没有收藏站点')).toBeVisible()
    await expect(favorites.getByRole('alert')).toHaveCount(0)
    await expect(favorites.locator('[aria-busy="true"]')).toHaveCount(0)
    expect(requests).toBe(2)
  } finally { pending.release() }
})

test('缓存收藏在刷新失败时保留入口并明确标记历史状态', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await installMockApi(page)
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  const favorites = page.getByRole('region', { name: '收藏站点', exact: true })
  await expect(favorites.getByRole('link', { name: /Jellyfin/ })).toBeVisible()
  await page.getByRole('link', { name: '设置', exact: true }).click()
  await expect(page.getByRole('heading', { name: '设置', exact: true })).toBeVisible()
  const pending = gate()
  let fail = true
  await page.route('**/api/v1/sites', async (route) => {
    if (!fail) return route.fallback()
    await pending.promise
    return route.fulfill(failure)
  })
  try {
    await page.getByRole('link', { name: '总览', exact: true }).click()
    await expect(favorites.getByText('正在更新收藏站点，暂时显示上次同步的结果。', { exact: false })).toBeVisible()
    await expect(favorites.getByRole('link', { name: /Jellyfin/ })).toHaveAttribute('href', 'http://ncp-fixture:8096')
    await expect(favorites.getByText('上次运行中', { exact: true })).toBeVisible()
    pending.release()
    await expect(favorites.getByRole('alert')).toContainText('收藏站点刷新失败')
    await expect(favorites.getByText('上次运行中', { exact: true })).toBeVisible()
    await expect(favorites.getByText('还没有收藏站点')).toHaveCount(0)
    fail = false
    await favorites.getByRole('button', { name: '重试', exact: true }).click()
    await expect(favorites.getByRole('alert')).toHaveCount(0)
    await expect(favorites.getByText('运行中', { exact: true })).toBeVisible()
    await expect(favorites.getByText('上次运行中', { exact: true })).toHaveCount(0)
  } finally { pending.release() }
})
