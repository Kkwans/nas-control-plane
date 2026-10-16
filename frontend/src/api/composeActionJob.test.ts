import { describe, expect, it, vi } from 'vitest'
import { NcpApiError } from './systemTransport'
import { isComposeActionAdmissionRejected, normalizeComposeProjectId, requestComposeActionJob, requestComposeActionJobStatus, requestComposeActionJobs, type ComposeAction, type ComposeActionJobInput } from './composeActionJob'
const input = { projectId: 'project %2F/中文', workingDirectory: '/volume1/apps/project', configFiles: ['compose.yaml', 'override.yml'] }
const job = { id: 'compose-1', type: 'compose-start', reference: input.projectId, cancellable: false, status: 'queued', artifactState: 'unknown', message: '任务已进入队列', progress: 0, createdAt: '2026-10-15T00:00:00.123456789Z', updatedAt: '2026-10-15T00:00:00.123456789Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const result = { projectId: input.projectId, action: 'start', state: 'running', serviceCount: 2 }
const completed = { ...job, status: 'completed', progress: 100, updatedAt: '2026-10-15T00:00:01Z', completedAt: '2026-10-15T00:00:01Z', composeActionResult: result }
const raw = (body: string | Uint8Array, status = 202) => new Response(body as BodyInit, { status, headers: { 'Content-Type': 'application/json' } })
const response = (body: unknown, status = 202) => raw(JSON.stringify(body), status)
const unknown = { code: 'COMPOSE_ACTION_ADMISSION_UNKNOWN' }, invalid = { code: 'COMPOSE_ACTION_JOB_RESPONSE_INVALID' }
const read = (value: unknown, action: ComposeAction = 'start') => requestComposeActionJobStatus(job.id, input.projectId, action, undefined, vi.fn().mockResolvedValue(response(value, 200)))
const listFetcher = (rows: unknown[] = []) => vi.fn().mockImplementation(async (url: string) => response({ jobs: url.endsWith('-start') ? rows : [] }, 200))

describe('Compose 单次接纳和冻结边界', () => {
  it.each(['start', 'stop', 'restart'] as const)('%s 精确入口仅编码一次和两个正文字段', async action => {
    const mutable = { ...input, projectId: ` ${input.projectId} `, configFiles: [...input.configFiles], extra: 'never send' }
    const fetcher = vi.fn().mockImplementation(async () => { mutable.configFiles.reverse(); mutable.projectId = 'changed'; return response({ ...job, type: `compose-${action}` }) })
    await expect(requestComposeActionJob(mutable, action, fetcher)).resolves.toMatchObject({ status: 'queued' })
    expect(fetcher).toHaveBeenCalledOnce()
    const [url, init] = fetcher.mock.calls[0]!
    expect(url).toBe(`/api/v1/docker/compose/projects/${encodeURIComponent(input.projectId)}/action-jobs/${action}`)
    expect(init).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error' })
    expect(init.headers.get('Content-Type')).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual({ workingDirectory: input.workingDirectory, configFiles: input.configFiles })
  })
  it('原始ID/UTF8/路径/文件数/正文超限在发送前拒绝', async () => {
    const bad = [null, { projectId: '' }, { projectId: ' '.repeat(257) + 'p' }, { projectId: '中'.repeat(86) }, { projectId: '\ud800' }, { projectId: 'a\n' }, { workingDirectory: null }, { workingDirectory: 'a'.repeat(4097) }, { workingDirectory: 'a\u0085' }, { configFiles: [] }, { configFiles: Array(1) }, { configFiles: Array(33).fill('a.yml') }, { configFiles: [null] }, { configFiles: ['a\u007f'] }, { configFiles: ['\udfff'] }, { configFiles: ['中'.repeat(1366)] }, { configFiles: Array(32).fill('a'.repeat(4096)) }]
    for (const patch of bad) {
      const fetcher = vi.fn()
      const error = await requestComposeActionJob((patch === null ? null : { ...input, ...patch }) as ComposeActionJobInput, 'start', fetcher).catch(error => error)
      expect(isComposeActionAdmissionRejected(error)).toBe(true)
      expect(fetcher).not.toHaveBeenCalled()
    }
    const fetcher = vi.fn()
    await expect(requestComposeActionJob(input, 'START' as ComposeAction, fetcher)).rejects.toMatchObject({ code: 'COMPOSE_ACTION_INVALID' })
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('合法上限及Go空白规范化', async () => {
    const boundary = { projectId: 'p'.repeat(256), workingDirectory: 'a'.repeat(4096), configFiles: Array.from({ length: 32 }, (_, i) => `${i}.yml`) }
    await expect(requestComposeActionJob(boundary, 'start', vi.fn().mockResolvedValue(response({ ...job, reference: boundary.projectId })))).resolves.toBeTruthy()
    expect(normalizeComposeProjectId('\u2000p\u3000')).toBe('p')
    expect(normalizeComposeProjectId('\ufeffp\ufeff')).toBe('\ufeffp\ufeff')
  })
  it.each([
    { id: '' }, { id: 'bad\n' }, { id: '\ud800' }, { type: 'compose-stop' }, { reference: 'other' }, { reference: null },
    { cancellable: true }, { artifactState: 'present' }, { status: 'running' }, { status: 'completed' }, { status: 'cancelled' },
    { message: null }, { message: '\ud800' }, { createdAt: '2026-02-30T00:00:00Z' }, { updatedAt: '2026-10-15T00:00:00.123456788Z' },
    { progress: 1 }, { downloadedBytes: 1 }, { totalBytes: 1 }, { speedBytes: 1 }, { layers: [] }, { layers: { fake: {} } },
    { progressLimited: true }, { completedAt: '' }, { persistenceStatus: 'pending' }, { cancellationStatus: 'requested' },
    { error: '' }, { errorCode: 'PRIVATE_SENTINEL' }, { composeActionResult: result }, { composeActionResult: null }, { extra: true }, { projectActionResult: {} },
  ])('畸形202未知%j', async fields => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...job, ...fields }))
    const error = await requestComposeActionJob(input, 'start', fetcher).catch(error => error)
    expect(error).toMatchObject(unknown)
    expect(isComposeActionAdmissionRejected(error)).toBe(false)
    expect(JSON.stringify(error) + String(error)).not.toContain('PRIVATE_SENTINEL')
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it.each([[400, 'COMPOSE_LIFECYCLE_INVALID'], [401, 'AUTH_UNAUTHORIZED'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'], [503, 'AGENT_DOCKER_CONTROL_UNAVAILABLE'], [503, 'JOB_QUEUE_FULL'], [503, 'JOB_PERSIST_FAILED'], [503, 'JOB_ENGINE_UNAVAILABLE']] as const)('仅精确%s/%s拒绝', async (status, code) => {
    const payload = { code, message: 'PRIVATE_SENTINEL', requestId: 'PRIVATE_SENTINEL' }
    const error = await requestComposeActionJob(input, 'start', vi.fn().mockResolvedValue(response(payload, status))).catch(error => error)
    expect(isComposeActionAdmissionRejected(error)).toBe(true)
    expect(error.code).toBe(code)
    expect(JSON.stringify(error) + String(error)).not.toContain('PRIVATE_SENTINEL')
    await expect(requestComposeActionJob(input, 'start', vi.fn().mockResolvedValue(response(payload, 500)))).rejects.toMatchObject(unknown)
  })
  it('伪造拒绝实例及未列出错误都未知', async () => {
    const spoof = new NcpApiError('JOB_QUEUE_FULL', 'PRIVATE_SENTINEL')
    expect(isComposeActionAdmissionRejected(spoof)).toBe(false)
    await expect(requestComposeActionJob(input, 'start', vi.fn().mockRejectedValue(spoof))).rejects.toMatchObject(unknown)
    for (const code of ['AGENT_PROTOCOL_MISMATCH', 'COMPOSE_LIFECYCLE_RECEIPT_INVALID', 'COMPOSE_ACTION_INVALID']) await expect(requestComposeActionJob(input, 'start', vi.fn().mockResolvedValue(response({ code, message: 'private', requestId: 'r' }, 400)))).rejects.toMatchObject(unknown)
  })
  it.each([200, 201, 301, 400, 404, 409, 500, 503])('非202/%s未知', async status => {
    await expect(requestComposeActionJob(input, 'start', vi.fn().mockResolvedValue(response(job, status)))).rejects.toMatchObject(unknown)
  })
  it('重复键、尾随、UTF8、BOM、深度、MIME、重定向及网络失败未知', async () => {
    const redirected = response(job); Object.defineProperty(redirected, 'redirected', { value: true })
    const malformed = [raw('{'), raw(JSON.stringify(job) + '{}'), raw(JSON.stringify(job).replace('"status":"queued"', '"status":"running","st\\u0061tus":"queued"')),
      raw('{"code":"UNKNOWN","code":"JOB_QUEUE_FULL","message":"error","requestId":"r"}', 503),
      response({ code: 'JOB_QUEUE_FULL', message: 'error', requestId: 'r', extra: true }, 503), raw(new Uint8Array([0xff])), raw(new Uint8Array([0xef, 0xbb, 0xbf, 0x7b, 0x7d])),
      raw('['.repeat(33) + '0' + ']'.repeat(33)), new Response(JSON.stringify(job), { status: 202, headers: { 'Content-Type': 'text/html' } }), redirected]
    for (const fetcher of [...malformed.map(value => vi.fn().mockResolvedValue(value)), vi.fn().mockRejectedValue(new Error('PRIVATE_SENTINEL'))]) {
      const error = await requestComposeActionJob(input, 'start', fetcher).catch(error => error)
      expect(error).toMatchObject(unknown)
      expect(String(error) + JSON.stringify(error)).not.toContain('PRIVATE_SENTINEL')
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
  it('超限取消流读取，逐字节UTF8可读', async () => {
    let cancelled = false
    const stream = new ReadableStream({ start(c) { c.enqueue(new Uint8Array(262145)) }, cancel() { cancelled = true } })
    await expect(requestComposeActionJob(input, 'start', vi.fn().mockResolvedValue(new Response(stream, { status: 202, headers: { 'Content-Type': 'application/json' } })))).rejects.toMatchObject(unknown)
    expect(cancelled).toBe(true)
    const stream2 = new ReadableStream({ start(c) { for (const byte of new TextEncoder().encode(JSON.stringify(job))) c.enqueue(new Uint8Array([byte])); c.close() } })
    await expect(requestComposeActionJob(input, 'start', vi.fn().mockResolvedValue(new Response(stream2, { status: 202, headers: { 'Content-Type': 'application/json; charset=utf-8' } })))).resolves.toEqual(job)
  })
})

describe('精确详情聚合结果验证', () => {
  it('精确GET、signal、不缓存、不重发', async () => {
    const signal = new AbortController().signal, fetcher = vi.fn().mockResolvedValue(response(completed, 200))
    await expect(requestComposeActionJobStatus(job.id, input.projectId, 'start', signal, fetcher)).resolves.toEqual(completed)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/jobs/compose-1', expect.objectContaining({ method: 'GET', cache: 'no-store', redirect: 'error', credentials: 'same-origin', signal }))
  })
  it.each(['start', 'stop', 'restart'] as const)('%s目标状态和计数边界', async action => {
    const state = action === 'stop' ? 'stopped' : 'running'
    for (const serviceCount of [1, 1024]) await expect(read({ ...completed, type: `compose-${action}`, composeActionResult: { ...result, action, state, serviceCount } }, action)).resolves.toBeTruthy()
    await expect(read({ ...completed, type: `compose-${action}`, composeActionResult: { ...result, action, state: state === 'running' ? 'stopped' : 'running' } }, action)).rejects.toMatchObject(invalid)
  })
  it('所有非completed状态禁止结果；pending仅活动', async () => {
    for (const status of ['queued', 'running', 'failed', 'interrupted']) {
      const terminal = status === 'failed' || status === 'interrupted'
      const value = { ...job, status, ...(terminal ? { completedAt: job.createdAt, errorCode: 'JOB_EXECUTION_TIMEOUT' } : {}) }
      await expect(read(value)).resolves.toBeTruthy()
      await expect(read({ ...value, composeActionResult: result })).rejects.toMatchObject(invalid)
    }
    await expect(read({ ...job, persistenceStatus: 'pending' })).resolves.toBeTruthy()
    await expect(read({ ...completed, persistenceStatus: 'pending' })).rejects.toMatchObject(invalid)
  })
  it.each([
    { id: 'other' }, { type: 'compose-restart' }, { reference: 'other' }, { reference: ` ${input.projectId}` }, { progress: 99 }, { composeActionResult: undefined }, { composeActionResult: null },
    { completedAt: undefined }, { completedAt: '2026-10-15T00:00:01.000000001Z' }, { persistenceStatus: 'pending' }, { errorCode: 'failed' }, { projectActionResult: {} },
  ])('坏身份/时间/完成元数据%j', async patch => { await expect(read({ ...completed, ...patch })).rejects.toMatchObject(invalid) })
  it.each([{ projectId: 'other' }, { action: 'restart' }, { state: 'exited' }, { extra: true }, { serviceCount: 0 }, { serviceCount: 1025 }, { serviceCount: 1.5 }, { serviceCount: '1' }, { serviceCount: null }, { serviceCount: undefined }, { items: [] }])('任何坏聚合拒绝整个结果%j', async patch => { await expect(read({ ...completed, composeActionResult: { ...result, ...patch } })).rejects.toMatchObject(invalid) })
  it('嵌套重复字段整条详情拒绝', async () => {
    const body = JSON.stringify(completed).replace('"serviceCount":2', '"serviceCount":1,"serviceCount":2')
    await expect(requestComposeActionJobStatus(job.id, input.projectId, 'start', undefined, vi.fn().mockResolvedValue(raw(body, 200)))).rejects.toMatchObject(invalid)
  })
  it('详情网络/重定向/非200只报固定核对失败', async () => {
    const redirected = response(completed, 200); Object.defineProperty(redirected, 'redirected', { value: true })
    for (const fetcher of [vi.fn().mockRejectedValue(new Error('PRIVATE_SENTINEL')), vi.fn().mockResolvedValue(redirected), vi.fn().mockResolvedValue(response(completed, 202))]) {
      const error = await requestComposeActionJobStatus(job.id, input.projectId, 'start', undefined, fetcher).catch(error => error)
      expect(error).toMatchObject(invalid)
      expect(String(error) + JSON.stringify(error)).not.toContain('PRIVATE_SENTINEL')
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
})

describe('三份有界只读观察列表', () => {
  const observation = { id: job.id, type: job.type, reference: job.reference, status: 'queued' }
  it('三个精确类型仅输出最小活动观察', async () => {
    const fetcher = listFetcher([job, { ...completed, id: 'done' }]), signal = new AbortController().signal
    await expect(requestComposeActionJobs(signal, fetcher)).resolves.toEqual({ jobs: [observation], invalidCount: 0 })
    expect(fetcher.mock.calls.map(call => call[0])).toEqual(['start', 'stop', 'restart'].map(action => `/api/v1/jobs?type=compose-${action}`))
    for (const [, init] of fetcher.mock.calls) expect(init).toMatchObject({ method: 'GET', signal, cache: 'no-store', redirect: 'error' })
  })
  it('坏行、重复ID、错误类型、坏终态计数并保留有效活动', async () => {
    const fetcher = listFetcher([job, job, { ...job, id: 'bad', type: 'compose-stop' }, { ...completed, id: 'bad-result', composeActionResult: null }, { ...job, id: 'pending', persistenceStatus: 'pending' }])
    await expect(requestComposeActionJobs(undefined, fetcher)).resolves.toEqual({ jobs: [observation, { ...observation, id: 'pending', persistenceStatus: 'pending' }], invalidCount: 3 })
  })
  it('合法500行大于64KiB可读', async () => {
    const rows = Array.from({ length: 500 }, (_, i) => ({ ...completed, id: `done-${i}` }))
    expect(JSON.stringify(rows).length).toBeGreaterThan(65536)
    await expect(requestComposeActionJobs(undefined, listFetcher(rows))).resolves.toEqual({ jobs: [], invalidCount: 0 })
  })
  it('任一整页失败不提供干净空列表', async () => {
    for (const bad of [response({ jobs: [] }, 503), response({ jobs: [], extra: true }, 200), raw('{"jobs":[],"jobs":[]}', 200), response({ jobs: Array(1025).fill(job) }, 200), raw(' '.repeat(8 * 1024 * 1024 + 1), 200)]) {
      const fetcher = vi.fn().mockImplementation(async (url: string) => url.endsWith('-stop') ? bad : response({ jobs: [] }, 200))
      await expect(requestComposeActionJobs(undefined, fetcher)).rejects.toMatchObject(invalid)
      expect(fetcher).toHaveBeenCalledTimes(3)
    }
  })
  it('空页无接纳推断；跨类型重复ID计不完整', async () => {
    await expect(requestComposeActionJobs(undefined, listFetcher())).resolves.toEqual({ jobs: [], invalidCount: 0 })
    const fetcher = vi.fn().mockImplementation(async (url: string) => response({ jobs: [{ ...job, type: url.split('type=')[1] }] }, 200))
    await expect(requestComposeActionJobs(undefined, fetcher)).resolves.toEqual({ jobs: [observation], invalidCount: 2 })
  })
})

describe('精确字节预算和只读输入边界', () => {
  it('64KiB正文精确接受，多一字节发送前拒绝', async () => {
    const configFiles = Array.from({ length: 16 }, () => 'a'.repeat(4096))
    const bounded = { ...input, workingDirectory: '/volume1/a', configFiles }
    const excess = new TextEncoder().encode(JSON.stringify({ workingDirectory: bounded.workingDirectory, configFiles })).length - 65536
    configFiles[15] = configFiles[15]!.slice(excess)
    const fetcher = vi.fn().mockResolvedValue(response(job))
    await expect(requestComposeActionJob(bounded, 'start', fetcher)).resolves.toEqual(job)
    expect(new TextEncoder().encode(fetcher.mock.calls[0]![1].body).length).toBe(65536)
    configFiles[15] += 'a'
    const blocked = vi.fn()
    expect(isComposeActionAdmissionRejected(await requestComposeActionJob(bounded, 'start', blocked).catch(error => error))).toBe(true)
    expect(blocked).not.toHaveBeenCalled()
  })
  it('坏只读身份不发请求；合法特殊jobID仅编码一次', async () => {
    for (const [id, project, action] of [['', input.projectId, 'start'], ['x\n', input.projectId, 'start'], ['x', ' ', 'start'], ['x', input.projectId, 'START']]) {
      const fetcher = vi.fn()
      await expect(requestComposeActionJobStatus(id!, project!, action as ComposeAction, undefined, fetcher)).rejects.toMatchObject(invalid)
      expect(fetcher).not.toHaveBeenCalled()
    }
    const id = 'job%2F/中文', fetcher = vi.fn().mockResolvedValue(response({ ...job, id }, 200))
    await expect(requestComposeActionJobStatus(id, ` ${input.projectId} `, 'start', undefined, fetcher)).resolves.toMatchObject({ id })
    expect(fetcher.mock.calls[0]![0]).toBe(`/api/v1/jobs/${encodeURIComponent(id)}`)
  })
})
