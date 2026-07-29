import { describe, expect, it, vi } from 'vitest'

import {
  loginRoot,
  pullDockerImage,
  requestCapabilities,
  requestContainerAction,
  requestContainerLogs,
  requestDockerImages,
  requestDockerHubTags,
  requestJobs,
  requestSystemSummary,
} from './system'

describe('NCP API client', () => {
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

  it('requests a bounded container log tail', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          containerId: 'abc123',
          tail: 40,
          collectedAt: '2026-07-23T02:00:00Z',
          entries: [{ stream: 'stdout', message: 'ready' }],
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
