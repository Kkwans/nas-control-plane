import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

test.use({ serviceWorkers: 'block', timezoneId: 'UTC', contextOptions: { reducedMotion: 'reduce' } })
const hash = (value: string | Buffer) => createHash('sha256').update(value).digest('hex')
const site = (id: string, name: string, state = 'running', hidden = false, category = '工具') => ({
  id, projectId: id, name, description: '筛选 fixture', iconUrl: '', category, state, hidden,
  primaryPort: 8080, ports: [8080], launchUrl: 'https://fixture.invalid/app',
  favorite: false, sortOrder: 0, lastVisitedAt: null, source: 'manual',
})
const catalog = [site('running', '工具运行'), site('stopped', '工具停止', 'stopped'), site('hidden', '工具隐藏', 'running', true), site('other', '媒体运行', 'running', false, '媒体')]
const ignored = [site('ignored', '工具忽略')]
const labels = ['运行中', '全部站点', '已隐藏', '已忽略']
// rows() 使用代码点排序以忽略分组显示顺序，独立期望也按相同顺序列出。
const expectedRows = [['媒体运行', '工具运行'], ['媒体运行', '工具停止', '工具运行'], ['工具隐藏'], ['工具忽略']]

async function fixture(page: Page, origin: string) {
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  await installMockApi(page, { clock: false, manualRealtime: true })
  const staticPaths = new Set<string>(), distHashes: Record<string, string> = {}
  async function visit(directory: string, prefix = '') {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const path = `${prefix}/${entry.name}`
      if (entry.isDirectory()) await visit(`${directory}/${entry.name}`, path)
      else if (entry.isFile()) { staticPaths.add(path); distHashes[path] = hash(await readFile(`${directory}/${entry.name}`)) }
    }
  }
  await visit('dist')
  const requests: Array<{ method: string; path: string; search: string }> = []
  const denied: string[] = [], errors: string[] = []
  const bootstrap = new Set(['/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/summary', '/api/v1/system/capabilities', '/api/v1/docker/inventory'])
  page.on('pageerror', error => errors.push(error.message))
  await page.routeWebSocket('**', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method()
    requests.push({ method, path: url.pathname, search: url.search })
    const block = () => { denied.push(`${method} ${url.href}`); return route.abort('blockedbyclient') }
    if (url.origin !== origin || method !== 'GET' || url.search) return block()
    if (url.pathname === '/api/v1/sites') return route.fulfill({ json: {
      collectedAt: '2026-10-13T12:00:00Z', sites: catalog,
      discovery: { status: 'complete', probeAvailable: true, candidateCount: 4, verifiedCount: 4, failedCount: 0, issues: [] },
    } })
    if (url.pathname === '/api/v1/sites/ignored') return route.fulfill({ json: ignored })
    if (url.pathname === '/api/v1/jobs') return route.fulfill({ json: { jobs: [] } })
    if (bootstrap.has(url.pathname)) return route.fallback()
    if (url.pathname === '/sites' || staticPaths.has(url.pathname)) return route.continue()
    return block()
  })
  return { requests, denied, errors, distHashes }
}
async function tabTo(page: Page, target: Locator) {
  for (let index = 0; index < 80; index++) {
    await page.keyboard.press('Tab')
    if (await target.evaluate(element => element === document.activeElement)) return
  }
  throw new Error('原生 Tab 未到达选中的站点状态筛选')
}
async function state(page: Page) {
  return page.locator('.workspace-filters').evaluate(element => ({
    text: element.textContent,
    groupScrollLeft: element.querySelector('[role="radiogroup"]')?.scrollLeft,
    controls: [...element.querySelectorAll<HTMLElement>('[role="radio"], [role="combobox"]')].map(control => {
      const rect = control.getBoundingClientRect(), style = getComputedStyle(control)
      const points = [[rect.left + rect.width / 2, rect.top + rect.height / 2], [rect.left + 3, rect.top + 3], [rect.right - 3, rect.top + 3], [rect.left + 3, rect.bottom - 3], [rect.right - 3, rect.bottom - 3]].map(([x, y]) => {
        const hit = document.elementFromPoint(x!, y!)
        return { x, y, hit: hit === control || (hit !== null && control.contains(hit)) }
      })
      const clips: Array<{ x: boolean; y: boolean; bounds: { left: number; right: number; top: number; bottom: number } }> = []
      for (let parent = control.parentElement; parent; parent = parent.parentElement) {
        if (parent === document.body || parent === document.documentElement) continue
        const css = getComputedStyle(parent)
        const x = /hidden|clip|auto|scroll/.test(css.overflowX), y = /hidden|clip|auto|scroll/.test(css.overflowY)
        if (x || y) clips.push({ x, y, bounds: parent.getBoundingClientRect().toJSON() })
      }
      const contained = (box: { left: number; right: number; top: number; bottom: number }) => clips.every(clip =>
        (!clip.x || (box.left >= clip.bounds.left - 1 && box.right <= clip.bounds.right + 1)) &&
        (!clip.y || (box.top >= clip.bounds.top - 1 && box.bottom <= clip.bounds.bottom + 1)))
      const range = document.createRange(); range.selectNodeContents(control)
      const lines = [...range.getClientRects()].filter(line => line.width && line.height)
      const extent = Number.parseFloat(style.outlineWidth) + Number.parseFloat(style.outlineOffset)
      const ring = { left: rect.left - extent, right: rect.right + extent, top: rect.top - extent, bottom: rect.bottom + extent }
      return { text: control.textContent, role: control.getAttribute('role'), checked: control.getAttribute('aria-checked'), tabIndex: control.tabIndex,
        focused: control === document.activeElement, focusVisible: control.matches(':focus-visible'), bounds: rect.toJSON(),
        outline: { width: style.outlineWidth, style: style.outlineStyle, color: style.outlineColor },
        points, clips, lines: lines.map(line => line.toJSON()), ring, ringContained: contained(ring),
        textContained: lines.length > 0 && lines.every(line => contained(line) && line.left >= rect.left - 1 && line.right <= rect.right + 1),
        inViewport: ring.left >= 0 && ring.top >= 0 && ring.right <= innerWidth && ring.bottom <= innerHeight,
        centerHit: points[0]!.hit, scrollWidth: control.scrollWidth, clientWidth: control.clientWidth }
    }),
    rows: [...document.querySelectorAll('.site-identity strong')].map(node => node.textContent).sort(),
  }))
}
async function capture(page: Page, info: TestInfo, name: string, artifacts: unknown[], api: Awaited<ReturnType<typeof fixture>>) {
  const requestsBefore = [...api.requests]
  // 先验证真实键盘可达，仅归零页面纵向滚动；保留分段控件原生横向滚动及焦点。
  const reachable = await state(page)
  const focused = reachable.controls.find(control => control.focused)!
  expect(focused.focusVisible).toBe(true)
  expect(focused.centerHit).toBe(true)
  expect(focused.bounds.width).toBeGreaterThan(0)
  expect(focused.bounds.height).toBeGreaterThanOrEqual(40)
  await page.evaluate(() => {
    for (const element of document.querySelectorAll('*')) if (element.scrollTop && !element.closest('.workspace-filters')) element.scrollTo(element.scrollLeft, 0)
    window.scrollTo(window.scrollX, 0)
  })
  await waitForPageReady(page)
  const before = await state(page)
  const captureFocus = before.controls.find(control => control.focused)!
  expect(captureFocus.text).toBe(focused.text)
  expect(captureFocus.checked).toBe('true')
  expect(before.groupScrollLeft).toBe(reachable.groupScrollLeft)
  expect(captureFocus.focusVisible).toBe(true)
  expect(captureFocus.inViewport).toBe(true)
  expect(captureFocus.points.every(point => point.hit)).toBe(true)
  expect(captureFocus.textContained).toBe(true)
  expect(captureFocus.ringContained).toBe(true)
  expect(Number.parseFloat(captureFocus.outline.width)).toBeGreaterThan(0)
  expect(captureFocus.outline.style).not.toBe('none')
  const path = info.outputPath('ui-captures', `${name}.png`)
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = await page.screenshot({ path, fullPage: true, animations: 'disabled' })
  const afterCapture = await state(page)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await state(page)
  artifacts.push({ name, screenshot: `${name}.png`, screenshotSha256: hash(png), reachable, before, afterCapture, afterAxe, axe, requestsBefore, requestsAfter: [...api.requests] })
  await info.attach(name, { path, contentType: 'image/png' })
  expect(api.requests.slice(requestsBefore.length).every(request => request.method === 'GET' && request.path === '/api/v1/jobs' && request.search === '')).toBe(true)
  expect(afterCapture).toEqual(before)
  expect(afterAxe).toEqual(before)
  expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
}

for (const width of [390, 1440]) {
  test(`站点状态筛选 ${width}px：原生单选键盘、计数与目录保持一致`, async ({ page, baseURL }, info) => {
    test.setTimeout(180_000)
    await page.setViewportSize({ width, height: 1000 })
    const api = await fixture(page, new URL(baseURL!).origin)
    const artifacts: unknown[] = [], keyboard: unknown[] = []
    let completed = false
    const sourceFiles = execFileSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '--', 'src', 'e2e/site-filter-keyboard.spec.ts', 'e2e/mockApi.ts', 'e2e/pageReadiness.ts', 'package.json', 'pnpm-lock.yaml', 'vite.config.ts', 'playwright.config.ts', 'index.html'], { encoding: 'utf8' }).trim().split('\n').filter(Boolean)
    const provenance = { sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), sourceHashes: Object.fromEntries(await Promise.all([...new Set(sourceFiles)].sort().map(async path => [path, hash(await readFile(path))]))) }
    try {
      await page.goto('/sites')
      await page.evaluate(() => document.fonts.ready)
      await waitForPageReady(page)
      await expect(page.locator('.el-message:visible')).toHaveCount(0)
      const group = page.getByRole('radiogroup', { name: '站点状态筛选' })
      const radios = group.getByRole('radio')
      const category = page.getByRole('combobox', { name: '按分类筛选站点' })
      const search = page.getByRole('textbox', { name: '搜索站点', exact: true })
      await expect(radios).toHaveText(['运行中2', '全部站点3', '已隐藏1', '已忽略1'])
      await expect(group.locator('[role="tab"], [aria-selected], [aria-controls]')).toHaveCount(0)
      await tabTo(page, radios.nth(0))
      await page.keyboard.press('Tab'); await expect(category).toBeFocused()
      await page.keyboard.press('Shift+Tab'); await expect(radios.nth(0)).toBeFocused()
      const rows = async () => (await page.locator('.site-identity strong').allTextContents()).sort()
      async function selected(index: number) {
        await expect(radios.nth(index)).toBeFocused()
        await expect(radios.nth(index)).toHaveAttribute('aria-checked', 'true')
        await expect(group.locator('[aria-checked="true"]')).toHaveCount(1)
        await expect(group.locator('[tabindex="0"]')).toHaveCount(1)
        await expect(radios.nth(index)).toHaveAttribute('tabindex', '0')
        await expect.poll(rows).toEqual(expectedRows[index])
        keyboard.push({ expected: labels[index], state: await state(page) })
      }
      await selected(0)
      await capture(page, info, `site-filter-${width}-running`, artifacts, api)
      for (const [key, index] of [['ArrowRight', 1], ['ArrowDown', 2], ['End', 3]] as const) {
        await page.keyboard.press(key); await selected(index)
        await capture(page, info, `site-filter-${width}-${index}`, artifacts, api)
      }
      for (const [key, index] of [['ArrowRight', 0], ['ArrowLeft', 3], ['ArrowUp', 2], ['Home', 0]] as const) {
        await page.keyboard.press(key); await selected(index)
      }
      await page.keyboard.press('Space'); await selected(0)
      await category.click(); await page.getByRole('option', { name: '工具', exact: true }).click()
      await search.fill('停止')
      await radios.nth(1).click()
      await expect.poll(rows).toEqual(['工具停止'])
      await expect(search).toHaveValue('停止')
      await expect(page.locator('.category-filter')).toContainText('工具')
      await radios.nth(1).press('Home')
      await expect(page.getByRole('heading', { name: '没有匹配的站点', exact: true })).toBeVisible()
      await expect(search).toHaveValue('停止')
      await expect(page.locator('.category-filter')).toContainText('工具')
      await page.keyboard.press('ArrowRight')
      await expect.poll(rows).toEqual(['工具停止'])
      await search.fill('')
      await expect.poll(rows).toEqual(['工具停止', '工具运行'])
      await expect(radios).toHaveText(['运行中2', '全部站点3', '已隐藏1', '已忽略1'])
      expect(api.requests.filter(request => request.method !== 'GET')).toEqual([])
      expect(api.requests.filter(request => request.path.startsWith('/api/v1/sites')).map(request => request.path).sort()).toEqual(['/api/v1/sites', '/api/v1/sites/ignored'])
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      completed = true
    } finally {
      // 最终状态与请求账本在所有断言之后保存；失败不得被标记为完成。
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      const path = info.outputPath('ui-captures', `site-filter-${width}-capture-state.json`)
      await writeFile(path, JSON.stringify({ ...provenance, fixtureOnly: true, nativeNASAcceptance: false, completed,
        testTitle: info.title, project: info.project.name, retry: info.retry, viewport: page.viewportSize(),
        artifacts, keyboard, distHashes: api.distHashes, requests: api.requests, denied: api.denied, errors: api.errors,
        assertionErrors: info.errors.map(error => error.message), finalState: await state(page).catch(() => null),
      }, null, 2))
      await info.attach(`site-filter-${width}-state`, { path, contentType: 'application/json' })
    }
  })
}
