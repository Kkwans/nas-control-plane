import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { summary, overviewDocker } from './mockApi'
import type { DockerProjectActionJobSnapshot } from '../src/api/dockerProjectActionJob'
import type { JobSnapshot } from '../src/api/system'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const containerId = 'd'.repeat(64), projectId = 'standalone', name = '回执验证项目'
const stamp = '2026-10-14T18:00:00.000Z'
const admission: JobSnapshot = { id: 'fixture-container-restart-job', type: 'docker-container-restart', reference: containerId, cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: stamp, updatedAt: stamp, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
type Mode = 'mismatch' | 'interrupted' | 'unknown'
function projectAdmission(mode: Mode): DockerProjectActionJobSnapshot {
  return { id: `fixture-project-restart-${mode}`, message: '任务已进入队列', progress: 0, createdAt: stamp, updatedAt: stamp, type: 'docker-project-restart', reference: 'standalone', cancellable: false, status: 'queued', artifactState: 'unknown', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
}
function projectReceipt(mode: 'mismatch' | 'interrupted'): DockerProjectActionJobSnapshot {
  return { ...projectAdmission(mode), status: mode === 'mismatch' ? 'failed' : 'interrupted', completedAt: stamp,
    errorCode: mode === 'mismatch' ? 'DOCKER_PROJECT_STATE_MISMATCH' : 'JOB_EXECUTION_INTERRUPTED',
    projectActionResult: { projectId, kind: 'standalone', action: 'restart', items: [{ containerId,
      outcome: mode === 'mismatch' ? 'state-mismatch' : 'unknown', state: mode === 'mismatch' ? 'stopped' : 'unknown',
      errorCode: mode === 'mismatch' ? 'DOCKER_CONTAINER_STATE_MISMATCH' : 'JOB_EXECUTION_INTERRUPTED' }] } }
}
const inventory = {
  collectedAt: stamp,
  engine: { ...overviewDocker.engine, containers: 1, containersRunning: 1, containersStopped: 0 },
  projects: [{ id: projectId, name, kind: 'standalone', workingDirectory: '', configFiles: [], containerCount: 1, runningCount: 1, state: 'running' }],
  containers: [{ ...overviewDocker.containers[0]!, id: containerId, name: 'fixture-web', projectId, projectName: name, state: 'running', status: 'Up', ports: [] }],
}
async function fixture(page: Page, standalone: boolean, origin: string) {
  // 浏览器时钟持续流动，避免 Vue 事件 invoker 的相同时间戳抑制后续点击。
  await page.clock.install({ time: new Date(stamp) })
  // 使用已打开的静默内存流，禁止通用初始快照替换本例目标。
  await page.addInitScript(() => {
    class SilentSource extends EventTarget {
      static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2
      readyState = 1; onopen: (() => void) | null = null; onerror: (() => void) | null = null
      constructor(readonly url: string) { super(); queueMicrotask(() => { this.onopen?.(); this.dispatchEvent(new Event('open')) }) }
      close() { this.readyState = 2 }
    }
    window.EventSource = SilentSource as unknown as typeof EventSource
  })
  const path = standalone ? `/api/v1/docker/projects/${projectId}/action-jobs/restart` : `/api/v1/docker/containers/${containerId}/action-jobs/restart`
  const requests: string[] = [], denied: string[] = [], errors: string[] = []
  const writes: Array<{ path: string; body: unknown }> = []
  let inventoryTime = Date.parse(stamp)
  let allowed: Mode | undefined
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs', { jobs: [] }],
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  page.on('pageerror', (error) => errors.push(error.message))
  await page.routeWebSocket('**', (socket) => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (url.origin !== origin) { denied.push(`HOST ${url.origin}`); return route.abort('blockedbyclient') }
    if (route.request().method() !== 'GET' || url.pathname.startsWith('/api/')) { denied.push(`${route.request().method()} ${url.pathname}`); return route.abort('blockedbyclient') }
    return route.fallback()
  })
  await page.route('**/api/**', async (route) => {
    const req = route.request(), url = new URL(req.url()), pathname = url.pathname
    if (url.origin !== origin) { denied.push(`HOST ${url.origin}`); return route.abort('blockedbyclient') }
    requests.push(`${req.method()} ${pathname}${url.search}`)
    if (url.search && !(req.method() === 'GET' && pathname === '/api/v1/jobs' && ['?type=docker-project-start', '?type=docker-project-stop', '?type=docker-project-restart'].includes(url.search))) { denied.push(`${req.method()} ${pathname}${url.search}`); return route.abort('blockedbyclient') }
    if (req.method() === 'GET' && pathname === '/api/v1/docker/inventory') {
      // 必须与页面校验使用同一时钟；CI 宿主时间会被视为未来快照并拒收。
      inventoryTime = Math.max(inventoryTime + 1, await page.evaluate(() => Date.now()))
      return route.fulfill({ json: { ...inventory, collectedAt: new Date(inventoryTime).toISOString() } })
    }
    if (req.method() === 'POST' && pathname === path && allowed) {
      const mode = allowed; allowed = undefined
      const body: unknown = req.postData() ? req.postDataJSON() : null
      writes.push({ path: pathname, body })
      expect(body).toEqual(standalone ? { containerIds: [containerId] } : null)
      if (mode === 'unknown') return route.abort('failed')
      if (!standalone) {
        reads.set(`/api/v1/jobs/${admission.id}`, { ...admission, status: 'failed', completedAt: stamp, errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH', error: '读回状态未达到目标' })
        return route.fulfill({ status: 202, json: admission })
      }
      reads.set(`/api/v1/jobs/${projectAdmission(mode).id}`, projectReceipt(mode))
      return route.fulfill({ status: 202, json: projectAdmission(mode) })
    }
    if (req.method() === 'GET' && reads.has(pathname)) return route.fulfill({ json: reads.get(pathname) })
    denied.push(`${req.method()} ${pathname}`)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return { requests, denied, errors, writes, allow(mode: Mode) { expect(allowed).toBeUndefined(); allowed = mode } }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')]))) }
}
async function geometry(page: Page, actionLabel: string) {
  return page.evaluate((label) => {
    const element = document.querySelector<HTMLElement>('.action-error')!, box = element.getBoundingClientRect()
    const range = document.createRange(); range.selectNodeContents(element.querySelector('span')!)
    const close = element.querySelector<HTMLButtonElement>('button')!, closeBox = close.getBoundingClientRect()
    const closeRange = document.createRange(); closeRange.selectNodeContents(close)
    const closeLines = [...closeRange.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
    const tooltips = [...document.querySelectorAll<HTMLElement>('[role="tooltip"]')].filter((node) => { const bounds = node.getBoundingClientRect(); return bounds.width > 0 && bounds.height > 0 && getComputedStyle(node).visibility !== 'hidden' && node.getAttribute('aria-hidden') !== 'true' }).map((node) => ({ text: node.textContent, bounds: node.getBoundingClientRect().toJSON(), inMain: Boolean(node.closest('main, [role="main"]')) }))
    const trigger = [...document.querySelectorAll<HTMLButtonElement>('button[aria-label]')].find((node) => node.getAttribute('aria-label') === label && node.getBoundingClientRect().width > 0)
    return { scrollX, scrollY, trigger: trigger?.getBoundingClientRect().toJSON(), width: innerWidth, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth,
      close: { bounds: closeBox.toJSON(), lineCount: closeLines.length, noWrap: getComputedStyle(close).whiteSpace === 'nowrap', textContained: closeLines.every((line) => line.left >= closeBox.left - 1 && line.right <= closeBox.right + 1) }, tooltips,
      alert: element.textContent, bounds: box.toJSON(), lines: [...range.getClientRects()].map((line) => line.toJSON()),
      textContained: [...range.getClientRects()].every((line) => line.left >= box.left - 1 && line.right <= box.right + 1), focus: document.activeElement?.getAttribute('aria-label') }
  }, actionLabel)
}
async function capture(page: Page, info: TestInfo, api: Fixture, state: string) {
  await expect(page.locator('.el-message')).toHaveCount(0)
  await page.evaluate(() => document.fonts.ready)
  // 桌面项目行可能已被保护面板推到首屏外；先滚到触发器，再固定此滚动位置采集。
  if (state.startsWith('project-') && page.viewportSize()!.width === 1440) {
    const trigger = page.getByRole('button', { name: `重启项目 ${name}`, exact: true }).filter({ visible: true })
    await trigger.hover()
    await expect(page.getByRole('tooltip').filter({ hasText: '重启项目' })).toBeVisible()
  } else await page.evaluate(() => scrollTo({ top: 0, left: 0, behavior: 'instant' }))
  if (state.startsWith('container-') || state === 'project-mismatch' || state === 'project-interrupted') {
    const panel = page.getByRole('region', { name: state.startsWith('project-') ? '项目操作防重复保护' : '单容器操作防重复保护' })
    await expect(panel).toBeVisible()
    await expect(panel).toContainText(containerId)
    const panelGeometry = async () => panel.evaluate((element, actionLabel) => {
      const bounds = element.getBoundingClientRect()
      const trigger = [...document.querySelectorAll<HTMLButtonElement>('button[aria-label]')].find(node => node.getAttribute('aria-label') === actionLabel && node.getBoundingClientRect().width > 0)
      const tooltips = [...document.querySelectorAll<HTMLElement>('[role="tooltip"]')].filter(node => { const box = node.getBoundingClientRect(); return box.width > 0 && box.height > 0 && getComputedStyle(node).visibility !== 'hidden' && node.getAttribute('aria-hidden') !== 'true' }).map(node => ({ bounds: node.getBoundingClientRect().toJSON(), inMain: Boolean(node.closest('main, [role="main"]')) }))
      return { scrollX, scrollY, bounds: bounds.toJSON(), trigger: trigger?.getBoundingClientRect().toJSON(), tooltips, width: innerWidth, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth,
        controls: [...element.querySelectorAll<HTMLElement>('button, summary')].map(control => {
          const box = control.getBoundingClientRect(), range = document.createRange(); range.selectNodeContents(control)
          return { bounds: box.toJSON(), textContained: [...range.getClientRects()].every(line => line.left >= box.left - 1 && line.right <= box.right + 1) }
        }), textContained: [...element.querySelectorAll<HTMLElement>('p, li')].every(node => { const box = node.getBoundingClientRect(); return box.left >= bounds.left - 1 && box.right <= bounds.right + 1 }) }
    }, `重启项目 ${name}`)
    const beforeCapture = await panelGeometry(), requestsBefore = [...api.requests]
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    const png = info.outputPath('ui-captures', `docker-action-outcome-${state}-${page.viewportSize()!.width}.png`)
    await page.screenshot({ path: png, fullPage: true, animations: 'disabled' })
    await info.attach(state, { path: png, contentType: 'image/png' })
    const afterCapture = await panelGeometry(), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await panelGeometry()
    const metadata = info.outputPath('ui-captures', `docker-action-outcome-${state}-${page.viewportSize()!.width}-axe-capture-state.json`)
    await writeFile(metadata, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, state, testTitle: info.title, beforeCapture, afterCapture, afterAxe, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), requestsBefore, requestsAfterAxe: [...api.requests], writes: api.writes, denied: api.denied, axe }, null, 2))
    await info.attach(`${state}-state`, { path: metadata, contentType: 'application/json' })
    expect(api.requests).toEqual(requestsBefore)
    for (const sample of [beforeCapture, afterCapture, afterAxe]) {
      expect(sample.scrollX).toBe(beforeCapture.scrollX); expect(sample.scrollY).toBe(beforeCapture.scrollY)
      expect(sample.scrollWidth).toBeLessThanOrEqual(sample.clientWidth + 1)
      expect(sample.textContained).toBe(true)
      expect(sample.tooltips.every(tooltip => tooltip.inMain)).toBe(true)
      if (state.startsWith('project-') && sample.width === 1440) {
        expect(sample.tooltips).toHaveLength(1); expect(sample.trigger).toBeDefined()
        const tip = sample.tooltips[0]!, trigger = sample.trigger!
        expect(trigger.top - tip.bounds.bottom).toBeGreaterThanOrEqual(0)
        expect(trigger.top - tip.bounds.bottom).toBeLessThanOrEqual(24)
        expect(Math.abs((trigger.left + trigger.right - tip.bounds.left - tip.bounds.right) / 2)).toBeLessThanOrEqual(4)
      }
      for (const control of sample.controls) { expect(control.bounds.height).toBeGreaterThanOrEqual(44); expect(control.textContained).toBe(true) }
    }
    expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
    expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
    return
  }
  const actionLabel = state.startsWith('project-') ? `重启项目 ${name}` : '重启容器'
  const before = await geometry(page, actionLabel), requestsBefore = [...api.requests]
  const prefix = `docker-action-outcome-${state}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: true, animations: 'disabled' })
  const after = await geometry(page, actionLabel), axe = await new AxeBuilder({ page }).analyze(), afterAxe = await geometry(page, actionLabel)
  const metadata = { ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, state, testTitle: info.title, screenshot: `${prefix}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), beforeCapture: before, afterCapture: after, afterAxe, requestsBefore, requestsAfterAxe: [...api.requests], writes: api.writes, denied: api.denied, axe }
  const statePath = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await info.attach(prefix, { path: png, contentType: 'image/png' }); await info.attach(`${prefix}-axe-state`, { path: statePath, contentType: 'application/json' })
  expect(api.requests).toEqual(requestsBefore); expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
  for (const sample of [before, after, afterAxe]) { expect(sample.scrollX).toBe(before.scrollX); expect(sample.scrollY).toBe(before.scrollY); expect(sample.scrollWidth).toBeLessThanOrEqual(sample.clientWidth + 1); expect(sample.textContained).toBe(true); expect(sample.close.bounds.width).toBeGreaterThanOrEqual(44); expect(sample.close.bounds.height).toBeGreaterThanOrEqual(44); expect(sample.close.lineCount).toBe(1); expect(sample.close.noWrap).toBe(true); expect(sample.close.textContained).toBe(true); expect(sample.tooltips.every((tooltip) => tooltip.inMain)).toBe(true); if (sample.width === 1440) {
    expect(sample.tooltips).toHaveLength(1)
    expect(sample.trigger).toBeDefined()
    const tip = sample.tooltips[0]!, trigger = sample.trigger!
    expect(trigger.top - tip.bounds.bottom).toBeGreaterThanOrEqual(0)
    expect(trigger.top - tip.bounds.bottom).toBeLessThanOrEqual(24)
    expect(Math.abs((trigger.left + trigger.right - tip.bounds.left - tip.bounds.right) / 2)).toBeLessThanOrEqual(4)
  } }
  expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
}
for (const width of [390, 1440]) for (const standalone of [false, true]) {
  test(`${width}px ${standalone ? '独立项目' : '单容器'} 读回未达目标与未知结果均不伪报完成或自动重发`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const api = await fixture(page, standalone, new URL(baseURL!).origin)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('heading', { name: 'Docker 管理', exact: true })).toBeVisible()
    if (!standalone) await page.getByRole('button', { name: '容器', exact: true }).click()
    const action = width === 390
      ? page.locator(standalone ? '.docker-mobile-list' : '.container-mobile-list').getByRole('button', { name: '重启', exact: true })
      : page.getByRole('button', { name: standalone ? `重启项目 ${name}` : '重启容器', exact: true }).filter({ visible: true })
    const modes: Mode[] = standalone ? ['mismatch', 'interrupted', 'unknown'] : ['mismatch', 'unknown']
    let journeyCompleted = false
    let ledgerError: unknown
    try {
      for (const [index, mode] of modes.entries()) {
        await expect(action).toBeEnabled()
        const readsBefore = api.requests.filter((request) => request === 'GET /api/v1/docker/inventory').length
        api.allow(mode)
        if (standalone) { await action.focus(); await expect(action).toBeFocused(); await page.keyboard.press('Enter') } else await action.click()
        const dialog = page.locator('.el-message-box')
        await expect(dialog).toBeVisible(); expect(api.writes).toHaveLength(index); await expect(action).toBeDisabled()
        await dialog.getByRole('button', { name: '确认重启', exact: true }).click(); await expect(dialog).toHaveCount(0)
        await expect(page.locator('.el-message--success')).toHaveCount(0)
        if (standalone) {
          const panel = page.getByRole('region', { name: '项目操作防重复保护' })
          await expect(action).toBeDisabled()
          if (mode === 'unknown') {
            await expect(panel).toContainText('接收结果未知，请勿重复提交')
            await expect(page.locator('.action-error')).toContainText('可能已经执行')
          } else {
            await expect(page.locator('.el-message--info')).toContainText('已接收，尚未确认完成')
            await expect(panel).toContainText(mode === 'mismatch' ? '该次调用回读与目标状态不符' : '执行结果未知，可能已执行')
            await expect(panel).toContainText(projectAdmission(mode).id)
            await expect.poll(() => api.requests.filter(request => request === 'GET /api/v1/docker/inventory').length).toBe(readsBefore + 1)
            expect(api.requests).toContain(`GET /api/v1/jobs/${projectAdmission(mode).id}`)
            await expect(page.locator('.action-error')).toHaveCount(0)
          }
        } else {
          const panel = page.getByRole('region', { name: '单容器操作防重复保护' })
          await expect(action).toBeDisabled()
          if (mode === 'mismatch') {
            await expect(page.locator('.el-message--info')).toContainText('已接收，尚未确认完成')
            await expect(panel).toContainText('DOCKER_CONTAINER_STATE_MISMATCH')
            await expect.poll(() => api.requests.filter((request) => request === 'GET /api/v1/docker/inventory').length).toBe(readsBefore + 1)
          } else {
            await expect(panel).toContainText('接收未知，勿重复提交')
            await expect(page.locator('.action-error')).toContainText('可能已经执行')
          }
        }
        expect(api.writes).toHaveLength(index + 1)
        // 覆盖默认五秒刷新周期，观察未知结果不会触发自动写入重试。
        if (standalone || mode === 'unknown') await page.waitForTimeout(6000)
        await capture(page, info, api, `${standalone ? 'project' : 'container'}-${mode}`)
        expect(api.writes).toHaveLength(index + 1)
        if (standalone && mode !== 'unknown') {
          const release = page.getByRole('button', { name: '确认重复执行风险并解除本会话保护', exact: true })
          await release.focus(); await expect(release).toBeFocused(); await page.keyboard.press('Enter')
          const confirmation = page.locator('.ncp-container-action-confirmation')
          await expect(confirmation).toContainText('重复执行风险')
          await confirmation.getByRole('button', { name: '保留保护', exact: true }).click()
          await expect(release).toBeFocused(); await expect(action).toBeDisabled()
          expect(api.writes).toHaveLength(index + 1)
          await page.keyboard.press('Enter')
          await confirmation.getByRole('button', { name: '确认风险并解除', exact: true }).click()
          await expect(action).toBeEnabled(); expect(api.writes).toHaveLength(index + 1)
        }
        if (!standalone && mode === 'mismatch') {
          await page.getByRole('button', { name: '解除本页防重复保护', exact: true }).click()
          await page.locator('.ncp-container-action-confirmation').getByRole('button', { name: '确认风险并解除', exact: true }).click()
          await expect(action).toBeEnabled()
          expect(api.writes).toHaveLength(index + 1)
        }
      }
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      expect(api.writes).toHaveLength(modes.length)
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      journeyCompleted = true
    } finally {
      try {
        await mkdir(info.outputPath('ui-captures'), { recursive: true })
        const ledger = info.outputPath('ui-captures', 'final-request-ledger-capture-state.json')
        await writeFile(ledger, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, journeyCompleted, requests: api.requests, writes: api.writes, denied: api.denied, errors: api.errors }, null, 2))
        await info.attach('final-request-ledger', { path: ledger, contentType: 'application/json' })
      } catch (artifactError) {
        ledgerError = artifactError
        info.annotations.push({ type: 'final-ledger-error', description: String(artifactError) })
      }
    }
    if (ledgerError) throw ledgerError
  })
}
