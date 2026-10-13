import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'

const createdId = 'manual:unknown-fixture'
const existingId = 'manual:existing-fixture'
const otherId = 'manual:other-fixture'
const site = (id: string, name: string) => ({
  id, projectId: id, name, description: '原有说明', iconUrl: '', category: '工具',
  state: 'running', primaryPort: 8080, ports: [8080], launchUrl: 'https://fixture.invalid/app',
  favorite: false, sortOrder: 0, lastVisitedAt: null, hidden: false, source: 'manual',
})
const existingSite = site(existingId, '原有站点')
const otherSite = site(otherId, '其他站点')
const pathFor = (id: string) => `/api/v1/sites/${encodeURIComponent(id)}`
const editor = (page: Page) => page.locator('.site-editor:visible')
const warning = (page: Page) => editor(page).getByRole('status')
const retry = (page: Page) => editor(page).getByRole('button', { name: '只读核对结果', exact: true })
const save = (page: Page) => editor(page).getByRole('button', { name: '保存站点资料', exact: true })
const create = (page: Page) => editor(page).getByRole('button', { name: '添加站点', exact: true })

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 全部请求均由 fixture 接管；错误成功响应不意味着真实后端完成或回滚。
async function installSiteFixture(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: Array<{ method: string; path: string }> = []
  let malformedCreate = true
  let malformedUpdate = true
  let failCatalog = false
  let present = false
  let ignored = false
  let gate: { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; method: string } | undefined
  await page.route('**/api/v1/sites**', async (route) => {
    const request = route.request()
    const method = request.method()
    const path = new URL(request.url()).pathname
    requests.push({ method, path })
    if (gate?.method === method && path !== '/api/v1/sites/ignored') {
      const held = gate
      gate = undefined
      held.received.resolve()
      await held.release.promise
    }
    if (method === 'GET') {
      if (path === '/api/v1/sites/ignored') {
        await route.fulfill({ json: ignored ? [site(createdId, '新建站点')] : [] })
      } else if (failCatalog) {
        await route.fulfill({ status: 503, json: { code: 'AGENT_UNAVAILABLE', message: '目录暂不可读', requestId: 'fixture-catalog' } })
      } else {
        await route.fulfill({ json: {
          collectedAt: '2026-10-13T12:00:00Z', sites: [existingSite, otherSite, ...(present ? [site(createdId, '新建站点')] : [])],
          discovery: { status: 'complete', probeAvailable: true, candidateCount: 2, verifiedCount: 2, failedCount: 0, issues: [] },
        } })
      }
    } else if (path.endsWith('/icon') || method === 'DELETE') {
      await route.fulfill({ status: 503, json: { code: 'AGENT_UNAVAILABLE', message: '确认不可用', requestId: 'fixture-write' } })
    } else if (method === 'POST') {
      await route.fulfill(malformedCreate
        ? { status: 201, contentType: 'application/json', body: '{' }
        : { status: 201, json: { ...request.postDataJSON(), projectId: createdId } })
    } else if (method === 'PUT') {
      const id = decodeURIComponent(path.split('/').at(-1)!)
      await route.fulfill(malformedUpdate && id === existingId
        ? { json: { ...request.postDataJSON(), projectId: otherId } }
        : { json: { ...request.postDataJSON(), projectId: id } })
    } else throw new Error(`未预期的 fixture 请求：${method} ${path}`)
  })
  return {
    requests,
    writes: () => requests.filter(({ method }) => method !== 'GET'),
    setCreateValid() { malformedCreate = false },
    setUpdateValid() { malformedUpdate = false },
    failCatalog(value: boolean) { failCatalog = value },
    catalog(presentValue: boolean, ignoredValue = false) { present = presentValue; ignored = ignoredValue },
    hold(method: string) {
      const held = { received: deferred(), release: deferred(), method }
      gate = held
      return held
    },
  }
}

async function openCreator(page: Page) {
  await page.getByRole('button', { name: '添加站点', exact: true }).click()
  await editor(page).getByLabel('站点名称', { exact: true }).fill('新建站点')
  await editor(page).getByPlaceholder('例如 https://nas.example.com/app/；留空按 NAS 地址和端口生成').fill('https://fixture.invalid/new')
}

async function expectHeldEditor(page: Page, description: string) {
  await expect(editor(page)).toContainText('站点结果待核对')
  await expect(editor(page).getByLabel('简介', { exact: true })).toHaveValue(description)
  await expect(save(page)).toHaveCount(0)
  await expect(create(page)).toHaveCount(0)
  await expect(editor(page).locator('input[type="file"]')).toBeDisabled()
  await expect(editor(page).locator('.icon-upload__preview')).toBeDisabled()
  await expect(warning(page)).toContainText('当前草稿已保留，提交和图标上传已暂停')
  await expect(page.getByText('站点资料保存失败，请检查 URL 和输入内容', { exact: true })).toHaveCount(0)
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
      const name = `site-write-unknown-warning-${page.viewportSize()!.width}-${scale * 100}`
      const path = info.outputPath('ui-captures', `${name}.png`)
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({
        sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
        fixtureOnly: true, fixtureSource: 'frontend/e2e/site-write-outcome.spec.ts + frontend/e2e/mockApi.ts', ...state,
      }, null, 2))
      await page.screenshot({ path, fullPage: false, animations: 'disabled' })
      await info.attach(`site-write-unknown-warning-${scale * 100}`, { path, contentType: 'image/png' })
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
  test(`创建 201 无效 JSON：${width}px 未知警告可读，只读核对不重放`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    const fixture = await installSiteFixture(page)
    await page.goto('/sites')
    await openCreator(page)
    await editor(page).getByLabel('简介', { exact: true }).fill('提交的草稿')
    await create(page).click()
    await expect(warning(page)).toContainText('创建请求返回成功状态，但响应无效，创建结果未确认。')
    await expectHeldEditor(page, '提交的草稿')
    await warningEvidence(page, info)
    await editor(page).getByLabel('简介', { exact: true }).fill('未知期间继续编辑')
    const writes = [{ method: 'POST', path: '/api/v1/sites' }]
    expect(fixture.writes()).toEqual(writes)
    fixture.failCatalog(true)
    let before = fixture.requests.length
    await retry(page).click()
    await expect(warning(page)).toContainText('本次核对未完成，结果仍未确认')
    expect(fixture.requests.slice(before)).toEqual(expect.arrayContaining([
      { method: 'GET', path: '/api/v1/sites' }, { method: 'GET', path: '/api/v1/sites/ignored' },
    ]))
    expect(fixture.requests.slice(before)).toHaveLength(2)
    fixture.failCatalog(false)
    fixture.catalog(true)
    before = fixture.requests.length
    await retry(page).click()
    await expect(warning(page)).toContainText('创建响应没有可信 ID，无法确认本次创建结果')
    await expectHeldEditor(page, '未知期间继续编辑')
    expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
    expect(fixture.writes()).toEqual(writes)
  })
}

test('更新 200 返回错误 ID：保留请求期间的编辑，核对存在仍不解锁提交', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  await page.goto('/sites')
  await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
  await editor(page).getByLabel('简介', { exact: true }).fill('已提交版本')
  const held = fixture.hold('PUT')
  await save(page).click()
  await held.received.promise
  await editor(page).getByLabel('简介', { exact: true }).fill('请求期间的新草稿')
  held.release.resolve()
  await expect(warning(page)).toContainText('保存请求返回成功状态，但响应无效，保存结果未确认。')
  await expect(warning(page)).toContainText(`记录 ID：${existingId}`)
  await expectHeldEditor(page, '请求期间的新草稿')
  const before = fixture.requests.length
  await retry(page).click()
  await expect(warning(page)).toContainText('本次在站点目录中发现该 ID')
  await expectHeldEditor(page, '请求期间的新草稿')
  expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
  expect(fixture.writes()).toEqual([{ method: 'PUT', path: pathFor(existingId) }])
})

for (const observation of ['present', 'ignored', 'absent', 'both'] as const) {
  test(`创建后图标失败且单次补偿失败：${observation} 仅陈述本次观察`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.setCreateValid()
    await page.goto('/sites')
    await openCreator(page)
    await editor(page).getByLabel('简介', { exact: true }).fill('保留图标草稿')
    await editor(page).locator('input[type="file"]').setInputFiles({
      name: 'fixture.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>'),
    })
    await create(page).click()
    await expect(warning(page)).toContainText('站点已创建，图标请求失败，补偿删除结果未确认。图标请求仍可能在服务端完成。')
    await expect(warning(page)).toContainText(`记录 ID：${createdId}`)
    await expectHeldEditor(page, '保留图标草稿')
    const writes = [
      { method: 'POST', path: '/api/v1/sites' },
      { method: 'POST', path: `${pathFor(createdId)}/icon` },
      { method: 'DELETE', path: pathFor(createdId) },
    ]
    expect(fixture.writes()).toEqual(writes)
    fixture.catalog(observation === 'present' || observation === 'both', observation === 'ignored' || observation === 'both')
    const before = fixture.requests.length
    await retry(page).click()
    const text = {
      present: '本次在站点目录中发现该 ID', ignored: '本次在已忽略列表中发现该 ID',
      absent: '本次未发现该记录；这不证明补偿成功、未来不会出现或图标文件已清理',
      both: '本次两份列表均发现该 ID，状态可能正在变化',
    }[observation]
    await expect(warning(page)).toContainText(text)
    await expectHeldEditor(page, '保留图标草稿')
    expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
    expect(fixture.writes()).toEqual(writes)
    await retry(page).click()
    await expect(retry(page)).toBeEnabled()
    expect(fixture.writes()).toEqual(writes)
  })
}

test('收藏未知结果按资源锁定：其他站点仍可保存，目录核对不重放或解锁', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  await page.goto('/sites')
  const favorite = page.getByRole('button', { name: '收藏 原有站点', exact: true })
  await favorite.click()
  await expect(favorite).toBeDisabled()
  await expect(page.locator('.site-error[role="status"]')).toContainText('结果未确认')
  const other = page.getByRole('button', { name: '收藏 其他站点', exact: true })
  await expect(other).toBeEnabled()
  await other.click()
  await expect(other).toBeEnabled()
  await expect.poll(() => fixture.writes()).toEqual([
    { method: 'PUT', path: pathFor(existingId) }, { method: 'PUT', path: pathFor(otherId) },
  ])
  const before = fixture.requests.length
  await page.getByRole('button', { name: '只读核对目录', exact: true }).click()
  await expect.poll(() => fixture.requests.length).toBe(before + 2)
  await expect(favorite).toBeDisabled()
  expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
  expect(fixture.writes()).toHaveLength(2)
})

test('迟到未知创建结果不污染关闭后打开的其他站点编辑器', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  await page.goto('/sites')
  await openCreator(page)
  const held = fixture.hold('POST')
  await create(page).click()
  await held.received.promise
  await editor(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(editor(page)).toHaveCount(0)
  await page.getByRole('button', { name: '编辑 其他站点', exact: true }).click()
  const response = page.waitForResponse((item) => item.request().method() === 'POST' && new URL(item.url()).pathname === '/api/v1/sites')
  held.release.resolve()
  await (await response).finished()
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(warning(page)).toHaveCount(0)
  await expect(editor(page).getByLabel('站点名称', { exact: true })).toHaveValue('其他站点')
  await expect(save(page)).toBeEnabled()
  await save(page).click()
  await expect(editor(page)).toHaveCount(0)
  expect(fixture.writes()).toEqual([{ method: 'POST', path: '/api/v1/sites' }, { method: 'PUT', path: pathFor(otherId) }])
})

test('迟到只读核对不污染关闭后新开的草稿或复用未知 ID', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  await page.goto('/sites')
  await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
  await save(page).click()
  await expect(warning(page)).toContainText('保存结果未确认')
  const held = fixture.hold('GET')
  await retry(page).click()
  await held.received.promise
  await editor(page).getByRole('button', { name: '关闭并查看目录', exact: true }).click()
  await expect(editor(page)).toHaveCount(0)
  await openCreator(page)
  await editor(page).getByLabel('简介', { exact: true }).fill('全新草稿')
  const response = page.waitForResponse((item) => item.request().method() === 'GET' && new URL(item.url()).pathname === '/api/v1/sites')
  held.release.resolve()
  await (await response).finished()
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(warning(page)).toHaveCount(0)
  await expect(editor(page).getByLabel('简介', { exact: true })).toHaveValue('全新草稿')
  fixture.setCreateValid()
  await create(page).click()
  await expect(editor(page)).toHaveCount(0)
  expect(fixture.writes()).toEqual([{ method: 'PUT', path: pathFor(existingId) }, { method: 'POST', path: '/api/v1/sites' }])
})
