import { describe, expect, it } from 'vitest'

import { hostPortBindingKey, presentDockerPorts } from './dockerPorts'

describe('Docker port presentation', () => {
  it('deduplicates only identical host/container protocol tuples', () => {
    const ports = presentDockerPorts([
      { hostIp: '', publicPort: 8080, privatePort: 80, protocol: 'tcp' },
      { hostIp: '', publicPort: 8080, privatePort: 80, protocol: 'tcp' },
      { hostIp: '', publicPort: 8080, privatePort: 80, protocol: 'udp' },
      { hostIp: '127.0.0.1', publicPort: 8080, privatePort: 80, protocol: 'tcp' },
    ])

    expect(ports).toHaveLength(3)
    expect(ports.map((port) => port.key)).toEqual([
      '0.0.0.0:8080/tcp->80',
      '0.0.0.0:8080/udp->80',
      '127.0.0.1:8080/tcp->80',
    ])
  })

  it('exposes a web URL only when the caller provides verified evidence', () => {
    const [port] = presentDockerPorts(
      [{ hostIp: '', publicPort: 8080, privatePort: 80, protocol: 'tcp' }],
      { webUrls: { '0.0.0.0:8080/tcp->80': 'https://nas.example.test/' } },
    )
    expect(port?.webUrl).toBe('https://nas.example.test/')
    expect(hostPortBindingKey('', 8080, 'tcp')).toBe('0.0.0.0:8080/tcp')
  })
})
