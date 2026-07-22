<script setup lang="ts">
import { computed, type PropType } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { Boxes, ChevronRight, LayoutDashboard, LogOut, RefreshCw, Server, ShieldCheck } from '@lucide/vue'

import type { SystemConnectionState } from '@/stores/system'

const route = useRoute()
const emit = defineEmits<{
  refresh: []
  logout: []
}>()

const props = defineProps({
  deviceName: { type: String, required: true },
  connectionState: { type: String as PropType<SystemConnectionState>, required: true },
  userName: { type: String, required: true },
  isRefreshing: { type: Boolean, default: false },
})

const routeTitle = computed(() => String(route.meta.title ?? 'NAS Control Plane'))
const connectionLabel = computed(() => {
  switch (props.connectionState) {
    case 'connected': return '实时数据已同步'
    case 'degraded': return '部分数据待恢复'
    case 'loading': return '正在获取实时数据'
    default: return '控制面暂不可用'
  }
})

const navigation = [
  { label: '控制室总览', to: '/', icon: LayoutDashboard },
  { label: '服务中心', to: '/services', icon: Boxes },
  { label: '能力地图', to: '/infrastructure', icon: ShieldCheck },
]
</script>

<template>
  <a class="skip-link" href="#app-main">跳到主要内容</a>

  <div class="app-shell">
    <aside class="app-sidebar" aria-label="主导航">
      <RouterLink class="brand" to="/" aria-label="NCP 控制室首页">
        <span class="brand__mark" aria-hidden="true">N</span>
        <span class="brand__text">
          <strong>NAS CONTROL PLANE</strong>
          <small>ROOT OPERATOR CONSOLE</small>
        </span>
      </RouterLink>

      <nav class="navigation" aria-label="控制台页面">
        <RouterLink v-for="item in navigation" :key="item.to" :to="item.to" class="navigation__item">
          <component :is="item.icon" :size="18" :stroke-width="1.8" aria-hidden="true" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="sidebar-footer">
        <div class="sidebar-footer__identity">
          <span class="sidebar-footer__avatar" aria-hidden="true">R</span>
          <div>
            <strong>{{ userName }}</strong>
            <span>ROOT SESSION</span>
          </div>
        </div>
        <button class="sidebar-footer__logout" type="button" @click="emit('logout')">
          <LogOut :size="15" aria-hidden="true" />
          退出登录
        </button>
      </div>
    </aside>

    <div class="app-stage">
      <header class="topbar">
        <div class="topbar__location" aria-label="当前位置">
          <Server :size="15" :stroke-width="1.85" aria-hidden="true" />
          <span>{{ deviceName }}</span>
          <ChevronRight :size="14" aria-hidden="true" />
          <strong>{{ routeTitle }}</strong>
        </div>
        <div class="topbar__actions">
          <span class="connection-state" :class="`connection-state--${connectionState}`">
            <span aria-hidden="true"></span>{{ connectionLabel }}
          </span>
          <button class="refresh-button" type="button" :disabled="isRefreshing" @click="emit('refresh')">
            <RefreshCw :class="{ spin: isRefreshing }" :size="15" aria-hidden="true" />
            <span>刷新</span>
          </button>
        </div>
      </header>

      <main id="app-main" class="app-main" tabindex="-1"><slot /></main>
    </div>
  </div>
</template>

<style scoped>
.app-shell {
  display: grid;
  grid-template-columns: 268px minmax(0, 1fr);
  min-height: 100dvh;
}

.app-sidebar {
  position: sticky;
  top: 0;
  display: flex;
  flex-direction: column;
  min-height: 100dvh;
  height: 100dvh;
  padding: 22px 16px 18px;
  border-right: 1px solid var(--ncp-line);
  background: rgba(255, 255, 255, 0.72);
  backdrop-filter: blur(18px);
}

.brand { display: flex; align-items: center; gap: 11px; min-height: 50px; padding: 0 8px; border-radius: var(--ncp-radius-sm); }
.brand__mark { display: grid; width: 34px; height: 34px; place-items: center; border-radius: 11px; background: linear-gradient(145deg, #2a70df, #1d59bd); box-shadow: 0 8px 18px rgba(31, 92, 197, 0.2); color: #fff; font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.9rem; font-weight: 800; }
.brand__text { display: grid; gap: 3px; }
.brand__text strong { color: var(--ncp-text); font-size: 0.68rem; letter-spacing: 0.075em; line-height: 1; }
.brand__text small { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.5rem; font-weight: 650; letter-spacing: 0.07em; }

.navigation { display: grid; gap: 6px; margin-top: 52px; }
.navigation__item { display: flex; align-items: center; gap: 12px; min-height: 46px; padding: 0 12px; border: 1px solid transparent; border-radius: 12px; color: var(--ncp-text-muted); font-size: 0.84rem; font-weight: 700; transition: border-color var(--ncp-duration-fast) var(--ncp-ease-out), background-color var(--ncp-duration-fast) var(--ncp-ease-out), color var(--ncp-duration-fast) var(--ncp-ease-out), transform var(--ncp-duration-fast) var(--ncp-ease-out); }
.navigation__item:hover { background: var(--ncp-surface-quiet); color: var(--ncp-text); transform: translateX(2px); }
.navigation__item.router-link-exact-active { border-color: rgba(44, 111, 223, 0.15); background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }

.sidebar-footer { display: grid; gap: 14px; margin-top: auto; padding: 17px 8px 4px; border-top: 1px solid var(--ncp-line); }
.sidebar-footer__identity { display: flex; align-items: center; gap: 10px; }
.sidebar-footer__avatar { display: grid; width: 32px; height: 32px; place-items: center; border-radius: 10px; background: var(--ncp-surface-quiet); color: var(--ncp-primary-strong); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.75rem; font-weight: 800; }
.sidebar-footer__identity div { display: grid; gap: 2px; min-width: 0; }
.sidebar-footer__identity strong { overflow: hidden; color: var(--ncp-text); font-size: 0.75rem; text-overflow: ellipsis; white-space: nowrap; }
.sidebar-footer__identity span:last-child { color: var(--ncp-text-subtle); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.52rem; font-weight: 700; letter-spacing: 0.08em; }
.sidebar-footer__logout { display: inline-flex; align-items: center; gap: 7px; width: max-content; padding: 0; background: transparent; color: var(--ncp-text-subtle); font-size: 0.7rem; font-weight: 700; transition: color var(--ncp-duration-fast) var(--ncp-ease-out); }
.sidebar-footer__logout:hover { color: var(--ncp-danger-strong); }

.app-stage { min-width: 0; }
.topbar { display: flex; align-items: center; justify-content: space-between; gap: var(--ncp-space-4); min-height: 74px; padding: 0 clamp(24px, 4vw, 58px); border-bottom: 1px solid rgba(226, 232, 240, 0.85); background: rgba(246, 248, 251, 0.64); backdrop-filter: blur(14px); }
.topbar__location, .topbar__actions, .connection-state, .refresh-button { display: flex; align-items: center; }
.topbar__location { gap: 8px; color: var(--ncp-text-subtle); font-size: 0.75rem; }
.topbar__location strong { color: var(--ncp-text-muted); font-weight: 750; }
.topbar__actions { gap: 12px; }
.connection-state { gap: 7px; color: var(--ncp-text-muted); font-family: 'JetBrains Mono Variable', ui-monospace, monospace; font-size: 0.63rem; font-weight: 650; }
.connection-state > span { width: 7px; height: 7px; border-radius: 50%; background: var(--ncp-primary); box-shadow: 0 0 0 4px var(--ncp-primary-soft); }
.connection-state--degraded > span, .connection-state--loading > span { background: var(--ncp-warning); box-shadow: 0 0 0 4px var(--ncp-warning-soft); }
.connection-state--unavailable > span { background: var(--ncp-danger); box-shadow: 0 0 0 4px var(--ncp-danger-soft); }
.refresh-button { gap: 7px; min-height: 36px; padding: 0 11px; border: 1px solid var(--ncp-line); border-radius: 10px; background: rgba(255, 255, 255, 0.72); color: var(--ncp-text-muted); font-size: 0.71rem; font-weight: 750; transition: border-color var(--ncp-duration-fast) var(--ncp-ease-out), color var(--ncp-duration-fast) var(--ncp-ease-out), transform var(--ncp-duration-fast) var(--ncp-ease-out); }
.refresh-button:hover:not(:disabled) { border-color: rgba(44, 111, 223, 0.3); color: var(--ncp-primary-strong); transform: translateY(-1px); }
.refresh-button:disabled { cursor: wait; opacity: 0.65; }
.spin { animation: spin 0.8s linear infinite; }
.app-main:focus { outline: none; }

@keyframes spin { to { transform: rotate(360deg); } }

@media (max-width: 960px) {
  .app-shell { display: block; }
  .app-sidebar { position: static; display: grid; grid-template-columns: auto minmax(0, 1fr); min-height: 0; height: auto; padding: 12px 18px; border-right: 0; border-bottom: 1px solid var(--ncp-line); }
  .navigation { display: flex; justify-content: flex-end; gap: 4px; margin-top: 0; }
  .navigation__item { min-height: 42px; padding: 0 10px; }
  .sidebar-footer { display: none; }
}

@media (max-width: 650px) {
  .brand__text, .topbar__location span, .topbar__location svg:last-of-type { display: none; }
  .navigation__item { gap: 0; width: 44px; justify-content: center; padding: 0; }
  .navigation__item span { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; }
  .topbar { min-height: 58px; padding: 0 18px; }
  .connection-state { font-size: 0.56rem; }
  .refresh-button span { display: none; }
  .refresh-button { width: 36px; justify-content: center; padding: 0; }
}
</style>
