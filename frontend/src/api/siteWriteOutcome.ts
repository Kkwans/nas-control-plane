import { isApiErrorResponse, NcpApiError } from './systemTransport'

export type SiteUnknownOperation = 'create' | 'update' | 'icon-compensation'

/** 写入或补偿可能已经执行；只能只读核对，不能当作普通失败直接重放。 */
export class SiteWriteUnknownError extends Error {
  readonly code = 'SITE_WRITE_UNKNOWN'
  constructor(readonly operation: SiteUnknownOperation, readonly siteId?: string) {
    super(operation === 'icon-compensation'
      ? '站点已创建，图标请求失败，补偿删除结果未确认。图标请求仍可能在服务端完成。'
      : operation === 'create'
        ? '创建请求可能已经执行，创建结果未确认。请先只读核对，勿重复添加。'
        : '保存请求可能已经执行，保存结果未确认。请先只读核对，勿重复提交。')
    this.name = 'SiteWriteUnknownError'
  }
}

// 仅接受在认证、请求解码或处理器入口明确早于写入的拒绝。
// SITE_INPUT_INVALID 同时覆盖 UpsertSiteProfile/ExecContext 错误，不能证明未写入。
const rejections: Record<string, readonly [number, string]> = {
  AUTH_UNAVAILABLE: [503, '登录服务暂不可用。'],
  AUTH_UNAUTHORIZED: [401, '请先登录。'],
  HTTP_CROSS_ORIGIN_REJECTED: [403, '请求来源校验失败。'],
  CONTROL_INPUT_INVALID: [400, '请求参数无效。'],
  HTTP_REQUEST_BODY_TOO_LARGE: [413, '请求体超过大小限制。'],
  HTTP_UNSUPPORTED_MEDIA_TYPE: [415, '请求必须使用 application/json 内容类型。'],
  SITES_UNAVAILABLE: [503, '站点资料暂不可用。'],
}

export async function requestSiteProfileWrite<T>(
  path: string,
  options: RequestInit,
  operation: 'create' | 'update',
  parse: (value: unknown) => T,
  siteId?: string,
): Promise<T> {
  // JSON 序列化和路径构造由调用方在进入发送边界前完成。
  const init: RequestInit = {
    ...options, credentials: 'same-origin', redirect: 'error',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
  }
  const unknown = () => new SiteWriteUnknownError(operation, siteId)
  let response: Response
  try { response = await fetch(path, init) } catch { throw unknown() }
  if (response.redirected) throw unknown()
  let payload: unknown
  try { payload = await response.json() } catch { throw unknown() }
  if (!response.ok) {
    if (!Array.isArray(payload) && isApiErrorResponse(payload)) {
      const rejection = Object.hasOwn(rejections, payload.code) ? rejections[payload.code] : undefined
      if (rejection && rejection[0] === response.status) throw new NcpApiError(payload.code, rejection[1], payload.requestId)
    }
    throw unknown()
  }
  if (response.status !== (operation === 'create' ? 201 : 200)) throw unknown()
  try { return parse(payload) } catch { throw unknown() }
}
