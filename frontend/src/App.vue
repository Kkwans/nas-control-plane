<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { LoaderCircle } from '@lucide/vue'

import AppShell from './layout/AppShell.vue'
import LoginView from './views/LoginView.vue'
import { createManualRefreshRegistry, provideManualRefreshRegistry } from '@/composables/manualRefresh'
import { useAuthStore } from './stores/auth'
import { useSystemStore } from './stores/system'
import type { RealtimeScope } from './stores/system'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const systemStore = useSystemStore()
const manualRefreshRegistry = createManualRefreshRegistry()
const manualRefreshInFlight = ref(false)
provideManualRefreshRegistry(manualRefreshRegistry)
let sessionStartPromise: Promise<void> | null = null

watch(() => authStore.isAuthenticated, (authenticated) => {
  if (authenticated) void startAuthenticatedSession()
})
watch(() => route.fullPath, () => {
  if (authStore.isAuthenticated) void syncRouteData()
})

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
  void initialize()
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  systemStore.stopRealtime()
})

async function initialize() {
  await router.isReady()
  await authStore.refresh()
  if (authStore.isAuthenticated) {
    await startAuthenticatedSession()
  }
}

async function handleAuthenticated() {
  await startAuthenticatedSession()
}

function startAuthenticatedSession() {
  if (sessionStartPromise) return sessionStartPromise
  sessionStartPromise = (async () => {
    try {
      await systemStore.loadPreferences()
    } catch {
      // 偏好读取失败不应阻塞首次实时数据加载。
    }
    await syncRouteData()
  })()
  return sessionStartPromise.finally(() => {
    sessionStartPromise = null
  })
}

async function handleRefresh() {
  if (manualRefreshInFlight.value) return
  manualRefreshInFlight.value = true
  try {
    await systemStore.refresh()
    await manualRefreshRegistry.refresh()
    if (systemStore.errorCode === 'AUTH_UNAUTHORIZED') {
      await authStore.refresh()
    }
  } finally {
    manualRefreshInFlight.value = false
  }
}

async function handleLogout() {
  systemStore.stopRealtime()
  await authStore.logout()
  systemStore.clear()
  await router.replace('/')
}

function handleVisibilityChange() {
  if (!authStore.isAuthenticated) return
  if (document.visibilityState === 'visible') {
    void syncRouteData()
  }
}

async function syncRouteData() {
  const scopes = Array.isArray(route.meta.realtime)
    ? route.meta.realtime.filter((scope): scope is RealtimeScope => scope === 'summary' || scope === 'docker')
    : []
  systemStore.startRealtime(scopes)
  if (route.meta.capabilities && !systemStore.capabilities) {
    await systemStore.refresh({ capabilities: true })
  }
}
</script>

<template>
  <ElConfigProvider :locale="zhCn" size="default" :z-index="3000">
    <main v-if="authStore.state === 'checking'" class="app-boot" aria-live="polite">
      <span class="app-boot__mark">N</span>
      <LoaderCircle class="app-boot__spinner" :size="18" aria-hidden="true" />
      <span>正在连接控制面</span>
    </main>
    <LoginView v-else-if="!authStore.isAuthenticated" @authenticated="handleAuthenticated" />
    <AppShell
      v-else
      :device-name="systemStore.deviceName"
      :connection-state="systemStore.connectionState"
      :realtime-state="systemStore.realtimeState"
      :refresh-interval-seconds="systemStore.refreshIntervalSeconds"
      :sidebar-default="systemStore.preferences.sidebarDefault"
      :navigation-order="systemStore.preferences.navigationOrder"
      :user-name="authStore.user?.username ?? 'root'"
      :is-refreshing="systemStore.isRefreshing || manualRefreshInFlight"
      :live-data-active="systemStore.realtimeScopes.length > 0"
      @refresh="handleRefresh"
      @logout="handleLogout"
    >
      <RouterView />
    </AppShell>
  </ElConfigProvider>
</template>

<style scoped>
.app-boot {
  display: flex;
  min-height: 100dvh;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--ncp-text-muted);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.72rem;
}

.app-boot__mark {
  display: grid;
  width: 30px;
  height: 30px;
  place-items: center;
  border-radius: 9px;
  background: var(--ncp-primary);
  color: white;
  font-weight: 800;
}

.app-boot__spinner {
  animation: app-spin 0.8s linear infinite;
  color: var(--ncp-primary);
}

@keyframes app-spin {
  to { transform: rotate(360deg); }
}
</style>
