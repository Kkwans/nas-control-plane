import { afterEach, describe, expect, it, vi } from 'vitest'
import { requestDockerInventory } from './docker'
import { parseDockerInventoryResponse } from './dockerInventoryResponse'
import { subscribeSystemEvents } from './systemInfo'
import type { DockerInventory } from './system'

const collectedAt = '2026-10-13T00:00:00Z'
function fixture(): DockerInventory {
  return {
    collectedAt,
    engine: { serverVersion: '28.0.0', operatingSystem: 'Linux', architecture: 'aarch64', containers: 1, containersRunning: 1, containersStopped: 0, images: 1 },
    containers: [{
      id: 'container-1', name: 'web', image: 'nginx:latest', state: 'running', status: 'Up', createdAt: collectedAt,
      ports: [{ hostIp: '', privatePort: 80, publicPort: 8080, protocol: 'tcp' }],
      projectId: 'compose:web', projectName: 'web', serviceName: 'web',
    }],
    projects: [{ id: 'compose:web', name: 'web', kind: 'compose', state: 'running', workingDirectory: '/web', configFiles: ['compose.yml'], containerCount: 1, runningCount: 1 }],
  }
}
const summary = { collectedAt, host: {}, cpu: {}, memory: {}, storage: [], network: [], warnings: [] }
const invalidCode = 'DOCKER_INVENTORY_RESPONSE_INVALID'

class FakeEventSource {
  static instances: FakeEventSource[] = []
  readonly close = vi.fn()
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  private readonly listeners = new Map<string, (event: unknown) => void>()

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: (event: unknown) => void) {
    this.listeners.set(type, listener)
  }

  emit(data: unknown) {
    this.emitRaw(JSON.stringify(data))
  }

  emitRaw(data: string) {
    this.listeners.get('snapshot')?.({ data })
  }
}

function subscription() {
  vi.stubGlobal('EventSource', FakeEventSource)
  const handlers = { open: vi.fn(), snapshot: vi.fn(), error: vi.fn() }
  const stream = subscribeSystemEvents(['summary', 'docker'], 3, handlers)
  return { source: FakeEventSource.instances[0]!, stream, handlers }
}

afterEach(() => {
  FakeEventSource.instances = []
  vi.unstubAllGlobals()
})

describe('Docker 库存 GET 边界', () => {
  it('保持 URL、注入 fetcher、signal、凭据和 Accept，并返回同一解码器的结果', async () => {
    const payload = fixture()
    Object.assign(payload, { future: true })
    Object.assign(payload.containers[0]!, { ports: null, future: true })
    Object.assign(payload.projects[0]!, { configFiles: null })
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(payload)))
    const signal = new AbortController().signal
    await expect(requestDockerInventory(fetcher, signal)).resolves.toEqual(parseDockerInventoryResponse(payload))
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/docker/inventory', expect.objectContaining({ signal, credentials: 'same-origin' }))
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get('Accept')).toBe('application/json')
  })

  it('接受空数组和 Go null 顶层集合', async () => {
    for (const collection of [[], null]) {
      const payload = { ...fixture(), containers: collection, projects: collection }
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(payload)))
      await expect(requestDockerInventory(fetcher)).resolves.toMatchObject({ containers: [], projects: [] })
    }
  })

  it.each([null, [], {}, { ...fixture(), containers: [null] }, { ...fixture(), projects: [{}] }, { ...fixture(), engine: { containers: '1' } }])('拒绝坏响应 %j', async (payload) => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(payload)))
    await expect(requestDockerInventory(fetcher)).rejects.toMatchObject({ code: invalidCode })
  })

  it('无效 JSON 返回稳定库存错误码', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response('{broken'))
    await expect(requestDockerInventory(fetcher)).rejects.toMatchObject({ code: invalidCode })
  })

  it('保留非 2xx 的服务端错误以及通用 HTTP 错误', async () => {
    const serverError = { code: 'DOCKER_INVENTORY_UNAVAILABLE', message: 'unavailable', requestId: 'req-inventory' }
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(new Response(JSON.stringify(serverError), { status: 503 }))
      .mockResolvedValueOnce(new Response('<html>unavailable</html>', { status: 502 }))
    await expect(requestDockerInventory(fetcher)).rejects.toMatchObject(serverError)
    await expect(requestDockerInventory(fetcher)).rejects.toMatchObject({ code: 'API_REQUEST_FAILED' })
  })

  it('不吞掉 AbortError 或网络异常', async () => {
    for (const failure of [new DOMException('Aborted', 'AbortError'), new TypeError('network failed')]) {
      const fetcher = vi.fn<typeof fetch>().mockRejectedValue(failure)
      await expect(requestDockerInventory(fetcher)).rejects.toBe(failure)
    }
  })
})

describe('实时库存切片边界', () => {
  it('保留订阅参数与 open/error 回调', () => {
    const { source, stream, handlers } = subscription()
    expect(stream).toBe(source)
    expect(source.url).toBe('/api/v1/system/events?interval=3&scope=summary&scope=docker')
    source.onopen?.()
    source.onerror?.()
    expect(handlers.open).toHaveBeenCalledOnce()
    expect(handlers.error).toHaveBeenCalledOnce()
  })

  it('SSE 与 GET 使用同一规范化和已知字段投影', () => {
    const { source, handlers } = subscription()
    const docker = fixture()
    Object.assign(docker, { future: 'ignored' })
    Object.assign(docker.containers[0]!, { ports: null, future: 'ignored' })
    Object.assign(docker.projects[0]!, { configFiles: null })
    source.emit({ collectedAt, docker })
    expect(handlers.snapshot).toHaveBeenCalledExactlyOnceWith({ collectedAt, docker: parseDockerInventoryResponse(docker) })
    expect(handlers.error).not.toHaveBeenCalled()
    expect(source.close).not.toHaveBeenCalled()
  })

  it.each([null, [], {}, false, { ...fixture(), containers: [null] }, { ...fixture(), projects: [{}] }])('坏 Docker %j 不妨碍摘要或下一帧恢复', (docker) => {
    const { source, handlers } = subscription()
    source.emit({ collectedAt, summary, docker, errors: ['SYSTEM_CAPABILITIES_UNAVAILABLE'] })
    expect(handlers.snapshot).toHaveBeenLastCalledWith({ collectedAt, summary, errors: ['SYSTEM_CAPABILITIES_UNAVAILABLE', invalidCode] })
    expect(handlers.snapshot.mock.calls[0]?.[0]).not.toHaveProperty('docker')
    expect(handlers.error).not.toHaveBeenCalled()
    expect(source.close).not.toHaveBeenCalled()

    const recovered = fixture()
    source.emit({ collectedAt, summary, docker: recovered })
    expect(handlers.snapshot).toHaveBeenLastCalledWith({ collectedAt, summary, docker: recovered })
    expect(handlers.snapshot).toHaveBeenCalledTimes(2)
    expect(handlers.error).not.toHaveBeenCalled()
    expect(source.close).not.toHaveBeenCalled()
  })

  it('Docker-only 坏帧追加模块错误且流保持打开', () => {
    const { source, handlers } = subscription()
    source.emit({ collectedAt, docker: null })
    expect(handlers.snapshot).toHaveBeenCalledExactlyOnceWith({ collectedAt, errors: [invalidCode] })
    expect(handlers.error).not.toHaveBeenCalled()
    expect(source.close).not.toHaveBeenCalled()
  })

  it('缺省 Docker 不制造错误，显式空库存仍是有效结果', () => {
    const { source, handlers } = subscription()
    source.emit({ collectedAt, summary })
    expect(handlers.snapshot).toHaveBeenLastCalledWith({ collectedAt, summary })
    source.emit({ collectedAt, docker: { ...fixture(), containers: null, projects: null } })
    expect(handlers.snapshot).toHaveBeenLastCalledWith({ collectedAt, docker: { ...fixture(), containers: [], projects: [] } })
    expect(handlers.error).not.toHaveBeenCalled()
    expect(source.close).not.toHaveBeenCalled()
  })

  it.each([
    null, [], {}, { collectedAt: null }, { collectedAt, summary: null }, { collectedAt, summary: {} },
    { collectedAt, errors: null }, { collectedAt, errors: [1] }, { collectedAt, errors: 'invalid' },
    { collectedAt, summary: { ...summary, storage: null }, docker: fixture() },
  ])('保留坏 envelope/summary/errors %j 的关闭行为', (payload) => {
    const { source, handlers } = subscription()
    source.emit(payload)
    expect(handlers.snapshot).not.toHaveBeenCalled()
    expect(handlers.error).toHaveBeenCalledOnce()
    expect(source.close).toHaveBeenCalledOnce()
  })

  it('坏 JSON 仍关闭整条流', () => {
    const { source, handlers } = subscription()
    source.emitRaw('{broken')
    expect(handlers.snapshot).not.toHaveBeenCalled()
    expect(handlers.error).toHaveBeenCalledOnce()
    expect(source.close).toHaveBeenCalledOnce()
  })
})
