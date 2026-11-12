import { describe, expect, it } from 'vitest'
import { decodeDatabaseCatalog, decodeDatabaseDiscovery, decodeDatabaseRows } from './databaseReadResponse'
import { NcpApiError } from './systemTransport'
import { databaseWriteMode } from '@/domain/database/valueConversion'
import legacyResponses from './fixtures/databaseReadResponse.legacy.json'

const source = {
  id: 'fixture', name: '数据库', driver: 'sqlite', category: 'project', project: '', module: '',
  location: '/fixture.sqlite', requiresLogin: false, status: 'available', tags: ['SQLite'],
}
const column = { name: 'id', dataType: 'INTEGER', nullable: false, primaryKey: true, position: 1 }
const table = { schema: '', name: 'sample', type: 'table', columns: [column] }
const rows = {
  table, rows: [{ id: '9007199254740993' }], limit: 25, offset: 0, hasMore: false,
  ordering: { stable: true, columns: ['id'] },
}

function expectInvalid(decode: () => unknown) {
  let caught: unknown
  try { decode() } catch (error) { caught = error }
  expect(caught).toBeInstanceOf(NcpApiError)
  expect(caught).toMatchObject({ code: 'DATABASE_RESPONSE_INVALID', message: '数据库服务返回了无法识别的数据。' })
  expect(caught).not.toHaveProperty('cause')
  expect(JSON.stringify(caught)).not.toContain('secret-fixture')
  expect(String(caught)).not.toContain('secret-fixture')
}

describe('数据库读取响应归一', () => {
  it('保留合法字段和精确文本，不修改输入或共享行对象', () => {
    const input = {
      ...rows,
      table: { ...table, type: 'BASE TABLE', columns: [{ ...column, default: '1.234567890123456789002500' }] },
      rows: [{ id: '9007199254740993', amount: '1.234567890123456789002500', nil: null, flag: false, count: 0 }],
    }
    const before = JSON.stringify(input)
    const result = decodeDatabaseRows(input)
    expect(result).toEqual(input)
    expect(JSON.stringify(input)).toBe(before)
    expect(result.rows[0]).not.toBe(input.rows[0])
    expect(result.table.columns[0]).not.toBe(input.table.columns[0])
  })

  it('Go nil slice 归一为空数组，columns 的 omitempty 也能省略', () => {
    expect(decodeDatabaseDiscovery({ collectedAt: '', sources: null })).toEqual({ collectedAt: '', sources: [] })
    expect(decodeDatabaseDiscovery({ collectedAt: '', sources: [{ ...source, tags: null }] }).sources[0]?.tags).toEqual([])
    expect(decodeDatabaseCatalog({ source, tables: null }).tables).toEqual([])
    for (const columns of [null, undefined, []]) {
      const result = decodeDatabaseRows({ ...rows, table: { ...table, columns }, rows: null, ordering: { stable: false, columns: null } })
      expect(result.table.columns).toEqual([])
      expect(result.rows).toEqual([])
      expect(result.ordering).toEqual({ stable: false, columns: [] })
    }
  })

  it('兼容缺省可选信息，保留旧 writeMode 的 required 回退', () => {
    for (const absent of [undefined, null]) {
      const decodedSource = decodeDatabaseCatalog({
        source: { ...source, path: absent, host: absent, port: absent, defaultDatabase: absent, reachability: absent, evidence: absent },
        tables: [{ ...table, columns: [{ ...column, writeMode: absent }], rowCount: absent, sizeBytes: absent, createdAt: absent, definition: absent }],
      })
      expect(decodedSource.source.path).toBeUndefined()
      expect(decodedSource.source.reachability).toBeUndefined()
      expect(decodedSource.tables[0]?.rowCount).toBeUndefined()
      expect(decodedSource.tables[0]?.columns[0]?.writeMode).toBeUndefined()
      expect(databaseWriteMode(decodedSource.tables[0]!.columns[0]!)).toBe('required')
      expect(decodeDatabaseRows({ ...rows, ordering: absent }).ordering).toBeUndefined()
    }
  })

  it.each([undefined, null, ''])('三个可选枚举仅对缺省和精确空串 %s 采用保守回退', (absent) => {
    const inputSource = { ...source, reachability: absent, evidence: absent }
    const inputTable = { ...table, columns: [{ ...column, writeMode: absent }] }
    const discovery = decodeDatabaseDiscovery({ collectedAt: '', sources: [inputSource] })
    const catalog = decodeDatabaseCatalog({ source: inputSource, tables: [inputTable] })
    const result = decodeDatabaseRows({ ...rows, table: inputTable })
    for (const item of [discovery.sources[0]!, catalog.source]) {
      expect(item.reachability).toBeUndefined()
      expect(item.evidence).toBeUndefined()
    }
    for (const item of [catalog.tables[0]!.columns[0]!, result.table.columns[0]!]) {
      expect(item.writeMode).toBeUndefined()
      expect(databaseWriteMode(item)).toBe('required')
    }
    expect(inputSource.reachability).toBe(absent)
    expect(inputTable.columns[0]!.writeMode).toBe(absent)
  })

  it('真实 Go codec/HTTP fixture 与旧 Agent 原 wire 解码后的保守元数据一致', () => {
    const discovery = decodeDatabaseDiscovery(legacyResponses.discovery.http)
    const catalog = decodeDatabaseCatalog(legacyResponses.catalog.http)
    const result = decodeDatabaseRows(legacyResponses.rows.http)
    expect(discovery).toEqual(decodeDatabaseDiscovery(legacyResponses.discovery.wire))
    expect(catalog).toEqual(decodeDatabaseCatalog(legacyResponses.catalog.wire))
    expect({ ...result, ordering: undefined }).toEqual(decodeDatabaseRows(legacyResponses.rows.wire))
    expect(result.ordering).toEqual({ stable: false, columns: [] })
    expect(catalog.source.reachability).toBeUndefined()
    expect(catalog.source.evidence).toBeUndefined()
    expect(result.table.columns[0]?.writeMode).toBeUndefined()
    expect(databaseWriteMode(result.table.columns[0]!)).toBe('required')
  })

  it('普通可选字符串的合法空文本不会套用枚举缺省规则', () => {
    const catalog = decodeDatabaseCatalog({
      source: { ...source, path: '', host: '', defaultDatabase: '' },
      tables: [{ ...table, createdAt: '', definition: '' }],
    })
    expect(catalog.source).toMatchObject({ path: '', host: '', defaultDatabase: '' })
    expect(catalog.tables[0]).toMatchObject({ createdAt: '', definition: '' })
  })

  it.each(['table', 'view', 'virtual', 'unknown', 'BASE TABLE', ''])('保留对象类型 %s 和不常见合法标识符', (type) => {
    const result = decodeDatabaseCatalog({ source, tables: [{ ...table, schema: '', name: ' 空白"表 ', type, columns: [{ ...column, name: '', dataType: '' }] }] })
    expect(result.tables[0]).toMatchObject({ name: ' 空白"表 ', type, columns: [{ name: '', dataType: '' }] })
  })

  it.each(['required', 'optional-default', 'server-generated'])('保留明确 writeMode %s', (writeMode) => {
    expect(decodeDatabaseRows({ ...rows, table: { ...table, columns: [{ ...column, writeMode }] } }).table.columns[0]?.writeMode).toBe(writeMode)
  })

  it.each([null, '', 0, false, true, '0', '9223372036854775807'])('保留合法默认值 %s', (value) => {
    expect(decodeDatabaseRows({ ...rows, table: { ...table, columns: [{ ...column, default: value }] } }).table.columns[0]?.default).toBe(value)
  })

  it('不把有限 JSON 数强制转成安全整数或字符串', () => {
    const largeNumber = 2 ** 63
    const result = decodeDatabaseRows({ ...rows, table: { ...table, rowCount: largeNumber, sizeBytes: largeNumber }, rows: [{ id: largeNumber, value: 0.125 }] })
    expect(result.rows[0]?.id).toBe(largeNumber)
    expect(result.rows[0]?.value).toBe(0.125)
    expect(result.table.rowCount).toBe(largeNumber)
    expect(decodeDatabaseCatalog({ source: { ...source, port: 70000 }, tables: [] }).source.port).toBe(70000)
  })

  it('PostgreSQL 未统计的 rowCount=-1 原样保留，不导致整个目录失效', () => {
    const result = decodeDatabaseCatalog({ source: { ...source, driver: 'postgresql' }, tables: [{ ...table, rowCount: -1 }] })
    expect(result.tables[0]?.rowCount).toBe(-1)
  })

  it('原型名称和空名称作为自有行字段原样保留', () => {
    const row = JSON.parse('{"__proto__":"原样文本","constructor":false,"toString":0,"":"空列"}')
    const result = decodeDatabaseRows({ ...rows, rows: [row] }).rows[0]!
    expect(Object.getPrototypeOf(result)).toBe(Object.prototype)
    expect(Object.keys(result)).toEqual(['__proto__', 'constructor', 'toString', ''])
    for (const name of Object.keys(row)) expect(Object.hasOwn(result, name) && result[name] === row[name]).toBe(true)
  })

  it('只复制支持字段，未知元数据不透传', () => {
    const result = decodeDatabaseCatalog({ source: { ...source, password: 'secret-fixture' }, tables: [{ ...table, credentials: 'secret-fixture' }], token: 'secret-fixture' })
    expect(JSON.stringify(result)).not.toContain('secret-fixture')
  })
})

describe('数据库读取畸形响应拒绝', () => {
  it.each([null, undefined, false, 17, 'secret-fixture', []])('拒绝非对象响应 %s', (input) => {
    expectInvalid(() => decodeDatabaseDiscovery(input))
    expectInvalid(() => decodeDatabaseCatalog(input))
    expectInvalid(() => decodeDatabaseRows(input))
  })

  it.each([undefined, {}, 'secret-fixture', 1, [null], [[]]])('拒绝缺失或非列表的必需 slices 及无效元素 %s', (input) => {
    expectInvalid(() => decodeDatabaseDiscovery({ collectedAt: '', sources: input }))
    expectInvalid(() => decodeDatabaseCatalog({ source, tables: input }))
    expectInvalid(() => decodeDatabaseRows({ ...rows, rows: input }))
  })

  it.each(['id', 'name', 'project', 'module', 'location', 'status'])('拒绝非字符串 source.%s', (key) => {
    expectInvalid(() => decodeDatabaseDiscovery({ collectedAt: '', sources: [{ ...source, [key]: 17 }] }))
    expectInvalid(() => decodeDatabaseCatalog({ source: { ...source, [key]: null }, tables: [] }))
  })

  it.each([
    { driver: 'oracle' }, { category: 'other' }, { requiresLogin: 'false' }, { tags: {} }, { tags: [false] },
    { path: [] }, { host: 17 }, { port: '5432' }, { port: 1.5 }, { defaultDatabase: {} },
    { reachability: 'other' }, { evidence: false },
  ])('拒绝无效 source 元数据 %j', (fields) => {
    expectInvalid(() => decodeDatabaseDiscovery({ collectedAt: '', sources: [{ ...source, ...fields }] }))
  })

  it.each([undefined, null, '', ' ', '\t\n', 'secret-fixture', false, 0])('必需 driver/category 不接受缺省或无效值 %s', (value) => {
    for (const key of ['driver', 'category']) {
      const invalidSource = { ...source, [key]: value }
      expectInvalid(() => decodeDatabaseDiscovery({ collectedAt: '', sources: [invalidSource] }))
      expectInvalid(() => decodeDatabaseCatalog({ source: invalidSource, tables: [] }))
    }
  })

  it.each([' ', '\t\n', ' host', 'required ', 'secret-fixture', false, 0, [], {}])('可选枚举不修剪或放过未知非空值 %j', (value) => {
    for (const key of ['reachability', 'evidence']) {
      const invalidSource = { ...source, [key]: value }
      expectInvalid(() => decodeDatabaseDiscovery({ collectedAt: '', sources: [invalidSource] }))
      expectInvalid(() => decodeDatabaseCatalog({ source: invalidSource, tables: [] }))
    }
    const invalidTable = { ...table, columns: [{ ...column, writeMode: value }] }
    expectInvalid(() => decodeDatabaseCatalog({ source, tables: [invalidTable] }))
    expectInvalid(() => decodeDatabaseRows({ ...rows, table: invalidTable }))
  })

  it.each([
    { schema: null }, { name: [] }, { type: 17 }, { columns: {} }, { columns: [null] },
    { rowCount: 1.5 }, { rowCount: '10' }, { sizeBytes: 0.5 }, { createdAt: 1 }, { definition: [] },
  ])('拒绝无效 table 元数据 %j', (fields) => {
    expectInvalid(() => decodeDatabaseCatalog({ source, tables: [{ ...table, ...fields }] }))
    expectInvalid(() => decodeDatabaseRows({ ...rows, table: { ...table, ...fields } }))
  })

  it.each([
    { name: 1 }, { dataType: null }, { nullable: 'false' }, { primaryKey: 1 }, { position: -1 }, { position: 1.5 },
    { position: undefined }, { writeMode: 'generated' }, { default: [] }, { default: { password: 'secret-fixture' } },
  ])('拒绝无效写入列元数据 %j', (fields) => {
    const malformedTable = { ...table, columns: [{ ...column, ...fields }] }
    expectInvalid(() => decodeDatabaseCatalog({ source, tables: [malformedTable] }))
    expectInvalid(() => decodeDatabaseRows({ ...rows, table: malformedTable }))
  })

  it.each([
    { limit: 0 }, { limit: 1.5 }, { limit: '25' }, { offset: -1 }, { offset: undefined }, { hasMore: 1 },
    { ordering: [] }, { ordering: {} }, { ordering: { stable: 'false', columns: [] } },
    { ordering: { stable: false, columns: {} } }, { ordering: { stable: true, columns: [null] } },
  ])('拒绝无效分页元数据 %j', (fields) => {
    expectInvalid(() => decodeDatabaseRows({ ...rows, ...fields }))
  })

  it.each([null, [], 'secret-fixture', 17, true])('拒绝非对象行 %s', (row) => {
    expectInvalid(() => decodeDatabaseRows({ ...rows, rows: [row] }))
  })

  it.each([{}, [], undefined, Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY])('拒绝非标量或非有限单元 %s', (value) => {
    expectInvalid(() => decodeDatabaseRows({ ...rows, rows: [{ id: value }] }))
  })

  it('时间字段和必需嵌套对象不以默认值掩盖', () => {
    expectInvalid(() => decodeDatabaseDiscovery({ collectedAt: null, sources: [] }))
    expectInvalid(() => decodeDatabaseCatalog({ tables: [] }))
    expectInvalid(() => decodeDatabaseRows({ ...rows, table: null }))
    expectInvalid(() => decodeDatabaseRows({ ...rows, ordering: { stable: true } }))
  })
})
