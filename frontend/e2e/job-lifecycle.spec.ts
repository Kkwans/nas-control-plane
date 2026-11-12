import { expect, test, type Page } from '@playwright/test'
import { installMockApi } from './mockApi'

const timestamp = '2026-10-13T00:00:00Z'
const job = (fields: Record<string, unknown> = {}) => ({
  id: 'job-lifecycle', type: 'docker-image-pull', status: 'queued', artifactState: 'unknown', reference: 'nginx:latest',
  message: '已加入下载队列', progress: 0, createdAt: timestamp, updatedAt: timestamp,
  downloadedBytes: 0, totalBytes: 100, speedBytes: 0, layers: {}, cancellable: true, ...fields,
})
function barrier() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => { release = resolve })
  return { promise, release }
}

// 保留通用 fixture 的概览事件，仅记录 Job 流并由断言驱动进度事件。
async function observeJobStreams(page: Page) {
  await page.evaluate(() => {
    const host = window as unknown as { jobStreams: EventSource[] }
    host.jobStreams = []
    const Original = window.EventSource
    window.EventSource = class extends Original {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options)
        if (String(url).includes('/api/v1/jobs/')) host.jobStreams.push(this)
      }
    }
  })
}
async function emitJob(page: Page, snapshot: ReturnType<typeof job>) {
  await page.evaluate((value) => {
    const streams = (window as unknown as { jobStreams: EventSource[] }).jobStreams
    for (const stream of streams) {
      if (stream.readyState !== EventSource.CLOSED) stream.dispatchEvent(new MessageEvent('progress', { data: JSON.stringify(value) }))
    }
  }, snapshot)
}
async function streamCount(page: Page) {
  return page.evaluate(() => (window as unknown as { jobStreams: EventSource[] }).jobStreams.filter((stream) => stream.readyState !== EventSource.CLOSED).length)
}

async function openImages(page: Page) {
  await page.goto('/docker')
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible()
  await observeJobStreams(page)
  await page.getByRole('button', { name: '镜像', exact: true }).click()
}

test('Hub 提交未返回时切到下载任务，接收后仍显示任务并持续更新进度', async ({ page }) => {
  await installMockApi(page)
  await page.route('**/api/v1/jobs?*', (route) => route.fulfill({ json: { jobs: [] } }))
  const accepted = barrier()
  let submitted = false
  await page.route('**/api/v1/docker/images/pull', async (route) => {
    submitted = true
    await accepted.promise
    await route.fulfill({ status: 202, json: job() })
  })
  await openImages(page)
  await page.getByRole('button', { name: '线上仓库', exact: true }).click()
  await page.getByRole('textbox', { name: '搜索 Docker Hub 镜像' }).fill('nginx')
  await page.getByRole('button', { name: '搜索镜像', exact: true }).click()
  await expect(page.getByText('选择版本', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '加入下载队列', exact: true }).click()
  await expect.poll(() => submitted).toBe(true)
  await page.getByRole('button', { name: /^下载任务/ }).click()
  await expect(page.locator('.download-workspace')).toBeVisible()
  await expect(page.locator('.hub-panel')).not.toBeVisible()
  accepted.release()
  const row = page.locator('.download-row').filter({ hasText: 'nginx:latest' })
  await expect(row).toContainText('排队中')
  await expect.poll(() => streamCount(page)).toBe(1)
  await emitJob(page, job({ status: 'running', progress: 42, downloadedBytes: 42, message: '镜像层下载中' }))
  await expect(row).toContainText('42%')
  await expect(row).toContainText('镜像层下载中')
  await emitJob(page, job({ status: 'completed', artifactState: 'present', progress: 100, downloadedBytes: 100, message: '镜像拉取完成' }))
  await expect(row).toContainText('已完成')
  await expect.poll(() => streamCount(page)).toBe(0)
})

test('取消请求与待保存结果保持活动界面，未知终态才允许删除', async ({ page }) => {
  await installMockApi(page)
  await page.route('**/api/v1/jobs?*', (route) => route.fulfill({ json: { jobs: [job({ status: 'running' })] } }))
  const cancellation = barrier()
  let cancelRequests = 0
  await page.route('**/api/v1/jobs/job-lifecycle/cancel', async (route) => {
    cancelRequests += 1
    await cancellation.promise
    await route.fulfill({ status: 202, json: job({ status: 'running', cancellationStatus: 'requested', message: '正在请求停止' }) })
  })
  await openImages(page)
  await page.getByRole('button', { name: /^下载任务/ }).click()
  const row = page.locator('.download-row').filter({ hasText: 'nginx:latest' })
  const cancel = row.getByRole('button', { name: '请求取消下载（不保证远端立即停止）', exact: true })
  await expect.poll(() => streamCount(page)).toBe(1)
  await cancel.click()
  await expect.poll(() => cancelRequests).toBe(1)
  await expect(cancel).toBeDisabled()
  cancellation.release()
  await expect(row).toContainText('取消中')
  await expect(row.getByRole('button', { name: '删除下载记录', exact: true })).toHaveCount(0)
  await emitJob(page, job({ status: 'running', cancellationStatus: 'requested', persistenceStatus: 'pending', message: '执行已返回，结果保存失败，正在重试' }))
  await expect(row).toContainText('结果保存中')
  await expect(cancel).toBeDisabled()
  await expect.poll(() => streamCount(page)).toBe(1)
  await emitJob(page, job({ status: 'interrupted', cancellationStatus: 'unknown', message: '远端执行结果未确认' }))
  await expect(row).toContainText('结果未确认')
  await expect(row).not.toContainText('已停止')
  await expect(row.getByRole('button', { name: '删除下载记录', exact: true })).toBeVisible()
  await expect(cancel).toHaveCount(0)
  await expect.poll(() => streamCount(page)).toBe(0)
  expect(cancelRequests).toBe(1)
})
