import type { DatabaseQueryTruncationReason, DatabaseValue, QueryResult } from './database'
import { isApiErrorResponse, NcpApiError } from './systemTransport'

// 诊断码只用于定位，不能证明 SQL 未执行；不接收服务端原始错误文本。
const diagnosticCodes = [
  'credentials_required', 'auth_failed', 'unreachable', 'database_not_found',
  'permission_denied', 'sql_invalid', 'result_mode_unsupported', 'result_too_large',
  'constraint_failed', 'agent_unavailable', 'timeout', 'credential_store_unavailable',
  'credential_corrupt', 'key_unavailable', 'key_rotation_failed', 'migration_failed',
  'DATABASE_OPERATION_FAILED', 'AUTH_UNAVAILABLE', 'AUTH_UNAUTHORIZED',
  'HTTP_CROSS_ORIGIN_REJECTED', 'DATABASE_INPUT_INVALID',
  'HTTP_REQUEST_BODY_TOO_LARGE', 'HTTP_UNSUPPORTED_MEDIA_TYPE',
] as const
export type DatabaseQueryDiagnosticCode = typeof diagnosticCodes[number]

function isDiagnosticCode(code: unknown): code is DatabaseQueryDiagnosticCode {
  return typeof code === 'string' && (diagnosticCodes as readonly string[]).includes(code)
}

/** 未知结果不保留 SQL、凭据、响应正文、请求编号或底层异常。 */
export class DatabaseQueryOutcomeUnknownError extends NcpApiError {
  readonly diagnosticCode?: DatabaseQueryDiagnosticCode

  constructor(diagnosticCode?: string) {
    super('DATABASE_QUERY_OUTCOME_UNKNOWN', 'SQL 执行结果未确认，可能部分或全部生效。请先只读核对，再次执行可能重复生效。')
    this.name = 'DatabaseQueryOutcomeUnknownError'
    this.diagnosticCode = isDiagnosticCode(diagnosticCode) ? diagnosticCode : undefined
  }
}

export function isDatabaseQueryOutcomeUnknownError(error: unknown): error is DatabaseQueryOutcomeUnknownError {
  return error instanceof DatabaseQueryOutcomeUnknownError
}

// 仅这些准确组合能证明 Server 在派发 Agent SQL 前拒绝了请求。
const preDispatchRejections = {
  AUTH_UNAVAILABLE: [503, '登录服务暂不可用。'],
  AUTH_UNAUTHORIZED: [401, '请先登录。'],
  HTTP_CROSS_ORIGIN_REJECTED: [403, '请求来源校验失败。'],
  DATABASE_INPUT_INVALID: [400, '数据库请求参数无效。'],
  HTTP_REQUEST_BODY_TOO_LARGE: [413, '请求体超过大小限制。'],
  HTTP_UNSUPPORTED_MEDIA_TYPE: [415, '请求必须使用 application/json 内容类型。'],
} as const
type PreDispatchRejectionCode = keyof typeof preDispatchRejections

export class DatabaseQueryPreDispatchRejectedError extends NcpApiError {
  constructor(code: PreDispatchRejectionCode, requestId?: string) {
    const safeRequestId = requestId && /^[A-Za-z0-9._:-]{1,128}$/.test(requestId) ? requestId : undefined
    super(code, preDispatchRejections[code][1], safeRequestId)
    this.name = 'DatabaseQueryPreDispatchRejectedError'
  }
}

export function isDatabaseQueryPreDispatchRejectedError(error: unknown): error is DatabaseQueryPreDispatchRejectedError {
  return error instanceof DatabaseQueryPreDispatchRejectedError
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function isDatabaseValue(value: unknown): value is DatabaseValue {
  return value === null || typeof value === 'string' || typeof value === 'boolean' ||
    (typeof value === 'number' && Number.isFinite(value))
}

export function decodeDatabaseQueryResult(payload: unknown): QueryResult {
  const invalid = () => { throw new DatabaseQueryOutcomeUnknownError() }
  if (!isRecord(payload)) return invalid()
  for (const key of ['columns', 'rows', 'rowsAffected', 'truncated', 'durationMs']) {
    if (!Object.hasOwn(payload, key)) return invalid()
  }

  // Go nil slice 编码为 null；缺字段和非法内层行不能按空结果处理。
  const rawColumns = payload.columns === null ? [] : payload.columns
  const rawRows = payload.rows === null ? [] : payload.rows
  if (!Array.isArray(rawColumns) || !Array.isArray(rawRows)) return invalid()
  const columns: string[] = []
  for (const column of rawColumns) {
    if (typeof column !== 'string') return invalid()
    columns.push(column)
  }
  const rows: DatabaseValue[][] = []
  for (const row of rawRows) {
    if (!Array.isArray(row) || row.length !== columns.length) return invalid()
    const values: DatabaseValue[] = []
    for (const value of row) {
      if (!isDatabaseValue(value)) return invalid()
      values.push(value)
    }
    rows.push(values)
  }

  const rowsAffectedKnown = payload.rowsAffectedKnown ?? false
  if (typeof rowsAffectedKnown !== 'boolean') return invalid()
  const rowsAffected = payload.rowsAffected
  // 旧 Agent 未知计数仍是有符号 int64，不能擅自收紧为非负安全整数。
  if (typeof rowsAffected !== 'number' || !Number.isInteger(rowsAffected)) return invalid()
  if (rowsAffectedKnown && (!Number.isSafeInteger(rowsAffected) || rowsAffected < 0)) return invalid()
  if (typeof payload.truncated !== 'boolean') return invalid()
  if (typeof payload.durationMs !== 'number' || !Number.isInteger(payload.durationMs) || payload.durationMs < 0) return invalid()

  // 旧 Go 零值兼容；非空值必须符合当前公开的结果合同，不 trim 或猜测。
  let truncationReason: DatabaseQueryTruncationReason | undefined
  if (payload.truncationReason != null && payload.truncationReason !== '') {
    if (payload.truncationReason !== 'row_limit' && payload.truncationReason !== 'byte_limit') return invalid()
    truncationReason = payload.truncationReason
  }
  return {
    columns, rows, rowsAffected, rowsAffectedKnown, truncated: payload.truncated,
    ...(truncationReason ? { truncationReason } : {}), durationMs: payload.durationMs,
  }
}

export async function requestDatabaseQuery(body: string, signal?: AbortSignal): Promise<QueryResult> {
  // 构造异常发生在 fetch 前，保留本地错误；从 fetch 调用开始一律按已派发处理。
  const headers = new Headers({ Accept: 'application/json', 'Content-Type': 'application/json' })
  const init: RequestInit = {
    method: 'POST', credentials: 'same-origin', redirect: 'error', headers, body,
    ...(signal ? { signal } : {}),
  }
  let response: Response
  try { response = await fetch('/api/v1/databases/query', init) } catch { throw new DatabaseQueryOutcomeUnknownError() }

  let payload: unknown
  let status: number
  try {
    if (response.redirected !== false) throw new DatabaseQueryOutcomeUnknownError()
    status = response.status
    payload = await response.json()
  } catch { throw new DatabaseQueryOutcomeUnknownError() }

  // 不使用 response.ok 放宽到其他 2xx，也不依据 SQL 错误码猜测提交状态。
  if (status !== 200) {
    let rejection: DatabaseQueryPreDispatchRejectedError | undefined
    let diagnosticCode: DatabaseQueryDiagnosticCode | undefined
    try {
      if (isRecord(payload) && ['code', 'message', 'requestId'].every((key) => Object.hasOwn(payload, key)) && isApiErrorResponse(payload)) {
        if (Object.hasOwn(preDispatchRejections, payload.code)) {
          const code = payload.code as PreDispatchRejectionCode
          if (status === preDispatchRejections[code][0]) {
            rejection = new DatabaseQueryPreDispatchRejectedError(code, payload.requestId)
          }
        }
        if (isDiagnosticCode(payload.code)) diagnosticCode = payload.code
      }
    } catch { throw new DatabaseQueryOutcomeUnknownError() }
    if (rejection) throw rejection
    throw new DatabaseQueryOutcomeUnknownError(diagnosticCode)
  }
  try { return decodeDatabaseQueryResult(payload) } catch { throw new DatabaseQueryOutcomeUnknownError() }
}
