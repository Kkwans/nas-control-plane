import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { installMockApi } from './mockApi'

// 仅测试 HTTP/SSE fixture；所有变更默认拒绝，唯一例外是显式点击 Hub 后的一次模拟拉取。
const timestamp = '2026-10-13T08:00:00Z'
const job = (fields: Record<string, unknown> = {}) => ({
  id: 'download-recovery', type: 'docker-image-pull', status: 'running', artifactState: 'unknown',
  reference: 'nginx:latest', message: '正在下载镜像层', progress: 42,
  createdAt: timestamp, updatedAt: timestamp, downloadedBytes: 42, totalBytes: 100,
  speedBytes: 1024, layers: {}, cancellable: true, ...fields,
})
type Snapshot = ReturnType<typeof job>
type StreamHost = { recoveryJobStreams: EventSource[] }
const workspace = (page: Page) => page.locator('.download-workspace')
const rowOf = (page: Page) => workspace(page).locator('.download-row').filter({ hasText: 'nginx:latest' })
const refresh = (page: Page) => page.getByRole('button', { name: '刷新下载任务', exact: true })
const recover = (page: Page) => rowOf(page).getByRole('button', { name: '重新获取 nginx:latest 的下载进度', exact: true })

async function fixture(page: Page, initial: Snapshot[] = [job()], initialFailure = false) {
  await installMockApi(page)
  let jobs = initial
  let failure = initialFailure
  let reads = 0
  let allowPull = false
  const mutations: string[] = []
  const blocked: string[] = []
  await page.route('**/api/v1/jobs?*', (route) => {
    reads += 1
    expect(route.request().method()).toBe('GET')
    expect(new URL(route.request().url()).searchParams.get('type')).toBe('docker-image-pull')
    return route.fulfill(failure
      ? { status: 503, json: { code: 'AGENT_UNAVAILABLE', message: 'Fixture 下载记录读取不可用' } }
      : { json: { jobs } })
  })
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    if (['GET', 'HEAD', 'OPTIONS'].includes(request.method())) return route.fallback()
    const target = `${request.method()} ${new URL(request.url()).pathname}`
    mutations.push(target)
    if (allowPull && target === 'POST /api/v1/docker/images/pull') {
      allowPull = false
      expect(request.postDataJSON()).toMatchObject({ reference: 'nginx:latest' })
      jobs = [job({ status: 'queued', progress: 0, downloadedBytes: 0, speedBytes: 0 })]
      return route.fulfill({ status: 202, json: jobs[0] })
    }
    blocked.push(target)
    return route.fulfill({ status: 405, json: { code: 'FIXTURE_MUTATION_DENIED', message: '未授权的 fixture 变更' } })
  })
  return {
    get reads() { return reads }, mutations, blocked,
    fail() { failure = true },
    succeed(next = jobs) { failure = false; jobs = next },
    allowOneHubPull() { allowPull = true },
  }
}

async function openImages(page: Page) {
  await page.goto('/docker')
  await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible()
  // 包装通用手动 EventSource fixture，不启动真实网络流，也不干扰概览流。
  await page.evaluate(() => {
    const host = window as unknown as StreamHost
    host.recoveryJobStreams = []
    const Original = window.EventSource
    window.EventSource = class extends Original {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options)
        if (String(url).includes('/api/v1/jobs/')) host.recoveryJobStreams.push(this)
      }
    }
  })
  await page.getByRole('button', { name: '镜像', exact: true }).click()
}
async function openDownloads(page: Page) {
  await openImages(page)
  await page.getByRole('button', { name: /^下载任务/ }).click()
  await expect(workspace(page)).toBeVisible()
  await expect(refresh(page)).toBeEnabled()
}
async function streams(page: Page) {
  return page.evaluate(() => {
    const values = (window as unknown as StreamHost).recoveryJobStreams
    return { created: values.length, active: values.filter((source) => source.readyState !== EventSource.CLOSED).length }
  })
}
async function emit(page: Page, snapshot?: Snapshot) {
  await page.evaluate((value) => {
    for (const source of (window as unknown as StreamHost).recoveryJobStreams) {
      if (source.readyState === EventSource.CLOSED) continue
      if (value) source.dispatchEvent(new MessageEvent('progress', { data: JSON.stringify(value) }))
      else source.onerror?.(new Event('error'))
    }
  }, snapshot)
}
async function expectInterrupted(page: Page) {
  const row = rowOf(page)
  await expect(row.locator('.job-state')).toHaveText('下载中')
  await expect(row.locator('.job-observation-warning')).toHaveText('进度连接中断')
  await expect(row).toContainText('任务可能仍在执行')
  await expect(row).not.toContainText('/s')
  await expect(row.getByRole('progressbar', { name: 'nginx:latest 上次报告进度', exact: true })).toHaveAttribute('aria-valuenow', '42')
  await expect.poll(() => streams(page)).toEqual({ created: 1, active: 0 })
}

async function capture(page: Page, info: TestInfo, name: string, state: Awaited<ReturnType<typeof fixture>>) {
  // 全量 axe 结果和截图描述同一状态；不得过滤 contrast/ARIA 等规则。
  const axe = await new AxeBuilder({ page }).analyze()
  const directory = info.outputPath('ui-captures')
  if (process.env.NCP_CAPTURE_UI === '1') {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: `${directory}/${name}.png`, fullPage: false, animations: 'disabled' })
  }
  // PNG 完成后读取状态；手动 Job 流在截图与状态记录期间不会自己推进。
  const browserState = await page.evaluate(() => {
    const panel = document.querySelector('.download-workspace')
    return {
      viewport: { width: innerWidth, height: innerHeight },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      panel: panel ? { bounds: panel.getBoundingClientRect().toJSON(), scrollLeft: panel.scrollLeft,
        scrollWidth: panel.scrollWidth, clientWidth: panel.clientWidth, text: panel.textContent } : null,
      rows: [...document.querySelectorAll('.download-row')].map((row) => ({
        text: row.textContent, status: row.querySelector('.job-state')?.textContent,
        warning: row.querySelector('.job-observation-warning')?.textContent,
        progress: row.querySelector('[role="progressbar"]')?.getAttribute('aria-valuenow'),
      })),
      controls: [...document.querySelectorAll('.download-workspace button, .download-mode-actions button')].map((element) => {
        const rect = element.getBoundingClientRect()
        const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)
        return { label: element.getAttribute('aria-label') || element.textContent || element.getAttribute('title'),
          bounds: rect.toJSON(), disabled: element.hasAttribute('disabled'), centerHit: Boolean(hit && element.contains(hit)) }
      }),
    }
  })
  expect(browserState.root.scrollWidth).toBeLessThanOrEqual(browserState.root.clientWidth + 1)
  if (process.env.NCP_CAPTURE_UI === '1') {
    const metadata = { sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
      fixtureOnly: true, fixtureSource: 'frontend/e2e/download-recovery.spec.ts + frontend/e2e/mockApi.ts',
      state: name, reads: state.reads, mutations: state.mutations, blocked: state.blocked,
      streams: await streams(page), ...browserState }
    await writeFile(`${directory}/${name}-capture-state.json`, JSON.stringify(metadata, null, 2))
    await writeFile(`${directory}/${name}-axe-capture-state.json`, JSON.stringify(axe, null, 2))
    await info.attach(`${name}-state`, { path: `${directory}/${name}-capture-state.json`, contentType: 'application/json' })
    await info.attach(`${name}-screenshot`, { path: `${directory}/${name}.png`, contentType: 'image/png' })
  }
  expect(axe.violations).toEqual([])
}

for (const width of [390, 1440]) {
  test(`${width}px 首次读取失败不伪装空列表，可只读重试`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const state = await fixture(page, [], true)
    await openDownloads(page)
    await expect(workspace(page).getByRole('alert')).toContainText('下载任务读取失败')
    await expect(workspace(page).locator('.empty-state')).toHaveCount(0)
    await capture(page, info, `initial-read-failure-${width}`, state)
    const before = state.reads
    state.succeed([])
    await page.getByRole('button', { name: '重新加载下载任务', exact: true }).click()
    await expect(workspace(page)).toContainText('暂无镜像下载任务')
    expect(state.reads).toBe(before + 1)
    expect(state.mutations).toEqual([])
    expect(state.blocked).toEqual([])
  })

  test(`${width}px 断流保留执行状态，缓存读取失败保留行，活动快照恢复单一观察`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const state = await fixture(page)
    await openDownloads(page)
    await expect.poll(() => streams(page)).toEqual({ created: 1, active: 1 })
    await emit(page, job())
    await expect(rowOf(page)).toContainText('/s')
    await emit(page)
    await expectInterrupted(page)
    // 窄屏使用浏览器正常滚动，让恢复按钮真实可见再截图及点击。
    await recover(page).scrollIntoViewIfNeeded()
    await expect(recover(page)).toBeInViewport({ ratio: 1 })
    await capture(page, info, `interrupted-${width}`, state)
    state.fail()
    await recover(page).click()
    await expect(workspace(page).getByRole('alert')).toContainText('下载任务刷新失败')
    await expect(workspace(page).getByRole('alert')).toContainText('上次成功获取')
    await expectInterrupted(page)
    await page.getByRole('button', { name: '重新加载下载任务', exact: true }).scrollIntoViewIfNeeded()
    await capture(page, info, `cached-read-failure-${width}`, state)
    state.succeed([job({ progress: 55, downloadedBytes: 55, updatedAt: '2026-10-13T08:01:00Z' })])
    await page.getByRole('button', { name: '重新加载下载任务', exact: true }).click()
    await expect(rowOf(page).locator('.job-observation-warning')).toHaveCount(0)
    await expect(rowOf(page)).toContainText('55%')
    await expect(rowOf(page)).not.toContainText('/s')
    await expect.poll(() => streams(page)).toEqual({ created: 2, active: 1 })
    await emit(page, job({ progress: 60, downloadedBytes: 60, updatedAt: '2026-10-13T08:02:00Z' }))
    await expect(rowOf(page)).toContainText('/s')
    expect(state.mutations).toEqual([])
    expect(state.blocked).toEqual([])
  })
}

test('断流后只读刷新取得终态，不重开流或重放变更', async ({ page }) => {
  const state = await fixture(page)
  await openDownloads(page)
  await expect.poll(() => streams(page)).toEqual({ created: 1, active: 1 })
  await emit(page, job())
  await expect(rowOf(page)).toContainText('/s')
  state.fail(); await refresh(page).click()
  await expect(workspace(page).getByRole('alert')).toContainText('下载任务刷新失败')
  await expect(rowOf(page)).toContainText('/s')
  await expect.poll(() => streams(page)).toEqual({ created: 1, active: 1 })
  await emit(page)
  await expectInterrupted(page)
  state.succeed([job({ status: 'completed', progress: 100, downloadedBytes: 100, cancellable: false })])
  await recover(page).click()
  await expect(rowOf(page).locator('.job-state')).toHaveText('已完成')
  await expect(rowOf(page).locator('.job-observation-warning')).toHaveCount(0)
  await expect(rowOf(page)).not.toContainText('/s')
  await expect.poll(() => streams(page)).toEqual({ created: 1, active: 0 })
  expect(state.mutations).toEqual([])
  expect(state.blocked).toEqual([])
})

test('真实 Hub 创建路径只有一个观察者，断流与读取恢复不会再次提交拉取', async ({ page }) => {
  const state = await fixture(page, [])
  await openImages(page)
  await page.getByRole('button', { name: '线上仓库', exact: true }).click()
  await page.getByRole('textbox', { name: '搜索 Docker Hub 镜像', exact: true }).fill('nginx')
  await page.getByRole('button', { name: '搜索镜像', exact: true }).click()
  await expect(page.getByText('选择版本', { exact: true })).toBeVisible()
  state.allowOneHubPull()
  await page.getByRole('button', { name: '加入下载队列', exact: true }).click()
  await expect.poll(() => state.mutations.length).toBe(1)
  await expect.poll(() => streams(page)).toEqual({ created: 1, active: 1 })
  await page.getByRole('button', { name: /^下载任务/ }).click()
  await emit(page, job())
  await expect(rowOf(page)).toContainText('42%')
  await emit(page)
  await expectInterrupted(page)
  state.fail(); await recover(page).click()
  await expect(workspace(page).getByRole('alert')).toContainText('下载任务刷新失败')
  state.succeed([job({ status: 'completed', progress: 100, downloadedBytes: 100, cancellable: false })])
  await page.getByRole('button', { name: '重新加载下载任务', exact: true }).click()
  await expect(rowOf(page).locator('.job-state')).toHaveText('已完成')
  await expect.poll(() => streams(page)).toEqual({ created: 1, active: 0 })
  expect(state.mutations).toEqual(['POST /api/v1/docker/images/pull'])
  expect(state.blocked).toEqual([])
})
