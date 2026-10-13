import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'

type Status = 'setup' | 'anonymous' | 'unavailable'
type BootstrapFailure = 'partial-503' | 'already-409' | 'network'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 所有 API 均在浏览器内截获；没有账号、NAS 或真实服务依赖，也不保存凭据请求体。
async function installBootstrapFixture(page: Page, failure: BootstrapFailure, statuses: Status[]) {
  const requests: string[] = []
  const bootstrapReceived = deferred()
  const releaseBootstrap = deferred()
  const reconciliationReceived = deferred()
  const releaseReconciliation = deferred()
  let statusCount = 0
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    requests.push(`${request.method()} ${path}`)
    if (path === '/api/v1/auth/status' && request.method() === 'GET') {
      const index = statusCount++
      if (index === 1) {
        reconciliationReceived.resolve()
        await releaseReconciliation.promise
      }
      const status = statuses[index]
      if (status === 'setup' || status === 'anonymous') {
        await route.fulfill({ json: { initialized: status === 'anonymous', authenticated: false } })
      } else {
        await route.fulfill({ status: 503, json: { code: 'AUTH_UNAVAILABLE', message: 'fixture status unavailable', requestId: 'fixture-status' } })
      }
      return
    }
    if (path === '/api/v1/auth/bootstrap' && request.method() === 'POST') {
      bootstrapReceived.resolve()
      await releaseBootstrap.promise
      if (failure === 'network') await route.abort('connectionreset')
      else await route.fulfill({
        status: failure === 'partial-503' ? 503 : 409,
        json: { code: failure === 'partial-503' ? 'AUTH_UNAVAILABLE' : 'AUTH_ALREADY_INITIALIZED', message: 'fixture bootstrap response', requestId: 'fixture-bootstrap' },
      })
      return
    }
    // 多余请求也不会漏到实际后端；下方请求账本断言会明确报错。
    await route.fulfill({ status: 500, json: { code: 'UNEXPECTED_FIXTURE_REQUEST', message: 'Unexpected request', requestId: 'fixture-unexpected' } })
  })
  return { requests, bootstrapReceived, releaseBootstrap, reconciliationReceived, releaseReconciliation }
}

type Fixture = Awaited<ReturnType<typeof installBootstrapFixture>>
const initialRequests = ['GET /api/v1/auth/status', 'POST /api/v1/auth/bootstrap', 'GET /api/v1/auth/status']

async function submitAndReconcile(page: Page, fixture: Fixture) {
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: '初始化 Root 控制台', exact: true })).toBeVisible()
  await page.getByLabel('账号', { exact: true }).fill('fixture-root')
  await page.getByLabel('密码', { exact: true }).fill('fixture-only-password')
  await page.getByLabel('确认密码', { exact: true }).fill('fixture-only-password')
  const submit = page.getByRole('button', { name: '创建 Root 账号', exact: true })
  await submit.click()
  await fixture.bootstrapReceived.promise
  await expect(submit).toBeDisabled()
  for (const input of await page.locator('.access-form input').all()) await expect(input).toBeDisabled()
  // 模拟已排队的重复 submit，确认方法级互斥，而不只依赖禁用按钮。
  await page.locator('form').dispatchEvent('submit')
  expect(fixture.requests).toEqual(initialRequests.slice(0, 2))
  fixture.releaseBootstrap.resolve()
  await fixture.reconciliationReceived.promise
  await expect(submit).toBeDisabled()
  await page.locator('form').dispatchEvent('submit')
  expect(fixture.requests).toEqual(initialRequests)
  fixture.releaseReconciliation.resolve()
}

async function expectStillAnonymous(page: Page, fixture: Fixture, statusRequests = 2) {
  await expect(page).toHaveURL(/\/system$/)
  await expect(page.locator('.access-panel')).toBeVisible()
  await expect(page.locator('.topbar')).toHaveCount(0)
  // App 的 authenticated 处理器会读取 preferences；无任何受保护请求也防止误发认证事件。
  expect(fixture.requests).toEqual([
    ...initialRequests,
    ...Array.from({ length: statusRequests - 2 }, () => 'GET /api/v1/auth/status'),
  ])
}

async function expectReadable(locator: Locator) {
  await expect(locator).toBeVisible()
  const metrics = await locator.evaluate((element) => {
    const range = document.createRange()
    range.selectNodeContents(element)
    const lines = [...range.getClientRects()].filter((rect) => rect.width && rect.height)
    const clips = [element.getBoundingClientRect()]
    for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor)
      if (/(hidden|clip|auto|scroll)/.test(`${style.overflowX} ${style.overflowY}`)) clips.push(ancestor.getBoundingClientRect())
    }
    return {
      contained: lines.every((line) => clips.every((clip) => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
      width: element.clientWidth,
      scrollWidth: element.scrollWidth,
    }
  })
  expect(metrics.contained).toBe(true)
  expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.width + 1)
}

async function recoveryEvidence(page: Page, testInfo: TestInfo, state: string) {
  await page.evaluate(() => document.fonts.ready)
  const originalFont = await page.locator('html').evaluate((element) => Number.parseFloat(getComputedStyle(element).fontSize))
  for (const scale of [1, 2]) {
    await page.evaluate((size) => document.documentElement.style.setProperty('font-size', `${size}px`, 'important'), originalFont * scale)
    await expect(page.locator('html')).toHaveCSS('font-size', `${originalFont * scale}px`)
    if (process.env.NCP_CAPTURE_UI === '1') {
      const name = `fixture-bootstrap-${state}-${page.viewportSize()!.width}-root-${scale * 100}`
      const path = testInfo.outputPath('ui-captures', `${name}.png`)
      await page.screenshot({ path, fullPage: true, animations: 'disabled' })
      await testInfo.attach(name, { path, contentType: 'image/png' })
    }
    // 检查恢复说明、状态码和操作的真实行框，不让 overflow:hidden 掩盖裁切。
    for (const locator of await page.locator('.access-description, .access-form__error, .access-form__notice, .access-error-code, .primary-action').all()) await expectReadable(locator)
    expect(await page.evaluate(() => document.body.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width)
    const results = await new AxeBuilder({ page }).analyze()
    const blocking = results.violations.filter((violation) => violation.impact === 'critical' || violation.impact === 'serious')
    expect(blocking, JSON.stringify(blocking, null, 2)).toEqual([])
  }
}

for (const scenario of [
  { failure: 'partial-503', width: 1440 },
  { failure: 'already-409', width: 390 },
] as const) {
  test(`${scenario.failure} 只读复核后进入登录，${scenario.width}px 清除密码且不启动会话`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width: scenario.width, height: 900 })
    const fixture = await installBootstrapFixture(page, scenario.failure, ['setup', 'anonymous'])
    await submitAndReconcile(page, fixture)
    await expect(page.getByRole('heading', { name: '进入 NAS Control Plane', exact: true })).toBeVisible()
    await expect(page.getByRole('status')).toHaveText('控制台已初始化，请登录。')
    await expect(page.getByLabel('密码', { exact: true })).toHaveValue('')
    await expect(page.getByLabel('确认密码', { exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: '进入控制台', exact: true })).toBeEnabled()
    await expectStillAnonymous(page, fixture)
    await recoveryEvidence(page, testInfo, scenario.failure)
    await expectStillAnonymous(page, fixture)
  })
}

test('网络结果不明但复核未初始化时保留设置，清空两个密码且不自动重试', async ({ page }) => {
  const fixture = await installBootstrapFixture(page, 'network', ['setup', 'setup'])
  await submitAndReconcile(page, fixture)
  await expect(page.getByRole('status')).toHaveText('已确认控制台尚未初始化，请检查后重新提交。')
  await expect(page.getByRole('heading', { name: '初始化 Root 控制台', exact: true })).toBeVisible()
  await expect(page.getByLabel('密码', { exact: true })).toHaveValue('')
  await expect(page.getByLabel('确认密码', { exact: true })).toHaveValue('')
  await expect(page.getByRole('button', { name: '创建 Root 账号', exact: true })).toBeEnabled()
  await expectStillAnonymous(page, fixture)
})

test('复核失败进入不可用态，重复检查只读状态并恢复登录，不重放创建或登录', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const fixture = await installBootstrapFixture(page, 'partial-503', ['setup', 'unavailable', 'unavailable', 'anonymous'])
  await submitAndReconcile(page, fixture)
  await expect(page.getByRole('heading', { name: '正式控制面尚未连接', exact: true })).toBeVisible()
  await expect(page.locator('.access-description')).toHaveText('初始化结果尚未确认，请重新检查。')
  await expect(page.locator('.access-error-code')).toHaveText('BOOTSTRAP_STATUS_UNCONFIRMED')
  await expect(page.locator('input[type="password"]')).toHaveCount(0)
  await expectStillAnonymous(page, fixture)
  await recoveryEvidence(page, testInfo, 'status-unconfirmed')
  await page.getByRole('button', { name: '重新检查', exact: true }).click()
  await expect(page.locator('.access-error-code')).toHaveText('AUTH_UNAVAILABLE')
  await expectStillAnonymous(page, fixture, 3)
  await page.getByRole('button', { name: '重新检查', exact: true }).click()
  await expect(page.getByRole('heading', { name: '进入 NAS Control Plane', exact: true })).toBeVisible()
  await expect(page.getByLabel('密码', { exact: true })).toHaveValue('')
  await expectStillAnonymous(page, fixture, 4)
})
