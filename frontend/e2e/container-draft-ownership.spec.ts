import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'

const createPath = '/api/v1/docker/containers'
const resourcePath = '/api/v1/docker/resources'
const entriesPath = '/api/v1/files/entries'
const drawer = (page: Page) => page.locator('.create-container-drawer')
const nameInput = (page: Page) => drawer(page).getByPlaceholder('例如 mysql-1')
const createButton = (page: Page) => drawer(page).getByRole('button', { name: '创建并启动', exact: true })
const picker = (page: Page) => page.getByRole('dialog', { name: '选择 NAS 路径', exact: true })
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}
type Gate = { path: string; status: number; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }

// 所有 API 被内存路由接管；仅允许用户明确确认的创建与退出登录写入，未知请求失败关闭。
async function fixture(page: Page) {
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: string[] = []
  const writes: Array<Record<string, unknown>> = []
  const unexpected: string[] = []
  const gates: Gate[] = []
  const reads = new Set(['/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities', '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/docker/images', '/api/v1/jobs', '/api/v1/sites', '/api/v1/sites/ignored'])
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    requests.push(`${method} ${path}`)
    if (method === 'POST' && path === '/api/v1/auth/logout') return route.fulfill({ status: 204 })
    if (method === 'GET' && (reads.has(path) || path.startsWith('/api/v1/preferences/lists/'))) return route.fallback()
    if (!(method === 'POST' && path === createPath) && !(method === 'GET' && [resourcePath, entriesPath].includes(path))) {
      unexpected.push(`${method} ${path}`)
      return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: 'fixture denied' } })
    }
    const body = method === 'POST' ? request.postDataJSON() as Record<string, unknown> : undefined
    if (body) writes.push(structuredClone(body))
    const index = gates.findIndex((gate) => gate.path === path)
    const gate = index < 0 ? undefined : gates.splice(index, 1)[0]!
    if (gate) { gate.received.resolve(); await gate.release.promise }
    try {
      if (gate && gate.status !== 200) return await route.fulfill({ status: gate.status, json: { code: 'DOCKER_CONTAINER_CREATE_INVALID', message: '容器创建参数无效。', requestId: 'fixture-create-a' } })
      if (body) return await route.fulfill({ status: 201, json: { containerId: 'created-fixture-a', name: body.name, image: body.image, state: 'running', created: true, started: true, runContainer: true } })
      if (path === resourcePath) return await route.fulfill({ json: { collectedAt: '2026-10-13T12:00:00Z', networks: [{ id: 'old-network', name: 'old-network', driver: 'bridge', scope: 'local', internal: false, subnets: [], gateways: [] }], volumes: [] } })
      if (gate && path === entriesPath) return await route.fulfill({ json: { path: '/obsolete', parent: '/', nextCursor: '', collectedAt: '2026-10-13T12:00:00Z', entries: [{ name: 'obsolete-entry', path: '/obsolete/entry', type: 'directory', readable: true }] } })
      return await route.fallback()
    } finally { gate?.finished.resolve() }
  })
  return {
    requests, writes, unexpected,
    hold(path: string, status = 200) {
      const gate: Gate = { path, status, received: deferred(), release: deferred(), finished: deferred() }
      gates.push(gate)
      return gate
    },
    async release(gate: Gate) {
      const response = gate.path === createPath ? page.waitForResponse((value) => new URL(value.url()).pathname === createPath && value.request().method() === 'POST') : null
      gate.release.resolve()
      await gate.finished.promise
      if (response) await (await response).finished()
      // 只等待响应后的渲染，不改状态、不绕过普通用户交互。
      await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    },
  }
}
async function openImages(page: Page) {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/docker', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: '镜像', exact: true }).click()
  await expect(page.getByRole('button', { name: '从镜像 media-api:latest 创建容器', exact: true })).toBeVisible()
}
async function openDraft(page: Page, image = 'media-api:latest') {
  await page.getByRole('button', { name: `从镜像 ${image} 创建容器`, exact: true }).click()
  await expect(drawer(page)).toBeVisible()
  await expect(drawer(page)).toHaveAccessibleName(`从 ${image} 创建容器`)
  await expect(drawer(page).getByRole('heading', { name: `从 ${image} 创建容器`, level: 2, exact: true })).toBeVisible()
}
async function submitA(page: Page) {
  await nameInput(page).fill('draft-a')
  await createButton(page).click()
  const preview = page.getByRole('dialog', { name: '确认容器配置', exact: true })
  await expect(preview).toContainText('draft-a')
  await preview.getByRole('button', { name: '确认创建并启动', exact: true }).click()
  await expect(preview).toBeHidden()
}
async function closeDrawer(page: Page, method: 'x' | 'escape') {
  if (method === 'x') await drawer(page).locator('.el-drawer__close-btn').click()
  else await page.keyboard.press('Escape')
  await expect(drawer(page)).toBeHidden()
}
async function capturePendingB(page: Page, info: TestInfo) {
  const files = ['e2e/container-draft-ownership.spec.ts', 'e2e/mockApi.ts', 'src/components/CreateContainerDrawer.vue', 'src/components/DockerImagePanel.vue']
  const sourceHashes = Object.fromEntries(await Promise.all(files.map(async (file) => [file, createHash('sha256').update(await readFile(file)).digest('hex')])))
  const sourceSha = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim()
  const workingTree = execFileSync('git', ['status', '--porcelain'], { encoding: 'utf8' })
  const observations = []
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.evaluate(() => document.fonts.ready)
    await nameInput(page).scrollIntoViewIfNeeded()
    const state = () => page.evaluate(() => {
      const selectors = ['.create-container-drawer .el-drawer__header', '.create-container-drawer input[placeholder="例如 mysql-1"]', '.create-container-drawer .drawer-actions', '.create-container-drawer .el-drawer__close-btn', ...[1, 2, 3].flatMap((index) => [`.create-container-drawer .drawer-actions button:nth-child(${index})`, `.create-container-drawer .drawer-actions button:nth-child(${index}) .action-button__label`])]
      return {
        viewport: { width: innerWidth, height: innerHeight }, scroll: { x: scrollX, y: scrollY },
        focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label'), placeholder: document.activeElement?.getAttribute('placeholder') },
        bodyWidth: document.body.scrollWidth,
        imageLayout: ['.image-workspace', '.image-modebar', '.image-workspace .resource-table'].map((selector) => {
          const element = document.querySelector<HTMLElement>(selector)!
          return { selector, bounds: element.getBoundingClientRect().toJSON(), scrollWidth: element.scrollWidth, clientWidth: element.clientWidth, overflowX: getComputedStyle(element).overflowX }
        }),
        controls: selectors.map((selector) => {
          const element = document.querySelector<HTMLElement>(selector)!
          const bounds = element.getBoundingClientRect()
          return { selector, bounds: bounds.toJSON(), scrollWidth: element.scrollWidth, clientWidth: element.clientWidth, inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth && bounds.bottom <= innerHeight }
        }),
        draft: document.querySelector<HTMLInputElement>('.create-container-drawer input[placeholder="例如 mysql-1"]')?.value,
        actions: [...document.querySelectorAll<HTMLButtonElement>('.create-container-drawer .drawer-actions button')].map((button) => ({ text: button.textContent?.trim(), disabled: button.disabled })),
      }
    })
    const beforeCapture = await state()
    const name = `container-draft-pending-b-${width}`
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    const path = info.outputPath('ui-captures', `${name}.png`)
    await page.screenshot({ path, animations: 'disabled' })
    const afterCapture = await state()
    const axe = await new AxeBuilder({ page }).include('.create-container-drawer').analyze()
    const afterAxe = await state()
    await writeFile(info.outputPath('ui-captures', `${name}-capture-state.json`), JSON.stringify({ sourceSha, sourceHashes, workingTree, fixtureOnly: true, state: 'A POST pending; B name edited; original POST lock retained', beforeCapture, afterCapture, afterAxe, axeViolations: axe.violations }, null, 2))
    await info.attach(name, { path, contentType: 'image/png' })
    observations.push({ width, beforeCapture, violations: axe.violations })
  }
  for (const observation of observations) {
    expect(observation.beforeCapture.draft).toBe('draft-b')
    expect(observation.beforeCapture.actions.every((action) => action.disabled)).toBe(true)
    expect(observation.beforeCapture.bodyWidth).toBeLessThanOrEqual(observation.width)
    expect(observation.beforeCapture.controls.every((control) => control.inViewport && control.scrollWidth <= control.clientWidth + 1), `${observation.width}px 关键控件完整可见`).toBe(true)
    expect(observation.violations.filter((violation) => ['critical', 'serious'].includes(violation.impact ?? '')), `${observation.width}px axe`).toEqual([])
  }
}

for (const outcome of [{ status: 200, close: 'x' }, { status: 400, close: 'escape' }] as const) {
  test(`A 提交后普通 ${outcome.close} 关闭，迟到 ${outcome.status} 保留 B 且不重放`, async ({ page }, info) => {
    const api = await fixture(page)
    await openImages(page); await openDraft(page)
    const held = api.hold(createPath, outcome.status)
    await submitA(page); await held.received.promise
    await closeDrawer(page, outcome.close)
    await openDraft(page, 'alpine:3.20')
    await expect(nameInput(page)).toHaveValue('alpine')
    await nameInput(page).fill('draft-b')
    await expect(createButton(page)).toBeDisabled()
    await expect(drawer(page).getByRole('button', { name: '仅创建', exact: true })).toBeDisabled()
    if (outcome.status === 200) await capturePendingB(page, info)
    await drawer(page).getByRole('button', { name: '添加环境变量', exact: true }).click()
    await drawer(page).getByPlaceholder('变量，例如 APP_MODE').fill('DRAFT')
    await drawer(page).getByPlaceholder('值', { exact: true }).fill('B')
    const readsBefore = api.requests.length
    await api.release(held)
    await expect(createButton(page)).toBeEnabled()
    await expect(drawer(page)).toBeVisible()
    await expect(nameInput(page)).toHaveValue('draft-b')
    await expect(drawer(page).getByPlaceholder('变量，例如 APP_MODE')).toHaveValue('DRAFT')
    await expect(drawer(page).getByPlaceholder('值', { exact: true })).toHaveValue('B')
    await expect(drawer(page).getByLabel('镜像', { exact: true })).toHaveValue('alpine:3.20')
    await expect(page.locator('.el-message')).toHaveCount(0)
    if (outcome.status === 200) await expect.poll(() => api.requests.slice(readsBefore).includes('GET /api/v1/docker/images')).toBe(true)
    if (outcome.status === 200) expect(api.requests.slice(readsBefore)).toEqual(['GET /api/v1/docker/inventory', 'GET /api/v1/docker/images'])
    else expect(api.requests.slice(readsBefore)).toEqual([])
    await createButton(page).click()
    const previewB = page.getByRole('dialog', { name: '确认容器配置', exact: true })
    await expect(previewB).toContainText('draft-b')
    await expect(previewB).toContainText('alpine:3.20')
    await previewB.getByRole('button', { name: '返回修改', exact: true }).click()
    expect(api.writes).toHaveLength(1)
    expect(api.writes[0]).toMatchObject({ name: 'draft-a', image: 'media-api:latest', runContainer: true })
    expect(api.unexpected).toEqual([])
  })
}

for (const destination of ['logout', 'unmount'] as const) {
  test(`A 提交后 ${destination}，迟到成功不通知或触发创建后读取`, async ({ page }) => {
    const api = await fixture(page)
    await openImages(page); await openDraft(page)
    const held = api.hold(createPath)
    await submitA(page); await held.received.promise
    await closeDrawer(page, 'x')
    if (destination === 'logout') {
      await page.getByRole('button', { name: '退出登录', exact: true }).click()
      await expect(page.getByRole('heading', { name: '进入 NAS Control Plane', exact: true })).toBeVisible()
    } else {
      await page.getByRole('button', { name: '容器', exact: true }).click()
      await expect(page.locator('.image-workspace')).toHaveCount(0)
    }
    const before = api.requests.length
    await api.release(held)
    await expect(page.locator('.el-message')).toHaveCount(0)
    expect(api.requests.slice(before)).toEqual([])
    expect(api.writes).toHaveLength(1)
    expect(api.unexpected).toEqual([])
  })
}

test('本地镜像表格在窄屏内部滚动，不撑宽页面', async ({ page }) => {
  const api = await fixture(page)
  await openImages(page)
  for (const width of [320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    const layout = await page.evaluate(() => {
      const table = document.querySelector<HTMLElement>('.image-workspace .resource-table')!
      const workspace = document.querySelector<HTMLElement>('.image-workspace')!
      return {
        viewport: innerWidth,
        bodyWidth: document.body.scrollWidth,
        workspaceRight: workspace.getBoundingClientRect().right,
        tableRight: table.getBoundingClientRect().right,
        tableClientWidth: table.clientWidth,
        tableScrollWidth: table.scrollWidth,
        tableOverflowX: getComputedStyle(table).overflowX,
      }
    })
    expect(layout.bodyWidth, `${width}px 页面不能被本地镜像表格撑宽：${JSON.stringify(layout)}`).toBeLessThanOrEqual(layout.viewport)
    expect(layout.workspaceRight).toBeLessThanOrEqual(layout.viewport)
    expect(layout.tableRight).toBeLessThanOrEqual(layout.viewport)
    if (width <= 820) {
      expect(layout.tableOverflowX).toBe('auto')
      expect(layout.tableScrollWidth).toBeGreaterThan(layout.tableClientWidth)
    }
  }
  expect(api.writes).toEqual([])
  expect(api.unexpected).toEqual([])
})

test('资源读取中编辑专用网络名，迟到清单不覆盖用户输入', async ({ page }) => {
  const api = await fixture(page)
  await openImages(page)
  const held = api.hold(resourcePath)
  await openDraft(page); await held.received.promise
  // Element Plus 的原生 input 为 0×0；点击用户可见的开关轨道，再验证实际 switch 状态。
  const customNetwork = drawer(page).getByRole('switch', { name: '启用自定义网络', exact: true })
  await drawer(page).locator('.el-switch').filter({ has: page.getByRole('switch', { name: '启用自定义网络', exact: true }) }).locator('.el-switch__core').click()
  await expect(customNetwork).toBeChecked()
  const dedicatedNetwork = drawer(page).locator('.network-mode-switch')
  await dedicatedNetwork.locator('.el-switch__core').click()
  await expect(dedicatedNetwork.getByRole('switch')).toBeChecked()
  await drawer(page).getByPlaceholder('例如 media-net').fill('my-edited-network')
  await drawer(page).getByPlaceholder('172.30.0.0/24').fill('172.29.0.0/24')
  await api.release(held)
  await expect(drawer(page).getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0)
  await expect(drawer(page).getByPlaceholder('例如 media-net')).toHaveValue('my-edited-network')
  await createButton(page).click()
  const preview = page.getByRole('dialog', { name: '确认容器配置', exact: true })
  await expect(preview).toContainText('my-edited-network')
  await preview.getByRole('button', { name: '返回修改', exact: true }).click()
  expect(api.writes).toEqual([])
  expect(api.unexpected).toEqual([])
})

test('关闭路径选择并删除旧行，迟到目录不污染新行或重开选择器', async ({ page }) => {
  const api = await fixture(page)
  await openImages(page); await openDraft(page)
  await drawer(page).getByRole('button', { name: '添加挂载', exact: true }).click()
  const held = api.hold(entriesPath)
  await drawer(page).getByRole('button', { name: '浏览', exact: true }).click()
  await held.received.promise
  await picker(page).locator('.el-dialog__headerbtn').click()
  await expect(picker(page)).toBeHidden()
  await drawer(page).locator('.repeat-row--mount .row-remove').click()
  await drawer(page).getByRole('button', { name: '添加挂载', exact: true }).click()
  await drawer(page).getByPlaceholder('/volume2/data').fill('/keep-b')
  await drawer(page).getByRole('button', { name: '浏览', exact: true }).click()
  await expect(picker(page).locator('.path-entry__name')).toContainText('volume2')
  await api.release(held)
  await expect(picker(page).locator('.path-browser__toolbar code')).toHaveText('/')
  await expect(picker(page).getByText('obsolete-entry')).toHaveCount(0)
  await expect(drawer(page).getByPlaceholder('/volume2/data')).toHaveValue('/keep-b')
  await picker(page).getByRole('button', { name: '选择', exact: true }).click()
  await expect(picker(page)).toBeHidden()
  await expect(drawer(page).getByPlaceholder('/volume2/data')).toHaveValue('/volume2')
  expect(api.writes).toEqual([])
  expect(api.unexpected).toEqual([])
})
