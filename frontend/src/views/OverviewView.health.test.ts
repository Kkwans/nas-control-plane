// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerInventory, SystemSummary } from '@/api/system'
import type { SystemConnectionState } from '@/stores/system'
import OverviewView from './OverviewView.vue'

vi.mock('vue-router', () => ({ RouterLink: { render: () => null } }))
vi.mock('@/components/RealtimeTrendChart.vue', () => ({ default: { render: () => null } }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => store }))
vi.mock('@/stores/sites', () => ({ useSitesStore: () => ({ visibleSites: [], collectedAt: '2026-10-13T00:00:00Z', loading: false, error: null, refresh: vi.fn(), visit: vi.fn() }) }))
const summary: SystemSummary = {
  collectedAt: '2026-10-13T00:00:00Z',
  host: { hostname: 'test', operatingSystem: 'Linux', kernelVersion: 'test', architecture: 'arm64', uptimeSeconds: 100, processCount: 1 },
  cpu: { usagePercent: 20, logicalCores: 4, load1: 1, load5: 1, load15: 1 },
  memory: { totalBytes: 100, usedBytes: 40, availableBytes: 60 },
  storage: [{ mountpoint: '/', totalBytes: 100, usedBytes: 20, freeBytes: 80 }],
  network: [{ name: 'eth0', receiveBytes: 100, transmitBytes: 50 }], warnings: [],
}
const inventory: DockerInventory = {
  collectedAt: summary.collectedAt, containers: [], projects: [],
  engine: { serverVersion: 'test', operatingSystem: 'Linux', architecture: 'arm64', containers: 0, containersRunning: 0, containersStopped: 0, images: 0 },
}
function state() {
  return reactive({ summary: structuredClone(summary), inventory, services: [], resourceHistory: [], lastUpdated: summary.collectedAt, connectionState: 'connected' as SystemConnectionState, connectionLabel: '数据已同步', connectionDetail: '', preferences: { linkOpenMode: 'new-tab', siteDefaultProtocol: 'http' } })
}
let store: ReturnType<typeof state>
let app: App | undefined
let host: HTMLDivElement
beforeEach(() => { store = state() })
afterEach(() => { app?.unmount(); host?.remove() })
function mount() {
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(OverviewView) }); app.mount(host)
}

describe('Overview 实际状态与指标', () => {
  it('旧缓存降级时不再显示运行稳定，保留可读的旧值和真实更新时间', async () => {
    mount()
    expect(host.textContent).toContain('运行稳定')
    store.connectionState = 'degraded'
    store.connectionLabel = '显示上次数据'
    store.connectionDetail = '已显示的数据超过 15 秒。'
    await nextTick()
    expect(host.textContent).not.toContain('运行稳定')
    expect(host.textContent).toContain('显示上次数据')
    expect(host.querySelector('.snapshot-health-note')?.textContent).toContain('上次更新')
    expect(host.querySelector('.metric-card strong')?.textContent).toBe('20.0%')
    store.connectionState = 'connected'
    await nextTick()
    expect(host.textContent).toContain('运行稳定')
  })

  it('CPU、内存和存储源警告显示不可用，不把默认零显示为健康数值', async () => {
    mount()
    store.summary.warnings = ['cpu', 'memory', 'storage'].map((source) => ({ source, code: 'SOURCE_FAILED' }))
    store.summary.cpu.usagePercent = 0
    store.summary.memory.totalBytes = 0
    store.summary.storage = []
    store.connectionState = 'degraded'
    store.connectionLabel = '部分指标不可用'
    store.connectionDetail = '部分采集源暂不可用。'
    await nextTick()
    const metrics = [...host.querySelectorAll('.metric-card')].slice(0, 3)
    for (const metric of metrics) {
      expect(metric.querySelector('strong')?.textContent).toBe('—')
      expect(metric.textContent).toContain('数据不可用')
    }
    expect(host.textContent).not.toContain('运行稳定')
    expect(host.querySelector('.storage-row')).toBeNull()
  })
})
