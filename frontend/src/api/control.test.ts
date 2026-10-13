import { afterEach, describe, expect, it, vi } from 'vitest'

import { requestMetricSamples, requestSites } from './control'

describe('用户只读 API 传输契约', () => {
  afterEach(() => vi.unstubAllGlobals())

  const policy = { minLength: 8, requireUppercase: false, requireLowercase: true, requireDigit: true, requireSpecial: false }

  it('两个 GET 独立返回并原样传递 signal，不携带密码或写入请求', async () => {
    const { requestUsers, requestPasswordPolicy } = await import('./control')
    const controller = new AbortController()
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response('[]'))
      .mockResolvedValueOnce(new Response(JSON.stringify(policy)))
    vi.stubGlobal('fetch', fetch)
    await expect(requestUsers(controller.signal)).resolves.toEqual([])
    await expect(requestPasswordPolicy(controller.signal)).resolves.toEqual(policy)
    expect(fetch.mock.calls.map(([path]) => path)).toEqual(['/api/v1/users', '/api/v1/users/password-policy'])
    expect(fetch).toHaveBeenCalledTimes(2)
    for (const [, init] of fetch.mock.calls) {
      expect(init).toMatchObject({ signal: controller.signal, credentials: 'same-origin', headers: { Accept: 'application/json' } })
      expect(init?.method ?? 'GET').toBe('GET')
      expect(init?.body).toBeUndefined()
    }
  })

  it.each(['requestUsers', 'requestPasswordPolicy'] as const)('%s 保留服务端错误码和取消，不重试', async (name) => {
    const api = await import('./control')
    const aborted = new DOMException('Aborted', 'AbortError')
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'USER_MANAGEMENT_UNAVAILABLE', message: 'fixture failure', requestId: 'fixture-request' }), { status: 503 }))
      .mockRejectedValueOnce(aborted)
    vi.stubGlobal('fetch', fetch)
    await expect(api[name]()).rejects.toMatchObject({ code: 'USER_MANAGEMENT_UNAVAILABLE', requestId: 'fixture-request' })
    await expect(api[name]()).rejects.toBe(aborted)
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it.each(['requestUsers', 'requestPasswordPolicy'] as const)('%s 将损坏 JSON、204 和非法结构统一为稳定读取错误', async (name) => {
    const api = await import('./control')
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response('{'))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response('{}'))
    vi.stubGlobal('fetch', fetch)
    for (let index = 0; index < 3; index++) {
      await expect(api[name]()).rejects.toMatchObject({ code: 'CONTROL_RESPONSE_INVALID' })
    }
    expect(fetch).toHaveBeenCalledTimes(3)
    for (const [, init] of fetch.mock.calls) expect(init?.method ?? 'GET').toBe('GET')
  })
})

describe('site discovery API', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('keeps discovery diagnostics and normalizes issue ports', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      collectedAt: '2026-08-11T08:00:00Z',
      sites: [],
      discovery: {
        status: 'partial', probeAvailable: true, candidateCount: 2,
        verifiedCount: 1, failedCount: 1,
        issues: [{ siteId: 'compose:demo', projectId: 'compose:demo', name: 'demo', reason: '端口未返回可识别的网页' }],
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))

    await expect(requestSites()).resolves.toMatchObject({
      discovery: {
        status: 'partial', candidateCount: 2, verifiedCount: 1, failedCount: 1,
        issues: [{ projectId: 'compose:demo', ports: [] }],
      },
    })
  })

  it('falls back safely while an older server is rolling forward', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      collectedAt: '2026-08-11T08:00:00Z',
      sites: [{
        id: 'manual:1', projectId: 'manual:1', name: '手动站点', state: 'running',
        primaryPort: 0, favorite: false, sortOrder: 0, lastVisitedAt: null,
        hidden: false, source: 'manual',
      }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))

    await expect(requestSites()).resolves.toMatchObject({
      discovery: { status: 'unavailable', candidateCount: 1, verifiedCount: 1, failedCount: 0 },
      sites: [{ ports: [], description: '', iconUrl: '', category: '', launchUrl: '' }],
    })
  })
})

describe('monitoring history API', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('keeps disk I/O and temperature fields from the shared MetricSample contract', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify([
      {
        collectedAt: '2026-08-24T00:00:00Z',
        cpuPercent: 12.5,
        memoryPercent: 34.5,
        load1: 0.4,
        diskPercent: 62,
        networkReceiveBytes: 100,
        networkTransmitBytes: 200,
        diskReadBytes: 300,
        diskWriteBytes: 400,
        temperatures: [{ name: 'soc-thermal', temperatureCelsius: 42.25 }],
      },
    ]), { status: 200, headers: { 'Content-Type': 'application/json' } })))

    await expect(requestMetricSamples('1h')).resolves.toEqual([expect.objectContaining({
      diskReadBytes: 300,
      diskWriteBytes: 400,
      temperatures: [{ name: 'soc-thermal', temperatureCelsius: 42.25 }],
    })])
  })
})

describe('monitoring history bounded response errors', () => {
  afterEach(() => vi.unstubAllGlobals())

  it.each([
    [422, 'MONITORING_HISTORY_LIMIT_EXCEEDED'],
    [500, 'MONITORING_HISTORY_INVALID'],
    [500, 'MONITORING_READ_FAILED'],
    [400, 'MONITORING_RANGE_INVALID'],
  ])('preserves the stable error code for HTTP %s / %s', async (status, code) => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ code, message: 'history unavailable' }), { status, headers: { 'Content-Type': 'application/json' } })))
    await expect(requestMetricSamples('7d')).rejects.toMatchObject({ code })
  })
})

describe('站点写入响应未知结果', () => {
  afterEach(() => vi.unstubAllGlobals())
  const input = { name: 'Demo', description: '', iconUrl: '', category: '', primaryPort: 8080, launchUrl: '', favorite: false, sortOrder: 0, hidden: false }

  it.each([{}, null, [], { ...input, projectId: 'other' }, { ...input, projectId: 'manual:' }])('POST 坏成功响应 %j 不产生可用身份', async (payload) => {
    const { createSite } = await import('./control')
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), { status: 201 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(createSite(input)).rejects.toMatchObject({ code: 'SITE_WRITE_UNKNOWN', operation: 'create', siteId: undefined })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each(['broken-json', 'no-body'])('成功状态的 %s 仍归为未知，而不是普通失败', async (kind) => {
    const { createSite } = await import('./control')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(kind === 'no-body' ? new Response(null, { status: 204 }) : new Response('{', { status: 201 })))
    await expect(createSite(input)).rejects.toMatchObject({ code: 'SITE_WRITE_UNKNOWN', operation: 'create' })
  })

  it('PUT 保留原目标，拒绝错误身份；编码身份不会被双重解码', async () => {
    const { updateSite } = await import('./control')
    const id = 'manual:中文/%2F'
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ ...input, projectId: 'manual:other' })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ...input, projectId: id })))
    vi.stubGlobal('fetch', fetchMock)
    await expect(updateSite(id, input)).rejects.toMatchObject({ code: 'SITE_WRITE_UNKNOWN', operation: 'update', siteId: id })
    await expect(updateSite(id, input)).resolves.toMatchObject({ projectId: id })
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/sites/${encodeURIComponent(id)}`)
  })

  it('合法 POST 返回手工 ID，HTTP 失败保留原始错误码', async () => {
    const { createSite } = await import('./control')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ ...input, projectId: 'manual:demo', source: 'manual' }), { status: 201 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'INPUT_INVALID', message: 'invalid' }), { status: 400 })))
    await expect(createSite(input)).resolves.toMatchObject({ projectId: 'manual:demo' })
    await expect(createSite(input)).rejects.toMatchObject({ code: 'INPUT_INVALID' })
  })
})
