import { afterEach, describe, expect, it, vi } from 'vitest'
import { requestPreferences, updatePreferences, type UserPreferences } from './control'
import { parsePreferencesResponse, PreferencesWriteUnknownError } from './preferenceResponse'

const preferences: UserPreferences = {
  refreshIntervalSeconds: 5, interfaceDensity: 'comfortable', baseFontSize: 15, pageSize: 10,
  sidebarDefault: 'collapsed', linkOpenMode: 'new-tab', siteDefaultProtocol: 'http',
  chineseFont: 'system', latinFont: 'system',
  navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'],
}

describe('偏好设置响应契约', () => {
  it.each([null, undefined, [], true, 'preferences', {}])('拒绝不完整对象：%j', (value) => {
    expect(() => parsePreferencesResponse(value)).toThrow(expect.objectContaining({ code: 'CONTROL_RESPONSE_INVALID' }))
  })

  it.each(Object.keys(preferences))('不能省略必填字段 %s', (field) => {
    const value: Record<string, unknown> = { ...preferences }
    delete value[field]
    expect(() => parsePreferencesResponse(value)).toThrow(expect.objectContaining({ code: 'CONTROL_RESPONSE_INVALID' }))
  })

  it.each([
    ['refreshIntervalSeconds', 2, 300], ['baseFontSize', 13, 18], ['pageSize', 1, 200],
  ] as const)('%s 严格校验整数和服务端范围', (field, min, max) => {
    for (const value of [min - 1, max + 1, min + 0.5, `${min}`, null, undefined, NaN, Infinity, -Infinity, {}, true]) {
      expect(() => parsePreferencesResponse({ ...preferences, [field]: value })).toThrow(expect.objectContaining({ code: 'CONTROL_RESPONSE_INVALID' }))
    }
    for (const value of [min, max]) expect(parsePreferencesResponse({ ...preferences, [field]: value })[field]).toBe(value)
  })

  it.each([
    ['interfaceDensity', ['comfortable', 'compact']], ['sidebarDefault', ['collapsed', 'expanded']],
    ['linkOpenMode', ['new-tab', 'same-tab']], ['siteDefaultProtocol', ['http', 'https']],
    ['chineseFont', ['system', 'noto-sans-sc', 'microsoft-yahei', 'source-han-sans-sc', 'misans', 'harmonyos-sans-sc']],
    ['latinFont', ['system', 'manrope', 'inter', 'ibm-plex-sans']],
  ] as const)('%s 校验全部合法选项，不将非法值替换为默认值', (field, options) => {
    for (const value of ['', 'unknown', null, undefined, 0, true, {}]) {
      expect(() => parsePreferencesResponse({ ...preferences, [field]: value })).toThrow(expect.objectContaining({ code: 'CONTROL_RESPONSE_INVALID' }))
    }
    for (const value of options) expect(parsePreferencesResponse({ ...preferences, [field]: value })[field]).toBe(value)
  })

  it.each([null, undefined, {}, 'overview', 0, [null], Array(1), ['overview', 1], ['overview', {}], ['sites', 'sites']])('拒绝损坏顺序：%j', (navigationOrder) => {
    expect(() => parsePreferencesResponse({ ...preferences, navigationOrder })).toThrow(expect.objectContaining({ code: 'CONTROL_RESPONSE_INVALID' }))
  })

  it('保留服务端默认值、空数组、未来路由和未知字段，并复制对象与顺序数组', () => {
    expect(parsePreferencesResponse(preferences)).toEqual(preferences)
    for (const navigationOrder of [[], ['future-module', 'settings']]) {
      const value = Object.freeze({ ...preferences, navigationOrder: Object.freeze(navigationOrder), future: { revision: 2 } })
      const parsed = parsePreferencesResponse(value)
      expect(parsed).toEqual(value)
      expect(parsed).not.toBe(value)
      expect(parsed.navigationOrder).not.toBe(value.navigationOrder)
    }
  })

  it('无效保存响应标记结果未知，不表示保存失败', () => {
    expect(() => parsePreferencesResponse({ ...preferences, navigationOrder: {} }, 'write')).toThrow(PreferencesWriteUnknownError)
  })
})

describe('偏好设置 GET/PUT 解码入口', () => {
  afterEach(() => vi.unstubAllGlobals())

  it.each(['GET', 'PUT'] as const)('%s 保留完整响应、请求方式、负载和取消信号', async (method) => {
    const value = { ...preferences, navigationOrder: ['future-module'], future: 1 }
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(value)))
    vi.stubGlobal('fetch', fetch)
    const controller = new AbortController()
    const result = method === 'GET' ? requestPreferences(controller.signal) : updatePreferences(preferences, controller.signal)
    await expect(result).resolves.toEqual(value)
    expect(fetch).toHaveBeenCalledTimes(1)
    const [path, init] = fetch.mock.calls[0]!
    expect(path).toBe('/api/v1/preferences')
    expect(init).toMatchObject({ signal: controller.signal, credentials: 'same-origin', headers: { Accept: 'application/json' } })
    expect(init?.method ?? 'GET').toBe(method)
    expect(init?.body).toBe(method === 'PUT' ? JSON.stringify(preferences) : undefined)
  })

  it.each(['GET', 'PUT'] as const)('%s 原子拒绝坏 JSON、204、null 和损坏顺序，不自动重试', async (method) => {
    const fetch = vi.fn<typeof globalThis.fetch>()
    vi.stubGlobal('fetch', fetch)
    const payloads = ['{', 'null', '{}', JSON.stringify({ ...preferences, navigationOrder: {} }), JSON.stringify({ ...preferences, navigationOrder: null }), JSON.stringify({ ...preferences, latinFont: 'invalid' })]
    const responses = [...payloads.map((body) => new Response(body)), new Response(null, { status: 204 })]
    for (const response of responses) {
      fetch.mockResolvedValueOnce(response)
      const result = method === 'GET' ? requestPreferences() : updatePreferences(preferences)
      await expect(result).rejects.toMatchObject({ code: method === 'GET' ? 'CONTROL_RESPONSE_INVALID' : 'PREFERENCES_WRITE_UNKNOWN' })
    }
    expect(fetch).toHaveBeenCalledTimes(responses.length)
  })

  it.each(['GET', 'PUT'] as const)('%s 保留 HTTP 错误、取消和传输异常，不重试', async (method) => {
    const aborted = new DOMException('Aborted', 'AbortError')
    const network = new TypeError('fixture offline')
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'PREFERENCES_INVALID', message: 'fixture error', requestId: 'fixture-request' }), { status: 400 }))
      .mockRejectedValueOnce(aborted).mockRejectedValueOnce(network)
    vi.stubGlobal('fetch', fetch)
    const call = () => method === 'GET' ? requestPreferences() : updatePreferences(preferences)
    await expect(call()).rejects.toMatchObject({ code: 'PREFERENCES_INVALID', requestId: 'fixture-request' })
    await expect(call()).rejects.toBe(aborted)
    await expect(call()).rejects.toBe(network)
    expect(fetch).toHaveBeenCalledTimes(3)
  })
})
