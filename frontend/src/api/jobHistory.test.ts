import { describe, expect, it, vi } from 'vitest'
import { HISTORY_RESPONSE_LIMIT, parseJobHistory, requestJobHistory, validHistoryType } from './jobHistory'
const item = (extra = {}) => ({ id: 'old/id', type: 'future-kind', status: 'future-state', message: '', createdAt: '0000-01-01T00:00:00Z', updatedAt: '9999-12-31T23:59:59Z', ...extra })
const query = { type: '', limit: 1 }
const page = (extra = {}) => ({ source: 'persisted', items: [item()], ...extra })
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json; charset=utf-8' } })

describe('只读历史协议', () => {
  it('开放类型/状态、旧 ID、时间边界和可选字段保持原值', () => {
    expect(parseJobHistory(page(), query)).toEqual(page())
    expect(parseJobHistory(page({ items: [item({ reference: '', cancellationStatus: 'future', errorCode: 'other', completedAt: '2026-10-15T00:00:00Z' })] }), query).items[0]?.cancellationStatus).toBe('future')
  })
  it.each([null, [], {}, { source: 'live', items: [] }, { source: 'persisted', items: null }, page({ total: 5 }), page({ items: [item(), item()] }), page({ items: [], nextCursor: 'YWJj' }), page({ nextCursor: '' }), page({ nextCursor: '=' }), page({ nextCursor: 'a' }), page({ nextCursor: 'a'.repeat(2049) })])('坏信封不伪装空页 %j', value => { expect(() => parseJobHistory(value, query)).toThrow('协议') })
  it.each([
    { id: '' }, { id: '\n' }, { id: '字'.repeat(86) }, { id: '\ud800' }, { type: '' }, { type: '\u0085' }, { type: '字'.repeat(43) },
    { status: '' }, { status: 'a'.repeat(65) }, { message: null }, { message: 'a'.repeat(8193) }, { reference: 'a'.repeat(4097) },
    { error: false }, { errorCode: 'a'.repeat(129) }, { cancellationStatus: 'a'.repeat(65) }, { completedAt: null },
    { createdAt: '2026-02-30T00:00:00Z' }, { updatedAt: '2026-10-15' }, { createdAt: '+010000-01-01T00:00:00Z' },
    { cancellable: true }, { persistenceStatus: 'pending' }, { artifactState: 'present' },
  ])('整页拒绝损坏字段 %j', extra => { expect(() => parseJobHistory(page({ items: [item(extra)] }), query)).toThrow('协议') })
  it('拒绝重复 ID、跨筛选类型和未前进游标', () => {
    expect(() => parseJobHistory(page({ items: [item(), item()] }), { ...query, limit: 2 })).toThrow()
    expect(() => parseJobHistory(page(), { ...query, type: 'other' })).toThrow()
    expect(() => parseJobHistory(page({ nextCursor: 'YWJj' }), { ...query, cursor: 'YWJj' })).toThrow()
  })
  it('原始类型精确编码，不 trim；仅 GET 历史且携带取消信号', async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ source: 'persisted', items: [] }))
    const signal = new AbortController().signal
    await requestJobHistory({ type: ' a&中 ', limit: 20, cursor: 'YWJj' }, signal, fetcher)
    const [url, options] = fetcher.mock.calls[0]!
    expect(new URL(url, 'http://fixture.test').searchParams.get('type')).toBe(' a&中 ')
    expect(options).toMatchObject({ method: 'GET', signal, cache: 'no-store', credentials: 'same-origin', redirect: 'error' })
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it('类型 UTF-8 边界、无控制字符与请求大小均在发起前验证', async () => {
    expect(validHistoryType('字'.repeat(42) + 'ab')).toBe(true); expect(validHistoryType('字'.repeat(43))).toBe(false)
    const fetcher = vi.fn()
    for (const bad of [{ type: '\n', limit: 50 }, { type: '', limit: 0 }, { type: '', limit: 101 }, { type: '', limit: 1.5 }, { type: '', limit: 20, cursor: '' }]) await expect(requestJobHistory(bad, undefined, fetcher)).rejects.toMatchObject({ code: 'JOB_HISTORY_INVALID_QUERY' })
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each([404, 501])('HTTP %s 明确不支持', async status => { await expect(requestJobHistory(query, undefined, vi.fn().mockResolvedValue(new Response('html', { status })))).rejects.toMatchObject({ code: 'JOB_HISTORY_UNSUPPORTED' }) })
  it.each([[401, 'anything', 'AUTH_UNAUTHORIZED'], [503, 'JOB_HISTORY_TOO_LARGE', 'JOB_HISTORY_TOO_LARGE'], [503, 'JOB_HISTORY_CORRUPT', 'JOB_HISTORY_CORRUPT'], [503, 'JOB_HISTORY_UNAVAILABLE', 'JOB_HISTORY_UNAVAILABLE'], [500, 'JOB_HISTORY_TOO_LARGE', 'JOB_HISTORY_UNAVAILABLE'], [201, 'anything', 'JOB_HISTORY_UNAVAILABLE']])('错误状态与代码 %s/%s', async (status, code, expected) => {
    await expect(requestJobHistory(query, undefined, vi.fn().mockResolvedValue(response({ code, message: 'secret backend text' }, status as number)))).rejects.toMatchObject({ code: expected })
  })
  it('坏 200、错误 MIME、非法 UTF-8 和超预算响应都为协议错误', async () => {
    const bad = [response({}), new Response('{}'), new Response(new Uint8Array([0xff]), { headers: { 'Content-Type': 'application/json' } }), new Response(' '.repeat(HISTORY_RESPONSE_LIMIT + 1), { headers: { 'Content-Type': 'application/json' } })]
    for (const value of bad) await expect(requestJobHistory(query, undefined, vi.fn().mockResolvedValue(value))).rejects.toMatchObject({ code: 'JOB_HISTORY_RESPONSE_INVALID' })
  })
  it('冻结查询并返回独占 DTO；调用者修改不能改变已发起读取', async () => {
    let resolve!: (response: Response) => void
    const fetcher = vi.fn().mockReturnValue(new Promise<Response>(yes => { resolve = yes }))
    const mutable = { type: 'future-kind', limit: 1 }
    const pending = requestJobHistory(mutable, undefined, fetcher)
    mutable.type = 'changed'; mutable.limit = 0
    resolve(response(page())); expect((await pending).items).toHaveLength(1)
    const raw = page(); const parsed = parseJobHistory(raw, query)
    raw.items[0]!.message = 'changed'; expect(parsed.items[0]?.message).toBe('')
  })
  it('拒绝重定向响应，不依据重定向后的状态作兼容回退', async () => {
    const redirected = response({}, 404); Object.defineProperty(redirected, 'redirected', { value: true })
    await expect(requestJobHistory(query, undefined, vi.fn().mockResolvedValue(redirected))).rejects.toMatchObject({ code: 'JOB_HISTORY_RESPONSE_INVALID' })
  })
  it('创建时间倒序及同秒 UTF-8 BINARY 次序；不使用 UTF-16 排序', () => {
    const a = item({ id: '😀' }), b = item({ id: '\ue000' })
    expect(parseJobHistory(page({ items: [a, b] }), { ...query, limit: 2 }).items).toHaveLength(2)
    expect(() => parseJobHistory(page({ items: [b, a] }), { ...query, limit: 2 })).toThrow()
    expect(() => parseJobHistory(page({ items: [item({ id: 'a' }), item({ id: 'b', createdAt: '2026-10-15T00:00:00Z' })] }), { ...query, limit: 2 })).toThrow()
  })
  it('完整 JSON 恰好 1 MiB 可读取，预算包含空白和多字节文本', async () => {
    const raw = JSON.stringify(page({ items: [item({ message: '中文' })] }))
    const body = raw + ' '.repeat(HISTORY_RESPONSE_LIMIT - new TextEncoder().encode(raw).length)
    expect(new TextEncoder().encode(body)).toHaveLength(HISTORY_RESPONSE_LIMIT)
    const result = await requestJobHistory(query, undefined, vi.fn().mockResolvedValue(new Response(body, { headers: { 'Content-Type': 'application/json' } })))
    expect(result.items[0]?.message).toBe('中文')
  })
  it('分块 UTF-8 有效解码且越界取消读取', async () => {
    const raw = new TextEncoder().encode(JSON.stringify(page({ items: [item({ message: '中文🙂' })] })))
    let i = 0
    const body = new ReadableStream({ pull(controller) { if (i < raw.length) controller.enqueue(raw.slice(i, ++i)); else controller.close() } })
    const result = await requestJobHistory(query, undefined, vi.fn().mockResolvedValue(new Response(body, { headers: { 'Content-Type': 'application/json' } })))
    expect(result.items[0]?.message).toBe('中文🙂')
    const cancel = vi.fn()
    const tooBig = new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array(HISTORY_RESPONSE_LIMIT + 1)) }, cancel })
    await expect(requestJobHistory(query, undefined, vi.fn().mockResolvedValue(new Response(tooBig, { headers: { 'Content-Type': 'application/json' } })))).rejects.toThrow()
    expect(cancel).toHaveBeenCalledOnce()
  })
})
