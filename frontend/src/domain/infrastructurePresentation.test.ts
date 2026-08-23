import { describe, expect, it } from 'vitest'

import type { SystemDetails } from '@/api/system'

import {
  blockDeviceDescription,
  blockDeviceKindLabel,
  blockDeviceTransport,
  formatBytes,
  formatDuration,
  interfaceAddress,
  interfaceIsOnline,
  interfaceStateLabel,
  listeningPortOwners,
  listeningSourceLabel,
  mihomoModeLabel,
  proxyStateLabel,
} from './infrastructurePresentation'

describe('infrastructure presentation helpers', () => {
  it('formats capacity and uptime with stable empty-state labels', () => {
    expect(formatBytes(0)).toBe('—')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatDuration(90061)).toBe('1 天 1 小时')
    expect(formatDuration(0)).toBe('—')
  })

  it('prefers IPv4 and distinguishes known interface state', () => {
    const item = {
      name: 'tailscale0',
      state: 'up',
      lowerUp: true,
      lowerUpKnown: true,
      addresses: [
        { address: '2001:db8::1', prefixLength: 128, family: 'ipv6' },
        { address: '100.64.0.2', prefixLength: 32, family: 'ipv4' },
      ],
    } as SystemDetails['network']['interfaces'][number]

    expect(interfaceIsOnline(item)).toBe(true)
    expect(interfaceStateLabel(item)).toBe('Overlay 可用')
    expect(interfaceAddress(item)?.address).toBe('100.64.0.2')
  })

  it('explains listener ownership and source labels without exposing raw source names', () => {
    const item = {
      pid: 0,
      containerId: '0123456789abcdef',
      detectionErrorCode: 'LISTENING_PORT_CONTAINER_NAME_UNAVAILABLE',
    } as SystemDetails['network']['listeningPorts'][number]

    expect(listeningPortOwners(item)).toEqual([{
      label: '0123456789ab',
      detail: 'Docker 容器名未知 · Docker CLI 未返回容器名称',
    }])
    expect(listeningSourceLabel(['docker', 'systemd', 'gopsutil', 'custom'])).toBe('Docker 容器映射、进程与系统服务、系统连接表、custom')
  })

  it('keeps proxy and block-device labels product-facing', () => {
    const disk = {
      kind: 'emmc-boot',
      rotational: false,
      description: '',
      transport: 'emmc',
    } as SystemDetails['storage']['disks'][number]

    expect(blockDeviceKindLabel(disk)).toBe('eMMC 启动区')
    expect(blockDeviceDescription(disk)).toBe('eMMC 启动区，用途由系统管理')
    expect(blockDeviceTransport(disk)).toBe('eMMC')
    expect(proxyStateLabel('running', true)).toBe('代理核心运行中')
    expect(mihomoModeLabel('rule')).toBe('规则模式')
  })
})
