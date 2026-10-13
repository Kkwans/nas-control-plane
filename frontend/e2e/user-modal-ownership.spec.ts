import { expect, test, type Locator, type Page } from '@playwright/test'
import { installMockApi } from './mockApi'

const usersPath = '/api/v1/users'
const policyPath = `${usersPath}/password-policy`
const policy = { minLength: 8, requireUppercase: false, requireLowercase: false, requireDigit: false, requireSpecial: false }
const account = (id: number, username: string) => ({ id, username, role: 'root', disabled: false, createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:00:00Z', lastLoginAt: null })
const userA = account(42, 'fixture-modal-a')
const userB = account(43, 'fixture-modal-b')
const created = account(44, 'fixture-created-a')
const secretA = 'Fixture-only-password-A-123!'
const secretB = 'Fixture-only-password-B-456!'
const row = (page: Page, username: string) => page.locator('.user-row').filter({ has: page.getByText(username, { exact: true }) })
const dialog = (page: Page) => page.getByRole('dialog')
const createButton = (page: Page) => page.locator('.users-panel').getByRole('button', { name: '新增账号', exact: true })
const createSubmit = (page: Page) => dialog(page).getByRole('button', { name: '创建账号', exact: true })
const passwordSubmit = (page: Page) => dialog(page).getByRole('button', { name: '保存新密码', exact: true })

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 每个 /api/v1 请求均由内存 mock 接管；下方写入只修改伪造目录，绝不访问真实账号。
async function fixture(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  let users = [userA, userB]
  const writes: Array<{ method: string; path: string; body: unknown }> = []
  const gates: Array<{ path: string; method: string; status: number; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> }> = []
  await page.route('**/api/v1/users**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    if (method === 'GET' && path === usersPath) return route.fulfill({ json: users })
    if (method === 'GET' && path === policyPath) return route.fulfill({ json: policy })
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    writes.push({ method, path, body })
    const index = gates.findIndex((gate) => gate.path === path && gate.method === method)
    if (index < 0) return route.fulfill({ status: 503, json: { code: 'FIXTURE_UNEXPECTED_WRITE', message: 'fixture unexpected write' } })
    const held = gates.splice(index, 1)[0]!
    held.received.resolve()
    await held.release.promise
    if (held.status >= 400) return route.fulfill({ status: held.status, json: { code: 'FIXTURE_FAILURE', message: 'fixture stale write failure' } })
    if (method === 'POST' && path === usersPath) {
      users = [...users, created]
      return route.fulfill({ status: held.status, json: created })
    }
    if (method === 'PUT' && path.endsWith('/status')) {
      const id = Number(path.split('/')[4])
      users = users.map((user) => user.id === id ? { ...user, disabled: true } : user)
      return route.fulfill({ status: held.status, json: users.find((user) => user.id === id) })
    }
    if (method === 'DELETE') users = users.filter((user) => user.id !== Number(path.split('/')[4]))
    return held.status === 204 ? route.fulfill({ status: 204 }) : route.fulfill({ status: held.status, json: {} })
  })
  return {
    writes,
    hold(path: string, method: string, status = method === 'POST' ? 201 : path.endsWith('/status') ? 200 : 204) {
      const held = { path, method, status, received: deferred(), release: deferred() }
      gates.push(held)
      return held
    },
    async release(held: (typeof gates)[number]) {
      const response = page.waitForResponse((value) => new URL(value.url()).pathname === held.path && value.request().method() === held.method)
      // 改密成功处理函数必定发起只读对账；先注册观察，避免错过快速的 204 回调。
      const reconciled = held.status === 204 && held.path.endsWith('/password')
        ? page.waitForRequest((request) => new URL(request.url()).pathname === usersPath && request.method() === 'GET')
        : undefined
      held.release.resolve()
      const result = await response
      if (reconciled) await reconciled
      else await result.finished()
      // 不依赖 CI 中未结束的 204 body-completion 等待；对账请求证明业务成功分支已执行。
      // 失败仍等待 JSON 响应传输完成，再等待 Vue 更新和 Element 消息挂载，保留错归属断言。
      await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    },
  }
}
async function ready(page: Page) {
  await page.goto('/users')
  await expect(createButton(page)).toBeEnabled()
  await expect(row(page, userB.username)).toBeVisible()
}
async function close(page: Page, mode: 'close' | 'cancel' | 'escape') {
  if (mode === 'close') await dialog(page).locator('.el-dialog__headerbtn').click()
  else if (mode === 'cancel') await dialog(page).getByRole('button', { name: '取消', exact: true }).click()
  else await page.keyboard.press('Escape')
  await expect(dialog(page)).toBeHidden()
  await expect(page.locator('.el-dialog input[type="password"]')).toHaveCount(0)
}
async function blankSecrets(target: Locator) {
  await expect(target.locator('input[type="password"]').first()).toBeVisible()
  for (const input of await target.locator('input[type="password"]').all()) await expect(input).toHaveValue('')
}
async function fillPassword(page: Page, value: string) {
  await expect(dialog(page).locator('input[type="password"]').first()).toBeVisible()
  for (const input of await dialog(page).locator('input[type="password"]').all()) await input.fill(value)
}
async function openPassword(page: Page, username: string) {
  await row(page, username).getByRole('button', { name: '重置密码', exact: true }).click()
  await expect(dialog(page)).toHaveAttribute('aria-label', `重置 ${username} 的密码`)
}
async function openDanger(page: Page, username: string, action: '禁用账号' | '删除账号') {
  await row(page, username).getByRole('button', { name: '更多账号操作' }).click()
  await page.getByRole('menuitem', { name: action, exact: true }).click()
  await expect(dialog(page)).toContainText(username)
}

for (const mode of ['close', 'cancel', 'escape'] as const) {
  test(`创建挂起后 ${mode} 清空敏感草稿；重开单飞且迟到结果不关闭新弹窗`, async ({ page }) => {
    const api = await fixture(page)
    await ready(page)
    const held = api.hold(usersPath, 'POST', mode === 'cancel' ? 503 : 201)
    await createButton(page).click()
    await dialog(page).getByPlaceholder('例如 operator').fill(created.username)
    await fillPassword(page, secretA)
    await createSubmit(page).click()
    await held.received.promise
    await createSubmit(page).dispatchEvent('click')
    await close(page, mode)
    await createButton(page).click()
    await blankSecrets(dialog(page))
    await dialog(page).getByPlaceholder('例如 operator').fill('fixture-created-b')
    await fillPassword(page, secretB)
    await expect(createSubmit(page)).toBeDisabled()
    await createSubmit(page).dispatchEvent('click')
    await api.release(held)
    await expect(createSubmit(page)).toBeEnabled()
    await expect(dialog(page)).toBeVisible()
    await expect(dialog(page).getByPlaceholder('例如 operator')).toHaveValue('fixture-created-b')
    await expect(dialog(page).locator('input[type="password"]')).toHaveValue(secretB)
    await expect(page.locator('.el-message')).toHaveCount(0)
    expect(api.writes).toEqual([{ method: 'POST', path: usersPath, body: { username: created.username, password: secretA } }])
    // 遮罩下的目录仍应消费已确认结果；503 为未知，不凭用户名猜测归属。
    if (mode === 'cancel') await expect(page.locator('.user-write-warning')).toContainText(created.username)
    await expect(row(page, created.username)).toHaveCount(mode === 'cancel' ? 0 : 1)
    await close(page, 'cancel')
  })
}

for (const status of [204, 503]) {
  test(`改密 A 挂起后重开 A/B，${status} 迟到结果不清 B 且保留策略草稿`, async ({ page }) => {
    const api = await fixture(page)
    await ready(page)
    const length = page.locator('.password-policy input[role="spinbutton"]')
    await length.fill('12')
    await length.press('Tab')
    const heldA = api.hold(`${usersPath}/${userA.id}/password`, 'PUT', status)
    await openPassword(page, userA.username)
    await fillPassword(page, secretA)
    await passwordSubmit(page).click()
    await heldA.received.promise
    await close(page, 'escape')
    await openPassword(page, userA.username)
    await blankSecrets(dialog(page))
    await fillPassword(page, secretB)
    await expect(passwordSubmit(page)).toBeDisabled()
    await passwordSubmit(page).dispatchEvent('click')
    await close(page, 'cancel')
    // 同账号的改密锁也保护危险写入，关窗不能绕过。
    await openDanger(page, userA.username, '禁用账号')
    await dialog(page).getByRole('button', { name: '禁用账号', exact: true }).dispatchEvent('click')
    expect(api.writes).toHaveLength(1)
    await close(page, 'close')
    await openPassword(page, userB.username)
    await blankSecrets(dialog(page))
    await expect(passwordSubmit(page)).toBeEnabled()
    await expect(dialog(page).getByPlaceholder('至少 8 个字符', { exact: true })).toBeVisible()
    await fillPassword(page, secretB)
    const heldB = api.hold(`${usersPath}/${userB.id}/password`, 'PUT')
    await passwordSubmit(page).click()
    await heldB.received.promise
    await api.release(heldA)
    await expect(dialog(page)).toHaveAttribute('aria-label', `重置 ${userB.username} 的密码`)
    for (const input of await dialog(page).locator('input[type="password"]').all()) await expect(input).toHaveValue(secretB)
    await expect(passwordSubmit(page)).toBeDisabled()
    await expect(page.locator('.el-message')).toHaveCount(0)
    await expect(length).toHaveValue('12')
    await expect(page.locator('.password-policy [role="status"]')).toContainText('有未保存的规则修改')
    await close(page, 'close')
    await api.release(heldB)
    await openPassword(page, userB.username)
    await blankSecrets(dialog(page))
    await expect(passwordSubmit(page)).toBeEnabled()
    await expect(page.locator('.el-message')).toHaveCount(0)
    expect(api.writes).toEqual([
      { method: 'PUT', path: `${usersPath}/${userA.id}/password`, body: { currentPassword: '', newPassword: secretA } },
      { method: 'PUT', path: `${usersPath}/${userB.id}/password`, body: { currentPassword: '', newPassword: secretB } },
    ])
  })
}

for (const status of [200, 503]) {
  test(`危险操作 A 冻结目标，${status} 迟到结果不关闭 B 删除确认或发错提示`, async ({ page }) => {
    const api = await fixture(page)
    await ready(page)
    const held = api.hold(`${usersPath}/${userA.id}/status`, 'PUT', status)
    await openDanger(page, userA.username, '禁用账号')
    await dialog(page).getByRole('button', { name: '禁用账号', exact: true }).click()
    await held.received.promise
    await close(page, 'close')
    await openDanger(page, userA.username, '删除账号')
    await dialog(page).getByPlaceholder(userA.username, { exact: true }).fill(userA.username)
    await dialog(page).getByRole('button', { name: '永久删除', exact: true }).dispatchEvent('click')
    expect(api.writes).toHaveLength(1)
    await close(page, 'escape')
    await openDanger(page, userB.username, '删除账号')
    await dialog(page).getByPlaceholder(userB.username, { exact: true }).fill(userB.username)
    await expect(dialog(page).getByRole('button', { name: '永久删除', exact: true })).toBeEnabled()
    await api.release(held)
    await expect(dialog(page)).toBeVisible()
    await expect(dialog(page)).toContainText(userB.username)
    await expect(dialog(page).getByPlaceholder(userB.username, { exact: true })).toHaveValue(userB.username)
    await expect(page.locator('.el-message')).toHaveCount(0)
    await expect(row(page, userA.username).locator('.account-status')).toHaveText(status === 200 ? '已禁用' : '可用')
    await expect(row(page, userB.username).locator('.account-status')).toHaveText('可用')
    expect(api.writes).toEqual([{ method: 'PUT', path: `${usersPath}/${userA.id}/status`, body: { disabled: true } }])
  })
}

// 错误成功状态也可能已提交；只读看见账号不确认原创建归属或解除重放保护。
test('创建返回 200 后保持未知；只读刷新与重开不解除同名保护', async ({ page }) => {
  const api = await fixture(page)
  await ready(page)
  const held = api.hold(usersPath, 'POST', 200)
  await createButton(page).click()
  await dialog(page).getByPlaceholder('例如 operator').fill(created.username)
  await fillPassword(page, secretA)
  await createSubmit(page).click()
  await held.received.promise
  await api.release(held)
  await expect(createSubmit(page)).toBeDisabled()
  await blankSecrets(dialog(page))
  await expect(page.locator('.el-message')).toHaveCount(0)
  await close(page, 'cancel')
  const warning = page.locator('.user-write-warning')
  await expect(warning).toContainText(created.username)
  const read = page.waitForResponse((response) => new URL(response.url()).pathname === usersPath && response.request().method() === 'GET')
  await warning.getByRole('button', { name: '只读刷新账号列表' }).click()
  await read
  await expect(row(page, created.username)).toBeVisible()
  await expect(warning).toBeVisible()
  await createButton(page).click()
  await blankSecrets(dialog(page))
  // NEL 是 Go TrimSpace 空白；JS trim 不处理，必须使用服务器一致的目标键。
  await dialog(page).getByPlaceholder('例如 operator').fill(`\u0085${created.username}\u0085`)
  await fillPassword(page, secretB)
  await expect(createSubmit(page)).toBeDisabled()
  await createSubmit(page).dispatchEvent('click')
  expect(api.writes).toHaveLength(1)
  await dialog(page).getByPlaceholder('例如 operator').fill('fixture-unrelated-new')
  await expect(createSubmit(page)).toBeEnabled()
})

for (const status of [200, 503]) {
  test(`改密返回 ${status} 后清空密码且同账号不重放；其他账号仍可操作`, async ({ page }) => {
    const api = await fixture(page)
    await ready(page)
    const held = api.hold(`${usersPath}/${userA.id}/password`, 'PUT', status)
    await openPassword(page, userA.username)
    await fillPassword(page, secretA)
    await passwordSubmit(page).click()
    await held.received.promise
    await api.release(held)
    await expect(passwordSubmit(page)).toBeDisabled()
    await blankSecrets(dialog(page))
    await expect(page.locator('.el-message')).toHaveCount(0)
    await close(page, 'close')
    await expect(page.locator('.user-write-warning')).toContainText(userA.username)
    await openPassword(page, userA.username)
    await blankSecrets(dialog(page))
    await fillPassword(page, secretB)
    await expect(passwordSubmit(page)).toBeDisabled()
    await passwordSubmit(page).dispatchEvent('click')
    expect(api.writes).toHaveLength(1)
    await close(page, 'cancel')
    await openPassword(page, userB.username)
    await expect(passwordSubmit(page)).toBeEnabled()
    await blankSecrets(dialog(page))
    expect(api.writes).toHaveLength(1)
  })
}
