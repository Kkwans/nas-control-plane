import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { installMockApi } from './mockApi'

// Static HTTP fixtures only: no real pull, cancellation, persistence or Docker acceptance.
// The outer route denies writes, external hosts, unknown APIs and real sockets.
test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const stamp = '2026-10-15T08:00:00Z'
const reference = 'registry.example.test/progress-fixture:latest'
const warning = '进度信息受资源限制，仅保留部分层或状态；数值不代表完整下载进度。'
const job = (fields: Record<string, unknown> = {}) => ({
  id: 'limited-progress-fixture', type: 'docker-image-pull', reference, status: 'running',
  artifactState: 'unknown', progress: 42, downloadedBytes: 44040192, totalBytes: 104857600,
  speedBytes: 1048576, layers: {}, message: '静态测试快照，未执行真实拉取',
  createdAt: stamp, updatedAt: stamp, cancellable: true, ...fields,
})
const scenarios = [
  { name: 'limited-running', fields: { progressLimited: true }, download: '下载中', task: '执行中', description: '' },
  { name: 'limited-completed', fields: { progressLimited: true, status: 'completed', artifactState: 'present', progress: 100, cancellable: false }, download: '已完成', task: '已完成', description: '' },
  { name: 'limited-pending', fields: { progressLimited: true, status: 'completed', persistenceStatus: 'pending', progress: 100, cancellable: false }, download: '结果保存中', task: '结果保存中', description: '执行结果正在保存，尚未确认最终结果。' },
  { name: 'limited-cancel-requested', fields: { progressLimited: true, cancellationStatus: 'requested' }, download: '取消中', task: '取消请求已接收', description: '取消请求已接收，远端执行可能仍在继续。' },
  { name: 'limited-cancel-unknown', fields: { progressLimited: true, status: 'interrupted', cancellationStatus: 'unknown', cancellable: false }, download: '结果未确认', task: '结果未确认', description: '执行结果未确认，请检查实际资源状态。' },
  { name: 'legacy-omitted', fields: {}, download: '下载中', task: '执行中', description: '' },
  { name: 'explicit-false', fields: { progressLimited: false }, download: '下载中', task: '执行中', description: '' },
] satisfies Array<{ name: string; fields: Record<string, unknown>; download: string; task: string; description: string }>
type Scenario = typeof scenarios[number]
const hash = (value: string | Buffer) => createHash('sha256').update(value).digest('hex')
const dialog = (page: Page) => page.getByRole('dialog', { name: '最近任务', exact: true })

async function fixture(page: Page, origin: string, snapshot: ReturnType<typeof job>) {
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  await installMockApi(page, { clock: false, manualRealtime: true })
  // Static evidence only: isolate the app's exact 15-second task-list refresh.
  // All other timers (including axe timers) keep running; this does not test polling.
  await page.addInitScript(() => {
    const host = window as typeof window & { __limitedProgressSuppressedPolls: number }
    host.__limitedProgressSuppressedPolls = 0
    const original = window.setTimeout.bind(window)
    window.setTimeout = ((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      if (delay === 15_000) return -(++host.__limitedProgressSuppressedPolls)
      return original(handler, delay, ...args)
    }) as typeof window.setTimeout
  })
  const requests: string[] = [], denied: string[] = [], writes: string[] = [], errors: string[] = []
  const captures: Array<{ file: string; sha256: string }> = []
  const reads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities', '/api/v1/system/summary',
    '/api/v1/docker/inventory', '/api/v1/docker/images', '/api/v1/sites', '/api/v1/sites/ignored',
    '/api/v1/preferences/lists/docker.projects', '/api/v1/preferences/lists/docker.containers',
    '/api/v1/preferences/lists/docker.images.local', '/api/v1/preferences/lists/docker.image-downloads',
  ])
  page.on('pageerror', error => errors.push(error.message))
  await page.routeWebSocket('**/*', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    const target = `${request.method()} ${url.pathname}${url.search}`
    if (url.pathname.startsWith('/api/')) requests.push(target)
    if (request.method() !== 'GET') writes.push(target)
    if (url.origin !== origin || request.method() !== 'GET') { denied.push(target); return route.abort('blockedbyclient') }
    if (url.pathname === '/api/v1/jobs' && ['', '?type=docker-image-pull'].includes(url.search)) return route.fulfill({ json: { jobs: [snapshot] } })
    if (reads.has(url.pathname + url.search)) return route.fallback()
    if (!url.search && (url.pathname === '/docker' || url.pathname === '/favicon.svg' || /^\/assets\/[A-Za-z0-9_.-]+$/.test(url.pathname))) return route.fallback()
    denied.push(target); return route.abort('blockedbyclient')
  })
  return { requests, denied, writes, errors, captures, snapshot, snapshotSha256: hash(JSON.stringify(snapshot)) }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async file => [file, hash(await readFile(resolve(root, file)))]))) }
}
async function measure(row: Locator) {
  return row.evaluate(element => {
    const box = element.getBoundingClientRect()
    const selectors = '.job-progress-warning,.job-lifecycle-description,.job-progress small,.job-state,.task-description,.task-progress-unknown,.task-state,.task-progress-row>span'
    return {
      pollingSuppressed: { delayMs: 15_000, count: (window as typeof window & { __limitedProgressSuppressedPolls: number }).__limitedProgressSuppressedPolls, realPollingVerified: false },
      viewport: { width: innerWidth, height: innerHeight },
      root: { clientWidth: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth, scrollLeft: document.documentElement.scrollLeft },
      row: { bounds: box.toJSON(), clientWidth: element.clientWidth, scrollWidth: element.scrollWidth, text: element.textContent },
      progressbars: [...element.querySelectorAll('progress,[role="progressbar"]')].map(bar => ({ value: bar.getAttribute('value') ?? bar.getAttribute('aria-valuenow') })),
      fields: [...element.querySelectorAll(selectors)].map(field => {
        const rect = field.getBoundingClientRect(), range = document.createRange(); range.selectNodeContents(field)
        const ancestors = []
        for (let parent = field.parentElement; parent; parent = parent.parentElement) {
          const style = getComputedStyle(parent)
          if (['hidden', 'clip', 'auto', 'scroll'].includes(style.overflowX) || ['hidden', 'clip', 'auto', 'scroll'].includes(style.overflowY)) {
            ancestors.push({ bounds: parent.getBoundingClientRect().toJSON(), overflowX: style.overflowX, overflowY: style.overflowY })
          }
        }
        return { text: field.textContent, bounds: rect.toJSON(), clientWidth: field.clientWidth, scrollWidth: field.scrollWidth, clientHeight: field.clientHeight, scrollHeight: field.scrollHeight, textOverflow: getComputedStyle(field).textOverflow, lines: [...range.getClientRects()].filter(line => line.width && line.height).map(line => line.toJSON()), ancestors }
      }),
    }
  })
}
async function checkContent(row: Locator, scenario: Scenario, surface: 'download' | 'task') {
  const limited = scenario.fields.progressLimited === true
  await expect(row.locator(surface === 'download' ? '.job-state' : '.task-state')).toHaveText(scenario[surface])
  await expect(row.getByText(warning, { exact: true })).toHaveCount(limited ? 1 : 0)
  await expect(row.getByText('完整进度未提供', { exact: true })).toHaveCount(limited ? 1 : 0)
  if (limited) {
    await expect(row.getByRole('progressbar')).toHaveCount(0)
    await expect(row).not.toContainText(/\d+(?:\.\d+)?\s*%|\d+(?:\.\d+)?\s*(?:[KMGT]?i?B)(?:\/s)?|\/s/)
    if (scenario.description) await expect(row.getByText(scenario.description, { exact: true })).toBeVisible()
    if (surface === 'download') await expect(row.locator('.job-speed')).toContainText('—')
    if (scenario.name === 'limited-completed') await expect(row).not.toContainText('结果未确认')
    if (scenario.name === 'limited-pending') await expect(row.getByRole('button', { name: '查看本地镜像', exact: true })).toHaveCount(0)
  } else {
    await expect(row.getByRole('progressbar')).toHaveCount(1)
    await expect(row).toContainText('42%')
    if (surface === 'download') await expect(row.locator('.job-progress')).toContainText('42 MB / 100 MB')
  }
}
async function capture(page: Page, info: TestInfo, api: Fixture, row: Locator, scenario: Scenario, surface: 'download' | 'task') {
  await page.evaluate(() => document.fonts.ready)
  // Only vertical scrolling is used. The checks below reject horizontal movement/overflow.
  const anchor = row.getByText(warning, { exact: true })
  if (scenario.fields.progressLimited === true) await anchor.scrollIntoViewIfNeeded()
  else await row.scrollIntoViewIfNeeded()
  const beforeCapture = await measure(row), requestsBefore = [...api.requests]
  const name = `job-progress-${scenario.name}-${surface}-${page.viewportSize()!.width}`
  const directory = info.outputPath('ui-captures')
  await mkdir(directory, { recursive: true })
  const png = `${directory}/${name}.png`
  const screenshot = await page.screenshot({ path: png, animations: 'disabled', fullPage: false })
  const afterCapture = await measure(row), requestsAfterCapture = [...api.requests]
  // Full, unfiltered axe: retain evidence before soft assertions, never waive violations.
  const axe = await new AxeBuilder({ page }).analyze(), afterAxe = await measure(row)
  const metadata = { ...await provenance(), fixtureOnly: true, actualPullExecuted: false, nativeDockerAcceptance: false,
    fixtureSource: 'frontend/e2e/job-progress-limited.spec.ts + frontend/e2e/mockApi.ts', fixtureSnapshot: api.snapshot, fixtureSnapshotSha256: api.snapshotSha256, scenario, surface,
    testTitle: info.title, project: info.project.name, retry: info.retry, screenshot: `${name}.png`, screenshotSha256: hash(screenshot),
    beforeCapture, afterCapture, afterAxe, requestsBefore, requestsAfterCapture, requestsAfterAxe: [...api.requests], writes: api.writes, denied: api.denied, errors: api.errors }
  await info.attach(name, { path: png, contentType: 'image/png' })
  api.captures.push({ file: `${name}.png`, sha256: hash(screenshot) })
  for (const [suffix, value] of [['capture-state', metadata], ['axe-capture-state', { ...metadata, axe }]] as const) {
    const file = `${name}-${suffix}.json`, path = `${directory}/${file}`, data = JSON.stringify(value, null, 2)
    await writeFile(path, data); api.captures.push({ file, sha256: hash(data) }); await info.attach(file, { path, contentType: 'application/json' })
  }
  expect.soft(axe.violations).toEqual([])
  expect.soft(api.requests).toEqual(requestsBefore)
  for (const frame of [beforeCapture, afterCapture, afterAxe]) {
    expect.soft(frame.row.text).toEqual(beforeCapture.row.text)
    expect.soft(frame.progressbars).toEqual(beforeCapture.progressbars)
    expect.soft(frame.root.scrollWidth).toBeLessThanOrEqual(frame.root.clientWidth + 1)
    expect.soft(frame.root.scrollLeft).toBe(0)
    expect.soft(frame.row.scrollWidth).toBeLessThanOrEqual(frame.row.clientWidth + 1)
    for (const field of frame.fields) {
      expect.soft(field.bounds.width, field.text ?? '').toBeGreaterThan(0)
      if (field.clientWidth) expect.soft(field.scrollWidth).toBeLessThanOrEqual(field.clientWidth + 1)
      if (field.clientHeight) expect.soft(field.scrollHeight).toBeLessThanOrEqual(field.clientHeight + 1)
      for (const line of field.lines) {
        expect.soft(line.left).toBeGreaterThanOrEqual(Math.max(0, frame.row.bounds.left) - 1)
        expect.soft(line.right).toBeLessThanOrEqual(Math.min(frame.viewport.width, frame.row.bounds.right) + 1)
        expect.soft(line.top).toBeGreaterThanOrEqual(field.bounds.top - 1)
        expect.soft(line.bottom).toBeLessThanOrEqual(field.bounds.bottom + 1)
        for (const parent of field.ancestors) {
          if (parent.overflowX !== 'visible') { expect.soft(line.left).toBeGreaterThanOrEqual(parent.bounds.left - 1); expect.soft(line.right).toBeLessThanOrEqual(parent.bounds.right + 1) }
          if (parent.overflowY !== 'visible') { expect.soft(line.top).toBeGreaterThanOrEqual(parent.bounds.top - 1); expect.soft(line.bottom).toBeLessThanOrEqual(parent.bounds.bottom + 1) }
        }
      }
    }
  }
  if (scenario.fields.progressLimited === true) await expect.soft(anchor).toBeInViewport({ ratio: 1 })
  await checkContent(row, scenario, surface)
}

for (const width of [390, 1440]) for (const scenario of scenarios) {
  test(`${width}px ${scenario.name}: download and task center preserve limited progress semantics`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    const api = await fixture(page, new URL(baseURL!).origin, job(scenario.fields))
    await page.setViewportSize({ width, height: 900 })
    try {
      await page.goto('/docker')
      await page.getByRole('button', { name: '镜像', exact: true }).click()
      await page.getByRole('button', { name: /^下载任务/ }).click()
      await expect(page.getByRole('button', { name: '刷新下载任务', exact: true })).toBeEnabled()
      const download = page.locator('.download-workspace').getByRole('article', { name: `${reference} 下载任务`, exact: true })
      await checkContent(download, scenario, 'download')
      await capture(page, info, api, download, scenario, 'download')
      await page.getByRole('button', { name: '任务', exact: true }).click()
      await expect(dialog(page).getByRole('button', { name: '刷新任务', exact: true })).toBeEnabled()
      const task = dialog(page).getByRole('article', { name: reference, exact: true })
      await checkContent(task, scenario, 'task')
      await capture(page, info, api, task, scenario, 'task')
      expect(api.requests).toContain('GET /api/v1/jobs?type=docker-image-pull')
      expect(api.requests).toContain('GET /api/v1/jobs')
    } finally {
      const path = info.outputPath('ui-captures', 'job-progress-final-request-ledger-capture-state.json')
      await mkdir(dirname(path), { recursive: true })
      await writeFile(path, JSON.stringify({ ...await provenance(), fixtureOnly: true, actualPullExecuted: false, linkedCaptures: api.captures, requestLedger: api.requests, writes: api.writes, denied: api.denied, errors: api.errors }, null, 2))
      await info.attach('job-progress-final-request-ledger', { path, contentType: 'application/json' })
      expect.soft(api.writes).toEqual([]); expect.soft(api.denied).toEqual([]); expect.soft(api.errors).toEqual([])
    }
  })
}
