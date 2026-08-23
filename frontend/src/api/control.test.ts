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
