import { afterEach, describe, expect, it, vi } from 'vitest'
import { connectDatabase, testDatabaseConnection, loadDatabaseCatalog, executeDatabaseSQL, insertDatabaseRow, updateDatabaseRow, deleteDatabaseRow, loadDatabaseRows, type DatabaseCredentials } from './database'

const source = { id: 'fixture', name: 'Fixture', driver: 'mysql', category: 'project', project: '', module: '', location: 'fixture:3306', requiresLogin: true, status: '', tags: [] }
const diagnostic = { connected: true, driver: 'mysql', endpoint: 'fixture:3306', operation: 'test_connection', durationMs: 1 }
const table = { schema: '', name: 'sample', type: 'table', columns: [] }
const result = { columns: [], rows: [], rowsAffected: 1, truncated: false, durationMs: 1 }
afterEach(() => vi.unstubAllGlobals())

for (const [name, submit, response] of [
  ['connect', (credentials: DatabaseCredentials) => connectDatabase({ sourceId: 'fixture', credentials }), diagnostic],
  ['test', (credentials: DatabaseCredentials) => testDatabaseConnection({ sourceId: 'fixture', credentials }), diagnostic],
  ['catalog', (credentials: DatabaseCredentials) => loadDatabaseCatalog({ sourceId: 'fixture', credentials }), { source, tables: [] }],
  ['query', (credentials: DatabaseCredentials) => executeDatabaseSQL({ sourceId: 'fixture', credentials, sql: 'SELECT 1', resultMode: 'rows' }), result],
  ['rows', (credentials: DatabaseCredentials) => loadDatabaseRows({ sourceId: 'fixture', credentials, table: 'sample' }), { table, rows: [], limit: 20, offset: 0, hasMore: false }],
  ['insert', (credentials: DatabaseCredentials) => insertDatabaseRow({ sourceId: 'fixture', credentials, table: 'sample', values: { id: 1 } }), { rowsAffected: 1 }],
  ['update', (credentials: DatabaseCredentials) => updateDatabaseRow({ sourceId: 'fixture', credentials, table: 'sample', keys: { id: 1 }, values: { value: 2 } }), { rowsAffected: 1 }],
  ['delete', (credentials: DatabaseCredentials) => deleteDatabaseRow({ sourceId: 'fixture', credentials, table: 'sample', keys: { id: 1 } }), { rowsAffected: 1 }],
] as const) {
  describe(`${name} TLS 请求序列化边界`, () => {
    it('派发前冻结嵌套策略，保留已有请求选项，只有一次派发', async () => {
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(response), { status: name === 'insert' ? 201 : 200 }))
      vi.stubGlobal('fetch', fetcher)
      const credentials: DatabaseCredentials = { username: 'fixture', password: '', tls: { mode: 'verify-full', caPEM: 'synthetic-ca', serverName: 'fixture' } }
      const expected = structuredClone(credentials)
      const pending = submit(credentials)
      credentials.tls!.caPEM = 'edited'; credentials.tls!.serverName = 'new'
      await pending
      expect(fetcher).toHaveBeenCalledTimes(1)
      const body = JSON.parse(String(fetcher.mock.calls[0]![1]!.body))
      expect(body.credentials).toEqual(expected)
      if (name === 'query') expect(body.resultMode).toBe('rows')
    })
    it('旧凭据不添加 tls 或默认 policy', async () => {
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(response), { status: name === 'insert' ? 201 : 200 }))
      vi.stubGlobal('fetch', fetcher)
      await submit({ username: 'fixture' })
      expect(JSON.parse(String(fetcher.mock.calls[0]![1]!.body)).credentials).toEqual({ username: 'fixture' })
    })
  })
}
