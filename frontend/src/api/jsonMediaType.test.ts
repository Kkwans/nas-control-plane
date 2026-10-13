import { afterEach, describe, expect, it, vi } from 'vitest'

import { beginSession, captureSession } from '@/session/sessionLifecycle'
import { bootstrapRoot, loginRoot, logoutRoot, requestAuthStatus } from './auth'
import * as control from './control'
import * as database from './database'
import * as docker from './docker'
import * as system from './systemInfo'
import { NcpApiError } from './systemTransport'
import { UserWriteOutcomeUnknownError } from './userWriteOutcome'

const credentials = { username: 'fixture', password: 'fixture-only' }
const project = { id: 'fixture', name: 'fixture', kind: 'compose' as const, workingDirectory: '/fixture', configFiles: ['/fixture/compose.yaml'] }
const site = { name: 'fixture', description: '', iconUrl: '', category: '', primaryPort: 0, launchUrl: 'http://fixture.test', favorite: false, sortOrder: 0, hidden: false }
const connection = { sourceId: 'fixture' }
const mediaError = {
  code: 'HTTP_UNSUPPORTED_MEDIA_TYPE',
  message: '请求必须使用 application/json 内容类型。',
  requestId: 'req-mime-fixture',
}

interface RequestCase {
  family: 'auth' | 'system' | 'docker' | 'control' | 'database'
  path: string
  method: 'POST' | 'PUT' | 'DELETE' | 'GET'
  run: () => Promise<unknown>
}

// 调用真实公开 API，而非模拟或直接调用内部 transport helper。
const jsonRequests: RequestCase[] = [
  { family: 'auth', path: '/auth/bootstrap', method: 'POST', run: () => bootstrapRoot(credentials) },
  { family: 'auth', path: '/auth/login', method: 'POST', run: () => loginRoot(credentials) },
  { family: 'system', path: '/system/dns/preview', method: 'POST', run: () => system.previewDNSChange({ nameservers: ['1.1.1.1'] }) },
  { family: 'system', path: '/system/dns/confirm', method: 'POST', run: () => system.confirmDNSChange({ previewId: 'fixture', confirmed: true }) },
  { family: 'system', path: '/system/dns/rollback', method: 'POST', run: () => system.rollbackDNSChange('fixture') },
  { family: 'system', path: '/proxy/mihomo/invoke', method: 'POST', run: () => system.invokeMihomo({ operation: 'read-config' }) },
  { family: 'system', path: '/proxy/mihomo/inspect', method: 'POST', run: () => system.inspectMihomo(true) },
  { family: 'docker', path: '/docker/images/pull', method: 'POST', run: () => docker.pullDockerImage('fixture:latest') },
  { family: 'docker', path: '/docker/images/remove', method: 'POST', run: () => docker.removeDockerImage('fixture') },
  { family: 'docker', path: '/docker/images/remove-batch', method: 'POST', run: () => docker.removeDockerImages(['fixture']) },
  { family: 'docker', path: '/docker/containers', method: 'POST', run: () => docker.createDockerContainer({ image: 'fixture:latest', runContainer: false }) },
  { family: 'docker', path: '/docker/projects/fixture/actions/stop', method: 'POST', run: () => docker.requestDockerProjectAction({ ...project, kind: 'standalone' }, 'stop') },
  { family: 'docker', path: '/docker/compose/projects/fixture/actions/stop', method: 'POST', run: () => docker.requestDockerProjectAction(project, 'stop') },
  { family: 'docker', path: '/docker/compose/projects/fixture', method: 'DELETE', run: () => docker.deleteDockerProject(project) },
  { family: 'docker', path: '/docker/compose/config/read', method: 'POST', run: () => docker.readComposeConfig(project) },
  { family: 'docker', path: '/docker/compose/config/validate', method: 'POST', run: () => docker.validateComposeConfig('/fixture/compose.yaml', 'services: {}') },
  { family: 'docker', path: '/docker/compose/deploy', method: 'POST', run: () => docker.deployComposeConfig({ projectId: project.id, workingDirectory: project.workingDirectory, configFiles: project.configFiles, targetPath: project.configFiles[0]!, content: 'services: {}', baseContentHash: '0'.repeat(64) }) },
  { family: 'control', path: '/preferences', method: 'PUT', run: () => control.updatePreferences({ refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 14, pageSize: 20, sidebarDefault: 'expanded', linkOpenMode: 'new-tab', siteDefaultProtocol: 'http', chineseFont: 'system', latinFont: 'system', navigationOrder: [] }) },
  { family: 'control', path: '/preferences/lists/fixture', method: 'PUT', run: () => control.updateListPreference('fixture', { pageSize: 20, sortKey: 'name', sortDirection: 'asc' }) },
  { family: 'control', path: '/databases/project-preferences', method: 'PUT', run: () => control.updateDatabaseProjectPreference({ projectKey: 'fixture', archived: false }) },
  { family: 'control', path: '/docker/compose/drafts', method: 'PUT', run: () => control.saveComposeDraft({ projectId: 'fixture', configPath: '/fixture/compose.yaml', content: 'services: {}' }) },
  { family: 'control', path: '/sites', method: 'POST', run: () => control.createSite(site) },
  { family: 'control', path: '/sites/fixture', method: 'PUT', run: () => control.updateSite('fixture', site) },
  { family: 'control', path: '/users', method: 'POST', run: () => control.createUser(credentials) },
  { family: 'control', path: '/users/password-policy', method: 'PUT', run: () => control.updatePasswordPolicy({ minLength: 12, requireUppercase: true, requireLowercase: true, requireDigit: true, requireSpecial: false }) },
  { family: 'control', path: '/users/1/status', method: 'PUT', run: () => control.updateUserStatus(1, true) },
  { family: 'control', path: '/users/1/password', method: 'PUT', run: () => control.updateUserPassword(1, { newPassword: 'fixture-only' }) },
  { family: 'database', path: '/databases/connect', method: 'POST', run: () => database.connectDatabase(connection) },
  { family: 'database', path: '/databases/test-connection', method: 'POST', run: () => database.testDatabaseConnection(connection) },
  { family: 'database', path: '/databases/catalog', method: 'POST', run: () => database.loadDatabaseCatalog(connection) },
  { family: 'database', path: '/databases/query', method: 'POST', run: () => database.executeDatabaseSQL({ ...connection, sql: 'SELECT 1' }) },
  { family: 'database', path: '/databases/rows', method: 'POST', run: () => database.loadDatabaseRows({ ...connection, table: 'fixture' }) },
  { family: 'database', path: '/databases/rows/insert', method: 'POST', run: () => database.insertDatabaseRow({ ...connection, table: 'fixture', values: { id: 1 } }) },
  { family: 'database', path: '/databases/rows/update', method: 'POST', run: () => database.updateDatabaseRow({ ...connection, table: 'fixture', values: { value: 'fixture' }, keys: { id: 1 } }) },
  { family: 'database', path: '/databases/rows/delete', method: 'POST', run: () => database.deleteDatabaseRow({ ...connection, table: 'fixture', keys: { id: 1 } }) },
]

const noBodyRequests: RequestCase[] = [
  { family: 'auth', path: '/auth/logout', method: 'POST', run: () => logoutRoot() },
  { family: 'control', path: '/users/1', method: 'DELETE', run: () => control.deleteUser(1) },
  { family: 'system', path: '/system/public-egress/detect', method: 'POST', run: () => system.detectPublicEgress() },
  { family: 'docker', path: '/jobs/fixture', method: 'DELETE', run: () => docker.deleteJob('fixture') },
  { family: 'docker', path: '/jobs/fixture/retry', method: 'POST', run: () => docker.retryJob('fixture') },
  { family: 'docker', path: '/jobs/fixture/cancel', method: 'POST', run: () => docker.cancelJob('fixture') },
  { family: 'control', path: '/sites/fixture', method: 'DELETE', run: () => control.deleteSite('fixture') },
  { family: 'control', path: '/sites/fixture/icon', method: 'DELETE', run: () => control.deleteSiteIcon('fixture') },
  { family: 'control', path: '/sites/fixture/restore', method: 'POST', run: () => control.restoreSite('fixture') },
  { family: 'control', path: '/sites/fixture/visit', method: 'POST', run: () => control.recordSiteVisit('fixture') },
  { family: 'docker', path: '/docker/containers/fixture/actions/restart', method: 'POST', run: () => docker.requestContainerAction('fixture', 'restart') },
  { family: 'auth', path: '/auth/status', method: 'GET', run: () => requestAuthStatus() },
  { family: 'system', path: '/system/summary', method: 'GET', run: () => system.requestSystemSummary() },
  { family: 'docker', path: '/docker/images', method: 'GET', run: () => docker.requestDockerImages() },
  { family: 'control', path: '/preferences', method: 'GET', run: () => control.requestPreferences() },
  { family: 'database', path: '/databases/discovery', method: 'GET', run: () => database.discoverDatabases() },
]

// 仅三个有 JSON body 的账号写入采用敏感响应净化；密码规则和其他 API 保持原合同。
function isSanitizedAccountJSONRequest(family: RequestCase['family'], method: RequestCase['method'], path: string) {
  return family === 'control' && (
    method === 'POST' && path === '/users' ||
    method === 'PUT' && /^\/users\/[1-9]\d*\/(status|password)$/.test(path)
  )
}

function expectJSONRejection(error: unknown, expected: typeof mediaError, family: RequestCase['family'], method: RequestCase['method'], path: string) {
  expect(error).toBeInstanceOf(NcpApiError)
  if (isSanitizedAccountJSONRequest(family, method, path)) {
    expect(error).not.toBeInstanceOf(UserWriteOutcomeUnknownError)
    expect(error).toMatchObject({ code: expected.code, message: expected.message, requestId: undefined })
  } else {
    expect(error).toMatchObject(expected)
  }
}

function rejectWithJSONError(status = 415, error = mediaError) {
  const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => new Response(JSON.stringify(error), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }))
  vi.stubGlobal('fetch', fetcher)
  return fetcher
}

afterEach(() => vi.unstubAllGlobals())

describe('JSON 请求 MIME 与大小错误契约', () => {
  it.each(jsonRequests)('$family $method $path 发送 JSON，并保留 415，不重试或跳转登录', async ({ run, path, method, family }) => {
    const fetcher = rejectWithJSONError()
    const session = beginSession()
    const location = { href: 'http://fixture.test/current', assign: vi.fn(), replace: vi.fn() }
    vi.stubGlobal('location', location)
    vi.stubGlobal('window', { location })

    const error = await run().catch((caught: unknown) => caught)
    expectJSONRejection(error, mediaError, family, method, path)
    expect(fetcher).toHaveBeenCalledTimes(1)
    const [url, init] = fetcher.mock.calls[0]!
    expect(url).toBe(`/api/v1${path}`)
    expect(init).toMatchObject({ method, credentials: 'same-origin' })
    const headers = new Headers(init?.headers)
    expect(headers.get('Accept')).toBe('application/json')
    expect(headers.get('Content-Type')).toBe('application/json')
    expect(typeof init?.body).toBe('string')
    expect(JSON.parse(init?.body as string)).toEqual(expect.any(Object))
    expect(captureSession()).toEqual(session)
    expect(session.signal.aborted).toBe(false)
    expect(location.href).toBe('http://fixture.test/current')
    expect(window.location).toBe(location)
    expect(location.assign).not.toHaveBeenCalled()
    expect(location.replace).not.toHaveBeenCalled()
  })

  it.each(jsonRequests)('$family $method $path 保留完整请求体 413，不重试或跳转登录', async ({ run, path, method, family }) => {
    const bodyError = {
      code: 'HTTP_REQUEST_BODY_TOO_LARGE',
      message: '请求体超过大小限制。',
      requestId: 'req-body-limit-fixture',
    }
    const fetcher = rejectWithJSONError(413, bodyError)
    const session = beginSession()
    const location = { href: 'http://fixture.test/current', assign: vi.fn(), replace: vi.fn() }
    vi.stubGlobal('location', location)
    vi.stubGlobal('window', { location })

    const error = await run().catch((caught: unknown) => caught)
    expectJSONRejection(error, bodyError, family, method, path)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(captureSession()).toEqual(session)
    expect(session.signal.aborted).toBe(false)
    expect(location.href).toBe('http://fixture.test/current')
    expect(window.location).toBe(location)
    expect(location.assign).not.toHaveBeenCalled()
    expect(location.replace).not.toHaveBeenCalled()
  })

  it.each(noBodyRequests)('$family $method $path 无 body 时不添加 Content-Type', async ({ run, path, method, family }) => {
    const fetcher = rejectWithJSONError()
    const error = await run().catch((caught: unknown) => caught)
    if (family === 'control' && method === 'DELETE' && /^\/users\/[1-9]\d*$/.test(path)) {
      // 服务端 DELETE 账号不经过 JSON MIME 检查；意外 415 不能作为已证明未执行。
      expect(error).toBeInstanceOf(UserWriteOutcomeUnknownError)
      expect(error).toMatchObject({ code: 'USER_WRITE_OUTCOME_UNKNOWN', operation: 'delete', requestId: undefined })
    } else {
      expect(error).toMatchObject(mediaError)
    }
    expect(fetcher).toHaveBeenCalledTimes(1)
    const [url, init] = fetcher.mock.calls[0]!
    expect(url).toBe(`/api/v1${path}`)
    expect(init?.method ?? 'GET').toBe(method)
    expect(init?.credentials).toBe('same-origin')
    expect(init?.body).toBeUndefined()
    expect(new Headers(init?.headers).has('Content-Type')).toBe(false)
  })

  it('图标上传保留 FormData，交由浏览器生成 multipart boundary', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ siteId: 'fixture', iconUrl: '/api/v1/sites/fixture/icon' })))
    vi.stubGlobal('fetch', fetcher)
    const file = new File(['fixture'], 'icon.png', { type: 'image/png' })
    await expect(control.uploadSiteIcon('fixture', file)).resolves.toEqual({ siteId: 'fixture', iconUrl: '/api/v1/sites/fixture/icon' })
    expect(fetcher).toHaveBeenCalledTimes(1)
    const [url, init] = fetcher.mock.calls[0]!
    expect(url).toBe('/api/v1/sites/fixture/icon')
    expect(init).toMatchObject({ method: 'POST', credentials: 'same-origin' })
    expect(new Headers(init?.headers).has('Content-Type')).toBe(false)
    expect(init?.body).toBeInstanceOf(FormData)
    expect((init?.body as FormData).get('icon')).toBe(file)
  })
})
