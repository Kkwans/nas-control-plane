import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { overviewDocker, summary } from './mockApi'
import type { DockerContainerCreateInput } from '../src/api/system'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })
const stamp = '2026-10-14T20:00:00.000Z'
const createPath = '/api/v1/docker/containers'
const argv = ['/app/server ', '  leading  spaces  ', ' ', '"quoted value"', '$APP_MODE && echo ok; $(literal) | *', '--name=a b']
const mount = { type: 'bind' as const, source: '/fixture/source  ', target: '/data/target  ', readOnly: false }
const device = { hostPath: '/dev/fixture  ', containerPath: '/dev/target  ', cgroupPermissions: 'rwm' }
const inventory = { ...overviewDocker, collectedAt: stamp, containers: [], projects: [], engine: { ...overviewDocker.engine, containers: 0, containersRunning: 0, containersStopped: 0, images: 1 } }
function expectedInput(defaultCommand: boolean): DockerContainerCreateInput {
  return { image: 'identity:latest', name: 'identity', restartPolicy: 'no', environment: {}, mounts: defaultCommand ? [] : [mount], ports: [],
    ...(defaultCommand ? {} : { command: argv }), privileged: false, capAdd: [], capDrop: [], devices: defaultCommand ? [] : [device], runContainer: true }
}
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

// 只允许指定来源的静态 GET 与精确 API 白名单。创建许可仅供一次真实确认点击消费；
// 未知请求、重发、WebSocket 均失败关闭，内存 SSE 不访问 Docker、NAS 或宿主机。
async function fixture(page: Page, origin: string, expected: DockerContainerCreateInput, allowRootDirectory = false) {
  await page.clock.setFixedTime(new Date(stamp))
  await page.addInitScript(() => {
    class SilentSource extends EventTarget {
      static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2
      readyState = 1; onopen: (() => void) | null = null; onerror: (() => void) | null = null
      constructor(readonly url: string) { super(); queueMicrotask(() => { this.onopen?.(); this.dispatchEvent(new Event('open')) }) }
      close() { this.readyState = 2 }
    }
    window.EventSource = SilentSource as unknown as typeof EventSource
  })
  const requests: string[] = [], denied: string[] = [], errors: string[] = []
  const writes: unknown[] = [], received = deferred(), release = deferred()
  let authorized = false
  const reads = new Map<string, unknown>([
    ['/api/v1/auth/status', { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } }],
    ['/api/v1/preferences', { refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10, sidebarDefault: 'expanded', linkOpenMode: 'same-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'] }],
    ['/api/v1/system/capabilities', { hostname: 'fixture', architecture: 'aarch64', docker: true, compose: true, systemd: true, journald: true, cgroupVersion: 2, dataVolumes: [], networkInterfaces: [] }],
    ['/api/v1/system/summary', { ...summary, collectedAt: stamp }],
    ['/api/v1/sites', { collectedAt: stamp, sites: [], discovery: { status: 'complete', probeAvailable: true, candidateCount: 0, verifiedCount: 0, failedCount: 0, issues: [] } }],
    ['/api/v1/sites/ignored', []], ['/api/v1/jobs', { jobs: [] }], ['/api/v1/jobs?type=docker-image-pull', { jobs: [] }],
    ['/api/v1/docker/inventory', inventory],
    ['/api/v1/docker/resources', { collectedAt: stamp, networks: [], volumes: [] }],
    ['/api/v1/docker/images', { collectedAt: stamp, images: [{ id: 'sha256:identity-fixture', repoTags: ['identity:latest'], repoDigests: [], sizeBytes: 1024, createdAt: stamp, containers: 0 }] }],
    ...['docker.projects', 'docker.containers', 'docker.images.local', 'docker.image-downloads'].map((listKey): [string, unknown] => [`/api/v1/preferences/lists/${listKey}`, { listKey, pageSize: 10, sortKey: '', sortDirection: 'asc' }]),
  ])
  if (allowRootDirectory) reads.set('/api/v1/files/entries?path=%2F&limit=100', { path: '/', parent: '/', collectedAt: stamp, entries: [{ name: 'fixture.txt', path: '/fixture.txt', type: 'file', readable: true }] })
  page.on('pageerror', (error) => errors.push(error.message))
  await page.routeWebSocket('**', (socket) => { denied.push(`WS ${socket.url()}`); socket.close() })
  await page.route('**/*', async (route) => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin || request.method() !== 'GET' || url.pathname.startsWith('/api/')) {
      denied.push(`${request.method()} ${url.href}`); return route.abort('blockedbyclient')
    }
    return route.fallback()
  })
  await page.route('**/api/**', async (route) => {
    const request = route.request(), url = new URL(request.url()), target = `${request.method()} ${url.pathname}${url.search}`
    requests.push(target)
    if (url.origin !== origin) { denied.push(target); return route.abort('blockedbyclient') }
    const readKey = url.pathname + url.search
    if (request.method() === 'GET' && reads.has(readKey)) return route.fulfill({ json: reads.get(readKey) })
    if (request.method() === 'POST' && url.pathname === createPath && !url.search && authorized) {
      authorized = false
      const body: unknown = request.postDataJSON()
      writes.push(body)
      received.resolve()
      expect(body).toEqual(expected)
      await release.promise
      return route.fulfill({ status: 201, json: { containerId: 'created-identity', name: 'identity', image: 'identity:latest', state: 'running', created: true, started: true, runContainer: true } })
    }
    denied.push(target)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return { requests, denied, errors, writes, received, release, authorize() { expect(authorized).toBe(false); expect(writes).toHaveLength(0); authorized = true } }
}
type Fixture = Awaited<ReturnType<typeof fixture>>
async function provenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes: Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')]))) }
}

async function geometry(page: Page) {
  return page.locator('.command-preview').evaluate((code) => {
    const cell = code.parentElement!, dialog = code.closest('[role="dialog"]')!
    const bounds = cell.getBoundingClientRect(), dialogBounds = dialog.getBoundingClientRect()
    const range = document.createRange(); range.selectNodeContents(code)
    const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
    const node = code.firstChild!
    const characters = Array.from({ length: node.textContent!.length }, (_, index) => {
      const character = document.createRange(); character.setStart(node, index); character.setEnd(node, index + 1)
      return { value: node.textContent![index], bounds: character.getBoundingClientRect().toJSON() }
    })
    const clips: Array<{ x: boolean; y: boolean; bounds: DOMRect }> = []
    for (let ancestor: Element | null = cell; ancestor; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor)
      const x = /hidden|clip|auto|scroll/.test(style.overflowX), y = /hidden|clip|auto|scroll/.test(style.overflowY)
      if (x || y) clips.push({ x, y, bounds: ancestor.getBoundingClientRect() })
    }
    const pathPreviews = [...dialog.querySelectorAll('.path-preview')].map((path) => {
      const text = path.firstChild!, parentBounds = path.parentElement!.getBoundingClientRect()
      const whole = document.createRange(); whole.selectNodeContents(path)
      const lines = [...whole.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
      const spaces = Array.from({ length: text.textContent!.length }, (_, index) => index).filter((index) => text.textContent![index] === ' ').map((index) => {
        const range = document.createRange(); range.setStart(text, index); range.setEnd(text, index + 1)
        return range.getBoundingClientRect().width
      })
      return { text: path.textContent, whiteSpace: getComputedStyle(path).whiteSpace, spaces,
        bounds: path.getBoundingClientRect().toJSON(), lines: lines.map((line) => line.toJSON()),
        fitsLabel: lines.length > 0 && lines.every((line) => line.left >= parentBounds.left - 1 && line.right <= parentBounds.right + 1 && line.top >= parentBounds.top - 1 && line.bottom <= parentBounds.bottom + 1) }
    })
    return { pathPreviews, text: code.textContent, whiteSpace: getComputedStyle(code).whiteSpace, characters,
      viewport: { width: innerWidth, height: innerHeight }, root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      bounds: bounds.toJSON(), dialogBounds: dialogBounds.toJSON(), lines: lines.map((line) => line.toJSON()),
      clips: clips.map((clip) => ({ ...clip, bounds: clip.bounds.toJSON() })),
      fullTextVisible: lines.length > 0 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1 && line.left >= -1 && line.right <= innerWidth + 1 && line.top >= -1 && line.bottom <= innerHeight + 1 && clips.every((clip) => (!clip.x || line.left >= clip.bounds.left - 1 && line.right <= clip.bounds.right + 1) && (!clip.y || line.top >= clip.bounds.top - 1 && line.bottom <= clip.bounds.bottom + 1))),
    }
  })
}
async function verifyConfigurationScroll(page: Page) {
  const dialog = page.getByRole('dialog', { name: '确认容器配置', exact: true })
  const region = dialog.getByRole('region', { name: '容器配置详情', exact: true })
  await region.focus()
  await page.waitForTimeout(350)
  const before = await modalChrome(page)
  await page.keyboard.press('End')
  await expect.poll(() => region.evaluate((element) => Math.abs(element.scrollHeight - element.clientHeight - element.scrollTop))).toBeLessThanOrEqual(1)
  const bottom = await region.evaluate((element) => {
    const bounds = element.getBoundingClientRect(), last = element.querySelector('.preview-grid dd:last-of-type')!.getBoundingClientRect()
    return { scrollTop: element.scrollTop, scrollHeight: element.scrollHeight, clientHeight: element.clientHeight,
      lastFieldVisible: last.top >= bounds.top - 1 && last.bottom <= bounds.bottom + 1 }
  })
  expect(bottom.lastFieldVisible).toBe(true)
  const atBottom = await modalChrome(page)
  expect(atBottom).toEqual(before); assertModalChrome(atBottom)
  await page.keyboard.press('Home')
  await expect.poll(() => region.evaluate((element) => element.scrollTop)).toBe(0)
  await dialog.locator('.el-dialog__headerbtn').focus()
  return { before, atBottom, bottom, homeRestoredTop: true }
}
async function modalChrome(page: Page) {
  return page.getByRole('dialog', { name: '确认容器配置', exact: true }).evaluate((dialog) => {
    const header = dialog.querySelector<HTMLElement>('.el-dialog__header')!
    const body = dialog.querySelector<HTMLElement>('.el-dialog__body')!
    const footer = dialog.querySelector<HTMLElement>('.el-dialog__footer')!
    const bounds = (element: Element) => element.getBoundingClientRect().toJSON()
    return { viewport: { width: innerWidth, height: innerHeight }, dialog: bounds(dialog.querySelector('.el-dialog')!), header: bounds(header), body: bounds(body), footer: bounds(footer),
      buttons: [...footer.querySelectorAll('button')].map((button) => {
        const rect = button.getBoundingClientRect()
        const top = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2)
        return { text: button.textContent, bounds: rect.toJSON(), hitTest: top !== null && (top === button || button.contains(top)) }
      }) }
  })
}
function assertModalChrome(chrome: Awaited<ReturnType<typeof modalChrome>>) {
  expect(chrome.dialog.top).toBeGreaterThanOrEqual(-1)
  expect(chrome.dialog.bottom).toBeLessThanOrEqual(chrome.viewport.height + 1)
  expect(chrome.header.bottom).toBeLessThanOrEqual(chrome.body.top + 1)
  expect(chrome.body.bottom).toBeLessThanOrEqual(chrome.footer.top + 1)
  expect(chrome.buttons).toHaveLength(2)
  for (const button of chrome.buttons) {
    expect(button.bounds.width).toBeGreaterThan(0); expect(button.bounds.height).toBeGreaterThan(0)
    expect(button.bounds.left).toBeGreaterThanOrEqual(-1); expect(button.bounds.right).toBeLessThanOrEqual(chrome.viewport.width + 1)
    expect(button.bounds.top).toBeGreaterThanOrEqual(-1); expect(button.bounds.bottom).toBeLessThanOrEqual(chrome.viewport.height + 1)
    expect(button.hitTest, JSON.stringify(button)).toBe(true)
  }
}
async function capture(page: Page, info: TestInfo, api: Fixture, defaultCommand: boolean) {
  await page.evaluate(() => document.fonts.ready)
  const scrollEvidence = await verifyConfigurationScroll(page)
  await page.locator('.command-preview').scrollIntoViewIfNeeded()
  // 等待弹窗进入动画结束，截图和几何必须来自同一稳定布局。
  await page.waitForTimeout(350)
  const before = { ...await geometry(page), chrome: await modalChrome(page) }, requestsBefore = [...api.requests]
  const prefix = `docker-create-identity-${defaultCommand ? 'default' : 'argv-paths'}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${prefix}.png`)
  await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
  const after = { ...await geometry(page), chrome: await modalChrome(page) }, axe = await new AxeBuilder({ page }).analyze(), afterAxe = { ...await geometry(page), chrome: await modalChrome(page) }
  const metadata = { scrollEvidence, ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false, expectedInput: expectedInput(defaultCommand), expectedPreview: defaultCommand ? '使用镜像默认命令' : JSON.stringify(argv), testTitle: info.title, screenshot: `${prefix}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'), beforeCapture: before, afterCapture: after, afterAxe, requestsBefore, requestsAfterAxe: [...api.requests], writes: [...api.writes], denied: [...api.denied], axe }
  const statePath = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await info.attach(prefix, { path: png, contentType: 'image/png' })
  await info.attach(`${prefix}-axe-state`, { path: statePath, contentType: 'application/json' })
  expect(api.requests).toEqual(requestsBefore); expect(api.writes).toEqual([])
  for (const sample of [before, after, afterAxe]) {
    assertModalChrome(sample.chrome)
    expect(sample.text).toBe(metadata.expectedPreview)
    expect(sample.whiteSpace).toBe('pre-wrap')
    expect(sample.pathPreviews.map((path) => path.text)).toEqual(defaultCommand ? [] : [mount.source, mount.target, device.hostPath, device.containerPath].map((path) => JSON.stringify(path)))
    for (const path of sample.pathPreviews) {
      expect(path.whiteSpace).toBe('break-spaces'); expect(path.fitsLabel, JSON.stringify(path)).toBe(true)
      expect(path.spaces.every((width) => width > 0)).toBe(true)
    }
    expect(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.clientWidth + 1)
    expect(sample.fullTextVisible, JSON.stringify(sample)).toBe(true)
    if (!defaultCommand) {
      expect(JSON.parse(sample.text!)).toEqual(argv)
      // 每个空格必须占据真实字形宽度，不能只验证 DOM 字符串或截图存在。
      expect(sample.characters.filter((character) => character.value === ' ').every((character) => character.bounds.width > 0)).toBe(true)
    }
  }
  expect(after).toEqual(before); expect(afterAxe).toEqual(before)
  expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
  return {
    prefix, screenshot: `${prefix}.png`, screenshotSha256: metadata.screenshotSha256,
    captureState: `${prefix}-axe-capture-state.json`,
    captureStateSha256: createHash('sha256').update(await readFile(statePath)).digest('hex'),
  }
}

for (const width of [390, 1440]) for (const defaultCommand of [false, true]) {
  test(`${width}px ${defaultCommand ? '全空白命令省略 command' : 'argv 与设备挂载路径保留原始空白'}，确认前零写入且仅接受一次 201 回执`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const expected = expectedInput(defaultCommand), api = await fixture(page, new URL(baseURL!).origin, expected)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    await page.getByRole('button', { name: '从镜像 identity:latest 创建容器', exact: true }).filter({ visible: true }).click()
    const drawer = page.locator('.create-container-drawer')
    await expect(drawer).toBeVisible()
    await expect.poll(() => api.requests.includes('GET /api/v1/docker/resources')).toBe(true)
    await expect(drawer.getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0)
    await drawer.getByRole('textbox', { name: '容器启动参数（每行一个）', exact: true }).fill(defaultCommand ? ' \t\n\u00a0\u3000' : '\n' + argv.join('\n\n') + '\n')
    if (!defaultCommand) {
      await drawer.getByRole('button', { name: '添加挂载', exact: true }).click()
      await drawer.getByPlaceholder('/volume2/data', { exact: true }).fill(mount.source)
      await drawer.getByPlaceholder('容器路径，例如 /data', { exact: true }).fill(mount.target)
      await drawer.locator('.advanced-toggle').click()
      await drawer.getByRole('button', { name: '添加设备', exact: true }).click()
      await drawer.getByPlaceholder('主机设备 /dev/...', { exact: true }).fill(device.hostPath)
      await drawer.getByPlaceholder('容器设备 /dev/...', { exact: true }).fill(device.containerPath)
    }
    await drawer.getByRole('button', { name: '创建并启动', exact: true }).click()
    const preview = page.getByRole('dialog', { name: '确认容器配置', exact: true })
    await expect(preview).toBeVisible()
    expect(api.writes).toEqual([])
    const previewEvidence = await capture(page, info, api, defaultCommand)
    api.authorize()
    const responsePromise = page.waitForResponse((response) => new URL(response.url()).pathname === createPath && response.request().method() === 'POST')
    try {
      await preview.getByRole('button', { name: '确认创建并启动', exact: true }).click()
      await api.received.promise
      expect(api.writes).toEqual([expected])
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      await expect(drawer).toBeVisible()
      await expect(drawer.getByRole('button', { name: '创建并启动', exact: true })).toBeDisabled()
      api.release.resolve()
      const response = await responsePromise
      expect(response.status()).toBe(201)
      expect(await response.json()).toEqual({ containerId: 'created-identity', name: 'identity', image: 'identity:latest', state: 'running', created: true, started: true, runContainer: true })
      await expect(page.getByText('容器“identity”已创建并启动', { exact: true })).toBeVisible()
      await expect(preview).not.toBeVisible()
      // 覆盖五秒刷新周期，确认无自动重放；未授权第二次 POST 会失败关闭。
      await page.waitForTimeout(6000)
      expect(api.writes).toEqual([expected])
      expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([`POST ${createPath}`])
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      // 独立记录提交后的回执与重放观察；不能冒充确认前 PNG 的截图时状态。
      const receiptPath = info.outputPath('ui-captures', `${previewEvidence.prefix}-receipt-capture-state.json`)
      const receipt = {
        ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
        evidenceKind: 'post-create-receipt', phase: 'after-create-response-and-replay-observation',
        testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
        preCreatePreviewEvidence: previewEvidence, replayObservationMilliseconds: 6000,
        expected, requests: [...api.requests], writes: [...api.writes], denied: [...api.denied], errors: [...api.errors],
        status: response.status(), response: await response.json(),
      }
      await writeFile(receiptPath, JSON.stringify(receipt, null, 2))
      await info.attach('strict-create-receipt', { path: receiptPath, contentType: 'application/json' })
    } finally { api.release.resolve() }
  })
}

// 真实键盘事件验证最上层弹窗；整个用例不授权创建请求。
for (const width of [390, 1440]) {
  test(`${width}px 创建和路径弹窗循环焦点，Escape 归还且取消零写入`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const api = await fixture(page, new URL(baseURL!).origin, expectedInput(true), true)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    await page.getByRole('button', { name: '从镜像 identity:latest 创建容器', exact: true }).filter({ visible: true }).click()
    const drawer = page.locator('.create-container-drawer')
    await expect(drawer).toBeVisible()
    await expect.poll(() => api.requests.includes('GET /api/v1/docker/resources')).toBe(true)
    await expect(drawer.getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0)
    const states: unknown[] = []
    for (const title of ['确认容器配置', '选择 NAS 路径']) {
      if (title === '选择 NAS 路径') await drawer.getByRole('button', { name: '添加挂载', exact: true }).click()
      const opener = drawer.getByRole('button', { name: title === '选择 NAS 路径' ? '浏览' : '仅创建', exact: true })
      await opener.focus(); await page.keyboard.press('Enter')
      const dialog = page.getByRole('dialog', { name: title, exact: true })
      await expect(dialog).toBeVisible()
      await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true)
      const close = dialog.locator('.el-dialog__headerbtn')
      const last = dialog.getByRole('button', { name: title === '选择 NAS 路径' ? '选择' : '确认创建', exact: true })
      await expect(last).toBeEnabled(); await last.focus()
      await page.keyboard.press('Tab'); await expect(close).toBeFocused()
      await page.keyboard.press('Shift+Tab'); await expect(last).toBeFocused()
      const geometry = await dialog.evaluate((element) => ({
        modal: element.getAttribute('aria-modal'), sectionAtBody: element.closest('section.create-container-dialogs')?.parentElement === document.body,
        hiddenAncestor: Boolean(element.closest('[inert], [aria-hidden="true"]')),
        dialogZ: Number(element.closest<HTMLElement>('.el-overlay')!.style.zIndex),
        drawerZ: Number(document.querySelector('.create-container-drawer')!.closest<HTMLElement>('.el-overlay')!.style.zIndex),
      }))
      expect(geometry.modal).toBe('true'); expect(geometry.sectionAtBody).toBe(true)
      expect(geometry.hiddenAncestor).toBe(false); expect(geometry.dialogZ).toBeGreaterThan(geometry.drawerZ)
      const axe = await new AxeBuilder({ page }).analyze()
      expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
      await page.keyboard.press('Escape')
      await expect(dialog).not.toBeVisible(); await expect(drawer).toBeVisible(); await expect(opener).toBeFocused()
      expect(api.writes).toEqual([])
      states.push({ title, geometry, axe, tabWrappedToClose: true, shiftTabWrappedToLast: true, escapeRestoredOpener: true })
    }
    await page.waitForTimeout(6000)
    expect(api.writes).toEqual([]); expect(api.requests.every((request) => request.startsWith('GET '))).toBe(true)
    expect(api.requests.filter((request) => request.startsWith('GET /api/v1/files/entries'))).toEqual(['GET /api/v1/files/entries?path=%2F&limit=100'])
    expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    const path = info.outputPath('ui-captures', `docker-create-keyboard-${width}-capture-state.json`)
    await writeFile(path, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
      evidenceKind: 'keyboard-modal-navigation', testTitle: info.title, states, requests: api.requests, writes: api.writes, denied: api.denied, errors: api.errors,
      noWriteObservationMilliseconds: 6000 }, null, 2))
    await info.attach('keyboard-modal-navigation', { path, contentType: 'application/json' })
  })
}

const portCases = [
  {
    name: 'loopback-ipv6',
    inputs: [
      { hostIp: ' 127.0.0.1 ', hostPort: '8080', containerPort: '80', protocol: 'tcp' },
      { hostIp: ' 2001:db8::1 ', hostPort: '8082', containerPort: '82', protocol: 'sctp' },
    ],
    ports: [
      { hostIp: '127.0.0.1', hostPort: 8080, containerPort: 80, protocol: 'tcp' },
      { hostIp: '2001:db8::1', hostPort: 8082, containerPort: 82, protocol: 'sctp' },
    ],
    preview: [
      ['主机 IP：127.0.0.1', '主机端口：8080', '容器端口：80/tcp'],
      ['主机 IP：2001:db8::1', '主机端口：8082', '容器端口：82/sctp'],
    ],
  },
  {
    name: 'wildcard-default',
    inputs: [
      { hostIp: '0.0.0.0', hostPort: '8081', containerPort: '81', protocol: 'udp' },
      { hostIp: '   ', hostPort: '', containerPort: '83', protocol: 'tcp' },
    ],
    ports: [
      { hostIp: '0.0.0.0', hostPort: 8081, containerPort: 81, protocol: 'udp' },
      { containerPort: 83, protocol: 'tcp' },
    ],
    preview: [
      ['主机 IP：0.0.0.0', '主机端口：8081', '容器端口：81/udp'],
      ['主机 IP：未指定（由 Docker 默认配置决定）', '主机端口：随机', '容器端口：83/tcp'],
    ],
  },
] satisfies Array<{ name: string; inputs: Array<{ hostIp: string; hostPort: string; containerPort: string; protocol: string }>; ports: DockerContainerCreateInput['ports']; preview: string[][] }>

async function portGeometry(page: Page) {
  return page.locator('ol[aria-label="端口映射"]').evaluate((list) => {
    const dialog = list.closest('[role="dialog"]')!
    const clips: Array<{ x: boolean; y: boolean; bounds: DOMRect }> = []
    for (let ancestor: Element | null = list; ancestor; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor)
      const x = /hidden|clip|auto|scroll/.test(style.overflowX), y = /hidden|clip|auto|scroll/.test(style.overflowY)
      if (x || y) clips.push({ x, y, bounds: ancestor.getBoundingClientRect() })
    }
    const fields = [...list.querySelectorAll('li > span')].map((field) => {
      const bounds = field.getBoundingClientRect(), range = document.createRange()
      range.selectNodeContents(field)
      const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
      return { text: field.textContent, bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()),
        fullTextVisible: lines.length > 0 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1 && line.left >= -1 && line.right <= innerWidth + 1 && line.top >= -1 && line.bottom <= innerHeight + 1 && clips.every((clip) => (!clip.x || line.left >= clip.bounds.left - 1 && line.right <= clip.bounds.right + 1) && (!clip.y || line.top >= clip.bounds.top - 1 && line.bottom <= clip.bounds.bottom + 1))) }
    })
    return { rows: [...list.querySelectorAll('li')].map((row) => [...row.querySelectorAll('span')].map((field) => field.textContent)), fields,
      viewport: { width: innerWidth, height: innerHeight }, root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      dialogBounds: dialog.getBoundingClientRect().toJSON(), listBounds: list.getBoundingClientRect().toJSON(),
      clips: clips.map((clip) => ({ ...clip, bounds: clip.bounds.toJSON() })), focusInside: dialog.contains(document.activeElement) }
  })
}

for (const width of [390, 1440]) for (const scenario of portCases) {
  test(`${width}px ${scenario.name} 端口确认预览与规范化请求一致且只接受一次 201`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const expected: DockerContainerCreateInput = { ...expectedInput(true), ports: scenario.ports }
    const api = await fixture(page, new URL(baseURL!).origin, expected)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    await page.getByRole('button', { name: '从镜像 identity:latest 创建容器', exact: true }).filter({ visible: true }).click()
    const drawer = page.locator('.create-container-drawer')
    await expect(drawer).toBeVisible()
    await expect.poll(() => api.requests.includes('GET /api/v1/docker/resources')).toBe(true)
    await expect(drawer.getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0)
    for (const [index, input] of scenario.inputs.entries()) {
      await drawer.getByRole('button', { name: '添加端口映射', exact: true }).click()
      const row = drawer.locator('.repeat-row--port').nth(index)
      await row.getByPlaceholder('主机 IP（可选）', { exact: true }).fill(input.hostIp)
      if (input.hostPort) await row.getByPlaceholder('主机端口', { exact: true }).fill(input.hostPort)
      await row.getByPlaceholder('容器端口', { exact: true }).fill(input.containerPort)
      if (input.protocol !== 'tcp') {
        // Element Plus 的输入框由当前值层覆盖；点击真实可见的选择器包装层。
        await expect(row.getByRole('combobox', { name: `第 ${index + 1} 个端口映射协议`, exact: true })).toHaveCount(1)
        await row.locator('.el-select__wrapper').click()
        await page.getByRole('option', { name: input.protocol.toUpperCase(), exact: true }).click()
      }
    }
    const opener = drawer.getByRole('button', { name: '创建并启动', exact: true })
    await opener.focus(); await page.keyboard.press('Enter')
    const preview = page.getByRole('dialog', { name: '确认容器配置', exact: true })
    await expect(preview).toBeVisible()
    await expect.poll(() => preview.evaluate((element) => element.contains(document.activeElement))).toBe(true)
    const confirm = preview.getByRole('button', { name: '确认创建并启动', exact: true })
    await confirm.focus(); await page.keyboard.press('Tab')
    await expect(preview.locator('.el-dialog__headerbtn')).toBeFocused()
    await page.keyboard.press('Shift+Tab'); await expect(confirm).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(preview).not.toBeVisible(); await expect(opener).toBeFocused()
    expect(api.writes).toEqual([])
    await page.keyboard.press('Enter'); await expect(preview).toBeVisible()
    await expect.poll(() => preview.evaluate((element) => element.contains(document.activeElement))).toBe(true)
    await page.evaluate(() => document.fonts.ready)
    const scrollEvidence = await verifyConfigurationScroll(page)
    await preview.locator('ol[aria-label="端口映射"]').scrollIntoViewIfNeeded()
    await page.waitForTimeout(350)
    const before = { ...await portGeometry(page), chrome: await modalChrome(page) }, requestsBefore = [...api.requests]
    const prefix = `docker-create-ports-${scenario.name}-${width}`
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    const png = info.outputPath('ui-captures', `${prefix}.png`)
    await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
    const after = { ...await portGeometry(page), chrome: await modalChrome(page) }, axe = await new AxeBuilder({ page }).analyze(), afterAxe = { ...await portGeometry(page), chrome: await modalChrome(page) }
    const captureState = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
    const screenshotSha256 = createHash('sha256').update(await readFile(png)).digest('hex')
    await writeFile(captureState, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
      evidenceKind: 'pre-create-port-preview', scrollEvidence, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
      expectedInput: expected, expectedPreview: scenario.preview, screenshot: `${prefix}.png`, screenshotSha256,
      beforeCapture: before, afterCapture: after, afterAxe, axe, requestsBefore, requestsAfterAxe: [...api.requests],
      writes: [...api.writes], denied: [...api.denied], errors: [...api.errors],
      keyboard: { tabWrappedToClose: true, shiftTabWrappedToConfirm: true, escapeRestoredOpener: true } }, null, 2))
    await info.attach(prefix, { path: png, contentType: 'image/png' })
    await info.attach(`${prefix}-axe-state`, { path: captureState, contentType: 'application/json' })
    for (const sample of [before, after, afterAxe]) {
      assertModalChrome(sample.chrome)
      expect(sample.rows).toEqual(scenario.preview)
      expect(sample.fields).toHaveLength(6)
      expect(sample.fields.every((field) => field.fullTextVisible), JSON.stringify(sample)).toBe(true)
      expect(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.clientWidth + 1)
      expect(sample.dialogBounds.left).toBeGreaterThanOrEqual(-1)
      expect(sample.dialogBounds.right).toBeLessThanOrEqual(width + 1)
      expect(sample.focusInside).toBe(true)
    }
    expect(after).toEqual(before); expect(afterAxe).toEqual(before)
    expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
    expect(api.requests).toEqual(requestsBefore); expect(api.writes).toEqual([])
    api.authorize()
    const responsePromise = page.waitForResponse((response) => new URL(response.url()).pathname === createPath && response.request().method() === 'POST')
    try {
      await confirm.click()
      await api.received.promise
      expect(api.writes).toEqual([expected])
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      await expect(drawer).toBeVisible(); await expect(opener).toBeDisabled()
      api.release.resolve()
      const response = await responsePromise
      expect(response.status()).toBe(201)
      expect(await response.json()).toEqual({ containerId: 'created-identity', name: 'identity', image: 'identity:latest', state: 'running', created: true, started: true, runContainer: true })
      await expect(page.getByText('容器“identity”已创建并启动', { exact: true })).toBeVisible()
      await expect(preview).not.toBeVisible()
      await page.waitForTimeout(6000)
      expect(api.writes).toEqual([expected])
      expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([`POST ${createPath}`])
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      const receiptPath = info.outputPath('ui-captures', `${prefix}-receipt-capture-state.json`)
      await writeFile(receiptPath, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
        evidenceKind: 'post-create-receipt', phase: 'after-create-response-and-replay-observation',
        testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
        preCreatePreviewEvidence: { prefix, screenshot: `${prefix}.png`, screenshotSha256, captureState: `${prefix}-axe-capture-state.json`, captureStateSha256: createHash('sha256').update(await readFile(captureState)).digest('hex') },
        replayObservationMilliseconds: 6000, expected, requests: [...api.requests], writes: [...api.writes], denied: [...api.denied], errors: [...api.errors],
        status: response.status(), response: await response.json() }, null, 2))
      await info.attach('strict-port-create-receipt', { path: receiptPath, contentType: 'application/json' })
    } finally { api.release.resolve() }
  })
}

// 资源和重启策略按确认快照逐项取证，边界检查覆盖真实字形和所有裁剪祖先。
const resourcePreviewLabels = ['CPU 限制', '内存限制', '重启策略', '最大重试次数']
const resourceCases = [
  { name: 'default-resources-zero-retries', cpu: undefined, memoryMiB: undefined, retries: 0,
    preview: ['未设置限制', '未设置限制', '失败时重启（on-failure）', '0（不限制重试次数）'] },
  { name: 'explicit-resources-positive-retries', cpu: 1.5, memoryMiB: 384, retries: 3,
    preview: ['1.5 核', '402653184 字节（384 MiB）', '失败时重启（on-failure）', '3 次'] },
]
async function resourceGeometry(page: Page) {
  return page.getByRole('dialog', { name: '确认容器配置', exact: true }).evaluate((dialog, labels) => {
    const fields = labels.map((label) => {
      const term = [...dialog.querySelectorAll('.preview-grid dt')].find((element) => element.textContent === label)!
      const field = term.nextElementSibling!, bounds = field.getBoundingClientRect(), range = document.createRange()
      range.selectNodeContents(field)
      const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
      const clips: Array<{ x: boolean; y: boolean; bounds: DOMRect }> = []
      for (let ancestor: Element | null = field; ancestor; ancestor = ancestor.parentElement) {
        const style = getComputedStyle(ancestor)
        const x = /hidden|clip|auto|scroll/.test(style.overflowX), y = /hidden|clip|auto|scroll/.test(style.overflowY)
        if (x || y) clips.push({ x, y, bounds: ancestor.getBoundingClientRect() })
      }
      return { label, text: field.textContent, bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()),
        clips: clips.map((clip) => ({ ...clip, bounds: clip.bounds.toJSON() })),
        fullTextVisible: lines.length > 0 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && line.top >= bounds.top - 1 && line.bottom <= bounds.bottom + 1 && line.left >= -1 && line.right <= innerWidth + 1 && line.top >= -1 && line.bottom <= innerHeight + 1 && clips.every((clip) => (!clip.x || line.left >= clip.bounds.left - 1 && line.right <= clip.bounds.right + 1) && (!clip.y || line.top >= clip.bounds.top - 1 && line.bottom <= clip.bounds.bottom + 1))) }
    })
    return { fields, root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      dialogBounds: dialog.getBoundingClientRect().toJSON(), focusInside: dialog.contains(document.activeElement) }
  }, resourcePreviewLabels)
}

for (const width of [390, 1440]) for (const scenario of resourceCases) {
  test(`${width}px ${scenario.name} 资源和重启确认预览与请求一致且只接受一次 201`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const expected: DockerContainerCreateInput = { ...expectedInput(true), restartPolicy: 'on-failure', restartMaxRetries: scenario.retries,
      ...(scenario.cpu === undefined ? {} : { cpu: scenario.cpu, memoryBytes: scenario.memoryMiB! * 1024 * 1024 }) }
    const api = await fixture(page, new URL(baseURL!).origin, expected)
    await page.goto('/docker', { waitUntil: 'domcontentloaded' })
    await page.getByRole('button', { name: '镜像', exact: true }).click()
    await page.getByRole('button', { name: '从镜像 identity:latest 创建容器', exact: true }).filter({ visible: true }).click()
    const drawer = page.locator('.create-container-drawer')
    await expect(drawer).toBeVisible()
    await expect.poll(() => api.requests.includes('GET /api/v1/docker/resources')).toBe(true)
    await expect(drawer.getByText('正在读取 Docker 网络、卷和端口清单…', { exact: true })).toHaveCount(0)
    if (scenario.cpu !== undefined) {
      await drawer.getByRole('spinbutton', { name: 'CPU 限制（核）', exact: true }).fill(String(scenario.cpu))
      await drawer.getByRole('spinbutton', { name: '内存限制（MiB）', exact: true }).fill(String(scenario.memoryMiB))
    }
    await expect(drawer.getByRole('combobox', { name: '容器重启策略', exact: true })).toHaveCount(1)
    await drawer.locator('label.field').filter({ has: page.getByRole('combobox', { name: '容器重启策略', exact: true }) }).locator('.el-select__wrapper').click()
    await page.getByRole('option', { name: '失败时重启', exact: true }).click()
    await drawer.getByRole('spinbutton', { name: '最大重试次数', exact: true }).fill(String(scenario.retries))
    const opener = drawer.getByRole('button', { name: '创建并启动', exact: true })
    await opener.focus(); await page.keyboard.press('Enter')
    const preview = page.getByRole('dialog', { name: '确认容器配置', exact: true })
    await expect(preview).toBeVisible()
    await expect.poll(() => preview.evaluate((element) => element.contains(document.activeElement))).toBe(true)
    const confirm = preview.getByRole('button', { name: '确认创建并启动', exact: true })
    await confirm.focus(); await page.keyboard.press('Tab')
    await expect(preview.locator('.el-dialog__headerbtn')).toBeFocused()
    await page.keyboard.press('Shift+Tab'); await expect(confirm).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(preview).not.toBeVisible(); await expect(opener).toBeFocused()
    expect(api.writes).toEqual([])
    await page.keyboard.press('Enter'); await expect(preview).toBeVisible()
    await expect.poll(() => preview.evaluate((element) => element.contains(document.activeElement))).toBe(true)
    await page.evaluate(() => document.fonts.ready)
    const scrollEvidence = await verifyConfigurationScroll(page)
    await preview.locator('.preview-grid dt').filter({ hasText: /^最大重试次数$/ }).locator('xpath=following-sibling::dd[1]').scrollIntoViewIfNeeded()
    await page.waitForTimeout(350)
    const before = { ...await resourceGeometry(page), chrome: await modalChrome(page) }, requestsBefore = [...api.requests]
    const prefix = `docker-create-resources-${scenario.name}-${width}`
    await mkdir(info.outputPath('ui-captures'), { recursive: true })
    const png = info.outputPath('ui-captures', `${prefix}.png`)
    await page.screenshot({ path: png, fullPage: false, animations: 'disabled' })
    const after = { ...await resourceGeometry(page), chrome: await modalChrome(page) }, axe = await new AxeBuilder({ page }).analyze(), afterAxe = { ...await resourceGeometry(page), chrome: await modalChrome(page) }
    const captureState = info.outputPath('ui-captures', `${prefix}-axe-capture-state.json`)
    const screenshotSha256 = createHash('sha256').update(await readFile(png)).digest('hex')
    await writeFile(captureState, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
      evidenceKind: 'pre-create-resource-restart-preview', scrollEvidence, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
      expectedInput: expected, expectedPreview: scenario.preview, screenshot: `${prefix}.png`, screenshotSha256,
      beforeCapture: before, afterCapture: after, afterAxe, axe, requestsBefore, requestsAfterAxe: [...api.requests],
      writes: [...api.writes], denied: [...api.denied], errors: [...api.errors],
      keyboard: { tabWrappedToClose: true, shiftTabWrappedToConfirm: true, escapeRestoredOpener: true } }, null, 2))
    await info.attach(prefix, { path: png, contentType: 'image/png' })
    await info.attach(`${prefix}-axe-state`, { path: captureState, contentType: 'application/json' })
    for (const sample of [before, after, afterAxe]) {
      assertModalChrome(sample.chrome)
      expect(sample.fields.map((field) => field.label)).toEqual(resourcePreviewLabels)
      expect(sample.fields.map((field) => field.text)).toEqual(scenario.preview)
      expect(sample.fields).toHaveLength(4)
      expect(sample.fields.every((field) => field.fullTextVisible), JSON.stringify(sample)).toBe(true)
      expect(sample.root.scrollWidth).toBeLessThanOrEqual(sample.root.clientWidth + 1)
      expect(sample.dialogBounds.left).toBeGreaterThanOrEqual(-1)
      expect(sample.dialogBounds.right).toBeLessThanOrEqual(width + 1)
      expect(sample.focusInside).toBe(true)
    }
    expect(after).toEqual(before); expect(afterAxe).toEqual(before)
    expect(axe.violations, JSON.stringify(axe.violations)).toEqual([])
    expect(api.requests).toEqual(requestsBefore); expect(api.writes).toEqual([])
    api.authorize()
    const responsePromise = page.waitForResponse((response) => new URL(response.url()).pathname === createPath && response.request().method() === 'POST')
    try {
      await confirm.click()
      await api.received.promise
      expect(api.writes).toEqual([expected])
      await expect(page.locator('.el-message--success')).toHaveCount(0)
      await expect(drawer).toBeVisible(); await expect(opener).toBeDisabled()
      api.release.resolve()
      const response = await responsePromise
      expect(response.status()).toBe(201)
      expect(await response.json()).toEqual({ containerId: 'created-identity', name: 'identity', image: 'identity:latest', state: 'running', created: true, started: true, runContainer: true })
      await expect(page.getByText('容器“identity”已创建并启动', { exact: true })).toBeVisible()
      await expect(preview).not.toBeVisible()
      await page.waitForTimeout(6000)
      expect(api.writes).toEqual([expected])
      expect(api.requests.filter((request) => !request.startsWith('GET '))).toEqual([`POST ${createPath}`])
      expect(api.denied).toEqual([]); expect(api.errors).toEqual([])
      const receiptPath = info.outputPath('ui-captures', `${prefix}-receipt-capture-state.json`)
      await writeFile(receiptPath, JSON.stringify({ ...await provenance(), fixtureOnly: true, nativeDockerAcceptance: false,
        evidenceKind: 'post-create-receipt', phase: 'after-create-response-and-replay-observation',
        testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(),
        preCreatePreviewEvidence: { prefix, screenshot: `${prefix}.png`, screenshotSha256, captureState: `${prefix}-axe-capture-state.json`, captureStateSha256: createHash('sha256').update(await readFile(captureState)).digest('hex') },
        replayObservationMilliseconds: 6000, expected, requests: [...api.requests], writes: [...api.writes], denied: [...api.denied], errors: [...api.errors],
        status: response.status(), response: await response.json() }, null, 2))
      await info.attach('strict-resource-restart-create-receipt', { path: receiptPath, contentType: 'application/json' })
    } finally { api.release.resolve() }
  })
}
