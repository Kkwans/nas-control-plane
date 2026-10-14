import type { DatabaseCatalog, DatabaseRows } from './database'
import { NcpApiError } from './systemTransport'

function invalidIdentity(): never {
  // 目标不匹配仍是读取失败；不回显来源、表名、凭据或行内容。
  throw new NcpApiError('DATABASE_RESPONSE_INVALID', '数据库服务返回了无法识别的数据。')
}

export function assertDatabaseCatalogIdentity(catalog: DatabaseCatalog, sourceId: string): void {
  if (catalog.source.id !== sourceId) invalidIdentity()
}

export function assertDatabaseRowsIdentity(rows: DatabaseRows, target: { table: string; schema?: string }): void {
  if (rows.table.name !== target.table || target.schema !== undefined && rows.table.schema !== target.schema) invalidIdentity()
}
