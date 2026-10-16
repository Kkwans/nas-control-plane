import { describe, expect, it, vi } from 'vitest'
import type { DockerContainerCreateInput } from './system'
import { isDockerContainerCreateAdmissionRejected, requestDockerContainerCreateJob, requestDockerContainerCreateJobStatus, requestDockerContainerCreateJobs } from './dockerContainerCreateJob'
const input: DockerContainerCreateInput = { image: 'PRIVATE_IMAGE', name: 'PRIVATE_NAME', environment: { SECRET: 'PRIVATE_SENTINEL' }, command: ['PRIVATE_COMMAND'], runContainer: true }
const job = { id: 'create-1', type: 'docker-container-create', cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: '2026-10-15T00:00:00.123456789Z', updatedAt: '2026-10-15T00:00:00.123456789Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const completed = { ...job, status: 'completed', progress: 100, completedAt: '2026-10-15T00:00:01Z', updatedAt: '2026-10-15T00:00:01Z', createResult: { containerId: 'a'.repeat(64), created: true, started: true } }
const raw = (body: string | Uint8Array, status = 202) => new Response(body as BodyInit, { status, headers: { 'Content-Type': 'application/json' } })
const response = (body: unknown, status = 202) => raw(JSON.stringify(body), status)
const unknown = { code: 'DOCKER_CONTAINER_CREATE_ADMISSION_UNKNOWN' }, invalidRead = { code: 'DOCKER_CONTAINER_CREATE_JOB_RESPONSE_INVALID' }
const read = (value: unknown, run = true) => requestDockerContainerCreateJobStatus(job.id, run, undefined, vi.fn().mockResolvedValue(response(value, 200)))
describe('严格创建接纳', () => {
  it('只发一次JSON POST，新路径、same-origin、拒绝重定向', async () => {
    const fetcher = vi.fn().mockResolvedValue(response(job))
    expect(await requestDockerContainerCreateJob(input, fetcher)).toEqual(job)
    expect(fetcher).toHaveBeenCalledOnce()
    const [url, init] = fetcher.mock.calls[0]!
    expect(url).toBe('/api/v1/docker/containers/create-jobs')
    expect(init).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error' })
    expect(init.headers.get('Content-Type')).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual(input)
  })
  it('可选字段零值归一化', async () => {
    const value = { ...job, reference: '', progressLimited: false }
    expect(await requestDockerContainerCreateJob(input, vi.fn().mockResolvedValue(response(value)))).toEqual({ ...job, progressLimited: false })
  })
  it.each([
    { id: '' }, { id: ' ' }, { id: 'bad\n' }, { id: '\ud800' }, { type: 'docker-image-pull' }, { reference: 'PRIVATE_NAME' }, { reference: null },
    { cancellable: true }, { cancellable: undefined }, { artifactState: 'present' }, { artifactState: undefined },
    { status: 'running' }, { status: 'completed' }, { status: 'failed' }, { status: 'cancelled' },
    { message: null }, { message: '\ud800' }, { createdAt: '' }, { createdAt: '2026-02-30T00:00:00Z' }, { updatedAt: '2026-10-15T00:00:01Z' },
    { progress: 1 }, { progress: null }, { downloadedBytes: 1 }, { totalBytes: 1 }, { speedBytes: 1 }, { layers: [] }, { layers: null }, { layers: { fake: {} } },
    { progressLimited: true }, { progressLimited: 'false' }, { completedAt: '' }, { persistenceStatus: 'pending' }, { cancellationStatus: 'requested' },
    { error: '' }, { errorCode: '' }, { persistenceStatus: '' }, { cancellationStatus: '' }, { error: 'PRIVATE_SENTINEL' }, { errorCode: 'FAILED' }, { createResult: completed.createResult }, { createResult: null }, { extra: true },
  ])('畸形202未知且不重发：%j', async fields => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...job, ...fields }))
    const error = await requestDockerContainerCreateJob(input, fetcher).catch(error => error)
    expect(error).toMatchObject(unknown)
    expect(isDockerContainerCreateAdmissionRejected(error)).toBe(false)
    expect(String(error) + JSON.stringify(error)).not.toContain('PRIVATE_SENTINEL')
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it.each([[400, 'DOCKER_CONTAINER_CREATE_INVALID'], [400, 'CONTROL_INPUT_INVALID'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'], [503, 'AGENT_DOCKER_CONTROL_UNAVAILABLE'], [503, 'JOB_QUEUE_FULL'], [503, 'JOB_PERSIST_FAILED'], [503, 'JOB_ENGINE_UNAVAILABLE']] as const)('仅精确%s/%s证明拒绝', async (status, code) => {
    const payload = { code, message: 'PRIVATE_SENTINEL', requestId: 'PRIVATE_SENTINEL' }
    const error = await requestDockerContainerCreateJob(input, vi.fn().mockResolvedValue(response(payload, status))).catch(error => error)
    expect(isDockerContainerCreateAdmissionRejected(error)).toBe(true)
    expect(error.code).toBe(code)
    expect(String(error) + JSON.stringify(error)).not.toContain('PRIVATE_SENTINEL')
    await expect(requestDockerContainerCreateJob(input, vi.fn().mockResolvedValue(response(payload, 500)))).rejects.toMatchObject(unknown)
  })
  it.each(['DOCKER_CONTAINER_CREATE_FAILED', 'DOCKER_CONTAINER_START_FAILED', 'DOCKER_CONTAINER_CREATE_CLEANUP_FAILED', 'AGENT_RPC_REQUEST_INVALID', 'AGENT_PROTOCOL_MISMATCH', 'AGENT_DOCKER_UNAVAILABLE', 'NEW_ERROR'])('未知码%s不证明拒绝', async code => {
    await expect(requestDockerContainerCreateJob(input, vi.fn().mockResolvedValue(response({ code, message: 'error', requestId: 'r' }, 503)))).rejects.toMatchObject(unknown)
  })
  it.each([200, 201, 204, 301, 400, 404, 409, 500, 503])('非202/%s未知', async status => {
    await expect(requestDockerContainerCreateJob(input, vi.fn().mockResolvedValue(status === 204 ? new Response(null, { status }) : response(job, status)))).rejects.toMatchObject(unknown)
  })
  it('重复键、转义重复键、尾随JSON、恶意错误包、UTF8、媒体类型、重定向和网络错误未知', async () => {
    const redirected = response({ code: 'JOB_QUEUE_FULL', message: 'error', requestId: 'r' }, 503)
    Object.defineProperty(redirected, 'redirected', { value: true })
    const bad = [raw('{'), raw(JSON.stringify(job) + '{}'), raw(JSON.stringify(job).replace('"status":"queued"', '"status":"running","status":"queued"')),
      raw(JSON.stringify(job).replace('"status":"queued"', '"status":"running","st\\u0061tus":"queued"')),
      response({ code: 'JOB_QUEUE_FULL', message: 'error', requestId: 'r', extra: true }, 503), raw('{"code":"UNKNOWN","code":"JOB_QUEUE_FULL","message":"error","requestId":"r"}', 503),
      new Response(JSON.stringify(job), { status: 202 }), redirected, response({ ...job, message: 'a'.repeat(65536) }), raw(new Uint8Array([0xff])), raw(new Uint8Array([0xef, 0xbb, 0xbf, 0x7b, 0x7d]))]
    for (const fetcher of [vi.fn().mockRejectedValue(new Error('PRIVATE_SENTINEL')), ...bad.map(value => vi.fn().mockResolvedValue(value))]) {
      const error = await requestDockerContainerCreateJob(input, fetcher).catch(error => error)
      expect(error).toMatchObject(unknown)
      expect(String(error) + JSON.stringify(error)).not.toContain('PRIVATE_SENTINEL')
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
  it('超限取消流读取，跨chunk合法UTF8可读', async () => {
    let cancelled = false
    const stream = new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array(65537)) }, cancel() { cancelled = true } })
    await expect(requestDockerContainerCreateJob(input, vi.fn().mockResolvedValue(new Response(stream, { status: 202, headers: { 'Content-Type': 'application/json' } })))).rejects.toMatchObject(unknown)
    expect(cancelled).toBe(true)
    const bytes = new TextEncoder().encode(JSON.stringify(job))
    const chunks = new ReadableStream({ start(controller) { for (const byte of bytes) controller.enqueue(new Uint8Array([byte])); controller.close() } })
    await expect(requestDockerContainerCreateJob(input, vi.fn().mockResolvedValue(new Response(chunks, { status: 202, headers: { 'Content-Type': 'application/json; charset=utf-8' } })))).resolves.toEqual(job)
  })
  it('循环、超大输入和启动意图异常在网络前拒绝', async () => {
    const circular = { ...input, recursive: {} }; circular.recursive = circular
    for (const value of [circular, { ...input, environment: { SECRET: 'x'.repeat(16384) } }, { ...input, runContainer: undefined }, { ...input, toJSON() { return { ...input, runContainer: false } } }]) {
      const fetcher = vi.fn(), error = await requestDockerContainerCreateJob(value as DockerContainerCreateInput, fetcher).catch(error => error)
      expect(isDockerContainerCreateAdmissionRejected(error)).toBe(true)
      expect(fetcher).not.toHaveBeenCalled()
      expect(String(error)).not.toContain('PRIVATE_SENTINEL')
    }
  })
})
describe('精确只读任务核对', () => {
  it('精确ID GET、无缓存、signal透传、无写请求', async () => {
    const signal = new AbortController().signal, fetcher = vi.fn().mockResolvedValue(response(completed, 200))
    expect(await requestDockerContainerCreateJobStatus(job.id, true, signal, fetcher)).toEqual(completed)
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]).toEqual(['/api/v1/jobs/create-1', expect.objectContaining({ method: 'GET', cache: 'no-store', redirect: 'error', credentials: 'same-origin', signal })])
  })
  it.each([true, false])('started=%s须精确绑定提交意图', async started => {
    const value = { ...completed, createResult: { ...completed.createResult, started } }
    await expect(read(value, started)).resolves.toEqual(value)
    await expect(read(value, !started)).rejects.toMatchObject(invalidRead)
  })
  it.each(['queued', 'running', 'failed', 'interrupted'])('%s不允许任何结果字段', async status => {
    const value = ['queued', 'running'].includes(status) ? { ...job, status } : { ...job, status, completedAt: completed.completedAt, updatedAt: completed.updatedAt, errorCode: 'JOB_EXECUTION_INTERRUPTED' }
    await expect(read(value)).resolves.toEqual(value)
    await expect(read({ ...value, createResult: completed.createResult })).rejects.toMatchObject(invalidRead)
    await expect(read({ ...value, createResult: null })).rejects.toMatchObject(invalidRead)
  })
  it.each(['queued', 'running'])('pending%s无结果', async status => {
    const value = { ...job, status, persistenceStatus: 'pending' }
    await expect(read(value)).resolves.toEqual(value)
    await expect(read({ ...value, createResult: completed.createResult })).rejects.toMatchObject(invalidRead)
  })
  it.each([
    { id: 'other' }, { type: 'docker-container-start' }, { reference: 'PRIVATE_IMAGE' }, { cancellable: true }, { completedAt: undefined }, { completedAt: '' },
    { completedAt: '2026-02-30T00:00:00Z' }, { completedAt: '2026-10-15T00:00:02Z' }, { progress: 99 }, { progress: 100.5 },
    { error: 'failed' }, { errorCode: 'FAILED' }, { persistenceStatus: 'pending' }, { persistenceStatus: 'unknown' }, { status: 'cancelled' },
    { createResult: undefined }, { createResult: null }, { createResult: {} }, { createResult: { containerId: 'a'.repeat(64), created: true } },
    { createResult: { ...completed.createResult, containerId: 'A'.repeat(64) } }, { createResult: { ...completed.createResult, containerId: 'a'.repeat(63) } },
    { createResult: { ...completed.createResult, containerId: 'name' } }, { createResult: { ...completed.createResult, created: false } },
    { createResult: { ...completed.createResult, started: 'true' } }, { createResult: { ...completed.createResult, image: 'PRIVATE_IMAGE' } },
    { createResult: { ...completed.createResult, name: 'PRIVATE_NAME' } }, { createResult: { ...completed.createResult, state: 'running' } }, { createResult: { ...completed.createResult, runContainer: true } },
  ])('畸形完成回执无成功证据：%j', async fields => { await expect(read({ ...completed, ...fields })).rejects.toMatchObject(invalidRead) })
  it('拒绝结果对象重复键', async () => {
    const body = JSON.stringify(completed).replace('"created":true', '"created":false,"created":true')
    await expect(requestDockerContainerCreateJobStatus(job.id, true, undefined, vi.fn().mockResolvedValue(raw(body, 200)))).rejects.toMatchObject(invalidRead)
  })
  it.each([202, 401, 404, 500])('GET%s核对失败不重发', async status => {
    const fetcher = vi.fn().mockResolvedValue(response(completed, status))
    await expect(requestDockerContainerCreateJobStatus(job.id, true, undefined, fetcher)).rejects.toMatchObject(invalidRead)
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it('GET断线、重定向不降级为写', async () => {
    const redirected = response(completed, 200); Object.defineProperty(redirected, 'redirected', { value: true })
    for (const fetcher of [vi.fn().mockResolvedValue(redirected), vi.fn().mockRejectedValue(new Error('PRIVATE_SENTINEL'))]) {
      await expect(requestDockerContainerCreateJobStatus(job.id, true, undefined, fetcher)).rejects.toMatchObject(invalidRead)
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
})


describe('未知接纳的严格只读列表', () => {
  it('请求专用类型列表，仅返回观察字段，不认领或发布结果', async () => {
    const signal = new AbortController().signal
    const fetcher = vi.fn().mockResolvedValue(response({ jobs: [job, { ...completed, id: 'done' }] }, 200))
    await expect(requestDockerContainerCreateJobs(signal, fetcher)).resolves.toEqual({ jobs: [{ id: job.id, type: job.type, status: 'queued' }, { id: 'done', type: job.type, status: 'completed' }], invalidCount: 0 })
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]).toEqual(['/api/v1/jobs?type=docker-container-create', expect.objectContaining({ method: 'GET', cache: 'no-store', redirect: 'error', signal })])
  })
  it('坏条目和重复ID使列表不完整，保留其它有效活动证据', async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ jobs: [{ ...job, persistenceStatus: 'pending' }, { ...job, id: 'bad', status: 'new-status' }, { ...job }, { ...completed, id: 'no-result', createResult: undefined }, { ...job, id: 'other-type', type: 'docker-image-pull' }] }, 200))
    await expect(requestDockerContainerCreateJobs(undefined, fetcher)).resolves.toEqual({ jobs: [{ id: job.id, type: job.type, status: 'queued', persistenceStatus: 'pending' }], invalidCount: 4 })
  })
  it.each([null, {}, { jobs: null }, { jobs: [], extra: true }, { jobs: 'no' }])('畸形列表信封%j保持失败', async value => {
    await expect(requestDockerContainerCreateJobs(undefined, vi.fn().mockResolvedValue(response(value, 200)))).rejects.toMatchObject(invalidRead)
  })
  it('完整空列表只表示未观察到活动，返回值不含接纳判定', async () => {
    await expect(requestDockerContainerCreateJobs(undefined, vi.fn().mockResolvedValue(response({ jobs: [] }, 200)))).resolves.toEqual({ jobs: [], invalidCount: 0 })
  })
  it('列表重定向/非200/断线/超限不是空列表', async () => {
    const redirected = response({ jobs: [] }, 200); Object.defineProperty(redirected, 'redirected', { value: true })
    for (const fetcher of [vi.fn().mockResolvedValue(redirected), vi.fn().mockResolvedValue(response({ jobs: [] }, 503)), vi.fn().mockRejectedValue(new Error('PRIVATE_SENTINEL')), vi.fn().mockResolvedValue(response({ jobs: [{ ...job, message: 'x'.repeat(65536) }] }, 200))]) {
      await expect(requestDockerContainerCreateJobs(undefined, fetcher)).rejects.toMatchObject(invalidRead)
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
  it('纳秒倒序时间拒绝', async () => {
    await expect(read({ ...job, updatedAt: '2026-10-15T00:00:00.123456788Z' })).rejects.toMatchObject(invalidRead)
    await expect(read({ ...completed, completedAt: '2026-10-15T00:00:01.000000001Z' })).rejects.toMatchObject(invalidRead)
  })
})
