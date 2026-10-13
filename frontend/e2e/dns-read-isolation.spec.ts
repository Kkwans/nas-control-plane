import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi, systemDetails } from './mockApi'

const detailsPath = '/api/v1/system/details'
const root = '/api/v1/system/dns'
const dnsPath = `${root}/capability`
const inspectPath = '/api/v1/proxy/mihomo/inspect'
const backend = 'ugos-network-service'
const nameservers = ['192.0.2.1']
const capability = { ...systemDetails.dns, backend, readOnly: false, canRead: true, canPreview: true, canConfirm: true, canRollback: true, nameservers, configuredNameservers: nameservers, errorCode: '' }
const endpoint = (values: string[]) => ({ interface: '', connectionId: '', nameservers: values, searchDomains: [] })
const dialog = (page: Page) => page.getByRole('dialog', { name: '编辑 DNS 服务器', exact: true })
const button = (page: Page, name: string) => dialog(page).getByRole('button', { name, exact: true })
const input = (page: Page) => dialog(page).getByLabel('DNS 服务器', { exact: false })
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

async function fixture(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const calls: string[] = []
  const unexpected: string[] = []
  const pageErrors: string[] = []
  const gates: Array<{ path: string; status: number; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> }> = []
  let previewDraft: string[] = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  // 只允许此页依赖的 GET 落入底层内存 fixture；其他请求 fail closed，绝不连接 NAS。
  const baseReads = new Set(['/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/summary', '/api/v1/system/capabilities', '/api/v1/docker/inventory'])
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const key = `${request.method()} ${path}`
    calls.push(key)
    const index = gates.findIndex((gate) => gate.path === path)
    if (index >= 0) {
      const gate = gates.splice(index, 1)[0]!
      gate.received.resolve()
      await gate.release.promise
      if (gate.status !== 200) return route.fulfill({ status: gate.status, json: { code: 'FIXTURE_READ_UNAVAILABLE', message: 'fixture read unavailable', requestId: 'fixture-dns-isolation' } })
    }
    if (key === `GET ${detailsPath}`) return route.fulfill({ json: { ...systemDetails, dns: capability } })
    if (key === `GET ${dnsPath}`) return route.fulfill({ json: capability })
    if (key === `POST ${inspectPath}`) return route.fallback()
    if (key === `POST ${root}/preview`) {
      previewDraft = (request.postDataJSON() as { nameservers: string[] }).nameservers
      return route.fulfill({ json: { previewId: 'fixture-preview', backend, before: endpoint(nameservers), after: endpoint(previewDraft), requiresConfirm: true, rollbackAvailable: true, expiresAt: '2099-01-01T00:00:00Z', errorCode: '' } })
    }
    if (key === `POST ${root}/confirm`) return route.fulfill({ json: { changeId: 'fixture-change', backend, applied: true, rollbackAvailable: true, appliedAt: '2026-10-13T12:00:00Z', errorCode: '' } })
    if (request.method() === 'GET' && baseReads.has(path)) return route.fallback()
    unexpected.push(key)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: 'unlisted fixture request' } })
  })
  return {
    calls, unexpected, pageErrors,
    counts: () => ({ details: calls.filter((key) => key === `GET ${detailsPath}`).length, dns: calls.filter((key) => key === `GET ${dnsPath}`).length, inspect: calls.filter((key) => key === `POST ${inspectPath}`).length }),
    writes: () => calls.filter((key) => key.startsWith(`POST ${root}/`)),
    hold(path: string, status = 200) {
      const gate = { path, status, received: deferred(), release: deferred() }
      gates.push(gate)
      return gate
    },
    async release(gate: (typeof gates)[number]) {
      const response = page.waitForResponse((value) => new URL(value.url()).pathname === gate.path)
      gate.release.resolve()
      await (await response).finished()
    },
  }
}

async function evidence(page: Page, info: TestInfo, outcome: string, scope: string) {
  const observations = []
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.evaluate(() => document.fonts.ready)
    await page.locator(scope).scrollIntoViewIfNeeded()
    const controls = await page.locator(scope).locator('input, button, span, p').evaluateAll((elements) => elements.map((element) => {
      const bounds = element.getBoundingClientRect()
      return { text: element.textContent, bounds: bounds.toJSON(), visible: bounds.width > 0 && bounds.height > 0, inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth && bounds.bottom <= innerHeight, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth }
    }))
    const bounds = await page.locator(scope).evaluate((element) => {
      const rect = element.getBoundingClientRect()
      return { left: rect.left, right: rect.right, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth }
    })
    const axe = await new AxeBuilder({ page }).include(scope).analyze()
    const state = { sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), fixtureOnly: true, fixtureSource: 'frontend/e2e/dns-read-isolation.spec.ts + frontend/e2e/mockApi.ts', outcome, viewport: page.viewportSize(), bounds, text: await page.locator(scope).innerText(), controls, axeViolations: axe.violations }
    observations.push(state)
    const name = `dns-read-${outcome}-${width}`
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify(state, null, 2))
    const path = info.outputPath('ui-captures', `${name}.png`)
    await page.screenshot({ path, animations: 'disabled' })
    await info.attach(name, { path, contentType: 'image/png' })
  }
  // 两档状态与截图先保存，再做布局和 scoped axe 断言。
  for (const state of observations) {
    expect(state.controls.filter((control) => control.visible).every((control) => control.inViewport && control.scrollWidth <= control.clientWidth + 1), `${outcome} ${state.viewport?.width}px`).toBe(true)
    expect(state.bounds.left).toBeGreaterThanOrEqual(0)
    expect(state.bounds.right).toBeLessThanOrEqual((state.viewport?.width ?? 0) + 1)
    expect(state.bounds.scrollWidth).toBeLessThanOrEqual(state.bounds.clientWidth + 1)
    expect(state.axeViolations).toEqual([])
  }
}

async function network(page: Page) {
  await page.getByRole('button', { name: '网络与代理', exact: true }).click()
}

function clean(api: Awaited<ReturnType<typeof fixture>>) {
  expect(api.unexpected).toEqual([])
  expect(api.pageErrors).toEqual([])
}

test('详情成功不等待 DNS：设备可见、探测已启动、伪可写 embedded DNS 不授权', async ({ page }, info) => {
  const api = await fixture(page)
  const dns = api.hold(dnsPath)
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await dns.received.promise
  await expect(page.getByText(systemDetails.device.model, { exact: true })).toBeVisible()
  await expect(page.getByLabel('正在加载系统详情')).toHaveCount(0)
  await expect.poll(api.counts).toEqual({ details: 1, dns: 1, inspect: 1 })
  await network(page)
  await expect(page.getByTestId('dns-read-status')).toContainText('DNS 能力读取中')
  await expect(page.getByRole('button', { name: '编辑 DNS', exact: true })).toHaveCount(0)
  await expect(page.locator('.dns-workspace')).not.toContainText('支持安全修改')
  await evidence(page, info, 'pending', '[data-testid="dns-read-status"]')
  expect(api.writes()).toEqual([])
  await api.release(dns)
  await expect(page.getByRole('button', { name: '编辑 DNS', exact: true })).toBeVisible()
  clean(api)
})

test('DNS 失败保留详情；目标重试只增加 DNS GET，不重读详情或探测', async ({ page }, info) => {
  const api = await fixture(page)
  const failed = api.hold(dnsPath, 503)
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await failed.received.promise
  await api.release(failed)
  await expect(page.getByText(systemDetails.device.model, { exact: true })).toBeVisible()
  await expect.poll(api.counts).toEqual({ details: 1, dns: 1, inspect: 1 })
  await expect(page.getByTestId('dns-read-status')).toContainText('DNS 能力读取失败')
  await evidence(page, info, 'failure-retry', '[data-testid="dns-read-status"]')
  const retry = api.hold(dnsPath)
  await page.getByRole('button', { name: '重试 DNS 能力', exact: true }).click()
  await retry.received.promise
  expect(api.counts()).toEqual({ details: 1, dns: 2, inspect: 1 })
  await api.release(retry)
  await expect(page.getByTestId('dns-read-status')).toHaveCount(0)
  await network(page)
  await expect(page.getByRole('button', { name: '编辑 DNS', exact: true })).toBeVisible()
  expect(api.counts()).toEqual({ details: 1, dns: 2, inspect: 1 })
  expect(api.writes()).toEqual([])
  clean(api)
})

test('详情失败与 DNS 成功各自保留；详情目标重试不重读 DNS', async ({ page }, info) => {
  const api = await fixture(page)
  const failed = api.hold(detailsPath, 503)
  const dns = api.hold(dnsPath)
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await Promise.all([failed.received.promise, dns.received.promise])
  await api.release(dns)
  await api.release(failed)
  await expect(page.getByTestId('dns-read-status')).toHaveCount(0)
  await expect(page.locator('.details-error')).toContainText('系统详情暂不可用')
  expect(api.counts()).toEqual({ details: 1, dns: 1, inspect: 0 })
  await evidence(page, info, 'details-failure-retry', '.details-error')
  const retry = api.hold(detailsPath)
  await page.getByRole('button', { name: '重试系统详情', exact: true }).click()
  await retry.received.promise
  expect(api.counts()).toEqual({ details: 2, dns: 1, inspect: 0 })
  await api.release(retry)
  await expect(page.getByText(systemDetails.device.model, { exact: true })).toBeVisible()
  await expect.poll(api.counts).toEqual({ details: 2, dns: 1, inspect: 1 })
  await network(page)
  await expect(page.getByRole('button', { name: '编辑 DNS', exact: true })).toBeVisible()
  expect(api.writes()).toEqual([])
  clean(api)
})

test('编辑器内 DNS 重试保留已知应用结果与草稿，等待期间禁止新写', async ({ page }, info) => {
  const api = await fixture(page)
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await network(page)
  await page.getByRole('button', { name: '编辑 DNS', exact: true }).click()
  await expect.poll(api.counts).toEqual({ details: 1, dns: 1, inspect: 1 })
  await input(page).fill('192.0.2.2')
  await button(page, '预览修改').click()
  await expect(button(page, '确认应用')).toBeEnabled()
  const failed = api.hold(dnsPath, 503)
  const primary = api.hold(detailsPath)
  await button(page, '确认应用').click()
  await Promise.all([failed.received.promise, primary.received.promise])
  await api.release(failed)
  await expect(dialog(page).locator('.dns-editor__message')).toHaveText('DNS 已应用。')
  await expect(page.getByTestId('dns-dialog-read-status')).toContainText('DNS 能力读取失败')
  await evidence(page, info, 'editor-failure-retry', '.dns-editor-dialog')
  const before = api.counts()
  const retry = api.hold(dnsPath)
  await button(page, '重试 DNS 能力').click()
  await retry.received.promise
  await expect(page.getByTestId('dns-dialog-read-status')).toContainText('DNS 能力正在复核')
  await expect(input(page)).toHaveValue('192.0.2.2')
  await expect(dialog(page).locator('.dns-editor__message')).toHaveText('DNS 已应用。')
  await expect(button(page, '预览修改')).toBeDisabled()
  await expect(button(page, '回滚上次修改')).toBeDisabled()
  await button(page, '预览修改').dispatchEvent('click')
  await button(page, '回滚上次修改').dispatchEvent('click')
  await expect(button(page, '确认应用')).toHaveCount(0)
  await api.release(retry)
  // 详情仍挂起，独立 DNS 已成功即可恢复权限，不复活已消费预览。
  await expect(button(page, '回滚上次修改')).toBeEnabled()
  await expect(button(page, '预览修改')).toBeEnabled()
  await expect(button(page, '确认应用')).toHaveCount(0)
  await expect(input(page)).toHaveValue('192.0.2.2')
  await expect(dialog(page).locator('.dns-editor__message')).toHaveText('DNS 已应用。')
  expect(api.counts()).toEqual({ ...before, dns: before.dns + 1 })
  expect(api.writes()).toEqual([`POST ${root}/preview`, `POST ${root}/confirm`])
  await api.release(primary)
  await expect.poll(api.counts).toEqual({ details: 2, dns: 3, inspect: 2 })
  clean(api)
})
