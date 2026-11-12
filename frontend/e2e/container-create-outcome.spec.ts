import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi, creationJobFixture, fixtureCreatedContainerId } from './mockApi'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })

const createPath = '/api/v1/docker/containers/create-jobs'
const jobId = 'job-create-outcome'
const jobPath = `/api/v1/jobs/${jobId}`
const redirectTargetPath = '/api/v1/docker/containers/fixture-redirect-denied'
const inventoryPath = '/api/v1/docker/inventory'
const submittedName = 'unknown-draft-a'
const submittedImage = 'media-api:latest'
const submittedTime = '2026-10-13T12:00:00.000Z'
const drawer = (page: Page) => page.locator('.create-container-drawer')
const notice = (page: Page) => page.locator('.create-protection[aria-label="容器创建防重复保护"]')
const nameInput = (page: Page) => drawer(page).getByPlaceholder('例如 mysql-1')
const createButton = (page: Page) => drawer(page).getByRole('button', { name: '创建并启动', exact: true })
const createOnlyButton = (page: Page) => drawer(page).getByRole('button', { name: '仅创建', exact: true })
const inspectButton = (page: Page) => notice(page).getByRole('button', { name: '只读核对任务与容器', exact: true })
const preview = (page: Page) => page.getByRole('dialog', { name: '确认容器配置', exact: true })

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

type Reply = { status: number; body: string; headers?: Record<string, string> } | { abort: 'failed' | 'aborted' }
type Gate = { transportFailure: boolean; received: ReturnType<typeof deferred>; release: ReturnType<typeof deferred>; finished: ReturnType<typeof deferred> }
type InventoryMode = 'empty' | 'same-name' | 'failure'
const invalidJson: Reply = { status: 202, body: '{' }
const validationFailure: Reply = { status: 400, body: JSON.stringify({ code: 'DOCKER_CONTAINER_CREATE_INVALID', message: '容器创建参数无效。', requestId: 'fixture-create-a' }) }
const transportFailure: Reply = { abort: 'failed' }
const cleanupFailure: Reply = { status: 409, body: JSON.stringify({ code: 'DOCKER_CONTAINER_CREATE_CLEANUP_FAILED', message: 'private-cleanup-diagnostic', requestId: 'fixture-cleanup', containerId: 'untrusted-partial-id' }) }
const agentFailure: Reply = { status: 503, body: JSON.stringify({ code: 'AGENT_DOCKER_CONTROL_UNAVAILABLE', message: 'private-agent-diagnostic', requestId: 'fixture-agent' }) }

// 严格内存 fixture：只放行逐次授权的一个 POST，所有其他 API 变更和未知路径失败关闭。
// 不访问 Docker/NAS；事件流也由通用 fixture 的手动 EventSource 接管。
async function fixture(page: Page) {
  // 日期与单调时钟自然前进；每次真实 fixture 读取返回新的采集版本。
  // 相同采集时间不会续期；静默事件流不应把多次成功读取伪装成同一旧快照。
  await page.clock.install({ time: new Date(submittedTime) })
  await installMockApi(page, { clock: false, manualRealtime: true })
  const requests: string[] = []
  const writes: Array<{ target: string; body: Record<string, unknown> }> = []
  const denied: string[] = []
  const reads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities',
    '/api/v1/system/summary', '/api/v1/docker/resources', '/api/v1/docker/images',
    '/api/v1/jobs', '/api/v1/sites', '/api/v1/sites/ignored',
  ])
  let permitted: { reply: Reply; gate?: Gate } | undefined
  let inventoryMode: InventoryMode = 'empty'
  let inventoryTimestamp = Date.parse(submittedTime)
  let jobs: unknown = { jobs: [] }
  let job: unknown = creationJobFixture(jobId, 'queued')
  let jobStatus = 200
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const method = request.method()
    const target = `${method} ${path}${new URL(request.url()).search}`
    requests.push(target)
    // 即使跳转策略回归，也只会命中失败关闭的内存端点。
    if (path === redirectTargetPath) {
      denied.push(target)
      return route.fulfill({ status: 503, json: { code: 'FIXTURE_REDIRECT_TARGET_DENIED', message: '禁止跟随创建跳转' } })
    }
    if (method === 'POST' && path === createPath && !new URL(request.url()).search && permitted) {
      const submission = permitted
      permitted = undefined
      writes.push({ target, body: structuredClone(request.postDataJSON() as Record<string, unknown>) })
      try {
        submission.gate?.received.resolve()
        if (submission.gate) await submission.gate.release.promise
        if ('abort' in submission.reply) await route.abort(submission.reply.abort)
        else await route.fulfill({ ...submission.reply, contentType: 'application/json' })
      } finally { submission.gate?.finished.resolve() }
      return
    }
    if (method === 'GET' && path === jobPath && !new URL(request.url()).search) return route.fulfill({ status: jobStatus, json: job })
    if (method === 'GET' && path === '/api/v1/jobs' && new URL(request.url()).search === '?type=docker-container-create') return route.fulfill({ json: jobs })
    if (method === 'GET' && path === inventoryPath) {
      if (inventoryMode === 'failure') return route.fulfill({ status: 503, json: { code: 'FIXTURE_INVENTORY_FAILED', message: '只读列表暂不可用' } })
      inventoryTimestamp = Math.max(inventoryTimestamp + 1, await page.evaluate(() => Date.now()))
      const containers = inventoryMode === 'same-name' ? [{
        id: 'unrelated-same-name-fixture', name: submittedName, image: submittedImage,
        state: 'running', status: 'Up 3 hours', ports: [],
        createdAt: '2026-10-12T12:00:00Z', startedAt: '2026-10-12T12:00:00Z',
        exitCode: 0, restartCount: 0, health: 'none', projectId: '', projectName: '', serviceName: '',
      }] : []
      return route.fulfill({ json: {
        collectedAt: new Date(inventoryTimestamp).toISOString(), projects: [], containers,
        engine: { serverVersion: 'fixture', operatingSystem: 'fixture', architecture: 'fixture', containers: containers.length, containersRunning: containers.length, containersStopped: 0, images: 2 },
      } })
    }
    if (method === 'GET' && (reads.has(path) || path.startsWith('/api/v1/preferences/lists/'))) return route.fallback()
    denied.push(target)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期的 API 请求已拒绝' } })
  })
  return {
    requests, writes, denied,
    jobs(value: unknown) { jobs = value },
    job(value: unknown, status = 200) { job = value; jobStatus = status },
    inventory(mode: InventoryMode) { inventoryMode = mode },
    async expectInventoryReady() {
      const recovery = page.locator('.inventory-recovery')
      if (inventoryMode === 'failure') await expect(recovery).toContainText('Docker 状态更新失败，显示上次数据')
      else await expect(recovery).toHaveCount(0)
    },
    allowOneCreate(reply: Reply = invalidJson, hold = false) {
      expect(permitted, '不能覆盖尚未消费的 fixture 提交许可').toBeUndefined()
      const gate: Gate | undefined = hold ? { transportFailure: 'abort' in reply, received: deferred(), release: deferred(), finished: deferred() } : undefined
      permitted = { reply, gate }
      return gate
    },
    async release(gate: Gate) {
      const completed = gate.transportFailure
        ? page.waitForEvent('requestfailed', (request) => new URL(request.url()).pathname === createPath && request.method() === 'POST').then(() => undefined)
        : page.waitForResponse((value) => new URL(value.url()).pathname === createPath && value.request().method() === 'POST').then((response) => response.finished())
      gate.release.resolve()
      await gate.finished.promise
      await completed
      await settle(page)
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>

async function settle(page: Page) {
  await page.evaluate(() => new Promise<void>((done) => requestAnimationFrame(() => requestAnimationFrame(() => done()))))
}

async function openImages(page: Page) {
  await page.goto('/docker', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: '镜像', exact: true }).click()
  await expect(page.getByRole('button', { name: `从镜像 ${submittedImage} 创建容器`, exact: true })).toBeVisible()
}

async function openDraft(page: Page, image = submittedImage) {
  await page.getByRole('button', { name: `从镜像 ${image} 创建容器`, exact: true }).click()
  await expect(drawer(page)).toBeVisible()
  await expect(drawer(page).getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0)
}

async function submitA(page: Page, runContainer = true) {
  await nameInput(page).fill(submittedName)
  await (runContainer ? createButton(page) : createOnlyButton(page)).click()
  await expect(preview(page)).toContainText(submittedName)
  await expect(preview(page)).toContainText(submittedImage)
  await preview(page).getByRole('button', { name: runContainer ? '确认创建并启动' : '确认创建', exact: true }).click()
  await expect(preview(page)).toBeHidden()
}

async function attemptSummary(page: Page) {
  return notice(page).locator('.protection-status').last().innerText()
}

async function expectUnknown(page: Page, runContainer = true) {
  await expect(notice(page)).toContainText('受理结果未知，容器可能已经创建')
  await expect(notice(page)).toContainText('空清单或同名容器都不能证明本次创建未发生')
  await expect(notice(page)).not.toContainText('private-')
  await expect(notice(page)).not.toContainText('untrusted-partial-id')
  await expect(notice(page)).not.toContainText(submittedName)
  await expect(notice(page)).not.toContainText(submittedImage)
  await expect(notice(page)).toContainText(runContainer ? '创建并启动' : '仅创建')
  await expect(page.getByRole('button', { name: '从镜像 alpine:3.20 创建容器', exact: true, includeHidden: true })).toBeDisabled()
  await expect(preview(page)).toBeHidden()
  await expect(page.getByRole('status', { name: '已确认的创建结果', exact: true })).toHaveCount(0)
  await expect(page.locator('.el-message')).toHaveCount(0)
}

async function expectOnePost(api: Fixture, runContainer = true) {
  expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([`POST ${createPath}`])
  expect(api.writes).toHaveLength(1)
  expect(api.writes[0]).toMatchObject({ target: `POST ${createPath}`, body: { name: submittedName, image: submittedImage, runContainer } })
  expect(api.denied).toEqual([])
}

async function closeDrawer(page: Page, method: 'x' | 'escape') {
  if (method === 'x') await drawer(page).locator('.el-drawer__close-btn').click()
  else await page.keyboard.press('Escape')
  await expect(drawer(page)).toBeHidden()
}

async function sourceProvenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--',
    'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json',
    'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts',
  ]).split('\n').filter(Boolean)
  const sourceHashes = Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [
    file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex'),
  ])))
  return {
    sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes,
    trackedDiffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex'),
  }
}

async function captureState(page: Page) {
  return notice(page).evaluate((panel) => {
    const selectors = ['h2', '.protection-status', '.protection-status > strong', '.protection-status > p', '.protection-actions', '.protection-actions button']
    return {
      viewport: { width: innerWidth, height: innerHeight },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY },
      focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label') },
      semantic: { notice: panel.textContent, inventoryRecovery: document.querySelector('.inventory-recovery')?.textContent ?? null,
        toasts: [...document.querySelectorAll('.el-message')].map(message => message.textContent) },
      controls: selectors.flatMap(selector => [...panel.querySelectorAll<HTMLElement>(selector)].map((element, index) => {
        const bounds = element.getBoundingClientRect(), style = getComputedStyle(element)
        const range = document.createRange(); range.selectNodeContents(element)
        const lines = [...range.getClientRects()].filter(line => line.width > 0 && line.height > 0)
        const clips: DOMRect[] = []
        for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
          const overflow = getComputedStyle(ancestor)
          if (/(hidden|clip|auto|scroll)/.test(`${overflow.overflowX} ${overflow.overflowY}`)) clips.push(ancestor.getBoundingClientRect())
        }
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return { selector, index, text: element.textContent?.trim(), bounds: bounds.toJSON(), lines: lines.map(line => line.toJSON()), clips: clips.map(clip => clip.toJSON()),
          scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
          visible: bounds.width > 0 && bounds.height > 0 && style.display !== 'none' && style.visibility !== 'hidden',
          inViewport: bounds.left >= 0 && bounds.top >= 0 && bounds.right <= innerWidth + 1 && bounds.bottom <= innerHeight + 1,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every(line => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every(clip => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
        }
      })),
    }
  })
}

// 只在真实浏览器执行时产出像素，PNG 与两份 JSON 都是 ui-captures 的直接子项。
// 通过普通滚动分别取警告与提交区，不强行让整张长表单挤进同一视口。
async function capture(page: Page, info: TestInfo, api: Fixture, stateName: string, region: 'notice' | 'actions') {
  // 成功读取核验新清单；失败读取保留并核验真实恢复提示，不隐藏或移除提示。
  await api.expectInventoryReady()
  await page.evaluate(() => document.fonts.ready)
  await (region === 'notice' ? notice(page) : notice(page).locator('.protection-actions')).scrollIntoViewIfNeeded()
  await settle(page)
  const provenance = await sourceProvenance()
  const beforeCapture = await captureState(page)
  const requestsBefore = [...api.requests]
  const name = `container-create-unknown-${stateName}-${page.viewportSize()!.width}`
  const directory = info.outputPath('ui-captures')
  await mkdir(directory, { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const afterCapture = await captureState(page)
  // 保留全页完整 axe 结果，不禁用对比度、ARIA 或其他规则。
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await captureState(page)
  const metadata = {
    ...provenance, fixtureOnly: true, fixtureSource: 'frontend/e2e/container-create-outcome.spec.ts + frontend/e2e/mockApi.ts',
    stateName, region, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
    screenshot: `${name}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    requestsBefore, requestsAfter: [...api.requests], writes: api.writes, denied: api.denied,
    beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  await info.attach(`${name}-axe-state`, { path: axePath, contentType: 'application/json' })

  // 几何或 axe 失败仍记录后续状态的真实证据；soft 断言仍使整个用例失败。
  expect.soft(afterCapture).toEqual(beforeCapture)
  expect.soft(afterAxe.semantic).toEqual(beforeCapture.semantic)
  expect.soft(afterAxe.scroll).toEqual(beforeCapture.scroll)
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  const required = region === 'notice' ? ['h2', '.protection-status > strong', '.protection-status > p'] : ['.protection-actions', '.protection-actions button']
  for (const control of beforeCapture.controls.filter((item) => required.includes(item.selector))) {
    expect.soft(control.visible && control.inViewport, JSON.stringify(control)).toBe(true)
    expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (control.selector.endsWith('button') || control.selector.endsWith('close-btn') || control.selector.endsWith('.action-button')) expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
  }
  expect.soft(axe.violations).toEqual([])
}

// 即使硬断言或软断言失败也保留最终请求账本；诊断写入失败不能遮盖原始测试错误。
async function withFinalLedger(info: TestInfo, api: Awaited<ReturnType<typeof fixture>>, name: string, journey: (details: Record<string, unknown>) => Promise<void>) {
  const details: Record<string, unknown> = {}
  let assertionsFinished = false
  let diagnosticFailure: unknown
  try {
    await journey(details)
    assertionsFinished = true
  } finally {
    try {
      const path = info.outputPath('ui-captures', `${name}-capture-state.json`)
      await mkdir(info.outputPath('ui-captures'), { recursive: true })
      await writeFile(path, JSON.stringify({ ...await sourceProvenance(), fixtureOnly: true, ...details,
        journeyCompleted: assertionsFinished && info.errors.length === 0,
        requests: api.requests, writes: api.writes, denied: api.denied,
      }, null, 2))
      await info.attach(name, { path, contentType: 'application/json' })
    } catch (error) {
      diagnosticFailure = error
      console.warn('创建流程诊断账本保存失败；保留原始测试错误', error)
    }
  }
  if (diagnosticFailure && info.errors.length === 0) throw diagnosticFailure
}

for (const width of [390, 1440]) {
  test(`${width}px 无效 202 保留会话保护；只读清单不能自动解锁或认领同名容器`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page)
    await withFinalLedger(info, api, `creation-unknown-result-${width}`, async () => {
      await openImages(page); await openDraft(page)
      api.allowOneCreate(); await submitA(page)
      await expectUnknown(page)
      await nameInput(page).fill('edited-current-draft')
      await nameInput(page).press('Enter')
      await expect(createButton(page)).toBeDisabled()
      await expect(createOnlyButton(page)).toBeDisabled()
      await closeDrawer(page, 'x')
      await capture(page, info, api, 'immutable-notice', 'notice')
      await capture(page, info, api, 'disabled-replay', 'actions')
      for (const mode of ['empty', 'same-name', 'failure'] as const) {
        api.inventory(mode)
        const before = api.requests.length
        await inspectButton(page).click()
        await expect(notice(page)).toContainText(mode === 'failure' ? '容器清单未成功刷新' : '容器清单已刷新')
        expect(api.requests.slice(before)).toEqual(['GET /api/v1/jobs?type=docker-container-create', `GET ${inventoryPath}`])
        await expectUnknown(page)
        await expect(notice(page).getByRole('button', { name: '解除本会话防重复保护', exact: true }))[mode === 'failure' ? 'toBeDisabled' : 'toBeEnabled']()
        await expectOnePost(api)
        if (mode === 'same-name') await capture(page, info, api, 'same-name-still-unknown', 'notice')
      }
      // 经过 SPA 标签切换、键盘激活禁用入口仍不能开启 B 或覆盖 A。
      await page.getByRole('button', { name: '容器', exact: true }).click()
      await page.getByRole('button', { name: '镜像', exact: true }).click()
      await expectUnknown(page)
      await capture(page, info, api, 'reopened-b-retains-a', 'notice')
      await capture(page, info, api, 'reopened-b-disabled-replay', 'actions')
      api.inventory('empty'); await inspectButton(page).click()
      const release = notice(page).getByRole('button', { name: '解除本会话防重复保护', exact: true })
      await expect(release).toBeEnabled(); await release.focus(); await release.press('Enter')
      const confirmation = page.getByRole('dialog', { name: '解除创建防重复保护', exact: true })
      await confirmation.getByRole('button', { name: '保留保护', exact: true }).click()
      await expectUnknown(page)
      await release.click()
      await confirmation.getByRole('button', { name: '确认风险并解除', exact: true }).click()
      await expect(notice(page)).toHaveCount(0)
      await openDraft(page, 'alpine:3.20')
      await expect(nameInput(page)).toHaveValue('alpine')
      await expectOnePost(api)
    })
  })
}

for (const sample of [
  { label: '204 空响应', reply: { status: 204, body: '' }, runContainer: true },
  { label: '200 缺失容器身份', reply: { status: 200, body: '{}' }, runContainer: true },
  { label: '201 启动标志与提交不符', reply: { status: 201, body: JSON.stringify({ containerId: 'fixture-result', name: submittedName, image: submittedImage, state: 'stopped', created: true, started: false, runContainer: false }) }, runContainer: true },
  { label: '仅创建 202 无效 JSON', reply: invalidJson, runContainer: false },
  { label: '创建传输失败', reply: transportFailure, runContainer: true },
  { label: '409 补偿失败', reply: cleanupFailure, runContainer: true },
]) {
  test(`${sample.label} 进入未知状态且没有创建后刷新`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const api = await fixture(page)
    await openImages(page); await openDraft(page)
    const before = api.requests.length
    api.allowOneCreate(sample.reply)
    await submitA(page, sample.runContainer)
    await expectUnknown(page, sample.runContainer)
    expect(api.requests.slice(before)).toEqual([`POST ${createPath}`])
    await expectOnePost(api, sample.runContainer)
  })
}

for (const outcome of [
  { label: '无效成功响应', reply: invalidJson, unknown: true, close: 'x' },
  { label: '400 参数校验拒绝', reply: validationFailure, unknown: false, close: 'escape' },
  { label: '传输失败', reply: transportFailure, unknown: true, close: 'escape' },
  { label: '409 补偿失败', reply: cleanupFailure, unknown: true, close: 'x' },
] as const) {
  test(`A 迟到${outcome.label}不覆盖 B，不弹旧错误或重放提交`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const api = await fixture(page)
    await openImages(page); await openDraft(page)
    const held = api.allowOneCreate(outcome.reply, true)!
    await submitA(page); await held.received.promise
    await closeDrawer(page, outcome.close)
    await expect(page.getByRole('button', { name: '从镜像 alpine:3.20 创建容器', exact: true, includeHidden: true })).toBeDisabled()
    const before = api.requests.length
    await api.release(held)
    await expect(drawer(page)).toBeHidden()
    await expect(page.locator('.el-message')).toHaveCount(0)
    expect(api.requests.slice(before)).toEqual([])
    if (outcome.unknown) {
      await expectUnknown(page)
      const originalAttempt = await attemptSummary(page)
      await page.getByRole('button', { name: '容器', exact: true }).click()
      await page.getByRole('button', { name: '镜像', exact: true }).click()
      await expectUnknown(page)
      expect(await attemptSummary(page)).toEqual(originalAttempt)
    } else {
      await expect(notice(page)).toHaveCount(0)
      await openDraft(page, 'alpine:3.20')
      await nameInput(page).fill('late-result-draft-b')
      await createButton(page).click()
      await expect(preview(page)).toContainText('late-result-draft-b')
      await expect(preview(page)).toContainText('alpine:3.20')
      await preview(page).getByRole('button', { name: '返回修改', exact: true }).click()
    }
    await expectOnePost(api)
  })
}

test('当前草稿收到 400 参数校验拒绝后可修改并重新预览', async ({ page }) => {
  const api = await fixture(page)
  await openImages(page); await openDraft(page)
  api.allowOneCreate(validationFailure)
  await submitA(page)
  await expect(page.locator('.el-message')).toContainText('创建任务未被接收，请检查配置后重新确认。')
  await expect(notice(page)).toHaveCount(0)
  await expect(createButton(page)).toBeEnabled()
  await nameInput(page).fill('corrected-draft')
  await createButton(page).click()
  await expect(preview(page)).toContainText('corrected-draft')
  await preview(page).getByRole('button', { name: '返回修改', exact: true }).click()
  await expectOnePost(api)
})

test('307 创建跳转直接进入未知，不向内存跳转目标重发 POST', async ({ page }) => {
  const api = await fixture(page)
  await openImages(page); await openDraft(page)
  const before = api.requests.length
  api.allowOneCreate({ status: 307, body: '', headers: { location: redirectTargetPath } })
  await submitA(page)
  await expectUnknown(page)
  expect(api.requests.slice(before)).toEqual([`POST ${createPath}`])
  expect(api.requests.some((request) => request.endsWith(` ${redirectTargetPath}`))).toBe(false)
  await expectOnePost(api)
})

test('503 Agent 明确接纳前拒绝不保留未知锁且不泄漏后台诊断', async ({ page }) => {
  const api = await fixture(page)
  await openImages(page); await openDraft(page)
  api.allowOneCreate(agentFailure); await submitA(page)
  await expect(page.locator('.el-message')).toContainText('创建任务未被接收，请检查配置后重新确认。')
  await expect(page.locator('body')).not.toContainText('private-agent-diagnostic')
  await expect(notice(page)).toHaveCount(0)
  await expect(createButton(page)).toBeEnabled()
  await expectOnePost(api)
})

for (const width of [390, 1440]) for (const runContainer of [true, false]) {
  test(`${width}px ${runContainer ? '启动' : '仅创建'} 202 后 queued/running/pending/404 都不能放开保护；只认精确 Job 的完整结果`, async ({ page }, info) => {
    test.setTimeout(180_000)
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page)
    await withFinalLedger(info, api, `creation-exact-result-${runContainer}-${width}`, async details => {
      await openImages(page); await openDraft(page)
      api.allowOneCreate({ status: 202, body: JSON.stringify(creationJobFixture(jobId)) })
      await submitA(page, runContainer)
      await expect(drawer(page)).toBeHidden()
      await expect(notice(page)).toContainText('任务已受理，尚未确认创建完成')
      await expect(page.getByRole('status', { name: '已确认的创建结果', exact: true })).toHaveCount(0)
      const release = notice(page).getByRole('button', { name: '解除本会话防重复保护', exact: true })
      await page.setViewportSize({ width, height: 900 })
      await capture(page, info, api, `${runContainer}-queued`, 'notice')
      for (const sample of [
        { phase: 'queued-read', job: creationJobFixture(jobId), status: 200 },
        { phase: 'running', job: creationJobFixture(jobId, 'running'), status: 200 },
        { phase: 'pending', job: { ...creationJobFixture(jobId, 'running'), persistenceStatus: 'pending' }, status: 200 },
        { phase: '404', job: { code: 'JOB_NOT_FOUND', message: '不存在', requestId: 'fixture' }, status: 404 },
        { phase: 'wrong-id', job: creationJobFixture('different-job', 'completed', runContainer), status: 200 },
        { phase: 'wrong-started', job: creationJobFixture(jobId, 'completed', !runContainer), status: 200 },
      ]) {
        api.job(sample.job, sample.status)
        await inspectButton(page).click()
        await expect(inspectButton(page)).toBeEnabled()
        await expect(release).toBeDisabled()
        await expect(page.getByRole('status', { name: '已确认的创建结果', exact: true })).toHaveCount(0)
        await expect(page.getByRole('button', { name: '从镜像 alpine:3.20 创建容器', exact: true, includeHidden: true })).toBeDisabled()
        await capture(page, info, api, `${runContainer}-${sample.phase}`, 'notice')
        await capture(page, info, api, `${runContainer}-${sample.phase}-locked`, 'actions')
      }
      api.job(creationJobFixture(jobId, 'completed', runContainer))
      const completedResponsePromise = page.waitForResponse(value => new URL(value.url()).pathname === jobPath && value.request().method() === 'GET')
      await inspectButton(page).click()
      const completedResponse = await completedResponsePromise
      const completedJob: unknown = await completedResponse.json()
      details.completedStatus = completedResponse.status()
      details.completedJob = completedJob
      expect(completedResponse.status()).toBe(200)
      expect(completedJob).toEqual(creationJobFixture(jobId, 'completed', runContainer))
      const result = page.getByRole('status', { name: '已确认的创建结果', exact: true })
      await expect(result).toContainText(fixtureCreatedContainerId)
      await expect(result).toContainText(runContainer ? '创建调用已确认创建并启动' : '创建调用已确认创建')
      await expect(result).toContainText('不保证容器持续运行')
      details.result = await result.innerText()
      await capture(page, info, api, `${runContainer}-exact-completed`, 'notice')
      await expect(page.getByRole('button', { name: '从镜像 alpine:3.20 创建容器', exact: true, includeHidden: true })).toBeEnabled()
      await page.getByRole('button', { name: '容器', exact: true }).click()
      await expect(result).toContainText(fixtureCreatedContainerId)
      await expectOnePost(api, runContainer)
      expect(api.requests.filter(target => target.startsWith('GET /api/v1/jobs/'))).toEqual(Array(7).fill(`GET ${jobPath}`))
      expect(info.errors, '完整几何和axe断言通过后才记录流程完成').toEqual([])
    })
  })
}

test('未知接纳只读任务列表观察 active 后，坏列表和读取失败不能清除活动保护或认领历史', async ({ page }) => {
  const api = await fixture(page)
  await openImages(page); await openDraft(page)
  api.allowOneCreate(); await submitA(page); await closeDrawer(page, 'escape')
  const release = notice(page).getByRole('button', { name: '解除本会话防重复保护', exact: true })
  api.jobs({ jobs: [creationJobFixture('unclaimed-active', 'running')] })
  await inspectButton(page).click()
  await expect(notice(page)).toContainText('观察到活动创建任务')
  await expect(release).toBeDisabled()
  api.jobs({ jobs: [null] })
  await inspectButton(page).click()
  await expect(notice(page)).toContainText('任务读取失败')
  await expect(notice(page)).toContainText('观察到活动创建任务')
  await expect(release).toBeDisabled()
  api.jobs({ jobs: [creationJobFixture('unclaimed-history', 'completed')] })
  api.inventory('failure'); await inspectButton(page).click()
  await expect(release).toBeDisabled()
  api.inventory('same-name'); await inspectButton(page).click()
  await expect(release).toBeEnabled()
  await expectUnknown(page)
  expect(api.requests.some(target => target.startsWith('GET /api/v1/jobs/'))).toBe(false)
  await expectOnePost(api)
})
