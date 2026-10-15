import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const id = 'media-api-id', name = 'media-api', detailPath = `/api/v1/docker/containers/${id}`
const collectedAt = '2026-08-24T02:00:00.000Z'
type Surface = 'list' | 'project-drawer'
type RequestRecord = { method: string; path: string; query: string }
type Cycle = { key: string; requests: RequestRecord[]; focus: unknown[]; restored: boolean; completed: boolean }
const hash = (data: string | Buffer) => createHash('sha256').update(data).digest('hex')
async function fixture(page: Page, origin: string) {
  await installMockApi(page, { manualRealtime: true })
  const requests: RequestRecord[] = [], denied: string[] = [], errors: string[] = []
  const allowed = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities', '/api/v1/system/summary',
    '/api/v1/docker/inventory', '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/jobs',
    ...['docker.projects', 'docker.containers', 'docker.images.local'].map((key) => `/api/v1/preferences/lists/${key}`),
  ])
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('request', (request) => {
    const url = new URL(request.url())
    requests.push({ method: request.method(), path: url.pathname, query: url.search })
  })
  await page.routeWebSocket('**', (socket) => { denied.push(`WS ${socket.url()}`); socket.close() })
  // Installed after the shared fixture: reject unknown API paths and every mutation
  // before they can reach its permissive fallback or any actual backend.
  await page.route('**/*', async (route) => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin || request.method() !== 'GET') {
      denied.push(`${request.method()} ${url.href}`)
      return route.abort('blockedbyclient')
    }
    if (!url.pathname.startsWith('/api/')) return route.fallback()
    if (url.pathname === detailPath && !url.search) return route.fulfill({ json: {
      id, name, image: 'media-api:latest', state: 'running', healthFailingStreak: 0, exitCode: 0, restartCount: 0,
      oomKilled: false, restartMaximumRetries: 0, autoRemove: false, privileged: false, readonlyRootfs: false,
      nanoCpus: 0, memoryBytes: 0, ports: [], mounts: [], networks: [{ name: `${id} network` }],
    } })
    if (url.pathname === `${detailPath}/logs` && url.search === '?tail=200') return route.fulfill({ json: {
      containerId: id, tail: 200, collectedAt, entries: [{ timestamp: collectedAt, stream: 'stdout', level: 'info', message: `${id} log entry` }],
    } })
    if (allowed.has(url.pathname)) return route.fallback()
    denied.push(`${request.method()} ${url.pathname}${url.search}`)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: 'Unexpected endpoint denied' } })
  })
  return { requests, denied, errors }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
const evidence = new WeakMap<Page, { api: Fixture; surface: Surface; cycles: Cycle[]; artifacts: { file: string; sha256: string }[] }>()
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), trackedDiffSha256: hash(git(['diff', 'HEAD', '--binary', '--', 'frontend'])), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, hash(await readFile(resolve(root, file)))]))) }
}
const basis = { fixtureOnly: true, nativeDockerAcceptance: false, gapBasis: 'source-inferred; no pre-fix browser failure claimed' }
const drawer = (page: Page) => page.locator('.container-detail-drawer')
function trigger(page: Page, surface: Surface) {
  if (surface === 'project-drawer') return page.locator('.project-drawer').getByRole('button', { name: `查看 ${name} 详情与日志`, exact: true })
  if (page.viewportSize()!.width === 390) return page.locator('.container-mobile-list .container-card').filter({ hasText: 'media-api:latest' }).getByRole('button', { name: '详情', exact: true })
  return page.locator('.container-row').filter({ hasText: 'media-api:latest' }).getByRole('button', { name: '查看容器详情与日志', exact: true })
}
async function geometry(page: Page) {
  return page.evaluate(() => {
    const active = document.activeElement, style = active ? getComputedStyle(active) : null
    const selectors = ['.container-detail-drawer', '.container-detail-drawer .drawer-heading', '.container-detail-drawer .el-drawer__close-btn', '.container-detail-drawer .facts-grid', '.container-detail-drawer .network-grid', '.container-detail-drawer .logs-block', '.project-drawer']
    return {
      viewport: { width: innerWidth, height: innerHeight }, root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      focus: { tag: active?.tagName, label: active?.getAttribute('aria-label'), text: active?.textContent, inDetails: Boolean(active?.closest('.container-detail-drawer')), inProject: Boolean(active?.closest('.project-drawer')), focusVisible: active?.matches(':focus-visible'), outlineWidth: style?.outlineWidth, outlineStyle: style?.outlineStyle },
      controls: selectors.flatMap((selector) => [...document.querySelectorAll<HTMLElement>(selector)].map((element) => {
        const bounds = element.getBoundingClientRect(), style = getComputedStyle(element)
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return { selector, bounds: bounds.toJSON(), text: element.textContent, clientWidth: element.clientWidth, scrollWidth: element.scrollWidth, visible: bounds.width > 0 && bounds.height > 0 && style.visibility !== 'hidden', centerHit: Boolean(hit && (hit === element || element.contains(hit))) }
      })).filter((control) => control.visible),
    }
  })
}
async function saveJson(page: Page, info: TestInfo, file: string, content: unknown) {
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const path = info.outputPath('ui-captures', file), data = JSON.stringify(content, null, 2)
  await writeFile(path, data)
  evidence.get(page)!.artifacts.push({ file, sha256: hash(data) })
  await info.attach(file, { path, contentType: 'application/json' })
}
async function capture(page: Page, info: TestInfo, key: string) {
  const saved = evidence.get(page)!, prefix = `container-details-${saved.surface}-${page.viewportSize()!.width}-${key}`
  await waitForPageReady(page)
  await page.evaluate(() => document.fonts.ready)
  const before = await geometry(page), requestsBefore = structuredClone(saved.api.requests)
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const file = `${prefix}.png`, path = info.outputPath('ui-captures', file)
  await page.screenshot({ path, fullPage: true, animations: 'disabled' })
  saved.artifacts.push({ file, sha256: hash(await readFile(path)) })
  await info.attach(prefix, { path, contentType: 'image/png' })
  // Persist screenshot metadata before axe, so even an interrupted scan retains evidence.
  const metadata = { ...basis, ...await provenance(), surface: saved.surface, key, testTitle: info.title, url: page.url(), screenshot: file, before, requestsBefore }
  await saveJson(page, info, `${prefix}-capture-state.json`, metadata)
  const axe = await new AxeBuilder({ page }).analyze(), after = await geometry(page)
  await saveJson(page, info, `${prefix}-axe-capture-state.json`, { ...metadata, axe, after, requestsAfter: saved.api.requests })
  expect(saved.api.requests).toEqual(requestsBefore)
  expect(after.focus).toEqual(before.focus)
  expect(after.root.scrollWidth).toBeLessThanOrEqual(after.root.clientWidth + 1)
  for (const control of after.controls.filter((item) => item.selector !== '.project-drawer')) {
    expect(control.bounds.left).toBeGreaterThanOrEqual(0)
    expect(control.bounds.right).toBeLessThanOrEqual(after.viewport.width + 1)
    expect(control.scrollWidth).toBeLessThanOrEqual(control.clientWidth + 1)
  }
  // Full-page axe is deliberately unfiltered, including existing drawer issues.
  expect.soft(axe.violations, JSON.stringify(axe.violations)).toEqual([])
}
test.afterEach(async ({ page }, info) => {
  const saved = evidence.get(page)
  if (!saved) return
  const requests = structuredClone(saved.api.requests)
  await saveJson(page, info, 'container-details-final-readonly-ledger-capture-state.json', {
    ...basis, ...await provenance(), surface: saved.surface, status: info.status, cycles: saved.cycles, artifacts: [...saved.artifacts],
    requests, requestsSha256: hash(JSON.stringify(requests)), mutations: requests.filter((entry) => entry.method !== 'GET'), denied: saved.api.denied, errors: saved.api.errors,
  })
  expect(requests.filter((entry) => entry.method !== 'GET')).toEqual([])
  expect(saved.api.denied).toEqual([])
  expect(saved.api.errors).toEqual([])
})
for (const width of [1440, 390]) for (const surface of ['list', 'project-drawer'] as const) {
  test(`${width}px ${surface} readonly container details native Enter Space Escape`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const api = await fixture(page, new URL(baseURL!).origin)
    const saved = { api, surface, cycles: [] as Cycle[], artifacts: [] as { file: string; sha256: string }[] }
    evidence.set(page, saved)
    await page.goto(surface === 'list' ? '/docker' : '/docker?project=media-stack', { waitUntil: 'domcontentloaded' })
    if (surface === 'list') await page.getByRole('button', { name: '容器', exact: true }).click()
    else await expect(page.locator('.project-drawer')).toBeVisible()
    for (const key of ['Enter', 'Space']) {
      const button = trigger(page, surface), before = api.requests.length
      const cycle: Cycle = { key, requests: [], focus: [], restored: false, completed: false }
      saved.cycles.push(cycle)
      try {
        await expect(button).toBeEnabled()
        await button.focus()
        await expect(button).toBeFocused()
        cycle.focus.push({ navigation: 'trigger', ...(await geometry(page)).focus })
        await page.keyboard.press(key)
        await expect(drawer(page)).toBeVisible()
        await expect(drawer(page).locator('.drawer-heading')).toContainText(name)
        await expect(drawer(page)).toContainText(`${id} network`)
        await expect(drawer(page)).toContainText(`${id} log entry`)
        await expect(drawer(page).locator('.drawer-error')).toHaveCount(0)
        await expect.poll(() => drawer(page).evaluate((element) => element.contains(document.activeElement))).toBe(true)
        for (const navigation of ['Tab', 'Shift+Tab']) {
          await page.keyboard.press(navigation)
          const sample = await geometry(page)
          cycle.focus.push({ navigation, ...sample.focus })
          expect(sample.focus.inDetails).toBe(true)
          expect(sample.focus.focusVisible).toBe(true)
        }
        if (surface === 'project-drawer') await expect(page.locator('.project-drawer')).toBeVisible()
        await capture(page, info, key)
        await page.keyboard.press('Escape')
        await expect(drawer(page)).not.toBeVisible()
        if (surface === 'project-drawer') await expect(page.locator('.project-drawer')).toBeVisible()
        await expect(button).toBeFocused()
        cycle.focus.push({ navigation: 'Escape', ...(await geometry(page)).focus })
        cycle.restored = true
        const reads = api.requests.slice(before).filter((entry) => entry.path.startsWith('/api/v1/docker/containers/'))
        expect(reads).toHaveLength(2)
        expect(reads).toEqual(expect.arrayContaining([
          { method: 'GET', path: detailPath, query: '' }, { method: 'GET', path: `${detailPath}/logs`, query: '?tail=200' },
        ]))
        expect(api.requests.filter((entry) => entry.method !== 'GET')).toEqual([])
        expect(api.denied).toEqual([])
        cycle.completed = true
      } finally { cycle.requests = structuredClone(api.requests.slice(before)) }
    }
  })
}
