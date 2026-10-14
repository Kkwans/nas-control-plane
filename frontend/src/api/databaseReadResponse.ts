import type {
  DatabaseCatalog,
  DatabaseColumn,
  DatabaseDiscovery,
  DatabaseRows,
  DatabaseSource,
  DatabaseTable,
  DatabaseValue,
} from './database'
import { NcpApiError } from './systemTransport'

/** 读取边界只返回固定诊断，不把响应、行数据或底层异常附到错误中。 */
function invalidResponse(): never {
  throw new NcpApiError('DATABASE_RESPONSE_INVALID', '数据库服务返回了无法识别的数据。')
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalidResponse()
  return value as Record<string, unknown>
}

function string(value: unknown): string {
  if (typeof value !== 'string') return invalidResponse()
  return value
}

function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') return invalidResponse()
  return value
}

function integer(value: unknown, minimum?: number): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || !Number.isInteger(value)
    || minimum !== undefined && value < minimum) return invalidResponse()
  return value
}

function member<const T extends readonly string[]>(value: unknown, values: T): T[number] {
  if (typeof value !== 'string' || !values.includes(value)) return invalidResponse()
  return value
}

function optional<T>(value: unknown, decode: (value: unknown) => T): T | undefined {
  return value == null ? undefined : decode(value)
}

function optionalMember<const T extends readonly string[]>(value: unknown, values: T): T[number] | undefined {
  // 旧 Agent 缺失的可选枚举经当前 Go DTO/HTTP 编码后会变为精确空串。
  // 仅将该零值视为缺省；空白与未知非空值仍须通过枚举校验。
  return value == null || value === '' ? undefined : member(value, values)
}

function slice<T>(value: unknown, decode: (value: unknown) => T, omitempty = false): T[] {
  // Go 的 nil slice 编码为 null；只有 Table.columns 声明了 omitempty。
  if (value === null || omitempty && value === undefined) return []
  if (!Array.isArray(value)) return invalidResponse()
  return Array.from(value, decode)
}

function databaseValue(value: unknown): DatabaseValue {
  if (value === null || typeof value === 'string' || typeof value === 'boolean'
    || typeof value === 'number' && Number.isFinite(value)) return value
  return invalidResponse()
}

function source(value: unknown): DatabaseSource {
  const input = object(value)
  return {
    id: string(input.id),
    name: string(input.name),
    driver: member(input.driver, ['sqlite', 'mysql', 'postgresql']),
    category: member(input.category, ['system', 'project']),
    project: string(input.project),
    module: string(input.module),
    location: string(input.location),
    path: optional(input.path, string),
    host: optional(input.host, string),
    port: optional(input.port, integer),
    defaultDatabase: optional(input.defaultDatabase, string),
    requiresLogin: boolean(input.requiresLogin),
    status: string(input.status),
    reachability: optionalMember(input.reachability, ['host', 'container-internal', 'unreachable']),
    evidence: optionalMember(input.evidence, ['published-port', 'host-network', 'host-path', 'database-url', 'none']),
    tags: slice(input.tags, string),
  }
}

function column(value: unknown): DatabaseColumn {
  const input = object(value)
  return {
    // SQLite 允许空列名和空声明类型；不修改标识符、声明或默认值文本。
    name: string(input.name),
    dataType: string(input.dataType),
    nullable: boolean(input.nullable),
    primaryKey: boolean(input.primaryKey),
    default: input.default === undefined ? undefined : databaseValue(input.default),
    position: integer(input.position, 0),
    // 旧 Agent 缺失此字段时，沿用调用方的 required 保守回退。
    writeMode: optionalMember(input.writeMode, ['required', 'optional-default', 'server-generated']),
  }
}

function table(value: unknown): DatabaseTable {
  const input = object(value)
  return {
    schema: string(input.schema),
    name: string(input.name),
    // Catalog 使用 table/view，Rows 的 loadTable 仍会返回 BASE TABLE。
    type: string(input.type),
    columns: slice(input.columns, column, true),
    // PostgreSQL reltuples 未统计时为 -1；当前 Go 端原样返回此估计值。
    rowCount: optional(input.rowCount, integer),
    sizeBytes: optional(input.sizeBytes, (item) => integer(item, 0)),
    createdAt: optional(input.createdAt, string),
    definition: optional(input.definition, string),
  }
}

export function decodeDatabaseDiscovery(value: unknown): DatabaseDiscovery {
  const input = object(value)
  return { collectedAt: string(input.collectedAt), sources: slice(input.sources, source) }
}

export function decodeDatabaseCatalog(value: unknown): DatabaseCatalog {
  const input = object(value)
  return { source: source(input.source), tables: slice(input.tables, table) }
}

export function decodeDatabaseRows(value: unknown): DatabaseRows {
  const input = object(value)
  return {
    table: table(input.table),
    rows: slice(input.rows, (row) => Object.fromEntries(
      // fromEntries 保留 __proto__ 等合法自有列名，不通过对象赋值触发原型 setter。
      Object.entries(object(row)).map(([name, value]) => [name, databaseValue(value)]),
    )),
    limit: integer(input.limit, 1),
    offset: integer(input.offset, 0),
    hasMore: boolean(input.hasMore),
    ordering: optional(input.ordering, (value) => {
      const ordering = object(value)
      return { stable: boolean(ordering.stable), columns: slice(ordering.columns, string) }
    }),
  }
}
