import { afterEach, describe, expect, it, vi } from 'vitest'

import { subscribeJob } from './docker'

class FakeEventSource {
  static instances: FakeEventSource[] = []
  readonly url: string
  readonly close = vi.fn()
  private readonly listeners = new Map<string, (event: unknown) => void>()

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: (event: unknown) => void) {
    this.listeners.set(type, listener)
  }

  emit(type: string, data: unknown) {
    this.listeners.get(type)?.({ data: JSON.stringify(data) })
  }
}

const job = (status: 'queued' | 'running' | 'completed' = 'running') => ({
  id: 'job-1', type: 'docker-image-pull', status, artifactState: 'unknown', message: '下载中', progress: status === 'completed' ? 100 : 50,
  createdAt: '2026-08-30T00:00:00Z', updatedAt: '2026-08-30T00:00:01Z', downloadedBytes: 50, totalBytes: 100, speedBytes: 10, layers: {},
})

describe('Docker job subscriptions', () => {
  afterEach(() => {
    FakeEventSource.instances = []
    vi.unstubAllGlobals()
  })

  it('closes the EventSource when a terminal snapshot arrives', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const progress = vi.fn()
    const subscription = subscribeJob('job/1', progress)
    const source = FakeEventSource.instances[0]
    source?.emit('progress', job('completed'))

    await expect(subscription.done).resolves.toMatchObject({ id: 'job-1', status: 'completed' })
    expect(progress).toHaveBeenCalledOnce()
    expect(source?.close).toHaveBeenCalledOnce()
  })

  it('exposes an explicit close that settles done without leaving a stream open', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const subscription = subscribeJob('job/1', vi.fn())
    const source = FakeEventSource.instances[0]
    subscription.close()

    await expect(subscription.done).rejects.toMatchObject({ name: 'AbortError' })
    expect(source?.close).toHaveBeenCalledOnce()
  })
})
