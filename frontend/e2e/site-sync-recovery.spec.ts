import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'

const createdId = 'manual-created-fixture'
const existingId = 'existing-fixture'
const existingSite = {
  id: existingId, projectId: existingId, name: '原有站点', description: '原有说明',
  iconUrl: '', category: '工具', state: 'running', primaryPort: 8080, ports: [8080],
  launchUrl: 'http://fixture.invalid:8080', favorite: false, sortOrder: 0,
  lastVisitedAt: null, hidden: false, source: 'manual',
}
const savedWarning = '站点资料已保存，但列表尚未同步。当前草稿已保留，请重新同步，无需重复提交。'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 全部 HTTP 与实时事件均为浏览器 fixture；不访问账号、真实 NAS 或服务。
async function installSiteFixture(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const writes: Array<{ method: string; path: string; body: Record<string, unknown> }> = []
  const reads: string[] = []
  let catalogFails = false
  let nextGate: { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> } | undefined
  await page.route('**/api/v1/sites**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() !== 'GET') {
      writes.push({ method: request.method(), path, body: request.postDataJSON() as Record<string, unknown> })
      await route.fulfill({ json: { ...request.postDataJSON(), projectId: path === '/api/v1/sites' ? createdId : path.split('/').at(-1) } })
      return
    }
    reads.push(path)
    if (path === '/api/v1/sites/ignored') {
      await route.fulfill({ json: [] })
      return
    }
    const fail = catalogFails
    const gate = nextGate
    nextGate = undefined
    if (gate) { gate.received.resolve(); await gate.release.promise }
    await route.fulfill(fail
      ? { status: 503, json: { code: 'AGENT_UNAVAILABLE', message: '目录暂不可读', requestId: 'fixture-catalog' } }
      : { json: {
        collectedAt: '2026-10-13T12:00:00Z', sites: [existingSite],
        discovery: { status: 'complete', probeAvailable: true, candidateCount: 1, verifiedCount: 1, failedCount: 0, issues: [] },
      } })
  })
  return {
    writes, reads,
    failCatalog(value: boolean) { catalogFails = value },
    holdCatalog() {
      const gate = { received: deferred(), release: deferred() }
      nextGate = gate
      return gate
    },
  }
}

const editor = (page: Page) => page.locator('.site-editor:visible')
const warning = (page: Page) => editor(page).getByRole('status')
const retry = (page: Page) => editor(page).getByRole('button', { name: '重新同步列表', exact: true })
const save = (page: Page) => editor(page).getByRole('button', { name: '保存站点资料', exact: true })

async function openCreator(page: Page) {
  await page.getByRole('button', { name: '添加站点', exact: true }).click()
  await editor(page).getByLabel('站点名称', { exact: true }).fill('新建站点')
  await editor(page).getByPlaceholder('例如 https://nas.example.com/app/；留空按 NAS 地址和端口生成').fill('https://fixture.invalid/app')
}

async function controlGeometry(locator: Locator) {
  return locator.evaluate((element) => {
    const range = document.createRange()
    range.selectNodeContents(element)
    const bounds = element.getBoundingClientRect()
    const clips = [bounds]
    for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor)
      if (/(hidden|clip|auto|scroll)/.test(`${style.overflowX} ${style.overflowY}`)) clips.push(ancestor.getBoundingClientRect())
    }
    const lines = [...range.getClientRects()].filter((line) => line.width && line.height)
    const center = { x: bounds.left + bounds.width / 2, y: bounds.top + bounds.height / 2 }
    const hit = document.elementFromPoint(center.x, center.y)
    const style = getComputedStyle(element)
    return {
      text: element.textContent, accessibleLabel: element.getAttribute('aria-label'),
      bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
      scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
      visible: bounds.width > 0 && bounds.height > 0 && style.visibility !== 'hidden' && style.display !== 'none',
      inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth && bounds.bottom <= innerHeight,
      center, centerHit: hit === element || (hit !== null && element.contains(hit)),
      hitElement: hit ? { tag: hit.tagName, className: hit.getAttribute('class'), text: hit.textContent } : null,
      contained: lines.length > 0 && element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => clips.every((clip) =>
        line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
    }
  })
}

async function warningEvidence(page: Page, info: TestInfo) {
  await page.evaluate(() => document.fonts.ready)
  const base = await page.locator('html').evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))
  const observations = []
  for (const scale of [1, 2]) {
    await page.evaluate((size) => document.documentElement.style.setProperty('font-size', `${size}px`, 'important'), base * scale)
    await warning(page).scrollIntoViewIfNeeded()
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    // 真实关闭按钮由 Element Plus Drawer 提供；不移动焦点、隐藏提示或裁掉遮挡物。
    const close = editor(page).locator('button.el-drawer__close-btn')
    const state = {
      viewport: page.viewportSize(),
      rootFontSize: await page.locator('html').evaluate((element) => getComputedStyle(element).fontSize),
      warning: await controlGeometry(warning(page)),
      retry: await controlGeometry(retry(page)),
      close: await controlGeometry(close),
    }
    if (process.env.NCP_CAPTURE_UI === '1') {
      const name = `site-sync-warning-${page.viewportSize()!.width}-${scale * 100}`
      const path = info.outputPath('ui-captures', `${name}.png`)
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({
        sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
        fixtureOnly: true, fixtureSource: 'frontend/e2e/site-sync-recovery.spec.ts + frontend/e2e/mockApi.ts', ...state,
      }, null, 2))
      await page.screenshot({ path, fullPage: false, animations: 'disabled' })
      await info.attach(`site-sync-warning-${scale * 100}`, { path, contentType: 'image/png' })
    }
    // 仅审计新增恢复说明与操作；保留 WCAG 正文对比度规则，不降低阈值。
    const results = await new AxeBuilder({ page }).include('.site-editor [role="status"]').include('.site-editor .el-drawer__footer').analyze()
    const blocking = results.violations.filter((item) => item.impact === 'serious' || item.impact === 'critical')
    observations.push({ scale, state, blocking })
  }
  // 先完成两种字号的实际像素与几何采集，100% 的失败也不会吞掉 200% 证据。
  for (const { scale, state, blocking } of observations) {
    expect(state.rootFontSize).toBe(`${base * scale}px`)
    for (const control of [state.warning, state.retry]) {
      expect(control.visible, JSON.stringify(control)).toBe(true)
      expect(control.contained, JSON.stringify(control)).toBe(true)
    }
    for (const control of [state.close, state.retry]) {
      expect(control.visible, JSON.stringify(control)).toBe(true)
      expect(control.inViewport, JSON.stringify(control)).toBe(true)
      expect(control.centerHit, JSON.stringify(control)).toBe(true)
    }
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
  }
  await page.evaluate((size) => document.documentElement.style.setProperty('font-size', `${size}px`, 'important'), base)
}

for (const width of [390, 1440]) {
  test(`新建成功但同步失败：${width}px 说明可读，只读重试且后续更新使用返回 ID`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    const fixture = await installSiteFixture(page)
    await page.goto('/sites')
    await openCreator(page)
    fixture.failCatalog(true)
    await editor(page).getByRole('button', { name: '添加站点', exact: true }).click()
    await expect(warning(page)).toHaveText(savedWarning)
    await expect(editor(page)).toContainText('编辑站点资料')
    expect(fixture.writes.map(({ method, path }) => `${method} ${path}`)).toEqual(['POST /api/v1/sites'])
    await expect(page.getByText('站点资料保存失败，请检查 URL 和输入内容', { exact: true })).toHaveCount(0)
    await warningEvidence(page, info)
    await editor(page).getByLabel('简介', { exact: true }).fill('同步期间继续编辑的草稿')
    const readCount = fixture.reads.length
    const failedRetry = page.waitForResponse((response) => new URL(response.url()).pathname === '/api/v1/sites' && response.status() === 503)
    await retry(page).click()
    await (await failedRetry).finished()
    await expect(retry(page)).toBeEnabled()
    await expect(warning(page)).toHaveText(savedWarning)
    await expect.poll(() => fixture.reads.slice(readCount).sort()).toEqual(['/api/v1/sites', '/api/v1/sites/ignored'])
    expect(fixture.writes).toHaveLength(1)
    fixture.failCatalog(false)
    await retry(page).click()
    await expect(save(page)).toBeEnabled()
    await expect(warning(page)).toHaveCount(0)
    await expect(editor(page).getByLabel('简介', { exact: true })).toHaveValue('同步期间继续编辑的草稿')
    expect(fixture.writes).toHaveLength(1)
    await save(page).click()
    await expect(editor(page)).toHaveCount(0)
    expect(fixture.writes.map(({ method, path }) => `${method} ${path}`)).toEqual(['POST /api/v1/sites', `PUT /api/v1/sites/${createdId}`])
    expect(fixture.writes[1]!.body.description).toBe('同步期间继续编辑的草稿')
  })
}

test('更新成功而目录失败：保留请求期间的新草稿，只读恢复不覆盖编辑', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  await page.goto('/sites')
  await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
  await editor(page).getByLabel('简介', { exact: true }).fill('已提交版本')
  fixture.failCatalog(true)
  const gate = fixture.holdCatalog()
  await save(page).click()
  await gate.received.promise
  await editor(page).getByLabel('简介', { exact: true }).fill('请求期间的新草稿')
  gate.release.resolve()
  await expect(warning(page)).toHaveText(savedWarning)
  fixture.failCatalog(false)
  await retry(page).click()
  await expect(save(page)).toBeEnabled()
  await expect(editor(page).getByLabel('简介', { exact: true })).toHaveValue('请求期间的新草稿')
  expect(fixture.writes).toEqual([{ method: 'PUT', path: `/api/v1/sites/${existingId}`, body: expect.objectContaining({ description: '已提交版本' }) }])
})

test('旧新建请求关闭后返回：不污染重新打开的现有站点编辑器和保存目标', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  await page.goto('/sites')
  await openCreator(page)
  fixture.failCatalog(true)
  const gate = fixture.holdCatalog()
  await editor(page).getByRole('button', { name: '添加站点', exact: true }).click()
  await gate.received.promise
  await editor(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(editor(page)).toHaveCount(0)
  await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
  gate.release.resolve()
  await expect(page.locator('.site-error')).toContainText('目录暂不可读')
  await expect(warning(page)).toHaveCount(0)
  await expect(editor(page).getByLabel('站点名称', { exact: true })).toHaveValue('原有站点')
  fixture.failCatalog(false)
  await save(page).click()
  await expect(editor(page)).toHaveCount(0)
  expect(fixture.writes.map(({ method, path }) => `${method} ${path}`)).toEqual(['POST /api/v1/sites', `PUT /api/v1/sites/${existingId}`])
})

test('旧只读重试关闭后返回：不关闭新建草稿，也不复用旧站点 ID', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  await page.goto('/sites')
  await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
  fixture.failCatalog(true)
  await save(page).click()
  await expect(warning(page)).toHaveText(savedWarning)
  fixture.failCatalog(false)
  const gate = fixture.holdCatalog()
  await retry(page).click()
  await gate.received.promise
  await editor(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(editor(page)).toHaveCount(0)
  await openCreator(page)
  await editor(page).getByLabel('简介', { exact: true }).fill('全新草稿')
  const response = page.waitForResponse((item) => new URL(item.url()).pathname === '/api/v1/sites' && item.request().method() === 'GET')
  gate.release.resolve()
  await (await response).finished()
  // 等待旧请求的 Promise 与 Vue 渲染都结束，再检查新编辑器。
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(page.locator('.discovery-status')).toBeVisible()
  await expect(editor(page).getByLabel('简介', { exact: true })).toHaveValue('全新草稿')
  await expect(warning(page)).toHaveCount(0)
  expect(fixture.writes).toHaveLength(1)
  await editor(page).getByRole('button', { name: '添加站点', exact: true }).click()
  await expect(editor(page)).toHaveCount(0)
  expect(fixture.writes.map(({ method, path }) => `${method} ${path}`)).toEqual([`PUT /api/v1/sites/${existingId}`, 'POST /api/v1/sites'])
  expect(fixture.writes[1]!.body.description).toBe('全新草稿')
})
