import { test, expect, type Locator } from '@playwright/test'

import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

// 检查字形行框与所有裁切祖先，不能只靠 body.scrollWidth 判断可读性。
async function expectReadableSecondaryText(locator: Locator, baseFontSize: number) {
  await expect(locator).toBeVisible()
  const metrics = await locator.evaluate((element) => {
    const style = getComputedStyle(element)
    const bounds = element.getBoundingClientRect()
    const range = document.createRange()
    range.selectNodeContents(element)
    const lines = [...range.getClientRects()].filter((rect) => rect.width && rect.height)
    const clips: DOMRect[] = [bounds]
    for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
      const ancestorStyle = getComputedStyle(ancestor)
      if (/(hidden|clip|auto|scroll)/.test(`${ancestorStyle.overflowX} ${ancestorStyle.overflowY}`)) {
        clips.push(ancestor.getBoundingClientRect())
      }
    }
    return {
      fontSize: Number.parseFloat(style.fontSize),
      lineHeight: Number.parseFloat(style.lineHeight),
      contained: lines.every((line) => clips.every((clip) =>
        line.left >= clip.left - 1 && line.right <= clip.right + 1 &&
        line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
      scrollWidth: element.scrollWidth,
      clientWidth: element.clientWidth,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
    }
  })
  expect(metrics.fontSize).toBeGreaterThanOrEqual(Math.max(12, baseFontSize * .875) - .01)
  expect(metrics.lineHeight).toBeGreaterThanOrEqual(metrics.fontSize * 1.5 - .01)
  expect(metrics.contained).toBe(true)
  expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.clientWidth + 1)
  expect(metrics.scrollHeight).toBeLessThanOrEqual(metrics.clientHeight + 1)
}

for (const baseFontSize of [13, 15, 18]) {
  for (const width of [320, 390, 768, 1280, 1440]) {
    for (const route of ['database', 'docker'] as const) {
      test(`${route} 辅助文字在 ${width}px / ${baseFontSize}px 字号下完整可读`, async ({ page }, testInfo) => {
        await installMockApi(page)
        // 通过实际偏好加载流程应用字号，不直接改根元素 CSS 模拟用户设置。
        await page.route('**/api/v1/preferences', (intercept) => intercept.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize,
            pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab',
            siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system',
            navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'],
          }),
        }))
        await page.setViewportSize({ width, height: 900 })
        await page.goto(route === 'database' ? '/databases/sqlite-main' : '/docker', { waitUntil: 'domcontentloaded' })
        const surface = page.locator(route === 'database' ? '.source-summary' : width <= 820 ? '.docker-mobile-list' : '.docker-table')
        await expect(surface).toBeVisible()
        await expect(page.locator('html')).toHaveCSS('font-size', `${baseFontSize}px`)
        await waitForPageReady(page)
        await page.evaluate(() => document.fonts.ready)
        if (route === 'database') {
          await expect(surface.locator('.source-summary__status small')).toHaveText('已读取对象目录，可打开数据表或视图。')
          const text = surface.locator('dt, div:not(.source-summary__status) > dd, small, .connection-pill')
          await expect(text).toHaveCount(11)
          for (const item of await text.all()) await expectReadableSecondaryText(item, baseFontSize)
        } else if (width <= 820) {
          await expect(surface.locator('.mobile-project')).toHaveCount(2)
          const text = surface.locator('dt, dd')
          await expect(text).toHaveCount(12)
          for (const item of await text.all()) await expectReadableSecondaryText(item, baseFontSize)
        } else {
          // 桌面仍保留原表格密度，并确认放大字体没有挤掉已有操作。
          await expect(surface.locator('.project-row')).toHaveCount(2)
          const bounds = (await surface.boundingBox())!
          for (const button of await surface.locator('.project-actions button').all()) {
            const box = (await button.boundingBox())!
            expect(box.x).toBeGreaterThanOrEqual(bounds.x)
            expect(box.x + box.width).toBeLessThanOrEqual(bounds.x + bounds.width)
          }
        }
        expect(await page.evaluate(() => document.body.scrollWidth)).toBeLessThanOrEqual(width)
        if (process.env.NCP_CAPTURE_UI === '1') {
          await page.screenshot({ path: testInfo.outputPath('ui-captures', `fixture-secondary-${route}-${width}-font-${baseFontSize}.png`), fullPage: true, animations: 'disabled' })
        }
      })
    }
  }
}
