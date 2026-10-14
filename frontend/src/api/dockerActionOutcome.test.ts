import { describe, expect, it, vi } from 'vitest'
import { requestContainerAction, requestDockerProjectAction } from './docker'
import { decodeContainerActionReceipt, decodeStandaloneActionReceipt, dockerActionStateMatches, DockerActionOutcomeUnknownError } from './dockerActionOutcome'

const containerIntent = { containerId: 'container-a', action: 'restart' as const }
const projectIntent = { projectId: 'standalone', action: 'restart' as const, containerIds: ['container-a', 'container-b'] }
const project = { id: 'standalone', kind: 'standalone' as const, workingDirectory: '', configFiles: [], containerIds: ['container-a', 'container-b'] }
const item = (id = 'container-a') => ({ containerId: id, name: '', action: 'restart', state: 'running', success: true })
const containerReceipt = () => ({ containerId: 'container-a', name: '', action: 'restart', state: 'running' })
const projectReceipt = () => ({ projectId: 'standalone', kind: 'standalone', action: 'restart', state: 'running', completed: true, containers: [item(), item('container-b')] })
const response = (payload: unknown, status = 200) => new Response(status === 204 ? null : JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
const errorEnvelope = (code: string) => ({ code, message: 'response-secret', requestId: 'request-1' })
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((yes) => { resolve = yes }); return { promise, resolve } }

const calls = [
  { scope: 'container', valid: containerReceipt, run: (fetcher: typeof fetch) => requestContainerAction('container-a', 'restart', fetcher) },
  { scope: 'standalone', valid: projectReceipt, run: (fetcher: typeof fetch) => requestDockerProjectAction(project, 'restart', fetcher) },
]

describe('Docker 生命周期回执结构与归属', () => {
  it.each([null, [], {}, false, 'secret', 9])('拒绝单容器非法根 %j', (payload) => {
    expect(() => decodeContainerActionReceipt(payload, containerIntent)).toThrow(DockerActionOutcomeUnknownError)
  })
  it.each(['containerId', 'name', 'action', 'state'])('拒绝单容器缺失或错误类型 %s', (key) => {
    for (const value of [undefined, null, false, 7, []]) {
      expect(() => decodeContainerActionReceipt({ ...containerReceipt(), [key]: value }, containerIntent)).toThrow(DockerActionOutcomeUnknownError)
    }
    const payload = { ...containerReceipt() } as Record<string, unknown>
    delete payload[key]
    expect(() => decodeContainerActionReceipt(payload, containerIntent)).toThrow(DockerActionOutcomeUnknownError)
    expect(() => decodeContainerActionReceipt(Object.create(containerReceipt()), containerIntent)).toThrow(DockerActionOutcomeUnknownError)
  })
  it.each([{ containerId: 'other' }, { containerId: '' }, { action: 'stop' }, { action: ' RESTART ' }, { state: '' }])('拒绝单容器不属于请求的回执 %j', (changes) => {
    expect(() => decodeContainerActionReceipt({ ...containerReceipt(), ...changes }, containerIntent)).toThrow(DockerActionOutcomeUnknownError)
  })
  it('投影新对象，兼容空名称和未知状态，不把瞬态/并发状态不符当坏回执', () => {
    const payload = Object.freeze({ ...containerReceipt(), state: 'stopped', extra: 'response-secret' })
    const parsed = decodeContainerActionReceipt(payload, containerIntent)
    expect(parsed).toEqual({ ...containerReceipt(), state: 'stopped' })
    expect(parsed).not.toBe(payload)
    expect(dockerActionStateMatches('restart', parsed.state)).toBe(false)
    expect(decodeContainerActionReceipt({ ...payload, state: 'restarting' }, containerIntent).state).toBe('restarting')
    expect(dockerActionStateMatches('start', 'running')).toBe(true)
    expect(dockerActionStateMatches('stop', 'stopped')).toBe(true)
    expect(dockerActionStateMatches('stop', 'running')).toBe(false)
  })
  it.each(['projectId', 'kind', 'action', 'state', 'completed', 'containers'])('拒绝项目缺失字段 %s', (key) => {
    const payload = projectReceipt() as Record<string, unknown>
    delete payload[key]
    expect(() => decodeStandaloneActionReceipt(payload, projectIntent)).toThrow(DockerActionOutcomeUnknownError)
  })
  it.each([
    { projectId: 'other' }, { kind: 'compose' }, { action: 'stop' }, { state: null }, { state: '' }, { completed: 1 },
    { containers: null }, { containers: [] }, { containers: [item()] }, { containers: [item(), item()] },
    { containers: [item(), item('other')] }, { containers: [item(), item('container-b'), item('other')] },
    { containers: [item(), null] }, { containers: new Array(2) },
    { containers: [item(), { ...item('container-b'), action: 'stop' }] },
    { containers: [item(), { ...item('container-b'), success: 'true' }] },
    { containers: [item(), { ...item('container-b'), errorCode: 7 }] },
    { containers: [item(), { ...item('container-b'), errorCode: 'DOCKER_CONTAINER_ACTION_FAILED' }] },
    { containers: [item(), { ...item('container-b'), success: false }] },
  ])('拒绝项目错归属、坏行和矛盾汇总 %j', (changes) => {
    expect(() => decodeStandaloneActionReceipt({ ...projectReceipt(), ...changes }, projectIntent)).toThrow(DockerActionOutcomeUnknownError)
  })
  it('允许结果重排和旧 completed=true 的未达目标观测；仅复制公开字段', () => {
    const payload = { ...projectReceipt(), state: 'stopped', extra: 'secret', containers: [{ ...item('container-b'), state: 'stopped', extra: 'secret' }, { ...item(), state: 'stopped' }] }
    const before = JSON.stringify(payload)
    const parsed = decodeStandaloneActionReceipt(payload, projectIntent)
    expect(parsed.completed).toBe(true)
    expect(parsed.containers.map((entry) => entry.containerId)).toEqual(['container-b', 'container-a'])
    expect(parsed.containers.every((entry) => dockerActionStateMatches('restart', entry.state))).toBe(false)
    expect(JSON.stringify(parsed)).not.toContain('secret')
    expect(JSON.stringify(payload)).toBe(before)
    expect(parsed.containers[0]).not.toBe(payload.containers[0])
  })
  it('失败项即使读回达到目标也不能升级为成功', () => {
    const payload = { ...projectReceipt(), completed: false, containers: [item(), { ...item('container-b'), success: false, errorCode: 'DOCKER_CONTAINER_ACTION_FAILED' }] }
    expect(decodeStandaloneActionReceipt(payload, projectIntent).containers[1]?.success).toBe(false)
  })
  it('错误 getter 不泄漏异常文本', () => {
    const payload = { ...containerReceipt(), get state() { throw new Error('response-secret') } }
    expect(() => decodeContainerActionReceipt(payload, containerIntent)).toThrow('结果尚未确认')
  })
})

describe.each(calls)('$scope 单次派发结果边界', ({ valid, run }) => {
  it('只发一次同源 POST，禁用自动跳转', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(valid()))
    await expect(run(fetcher)).resolves.toMatchObject({ action: 'restart' })
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error' })
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get('Accept')).toBe('application/json')
  })
  it.each([201, 202, 204, 206, 299, 400, 401, 403, 404, 409, 500, 503, 504])('状态 %s 不能冒充有效操作回执', async (status) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(valid(), status))
    await expect(run(fetcher)).rejects.toBeInstanceOf(DockerActionOutcomeUnknownError)
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it('拒绝跟随跳转后的回执', async () => {
    const result = response(valid())
    Object.defineProperty(result, 'redirected', { value: true })
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(result)
    await expect(run(fetcher)).rejects.toBeInstanceOf(DockerActionOutcomeUnknownError)
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it('网络错误、非法 JSON、坏回执都不会诱导自动重发或泄漏原文', async () => {
    for (const fetcher of [vi.fn<typeof fetch>().mockRejectedValue(new Error('response-secret')), vi.fn<typeof fetch>().mockResolvedValue(new Response('response-secret')), vi.fn<typeof fetch>().mockResolvedValue(response({ secret: 'response-secret' }))]) {
      const error = await run(fetcher).catch((caught: unknown) => caught)
      expect(error).toBeInstanceOf(DockerActionOutcomeUnknownError)
      expect(String(error)).not.toContain('response-secret')
      expect(String(error)).toContain('勿重复提交')
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
  it('只保留精确派发前拒绝，错误正文不作为用户文案', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(errorEnvelope('HTTP_CROSS_ORIGIN_REJECTED'), 403))
    await expect(run(fetcher)).rejects.toMatchObject({ code: 'HTTP_CROSS_ORIGIN_REJECTED', requestId: 'request-1', message: '请求来源校验失败。' })
    const wrongStatus = vi.fn<typeof fetch>().mockResolvedValue(response(errorEnvelope('HTTP_CROSS_ORIGIN_REJECTED'), 500))
    await expect(run(wrongStatus)).rejects.toBeInstanceOf(DockerActionOutcomeUnknownError)
  })
  it.each(['DOCKER_CONTAINER_NOT_FOUND', 'DOCKER_CONTAINER_ACTION_FAILED', 'DOCKER_CONTAINER_INSPECT_FAILED', 'AGENT_RPC_TIMEOUT', 'DOCKER_PROJECT_ACTION_FAILED', 'DOCKER_CONTAINER_ACTION_UNAVAILABLE', 'AGENT_DOCKER_CONTROL_UNAVAILABLE'])('派发后诊断 %s 不能证明未执行', async (code) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(errorEnvelope(code), 409))
    await expect(run(fetcher)).rejects.toBeInstanceOf(DockerActionOutcomeUnknownError)
    expect(fetcher).toHaveBeenCalledOnce()
  })
})

it('独立项目固定派发前的 project/action/目标集合快照', async () => {
  const pending = deferred<Response>()
  const fetcher = vi.fn<typeof fetch>().mockReturnValue(pending.promise)
  const mutable = { ...project, containerIds: [...project.containerIds] }
  const request = requestDockerProjectAction(mutable, 'restart', fetcher)
  mutable.id = 'changed'
  mutable.containerIds[0] = 'other'
  mutable.containerIds.push('extra')
  pending.resolve(response(projectReceipt()))
  await expect(request).resolves.toMatchObject({ projectId: 'standalone' })
  expect(fetcher).toHaveBeenCalledOnce()
  expect(fetcher.mock.calls[0]?.[0]).toBe('/api/v1/docker/projects/standalone/actions/restart')
  expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toEqual({ projectId: 'standalone', kind: 'standalone', containerIds: ['container-a', 'container-b'], action: 'restart' })
})

it('Compose 保持既有传输及独立 services/output 回执', async () => {
  const payload = { projectId: 'compose:demo', action: 'restart', state: 'running', completed: true, output: '', services: [{ name: 'web', state: 'running', running: true }] }
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response(payload))
  await expect(requestDockerProjectAction({ ...project, id: 'compose:demo', kind: 'compose' }, 'restart', fetcher)).resolves.toEqual(payload)
  expect(fetcher.mock.calls[0]?.[0]).toBe('/api/v1/docker/compose/projects/compose%3Ademo/actions/restart')
})
