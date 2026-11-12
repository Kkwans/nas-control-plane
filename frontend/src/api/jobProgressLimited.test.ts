import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelJob, pullDockerImage, requestJobs, requestJobSnapshot, retryJob, subscribeJob } from './docker'
import { beginSession } from '@/session/sessionLifecycle'

const job = (progressLimited?: unknown) => ({
  id: 'pull-1', type: 'docker-image-pull', status: 'running', artifactState: 'unknown', message: '下载中',
  progress: 100, progressLimited, createdAt: '2026-10-15T00:00:00Z', updatedAt: '2026-10-15T00:00:01Z',
  downloadedBytes: 100, totalBytes: 100, speedBytes: 10, layers: {},
})
const fetchResult = (value: unknown) => vi.fn().mockResolvedValue(new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } }))
const readers = [
  { name: 'pull', read: (fetcher: typeof fetch) => pullDockerImage('alpine:latest', 0, fetcher), code: 'DOCKER_IMAGE_PULL_RESPONSE_INVALID' },
  { name: 'snapshot', read: (fetcher: typeof fetch) => requestJobSnapshot('/api/v1/jobs/pull-1', {}, fetcher, 'JOB_RESPONSE_INVALID'), code: 'JOB_RESPONSE_INVALID' },
  { name: 'retry', read: (fetcher: typeof fetch) => retryJob('pull-1', fetcher), code: 'JOB_RETRY_RESPONSE_INVALID' },
  { name: 'cancel', read: (fetcher: typeof fetch) => cancelJob('pull-1', fetcher), code: 'JOB_CANCEL_RESPONSE_INVALID' },
]
class FakeEventSource {
  static current: FakeEventSource
  close = vi.fn()
  private listener?: (event: { data: string }) => void
  constructor() { FakeEventSource.current = this }
  addEventListener(_type: string, listener: (event: { data: string }) => void) { this.listener = listener }
  emit(value: unknown) { this.listener?.({ data: JSON.stringify(value) }) }
}
beforeEach(() => beginSession())
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('可选进度受限协议', () => {
  it.each([undefined, false, true])('列表和所有单任务响应保留合法值 %s', async (limited) => {
    for (const reader of readers) {
      const snapshot = await reader.read(fetchResult(job(limited)))
      expect(snapshot.progressLimited).toBe(limited)
      expect(snapshot.status).toBe('running')
    }
    const result = await requestJobs('', fetchResult({ jobs: [job(limited)] }))
    expect(result.invalidCount).toBe(0)
    expect(result.jobs[0]?.progressLimited).toBe(limited)
  })
  it.each([null, 0, 1, 'true', {}, []])('各通道拒绝非布尔值 %j', async (limited) => {
    for (const reader of readers) await expect(reader.read(fetchResult(job(limited)))).rejects.toMatchObject({ code: reader.code })
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const result = await requestJobs('', fetchResult({ jobs: [job(limited), job(true)] }))
    expect(result.invalidCount).toBe(1)
    expect(result.jobs).toHaveLength(1)
    expect(result.jobs[0]?.progressLimited).toBe(true)
    vi.stubGlobal('EventSource', FakeEventSource)
    const progress = vi.fn(), subscription = subscribeJob('pull-1', progress)
    FakeEventSource.current.emit(job(limited))
    await expect(subscription.done).rejects.toThrow('任务进度格式无效')
    expect(progress).not.toHaveBeenCalled()
    expect(FakeEventSource.current.close).toHaveBeenCalledOnce()
  })
  it.each([undefined, false, true])('SSE 保留 %s；数值到 100 和待保存都不是最终完成回执', async (limited) => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const progress = vi.fn(), subscription = subscribeJob('pull-1', progress)
    FakeEventSource.current.emit(job(limited))
    expect(progress.mock.lastCall?.[0].progressLimited).toBe(limited)
    expect(FakeEventSource.current.close).not.toHaveBeenCalled()
    FakeEventSource.current.emit({ ...job(limited), status: 'completed', persistenceStatus: 'pending' })
    expect(FakeEventSource.current.close).not.toHaveBeenCalled()
    FakeEventSource.current.emit({ ...job(limited), status: 'completed' })
    await expect(subscription.done).resolves.toMatchObject({ status: 'completed' })
    expect(FakeEventSource.current.close).toHaveBeenCalledOnce()
  })
})
