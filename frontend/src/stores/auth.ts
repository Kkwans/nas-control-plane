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

export type AuthenticationState = 'checking' | 'setup' | 'anonymous' | 'authenticated' | 'unavailable'

export const useAuthStore = defineStore('auth', () => {
  const state = ref<AuthenticationState>('checking')
  const initialized = ref(false)
  const user = ref<RootUser | null>(null)
  const errorCode = ref<string | null>(null)
  const errorMessage = ref<string | null>(null)

  const isAuthenticated = computed(() => state.value === 'authenticated' && user.value !== null)

  async function refresh() {
    state.value = 'checking'
    try {
      const status = await requestAuthStatus()
      initialized.value = status.initialized
      user.value = status.user ?? null
      errorCode.value = null
      errorMessage.value = null
      state.value = status.authenticated && status.user ? 'authenticated' : status.initialized ? 'anonymous' : 'setup'
    } catch (error) {
      setUnavailable(error)
    }
  }

  async function bootstrap(credentials: { username: string; password: string }) {
    const session = await bootstrapRoot(credentials)
    initialized.value = true
    user.value = session.user
    state.value = 'authenticated'
    errorCode.value = null
    errorMessage.value = null
  }

  async function login(credentials: { username: string; password: string }) {
    const session = await loginRoot(credentials)
    initialized.value = true
    user.value = session.user
    state.value = 'authenticated'
    errorCode.value = null
    errorMessage.value = null
  }

  async function logout() {
    await logoutRoot()
    user.value = null
    state.value = initialized.value ? 'anonymous' : 'setup'
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
