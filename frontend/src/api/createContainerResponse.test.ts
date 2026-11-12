import { describe, expect, it, vi } from 'vitest'
import { createDockerContainer, CreateContainerOutcomeUnknownError } from './docker'
import { CreateContainerOutcomeUnknownError as PublicOutcomeUnknownError } from './system'
import type { DockerContainerCreateInput } from './system'
import { NcpApiError } from './systemTransport'

const input = (runContainer = false): DockerContainerCreateInput => ({ image: 'redis:8-alpine', runContainer })
const result = (runContainer = false) => ({
  containerId: '  exact-container-id  ', name: '', image: 'redis:8-alpine', state: 'stopped',
  created: true, started: runContainer, runContainer,
})
const jsonResponse = (value: unknown, status = 201) => new Response(JSON.stringify(value), { status })

async function expectUnknown(response: Response, runContainer = false) {
  return expectUnknownFetch(vi.fn<typeof fetch>().mockResolvedValue(response), runContainer)
}

async function expectUnknownFetch(fetcher: ReturnType<typeof vi.fn<typeof fetch>>, runContainer = false) {
  const error = await createDockerContainer(input(runContainer), fetcher).catch((caught: unknown) => caught)
  expect(error).toBeInstanceOf(CreateContainerOutcomeUnknownError)
  expect(error).toBeInstanceOf(NcpApiError)
  expect(error).toMatchObject({ name: 'CreateContainerOutcomeUnknownError', code: 'DOCKER_CONTAINER_CREATE_OUTCOME_UNKNOWN' })
  expect(error).not.toHaveProperty('containerId')
  expect(error).not.toHaveProperty('payload')
  expect(error).not.toHaveProperty('cause')
  expect(String(error)).not.toContain('exact-container-id')
  expect(fetcher).toHaveBeenCalledOnce()
  return error
}

describe('容器创建结果的完整确认边界', () => {
  it('从公共 API 导出专用错误类型', () => {
    expect(PublicOutcomeUnknownError).toBe(CreateContainerOutcomeUnknownError)
  })

  it.each([false, true])('只接受符合提交意图的完整成功结果：runContainer=%s', async (runContainer) => {
    // 启动成功后进程可能已退出，不能以 state 强制推断 started。
    const payload = result(runContainer)
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))
    await expect(createDockerContainer(input(runContainer), fetcher)).resolves.toEqual(payload)
  })

  it.each([false, true])('跳转后的完整成功结果仍不能确认原始创建：runContainer=%s', async (runContainer) => {
    const response = jsonResponse(result(runContainer))
    Object.defineProperty(response, 'redirected', { value: true })
    await expectUnknown(response, runContainer)
  })

  it.each(['', '<html>proxy error</html>', '{"containerId":"exact-container-id",'])('无法解析的 2xx JSON 不提供权威 ID：%s', async (body) => {
    await expectUnknown(new Response(body, { status: 200 }))
  })

  it('空 204 响应也属于结果未知', async () => {
    await expectUnknown(new Response(null, { status: 204 }))
  })

  it.each(['containerId', 'name', 'image', 'state', 'created', 'started', 'runContainer'])('缺少 %s 时不得承认成功', async (field) => {
    const payload: Record<string, unknown> = result()
    delete payload[field]
    await expectUnknown(jsonResponse(payload))
  })

  it.each(['containerId', 'image', 'state'])('%s 必须是非空白字符串', async (field) => {
    for (const value of ['', ' \t\r\n ', null, 42]) {
      await expectUnknown(jsonResponse({ ...result(), [field]: value }))
    }
  })

  it.each([null, [], false, 'result', 42])('拒绝非结果对象 %j', async (payload) => {
    await expectUnknown(jsonResponse(payload))
  })

  it.each([
    { state: 'unknown' }, { state: ' running ' }, { name: null }, { created: false }, { created: 'true' }, { started: 'false' }, { runContainer: 'false' },
  ])('拒绝字段类型或创建状态错误 %j', async (override) => {
    await expectUnknown(jsonResponse({ ...result(), ...override }))
  })

  it.each([false, true])('拒绝 runContainer 或 started 与提交意图冲突：%s', async (runContainer) => {
    await expectUnknown(jsonResponse({ ...result(runContainer), runContainer: !runContainer }), runContainer)
    await expectUnknown(jsonResponse({ ...result(runContainer), started: !runContainer }), runContainer)
    await expectUnknown(jsonResponse(result(!runContainer)), runContainer)
  })

  it('不会保存或透传响应中的敏感值或看似有效的部分 ID', async () => {
    const secret = 'fixture-secret-do-not-expose'
    const error = await expectUnknown(jsonResponse({ containerId: 'exact-container-id', environment: { TOKEN: secret }, message: secret }))
    expect(JSON.stringify(error)).not.toContain(secret)
    expect(String(error)).not.toContain(secret)
    expect(Object.getOwnPropertyNames(error)).toEqual(expect.arrayContaining(['name', 'message', 'code']))
  })

  it('保留 fetcher 注入、JSON 和凭证选项，并禁止跳转重放 POST', async () => {
    const submitted = { ...input(), environment: { APP_MODE: 'fixture' }, command: ['echo', '$(literal)'] }
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(result()))
    await createDockerContainer(submitted, fetcher)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/docker/containers', expect.objectContaining({
      method: 'POST', credentials: 'same-origin', redirect: 'error', body: JSON.stringify(submitted),
    }))
    const headers = new Headers(fetcher.mock.calls[0]![1]?.headers)
    expect(headers.get('Accept')).toBe('application/json')
    expect(headers.get('Content-Type')).toBe('application/json')
  })

  it('按已提交意图核验，而非异步等待期间被修改的输入', async () => {
    const submitted = input(false)
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => {
      submitted.runContainer = true
      return jsonResponse(result(false))
    })
    await expect(createDockerContainer(submitted, fetcher)).resolves.toEqual(result(false))
  })

  const ordinaryRejections = [
    { status: 400, code: 'DOCKER_CONTAINER_CREATE_INVALID' },
    { status: 400, code: 'CONTROL_INPUT_INVALID' },
    { status: 401, code: 'AUTH_UNAUTHORIZED' },
    { status: 403, code: 'HTTP_CROSS_ORIGIN_REJECTED' },
    { status: 404, code: 'ROUTE_NOT_FOUND' },
    { status: 405, code: 'METHOD_NOT_ALLOWED' },
    { status: 413, code: 'HTTP_REQUEST_BODY_TOO_LARGE' },
    { status: 415, code: 'HTTP_UNSUPPORTED_MEDIA_TYPE' },
    { status: 503, code: 'AUTH_UNAVAILABLE' },
  ]

  it.each(ordinaryRejections)('只保留已核实的拒绝配对 $status $code 及 requestId', async ({ status, code }) => {
    const payload = { code, message: '请求在创建前已被拒绝', requestId: 'req-1' }
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload, status))
    const error = await createDockerContainer(input(), fetcher).catch((caught: unknown) => caught)
    expect(error).toBeInstanceOf(NcpApiError)
    expect(error).not.toBeInstanceOf(CreateContainerOutcomeUnknownError)
    expect(error).toMatchObject(payload)
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it.each(ordinaryRejections)('已知码 $code 不能跨状态证明尚未创建', async ({ status, code }) => {
    for (const wrongStatus of [400, 401, 403, 404, 405, 409, 413, 415, 429, 500, 502, 503, 504].filter((value) => value !== status)) {
      await expectUnknown(jsonResponse({ code, message: '拒绝', requestId: 'req-1' }, wrongStatus))
    }
  })

  it.each(ordinaryRejections)('跳转后的 $status $code 不能证明原始创建请求被拒绝', async ({ status, code }) => {
    const response = jsonResponse({ code, message: '后续请求已拒绝', requestId: 'req-redirected' }, status)
    Object.defineProperty(response, 'redirected', { value: true })
    await expectUnknown(response)
  })

  it.each([
    { status: 409, code: 'DOCKER_CONTAINER_CREATE_FAILED' },
    { status: 409, code: 'DOCKER_CONTAINER_START_FAILED' },
    { status: 409, code: 'DOCKER_CONTAINER_INSPECT_FAILED' },
    { status: 409, code: 'DOCKER_CONTAINER_CREATE_CLEANUP_FAILED' },
    { status: 503, code: 'AGENT_DOCKER_CONTROL_UNAVAILABLE' },
    { status: 503, code: 'DOCKER_CONTAINER_CREATE_UNAVAILABLE' },
    { status: 400, code: 'UNRECOGNIZED_REJECTION' },
    { status: 400, code: 'docker_container_create_invalid' },
    { status: 400, code: ' DOCKER_CONTAINER_CREATE_INVALID ' },
    { status: 302, code: 'ROUTE_NOT_FOUND' },
    { status: 500, code: 'INTERNAL_ERROR' },
    { status: 502, code: 'BAD_GATEWAY' },
    { status: 504, code: 'GATEWAY_TIMEOUT' },
  ])('$status $code 不能证明未创建，也不透传原始诊断或部分 ID', async ({ status, code }) => {
    const secret = 'private-failure-diagnostic'
    const error = await expectUnknown(jsonResponse({ code, message: secret, requestId: secret, containerId: 'exact-container-id' }, status))
    expect(String(error)).not.toContain(secret)
    expect(JSON.stringify(error)).not.toContain(secret)
  })

  it.each(ordinaryRejections)('$code 缺少必需错误字段或字段类型错误仍未知', async ({ status, code }) => {
    const valid = { code, message: '拒绝', requestId: 'req-1' }
    for (const field of ['code', 'message', 'requestId']) {
      const missing: Record<string, unknown> = { ...valid }
      delete missing[field]
      await expectUnknown(jsonResponse(missing, status))
      for (const value of [null, 1, false, {}, []]) {
        await expectUnknown(jsonResponse({ ...valid, [field]: value }, status))
      }
    }
  })

  it.each([null, [], false, 'error', 42])('非对象错误体 %j 不能证明未创建', async (payload) => {
    await expectUnknown(jsonResponse(payload, 400))
  })

  it.each([400, 401, 403, 404, 405, 413, 415, 503, 502])('HTTP %s 的空体、HTML 或坏 JSON 均未知', async (status) => {
    for (const body of ['', '<html>private-proxy-error</html>', '{"code":"DOCKER_CONTAINER_CREATE_INVALID",']) {
      await expectUnknown(new Response(body, { status }))
    }
  })

  it.each([201, 400, 503])('HTTP %s 读取响应体失败也未知，不透传 cause', async (status) => {
    const response = new Response(null, { status })
    vi.spyOn(response, 'json').mockRejectedValue(new TypeError('private-body-read-error'))
    const error = await expectUnknown(response)
    expect(String(error)).not.toContain('private-body-read-error')
  })

  it.each([new TypeError('private-fetch-failure'), new DOMException('private-abort-detail', 'AbortError')])('fetch 拒绝成为未知且不重试：%s', async (failure) => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(failure)
    const error = await expectUnknownFetch(fetcher)
    expect(error).not.toBe(failure)
    expect(String(error)).not.toContain(failure.message)
    expect(JSON.stringify(error)).not.toContain(failure.message)
  })

  it('同步 fetch 抛错也成为未知且不重试', async () => {
    const fetcher = vi.fn<typeof fetch>().mockImplementation(() => { throw new TypeError('private-sync-failure') })
    const error = await expectUnknownFetch(fetcher)
    expect(String(error)).not.toContain('private-sync-failure')
  })

  it('本地循环引用序列化失败仍是普通错误，fetch 调用为零', async () => {
    const submitted = input() as DockerContainerCreateInput & { cycle?: unknown }
    submitted.cycle = submitted
    const fetcher = vi.fn<typeof fetch>()
    const error = await createDockerContainer(submitted, fetcher).catch((caught: unknown) => caught)
    expect(error).toBeInstanceOf(TypeError)
    expect(error).not.toBeInstanceOf(CreateContainerOutcomeUnknownError)
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('本地 toJSON 抛错保留原始错误，fetch 调用为零', async () => {
    const failure = new Error('local-serialization-failure')
    const submitted = { ...input(), toJSON() { throw failure } }
    const fetcher = vi.fn<typeof fetch>()
    await expect(createDockerContainer(submitted, fetcher)).rejects.toBe(failure)
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('本地请求选项构造失败仍是普通错误，fetch 调用为零', async () => {
    const failure = new Error('local-header-failure')
    const fetcher = vi.fn<typeof fetch>()
    vi.stubGlobal('Headers', class { constructor() { throw failure } })
    try {
      await expect(createDockerContainer(input(), fetcher)).rejects.toBe(failure)
      expect(fetcher).not.toHaveBeenCalled()
    } finally { vi.unstubAllGlobals() }
  })
})
