import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

import { installMockApi } from './mockApi'

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
  await page.goto('/docker', { waitUntil: 'domcontentloaded', timeout: 15_000 })
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible({ timeout: 15_000 })
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
  await expect.poll(() => actionRequests.some((url) => url.includes('/docker/compose/projects/media-stack/actions/stop'))).toBe(true)

  const projectIdentity = page.getByRole('button', { name: '查看 Docker 项目 备份任务', exact: true })
  await projectIdentity.focus()
  await expect(projectIdentity).toBeFocused()
  await projectIdentity.press('Enter')
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(browserErrors).toEqual([])
})
