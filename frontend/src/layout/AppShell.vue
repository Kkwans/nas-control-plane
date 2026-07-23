<script setup lang="ts">
import { computed, type Component, type PropType } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import {
  Activity,
  Boxes,
  ChevronRight,
  Database,
  Container,
  FileClock,
  Gauge,
  Info,
  LayoutDashboard,
  LogOut,
  RefreshCw,
  Server,
  Settings,
  TerminalSquare,
} from '@lucide/vue'
import { ElTooltip } from 'element-plus'

import type { RealtimeState, SystemConnectionState } from '@/stores/system'

interface NavigationItem {
  label: string
  icon: Component
  to?: string
  planned?: boolean
}

const route = useRoute()
const emit = defineEmits<{ refresh: []; logout: [] }>()
const props = defineProps({
  deviceName: { type: String, required: true },
  connectionState: { type: String as PropType<SystemConnectionState>, required: true },
  realtimeState: { type: String as PropType<RealtimeState>, required: true },
  userName: { type: String, required: true },
  isRefreshing: { type: Boolean, default: false },
})

const primaryNavigation: NavigationItem[] = [
  { label: '总览', to: '/', icon: LayoutDashboard },
  { label: '服务入口', to: '/services', icon: Boxes },
  { label: 'Docker 管理', to: '/docker', icon: Container },
  { label: '系统信息', to: '/system', icon: Info },
]
const plannedNavigation: NavigationItem[] = [
  { label: '数据库', icon: Database, planned: true },
  { label: '监控', icon: Gauge, planned: true },
  { label: '日志中心', icon: FileClock, planned: true },
  { label: '终端', icon: TerminalSquare, planned: true },
  { label: '系统设置', icon: Settings, planned: true },
]

const routeTitle = computed(() => String(route.meta.title ?? 'NAS 管理面板'))
const realtimeLabel = computed(() => {
  if (props.connectionState === 'unavailable') return '数据暂不可用'
  if (props.realtimeState === 'streaming') return '实时更新中'
  if (props.realtimeState === 'polling') return '轮询更新中'
  if (props.realtimeState === 'connecting') return '正在连接'
  return props.connectionState === 'connected' ? '数据已同步' : '等待数据'
})
</script>

<template>
  <a class="skip-link" href="#app-main">跳到主要内容</a>
  <div class="app-shell">
    <aside class="app-sidebar" aria-label="主导航">
      <RouterLink class="brand" to="/" aria-label="NAS 管理面板首页">
        <span class="brand__mark" aria-hidden="true">N</span>
        <span class="brand__text"><strong>NAS 管理面板</strong><small>{{ deviceName }}</small></span>
      </RouterLink>

      <nav class="navigation" aria-label="控制台页面">
        <p class="navigation__label">管理</p>
        <RouterLink v-for="item in primaryNavigation" :key="item.label" :to="item.to!" class="navigation__item">
          <component :is="item.icon" :size="19" :stroke-width="1.8" aria-hidden="true" />
          <span>{{ item.label }}</span>
        </RouterLink>
        <p class="navigation__label navigation__label--secondary">后续模块</p>
        <ElTooltip v-for="item in plannedNavigation" :key="item.label" content="功能待接入" placement="right">
          <button class="navigation__item navigation__item--planned" type="button" disabled>
            <component :is="item.icon" :size="19" :stroke-width="1.8" aria-hidden="true" />
            <span>{{ item.label }}</span><small>待接入</small>
          </button>
        </ElTooltip>
      </nav>

      <div class="sidebar-footer">
        <div class="sidebar-footer__identity">
          <span class="sidebar-footer__avatar" aria-hidden="true">{{ userName.slice(0, 1).toUpperCase() }}</span>
          <div><strong>{{ userName }}</strong><span>Root 管理会话</span></div>
        </div>
        <button class="sidebar-footer__logout" type="button" @click="emit('logout')">
          <LogOut :size="16" aria-hidden="true" />退出登录
        </button>
      </div>
    </aside>

    <div class="app-stage">
      <header class="topbar">
        <div class="topbar__location" aria-label="当前位置">
          <Server :size="16" aria-hidden="true" /><span>{{ deviceName }}</span>
          <ChevronRight :size="14" aria-hidden="true" /><strong>{{ routeTitle }}</strong>
        </div>
        <div class="topbar__actions">
          <span class="connection-state" :class="`connection-state--${realtimeState}`">
            <span aria-hidden="true"></span>{{ realtimeLabel }}
          </span>
          <ElTooltip content="立即刷新数据" placement="bottom">
            <button class="refresh-button" type="button" :disabled="isRefreshing" aria-label="立即刷新数据" @click="emit('refresh')">
              <RefreshCw :class="{ spin: isRefreshing }" :size="17" aria-hidden="true" /><span>刷新</span>
            </button>
          </ElTooltip>
        </div>
      </header>
      <main id="app-main" class="app-main" tabindex="-1"><slot /></main>
    </div>
  </div>
</template>

<style scoped>
.app-shell { display: grid; grid-template-columns: 224px minmax(0, 1fr); min-height: 100dvh; }
.app-sidebar { position: sticky; top: 0; display: flex; flex-direction: column; height: 100dvh; padding: 18px 12px 14px; border-right: 1px solid var(--ncp-line); background: #fff; }
.brand { display: flex; align-items: center; gap: 10px; min-height: 46px; padding: 0 9px; border-radius: 10px; }
.brand__mark { display: grid; width: 34px; height: 34px; place-items: center; border-radius: 10px; background: var(--ncp-primary); box-shadow: 0 7px 18px rgba(32, 102, 210, .2); color: #fff; font-weight: 800; }
.brand__text { display: grid; gap: 2px; min-width: 0; }
.brand__text strong { color: var(--ncp-text); font-size: .88rem; letter-spacing: -.02em; }
.brand__text small { overflow: hidden; color: var(--ncp-text-subtle); font-size: .67rem; text-overflow: ellipsis; white-space: nowrap; }
.navigation { display: grid; gap: 3px; margin-top: 26px; }
.navigation__label { margin: 0 10px 7px; color: var(--ncp-text-subtle); font-size: .66rem; font-weight: 700; }
.navigation__label--secondary { margin-top: 18px; }
.navigation__item { display: flex; width: 100%; min-height: 42px; align-items: center; gap: 10px; padding: 0 11px; border: 0; border-radius: 9px; background: transparent; color: var(--ncp-text-muted); font-size: .82rem; font-weight: 650; text-align: left; transition: background-color var(--ncp-duration-fast), color var(--ncp-duration-fast); }
.navigation__item:hover { background: var(--ncp-surface-quiet); color: var(--ncp-text); }
.navigation__item.router-link-exact-active { background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.navigation__item--planned { cursor: not-allowed; opacity: .58; }
.navigation__item--planned small { margin-left: auto; font-size: .58rem; font-weight: 600; }
.sidebar-footer { display: grid; gap: 10px; margin-top: auto; padding: 14px 8px 2px; border-top: 1px solid var(--ncp-line); }
.sidebar-footer__identity { display: flex; align-items: center; gap: 9px; }
.sidebar-footer__avatar { display: grid; width: 32px; height: 32px; place-items: center; border-radius: 9px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-weight: 800; }
.sidebar-footer__identity div { display: grid; gap: 1px; min-width: 0; }
.sidebar-footer__identity strong { font-size: .76rem; }
.sidebar-footer__identity span:last-child { color: var(--ncp-text-subtle); font-size: .62rem; }
.sidebar-footer__logout { display: flex; min-height: 40px; align-items: center; gap: 8px; padding: 0 9px; border-radius: 8px; background: transparent; color: var(--ncp-text-muted); font-size: .73rem; }
.sidebar-footer__logout:hover { background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); }
.app-stage { min-width: 0; }
.topbar { position: sticky; top: 0; z-index: 20; display: flex; min-height: 58px; align-items: center; justify-content: space-between; gap: 16px; padding: 0 26px; border-bottom: 1px solid var(--ncp-line); background: rgba(248, 250, 252, .9); backdrop-filter: blur(14px); }
.topbar__location, .topbar__actions, .connection-state, .refresh-button { display: flex; align-items: center; }
.topbar__location { gap: 8px; color: var(--ncp-text-subtle); font-size: .75rem; }
.topbar__location strong { color: var(--ncp-text); font-weight: 700; }
.topbar__actions { gap: 12px; }
.connection-state { gap: 7px; color: var(--ncp-text-muted); font-size: .7rem; }
.connection-state > span { width: 7px; height: 7px; border-radius: 50%; background: var(--ncp-primary); box-shadow: 0 0 0 4px var(--ncp-primary-soft); }
.connection-state--polling > span, .connection-state--connecting > span { background: var(--ncp-warning); box-shadow: 0 0 0 4px var(--ncp-warning-soft); }
.connection-state--streaming > span { background: #16a177; box-shadow: 0 0 0 4px rgba(22, 161, 119, .1); }
.refresh-button { min-width: 76px; min-height: 40px; justify-content: center; gap: 7px; padding: 0 12px; border: 1px solid var(--ncp-line); border-radius: 9px; background: #fff; color: var(--ncp-text-muted); font-size: .72rem; font-weight: 700; transition: border-color var(--ncp-duration-fast), color var(--ncp-duration-fast); }
.refresh-button:hover:not(:disabled) { border-color: rgba(44,111,223,.35); color: var(--ncp-primary-strong); }
.refresh-button:disabled { cursor: wait; opacity: .6; }
.spin { animation: spin .8s linear infinite; }
.app-main:focus { outline: none; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (max-width: 900px) {
  .app-shell { grid-template-columns: 76px minmax(0, 1fr); }
  .brand { justify-content: center; padding: 0; }
  .brand__text, .navigation__label, .navigation__item span, .navigation__item small, .sidebar-footer__identity div, .sidebar-footer__logout { display: none; }
  .navigation__item { justify-content: center; padding: 0; }
  .sidebar-footer__identity { justify-content: center; }
}
@media (max-width: 600px) {
  .app-shell { display: block; }
  .app-sidebar { position: static; width: 100%; height: auto; flex-direction: row; align-items: center; padding: 8px 12px; overflow-x: auto; border-right: 0; border-bottom: 1px solid var(--ncp-line); }
  .navigation { display: flex; margin: 0 0 0 8px; }
  .navigation__label, .navigation__item--planned, .sidebar-footer { display: none; }
  .navigation__item { width: 42px; flex: 0 0 42px; }
  .topbar { min-height: 54px; padding: 0 14px; }
  .topbar__location > span, .topbar__location > svg:nth-of-type(2), .connection-state { display: none; }
  .refresh-button { min-width: 40px; width: 40px; padding: 0; }
  .refresh-button span { display: none; }
}
</style>
