import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type Request, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'

const path = '/api/v1/preferences'
const baseline = {
  refreshIntervalSeconds: 30, interfaceDensity: 'compact', baseFontSize: 16, pageSize: 37,
  sidebarDefault: 'expanded', linkOpenMode: 'new-tab', siteDefaultProtocol: 'https',
  chineseFont: 'noto-sans-sc', latinFont: 'inter',
  navigationOrder: ['settings', 'system', 'overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'users', 'terminal'],
}
type Preferences = typeof baseline
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = { method: 'GET' | 'PUT'; status: number; malformed: boolean; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }
const status = (page: Page) => page.locator('.save-state')
const row = (page: Page, label: string) => page.locator('.setting-row').filter({ has: page.locator('strong', { hasText: label }) })

// 所有 API 均由内存 fixture 接管；未知请求直接失败，绝不访问真实设置或 NAS。
async function fixture(page: Page) {
  await installMockApi(page, { manualRealtime: true })
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000))
  const writes: Preferences[] = []
  const requests: string[] = []
  const unexpected: string[] = []
  const gates: Gate[] = []
  let saved = structuredClone(baseline)
  const allowedReads = new Set(['/api/v1/auth/status', '/api/v1/system/capabilities', '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/sites', '/api/v1/sites/ignored'])
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const pathname = new URL(request.url()).pathname
    const method = request.method()
    requests.push(`${method} ${pathname}`)
    if (pathname === '/api/v1/auth/logout' && method === 'POST') return route.fulfill({ status: 204 })
    if (method === 'GET' && allowedReads.has(pathname)) return route.fallback()
    if (pathname !== path || (method !== 'GET' && method !== 'PUT')) {
      unexpected.push(`${method} ${pathname}`)
      return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: 'fixture denied' } })
    }
    const snapshot = method === 'PUT' ? request.postDataJSON() as Preferences : structuredClone(saved)
    if (method === 'PUT') writes.push(structuredClone(snapshot))
    const index = gates.findIndex((gate) => gate.method === method)
    const gate = index < 0 ? undefined : gates.splice(index, 1)[0]!
    if (gate) { gate.received.resolve(); await gate.release.promise }
    try {
      if (gate && gate.status !== 200) return await route.fulfill({ status: gate.status, json: { code: 'FIXTURE_UNAVAILABLE', message: 'fixture unavailable' } })
      // 无效 2xx 的服务端结果刻意不推断；核实 GET 返回原基线。
      if (gate?.malformed) return await route.fulfill({ json: { ...snapshot, baseFontSize: '18' } })
      if (method === 'PUT') saved = structuredClone(snapshot)
      await route.fulfill({ json: snapshot })
    } finally { gate?.finished.resolve() }
  })
  return {
    writes, requests, unexpected,
    hold(method: Gate['method'], options: { status?: number; malformed?: boolean } = {}) {
      const gate: Gate = { method, status: options.status ?? 200, malformed: options.malformed ?? false, received: deferred(), release: deferred(), finished: deferred() }
      gates.push(gate)
      return gate
    },
    async release(gate: Gate) { gate.release.resolve(); await gate.finished.promise },
  }
}
async function open(page: Page) {
  await page.goto('/settings', { waitUntil: 'domcontentloaded' })
  await page.locator('.settings-page').waitFor()
}
async function ready(page: Page) {
  await expect(page.locator('.settings-grid')).not.toHaveAttribute('disabled', '')
  await expect(row(page, '基础字号')).toContainText('16 px')
}
async function assertBaselineLocked(page: Page) {
  const grid = page.locator('.settings-grid')
  // Playwright 的 disabled matcher 不覆盖 fieldset 本身；核对原生属性及其子控件语义。
  await expect(grid).toHaveAttribute('disabled', '')
  await expect(grid).toHaveAttribute('inert', '')
  expect(await grid.locator('input, button').evaluateAll((controls) => controls.length > 0 && controls.every((control) => control.matches(':disabled')))).toBe(true)
  expect(await grid.locator('input').first().evaluate((input) => {
    input.focus()
    return document.activeElement === input
  })).toBe(false)
}
async function dropdownState(page: Page, label: string, option: string) {
  return row(page, label).getByRole('combobox').evaluate((input, expected) => {
    const listbox = document.getElementById(input.getAttribute('aria-controls') ?? '')
    const popper = listbox?.closest('.el-select__popper')
    const choice = [...(listbox?.querySelectorAll('[role="option"]') ?? [])].find((element) => element.textContent?.trim() === expected)
    const describe = (element: Element) => {
      const style = getComputedStyle(element)
      return { tag: element.tagName, id: element.id, classes: element.getAttribute('class'),
        bounds: element.getBoundingClientRect().toJSON(), display: style.display, visibility: style.visibility,
        opacity: style.opacity, transform: style.transform, overflow: style.overflow,
        transitionDuration: style.transitionDuration, transitionDelay: style.transitionDelay,
        ariaHidden: element.getAttribute('aria-hidden'), placement: element.getAttribute('data-popper-placement') }
    }
    const ancestors = (element: Element | undefined) => {
      const result = []
      for (let current = element; current; current = current.parentElement ?? undefined) result.push(describe(current))
      return result
    }
    const seconds = (value: string) => value.split(',').map((part) => Number.parseFloat(part) * (part.trim().endsWith('ms') ? 1 : 1000))
    const style = popper ? getComputedStyle(popper) : null
    const transitionMilliseconds = style ? Math.max(...seconds(style.transitionDuration)) + Math.max(...seconds(style.transitionDelay)) : 0
    return { clock: { now: Date.now(), performance: performance.now() }, viewport: { width: innerWidth, height: innerHeight },
      fonts: { status: document.fonts.status, loadedFamilies: [...new Set([...document.fonts].filter((font) => font.status === 'loaded').map((font) => font.family))] },
      expanded: input.getAttribute('aria-expanded'), controls: input.getAttribute('aria-controls'),
      trigger: describe(input.closest('.el-select__wrapper') ?? input), optionAncestors: ancestors(choice),
      popup: popper ? describe(popper) : null, transitionMilliseconds,
      entering: Boolean(popper && [...popper.classList].some((name) => /^el-zoom-in-top-enter-/.test(name))),
    }
  }, option)
}
const selectionCaptures = new WeakMap<Page, number>()
async function select(page: Page, label: string, option: string) {
  // 非搜索型 ElSelect 的只读 input 会被占位文案覆盖；点击用户实际看到的 wrapper。
  const control = row(page, label).locator('.el-select__wrapper')
  await control.evaluate((element) => element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }))
  await expect(control).toBeVisible()
  const openingWrites: string[] = []
  const observeWrite = (request: Request) => {
    if (request.method() === 'PUT' && new URL(request.url()).pathname === path) openingWrites.push(request.postData() ?? '')
  }
  page.on('request', observeWrite)
  try {
    await control.click()
    await expect(row(page, label).getByRole('combobox')).toHaveAttribute('aria-expanded', 'true')
    const samples = [await dropdownState(page, label, option)]
    // Vue 两次 rAF 移除 enter-from，随后按实际 CSS duration + 1 ms 完成 transition。
    // 观察类名逐帧推进，最多为当前 CSS 时长加 34 ms；硬上限低于 400 ms 保存 debounce。
    const budget = Math.min(384, Math.max(34, Math.ceil(samples[0]!.transitionMilliseconds + 34)))
    let advanced = 34
    await page.clock.runFor(advanced)
    samples.push(await dropdownState(page, label, option))
    while (samples.at(-1)!.entering && advanced < budget) {
      const frame = Math.min(16, budget - advanced)
      await page.clock.runFor(frame)
      advanced += frame
      samples.push(await dropdownState(page, label, option))
    }
    if (process.env.NCP_CAPTURE_UI === '1') {
      const info = test.info()
      const sequence = (selectionCaptures.get(page) ?? 0) + 1
      selectionCaptures.set(page, sequence)
      const name = `settings-dropdown-${sequence}-${option.replace(/[^a-z0-9]+/gi, '-')}`
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      // 在可见性断言前落盘；allow 保留真实动画，不能由诊断截图修复待观察状态。
      await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({
        sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), fixtureOnly: true,
        fixtureSource: 'frontend/e2e/settings-ownership.spec.ts + frontend/e2e/mockApi.ts',
        label, option, viewport: page.viewportSize(), budget, advanced, samples, openingWrites,
      }, null, 2))
      const image = info.outputPath('ui-captures', `${name}.png`)
      await page.screenshot({ path: image, animations: 'allow' })
      await info.attach(name, { path: image, contentType: 'image/png' })
    }
    expect(openingWrites, '打开选择菜单不能提前发送待保存草稿').toEqual([])
    await expect(page.getByRole('option', { name: option, exact: true })).toBeVisible()
  } finally {
    page.off('request', observeWrite)
  }
  await page.getByRole('option', { name: option, exact: true }).click()
}
async function font(page: Page, size: number) {
  await expect(row(page, '基础字号')).toContainText(`${size} px`)
  await expect(page.locator('html')).toHaveCSS('font-size', `${size}px`)
}
async function advance(page: Page) { await page.clock.runFor(401) }

async function heldStateAccessibility(page: Page) {
  // 与 app.spec 一样，axe 异步规则必须使用运行中的页面计时器。
  // 仅用于下方三种稳定状态：GET 由外部 gate 挂起、首次读取失败、未知写入队列已暂停。
  // 它们均无可发送的 debounce；不可将此 helper 用于正在编辑或等待保存的状态。
  await page.clock.resume()
  try {
    return await new AxeBuilder({ page }).include('.settings-page').analyze()
  } finally {
    await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000))
  }
}

async function evidence(page: Page, info: TestInfo, outcome: 'loading' | 'load-error' | 'write-unknown', api: Awaited<ReturnType<typeof fixture>>) {
  const before = { status: await status(page).innerText(), writes: api.writes.length }
  const observations = []
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.evaluate(() => document.fonts.ready)
    await status(page).scrollIntoViewIfNeeded()
    const state = await page.locator('.settings-page').evaluate((element) => ({
      text: element.textContent,
      horizontalOverflow: document.documentElement.scrollWidth > innerWidth + 1,
      fontSize: document.documentElement.style.fontSize,
      density: document.documentElement.dataset.density,
      controls: [...element.querySelectorAll('button, input, fieldset, [role="combobox"], [role="status"]')].map((control) => ({
        text: control.textContent, label: control.getAttribute('aria-label'), disabled: control.matches(':disabled'),
        ariaDisabled: control.getAttribute('aria-disabled'), inert: control.hasAttribute('inert'), bounds: control.getBoundingClientRect().toJSON(),
      })),
    }))
    const axe = await heldStateAccessibility(page)
    const after = { status: await status(page).innerText(), writes: api.writes.length }
    observations.push({ width, state, before, after, violations: axe.violations })
    if (process.env.NCP_CAPTURE_UI === '1') {
      const name = `settings-${outcome}-${width}`
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      const image = info.outputPath('ui-captures', `${name}.png`)
      await page.screenshot({ path: image, animations: 'disabled' })
      await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({
        sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), fixtureOnly: true,
        fixtureSource: 'frontend/e2e/settings-ownership.spec.ts + frontend/e2e/mockApi.ts',
        viewport: page.viewportSize(), outcome, ...state, auditBefore: before, auditAfter: after, requests: api.requests, writes: api.writes, unexpected: api.unexpected, axeViolations: axe.violations,
      }, null, 2))
      await info.attach(name, { path: image, contentType: 'image/png' })
    }
  }
  // 先完成两档截图和状态取证，随后执行布局和可访问性断言。
  for (const observation of observations) {
    expect(observation.after, `${outcome} ${observation.width}px axe 不推进设置状态或写队列`).toEqual(observation.before)
    expect(observation.state.horizontalOverflow, `${outcome} ${observation.width}px 横向溢出`).toBe(false)
    expect(observation.violations, `${outcome} ${observation.width}px axe`).toEqual([])
  }
}

test('首次读取挂起时禁用编辑，完整基线 hydration 不触发写入', async ({ page }, info) => {
  const api = await fixture(page)
  const held = api.hold('GET')
  await open(page)
  await held.received.promise
  await page.getByText('正在读取已保存设置…', { exact: true }).waitFor()
  await evidence(page, info, 'loading', api)
  await assertBaselineLocked(page)
  await expect(page.getByRole('button', { name: '恢复全部默认', exact: true })).toBeDisabled()
  await expect(page.locator('.navigation-order li[draggable="true"]')).toHaveCount(0)
  await advance(page)
  expect(api.writes).toEqual([])
  await api.release(held)
  await ready(page)
  await advance(page)
  await font(page, 16)
  await expect(page.locator('.navigation-order li').first()).toContainText('settings')
  expect(api.requests.filter((request) => request === `GET ${path}`)).toHaveLength(1)
  expect(api.writes).toEqual([])
  expect(api.unexpected).toEqual([])
})

test('首次读取失败只读重试，单项编辑保留所有无关保存字段', async ({ page }, info) => {
  const api = await fixture(page)
  const failed = api.hold('GET', { status: 503 })
  await open(page)
  await failed.received.promise
  await api.release(failed)
  await page.getByText('设置读取失败，已保留可用数据。首次读取成功前无法编辑。', { exact: true }).waitFor()
  await evidence(page, info, 'load-error', api)
  await assertBaselineLocked(page)
  await advance(page)
  expect(api.writes).toEqual([])
  const retry = api.hold('GET')
  await page.getByRole('button', { name: '重新读取', exact: true }).click()
  await retry.received.promise
  expect(api.writes).toEqual([])
  await api.release(retry)
  await ready(page)
  await select(page, '界面密度', '舒适')
  await advance(page)
  await expect(status(page)).toHaveText('已自动保存')
  expect(api.writes).toEqual([{ ...baseline, interfaceDensity: 'comfortable' }])
  expect(api.requests.filter((request) => request === `GET ${path}`)).toHaveLength(2)
  expect(api.unexpected).toEqual([])
})

test('A 保存挂起后编辑 B，旧成功及 B 失败不回退控件或预览', async ({ page }) => {
  const api = await fixture(page)
  await open(page); await ready(page)
  const a = api.hold('PUT')
  await select(page, '基础字号', '17 px'); await advance(page); await a.received.promise
  const b = api.hold('PUT', { status: 503 })
  await select(page, '基础字号', '18 px'); await advance(page)
  await font(page, 18)
  expect(api.writes).toEqual([{ ...baseline, baseFontSize: 17 }])
  await api.release(a); await b.received.promise
  await font(page, 18)
  await api.release(b)
  await expect(status(page)).toContainText('保存失败')
  await font(page, 18)
  await advance(page)
  expect(api.writes).toEqual([{ ...baseline, baseFontSize: 17 }, { ...baseline, baseFontSize: 18 }])
  await expect(status(page).getByRole('button', { name: '重试', exact: true })).toBeEnabled()
  expect(api.unexpected).toEqual([])
})

test('无效成功响应保留草稿，先只读核实再明确确认保存', async ({ page }, info) => {
  const api = await fixture(page)
  await open(page); await ready(page)
  const unknown = api.hold('PUT', { malformed: true })
  await select(page, '基础字号', '18 px'); await advance(page); await unknown.received.promise
  await api.release(unknown)
  await page.getByText('保存结果未知，草稿已保留。请先重新读取核实，再确认是否保存当前草稿。', { exact: true }).waitFor()
  await evidence(page, info, 'write-unknown', api)
  await font(page, 18)
  await advance(page)
  expect(api.writes).toHaveLength(1)
  const reconcile = api.hold('GET')
  await page.getByRole('button', { name: '重新读取', exact: true }).click()
  await reconcile.received.promise
  await advance(page)
  expect(api.writes).toHaveLength(1)
  await api.release(reconcile)
  await expect(status(page)).toContainText('已核实，请确认草稿')
  await font(page, 18)
  await advance(page)
  expect(api.writes).toHaveLength(1)
  await status(page).getByRole('button', { name: '保存当前草稿', exact: true }).click()
  await expect(status(page)).toHaveText('已自动保存')
  expect(api.writes).toEqual([{ ...baseline, baseFontSize: 18 }, { ...baseline, baseFontSize: 18 }])
  expect(api.requests.filter((request) => request === `GET ${path}`)).toHaveLength(2)
  expect(api.unexpected).toEqual([])
})

test('同会话离开并重开设置沿用串行队列，旧响应不覆盖新草稿', async ({ page }) => {
  const api = await fixture(page)
  await open(page); await ready(page)
  const a = api.hold('PUT')
  await select(page, '基础字号', '17 px'); await advance(page); await a.received.promise
  await select(page, '基础字号', '18 px')
  await page.locator('.navigation a[href="/"]').click()
  await page.locator('.navigation a[href="/settings"]').click()
  await expect(row(page, '基础字号')).toContainText('18 px')
  await select(page, '基础字号', '14 px')
  const latest = api.hold('PUT')
  await api.release(a); await latest.received.promise
  await font(page, 14)
  expect(api.writes).toEqual([{ ...baseline, baseFontSize: 17 }, { ...baseline, baseFontSize: 14 }])
  await api.release(latest)
  await expect(status(page)).toHaveText('已自动保存')
  await advance(page)
  expect(api.writes).toHaveLength(2)
  expect(api.unexpected).toEqual([])
})

test('A 保存中退出登录立即丢弃 B 队列，迟到响应不恢复预览或新 PUT', async ({ page }) => {
  const api = await fixture(page)
  await open(page); await ready(page)
  const a = api.hold('PUT')
  await select(page, '基础字号', '17 px'); await advance(page); await a.received.promise
  await select(page, '基础字号', '18 px')
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '进入 NAS Control Plane', exact: true })).toBeVisible()
  await api.release(a)
  await page.clock.runFor(1200)
  await expect(page.locator('html')).toHaveCSS('font-size', '15px')
  expect(api.writes).toEqual([{ ...baseline, baseFontSize: 17 }])
  expect(api.requests.filter((request) => request === 'POST /api/v1/auth/logout')).toHaveLength(1)
  expect(api.unexpected).toEqual([])
})
