// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DatabaseConnectionDiagnostic, DatabaseDiscovery, DatabaseSource } from '@/api/database'
import { NcpApiError } from '@/api/system'
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
async function enterCredentials() {
  const inputs = host.querySelectorAll('input')
  for (const [index, value] of ['alice', 'secret', 'catalog_a'].entries()) {
    const input = inputs[index]!
    input.value = value
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
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
  afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

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
})
