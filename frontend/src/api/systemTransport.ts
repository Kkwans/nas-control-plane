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

export async function requestJson<T>(
  path: string,
  init: RequestInit,
  validate: (value: unknown) => value is T,
  fetcher: typeof fetch,
  invalidCode: string,
): Promise<T> {
  const response = await fetcher(path, requestOptions(init))
  if (!response.ok) {
    throw await responseError(response)
  }
  let payload: unknown
  try {
    payload = await response.json()
  } catch {
    throw new NcpApiError(invalidCode, 'NCP 服务返回了无法识别的数据。')
  }
  if (!validate(payload)) {
    throw new NcpApiError(invalidCode, 'NCP 服务返回了无法识别的数据。')
  }
  return payload
}

export function requestOptions(init: RequestInit): RequestInit {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  return {
    ...init,
    credentials: 'same-origin',
    headers,
  }
}

export function jsonRequest(body: unknown): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

export function isApiErrorResponse(value: unknown): value is ApiErrorResponse {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as { code?: unknown }).code === 'string' &&
    typeof (value as { message?: unknown }).message === 'string' &&
    typeof (value as { requestId?: unknown }).requestId === 'string'
  )
}

export async function responseError(response: Response): Promise<NcpApiError> {
  try {
    const payload = await response.json()
    if (isApiErrorResponse(payload)) {
      return new NcpApiError(payload.code, payload.message, payload.requestId)
    }
  } catch {
    // Stable client errors avoid exposing a proxy or HTML response to the UI.
  }
  return new NcpApiError('API_REQUEST_FAILED', 'NCP 服务暂不可用。')
}
