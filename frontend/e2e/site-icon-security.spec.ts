import { test, expect } from '@playwright/test'
import fixture from './fixtures/site-icon-security.json' with { type: 'json' }

// 重放由 Go TestUploadedSiteIconMatchesBrowserSecurityContract 锁定的 handler 契约。
// 此处验证 Chromium 对响应策略的执行，不代表真实 HTTP 后端或 NAS 部署验收。
const protectedSVG = '/api/v1/sites/security-fixture/icon'
const unprotectedSVG = '/icon-security-positive-control.svg'
const protectedPNG = '/api/v1/sites/raster-fixture/icon'

test.beforeEach(async ({ page }) => {
  await page.route('**/icon-security-host', (route) => route.fulfill({
    contentType: 'text/html', body: '<!doctype html><html><body><main>Icon security fixture</main></body></html>',
  }))
  await page.route(`**${protectedSVG}`, (route) => route.fulfill({
    contentType: 'image/svg+xml', headers: fixture.headers, body: fixture.svg,
  }))
  // 同一 SVG 的正对照，确保载荷有效，不能把未执行测试当成隔离成功。
  await page.route(`**${unprotectedSVG}`, (route) => route.fulfill({
    contentType: 'image/svg+xml', body: fixture.svg,
  }))
  await page.route(`**${protectedPNG}`, (route) => route.fulfill({
    contentType: 'image/png', headers: fixture.headers, body: Buffer.from(fixture.pngBase64, 'base64'),
  }))
})

test('上传 SVG 契约：独立文档阻止 script/onload，未保护正对照会执行', async ({ page }) => {
  await page.goto(unprotectedSVG)
  await expect(page.locator('svg')).toHaveAttribute('data-script', 'executed')
  await expect(page.locator('svg')).toHaveAttribute('data-onload', 'executed')

  const response = await page.goto(protectedSVG)
  expect(response?.headers()['content-security-policy']).toBe(fixture.headers['Content-Security-Policy'])
  expect(response?.headers()['x-content-type-options']).toBe('nosniff')
  await expect(page.locator('svg')).toBeVisible()
  await expect(page.locator('rect')).toHaveCSS('fill', 'rgb(0, 128, 0)')
  expect(await page.locator('svg').getAttribute('data-script')).toBeNull()
  expect(await page.locator('svg').getAttribute('data-onload')).toBeNull()
})

test('上传 SVG 契约：嵌入文档隔离同源权限，正对照可以访问父页面', async ({ page }) => {
  await page.goto('/icon-security-host')
  await page.evaluate(({ protectedSVG, unprotectedSVG }) => {
    for (const [name, src] of [['protected', protectedSVG], ['control', unprotectedSVG]]) {
      const frame = document.createElement('iframe')
      frame.name = name!
      frame.src = src!
      document.body.append(frame)
    }
  }, { protectedSVG, unprotectedSVG })
  const control = page.frameLocator('iframe[name="control"]')
  const protectedFrame = page.frameLocator('iframe[name="protected"]')
  await expect(control.locator('svg')).toHaveAttribute('data-onload', 'executed')
  await expect(protectedFrame.locator('svg')).toBeVisible()
  await expect.poll(() => protectedFrame.locator('svg').evaluate(() => document.readyState)).toBe('complete')
  expect(await control.locator('svg').evaluate(() => parent.document.querySelector('main')?.textContent)).toBe('Icon security fixture')
  expect(await protectedFrame.locator('svg').evaluate(() => {
    try { return parent.document.querySelector('main')?.textContent }
    catch (error) { return (error as DOMException).name }
  })).toBe('SecurityError')
  expect(await protectedFrame.locator('svg').getAttribute('data-script')).toBeNull()
  expect(await protectedFrame.locator('svg').getAttribute('data-onload')).toBeNull()
})

test('上传图标契约：SVG 和 PNG 仍可作为图片解码，SVG 内联样式正常渲染', async ({ page }) => {
  await page.goto('/icon-security-host')
  const images = await page.evaluate(async ({ protectedSVG, protectedPNG }) => {
    const results = []
    for (const src of [protectedSVG, protectedPNG]) {
      const image = new Image()
      image.src = src
      document.body.append(image)
      await image.decode()
      const canvas = document.createElement('canvas')
      canvas.width = image.naturalWidth
      canvas.height = image.naturalHeight
      const context = canvas.getContext('2d')!
      context.drawImage(image, 0, 0)
      results.push({ width: image.naturalWidth, height: image.naturalHeight, pixel: Array.from(context.getImageData(0, 0, 1, 1).data) })
    }
    return results
  }, { protectedSVG, protectedPNG })
  expect(images[0]).toEqual({ width: 32, height: 32, pixel: [0, 128, 0, 255] })
  expect(images[1]?.width).toBe(1)
  expect(images[1]?.height).toBe(1)
})
