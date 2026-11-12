import { isApiErrorResponse, NcpApiError } from './systemTransport'

export type DatabaseRowWriteOperation = 'update' | 'delete'

/** 未知结果不保留主键、提交值、凭据、响应正文或底层异常。 */
export class DatabaseRowWriteOutcomeUnknownError extends NcpApiError {
  constructor() {
    super('DATABASE_ROW_WRITE_OUTCOME_UNKNOWN', '行修改请求可能已生效，结果尚未确认。请先只读核对，勿重复修改或删除。')
    this.name = 'DatabaseRowWriteOutcomeUnknownError'
  }
}

// 仅这些准确组合能证明 Server 在派发 Agent 行修改前拒绝了请求。
const rejections: Record<string, readonly [number, string]> = {
  AUTH_UNAVAILABLE: [503, '登录服务暂不可用。'],
  AUTH_UNAUTHORIZED: [401, '请先登录。'],
  HTTP_CROSS_ORIGIN_REJECTED: [403, '请求来源校验失败。'],
  DATABASE_INPUT_INVALID: [400, '数据库请求参数无效。'],
  HTTP_REQUEST_BODY_TOO_LARGE: [413, '请求体超过大小限制。'],
  HTTP_UNSUPPORTED_MEDIA_TYPE: [415, '请求必须使用 application/json 内容类型。'],
}

export async function requestDatabaseRowWrite(operation: DatabaseRowWriteOperation, body: string, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  const init: RequestInit = {
    method: 'POST', credentials: 'same-origin', redirect: 'error',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body,
    ...(signal ? { signal } : {}),
  }
  let response: Response
  try { response = await fetch(`/api/v1/databases/rows/${operation}`, init) } catch { throw new DatabaseRowWriteOutcomeUnknownError() }
  if (response.redirected !== false) throw new DatabaseRowWriteOutcomeUnknownError()
  let payload: unknown
  try { payload = await response.json() } catch { throw new DatabaseRowWriteOutcomeUnknownError() }
  if (!response.ok) {
    if (!Array.isArray(payload) && isApiErrorResponse(payload)) {
      const rejection = Object.hasOwn(rejections, payload.code) ? rejections[payload.code] : undefined
      if (rejection && response.status === rejection[0]) {
        const requestId = /^[A-Za-z0-9._:-]{1,128}$/.test(payload.requestId) ? payload.requestId : undefined
        throw new NcpApiError(payload.code, rejection[1], requestId)
      }
    }
    throw new DatabaseRowWriteOutcomeUnknownError()
  }
  if (response.status !== 200 || !payload || typeof payload !== 'object' || Array.isArray(payload)) throw new DatabaseRowWriteOutcomeUnknownError()
  const rowsAffected = (payload as { rowsAffected?: unknown }).rowsAffected
  // 0 是既有合同合法值；影响计数不证明目标行发生变化，也不能静默舍入 int64。
  if (typeof rowsAffected !== 'number' || !Number.isSafeInteger(rowsAffected) || rowsAffected < 0) throw new DatabaseRowWriteOutcomeUnknownError()
  return { rowsAffected }
}
