import type { Page, Route } from '@playwright/test'

const collectedAt = '2026-08-24T02:00:00.000Z'

const preferences = {
  refreshIntervalSeconds: 5,
  interfaceDensity: 'comfortable',
  baseFontSize: 15,
  pageSize: 10,
  sidebarDefault: 'expanded',
  linkOpenMode: 'same-tab',
  siteDefaultProtocol: 'http',
  chineseFont: 'system',
  latinFont: 'system',
  navigationOrder: ['overview', 'sites', 'docker', 'databases', 'logs', 'monitoring', 'system', 'users', 'terminal', 'settings'],
}

const summary = {
  collectedAt,
  host: {
    hostname: 'ncp-fixture',
    operatingSystem: 'Linux',
    kernelVersion: '6.12.0-fixture',
    architecture: 'aarch64',
    uptimeSeconds: 86400,
    processCount: 128,
  },
  cpu: { usagePercent: 24.5, logicalCores: 8, load1: 1.2, load5: 1.1, load15: 0.9 },
  memory: { totalBytes: 16 * 1024 ** 3, usedBytes: 6 * 1024 ** 3, availableBytes: 10 * 1024 ** 3 },
  storage: [{ mountpoint: '/volume2', totalBytes: 4 * 1024 ** 4, usedBytes: 18 * 1024 ** 3, freeBytes: 22 * 1024 ** 3 }],
  network: [{ name: 'bond0', receiveBytes: 1200000, transmitBytes: 800000 }],
  content: '',
  warnings: [],
}

const docker = {
  collectedAt,
  engine: {
    serverVersion: '27.5.1',
    operatingSystem: 'Linux',
    architecture: 'aarch64',
    containers: 2,
    containersRunning: 2,
    containersStopped: 0,
    images: 6,
  },
  containers: [],
  projects: [{
    id: 'media-stack',
    name: '媒体服务',
    kind: 'compose',
    state: 'running',
    workingDirectory: '/volume2/DockerProject/media-stack',
    configFiles: ['compose.yml'],
    containerCount: 2,
    runningCount: 2,
  }],
}

const sites = {
  collectedAt,
  sites: [{
    id: 'site-jellyfin',
    projectId: 'media-stack',
    name: 'Jellyfin',
    description: '家庭媒体库',
    iconUrl: '',
    category: '媒体',
    state: 'running',
    primaryPort: 8096,
    ports: [8096],
    launchUrl: 'http://ncp-fixture:8096',
    favorite: true,
    sortOrder: 1,
    lastVisitedAt: null,
    hidden: false,
    source: 'auto',
  }],
  discovery: { status: 'complete', probeAvailable: true, candidateCount: 1, verifiedCount: 1, failedCount: 0, issues: [] },
}

const systemCapabilities = {
  hostname: 'ncp-fixture',
  architecture: 'aarch64',
  docker: true,
  compose: true,
  systemd: true,
  journald: true,
  cgroupVersion: 2,
  procReadable: true,
  sysReadable: true,
  hostTerminal: true,
  dataVolumes: ['/volume2'],
  networkInterfaces: ['bond0'],
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
}

export async function installMockApi(page: Page) {
  await page.addInitScript((snapshot) => {
    class MockEventSource extends EventTarget {
      static readonly CONNECTING = 0
      static readonly OPEN = 1
      static readonly CLOSED = 2
      readonly url: string
      readonly withCredentials = false
      readyState = MockEventSource.CONNECTING
      onopen: ((event: Event) => void) | null = null
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      onerror: ((event: Event) => void) | null = null

      constructor(url: string) {
        super()
        this.url = url
        setTimeout(() => {
          if (this.readyState === MockEventSource.CLOSED) return
          this.readyState = MockEventSource.OPEN
          const openEvent = new Event('open')
          this.onopen?.(openEvent)
          this.dispatchEvent(openEvent)
          this.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify(snapshot) }))
        }, 0)
      }

      close() {
        this.readyState = MockEventSource.CLOSED
      }
    }

    window.EventSource = MockEventSource as unknown as typeof EventSource
  }, { collectedAt, summary, docker })

  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname

    if (path === '/api/v1/auth/status') return json(route, { initialized: true, authenticated: true, user: { id: 1, username: 'root', role: 'root' } })
    if (path === '/api/v1/preferences') return json(route, preferences)
    if (path === '/api/v1/sites') return json(route, sites)
    if (path === '/api/v1/sites/ignored') return json(route, [])
    if (path === '/api/v1/system/events') {
      const body = `event: snapshot\ndata: ${JSON.stringify({ collectedAt, summary, docker })}\n\n`
      return route.fulfill({ status: 200, headers: { 'cache-control': 'no-cache', 'content-type': 'text/event-stream' }, body })
    }
    if (path === '/api/v1/system/capabilities') return json(route, systemCapabilities)
    if (path === '/api/v1/monitoring/samples') return json(route, [])
    if (path === '/api/v1/logs') return json(route, { collectedAt, entries: [], nextCursor: '' })
    if (path === '/api/v1/users') return json(route, [])
    if (path === '/api/v1/users/password-policy') return json(route, { minLength: 6, requireUppercase: false, requireLowercase: false, requireDigit: false, requireSpecial: false })
    return json(route, {})
  })
}
