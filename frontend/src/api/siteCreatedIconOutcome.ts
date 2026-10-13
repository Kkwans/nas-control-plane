import { isApiErrorResponse } from './systemTransport'

/** 资料创建已确认；图标的独立结果不能授权删除整站或重新创建。 */
export class SiteCreatedIconError extends Error {
  readonly code = 'SITE_CREATED_ICON_INCOMPLETE'
  constructor(readonly siteId: string, readonly iconOutcome: 'rejected' | 'unknown') {
    super(iconOutcome === 'rejected'
      ? '站点已创建，本次图标上传未完成。请只读核对目录，再打开已有站点处理图标。'
      : '站点已创建，图标上传结果未确认。请只读核对目录，勿重复添加或上传。')
    this.name = 'SiteCreatedIconError'
  }
}

/** 现有站点的上传结果；不暗示创建发生，也不保留原始响应。 */
export class SiteIconUploadError extends Error {
  readonly code = 'SITE_ICON_UPLOAD_OUTCOME'
  constructor(readonly siteId: string, readonly iconOutcome: 'rejected' | 'unknown') {
    super(iconOutcome === 'rejected'
      ? '本次图标上传未完成，请核对站点和文件后重新选择。'
      : '图标上传可能已经执行，结果未确认。本页已暂停该站点的重复上传，请只读核对目录。')
    this.name = 'SiteIconUploadError'
  }
}

export async function requestCreatedSiteIcon(siteId: string, icon: File, signal?: AbortSignal): Promise<void> {
  try { await requestSiteIconUpload(siteId, icon, signal) }
  catch (error) {
    throw new SiteCreatedIconError(siteId, error instanceof SiteIconUploadError ? error.iconOutcome : 'unknown')
  }
}

// 精确配对且合法错误信封才证明此次上传未提交；500 可能发生在 CAS 提交后。
const rejections: Record<string, number> = {
  AUTH_UNAVAILABLE: 503,
  AUTH_UNAUTHORIZED: 401,
  HTTP_CROSS_ORIGIN_REJECTED: 403,
  SITE_ICON_UNAVAILABLE: 503,
  SITE_ICON_INVALID: 400,
  SITE_ICON_REQUIRED: 400,
  SITE_ICON_TYPE_UNSUPPORTED: 400,
  SITE_NOT_FOUND: 404,
  SITE_ICON_CONFLICT: 409,
}

function matchesIconUrl(value: unknown, siteId: string): boolean {
  if (typeof value !== 'string' || !value.startsWith('/api/v1/sites/') || !value.endsWith('/icon')) return false
  const component = value.slice('/api/v1/sites/'.length, -'/icon'.length)
  if (/[/?#]/.test(component)) return false
  try { return decodeURIComponent(component) === siteId } catch { return false }
}

export async function requestSiteIconUpload(siteId: string, icon: File, signal?: AbortSignal): Promise<{ siteId: string; iconUrl: string }> {
  // 现有和新建站点共用精确上传合同，不自动跳转重放。
  let response: Response
  try {
    const body = new FormData()
    body.append('icon', icon)
    response = await fetch(`/api/v1/sites/${encodeURIComponent(siteId)}/icon`, {
      method: 'POST', body, credentials: 'same-origin', redirect: 'error',
      headers: { Accept: 'application/json' }, ...(signal ? { signal } : {}),
    })
  } catch { throw new SiteIconUploadError(siteId, 'unknown') }
  if (response.redirected) throw new SiteIconUploadError(siteId, 'unknown')
  let payload: unknown
  try { payload = await response.json() } catch { throw new SiteIconUploadError(siteId, 'unknown') }
  if (!response.ok) {
    if (!Array.isArray(payload) && isApiErrorResponse(payload)
      && Object.hasOwn(rejections, payload.code) && rejections[payload.code] === response.status) {
      throw new SiteIconUploadError(siteId, 'rejected')
    }
    throw new SiteIconUploadError(siteId, 'unknown')
  }
  if (response.status !== 200 || !payload || typeof payload !== 'object' || Array.isArray(payload)
    || !('siteId' in payload) || payload.siteId !== siteId
    || !('iconUrl' in payload) || !matchesIconUrl(payload.iconUrl, siteId)) {
    throw new SiteIconUploadError(siteId, 'unknown')
  }
  return { siteId, iconUrl: payload.iconUrl as string }
}
