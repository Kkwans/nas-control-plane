import { Buffer } from 'node:buffer'
import { expect, test, type Download, type Locator, type Page, type Request, type TestInfo } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

import type { LogEntry } from '../src/api/control'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

const collectedAt = '2026-10-13T00:00:00.000Z'
const exportName = /^导出当前页（\d+ 条）$/
const draftHint = '搜索条件尚未应用，请按回车或刷新后导出'
const metadataKeys = ['formatVersion', 'scope', 'exportedAt', 'page', 'pageSize', 'entryCount', 'loadedCount', 'order', 'source']

type FixtureEntry = LogEntry & { privateFixture?: string }
type ExportRow = Pick<LogEntry, 'timestamp' | 'source' | 'unit' | 'level' | 'stream' | 'message'>
type ExportMetadata = {
  formatVersion: number
  scope: string
  exportedAt: string
  page: number
  pageSize: number
  entryCount: number
  loadedCount: number
  order: string
  source: LogEntry['source']
}
type DownloadProbe = {
  created: { url: string; type: string; size: number }[]
  revoked: string[]
  clicks: number
  afterAnchorClick?: () => void
}
type ProbeWindow = Window & { logDownloadProbe: DownloadProbe; logStreams: EventSource[] }

function barrier() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => { release = resolve })
  return { promise, release }
}

function entry(index: number, fields: Partial<FixtureEntry> = {}): FixtureEntry {
  return {
    id: `private-cursor-${index}`,
    timestamp: `2026-10-13T00:00:${String(index).padStart(2, '0')}.123456789Z`,
    source: 'system',
    unit: 'fixture.service',
    level: 'info',
    message: `日志 ${index}：中文与 emoji 🐈`,
    rawMessage: '原始日志不应出现在导出文件中',
    privateFixture: '额外接口字段不应出现在导出文件中',
    ...fields,
  }
}

function whitelist(row: FixtureEntry): ExportRow {
  return {
    timestamp: row.timestamp,
    source: row.source,
    unit: row.unit,
    level: row.level,
    ...(row.stream ? { stream: row.stream } : {}),
    message: row.message,
  }
}

function exportButton(page: Page) {
  return page.getByRole('button', { name: exportName })
}

async function installExportFixture(page: Page, pageSize = 5) {
  await installMockApi(page)
  await page.route('**/api/v1/preferences/lists/logs.events', (route) => route.fulfill({ json: {
    listKey: 'logs.events', pageSize, sortKey: '', sortDirection: 'asc',
  } }))
  // 保留原生 Blob、URL 和 anchor 行为，观察资源生命周期但不伪造下载事件。
  await page.addInitScript(() => {
    const host = window as unknown as ProbeWindow
    host.logDownloadProbe = { created: [], revoked: [], clicks: 0 }
    host.logStreams = []
    const originalCreate = URL.createObjectURL.bind(URL)
    const originalRevoke = URL.revokeObjectURL.bind(URL)
    const originalClick = HTMLAnchorElement.prototype.click
    const OriginalEventSource = window.EventSource
    URL.createObjectURL = (object) => {
      const url = originalCreate(object)
      if (object instanceof Blob) host.logDownloadProbe.created.push({ url, type: object.type, size: object.size })
      return url
    }
    URL.revokeObjectURL = (url) => {
      host.logDownloadProbe.revoked.push(url)
      originalRevoke(url)
    }
    HTMLAnchorElement.prototype.click = function () {
      if (!this.download.startsWith('ncp-logs-')) return originalClick.call(this)
      host.logDownloadProbe.clicks += 1
      originalClick.call(this)
      host.logDownloadProbe.afterAnchorClick?.()
    }
    window.EventSource = class extends OriginalEventSource {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options)
        if (String(url).includes('/api/v1/logs/events')) host.logStreams.push(this)
      }
    }
  })
}

async function mockLogs(page: Page, entries: FixtureEntry[], nextCursor = '') {
  await page.route('**/api/v1/logs?**', (route) => route.fulfill({ json: { collectedAt, entries, nextCursor } }))
}

async function openLogs(page: Page) {
  await page.goto('/logs', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: '日志中心', exact: true })).toBeVisible()
  await waitForPageReady(page)
}

async function readExport(download: Download) {
  expect(await download.failure()).toBeNull()
  const stream = await download.createReadStream()
  expect(stream, '浏览器必须生成实际可读取的下载文件').not.toBeNull()
  const chunks: Buffer[] = []
  for await (const chunk of stream!) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))
  const bytes = Buffer.concat(chunks)
  expect(bytes.length).toBeGreaterThan(0)
  const content = bytes.toString('utf8')
  expect(content.startsWith('\uFEFF')).toBe(false)
  expect(content.endsWith('\n')).toBe(true)
  expect(content.endsWith('\n\n')).toBe(false)
  expect(content).not.toContain('\r')
  const lines = content.slice(0, -1).split('\n')
  const metadata = JSON.parse(lines[0]!) as ExportMetadata
  const rows = lines.slice(1).map((line) => JSON.parse(line) as ExportRow)
  expect(Object.keys(metadata)).toEqual(metadataKeys)
  expect(metadata).toMatchObject({ formatVersion: 1, scope: 'current-page', order: 'newest-first', entryCount: rows.length })
  expect(metadata.exportedAt).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/)
  expect(download.suggestedFilename()).toBe(
    `ncp-logs-${metadata.source}-page-${metadata.page}-${metadata.exportedAt.replace(/[-:.]/g, '')}.txt`,
  )
  for (const row of rows) {
    expect(Object.keys(row)).toEqual(['timestamp', 'source', 'unit', 'level', ...(row.stream ? ['stream'] : []), 'message'])
  }
  return { bytes, content, metadata, rows }
}

async function exportCurrentPage(page: Page, activate: (button: Locator) => Promise<void> = (button) => button.click()) {
  const requests: string[] = []
  const observe = (request: Request) => {
    if (new URL(request.url()).pathname.startsWith('/api/')) requests.push(request.url())
  }
  await expect(exportButton(page)).toBeEnabled()
  const before = await page.evaluate(() => (window as unknown as ProbeWindow).logDownloadProbe.created.length)
  page.on('request', observe)
  try {
    const downloading = page.waitForEvent('download')
    await activate(exportButton(page))
    const result = await readExport(await downloading)
    await expect.poll(() => page.evaluate(() => {
      const probe = (window as unknown as ProbeWindow).logDownloadProbe
      return probe.created.length === probe.revoked.length
    })).toBe(true)
    const probe = await page.evaluate(() => (window as unknown as ProbeWindow).logDownloadProbe)
    expect(probe.created).toHaveLength(before + 1)
    expect(probe.created.at(-1)).toMatchObject({ type: 'text/plain;charset=utf-8', size: result.bytes.length })
    expect(probe.revoked.filter((url) => url === probe.created.at(-1)!.url)).toHaveLength(1)
    await expect(page.locator('a[download^="ncp-logs-"]')).toHaveCount(0)
    expect(requests, '导出不能请求 API、自动加载历史或重新连接日志流').toEqual([])
    return result
  } finally {
    page.off('request', observe)
  }
}

async function captureExport(page: Page, testInfo: TestInfo, state: string) {
  if (process.env.NCP_CAPTURE_UI !== '1') return
  const name = `fixture-log-export-${state}.png`
  const path = testInfo.outputPath('ui-captures', name)
  await page.screenshot({ path, fullPage: true, animations: 'disabled' })
  await testInfo.attach(name, { path, contentType: 'image/png' })
}

async function expectNoDownload(page: Page) {
  await expect.poll(() => page.evaluate(() => (window as unknown as ProbeWindow).logDownloadProbe.clicks)).toBe(0)
  expect(await page.evaluate(() => (window as unknown as ProbeWindow).logDownloadProbe.created)).toEqual([])
}

async function selectAgent(page: Page) {
  const select = page.locator('.log-filter-field--source .el-select__wrapper')
  await select.evaluate((element) => element.scrollIntoView({ block: 'center', behavior: 'instant' }))
  await select.click()
  await page.getByRole('option', { name: 'NCP Agent', exact: true }).click()
}

test('导出真实 UTF-8 文件，只含当前页的白名单字段并保留显示顺序', async ({ page }) => {
  await installExportFixture(page)
  const rows = Array.from({ length: 7 }, (_, index) => entry(index + 1))
  rows[6] = entry(7, { message: '中文 🐈\r\n下一行\t"引号"\\路径\u001b[31m\u0000\u2028\u2029\u202e<img src=x> =SUM(A1)' })
  // 同一时间的两条记录沿用界面顺序，不能在导出时按 id 或消息再次排序。
  rows[5] = entry(6, { timestamp: rows[6]!.timestamp, message: '相同时间的另一条记录' })
  await mockLogs(page, rows, 'private-next-cursor')
  await openLogs(page)
  await expect(exportButton(page)).toHaveAccessibleName('导出当前页（5 条）')
  const displayed = await page.locator('.log-message').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('title')))
  const result = await exportCurrentPage(page)
  expect(result.metadata).toMatchObject({ source: 'system', page: 1, pageSize: 5, entryCount: 5, loadedCount: 7 })
  expect(result.rows.map((row) => row.message)).toEqual(displayed)
  expect(result.rows).toEqual(displayed.map((message) => whitelist(rows.find((row) => row.message === message)!)))
  expect(result.content).toContain('中文 🐈')
  expect(result.content).toContain('\\u2028')
  expect(result.content).toContain('\\u2029')
  expect(result.content).toMatch(/\\u202e/i)
  for (const control of ['\u0000', '\u001b', '\u2028', '\u2029', '\u202e']) expect(result.content).not.toContain(control)
  expect(result.content).not.toContain('private-cursor')
  expect(result.content).not.toContain('private-next-cursor')
  expect(result.content).not.toContain('原始日志不应')
  expect(result.content).not.toContain('额外接口字段不应')
  await expect(page.getByText('已发起 5 条日志下载', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '下一页', exact: true }).click()
  const lastPage = await exportCurrentPage(page)
  expect(lastPage.metadata).toMatchObject({ page: 2, pageSize: 5, entryCount: 2, loadedCount: 7 })
  expect(lastPage.rows).toEqual([whitelist(rows[1]!), whitelist(rows[0]!)])
})

test('容器导出保留 stdout 和 stderr，文件名不包含容器名或日志内容', async ({ page }) => {
  await installExportFixture(page)
  const rows = [
    entry(2, { source: 'container', unit: '../../private-container-name', stream: 'stderr', message: '容器标准错误 🐈' }),
    entry(1, { source: 'container', unit: '../../private-container-name', stream: 'stdout', message: '容器标准输出 中文' }),
  ]
  await page.route('**/api/v1/logs?**', (route) => {
    const container = new URL(route.request().url()).searchParams.get('source') === 'container'
    return route.fulfill({ json: { collectedAt, entries: container ? rows : [entry(1)], nextCursor: '' } })
  })
  await openLogs(page)
  await page.locator('.log-filter-field--source .el-select__wrapper').click()
  await page.getByRole('option', { name: 'Docker 容器日志', exact: true }).click()
  await expect(exportButton(page)).toBeDisabled()
  await page.locator('.log-filter-field--container .el-select__wrapper').click()
  await page.getByRole('option', { name: 'media-api', exact: true }).click()
  await expect(page.getByText('容器标准错误 🐈', { exact: true })).toBeVisible()
  const result = await exportCurrentPage(page)
  expect(result.metadata).toMatchObject({ source: 'container', entryCount: 2, loadedCount: 2 })
  expect(result.rows).toEqual(rows.map(whitelist))
})

test('加载更多期间禁用，完成后仍只导出当前页而非全部已加载历史', async ({ page }) => {
  await installExportFixture(page)
  const more = barrier()
  const cursors: string[] = []
  await page.route('**/api/v1/logs?**', async (route) => {
    const cursor = new URL(route.request().url()).searchParams.get('cursor') || ''
    cursors.push(cursor)
    if (cursor) await more.promise
    await route.fulfill({ json: {
      collectedAt,
      entries: cursor ? [entry(2), entry(1)] : [entry(7), entry(6), entry(5), entry(4), entry(3)],
      nextCursor: cursor ? '' : 'older',
    } })
  })
  await openLogs(page)
  await page.getByRole('button', { name: '加载更多结果', exact: true }).click()
  try {
    await expect.poll(() => cursors).toEqual(['', 'older'])
    await expect(exportButton(page)).toBeDisabled()
    await exportButton(page).evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await expectNoDownload(page)
  } finally { more.release() }
  await expect(page.getByText(/当前已载入 7 条/)).toBeVisible()
  const result = await exportCurrentPage(page)
  expect(result.metadata).toMatchObject({ page: 1, entryCount: 5, loadedCount: 7 })
  expect(result.rows.map((row) => row.message)).toEqual([7, 6, 5, 4, 3].map((index) => entry(index).message))
  expect(cursors).toEqual(['', 'older'])
})

for (const apply of ['回车', '刷新']) {
  test(`未应用的搜索草稿禁用导出，${apply}应用后只导出新结果`, async ({ page }) => {
    await installExportFixture(page)
    const queries: string[] = []
    await page.route('**/api/v1/logs?**', (route) => {
      const query = new URL(route.request().url()).searchParams.get('query') || ''
      queries.push(query)
      return route.fulfill({ json: { collectedAt, entries: [entry(1, { message: query ? '新查询匹配结果' : '旧查询结果' })], nextCursor: '' } })
    })
    await openLogs(page)
    await expect(exportButton(page)).toBeEnabled()
    const search = page.getByRole('textbox', { name: '搜索日志消息或服务', exact: true })
    await search.fill('  private-query-keyword  ')
    await expect(exportButton(page)).toBeDisabled()
    await expect(page.getByText(draftHint, { exact: true })).toBeVisible()
    await exportButton(page).evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await expectNoDownload(page)
    expect(queries).toEqual([''])
    if (apply === '回车') await search.press('Enter')
    else await page.getByRole('button', { name: '刷新', exact: true }).click()
    await expect(page.getByText('新查询匹配结果', { exact: true })).toBeVisible()
    await expect(page.getByText(draftHint, { exact: true })).toHaveCount(0)
    const result = await exportCurrentPage(page)
    expect(result.rows.map((row) => row.message)).toEqual(['新查询匹配结果'])
    expect(result.content).not.toContain('旧查询结果')
    expect(result.content).not.toContain('private-query-keyword')
    expect(queries).toEqual(['', 'private-query-keyword'])
    // 首尾空白变化不构成新的未应用搜索。
    await search.fill('private-query-keyword')
    await expect(exportButton(page)).toBeEnabled()
  })
}

test('首次读取、同筛选刷新和读取失败都不能导出旧结果；重试成功后恢复', async ({ page }) => {
  await installExportFixture(page)
  const initial = barrier()
  const refresh = barrier()
  let reads = 0
  await page.route('**/api/v1/logs?**', async (route) => {
    const read = ++reads
    if (read === 1) await initial.promise
    if (read === 2) await refresh.promise
    await route.fulfill(read === 2
      ? { status: 503, json: { code: 'LOGS_UNAVAILABLE', message: 'fixture read failed' } }
      : { json: { collectedAt, entries: [entry(read)], nextCursor: '' } })
  })
  await openLogs(page)
  try {
    await expect(exportButton(page)).toBeDisabled()
    await expect(exportButton(page)).toHaveAccessibleName('导出当前页（0 条）')
    await exportButton(page).evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await expectNoDownload(page)
  } finally { initial.release() }
  await expect(exportButton(page)).toBeEnabled()
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  try {
    await expect.poll(() => reads).toBe(2)
    await expect(page.getByText(entry(1).message, { exact: true })).toBeVisible()
    await expect(exportButton(page)).toBeDisabled()
    await exportButton(page).evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await expectNoDownload(page)
  } finally { refresh.release() }
  await expect(page.getByText('日志读取失败', { exact: true })).toBeVisible()
  await expect(page.getByText(entry(1).message, { exact: true })).toBeVisible()
  await expect(exportButton(page)).toBeDisabled()
  await exportButton(page).evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await expectNoDownload(page)
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.getByText(entry(3).message, { exact: true })).toBeVisible()
  const result = await exportCurrentPage(page)
  expect(result.rows).toEqual([whitelist(entry(3))])
  expect(reads).toBe(3)
})

test('空结果显示原因且不会创建下载资源', async ({ page }) => {
  await installExportFixture(page)
  await mockLogs(page, [])
  await openLogs(page)
  await expect(page.getByText('当前筛选条件下没有日志记录', { exact: true })).toBeVisible()
  await expect(exportButton(page)).toHaveAccessibleName('导出当前页（0 条）')
  await expect(exportButton(page)).toBeDisabled()
  await exportButton(page).evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await expectNoDownload(page)
})

test('旧来源分页迟到不污染新来源导出', async ({ page }) => {
  await installExportFixture(page)
  const oldPage = barrier()
  let waiting = false
  let settled = false
  await page.route('**/api/v1/logs?**', async (route) => {
    const url = new URL(route.request().url())
    const source = url.searchParams.get('source') as LogEntry['source']
    const cursor = url.searchParams.get('cursor') || ''
    if (source === 'system' && cursor) { waiting = true; await oldPage.promise }
    await route.fulfill({ json: {
      collectedAt,
      entries: [entry(cursor ? 2 : 1, { source, message: `${source} ${cursor ? '迟到旧记录' : '当前记录'}` })],
      nextCursor: cursor ? '' : 'more',
    } }).catch(() => { /* 来源切换取消旧分页，允许路由响应被丢弃。 */ })
    if (source === 'system' && cursor) settled = true
  })
  await openLogs(page)
  await page.getByRole('button', { name: '加载更多结果', exact: true }).click()
  try {
    await expect.poll(() => waiting).toBe(true)
    await selectAgent(page)
    await expect(page.getByText('agent 当前记录', { exact: true })).toBeVisible()
  } finally { oldPage.release() }
  await expect.poll(() => settled).toBe(true)
  const result = await exportCurrentPage(page)
  expect(result.metadata).toMatchObject({ source: 'agent', loadedCount: 1 })
  expect(result.rows.map((row) => row.message)).toEqual(['agent 当前记录'])
  expect(result.content).not.toContain('system')
})

test('键盘 Enter 和 Space 可下载，同一调用栈的重复点击只交接一次，清理后可再导出', async ({ page }) => {
  await installExportFixture(page)
  await mockLogs(page, [entry(1)])
  await openLogs(page)
  await exportButton(page).focus()
  await exportCurrentPage(page, (button) => button.press('Enter'))
  await exportCurrentPage(page, (button) => button.press('Space'))
  await exportCurrentPage(page, (button) => button.evaluate((element) => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  }))
  expect(await page.evaluate(() => (window as unknown as ProbeWindow).logDownloadProbe.clicks)).toBe(3)
  await exportCurrentPage(page)
  expect(await page.evaluate(() => (window as unknown as ProbeWindow).logDownloadProbe.clicks)).toBe(4)
})

test('实时新记录在下载交接后到达，不改变已冻结文件或重启跟随', async ({ page }) => {
  await installExportFixture(page)
  await mockLogs(page, [entry(1)])
  const streamHeld = barrier()
  let streamRequested = false
  await page.route('**/api/v1/logs/events?**', async (route) => {
    streamRequested = true
    await streamHeld.promise
    await route.fulfill({ headers: { 'content-type': 'text/event-stream' }, body: '' }).catch(() => { /* 页面关闭会取消流。 */ })
  })
  await openLogs(page)
  await page.locator('.follow-switch .el-switch').click()
  try {
    await expect.poll(() => streamRequested).toBe(true)
    await expect.poll(() => page.evaluate(() => (window as unknown as ProbeWindow).logStreams.length)).toBe(1)
    await page.evaluate((snapshot) => {
      const host = window as unknown as ProbeWindow
      host.logStreams[0]!.dispatchEvent(new MessageEvent('logs', { data: JSON.stringify(snapshot) }))
      host.logDownloadProbe.afterAnchorClick = () => {
        host.logDownloadProbe.afterAnchorClick = undefined
        host.logStreams[0]!.dispatchEvent(new MessageEvent('logs', { data: JSON.stringify({
          ...snapshot,
          entries: [{ ...snapshot.entries[0], id: 'live-new', timestamp: '2026-10-13T00:01:00Z', message: '点击后的实时新记录' }],
        }) }))
      }
    }, { collectedAt, entries: [entry(1)], nextCursor: '' })
    await expect(page.getByRole('status', { name: '实时日志已连接', exact: true })).toBeVisible()
    const result = await exportCurrentPage(page)
    expect(result.metadata).toMatchObject({ entryCount: 1, loadedCount: 1 })
    expect(result.rows).toEqual([whitelist(entry(1))])
    await expect(page.getByText('点击后的实时新记录', { exact: true })).toBeVisible()
    expect(await page.evaluate(() => (window as unknown as ProbeWindow).logStreams.length)).toBe(1)
    await expect(page.getByRole('status', { name: '实时日志已连接', exact: true })).toBeVisible()
  } finally { streamHeld.release() }
})

test('离开页面后旧按钮和迟到读取不能下载，前进后退只使用当前页面数据', async ({ page }) => {
  await installExportFixture(page)
  const delayed = barrier()
  let reads = 0
  let settled = false
  await page.route('**/api/v1/logs?**', async (route) => {
    const read = ++reads
    if (read === 2) await delayed.promise
    await route.fulfill({ json: { collectedAt, entries: [entry(read)], nextCursor: '' } })
      .catch(() => { /* 离开页面取消旧读取。 */ })
    if (read === 2) settled = true
  })
  await openLogs(page)
  const oldButton = await exportButton(page).elementHandle()
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  try {
    await expect.poll(() => reads).toBe(2)
    await page.locator('.navigation a[href="/settings"]').click()
    await expect(page.getByRole('heading', { name: '设置', exact: true })).toBeVisible()
    await oldButton!.evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  } finally { delayed.release() }
  await expect.poll(() => settled).toBe(true)
  await expectNoDownload(page)
  await page.goBack()
  await expect(page.getByText(entry(3).message, { exact: true })).toBeVisible()
  const result = await exportCurrentPage(page)
  expect(result.rows).toEqual([whitelist(entry(3))])
  await page.goForward()
  await expect(page.getByRole('heading', { name: '设置', exact: true })).toBeVisible()
  expect(await page.evaluate(() => (window as unknown as ProbeWindow).logDownloadProbe.clicks)).toBe(1)
})

test('退出会话立即阻止仍在页面上的旧日志导出，服务端退出完成前也不能交接文件', async ({ page }) => {
  await installExportFixture(page)
  await mockLogs(page, [entry(1)])
  const logout = barrier()
  let requested = false
  await page.route('**/api/v1/auth/logout', async (route) => {
    requested = true
    await logout.promise
    await route.fulfill({ status: 204 })
  })
  await openLogs(page)
  await expect(exportButton(page)).toBeEnabled()
  const oldButton = await exportButton(page).elementHandle()
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  try {
    await expect.poll(() => requested).toBe(true)
    await expect(exportButton(page)).toBeDisabled()
    await oldButton!.evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await expectNoDownload(page)
  } finally { logout.release() }
  await expect(page.getByRole('heading', { name: '进入 NAS Control Plane', exact: true })).toBeVisible()
  await oldButton!.evaluate((button) => button.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await expectNoDownload(page)
})

for (const width of [390, 768, 1280, 1440]) {
  test(`导出控件在 ${width}px、200% 字体和减少动态效果下可操作且无严重无障碍问题`, async ({ page }, testInfo) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: 1000 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await installExportFixture(page)
    await mockLogs(page, [entry(1)])
    await openLogs(page)
    await expect(exportButton(page)).toBeEnabled()
    const layout = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
      viewportWidth: window.innerWidth,
    }))
    expect(layout.bodyWidth, '日志导出工具栏不能导致页面横向溢出').toBeLessThanOrEqual(layout.viewportWidth)
    expect(layout.mainWidth, '日志导出工具栏不能导致主区域横向溢出').toBeLessThanOrEqual(layout.viewportWidth)
    await captureExport(page, testInfo, `${width}px-100pct`)
    await page.evaluate(() => {
      const root = document.documentElement
      root.style.setProperty('font-size', `${parseFloat(getComputedStyle(root).fontSize) * 2}px`, 'important')
    })
    const enlargedLayout = await page.evaluate(() => ({
      bodyWidth: document.body.scrollWidth,
      mainWidth: document.querySelector('#app-main')?.scrollWidth ?? 0,
      viewportWidth: window.innerWidth,
    }))
    expect(enlargedLayout.bodyWidth, '200% 字体日志页不能横向溢出').toBeLessThanOrEqual(enlargedLayout.viewportWidth)
    expect(enlargedLayout.mainWidth, '200% 字体日志主区域不能横向溢出').toBeLessThanOrEqual(enlargedLayout.viewportWidth)
    const pageSizeLabel = page.locator('.page-size-control .el-select__placeholder')
    await expect(pageSizeLabel).toHaveText('5 条')
    expect(await pageSizeLabel.evaluate((node) => node.scrollWidth <= node.clientWidth + 1), '放大根字体后每页数量不能省略').toBe(true)
    const button = exportButton(page)
    await button.scrollIntoViewIfNeeded()
    await expect(button).toBeVisible()
    await expect(button).toHaveAccessibleName('导出当前页（1 条）')
    const bounds = await button.boundingBox()
    expect(bounds).not.toBeNull()
    expect(bounds!.height).toBeGreaterThanOrEqual(44)
    expect(bounds!.width).toBeGreaterThanOrEqual(44)
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1)
    await expect(button.locator('svg')).toHaveAttribute('aria-hidden', 'true')
    await captureExport(page, testInfo, `${width}px-200pct`)
    await button.focus()
    await page.keyboard.press('Tab')
    await page.keyboard.press('Shift+Tab')
    await expect(button).toBeFocused()
    expect(await button.evaluate((node) => {
      const style = getComputedStyle(node)
      return node.matches(':focus-visible') && ((style.outlineStyle !== 'none' && parseFloat(style.outlineWidth) > 0) || style.boxShadow !== 'none')
    })).toBe(true)
    await expect(page.locator('#log-export-hint')).toContainText('日志可能含敏感信息，分享前请检查。')
    const results = await new AxeBuilder({ page }).include('.logs-page').analyze()
    const blocking = results.violations.filter((violation) => violation.impact === 'critical' || violation.impact === 'serious')
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
    await exportCurrentPage(page, (control) => control.press('Enter'))
    const feedback = page.getByText('已发起 1 条日志下载', { exact: true })
    await expect(feedback).toBeVisible()
    expect(await feedback.evaluate((node) => Boolean(node.closest('[aria-live="polite"], [role="status"]')))).toBe(true)
  })
}
