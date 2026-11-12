import { describe, expect, it } from 'vitest'
import { DOCKER_INVENTORY_RESPONSE_INVALID, parseDockerInventoryResponse } from './dockerInventoryResponse'
import { NcpApiError } from './systemTransport'

function fixture(): Record<string, unknown> {
  return {
    collectedAt: '2026-10-13T00:00:00Z',
    engine: {
      serverVersion: '28.0.0', operatingSystem: 'Linux', architecture: 'aarch64',
      containers: 1, containersRunning: 1, containersStopped: 0, images: 2,
    },
    containers: [{
      id: 'container-1', name: 'web', image: 'nginx:latest', state: 'running', status: 'Up 1 hour',
      createdAt: '2026-10-12T00:00:00Z', startedAt: '2026-10-13T00:00:00Z',
      finishedAt: '0001-01-01T00:00:00Z', exitCode: 0, restartCount: 1, health: 'healthy',
      ports: [{ hostIp: '0.0.0.0', privatePort: 80, publicPort: 8080, protocol: 'tcp' }],
      projectId: 'compose:web', projectName: 'web', serviceName: 'web',
    }],
    projects: [{
      id: 'compose:web', name: 'web', kind: 'compose', state: 'running',
      workingDirectory: '/compose/web', configFiles: ['/compose/web/compose.yml'], containerCount: 1, runningCount: 1,
    }],
  }
}

function set(value: Record<string, unknown>, path: string, replacement: unknown) {
  const keys = path.split('.')
  let current = value
  for (const key of keys.slice(0, -1)) current = current[key] as Record<string, unknown>
  current[keys.at(-1)!] = replacement
}

const requiredStrings = [
  'collectedAt', 'engine.serverVersion', 'engine.operatingSystem', 'engine.architecture',
  ...['id', 'name', 'image', 'state', 'status', 'createdAt', 'projectId', 'projectName', 'serviceName'].map((key) => `containers.0.${key}`),
  'containers.0.ports.0.hostIp', 'containers.0.ports.0.protocol',
  'projects.0.id', 'projects.0.name', 'projects.0.workingDirectory',
]
const requiredCounts = [
  'engine.containers', 'engine.containersRunning', 'engine.containersStopped', 'engine.images',
  'projects.0.containerCount', 'projects.0.runningCount',
]
const collections = ['containers', 'projects', 'containers.0.ports', 'projects.0.configFiles']
const optionalFields = ['startedAt', 'finishedAt', 'exitCode', 'restartCount', 'health']

function expectInvalid(value: unknown) {
  expect(() => parseDockerInventoryResponse(value)).toThrow(expect.objectContaining({
    name: 'NcpApiError', code: DOCKER_INVENTORY_RESPONSE_INVALID,
  }))
}

describe('Docker 库存响应边界', () => {
  it('接受完整响应并投影为独立的已知字段对象', () => {
    const value = fixture()
    const expected = structuredClone(value)
    for (const path of ['future', 'engine.future', 'containers.0.future', 'containers.0.ports.0.future', 'projects.0.future']) {
      set(value, path, { secret: '不进入库存状态' })
    }
    const before = structuredClone(value)
    const parsed = parseDockerInventoryResponse(value)
    expect(parsed).toEqual(expected)
    expect(value).toEqual(before)
    expect(parsed).not.toBe(value)
    expect(parsed.engine).not.toBe(value.engine)
    expect(parsed.containers).not.toBe(value.containers)
    expect(parsed.containers[0]).not.toBe((value.containers as unknown[])[0])
    expect(parsed.projects[0]?.configFiles).not.toBe((value.projects as Array<{ configFiles: unknown }>)[0]?.configFiles)
  })

  it.each([null, undefined, [], {}, false, 1, 'inventory'])('拒绝错误根值 %j', (value) => {
    expectInvalid(value)
  })

  it.each(['engine', 'containers.0', 'containers.0.ports.0', 'projects.0'])('拒绝非对象节点 %s', (path) => {
    for (const bad of [null, undefined, [], {}, false, 'object', 0]) {
      const value = fixture()
      set(value, path, bad)
      expectInvalid(value)
    }
  })

  it.each(requiredStrings)('必填字符串 %s 不能缺省、为空值或为其他类型', (path) => {
    for (const bad of [undefined, null, 0, false, [], {}]) {
      const value = fixture()
      set(value, path, bad)
      expectInvalid(value)
    }
  })

  it.each(requiredCounts)('计数字段 %s 只接受有限非负整数', (path) => {
    for (const bad of [undefined, null, -1, 0.5, NaN, Infinity, -Infinity, '1', true]) {
      const value = fixture()
      set(value, path, bad)
      expectInvalid(value)
    }
    for (const valid of [0, 2 ** 60]) {
      const value = fixture()
      set(value, path, valid)
      expect(() => parseDockerInventoryResponse(value)).not.toThrow()
    }
  })

  it.each(collections)('仅将集合 %s 的显式 null 归一为空数组', (path) => {
    for (const replacement of [null, []]) {
      const value = fixture()
      set(value, path, replacement)
      const before = structuredClone(value)
      const parsed = parseDockerInventoryResponse(value)
      const expected = structuredClone(before)
      set(expected, path, [])
      expect(parsed).toEqual(expected)
      expect(value).toEqual(before)
    }
    for (const bad of [undefined, {}, '', false, 1, [null], [undefined], [{}], Array(1)]) {
      const value = fixture()
      set(value, path, bad)
      expectInvalid(value)
    }
  })

  it('拒绝混入坏行的整个库存而不是静默删除坏行', () => {
    for (const collection of ['containers', 'projects']) {
      const value = fixture()
      ;(value[collection] as unknown[]).push(null)
      expectInvalid(value)
    }
    const value = fixture()
    set(value, 'containers.0.ports', [{ hostIp: '', privatePort: 0, publicPort: 0, protocol: '' }, null])
    expectInvalid(value)
    set(value, 'containers.0.ports', [])
    set(value, 'projects.0.configFiles', ['compose.yml', 1])
    expectInvalid(value)
  })

  it.each(['privatePort', 'publicPort'])('端口 %s 只接受 uint16 范围内的整数', (key) => {
    for (const bad of [undefined, null, -1, 65536, 1.5, Infinity, -Infinity, NaN, '80', false]) {
      const value = fixture()
      set(value, `containers.0.ports.0.${key}`, bad)
      expectInvalid(value)
    }
    for (const valid of [0, 65535]) {
      const value = fixture()
      set(value, `containers.0.ports.0.${key}`, valid)
      expect(() => parseDockerInventoryResponse(value)).not.toThrow()
    }
  })

  it('兼容五个可选补充字段缺省，并拒绝其显式 null', () => {
    const value = fixture()
    for (const key of optionalFields) set(value, `containers.0.${key}`, undefined)
    const parsed = parseDockerInventoryResponse(value)
    for (const key of optionalFields) expect(parsed.containers[0]).not.toHaveProperty(key)
    for (const key of optionalFields) {
      const malformed = fixture()
      set(malformed, `containers.0.${key}`, null)
      expectInvalid(malformed)
    }
  })

  it.each(['startedAt', 'finishedAt'])('可选时间 %s 仍需为字符串', (key) => {
    const value = fixture()
    set(value, `containers.0.${key}`, 123)
    expectInvalid(value)
  })

  it('exitCode 允许负整数，restartCount 仅允许非负整数', () => {
    for (const key of ['exitCode', 'restartCount']) {
      for (const bad of [1.5, Infinity, -Infinity, NaN, '0', false]) {
        const value = fixture()
        set(value, `containers.0.${key}`, bad)
        expectInvalid(value)
      }
    }
    const value = fixture()
    set(value, 'containers.0.exitCode', -137)
    set(value, 'containers.0.restartCount', 0)
    expect(parseDockerInventoryResponse(value).containers[0]).toMatchObject({ exitCode: -137, restartCount: 0 })
    set(value, 'containers.0.restartCount', -1)
    expectInvalid(value)
  })

  it.each([
    ['projects.0.kind', ['compose', 'standalone']],
    ['projects.0.state', ['running', 'stopped', 'degraded']],
    ['containers.0.health', ['none', 'starting', 'healthy', 'unhealthy']],
  ] as const)('校验封闭枚举 %s', (path, values) => {
    for (const valid of values) {
      const value = fixture()
      set(value, path, valid)
      expect(() => parseDockerInventoryResponse(value)).not.toThrow()
    }
    for (const bad of ['', 'unknown', 1, false, null]) {
      const value = fixture()
      set(value, path, bad)
      expectInvalid(value)
    }
    if (path.startsWith('projects.')) {
      const value = fixture()
      set(value, path, undefined)
      expectInvalid(value)
    }
  })

  it('允许空字符串、Go 零时间、未知容器状态和协议，不添加格式或数量一致性约束', () => {
    const value = fixture()
    for (const path of requiredStrings) set(value, path, '')
    set(value, 'containers.0.state', 'future-engine-state')
    set(value, 'containers.0.ports.0.protocol', 'future-protocol')
    set(value, 'containers.0.createdAt', '0001-01-01T00:00:00Z')
    set(value, 'containers.0.startedAt', '')
    set(value, 'containers.0.finishedAt', 'unformatted-time')
    set(value, 'projects.0.configFiles', ['', 'relative-file'])
    set(value, 'engine.containers', 0)
    set(value, 'projects.0.containerCount', 0)
    set(value, 'projects.0.runningCount', 100)
    expect(parseDockerInventoryResponse(value)).toEqual(value)
  })

  it('所有解码失败均返回稳定 API 错误', () => {
    try {
      parseDockerInventoryResponse(null)
      expect.unreachable()
    } catch (error) {
      expect(error).toBeInstanceOf(NcpApiError)
      expect(error).toMatchObject({ code: DOCKER_INVENTORY_RESPONSE_INVALID })
    }
  })
})
