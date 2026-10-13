import { afterEach, describe, expect, it, vi } from 'vitest'

import { requestMetricSamples, requestSites } from './control'

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
