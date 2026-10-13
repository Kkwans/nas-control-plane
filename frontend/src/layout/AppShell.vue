<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, type PropType, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import {
  ChevronRight,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  RefreshCw,
  Server,
  X,
} from '@lucide/vue'
import { ElTooltip } from 'element-plus'

import NcpLogo from '@/components/NcpLogo.vue'
import { navigationByID, primaryNavigation, type NavigationItem } from '@/router/navigation'
import type { RealtimeState, SystemConnectionState } from '@/stores/system'

const route = useRoute()
const emit = defineEmits<{ refresh: []; logout: [] }>()
const props = defineProps({
  deviceName: { type: String, required: true },
  connectionState: { type: String as PropType<SystemConnectionState>, required: true },
  connectionLabel: { type: String, default: '' },
  connectionDetail: { type: String, default: '' },
  realtimeState: { type: String as PropType<RealtimeState>, required: true },
  refreshIntervalSeconds: { type: Number, required: true },
  sidebarDefault: { type: String as PropType<'collapsed' | 'expanded'>, default: 'collapsed' },
  navigationOrder: { type: Array as PropType<string[]>, default: () => [] },
  userName: { type: String, required: true },
  isRefreshing: { type: Boolean, default: false },
  liveDataActive: { type: Boolean, default: false },
})

// 与下方 CSS 保持一致：768px 也使用移动菜单。
const mobileViewport = window.matchMedia('(max-width: 768px)')
const isMobile = ref(mobileViewport.matches)
const mobileNavigationOpen = ref(false)
const sidebar = ref<HTMLElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)
let disposed = false
let focusRevision = 0
let lastFocusedElement: Element | null = null
let focusEntryRevision = 0
const sidebarCollapsed = ref(props.sidebarDefault === 'collapsed')
const menuButton = ref<HTMLButtonElement | null>(null)

const orderedNavigation = computed(() => {
  const result: NavigationItem[] = []
  const seen = new Set<string>()
  for (const id of [...props.navigationOrder, ...primaryNavigation.map((item) => item.id)]) {
    const item = navigationByID.get(id)
    if (!item || seen.has(id)) continue
    seen.add(id)
    result.push(item)
  }
  return result
})

const routeTitle = computed(() => String(route.meta.title ?? 'NAS 管理面板'))
const breadcrumbs = computed(() => {
  if (!route.path.startsWith('/databases')) return [{ label: routeTitle.value }]
  const items: Array<{ label: string; to?: string }> = [{ label: '数据库', to: '/databases' }]
  if (route.params.sourceId) {
    items.push({
      label: String(route.query.sourceName || '数据库详情'),
      to: route.params.table ? `/databases/${route.params.sourceId}?sourceName=${encodeURIComponent(String(route.query.sourceName || ''))}` : undefined,
    })
  }
  if (route.params.table) items.push({ label: String(route.query.tableName || route.params.table) })
  return items
})
const realtimeLabel = computed(() => {
  if (props.connectionState !== 'connected') return props.connectionLabel || (props.connectionState === 'degraded' ? '数据同步受限' : props.connectionState === 'unavailable' ? '数据暂不可用' : '等待实时数据')
  if (props.realtimeState === 'streaming') return isMobile.value ? '实时更新' : `实时更新 · ${props.refreshIntervalSeconds} 秒`
  if (props.realtimeState === 'connecting') return '正在连接'
  return props.connectionState === 'connected' ? '数据已同步' : '等待数据'
})

function isNavigationActive(item: NavigationItem) {
  return route.path === item.to || (item.to !== '/' && route.path.startsWith(`${item.to}/`))
}

function focusAfterRender(target: () => HTMLElement | null | undefined) {
  const revision = ++focusRevision
  void nextTick(() => {
    if (!disposed && revision === focusRevision) target()?.focus({ preventScroll: true })
  })
}

function setMobileNavigation(open: boolean, restoreFocus = false) {
  ++focusRevision
  mobileNavigationOpen.value = open && isMobile.value
  document.body.classList.toggle('mobile-navigation-open', mobileNavigationOpen.value)
  if (mobileNavigationOpen.value) focusAfterRender(() => {
    if (sidebar.value) sidebar.value.scrollTop = 0
    return closeButton.value
  })
  else if (restoreFocus && isMobile.value) focusAfterRender(() => menuButton.value)
}

function focusableNavigation() {
  return Array.from(sidebar.value?.querySelectorAll<HTMLElement>('a[href], button:not(:disabled), [tabindex="0"]') ?? [])
    .filter((element) => element.getClientRects().length > 0 && !element.closest('[inert]'))
}

function onKeydown(event: KeyboardEvent) {
  if (!mobileNavigationOpen.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    setMobileNavigation(false, true)
  } else if (event.key === 'Tab') {
    const elements = focusableNavigation()
    const first = elements[0]
    const last = elements.at(-1)
    if (!first || !last) {
      event.preventDefault()
      sidebar.value?.focus()
    } else if (event.shiftKey && (document.activeElement === first || !sidebar.value?.contains(document.activeElement))) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && (document.activeElement === last || !sidebar.value?.contains(document.activeElement))) {
      event.preventDefault()
      first.focus()
    }
  }
}

function onFocusIn(event: FocusEvent) {
  ++focusEntryRevision
  lastFocusedElement = event.target instanceof Element ? event.target : null
  if (mobileNavigationOpen.value && !sidebar.value?.contains(event.target as Node)) closeButton.value?.focus()
}

function onFocusOut(event: FocusEvent) {
  // CSS 可在 change 回调前隐藏元素并触发 blur。只为尚未处理的断点变化
  // 保留焦点记录；普通 blur 必须清除，避免后续 resize 抢回旧焦点。
  if (event.target === lastFocusedElement && mobileViewport.matches === isMobile.value) lastFocusedElement = null
}

function onViewportChange(event: MediaQueryListEvent) {
  // 快速往返断点时，丢弃已经被实际 viewport 超越的排队事件。
  if (event.matches !== mobileViewport.matches || event.matches === isMobile.value) return
  const current = document.activeElement
  const active = (!current || current === document.body) && lastFocusedElement?.isConnected
    ? lastFocusedElement
    : current
  const focusWillHide = event.matches
    ? sidebar.value?.contains(active)
    : active === closeButton.value || active === menuButton.value
  isMobile.value = event.matches
  setMobileNavigation(false)
  lastFocusedElement = null
  const entryRevision = focusEntryRevision
  if (focusWillHide) focusAfterRender(() => {
    // nextTick 前若用户/页面已聚焦新目标，尊重新焦点而非覆盖它。
    if (focusEntryRevision !== entryRevision) return null
    return event.matches ? menuButton.value : document.getElementById('app-main')
  })
}

function onNavigationClick(event: MouseEvent) {
  // 新标签页/修饰键点击不应关闭当前导航；成功换路由由 watcher 处理。
  if (!mobileNavigationOpen.value || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
  const link = (event.target as Element).closest<HTMLAnchorElement>('a[href]')
  if (link && new URL(link.href).pathname === route.path && new URL(link.href).search === window.location.search) {
    setMobileNavigation(false)
    focusAfterRender(() => document.getElementById('app-main'))
  }
}

watch(() => route.fullPath, () => {
  const wasOpen = mobileNavigationOpen.value
  setMobileNavigation(false)
  // 菜单选路由也包括清除当前页 query；router 对普通 query 更新保留焦点。
  if (wasOpen) focusAfterRender(() => document.getElementById('app-main'))
})
watch(() => props.sidebarDefault, (value) => { sidebarCollapsed.value = value === 'collapsed' })
onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('focusin', onFocusIn)
  window.addEventListener('focusout', onFocusOut)
  mobileViewport.addEventListener('change', onViewportChange)
})
onBeforeUnmount(() => {
  disposed = true
  ++focusRevision
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('focusin', onFocusIn)
  window.removeEventListener('focusout', onFocusOut)
  lastFocusedElement = null
  mobileViewport.removeEventListener('change', onViewportChange)
  document.body.classList.remove('mobile-navigation-open')
})
</script>

<template>
  <a class="skip-link" :inert="mobileNavigationOpen || undefined" href="#app-main">跳到主要内容</a>
  <div :class="['app-shell', { 'app-shell--collapsed': sidebarCollapsed }]">
    <button
      v-if="mobileNavigationOpen"
      class="navigation-scrim"
      type="button"
      aria-label="关闭主菜单"
      tabindex="-1"
      aria-hidden="true"
      @click="setMobileNavigation(false, true)"
    ></button>

    <aside
      ref="sidebar"
      id="mobile-primary-navigation"
      :inert="(isMobile && !mobileNavigationOpen) || undefined"
      :role="mobileNavigationOpen ? 'dialog' : undefined"
      :aria-modal="mobileNavigationOpen ? true : undefined"
      :class="['app-sidebar', { 'app-sidebar--open': mobileNavigationOpen }]"
      tabindex="-1"
      aria-label="主导航"
      @click.capture="onNavigationClick"
    >
      <div class="sidebar-brand-row">
        <RouterLink class="brand" to="/" aria-label="NAS 管理面板首页">
          <NcpLogo :size="38" />
          <span class="brand__text"><strong>NAS 管理面板</strong><small>{{ deviceName }}</small></span>
        </RouterLink>
        <button ref="closeButton" class="sidebar-close" type="button" aria-label="关闭主菜单" @click="setMobileNavigation(false, true)">
          <X :size="21" />
        </button>
      </div>

      <nav class="navigation" aria-label="控制台页面">
        <p class="navigation__label">管理</p>
        <ElTooltip v-for="item in orderedNavigation" :key="item.id" :disabled="isMobile || !sidebarCollapsed" :content="item.label" placement="right">
          <RouterLink :to="item.to" class="navigation__item" :class="{ 'navigation__item--active': isNavigationActive(item) }" :aria-current="isNavigationActive(item) ? (route.path === item.to ? 'page' : 'location') : undefined" :aria-label="item.label">
            <component :is="item.icon" :size="20" :stroke-width="1.8" aria-hidden="true" />
            <span>{{ item.label }}</span>
            <ChevronRight class="navigation__arrow" :size="15" aria-hidden="true" />
          </RouterLink>
        </ElTooltip>
      </nav>

      <div class="sidebar-footer">
        <div class="sidebar-footer__identity">
          <span class="sidebar-footer__avatar" aria-hidden="true">{{ userName.slice(0, 1).toUpperCase() }}</span>
          <div><strong>{{ userName }}</strong><span>Root 管理会话</span></div>
        </div>
        <ElTooltip :disabled="isMobile || !sidebarCollapsed" content="退出登录" placement="right">
          <button class="sidebar-footer__logout" type="button" aria-label="退出登录" @click="emit('logout')">
            <LogOut :size="17" aria-hidden="true" /><span>退出登录</span>
          </button>
        </ElTooltip>
        <ElTooltip :content="sidebarCollapsed ? '展开侧边栏' : '折叠侧边栏'" placement="right">
          <button class="sidebar-collapse" type="button" :aria-label="sidebarCollapsed ? '展开侧边栏' : '折叠侧边栏'" @click="sidebarCollapsed = !sidebarCollapsed">
            <PanelLeftOpen v-if="sidebarCollapsed" :size="18" />
            <PanelLeftClose v-else :size="18" />
            <span>{{ sidebarCollapsed ? '展开菜单' : '折叠菜单' }}</span>
          </button>
        </ElTooltip>
      </div>
    </aside>

    <div class="app-stage" :inert="mobileNavigationOpen || undefined">
      <header class="topbar">
        <div class="topbar__leading">
          <button
            ref="menuButton"
            class="menu-button"
            type="button"
            :aria-expanded="mobileNavigationOpen"
            aria-controls="mobile-primary-navigation"
            aria-label="打开主菜单"
            @click="setMobileNavigation(true)"
          >
            <Menu :size="21" />
          </button>
          <div class="topbar__location" aria-label="当前位置">
            <Server :size="17" aria-hidden="true" /><span>{{ deviceName }}</span>
            <template v-for="item in breadcrumbs" :key="`${item.label}-${item.to ?? ''}`">
              <ChevronRight :size="14" aria-hidden="true" />
              <RouterLink v-if="item.to" :to="item.to">{{ item.label }}</RouterLink>
              <strong v-else>{{ item.label }}</strong>
            </template>
          </div>
        </div>
        <div class="topbar__actions">
          <span v-if="liveDataActive" class="connection-state" :class="`connection-state--${connectionState}`" :title="connectionDetail" role="status">
            <span aria-hidden="true"></span>{{ realtimeLabel }}
          </span>
          <ElTooltip v-if="liveDataActive || route.meta.manualRefresh === true" content="立即刷新数据" placement="bottom">
            <button class="refresh-button" type="button" :disabled="isRefreshing" aria-label="立即刷新数据" @click="emit('refresh')">
              <RefreshCw :class="{ spin: isRefreshing }" :size="18" aria-hidden="true" /><span>刷新</span>
            </button>
          </ElTooltip>
        </div>
      </header>
      <main id="app-main" class="app-main" tabindex="-1"><slot /></main>
    </div>
  </div>
</template>

<style scoped>
.app-shell { display: grid; grid-template-columns: var(--ncp-sidebar-width) minmax(0, 1fr); min-height: 100dvh; }
.app-shell--collapsed { grid-template-columns: var(--ncp-sidebar-collapsed-width) minmax(0, 1fr); }
.app-sidebar { position: sticky; z-index: 50; top: 0; display: flex; height: 100dvh; flex-direction: column; padding: 18px 14px 14px; border-right: 1px solid var(--ncp-line); background: rgba(255,255,255,.96); box-shadow: 8px 0 30px rgba(53,76,108,.025); }
.sidebar-brand-row { display: flex; align-items: center; }
.brand { display: flex; min-width: 0; flex: 1; align-items: center; gap: 10px; min-height: 48px; padding: 0 8px; border-radius: 11px; }
.brand__text { display: grid; gap: 1px; min-width: 0; }
.brand__text strong { overflow: hidden; color: var(--ncp-text); font-size: .88rem; letter-spacing: -.025em; text-overflow: ellipsis; white-space: nowrap; }
.brand__text small { overflow: hidden; color: var(--ncp-text-subtle); font-size: .72rem; text-overflow: ellipsis; white-space: nowrap; }
.sidebar-close, .menu-button { display: none; width: var(--ncp-touch-target); height: var(--ncp-touch-target); place-items: center; border-radius: 10px; background: transparent; color: var(--ncp-text-muted); }
.navigation { display: grid; gap: 4px; margin-top: 24px; }
.navigation__label { margin: 0 10px 7px; color: var(--ncp-text-subtle); font-size: .78rem; font-weight: 750; letter-spacing: .02em; }
.navigation__item { display: flex; width: 100%; min-height: 44px; align-items: center; gap: 11px; padding: 0 11px; border: 1px solid transparent; border-radius: 11px; background: transparent; color: var(--ncp-text-muted); font-size: .88rem; font-weight: 670; text-align: left; transition: color var(--ncp-duration-fast), background-color var(--ncp-duration-fast), border-color var(--ncp-duration-fast), transform var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
.navigation__item:hover { border-color: color-mix(in srgb, var(--ncp-primary) 11%, transparent); background: var(--ncp-surface-quiet); color: var(--ncp-text); }
.navigation__item.navigation__item--active {
  border-color: color-mix(in srgb, var(--ncp-primary) 20%, transparent);
  background: var(--ncp-primary-soft);
  box-shadow: 0 5px 14px color-mix(in srgb, var(--ncp-primary) 10%, transparent);
  color: var(--ncp-primary-strong);
}
.navigation__arrow { margin-left: auto; opacity: 0; transform: translateX(-3px); transition: opacity var(--ncp-duration-fast), transform var(--ncp-duration-fast); }
.navigation__item:hover .navigation__arrow, .navigation__item.navigation__item--active .navigation__arrow { opacity: 1; transform: translateX(0); }
.sidebar-footer { display: grid; gap: 10px; margin-top: auto; padding: 14px 6px 2px; border-top: 1px solid var(--ncp-line); }
.sidebar-footer__identity { display: flex; align-items: center; gap: 9px; }
.sidebar-footer__avatar { display: grid; width: 36px; height: 36px; place-items: center; border-radius: 10px; background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); font-weight: 800; }
.sidebar-footer__identity div { display: grid; min-width: 0; }
.sidebar-footer__identity strong { font-size: .82rem; }
.sidebar-footer__identity span:last-child { color: var(--ncp-text-subtle); font-size: .75rem; }
.sidebar-footer__logout { display: flex; min-height: 44px; align-items: center; gap: 9px; padding: 0 10px; border-radius: 9px; background: transparent; color: var(--ncp-text-muted); font-size: .72rem; transition: background-color var(--ncp-duration-fast), color var(--ncp-duration-fast); }
.sidebar-footer__logout:hover { background: var(--ncp-danger-soft); color: var(--ncp-danger-strong); }
.sidebar-collapse { display: flex; min-height: 40px; align-items: center; gap: 9px; padding: 0 10px; border-radius: 9px; background: var(--ncp-surface-quiet); color: var(--ncp-text-muted); font-size: .72rem; font-weight: 680; }
.sidebar-collapse:hover { background: var(--ncp-primary-soft); color: var(--ncp-primary-strong); }
.app-shell--collapsed .app-sidebar { padding-inline: 9px; }
.app-shell--collapsed .brand { justify-content: center; padding: 0; }
.app-shell--collapsed .brand__text,
.app-shell--collapsed .navigation__label,
.app-shell--collapsed .navigation__item span,
.app-shell--collapsed .navigation__item small,
.app-shell--collapsed .navigation__arrow,
.app-shell--collapsed .sidebar-footer__identity div,
.app-shell--collapsed .sidebar-footer__logout span,
.app-shell--collapsed .sidebar-collapse span { display: none; }
.app-shell--collapsed .navigation { margin-top: 20px; }
.app-shell--collapsed .navigation__item,
.app-shell--collapsed .sidebar-footer__logout,
.app-shell--collapsed .sidebar-collapse { justify-content: center; padding: 0; }
.app-shell--collapsed .navigation__item:hover { transform: none; }
.app-shell--collapsed .sidebar-footer__identity { justify-content: center; }
.app-stage { min-width: 0; }
.topbar { position: sticky; top: 0; z-index: 30; display: flex; min-height: var(--ncp-topbar-height); align-items: center; justify-content: space-between; gap: 16px; padding: 0 clamp(20px, 2.2vw, 34px); border-bottom: 1px solid rgba(228,234,242,.9); background: rgba(250,252,254,.88); backdrop-filter: blur(18px) saturate(135%); }
.topbar__leading, .topbar__location, .topbar__actions, .connection-state, .refresh-button { display: flex; align-items: center; }
.topbar__leading { min-width: 0; gap: 8px; }
.topbar__location { gap: 8px; color: var(--ncp-text-subtle); font-size: .84rem; }
.topbar__location strong { color: var(--ncp-text); font-weight: 750; }
.topbar__location a { color: var(--ncp-text-muted); font-weight: 680; transition: color var(--ncp-duration-fast); }
.topbar__location a:hover { color: var(--ncp-primary-strong); }
.topbar__actions { gap: 12px; }
.connection-state { gap: 7px; color: var(--ncp-text-muted); font-size: .8rem; }
.connection-state > span { width: 7px; height: 7px; border-radius: 50%; background: var(--ncp-primary); box-shadow: 0 0 0 4px var(--ncp-primary-soft); }
.connection-state--degraded > span, .connection-state--loading > span { background: var(--ncp-warning); box-shadow: 0 0 0 4px var(--ncp-warning-soft); }
.connection-state--connected > span { background: var(--ncp-success); box-shadow: 0 0 0 4px var(--ncp-success-soft); }
.refresh-button { min-width: 80px; min-height: 44px; justify-content: center; gap: 7px; padding: 0 13px; border: 1px solid var(--ncp-line); border-radius: 10px; background: #fff; color: var(--ncp-text-muted); font-size: .72rem; font-weight: 720; transition: border-color var(--ncp-duration-fast), color var(--ncp-duration-fast), box-shadow var(--ncp-duration-fast); }
.refresh-button:hover:not(:disabled) { border-color: rgba(36,104,216,.32); box-shadow: 0 5px 16px rgba(36,104,216,.08); color: var(--ncp-primary-strong); }
.refresh-button:disabled { cursor: wait; opacity: .6; }
.navigation-scrim { display: none; }
.spin { animation: spin .8s linear infinite; }
.app-main:focus { outline: none; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (max-width: 768px) {
  .app-shell { display: block; }
  .menu-button, .sidebar-close { display: grid; }
  .app-sidebar { overflow-y: auto; overscroll-behavior: contain; visibility: hidden; position: fixed; left: 0; width: min(304px, 86vw); padding-top: calc(14px + env(safe-area-inset-top)); transform: translateX(-104%); box-shadow: var(--ncp-shadow-float); transition: transform var(--ncp-duration-base) var(--ncp-ease-out); }
  .sidebar-brand-row, .navigation, .sidebar-footer { flex-shrink: 0; }
  .sidebar-collapse { display: none; }
  .app-shell--collapsed .app-sidebar { padding-inline: 14px; }
  .app-shell--collapsed .brand { justify-content: flex-start; padding: 0 8px; }
  .app-shell--collapsed .brand__text,
  .app-shell--collapsed .sidebar-footer__identity div { display: grid; }
  .app-shell--collapsed .navigation__label { display: block; }
  .app-shell--collapsed .navigation__item span,
  .app-shell--collapsed .navigation__item small,
  .app-shell--collapsed .navigation__arrow,
  .app-shell--collapsed .sidebar-footer__logout span { display: inline; }
  .app-shell--collapsed .sidebar-collapse { display: none; }
  .app-shell--collapsed .navigation__item,
  .app-shell--collapsed .sidebar-footer__logout { justify-content: flex-start; padding: 0 11px; }
  .app-shell--collapsed .sidebar-footer__identity { justify-content: flex-start; }
  .app-sidebar--open { visibility: visible; transform: translateX(0); }
  .navigation-scrim { position: fixed; z-index: 45; inset: 0; display: block; width: 100%; height: 100%; background: rgba(12,24,44,.46); backdrop-filter: blur(2px); }
  .topbar { min-height: 58px; padding: 0 14px; }
  .topbar__location > svg:first-child, .topbar__location > span, .topbar__location > svg:nth-of-type(2) { display: none; }
  .topbar__actions { flex-shrink: 0; gap: 8px; }
  .connection-state { display: flex; gap: 6px; font-size: .72rem; white-space: nowrap; }
  .connection-state > span, .menu-button, .refresh-button { flex-shrink: 0; }
  .refresh-button { min-width: 44px; width: 44px; padding: 0; }
  .refresh-button span { display: none; }
}
</style>

<style>
body.mobile-navigation-open { overflow: hidden; }
</style>
