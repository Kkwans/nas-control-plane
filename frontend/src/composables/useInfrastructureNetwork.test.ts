import { computed, nextTick, ref } from 'vue'
import { describe, expect, it } from 'vitest'

import type { SystemDetails, TailscaleCapability } from '@/api/system'

import { useInfrastructureNetwork } from './useInfrastructureNetwork'

function createDetails(): SystemDetails {
  return {
    network: {
      interfaces: [
        { name: 'eth0', hardwareAddress: '', mtu: 1500, state: 'up', lowerUp: true, lowerUpKnown: true, speedMbps: 1000, duplex: 'full', addresses: [] },
        { name: 'tailscale0', hardwareAddress: '', mtu: 1280, state: 'up', lowerUp: true, lowerUpKnown: true, speedMbps: 0, duplex: '', addresses: [] },
        { name: 'Meta', hardwareAddress: '', mtu: 9000, state: 'up', lowerUp: true, lowerUpKnown: true, speedMbps: 0, duplex: '', addresses: [] },
      ],
      listeningPorts: [
        { protocol: 'tcp', port: 80, address: '0.0.0.0', pid: 10, processName: 'nginx', detectionSources: ['proc'], detectionSource: 'proc' },
        { protocol: 'tcp', port: 80, address: '::', pid: 10, processName: 'nginx', detectionSources: ['proc'], detectionSource: 'proc' },
        { protocol: 'tcp', port: 443, address: '127.0.0.1', pid: 11, processName: 'admin', detectionSources: ['proc'], detectionSource: 'proc' },
      ],
    },
  } as unknown as SystemDetails
}

function createTailscaleDetails(): TailscaleCapability {
  return {
    detected: true,
    state: 'running',
    backendState: 'Running',
    version: '1.0.0',
    interface: 'tailscale0',
    overlayIps: [],
    online: true,
    linkState: 'up',
    heartbeatState: 'ok',
    reachable: true,
    evidence: [],
    warnings: [],
  }
}

describe('useInfrastructureNetwork', () => {
  it('keeps primary interfaces separate from auxiliary interfaces and groups listeners', () => {
    const details = ref<SystemDetails | null>(createDetails())
    const network = useInfrastructureNetwork(details, computed(createTailscaleDetails))

    expect(network.primaryInterfaces.value.map((item) => item.name)).toEqual(['eth0', 'tailscale0'])
    expect(network.secondaryInterfaces.value.map((item) => item.name)).toEqual(['Meta'])
    expect(network.listeningPortGroups.value).toHaveLength(2)
    expect(network.exposedListenerCount.value).toBe(1)
    expect(network.localListenerCount.value).toBe(1)
  })

  it('filters listener groups and resets the visible window after a filter change', async () => {
    const details = ref<SystemDetails | null>(createDetails())
    const network = useInfrastructureNetwork(details, computed(createTailscaleDetails))

    network.showMoreListeningPorts()
    expect(network.listenerVisibleLimit.value).toBe(48)
    network.listenerQuery.value = '443'
    await nextTick()
    expect(network.filteredListeningPortGroups.value.map((item) => item.port)).toEqual([443])
    expect(network.listenerVisibleLimit.value).toBe(24)
  })
})
