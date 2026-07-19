import { describe, expect, it, vi } from 'vitest'

import { requestCapabilities } from './system'

describe('requestCapabilities', () => {
  it('返回符合能力契约的只读系统数据', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          hostname: 'DH4300-PLUS',
          architecture: 'arm64',
          docker: true,
          cgroupVersion: 2,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestCapabilities(fetcher)).resolves.toMatchObject({
      hostname: 'DH4300-PLUS',
      architecture: 'arm64',
      docker: true,
    })
    expect(fetcher).toHaveBeenCalledWith('/api/v1/system/capabilities', expect.any(Object))
  })

  it('将服务端稳定错误码保留给界面的降级状态', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          code: 'SYSTEM_CAPABILITIES_UNAVAILABLE',
          message: '系统能力暂不可用，请确认 Agent 已启动。',
          requestId: 'req-test',
        }),
        { status: 503, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestCapabilities(fetcher)).rejects.toMatchObject({
      code: 'SYSTEM_CAPABILITIES_UNAVAILABLE',
      requestId: 'req-test',
    })
  })
})
