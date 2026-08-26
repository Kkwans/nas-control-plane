import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

import { installMockApi } from './mockApi'

const routeNavigationTimeout = 30_000

test('Docker 生命周期按影响确认并保持项目入口可键盘访问', async ({ page }) => {
  const browserErrors: string[] = []
  const actionRequests: string[] = []
  page.on('pageerror', (error) => browserErrors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error') browserErrors.push(`console.error: ${message.text()}`)
  })
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().includes('/api/v1/docker/')) actionRequests.push(request.url())
  })

  await installMockApi(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/docker', { waitUntil: 'domcontentloaded', timeout: routeNavigationTimeout })
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible({ timeout: routeNavigationTimeout })
  await expect(page.getByRole('button', { name: '查看 Docker 项目 备份任务', exact: true })).toBeVisible()

  const accessibility = await new AxeBuilder({ page }).analyze()
  const blockingViolations = accessibility.violations.filter((violation) => violation.impact === 'critical' || violation.impact === 'serious')
  expect(blockingViolations, JSON.stringify(blockingViolations, null, 2)).toEqual([])

  const startButton = page.getByRole('button', { name: '启动项目 备份任务', exact: true })
  await expect(startButton).toBeEnabled()
  await startButton.click()
  await expect(page.locator('.el-message-box')).toHaveCount(0)
  await expect.poll(() => actionRequests.some((url) => url.includes('/docker/compose/projects/backup-stack/actions/start'))).toBe(true)
  await expect(page.getByText('项目“备份任务”已启动', { exact: true })).toBeVisible()

  const stopButton = page.getByRole('button', { name: '停止项目 媒体服务', exact: true })
  await expect(stopButton).toBeEnabled()
  await stopButton.click()
  const confirmation = page.locator('.el-message-box')
  await expect(confirmation).toBeVisible()
  await expect(confirmation).toContainText('镜像、卷、Compose 文件和工作目录不会删除')
  await confirmation.getByRole('button', { name: '确认停止', exact: true }).click()
  await expect(confirmation).toHaveCount(0)
  await expect.poll(() => actionRequests.some((url) => url.includes('/docker/compose/projects/media-stack/actions/stop'))).toBe(true)

  const projectIdentity = page.getByRole('button', { name: '查看 Docker 项目 备份任务', exact: true })
  await projectIdentity.focus()
  await expect(projectIdentity).toBeFocused()
  await projectIdentity.press('Enter')
  await expect(page.locator('.project-drawer')).toBeVisible()
  await expect(browserErrors).toEqual([])
})

test('Docker 镜像仓库在四档 viewport 可搜索、查看标签且无横向溢出', async ({ page }) => {
  test.setTimeout(120_000)
  const browserErrors: string[] = []
  page.on('pageerror', (error) => browserErrors.push(`pageerror: ${error.message}`))
  page.on('console', (message) => {
    if (message.type() === 'error' && !message.text().includes('status of 502 (Bad Gateway)')) browserErrors.push(`console.error: ${message.text()}`)
  })
  await installMockApi(page)

  for (const viewport of [
    { name: 'desktop-wide', width: 1440, height: 900 },
    { name: 'desktop-compact', width: 1280, height: 800 },
    { name: 'tablet', width: 768, height: 1024 },
    { name: 'mobile', width: 390, height: 844 },
  ]) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.goto('/docker', { waitUntil: 'domcontentloaded', timeout: routeNavigationTimeout })
    await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible({ timeout: routeNavigationTimeout })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    await expect(page.getByText('media-api:latest', { exact: true })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: '线上仓库', exact: true }).click()
    await page.getByRole('textbox', { name: '搜索 Docker Hub 镜像' }).fill('nginx')
    await page.getByRole('button', { name: '搜索镜像', exact: true }).click()
    await expect(page.getByRole('button', { name: /nginx/ }).first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('选择版本', { exact: true })).toBeVisible({ timeout: 15_000 })

    await page.getByRole('textbox', { name: '搜索 Docker Hub 镜像' }).fill('missing')
    await page.getByRole('button', { name: '搜索镜像', exact: true }).click()
    await expect(page.getByText('输入关键字，搜索 Docker Hub 公共镜像', { exact: true })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('textbox', { name: '搜索 Docker Hub 镜像' }).fill('broken')
    await page.getByRole('button', { name: '搜索镜像', exact: true }).click()
    await expect(page.getByText('Fixture Docker Hub 搜索失败。', { exact: true })).toBeVisible({ timeout: 15_000 })
    await page.getByRole('textbox', { name: '搜索 Docker Hub 镜像' }).fill('nginx')
    await page.getByRole('button', { name: '搜索镜像', exact: true }).click()
    await expect(page.getByText('选择版本', { exact: true })).toBeVisible({ timeout: 15_000 })

    const layout = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      viewportWidth: window.innerWidth,
      mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
    }))
    expect(layout.bodyWidth, `${viewport.name} body 横向溢出`).toBeLessThanOrEqual(layout.viewportWidth)
    expect(layout.mainWidth, `${viewport.name} main 横向溢出`).toBeLessThanOrEqual(layout.viewportWidth)

    if (viewport.name === 'desktop-wide') {
      const accessibility = await new AxeBuilder({ page }).analyze()
      const blockingViolations = accessibility.violations.filter((violation) => violation.impact === 'critical' || violation.impact === 'serious')
      expect(blockingViolations, JSON.stringify(blockingViolations, null, 2)).toEqual([])
    }

    await expect(page).toHaveScreenshot(`docker-hub-${viewport.name}.png`, { fullPage: false, animations: 'disabled', timeout: 30_000 })
  }

  expect(browserErrors).toEqual([])
})

test('创建容器使用已有资源、浏览 NAS 路径并在提交前展示配置摘要', async ({ page }) => {
  await installMockApi(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/docker', { waitUntil: 'domcontentloaded', timeout: routeNavigationTimeout })
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible({ timeout: routeNavigationTimeout })
  await page.getByRole('button', { name: '镜像', exact: true }).click()
  await expect(page.getByText('media-api:latest', { exact: true })).toBeVisible({ timeout: 15_000 })
  await page.getByRole('button', { name: '从镜像 media-api:latest 创建容器', exact: true }).click()

  const drawer = page.locator('.create-container-drawer')
  await expect(drawer).toBeVisible()
  await expect(drawer.getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0, { timeout: 15_000 })
  await drawer.getByRole('button', { name: '添加挂载', exact: true }).click()
  await drawer.getByRole('button', { name: '浏览', exact: true }).click()
  const browser = page.getByRole('dialog', { name: '选择 NAS 路径' })
  await expect(browser).toBeVisible()
  await browser.locator('button.path-entry__name').first().click()
  await expect(browser.locator('.path-browser__toolbar code')).toHaveText('/volume2')
  await browser.getByRole('button', { name: '选择', exact: true }).first().click()
  await expect(browser).toHaveCount(0)
  await drawer.getByPlaceholder('容器路径，例如 /data').fill('/data')

  await drawer.getByRole('button', { name: '创建并启动', exact: true }).click()
  const preview = page.getByRole('dialog', { name: '确认容器配置' })
  await expect(preview).toBeVisible()
  await expect(preview).toContainText('media-api:latest')
  await expect(preview).toContainText('/volume2')
  await preview.getByRole('button', { name: '确认创建并启动', exact: true }).click()
  await expect(page.getByText('容器“media-api”已创建并启动', { exact: true })).toBeVisible()
})
