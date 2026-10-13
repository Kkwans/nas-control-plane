import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi, summary } from './mockApi'
import { waitForPageReady } from './pageReadiness'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })

const evidence = new WeakMap<Page, { requests: Array<{ method: string; path: string }>; pageErrors: string[] }>()

async function openPage(page: Page) {
  const events = { requests: [] as Array<{ method: string; path: string }>, pageErrors: [] as string[] }
  evidence.set(page, events)
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname.startsWith('/api/')) events.requests.push({ method: request.method(), path: url.pathname + url.search })
  })
  page.on('pageerror', (error) => events.pageErrors.push(error.message))
  await installMockApi(page, { clock: false })
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: '系统总览', exact: true })).toBeVisible()
  await waitForPageReady(page)
  // 等待挂起的路由焦点帧；初始 auth 期间 main 可能尚未挂载，不要求它已被聚焦。
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  const button = page.getByRole('button', { name: '立即刷新数据', exact: true })
  await expect(button).toBeEnabled()
  await page.getByRole('main').focus()
  return button
}
async function openHelp(page: Page, button: Locator) {
  const help = page.getByRole('tooltip', { name: '立即刷新数据', exact: true })
  await expect(help).toBeVisible()
  const id = await help.getAttribute('id')
  expect(id).toBeTruthy()
  await expect(button).toHaveAttribute('aria-describedby', id!)
  expect(await help.evaluate((element) => element.parentElement?.matches('header.topbar'))).toBe(true)
  await expect(page.getByRole('banner')).toHaveCount(1)
  const bounds = await help.boundingBox()
  const viewport = page.viewportSize()!
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.y).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width)
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height)
  return help
}
async function helpState(page: Page) {
  return page.evaluate(() => {
    const button = document.querySelector<HTMLButtonElement>('button[aria-label="立即刷新数据"]')!
    const id = button.getAttribute('aria-describedby')
    const help = id ? document.getElementById(id) : null
    const header = button.closest('header.topbar')
    const active = document.activeElement
    const geometry = (element: HTMLElement | null) => element ? {
      bounds: element.getBoundingClientRect().toJSON(), scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
      display: getComputedStyle(element).display, visibility: getComputedStyle(element).visibility,
    } : null
    return {
      viewport: { width: innerWidth, height: innerHeight, scrollX, scrollY },
      focus: { tag: active?.tagName, id: active?.id, label: active?.getAttribute('aria-label'), buttonFocused: active === button },
      button: { describedBy: id, disabled: button.disabled, hovered: button.matches(':hover'), geometry: geometry(button) },
      help: help ? {
        id: help.id, text: help.textContent, role: help.getAttribute('role'), ariaHidden: help.getAttribute('aria-hidden'),
        hovered: help.matches(':hover'), geometry: geometry(help), parentTag: help.parentElement?.tagName,
        parentClass: help.parentElement?.className, directHeaderChild: help.parentElement === header,
      } : null,
      landmark: { headerCount: document.querySelectorAll('header.topbar').length, headerContainsButton: header?.contains(button), headerContainsHelp: help ? header?.contains(help) : false, mainCount: document.querySelectorAll('main').length },
    }
  })
}
async function auditOpenHelp(page: Page, testInfo: TestInfo, label: string) {
  // screenshot 会等待字体；采样前同样等待，避免把字体落地误判成提示状态漂移。
  await page.evaluate(() => document.fonts.ready.then(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))))
  const button = page.getByRole('button', { name: '立即刷新数据', exact: true })
  await openHelp(page, button)
  const events = evidence.get(page)!
  const beforeCapture = await helpState(page)
  const requestsBefore = [...events.requests]
  // CI 官方证据收集器只保留此目录中的 PNG 和配对 capture-state JSON。
  await mkdir(testInfo.outputPath('ui-captures'), { recursive: true })
  const png = testInfo.outputPath('ui-captures', `${label}.png`)
  await page.screenshot({ path: png, fullPage: true })
  const afterScreenshot = await helpState(page)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await helpState(page)
  const json = testInfo.outputPath('ui-captures', `${label}-capture-state.json`)
  await writeFile(json, JSON.stringify({
    fixtureOnly: true, sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    fixtureSource: 'frontend/e2e/refresh-help.spec.ts + frontend/e2e/mockApi.ts',
    fixtureSha256: createHash('sha256').update(await readFile(new URL('./refresh-help.spec.ts', import.meta.url))).digest('hex'),
    mockApiSha256: createHash('sha256').update(await readFile(new URL('./mockApi.ts', import.meta.url))).digest('hex'),
    testTitle: testInfo.title, project: testInfo.project.name, retry: testInfo.retry, url: page.url(),
    screenshot: `${label}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    beforeCapture, afterScreenshot, afterAxe, requestsBefore, requestsAfter: [...events.requests], pageErrors: [...events.pageErrors],
    axe,
  }, null, 2))
  await testInfo.attach(label, { path: png, contentType: 'image/png' })
  await testInfo.attach(`${label}-state`, { path: json, contentType: 'application/json' })
  await openHelp(page, button)
  expect(afterScreenshot).toEqual(beforeCapture)
  expect(afterAxe).toEqual(beforeCapture)
  expect(events.requests).toEqual(requestsBefore)
  expect(events.pageErrors).toEqual([])
  // 全页真实扫描；region 同样阻断，不能因通常属于 moderate 而放过。
  const violations = axe.violations.filter((item) => item.id === 'region' || item.impact === 'serious' || item.impact === 'critical')
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([])
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`刷新帮助保留真实 banner、焦点、hover 与 Escape（${viewport.width}px）`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport)
    const button = await openPage(page)
    await button.focus()
    let help = await openHelp(page, button)
    await auditOpenHelp(page, testInfo, `refresh-focus-${viewport.width}`)
    await page.keyboard.press('Escape')
    await expect(help).toBeHidden()
    await expect(button).toBeFocused()
    await expect(button).not.toHaveAttribute('aria-describedby')

    await page.getByRole('main').focus()
    await button.hover()
    help = await openHelp(page, button)
    await expect(button).not.toBeFocused()
    await help.hover()
    await expect(help).toBeVisible()
    await button.focus()
    await page.mouse.move(1, viewport.height - 1)
    await expect(help).toBeVisible()
    await button.hover()
    await page.getByRole('main').focus()
    await expect(help).toBeVisible()
    await auditOpenHelp(page, testInfo, `refresh-hover-${viewport.width}`)
    await page.keyboard.press('Escape')
    await expect(help).toBeHidden()
    await expect(page.getByRole('main')).toBeFocused()
    await page.mouse.move(1, viewport.height - 1)
    await button.hover()
    await openHelp(page, button)
    await page.mouse.move(1, viewport.height - 1)
    await expect(help).toBeHidden()
  })
}

test('原生 Enter/Space 各刷新一次，loading 清理帮助且不会复活旧焦点状态', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const button = await openPage(page)
  await button.evaluate((element) => {
    element.setAttribute('data-fixture-clicks', '0')
    element.addEventListener('click', () => element.setAttribute('data-fixture-clicks', String(Number(element.getAttribute('data-fixture-clicks')) + 1)))
  })
  let reads = 0
  let release: (() => void) | undefined
  await page.route('**/api/v1/system/summary', async (route) => {
    reads += 1
    await new Promise<void>((resolve) => { release = resolve })
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ ...summary, collectedAt: new Date().toISOString() }) })
  })
  try {
    for (const key of ['Enter', 'Space']) {
      await page.getByRole('main').focus()
      await button.focus()
      const help = await openHelp(page, button)
      const previous = reads
      await page.keyboard.press(key)
      await expect.poll(() => reads).toBe(previous + 1)
      await expect(button).toHaveAttribute('data-fixture-clicks', String(previous + 1))
      await expect(button).toBeDisabled()
      await expect(help).toBeHidden()
      await expect(button).not.toHaveAttribute('aria-describedby')
      await page.keyboard.press(key)
      expect(reads).toBe(previous + 1)
      await expect(button).toHaveAttribute('data-fixture-clicks', String(previous + 1))
      release!(); release = undefined
      await expect(button).toBeEnabled()
      await expect(help).toBeHidden()
      await expect(button).not.toHaveAttribute('aria-describedby')
    }
    await page.getByRole('main').focus()
    await button.focus()
    await openHelp(page, button)
    expect(reads).toBe(2)
    await expect(button).toHaveAttribute('data-fixture-clicks', '2')
  } finally { release?.() }
})
