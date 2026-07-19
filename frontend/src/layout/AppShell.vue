<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import {
  Boxes,
  ChevronRight,
  LayoutDashboard,
  Server,
  ShieldCheck,
} from '@lucide/vue'

const route = useRoute()

const routeTitle = computed(() => String(route.meta.title ?? 'NAS Control Plane'))

const navigation = [
  { label: '控制室', to: '/', icon: LayoutDashboard },
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
          <strong>NCP</strong>
          <small>NAS CONTROL PLANE</small>
        </span>
      </RouterLink>

      <nav class="navigation" aria-label="控制台页面">
        <RouterLink
          v-for="item in navigation"
          :key="item.to"
          :to="item.to"
          class="navigation__item"
        >
          <component :is="item.icon" :size="18" :stroke-width="1.75" aria-hidden="true" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="sidebar-footer">
        <div class="sidebar-footer__signal">
          <span class="presence-dot" aria-hidden="true"></span>
          <span>开发预览</span>
        </div>
        <p>Phase 0 · 架构验证中</p>
      </div>
    </aside>

    <div class="app-stage">
      <header class="topbar">
        <div class="topbar__location" aria-label="当前位置">
          <Server :size="15" :stroke-width="1.75" aria-hidden="true" />
          <span>本地设备</span>
          <ChevronRight :size="14" aria-hidden="true" />
          <strong>{{ routeTitle }}</strong>
        </div>
        <div class="topbar__status">
          <span class="topbar__status-dot" aria-hidden="true"></span>
          <span>数据演示模式</span>
        </div>
      </header>

      <main id="app-main" class="app-main" tabindex="-1">
        <slot />
      </main>
    </div>
  </div>
</template>

<style scoped>
.app-shell {
  display: grid;
  grid-template-columns: 252px minmax(0, 1fr);
  min-height: 100dvh;
}

.app-sidebar {
  position: sticky;
  top: 0;
  display: flex;
  flex-direction: column;
  min-height: 100dvh;
  height: 100dvh;
  padding: 24px 16px 20px;
  border-right: 1px solid var(--ncp-line);
  background: rgba(9, 15, 16, 0.72);
}

.brand {
  display: flex;
  align-items: center;
  gap: 11px;
  min-height: 48px;
  padding: 0 8px;
  border-radius: var(--ncp-radius-sm);
}

.brand__mark {
  display: grid;
  width: 31px;
  height: 31px;
  place-items: center;
  border: 1px solid rgba(140, 226, 190, 0.55);
  border-radius: 8px;
  background: linear-gradient(145deg, rgba(140, 226, 190, 0.26), rgba(82, 186, 144, 0.08));
  color: var(--ncp-primary);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.91rem;
  font-weight: 800;
}

.brand__text {
  display: grid;
  gap: 1px;
}

.brand__text strong {
  color: var(--ncp-text);
  font-size: 0.86rem;
  letter-spacing: 0.1em;
  line-height: 1;
}

.brand__text small {
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.55rem;
  letter-spacing: 0.07em;
}

.navigation {
  display: grid;
  gap: 5px;
  margin-top: 48px;
}

.navigation__item {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 44px;
  padding: 0 12px;
  border: 1px solid transparent;
  border-radius: var(--ncp-radius-sm);
  color: var(--ncp-text-muted);
  font-size: 0.88rem;
  font-weight: 650;
  transition:
    border-color var(--ncp-duration-fast) var(--ncp-ease-out),
    background-color var(--ncp-duration-fast) var(--ncp-ease-out),
    color var(--ncp-duration-fast) var(--ncp-ease-out),
    transform var(--ncp-duration-fast) var(--ncp-ease-out);
}

.navigation__item:hover {
  background: rgba(255, 255, 255, 0.035);
  color: var(--ncp-text);
  transform: translateX(2px);
}

.navigation__item.router-link-exact-active {
  border-color: rgba(140, 226, 190, 0.18);
  background: var(--ncp-primary-soft);
  color: var(--ncp-primary);
}

.sidebar-footer {
  margin-top: auto;
  padding: 16px 8px 4px;
  border-top: 1px solid var(--ncp-line);
}

.sidebar-footer__signal {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--ncp-text-muted);
  font-size: 0.76rem;
  font-weight: 650;
}

.sidebar-footer__signal > span:first-child,
.topbar__status-dot {
  width: 7px;
  height: 7px;
  border-radius: var(--ncp-radius-pill);
  background: var(--ncp-primary);
}

.sidebar-footer p {
  margin: 7px 0 0 16px;
  color: var(--ncp-text-subtle);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.66rem;
}

.app-stage {
  min-width: 0;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--ncp-space-4);
  min-height: 72px;
  padding: 0 clamp(24px, 4vw, 54px);
  border-bottom: 1px solid var(--ncp-line);
  background: rgba(11, 18, 19, 0.64);
}

.topbar__location,
.topbar__status {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--ncp-text-subtle);
  font-size: 0.76rem;
}

.topbar__location strong {
  color: var(--ncp-text-muted);
  font-weight: 700;
}

.topbar__status {
  gap: 7px;
  color: var(--ncp-text-muted);
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.68rem;
}

.app-main:focus {
  outline: none;
}

@media (max-width: 920px) {
  .app-shell {
    display: block;
  }

  .app-sidebar {
    position: static;
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    min-height: 0;
    height: auto;
    padding: 12px 18px;
    border-right: 0;
    border-bottom: 1px solid var(--ncp-line);
  }

  .navigation {
    display: flex;
    justify-content: flex-end;
    gap: 4px;
    margin-top: 0;
  }

  .navigation__item {
    min-height: 42px;
    padding: 0 10px;
  }

  .sidebar-footer {
    display: none;
  }
}

@media (max-width: 620px) {
  .brand__text,
  .topbar__location span,
  .topbar__location svg:last-of-type {
    display: none;
  }

  .navigation__item {
    gap: 0;
    width: 44px;
    padding: 0;
    justify-content: center;
  }

  .navigation__item span {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
  }

  .topbar {
    min-height: 56px;
    padding: 0 18px;
  }

  .topbar__status {
    font-size: 0.62rem;
  }
}
</style>
