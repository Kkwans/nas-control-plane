import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

// 只使用拦截的 HTTP fixture；截图是 CI 检查产物，不依赖真实数据库或部署。
async function openWorkbench(page: Page, width: number, baseFontSize: number) {
  await installMockApi(page)
  await page.route('**/api/v1/preferences', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize,
      pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab',
      siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system',
      navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'],
    }),
  }))
  await page.setViewportSize({ width, height: 900 })
}

async function navigateWorkbench(page: Page, baseFontSize: number) {
  await page.goto('/databases/sqlite-main/tables/settings', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: 'settings', exact: true })).toBeVisible()
  await expect(page.locator('html')).toHaveCSS('font-size', `${baseFontSize}px`)
  await page.getByRole('tab', { name: '执行 SQL', exact: true }).click()
  await waitForPageReady(page)
  await page.evaluate(() => document.fonts.ready)
}

async function expectUnclippedText(locator: Locator) {
  await expect(locator).toBeVisible()
  const metrics = await locator.evaluate((element) => {
    const bounds = element.getBoundingClientRect()
    const range = document.createRange()
    range.selectNodeContents(element)
    const lines = [...range.getClientRects()].filter((line) => line.width && line.height)
    return {
      fits: lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1),
      width: element.scrollWidth - element.clientWidth,
      height: element.scrollHeight - element.clientHeight,
    }
  })
  expect(metrics.fits).toBe(true)
  expect(metrics.width).toBeLessThanOrEqual(1)
  expect(metrics.height).toBeLessThanOrEqual(1)
}

async function expectWorkbenchFits(page: Page, width: number) {
  const workbench = page.locator('.sql-workbench')
  const bounds = (await workbench.boundingBox())!
  expect(bounds.x).toBeGreaterThanOrEqual(0)
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(width)
  for (const locator of [page.getByRole('combobox', { name: 'SQL 结果模式' }), page.locator('.sql-toolbar__actions button'), page.locator('.sql-mode-note'), page.locator('.query-result')]) {
    const box = (await locator.boundingBox())!
    expect(box.x).toBeGreaterThanOrEqual(bounds.x)
    expect(box.x + box.width).toBeLessThanOrEqual(bounds.x + bounds.width + 1)
    expect(box.y + box.height).toBeLessThanOrEqual(bounds.y + bounds.height + 1)
  }
  await expectUnclippedText(page.locator('.sql-mode-note'))
  expect(await page.evaluate(() => document.body.scrollWidth)).toBeLessThanOrEqual(width)
}

async function capture(page: Page, info: TestInfo, name: string) {
  const path = info.outputPath('ui-captures', `${name}.png`)
  await page.locator('.sql-workbench').screenshot({ path, animations: 'disabled' })
  await info.attach(name, { path, contentType: 'image/png' })
}

for (const width of [320, 390]) {
  for (const baseFontSize of [15, 18]) {
    test(`返回行、未知影响及刷新失败在 ${width}px / ${baseFontSize}px 字号下保持可读`, async ({ page }, testInfo) => {
      await openWorkbench(page, width, baseFontSize)
      let refreshFailed = false
      let rowsRequests = 0
      const requests: Array<{ sql: string; resultMode?: string }> = []
      await page.route('**/api/v1/databases/rows', async (route) => {
        rowsRequests += 1
        if (!refreshFailed) return route.fallback()
        await route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ code: 'database_not_found', message: 'Fixture table not found' }) })
      })
      await page.route('**/api/v1/databases/query', async (route) => {
        requests.push(route.request().postDataJSON())
        await route.fulfill({ contentType: 'application/json', body: JSON.stringify({
          columns: ['id', 'value'], rows: [[1, 'fixture-updated']], rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 2,
        }) })
      })
      await navigateWorkbench(page, baseFontSize)
      const sql = "UPDATE settings SET value = 'fixture-updated' WHERE id = 1 RETURNING id, value"
      await page.locator('.cm-content').fill(sql)
      await page.getByRole('combobox', { name: 'SQL 结果模式' }).selectOption('rows')
      const execute = page.locator('.sql-toolbar__actions').getByRole('button', { name: /执行/ })
      await execute.click()
      await expect(page.locator('.query-result header')).toContainText('返回 1 行')
      await expect(page.locator('.query-affected-summary')).toHaveText('执行完成；数据库未提供可用的影响行数。')
      await expect(page.locator('.query-table')).toContainText('fixture-updated')
      await expect(execute).toBeEnabled()
      expect(requests).toEqual([{ sourceId: 'sqlite-main', sql, resultMode: 'rows' }])
      expect(rowsRequests).toBe(2)
      await expectWorkbenchFits(page, width)
      await expectUnclippedText(page.locator('.query-affected-summary'))
      await capture(page, testInfo, `fixture-sql-rows-unknown-${width}-font-${baseFontSize}`)

      refreshFailed = true
      await execute.click()
      await expect(page.locator('.query-refresh-warning')).toContainText('SQL 已执行，表数据刷新失败')
      await expect(page.locator('.query-table')).toContainText('fixture-updated')
      await expect(page.locator('.query-message--error')).toHaveCount(0)
      await expect(execute).toBeEnabled()
      expect(requests).toHaveLength(2)
      expect(rowsRequests).toBe(3)
      await expectWorkbenchFits(page, width)
      await expectUnclippedText(page.locator('.query-refresh-warning'))
      await capture(page, testInfo, `fixture-sql-refresh-failed-${width}-font-${baseFontSize}`)
    })
  }
}

test('旧响应缺少计数标志时显示未知，自动模式不发送 resultMode', async ({ page }, testInfo) => {
  await openWorkbench(page, 1280, 18)
  let request: Record<string, unknown> | undefined
  await page.route('**/api/v1/databases/query', async (route) => {
    request = route.request().postDataJSON()
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ columns: [], rows: [], rowsAffected: 7, truncated: false, durationMs: 2 }) })
  })
  await navigateWorkbench(page, 18)
  await page.locator('.cm-content').fill("UPDATE settings SET value = 'fixture' WHERE id = 1")
  await page.locator('.sql-toolbar__actions').getByRole('button', { name: /执行/ }).click()
  await expect(page.locator('.query-message--success')).toContainText('数据库未提供可用的影响行数')
  await expect(page.locator('.query-result')).not.toContainText('影响 7 行')
  expect(request).not.toHaveProperty('resultMode')
  await capture(page, testInfo, 'fixture-sql-legacy-affected-unknown')
})

for (const width of [320, 390]) {
  test(`SQL 确认预览在 ${width}px 大字号下保留注释后的换行`, async ({ page }, testInfo) => {
    await openWorkbench(page, width, 18)
    let queryRequests = 0
    await page.route('**/api/v1/databases/query', async (route) => {
      queryRequests += 1
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ columns: [], rows: [], rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 2 }) })
    })
    await navigateWorkbench(page, 18)
    const longToken = 'x'.repeat(80)
    const sql = `DELETE FROM settings; -- comment\nDELETE FROM other_table;\n-- ${longToken}`
    await page.locator('.cm-content').fill(sql)
    await page.getByRole('combobox', { name: 'SQL 结果模式' }).selectOption('exec')
    await page.locator('.sql-toolbar__actions').getByRole('button', { name: /执行/ }).click()
    const dialog = page.getByRole('dialog')
    const preview = dialog.locator('.sql-confirmation-preview')
    await expect(preview).toBeVisible()
    expect(await preview.textContent()).toBe(sql)
    await expect(preview).toHaveCSS('white-space', 'pre-wrap')
    await expect(preview).toHaveCSS('overflow-wrap', 'anywhere')
    await expect(preview).toHaveCSS('min-width', '0px')
    await expect(dialog).toContainText('结果模式：执行（不展示返回行）')
    await expectUnclippedText(preview)
    const lineBoxes = await preview.evaluate((element) => {
      const text = element.firstChild
      if (!(text instanceof Text)) throw new Error('SQL 预览必须只使用文本节点')
      const value = text.textContent ?? ''
      const comment = value.indexOf('-- comment')
      const nextStatement = value.indexOf('DELETE FROM other_table;')
      const range = document.createRange()
      range.setStart(text, comment)
      range.setEnd(text, comment + '-- comment'.length)
      const commentRects = [...range.getClientRects()]
      const commentBottom = Math.max(...commentRects.map((rect) => rect.bottom))
      range.setStart(text, nextStatement)
      range.setEnd(text, nextStatement + 'DELETE'.length)
      const nextTop = Math.min(...[...range.getClientRects()].map((rect) => rect.top))
      const tokenStart = value.lastIndexOf('-- ') + 3
      range.setStart(text, tokenStart)
      range.setEnd(text, value.length)
      const tokenLineCount = new Set([...range.getClientRects()].map((rect) => rect.top)).size
      const bounds = element.getBoundingClientRect()
      return { commentBottom, nextTop, tokenLineCount, left: bounds.left, right: bounds.right }
    })
    expect(lineBoxes.nextTop).toBeGreaterThanOrEqual(lineBoxes.commentBottom - 1)
    expect(lineBoxes.tokenLineCount).toBeGreaterThan(1)
    expect(lineBoxes.left).toBeGreaterThanOrEqual(0)
    expect(lineBoxes.right).toBeLessThanOrEqual(width)
    expect(queryRequests).toBe(0)
    const name = `fixture-sql-confirmation-newline-${width}-font-18`
    const path = testInfo.outputPath('ui-captures', `${name}.png`)
    await dialog.screenshot({ path, animations: 'disabled' })
    await testInfo.attach(name, { path, contentType: 'image/png' })
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    expect(queryRequests).toBe(0)
  })
}
