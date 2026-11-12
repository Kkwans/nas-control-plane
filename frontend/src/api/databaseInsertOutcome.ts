import { isApiErrorResponse, NcpApiError } from './systemTransport'

/** 不保留请求、凭据、响应正文或底层异常；未知结果不能作为重试依据。 */
export class DatabaseInsertOutcomeUnknownError extends NcpApiError {
  constructor() {
    super('DATABASE_INSERT_OUTCOME_UNKNOWN', '新增请求可能已生效，结果尚未确认。请先只读核对，勿重复新增。')
    this.name = 'DatabaseInsertOutcomeUnknownError'
  }
}

// 仅限 Server 在派发 Agent INSERT 前返回的准确状态/错误码组合。
// Agent、SQL、约束或连接错误均不作为“未写入”的证据。
const rejections: Record<string, readonly [number, string]> = {
  AUTH_UNAVAILABLE: [503, '登录服务暂不可用。'],
  AUTH_UNAUTHORIZED: [401, '请先登录。'],
  HTTP_CROSS_ORIGIN_REJECTED: [403, '请求来源校验失败。'],
  DATABASE_INPUT_INVALID: [400, '数据库请求参数无效。'],
  HTTP_REQUEST_BODY_TOO_LARGE: [413, '请求体超过大小限制。'],
  HTTP_UNSUPPORTED_MEDIA_TYPE: [415, '请求必须使用 application/json 内容类型。'],
}

export async function requestDatabaseInsert(body: string, signal?: AbortSignal): Promise<{ rowsAffected: number }> {
  const init: RequestInit = {
    method: 'POST', credentials: 'same-origin', redirect: 'error',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body,
    ...(signal ? { signal } : {}),
  }
  let response: Response
  try { response = await fetch('/api/v1/databases/rows/insert', init) } catch { throw new DatabaseInsertOutcomeUnknownError() }
  if (response.redirected !== false) throw new DatabaseInsertOutcomeUnknownError()
  let payload: unknown
  try { payload = await response.json() } catch { throw new DatabaseInsertOutcomeUnknownError() }
  if (!response.ok) {
    if (!Array.isArray(payload) && isApiErrorResponse(payload)) {
      const rejection = Object.hasOwn(rejections, payload.code) ? rejections[payload.code] : undefined
      if (rejection && response.status === rejection[0]) {
        // 只保留有界支持编号；诊断文本不能借 requestId 字段进入错误对象。
        const requestId = /^[A-Za-z0-9._:-]{1,128}$/.test(payload.requestId) ? payload.requestId : undefined
        throw new NcpApiError(payload.code, rejection[1], requestId)
      }
    }
    throw new DatabaseInsertOutcomeUnknownError()
  }
  if (response.status !== 201 || !payload || typeof payload !== 'object' || Array.isArray(payload)) throw new DatabaseInsertOutcomeUnknownError()
  const rowsAffected = (payload as { rowsAffected?: unknown }).rowsAffected
  // 0 是现有驱动响应的合法值，不推断行数之外的写入语义。
  if (typeof rowsAffected !== 'number' || !Number.isSafeInteger(rowsAffected) || rowsAffected < 0) throw new DatabaseInsertOutcomeUnknownError()
  return { rowsAffected }
}
