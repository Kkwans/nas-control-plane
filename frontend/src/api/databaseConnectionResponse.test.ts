import { afterEach, describe, expect, it, vi } from 'vitest'
import { connectDatabase, testDatabaseConnection, type DatabaseConnection } from './database'
import { DatabaseConnectionOutcomeUnknownError, decodeDatabaseConnectionDiagnostic } from './databaseConnectionResponse'
import { NcpApiError } from './systemTransport'
import wireFixtures from './fixtures/databaseConnectionResponse.wire.json'

const diagnostic = { connected: true, driver: 'mysql', endpoint: 'fixture:3306/catalog', operation: 'test_connection', durationMs: 0 }
const connection = { sourceId: 'fixture', credentials: { username: 'fixture-user', password: 'private-fixture' } }
const unknownMessage = '连接结果尚未确认，服务端可能已保存本次凭据。请核对后再决定是否重新连接。'
const conflictMessage = '其他操作已修改保存的凭据，本次连接未保存凭据。请重新确认后再连接。'
const fields = ['connected', 'driver', 'endpoint', 'operation', 'durationMs'] as const
const invalidFields: unknown[] = [undefined, null, {}, [], ['private-fixture'], { password: 'private-fixture' }]
const malformed = [null, false, true, 1, 'private-fixture', [], {},
  ...fields.flatMap(field => invalidFields.map(value => ({ ...diagnostic, [field]: value }))),
  ...['false', 'true', 0, 1].map(connected => ({ ...diagnostic, connected })),
  ...['', 'future-driver', 'MYSQL', 1].map(driver => ({ ...diagnostic, driver })),
  ...[-1, 0.5, '1', true].map(durationMs => ({ ...diagnostic, durationMs })),
  ...['endpoint', 'operation'].flatMap(field => [true, 1].map(value => ({ ...diagnostic, [field]: value }))),
  ...['code', 'database'].flatMap(field => [true, 1, {}, [], { password: 'private-fixture' }].map(value => ({ ...diagnostic, [field]: value }))),
  { ...diagnostic, code: 'auth_failed' }, { ...diagnostic, code: 'future_outcome' },
  { ...diagnostic, connected: false, code: 'connected' }, { ...diagnostic, connected: false, code: 'ok' },
]
function response(payload: unknown, status = 200) { return new Response(JSON.stringify(payload), { status }) }
function stub(value: Response) { const fetcher = vi.fn<typeof fetch>().mockResolvedValue(value); vi.stubGlobal('fetch', fetcher); return fetcher }
function expectSafe(error: unknown, code: string, message?: string) {
  expect(error).toBeInstanceOf(NcpApiError)
  expect(error).toMatchObject({ code, ...(message ? { message } : {}) })
  expect(error).not.toHaveProperty('cause')
  expect(JSON.stringify(error)).not.toContain('private-')
  expect(String(error)).not.toContain('private-')
}
afterEach(() => vi.unstubAllGlobals())

describe('数据库连接诊断解码', () => {
  it('兼容真实 Go codec 的初始/当前字段与可选零值', () => {
    for (const fixture of Object.values(wireFixtures)) {
      expect(decodeDatabaseConnectionDiagnostic(fixture.http)).toEqual(decodeDatabaseConnectionDiagnostic(fixture.wire))
    }
  })
  it.each(malformed)('拒绝畸形或矛盾诊断 %j', (payload) => {
    expectSafe((() => { try { decodeDatabaseConnectionDiagnostic(payload) } catch (error) { return error } })(), 'DATABASE_RESPONSE_INVALID')
  })
  it.each([NaN, Infinity, -Infinity])('拒绝非有限耗时 %s', (durationMs) => {
    expect(() => decodeDatabaseConnectionDiagnostic({ ...diagnostic, durationMs })).toThrow(NcpApiError)
  })
  it.each(fields)('不接受继承的必填字段 %s', (field) => {
    const payload = { ...diagnostic }; const value = payload[field]; Reflect.deleteProperty(payload, field); Object.setPrototypeOf(payload, { [field]: value })
    expect(() => decodeDatabaseConnectionDiagnostic(payload)).toThrow(NcpApiError)
  })
  it.each([undefined, null, '', 'connected', 'ok'])('兼容可选成功码 %s 与 Go 零耗时', (code) => {
    expect(decodeDatabaseConnectionDiagnostic({ ...diagnostic, code, database: null })).toEqual({ ...diagnostic, code: code ?? undefined, database: undefined })
  })
  it.each(['sqlite', 'mysql', 'postgresql'])('保留 %s、合法文本、开放操作和失败码', (driver) => {
    const payload = { ...diagnostic, connected: false, driver, endpoint: ' endpoint ', operation: 'future_operation', code: 'future_failure', database: '', extra: { password: 'private-fixture' } }
    const before = JSON.stringify(payload)
    const decoded = decodeDatabaseConnectionDiagnostic(payload)
    expect(decoded).toEqual({ connected: false, driver, endpoint: ' endpoint ', operation: 'future_operation', code: 'future_failure', database: '', durationMs: 0 })
    expect(decoded).not.toBe(payload)
    expect(JSON.stringify(payload)).toBe(before)
    expect(JSON.stringify(decoded)).not.toContain('private-')
  })
})

describe.each([
  { name: 'connect', run: connectDatabase, path: '/api/v1/databases/connect', code: 'DATABASE_CONNECTION_OUTCOME_UNKNOWN' },
  { name: 'test', run: testDatabaseConnection, path: '/api/v1/databases/test-connection', code: 'DATABASE_RESPONSE_INVALID' },
])('公开诊断 API $name', ({ name, run, path, code }) => {
  it.each(malformed)('畸形 200 不进入调用方且仅派发一次 %j', async (payload) => {
    const fetcher = stub(response(payload))
    expectSafe(await run(connection).catch(error => error), code)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('保持请求路径、凭据策略、序列化快照和 signal，只返回已验证字段', async () => {
    const fetcher = stub(response({ ...diagnostic, extra: { password: 'private-fixture' } }))
    const controller = new AbortController()
    expect(await run(connection, controller.signal)).toEqual(diagnostic)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith(path, {
      method: 'POST', credentials: 'same-origin', body: JSON.stringify(connection), signal: controller.signal,
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' }, ...(name === 'connect' ? { redirect: 'error' } : {}),
    })
  })
  it('畸形 JSON 丢弃正文', async () => {
    const fetcher = stub(new Response('private-fixture: broken JSON', { status: 200 }))
    expectSafe(await run(connection).catch(error => error), code)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
})

describe('connect 保存结果边界', () => {
  it.each([new TypeError('private-fixture network'), new DOMException('private-fixture canceled', 'AbortError')])('派发后网络/取消错误保持结果未知，不保留底层异常', async (error) => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(error); vi.stubGlobal('fetch', fetcher)
    expectSafe(await connectDatabase(connection).catch(error => error), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN', unknownMessage)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('派发前取消和序列化失败保持本地错误，零请求', async () => {
    const fetcher = vi.fn<typeof fetch>(); vi.stubGlobal('fetch', fetcher)
    const controller = new AbortController(); controller.abort()
    await expect(connectDatabase(connection, controller.signal)).rejects.toMatchObject({ name: 'AbortError' })
    const cyclic = { sourceId: 'fixture' } as DatabaseConnection; Object.assign(cyclic, { credentials: cyclic })
    await expect(connectDatabase(cyclic)).rejects.toBeInstanceOf(TypeError)
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each([201, 202, 204, 206])('不把 HTTP %s 当作已确认连接', async (status) => {
    const fetcher = stub(status === 204 ? new Response(null, { status }) : response(diagnostic, status))
    expectSafe(await connectDatabase(connection).catch(error => error), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('不接受已重定向响应', async () => {
    const value = response(diagnostic); Object.defineProperty(value, 'redirected', { value: true }); stub(value)
    await expect(connectDatabase(connection)).rejects.toBeInstanceOf(DatabaseConnectionOutcomeUnknownError)
  })
  it.each([
    [503, 'credential_store_unavailable'], [503, 'key_unavailable'], [503, 'agent_unavailable'], [504, 'timeout'],
    [400, 'future_failure'], [500, 'auth_failed'], [503, 'credentials_required'], [401, 'HTTP_UNSUPPORTED_MEDIA_TYPE'],
    [400, 'credential_conflict'], [401, 'credential_conflict'], [500, 'credential_conflict'], [503, 'credential_conflict'],
    [409, 'credentials_required'], [409, 'auth_failed'], [409, 'CREDENTIAL_CONFLICT'], [409, 'credential_conflict '], [409, 'future_failure'],
  ])('完整 HTTP %s/%s 错误也不能一概证明未保存', async (status, code) => {
    const fetcher = stub(response({ code, message: 'private-fixture', requestId: 'private-request' }, Number(status)))
    expectSafe(await connectDatabase(connection).catch(error => error), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN', unknownMessage)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it.each([
    [503, 'AUTH_UNAVAILABLE'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'],
    [400, 'DATABASE_INPUT_INVALID'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'],
    [400, 'credentials_required'], [401, 'auth_failed'], [409, 'credential_conflict'],
  ])('仅保留确证保存前拒绝的 HTTP %s/%s', async (status, code) => {
    const fetcher = stub(response({ code, message: 'private-fixture', requestId: 'fixture-request' }, Number(status)))
    const error = await connectDatabase(connection).catch(error => error)
    expectSafe(error, String(code))
    expect(error).toMatchObject({ requestId: 'fixture-request' })
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  describe.each([{ code: 'credentials_required', status: 400 }, { code: 'credential_conflict', status: 409 }])('$status/$code 错误信封', ({ code, status }) => {
    it.each(['code', 'message', 'requestId'])('拒绝非自有或非字符串字段 %s', async (field) => {
      for (const value of [undefined, null, 1, {}, []]) {
        const fetcher = stub(response({ code, message: 'private-fixture', requestId: 'private-request', [field]: value }, status))
        expectSafe(await connectDatabase(connection).catch(error => error), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN')
        expect(fetcher).toHaveBeenCalledTimes(1)
      }
      const payload = { code, message: 'private-fixture', requestId: 'private-request' }
      const value = payload[field as keyof typeof payload]; Reflect.deleteProperty(payload, field); Object.setPrototypeOf(payload, { [field]: value })
      const valueResponse = response(null, status); vi.spyOn(valueResponse, 'json').mockResolvedValue(payload); const fetcher = stub(valueResponse)
      expectSafe(await connectDatabase(connection).catch(error => error), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN')
      expect(fetcher).toHaveBeenCalledTimes(1)
    })
  })
  it('精确保存冲突只暴露固定安全文案，不保留正文细节或自动重放', async () => {
    const fetcher = stub(response({
      code: 'credential_conflict', message: 'private-fixture: password /dsn token', requestId: 'private-request?password=fixture',
      credentials: connection.credentials, cause: { password: 'private-fixture' }, diagnostic,
    }, 409))
    const error = await connectDatabase(connection).catch(error => error)
    expectSafe(error, 'credential_conflict', conflictMessage)
    expect(error.requestId).toBeUndefined()
    expect(error).not.toHaveProperty('credentials')
    expect(error).not.toHaveProperty('diagnostic')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it.each([new Response('private-fixture: invalid JSON', { status: 409 }), response({ code: 'credential_conflict', message: 'private-fixture', requestId: 'fixture-request' })])('无法验证的保存冲突仍保持结果未知', async (value) => {
    const fetcher = stub(value)
    expectSafe(await connectDatabase(connection).catch(error => error), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN', unknownMessage)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it.each([null, [], {}, 'private-fixture'])('拒绝畸形非成功错误信封 %j', async (payload) => {
    stub(response(payload, 503))
    expectSafe(await connectDatabase(connection).catch(error => error), 'DATABASE_CONNECTION_OUTCOME_UNKNOWN')
  })
})
