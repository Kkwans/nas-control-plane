import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  connectDatabase, discoverDatabases, loadDatabaseCatalog, loadDatabaseRows, testDatabaseConnection,
} from './database'
import { NcpApiError } from './systemTransport'

const connection = { sourceId: 'fixture', credentials: { username: 'fixture-user' } }
const requests = [
  { name: 'discovery', path: '/api/v1/databases/discovery?refresh=true', method: 'GET', body: undefined, run: (signal?: AbortSignal) => discoverDatabases(true, signal) },
  { name: 'catalog', path: '/api/v1/databases/catalog', method: 'POST', body: connection, run: (signal?: AbortSignal) => loadDatabaseCatalog(connection, signal) },
  { name: 'rows', path: '/api/v1/databases/rows', method: 'POST', body: { ...connection, table: 'sample' }, run: (signal?: AbortSignal) => loadDatabaseRows({ ...connection, table: 'sample' }, signal) },
  { name: 'connect', path: '/api/v1/databases/connect', method: 'POST', body: connection, run: (signal?: AbortSignal) => connectDatabase(connection, signal) },
  { name: 'test-connection', path: '/api/v1/databases/test-connection', method: 'POST', body: connection, run: (signal?: AbortSignal) => testDatabaseConnection(connection, signal) },
]
const envelope = { code: 'unreachable', message: 'private-read-error', requestId: 'private-request-id' }
const invalidFields: unknown[] = [undefined, null, false, true, 0, 17, {}, [], ['private-read-error'], { toString: null }, { password: 'private-read-error' }]
const malformedEnvelopes = [
  null, false, 17, 'private-read-error', [], {},
  ...(['code', 'message', 'requestId'] as const).flatMap((field) => invalidFields.map((value) => ({ ...envelope, [field]: value }))),
]

function response(payload: unknown, status = 400) {
  return new Response(JSON.stringify(payload), { status })
}

function stubResponse(value: Response) {
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(value)
  vi.stubGlobal('fetch', fetcher)
  return fetcher
}

function expectSafeFallback(error: unknown, code = 'DATABASE_OPERATION_FAILED', message = '数据库操作失败。') {
  expect(error).toBeInstanceOf(NcpApiError)
  expect(error).toMatchObject({ code, message, requestId: undefined })
  expect(error).not.toHaveProperty('cause')
  expect(JSON.stringify(error)).not.toContain('private-')
  expect(String(error)).not.toContain('private-')
  // 页面把 code 作为文案表键；无效对象不能到达属性键转换。
  expect(() => ({ [(error as NcpApiError).code]: true })).not.toThrow()
}

afterEach(() => vi.unstubAllGlobals())

describe.each(requests)('数据库通用 $name 错误响应边界', ({ path, method, body, run }) => {
  it.each(malformedEnvelopes)('拒绝畸形 HTTP 错误信封 %j，丢弃整段诊断且不重放', async (payload) => {
    const fetcher = stubResponse(response(payload))
    const error = await run().catch((error: unknown) => error)
    expectSafeFallback(error)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each(['code', 'message', 'requestId'] as const)('不把继承的 %s 作为完整错误信封', async (field) => {
    const payload = { ...envelope }
    const value = payload[field]
    Reflect.deleteProperty(payload, field)
    Object.setPrototypeOf(payload, { [field]: value })
    const valueResponse = response(null)
    vi.spyOn(valueResponse, 'json').mockResolvedValue(payload)
    const fetcher = stubResponse(valueResponse)
    expectSafeFallback(await run().catch((error: unknown) => error))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('读取错误正文抛出的异常也不能作为可信 API 错误透传', async () => {
    const valueResponse = response(null)
    vi.spyOn(valueResponse, 'json').mockRejectedValue(new NcpApiError('private-code', 'private-read-error', 'private-request-id'))
    stubResponse(valueResponse)
    expectSafeFallback(await run().catch((error: unknown) => error))
  })

  it.each([
    { code: 'auth_failed', message: '数据库认证失败。', requestId: 'fixture-auth' },
    { code: 'result_too_large', message: '结果超过预算', requestId: 'fixture-budget' },
    { code: 'future_read_error', message: '后续版本的服务端说明', requestId: 'fixture-future' },
    { code: 'unreachable', message: '数据库服务不可达。', requestId: '' },
  ])('保留完整字符串信封 %j，忽略未知额外字段', async (payload) => {
    const fetcher = stubResponse(response({ ...payload, details: { password: 'private-read-error' } }, 422))
    const controller = new AbortController()
    const error = await run(controller.signal).catch((error: unknown) => error)
    expect(error).toBeInstanceOf(NcpApiError)
    expect(error).toMatchObject(payload)
    expect(error).not.toHaveProperty('cause')
    expect(JSON.stringify(error)).not.toContain('private-')
    expect(fetcher).toHaveBeenCalledExactlyOnceWith(path, {
      method, credentials: 'same-origin', signal: controller.signal,
      headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  })

  it('空字符串沿用既有错误码和消息回退，不擅自改写支持编号', async () => {
    stubResponse(response({ code: '', message: '', requestId: '' }))
    await expect(run()).rejects.toMatchObject({ code: 'DATABASE_OPERATION_FAILED', message: '数据库操作失败。', requestId: '' })
  })

  it.each([400, 502])('HTTP %s 的非 JSON 正文不进入错误对象', async (status) => {
    const fetcher = stubResponse(new Response('<html>private-proxy-error</html>', { status }))
    expectSafeFallback(await run().catch((error: unknown) => error))
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('坏成功 JSON 保持读取响应错误，不变成操作失败', async () => {
    const fetcher = stubResponse(new Response('private-read-error: invalid JSON', { status: 200 }))
    expectSafeFallback(await run().catch((error: unknown) => error), 'DATABASE_RESPONSE_INVALID', '数据库服务返回了无法识别的数据。')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it.each([new DOMException('Canceled', 'AbortError'), new TypeError('Network unavailable')])('保留 fetch 拒绝对象 %s，不改写为写入结果或自动重试', async (error) => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(error)
    vi.stubGlobal('fetch', fetcher)
    await expect(run()).rejects.toBe(error)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
})
