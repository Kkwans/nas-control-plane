import type { DatabaseConnectionDiagnostic } from './database'
import { isApiErrorResponse, NcpApiError } from './systemTransport'

/** connect 可能先保存凭据再返回；未知结果不能证明服务端没有保存。 */
export class DatabaseConnectionOutcomeUnknownError extends NcpApiError {
  constructor() {
    super('DATABASE_CONNECTION_OUTCOME_UNKNOWN', '连接结果尚未确认，服务端可能已保存本次凭据。请核对后再决定是否重新连接。')
    this.name = 'DatabaseConnectionOutcomeUnknownError'
  }
}

/** 连接响应已确认，但目录尚未可靠读取；不能把后续失败说成连接失败。 */
export class DatabaseConnectionCatalogError extends NcpApiError {
  constructor() {
    super('DATABASE_CONNECTION_CATALOG_FAILED', '连接已确认，但数据库对象目录读取失败。服务端可能已保存本次凭据。')
    this.name = 'DatabaseConnectionCatalogError'
  }
}

function invalidResponse(): never {
  throw new NcpApiError('DATABASE_RESPONSE_INVALID', '数据库服务返回了无法识别的数据。')
}

function string(value: unknown): string {
  if (typeof value !== 'string') return invalidResponse()
  return value
}

/** 只返回已验证字段，不携带原响应、凭据、额外字段或底层异常。 */
export function decodeDatabaseConnectionDiagnostic(value: unknown): DatabaseConnectionDiagnostic {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalidResponse()
  const input = value as Record<string, unknown>
  if (!['connected', 'driver', 'endpoint', 'operation', 'durationMs'].every((key) => Object.hasOwn(input, key))) return invalidResponse()
  if (typeof input.connected !== 'boolean') return invalidResponse()
  if (input.driver !== 'sqlite' && input.driver !== 'mysql' && input.driver !== 'postgresql') return invalidResponse()
  if (typeof input.durationMs !== 'number' || !Number.isFinite(input.durationMs)
    || !Number.isInteger(input.durationMs) || input.durationMs < 0) return invalidResponse()
  // Go 的 code/database 为 omitempty；旧 wire 中的 null 会经 DTO 归一为缺省。
  // 失败码和 operation 保持开放；成功码遵循当前 coordinator 的明确成功合同。
  const code = Object.hasOwn(input, 'code') ? input.code : undefined
  const database = Object.hasOwn(input, 'database') ? input.database : undefined
  if (code != null) string(code)
  const successCode = code === 'connected' || code === 'ok'
  if (input.connected ? code != null && code !== '' && !successCode : successCode) return invalidResponse()
  return {
    connected: input.connected,
    code: code == null ? undefined : string(code),
    driver: input.driver,
    endpoint: string(input.endpoint),
    database: database == null ? undefined : string(database),
    operation: string(input.operation),
    durationMs: input.durationMs,
  }
}

// 仅这些准确状态/错误码组合能证明服务端在保存凭据前拒绝请求。
// 保存服务错误、超时和无法验证的错误信封均不作为“未保存”的证据。
const rejections: Record<string, readonly [number, string]> = {
  AUTH_UNAVAILABLE: [503, '登录服务暂不可用。'],
  AUTH_UNAUTHORIZED: [401, '请先登录。'],
  HTTP_CROSS_ORIGIN_REJECTED: [403, '请求来源校验失败。'],
  DATABASE_INPUT_INVALID: [400, '数据库请求参数无效。'],
  HTTP_REQUEST_BODY_TOO_LARGE: [413, '请求体超过大小限制。'],
  HTTP_UNSUPPORTED_MEDIA_TYPE: [415, '请求必须使用 application/json 内容类型。'],
  credentials_required: [400, '连接需要数据库凭据，请填写表单后重试。'],
  auth_failed: [401, '数据库认证失败，请核对用户名和密码。'],
}

export async function requestDatabaseConnect(body: string, signal?: AbortSignal): Promise<DatabaseConnectionDiagnostic> {
  // 已取消与序列化失败属于本地零派发；不产生保存结果未知的警告。
  signal?.throwIfAborted()
  const init: RequestInit = {
    method: 'POST', credentials: 'same-origin', redirect: 'error',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body, ...(signal ? { signal } : {}),
  }
  let response: Response
  try { response = await fetch('/api/v1/databases/connect', init) } catch { throw new DatabaseConnectionOutcomeUnknownError() }
  if (response.redirected !== false) throw new DatabaseConnectionOutcomeUnknownError()
  let payload: unknown
  try { payload = await response.json() } catch { throw new DatabaseConnectionOutcomeUnknownError() }
  if (!response.ok) {
    if (!Array.isArray(payload) && isApiErrorResponse(payload)
      && ['code', 'message', 'requestId'].every((key) => Object.hasOwn(payload, key))) {
      const rejection = Object.hasOwn(rejections, payload.code) ? rejections[payload.code] : undefined
      if (rejection && response.status === rejection[0]) {
        const requestId = /^[A-Za-z0-9._:-]{1,128}$/.test(payload.requestId) ? payload.requestId : undefined
        throw new NcpApiError(payload.code, rejection[1], requestId)
      }
    }
    throw new DatabaseConnectionOutcomeUnknownError()
  }
  if (response.status !== 200) throw new DatabaseConnectionOutcomeUnknownError()
  try { return decodeDatabaseConnectionDiagnostic(payload) } catch { throw new DatabaseConnectionOutcomeUnknownError() }
}
