import { describe, expect, it, vi } from 'vitest'
import { deleteDockerProject, isDockerProjectDeleteRejected } from './docker'
import { NcpApiError } from './systemTransport'

const project = () => ({ id: 'compose:demo', name: 'demo', kind: 'compose' as const })
const receipt = () => ({
  projectId: 'compose:demo', kind: 'compose', completed: true, partial: false,
  registryDeleted: true, registryRolledBack: false,
  containers: [{ containerId: 'one', name: '', state: 'stopped', deleted: true, success: true }],
})
const json = (payload: unknown, status = 200) => new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })

async function expectUnknown(fetcher: ReturnType<typeof vi.fn<typeof fetch>>) {
  const result = await deleteDockerProject(project(), fetcher).catch((error: unknown) => error)
  expect(result).toBeInstanceOf(NcpApiError)
  expect(isDockerProjectDeleteRejected(result)).toBe(false)
  expect(result).toMatchObject({ code: 'DOCKER_PROJECT_DELETE_OUTCOME_UNKNOWN' })
  expect(String(result)).toContain('勿重复提交')
  expect(result).not.toHaveProperty('payload')
  expect(fetcher).toHaveBeenCalledOnce()
}

describe('项目删除回执绑定与完整成功核验', () => {
  it.each([
    { projectId: 'compose:other' }, { projectId: '' }, { kind: 'standalone' },
    { completed: false }, { partial: true }, { registryDeleted: false }, { registryRolledBack: true },
    { containers: [{ ...receipt().containers[0], deleted: false }] },
    { containers: [{ ...receipt().containers[0], containerId: '' }] },
    { containers: [{ ...receipt().containers[0], containerId: '  ' }] },
    { containers: [receipt().containers[0], receipt().containers[0]] },
    { containers: [{ ...receipt().containers[0], success: false }] },
    { containers: [{ ...receipt().containers[0], errorCode: 'DOCKER_PROJECT_DELETE_FAILED' }] },
  ])('不能将矛盾或异项目回执当成功：%j', async (change) => {
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(json({ ...receipt(), ...change })))
  })

  it('接受完整成功及可省略的 errorCode', async () => {
    const value = receipt()
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(json(value))
    await expect(deleteDockerProject(project(), fetcher)).resolves.toEqual(value)
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it.each(['projectId', 'kind', 'completed', 'partial', 'registryDeleted', 'registryRolledBack', 'containers'])('缺失 %s 不能证明成功', async (key) => {
    const value: Record<string, unknown> = receipt()
    delete value[key]
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(json(value)))
  })

  it.each(['containerId', 'name', 'state', 'deleted', 'success'])('逐容器缺失 %s 不能证明成功', async (key) => {
    const container: Record<string, unknown> = { ...receipt().containers[0] }
    delete container[key]
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(json({ ...receipt(), containers: [container] })))
  })

  it.each([{}, { errorCode: '' }])('保留合法可选元数据兼容：%j', async (metadata) => {
    const value = { ...receipt(), containers: [{ ...receipt().containers[0], name: '', state: '', ...metadata }] }
    await expect(deleteDockerProject(project(), vi.fn<typeof fetch>().mockResolvedValue(json(value)))).resolves.toEqual(value)
  })

  it('保留旧 HTTP 合同允许的空容器数组', async () => {
    const value = { ...receipt(), containers: [] }
    await expect(deleteDockerProject(project(), vi.fn<typeof fetch>().mockResolvedValue(json(value)))).resolves.toEqual(value)
  })

  it('等待响应时修改调用方对象不改变提交身份', async () => {
    const input = project()
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => {
      input.id = 'compose:other'
      input.name = 'other'
      return json(receipt())
    })
    await expect(deleteDockerProject(input, fetcher)).resolves.toEqual(receipt())
    expect(fetcher).toHaveBeenCalledWith('/api/v1/docker/compose/projects/compose%3Ademo', expect.objectContaining({
      method: 'DELETE', redirect: 'error', credentials: 'same-origin',
      body: JSON.stringify({ projectId: 'compose:demo', kind: 'compose', registryName: 'demo' }),
    }))
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it.each([201, 202, 206, 302, 409, 500])('非 200 即使携带完整回执也不能确认：%s', async (status) => {
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(json(receipt(), status)))
  })

  it.each(['', '<html>secret</html>', '{'])('损坏响应保持 unknown：%j', async (body) => {
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(body)))
  })

  it('空 204 保持 unknown', async () => {
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 })))
  })

  it('网络中断保持 unknown，不重试', async () => {
    await expectUnknown(vi.fn<typeof fetch>().mockRejectedValue(new Error('secret')))
  })

  it('跳转后的成功回执不能证明原请求成功', async () => {
    const response = json(receipt())
    Object.defineProperty(response, 'redirected', { value: true })
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(response))
  })

  it.each([
    'DOCKER_PROJECT_DELETE_FAILED', 'DOCKER_PROJECT_DELETE_REGISTRY_FAILED',
    'DOCKER_PROJECT_DELETE_INTEGRITY_FAILED', 'DOCKER_PROJECT_DELETE_ROLLBACK_FAILED',
    'AGENT_DOCKER_CONTROL_UNAVAILABLE',
  ])('可能已部分删除的错误不降级为普通拒绝：%s', async (code) => {
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(json({ code, message: 'secret', requestId: 'request-1' }, code.startsWith('AGENT_') ? 503 : 409)))
  })

  it.each([
    ['DOCKER_PROJECT_DELETE_INVALID', 400], ['DOCKER_PROJECT_DELETE_RUNNING', 409],
    ['DOCKER_PROJECT_DELETE_PROTECTED', 409], ['DOCKER_PROJECT_DELETE_REGISTRY_NOT_FOUND', 409],
    ['DOCKER_PROJECT_DELETE_NOT_FOUND', 404], ['AUTH_UNAUTHORIZED', 401],
    ['CONTROL_INPUT_INVALID', 400], ['AUTH_UNAVAILABLE', 503],
    ['HTTP_CROSS_ORIGIN_REJECTED', 403], ['ROUTE_NOT_FOUND', 404], ['METHOD_NOT_ALLOWED', 405],
    ['HTTP_REQUEST_BODY_TOO_LARGE', 413], ['HTTP_UNSUPPORTED_MEDIA_TYPE', 415],
  ] as const)('仅已知副作用前拒绝保留稳定码：%s', async (code, status) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(json({ code, message: 'secret', requestId: 'request-1' }, status))
    const error = await deleteDockerProject(project(), fetcher).catch((caught: unknown) => caught)
    expect(error).toMatchObject({ code, requestId: 'request-1' })
    expect(isDockerProjectDeleteRejected(error)).toBe(true)
    expect(isDockerProjectDeleteRejected(new NcpApiError(code, '伪造相同码'))).toBe(false)
    expect(String(error)).not.toContain('secret')
    expect(fetcher).toHaveBeenCalledOnce()
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(json({ code, message: 'secret', requestId: 'request-1' }, 500)))
  })

  it.each([null, [], false, 42, 'receipt', { ...receipt(), containers: null }, { ...receipt(), partial: 'false' }])('拒绝错误形状：%j', async (value) => {
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(json(value)))
  })

  it('不能借调用方身份变化接受另一个项目的回执', async () => {
    const input = project()
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async () => {
      input.id = 'compose:other'
      return json({ ...receipt(), projectId: input.id })
    })
    await expect(deleteDockerProject(input, fetcher)).rejects.toMatchObject({ code: 'DOCKER_PROJECT_DELETE_OUTCOME_UNKNOWN' })
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it.each(['text/html', 'text/plain', 'application/problem+json', ''])('错误媒体类型为 unknown：%s', async (contentType) => {
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(receipt()), { headers: { 'Content-Type': contentType } })))
  })

  it('严格拒绝无效 UTF8 字节而非替换后承认成功', async () => {
    const bytes = new TextEncoder().encode(JSON.stringify({ ...receipt(), extra: 'X' }))
    bytes[bytes.indexOf(88)] = 255
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(bytes, { headers: { 'Content-Type': 'application/json' } })))
  })

  it.each([
    '"completed":false,"completed":true',
    '"completed":false,"compl\\u0065ted":true',
    '"registryDeleted":false,"registryDeleted":true',
    '"deleted":false,"deleted":true',
  ])('拒绝重复或转义后重复关键字段：%s', async (duplicate) => {
    const field = duplicate.split(':')[0]!
    const source = JSON.stringify(receipt()).replace(`${field}:true`, duplicate)
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(source, { headers: { 'Content-Type': 'application/json' } })))
  })

  it('容纳大于创建回执上限的合法容器数组', async () => {
    const value = { ...receipt(), containers: Array.from({ length: 2000 }, (_, index) => ({ ...receipt().containers[0], containerId: `container-${index}` })) }
    expect(new TextEncoder().encode(JSON.stringify(value)).length).toBeGreaterThan(65536)
    await expect(deleteDockerProject(project(), vi.fn<typeof fetch>().mockResolvedValue(json(value)))).resolves.toEqual(value)
  })

  it('分块超过32MiB即取消读取并保留 unknown，不继续读或重试', async () => {
    const cancel = vi.fn()
    let reads = 0
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) { reads++; controller.enqueue(new Uint8Array(1024 * 1024).fill(32)) },
      cancel,
    }, { highWaterMark: 0 })
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(stream, { headers: { 'Content-Type': 'application/json' } })))
    expect(reads).toBe(33)
    expect(cancel).toHaveBeenCalledOnce()
    expect(stream.locked).toBe(false)
  })

  it('允许 JSON 媒体类型大小写和 charset 参数及分块多字节 UTF8', async () => {
    const value = { ...receipt(), containers: [{ ...receipt().containers[0], name: '容器' }] }
    const bytes = new TextEncoder().encode(JSON.stringify(value))
    let index = 0
    const stream = new ReadableStream<Uint8Array>({ pull(controller) {
      if (index === bytes.length) controller.close()
      else controller.enqueue(bytes.slice(index, ++index))
    } })
    await expect(deleteDockerProject(project(), vi.fn<typeof fetch>().mockResolvedValue(new Response(stream, { headers: { 'Content-Type': 'Application/JSON; charset=utf-8' } })))).resolves.toEqual(value)
    expect(stream.locked).toBe(false)
  })

  it('拒绝截断 UTF8、BOM 和读取中断', async () => {
    for (const bytes of [new Uint8Array([0xe4, 0xb8]), new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode(JSON.stringify(receipt()))])]) {
      await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(bytes, { headers: { 'Content-Type': 'application/json' } })))
    }
    const stream = new ReadableStream({ pull(controller) { controller.error(new Error('secret')) } })
    await expectUnknown(vi.fn<typeof fetch>().mockResolvedValue(new Response(stream, { headers: { 'Content-Type': 'application/json' } })))
    expect(stream.locked).toBe(false)
  })
})
