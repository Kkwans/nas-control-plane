import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSite, requestIgnoredSites, requestSites, updateSite } from './control'
import { parseSiteProfileResponse } from './siteResponse'

const profile = {
  projectId: 'manual:1', name: '站点', description: '', iconUrl: '/icons/a.png', category: '',
  primaryPort: 0, launchUrl: '', favorite: false, sortOrder: 0, hidden: false,
}
const site = { ...profile, id: 'manual:1', state: 'running', source: 'manual', lastVisitedAt: null, ports: [] }
const list = { collectedAt: '2026-10-13T12:00:00Z', sites: [site] }
const invalid = { code: 'CONTROL_RESPONSE_INVALID' }

function respond(value: unknown, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(value), { status }))
  vi.stubGlobal('fetch', fetch)
  return fetch
}

afterEach(() => vi.unstubAllGlobals())

describe('站点列表响应边界', () => {
  it.each([null, true, 'bad', [], {}, { sites: null }, { sites: {} }, { sites: 'bad' }, { sites: [] }, { ...list, collectedAt: '' }, { ...list, collectedAt: 'yesterday' }])('拒绝非法集合而非伪造空快照：%j', async (value) => {
    respond(value)
    await expect(requestSites()).rejects.toMatchObject(invalid)
  })

  it.each([
    null, {}, { ...site, id: '' }, { ...site, projectId: null }, { ...site, name: ' ' },
    { ...site, favorite: 1 }, { ...site, hidden: 'false' }, { ...site, sortOrder: 1.5 },
    { ...site, primaryPort: -1 }, { ...site, primaryPort: 65536 }, { ...site, source: 'unknown' },
    { ...site, lastVisitedAt: undefined }, { ...site, lastVisitedAt: 'invalid' }, { ...site, state: 1 },
    { ...site, ports: {} }, { ...site, ports: [0] }, { ...site, ports: [65536] }, { ...site, ports: [1.5] },
    { ...site, description: {} }, { ...site, iconUrl: 7 }, { ...site, category: false }, { ...site, launchUrl: [] },
  ])('坏行导致整份列表拒绝：%j', async (value) => {
    respond({ ...list, sites: [site, value] })
    await expect(requestSites()).rejects.toMatchObject(invalid)
  })

  it('保留空集合、未知附加字段以及旧版本 nullish 默认值', async () => {
    respond({ ...list, extra: 'server-added', discovery: null, sites: [{ ...site, state: 'future-state', description: null, iconUrl: null, launchUrl: null, category: null, ports: null, future: true }] })
    await expect(requestSites()).resolves.toMatchObject({ extra: 'server-added', sites: [{ state: 'future-state', ports: [], description: '', iconUrl: '', launchUrl: '', category: '', future: true }], discovery: { status: 'unavailable', candidateCount: 1, verifiedCount: 1 } })
    respond({ ...list, sites: [] })
    await expect(requestSites()).resolves.toMatchObject({ sites: [], discovery: { candidateCount: 0 } })
  })

  it.each([
    [], 'bad', { status: 'future' }, { probeAvailable: 1 }, { candidateCount: -1 }, { verifiedCount: 1.5 },
    { failedCount: '0' }, { issues: {} }, { issues: [null] }, { issues: [{}] },
    { issues: [{ siteId: '1', projectId: '1', name: '1', reason: '1', ports: ['80'] }] },
  ])('拒绝已提供但形状错误的发现摘要：%j', async (discovery) => {
    respond({ ...list, discovery })
    await expect(requestSites()).rejects.toMatchObject(invalid)
  })

  it.each(['partial', 'unavailable'])('保留 %s 下的手工站点，不推断统计等式', async (status) => {
    respond({ ...list, discovery: { status, candidateCount: 9, verifiedCount: 5, failedCount: null, probeAvailable: null, issues: null } })
    await expect(requestSites()).resolves.toMatchObject({ sites: [site], discovery: { status, candidateCount: 9, verifiedCount: 5, failedCount: 0, probeAvailable: false, issues: [] } })
  })
})

describe('忽略列表响应边界', () => {
  it.each([null, {}, { sites: [] }, [null], [{}], [{ ...profile, projectId: '' }], [{ ...profile, ignored: false }], [{ ...profile, ignored: 'true' }], [{ ...profile, detectedTitle: null }], [{ ...profile, autoIconUrl: 3 }], [{ ...profile, localIconName: {} }], [{ ...profile, lastVisitedAt: false }], [{ ...profile, source: 'bad' }]])('拒绝非法 ignored 响应：%j', async (value) => {
    respond(value)
    await expect(requestIgnoredSites()).rejects.toMatchObject(invalid)
  })

  it('允许空列表以及名称为空的自动忽略占位资料', async () => {
    respond([])
    await expect(requestIgnoredSites()).resolves.toEqual([])
    const placeholder = { ...profile, projectId: 'compose:missing', name: '', source: 'auto', ignored: true, lastVisitedAt: null, detectedTitle: '', autoIconUrl: '', localIconName: '', extra: 3 }
    respond([placeholder])
    await expect(requestIgnoredSites()).resolves.toEqual([placeholder])
  })

  it('允许旧版本不带额外 metadata 的资料，坏行不被静默过滤', async () => {
    respond([profile])
    await expect(requestIgnoredSites()).resolves.toEqual([profile])
    respond([profile, { ...profile, favorite: null }])
    await expect(requestIgnoredSites()).rejects.toMatchObject(invalid)
  })
})

describe('站点读取传输语义', () => {
  it.each([requestSites, requestIgnoredSites])('保留 signal、HTTP 错误码与取消', async (request) => {
    const controller = new AbortController()
    const fetch = respond({ code: 'SITES_READ_FAILED', message: 'unavailable', requestId: 'req1' }, 503)
    await expect(request(controller.signal)).rejects.toMatchObject({ code: 'SITES_READ_FAILED', requestId: 'req1' })
    expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ signal: controller.signal, credentials: 'same-origin' }))
    const aborted = new DOMException('Aborted', 'AbortError')
    fetch.mockRejectedValueOnce(aborted)
    await expect(request(controller.signal)).rejects.toBe(aborted)
  })

  it.each([requestSites, requestIgnoredSites])('拒绝 204 与损坏 JSON，不重试或发起写入', async (request) => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValueOnce(new Response(null, { status: 204 })).mockResolvedValueOnce(new Response('{'))
    vi.stubGlobal('fetch', fetch)
    await expect(request()).rejects.toMatchObject(invalid)
    await expect(request()).rejects.toMatchObject(invalid)
    expect(fetch).toHaveBeenCalledTimes(2)
    for (const [, init] of fetch.mock.calls) expect(init?.method).toBeUndefined()
  })
})

describe('写入身份解析与传输', () => {
  it('POST/PUT 坏响应标记未知并保留请求目标与传输行为', async () => {
    const controller = new AbortController()
    const fetch = respond({})
    await expect(createSite(profile, controller.signal)).rejects.toMatchObject({ code: 'SITE_WRITE_UNKNOWN', operation: 'create' })
    fetch.mockResolvedValueOnce(new Response('{}'))
    const siteId = 'compose:a/b%20c'
    await expect(updateSite(siteId, profile, controller.signal)).rejects.toMatchObject({ code: 'SITE_WRITE_UNKNOWN', operation: 'update', siteId })
    expect(fetch).toHaveBeenNthCalledWith(1, '/api/v1/sites', expect.objectContaining({ method: 'POST', signal: controller.signal }))
    expect(fetch).toHaveBeenNthCalledWith(2, `/api/v1/sites/${encodeURIComponent(siteId)}`, expect.objectContaining({ method: 'PUT', signal: controller.signal }))
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('创建要求服务端的非空 manual 身份', () => {
    expect(parseSiteProfileResponse(profile, { operation: 'create' })).toEqual(profile)
    for (const value of [{ ...profile, projectId: 'manual:' }, { ...profile, projectId: 'compose:1' }, { ...profile, source: 'auto' }, { ...profile, name: '' }, {}]) {
      expect(() => parseSiteProfileResponse(value, { operation: 'create' })).toThrow(expect.objectContaining(invalid))
    }
  })

  it('更新要求原始身份精确匹配，不重复 URI 解码', () => {
    const siteId = 'compose:a/b%20c'
    expect(parseSiteProfileResponse({ ...profile, projectId: siteId }, { operation: 'update', siteId }).projectId).toBe(siteId)
    expect(() => parseSiteProfileResponse({ ...profile, projectId: decodeURIComponent(siteId) }, { operation: 'update', siteId })).toThrow(expect.objectContaining(invalid))
  })
})
