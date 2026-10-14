import { afterEach, describe, expect, it, vi } from 'vitest'
import { updateDatabaseRow, deleteDatabaseRow, type DatabaseValue } from './database'
import { DatabaseRowWriteOutcomeUnknownError } from './databaseRowWriteOutcome'
import { NcpApiError } from './systemTransport'

function response(payload: unknown, status = 200) {
  return new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}
function useResponse(value: Response) {
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(value)
  vi.stubGlobal('fetch', fetcher)
  return fetcher
}
const rejections = [
  [503, 'AUTH_UNAVAILABLE'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'],
  [400, 'DATABASE_INPUT_INVALID'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'],
] as const
const envelope = (code: string) => ({ code, message: 'raw-diagnostic-secret', requestId: 'req-fixture-42' })

afterEach(() => vi.unstubAllGlobals())

describe.each(['update', 'delete'] as const)('数据库 %s 发送边界', (operation) => {
  const input = { sourceId: 'source-a', schema: 'public', table: 'sample', keys: { token: 'key-secret' }, credentials: { password: 'credential-secret' }, ...(operation === 'update' ? { values: { label: 'draft-secret' } } : {}) }
  function write(payload: Parameters<typeof deleteDatabaseRow>[0] & { values?: Record<string, DatabaseValue> } = input, signal?: AbortSignal) {
    return operation === 'update' ? updateDatabaseRow(payload as Parameters<typeof updateDatabaseRow>[0], signal) : deleteDatabaseRow(payload, signal)
  }
  it.each([0, 1, Number.MAX_SAFE_INTEGER])('仅接受 200 中合法的影响行数 %s，零不改写语义', async (rowsAffected) => {
    const fetcher = useResponse(response({ rowsAffected, ignored: 'raw-response-secret' }))
    const controller = new AbortController()
    await expect(write(input, controller.signal)).resolves.toEqual({ rowsAffected })
    expect(fetcher).toHaveBeenCalledExactlyOnceWith(`/api/v1/databases/rows/${operation}`, {
      method: 'POST', credentials: 'same-origin', redirect: 'error',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify(input), signal: controller.signal,
    })
  })

  it.each([null, {}, [], [{ rowsAffected: 1 }], true, 1, 'ok', { rowsAffected: '1' }, { rowsAffected: null }, { rowsAffected: -1 }, { rowsAffected: 1.5 }, { rowsAffected: Number.MAX_SAFE_INTEGER + 1 }])('不完整或非法成功载荷 %j 归为未知', async (payload) => {
    const fetcher = useResponse(response(payload))
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each([201, 202, 400, 500])('HTTP %s 不能冒充 UPDATE/DELETE 确认', async (status) => {
    useResponse(response({ rowsAffected: 1 }, status))
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it.each([200, 400])('HTTP %s 坏 JSON 不会重试', async (status) => {
    const fetcher = useResponse(new Response('{broken raw-response-secret', { status }))
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('HTTP 204 空响应不是成功确认', async () => {
    useResponse(new Response(null, { status: 204 }))
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it.each([200, 400])('重定向响应 %s 即使有合法载荷也归为未知', async (status) => {
    const result = response(status === 200 ? { rowsAffected: 1 } : envelope('DATABASE_INPUT_INVALID'), status)
    Object.defineProperty(result, 'redirected', { value: true })
    useResponse(result)
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it.each(rejections)('只接受派发前拒绝 %s / %s 的准确组合，保留请求 ID 并丢弃原始诊断', async (status, code) => {
    useResponse(response(envelope(code), status))
    const error = await write(input).catch((error: unknown) => error)
    expect(error).toBeInstanceOf(NcpApiError)
    expect(error).not.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(error).toMatchObject({ code, requestId: 'req-fixture-42' })
    expect(String(error)).not.toContain('secret')
  })

  it.each(['raw diagnostic text with password=secret', 'x'.repeat(129), '', '诊断信息', 'line\nsecret'])('已知拒绝丢弃非法支持编号 %j，不改变派发前拒绝结论', async (requestId) => {
    useResponse(response({ ...envelope('DATABASE_INPUT_INVALID'), requestId }, 400))
    const error = await write(input).catch((error: unknown) => error)
    expect(error).toBeInstanceOf(NcpApiError)
    expect(error).not.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(error).toMatchObject({ code: 'DATABASE_INPUT_INVALID', requestId: undefined })
    expect(JSON.stringify(error)).not.toContain('secret')
  })

  it.each(rejections)('白名单码 %s / %s 搭配错误状态不得当作未派发', async (status, code) => {
    useResponse(response(envelope(code), status === 400 ? 403 : 400))
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it.each([
    { code: 'DATABASE_INPUT_INVALID' },
    { code: 'DATABASE_INPUT_INVALID', message: 'invalid' },
    { code: 'DATABASE_INPUT_INVALID', message: 7, requestId: 'id' },
    { code: 'DATABASE_INPUT_INVALID', message: 'invalid', requestId: null },
    [envelope('DATABASE_INPUT_INVALID')],
  ])('拒绝信封 %j 未通过结构验证则保持未知', async (payload) => {
    useResponse(response(payload, 400))
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it.each(['constraint_failed', 'sql_invalid', 'timeout', 'agent_unavailable', 'auth_failed', 'DATABASE_OPERATION_FAILED', 'ROUTE_NOT_FOUND', 'credentials_required', 'credential_store_unavailable', 'credential_corrupt', 'key_unavailable', 'toString', '__proto__'])('通用/Agent 错误 %s 不证明没有写入', async (code) => {
    useResponse(response(envelope(code), 400))
    await expect(write(input)).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it.each([new TypeError('credential-secret'), new DOMException('raw-diagnostic-secret', 'AbortError'), new NcpApiError('DATABASE_INPUT_INVALID', 'raw-diagnostic-secret')])('发送后的异常只保留固定安全错误', async (cause) => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(cause)
    vi.stubGlobal('fetch', fetcher)
    const error = await write(input).catch((error: unknown) => error)
    expect(error).toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(error).toMatchObject({ code: 'DATABASE_ROW_WRITE_OUTCOME_UNKNOWN' })
    expect(JSON.stringify(error)).not.toMatch(/secret|source-a|sample/)
    expect(String(error)).not.toContain('secret')
    expect((error as Error).cause).toBeUndefined()
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('序列化失败发生在 fetch 前，不改写为发送后结果未知', async () => {
    const fetcher = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetcher)
    const error = await write({ ...input, keys: { id: BigInt(1) as unknown as DatabaseValue } }).catch((error: unknown) => error)
    expect(error).toBeInstanceOf(TypeError)
    expect(error).not.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each([undefined, null])('redirected=%s 未明确为 false 则不能确认', async (redirected) => {
    const result = response({ rowsAffected: 1 })
    Object.defineProperty(result, 'redirected', { value: redirected })
    useResponse(result)
    await expect(write()).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it.each([301, 302, 303, 307, 308])('HTTP %s 重定向不自动跟随或重放', async (status) => {
    const fetcher = useResponse(new Response(null, { status, headers: { Location: '/not-followed' } }))
    await expect(write()).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0]![1]!.redirect).toBe('error')
  })

  it.each([NaN, Infinity, -Infinity])('非有限数字 %s 不能构成成功确认', async (rowsAffected) => {
    const result = response({ rowsAffected: 1 })
    vi.spyOn(result, 'json').mockResolvedValue({ rowsAffected })
    useResponse(result)
    await expect(write()).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
  })

  it('fetch 调用时同步抛出的异常仍然不能证明未派发', async () => {
    const fetcher = vi.fn<typeof fetch>(() => { throw new TypeError('credential-secret') })
    vi.stubGlobal('fetch', fetcher)
    await expect(write()).rejects.toBeInstanceOf(DatabaseRowWriteOutcomeUnknownError)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('本地 toJSON 失败保持零派发且不保存诊断', async () => {
    const fetcher = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetcher)
    const credentials = { password: 'fixture-only', toJSON() { throw new TypeError('local-secret') } }
    await expect(write({ ...input, credentials })).rejects.toThrow('local-secret')
    expect(fetcher).not.toHaveBeenCalled()
  })

})
