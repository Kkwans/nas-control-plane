import { expect, test } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { installMockApi, summary } from './mockApi'

// 独立 HTTP fixture，不连接 NAS；根字号压力测试并非浏览器真实 zoom。
for (const width of [320, 390, 768, 1280, 1440]) {
  for (const rootFontSize of [15, 30]) {
    test(`总览摘要 ${width}px / ${rootFontSize}px 根字号保留完整时间`, async ({ page }, info) => {
      await installMockApi(page, { manualRealtime: true })
      await page.clock.setFixedTime(new Date(summary.collectedAt))
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await page.setViewportSize({ width, height: width <= 390 ? 640 : 900 })
      await page.goto('/')
      const stats = page.locator('.overview-page .workspace-stats')
      await expect(stats.locator('dt')).toHaveText(['运行项目', '运行时间', '上次更新'])
      await expect(stats.locator('dd').nth(1)).toHaveText('1 天 0 小时')
      await expect(stats.locator('dd').nth(2)).toHaveText(/^\d{2}:\d{2}:\d{2}$/)
      await expect(page.locator('html')).toHaveCSS('font-size', '15px')
      await page.addStyleTag({ content: `html { font-size: ${rootFontSize}px !important; }` })
      await page.evaluate(() => document.fonts.ready)
      await stats.scrollIntoViewIfNeeded()
      const state = await stats.evaluate((element) => {
        const header = element.closest('.workspace-header')!
        return {
          viewport: { width: innerWidth, height: innerHeight },
          rootFontSize: getComputedStyle(document.documentElement).fontSize,
          header: header.getBoundingClientRect().toJSON(),
          body: { scrollWidth: document.body.scrollWidth, clientWidth: document.body.clientWidth },
          fields: [...element.querySelectorAll('dt, dd')].map((field) => {
            const range = document.createRange()
            range.selectNodeContents(field)
            const bounds = field.getBoundingClientRect()
            const clips = [bounds, header.getBoundingClientRect()]
            for (let ancestor = field.parentElement; ancestor; ancestor = ancestor.parentElement) {
              const style = getComputedStyle(ancestor)
              if (/(hidden|clip|auto|scroll)/.test(`${style.overflowX} ${style.overflowY}`)) clips.push(ancestor.getBoundingClientRect())
            }
            const lines = [...range.getClientRects()].filter((rect) => rect.width && rect.height)
            return {
              text: field.textContent, bounds: bounds.toJSON(),
              lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
              scrollWidth: field.scrollWidth, clientWidth: field.clientWidth,
              contained: lines.length > 0 && lines.every((line) => clips.every((clip) =>
                line.left >= clip.left - 1 && line.right <= clip.right + 1 &&
                line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
            }
          }),
        }
      })
      // 先保存实际像素和字形坐标，失败时也能定位，不能只靠 body 没有横向滚动。
      if (process.env.NCP_CAPTURE_UI === '1') {
        const name = `fixture-overview-summary-${width}-font-${rootFontSize}`
        await mkdir(info.outputPath('ui-captures'), { recursive: true })
        await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({
          sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
          fixtureOnly: true, fixtureSource: 'frontend/e2e/overview-readability.spec.ts + frontend/e2e/mockApi.ts', ...state,
        }, null, 2))
        await page.screenshot({ path: info.outputPath('ui-captures', `${name}.png`), fullPage: false, animations: 'disabled' })
      }
      expect(state.rootFontSize).toBe(`${rootFontSize}px`)
      expect(state.body.scrollWidth).toBeLessThanOrEqual(width)
      expect(state.fields).toHaveLength(6)
      for (const field of state.fields) {
        expect(field.contained, `${field.text}: ${JSON.stringify(field)}`).toBe(true)
        expect(field.scrollWidth).toBeLessThanOrEqual(field.clientWidth + 1)
        expect(field.bounds.left).toBeGreaterThanOrEqual(0)
        expect(field.bounds.right).toBeLessThanOrEqual(width)
      }
    })
  }
}
