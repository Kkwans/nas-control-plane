import { test, expect, type Locator } from '@playwright/test'

import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

// 测量真实文字行框，避免 overflow:hidden 让页面宽度检查产生假通过。
async function expectReadableText(locator: Locator, singleLine = false) {
  await expect(locator).toBeVisible()
  const layout = await locator.evaluate((element) => {
    const range = document.createRange()
    range.selectNodeContents(element)
    const bounds = element.getBoundingClientRect()
    const lines = [...range.getClientRects()].filter((rect) => rect.width && rect.height)
    return {
      lineTops: [...new Set(lines.map((rect) => Math.round(rect.top)))],
      contained: lines.every((rect) => rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1 && rect.top >= bounds.top - 1 && rect.bottom <= bounds.bottom + 1),
      scrollWidth: element.scrollWidth,
      clientWidth: element.clientWidth,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
    }
  })
  expect(layout.contained).toBe(true)
  expect(layout.scrollWidth).toBeLessThanOrEqual(layout.clientWidth + 1)
  expect(layout.scrollHeight).toBeLessThanOrEqual(layout.clientHeight + 1)
  if (singleLine) expect(layout.lineTops).toHaveLength(1)
}

for (const width of [320, 390, 768]) {
  test(`Docker 分页在 ${width}px 保持单行按钮和完整触控区域`, async ({ page }, testInfo) => {
    await installMockApi(page)
    await page.setViewportSize({ width, height: 844 })
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    const footer = page.locator('.docker-mobile-list footer.resource-pagination')
    await expect(footer).toBeVisible()
    await waitForPageReady(page)
    const footerBounds = await footer.boundingBox()
    expect(footerBounds).not.toBeNull()
    for (const label of ['上一页', '下一页']) {
      const button = footer.getByRole('button', { name: label, exact: true })
      await expectReadableText(button, true)
      const bounds = (await button.boundingBox())!
      expect(bounds.width).toBeGreaterThanOrEqual(44)
      expect(bounds.height).toBeGreaterThanOrEqual(44)
      expect(bounds.x).toBeGreaterThanOrEqual(footerBounds!.x)
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(footerBounds!.x + footerBounds!.width)
      expect(bounds.y + bounds.height).toBeLessThanOrEqual(footerBounds!.y + footerBounds!.height)
    }
    await expectReadableText(footer.locator('.page-size-control > span'), true)
    const selectorBounds = (await footer.locator('.el-select__wrapper').boundingBox())!
    expect(selectorBounds.height).toBeGreaterThanOrEqual(44)
    expect(selectorBounds.x + selectorBounds.width).toBeLessThanOrEqual(footerBounds!.x + footerBounds!.width)
    if (process.env.NCP_CAPTURE_UI === '1') {
      await page.screenshot({ path: testInfo.outputPath('ui-captures', `fixture-docker-pagination-${width}.png`), fullPage: true, animations: 'disabled' })
    }
  })

  test(`数据库摘要在 ${width}px 完整展示说明和路径`, async ({ page }, testInfo) => {
    await installMockApi(page)
    await page.setViewportSize({ width, height: 844 })
    await page.goto('/databases/sqlite-main', { waitUntil: 'domcontentloaded' })
    const summary = page.locator('.source-summary')
    const description = summary.locator('.source-summary__status small')
    await expect(description).toHaveText('已读取对象目录，可打开数据表或视图。')
    await waitForPageReady(page)
    await expectReadableText(description)
    const path = summary.locator('div').filter({ has: page.locator('dt', { hasText: '连接位置' }) }).locator('dd')
    await expect(path).toHaveText('/volume2/Project/nas-control-plane/data/ncp.db')
    await expectReadableText(path)
    if (width <= 390) {
      const pathBounds = (await path.boundingBox())!
      const summaryBounds = (await summary.boundingBox())!
      expect(pathBounds.width).toBeGreaterThan(summaryBounds.width * 0.8)
    }
    if (process.env.NCP_CAPTURE_UI === '1') {
      await page.screenshot({ path: testInfo.outputPath('ui-captures', `fixture-database-summary-${width}.png`), fullPage: true, animations: 'disabled' })
    }
  })
}

for (const width of [1280, 1440]) {
  test(`Docker 在 ${width}px 不裁切项目操作按钮`, async ({ page }, testInfo) => {
    await installMockApi(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    const table = page.locator('.docker-table')
    await expect(table.locator('.project-row')).toHaveCount(2)
    await waitForPageReady(page)
    const tableBounds = (await table.boundingBox())!
    for (const row of await table.locator('.project-row').all()) {
      const rowBounds = (await row.boundingBox())!
      const buttons = row.locator('.project-actions button')
      await expect(buttons).toHaveCount(4)
      for (const button of await buttons.all()) {
        const bounds = (await button.boundingBox())!
        expect(bounds.x).toBeGreaterThanOrEqual(tableBounds.x + 1)
        expect(bounds.x + bounds.width).toBeLessThanOrEqual(tableBounds.x + tableBounds.width - 1)
        expect(bounds.y).toBeGreaterThanOrEqual(rowBounds.y)
        expect(bounds.y + bounds.height).toBeLessThanOrEqual(rowBounds.y + rowBounds.height)
      }
    }
    if (process.env.NCP_CAPTURE_UI === '1') {
      await page.screenshot({ path: testInfo.outputPath('ui-captures', `fixture-docker-actions-${width}.png`), fullPage: true, animations: 'disabled' })
    }
  })
}

for (const width of [390, 768, 1280, 1440]) {
  test(`日志时间范围在 ${width}px 保留完整单位`, async ({ page }, testInfo) => {
    await installMockApi(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/logs', { waitUntil: 'domcontentloaded' })
    const hours = page.locator('.log-filter-field--hours')
    const label = hours.locator('.el-select__selected-item').filter({ hasText: '最近 6 小时' })
    await expect(label).toBeVisible()
    await waitForPageReady(page)
    await expectReadableText(label)
    const hoursSelect = hours.locator('.el-select__wrapper')
    await hoursSelect.evaluate((element) => element.scrollIntoView({ block: 'center', behavior: 'instant' }))
    await expect(hoursSelect).toBeVisible()
    await hoursSelect.click()
    await expect(hours.getByRole('combobox')).toHaveAttribute('aria-expanded', 'true')
    await page.getByRole('option', { name: '最近 24 小时', exact: true }).click()
    const longestLabel = hours.locator('.el-select__selected-item').filter({ hasText: '最近 24 小时' })
    await expectReadableText(longestLabel)
    const bounds = (await hours.boundingBox())!
    expect(bounds.x).toBeGreaterThanOrEqual(0)
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(width)
    if (process.env.NCP_CAPTURE_UI === '1') {
      await page.screenshot({ path: testInfo.outputPath('ui-captures', `fixture-logs-time-range-${width}.png`), fullPage: true, animations: 'disabled' })
    }
  })
}
