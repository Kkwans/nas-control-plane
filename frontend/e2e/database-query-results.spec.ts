import { mkdir, writeFile } from 'node:fs/promises'
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

async function readCaptureState(page: Page) {
  return page.evaluate(() => {
    function bounds(element: Element | null) {
      if (!element) return null
      const rect = element.getBoundingClientRect()
      return { x: rect.x, y: rect.y, top: rect.top, right: rect.right, bottom: rect.bottom, left: rect.left, width: rect.width, height: rect.height }
    }
    function describe(element: Element | null) {
      if (!element) return null
      return {
        tagName: element.tagName, id: element.id, className: element.getAttribute('class'),
        role: element.getAttribute('role'), ariaLabel: element.getAttribute('aria-label'),
        ariaExpanded: element.getAttribute('aria-expanded'), ariaAutocomplete: element.getAttribute('aria-autocomplete'),
        ariaControls: element.getAttribute('aria-controls'), ariaActiveDescendant: element.getAttribute('aria-activedescendant'),
        contentEditable: element.getAttribute('contenteditable'), bounds: bounds(element),
      }
    }
    function visible(element: Element) {
      const style = getComputedStyle(element)
      return element.getClientRects().length > 0 && style.display !== 'none' && style.visibility !== 'hidden' && Number(style.opacity) > 0
    }
    const topbar = document.querySelector('.topbar')
    const toolbar = document.querySelector('.sql-toolbar')
    const modeLabel = document.querySelector('.sql-result-mode > span')
    const select = document.querySelector('.sql-result-mode select')
    const execute = document.querySelector('.sql-toolbar__actions button')
    const editor = document.querySelector('.cm-content')
    const controls = [
      { name: 'modeLabel', element: modeLabel }, { name: 'select', element: select }, { name: 'execute', element: execute },
    ].map(({ name, element }) => {
      const rect = bounds(element)
      // 中点及四个内侧采样点均使用当前视口坐标，不用全页截图推断遮挡。
      const hits = rect ? [[.5, .5], [.25, .5], [.75, .5], [.5, .25], [.5, .75]].map(([xRatio, yRatio]) => {
        const x = rect.left + rect.width * xRatio!
        const y = rect.top + rect.height * yRatio!
        const hit = document.elementFromPoint(x, y)
        return { x, y, hit: describe(hit), unobscured: Boolean(element && hit && element.contains(hit)) }
      }) : []
      return { name, bounds: rect, visible: Boolean(element && visible(element)), hits }
    })
    return {
      scrollX: window.scrollX, scrollY: window.scrollY,
      viewport: { width: window.innerWidth, height: window.innerHeight, devicePixelRatio: window.devicePixelRatio },
      documentHeight: document.documentElement.scrollHeight,
      bounds: {
        topbar: bounds(topbar), toolbar: bounds(toolbar), modeLabel: bounds(modeLabel), select: bounds(select),
        execute: bounds(execute), result: bounds(document.querySelector('.query-result')),
      },
      controls,
      focusedElement: describe(document.activeElement),
      editor: { ...describe(editor), focused: Boolean(editor && document.activeElement && editor.contains(document.activeElement)) },
      autocomplete: [...document.querySelectorAll('.cm-tooltip-autocomplete')].map((element) => ({
        ...describe(element), visible: visible(element), text: element.textContent,
        selectedOption: element.querySelector('[aria-selected="true"]')?.textContent ?? null,
        optionCount: element.querySelectorAll('[role="option"]').length,
      })),
    }
  })
}

async function capture(page: Page, info: TestInfo, name: string) {
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  // 先记录真实执行后的焦点和补全菜单，不调用 blur/focus、Escape 或隐藏元素。
  const initialState = await readCaptureState(page)
  const initialPath = info.outputPath('ui-captures', `${name}-post-execution-viewport.png`)
  await page.screenshot({ path: initialPath, fullPage: false, animations: 'disabled' })
  await info.attach(`${name}-post-execution-viewport`, { path: initialPath, contentType: 'image/png' })

  // 明确把工具栏移到真实 sticky header 下方，避免 locator.screenshot 的隐式滚动。
  const scrollIntent = await page.evaluate(() => {
    const header = document.querySelector('.topbar')!
    const toolbar = document.querySelector('.sql-toolbar')!
    const stickyBottom = Math.max(0, header.getBoundingClientRect().bottom)
    const targetToolbarTop = stickyBottom + 12
    const requestedScrollY = window.scrollY + toolbar.getBoundingClientRect().top - targetToolbarTop
    const maxScrollY = Math.max(0, document.documentElement.scrollHeight - window.innerHeight)
    const targetScrollY = Math.round(Math.min(maxScrollY, Math.max(0, requestedScrollY)))
    window.scrollTo({ top: targetScrollY, left: 0, behavior: 'instant' })
    return { stickyBottom, targetToolbarTop, requestedScrollY, targetScrollY, maxScrollY }
  })
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeCloseTo(scrollIntent.targetScrollY, 0)
  const scrolledState = await readCaptureState(page)
  const viewportPath = info.outputPath('ui-captures', `${name}-scrolled-viewport.png`)
  await page.screenshot({ path: viewportPath, fullPage: false, animations: 'disabled' })
  await info.attach(`${name}-scrolled-viewport`, { path: viewportPath, contentType: 'image/png' })

  // 只重置滚动坐标后生成规范全页图；保留交互产生的实际焦点与补全状态。
  await page.evaluate(() => window.scrollTo({ top: 0, left: 0, behavior: 'instant' }))
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
  const canonicalState = await readCaptureState(page)
  const path = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path, fullPage: true, animations: 'disabled' })
  await info.attach(name, { path, contentType: 'image/png' })
  const afterCaptureState = await readCaptureState(page)
  const captureState = JSON.stringify({
    focusPolicy: '保留真实焦点和补全状态；采集过程只主动改变窗口滚动坐标。',
    screenshots: { postExecutionViewport: initialPath, scrolledViewport: viewportPath, canonicalFullPage: path },
    initialState, scrollIntent, scrolledState, canonicalState, afterCaptureState,
  }, null, 2)
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  await writeFile(statePath, captureState, 'utf8')
  await info.attach(`${name}-capture-state`, { path: statePath, contentType: 'application/json' })

  // 先保存证据再断言，以便 CI 失败时仍可还原实际滚动和焦点状态。
  expect(scrolledState.bounds.topbar).not.toBeNull()
  expect(scrolledState.bounds.topbar!.top).toBeCloseTo(0, 0)
  for (const control of scrolledState.controls) {
    expect(control.visible, `${control.name} 应可见`).toBe(true)
    expect(control.bounds).not.toBeNull()
    expect(control.bounds!.top).toBeGreaterThanOrEqual(scrolledState.bounds.topbar!.bottom)
    expect(control.bounds!.bottom).toBeLessThanOrEqual(scrolledState.viewport.height)
    expect(control.bounds!.left).toBeGreaterThanOrEqual(0)
    expect(control.bounds!.right).toBeLessThanOrEqual(scrolledState.viewport.width)
    expect(control.hits).toHaveLength(5)
    for (const hit of control.hits) expect(hit.unobscured, `${control.name} 视口采样点 (${hit.x}, ${hit.y}) 被遮挡`).toBe(true)
  }
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
