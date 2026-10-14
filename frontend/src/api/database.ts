import { isApiErrorResponse, NcpApiError } from './systemTransport'
import { requestDatabaseInsert } from './databaseInsertOutcome'
import { requestDatabaseRowWrite } from './databaseRowWriteOutcome'
import { requestDatabaseQuery } from './databaseQueryOutcome'
import { decodeDatabaseCatalog, decodeDatabaseDiscovery, decodeDatabaseRows } from './databaseReadResponse'

export type DatabaseDriver = 'sqlite' | 'mysql' | 'postgresql'
export type DatabaseValue = string | number | boolean | null
export type DatabaseQueryResultMode = 'auto' | 'rows' | 'exec'
export type DatabaseQueryTruncationReason = 'row_limit' | 'byte_limit'

export interface DatabaseSource {
  id: string
  name: string
  driver: DatabaseDriver
  category: 'system' | 'project'
  project: string
  module: string
  location: string
  path?: string
  host?: string
  port?: number
  defaultDatabase?: string
  requiresLogin: boolean
  status: string
  reachability?: 'host' | 'container-internal' | 'unreachable'
  evidence?: 'published-port' | 'host-network' | 'host-path' | 'database-url' | 'none'
  tags: string[]
}

export interface DatabaseCredentials {
  username?: string
  password?: string
  token?: string
  database?: string
}

export interface DatabaseConnection {
  sourceId: string
  credentials?: DatabaseCredentials
}

export interface DatabaseConnectionDiagnostic {
  connected: boolean
  code?: string
  driver: DatabaseDriver
  endpoint: string
  database?: string
  operation: string
  durationMs: number
}

export interface DatabaseSavedCredential {
  sourceId: string
  driver: DatabaseDriver
  endpoint: string
  username?: string
  database?: string
  keyVersion: number
  automaticEnabled: boolean
  lastErrorCode?: string
  updatedAt: string
}

export interface DatabaseColumn {
  name: string
  dataType: string
  nullable: boolean
  primaryKey: boolean
  default?: DatabaseValue
  position: number
  writeMode?: DatabaseWriteMode
}

export type DatabaseWriteMode = 'required' | 'optional-default' | 'server-generated'

export interface DatabaseTable {
  schema: string
  name: string
  type: string
  columns: DatabaseColumn[]
  rowCount?: number
  sizeBytes?: number
  createdAt?: string
  definition?: string
}

export interface DatabaseDiscovery {
  collectedAt: string
  sources: DatabaseSource[]
}

export interface DatabaseCatalog {
  source: DatabaseSource
  tables: DatabaseTable[]
}

export interface DatabaseRows {
  table: DatabaseTable
  rows: Array<Record<string, DatabaseValue>>
  limit: number
  offset: number
  hasMore: boolean
  ordering?: { stable: boolean; columns: string[] }
}

export interface QueryResult {
  columns: string[]
  rows: DatabaseValue[][]
  rowsAffected: number
  rowsAffectedKnown?: boolean
  truncated: boolean
  truncationReason?: DatabaseQueryTruncationReason
  durationMs: number
}

export async function discoverDatabases(force = false, signal?: AbortSignal): Promise<DatabaseDiscovery> {
  return decodeDatabaseDiscovery(await request<unknown>(force ? '/api/v1/databases/discovery?refresh=true' : '/api/v1/databases/discovery', undefined, signal))
}

export async function testDatabaseConnection(connection: DatabaseConnection, signal?: AbortSignal): Promise<DatabaseConnectionDiagnostic> {
  return request<DatabaseConnectionDiagnostic>('/api/v1/databases/test-connection', connection, signal)
}

export async function connectDatabase(connection: DatabaseConnection, signal?: AbortSignal): Promise<DatabaseConnectionDiagnostic> {
  return request<DatabaseConnectionDiagnostic>('/api/v1/databases/connect', connection, signal)
}

export async function loadDatabaseCatalog(connection: DatabaseConnection, signal?: AbortSignal): Promise<DatabaseCatalog> {
  return decodeDatabaseCatalog(await request<unknown>('/api/v1/databases/catalog', connection, signal))
}

export async function loadDatabaseRows(input: DatabaseConnection & {
  schema?: string
  table: string
  limit?: number
  offset?: number
  sortColumn?: string
  sortDirection?: string
}, signal?: AbortSignal): Promise<DatabaseRows> {
  return decodeDatabaseRows(await request<unknown>('/api/v1/databases/rows', input, signal))
}

export async function executeDatabaseSQL(input: DatabaseConnection & { sql: string; resultMode?: DatabaseQueryResultMode }, signal?: AbortSignal): Promise<QueryResult> {
  // 序列化在发送边界之前完成，本地失败仍然是零派发。
  const body = JSON.stringify(input)
  return requestDatabaseQuery(body, signal)
}

export async function insertDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  values: Record<string, DatabaseValue>
}, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  // 序列化失败发生在发送边界之前，保持本地错误，不能误判为结果未知。
  const body = JSON.stringify(input)
  return requestDatabaseInsert(body, signal)
}

export async function updateDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  values: Record<string, DatabaseValue>
  keys: Record<string, DatabaseValue>
}, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  // 序列化在发送边界之外完成，本地失败仍然是零派发。
  const body = JSON.stringify(input)
  return requestDatabaseRowWrite('update', body, signal)
}

export async function deleteDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  keys: Record<string, DatabaseValue>
}, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  const body = JSON.stringify(input)
  return requestDatabaseRowWrite('delete', body, signal)
}

async function request<T>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    ...(signal ? { signal } : {}),
  })
  if (!response.ok) {
    let payload: unknown
    try {
      payload = await response.json()
    } catch {
      // 畸形正文和底层读取异常不进入诊断对象。
    }
    // Go ErrorResponse 一直包含三个自有字符串字段，缺失或类型错误不是旧版本兼容值。
    if (!Array.isArray(payload) && isApiErrorResponse(payload)
      && ['code', 'message', 'requestId'].every((key) => Object.hasOwn(payload, key))) {
      throw new NcpApiError(payload.code || 'DATABASE_OPERATION_FAILED', payload.message || '数据库操作失败。', payload.requestId)
    }
    throw new NcpApiError('DATABASE_OPERATION_FAILED', '数据库操作失败。')
  }
  try {
    return await response.json() as T
  } catch {
    throw new NcpApiError('DATABASE_RESPONSE_INVALID', '数据库服务返回了无法识别的数据。')
  }
}
