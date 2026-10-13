import { isApiErrorResponse, NcpApiError } from './systemTransport'

export type UserWriteOperation = 'create' | 'status' | 'password' | 'delete'

/** 仅记录操作类别，绝不保留凭据、原始响应或底层异常。 */
export class UserWriteOutcomeUnknownError extends NcpApiError {
  constructor(readonly operation: UserWriteOperation) {
    super('USER_WRITE_OUTCOME_UNKNOWN', '账号操作可能已经执行，结果尚未确认。请先只读核对，勿重复提交。')
    this.name = 'UserWriteOutcomeUnknownError'
  }
}

type Rejection = readonly [number, string]
const common: Record<string, Rejection> = {
  AUTH_INPUT_INVALID: [400, '账号操作参数不符合要求。'],
  AUTH_UNAUTHORIZED: [401, '请先登录。'],
  HTTP_CROSS_ORIGIN_REJECTED: [403, '请求来源校验失败。'],
  ROUTE_NOT_FOUND: [404, '请求的资源不存在。'],
  METHOD_NOT_ALLOWED: [405, '请求方法不受支持。'],
  AUTH_UNAVAILABLE: [503, '登录服务暂不可用。'],
  USER_MANAGEMENT_UNAVAILABLE: [503, '用户管理服务暂不可用。'],
}
const bodyRejections: Record<string, Rejection> = {
  HTTP_REQUEST_BODY_TOO_LARGE: [413, '请求体超过大小限制。'],
  HTTP_UNSUPPORTED_MEDIA_TYPE: [415, '请求必须使用 application/json 内容类型。'],
}
const protectedRejections: Record<string, Rejection> = {
  AUTH_CURRENT_USER_PROTECTED: [409, '不能禁用或删除当前登录账号。'],
  AUTH_LAST_USER_PROTECTED: [409, '至少需要保留一个可用账号。'],
}
const missing: Rejection = [404, '目标账号不存在。']
const rejections: Record<UserWriteOperation, Record<string, Rejection>> = {
  create: { ...common, ...bodyRejections, AUTH_USERNAME_EXISTS: [409, '该用户名已存在。'] },
  // 状态提交后还会 userByID；该读取的 404 不能证明没有副作用。
  status: { ...common, ...bodyRejections, ...protectedRejections },
  password: { ...common, ...bodyRejections, AUTH_USER_NOT_FOUND: missing, AUTH_CURRENT_PASSWORD_INVALID: [401, '当前密码无效或账号状态已发生变化。'] },
  delete: { ...common, ...protectedRejections, AUTH_USER_NOT_FOUND: missing },
}

export async function requestUserWrite<T>(
  path: string,
  options: RequestInit,
  operation: UserWriteOperation,
  expectedStatus: number,
  parse: (value: unknown) => T,
): Promise<T> {
  // 所有本地请求构造在 fetch 前完成；异常不误报成已发送结果未知。
  const init: RequestInit = {
    ...options, credentials: 'same-origin', redirect: 'error',
    headers: { Accept: 'application/json', ...(options.body ? { 'Content-Type': 'application/json' } : {}) },
  }
  let response: Response
  try { response = await fetch(path, init) } catch { throw new UserWriteOutcomeUnknownError(operation) }
  if (response.redirected) throw new UserWriteOutcomeUnknownError(operation)
  if (response.ok && response.status === expectedStatus && expectedStatus === 204) return parse(undefined)
  let payload: unknown
  try { payload = await response.json() } catch { throw new UserWriteOutcomeUnknownError(operation) }
  if (!response.ok) {
    if (!Array.isArray(payload) && isApiErrorResponse(payload)) {
      const code = payload.code
      const rejection = Object.hasOwn(rejections[operation], code) ? rejections[operation][code] : undefined
      if (rejection && rejection[0] === response.status) throw new NcpApiError(code, rejection[1])
    }
    throw new UserWriteOutcomeUnknownError(operation)
  }
  if (response.status !== expectedStatus) throw new UserWriteOutcomeUnknownError(operation)
  try { return parse(payload) } catch { throw new UserWriteOutcomeUnknownError(operation) }
}
