import { describe, expect, it, vi } from 'vitest'
import { requestDockerImageRemoveJob } from './docker'
const imageId = `sha256:${'a'.repeat(64)}`
const job = { id: 'remove-1', type: 'docker-image-remove', reference: imageId, cancellable: false, status: 'queued', artifactState: 'unknown', message: '', progress: 0, createdAt: '2026-10-15T00:00:00Z', updatedAt: '2026-10-15T00:00:00Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }
const response = (body: unknown, status = 202) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
describe('single image deletion admission', () => {
  it('sends the canonical identity and accepts only a matching 202 job', async () => {
    const fetcher = vi.fn().mockResolvedValue(response(job))
    expect(await requestDockerImageRemoveJob(imageId, fetcher)).toMatchObject(job)
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]![0]).toBe('/api/v1/docker/images/remove-job')
    expect(JSON.parse(fetcher.mock.calls[0]![1].body)).toEqual({ imageId })
  })
  it.each(['alpine:latest', 'sha256:abc', `sha256:${'A'.repeat(64)}`])('rejects invalid input before transport: %s', async (id) => {
    const fetcher = vi.fn()
    await expect(requestDockerImageRemoveJob(id, fetcher)).rejects.toMatchObject({ code: 'DOCKER_IMAGE_REMOVE_INVALID' })
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each([{ artifactState: undefined }, { artifactState: 'invalid' }, { artifactState: 'present' }, { artifactState: 'deleted' }, { type: 'docker-image-pull' }, { reference: 'other' }, { cancellable: true }, { cancellable: undefined }, { id: '' }, { status: 'mystery' }, { createdAt: '' }, { status: 'completed' }, { status: 'running' }, { status: 'cancelled' }, { cancellationStatus: 'requested' }, { persistenceStatus: 'pending' }, { progress: -1 }, { progress: 101 }, { downloadedBytes: 1 }, { layers: { fake: {} } }])('treats mismatched receipts as unknown', async (fields) => {
    await expect(requestDockerImageRemoveJob(imageId, vi.fn().mockResolvedValue(response({ ...job, ...fields })))).rejects.toMatchObject({ code: 'DOCKER_IMAGE_REMOVE_ADMISSION_UNKNOWN' })
  })
  it.each([200, 201, 204, 500])('does not equate status %s with admission', async (status) => {
    const result = status === 204 ? new Response(null, { status }) : response(job, status)
    await expect(requestDockerImageRemoveJob(imageId, vi.fn().mockResolvedValue(result))).rejects.toMatchObject({ code: 'DOCKER_IMAGE_REMOVE_ADMISSION_UNKNOWN' })
  })
  it.each(['JOB_QUEUE_FULL', 'JOB_PERSIST_FAILED', 'JOB_ENGINE_UNAVAILABLE', 'AGENT_DOCKER_IMAGES_UNAVAILABLE'])('preserves explicit rejection %s', async (code) => {
    await expect(requestDockerImageRemoveJob(imageId, vi.fn().mockResolvedValue(response({ code, message: 'rejected', requestId: 'r' }, 503)))).rejects.toMatchObject({ code })
  })
  it('does not retry transport failures or malformed JSON', async () => {
    for (const fetcher of [vi.fn().mockRejectedValue(new Error('offline')), vi.fn().mockResolvedValue(new Response('{', { status: 202, headers: { 'Content-Type': 'application/json' } }))]) {
      await expect(requestDockerImageRemoveJob(imageId, fetcher)).rejects.toMatchObject({ code: 'DOCKER_IMAGE_REMOVE_ADMISSION_UNKNOWN' })
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })
})
