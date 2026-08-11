import { describe, expect, it } from 'vitest'

import {
  classifyListenerScope,
  editableDNSNameservers,
  isAuxiliaryNetworkInterface,
  isSubscriptionStatusNodeName,
  networkInterfaceKindLabel,
} from './network'

describe('classifyListenerScope', () => {
  it.each([
    { addresses: ['0.0.0.0', '::'], expected: 'all-interfaces' },
    { addresses: ['127.0.0.1', '::1'], expected: 'loopback' },
    { addresses: ['192.168.5.110'], expected: 'lan' },
    { addresses: ['172.24.0.1'], expected: 'container' },
    { addresses: ['100.66.66.66', 'fd7a:115c:a1e0::7f01:adaa'], expected: 'overlay' },
    { addresses: ['203.0.113.10'], expected: 'public' },
  ])('classifies $addresses as $expected', ({ addresses, expected }) => {
    expect(classifyListenerScope(addresses).value).toBe(expected)
  })

  it('uses the live Overlay IP list for non-standard VPN ranges', () => {
    expect(classifyListenerScope(['10.42.0.8'], ['10.42.0.8']).value).toBe('overlay')
  })

  it('uses the most exposed scope when a group contains multiple addresses', () => {
    expect(classifyListenerScope(['127.0.0.1', '0.0.0.0']).value).toBe('all-interfaces')
  })
})

describe('editableDNSNameservers', () => {
  it('uses backend-managed values instead of the larger effective resolver list', () => {
    expect(editableDNSNameservers({
      nameservers: ['240c::6666', '240c::6644', '192.168.5.1'],
      configuredNameservers: ['240c::6666', '192.168.5.1'],
    })).toEqual(['240c::6666', '192.168.5.1'])
  })

  it('falls back to effective resolvers for read-only backends', () => {
    expect(editableDNSNameservers({ nameservers: ['1.1.1.1'], configuredNameservers: [] })).toEqual(['1.1.1.1'])
  })
})

describe('network interface presentation', () => {
  it('moves the Mihomo Meta TUN interface out of primary connections', () => {
    expect(isAuxiliaryNetworkInterface('Meta')).toBe(true)
    expect(networkInterfaceKindLabel('Meta')).toBe('Mihomo TUN 虚拟接口')
  })

  it('keeps physical and Tailscale interfaces in the primary group', () => {
    expect(isAuxiliaryNetworkInterface('eth0')).toBe(false)
    expect(isAuxiliaryNetworkInterface('tailscale0')).toBe(false)
    expect(networkInterfaceKindLabel('tailscale0')).toBe('Tailscale Overlay 接口')
  })
})

describe('Mihomo node presentation', () => {
  it.each(['剩余流量：388.49 GB', '套餐到期 2026-12-31', 'Traffic 82%'])('recognizes subscription status node %s', (name) => {
    expect(isSubscriptionStatusNodeName(name)).toBe(true)
  })

  it('does not relabel a normal node name', () => {
    expect(isSubscriptionStatusNodeName('香港 IEPL 01')).toBe(false)
  })
})
