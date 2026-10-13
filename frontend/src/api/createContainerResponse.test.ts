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
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response)
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

describe('容器创建成功响应的完整确认边界', () => {
  it('从公共 API 导出专用错误类型', () => {
    expect(PublicOutcomeUnknownError).toBe(CreateContainerOutcomeUnknownError)
  })

  it.each([false, true])('只接受符合提交意图的完整成功结果：runContainer=%s', async (runContainer) => {
    // 启动成功后进程可能已退出，不能以 state 强制推断 started。
    const payload = result(runContainer)
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(payload))
    await expect(createDockerContainer(input(runContainer), fetcher)).resolves.toEqual(payload)
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

  it('保留 fetcher 注入、JSON 请求和凭证选项', async () => {
    const submitted = { ...input(), environment: { APP_MODE: 'fixture' }, command: ['echo', '$(literal)'] }
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(result()))
    await createDockerContainer(submitted, fetcher)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/docker/containers', expect.objectContaining({
      method: 'POST', credentials: 'same-origin', body: JSON.stringify(submitted),
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

  it('非 2xx 保留服务端稳定错误及 requestId', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({ code: 'DOCKER_CONTAINER_CREATE_FAILED', message: '创建失败', requestId: 'req-1' }, 409))
    const error = await createDockerContainer(input(), fetcher).catch((caught: unknown) => caught)
    expect(error).toBeInstanceOf(NcpApiError)
    expect(error).not.toBeInstanceOf(CreateContainerOutcomeUnknownError)
    expect(error).toMatchObject({ code: 'DOCKER_CONTAINER_CREATE_FAILED', message: '创建失败', requestId: 'req-1' })
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it('非 JSON 错误响应保留通用请求失败处理', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response('<html>error</html>', { status: 502 }))
    await expect(createDockerContainer(input(), fetcher)).rejects.toMatchObject({ code: 'API_REQUEST_FAILED' })
  })

  it.each([new TypeError('fetch failed'), new DOMException('Aborted', 'AbortError')])('fetch 拒绝保持原始边界，不重试：%s', async (failure) => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValue(failure)
    await expect(createDockerContainer(input(), fetcher)).rejects.toBe(failure)
    expect(fetcher).toHaveBeenCalledOnce()
  })
})
