import { describe, expect, it, vi } from 'vitest'
import type { LogEntry } from '../api/control'
import { formatLogExport, LOG_EXPORT_MAX_BYTES, LOG_EXPORT_MAX_ROWS, LOG_EXPORT_TOO_LARGE, type LogExportInput } from './logExport'

const entry: LogEntry = {
  id: '不应导出的游标', timestamp: '2026-10-13T09:08:07.123456789+08:00', source: 'system',
  unit: '服务/../../secret', level: 'info', message: '中文日志 😀',
}

function input(overrides: Partial<LogExportInput> = {}): LogExportInput {
  return { entries: [{ ...entry }], source: 'system', page: 2, pageSize: 20, loadedCount: 83,
    exportedAt: new Date('2026-10-13T09:08:07.006+08:00'), ...overrides }
}

function withMessage(message: string) {
  return input({ entries: [{ ...entry, message }] })
}

describe('当前页日志导出文本', () => {
  it.each(['system', 'agent', 'container'] as const)('固定 %s 文件名、UTC 时间及元数据字段顺序', (source) => {
    const file = formatLogExport(input({ source, entries: [{ ...entry, source }] }))
    expect(file.filename).toBe(`ncp-logs-${source}-page-2-20261013T010807006Z.txt`)
    expect(file.text.split('\n')[0]).toBe('{"formatVersion":1,"scope":"current-page","exportedAt":"2026-10-13T01:08:07.006Z","page":2,"pageSize":20,"entryCount":1,"loadedCount":83,"order":"newest-first","source":"' + source + '"}')
    expect(file.entryCount).toBe(1)
    expect(file.byteLength).toBe(new TextEncoder().encode(file.text).byteLength)
    expect(file.text.startsWith('\ufeff')).toBe(false)
    expect(file.text.endsWith('\n')).toBe(true)
    expect(file.text.endsWith('\n\n')).toBe(false)
  })

  it('仅读取白名单，保留字段顺序及 RFC3339Nano 原始精度', () => {
    const row = { ...entry, stream: 'stderr' as const }
    for (const key of ['rawMessage', 'cookie', 'token', 'query', 'stack', 'id']) {
      Object.defineProperty(row, key, { enumerable: true, get() { throw new Error(`禁止读取 ${key}`) } })
    }
    const file = formatLogExport(input({ entries: [row] }))
    expect(file.text.split('\n')[1]).toBe(JSON.stringify({ timestamp: entry.timestamp, source: 'system', unit: entry.unit, level: 'info', stream: 'stderr', message: entry.message }))
    expect(Object.keys(JSON.parse(file.text.split('\n')[1]!))).toEqual(['timestamp', 'source', 'unit', 'level', 'stream', 'message'])
    expect(Object.keys(JSON.parse(formatLogExport(input()).text.split('\n')[1]!))).toEqual(['timestamp', 'source', 'unit', 'level', 'message'])
  })

  it('沿用快照顺序、保留同时间和同消息的独立记录，不重新排序或去重', () => {
    const rows: LogEntry[] = [
      { ...entry, id: '较旧先出现', timestamp: '2026-10-12T00:00:00.000000001Z', stream: 'stdout' },
      { ...entry, id: '相同时间一', stream: 'stderr' },
      { ...entry, id: '相同时间二', stream: 'stdout' },
      { ...entry, id: '独立重复事件', stream: 'stdout' },
    ]
    const file = formatLogExport(input({ entries: rows }))
    const lines = file.text.trimEnd().split('\n').map((line) => JSON.parse(line))
    expect(lines[0].entryCount).toBe(4)
    expect(file.entryCount).toBe(4)
    expect(lines.slice(1).map(({ timestamp, stream, message }) => ({ timestamp, stream, message }))).toEqual(rows.map(({ timestamp, stream, message }) => ({ timestamp, stream, message })))
    rows[0]!.message = '点击后新增内容'
    rows.push({ ...entry, message: '实时新记录' })
    expect(file.text).not.toContain('点击后新增内容')
    expect(file.text).not.toContain('实时新记录')
  })

  it('恶意内容、控制符和 Unicode 分隔符可逆且不能伪造逐行 JSON', () => {
    const controls = '\0\x01\x1b\x7f\x80\x9f\u061c\u200e\u200f\u2028\u2029\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069'
    const message = '中文😀\r\n{"source":"伪造"}\t"\\ <script>alert(1)</script> =HYPERLINK("secret") ../../etc/passwd ' + controls
    const row = { ...entry, unit: `单元${controls}`, message }
    const file = formatLogExport(input({ entries: [row] }))
    const lines = file.text.split('\n')
    expect(lines).toHaveLength(3)
    expect(lines[2]).toBe('')
    expect(JSON.parse(lines[1]!).message).toBe(message)
    expect(JSON.parse(lines[1]!).unit).toBe(row.unit)
    expect(file.text).toContain('中文😀')
    for (const control of controls) expect(file.text).not.toContain(control)
    expect(file.text).not.toContain('\r')
    expect(file.text).not.toContain('\t')
    expect(file.filename).not.toMatch(/secret|passwd|服务/)
  })

  it('200 条硬上限允许正常当前页，元数据与实际行数一致', () => {
    const file = formatLogExport(input({ entries: Array.from({ length: LOG_EXPORT_MAX_ROWS }, () => ({ ...entry })), pageSize: 200, loadedCount: 900 }))
    expect(file.entryCount).toBe(200)
    expect(file.text.split('\n')).toHaveLength(202)
    expect(JSON.parse(file.text.split('\n')[0]!)).toMatchObject({ entryCount: 200, loadedCount: 900, pageSize: 200 })
  })

  it.each([
    ['零条', { entries: [] }],
    ['201 条', { entries: Array.from({ length: 201 }, () => ({ ...entry })), pageSize: 200, loadedCount: 201 }],
    ['超过当前页', { entries: [entry, entry], pageSize: 1 }],
    ['页码为零', { page: 0 }], ['小数页码', { page: 1.5 }], ['非有限页码', { page: Infinity }],
    ['超过安全整数页码', { page: Number.MAX_SAFE_INTEGER + 1 }],
    ['每页为零', { pageSize: 0 }], ['每页超过上限', { pageSize: 201 }], ['每页为 NaN', { pageSize: NaN }],
    ['已加载计数不足', { loadedCount: 0 }], ['已加载小数', { loadedCount: 1.5 }],
    ['未知元数据来源', { source: '../../恶意' as LogEntry['source'] }],
  ] satisfies Array<[string, Partial<LogExportInput>]>)('拒绝无效范围：%s', (_name, overrides) => {
    expect(() => formatLogExport(input(overrides))).toThrow('当前页日志范围无效')
  })

  it.each(['source', 'level', 'stream'] as const)('拒绝未知记录枚举 %s', (field) => {
    const row = { ...entry, [field]: '../../恶意' } as LogEntry
    expect(() => formatLogExport(input({ entries: [row] }))).toThrow('日志字段无效')
  })

  it('拒绝非字符串白名单字段', () => {
    expect(() => formatLogExport(input({ entries: [{ ...entry, message: null as unknown as string }] }))).toThrow('日志字段无效')
  })

  it('拒绝无效导出日期', () => {
    expect(() => formatLogExport(input({ exportedAt: new Date(NaN) }))).toThrow()
  })

  it.each([
    ['ASCII', 'x', 1],
    ['中文和 emoji', '中😀', 7],
    ['JSON 控制符转义膨胀', '\0', 6],
    ['双向控制符额外转义膨胀', '\u202e', 6],
  ] as const)('UTF-8 成品 2 MiB 精确边界包含元数据和 LF：%s', (_name, chunk, bytes) => {
    const overhead = formatLogExport(withMessage('')).byteLength
    const budget = LOG_EXPORT_MAX_BYTES - overhead
    const message = chunk.repeat(Math.floor(budget / bytes)) + 'x'.repeat(budget % bytes)
    const exact = formatLogExport(withMessage(message))
    expect(exact.byteLength).toBe(2 * 1024 * 1024)
    expect(new TextEncoder().encode(exact.text).byteLength).toBe(LOG_EXPORT_MAX_BYTES)
    expect(JSON.parse(exact.text.split('\n')[1]!).message).toBe(message)
    const underBudget = budget - 1
    const under = chunk.repeat(Math.floor(underBudget / bytes)) + 'x'.repeat(underBudget % bytes)
    expect(formatLogExport(withMessage(under)).byteLength).toBe(LOG_EXPORT_MAX_BYTES - 1)
    expect(() => formatLogExport(withMessage(message + 'x'))).toThrow(LOG_EXPORT_TOO_LARGE)
    expect(() => formatLogExport(withMessage(message + '😀'))).toThrow(LOG_EXPORT_TOO_LARGE)
  })

  it('预算跨记录累计，超过总上限时整次拒绝', () => {
    const rows = [{ ...entry, message: '' }, { ...entry, message: '' }]
    const budget = LOG_EXPORT_MAX_BYTES - formatLogExport(input({ entries: rows })).byteLength
    rows[0]!.message = 'x'.repeat(Math.floor(budget / 2))
    rows[1]!.message = 'x'.repeat(budget - rows[0]!.message.length)
    expect(formatLogExport(input({ entries: rows })).byteLength).toBe(LOG_EXPORT_MAX_BYTES)
    rows[1]!.message += 'x'
    expect(() => formatLogExport(input({ entries: rows }))).toThrow(LOG_EXPORT_TOO_LARGE)
  })

  it.each(['message', 'unit', 'timestamp'] as const)('超长原始 %s 在记录序列化前拒绝', (field) => {
    const oversized = input({ entries: [{ ...entry, [field]: 'x'.repeat(LOG_EXPORT_MAX_BYTES + 1) }] })
    const stringify = vi.spyOn(JSON, 'stringify')
    let error: unknown
    try { formatLogExport(oversized) } catch (caught) { error = caught }
    const calls = [...stringify.mock.calls]
    stringify.mockRestore()
    expect(error).toBeInstanceOf(Error)
    expect((error as Error).message).toBe(LOG_EXPORT_TOO_LARGE)
    expect(calls).toHaveLength(1)
    expect(calls[0]![0]).toMatchObject({ scope: 'current-page' })
  })

  it('整条记录多个字段累计过长时同样先拒绝，不只检查 message', () => {
    const half = 'x'.repeat(LOG_EXPORT_MAX_BYTES / 2)
    const stringify = vi.spyOn(JSON, 'stringify')
    let error: unknown
    try { formatLogExport(input({ entries: [{ ...entry, unit: half, message: half }] })) } catch (caught) { error = caught }
    const count = stringify.mock.calls.length
    stringify.mockRestore()
    expect((error as Error).message).toBe(LOG_EXPORT_TOO_LARGE)
    expect(count).toBe(1)
  })
})
