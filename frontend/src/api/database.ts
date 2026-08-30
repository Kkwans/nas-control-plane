import { NcpApiError } from './system'

export type DatabaseDriver = 'sqlite' | 'mysql' | 'postgresql'
export type DatabaseValue = string | number | boolean | null

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
  truncated: boolean
  durationMs: number
}

export async function discoverDatabases(force = false, signal?: AbortSignal): Promise<DatabaseDiscovery> {
  return request<DatabaseDiscovery>(force ? '/api/v1/databases/discovery?refresh=true' : '/api/v1/databases/discovery', undefined, signal)
}

export async function testDatabaseConnection(connection: DatabaseConnection, signal?: AbortSignal): Promise<DatabaseConnectionDiagnostic> {
  return request<DatabaseConnectionDiagnostic>('/api/v1/databases/test-connection', connection, signal)
}

export async function connectDatabase(connection: DatabaseConnection, signal?: AbortSignal): Promise<DatabaseConnectionDiagnostic> {
  return request<DatabaseConnectionDiagnostic>('/api/v1/databases/connect', connection, signal)
}

export async function loadDatabaseCatalog(connection: DatabaseConnection, signal?: AbortSignal): Promise<DatabaseCatalog> {
  return request<DatabaseCatalog>('/api/v1/databases/catalog', connection, signal)
}

export async function loadDatabaseRows(input: DatabaseConnection & {
  schema?: string
  table: string
  limit?: number
  offset?: number
  sortColumn?: string
  sortDirection?: string
}, signal?: AbortSignal): Promise<DatabaseRows> {
  return request<DatabaseRows>('/api/v1/databases/rows', input, signal)
}

export async function executeDatabaseSQL(input: DatabaseConnection & { sql: string }, signal?: AbortSignal): Promise<QueryResult> {
  return request<QueryResult>('/api/v1/databases/query', input, signal)
}

export async function insertDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  values: Record<string, DatabaseValue>
}, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  return request('/api/v1/databases/rows/insert', input, signal)
}

export async function updateDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  values: Record<string, DatabaseValue>
  keys: Record<string, DatabaseValue>
}, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  return request('/api/v1/databases/rows/update', input, signal)
}

export async function deleteDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  keys: Record<string, DatabaseValue>
}, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  return request('/api/v1/databases/rows/delete', input, signal)
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
    try {
      const error = await response.json() as { code?: string; message?: string; requestId?: string }
      throw new NcpApiError(error.code || 'DATABASE_OPERATION_FAILED', error.message || '数据库操作失败。', error.requestId)
    } catch (error) {
      if (error instanceof NcpApiError) throw error
      throw new NcpApiError('DATABASE_OPERATION_FAILED', '数据库操作失败。')
    }
  }
  try {
    return await response.json() as T
  } catch {
    throw new NcpApiError('DATABASE_RESPONSE_INVALID', '数据库服务返回了无法识别的数据。')
  }
}
