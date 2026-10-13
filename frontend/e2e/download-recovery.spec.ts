import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { installMockApi } from './mockApi'

// 仅测试 HTTP/SSE fixture；所有变更默认拒绝，唯一例外是显式点击 Hub 后的一次模拟拉取。
const timestamp = '2026-10-13T08:00:00Z'
const responsiveWidths = [390, 768, 820, 821, 1024, 1200, 1280, 1440]
const longReference = `registry.example.test/team/${'unbrokendownloadreference'.repeat(6)}:release-2026-10-13`
const sha256 = (value: string | Buffer) => createHash('sha256').update(value).digest('hex')
const job = (fields: Record<string, unknown> = {}) => ({
  id: 'download-recovery', type: 'docker-image-pull', status: 'running', artifactState: 'unknown',
  reference: 'nginx:latest', message: '正在下载镜像层', progress: 42,
  createdAt: timestamp, updatedAt: timestamp, downloadedBytes: 42, totalBytes: 100,
  speedBytes: 1024, layers: {}, cancellable: true, ...fields,
})
type Snapshot = ReturnType<typeof job>
type StreamHost = { recoveryJobStreams: EventSource[] }
const workspace = (page: Page) => page.locator('.download-workspace')
const rowOf = (page: Page, reference = 'nginx:latest') => workspace(page).getByRole('article', { name: `${reference} 下载任务`, exact: true })
const refresh = (page: Page) => page.getByRole('button', { name: '刷新下载任务', exact: true })
const recover = (page: Page, reference = 'nginx:latest') => rowOf(page, reference).getByRole('button', { name: `重新获取 ${reference} 的下载进度`, exact: true })

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
async function expectInterrupted(page: Page, reference = 'nginx:latest') {
  const row = rowOf(page, reference)
  await expect(row.locator('.job-state')).toHaveText('下载中')
  await expect(row.locator('.job-observation-warning')).toHaveText('进度连接中断')
  await expect(row).toContainText('任务可能仍在执行')
  await expect(row).not.toContainText('/s')
  await expect(row.getByRole('progressbar', { name: `${reference} 上次报告进度`, exact: true })).toHaveAttribute('aria-valuenow', '42')
  await expect.poll(() => streams(page)).toEqual({ created: 1, active: 0 })
}

// 不通过横向滚动把隐藏的按钮移入视口；同时检查容器与每个语义字段。
async function downloadLayout(page: Page) {
  return workspace(page).evaluate((panel) => {
    const bounds = (element: Element) => {
      const range = document.createRange()
      range.selectNodeContents(element)
      const style = getComputedStyle(element)
      return {
        bounds: element.getBoundingClientRect().toJSON(),
        scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
        scrollHeight: element.scrollHeight, clientHeight: element.clientHeight,
        text: element.textContent, whiteSpace: style.whiteSpace, overflow: style.overflow,
        textOverflow: style.textOverflow,
        textBounds: [...range.getClientRects()].map((rect) => rect.toJSON()),
      }
    }
    const style = getComputedStyle(panel)
    const row = panel.querySelector('.download-row')
    const toolbar = document.querySelector('.image-modebar')!
    const tabs = toolbar.querySelector('.section-tabs')!
    const actions = toolbar.querySelector('.download-mode-actions')!
    const selectors = {
      identity: '.job-identity strong', message: '.job-identity small', status: '.job-state',
      warning: '.job-observation-warning', progress: '.job-progress', progressText: '.job-progress small',
      speed: '.job-speed', observedTime: '.job-observed-time',
      caveat: '.job-observation-recovery>span', recover: '.job-observation-recovery button',
      readRetry: '.download-read-state button',
    }
    return {
      rootFontPixels: Number.parseFloat(getComputedStyle(document.documentElement).fontSize),
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth, scrollLeft: document.documentElement.scrollLeft },
      panel: { ...bounds(panel), scrollLeft: panel.scrollLeft,
        contentWidth: panel.clientWidth - Number.parseFloat(style.paddingLeft) - Number.parseFloat(style.paddingRight),
        containerName: style.containerName, containerType: style.containerType },
      toolbar: {
        ...bounds(toolbar),
        tabs: { ...bounds(tabs), scrollLeft: tabs.scrollLeft },
        actions: bounds(actions),
        buttons: [...tabs.querySelectorAll('button')].map((button) => ({
          ...bounds(button), selected: button.getAttribute('aria-pressed') === 'true',
          label: bounds(button.querySelector('span')!),
          count: button.querySelector('small') ? bounds(button.querySelector('small')!) : null,
        })),
        controls: Object.fromEntries(['.el-input', '.ncp-select', '.el-select__placeholder', '.icon-button'].map((selector) => [selector, bounds(actions.querySelector(selector)!)])),
      },
      headDisplay: getComputedStyle(panel.querySelector('.download-head')!).display,
      rowColumns: row ? getComputedStyle(row).gridTemplateColumns.split(' ') : [],
      fields: Object.fromEntries(Object.entries(selectors).map(([name, selector]) => {
        const element = panel.querySelector(selector)
        return [name, element ? bounds(element) : null]
      })),
    }
  })
}

function expectImageToolbarLayout(layout: Awaited<ReturnType<typeof downloadLayout>>) {
  const { toolbar } = layout
  type Field = typeof toolbar.buttons[number]['label']
  expect(toolbar.bounds.left).toBeGreaterThanOrEqual(layout.panel.bounds.left - 1)
  expect(toolbar.bounds.right).toBeLessThanOrEqual(layout.panel.bounds.right + 1)
  expect(toolbar.tabs.scrollLeft, '镜像来源不能通过自动横向滚动隐藏其他标签').toBe(0)
  expect(toolbar.buttons.map((button) => button.label.text)).toEqual(['本地镜像', '线上仓库', '下载任务'])
  expect(toolbar.buttons.filter((button) => button.selected).map((button) => button.label.text)).toEqual(['下载任务'])
  const contents: [string, Field][] = [
    ['toolbar', toolbar], ['tabs', toolbar.tabs], ['actions', toolbar.actions],
    ...toolbar.buttons.flatMap((button, index): [string, Field][] => [[`tab-${index}`, button], [`tab-label-${index}`, button.label], ...(button.count ? [[`tab-count-${index}`, button.count] as [string, Field]] : [])]),
    ...Object.entries(toolbar.controls),
  ]
  for (const [name, entry] of contents) {
    // label/count 是行内元素，clientWidth 为 0；其 Range 必须完整落在按钮和工具栏内。
    expect(entry.bounds.width, `${name} 必须可见`).toBeGreaterThan(0)
    if (entry.clientWidth) expect(entry.scrollWidth, `${name} 不得横向截断或依赖滚动`).toBeLessThanOrEqual(entry.clientWidth + 1)
    if (entry.clientHeight) expect(entry.scrollHeight, `${name} 不得纵向截断`).toBeLessThanOrEqual(entry.clientHeight + 1)
    for (const rect of [entry.bounds, ...entry.textBounds]) {
      expect(rect.left, `${name} 不得藏在工具栏左侧`).toBeGreaterThanOrEqual(toolbar.bounds.left - 1)
      expect(rect.right, `${name} 不得藏在工具栏右侧`).toBeLessThanOrEqual(toolbar.bounds.right + 1)
      expect(rect.top, `${name} 不得藏在工具栏上侧`).toBeGreaterThanOrEqual(toolbar.bounds.top - 1)
      expect(rect.bottom, `${name} 不得藏在工具栏下侧`).toBeLessThanOrEqual(toolbar.bounds.bottom + 1)
    }
  }
  const overlaps = (a: Field, b: Field) => Math.min(a.bounds.right, b.bounds.right) - Math.max(a.bounds.left, b.bounds.left) > 1
    && Math.min(a.bounds.bottom, b.bounds.bottom) - Math.max(a.bounds.top, b.bounds.top) > 1
  expect(overlaps(toolbar.tabs, toolbar.actions), '来源标签与搜索筛选不得重叠').toBe(false)
  for (const button of toolbar.buttons) {
    for (const sibling of toolbar.buttons) {
      if (sibling !== button) expect(overlaps(button, sibling), '换行后的来源按钮不得互相重叠').toBe(false)
    }
    expect(button.label.textOverflow, '标签不得省略完整名称').not.toBe('ellipsis')
    for (const rect of button.label.textBounds) {
      expect(rect.left).toBeGreaterThanOrEqual(button.bounds.left - 1)
      expect(rect.right).toBeLessThanOrEqual(button.bounds.right + 1)
      expect(rect.top).toBeGreaterThanOrEqual(button.bounds.top - 1)
      expect(rect.bottom).toBeLessThanOrEqual(button.bounds.bottom + 1)
    }
  }
  expect(toolbar.controls['.el-select__placeholder']!.textOverflow, '状态筛选保留完整文字').not.toBe('ellipsis')
}

async function expectDownloadLayout(page: Page, reference: string, interrupted = true, requireCards = false) {
  const layout = await downloadLayout(page)
  expectImageToolbarLayout(layout)
  // 200% 字号必须明确进入卡片，不能退回桌面省略/弱几何断言。
  const cards = requireCards || layout.panel.contentWidth <= Math.max(980, 61.25 * layout.rootFontPixels)
  expect(layout.root.scrollWidth, '页面不得依赖横向滚动').toBeLessThanOrEqual(layout.root.clientWidth + 1)
  expect(layout.root.scrollLeft).toBe(0)
  expect(layout.panel.scrollWidth, '下载面板不得保留 960px 宽的隐藏表格').toBeLessThanOrEqual(layout.panel.clientWidth + 1)
  expect(layout.panel.scrollLeft).toBe(0)
  expect(layout.panel.containerName).toBe('image-downloads')
  expect(layout.panel.containerType).toBe('inline-size')
  expect(layout.headDisplay).toBe(cards ? 'none' : 'grid')
  expect(layout.rowColumns).toHaveLength(cards ? 1 : 6)
  expect(layout.fields.identity?.text).toBe(reference)
  const required = ['identity', 'message', 'status', 'progress', 'progressText', 'speed', 'observedTime',
    ...(interrupted ? ['warning', 'caveat', 'recover'] : [])]
  for (const name of required) expect(layout.fields[name], `${name} 必须保留`).not.toBeNull()
  for (const [name, field] of Object.entries(layout.fields)) {
    if (!field) continue
    expect(field.bounds.width, `${name} 有可见宽度`).toBeGreaterThan(0)
    expect(field.bounds.left, `${name} 不得越过面板左侧`).toBeGreaterThanOrEqual(layout.panel.bounds.left - 1)
    expect(field.bounds.right, `${name} 不得越过面板右侧`).toBeLessThanOrEqual(layout.panel.bounds.right + 1)
    if (cards) {
      expect(field.scrollWidth, `${name} 卡片内容必须完整换行`).toBeLessThanOrEqual(field.clientWidth + 1)
      expect(field.scrollHeight, `${name} 卡片内容不得纵向截断`).toBeLessThanOrEqual(field.clientHeight + 1)
      for (const rect of field.textBounds) {
        expect(rect.left, `${name} 文本不得藏在面板左侧`).toBeGreaterThanOrEqual(layout.panel.bounds.left - 1)
        expect(rect.right, `${name} 文本不得藏在面板右侧`).toBeLessThanOrEqual(layout.panel.bounds.right + 1)
      }
    }
  }
  if (cards) {
    expect(layout.fields.identity?.whiteSpace).toBe('normal')
    expect(layout.fields.identity?.overflow).toBe('visible')
    for (const name of ['recover', 'readRetry']) {
      if (layout.fields[name]) expect(layout.fields[name]!.bounds.height, `${name} 触控高度`).toBeGreaterThanOrEqual(44)
    }
  }
  return layout
}

async function expectRecoveryInView(page: Page, reference: string, allContext: boolean, requireCards: boolean) {
  const row = rowOf(page, reference)
  // 只允许正常滚动；先断言没有横向依赖，避免 scrollIntoView 掩盖表格溢出。
  await expectDownloadLayout(page, reference, true, requireCards)
  await row.scrollIntoViewIfNeeded()
  await recover(page, reference).scrollIntoViewIfNeeded()
  await expect(recover(page, reference)).toBeInViewport({ ratio: 1 })
  if (allContext) {
    for (const selector of ['.job-identity strong', '.job-state', '.job-observation-warning', '.job-progress', '.job-observation-recovery>span']) {
      await expect(row.locator(selector), `${selector} 与恢复按钮应可同时阅读`).toBeInViewport({ ratio: 1 })
    }
  }
  await expectDownloadLayout(page, reference, true, requireCards)
  const hit = await recover(page, reference).evaluate((element) => {
    const rect = element.getBoundingClientRect()
    const target = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)
    return Boolean(target && element.contains(target))
  })
  expect(hit, '恢复按钮中心必须真实可点击，不得被遮挡').toBe(true)
}

async function capture(page: Page, info: TestInfo, name: string, state: Awaited<ReturnType<typeof fixture>>) {
  // 全量 axe 结果和截图描述同一状态；不得过滤 contrast/ARIA 等规则。
  const axe = await new AxeBuilder({ page }).analyze()
  const directory = info.outputPath('ui-captures')
  let screenshot: Buffer | undefined
  if (process.env.NCP_CAPTURE_UI === '1') {
    await mkdir(directory, { recursive: true })
    screenshot = await page.screenshot({ path: `${directory}/${name}.png`, fullPage: false, animations: 'disabled' })
  }
  // PNG 完成后读取状态；手动 Job 流在截图与状态记录期间不会自己推进。
  const browserState = await page.evaluate(() => ({
    viewport: { width: innerWidth, height: innerHeight },
    rootFontSize: getComputedStyle(document.documentElement).fontSize,
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
  }))
  const layout = await downloadLayout(page)
  expectImageToolbarLayout(layout)
  expect(layout.root.scrollWidth).toBeLessThanOrEqual(layout.root.clientWidth + 1)
  expect(layout.panel.scrollWidth).toBeLessThanOrEqual(layout.panel.clientWidth + 1)
  expect(layout.panel.scrollLeft).toBe(0)
  if (screenshot) {
    const sources = ['src/components/DockerImagePanel.vue', 'e2e/download-recovery.spec.ts', 'e2e/mockApi.ts']
    const sourceHashes = Object.fromEntries(await Promise.all(sources.map(async (source) => [source, sha256(await readFile(source))])))
    const metadata = { sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), sourceHashes,
      fixtureOnly: true, fixtureSource: 'frontend/e2e/download-recovery.spec.ts + frontend/e2e/mockApi.ts',
      state: name, reads: state.reads, mutations: state.mutations, blocked: state.blocked,
      streams: await streams(page), ...browserState, ...layout }
    const stateJson = JSON.stringify(metadata, null, 2)
    const axeJson = JSON.stringify(axe, null, 2)
    const manifest = { algorithm: 'sha256', files: {
      [`${name}.png`]: sha256(screenshot),
      [`${name}-capture-state.json`]: sha256(stateJson),
      [`${name}-axe-capture-state.json`]: sha256(axeJson),
    } }
    await writeFile(`${directory}/${name}-capture-state.json`, stateJson)
    await writeFile(`${directory}/${name}-axe-capture-state.json`, axeJson)
    await writeFile(`${directory}/${name}-sha256.json`, JSON.stringify(manifest, null, 2))
    await info.attach(`${name}-state`, { path: `${directory}/${name}-capture-state.json`, contentType: 'application/json' })
    await info.attach(`${name}-axe`, { path: `${directory}/${name}-axe-capture-state.json`, contentType: 'application/json' })
    await info.attach(`${name}-sha256`, { path: `${directory}/${name}-sha256.json`, contentType: 'application/json' })
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
}

for (const width of responsiveWidths) {
  for (const scale of [1, 2]) {
    test(`${width}px / ${scale * 100}% 根字号断流恢复保留可读上下文${scale === 2 ? '（长镜像引用）' : ''}`, async ({ page }, info) => {
      await page.setViewportSize({ width, height: 900 })
      const reference = scale === 2 ? longReference : 'nginx:latest'
      const snapshot = (fields: Record<string, unknown> = {}) => job({ reference, ...fields })
      const state = await fixture(page, [snapshot()])
      await openDownloads(page)
      const rootFontSize = await page.locator('html').evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))
      await page.addStyleTag({ content: `html { font-size: ${rootFontSize * scale}px !important; }` })
      await expect(page.locator('html')).toHaveCSS('font-size', `${rootFontSize * scale}px`)
      const row = rowOf(page, reference)
      await expect.poll(() => streams(page)).toEqual({ created: 1, active: 1 })
      await emit(page, snapshot())
      await expect(row).toContainText('/s')
      await emit(page)
      await expectInterrupted(page, reference)
      await expectRecoveryInView(page, reference, scale === 1, scale === 2)
      await capture(page, info, `interrupted-${width}-font-${scale * 100}`, state)
      const beforeRecovery = state.reads
      state.fail()
      await recover(page, reference).click()
      await expect(workspace(page).getByRole('alert')).toContainText('下载任务刷新失败')
      await expect(workspace(page).getByRole('alert')).toContainText('上次成功获取')
      expect(state.reads).toBe(beforeRecovery + 1)
      await expectInterrupted(page, reference)
      await expectDownloadLayout(page, reference, true, scale === 2)
      const reload = page.getByRole('button', { name: '重新加载下载任务', exact: true })
      await reload.scrollIntoViewIfNeeded()
      await expect(reload).toBeInViewport({ ratio: 1 })
      await expectDownloadLayout(page, reference, true, scale === 2)
      await capture(page, info, `cached-read-failure-${width}-font-${scale * 100}`, state)
      state.succeed([snapshot({ progress: 55, downloadedBytes: 55, updatedAt: '2026-10-13T08:01:00Z' })])
      const beforeReload = state.reads
      await reload.click()
      await expect(row.locator('.job-observation-warning')).toHaveCount(0)
      await expect(row).toContainText('55%')
      await expect(row).not.toContainText('/s')
      expect(state.reads).toBe(beforeReload + 1)
      await expectDownloadLayout(page, reference, false, scale === 2)
      await expect.poll(() => streams(page)).toEqual({ created: 2, active: 1 })
      await emit(page, snapshot({ progress: 60, downloadedBytes: 60, updatedAt: '2026-10-13T08:02:00Z' }))
      await expect(row).toContainText('/s')
      expect(state.mutations).toEqual([])
      expect(state.blocked).toEqual([])
    })
  }
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
