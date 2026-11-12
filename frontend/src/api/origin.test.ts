import { describe, expect, it, vi } from 'vitest'
import { loginRoot, logoutRoot } from './auth'
import { requestContainerAction } from './system'

describe('browser origin rejection', () => {
  it('preserves the stable 403 error and does not retry login, logout or no-body actions', async () => {
    const calls = [
      (fetcher: typeof fetch) => loginRoot({ username: 'test', password: 'fixture-only' }, fetcher),
      (fetcher: typeof fetch) => logoutRoot(fetcher),
      (fetcher: typeof fetch) => requestContainerAction('fixture', 'restart', fetcher),
    ]
    for (const call of calls) {
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
        code: 'HTTP_CROSS_ORIGIN_REJECTED',
        message: '请求来源校验失败，请从当前控制台页面重试。',
        requestId: 'req-origin',
      }), { status: 403, headers: { 'Content-Type': 'application/json' } }))
      await expect(call(fetcher)).rejects.toMatchObject({
        code: 'HTTP_CROSS_ORIGIN_REJECTED', requestId: 'req-origin',
      })
      expect(fetcher).toHaveBeenCalledTimes(1)
      expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ credentials: 'same-origin' })
    }
  })
})
