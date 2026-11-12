import { describe, expect, it } from 'vitest'
import { parseLogResponse } from './logResponse'

const time = '2026-10-16T00:00:00.123456789+08:00'
const entry = { id: '', timestamp: time, source: 'system', unit: '', level: 'info', message: '' }
const page = { collectedAt: time, entries: [entry], nextCursor: '' }

describe('日志响应安全投影', () => {
  it('保留空标识符、任意游标、原始多行 Unicode 与纳秒时间，不携带未知属性', () => {
    const message = ' 首行\n中文🙂\u0000\u001b[31m\u202e末行 '
    const cursor = ' opaque\n游标/+=? '.repeat(500)
    const result = parseLogResponse({ ...page, nextCursor: cursor, secret: 'private', entries: [
      { ...entry, message, rawMessage: message, stream: 'stderr', secret: 'private' },
    ] })
    expect(result).toEqual({ ...page, nextCursor: cursor, entries: [{ ...entry, message, rawMessage: message, stream: 'stderr' }] })
    expect(result.entries[0]).not.toBe(entry)
  })

  it.each([undefined, null, ''])('可选字段 %j 宽容视为缺省且原始空文本不丢失', (value) => {
    const result = parseLogResponse({ ...page, entries: [{ ...entry, rawMessage: value, stream: value }] })
    expect(result.entries[0]?.stream).toBeUndefined()
    expect(result.entries[0]?.rawMessage).toBe(value === '' ? '' : undefined)
    expect(parseLogResponse(page)).toEqual(page)
  })

  it('接受空页、无大小上限页面、所有来源级别和 stdout/stderr', () => {
    expect(parseLogResponse({ ...page, entries: [] }).entries).toEqual([])
    const entries = Array.from({ length: 250 }, (_, i) => ({ ...entry,
      source: ['system', 'agent', 'container'][i % 3], level: ['error', 'warning', 'info', 'debug'][i % 4],
      stream: i % 2 ? 'stdout' : 'stderr',
    }))
    expect(parseLogResponse({ ...page, entries }).entries).toEqual(entries)
  })

  it.each(['0000-02-29T00:00:00Z', '0001-01-01T00:00:00Z', '2000-02-29T23:59:59.000000001Z', '9999-12-31T23:59:59-12:00'])('接受合法 Go 时间 %s', (timestamp) => {
    expect(parseLogResponse({ ...page, collectedAt: timestamp, entries: [{ ...entry, timestamp }] }).collectedAt).toBe(timestamp)
  })

  const invalidPages: unknown[] = [null, [], {}, { ...page, entries: null }, { ...page, entries: {} },
    { ...page, entries: [entry, {}] }, { ...page, entries: [null] }, { ...page, entries: new Array(1) },
    { ...page, nextCursor: null }, { ...page, nextCursor: 1 }, { ...page, nextCursor: undefined }]
  for (const field of ['id', 'timestamp', 'source', 'unit', 'level', 'message']) {
    for (const value of [undefined, null, 1, {}, []]) invalidPages.push({ ...page, entries: [{ ...entry, [field]: value }] })
  }
  for (const [field, value] of [['source', 'other'], ['level', 'fatal'], ['stream', 'other'], ['stream', false], ['rawMessage', {}]]) {
    invalidPages.push({ ...page, entries: [{ ...entry, [field as string]: value }] })
  }
  for (const timestamp of ['', 'now', '2026-02-29T00:00:00Z', '2026-04-31T00:00:00Z', '2026-00-01T00:00:00Z',
    '2026-10-16', '2026-10-16T24:00:00Z', '2026-10-16T00:00:00+24:00', '2026-10-16T00:00:00']) {
    invalidPages.push({ ...page, collectedAt: timestamp }, { ...page, entries: [{ ...entry, timestamp }] })
  }
  it.each(invalidPages.map((value, index) => [index, value]))('畸形输入 #%i 只返回固定无敏感信息诊断', (_index, value) => {
    try {
      parseLogResponse(value)
      expect.fail('应拒绝畸形日志')
    } catch (error) {
      expect(error).toMatchObject({ code: 'CONTROL_RESPONSE_INVALID', message: '日志接口返回了无法识别的数据。' })
      expect(error).not.toHaveProperty('cause')
      expect(error).not.toHaveProperty('requestId', 'private')
    }
  })
})
