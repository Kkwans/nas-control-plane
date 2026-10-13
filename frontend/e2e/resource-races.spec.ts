import { expect, test } from '@playwright/test'
import { installMockApi } from './mockApi'

const collectedAt = '2026-10-13T00:00:00Z'
function barrier() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => { release = resolve })
  return { promise, release }
}

test('容器 A 慢 B 快：关闭 A 后只显示 B 标题、详情和日志', async ({ page }) => {
  await installMockApi(page)
  const delayed = barrier()
  let waiting = 0
  let released = 0
  await page.route('**/api/v1/docker/containers/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const id = path.split('/')[5]!
    if (id === 'media-api-id') {
      waiting += 1
      await delayed.promise
    }
    const body = path.endsWith('/logs')
      ? { containerId: id, tail: 200, collectedAt, entries: [{ timestamp: collectedAt, stream: 'stdout', level: 'info', message: `${id} 日志` }] }
      : { id, name: id, image: `${id}:latest`, state: 'running', healthFailingStreak: 0, exitCode: 0, restartCount: 0, oomKilled: false, restartMaximumRetries: 0, autoRemove: false, privileged: false, readonlyRootfs: false, nanoCpus: 0, memoryBytes: 0, ports: [], mounts: [], networks: [{ name: `${id} 网络` }] }
    await route.fulfill({ json: body }).catch(() => { /* 已取消的只读请求无需重试。 */ })
    if (id === 'media-api-id') released += 1
  })
  await page.goto('/docker')
  await page.getByRole('button', { name: '容器', exact: true }).click()
  await page.locator('.container-row').filter({ hasText: 'media-api:latest' }).getByRole('button', { name: '查看容器详情与日志' }).click()
  await expect.poll(() => waiting).toBe(2)
  const drawer = page.locator('.container-detail-drawer')
  await expect(drawer).toContainText('正在读取容器信息')
  await drawer.getByRole('button', { name: /close|关闭/i }).click()
  await page.locator('.container-row').filter({ hasText: 'media-worker:latest' }).getByRole('button', { name: '查看容器详情与日志' }).click()
  await expect(drawer).toContainText('media-worker-id 日志')
  await expect(drawer).toContainText('media-worker-id 网络')
  delayed.release()
  await expect.poll(() => released).toBe(2)
  await expect(drawer.locator('.drawer-heading')).toContainText('media-worker')
  await expect(drawer).not.toContainText('media-api-id')
  await drawer.getByRole('button', { name: /close|关闭/i }).click()
  await expect(drawer).not.toBeVisible()
})

test('日志分页未完成时换筛选，旧结果不污染新查询且仍可加载更多', async ({ page }) => {
  await installMockApi(page)
  const delayed = barrier()
  let waiting = false
  let released = false
  const observed: string[] = []
  await page.route('**/api/v1/logs**', async (route) => {
    const url = new URL(route.request().url())
    const source = url.searchParams.get('source') || 'system'
    const cursor = url.searchParams.get('cursor') || ''
    observed.push(`${source}:${cursor}`)
    if (source === 'system' && cursor) { waiting = true; await delayed.promise }
    await route.fulfill({ json: {
      collectedAt,
      entries: [{ id: `${source}-${cursor || 'first'}`, timestamp: collectedAt, source, unit: 'fixture.service', level: 'info', message: `${source} ${cursor ? '更多记录' : '首批记录'}` }],
      nextCursor: cursor ? '' : 'next',
    } }).catch(() => { /* 筛选切换会取消旧分页。 */ })
    if (source === 'system' && cursor) released = true
  })
  await page.goto('/logs')
  await expect(page.getByText('system 首批记录', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '加载更多结果' }).click()
  await expect.poll(() => waiting).toBe(true)
  // Element Plus 的输入节点被选中项覆盖；点击实际选择器，并避开粘性顶栏。
  const sourceSelect = page.locator('.log-filter-field--source .el-select__wrapper')
  await sourceSelect.evaluate((element) => element.scrollIntoView({ block: 'center', behavior: 'instant' }))
  await expect(sourceSelect).toBeVisible()
  await sourceSelect.click()
  await expect(page.getByRole('combobox', { name: '日志来源' })).toHaveAttribute('aria-expanded', 'true')
  await page.getByRole('option', { name: 'NCP Agent', exact: true }).click()
  await expect(page.getByText('agent 首批记录', { exact: true })).toBeVisible()
  delayed.release()
  await expect.poll(() => released).toBe(true)
  await page.getByRole('button', { name: '加载更多结果' }).click()
  await expect(page.getByText('agent 更多记录', { exact: true })).toBeVisible()
  await expect(page.getByText('system 更多记录', { exact: true })).toHaveCount(0)
  expect(observed).toContain('agent:next')
})
