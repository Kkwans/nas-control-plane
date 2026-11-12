import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'

const usersPath = '/api/v1/users'
const policyPath = `${usersPath}/password-policy`
const policy = { minLength: 8, requireUppercase: false, requireLowercase: false, requireDigit: false, requireSpecial: false }
const account = { id: 42, username: 'isolated-operator', role: 'root', disabled: false, createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:00:00Z', lastLoginAt: null }
const policyPanel = (page: Page) => page.locator('.password-policy')
const usersPanel = (page: Page) => page.locator('.users-panel')
const savePolicy = (page: Page) => policyPanel(page).getByRole('button', { name: '保存规则', exact: true })
const retryPolicy = (page: Page) => policyPanel(page).getByRole('button', { name: '重试', exact: true })
const lengthInput = (page: Page) => policyPanel(page).getByRole('spinbutton')
const create = (page: Page) => usersPanel(page).getByRole('button', { name: '新增账号', exact: true })
const reset = (page: Page) => usersPanel(page).getByRole('button', { name: '重置密码', exact: true })
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 全部用户 API 都由内存 fixture 接管，不访问真实账号和凭据。
async function installUsersFixture(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: Array<{ method: string; path: string; body?: unknown }> = []
  let usersStatus = 200
  let policyStatus = 200
  let policyBody: unknown = { ...policy }
  let invalidSave = false
  let gate: { path: string; method: string; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> } | undefined
  await page.route('**/api/v1/users**', async (route) => {
    const request = route.request()
    const method = request.method()
    const path = new URL(request.url()).pathname
    const body: unknown = method === 'GET' ? undefined : request.postDataJSON()
    requests.push({ method, path, ...(body === undefined ? {} : { body }) })
    if (gate?.path === path && gate.method === method) {
      const held = gate
      gate = undefined
      held.received.resolve()
      await held.release.promise
    }
    if (method === 'GET' && path === usersPath) {
      await route.fulfill({ status: usersStatus, json: usersStatus === 200 ? [account] : { code: 'UNAVAILABLE', message: 'fixture users unavailable' } })
    } else if (method === 'GET' && path === policyPath) {
      await route.fulfill({ status: policyStatus, json: policyStatus === 200 ? policyBody : { code: 'UNAVAILABLE', message: 'fixture policy unavailable' } })
    } else if (method === 'PUT' && path === policyPath) {
      policyBody = body
      await route.fulfill({ json: invalidSave ? { minLength: 'invalid' } : body })
    } else {
      await route.fulfill({ status: 503, json: { code: 'FIXTURE_WRITE_BLOCKED', message: 'unexpected fixture write' } })
    }
  })
  return {
    requests, writes: () => requests.filter(({ method }) => method !== 'GET'),
    usersStatus(value: number) { usersStatus = value },
    policyStatus(value: number) { policyStatus = value },
    policyBody(value: unknown) { policyBody = value },
    invalidSave() { invalidSave = true },
    hold(path: string, method = 'GET') {
      const held = { path, method, received: deferred(), release: deferred() }
      gate = held
      return held
    },
  }
}
async function expectLocked(page: Page) {
  await expect(savePolicy(page)).toBeDisabled()
  await expect(create(page)).toBeDisabled()
  await expect(reset(page)).toBeDisabled()
  await expect(usersPanel(page).getByRole('button', { name: '更多账号操作' })).toBeEnabled()
}
async function geometry(locator: Locator) {
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
    const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
    const style = getComputedStyle(element)
    return {
      text: element.textContent, bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
      visible: bounds.width > 0 && bounds.height > 0 && style.visibility !== 'hidden' && style.display !== 'none',
      inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth && bounds.bottom <= innerHeight,
      centerHit: hit === element || (hit !== null && element.contains(hit)),
      contained: lines.length > 0 && element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => clips.every((clip) =>
        line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
    }
  })
}
async function errorEvidence(page: Page, info: TestInfo) {
  await page.evaluate(() => document.fonts.ready)
  const base = await page.locator('html').evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))
  const observations = []
  for (const scale of [1, 2]) {
    await page.evaluate((size) => document.documentElement.style.setProperty('font-size', `${size}px`, 'important'), base * scale)
    const alert = policyPanel(page).getByRole('alert')
    await alert.scrollIntoViewIfNeeded()
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    const state = {
      viewport: page.viewportSize(), rootFontSize: await page.locator('html').evaluate((element) => getComputedStyle(element).fontSize),
      title: await geometry(alert.locator('strong')), message: await geometry(alert.locator('p')), retry: await geometry(retryPolicy(page)),
      documentOverflow: await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1),
    }
    // 全部字号的截图和行框先采集，随后断言；不隐藏遮挡、不修改对比度。
    if (process.env.NCP_CAPTURE_UI === '1') {
      const name = `user-policy-read-error-${page.viewportSize()!.width}-${scale * 100}`
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      const path = info.outputPath('ui-captures', `${name}.png`)
      await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({
        sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
        fixtureOnly: true, fixtureSource: 'frontend/e2e/user-read-isolation.spec.ts + frontend/e2e/mockApi.ts', ...state,
      }, null, 2))
      await page.screenshot({ path, fullPage: false, animations: 'disabled' })
      await info.attach(name, { path, contentType: 'image/png' })
    }
    const results = await new AxeBuilder({ page }).include('.password-policy .resource-state').analyze()
    observations.push({ scale, state, blocking: results.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical') })
  }
  for (const { scale, state, blocking } of observations) {
    expect(state.rootFontSize).toBe(`${base * scale}px`)
    expect(state.documentOverflow).toBe(false)
    for (const item of [state.title, state.message, state.retry]) {
      expect(item.visible, JSON.stringify(item)).toBe(true)
      expect(item.contained, JSON.stringify(item)).toBe(true)
    }
    expect(state.retry.inViewport, JSON.stringify(state.retry)).toBe(true)
    expect(state.retry.centerHit, JSON.stringify(state.retry)).toBe(true)
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
  }
  await page.evaluate((size) => document.documentElement.style.setProperty('font-size', `${size}px`, 'important'), base)
}

test('规则挂起时账号独立显示；503 不隐藏账号且重试仅 GET 规则', async ({ page }) => {
  const fixture = await installUsersFixture(page)
  fixture.policyStatus(503)
  const held = fixture.hold(policyPath)
  await page.goto('/users')
  await held.received.promise
  try {
    await expect(usersPanel(page).getByText(account.username, { exact: true })).toBeVisible()
    await expect(policyPanel(page).getByRole('status')).toContainText('正在加载密码规则')
    await expectLocked(page)
  } finally { held.release.resolve() }
  await expect(policyPanel(page).getByRole('alert')).toContainText('密码规则加载失败')
  await expectLocked(page)
  const before = fixture.requests.length
  fixture.policyStatus(200)
  await retryPolicy(page).click()
  await expect(savePolicy(page)).toBeEnabled()
  await expect(create(page)).toBeEnabled()
  await expect(reset(page)).toBeEnabled()
  expect(fixture.requests.slice(before)).toEqual([{ method: 'GET', path: policyPath }])
  expect(fixture.writes()).toEqual([])
})

test('账号 503 不遮蔽已确认规则；重试只 GET 账号', async ({ page }) => {
  const fixture = await installUsersFixture(page)
  fixture.usersStatus(503)
  await page.goto('/users')
  await expect(usersPanel(page).getByRole('alert')).toContainText('账号列表加载失败')
  await expect(lengthInput(page)).toHaveValue('8')
  await expect(savePolicy(page)).toBeEnabled()
  await expect(create(page)).toBeEnabled()
  const before = fixture.requests.length
  fixture.usersStatus(200)
  await usersPanel(page).getByRole('button', { name: '重试', exact: true }).click()
  await expect(usersPanel(page).getByText(account.username, { exact: true })).toBeVisible()
  expect(fixture.requests.slice(before)).toEqual([{ method: 'GET', path: usersPath }])
  expect(fixture.writes()).toEqual([])
})

for (const width of [390, 1440]) {
  test(`坏规则响应关闭写入口，${width}px 100/200% 错误恢复可读`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    const fixture = await installUsersFixture(page)
    fixture.policyBody({ ...policy, requireDigit: 'false' })
    await page.goto('/users')
    await expect(policyPanel(page).getByRole('alert')).toBeVisible()
    await errorEvidence(page, info)
    await expectLocked(page)
    await savePolicy(page).dispatchEvent('click')
    expect(fixture.writes()).toEqual([])
  })
}

test('保存快照 A 时继续编辑 B；确认规则使用 A 且不覆盖 B', async ({ page }) => {
  const fixture = await installUsersFixture(page)
  await page.goto('/users')
  await expect(lengthInput(page)).toHaveValue('8')
  await lengthInput(page).fill('10')
  await lengthInput(page).press('Tab')
  const held = fixture.hold(policyPath, 'PUT')
  await savePolicy(page).click()
  await held.received.promise
  try {
    await expect(savePolicy(page)).toBeDisabled()
    await expect(create(page)).toBeDisabled()
    await lengthInput(page).fill('12')
    await lengthInput(page).press('Tab')
  } finally { held.release.resolve() }
  await expect(savePolicy(page)).toBeEnabled()
  await expect(lengthInput(page)).toHaveValue('12')
  await expect(policyPanel(page).getByRole('status')).toContainText('有未保存的规则修改')
  await create(page).click()
  await expect(page.getByRole('dialog').getByPlaceholder('至少 10 个字符', { exact: true })).toBeVisible()
  expect(fixture.writes()).toEqual([{ method: 'PUT', path: policyPath, body: { ...policy, minLength: 10 } }])
})

for (const mode of ['create', 'password'] as const) {
  test(`无效保存 2xx 后 ${mode} 提交关闭；仅 GET 恢复且保留草稿`, async ({ page }) => {
    const fixture = await installUsersFixture(page)
    await page.goto('/users')
    await expect(savePolicy(page)).toBeEnabled()
    await lengthInput(page).fill('12')
    await lengthInput(page).press('Tab')
    await (mode === 'create' ? create(page) : reset(page)).click()
    const dialog = page.getByRole('dialog')
    const submit = dialog.getByRole('button', { name: mode === 'create' ? '创建账号' : '保存新密码', exact: true })
    if (mode === 'create') {
      await dialog.getByPlaceholder('例如 operator').fill('fixture-new-user')
      await dialog.locator('input[type="password"]').fill('Fixture-only-123!')
    } else {
      await dialog.locator('input[type="password"]').nth(0).fill('Fixture-only-123!')
      await dialog.locator('input[type="password"]').nth(1).fill('Fixture-only-123!')
    }
    fixture.invalidSave()
    // 合成事件仅模拟弹窗期间规则确认失效，不代表用户能点击遮罩后的保存按钮。
    // 背景区域可能 aria-hidden，因此这里只读取 DOM 状态；实际错误可读性由上方独立测试验证。
    await policyPanel(page).locator('button').filter({ hasText: '保存规则' }).dispatchEvent('click')
    await expect(policyPanel(page).locator('.resource-state[role="alert"]')).toContainText('规则保存结果未确认')
    await expect(submit).toBeDisabled()
    await submit.dispatchEvent('click')
    await policyPanel(page).locator('button').filter({ hasText: '保存规则' }).dispatchEvent('click')
    expect(fixture.writes()).toEqual([{ method: 'PUT', path: policyPath, body: { ...policy, minLength: 12 } }])
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    fixture.policyBody({ ...policy, minLength: 10 })
    const before = fixture.requests.length
    await retryPolicy(page).click()
    await expect(savePolicy(page)).toBeEnabled()
    await expect(lengthInput(page)).toHaveValue('12')
    await expect(create(page)).toBeEnabled()
    await expect(reset(page)).toBeEnabled()
    expect(fixture.requests.slice(before)).toEqual([{ method: 'GET', path: policyPath }])
    expect(fixture.writes()).toHaveLength(1)
    await (mode === 'create' ? create(page) : reset(page)).click()
    await expect(page.getByRole('dialog').getByPlaceholder('至少 10 个字符', { exact: true })).toBeVisible()
    await expect(page.getByRole('dialog').getByRole('button', { name: mode === 'create' ? '创建账号' : '保存新密码', exact: true })).toBeEnabled()
  })
}
