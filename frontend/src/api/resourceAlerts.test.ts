import { afterEach, describe, expect, it, vi } from 'vitest'
import { requestResourceAlerts } from './control'
import { alertsFixture } from '@/domain/monitoring/alerts.fixture'
afterEach(() => vi.unstubAllGlobals())
describe('资源提示只读传输契约', () => {
  it('reuses same-origin credentials and passes cancellation without writes', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(alertsFixture())))
    vi.stubGlobal('fetch', fetch); const controller = new AbortController()
    await expect(requestResourceAlerts(controller.signal)).resolves.toEqual(alertsFixture())
    expect(fetch).toHaveBeenCalledWith('/api/v1/monitoring/alerts', { signal: controller.signal, credentials: 'same-origin', headers: { Accept: 'application/json' } })
  })
  it('rejects malformed JSON, missing nullable fields, 204 and empty successes', async () => {
    const missing = alertsFixture(); delete (missing.rules.cpu as unknown as Record<string, unknown>).sampleAt
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response('{'))
      .mockResolvedValueOnce(new Response(JSON.stringify(missing)))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response('{}'))
    vi.stubGlobal('fetch', fetch)
    for (let i = 0; i < 4; i++) await expect(requestResourceAlerts()).rejects.toMatchObject({ code: 'MONITORING_ALERTS_RESPONSE_INVALID' })
  })
  it('preserves stable provider failure and abort errors', async () => {
    const aborted = new DOMException('Aborted', 'AbortError')
    vi.stubGlobal('fetch', vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'MONITORING_ALERTS_UNAVAILABLE', message: 'fixture' }), { status: 503 }))
      .mockRejectedValueOnce(aborted))
    await expect(requestResourceAlerts()).rejects.toMatchObject({ code: 'MONITORING_ALERTS_UNAVAILABLE' })
    await expect(requestResourceAlerts()).rejects.toBe(aborted)
  })
})
