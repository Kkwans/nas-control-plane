import type { Component } from 'vue'
import {
  Boxes,
  Container,
  Database,
  FileClock,
  Gauge,
  Info,
  LayoutDashboard,
  Settings,
  TerminalSquare,
  UserRound,
} from '@lucide/vue'

export interface NavigationItem {
  id: string
  label: string
  to: string
  icon: Component
}

export const primaryNavigation: readonly NavigationItem[] = [
  { id: 'overview', label: '总览', to: '/', icon: LayoutDashboard },
  { id: 'sites', label: '站点管理', to: '/sites', icon: Boxes },
  { id: 'docker', label: 'Docker', to: '/docker', icon: Container },
  { id: 'databases', label: '数据库', to: '/databases', icon: Database },
  { id: 'logs', label: '日志中心', to: '/logs', icon: FileClock },
  { id: 'monitoring', label: '系统监控', to: '/monitoring', icon: Gauge },
  { id: 'system', label: '系统信息', to: '/system', icon: Info },
  { id: 'users', label: '用户管理', to: '/users', icon: UserRound },
  { id: 'terminal', label: '终端', to: '/terminal', icon: TerminalSquare },
  { id: 'settings', label: '设置', to: '/settings', icon: Settings },
]

export const navigationByID = new Map(primaryNavigation.map((item) => [item.id, item]))
export const DEFAULT_NAVIGATION_ORDER = primaryNavigation.map((item) => item.id)
export const navigationLabels = Object.fromEntries(primaryNavigation.map((item) => [item.id, item.label])) as Record<string, string>
