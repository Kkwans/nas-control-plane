import { describe, expect, it, vi } from 'vitest'
import { parseSystemDetailsResponse } from './systemDetailsResponse'
import { requestSystemDetails } from './systemInfo'

// 与浏览器 fixture 的服务端字段保持一致；测试只使用内存数据。
const collectedAt = '2026-08-24T02:00:00.000Z'
const systemDetails = {
  collectedAt,
  warnings: [],
  device: {
    hostname: 'ncp-fixture',
    model: 'DH4300Plus Fixture',
    operatingSystem: 'Linux',
    kernelVersion: '6.12.0-fixture',
    architecture: 'aarch64',
    uptimeSeconds: 86400,
    processCount: 128,
    cgroupVersion: 'v2',
  },
  hardware: {
    cpu: {
      model: 'ARM64 NAS CPU',
      physicalCores: 4,
      logicalCores: 8,
      frequencyMHz: 1800,
      temperatureCelsius: 42,
    },
    memory: { totalBytes: 16 * 1024 ** 3, availableBytes: 10 * 1024 ** 3 },
    sensors: [],
  },
  network: {
    interfaces: [
      {
        name: 'bond0', hardwareAddress: '02:00:00:00:00:01', mtu: 1500, state: 'up',
        lowerUp: true, lowerUpKnown: true, speedMbps: 1000, duplex: 'full',
        addresses: [{ address: '192.168.5.10', prefixLength: 24, family: 'ipv4' }],
      },
      {
        name: 'tailscale0', hardwareAddress: '', mtu: 1280, state: 'up',
        lowerUp: true, lowerUpKnown: true, speedMbps: 0, duplex: '',
        addresses: [{ address: '100.64.0.10', prefixLength: 32, family: 'ipv4' }],
      },
      {
        name: 'docker0', hardwareAddress: '02:42:00:00:00:01', mtu: 1500, state: 'down',
        lowerUp: false, lowerUpKnown: true, speedMbps: 0, duplex: '',
        addresses: [{ address: '172.17.0.1', prefixLength: 16, family: 'ipv4' }],
      },
    ],
    gateway: '192.168.5.1',
    routes: [
      { destination: '0.0.0.0/0', gateway: '192.168.5.1', interface: 'bond0', metric: 100 },
      { destination: '192.168.5.0/24', gateway: '', interface: 'bond0', metric: 100 },
    ],
    dnsServers: ['192.168.5.1', '240c::6666'],
    listeningPorts: [
      { protocol: 'tcp', address: '0.0.0.0', port: 8760, pid: 120, processName: 'ncp-console', containerName: 'ncp-console', detectionSource: 'docker' },
      { protocol: 'tcp', address: '127.0.0.1', port: 8750, pid: 121, processName: 'ncp-server', systemdUnit: 'ncp-server.service', detectionSource: 'proc' },
    ],
  },
  storage: {
    mounts: [{ path: '/volume2', device: '/dev/md0', filesystem: 'btrfs', totalBytes: 4 * 1024 ** 4, usedBytes: 18 * 1024 ** 3, availableBytes: 4 * 1024 ** 4 - 18 * 1024 ** 3, usedPercent: 0.42 }],
    disks: [{ name: 'sda', model: 'Fixture SSD', sizeBytes: 4 * 1024 ** 4, rotational: false, kind: 'physical', role: 'data', transport: 'sata', description: '测试数据盘', health: 'healthy', temperatureCelsius: 38 }],
    raid: [],
  },
  proxy: {
    mihomo: { detected: false, state: 'not-found', detail: '未发现本地代理核心' },
    system: [],
    associations: [],
  },
  tailscale: {
    detected: true, state: 'running', backendState: 'Running', version: '1.86.2', interface: 'tailscale0',
    overlayIps: ['100.64.0.10'], online: true, linkState: 'up', heartbeatState: 'healthy', reachable: true,
    evidence: [{ source: 'fixture', status: 'reachable', detail: '模拟 Overlay 接口和地址' }], warnings: [],
  },
  dns: {
    backend: 'static-resolv-conf', detected: true, state: 'available', readOnly: true,
    canRead: true, canPreview: false, canConfirm: false, canRollback: false,
    nameservers: ['192.168.5.1', '240c::6666'], configuredNameservers: [],
    detectionSource: 'fixture', errorCode: 'DNS_BACKEND_READ_ONLY',
  },
  publicEgress: {
    configured: false, status: 'unavailable', endpoint: '', requiresUserAction: true,
    detectionSource: 'fixture', errorCode: 'PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED',
  },
  control: {
    nodes: [{ id: 'ncp-agent', name: 'ncp-agent', detail: 'Root Agent · Unix Socket', status: 'ready', version: '2026.8.24-v1', lastSeen: collectedAt }],
  },
}

function fixture(): Record<string, unknown> {
  return structuredClone(systemDetails)
}

function set(value: Record<string, unknown>, path: string, replacement: unknown) {
  const keys = path.split('.')
  let current = value
  for (const key of keys.slice(0, -1)) current = current[key] as Record<string, unknown>
  current[keys.at(-1)!] = replacement
}

const malformed: Array<[string, unknown]> = [
  ['warnings', [null]], ['device', []], ['device.hostname', null], ['device.processCount', '0'],
  ['device.uptimeSeconds', -1], ['hardware.cpu.frequencyMHz', Infinity], ['hardware.cpu', {}],
  ['hardware.memory.availableBytes', NaN], ['hardware.sensors', [null]],
  ['network.interfaces', [null]], ['network.interfaces.0.name', 1],
  ['network.interfaces.0.addresses', [null]], ['network.interfaces.0.addresses.0.prefixLength', '24'],
  ['network.interfaces.0.lowerUp', 1], ['network.interfaces.0.speedMbps', 1.5], ['network.interfaces.0.speedMbps', -2],
  ['network.routes', [null]], ['network.routes.0.metric', '0'], ['network.dnsServers', [1]],
  ['network.listeningPorts', [null]], ['network.listeningPorts.0.systemdUnit', 3],
  ['network.listeningPorts.0.port', 65536], ['network.listeningPorts.0.detectionSources', [null]],
  ['storage.mounts', [null]], ['storage.mounts.0.path', 1], ['storage.mounts.0.usedPercent', '1'],
  ['storage.mounts.0.totalBytes', -1], ['storage.disks', [null]], ['storage.disks.0.kind', 'invalid'],
  ['storage.disks.0.rotational', 'false'], ['storage.raid', [{ name: '', level: '', state: '', devices: [null] }]],
  ['proxy.mihomo', []], ['proxy.mihomo.detected', 'false'], ['proxy.mihomo.operations', [null]],
  ['proxy.system', [null]], ['proxy.associations', [null]], ['proxy.mihomoCapability', {}],
  ['tailscale', null], ['tailscale.overlayIps', [null]], ['tailscale.evidence', [null]],
  ['tailscale.warnings', [{ code: 1, source: '' }]], ['dns', {}], ['dns.nameservers', [false]],
  ['dns.canConfirm', 'true'], ['publicEgress.configured', 'false'], ['control.nodes', [null]],
  ['control.nodes.0.lastSeen', false],
]

describe('系统详情响应边界', () => {
  it('接受完整 fixture，保留附加字段且不修改输入', () => {
    const value = { ...fixture(), future: { enabled: true } }
    expect(parseSystemDetailsResponse(value)).toMatchObject(value)
    expect(parseSystemDetailsResponse(value)).not.toBe(value)
  })

  it.each(malformed)('拒绝错误字段 %s = %j', (path, replacement) => {
    const value = fixture()
    set(value, path, replacement)
    expect(() => parseSystemDetailsResponse(value)).toThrow(expect.objectContaining({ code: 'SYSTEM_DETAILS_RESPONSE_INVALID' }))
  })

  it.each([null, [], {}, false, 'details'])('拒绝错误顶层 %j', (value) => {
    expect(() => parseSystemDetailsResponse(value)).toThrow(expect.objectContaining({ code: 'SYSTEM_DETAILS_RESPONSE_INVALID' }))
  })

  it('保留零、空字符串、负温度、未知速率和可选字段缺省', () => {
    const value = fixture()
    for (const [path, replacement] of [
      ['device.hostname', ''], ['device.processCount', 0], ['device.uptimeSeconds', 0],
      ['hardware.cpu.temperatureCelsius', -10], ['network.interfaces.0.speedMbps', -1],
      ['storage.disks.0.kind', ''], ['storage.disks.0.role', ''], ['storage.mounts.0.totalBytes', 0],
      ['tailscale', undefined], ['dns', undefined], ['publicEgress', undefined],
    ] as Array<[string, unknown]>) set(value, path, replacement)
    expect(parseSystemDetailsResponse(value)).toMatchObject({
      device: { hostname: '', processCount: 0 }, network: { interfaces: [{ speedMbps: -1 }, {}, {}] },
      storage: { disks: [{ kind: '', role: '' }] },
    })
  })

  it('接受大于 MAX_SAFE_INTEGER 的 Go uint64 容量，不额外承诺 JS 整数精度', () => {
    const value = fixture()
    const capacity = 2 ** 60
    set(value, 'storage.mounts.0.totalBytes', capacity)
    set(value, 'storage.disks.0.sizeBytes', capacity)
    set(value, 'hardware.memory.totalBytes', capacity)
    expect(() => parseSystemDetailsResponse(value)).not.toThrow()
  })

  it('只将已知嵌套 Go null 切片转为空数组', () => {
    const value = fixture()
    const paths = ['network.interfaces.0.addresses', 'network.listeningPorts.0.detectionSources', 'proxy.mihomo.operations', 'tailscale.overlayIps', 'tailscale.evidence', 'tailscale.warnings', 'dns.nameservers', 'dns.configuredNameservers']
    for (const path of paths) set(value, path, null)
    set(value, 'storage.raid', [{ name: '', level: '', state: '', devices: null }])
    const parsed = parseSystemDetailsResponse(value)
    expect(parsed.network.interfaces[0]?.addresses).toEqual([])
    expect(parsed.network.listeningPorts[0]?.detectionSources).toEqual([])
    expect(parsed.storage.raid[0]?.devices).toEqual([])
    expect(parsed.proxy.mihomo.operations).toEqual([])
    expect(parsed.tailscale).toMatchObject({ overlayIps: [], evidence: [], warnings: [] })
    expect(parsed.dns).toMatchObject({ nameservers: [], configuredNameservers: [] })
    expect((value.network as typeof systemDetails.network).interfaces[0]?.addresses).toBeNull()
  })

  it.each(['warnings', 'hardware.sensors', 'network.interfaces', 'network.routes', 'network.dnsServers', 'network.listeningPorts', 'storage.mounts', 'storage.disks', 'storage.raid', 'proxy.system', 'proxy.associations', 'control.nodes'])('顶层集合 %s 允许空数组但拒绝 null 或缺失', (path) => {
    const value = fixture()
    set(value, path, [])
    expect(() => parseSystemDetailsResponse(value)).not.toThrow()
    for (const replacement of [null, undefined]) {
      set(value, path, replacement)
      expect(() => parseSystemDetailsResponse(value)).toThrow()
    }
  })


  it('校验可选 Mihomo 的控制器、证据与警告并兼容其 null 切片', () => {
    const value = fixture()
    const capability = {
      detected: false, state: '', processName: '', executable: '', version: '',
      controller: { detected: false, endpoint: '', reachable: false, authRequired: false, tokenConfigured: false, operations: null, detectionSource: '' },
      evidence: null, warnings: null,
    }
    set(value, 'proxy.mihomoCapability', capability)
    expect(parseSystemDetailsResponse(value).proxy.mihomoCapability).toMatchObject({ controller: { operations: [] }, evidence: [], warnings: [] })
    for (const [path, bad] of [
      ['proxy.mihomoCapability.controller', null],
      ['proxy.mihomoCapability.controller.operations', [null]],
      ['proxy.mihomoCapability.evidence', [{}]],
      ['proxy.mihomoCapability.warnings', [{ code: '', source: null }]],
    ] as Array<[string, unknown]>) {
      const malformed = structuredClone(value)
      set(malformed, path, bad)
      expect(() => parseSystemDetailsResponse(malformed)).toThrow()
    }
  })

  it('已识别的必填嵌套切片缺失时仍拒绝', () => {
    for (const path of ['network.interfaces.0.addresses', 'tailscale.overlayIps', 'tailscale.evidence', 'tailscale.warnings', 'dns.nameservers']) {
      const value = fixture()
      set(value, path, undefined)
      expect(() => parseSystemDetailsResponse(value)).toThrow()
    }
  })

  it('请求携带原有 signal 和凭据，并在发布坏数据前返回稳定错误码', async () => {
    const value = fixture()
    set(value, 'storage.mounts', [null])
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(value)))
    const signal = new AbortController().signal
    await expect(requestSystemDetails(fetcher, signal)).rejects.toMatchObject({ code: 'SYSTEM_DETAILS_RESPONSE_INVALID' })
    expect(fetcher).toHaveBeenCalledWith('/api/v1/system/details', expect.objectContaining({ signal, credentials: 'same-origin' }))
  })
})
