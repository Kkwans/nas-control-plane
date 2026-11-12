import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

const usersPath = '/api/v1/users'
const policyPath = `${usersPath}/password-policy`
const policy = { minLength: 8, requireUppercase: false, requireLowercase: false, requireDigit: false, requireSpecial: false }
const account = { id: 42, username: 'isolated-operator', role: 'root', disabled: false, createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:00:00Z', lastLoginAt: null }
const cases = [
  { name: 'policy', path: policyPath, panel: '.password-policy', title: '密码规则暂不可用' },
  { name: 'accounts', path: usersPath, panel: '.users-panel', title: '账号列表加载失败' },
] as const

test.use({ serviceWorkers: 'block', timezoneId: 'UTC', contextOptions: { reducedMotion: 'reduce' } })

// 沿用 user-read-isolation 的账号/规则错误数据，独立安装严格只读路由，不改共享 fixture。
const hash = (value: string | Buffer) => createHash('sha256').update(value).digest('hex')
async function sourceProvenance() {
  const files = execFileSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '--', 'src', 'e2e/resource-state-touch-target.spec.ts', 'e2e/mockApi.ts', 'e2e/pageReadiness.ts', 'index.html', 'package.json', 'pnpm-lock.yaml', 'vite.config.ts', 'playwright.config.ts'], { encoding: 'utf8' }).trim().split('\n').filter(Boolean)
  return {
    sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async file => [file, hash(await readFile(file))]))),
  }
}
async function fixture(page: Page, origin: string) {
  expect(new URL(origin).hostname).toBe('127.0.0.1')
  await installMockApi(page, { clock: false, manualRealtime: true })
  const staticPaths = new Set<string>()
  const distHashes: Record<string, string> = {}
  async function visit(directory: string, prefix = '') {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const relative = `${prefix}/${entry.name}`
      if (entry.isDirectory()) await visit(`${directory}/${entry.name}`, relative)
      else if (entry.isFile()) { staticPaths.add(relative); distHashes[relative] = hash(await readFile(`${directory}/${entry.name}`)) }
    }
  }
  await visit('dist')
  let failedPath = policyPath
  const requests: Array<{ method: string; path: string; search: string }> = []
  const denied: string[] = [], pageErrors: string[] = []
  const bootstrapReads = new Set(['/api/v1/auth/status', '/api/v1/preferences'])
  page.on('pageerror', error => pageErrors.push(error.message))
  await page.routeWebSocket('**', socket => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    if (url.pathname.startsWith('/api/')) requests.push({ method, path: url.pathname, search: url.search })
    const block = () => { denied.push(`${method} ${url.href}`); return route.abort('blockedbyclient') }
    if (url.origin !== origin || method !== 'GET' || url.search) return block()
    if (url.pathname === usersPath || url.pathname === policyPath) {
      if (url.pathname === failedPath) return route.fulfill({ status: 503, json: { code: 'UNAVAILABLE', message: 'fixture read unavailable' } })
      return route.fulfill({ json: url.pathname === usersPath ? [account] : policy })
    }
    if (url.pathname === '/api/v1/jobs') return route.fulfill({ json: { jobs: [] } })
    if (bootstrapReads.has(url.pathname)) return route.fallback()
    if (url.pathname === '/users' || staticPaths.has(url.pathname)) return route.fallback()
    return block()
  })
  return { requests, denied, pageErrors, distHashes, fail(path: string) { failedPath = path }, recover() { failedPath = '' } }
}

async function focusByTab(page: Page, button: Locator) {
  // 从真实文档焦点出发，只用 Tab；不调用 focus() 或合成事件。
  for (let index = 0; index < 80; index += 1) {
    await page.keyboard.press('Tab')
    if (await button.evaluate(element => element === document.activeElement)) return
  }
  throw new Error('重试按钮未能通过原生 Tab 到达')
}

async function measure(button: Locator) {
  return button.evaluate(element => {
    const bounds = element.getBoundingClientRect()
    const style = getComputedStyle(element)
    const outlineExtent = Number.parseFloat(style.outlineWidth) + Number.parseFloat(style.outlineOffset)
    const outline = { left: bounds.left - outlineExtent, right: bounds.right + outlineExtent, top: bounds.top - outlineExtent, bottom: bounds.bottom + outlineExtent }
    const clips: Array<{ x: boolean; y: boolean; bounds: DOMRectJSON }> = []
    for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
      if (ancestor === document.body || ancestor === document.documentElement) continue
      const computed = getComputedStyle(ancestor)
      const x = /hidden|clip|auto|scroll/.test(computed.overflowX)
      const y = /hidden|clip|auto|scroll/.test(computed.overflowY)
      if (x || y) clips.push({ x, y, bounds: ancestor.getBoundingClientRect().toJSON() })
    }
    const contains = (box: { left: number; right: number; top: number; bottom: number }) => clips.every(clip =>
      (!clip.x || (box.left >= clip.bounds.left - 1 && box.right <= clip.bounds.right + 1)) &&
      (!clip.y || (box.top >= clip.bounds.top - 1 && box.bottom <= clip.bounds.bottom + 1)))
    const range = document.createRange()
    range.selectNodeContents(element)
    const lines = [...range.getClientRects()].filter(line => line.width > 0 && line.height > 0)
    const points = [
      { name: 'center', x: bounds.left + bounds.width / 2, y: bounds.top + bounds.height / 2 },
      { name: 'top-left', x: bounds.left + 3, y: bounds.top + 3 },
      { name: 'top-right', x: bounds.right - 3, y: bounds.top + 3 },
      { name: 'bottom-left', x: bounds.left + 3, y: bounds.bottom - 3 },
      { name: 'bottom-right', x: bounds.right - 3, y: bounds.bottom - 3 },
    ].map(point => {
      const hit = document.elementFromPoint(point.x, point.y)
      return { ...point, hit: hit === element || (hit !== null && element.contains(hit)), hitTag: hit?.tagName ?? null }
    })
    return {
      bounds: bounds.toJSON(), clips, lines: lines.map(line => line.toJSON()), points,
      text: element.textContent, tag: element.tagName, type: element.getAttribute('type'), disabled: (element as HTMLButtonElement).disabled,
      focused: element === document.activeElement, focusVisible: element.matches(':focus-visible'),
      outline, outlineStyle: style.outlineStyle, outlineWidth: style.outlineWidth, outlineOffset: style.outlineOffset, outlineColor: style.outlineColor,
      boxContained: contains(bounds), outlineContained: contains(outline),
      inViewport: outline.left >= 0 && outline.top >= 0 && outline.right <= innerWidth && outline.bottom <= innerHeight,
      textContained: lines.length > 0 && lines.every(line => contains(line) && line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1),
      scrollWidth: element.scrollWidth, clientWidth: element.clientWidth, scrollHeight: element.scrollHeight, clientHeight: element.clientHeight,
      scrollX, scrollY, innerWidth, rootFontSize: getComputedStyle(document.documentElement).fontSize,
      documentScrollWidth: document.documentElement.scrollWidth,
    }
  })
}

// DOMRect.toJSON() 的实际 JSON 字段；不将 JSDOM 或 CSS 合同当成几何证明。
type DOMRectJSON = { left: number; right: number; top: number; bottom: number }

async function png(page: Page, info: TestInfo, name: string, fullPage = false) {
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const path = info.outputPath('ui-captures', `${name}.png`)
  if (fullPage) {
    // 先完成可达性测量，再复位截图滚动；不主动移动或移除键盘焦点。
    const focused = await page.evaluateHandle(() => document.activeElement)
    await page.evaluate(() => window.scrollTo({ left: 0, top: 0, behavior: 'instant' }))
    await expect.poll(() => page.evaluate(() => ({ x: scrollX, y: scrollY }))).toEqual({ x: 0, y: 0 })
    expect(await page.evaluate(element => document.activeElement === element, focused)).toBe(true)
    await focused.dispose()
  }
  const buffer = await page.screenshot({ path, fullPage, animations: 'disabled' })
  await info.attach(name, { path, contentType: 'image/png' })
  return { file: `${name}.png`, sha256: hash(buffer), fullPage }
}

for (const width of [390, 1440]) {
  for (const scale of [1, 2]) {
    test(`ResourceState 重试触达 ${width}px / 根字号 ${scale * 100}%`, async ({ page, baseURL }, info) => {
      test.setTimeout(120_000)
      await page.setViewportSize({ width, height: 1000 })
      const api = await fixture(page, new URL(baseURL!).origin)
      const provenance = await sourceProvenance()
      for (const target of cases) {
        for (const key of ['Enter', 'Space'] as const) {
          const prefix = `resource-state-target-${target.name}-${key}-${width}-${scale * 100}`
          const captures: Awaited<ReturnType<typeof png>>[] = []
          const state: Record<string, unknown> = {
            ...provenance, distHashes: api.distHashes, fixtureOnly: true, completed: false,
            fixtureSource: 'resource-state-touch-target.spec.ts + mockApi.ts; data adapted from user-read-isolation.spec.ts',
            magnification: 'root-font-size (not browser zoom)', width, scale, target, key, captures,
          }
          try {
            api.fail(target.path)
            await page.goto('/users', { waitUntil: 'domcontentloaded' })
            const panel = page.locator(target.panel)
            const alert = panel.getByRole('alert')
            const retry = alert.getByRole('button', { name: '重试', exact: true })
            await expect(alert).toContainText(target.title)
            await expect(retry).toBeEnabled()
            await expect(page.locator('.users-page')).toBeVisible()
            await page.evaluate(() => document.fonts.ready)
            const baseFontSize = await page.locator('html').evaluate(element => Number.parseFloat(getComputedStyle(element).fontSize))
            // 仅根字号倍增，不模拟 browser zoom，不改变视口或正文/按钮内容。
            await page.evaluate(size => document.documentElement.style.setProperty('font-size', `${size}px`, 'important'), baseFontSize * scale)
            await waitForPageReady(page)
            await focusByTab(page, retry)
            await retry.evaluate(element => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }))
            await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
            const geometry = await measure(retry)
            const textGeometry = []
            for (const item of await alert.locator('strong, p, small').all()) textGeometry.push(await measure(item))
            Object.assign(state, { baseFontSize, geometry, textGeometry, requestsBeforeCapture: [...api.requests] })
            captures.push(await png(page, info, `${prefix}-focused-viewport`))
            const afterFocusedCapture = await measure(retry)
            const afterFocusedText = []
            for (const item of await alert.locator('strong, p, small').all()) afterFocusedText.push(await measure(item))
            const requestsAfterFocusedCapture = [...api.requests]
            Object.assign(state, { afterFocusedCapture, afterFocusedText, requestsAfterFocusedCapture })
            captures.push(await png(page, info, `${prefix}-error-fullpage`, true))
            const beforeAxe = await alert.textContent()
            const errorAxe = await new AxeBuilder({ page }).analyze()
            Object.assign(state, { errorAxe, beforeAxe, afterAxe: await alert.textContent(), requestsAfterAxe: [...api.requests] })
            expect(await alert.textContent()).toBe(beforeAxe)
            // axe 使用自然时钟。激活前重取计数，既不暂停轮询也不吞掉其他 API。
            await retry.evaluate(element => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }))
            await expect(retry).toBeFocused()
            const beforeActivation = await measure(retry)
            const preActivationRequests = api.requests.slice((state.requestsBeforeCapture as unknown[]).length)
            Object.assign(state, { beforeActivation, preActivationRequests })
            expect(beforeActivation).toEqual(geometry)
            expect(preActivationRequests.every(request => request.method === 'GET' && request.path === '/api/v1/jobs' && request.search === '')).toBe(true)
            api.recover()
            const baseline = api.requests.length
            await page.keyboard.press(key)
            await expect(alert).toHaveCount(0)
            await expect(page.locator('.password-policy').getByRole('spinbutton')).toHaveValue('8')
            await expect(page.locator('.users-panel').getByText(account.username, { exact: true })).toBeVisible()
            await waitForPageReady(page)
            captures.push(await png(page, info, `${prefix}-recovered-fullpage`, true))
            const recoveredAxe = await new AxeBuilder({ page }).analyze()
            const recoveryRequests = api.requests.slice(baseline)
            Object.assign(state, { recoveryRequests, recoveredAxe })
            // PNG 先落盘；原状几何、全部 axe 与断言结果由 finally 无条件写入证据。
            expect(afterFocusedCapture).toEqual(geometry)
            expect(afterFocusedText).toEqual(textGeometry)
            const captureReads = requestsAfterFocusedCapture.slice((state.requestsBeforeCapture as unknown[]).length)
            expect(captureReads.every(request => request.method === 'GET' && request.path === '/api/v1/jobs' && request.search === '')).toBe(true)
            expect(geometry.bounds.width).toBeGreaterThanOrEqual(44)
            expect(geometry.bounds.height).toBeGreaterThanOrEqual(44)
            expect(geometry.points).toHaveLength(5)
            expect(geometry.points.every(point => point.hit)).toBe(true)
            expect(geometry.tag).toBe('BUTTON')
            expect(geometry.type).toBe('button')
            expect(geometry.disabled).toBe(false)
            expect(geometry.text).toBe('重试')
            expect(geometry.focused && geometry.focusVisible && geometry.boxContained && geometry.outlineContained && geometry.inViewport && geometry.textContained).toBe(true)
            expect(geometry.outlineStyle).toBe('solid')
            expect(geometry.outlineWidth).toBe('2px')
            expect(geometry.outlineOffset).toBe('2px')
            expect(geometry.outlineColor).not.toBe('rgba(0, 0, 0, 0)')
            expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth + 1)
            expect(geometry.scrollHeight).toBeLessThanOrEqual(geometry.clientHeight + 1)
            expect(geometry.innerWidth).toBe(width)
            expect(geometry.rootFontSize).toBe(`${baseFontSize * scale}px`)
            expect(geometry.documentScrollWidth).toBeLessThanOrEqual(width + 1)
            for (const item of textGeometry) {
              expect(item.textContained && item.boxContained, JSON.stringify(item)).toBe(true)
              expect(item.scrollWidth).toBeLessThanOrEqual(item.clientWidth + 1)
              expect(item.scrollHeight).toBeLessThanOrEqual(item.clientHeight + 1)
            }
            // 仅允许应用既有 GET /jobs 后台轮询；记录其完整查询，不清空请求日志。
            expect(recoveryRequests.filter(request => request.path !== '/api/v1/jobs')).toEqual([{ method: 'GET', path: target.path, search: '' }])
            expect(recoveryRequests.filter(request => request.path === '/api/v1/jobs').every(request => request.method === 'GET' && request.search === '')).toBe(true)
            expect(api.requests.filter(request => request.method !== 'GET')).toEqual([])
            expect(api.denied).toEqual([])
            expect(api.pageErrors).toEqual([])
            for (const result of [errorAxe, recoveredAxe]) {
              expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([])
            }
            state.completed = true
          } catch (error) {
            state.failure = error instanceof Error ? { message: error.message, stack: error.stack } : String(error)
            throw error
          } finally {
            Object.assign(state, { requests: [...api.requests], denied: [...api.denied], pageErrors: [...api.pageErrors] })
            await mkdir(info.outputPath('ui-captures'), { recursive: true })
            const json = JSON.stringify(state, null, 2)
            await writeFile(info.outputPath('ui-captures', `${prefix}-capture-state.json`), json)
            await info.attach(`${prefix}-capture-state`, { body: json, contentType: 'application/json' })
          }
        }
      }
    })
  }
}
