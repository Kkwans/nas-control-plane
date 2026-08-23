import { describe, expect, it, vi } from 'vitest'

import {
  createDockerContainer,
  detectPublicEgress,
  deleteDockerProject,
  loginRoot,
  requestCapabilities,
  requestContainerAction,
  requestContainerDetails,
  requestContainerLogs,
  requestDNSCapability,
  requestSystemSummary,
  inspectMihomo,
} from './system'
import { pullDockerImage, requestDockerImages, requestDockerHubTags, requestJobs } from './docker'

describe('NCP API client', () => {
  it('keeps effective and backend-managed DNS values separate', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      backend: 'ugos-network-service', detected: true, state: 'available', readOnly: false,
      canRead: true, canPreview: true, canConfirm: true, canRollback: true,
      nameservers: ['240c::6666', '240c::6644', '192.168.5.1'],
      configuredNameservers: ['240c::6666', '192.168.5.1'],
      detectionSource: 'ugos-net-serv', errorCode: '',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await expect(requestDNSCapability(fetcher)).resolves.toMatchObject({
      nameservers: ['240c::6666', '240c::6644', '192.168.5.1'],
      configuredNameservers: ['240c::6666', '192.168.5.1'],
    })
  })

  it('accepts DNS capability responses from the previous Agent during a rolling upgrade', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      backend: 'ugos-network-service', detected: true, state: 'available', readOnly: false,
      canRead: true, canPreview: true, canConfirm: true, canRollback: true,
      nameservers: ['192.168.5.1'], detectionSource: 'ugos-net-serv', errorCode: '',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await expect(requestDNSCapability(fetcher)).resolves.toMatchObject({ nameservers: ['192.168.5.1'] })
  })

  it('requests protected system data with same-origin credentials', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          hostname: 'DH4300-PLUS',
          architecture: 'arm64',
          docker: true,
          compose: true,
          systemd: true,
          journald: true,
          cgroupVersion: 2,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestCapabilities(fetcher)).resolves.toMatchObject({ hostname: 'DH4300-PLUS', docker: true })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/system/capabilities',
      expect.objectContaining({ credentials: 'same-origin' }),
    )
  })

  it('normalizes Docker Hub tag publication time across rolling upgrades', async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        count: 1,
        page: 1,
        pageSize: 25,
        results: [{
          name: 'latest',
          lastUpdated: '2026-07-29T10:00:00Z',
          fullSize: 1024,
          architectures: ['linux/arm64'],
        }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        count: 1,
        page: 1,
        pageSize: 25,
        results: [{
          name: '8-alpine',
          publishedAt: '2026-07-30T10:00:00Z',
          lastUpdated: '2026-07-30T10:00:00Z',
          fullSize: 2048,
          architectures: ['linux/amd64'],
        }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await expect(requestDockerHubTags('library', 'redis', 1, 25, fetcher))
      .resolves.toMatchObject({ results: [{ publishedAt: '2026-07-29T10:00:00Z' }] })
    await expect(requestDockerHubTags('library', 'redis', 1, 25, fetcher))
      .resolves.toMatchObject({ results: [{ publishedAt: '2026-07-30T10:00:00Z' }] })
  })

  it('sends Root credentials only in the login request body and accepts an HttpOnly-cookie session response', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ user: { id: 1, username: 'root-admin', role: 'root' }, expiresAt: '2026-07-24T12:00:00Z' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const password = `${Date.now()}-${Math.random()}-${performance.now()}`
    await expect(loginRoot({ username: 'root-admin', password }, fetcher)).resolves.toMatchObject({
      user: { role: 'root' },
    })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/auth/login',
      expect.objectContaining({ method: 'POST', credentials: 'same-origin' }),
    )
  })

  it('retains stable server error codes for UI states', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ code: 'SYSTEM_SUMMARY_UNAVAILABLE', message: 'unavailable', requestId: 'req-test' }),
        { status: 503, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestSystemSummary(fetcher)).rejects.toMatchObject({
      code: 'SYSTEM_SUMMARY_UNAVAILABLE',
      requestId: 'req-test',
    })
  })

  it('preserves explicit public-egress diagnostics returned with HTTP 503', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify({
        status: 'unavailable', address: '', checkedAt: '0001-01-01T00:00:00Z',
        detectionSource: 'deployment-config', errorCode: 'PUBLIC_EGRESS_ENDPOINT_UNAVAILABLE',
      }), { status: 503, headers: { 'Content-Type': 'application/json' } }),
    )

    await expect(detectPublicEgress(fetcher)).resolves.toMatchObject({
      status: 'unavailable', errorCode: 'PUBLIC_EGRESS_ENDPOINT_UNAVAILABLE',
    })
  })

  it('requests a safe Mihomo route inspection without controller credentials', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      status: 'available',
      capability: {
        detected: true, state: 'available', processName: 'mihomo', executable: '/usr/bin/mihomo', version: '1.19.27',
        controller: { detected: true, endpoint: 'http://127.0.0.1:9091', reachable: true, authRequired: false, tokenConfigured: false, operations: ['version', 'proxies'], detectionSource: 'deployment-config' },
        evidence: [], warnings: [],
      },
      localProxy: { address: 'http://127.0.0.1:7890', mode: 'rule' },
      strategy: { group: '节点选择', selectedNode: '上海-01', nodeType: 'trojan', provider: 'provider-a' },
      node: { server: 'node.example.test', port: 443, resolvedIp: '203.0.113.10', country: '中国', region: '上海', isp: 'Example ISP', asn: 'AS4809' },
      publicEgress: { status: 'available', address: '198.51.100.20', country: '中国', region: '上海', isp: 'Example ISP', asn: 'AS4809', checkedAt: '2026-08-08T12:00:00Z', detectionSource: 'deployment-config', errorCode: '' },
      checkedAt: '2026-08-08T12:00:00Z', expiresAt: '2026-08-08T12:01:00Z', errorCode: '',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await expect(inspectMihomo(true, fetcher)).resolves.toMatchObject({
      strategy: { selectedNode: '上海-01' }, publicEgress: { address: '198.51.100.20' },
    })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/proxy/mihomo/inspect',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ force: true }) }),
    )
    expect(fetcher.mock.calls[0]?.[1]?.body).not.toContain('secret')
  })

  it('posts an explicit container lifecycle action with same-origin credentials', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ containerId: 'abc123', name: 'web', action: 'restart', state: 'running' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestContainerAction('abc123', 'restart', fetcher)).resolves.toMatchObject({
      containerId: 'abc123',
      state: 'running',
    })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/docker/containers/abc123/actions/restart',
      expect.objectContaining({ method: 'POST', credentials: 'same-origin' }),
    )
  })

  it('reads the safe container detail endpoint', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      id: 'abc123', name: 'web', image: 'demo:latest', state: 'running', health: 'healthy',
      healthFailingStreak: 0, exitCode: 0, restartCount: 1, oomKilled: false,
      restartMaximumRetries: 0, autoRemove: false, privileged: false, readonlyRootfs: false,
      nanoCpus: 0, memoryBytes: 0, ports: [], mounts: [], networks: [],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await expect(requestContainerDetails('abc123', fetcher)).resolves.toMatchObject({
      id: 'abc123', image: 'demo:latest', health: 'healthy',
    })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/docker/containers/abc123',
      expect.objectContaining({ credentials: 'same-origin' }),
    )
  })

  it('creates a Docker container from structured options without shell interpolation', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          containerId: 'created-123',
          name: 'redis-cache',
          image: 'redis:8-alpine',
          state: 'stopped',
          created: true,
          started: false,
          runContainer: false,
        }),
        { status: 201, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(createDockerContainer({
      image: 'redis:8-alpine',
      name: 'redis-cache',
      environment: { APP_MODE: 'production' },
      ports: [{ containerPort: 6379, hostPort: 6379, protocol: 'tcp' }],
      command: ['redis-server', '--appendonly', 'yes'],
      runContainer: false,
    }, fetcher)).resolves.toMatchObject({ containerId: 'created-123', started: false })

    const [, init] = fetcher.mock.calls[0]!
    expect(init).toMatchObject({ method: 'POST', credentials: 'same-origin' })
    expect(JSON.parse(String(init?.body))).toMatchObject({
      image: 'redis:8-alpine',
      command: ['redis-server', '--appendonly', 'yes'],
      runContainer: false,
    })
  })

  it('deletes a stopped Compose project using only its public identity', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          projectId: 'compose:heimdall',
          kind: 'compose',
          completed: true,
          partial: false,
          registryDeleted: true,
          registryRolledBack: false,
          containers: [{
            containerId: 'abc123',
            name: 'heimdall',
            state: 'stopped',
            deleted: true,
            success: true,
          }],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(deleteDockerProject({
      id: 'compose:heimdall',
      name: 'heimdall',
      kind: 'compose',
    }, fetcher)).resolves.toMatchObject({ completed: true, registryDeleted: true })

    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/docker/compose/projects/compose%3Aheimdall',
      expect.objectContaining({ method: 'DELETE', credentials: 'same-origin' }),
    )
    const [, init] = fetcher.mock.calls[0]!
    expect(JSON.parse(String(init?.body))).toEqual({
      projectId: 'compose:heimdall',
      kind: 'compose',
      registryName: 'heimdall',
    })
  })

  it('requests a bounded container log tail', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          containerId: 'abc123',
          tail: 40,
          collectedAt: '2026-07-23T02:00:00Z',
          entries: [{ timestamp: '2026-07-23T02:00:00Z', level: 'info', stream: 'stdout', message: 'ready' }],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestContainerLogs('abc123', 40, fetcher)).resolves.toMatchObject({
      containerId: 'abc123',
      tail: 40,
      entries: [{ message: 'ready' }],
    })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/v1/docker/containers/abc123/logs?tail=40',
      expect.objectContaining({ credentials: 'same-origin' }),
    )
  })

  it('normalizes legacy null image tags during rolling updates', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          collectedAt: '2026-07-25T02:00:00Z',
          images: [{
            id: 'sha256:dangling',
            repoTags: null,
            repoDigests: null,
            sizeBytes: 1024,
            createdAt: '2026-07-25T01:00:00Z',
            containers: 0,
          }],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestDockerImages(fetcher)).resolves.toMatchObject({
      images: [{ repoTags: [], repoDigests: [] }],
    })
  })

  it('normalizes an empty artifact state returned while Agent and Server roll forward', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: 'job-new',
          type: 'docker-image-pull',
          status: 'queued',
          reference: 'redis:latest',
          message: '任务已进入队列',
          progress: 0,
          createdAt: '2026-07-29T15:04:03Z',
          updatedAt: '2026-07-29T15:04:03Z',
          downloadedBytes: 0,
          totalBytes: 1024,
          speedBytes: 0,
          layers: {},
          artifactState: '',
        }),
        { status: 202, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(pullDockerImage('redis:latest', 1024, fetcher)).resolves.toMatchObject({
      id: 'job-new',
      artifactState: 'unknown',
    })
  })

  it('keeps valid download jobs when one historical row is malformed', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          jobs: [
            {
              id: 'job-valid',
              type: 'docker-image-pull',
              status: 'completed',
              reference: 'redis:latest',
              message: '镜像拉取完成',
              progress: 100,
              createdAt: '2026-07-29T15:04:03Z',
              updatedAt: '2026-07-29T15:05:03Z',
              downloadedBytes: 1024,
              totalBytes: 1024,
              speedBytes: 0,
              layers: {},
              artifactState: '',
            },
            { id: 'job-dirty', status: 'completed' },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    await expect(requestJobs('docker-image-pull', fetcher)).resolves.toMatchObject({
      jobs: [{ id: 'job-valid', artifactState: 'unknown' }],
      invalidCount: 1,
    })
    expect(warn).toHaveBeenCalledOnce()
    warn.mockRestore()
  })
})
