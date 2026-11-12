import { describe, expect, it, vi } from 'vitest'
import type { ContainerAction } from './system'
import { isDockerContainerActionAdmissionRejected, requestDockerContainerActionJob, requestDockerContainerActionJobStatus } from './dockerContainerActionJob'

const id = 'a'.repeat(64)
const job = { id: 'action-1', type: 'docker-container-start', reference: id, cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: '2026-10-15T00:00:00.123456789Z', updatedAt: '2026-10-15T00:00:00.123456789Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const response = (body: unknown, status = 202) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unknown = { code: 'DOCKER_CONTAINER_ACTION_ADMISSION_UNKNOWN' }
const invalidRead = { code: 'DOCKER_CONTAINER_ACTION_JOB_RESPONSE_INVALID' }

describe('单容器任务严格接收边界', () => {
  it.each(['start', 'stop', 'restart'] as const)('只发送一次无正文 %s，202 仅表示已接收', async action => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...job, type: `docker-container-${action}` }))
    expect(await requestDockerContainerActionJob(id, action, fetcher)).toMatchObject({ status: 'queued', reference: id })
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]![0]).toBe(`/api/v1/docker/containers/${id}/action-jobs/${action}`)
    expect(fetcher.mock.calls[0]![1]).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error' })
    expect(fetcher.mock.calls[0]![1].body).toBeUndefined()
    expect(fetcher.mock.calls[0]![1].headers.get('Content-Type')).toBeNull()
  })
  it.each(['name', 'abc', 'a'.repeat(63), 'A'.repeat(64), ` ${id}`, `${id}\n`, `sha256:${id}`])('无效 ID 在传输前拒绝：%s', async containerId => {
    const fetcher = vi.fn()
    const error = await requestDockerContainerActionJob(containerId, 'start', fetcher).catch(error => error)
    expect(error).toMatchObject({ code: 'DOCKER_CONTAINER_ACTION_INVALID' })
    expect(isDockerContainerActionAdmissionRejected(error)).toBe(true)
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each(['START', ' start', 'remove', '', null])('拒绝非精确动作：%s', async action => {
    const fetcher = vi.fn()
    await expect(requestDockerContainerActionJob(id, action as ContainerAction, fetcher)).rejects.toMatchObject({ code: 'DOCKER_CONTAINER_ACTION_INVALID' })
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each([
    { id: '' }, { id: ' ' }, { id: 'bad\n' }, { type: 'docker-container-stop' }, { reference: 'b'.repeat(64) },
    { cancellable: true }, { cancellable: undefined }, { artifactState: undefined }, { artifactState: 'present' },
    { status: 'running' }, { status: 'completed' }, { status: 'failed' }, { status: 'cancelled' },
    { message: null }, { createdAt: '' }, { createdAt: '2026-02-30T00:00:00Z' }, { updatedAt: '2026-10-15T00:00:01Z' },
    { progress: 1 }, { progress: -1 }, { progress: 101 }, { progress: null }, { downloadedBytes: 1 }, { totalBytes: 1 }, { speedBytes: 1 },
    { layers: [] }, { layers: { fake: {} } }, { progressLimited: true }, { progressLimited: 'false' },
    { completedAt: '2026-10-15T00:00:01Z' }, { persistenceStatus: 'pending' }, { cancellationStatus: 'requested' },
    { error: '' }, { errorCode: '' }, { extra: true },
  ])('无效初始回执保持未知：%j', async fields => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...job, ...fields }))
    const error = await requestDockerContainerActionJob(id, 'start', fetcher).catch(error => error)
    expect(error).toMatchObject(unknown)
    expect(isDockerContainerActionAdmissionRejected(error)).toBe(false)
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it('接受显式 false，不扩大进度证据', async () => {
    await expect(requestDockerContainerActionJob(id, 'start', vi.fn().mockResolvedValue(response({ ...job, progressLimited: false })))).resolves.toMatchObject(job)
  })
  const rejections = [[400, 'DOCKER_CONTAINER_ACTION_INVALID'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'], [503, 'JOB_QUEUE_FULL'], [503, 'JOB_PERSIST_FAILED'], [503, 'JOB_ENGINE_UNAVAILABLE']] as const
  it.each(rejections)('只识别精确状态和错误码：%i %s', async (status, code) => {
    const error = await requestDockerContainerActionJob(id, 'start', vi.fn().mockResolvedValue(response({ code, message: 'rejected', requestId: 'r' }, status))).catch(error => error)
    expect(error).toMatchObject({ code, requestId: 'r' })
    expect(isDockerContainerActionAdmissionRejected(error)).toBe(true)
    await expect(requestDockerContainerActionJob(id, 'start', vi.fn().mockResolvedValue(response({ code, message: 'rejected', requestId: 'r' }, 500)))).rejects.toMatchObject(unknown)
  })
  it.each([200, 201, 204, 301, 400, 404, 409, 500, 503])('意外 HTTP %i 不是未接收证据', async status => {
    const result = status === 204 ? new Response(null, { status }) : response(job, status)
    await expect(requestDockerContainerActionJob(id, 'start', vi.fn().mockResolvedValue(result))).rejects.toMatchObject(unknown)
  })
  it.each(['AGENT_DOCKER_UNAVAILABLE', 'DOCKER_CONTAINER_NOT_FOUND', 'AGENT_DOCKER_IMAGES_UNAVAILABLE', 'NEW_ERROR'])('任意错误码不能证明未接收：%s', async code => {
    await expect(requestDockerContainerActionJob(id, 'start', vi.fn().mockResolvedValue(response({ code, message: 'error', requestId: 'r' }, 503)))).rejects.toMatchObject(unknown)
  })
  it('网络、解析、媒体类型、重定向和超大响应未知且不重试', async () => {
    const redirected = response({ code: 'JOB_QUEUE_FULL', message: 'rejected', requestId: 'r' }, 503)
    Object.defineProperty(redirected, 'redirected', { value: true })
    const responses = [new Response('{', { status: 202, headers: { 'Content-Type': 'application/json' } }), new Response(JSON.stringify(job), { status: 202 }), redirected, response({ ...job, message: 'a'.repeat(65536) }), new Response(new Uint8Array([0xff]), { status: 202, headers: { 'Content-Type': 'application/json' } })]
    const fetchers = [vi.fn().mockRejectedValue(new Error('offline')), ...responses.map(result => vi.fn().mockResolvedValue(result))]
    for (const fetcher of fetchers) {
      await expect(requestDockerContainerActionJob(id, 'start', fetcher)).rejects.toMatchObject(unknown)
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
})

describe('已知单容器任务只读核对', () => {
  const completed = { ...job, status: 'completed', progress: 100, completedAt: '2026-10-15T00:00:01Z', updatedAt: '2026-10-15T00:00:01Z' }
  it('按已知 ID 读取并保留待落盘标记，绝不发起重试或取消', async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...completed, persistenceStatus: 'pending' }, 200))
    const signal = new AbortController().signal
    await expect(requestDockerContainerActionJobStatus(job.id, id, 'start', signal, fetcher)).resolves.toMatchObject({ ...completed, persistenceStatus: 'pending' })
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]).toEqual(['/api/v1/jobs/action-1', expect.objectContaining({ method: 'GET', cache: 'no-store', redirect: 'error', signal })])
  })
  it.each([
    { id: 'other' }, { type: 'docker-container-stop' }, { reference: 'b'.repeat(64) }, { cancellable: true },
    { completedAt: undefined }, { completedAt: '' }, { completedAt: '2026-02-30T00:00:00Z' }, { completedAt: '2026-10-15T00:00:02Z' },
    { progress: 99 }, { progress: 100.5 }, { error: 'failed' }, { errorCode: 'FAILED' }, { persistenceStatus: 'unknown' },
    { cancellationStatus: 'confirmed' }, { status: 'cancelled' }, { layers: [] }, { updatedAt: '' },
  ])('拒绝身份或终态证据错误：%j', async fields => {
    await expect(requestDockerContainerActionJobStatus(job.id, id, 'start', undefined, vi.fn().mockResolvedValue(response({ ...completed, ...fields }, 200)))).rejects.toMatchObject(invalidRead)
  })
  it.each(['queued', 'running', 'failed', 'interrupted', 'completed'])('读取合法 %s，不推断容器当前状态', async status => {
    const value = ['queued', 'running'].includes(status) ? { ...job, status } : { ...completed, status, ...(status !== 'completed' ? { progress: 0, errorCode: 'JOB_EXECUTION_INTERRUPTED' } : {}) }
    await expect(requestDockerContainerActionJobStatus(job.id, id, 'start', undefined, vi.fn().mockResolvedValue(response(value, 200)))).resolves.toMatchObject(value)
  })
  it.each(['queued', 'running'])('保留真实活动任务 %s 的待落盘标记', async status => {
    const value = { ...job, status, persistenceStatus: 'pending' }
    await expect(requestDockerContainerActionJobStatus(job.id, id, 'start', undefined, vi.fn().mockResolvedValue(response(value, 200)))).resolves.toMatchObject(value)
  })
  it.each([202, 401, 404, 500])('GET %s 保持核对失败', async status => {
    await expect(requestDockerContainerActionJobStatus(job.id, id, 'start', undefined, vi.fn().mockResolvedValue(response(completed, status)))).rejects.toMatchObject(invalidRead)
  })
  it('GET 重定向或断线不降级成新写请求', async () => {
    const result = response(completed, 200)
    Object.defineProperty(result, 'redirected', { value: true })
    for (const fetcher of [vi.fn().mockResolvedValue(result), vi.fn().mockRejectedValue(new Error('offline'))]) {
      await expect(requestDockerContainerActionJobStatus(job.id, id, 'start', undefined, fetcher)).rejects.toMatchObject(invalidRead)
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
})
