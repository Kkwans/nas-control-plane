import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import {
  NcpApiError,
  bootstrapRoot,
  loginRoot,
  logoutRoot,
  requestAuthStatus,
  type RootUser,
} from '@/api/system'
import { isAbortError } from '@/session/sessionLifecycle'

export type AuthenticationState = 'checking' | 'setup' | 'anonymous' | 'authenticated' | 'unavailable'

export const useAuthStore = defineStore('auth', () => {
  const state = ref<AuthenticationState>('checking')
  const initialized = ref(false)
  const user = ref<RootUser | null>(null)
  const errorCode = ref<string | null>(null)
  const errorMessage = ref<string | null>(null)
  let operationSequence = 0
  let operationController: AbortController | null = null

  function beginOperation() {
    operationController?.abort()
    const controller = new AbortController()
    operationController = controller
    return { sequence: ++operationSequence, signal: controller.signal }
  }

  function isCurrent(sequence: number) {
    return sequence === operationSequence
  }

  function finishOperation(sequence: number) {
    if (isCurrent(sequence)) operationController = null
  }

  const isAuthenticated = computed(() => state.value === 'authenticated' && user.value !== null)

  async function refresh() {
    const operation = beginOperation()
    state.value = 'checking'
    try {
      const status = await requestAuthStatus(fetch, operation.signal)
      if (!isCurrent(operation.sequence)) return
      initialized.value = status.initialized
      user.value = status.user ?? null
      errorCode.value = null
      errorMessage.value = null
      state.value = status.authenticated && status.user ? 'authenticated' : status.initialized ? 'anonymous' : 'setup'
    } catch (error) {
      if (!isCurrent(operation.sequence) || isAbortError(error)) return
      setUnavailable(error)
    } finally {
      finishOperation(operation.sequence)
    }
  }

  async function bootstrap(credentials: { username: string; password: string }) {
    const operation = beginOperation()
    try {
      const session = await bootstrapRoot(credentials, fetch, operation.signal)
      if (!isCurrent(operation.sequence)) return
      initialized.value = true
      user.value = session.user
      state.value = 'authenticated'
      errorCode.value = null
      errorMessage.value = null
    } finally {
      finishOperation(operation.sequence)
    }
  }

  async function login(credentials: { username: string; password: string }) {
    const operation = beginOperation()
    try {
      const session = await loginRoot(credentials, fetch, operation.signal)
      if (!isCurrent(operation.sequence)) return
      initialized.value = true
      user.value = session.user
      state.value = 'authenticated'
      errorCode.value = null
      errorMessage.value = null
    } finally {
      finishOperation(operation.sequence)
    }
  }

  async function logout(): Promise<{ serverRevoked: boolean }> {
    const operation = beginOperation()
    let serverRevoked = false
    let failure: unknown
    try {
      await logoutRoot(fetch, operation.signal)
      serverRevoked = true
    } catch (error) {
      if (!isAbortError(error)) failure = error
    } finally {
      if (isCurrent(operation.sequence)) {
        user.value = null
        state.value = initialized.value ? 'anonymous' : 'setup'
        if (failure) {
          errorCode.value = failure instanceof NcpApiError ? failure.code : 'LOGOUT_UNCONFIRMED'
          errorMessage.value = '本地已退出，但服务端会话尚未确认。'
        } else {
          errorCode.value = null
          errorMessage.value = null
        }
      }
      finishOperation(operation.sequence)
    }
    return { serverRevoked }
  }

  function setUnavailable(error: unknown) {
    initialized.value = false
    user.value = null
    errorCode.value = error instanceof NcpApiError ? error.code : 'NETWORK_UNAVAILABLE'
    errorMessage.value = error instanceof NcpApiError ? error.message : '无法连接 NCP Server。'
    state.value = 'unavailable'
  }

  return {
    state,
    initialized,
    user,
    errorCode,
    errorMessage,
    isAuthenticated,
    refresh,
    bootstrap,
    login,
    logout,
  }
})
