import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
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
const winnerSite = {
  ...site(createdId, '并发保留站点'), description: '并发写入的资料',
  iconUrl: 'data:image/svg+xml,%3Csvg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/%3E',
}
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
  let transportFailure: 'disconnect' | 'gateway' | undefined
  let iconFailure: 'conflict' | 'commit-500' | 'disconnect' | undefined
  let existingIconFailure: 'commit-500' | 'disconnect' | undefined
  const committed: Array<{ method: string; id: string }> = []
  const committedSites = new Map<string, ReturnType<typeof site>>()
  let gate: { received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; method: string; path?: string } | undefined
  await page.route('**/api/v1/sites**', async (route) => {
    const request = route.request()
    const method = request.method()
    const path = new URL(request.url()).pathname
    requests.push({ method, path })
    if (gate?.method === method && (!gate.path || gate.path === path) && path !== '/api/v1/sites/ignored') {
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
          collectedAt: '2026-10-13T12:00:00Z', sites: [existingSite, otherSite, ...(present ? [site(createdId, '新建站点')] : [])].map((item) => committedSites.get(item.id) ?? item),
          discovery: { status: 'complete', probeAvailable: true, candidateCount: 2, verifiedCount: 2, failedCount: 0, issues: [] },
        } })
      }
    } else if (existingIconFailure && method === 'POST' && path === `${pathFor(existingId)}/icon`) {
      committedSites.set(existingId, { ...existingSite, iconUrl: winnerSite.iconUrl })
      if (existingIconFailure === 'disconnect') await route.abort('connectionfailed')
      else await route.fulfill({ status: 500, json: { code: 'SITE_ICON_SAVE_FAILED', message: '提交后确认失败', requestId: 'fixture-existing-icon' } })
    } else if (iconFailure && method === 'POST' && path === `${pathFor(createdId)}/icon`) {
      // 冲突由另一个写入者获胜；另两种情况模拟提交后清理失败或确认断线。
      committedSites.set(createdId, { ...winnerSite })
      if (iconFailure === 'disconnect') await route.abort('connectionfailed')
      else await route.fulfill({
        status: iconFailure === 'conflict' ? 409 : 500,
        json: { code: iconFailure === 'conflict' ? 'SITE_ICON_CONFLICT' : 'SITE_ICON_SAVE_FAILED', message: '图标结果 fixture', requestId: 'fixture-icon' },
      })
    } else if (iconFailure && method === 'DELETE' && path === pathFor(createdId)) {
      // 若前端错误补偿，真实删除内存记录，不能靠一个永远失败的 DELETE 掩盖数据丢失。
      committedSites.delete(createdId)
      present = false
      await route.fulfill({ status: 204 })
    } else if (path.endsWith('/icon') || method === 'DELETE') {
      await route.fulfill({ status: 503, json: { code: 'AGENT_UNAVAILABLE', message: '确认不可用', requestId: 'fixture-write' } })
    } else if (transportFailure && (method === 'POST' && path === '/api/v1/sites' || method === 'PUT' && path === pathFor(existingId))) {
      // 只在内存中提交，再丢弃确认；GET 可见不等于浏览器持有可信创建 ID。
      const id = method === 'POST' ? createdId : existingId
      committed.push({ method, id })
      committedSites.set(id, { ...site(id, method === 'POST' ? '新建站点' : '原有站点'), ...request.postDataJSON(), id, projectId: id })
      if (method === 'POST') present = true
      if (transportFailure === 'disconnect') await route.abort('connectionfailed')
      else await route.fulfill({ status: 502, contentType: 'text/plain', body: 'Bad Gateway' })
    } else if (method === 'POST') {
      if (iconFailure && !malformedCreate) {
        committed.push({ method, id: createdId })
        committedSites.set(createdId, { ...site(createdId, '新建站点'), ...request.postDataJSON(), id: createdId, projectId: createdId })
        present = true
      }
      await route.fulfill(malformedCreate
        ? { status: 201, contentType: 'application/json', body: '{' }
        : { status: 201, json: { ...request.postDataJSON(), projectId: createdId } })
    } else if (method === 'PUT') {
      const id = decodeURIComponent(path.split('/').at(-1)!)
      if (transportFailure || iconFailure) committedSites.set(id, { ...site(id, id === otherId ? '其他站点' : '原有站点'), ...request.postDataJSON(), id, projectId: id })
      await route.fulfill(malformedUpdate && id === existingId
        ? { json: { ...request.postDataJSON(), projectId: otherId } }
        : { json: { ...request.postDataJSON(), projectId: id } })
    } else throw new Error(`未预期的 fixture 请求：${method} ${path}`)
  })
  return {
    requests,
    committed,
    persistedSite: () => committedSites.get(createdId),
    failExistingIcon(value: 'commit-500' | 'disconnect') { existingIconFailure = value },
    failIcon(value: 'conflict' | 'commit-500' | 'disconnect') { iconFailure = value; malformedCreate = false },
    writes: () => requests.filter(({ method }) => method !== 'GET'),
    failAfterCommit(value: 'disconnect' | 'gateway') { transportFailure = value },
    setCreateValid() { malformedCreate = false },
    setUpdateValid() { malformedUpdate = false },
    failCatalog(value: boolean) { failCatalog = value },
    catalog(presentValue: boolean, ignoredValue = false) { present = presentValue; ignored = ignoredValue },
    hold(method: string, path?: string) {
      const held = { received: deferred(), release: deferred(), method, path }
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
  await expect(warning(page)).toContainText('草稿保留在当前编辑窗口，提交和图标上传已暂停。')
  await expect(warning(page)).toContainText('只读核对仅查看当前目录，不能确认最终结果，也不会解除暂停。')
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
    await expect(warning(page)).toContainText('创建请求可能已经执行，创建结果未确认。')
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
    await expect(warning(page)).toContainText('没有可用于核对的站点 ID。创建结果仍未确认')
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
  await expect(warning(page)).toContainText('保存请求可能已经执行，保存结果未确认。')
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
  test(`创建后图标未知且不删除整站：${observation} 仅陈述本次观察`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.setCreateValid()
    await page.goto('/sites')
    await openCreator(page)
    await editor(page).getByLabel('简介', { exact: true }).fill('保留图标草稿')
    await editor(page).locator('input[type="file"]').setInputFiles({
      name: 'fixture.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>'),
    })
    await create(page).click()
    await expect(warning(page)).toContainText('站点已创建，图标上传结果未确认。请只读核对目录，勿重复添加或上传。')
    await expect(warning(page)).toContainText(`记录 ID：${createdId}`)
    await expectHeldEditor(page, '保留图标草稿')
    const writes = [
      { method: 'POST', path: '/api/v1/sites' },
      { method: 'POST', path: `${pathFor(createdId)}/icon` },
    ]
    expect(fixture.writes()).toEqual(writes)
    fixture.catalog(observation === 'present' || observation === 'both', observation === 'ignored' || observation === 'both')
    const before = fixture.requests.length
    await retry(page).click()
    const text = {
      present: '本次在站点目录中发现该 ID', ignored: '本次在已忽略列表中发现该 ID',
      absent: '本次未找到该记录，仍不能确认请求是否完成',
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

for (const failure of ['disconnect', 'gateway'] as const) {
  test(`创建内存提交后 ${failure}：保留图标草稿，反复只读核对不上传、不补偿、不重放`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.failAfterCommit(failure)
    await page.goto('/sites')
    await openCreator(page)
    await editor(page).getByLabel('简介', { exact: true }).fill('传输未确认的创建草稿')
    await editor(page).locator('input[type="file"]').setInputFiles({
      name: 'pending.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>'),
    })
    await expect(editor(page).locator('.icon-upload__preview img')).toBeVisible()
    await create(page).click()
    await expect(warning(page)).toContainText('创建请求可能已经执行，创建结果未确认。')
    await expectHeldEditor(page, '传输未确认的创建草稿')
    await expect(warning(page)).not.toContainText('记录 ID：')
    await expect(editor(page).locator('.icon-upload__preview img')).toBeVisible()
    expect(fixture.committed).toEqual([{ method: 'POST', id: createdId }])
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const before = fixture.requests.length
      await retry(page).click()
      await expect(retry(page)).toBeEnabled()
      await expect(warning(page)).toContainText('没有可用于核对的站点 ID。创建结果仍未确认')
      await expectHeldEditor(page, '传输未确认的创建草稿')
      expect(fixture.requests.slice(before)).toHaveLength(2)
      expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
      expect(fixture.writes()).toEqual([{ method: 'POST', path: '/api/v1/sites' }])
    }
    await expect(page.getByText('站点已添加', { exact: true })).toHaveCount(0)
  })

  test(`更新内存提交后 ${failure}：保留在途编辑和目标，目录可见仍不解锁`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.failAfterCommit(failure)
    await page.goto('/sites')
    await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
    await editor(page).getByLabel('简介', { exact: true }).fill('已提交的资料')
    const held = fixture.hold('PUT')
    await save(page).click()
    await held.received.promise
    await editor(page).getByLabel('简介', { exact: true }).fill('请求期间继续修改')
    held.release.resolve()
    await expect(warning(page)).toContainText('保存请求可能已经执行，保存结果未确认。')
    await expect(warning(page)).toContainText(`记录 ID：${existingId}`)
    await expectHeldEditor(page, '请求期间继续修改')
    expect(fixture.committed).toEqual([{ method: 'PUT', id: existingId }])
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const before = fixture.requests.length
      await retry(page).click()
      await expect(retry(page)).toBeEnabled()
      await expect(warning(page)).toContainText('本次在站点目录中发现该 ID')
      await expectHeldEditor(page, '请求期间继续修改')
      expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
      expect(fixture.writes()).toEqual([{ method: 'PUT', path: pathFor(existingId) }])
    }
    await expect(page.getByText('站点资料已保存', { exact: true })).toHaveCount(0)
  })

  test(`收藏内存提交后 ${failure}：只锁定该资源，读取已提交状态仍不重放`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.failAfterCommit(failure)
    await page.goto('/sites')
    const favorite = page.getByRole('button', { name: /^(取消收藏|收藏) 原有站点$/ })
    await favorite.click()
    await expect(favorite).toBeDisabled()
    await expect(page.locator('.site-error[role="status"]')).toContainText('收藏保存请求可能已经执行，结果未确认')
    expect(fixture.committed).toEqual([{ method: 'PUT', id: existingId }])
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const before = fixture.requests.length
      const reads = ['/api/v1/sites', '/api/v1/sites/ignored'].map((path) =>
        page.waitForResponse((response) => response.request().method() === 'GET' && new URL(response.url()).pathname === path).then((response) => response.finished()))
      await page.getByRole('button', { name: '只读核对目录', exact: true }).click()
      await Promise.all(reads)
      await expect.poll(() => fixture.requests.length).toBe(before + 2)
      await expect(favorite).toHaveAccessibleName('取消收藏 原有站点')
      await expect(favorite).toBeDisabled()
      expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
      expect(fixture.writes()).toEqual([{ method: 'PUT', path: pathFor(existingId) }])
    }
    await expect(page.getByText('收藏状态保存失败', { exact: true })).toHaveCount(0)
    const other = page.getByRole('button', { name: /^(取消收藏|收藏) 其他站点$/ })
    await expect(other).toBeEnabled()
    await other.click()
    await expect.poll(() => fixture.writes().length).toBe(2)
    await expect(other).toHaveAccessibleName('取消收藏 其他站点')
    await expect(other).toBeEnabled()
    expect(fixture.writes()).toEqual([{ method: 'PUT', path: pathFor(existingId) }, { method: 'PUT', path: pathFor(otherId) }])
    await expect(favorite).toBeDisabled()
  })

  test(`迟到创建 ${failure} 不污染关闭后打开的编辑器`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.failAfterCommit(failure)
    await page.goto('/sites')
    await openCreator(page)
    const held = fixture.hold('POST')
    await create(page).click()
    await held.received.promise
    await editor(page).getByRole('button', { name: '取消', exact: true }).click()
    await expect(editor(page)).toHaveCount(0)
    await page.getByRole('button', { name: '编辑 其他站点', exact: true }).click()
    const settled = failure === 'disconnect'
      ? page.waitForEvent('requestfailed', (request) => request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/sites')
      : page.waitForResponse((response) => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/sites').then((response) => response.finished())
    held.release.resolve()
    await settled
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    await expect(warning(page)).toHaveCount(0)
    await expect(editor(page).getByLabel('站点名称', { exact: true })).toHaveValue('其他站点')
    await expect(save(page)).toBeEnabled()
    await save(page).click()
    await expect(editor(page)).toHaveCount(0)
    expect(fixture.committed).toEqual([{ method: 'POST', id: createdId }])
    expect(fixture.writes()).toEqual([{ method: 'POST', path: '/api/v1/sites' }, { method: 'PUT', path: pathFor(otherId) }])
    await expect(page.getByText('站点资料保存失败，请检查 URL 和输入内容', { exact: true })).toHaveCount(0)
  })
}

for (const failure of ['conflict', 'commit-500', 'disconnect'] as const) {
  test(`创建确认后图标 ${failure}：保留获胜记录和图标草稿，只读核对不删站或重放`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.failIcon(failure)
    await page.goto('/sites')
    await openCreator(page)
    await editor(page).getByLabel('简介', { exact: true }).fill('保留的创建草稿')
    await editor(page).locator('input[type="file"]').setInputFiles({
      name: 'pending.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>'),
    })
    const preview = editor(page).locator('.icon-upload__preview img')
    await expect(preview).toBeVisible()
    const stagedPreview = await preview.getAttribute('src')
    const held = fixture.hold('POST', `${pathFor(createdId)}/icon`)
    await create(page).click()
    await held.received.promise
    expect(fixture.persistedSite()?.id).toBe(createdId)
    await editor(page).getByLabel('简介', { exact: true }).fill('图标在途时继续编辑')
    held.release.resolve()
    await expect(warning(page)).toContainText(failure === 'conflict'
      ? '站点已创建，本次图标上传未完成。请只读核对目录，再打开已有站点处理图标。'
      : '站点已创建，图标上传结果未确认。请只读核对目录，勿重复添加或上传。')
    await expect(warning(page)).toContainText(`记录 ID：${createdId}`)
    await expectHeldEditor(page, '图标在途时继续编辑')
    await expect(preview).toHaveAttribute('src', stagedPreview!)
    expect(fixture.committed).toEqual([{ method: 'POST', id: createdId }])
    const writes = [{ method: 'POST', path: '/api/v1/sites' }, { method: 'POST', path: `${pathFor(createdId)}/icon` }]
    expect(fixture.writes()).toEqual(writes)
    expect(fixture.persistedSite()).toEqual(winnerSite)
    for (const unreadable of [true, false, false]) {
      fixture.failCatalog(unreadable)
      const before = fixture.requests.length
      await retry(page).click()
      await expect(retry(page)).toBeEnabled()
      await expect(warning(page)).toContainText(unreadable ? '本次核对未完成' : '本次在站点目录中发现该 ID')
      await expectHeldEditor(page, '图标在途时继续编辑')
      await expect(preview).toHaveAttribute('src', stagedPreview!)
      expect(fixture.requests.slice(before)).toHaveLength(2)
      expect(fixture.requests.slice(before)).toEqual(expect.arrayContaining([
        { method: 'GET', path: '/api/v1/sites' }, { method: 'GET', path: '/api/v1/sites/ignored' },
      ]))
      expect(fixture.writes()).toEqual(writes)
      expect(fixture.persistedSite()).toEqual(winnerSite)
    }
    await expect(page.getByText('站点已添加', { exact: true })).toHaveCount(0)
    await editor(page).getByRole('button', { name: '关闭并查看目录', exact: true }).click()
    await expect(editor(page)).toHaveCount(0)
    await expect(page.getByRole('button', { name: '编辑 并发保留站点', exact: true })).toBeVisible()
    expect(fixture.writes()).toEqual(writes)
  })
}

test('迟到图标提交后 500 不污染新编辑器，也不删除已创建的获胜记录', async ({ page }) => {
  const fixture = await installSiteFixture(page)
  fixture.failIcon('commit-500')
  await page.goto('/sites')
  await openCreator(page)
  await editor(page).locator('input[type="file"]').setInputFiles({
    name: 'pending.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>'),
  })
  const iconPath = `${pathFor(createdId)}/icon`
  const held = fixture.hold('POST', iconPath)
  await create(page).click()
  await held.received.promise
  await editor(page).getByRole('button', { name: '取消', exact: true }).click()
  await expect(editor(page)).toHaveCount(0)
  await page.getByRole('button', { name: '编辑 其他站点', exact: true }).click()
  await editor(page).getByLabel('简介', { exact: true }).fill('新编辑器的独立草稿')
  const settled = page.waitForResponse((response) => response.request().method() === 'POST' && new URL(response.url()).pathname === iconPath)
  held.release.resolve()
  await (await settled).finished()
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await expect(warning(page)).toHaveCount(0)
  await expect(editor(page).getByLabel('站点名称', { exact: true })).toHaveValue('其他站点')
  await expect(editor(page).getByLabel('简介', { exact: true })).toHaveValue('新编辑器的独立草稿')
  await expect(editor(page).locator('input[type="file"]')).toBeEnabled()
  await expect(editor(page).locator('.icon-upload__preview img')).toHaveCount(0)
  await expect(save(page)).toBeEnabled()
  await save(page).click()
  await expect(editor(page)).toHaveCount(0)
  await expect(page.getByRole('button', { name: '编辑 并发保留站点', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '编辑 其他站点', exact: true }).click()
  await expect(editor(page).getByLabel('简介', { exact: true })).toHaveValue('新编辑器的独立草稿')
  await expect(warning(page)).toHaveCount(0)
  expect(fixture.persistedSite()).toEqual(winnerSite)
  expect(fixture.committed).toEqual([{ method: 'POST', id: createdId }])
  expect(fixture.writes()).toEqual([
    { method: 'POST', path: '/api/v1/sites' }, { method: 'POST', path: iconPath },
    { method: 'PUT', path: pathFor(otherId) },
  ])
})


for (const failure of ['commit-500', 'disconnect'] as const) {
  test(`已有站点图标 ${failure}：在途单飞，未知后核对与重开都不重放`, async ({ page }) => {
    const fixture = await installSiteFixture(page)
    fixture.failExistingIcon(failure)
    await page.goto('/sites')
    await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
    const iconPath = `${pathFor(existingId)}/icon`
    const held = fixture.hold('POST', iconPath)
    await editor(page).locator('input[type="file"]').setInputFiles({
      name: 'existing.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>'),
    })
    await held.received.promise
    await expect(editor(page).locator('input[type="file"]')).toBeDisabled()
    await expect(save(page)).toBeDisabled()
    await expect(editor(page).getByRole('status')).toContainText('图标正在上传，请等待确认。')
    expect(fixture.writes()).toEqual([{ method: 'POST', path: iconPath }])
    held.release.resolve()
    await expect(warning(page)).toContainText('图标上传可能已经执行，结果未确认')
    await expectHeldEditor(page, '原有说明')
    await expect(editor(page).getByAltText('待上传图标预览')).toBeVisible()
    for (const failed of [true, false]) {
      fixture.failCatalog(failed)
      const before = fixture.requests.length
      await retry(page).click()
      await expect(retry(page)).toBeEnabled()
      await expect(warning(page)).toContainText(failed ? '本次核对未完成' : '本次在站点目录中发现该 ID')
      expect(fixture.requests.slice(before).map(({ method }) => method)).toEqual(['GET', 'GET'])
      await expectHeldEditor(page, '原有说明')
    }
    await editor(page).getByRole('button', { name: '关闭并查看目录', exact: true }).click()
    await expect(editor(page)).toHaveCount(0)
    await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
    await expectHeldEditor(page, '原有说明')
    await editor(page).getByRole('button', { name: '关闭并查看目录', exact: true }).click()
    await expect(editor(page)).toHaveCount(0)
    await page.getByRole('button', { name: '编辑 其他站点', exact: true }).click()
    await expect(editor(page).locator('input[type="file"]')).toBeEnabled()
    await expect(save(page)).toBeEnabled()
    await expect(warning(page)).toHaveCount(0)
    expect(fixture.writes()).toEqual([{ method: 'POST', path: iconPath }])
    await expect(page.getByText('站点图标已上传', { exact: true })).toHaveCount(0)
    await expect(page.getByText('图标上传失败，请使用 2 MB 以内的 PNG、JPEG、WebP 或 SVG 文件', { exact: true })).toHaveCount(0)
  })
}

// 实际打开的未知上传编辑器；仅浏览器执行才产出像素，不用空白图标或 DOM 替身。
for (const width of [390, 1440]) {
  test(`已有站点图标未知恢复视觉证据：${width}px 预览、警告与只读操作`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    const fixture = await installSiteFixture(page)
    fixture.failExistingIcon('commit-500')
    await page.goto('/sites')
    await page.getByRole('button', { name: '编辑 原有站点', exact: true }).click()
    const icon = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="58" height="58" viewBox="0 0 58 58"><rect width="58" height="58" rx="12" fill="#176456"/><path d="M15 19h28v20H15z" fill="#fff"/><path d="M20 24h18M20 29h18M20 34h10" stroke="#176456" stroke-width="3"/></svg>')
    await editor(page).locator('input[type="file"]').setInputFiles({ name: 'existing-preview.svg', mimeType: 'image/svg+xml', buffer: icon })
    await expect(warning(page)).toContainText('图标上传可能已经执行，结果未确认')
    await expectHeldEditor(page, '原有说明')
    const preview = editor(page).getByAltText('待上传图标预览')
    await expect(preview).toBeVisible()
    await preview.evaluate((element) => (element as HTMLImageElement).decode())
    await page.evaluate(async () => {
      await document.fonts.ready
      await Promise.all(document.getAnimations().filter((animation) =>
        animation.effect?.getComputedTiming().iterations !== Infinity).map((animation) => animation.finished.catch(() => undefined)))
    })
    // 保留真实滚动位置、焦点和全部控件；不能隐藏表单或覆盖样式来消除 axe 失败。
    const state = async () => ({
      viewport: page.viewportSize(),
      document: await page.evaluate(() => ({
        fonts: document.fonts.status, rootFontSize: getComputedStyle(document.documentElement).fontSize,
        scrollX, scrollY, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth,
        focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label') },
      })),
      drawerScroll: await editor(page).locator('.el-drawer__body').evaluate((element) => ({ top: element.scrollTop, left: element.scrollLeft })),
      warning: await controlGeometry(warning(page)),
      preview: { ...await controlGeometry(preview), ...await preview.evaluate((element) => {
        const image = element as HTMLImageElement
        return { src: image.src, complete: image.complete, naturalWidth: image.naturalWidth, naturalHeight: image.naturalHeight }
      }) },
      footer: await controlGeometry(editor(page).locator('.el-drawer__footer')),
      retry: await controlGeometry(retry(page)),
      close: await controlGeometry(editor(page).getByRole('button', { name: '关闭并查看目录', exact: true })),
      dismiss: await controlGeometry(editor(page).locator('button.el-drawer__close-btn')),
      draft: await editor(page).getByLabel('简介', { exact: true }).inputValue(),
      uploadDisabled: await editor(page).locator('input[type="file"]').isDisabled(),
    })
    let previous = await state()
    await expect.poll(async () => {
      await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
      const current = await state()
      const stable = JSON.stringify(current) === JSON.stringify(previous)
      previous = current
      return stable
    }).toBe(true)
    const beforeCapture = await state()
    const requestsBefore = [...fixture.requests]
    const name = `site-existing-icon-unknown-${width}`
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    const png = info.outputPath('ui-captures', `${name}.png`)
    await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
    const afterCapture = await state()
    // 完整页面、所有默认规则、全部原始结果；不限定警告区，不剔除既有表单问题。
    const axe = await new AxeBuilder({ page }).analyze()
    const afterAxe = await state()
    const sha256 = (value: Buffer | string) => createHash('sha256').update(value).digest('hex')
    const capture = {
      sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
      workingTree: execFileSync('git', ['status', '--porcelain'], { encoding: 'utf8' }),
      fixtureOnly: true, fixtureSource: 'frontend/e2e/site-write-outcome.spec.ts + frontend/e2e/mockApi.ts',
      fixtureSha256: sha256(await readFile(new URL('./site-write-outcome.spec.ts', import.meta.url))),
      mockApiSha256: sha256(await readFile(new URL('./mockApi.ts', import.meta.url))), iconFixtureSha256: sha256(icon),
      testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
      screenshot: `${name}.png`, screenshotSha256: sha256(await readFile(png)),
      stateName: 'existing-site icon commit-500; unknown upload; original preview retained',
      beforeCapture, afterCapture, afterAxe, requestsBefore, requestsAfter: [...fixture.requests], axe,
    }
    const json = info.outputPath('ui-captures', `${name}-capture-state.json`)
    await writeFile(json, JSON.stringify(capture, null, 2))
    await info.attach(name, { path: png, contentType: 'image/png' })
    await info.attach(`${name}-state`, { path: json, contentType: 'application/json' })
    // 先保存完整失败证据，再验证几何与未筛选 axe；soft 不会将违规转换成通过。
    expect.soft(afterCapture).toEqual(beforeCapture)
    expect.soft(afterAxe).toEqual(beforeCapture)
    expect.soft(fixture.requests).toEqual(requestsBefore)
    expect.soft(fixture.writes()).toEqual([{ method: 'POST', path: `${pathFor(existingId)}/icon` }])
    expect.soft(beforeCapture.document.fonts).toBe('loaded')
    expect.soft(beforeCapture.document.scrollWidth).toBeLessThanOrEqual(beforeCapture.document.clientWidth + 1)
    expect.soft(beforeCapture.preview.complete && beforeCapture.preview.naturalWidth === 58 && beforeCapture.preview.naturalHeight === 58).toBe(true)
    expect.soft(beforeCapture.uploadDisabled).toBe(true)
    expect.soft(beforeCapture.preview.clips.every((clip) =>
      beforeCapture.preview.bounds.left >= clip.left - 1 && beforeCapture.preview.bounds.right <= clip.right + 1 &&
      beforeCapture.preview.bounds.top >= clip.top - 1 && beforeCapture.preview.bounds.bottom <= clip.bottom + 1)).toBe(true)
    for (const control of [beforeCapture.warning, beforeCapture.preview, beforeCapture.footer, beforeCapture.retry, beforeCapture.close, beforeCapture.dismiss]) {
      expect.soft(control.visible && control.inViewport, JSON.stringify(control)).toBe(true)
    }
    for (const control of [beforeCapture.warning, beforeCapture.footer, beforeCapture.retry, beforeCapture.close]) {
      expect.soft(control.contained, JSON.stringify(control)).toBe(true)
    }
    for (const control of [beforeCapture.retry, beforeCapture.close, beforeCapture.dismiss]) {
      expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
    }
    expect.soft(beforeCapture.warning.bounds.bottom).toBeLessThanOrEqual(beforeCapture.preview.bounds.top)
    expect.soft(beforeCapture.preview.bounds.bottom).toBeLessThanOrEqual(beforeCapture.footer.bounds.top)
    expect.soft(axe.violations, JSON.stringify(axe.violations, null, 2)).toEqual([])
  })
}
