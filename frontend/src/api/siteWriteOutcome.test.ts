import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSite, updateSite, type SiteProfileInput } from './control'

const profile = { name: '站点', description: '', iconUrl: '', category: '', primaryPort: 8080, launchUrl: '', favorite: false, sortOrder: 0, hidden: false }
const id = 'manual:a/b%20c'
const result = { ...profile, projectId: id, source: 'manual' }
const error = (code: string) => ({ code, message: 'untrusted server text', requestId: 'fixture' })
afterEach(() => vi.unstubAllGlobals())

for (const operation of ['create', 'update'] as const) {
  const expectedStatus = operation === 'create' ? 201 : 200
  const submit = (input: SiteProfileInput = profile, signal?: AbortSignal) => operation === 'create' ? createSite(input, signal) : updateSite(id, input, signal)
  const unknown = { code: 'SITE_WRITE_UNKNOWN', operation, siteId: operation === 'update' ? id : undefined }
  const respond = (response: Response) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response)
    vi.stubGlobal('fetch', fetch)
    return fetch
  }
  describe(`站点 ${operation} 发送结果`, () => {
    it('精确状态、身份、signal 与禁止重定向', async () => {
      const fetch = respond(new Response(JSON.stringify(result), { status: expectedStatus }))
      const signal = new AbortController().signal
      await expect(submit(profile, signal)).resolves.toMatchObject(result)
      expect(fetch).toHaveBeenCalledOnce()
      expect(fetch).toHaveBeenCalledWith(operation === 'create' ? '/api/v1/sites' : `/api/v1/sites/${encodeURIComponent(id)}`, expect.objectContaining({
        method: operation === 'create' ? 'POST' : 'PUT', redirect: 'error', credentials: 'same-origin', signal,
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' }, body: JSON.stringify(profile),
      }))
    })
    it.each([new TypeError('offline'), new DOMException('aborted', 'AbortError')])('发送后异常仅返回未知，不泄露底层信息', async (failure) => {
      const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(failure)
      vi.stubGlobal('fetch', fetch)
      await expect(submit()).rejects.toMatchObject(unknown)
      expect(fetch).toHaveBeenCalledOnce()
    })
    it.each([200, 201, 202, 204, 307, 308, 400, 500, 502].filter((status) => status !== expectedStatus))('意外状态 %s 不能证明写入结果', async (status) => {
      const fetch = respond(new Response(status === 204 ? null : JSON.stringify(result), { status }))
      await expect(submit()).rejects.toMatchObject(unknown)
      expect(fetch).toHaveBeenCalledOnce()
    })
    it('防御已被重定向的合法形状响应', async () => {
      const response = new Response(JSON.stringify(result), { status: expectedStatus })
      Object.defineProperty(response, 'redirected', { value: true })
      respond(response)
      await expect(submit()).rejects.toMatchObject(unknown)
    })
    it.each(['', '{', '{}', 'null', '[]'])('坏成功体 %s 维持未知', async (body) => {
      respond(new Response(body, { status: expectedStatus }))
      await expect(submit()).rejects.toMatchObject(unknown)
    })
    it.each(['SITE_INPUT_INVALID', 'SITE_ICON_SAVE_FAILED', 'constructor', 'toString', 'UNKNOWN'])('未经证明的拒绝 %s 维持未知', async (code) => {
      respond(new Response(JSON.stringify(error(code)), { status: 400 }))
      await expect(submit()).rejects.toMatchObject(unknown)
    })
    it.each([
      [401, 'AUTH_UNAUTHORIZED'], [503, 'AUTH_UNAVAILABLE'], [403, 'HTTP_CROSS_ORIGIN_REJECTED'],
      [400, 'CONTROL_INPUT_INVALID'], [413, 'HTTP_REQUEST_BODY_TOO_LARGE'], [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'], [503, 'SITES_UNAVAILABLE'],
    ] as const)('只有已核实的 %s/%s 拒绝可普通失败', async (status, code) => {
      respond(new Response(JSON.stringify(error(code)), { status }))
      await expect(submit()).rejects.toMatchObject({ code, requestId: 'fixture' })
      respond(new Response(JSON.stringify(error(code)), { status: 502 }))
      await expect(submit()).rejects.toMatchObject(unknown)
      respond(new Response(JSON.stringify({ code }), { status }))
      await expect(submit()).rejects.toMatchObject(unknown)
    })
    it('非 JSON 网关错误不冒充失败', async () => {
      respond(new Response('Bad Gateway', { status: 502 }))
      await expect(submit()).rejects.toMatchObject(unknown)
    })
    it('未知拒绝丢弃响应说明、requestId 和底层原因', async () => {
      respond(new Response(JSON.stringify(error('SITE_INPUT_INVALID')), { status: 400 }))
      const caught = await submit().catch((value: unknown) => value)
      expect(caught).toMatchObject(unknown)
      expect(caught).not.toHaveProperty('requestId')
      expect(caught).not.toHaveProperty('cause')
      expect(String(caught)).not.toContain('untrusted server text')
      expect(JSON.stringify(caught)).not.toContain('fixture')
    })
    it('本地 JSON 构造失败不发送，也不冒充已派发未知', () => {
      const fetch = vi.fn<typeof globalThis.fetch>()
      vi.stubGlobal('fetch', fetch)
      const input = { ...profile, toJSON() { throw new Error('local serialization') } }
      expect(() => submit(input)).toThrow('local serialization')
      expect(fetch).not.toHaveBeenCalled()
    })
  })
}
