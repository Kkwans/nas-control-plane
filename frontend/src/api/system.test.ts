import { describe, expect, it, vi } from 'vitest'

import { NcpApiError, loginRoot, requestCapabilities, requestSystemSummary } from './system'

describe('NCP API client', () => {
  it('requests protected system data with same-origin credentials', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          hostname: 'DH4300-PLUS',
          architecture: 'arm64',
          docker: true,
          compose: true,
          systemd: true,
          journald: true,
          cgroupVersion: 2,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestCapabilities(fetcher)).resolves.toMatchObject({ hostname: 'DH4300-PLUS', docker: true })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/system/capabilities',
      expect.objectContaining({ credentials: 'same-origin' }),
    )
  })

  it('sends Root credentials only in the login request body and accepts an HttpOnly-cookie session response', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ user: { id: 1, username: 'root-admin', role: 'root' }, expiresAt: '2026-07-24T12:00:00Z' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const password = `${Date.now()}-${Math.random()}-${performance.now()}`
    await expect(loginRoot({ username: 'root-admin', password }, fetcher)).resolves.toMatchObject({
      user: { role: 'root' },
    })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/auth/login',
      expect.objectContaining({ method: 'POST', credentials: 'same-origin' }),
    )
  })

  it('retains stable server error codes for UI states', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ code: 'SYSTEM_SUMMARY_UNAVAILABLE', message: 'unavailable', requestId: 'req-test' }),
        { status: 503, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestSystemSummary(fetcher)).rejects.toMatchObject({
      code: 'SYSTEM_SUMMARY_UNAVAILABLE',
      requestId: 'req-test',
    })
  })
})
