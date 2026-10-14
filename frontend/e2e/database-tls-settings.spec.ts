import { execFileSync } from 'node:child_process'
import { createHash, X509Certificate } from 'node:crypto'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { installMockApi } from './mockApi'
import { waitForPageReady } from './pageReadiness'

test.use({ timezoneId: 'UTC', serviceWorkers: 'block', contextOptions: { reducedMotion: 'reduce' } })

const connectPath = '/api/v1/databases/connect'
const diagnosticPath = '/api/v1/databases/test-connection'
const catalogPath = '/api/v1/databases/catalog'
const source = {
  id: 'fixture-tls-postgresql', name: 'Fixture TLS PostgreSQL', driver: 'postgresql', category: 'project',
  project: 'Fixture 项目', module: 'TLS 表单验证', location: 'fixture-db.invalid:5432',
  host: 'fixture-db.invalid', port: 5432, defaultDatabase: 'fixture_catalog',
  requiresLogin: true, status: 'credentials_required', reachability: 'host', evidence: 'published-port', tags: ['fixture-only'],
}
const originalDraft = { username: 'fixture_reader', password: 'fixture-only-password', database: 'fixture_selected_catalog' }
const laterDraft = { username: 'fixture_later_reader', password: 'fixture-only-later-password', database: 'fixture_later_catalog' }
const serverName = 'fixture-identity.invalid'
const laterServerName = 'fixture-later-identity.invalid'

// 专为本 fixture 生成的公开、自签 CA；BasicConstraints=CA:TRUE、KeyUsage=CertSign/CRLSign。
// 私钥生成时即丢弃，仓库与截图均不包含私钥，也不访问任何真实数据库或 CA 服务。
const caPEM = `-----BEGIN CERTIFICATE-----
MIIB2zCCAYGgAwIBAgIUakD9qHsQKhr52rOb6FitpeZzvxgwCgYIKoZIzj0EAwIw
OjEhMB8GA1UEAwwYTkNQIFN5bnRoZXRpYyBUTFMgRTJFIENBMRUwEwYDVQQKDAxG
aXh0dXJlIE9ubHkwIBcNMjYxMDE0MTcxMTQ2WhgPMjEyNjA5MjAxNzExNDZaMDox
ITAfBgNVBAMMGE5DUCBTeW50aGV0aWMgVExTIEUyRSBDQTEVMBMGA1UECgwMRml4
dHVyZSBPbmx5MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE1mXd3EUjCEg7jocV
b2+grZEhPwUHUivJBZl9o9jpoJA7c7rpgp53yNrGyA9AwKbX4wtBc92zQVxtUG+Q
BjbVvaNjMGEwHQYDVR0OBBYEFFm31bjdpuwW/sPKuD+vAQ+FnJvJMB8GA1UdIwQY
MBaAFFm31bjdpuwW/sPKuD+vAQ+FnJvJMA8GA1UdEwEB/wQFMAMBAf8wDgYDVR0P
AQH/BAQDAgEGMAoGCCqGSM49BAMCA0gAMEUCIQCpHKa9Cj1Tddk4u5mVMW6hwDF2
dt/mJxBHaHR93/HI3wIgJZYtZYZQ58NLrWXW6ZpfKq4WM13Jt40IwffUwAdp36E=
-----END CERTIFICATE-----
`
type Draft = typeof originalDraft
type Policy = { mode: 'verify-full'; caPEM?: string; serverName?: string }
type Connection = { sourceId: string; credentials?: Draft & { tls?: Policy } }
type RequestRecord = { method: string; path: string; body?: unknown }
type Reply = 'credentials-required' | 'credential-conflict' | 'disconnect' | 'connected' | 'catalog'
type Permit = { expected: Connection; reply: Reply; gate?: Promise<void> }
const submission = (tls?: Policy, credentials = originalDraft): Connection => ({ sourceId: source.id, credentials: { ...credentials, ...(tls ? { tls } : {}) } })
const strictPolicy = (): Policy => ({ mode: 'verify-full', caPEM, serverName })
const tableName = (database: string) => `${database}_records`
const form = (page: Page) => page.locator('.connection-form')
const field = (page: Page, label: string) => form(page).getByLabel(label, { exact: true })
const beginTls = (page: Page) => form(page).getByRole('button', { name: '设置证书校验 TLS', exact: true })
const tlsGroup = (page: Page) => form(page).getByRole('group', { name: '证书校验 TLS', exact: true })
const trust = (page: Page) => form(page).getByRole('combobox', { name: '信任来源', exact: true })
const ca = (page: Page) => field(page, 'CA 证书（PEM）')
const identity = (page: Page) => field(page, '服务器名（可选）')
const testConnection = (page: Page) => form(page).getByRole('button', { name: '测试连接', exact: true })
const connect = (page: Page) => form(page).getByRole('button', { name: '连接并读取数据表', exact: true })
const error = (page: Page) => page.locator('.database-error[role="alert"]')

// 所有 POST 均逐次许可并深比较完整请求，未知 API、真实主机和任何未许可写入失败关闭。
// 旧响应刻意不提供 appliedTls；普通连接成功永远不能当作 TLS 已应用证明。
async function fixture(page: Page, baseURL: string) {
  const certificate = new X509Certificate(caPEM)
  expect(certificate.ca).toBe(true)
  expect(certificate.verify(certificate.publicKey)).toBe(true)
  await page.clock.install({ time: new Date('2026-10-14T18:00:00.000Z') })
  await installMockApi(page, { clock: false, manualRealtime: true })
  const origin = new URL(baseURL).origin
  const requests: RequestRecord[] = []
  const denied: RequestRecord[] = []
  const permits = new Map<string, Permit>()
  const getReads = new Set([
    '/api/v1/auth/status', '/api/v1/preferences', '/api/v1/system/capabilities',
    '/api/v1/system/summary', '/api/v1/docker/inventory', '/api/v1/jobs',
    '/api/v1/sites', '/api/v1/sites/ignored', '/api/v1/databases/project-preferences',
  ])
  await page.route('**/*', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()
    if (url.origin === origin && !url.pathname.startsWith('/api/') && method === 'GET') return route.fallback()
    const body: unknown = request.postData() ? request.postDataJSON() : undefined
    const entry: RequestRecord = { method, path: url.pathname, ...(body === undefined ? {} : { body }) }
    requests.push(entry)
    if (url.origin === origin) {
      if (method === 'POST' && permits.has(url.pathname)) {
        const permit = permits.get(url.pathname)!
        permits.delete(url.pathname)
        expect(body).toEqual(permit.expected)
        await permit.gate
        if (permit.reply === 'credentials-required') return route.fulfill({ status: 400, json: { code: 'credentials_required', message: 'fixture-only', requestId: 'fixture-initial-connect' } })
        if (permit.reply === 'credential-conflict') return route.fulfill({ status: 409, json: { code: 'credential_conflict', message: 'fixture-private-diagnostic-must-not-render', requestId: 'fixture-tls-conflict', credentials: { password: 'fixture-private-password-must-not-render' } } })
        if (permit.reply === 'disconnect') return route.abort('failed')
        const database = permit.expected.credentials?.database ?? source.defaultDatabase
        if (permit.reply === 'catalog') return route.fulfill({ json: { source, tables: [{ schema: 'public', name: tableName(database), type: 'table', columns: [{ name: 'id', dataType: 'bigint', nullable: false, primaryKey: true, position: 1 }], rowCount: 2, sizeBytes: 4096 }] } })
        return route.fulfill({ json: { connected: true, code: 'connected', driver: source.driver, endpoint: source.location, database, operation: 'test_connection', durationMs: 2 } })
      }
      if (method === 'GET' && url.pathname === '/api/v1/databases/discovery') return route.fulfill({ json: { collectedAt: '2026-10-14T18:00:00.000Z', sources: [source] } })
      if (method === 'GET' && getReads.has(url.pathname)) return route.fallback()
    }
    denied.push(entry)
    return route.fulfill({ status: 503, json: { code: 'FIXTURE_DENIED', message: '未预期请求已拒绝' } })
  })
  return {
    requests, denied,
    allow(path: string, expected: Connection, reply: Reply, delayed = false) {
      expect([connectPath, diagnosticPath, catalogPath]).toContain(path)
      expect(permits.has(path), '不能覆盖未消费的一次性许可').toBe(false)
      let release = () => {}
      const gate = delayed ? new Promise<void>((resolve) => { release = resolve }) : undefined
      permits.set(path, { expected: structuredClone(expected), reply, gate })
      return release
    },
    expectCounts(connections: number, diagnostics: number, catalogs: number) {
      for (const [path, count] of [[connectPath, connections], [diagnosticPath, diagnostics], [catalogPath, catalogs]] as const) {
        expect(requests.filter((entry) => entry.path === path)).toHaveLength(count)
      }
      expect(denied).toEqual([])
      expect(permits.size, '所有一次性许可均应消费').toBe(0)
    },
  }
}
type Fixture = Awaited<ReturnType<typeof fixture>>

async function draft(page: Page, values: Draft, fill = false) {
  for (const [key, label] of [['username', '用户名'], ['password', '密码'], ['database', '数据库名']] as const) {
    if (fill) await field(page, label).fill(values[key])
    await expect(field(page, label)).toBeEditable()
    await expect(field(page, label)).toHaveValue(values[key])
  }
  await expect(field(page, '密码')).toHaveAttribute('type', 'password')
  await expect(page.locator('.database-detail')).not.toContainText(values.password)
}
async function chooseTrust(page: Page, value: 'system' | 'custom') {
  // 验证真实 ElSelect 的键盘路径，不强点被选中值遮住的只读 input。
  await trust(page).focus()
  await trust(page).press('ArrowDown')
  await expect(trust(page)).toHaveAttribute('aria-expanded', 'true')
  await trust(page).press(value === 'system' ? 'Home' : 'End')
  await trust(page).press('Enter')
  await expect(trust(page)).toHaveAttribute('aria-expanded', 'false')
  await expect(form(page).locator('.el-select__selected-item').filter({ hasText: value === 'system' ? 'Root Agent 系统根证书' : '自定义 PEM CA' })).toBeVisible()
}
async function customTls(page: Page) {
  await beginTls(page).click()
  await expect(tlsGroup(page)).toBeVisible()
  await chooseTrust(page, 'custom')
  await ca(page).fill(caPEM)
  await identity(page).fill(serverName)
}
async function expectTlsDraft(page: Page, policy = strictPolicy()) {
  await expect(tlsGroup(page)).toBeVisible()
  await expect(ca(page)).toHaveValue(policy.caPEM!)
  await expect(identity(page)).toHaveValue(policy.serverName!)
}
async function noTlsProof(page: Page) {
  await expect(page.getByText(/^(已加密|TLS 已验证|TLS 校验通过|证书校验通过)$/)).toHaveCount(0)
  await expect(page.locator('.database-detail .lucide-lock, .database-detail .lucide-lock-keyhole, .database-detail [data-tls-verified="true"]')).toHaveCount(0)
  if (await form(page).count()) await expect(form(page)).toContainText('TLS 状态未确认')
}
async function noAutomaticRetry(page: Page, api: Fixture, connections: number, diagnostics: number, catalogs: number) {
  await page.clock.fastForward(15_000)
  await waitForPageReady(page)
  api.expectCounts(connections, diagnostics, catalogs)
  await noTlsProof(page)
}
async function ready(page: Page, api: Fixture, existingCatalog: boolean) {
  api.allow(connectPath, { sourceId: source.id }, existingCatalog ? 'connected' : 'credentials-required')
  if (existingCatalog) api.allow(catalogPath, { sourceId: source.id }, 'catalog')
  await page.goto(`/databases/${source.id}`, { waitUntil: 'domcontentloaded' })
  await expect(page.getByRole('heading', { name: source.name, exact: true })).toBeVisible()
  if (existingCatalog) {
    await expect(page.getByRole('link', { name: `打开 ${tableName(source.defaultDatabase)} 数据表详情`, exact: true })).toBeVisible()
    await noTlsProof(page)
    await page.getByRole('button', { name: '连接设置', exact: true }).click()
    await expect(field(page, '密码')).toHaveValue('')
  }
  await expect(connect(page)).toBeEnabled()
  api.expectCounts(1, 0, existingCatalog ? 1 : 0)
}

async function sourceProvenance() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
  const git = (args: string[]) => execFileSync('git', args, { cwd: root, encoding: 'utf8' })
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard', '--', 'frontend/src', 'frontend/e2e', 'frontend/index.html', 'frontend/package.json', 'frontend/pnpm-lock.yaml', 'frontend/vite.config.ts', 'frontend/playwright.config.ts']).split('\n').filter(Boolean)
  const sourceHashes = Object.fromEntries(await Promise.all([...new Set(files)].sort().map(async (file) => [file, createHash('sha256').update(await readFile(resolve(root, file))).digest('hex')])))
  return { sourceSha: git(['rev-parse', 'HEAD']).trim(), workingTree: git(['status', '--porcelain']), sourceHashes, trackedDiffSha256: createHash('sha256').update(git(['diff', 'HEAD', '--binary', '--', 'frontend'])).digest('hex') }
}
async function captureState(page: Page) {
  return page.evaluate(() => {
    const scope = document.querySelector<HTMLElement>('.database-detail')!
    const describe = (element: Element | null) => element ? { tag: element.tagName, id: element.id, role: element.getAttribute('role'), label: element.getAttribute('aria-label'), text: element.textContent?.trim() } : null
    const selectors = [
      '.database-error__body strong', '.database-error__body span', '.database-error__body small', '.database-error code',
      '.source-summary__status .connection-pill', '.source-summary__status small', '.connection-form .el-form-item__label',
      '.connection-form .el-input__inner', '.connection-form textarea', '.connection-form .el-select__wrapper',
      '.connection-form .el-select__placeholder', '.connection-form legend', '.connection-form .field-description',
      '.connection-form button', '.connection-form button > span', '.connection-form p', '.connection-form [role="alert"]',
    ]
    return {
      viewport: { width: innerWidth, height: innerHeight, devicePixelRatio },
      root: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
      scroll: { x: scrollX, y: scrollY }, focus: describe(document.activeElement),
      semantic: {
        error: scope.querySelector('.database-error')?.textContent ?? null,
        status: scope.querySelector('.source-summary__status')?.textContent,
        diagnostic: scope.querySelector('.diagnostic-result')?.textContent ?? null,
        catalog: scope.querySelector('[aria-label="数据表和视图列表"]')?.textContent ?? null,
        tls: scope.querySelector('[aria-label="证书校验 TLS"]')?.textContent ?? null,
        inputs: [...scope.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>('.connection-form input, .connection-form textarea')].map((input) => ({ id: input.id, label: input.getAttribute('aria-label'), labels: [...(input.labels ?? [])].map((label) => label.textContent?.trim()), type: input.type, value: input.value, disabled: input.disabled, invalid: input.getAttribute('aria-invalid'), describedBy: input.getAttribute('aria-describedby') })),
        actions: [...scope.querySelectorAll<HTMLButtonElement>('.connection-form button')].map((button) => ({ text: button.textContent?.trim(), disabled: button.disabled })),
      },
      controls: selectors.flatMap((selector) => [...scope.querySelectorAll<HTMLElement>(selector)].map((element, index) => {
        const bounds = element.getBoundingClientRect()
        const style = getComputedStyle(element)
        const range = document.createRange()
        range.selectNodeContents(element)
        const lines = [...range.getClientRects()].filter((line) => line.width > 0 && line.height > 0)
        const clips: DOMRect[] = []
        for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
          const overflow = getComputedStyle(ancestor)
          if (/(hidden|clip|auto|scroll)/.test(`${overflow.overflowX} ${overflow.overflowY}`)) clips.push(ancestor.getBoundingClientRect())
        }
        const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height / 2)
        return {
          selector, index, ...describe(element), bounds: bounds.toJSON(), lines: lines.map((line) => line.toJSON()), clips: clips.map((clip) => clip.toJSON()),
          visible: bounds.width > 0 && bounds.height > 0 && style.display !== 'none' && style.visibility !== 'hidden',
          inViewport: bounds.top >= 0 && bounds.bottom <= innerHeight + 1,
          centerHit: Boolean(hit && (hit === element || element.contains(hit))),
          textContained: element.scrollWidth <= element.clientWidth + 1 && lines.every((line) => line.left >= bounds.left - 1 && line.right <= bounds.right + 1 && clips.every((clip) => line.left >= clip.left - 1 && line.right <= clip.right + 1 && line.top >= clip.top - 1 && line.bottom <= clip.bottom + 1)),
        }
      })),
    }
  })
}

// 沿用连接结果套件的 CI 配对证据：原始 PNG、完整 axe、source/PNG 哈希、几何、焦点和请求。
// 不改 DOM/CSS、不隐藏内容；记录的 CA、密码、主机和数据库全部是上述合成 fixture。
async function capture(page: Page, info: TestInfo, api: Fixture, view: string) {
  await page.evaluate(() => document.fonts.ready)
  await waitForPageReady(page)
  const initialState = await captureState(page)
  await page.evaluate(() => scrollTo({ top: 0, left: 0, behavior: 'instant' }))
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(0)
  const provenance = await sourceProvenance()
  let beforeCapture = await captureState(page)
  let stable = 0
  await expect.poll(async () => {
    const next = await captureState(page)
    stable = JSON.stringify(next) === JSON.stringify(beforeCapture) ? stable + 1 : 0
    beforeCapture = next
    return stable
  }, { intervals: [80], timeout: 5_000 }).toBeGreaterThanOrEqual(3)
  const requestsBefore = structuredClone(api.requests)
  const name = `database-tls-settings-${view}-${page.viewportSize()!.width}`
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const png = info.outputPath('ui-captures', `${name}.png`)
  await page.screenshot({ path: png, fullPage: true, animations: 'disabled' })
  const afterCapture = await captureState(page)
  const requestsAfterCapture = structuredClone(api.requests)
  const axe = await new AxeBuilder({ page }).analyze()
  const afterAxe = await captureState(page)
  const metadata = {
    ...provenance, fixtureOnly: true, nativeDatabaseAcceptance: false, appliedTlsProof: false,
    fixtureSource: 'frontend/e2e/database-tls-settings.spec.ts + frontend/e2e/mockApi.ts',
    view, testTitle: info.title, project: info.project.name, retry: info.retry, url: page.url(), fullPage: true,
    screenshot: `${name}.png`, screenshotSha256: createHash('sha256').update(await readFile(png)).digest('hex'),
    fixture: { sourceId: source.id, caSha256: createHash('sha256').update(caPEM).digest('hex'), denied: api.denied },
    requestsBefore, requestsAfterCapture, requestsAfterAxe: structuredClone(api.requests), initialState, beforeCapture, afterCapture, afterAxe,
  }
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  const axePath = info.outputPath('ui-captures', `${name}-axe-capture-state.json`)
  await writeFile(statePath, JSON.stringify(metadata, null, 2))
  await writeFile(axePath, JSON.stringify({ ...metadata, axe }, null, 2))
  await info.attach(name, { path: png, contentType: 'image/png' })
  await info.attach(`${name}-state`, { path: statePath, contentType: 'application/json' })
  await info.attach(`${name}-axe-state`, { path: axePath, contentType: 'application/json' })
  for (const state of [afterCapture, afterAxe]) {
    expect.soft(state.semantic).toEqual(beforeCapture.semantic)
    expect.soft(state.focus).toEqual(beforeCapture.focus)
    expect.soft(state.scroll).toEqual(beforeCapture.scroll)
    expect.soft(state.controls).toHaveLength(beforeCapture.controls.length)
    for (const [index, control] of state.controls.entries()) {
      const previous = beforeCapture.controls[index]!
      expect.soft({ ...control, bounds: undefined, lines: undefined, clips: undefined }).toEqual({ ...previous, bounds: undefined, lines: undefined, clips: undefined })
      for (const key of ['left', 'right', 'top', 'bottom', 'width', 'height'] as const) expect.soft(Math.abs(control.bounds[key] - previous.bounds[key])).toBeLessThanOrEqual(1 / 8)
    }
  }
  expect.soft(api.requests).toEqual(requestsBefore)
  expect.soft(beforeCapture.root.scrollWidth).toBeLessThanOrEqual(beforeCapture.root.clientWidth + 1)
  expect.soft(beforeCapture.semantic.inputs.find((input) => input.labels.includes('密码'))?.type).toBe('password')
  expect.soft(beforeCapture.controls.filter((control) => control.selector === '.connection-form textarea')).toHaveLength(1)
  for (const control of beforeCapture.controls) {
    expect.soft(control.visible, JSON.stringify(control)).toBe(true)
    expect.soft(control.bounds.left).toBeGreaterThanOrEqual(0)
    expect.soft(control.bounds.right).toBeLessThanOrEqual(beforeCapture.viewport.width + 1)
    // 长 PEM/输入值可在控件内正常滚动；说明和操作文字必须完整呈现。
    if (!['INPUT', 'TEXTAREA'].includes(control.tag ?? '')) expect.soft(control.textContained, JSON.stringify(control)).toBe(true)
    if (control.inViewport && (control.tag === 'BUTTON' || control.selector === '.connection-form .el-select__wrapper')) expect.soft(control.centerHit, JSON.stringify(control)).toBe(true)
  }
  expect.soft(axe.violations, JSON.stringify(axe.violations, null, 2)).toEqual([])
}

for (const width of [390, 1440]) {
  test(`${width}px 已有目录可设置 TLS，诊断不保存、撤销不关闭策略，连接与目录使用冻结草稿`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    expect(baseURL).toBeTruthy()
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, baseURL!)
    await ready(page, api, true)
    await draft(page, originalDraft, true)
    await beginTls(page).click()
    await expect(tlsGroup(page)).toBeVisible()
    await expect(tlsGroup(page)).toContainText('verify-full')
    await chooseTrust(page, 'system')
    await noTlsProof(page)
    api.expectCounts(1, 0, 1)

    api.allow(diagnosticPath, submission({ mode: 'verify-full' }), 'connected')
    await testConnection(page).click()
    await expect(page.locator('.diagnostic-result')).toContainText('连接诊断通过')
    await noAutomaticRetry(page, api, 1, 1, 1)

    await chooseTrust(page, 'custom')
    await ca(page).fill(caPEM)
    await identity(page).fill(serverName)
    api.allow(diagnosticPath, submission(strictPolicy()), 'connected')
    await testConnection(page).click()
    await expect(page.locator('.diagnostic-result')).toContainText('连接诊断通过')
    await draft(page, originalDraft)
    await expectTlsDraft(page)
    await noAutomaticRetry(page, api, 1, 2, 1)
    await identity(page).focus()
    await capture(page, info, api, 'custom-ca-legacy-diagnostic')

    // 系统根不会偷偷携带自定义 CA；身份覆盖仍不更换 sourceId、实际目标或数据库。
    await chooseTrust(page, 'system')
    api.allow(diagnosticPath, submission({ mode: 'verify-full', serverName }), 'connected')
    await testConnection(page).click()
    await expect(page.locator('.diagnostic-result')).toContainText('连接诊断通过')
    await noAutomaticRetry(page, api, 1, 3, 1)
    await chooseTrust(page, 'custom')
    await ca(page).fill(caPEM)
    await form(page).getByRole('button', { name: '撤销本次 TLS 编辑', exact: true }).click()
    await expect(tlsGroup(page)).toHaveCount(0)
    await expect(beginTls(page)).toBeFocused()
    await expect(form(page)).toContainText('已保存')
    api.allow(diagnosticPath, submission(), 'connected')
    await testConnection(page).click()
    await expect(page.locator('.diagnostic-result')).toContainText('连接诊断通过')
    await noAutomaticRetry(page, api, 1, 4, 1)

    await customTls(page)
    const frozen = submission(strictPolicy())
    const release = api.allow(connectPath, frozen, 'connected', true)
    api.allow(catalogPath, frozen, 'catalog')
    await connect(page).click()
    await expect.poll(() => api.requests.filter((entry) => entry.path === connectPath).length).toBe(2)
    try {
      await draft(page, laterDraft, true)
      await ca(page).fill(`${caPEM}\n`)
      await identity(page).fill(laterServerName)
    } finally { release() }
    await expect(page.getByRole('link', { name: `打开 ${tableName(originalDraft.database)} 数据表详情`, exact: true })).toBeVisible()
    await expect(page.getByRole('link', { name: `打开 ${tableName(laterDraft.database)} 数据表详情`, exact: true })).toHaveCount(0)
    await draft(page, laterDraft)
    await expectTlsDraft(page, { mode: 'verify-full', caPEM: `${caPEM}\n`, serverName: laterServerName })
    await expect(page.locator('.diagnostic-result')).toContainText('上次提交结果')
    await expect(page.locator('.diagnostic-result')).not.toContainText('连接诊断通过')
    await noAutomaticRetry(page, api, 2, 4, 2)
    await capture(page, info, api, 'later-draft-frozen-catalog')
    expect(api.requests.filter((entry) => entry.method === 'POST').map((entry) => entry.path)).toEqual([
      connectPath, catalogPath, diagnosticPath, diagnosticPath, diagnosticPath, diagnosticPath, connectPath, catalogPath,
    ])
  })

  test(`${width}px 精确 409 与响应丢失保留 TLS 且不重试，UTF-8 超预算不发送请求`, async ({ page, baseURL }, info) => {
    test.setTimeout(120_000)
    expect(baseURL).toBeTruthy()
    await page.setViewportSize({ width, height: 900 })
    const api = await fixture(page, baseURL!)
    await ready(page, api, false)
    await draft(page, originalDraft, true)
    await customTls(page)
    api.allow(connectPath, submission(strictPolicy()), 'credential-conflict')
    await connect(page).click()
    await expect(error(page).locator('strong')).toHaveText('连接凭据未保存')
    await expect(error(page).locator('code')).toHaveText('代码 credential_conflict')
    await expect(error(page)).toContainText('本次连接未保存凭据')
    await expect(error(page)).toContainText('不会自动重试')
    await expect(error(page)).not.toContainText('服务端可能已保存')
    await draft(page, originalDraft)
    await expectTlsDraft(page)
    for (const text of ['fixture-private-diagnostic-must-not-render', 'fixture-private-password-must-not-render']) await expect(page.locator('.database-detail')).not.toContainText(text)
    await expect(page.locator('.connection-pill--success, .diagnostic-result')).toHaveCount(0)
    await noAutomaticRetry(page, api, 2, 0, 0)
    await capture(page, info, api, 'credential-conflict')

    // 只有这次明确点击才重试；丢响应后继续保留同一 TLS，不自动去掉策略重连。
    api.allow(connectPath, submission(strictPolicy()), 'disconnect')
    await connect(page).click()
    await expect(error(page)).toContainText('DATABASE_CONNECTION_OUTCOME_UNKNOWN')
    await expect(error(page)).toContainText('服务端可能已保存本次凭据')
    await expect(error(page)).toContainText('不会自动重试')
    await draft(page, originalDraft)
    await expectTlsDraft(page)
    await expect(page.getByRole('region', { name: '数据表和视图列表', exact: true })).toHaveCount(0)
    await expect(page.locator('.connection-pill--success, .diagnostic-result')).toHaveCount(0)
    await noAutomaticRetry(page, api, 3, 0, 0)
    await capture(page, info, api, 'connection-unknown')

    // 在合成 PEM 后追加多字节空白：字符数未超限，UTF-8 字节预算应先于语法拒绝。
    // 不依赖 maxlength 静默截断，也不能用 ASCII 字符数误充 64 KiB 字节预算。
    const oversized = `${caPEM}${'\u3000'.repeat(22_000)}`
    expect(oversized.length).toBeLessThan(65_536)
    expect(Buffer.byteLength(oversized, 'utf8')).toBeGreaterThan(65_536)
    await ca(page).fill(oversized)
    await expect(ca(page)).toHaveValue(oversized)
    for (const action of [testConnection(page), connect(page)]) {
      await action.click()
      await expect(ca(page)).toBeFocused()
      await expect(ca(page)).toHaveAttribute('aria-invalid', 'true')
      await expect(ca(page)).toHaveAccessibleDescription(/CA 内容超过 64 KiB/)
      await expect(form(page)).toContainText('CA 内容超过 64 KiB')
      await noAutomaticRetry(page, api, 3, 0, 0)
    }
    await ca(page).fill(caPEM)
    await expectTlsDraft(page)
    await noAutomaticRetry(page, api, 3, 0, 0)
    expect(api.requests.filter((entry) => entry.method === 'POST')).toEqual([
      { method: 'POST', path: connectPath, body: { sourceId: source.id } },
      { method: 'POST', path: connectPath, body: submission(strictPolicy()) },
      { method: 'POST', path: connectPath, body: submission(strictPolicy()) },
    ])
  })
}
