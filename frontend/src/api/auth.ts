import {
  requestJson,
  requestOptions,
  responseError,
} from './systemTransport'
import type { AuthSession, AuthStatus, RootUser } from './system'

export async function requestAuthStatus(fetcher: typeof fetch = fetch, signal?: AbortSignal): Promise<AuthStatus> {
  return requestJson('/api/v1/auth/status', signal ? { signal } : {}, isAuthStatus, fetcher, 'AUTH_STATUS_RESPONSE_INVALID')
}

export async function bootstrapRoot(
  credentials: { username: string; password: string },
  fetcher: typeof fetch = fetch,
  signal?: AbortSignal,
): Promise<AuthSession> {
  return requestCredentials('/api/v1/auth/bootstrap', credentials, fetcher, signal)
}

export async function loginRoot(
  credentials: { username: string; password: string },
  fetcher: typeof fetch = fetch,
  signal?: AbortSignal,
): Promise<AuthSession> {
  return requestCredentials('/api/v1/auth/login', credentials, fetcher, signal)
}

export async function logoutRoot(fetcher: typeof fetch = fetch, signal?: AbortSignal): Promise<void> {
  const response = await fetcher('/api/v1/auth/logout', requestOptions({ method: 'POST', ...(signal ? { signal } : {}) }))
  if (!response.ok) {
    throw await responseError(response)
  }
}

async function requestCredentials(
  path: string,
  credentials: { username: string; password: string },
  fetcher: typeof fetch,
  signal?: AbortSignal,
): Promise<AuthSession> {
  return requestJson(
    path,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(credentials),
      ...(signal ? { signal } : {}),
    },
    isAuthSession,
    fetcher,
    'AUTH_SESSION_RESPONSE_INVALID',
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isRootUser(value: unknown): value is RootUser {
  return isRecord(value) && typeof value.id === 'number' && typeof value.username === 'string' && value.role === 'root'
}

function isAuthStatus(value: unknown): value is AuthStatus {
  return (
    isRecord(value) &&
    typeof value.initialized === 'boolean' &&
    typeof value.authenticated === 'boolean' &&
    (value.user === undefined || isRootUser(value.user))
  )
}

function isAuthSession(value: unknown): value is AuthSession {
  return isRecord(value) && isRootUser(value.user) && typeof value.expiresAt === 'string'
}
