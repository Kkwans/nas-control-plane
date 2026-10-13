import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi, systemDetails } from './mockApi'

const root = '/api/v1/system/dns'
const detailsPath = '/api/v1/system/details'
const capabilityPath = `${root}/capability`
const backend = 'ugos-network-service'
const changeId = 'fixture-dns-change-a'
const initial = ['192.0.2.1']
const draftA = '192.0.2.2'
const draftB = '192.0.2.3'
const capability = { ...systemDetails.dns, backend, readOnly: false, canRead: true, canPreview: true, canConfirm: true, canRollback: true, nameservers: initial, configuredNameservers: initial, errorCode: '' }
const endpoint = (nameservers: string[]) => ({ interface: '', connectionId: '', nameservers, searchDomains: [] })
const dialog = (page: Page) => page.getByRole('dialog', { name: '编辑 DNS 服务器', exact: true })
const input = (page: Page) => dialog(page).getByLabel('DNS 服务器', { exact: false })
const button = (page: Page, name: string) => dialog(page).getByRole('button', { name, exact: true })
const status = (page: Page) => dialog(page).locator('.dns-editor__message[role="status"]')
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 底层 mock 接管全部 /api/v1/**，此层明确拒绝未列出的 DNS 写入；没有真实网络设置或代理探测。
async function fixture(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const writes: Array<{ path: string; body: unknown }> = []
  const gates: Array<{ path: string; statusCode: number; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> }> = []
  const previews = new Map<string, string[]>()
  let configured = [...initial]
  let failCapability = false
  let mismatchRollback = false
  let previewSequence = 0
  await page.route('**/api/v1/system/{dns/**,details}', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    if (method === 'POST') writes.push({ path, body })
    const index = gates.findIndex((gate) => gate.path === path)
    if (index >= 0) {
      const held = gates.splice(index, 1)[0]!
      held.received.resolve()
      await held.release.promise
      if (held.statusCode !== 200) return route.fulfill({ status: held.statusCode, json: { code: 'FIXTURE_RESPONSE_UNKNOWN', message: 'fixture response unavailable', requestId: 'fixture-dns-response' } })
    }
    if (method === 'GET' && path === detailsPath) return route.fulfill({ json: { ...systemDetails, dns: { ...capability, nameservers: configured, configuredNameservers: configured } } })
    if (method === 'GET' && path === capabilityPath) return route.fulfill(failCapability
      ? { status: 503, json: { code: 'FIXTURE_CAPABILITY_UNAVAILABLE', message: 'fixture capability unavailable', requestId: 'fixture-dns-capability' } }
      : { json: { ...capability, nameservers: configured, configuredNameservers: configured } })
    if (method === 'POST' && path === `${root}/preview`) {
      const nameservers = (body as { nameservers: string[] }).nameservers
      const previewId = `fixture-preview-${++previewSequence}`
      previews.set(previewId, [...nameservers])
      return route.fulfill({ json: { previewId, backend, before: endpoint(configured), after: endpoint(nameservers), requiresConfirm: true, rollbackAvailable: true, expiresAt: '2099-01-01T00:00:00Z', errorCode: '' } })
    }
    if (method === 'POST' && path === `${root}/confirm`) {
      const { previewId, confirmed } = body as { previewId: string; confirmed: boolean }
      if (!confirmed || !previews.has(previewId)) return route.fulfill({ status: 409, json: { code: 'FIXTURE_PREVIEW_CONSUMED', message: 'fixture preview consumed', requestId: 'fixture-dns-preview' } })
      configured = previews.get(previewId)!
      previews.delete(previewId)
      return route.fulfill({ json: { changeId, backend, applied: true, rollbackAvailable: true, appliedAt: '2026-10-13T12:00:00Z', errorCode: '' } })
    }
    if (method === 'POST' && path === `${root}/rollback`) {
      if ((body as { changeId: string }).changeId !== changeId) return route.fulfill({ status: 409, json: { code: 'FIXTURE_WRONG_TARGET', message: 'fixture wrong rollback target', requestId: 'fixture-dns-rollback' } })
      if (!mismatchRollback) configured = [...initial]
      return route.fulfill({ json: { changeId: mismatchRollback ? 'fixture-other-change' : changeId, backend, applied: false, rollbackAvailable: false, appliedAt: '2026-10-13T12:00:00Z', errorCode: '' } })
    }
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_UNEXPECTED_REQUEST', message: 'fixture denied', requestId: 'fixture-dns-denied' } })
  })
  // installMockApi 明确 fulfill 自动 Mihomo inspect；绝不调用真实探针。
  return {
    writes,
    failCapability(value: boolean) { failCapability = value },
    mismatchRollback(value: boolean) { mismatchRollback = value },
    hold(path: string, statusCode = 200) {
      const gate = { path, statusCode, received: deferred(), release: deferred() }
      gates.push(gate)
      return gate
    },
    async release(gate: (typeof gates)[number]) {
      const response = page.waitForResponse((value) => new URL(value.url()).pathname === gate.path)
      gate.release.resolve()
      await (await response).finished()
      await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    },
  }
}
async function open(page: Page) {
  await page.goto('/system', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: '网络与代理', exact: true }).click()
  await page.getByRole('button', { name: '编辑 DNS', exact: true }).click()
  await input(page).fill(draftA)
}
async function preview(page: Page) {
  await button(page, '预览修改').click()
  await expect(button(page, '确认应用')).toBeEnabled()
}
async function evidence(page: Page, info: TestInfo, outcome: string) {
  const observations = []
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.evaluate(() => document.fonts.ready)
    await status(page).scrollIntoViewIfNeeded()
    const controls = await dialog(page).locator('input, button, [role="status"]').evaluateAll((elements) => elements.map((element) => {
      const bounds = element.getBoundingClientRect()
      const style = getComputedStyle(element)
      return { text: element.textContent, label: element.getAttribute('aria-label'), disabled: element.hasAttribute('disabled'), bounds: bounds.toJSON(), visible: bounds.width > 0 && bounds.height > 0 && style.visibility !== 'hidden', inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth && bounds.bottom <= innerHeight, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth }
    }))
    const axe = await new AxeBuilder({ page }).include('.dns-editor-dialog').analyze()
    observations.push({ width, controls, violations: axe.violations })
    if (process.env.NCP_CAPTURE_UI === '1') {
      const name = `dns-editor-${outcome}-${width}`
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      const path = info.outputPath('ui-captures', `${name}.png`)
      await page.screenshot({ path, animations: 'disabled' })
      await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({ sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), fixtureOnly: true, fixtureSource: 'frontend/e2e/dns-editor-ownership.spec.ts + frontend/e2e/mockApi.ts', viewport: page.viewportSize(), outcome, message: await status(page).innerText(), draft: await input(page).inputValue(), controls, axeViolations: axe.violations }, null, 2))
      await info.attach(name, { path, contentType: 'image/png' })
    }
  }
  // 两档证据均先落盘，再断言；首档失败也不会丢失另一档的截图和状态。
  for (const observation of observations) {
    expect(observation.controls.filter((control) => control.visible).every((control) => control.inViewport && control.scrollWidth <= control.clientWidth + 1), `${outcome} ${observation.width}px 控件完整可见`).toBe(true)
    expect(observation.violations, `${outcome} ${observation.width}px axe`).toEqual([])
  }
}

for (const code of [200, 503]) {
  test(`预览 A 挂起后编辑 B：迟到 ${code} 不复活 A 且释放 busy`, async ({ page }) => {
    const api = await fixture(page)
    await open(page)
    const held = api.hold(`${root}/preview`, code)
    await button(page, '预览修改').click()
    await held.received.promise
    await expect(input(page)).toBeEditable()
    await input(page).fill(draftB)
    await api.release(held)
    await expect(input(page)).toHaveValue(draftB)
    await expect(dialog(page).locator('.dns-preview')).toHaveCount(0)
    await expect(status(page)).toHaveCount(0)
    await expect(button(page, '确认应用')).toHaveCount(0)
    await expect(button(page, '预览修改')).toBeEnabled()
    expect(api.writes).toEqual([{ path: `${root}/preview`, body: { nameservers: [draftA] } }])
    await preview(page)
    await expect(dialog(page).locator('.dns-preview')).toContainText(draftB)
    expect(api.writes).toEqual([{ path: `${root}/preview`, body: { nameservers: [draftA] } }, { path: `${root}/preview`, body: { nameservers: [draftB] } }])
  })
}

test('确认成功在挂起 GET 前落账，一次性预览与回滚目标不重发', async ({ page }, info) => {
  const api = await fixture(page)
  await open(page)
  await preview(page)
  const confirm = api.hold(`${root}/confirm`)
  // 独立 DNS 能力决定写权限；仅挂起详情不能阻止回滚。
  const refresh = api.hold(capabilityPath)
  await button(page, '确认应用').click()
  await confirm.received.promise
  await expect(input(page)).toBeDisabled()
  await expect(button(page, '预览修改')).toBeDisabled()
  // 合成 click 只检查动作入口防重，不声称用户可绕过 disabled。
  await button(page, '预览修改').dispatchEvent('click')
  await api.release(confirm)
  await refresh.received.promise
  await expect(status(page)).toHaveText('DNS 已应用。')
  await expect(button(page, '回滚上次修改')).toBeVisible()
  await expect(button(page, '回滚上次修改')).toBeDisabled()
  await expect(button(page, '确认应用')).toHaveCount(0)
  await expect(input(page)).toBeEditable()
  expect(api.writes).toHaveLength(2)
  await evidence(page, info, 'applied-refresh-pending')
  await api.release(refresh)
  await expect(button(page, '回滚上次修改')).toBeEnabled()
  await button(page, '回滚上次修改').click()
  await expect(status(page)).toHaveText('DNS 已回滚。')
  await expect(button(page, '回滚上次修改')).toHaveCount(0)
  expect(api.writes).toEqual([{ path: `${root}/preview`, body: { nameservers: [draftA] } }, { path: `${root}/confirm`, body: { previewId: 'fixture-preview-1', confirmed: true } }, { path: `${root}/rollback`, body: { changeId } }])
})

test('确认响应未知消费预览，保留草稿且不自动重放', async ({ page }, info) => {
  const api = await fixture(page)
  await open(page)
  await preview(page)
  const held = api.hold(`${root}/confirm`, 503)
  await button(page, '确认应用').click()
  await held.received.promise
  await api.release(held)
  await expect(status(page)).toContainText('DNS 应用结果不明确，请先核实当前配置，再生成新预览。')
  await expect(input(page)).toHaveValue(draftA)
  await expect(button(page, '确认应用')).toHaveCount(0)
  await expect(button(page, '预览修改')).toBeEnabled()
  await expect(button(page, '回滚上次修改')).toHaveCount(0)
  await evidence(page, info, 'confirm-unknown')
  expect(api.writes).toEqual([{ path: `${root}/preview`, body: { nameservers: [draftA] } }, { path: `${root}/confirm`, body: { previewId: 'fixture-preview-1', confirmed: true } }])
})

test('能力刷新失败保留已应用草稿与句柄，恢复新鲜能力后只回滚原目标', async ({ page }) => {
  const api = await fixture(page)
  await open(page)
  await preview(page)
  api.failCapability(true)
  const failedRead = api.hold(capabilityPath)
  await button(page, '确认应用').click()
  await failedRead.received.promise
  await api.release(failedRead)
  await expect(status(page)).toHaveText('DNS 已应用。')
  await expect(input(page)).toHaveValue(draftA)
  await expect(button(page, '回滚上次修改')).toBeDisabled()
  await expect(button(page, '预览修改')).toBeDisabled()
  await button(page, '回滚上次修改').dispatchEvent('click')
  expect(api.writes).toHaveLength(2)
  await button(page, '取消').click()
  await expect(dialog(page)).toBeHidden()
  api.failCapability(false)
  await page.getByRole('button', { name: '立即刷新数据', exact: true }).click()
  await page.getByRole('button', { name: '编辑 DNS', exact: true }).click()
  await expect(button(page, '回滚上次修改')).toBeEnabled()
  await button(page, '回滚上次修改').click()
  await expect(status(page)).toHaveText('DNS 已回滚。')
  expect(api.writes.at(-1)).toEqual({ path: `${root}/rollback`, body: { changeId } })
  expect(api.writes).toHaveLength(3)
})

test('回滚返回不同目标不误清句柄，明确匹配成功才清理', async ({ page }) => {
  const api = await fixture(page)
  await open(page)
  await preview(page)
  await button(page, '确认应用').click()
  await expect(button(page, '回滚上次修改')).toBeEnabled()
  api.mismatchRollback(true)
  const held = api.hold(`${root}/rollback`)
  await button(page, '回滚上次修改').click()
  await held.received.promise
  await expect(input(page)).toBeDisabled()
  await button(page, '回滚上次修改').dispatchEvent('click')
  await api.release(held)
  await expect(status(page)).toContainText('DNS 未确认回滚')
  await expect(button(page, '回滚上次修改')).toBeEnabled()
  expect(api.writes).toHaveLength(3)
  api.mismatchRollback(false)
  await button(page, '回滚上次修改').click()
  await expect(status(page)).toHaveText('DNS 已回滚。')
  await expect(button(page, '回滚上次修改')).toHaveCount(0)
  expect(api.writes.slice(2)).toEqual([{ path: `${root}/rollback`, body: { changeId } }, { path: `${root}/rollback`, body: { changeId } }])
})
