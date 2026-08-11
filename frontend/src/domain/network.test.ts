import { describe, expect, it } from 'vitest'

import { classifyListenerScope } from './network'

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
