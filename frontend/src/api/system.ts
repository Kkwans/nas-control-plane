export interface SystemCapabilities {
  hostname?: string
  architecture: string
  docker: boolean
  cgroupVersion: number
  [key: string]: unknown
}

interface ApiErrorResponse {
  code: string
  message: string
  requestId: string
}

export class NcpApiError extends Error {
  readonly code: string
  readonly requestId?: string

  constructor(code: string, message: string, requestId?: string) {
    super(message)
    this.name = 'NcpApiError'
    this.code = code
    this.requestId = requestId
  }
}

export async function requestCapabilities(fetcher: typeof fetch = fetch): Promise<SystemCapabilities> {
  const response = await fetcher('/api/v1/system/capabilities', {
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) {
    throw await responseError(response)
  }

  const payload = await response.json()
  if (!isSystemCapabilities(payload)) {
    throw new NcpApiError('SYSTEM_CAPABILITIES_RESPONSE_INVALID', '系统能力响应格式无效。')
  }
  return payload
}

async function responseError(response: Response): Promise<NcpApiError> {
  try {
    const payload = await response.json()
    if (isApiErrorResponse(payload)) {
      return new NcpApiError(payload.code, payload.message, payload.requestId)
    }
  } catch {
    // 无法解析的错误响应不应泄露底层 HTML 或代理实现细节。
  }
  return new NcpApiError('API_REQUEST_FAILED', 'NCP 服务暂不可用。')
}

function isSystemCapabilities(value: unknown): value is SystemCapabilities {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const candidate = value as Record<string, unknown>
  return (
    typeof candidate.architecture === 'string' &&
    typeof candidate.docker === 'boolean' &&
    typeof candidate.cgroupVersion === 'number'
  )
}

function isApiErrorResponse(value: unknown): value is ApiErrorResponse {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const candidate = value as Record<string, unknown>
  return (
    typeof candidate.code === 'string' &&
    typeof candidate.message === 'string' &&
    typeof candidate.requestId === 'string'
  )
}
