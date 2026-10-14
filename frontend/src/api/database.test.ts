import { afterEach, describe, expect, it, vi } from 'vitest'
import { executeDatabaseSQL, loadDatabaseRows, type QueryResult } from './database'
import { DatabaseQueryOutcomeUnknownError } from './databaseQueryOutcome'

afterEach(() => vi.unstubAllGlobals())

describe('数据库结果预算 API 兼容', () => {
  it.each(['row_limit', 'byte_limit', undefined] as const)('原样保留可选截断原因 %s 和精确文本', async (truncationReason) => {
    const result: QueryResult = {
      columns: ['id', 'amount'], rows: [['9007199254740993', '1.234567890123456789002500']],
      rowsAffected: 0, rowsAffectedKnown: false, truncated: true, truncationReason, durationMs: 1,
    }
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(result), { status: 200 }))
    vi.stubGlobal('fetch', fetcher)
    const response = await executeDatabaseSQL({ sourceId: 'fixture', sql: 'SELECT id, amount FROM sample' })
    expect(response).toEqual(JSON.parse(JSON.stringify(result)))
    expect(response.rows[0]).toEqual(['9007199254740993', '1.234567890123456789002500'])
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('接受首行超预算的空前缀与结果列', async () => {
    const result: QueryResult = {
      columns: ['large_value'], rows: [], rowsAffected: 0, rowsAffectedKnown: false,
      truncated: true, truncationReason: 'byte_limit', durationMs: 1,
    }
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(result), { status: 200 })))
    await expect(executeDatabaseSQL({ sourceId: 'fixture', sql: 'SELECT large_value FROM sample' })).resolves.toEqual(result)
  })

  it.each(['query', 'rows'] as const)('%s 的 HTTP 422 保留安全诊断，不自动重放请求', async (kind) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      code: 'result_too_large', message: '结果超过预算', requestId: 'fixture-budget',
    }), { status: 422 }))
    vi.stubGlobal('fetch', fetcher)
    const request = kind === 'query'
      ? executeDatabaseSQL({ sourceId: 'fixture', sql: 'UPDATE sample SET value = value RETURNING value', resultMode: 'rows' })
      : loadDatabaseRows({ sourceId: 'fixture', table: 'sample', limit: 25, offset: 25 })
    if (kind === 'query') {
      const error = await request.catch((error: unknown) => error)
      expect(error).toBeInstanceOf(DatabaseQueryOutcomeUnknownError)
      expect(error).toMatchObject({ code: 'DATABASE_QUERY_OUTCOME_UNKNOWN', diagnosticCode: 'result_too_large', requestId: undefined })
    } else {
      await expect(request).rejects.toMatchObject({ code: 'result_too_large', requestId: 'fixture-budget' })
    }
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
})
