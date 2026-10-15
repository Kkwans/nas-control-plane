import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  connectDatabase, deleteDatabaseRow, discoverDatabases, executeDatabaseSQL, insertDatabaseRow,
  loadDatabaseCatalog, loadDatabaseRows, testDatabaseConnection, updateDatabaseRow,
} from './database'

const connection = { sourceId: 'fixture', credentials: { password: 'credential-secret' } }
const row = { ...connection, table: 'sample', keys: { id: 1 }, values: { label: 'row-secret' } }
function busy() {
  return new Response(JSON.stringify({ code: 'operation_busy', message: 'private-response-secret', requestId: 'request-1' }), { status: 503 })
}
afterEach(() => { vi.unstubAllGlobals() })

describe('数据库繁忙不会自动重放请求或扩大写入安全拒绝范围', () => {
  it.each([
    ['discovery', () => discoverDatabases()],
    ['diagnostic', () => testDatabaseConnection(connection)],
    ['catalog', () => loadDatabaseCatalog(connection)],
    ['rows', () => loadDatabaseRows({ ...connection, table: 'sample', offset: 25 })],
  ] as const)('%s 保留稳定繁忙码且仅发送一次', async (_, request) => {
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => busy())
    vi.stubGlobal('fetch', fetcher)
    await expect(request()).rejects.toMatchObject({ code: 'operation_busy' })
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['connect', () => connectDatabase(connection), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN'],
    ['SQL', () => executeDatabaseSQL({ ...connection, sql: 'UPDATE sample SET n=n+1' }), 'DATABASE_QUERY_OUTCOME_UNKNOWN'],
    ['insert', () => insertDatabaseRow(row), 'DATABASE_INSERT_OUTCOME_UNKNOWN'],
    ['update', () => updateDatabaseRow(row), 'DATABASE_ROW_WRITE_OUTCOME_UNKNOWN'],
    ['delete', () => deleteDatabaseRow(row), 'DATABASE_ROW_WRITE_OUTCOME_UNKNOWN'],
  ] as const)('%s 的 503 operation_busy 仍是结果未知，不泄露诊断或重放', async (_, request, code) => {
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => busy())
    vi.stubGlobal('fetch', fetcher)
    const error: unknown = await request().catch((value: unknown) => value)
    expect(error).toMatchObject({ code, requestId: undefined })
    expect(String(error)).toContain('未确认')
    expect(String(error)).not.toMatch(/secret|operation_busy/)
    expect(JSON.stringify(error)).not.toContain('secret')
    if (code === 'DATABASE_QUERY_OUTCOME_UNKNOWN') expect(error).toMatchObject({ diagnosticCode: 'operation_busy' })
    else expect(error).not.toHaveProperty('diagnosticCode')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
})
