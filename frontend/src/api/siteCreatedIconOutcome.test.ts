import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { uploadCreatedSiteIcon } from './control'
import { SiteCreatedIconError } from './siteCreatedIconOutcome'
import { useSitesStore } from '@/stores/sites'
import { invalidateSession } from '@/session/sessionLifecycle'

const id = 'manual:fixture'
const icon = new File(['icon'], 'icon.svg', { type: 'image/svg+xml' })
const payload = { siteId: id, iconUrl: `/api/v1/sites/${id}/icon` }
const input = { name: 'Creator', description: '', iconUrl: '', category: '', primaryPort: 8080, launchUrl: '', favorite: false, sortOrder: 0, hidden: false }
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status })
const error = (code: string) => ({ code, message: 'private diagnostics', requestId: 'private-id' })
const respond = (value: Response) => {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(value)
  vi.stubGlobal('fetch', fetch)
  return fetch
}
beforeEach(() => { invalidateSession(); setActivePinia(createPinia()) })
afterEach(() => vi.unstubAllGlobals())

describe('创建专用图标结果边界', () => {
  it.each([payload.iconUrl, `/api/v1/sites/${encodeURIComponent(id)}/icon`])('接受匹配身份的本地地址 %s', async (iconUrl) => {
    const fetch = respond(response({ ...payload, iconUrl }))
    const signal = new AbortController().signal
    await expect(uploadCreatedSiteIcon(id, icon, signal)).resolves.toBeUndefined()
    expect(fetch).toHaveBeenCalledExactlyOnceWith(`/api/v1/sites/${encodeURIComponent(id)}/icon`, expect.objectContaining({
      method: 'POST', redirect: 'error', credentials: 'same-origin', signal,
    }))
    const init = fetch.mock.calls[0]![1]!
    expect(new Headers(init.headers).has('Content-Type')).toBe(false)
    expect((init.body as FormData).get('icon')).toBe(icon)
  })

  it.each([
    ['AUTH_UNAVAILABLE', 503], ['AUTH_UNAUTHORIZED', 401], ['HTTP_CROSS_ORIGIN_REJECTED', 403],
    ['SITE_ICON_UNAVAILABLE', 503], ['SITE_ICON_INVALID', 400], ['SITE_ICON_REQUIRED', 400],
    ['SITE_ICON_TYPE_UNSUPPORTED', 400], ['SITE_NOT_FOUND', 404], ['SITE_ICON_CONFLICT', 409],
  ] as const)('明确拒绝 %s/%s 不混淆为未知', async (code, status) => {
    respond(response(error(code), status))
    await expect(uploadCreatedSiteIcon(id, icon)).rejects.toMatchObject({ siteId: id, iconOutcome: 'rejected' })
  })

  it.each([
    [error('SITE_ICON_SAVE_FAILED'), 500], [error('SITE_ICON_CONFLICT'), 500], [error('SITE_ICON_INVALID'), 409],
    [{ code: 'SITE_ICON_CONFLICT' }, 409], [null, 409], [[], 409],
    [payload, 201], [{}, 200], [null, 200], [[], 200], [{ ...payload, siteId: 'manual:other' }, 200],
    [{ ...payload, iconUrl: 'https://example.invalid/icon' }, 200], [{ ...payload, iconUrl: '/api/v1/sites/manual:other/icon' }, 200],
    [{ ...payload, iconUrl: `${payload.iconUrl}?v=1` }, 200], [{ ...payload, iconUrl: '/api/v1/sites/%zz/icon' }, 200],
  ])('无法证明图标结果：%j / %s', async (value, status) => {
    respond(response(value, status as number))
    await expect(uploadCreatedSiteIcon(id, icon)).rejects.toMatchObject({ siteId: id, iconOutcome: 'unknown' })
  })

  it.each([new Response(null, { status: 204 }), new Response('{', { status: 200 }), new Response('Bad Gateway', { status: 502 })])('空体、坏 JSON 和代理失败保持未知', async (value) => {
    respond(value)
    await expect(uploadCreatedSiteIcon(id, icon)).rejects.toBeInstanceOf(SiteCreatedIconError)
  })

  it.each([new TypeError('private network error'), new DOMException('aborted', 'AbortError')])('发送失败不丢失已确认创建身份', async (failure) => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(failure))
    await expect(uploadCreatedSiteIcon(id, icon)).rejects.toMatchObject({ siteId: id, iconOutcome: 'unknown' })
  })

  it('拒绝已经跳转的响应，且部分结果不携带原始诊断', async () => {
    const value = response(payload)
    Object.defineProperty(value, 'redirected', { value: true })
    respond(value)
    const caught = await uploadCreatedSiteIcon(id, icon).catch((error: unknown) => error)
    expect(caught).toMatchObject({ siteId: id, iconOutcome: 'unknown' })
    expect(caught).not.toHaveProperty('cause')
    expect(caught).not.toHaveProperty('requestId')
    expect(caught).not.toHaveProperty('response')
  })
})

describe('真实 API→store 图标失败不能删除已创建站点', () => {
  it.each(['conflict', 'committed-500', 'committed-network-loss'] as const)('%s 保留并发获胜资料和图标，零 DELETE', async (outcome) => {
    let row: { name: string; icon: string } | undefined
    const writes: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (path: string, init: RequestInit = {}) => {
      const method = init.method ?? 'GET'
      if (method !== 'GET') writes.push(`${method} ${path}`)
      if (path === '/api/v1/sites' && method === 'POST') {
        row = { name: input.name, icon: '' }
        return response({ ...input, projectId: id, source: 'manual' }, 201)
      }
      if (path.endsWith('/icon')) {
        row = { name: 'Newer work from other tab', icon: outcome === 'conflict' ? 'winner.svg' : 'committed.svg' }
        if (outcome === 'committed-network-loss') throw new TypeError('response lost')
        return response(error(outcome === 'conflict' ? 'SITE_ICON_CONFLICT' : 'SITE_ICON_SAVE_FAILED'), outcome === 'conflict' ? 409 : 500)
      }
      if (method === 'DELETE') { row = undefined; return new Response(null, { status: 204 }) }
      throw new Error(`unexpected request: ${method} ${path}`)
    }))
    await expect(useSitesStore().create(input, icon)).rejects.toMatchObject({
      siteId: id, iconOutcome: outcome === 'conflict' ? 'rejected' : 'unknown',
    })
    expect(writes).toEqual(['POST /api/v1/sites', 'POST /api/v1/sites/manual%3Afixture/icon'])
    expect(row).toEqual({ name: 'Newer work from other tab', icon: outcome === 'conflict' ? 'winner.svg' : 'committed.svg' })
  })
})
