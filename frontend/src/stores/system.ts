import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import { NcpApiError, requestCapabilities, type SystemCapabilities } from '@/api/system'

export type SystemConnectionState = 'checking' | 'connected' | 'preview'

export const useSystemStore = defineStore('system', () => {
  const connectionState = ref<SystemConnectionState>('checking')
  const capabilities = ref<SystemCapabilities | null>(null)
  const errorCode = ref<string | null>(null)

  const deviceName = computed(() => capabilities.value?.hostname || '本地设备')

  async function refresh() {
    connectionState.value = 'checking'
    try {
      capabilities.value = await requestCapabilities()
      errorCode.value = null
      connectionState.value = 'connected'
    } catch (error) {
      capabilities.value = null
      errorCode.value = error instanceof NcpApiError ? error.code : 'NETWORK_UNAVAILABLE'
      connectionState.value = 'preview'
    }
  }

  return {
    connectionState,
    capabilities,
    errorCode,
    deviceName,
    refresh,
  }
})
