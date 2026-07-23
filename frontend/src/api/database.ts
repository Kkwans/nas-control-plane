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
  tags: string[]
}

export interface DatabaseCredentials {
  username?: string
  password?: string
  database?: string
}

export interface DatabaseConnection {
  sourceId: string
  credentials?: DatabaseCredentials
}

export interface DatabaseColumn {
  name: string
  dataType: string
  nullable: boolean
  primaryKey: boolean
  default?: DatabaseValue
  position: number
}

export interface DatabaseTable {
  schema: string
  name: string
  type: string
  columns: DatabaseColumn[]
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
}

export interface QueryResult {
  columns: string[]
  rows: DatabaseValue[][]
  rowsAffected: number
  truncated: boolean
  durationMs: number
}

export async function discoverDatabases(): Promise<DatabaseDiscovery> {
  return request<DatabaseDiscovery>('/api/v1/databases/discovery')
}

export async function loadDatabaseCatalog(connection: DatabaseConnection): Promise<DatabaseCatalog> {
  return request<DatabaseCatalog>('/api/v1/databases/catalog', connection)
}

export async function loadDatabaseRows(input: DatabaseConnection & {
  schema?: string
  table: string
  limit?: number
  offset?: number
  sortColumn?: string
  sortDirection?: string
}): Promise<DatabaseRows> {
  return request<DatabaseRows>('/api/v1/databases/rows', input)
}

export async function executeDatabaseSQL(input: DatabaseConnection & { sql: string }): Promise<QueryResult> {
  return request<QueryResult>('/api/v1/databases/query', input)
}

export async function insertDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  values: Record<string, DatabaseValue>
}): Promise<{ rowsAffected: number }> {
  return request('/api/v1/databases/rows/insert', input)
}

export async function updateDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  values: Record<string, DatabaseValue>
  keys: Record<string, DatabaseValue>
}): Promise<{ rowsAffected: number }> {
  return request('/api/v1/databases/rows/update', input)
}

export async function deleteDatabaseRow(input: DatabaseConnection & {
  schema?: string
  table: string
  keys: Record<string, DatabaseValue>
}): Promise<{ rowsAffected: number }> {
  return request('/api/v1/databases/rows/delete', input)
}

async function request<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
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
