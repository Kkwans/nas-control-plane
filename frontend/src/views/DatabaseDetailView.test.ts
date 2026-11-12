// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseConnectionDiagnostic, DatabaseDiscovery, DatabaseSource } from '@/api/database'
import { NcpApiError } from '@/api/system'
import { DatabaseConnectionOutcomeUnknownError } from '@/api/databaseConnectionResponse'
import { invalidateSession } from '@/session/sessionLifecycle'
import { useDatabaseStore } from '@/stores/database'
import DatabaseDetailView from './DatabaseDetailView.vue'

const mocks = vi.hoisted(() => ({ discover: vi.fn(), connect: vi.fn(), diagnostic: vi.fn(), catalog: vi.fn(), route: { params: { sourceId: 'a' } } }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, RouterLink: { render: () => null } }))
vi.mock('@/api/control', () => ({ requestDatabaseProjectPreferences: vi.fn(async () => []), updateDatabaseProjectPreference: vi.fn() }))
vi.mock('@/api/database', () => ({ discoverDatabases: mocks.discover, connectDatabase: mocks.connect, testDatabaseConnection: mocks.diagnostic, loadDatabaseCatalog: mocks.catalog }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function source(id: string): DatabaseSource {
  return { id, name: `Source ${id}`, driver: 'mysql', category: 'project', project: '', module: '', location: `${id}:3306`, requiresLogin: true, status: 'credentials_required', tags: [] }
}
function diagnostic(id: string): DatabaseConnectionDiagnostic {
  return { connected: true, driver: 'mysql', endpoint: `${id}:3306`, operation: 'test_connection', durationMs: 1 }
}
function response(payload: unknown, status = 200) { return new Response(JSON.stringify(payload), { status }) }
function credentialsRequired() { return response({ code: 'credentials_required', message: 'private-fixture', requestId: 'fixture-request' }, 400) }
function credentialConflict() {
  return response({ code: 'credential_conflict', message: 'private-fixture password=secret', requestId: 'fixture-request', credentials: { password: 'private-fixture' } }, 409)
}
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
let app: App | undefined
let host: HTMLDivElement
function mount() {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseDetailView) })
  app.mount(host)
}
function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((item) => item.textContent?.includes(label))
  if (!found) throw new Error(`Missing button ${label}`)
  return found
}
async function enterCredentials(values = ['alice', 'secret', 'catalog_a']) {
  const inputs = host.querySelectorAll('input')
  for (const [index, value] of values.entries()) {
    const input = inputs[index]!
    input.value = value
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  await settle()
}
async function submitConnection() {
  host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await settle()
}

describe('DatabaseDetailView request ownership', () => {
  beforeEach(() => {
    invalidateSession()
    setActivePinia(createPinia())
    vi.resetAllMocks()
    mocks.route = reactive({ params: { sourceId: 'a' } })
    mocks.connect.mockRejectedValue(new NcpApiError('credentials_required', '需要凭据'))
    useDatabaseStore().sources = [source('a'), source('b')]
  })
  afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.unstubAllGlobals() })

  it('clears all credential fields and ignores late diagnostics when the source changes', async () => {
    const pending = deferred<DatabaseConnectionDiagnostic>()
    mocks.diagnostic.mockReturnValue(pending.promise)
    mount()
    await settle()
    await enterCredentials()
    button('测试连接').click()
    await settle()
    const signal = mocks.diagnostic.mock.calls[0]![1] as AbortSignal
    expect(mocks.diagnostic.mock.calls[0]![0]).toEqual({ sourceId: 'a', credentials: { username: 'alice', password: 'secret', database: 'catalog_a' } })
    mocks.route.params.sourceId = 'b'
    await settle()
    expect(signal.aborted).toBe(true)
    expect([...host.querySelectorAll('input')].map((input) => input.value)).toEqual(['', '', ''])
    pending.resolve(diagnostic('a'))
    await settle()
    expect(host.textContent).toContain('Source b')
    expect(host.querySelector('.diagnostic-result')).toBeNull()
    expect(host.textContent).not.toContain('a:3306')
  })

  it('诊断繁忙保留凭据草稿，只允许手动重新测试', async () => {
    mount()
    await settle()
    await enterCredentials()
    const connects = mocks.connect.mock.calls.length
    mocks.diagnostic.mockRejectedValueOnce(new NcpApiError('operation_busy', 'private-response-secret'))
    button('测试连接').click()
    await settle()
    expect(host.textContent).toContain('数据库操作繁忙，请稍后核对状态再操作。')
    expect(host.textContent).toContain('不会自动重试')
    expect(host.textContent).not.toContain('private-response-secret')
    expect(mocks.diagnostic.mock.calls[0]![0]).toMatchObject({ credentials: { username: 'alice', password: 'secret', database: 'catalog_a' } })
    expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
    await settle()
    expect(mocks.diagnostic).toHaveBeenCalledTimes(1)
    expect(mocks.connect).toHaveBeenCalledTimes(connects)
    expect(mocks.catalog).not.toHaveBeenCalled()
    mocks.diagnostic.mockResolvedValueOnce(diagnostic('a'))
    button('测试连接').click()
    await settle()
    expect(mocks.diagnostic).toHaveBeenCalledTimes(2)
    expect(mocks.diagnostic.mock.calls[1]![0]).toEqual(mocks.diagnostic.mock.calls[0]![0])
    expect(host.textContent).toContain('连接诊断通过')
    expect(host.textContent).not.toContain('数据库操作繁忙')
  })

  it('clears a displayed diagnostic error when switching sources', async () => {
    mocks.diagnostic.mockRejectedValue(new NcpApiError('auth_failed', '认证失败'))
    mount()
    await settle()
    button('测试连接').click()
    await settle()
    expect(host.querySelector('.diagnostic-result')).not.toBeNull()
    expect(host.textContent).toContain('数据库认证失败')
    mocks.route.params.sourceId = 'b'
    await settle()
    expect(host.querySelector('.diagnostic-result')).toBeNull()
    expect(host.textContent).not.toContain('数据库认证失败')
  })

  it('keeps new-source loading intact when an old connection fails', async () => {
    const a = deferred<DatabaseConnectionDiagnostic>()
    const b = deferred<DatabaseConnectionDiagnostic>()
    mocks.connect.mockReset().mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    mount()
    await settle()
    mocks.route.params.sourceId = 'b'
    await settle()
    a.reject(new NcpApiError('auth_failed', '旧来源失败'))
    await settle()
    expect(host.querySelector('[aria-label="正在读取数据库结构"]')).not.toBeNull()
    expect(host.textContent).not.toContain('旧来源失败')
    b.reject(new NcpApiError('credentials_required', '需要凭据'))
    await settle()
    expect(host.textContent).toContain('输入数据库凭据')
  })

  it('shows loading before successful discovery proves a missing source', async () => {
    useDatabaseStore().sources = []
    const pending = deferred<DatabaseDiscovery>()
    mocks.discover.mockReturnValue(pending.promise)
    mount()
    expect(host.textContent).toContain('正在发现数据库来源')
    expect(host.textContent).not.toContain('数据库来源不存在')
    pending.resolve({ sources: [], collectedAt: '' })
    await settle()
    expect(host.textContent).toContain('数据库来源不存在')
  })

  it('renders discovery failure with retry rather than not-found', async () => {
    useDatabaseStore().sources = []
    mocks.discover.mockRejectedValue(new NcpApiError('agent_unavailable', '代理不可用'))
    mount()
    await settle()
    expect(host.textContent).toContain('数据库发现失败')
    expect(host.textContent).not.toContain('数据库来源不存在')
    mocks.discover.mockResolvedValue({ sources: [source('a')], collectedAt: '' })
    button('重试').click()
    await settle()
    expect(mocks.discover).toHaveBeenCalledTimes(2)
    expect(host.textContent).toContain('Source a')
    expect(host.textContent).toContain('输入数据库凭据')
    expect(host.textContent).not.toContain('数据库发现失败')
    expect(host.textContent).not.toContain('数据库对象目录读取失败')
  })

  it('retries a failed initial catalog read without re-running discovery or connection', async () => {
    useDatabaseStore().sources = [{ ...source('a'), requiresLogin: false }]
    mocks.catalog.mockRejectedValueOnce(new NcpApiError('agent_unavailable', '代理不可用'))
      .mockResolvedValueOnce({ tables: [] })
    mount()
    await settle()
    expect(host.textContent).toContain('数据库对象目录读取失败')
    expect(host.textContent).not.toContain('数据库发现失败')
    expect(host.textContent).not.toContain('数据库连接失败')
    button('重新读取目录').click()
    await settle()
    expect(mocks.catalog).toHaveBeenCalledTimes(2)
    expect(mocks.discover).not.toHaveBeenCalled()
    expect(mocks.connect).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="数据表和视图列表"]')).not.toBeNull()
    expect(host.textContent).not.toContain('数据库对象目录读取失败')
  })

  it('keeps credential errors in the connection form without a catalog retry action', async () => {
    mocks.connect.mockRejectedValue(new NcpApiError('auth_failed', '认证失败'))
    mount()
    await settle()
    expect(host.textContent).toContain('数据库连接失败')
    expect(host.textContent).toContain('输入数据库凭据')
    expect(host.textContent).not.toContain('重新读取目录')
    expect(host.textContent).not.toContain('数据库发现失败')
  })

  it.each(['logout', 'unmount'])('invalidates a pending diagnostic on %s', async (action) => {
    const pending = deferred<DatabaseConnectionDiagnostic>()
    mocks.diagnostic.mockReturnValue(pending.promise)
    mount()
    await settle()
    await enterCredentials()
    button('测试连接').click()
    await settle()
    const signal = mocks.diagnostic.mock.calls[0]![1] as AbortSignal
    if (action === 'logout') invalidateSession()
    else { app?.unmount(); app = undefined }
    expect(signal.aborted).toBe(true)
    pending.resolve(diagnostic('a'))
    await settle()
    expect(host.querySelector('.diagnostic-result')).toBeNull()
    expect([...host.querySelectorAll('input')].every((input) => input.value === '')).toBe(true)
  })

  it('未知连接结果保留草稿，不显示失败诊断或自动重连', async () => {
    mount(); await settle(); await enterCredentials()
    mocks.connect.mockRejectedValue(new DatabaseConnectionOutcomeUnknownError())
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await settle()
    expect(host.textContent).toContain('数据库连接结果未确认')
    expect(host.textContent).toContain('服务端可能已保存本次凭据')
    expect(host.textContent).not.toContain('数据库连接失败')
    expect(host.querySelector('.diagnostic-result')).toBeNull()
    expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
    expect(useDatabaseStore().credentials).toEqual({})
    expect(useDatabaseStore().catalogs).toEqual({})
    expect(mocks.catalog).not.toHaveBeenCalled()
    expect(mocks.connect).toHaveBeenCalledTimes(2)
    // 不加全局锁；用户看过风险后仍能明确发起一次新连接。
    expect(button('连接并读取数据表').disabled).toBe(false)
    await settle(); expect(mocks.connect).toHaveBeenCalledTimes(2)
  })

  it('测试响应无效显示未确认，不误报凭据已保存或诊断未通过', async () => {
    mocks.diagnostic.mockRejectedValue(new NcpApiError('DATABASE_RESPONSE_INVALID', '数据库服务返回了无法识别的数据。'))
    mount(); await settle(); button('测试连接').click(); await settle()
    expect(host.textContent).toContain('数据库连接诊断结果未确认')
    expect(host.textContent).toContain('测试连接不会保存凭据')
    expect(host.textContent).not.toContain('服务端可能已保存')
    expect(host.textContent).not.toContain('连接诊断未通过')
    expect(host.querySelector('.diagnostic-result')).toBeNull()
    expect(mocks.diagnostic).toHaveBeenCalledTimes(1)
  })

  it('已确认连接的目录失败显示正确阶段，保留草稿', async () => {
    mount(); await settle(); await enterCredentials()
    mocks.connect.mockResolvedValue(diagnostic('a'))
    mocks.catalog.mockRejectedValue(new Error('private-fixture'))
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await settle()
    expect(host.textContent).toContain('数据库连接已确认，目录读取失败')
    expect(host.textContent).toContain('服务端可能已保存本次凭据')
    expect(host.textContent).not.toContain('private-fixture')
    expect(host.textContent).not.toContain('数据库连接失败')
    expect(host.querySelector('.diagnostic-result')).toBeNull()
    expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
    expect(useDatabaseStore().credentials).toEqual({})
  })

  it.each(['__proto__', 'constructor', 'toString', 'future_failure'])('未知失败码 %s 使用自有文案回退', async (code) => {
    mocks.diagnostic.mockResolvedValue({ ...diagnostic('a'), connected: false, code })
    mount(); await settle(); button('测试连接').click(); await settle()
    expect(host.textContent).toContain('连接诊断未通过。')
    expect(host.textContent).toContain(code)
    expect(host.textContent).not.toContain('[object Object]')
    expect(host.textContent).not.toContain('function Object')
    expect(host.querySelector('.diagnostic-result--success')).toBeNull()
  })

  describe('真实 API、store 与保存冲突视图', () => {
    beforeEach(async () => {
      const api = await vi.importActual<typeof import('@/api/database')>('@/api/database')
      mocks.connect.mockImplementation(api.connectDatabase)
      mocks.diagnostic.mockImplementation(api.testDatabaseConnection)
      mocks.catalog.mockImplementation(api.loadDatabaseCatalog)
    })

    it('精确 409 保留草稿及既有缓存，只有明确重试并读到目录后才发布新凭据', async () => {
      const pendingCatalog = deferred<Response>()
      const fetcher = vi.fn<typeof fetch>()
        .mockResolvedValueOnce(credentialsRequired())
        .mockResolvedValueOnce(response(diagnostic('a')))
        .mockResolvedValueOnce(credentialConflict())
        .mockResolvedValueOnce(response(diagnostic('a')))
        .mockReturnValueOnce(pendingCatalog.promise)
      vi.stubGlobal('fetch', fetcher)
      const store = useDatabaseStore()
      const previousCredentials = { a: { username: 'previous-a' }, b: { username: 'previous-b' } }
      const previousCatalogs = { b: { source: source('b'), tables: [] } }
      store.credentials = previousCredentials
      store.catalogs = previousCatalogs
      mount(); await settle(); await enterCredentials()
      button('测试连接').click(); await settle()
      expect(host.textContent).toContain('连接诊断通过')
      await submitConnection()
      expect(host.querySelector('.connection-pill')?.textContent).toBe('连接凭据未保存')
      expect(host.textContent).toContain('其他操作已修改保存的凭据，本次连接未保存凭据。请重新确认后再连接。')
      expect(host.textContent).toContain('手动重新连接；不会自动重试')
      expect(host.textContent).not.toContain('服务端可能已保存')
      expect(host.textContent).not.toContain('数据库连接失败')
      expect(host.textContent).not.toContain('private-fixture')
      expect(host.textContent).not.toContain('password=secret')
      expect(host.querySelector('.diagnostic-result')).toBeNull()
      expect(host.querySelector('[aria-label="数据表和视图列表"]')).toBeNull()
      expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
      expect(store.credentials).toEqual(previousCredentials)
      expect(store.catalogs).toEqual(previousCatalogs)
      expect(mocks.catalog).not.toHaveBeenCalled()
      expect(button('连接并读取数据表').disabled).toBe(false)
      await settle()
      expect(fetcher).toHaveBeenCalledTimes(3)

      await enterCredentials(['bob', 'new-secret', 'catalog_retry'])
      await submitConnection()
      expect(fetcher).toHaveBeenCalledTimes(5)
      const retried = { sourceId: 'a', credentials: { username: 'bob', password: 'new-secret', database: 'catalog_retry' } }
      expect(JSON.parse(String(fetcher.mock.calls[2]![1]!.body))).toEqual({ sourceId: 'a', credentials: { username: 'alice', password: 'secret', database: 'catalog_a' } })
      expect(JSON.parse(String(fetcher.mock.calls[3]![1]!.body))).toEqual(retried)
      expect(fetcher.mock.calls[4]![0]).toBe('/api/v1/databases/catalog')
      expect(JSON.parse(String(fetcher.mock.calls[4]![1]!.body))).toEqual(retried)
      expect(store.credentials).toEqual(previousCredentials)
      expect(store.catalogs).toEqual(previousCatalogs)
      pendingCatalog.resolve(response({ source: source('a'), tables: [] }))
      await settle()
      expect(store.credentials).toEqual({ ...previousCredentials, a: retried.credentials })
      expect(store.catalogs.b).toEqual(previousCatalogs.b)
      expect(store.catalogs.a?.source.id).toBe('a')
      expect(host.querySelector('.connection-pill')?.textContent).toBe('已连接')
      expect(host.textContent).not.toContain('连接凭据未保存')
      expect(host.querySelector('form')).toBeNull()
      expect(fetcher).toHaveBeenCalledTimes(5)
    })

    it('冲突后的测试连接仍只诊断，不保存或发布凭据，不自动重连', async () => {
      const fetcher = vi.fn<typeof fetch>()
        .mockResolvedValueOnce(credentialsRequired())
        .mockResolvedValueOnce(credentialConflict())
        .mockResolvedValueOnce(response(diagnostic('a')))
      vi.stubGlobal('fetch', fetcher)
      mount(); await settle(); await enterCredentials(); await submitConnection()
      button('测试连接').click(); await settle()
      expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
        '/api/v1/databases/connect', '/api/v1/databases/connect', '/api/v1/databases/test-connection',
      ])
      expect(host.querySelector('.connection-pill')?.textContent).toBe('诊断通过')
      expect(host.textContent).not.toContain('服务端可能已保存')
      expect(host.textContent).not.toContain('已连接')
      expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
      expect(useDatabaseStore().credentials).toEqual({})
      expect(useDatabaseStore().catalogs).toEqual({})
      expect(mocks.catalog).not.toHaveBeenCalled()
      await settle(); expect(fetcher).toHaveBeenCalledTimes(3)
    })

    it('连接确认后的目录 409 仍属于目录失败，不能证明本次凭据未保存', async () => {
      const fetcher = vi.fn<typeof fetch>()
        .mockResolvedValueOnce(credentialsRequired())
        .mockResolvedValueOnce(response(diagnostic('a')))
        .mockResolvedValueOnce(credentialConflict())
      vi.stubGlobal('fetch', fetcher)
      mount(); await settle(); await enterCredentials(); await submitConnection()
      expect(host.textContent).toContain('数据库连接已确认，目录读取失败')
      expect(host.textContent).toContain('服务端可能已保存本次凭据')
      expect(host.textContent).not.toContain('连接凭据未保存')
      expect(host.textContent).not.toContain('private-fixture')
      expect(host.querySelector('.connection-pill')?.textContent).toBe('目录读取失败')
      expect(host.querySelector('.diagnostic-result')).toBeNull()
      expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
      expect(useDatabaseStore().credentials).toEqual({})
      expect(useDatabaseStore().catalogs).toEqual({})
      expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
        '/api/v1/databases/connect', '/api/v1/databases/connect', '/api/v1/databases/catalog',
      ])
      await settle(); expect(fetcher).toHaveBeenCalledTimes(3)
    })

    it.each([
      ['tls_configuration_invalid', 'TLS 设置无效，请检查 CA 证书和服务器名。'],
      ['tls_policy_unsupported', 'Root Agent 尚不支持此 TLS 策略，请升级后重试。'],
      ['tls_verification_failed', '证书或服务器身份校验失败，请核对 CA、服务器名和证书有效期。'],
    ])('只读诊断 %s 使用安全文案，不声称已连接或自动重试', async (code, copy) => {
      const fetcher = vi.fn<typeof fetch>()
        .mockResolvedValueOnce(credentialsRequired())
        .mockResolvedValueOnce(response({ code, message: 'private-fixture caPEM password=secret', requestId: 'fixture-request' }, 400))
      vi.stubGlobal('fetch', fetcher)
      mount(); await settle(); await enterCredentials()
      button('测试连接').click(); await settle()
      expect(host.textContent).toContain(copy)
      expect(host.textContent).not.toContain('private-fixture')
      expect(host.textContent).not.toContain('password=secret')
      expect(host.textContent).not.toContain('已连接')
      expect(host.textContent).not.toContain('本次连接未保存')
      expect(host.querySelector('.diagnostic-result--success')).toBeNull()
      expect(host.querySelector('.diagnostic-result--failure')?.textContent).toContain(code)
      expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
      expect(useDatabaseStore().credentials).toEqual({})
      expect(useDatabaseStore().catalogs).toEqual({})
      await settle(); expect(fetcher).toHaveBeenCalledTimes(2)
    })

    it.each(['diagnostic', 'catalog'])('%s 的同名冲突不继承 connect 专有的保存结论', async (operation) => {
      const fetcher = vi.fn<typeof fetch>()
      if (operation === 'diagnostic') {
        fetcher.mockResolvedValueOnce(credentialsRequired()).mockResolvedValueOnce(credentialConflict())
      } else {
        useDatabaseStore().sources = [{ ...source('a'), requiresLogin: false }]
        fetcher.mockResolvedValueOnce(credentialConflict())
      }
      vi.stubGlobal('fetch', fetcher)
      mount(); await settle()
      if (operation === 'diagnostic') { button('测试连接').click(); await settle() }
      expect(host.textContent).toContain('数据库凭据发生冲突，请重新确认后重试。')
      expect(host.textContent).not.toContain('连接凭据未保存')
      expect(host.textContent).not.toContain('本次连接未保存')
      expect(host.textContent).not.toContain('服务端可能已保存')
      expect(host.textContent).not.toContain('private-fixture')
      expect(useDatabaseStore().credentials).toEqual({})
      expect(useDatabaseStore().catalogs).toEqual({})
      expect(fetcher).toHaveBeenCalledTimes(operation === 'diagnostic' ? 2 : 1)
    })

    it.each([
      { name: '冲突码状态不匹配', reply: () => Promise.resolve(response({ code: 'credential_conflict', message: 'private-fixture', requestId: 'fixture-request' }, 503)) },
      { name: '409 错误码不匹配', reply: () => Promise.resolve(response({ code: 'auth_failed', message: 'private-fixture', requestId: 'fixture-request' }, 409)) },
      { name: '409 信封缺少 requestId', reply: () => Promise.resolve(response({ code: 'credential_conflict', message: 'private-fixture' }, 409)) },
      { name: '409 正文无法解析', reply: () => Promise.resolve(new Response('private-fixture', { status: 409 })) },
      ...['tls_configuration_invalid', 'tls_policy_unsupported', 'tls_verification_failed'].map(code => ({
        name: `连接返回 ${code}`, reply: () => Promise.resolve(response({ code, message: 'private-fixture', requestId: 'fixture-request' }, 400)),
      })),
      { name: '网络中断', reply: () => Promise.reject(new TypeError('private-fixture')) },
    ])('$name 保持结果未知，不把草稿说成未保存', async ({ reply }) => {
      const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(credentialsRequired()).mockImplementationOnce(reply)
      vi.stubGlobal('fetch', fetcher)
      mount(); await settle(); await enterCredentials(); await submitConnection()
      expect(host.querySelector('.connection-pill')?.textContent).toBe('连接结果未确认')
      expect(host.textContent).toContain('服务端可能已保存本次凭据')
      expect(host.textContent).not.toContain('连接凭据未保存')
      expect(host.textContent).not.toContain('private-fixture')
      expect(host.querySelector('.diagnostic-result')).toBeNull()
      expect([...host.querySelectorAll('input')].map(input => input.value)).toEqual(['alice', 'secret', 'catalog_a'])
      expect(useDatabaseStore().credentials).toEqual({})
      expect(useDatabaseStore().catalogs).toEqual({})
      expect(mocks.catalog).not.toHaveBeenCalled()
      await settle(); expect(fetcher).toHaveBeenCalledTimes(2)
    })

    it.each(['source', 'session', 'unmount'])('迟到的保存冲突不会污染已变更的 %s 上下文', async (change) => {
      const pending = deferred<Response>()
      const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(credentialsRequired()).mockReturnValueOnce(pending.promise)
      vi.stubGlobal('fetch', fetcher)
      const store = useDatabaseStore()
      mount(); await settle(); await enterCredentials(); await submitConnection()
      const signal = fetcher.mock.calls[1]![1]!.signal as AbortSignal
      const currentCredentials = { username: 'current-user' }
      const currentCatalog = { source: source(change === 'source' ? 'b' : 'a'), tables: [] }
      if (change === 'source') {
        store.credentials.b = currentCredentials
        store.catalogs.b = currentCatalog
        mocks.route.params.sourceId = 'b'
      } else if (change === 'session') {
        invalidateSession()
        store.resetSessionState()
        store.sources = [source('a'), source('b')]
        store.credentials.a = currentCredentials
        store.catalogs.a = currentCatalog
        app?.unmount(); app = undefined; host.remove()
        mount()
      } else { app?.unmount(); app = undefined }
      await settle()
      expect(signal.aborted).toBe(true)
      const credentialsBefore = JSON.stringify(store.credentials)
      const catalogsBefore = JSON.stringify(store.catalogs)
      pending.resolve(credentialConflict())
      await settle()
      expect(host.textContent).not.toContain('连接凭据未保存')
      expect(host.textContent).not.toContain('private-fixture')
      expect(host.querySelector('.diagnostic-result')).toBeNull()
      expect(JSON.stringify(store.credentials)).toBe(credentialsBefore)
      expect(JSON.stringify(store.catalogs)).toBe(catalogsBefore)
      expect([...host.querySelectorAll('input')].every(input => input.value === '')).toBe(true)
      expect(mocks.catalog).not.toHaveBeenCalled()
      expect(fetcher).toHaveBeenCalledTimes(2)
    })
  })

})

const syntheticPEM = '-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n'
async function inputText(label: string, value: string) {
  const input = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(`[aria-label="${label}"]`)!
  expect(input).not.toBeNull()
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}
async function chooseTrust(label: string) {
  host.querySelector<HTMLElement>('[data-connection-field="trust"] .el-select__wrapper')!.click()
  await settle()
  const option = [...document.querySelectorAll<HTMLElement>('.el-select-dropdown__item')].find(item => item.textContent?.includes(label))
  expect(option).toBeDefined()
  option!.click()
  await settle()
}
async function startTLS(trust = '自定义 PEM CA') {
  button('设置证书校验 TLS').click(); await settle()
  await chooseTrust(trust)
  if (trust === '自定义 PEM CA') await inputText('CA 证书（PEM）', syntheticPEM)
}

describe('DatabaseDetailView explicit TLS settings', () => {
  beforeEach(() => {
    invalidateSession(); setActivePinia(createPinia()); vi.resetAllMocks()
    mocks.route = reactive({ params: { sourceId: 'a' } })
    mocks.connect.mockRejectedValue(new NcpApiError('credentials_required', '需要凭据'))
    mocks.diagnostic.mockResolvedValue(diagnostic('a'))
    useDatabaseStore().sources = [source('a')]
  })
  afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.unstubAllGlobals() })

  it('发现填充同 ID 时只初始化一次；目标变化重新初始化且展示改名不重连', async () => {
    const store = useDatabaseStore()
    store.sources = []
    mocks.discover.mockResolvedValue({ sources: [source('a')], collectedAt: '2026-10-14T17:00:00Z' })
    mount(); await settle()
    expect(mocks.discover).toHaveBeenCalledTimes(1)
    expect(mocks.connect).toHaveBeenCalledTimes(1)
    expect(mocks.catalog).not.toHaveBeenCalled()
    store.sources[0]!.name = 'Changed display name'
    await settle()
    expect(mocks.connect).toHaveBeenCalledTimes(1)
    store.sources[0]!.host = 'replacement'
    await settle()
    expect(mocks.discover).toHaveBeenCalledTimes(1)
    expect(mocks.connect).toHaveBeenCalledTimes(2)
  })

  it('完整成功只清理未变化的草稿，再次打开不会回填密码或 CA', async () => {
    mount(); await settle(); await enterCredentials(); await startTLS()
    mocks.connect.mockResolvedValue(diagnostic('a'))
    mocks.catalog.mockResolvedValue({ source: source('a'), tables: [] })
    await submitConnection()
    expect(mocks.connect).toHaveBeenCalledTimes(2)
    expect(mocks.catalog).toHaveBeenCalledTimes(1)
    expect(host.querySelector('form')).toBeNull()
    expect(host.querySelector('.connection-pill')?.textContent).toBe('已连接')
    button('连接设置').click(); await settle()
    expect([...host.querySelectorAll<HTMLInputElement>('form input')].every(input => input.value === '')).toBe(true)
    expect(host.querySelector('textarea')).toBeNull()
    expect(host.textContent).toContain('TLS 状态未确认')
    expect(host.textContent).not.toContain('TLS 已验证')
  })

  it('TLS 字段重新挂载后实际 combobox 仍关联即时错误与帮助', async () => {
    mount(); await settle(); await enterCredentials()
    for (let iteration = 0; iteration < 2; iteration++) {
      button('设置证书校验 TLS').click(); await settle()
      button('测试连接').click(); await settle()
      const input = host.querySelector<HTMLInputElement>('[data-connection-field="trust"] input[role="combobox"]')!
      expect(document.activeElement).toBe(input)
      expect(input.getAttribute('aria-describedby')).toBe('tls-trust-help tls-trust-error')
      expect(host.querySelectorAll('#tls-trust-error')).toHaveLength(1)
      expect(host.querySelector('#tls-trust-error')?.textContent).toBe('请选择信任来源')
      button('撤销本次 TLS 编辑').click(); await settle()
      expect(host.querySelector('#tls-trust-error')).toBeNull()
    }
    expect(mocks.diagnostic).not.toHaveBeenCalled()
  })

  it.each(['mysql', 'postgresql'] as const)('已有 %s 目录可打开同一表单，保留列表且状态未确认', async driver => {
    const store = useDatabaseStore()
    store.sources = [{ ...source('a'), driver }]
    store.catalogs.a = { source: store.sources[0]!, tables: [] }
    mount(); await settle()
    expect(host.querySelector('form')).toBeNull()
    button('连接设置').click(); await settle()
    expect(host.querySelector('[aria-label="数据表和视图列表"]')).not.toBeNull()
    expect(host.querySelector('form')?.textContent).toContain('TLS 状态未确认')
    expect(host.textContent).toContain('服务端沿用已保存的严格策略')
    expect(host.textContent).not.toContain('已加密')
    expect(host.textContent).not.toContain('TLS 已验证')
    expect(mocks.connect).not.toHaveBeenCalled()
  })

  it('SQLite 没有 TLS 或连接设置入口', async () => {
    const store = useDatabaseStore()
    store.sources = [{ ...source('a'), driver: 'sqlite', requiresLogin: false }]
    store.catalogs.a = { source: store.sources[0]!, tables: [] }
    mount(); await settle()
    expect(host.textContent).not.toContain('设置证书校验 TLS')
    expect([...host.querySelectorAll('button')].some(item => item.textContent === '连接设置')).toBe(false)
  })

  it('显式系统根测试只发 verify-full，不复制保存 CA 或密码，也不宣称安全证明', async () => {
    useDatabaseStore().credentials.a = { username: 'saved', password: 'saved-synthetic', tls: { mode: 'verify-full', caPEM: 'saved-ca', serverName: 'saved.example' } }
    mount(); await settle()
    await enterCredentials(['alice', 'typed-synthetic', 'fixture'])
    await inputText('密码', '')
    await startTLS('Root Agent 系统根证书')
    button('测试连接').click(); await settle()
    expect(mocks.diagnostic).toHaveBeenCalledWith({ sourceId: 'a', credentials: { username: 'alice', password: '', database: 'fixture', tls: { mode: 'verify-full' } } }, expect.any(AbortSignal))
    expect(host.textContent).toContain('连接诊断通过')
    expect(host.textContent).toContain('TLS 状态未确认')
    expect(host.textContent).not.toContain('saved-')
    expect(host.textContent).not.toContain('已加密')
    expect(mocks.connect).toHaveBeenCalledTimes(1)
  })

  it('自定义和系统切换清除 CA；撤销只省略本次策略并将焦点返回入口', async () => {
    mount(); await settle(); await enterCredentials()
    await startTLS()
    await inputText('服务器名（可选）', 'DB.Example.')
    button('测试连接').click(); await settle()
    expect(mocks.diagnostic.mock.calls[0]![0].credentials.tls).toEqual({ mode: 'verify-full', caPEM: syntheticPEM, serverName: 'DB.Example.' })
    await chooseTrust('Root Agent 系统根证书')
    button('测试连接').click(); await settle()
    expect(mocks.diagnostic.mock.calls[1]![0].credentials.tls).toEqual({ mode: 'verify-full', serverName: 'DB.Example.' })
    await chooseTrust('自定义 PEM CA')
    expect(host.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe('')
    button('撤销本次 TLS 编辑').click(); await settle()
    expect(document.activeElement).toBe(button('设置证书校验 TLS'))
    button('测试连接').click(); await settle()
    expect(mocks.diagnostic.mock.calls[2]![0].credentials).not.toHaveProperty('tls')
    expect(host.querySelector('textarea')).toBeNull()
    expect(host.textContent).toContain('沿用已保存的严格策略')
  })

  it('未选根来源和无效 CA 都零派发；错误关联字段并聚焦首错', async () => {
    mount(); await settle(); await enterCredentials()
    button('设置证书校验 TLS').click(); await settle()
    button('测试连接').click(); await settle()
    expect(host.querySelector('[data-connection-field="trust"]')!.contains(document.activeElement)).toBe(true)
    expect(document.activeElement?.getAttribute('aria-describedby')).toBe('tls-trust-help tls-trust-error')
    expect(document.activeElement?.getAttribute('aria-invalid')).toBe('true')
    expect(host.querySelector('#tls-trust-error')?.textContent).toBe('请选择信任来源')
    expect(mocks.diagnostic).not.toHaveBeenCalled()
    await chooseTrust('自定义 PEM CA')
    await inputText('CA 证书（PEM）', '界'.repeat(21846))
    button('测试连接').click(); await settle()
    const textarea = host.querySelector('textarea')!
    expect(document.activeElement).toBe(textarea)
    expect(textarea.getAttribute('aria-invalid')).toBe('true')
    expect(textarea.getAttribute('aria-describedby')).toContain('tls-ca-error')
    expect(host.querySelector('#tls-ca-error')?.textContent).toBe('CA 内容超过 64 KiB')
    expect(mocks.diagnostic).not.toHaveBeenCalled()
    await inputText('CA 证书（PEM）', syntheticPEM)
    await inputText('服务器名（可选）', ' db.example')
    button('测试连接').click(); await settle()
    expect(document.activeElement?.getAttribute('aria-label')).toBe('服务器名（可选）')
    expect(host.textContent).toContain('服务器名格式无效')
    expect(mocks.diagnostic).not.toHaveBeenCalled()
  })

  it('TLS-only 需要本次用户名和有效数据库，不从保存凭据补全', async () => {
    useDatabaseStore().credentials.a = { username: 'saved', password: 'saved-synthetic', database: 'saved' }
    mount(); await settle(); await startTLS('Root Agent 系统根证书')
    button('测试连接').click(); await settle()
    expect(mocks.diagnostic).not.toHaveBeenCalled()
    expect(host.textContent).toContain('请输入本次连接的用户名')
    expect(host.textContent).toContain('请输入数据库名')
    expect(document.activeElement?.getAttribute('aria-label')).toBe('用户名')
  })

  it('测试期间可以编辑，禁用重复派发，迟到响应只表示上次提交', async () => {
    const pending = deferred<DatabaseConnectionDiagnostic>()
    mocks.diagnostic.mockReturnValue(pending.promise)
    mount(); await settle(); await enterCredentials(); await startTLS()
    button('测试连接').click(); await settle()
    expect(button('测试连接').disabled).toBe(true)
    expect(button('连接并读取数据表').disabled).toBe(true)
    await inputText('服务器名（可选）', 'new.example')
    await inputText('CA 证书（PEM）', syntheticPEM + '\n')
    await submitConnection()
    expect(mocks.connect).toHaveBeenCalledTimes(1)
    pending.resolve(diagnostic('a')); await settle()
    expect(mocks.diagnostic.mock.calls[0]![0].credentials.tls).toEqual({ mode: 'verify-full', caPEM: syntheticPEM })
    expect(host.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe(syntheticPEM + '\n')
    expect(host.textContent).toContain('上次提交结果')
    expect(host.textContent).not.toContain('连接诊断通过')
    expect(host.querySelector('.diagnostic-result--success')).toBeNull()
  })

  it('connect 与 catalog 使用冻结草稿，迟到完整成功不清空新输入', async () => {
    mount(); await settle(); await enterCredentials(); await startTLS()
    const pending = deferred<DatabaseConnectionDiagnostic>()
    mocks.connect.mockReturnValue(pending.promise)
    mocks.catalog.mockResolvedValue({ source: source('a'), tables: [] })
    await submitConnection()
    await inputText('服务器名（可选）', 'new.example')
    await inputText('数据库名', 'new_database')
    await inputText('CA 证书（PEM）', syntheticPEM + '\n')
    pending.resolve(diagnostic('a')); await settle()
    const frozen = { sourceId: 'a', credentials: { username: 'alice', password: 'secret', database: 'catalog_a', tls: { mode: 'verify-full', caPEM: syntheticPEM } } }
    expect(mocks.connect.mock.calls[1]![0]).toEqual(frozen)
    expect(mocks.catalog.mock.calls[0]![0]).toEqual(frozen)
    expect(host.querySelector('[aria-label="数据表和视图列表"]')).not.toBeNull()
    expect(host.querySelector('form')).not.toBeNull()
    expect(host.querySelector<HTMLInputElement>('[aria-label="数据库名"]')!.value).toBe('new_database')
    expect(host.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe(syntheticPEM + '\n')
    expect(host.textContent).toContain('上次提交结果')
  })

  it('相同 ID 换目标立即清草稿、密码和诊断并废弃旧请求', async () => {
    const pending = deferred<DatabaseConnectionDiagnostic>()
    mocks.diagnostic.mockReturnValue(pending.promise)
    mount(); await settle(); await enterCredentials(); await startTLS()
    button('测试连接').click(); await settle()
    const signal = mocks.diagnostic.mock.calls[0]![1] as AbortSignal
    useDatabaseStore().sources[0]!.host = 'replacement'
    await settle()
    expect(signal.aborted).toBe(true)
    expect(host.querySelector('textarea')).toBeNull()
    expect(host.querySelector<HTMLInputElement>('[aria-label="密码"]')!.value).toBe('')
    pending.resolve(diagnostic('a')); await settle()
    expect(host.querySelector('.diagnostic-result')).toBeNull()
    expect(host.textContent).not.toContain('连接诊断通过')
  })

  it.each(['conflict', 'unknown', 'catalog'] as const)('已有目录不会遮盖 %s，保留本次 TLS 草稿和旧缓存', async outcome => {
    const store = useDatabaseStore()
    store.catalogs.a = { source: source('a'), tables: [] }
    store.credentials.a = { username: 'old', tls: { mode: 'verify-full' } }
    mount(); await settle(); button('连接设置').click(); await settle()
    await inputText('用户名', 'alice'); await inputText('数据库名', 'fixture'); await startTLS()
    if (outcome === 'conflict') mocks.connect.mockRejectedValue(new NcpApiError('credential_conflict', 'private'))
    else if (outcome === 'unknown') mocks.connect.mockRejectedValue(new DatabaseConnectionOutcomeUnknownError())
    else { mocks.connect.mockResolvedValue(diagnostic('a')); mocks.catalog.mockRejectedValue(new Error('private')) }
    await submitConnection()
    const expected = outcome === 'conflict' ? '连接凭据未保存' : outcome === 'unknown' ? '连接结果未确认' : '目录读取失败'
    expect(host.querySelector('.connection-pill')?.textContent).toBe(expected)
    expect(host.querySelector('[aria-label="数据表和视图列表"]')).not.toBeNull()
    expect(host.querySelector<HTMLTextAreaElement>('textarea')!.value).toBe(syntheticPEM)
    expect(store.credentials.a).toEqual({ username: 'old', tls: { mode: 'verify-full' } })
    expect(host.textContent).not.toContain('private')
    expect(mocks.connect).toHaveBeenCalledTimes(1)
  })

  it('未知服务端错误消息不反射 CA 或密码', async () => {
    mocks.diagnostic.mockRejectedValue(new NcpApiError('future_failure', 'reflected-synthetic-ca password=synthetic'))
    mount(); await settle(); button('测试连接').click(); await settle()
    expect(host.textContent).toContain('连接诊断失败，请稍后重试。')
    expect(host.textContent).not.toContain('reflected-synthetic')
    expect(host.textContent).not.toContain('password=')
  })
})
