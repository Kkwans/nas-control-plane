import { describe, expect, it, vi } from 'vitest'
import { NcpApiError } from './systemTransport'
import { isDockerProjectActionAdmissionRejected, requestDockerProjectActionJob, requestDockerProjectActionJobStatus, requestDockerProjectActionJobs, type DockerProjectAction } from './dockerProjectActionJob'
const ids = ['a'.repeat(64), 'b'.repeat(64)]
const job = { id: 'project-1', type: 'docker-project-start', reference: 'standalone', cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: '2026-10-15T00:00:00.123456789Z', updatedAt: '2026-10-15T00:00:00.123456789Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const result = { projectId: 'standalone', kind: 'standalone', action: 'start', items: ids.map(containerId => ({ containerId, outcome: 'succeeded', state: 'running' })) }
const completed = { ...job, status: 'completed', progress: 100, updatedAt: '2026-10-15T00:00:01Z', completedAt: '2026-10-15T00:00:01Z', projectActionResult: result }
const raw = (body: string | Uint8Array, status = 202) => new Response(body as BodyInit, { status, headers: { 'Content-Type': 'application/json' } })
const response = (body: unknown, status = 202) => raw(JSON.stringify(body), status)
const unknown = { code: 'DOCKER_PROJECT_ACTION_ADMISSION_UNKNOWN' }, invalid = { code: 'DOCKER_PROJECT_ACTION_JOB_RESPONSE_INVALID' }
const read = (value: unknown, frozen = ids, action: DockerProjectAction = 'start') => requestDockerProjectActionJobStatus(job.id, frozen, action, undefined, vi.fn().mockResolvedValue(response(value, 200)))
const listFetcher = (rows: unknown[] = []) => vi.fn().mockImplementation(async (url: string) => response({ jobs: url.endsWith('-start') ? rows : [] }, 200))

describe('独立项目动作严格单次接纳', () => {
  it.each(['start', 'stop', 'restart'] as const)('%s冻结JSON只写一次', async action => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...job, type: `docker-project-${action}` }))
    await expect(requestDockerProjectActionJob(ids, action, fetcher)).resolves.toMatchObject({ status: 'queued' })
    expect(fetcher).toHaveBeenCalledOnce()
    const [url, init] = fetcher.mock.calls[0]!
    expect(url).toBe(`/api/v1/docker/projects/standalone/action-jobs/${action}`)
    expect(init).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error' })
    expect(init.headers.get('Content-Type')).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual({ containerIds: ids })
  })
  it('发送前拒绝坏集合及动作', async () => {
    for (const bad of [[], ['name'], ['A'.repeat(64)], ['a'.repeat(63)], [ids[0], ids[0]], Array.from({ length: 129 }, (_, i) => i.toString(16).padStart(64, '0')), null, 'x']) {
      const fetcher = vi.fn()
      expect(isDockerProjectActionAdmissionRejected(await requestDockerProjectActionJob(bad as string[], 'start', fetcher).catch(error => error))).toBe(true)
      expect(fetcher).not.toHaveBeenCalled()
    }
    const fetcher = vi.fn()
    expect(isDockerProjectActionAdmissionRejected(await requestDockerProjectActionJob(ids, 'START' as DockerProjectAction, fetcher).catch(error => error))).toBe(true)
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each([
    { id: '' }, { id: 'bad\n' }, { id: '\ud800' }, { type: 'docker-project-stop' }, { reference: '' }, { reference: null },
    { cancellable: true }, { artifactState: 'present' }, { status: 'running' }, { status: 'completed' }, { status: 'cancelled' },
    { message: null }, { message: '\ud800' }, { createdAt: '2026-02-30T00:00:00Z' }, { updatedAt: '2026-10-15T00:00:00.123456788Z' },
    { progress: 1 }, { downloadedBytes: 1 }, { totalBytes: 1 }, { speedBytes: 1 }, { layers: [] }, { layers: { fake: {} } },
    { progressLimited: true }, { completedAt: '' }, { persistenceStatus: 'pending' }, { cancellationStatus: 'requested' },
    { error: '' }, { errorCode: 'PRIVATE_SENTINEL' }, { projectActionResult: result }, { projectActionResult: null }, { extra: true }, { createResult: {} },
  ])('畸形202未知%j', async fields => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...job, ...fields }))
    const error = await requestDockerProjectActionJob(ids, 'start', fetcher).catch(error => error)
    expect(error).toMatchObject(unknown)
    expect(isDockerProjectActionAdmissionRejected(error)).toBe(false)
    expect(JSON.stringify(error) + String(error)).not.toContain('PRIVATE_SENTINEL')
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it.each([[400, 'DOCKER_PROJECT_ACTION_INVALID'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'], [503, 'AGENT_DOCKER_CONTROL_UNAVAILABLE'], [503, 'JOB_QUEUE_FULL'], [503, 'JOB_PERSIST_FAILED'], [503, 'JOB_ENGINE_UNAVAILABLE']] as const)('仅精确%s/%s拒绝', async (status, code) => {
    const payload = { code, message: 'PRIVATE_SENTINEL', requestId: 'PRIVATE_SENTINEL' }
    const error = await requestDockerProjectActionJob(ids, 'start', vi.fn().mockResolvedValue(response(payload, status))).catch(error => error)
    expect(isDockerProjectActionAdmissionRejected(error)).toBe(true)
    expect(error.code).toBe(code)
    expect(JSON.stringify(error) + String(error)).not.toContain('PRIVATE_SENTINEL')
    await expect(requestDockerProjectActionJob(ids, 'start', vi.fn().mockResolvedValue(response(payload, 500)))).rejects.toMatchObject(unknown)
  })
  it('相同code不能伪造私有拒绝实例', async () => {
    const spoof = new NcpApiError('JOB_QUEUE_FULL', 'PRIVATE_SENTINEL')
    expect(isDockerProjectActionAdmissionRejected(spoof)).toBe(false)
    await expect(requestDockerProjectActionJob(ids, 'start', vi.fn().mockRejectedValue(spoof))).rejects.toMatchObject(unknown)
  })
  it.each([200, 201, 301, 400, 404, 409, 500, 503])('非202/%s不是接纳证据', async status => {
    await expect(requestDockerProjectActionJob(ids, 'start', vi.fn().mockResolvedValue(response(job, status)))).rejects.toMatchObject(unknown)
  })
  it('重复键、尾随、UTF8、BOM、深度、MIME、重定向及网络失败未知', async () => {
    const redirected = response(job); Object.defineProperty(redirected, 'redirected', { value: true })
    const malformed = [raw('{'), raw(JSON.stringify(job) + '{}'), raw(JSON.stringify(job).replace('"status":"queued"', '"status":"running","st\\u0061tus":"queued"')),
      raw('{"code":"UNKNOWN","code":"JOB_QUEUE_FULL","message":"error","requestId":"r"}', 503),
      response({ code: 'JOB_QUEUE_FULL', message: 'error', requestId: 'r', extra: true }, 503), raw(new Uint8Array([0xff])), raw(new Uint8Array([0xef, 0xbb, 0xbf, 0x7b, 0x7d])),
      raw('['.repeat(33) + '0' + ']'.repeat(33)), new Response(JSON.stringify(job), { status: 202, headers: { 'Content-Type': 'text/html' } }), redirected]
    for (const fetcher of [...malformed.map(value => vi.fn().mockResolvedValue(value)), vi.fn().mockRejectedValue(new Error('PRIVATE_SENTINEL'))]) {
      const error = await requestDockerProjectActionJob(ids, 'start', fetcher).catch(error => error)
      expect(error).toMatchObject(unknown)
      expect(String(error) + JSON.stringify(error)).not.toContain('PRIVATE_SENTINEL')
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
  it('超限取消流读取，逐字节UTF8可读', async () => {
    let cancelled = false
    const stream = new ReadableStream({ start(c) { c.enqueue(new Uint8Array(262145)) }, cancel() { cancelled = true } })
    await expect(requestDockerProjectActionJob(ids, 'start', vi.fn().mockResolvedValue(new Response(stream, { status: 202, headers: { 'Content-Type': 'application/json' } })))).rejects.toMatchObject(unknown)
    expect(cancelled).toBe(true)
    const stream2 = new ReadableStream({ start(c) { for (const byte of new TextEncoder().encode(JSON.stringify(job))) c.enqueue(new Uint8Array([byte])); c.close() } })
    await expect(requestDockerProjectActionJob(ids, 'start', vi.fn().mockResolvedValue(new Response(stream2, { status: 202, headers: { 'Content-Type': 'application/json; charset=utf-8' } })))).resolves.toEqual(job)
  })
})

describe('精确详情完整结果验证', () => {
  it('精确GET、signal、不缓存、不重发', async () => {
    const signal = new AbortController().signal, fetcher = vi.fn().mockResolvedValue(response(completed, 200))
    await expect(requestDockerProjectActionJobStatus(job.id, ids, 'start', signal, fetcher)).resolves.toEqual(completed)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/jobs/project-1', expect.objectContaining({ method: 'GET', cache: 'no-store', redirect: 'error', credentials: 'same-origin', signal }))
  })
  it('集合允许换序，不能缺失、重复或增加', async () => {
    await expect(read({ ...completed, projectActionResult: { ...result, items: [...result.items].reverse() } })).resolves.toBeTruthy()
    for (const items of [result.items.slice(0, 1), [...result.items, result.items[0]], [result.items[0], { ...result.items[1], containerId: 'c'.repeat(64) }]]) {
      await expect(read({ ...completed, projectActionResult: { ...result, items } })).rejects.toMatchObject(invalid)
    }
  })
  it.each(['start', 'stop', 'restart'] as const)('%s目标状态精确匹配', async action => {
    const state = action === 'stop' ? 'stopped' : 'running'
    const value = { ...completed, type: `docker-project-${action}`, projectActionResult: { ...result, action, items: result.items.map(item => ({ ...item, state })) } }
    await expect(read(value, ids, action)).resolves.toEqual(value)
    await expect(read({ ...value, projectActionResult: { ...value.projectActionResult, items: value.projectActionResult.items.map(item => ({ ...item, state: state === 'running' ? 'stopped' : 'running' })) } }, ids, action)).rejects.toMatchObject(invalid)
  })
  it('failed/interrupted可无结果；有结果须匹配汇总', async () => {
    for (const status of ['failed', 'interrupted']) await expect(read({ ...completed, status, progress: 0, projectActionResult: undefined, errorCode: 'JOB_EXECUTION_TIMEOUT' })).resolves.toMatchObject({ status })
    const mismatch = { ...result.items[1], outcome: 'state-mismatch', state: 'stopped', errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }
    const unknownItem = { ...result.items[1], outcome: 'unknown', state: 'unknown', errorCode: 'JOB_EXECUTION_INTERRUPTED' }
    for (const [status, errorCode, item] of [['failed', 'DOCKER_PROJECT_STATE_MISMATCH', mismatch], ['interrupted', 'JOB_EXECUTION_INTERRUPTED', unknownItem]] as const) {
      const value = { ...completed, status, progress: 0, errorCode, projectActionResult: { ...result, items: [result.items[0], item] } }
      await expect(read(value)).resolves.toEqual(value)
      await expect(read({ ...value, projectActionResult: result })).rejects.toMatchObject(invalid)
      await expect(read({ ...value, status: 'completed', progress: 100, errorCode: undefined })).rejects.toMatchObject(invalid)
    }
  })
  it.each([
    { id: 'other' }, { type: 'docker-project-restart' }, { reference: 'compose' }, { progress: 99 }, { projectActionResult: undefined }, { projectActionResult: null },
    { completedAt: undefined }, { completedAt: '2026-10-15T00:00:01.000000001Z' }, { persistenceStatus: 'pending' }, { errorCode: 'failed' },
  ])('坏身份/时间/完成元数据%j', async patch => { await expect(read({ ...completed, ...patch })).rejects.toMatchObject(invalid) })
  it.each([
    { projectId: 'other' }, { kind: 'compose' }, { action: 'restart' }, { extra: true }, { items: [] },
    { items: [{ ...result.items[0], containerId: 'A'.repeat(64) }, result.items[1]] },
    { items: [{ ...result.items[0], outcome: 'unknown', errorCode: 'RAW_SECRET' }, result.items[1]] },
    { items: [{ ...result.items[0], errorCode: '' }, result.items[1]] },
    { items: [{ ...result.items[0], name: 'PRIVATE_SENTINEL' }, result.items[1]] },
  ])('任何坏项拒绝整个结果%j', async patch => { await expect(read({ ...completed, projectActionResult: { ...result, ...patch } })).rejects.toMatchObject(invalid) })
  it('pending活动无结果；纳秒倒序拒绝；调用方变更不改变冻结集合', async () => {
    await expect(read({ ...job, persistenceStatus: 'pending' })).resolves.toMatchObject({ persistenceStatus: 'pending' })
    await expect(read({ ...job, persistenceStatus: 'pending', projectActionResult: result })).rejects.toMatchObject(invalid)
    await expect(read({ ...job, updatedAt: '2026-10-15T00:00:00.123456788Z' })).rejects.toMatchObject(invalid)
    const mutable = [...ids], fetcher = vi.fn().mockImplementation(async () => { mutable.pop(); return response(completed, 200) })
    await expect(requestDockerProjectActionJobStatus(job.id, mutable, 'start', undefined, fetcher)).resolves.toEqual(completed)
  })
})

describe('三份有界只读观察列表', () => {
  it('三个精确类型仅输出最小活动观察', async () => {
    const fetcher = listFetcher([job, { ...completed, id: 'done' }]), signal = new AbortController().signal
    await expect(requestDockerProjectActionJobs(signal, fetcher)).resolves.toEqual({ jobs: [{ id: job.id, type: job.type, status: 'queued' }], invalidCount: 0 })
    expect(fetcher.mock.calls.map(call => call[0])).toEqual(['start', 'stop', 'restart'].map(action => `/api/v1/jobs?type=docker-project-${action}`))
    for (const [, init] of fetcher.mock.calls) expect(init).toMatchObject({ method: 'GET', signal, cache: 'no-store', redirect: 'error' })
  })
  it('坏行、重复ID、错误类型、坏终态计数并保留有效活动', async () => {
    const fetcher = listFetcher([job, job, { ...job, id: 'bad', type: 'docker-project-stop' }, { ...completed, id: 'bad-result', projectActionResult: null }, { ...job, id: 'pending', persistenceStatus: 'pending' }])
    await expect(requestDockerProjectActionJobs(undefined, fetcher)).resolves.toEqual({ jobs: [{ id: job.id, type: job.type, status: 'queued' }, { id: 'pending', type: job.type, status: 'queued', persistenceStatus: 'pending' }], invalidCount: 3 })
  })
  it('合法500行大于64KiB可读，不复用详情预算', async () => {
    const rows = Array.from({ length: 500 }, (_, i) => ({ ...completed, id: `done-${i}` }))
    expect(JSON.stringify(rows).length).toBeGreaterThan(65536)
    await expect(requestDockerProjectActionJobs(undefined, listFetcher(rows))).resolves.toEqual({ jobs: [], invalidCount: 0 })
  })
  it('任一整页失败不提供干净空列表', async () => {
    for (const bad of [response({ jobs: [] }, 503), response({ jobs: [], extra: true }, 200), raw('{"jobs":[],"jobs":[]}', 200), response({ jobs: Array(1025).fill(job) }, 200), raw(' '.repeat(8 * 1024 * 1024 + 1), 200)]) {
      const fetcher = vi.fn().mockImplementation(async (url: string) => url.endsWith('-stop') ? bad : response({ jobs: [] }, 200))
      await expect(requestDockerProjectActionJobs(undefined, fetcher)).rejects.toMatchObject(invalid)
      expect(fetcher).toHaveBeenCalledTimes(3)
    }
  })
  it('三份空页无认领/接纳推断', async () => {
    await expect(requestDockerProjectActionJobs(undefined, listFetcher())).resolves.toEqual({ jobs: [], invalidCount: 0 })
  })
})

describe('边界与失败保持关闭', () => {
  it('128项接纳及完整回执合法，129项结果拒绝', async () => {
    const frozen = Array.from({ length: 128 }, (_, i) => i.toString(16).padStart(64, '0'))
    const fetcher = vi.fn().mockResolvedValue(response(job))
    await expect(requestDockerProjectActionJob(frozen, 'start', fetcher)).resolves.toEqual(job)
    const full = { ...result, items: frozen.map(containerId => ({ containerId, outcome: 'succeeded', state: 'running' })) }
    await expect(read({ ...completed, projectActionResult: full }, frozen)).resolves.toMatchObject({ status: 'completed' })
    await expect(read({ ...completed, projectActionResult: { ...full, items: [...full.items, { ...full.items[0], containerId: 'f'.repeat(64) }] } }, frozen)).rejects.toMatchObject(invalid)
  })
  it('稀疏输入在POST前拒绝', async () => {
    const fetcher = vi.fn()
    expect(isDockerProjectActionAdmissionRejected(await requestDockerProjectActionJob(Array(1), 'start', fetcher).catch(error => error))).toBe(true)
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('合法混合结果可以明确部分成功，但不能把未知视为反态失败', async () => {
    const frozen = [...ids, 'c'.repeat(64)]
    const items = [result.items[0], { ...result.items[1], outcome: 'state-mismatch', state: 'stopped', errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }, { containerId: frozen[2], outcome: 'unknown', state: 'unknown', errorCode: 'JOB_EXECUTION_INTERRUPTED' }]
    const value = { ...completed, status: 'interrupted', progress: 0, errorCode: 'JOB_EXECUTION_INTERRUPTED', projectActionResult: { ...result, items } }
    await expect(read(value, frozen)).resolves.toEqual(value)
    await expect(read({ ...value, status: 'failed', errorCode: 'DOCKER_PROJECT_STATE_MISMATCH' }, frozen)).rejects.toMatchObject(invalid)
  })
  it.each(['AGENT_RPC_REQUEST_INVALID', 'AGENT_PROTOCOL_MISMATCH', 'DOCKER_PROJECT_STATE_MISMATCH', 'CONTROL_INPUT_INVALID'])('非接纳拒绝码%s不可允许重发', async code => {
    await expect(requestDockerProjectActionJob(ids, 'start', vi.fn().mockResolvedValue(response({ code, message: 'private', requestId: 'r' }, 400)))).rejects.toMatchObject(unknown)
  })
  it('嵌套重复字段整条详情拒绝', async () => {
    const body = JSON.stringify(completed).replace('"outcome":"succeeded"', '"outcome":"unknown","outcome":"succeeded"')
    await expect(requestDockerProjectActionJobStatus(job.id, ids, 'start', undefined, vi.fn().mockResolvedValue(raw(body, 200)))).rejects.toMatchObject(invalid)
  })
  it('详情网络/重定向/非200只报固定核对失败', async () => {
    const redirected = response(completed, 200); Object.defineProperty(redirected, 'redirected', { value: true })
    for (const fetcher of [vi.fn().mockRejectedValue(new Error('PRIVATE_SENTINEL')), vi.fn().mockResolvedValue(redirected), vi.fn().mockResolvedValue(response(completed, 202))]) {
      const error = await requestDockerProjectActionJobStatus(job.id, ids, 'start', undefined, fetcher).catch(error => error)
      expect(error).toMatchObject(invalid)
      expect(String(error) + JSON.stringify(error)).not.toContain('PRIVATE_SENTINEL')
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
  it('三页独立预算且跨类型重复ID计为不完整', async () => {
    const fetcher = vi.fn().mockImplementation(async (url: string) => response({ jobs: [{ ...job, type: url.split('type=')[1] }] }, 200))
    await expect(requestDockerProjectActionJobs(undefined, fetcher)).resolves.toEqual({ jobs: [{ id: job.id, type: job.type, status: 'queued' }], invalidCount: 2 })
  })
})
