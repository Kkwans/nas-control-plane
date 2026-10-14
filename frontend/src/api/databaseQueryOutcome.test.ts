import { afterEach, describe, expect, it, vi } from 'vitest'
import { executeDatabaseSQL, type DatabaseQueryResultMode, type QueryResult } from './database'
import {
  DatabaseQueryOutcomeUnknownError, DatabaseQueryPreDispatchRejectedError,
  decodeDatabaseQueryResult, isDatabaseQueryOutcomeUnknownError, isDatabaseQueryPreDispatchRejectedError,
} from './databaseQueryOutcome'
import { NcpApiError } from './systemTransport'
import wireFixtures from './fixtures/databaseQueryOutcome.wire.json'

const input = {
  sourceId: 'source-secret', sql: 'UPDATE sample SET n=n+1 WHERE token=\'sql-secret\'',
  credentials: { password: 'credential-secret' }, resultMode: 'exec' as const,
}
const successfulResult: QueryResult = {
  columns: ['id', 'amount'], rows: [['9007199254740993', '1.234567890123456789002500']],
  rowsAffected: 0, rowsAffectedKnown: false, truncated: false, durationMs: 1,
}
const rejections = [
  [503, 'AUTH_UNAVAILABLE'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'],
  [400, 'DATABASE_INPUT_INVALID'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'],
] as const
const envelope = (code: string) => ({ code, message: 'response-diagnostic-secret', requestId: 'request-secret' })

function response(payload: unknown = successfulResult, status = 200) {
  return new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}
function useResponse(value: Response) {
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(value)
  vi.stubGlobal('fetch', fetcher)
  return fetcher
}
async function expectUnknown(request: Promise<unknown>, diagnosticCode?: string) {
  const error: unknown = await request.catch((error: unknown) => error)
  expect(isDatabaseQueryOutcomeUnknownError(error)).toBe(true)
  expect(isDatabaseQueryPreDispatchRejectedError(error)).toBe(false)
  expect(error).toBeInstanceOf(DatabaseQueryOutcomeUnknownError)
  expect(error).toMatchObject({ code: 'DATABASE_QUERY_OUTCOME_UNKNOWN', diagnosticCode, requestId: undefined })
  expect((error as Error).message).toBe('SQL 执行结果未确认，可能部分或全部生效。请先只读核对，再次执行可能重复生效。')
  expect(error).not.toHaveProperty('cause')
  expect(JSON.stringify(error)).not.toMatch(/secret|UPDATE sample|password/)
  expect(String(error)).not.toMatch(/secret|UPDATE sample|password/)
  expect(Object.getOwnPropertyNames(error).map((key) => String((error as unknown as Record<string, unknown>)[key])).join(' ')).not.toMatch(/secret|UPDATE sample|password/)
  return error
}

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('SQL 响应解码与旧 Go wire 兼容', () => {
  it.each(wireFixtures)('真实 codec HTTP 样例 $name', async ({ http, accepted }) => {
    const fetcher = useResponse(response(http))
    if (accepted) {
      const result = await executeDatabaseSQL(input)
      expect(result).toEqual({ ...http, columns: http.columns ?? [], rows: http.rows ?? [] })
    } else {
      await expectUnknown(executeDatabaseSQL(input))
    }
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('保留所有合法标量、空/重复/特殊列名和精确文本，并复制数组', () => {
    const payload = {
      ...successfulResult,
      columns: ['', 'x', 'x', '__proto__', 'constructor', 'a.b', '  '],
      rows: [[null, true, 1.25, '9007199254740993', '1.234567890123456789002500', 'Infinity', '  文本\n']],
      ignored: { password: 'response-secret' },
    }
    const before = JSON.stringify(payload)
    const result = decodeDatabaseQueryResult(payload)
    expect(result.columns).toEqual(payload.columns)
    expect(result.rows).toEqual(payload.rows)
    expect(result).not.toHaveProperty('ignored')
    expect(result.columns).not.toBe(payload.columns)
    expect(result.rows).not.toBe(payload.rows)
    expect(result.rows[0]).not.toBe(payload.rows[0])
    expect(JSON.stringify(payload)).toBe(before)
  })

  it.each([
    { columns: null, rows: null }, { columns: [], rows: [] }, { columns: null, rows: [[]] },
    { columns: ['id'], rows: null }, { columns: [], rows: [[]] },
  ])('顶层 nil 切片仅归一为空数组，保留合法零宽行 %j', async (overrides) => {
    useResponse(response({ ...successfulResult, ...overrides }))
    await expect(executeDatabaseSQL(input)).resolves.toMatchObject({ columns: overrides.columns ?? [], rows: overrides.rows ?? [] })
  })

  it.each([null, [], [{ ...successfulResult }], {}, false, 'response-secret', 7])('拒绝非法根或缺失字段 %j', async (payload) => {
    useResponse(response(payload))
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each(['columns', 'rows', 'rowsAffected', 'truncated', 'durationMs'])('必需字段 %s 不能省略', async (key) => {
    const payload: Record<string, unknown> = { ...successfulResult }
    delete payload[key]
    useResponse(response(payload))
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each([
    { columns: 'id' }, { columns: {} }, { columns: [1, 'amount'] }, { columns: [null, 'amount'] },
    { rows: {} }, { rows: 'response-secret' }, { rows: [null] }, { rows: ['not-a-row'] },
    { rows: [{ id: 1, amount: 2 }] }, { rows: [[1]] }, { rows: [[1, 2, 3]] }, { rows: [[]] },
    { rows: [[{}, 1]] }, { rows: [[[1], 2]] },
  ])('列、每行宽度和单元类型都必须合法 %j', async (overrides) => {
    useResponse(response({ ...successfulResult, ...overrides }))
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each([NaN, Infinity, -Infinity, undefined, BigInt(1), Symbol('value'), () => 1])('拒绝非 JSON/非有限单元 %s', async (value) => {
    const result = response()
    vi.spyOn(result, 'json').mockResolvedValue({ ...successfulResult, rows: [[value, 1]] })
    useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
  })

  it('稀疏数组不绕过列或单元校验', () => {
    expect(() => decodeDatabaseQueryResult({ ...successfulResult, columns: new Array(2) })).toThrow(DatabaseQueryOutcomeUnknownError)
    expect(() => decodeDatabaseQueryResult({ ...successfulResult, rows: [new Array(2)] })).toThrow(DatabaseQueryOutcomeUnknownError)
    expect(() => decodeDatabaseQueryResult({ ...successfulResult, rows: new Array(1) })).toThrow(DatabaseQueryOutcomeUnknownError)
  })

  it.each([undefined, null, false])('旧 known=%s 一律未知，并保留有符号及 unsafe 整数计数', async (rowsAffectedKnown) => {
    for (const rowsAffected of [-Number.MAX_SAFE_INTEGER - 1, -1, 0, 1, Number.MAX_SAFE_INTEGER + 1]) {
      useResponse(response({ ...successfulResult, rowsAffectedKnown, rowsAffected }))
      await expect(executeDatabaseSQL(input)).resolves.toMatchObject({ rowsAffected, rowsAffectedKnown: false })
    }
  })

  it.each([0, 1, Number.MAX_SAFE_INTEGER])('known=true 接受非负安全整数 %s', async (rowsAffected) => {
    useResponse(response({ ...successfulResult, rowsAffectedKnown: true, rowsAffected }))
    await expect(executeDatabaseSQL(input)).resolves.toMatchObject({ rowsAffected, rowsAffectedKnown: true })
  })

  it.each([-1, 1.5, Number.MAX_SAFE_INTEGER + 1, null, '1', false, NaN, Infinity, -Infinity])('known=true 拒绝无效计数 %s', async (rowsAffected) => {
    const result = response()
    vi.spyOn(result, 'json').mockResolvedValue({ ...successfulResult, rowsAffectedKnown: true, rowsAffected })
    useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each([1.5, null, '1', false, NaN, Infinity, -Infinity])('known=false 仍拒绝非有限整数计数 %s', async (rowsAffected) => {
    const result = response()
    vi.spyOn(result, 'json').mockResolvedValue({ ...successfulResult, rowsAffected })
    useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each(['true', 'false', 0, 1, '', [], {}])('known=%j 不能被当成布尔值', async (rowsAffectedKnown) => {
    useResponse(response({ ...successfulResult, rowsAffectedKnown }))
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each([undefined, null, '', 'false', 0, 1])('truncated=%j 必须是布尔值', async (truncated) => {
    useResponse(response({ ...successfulResult, truncated }))
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each([-1, 1.5, null, '1', false, NaN, Infinity, -Infinity])('拒绝非法 durationMs=%s', async (durationMs) => {
    const result = response()
    vi.spyOn(result, 'json').mockResolvedValue({ ...successfulResult, durationMs })
    useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each([0, 1, Number.MAX_SAFE_INTEGER + 1])('durationMs=%s 仅要求非负有限整数', async (durationMs) => {
    useResponse(response({ ...successfulResult, durationMs }))
    await expect(executeDatabaseSQL(input)).resolves.toMatchObject({ durationMs })
  })

  it.each([undefined, null, '', 'row_limit', 'byte_limit'])('兼容 reason=%s，不影响写入计数可信度', async (truncationReason) => {
    useResponse(response({ ...successfulResult, truncated: true, truncationReason, rows: [] }))
    const result = await executeDatabaseSQL(input)
    expect(result.rows).toEqual([])
    expect(result.truncated).toBe(true)
    expect(result.rowsAffectedKnown).toBe(false)
    expect(result.truncationReason).toBe(truncationReason || undefined)
  })

  it.each(['ROW_LIMIT', ' row_limit ', ' ', 'new_limit', false, 1, [], {}])('非空不支持的 reason=%j 不 trim、不猜测', async (truncationReason) => {
    useResponse(response({ ...successfulResult, truncated: true, truncationReason }))
    await expectUnknown(executeDatabaseSQL(input))
  })
})

describe('真实 SQL API 的单次发送边界', () => {
  it.each([undefined, 'auto', 'rows', 'exec'] as const)('保留原始 SQL、凭据和模式 %s，仅派发一次', async (resultMode) => {
    const payload = { ...input, sql: ' \n/* 精确原文 */ UPDATE sample SET n=n+1;\n', resultMode }
    const fetcher = useResponse(response())
    const controller = new AbortController()
    await expect(executeDatabaseSQL(payload, controller.signal)).resolves.toEqual(successfulResult)
    expect(fetcher).toHaveBeenCalledTimes(1)
    const [path, options] = fetcher.mock.calls[0]!
    expect(path).toBe('/api/v1/databases/query')
    expect(options).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error', body: JSON.stringify(payload), signal: controller.signal })
    expect(Object.fromEntries(new Headers(options!.headers).entries())).toEqual({ accept: 'application/json', 'content-type': 'application/json' })
  })

  it.each([201, 202, 203, 206, 400, 401, 403, 404, 409, 422, 500, 503, 504])('HTTP %s 不能靠成功形状冒充执行确认', async (status) => {
    const fetcher = useResponse(response(successfulResult, status))
    await expectUnknown(executeDatabaseSQL(input))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('204 空响应不能确认 SQL 完成', async () => {
    const fetcher = useResponse(new Response(null, { status: 204 }))
    await expectUnknown(executeDatabaseSQL(input))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each([200, 400, 500])('HTTP %s 坏 JSON 不保留响应或底层错误', async (status) => {
    const fetcher = useResponse(new Response('{response-secret', { status }))
    await expectUnknown(executeDatabaseSQL(input))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each([true, undefined, null])('redirected=%s 不能通过成功或拒绝结果确认', async (redirected) => {
    for (const status of [200, 400]) {
      const result = response(status === 200 ? successfulResult : envelope('DATABASE_INPUT_INVALID'), status)
      Object.defineProperty(result, 'redirected', { value: redirected })
      const json = vi.spyOn(result, 'json')
      useResponse(result)
      await expectUnknown(executeDatabaseSQL(input))
      expect(json).not.toHaveBeenCalled()
    }
  })

  it.each([301, 302, 303, 307, 308])('HTTP %s 重定向不跟随、不重放、不降级模式', async (status) => {
    const fetcher = useResponse(new Response(null, { status, headers: { Location: '/not-followed' } }))
    await expectUnknown(executeDatabaseSQL(input))
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0]![1]!.redirect).toBe('error')
  })

  it.each(rejections)('只按准确 %s / %s + 合法信封认定执行前拒绝', async (status, code) => {
    const fetcher = useResponse(response({ ...envelope(code), requestId: 'req-fixture-42' }, status))
    const error: unknown = await executeDatabaseSQL(input).catch((error: unknown) => error)
    expect(error).toBeInstanceOf(DatabaseQueryPreDispatchRejectedError)
    expect(isDatabaseQueryPreDispatchRejectedError(error)).toBe(true)
    expect(isDatabaseQueryOutcomeUnknownError(error)).toBe(false)
    expect(error).toMatchObject({ code, requestId: 'req-fixture-42' })
    expect(String(error)).not.toContain('secret')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each(rejections)('白名单码 %s / %s 在错误 HTTP 状态时仍未知', async (status, code) => {
    for (const wrongStatus of [200, 201, 500, status === 400 ? 403 : 400]) {
      useResponse(response(envelope(code), wrongStatus))
      await expectUnknown(executeDatabaseSQL(input), wrongStatus === 200 ? undefined : code)
    }
  })

  it.each([
    { code: 'DATABASE_INPUT_INVALID' }, { code: 'DATABASE_INPUT_INVALID', message: 'response-secret' },
    { code: 'DATABASE_INPUT_INVALID', message: 7, requestId: 'request-secret' },
    { code: 'DATABASE_INPUT_INVALID', message: 'response-secret', requestId: null },
    [envelope('DATABASE_INPUT_INVALID')],
  ])('伪造/残缺信封 %j 不能证明执行前拒绝', async (payload) => {
    useResponse(response(payload, 400))
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each(['code', 'message', 'requestId'])('继承的信封字段 %s 不能冒充有效响应成员', async (key) => {
    const complete = envelope('DATABASE_INPUT_INVALID')
    const payload: Record<string, unknown> = { ...complete }
    delete payload[key]
    Object.setPrototypeOf(payload, { [key]: complete[key as keyof typeof complete] })
    const result = response(undefined, 400)
    vi.spyOn(result, 'json').mockResolvedValue(payload)
    const fetcher = useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each(['', 'raw request-secret text', 'x'.repeat(129), '诊断信息', 'line\nsecret'])('前置拒绝丢弃非法支持编号 %j', async (requestId) => {
    useResponse(response({ ...envelope('DATABASE_INPUT_INVALID'), requestId }, 400))
    const error: unknown = await executeDatabaseSQL(input).catch((error: unknown) => error)
    expect(isDatabaseQueryPreDispatchRejectedError(error)).toBe(true)
    expect(error).toMatchObject({ requestId: undefined })
    expect(JSON.stringify(error)).not.toContain('secret')
  })

  it.each([
    [400, 'sql_invalid'], [409, 'constraint_failed'], [403, 'permission_denied'], [504, 'timeout'],
    [503, 'agent_unavailable'], [422, 'result_too_large'], [400, 'DATABASE_OPERATION_FAILED'],
    [400, 'credentials_required'], [401, 'auth_failed'], [503, 'unreachable'], [404, 'database_not_found'],
    [503, 'credential_store_unavailable'], [503, 'credential_corrupt'], [503, 'key_unavailable'],
    [503, 'key_rotation_failed'], [503, 'migration_failed'], [409, 'credential_conflict'],
    [400, 'tls_configuration_invalid'], [400, 'tls_policy_unsupported'], [400, 'tls_verification_failed'],
  ] as const)('%s / %s 仅保留安全诊断，不能证明 SQL 未执行', async (status, code) => {
    const fetcher = useResponse(response(envelope(code), status))
    await expectUnknown(executeDatabaseSQL(input), code)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each([undefined, 'auto', 'rows', 'exec', 'bogus'] as const)('mode=%s 的 result_mode_unsupported 保守未知，不移除模式重试', async (resultMode) => {
    const fetcher = useResponse(response(envelope('result_mode_unsupported'), 400))
    await expectUnknown(executeDatabaseSQL({ ...input, resultMode: resultMode as DatabaseQueryResultMode }), 'result_mode_unsupported')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each(['response-secret', 'ROUTE_NOT_FOUND', 'toString', '__proto__', 'constructor'])('不支持的诊断码 %s 不进入错误对象', async (code) => {
    useResponse(response(envelope(code), 400))
    await expectUnknown(executeDatabaseSQL(input))
  })

  it.each([
    new TypeError('credential-secret'), new DOMException('sql-secret', 'AbortError'),
    new NcpApiError('DATABASE_INPUT_INVALID', 'response-secret'),
    new DatabaseQueryPreDispatchRejectedError('DATABASE_INPUT_INVALID'),
  ])('fetch 拒绝时的异常类型不能冒充前置拒绝', async (cause) => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(cause)
    vi.stubGlobal('fetch', fetcher)
    await expectUnknown(executeDatabaseSQL(input))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('fetch 同步抛错仍视作已跨发送边界，不能误标零派发', async () => {
    const fetcher = vi.fn<typeof fetch>(() => { throw new TypeError('credential-secret') })
    vi.stubGlobal('fetch', fetcher)
    await expectUnknown(executeDatabaseSQL(input))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('调用时已 aborted 也保守未知，仍不重试', async () => {
    const controller = new AbortController()
    controller.abort()
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(new DOMException('aborted-secret', 'AbortError'))
    vi.stubGlobal('fetch', fetcher)
    await expectUnknown(executeDatabaseSQL(input, controller.signal))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each(['status', 'redirected', 'json'])('响应属性 %s 读取抛错不能漏出原始异常', async (property) => {
    const result = response()
    Object.defineProperty(result, property, { get() { throw new TypeError('response-secret') } })
    useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
  })

  it('response.json 异步拒绝不保留异常或信任异常类', async () => {
    const result = response()
    vi.spyOn(result, 'json').mockRejectedValue(new DatabaseQueryPreDispatchRejectedError('DATABASE_INPUT_INVALID'))
    useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
  })

  it('成功解码期间意外异常也归为未知', async () => {
    const result = response()
    vi.spyOn(result, 'json').mockResolvedValue({ ...successfulResult, get columns() { throw new Error('response-secret') } })
    useResponse(result)
    await expectUnknown(executeDatabaseSQL(input))
  })

  it('序列化 BigInt 失败保持本地异常且零 fetch', async () => {
    const fetcher = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetcher)
    const payload = { ...input, credentials: { ...input.credentials, extra: BigInt(1) } }
    const error: unknown = await executeDatabaseSQL(payload).catch((error: unknown) => error)
    expect(error).toBeInstanceOf(TypeError)
    expect(isDatabaseQueryOutcomeUnknownError(error)).toBe(false)
    expect(isDatabaseQueryPreDispatchRejectedError(error)).toBe(false)
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('本地 toJSON 失败保留原异常，不构造 headers 或派发 fetch', async () => {
    const fetcher = vi.fn<typeof fetch>()
    const headerConstructor = vi.fn<typeof Headers>()
    vi.stubGlobal('fetch', fetcher)
    vi.stubGlobal('Headers', headerConstructor)
    const local = new TypeError('local-secret')
    const payload = { ...input, toJSON() { throw local } }
    await expect(executeDatabaseSQL(payload)).rejects.toBe(local)
    expect(fetcher).not.toHaveBeenCalled()
    expect(headerConstructor).not.toHaveBeenCalled()
  })

  it('本地 Headers 构造失败保留原异常且零 fetch', async () => {
    const fetcher = vi.fn<typeof fetch>()
    const local = new TypeError('local-secret')
    vi.stubGlobal('fetch', fetcher)
    vi.stubGlobal('Headers', class { constructor() { throw local } })
    await expect(executeDatabaseSQL(input)).rejects.toBe(local)
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('只有受控错误实例通过 guard，单凭 code 或对象外形不能证明状态', () => {
    for (const error of [null, undefined, new Error('local'), new NcpApiError('DATABASE_INPUT_INVALID', 'invalid'), { code: 'DATABASE_QUERY_OUTCOME_UNKNOWN' }]) {
      expect(isDatabaseQueryOutcomeUnknownError(error)).toBe(false)
      expect(isDatabaseQueryPreDispatchRejectedError(error)).toBe(false)
    }
    expect(new DatabaseQueryOutcomeUnknownError('arbitrary-secret').diagnosticCode).toBeUndefined()
  })
})
