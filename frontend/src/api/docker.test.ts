/// <reference types="node" />
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createHash } from 'node:crypto'

import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { deployComposeConfig, readComposeConfig, subscribeJob } from './docker'
import type { ComposeDeployInput } from './system'

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

describe('Job lifecycle optional protocol fields', () => {
  afterEach(() => { FakeEventSource.instances = []; vi.unstubAllGlobals() })

  it('keeps requested cancellation and pending persistence subscribed until a durable terminal result', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const progress = vi.fn()
    const subscription = subscribeJob('job-1', progress)
    const source = FakeEventSource.instances[0]!
    source.emit('progress', { ...job(), cancellationStatus: 'requested', cancellable: true })
    source.emit('progress', { ...job(), cancellationStatus: 'requested', persistenceStatus: 'pending', errorCode: 'JOB_PERSIST_FAILED' })
    expect(source.close).not.toHaveBeenCalled()
    expect(progress).toHaveBeenLastCalledWith(expect.objectContaining({ persistenceStatus: 'pending' }))
    source.emit('progress', { ...job(), status: 'interrupted', cancellationStatus: 'unknown' })
    await expect(subscription.done).resolves.toMatchObject({ status: 'interrupted', cancellationStatus: 'unknown' })
    expect(source.close).toHaveBeenCalledOnce()
  })

  it('keeps queue timeout pending subscribed and accepts the durable failure', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const subscription = subscribeJob('job-1', vi.fn())
    const source = FakeEventSource.instances[0]!
    source.emit('progress', { ...job('queued'), persistenceStatus: 'pending', message: '任务未开始，结果等待保存' })
    expect(source.close).not.toHaveBeenCalled()
    source.emit('progress', { ...job('queued'), status: 'failed', errorCode: 'JOB_QUEUE_TIMEOUT', message: '排队等待超时，任务未开始执行' })
    await expect(subscription.done).resolves.toMatchObject({ status: 'failed', errorCode: 'JOB_QUEUE_TIMEOUT' })
    expect(source.close).toHaveBeenCalledOnce()
  })

  it.each([
    { cancellationStatus: 'stopped' }, { persistenceStatus: 'saved' }, { cancellable: 'false' }, { errorCode: 123 },
  ])('rejects invalid optional lifecycle fields: %j', async (fields) => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const subscription = subscribeJob('job-1', vi.fn())
    FakeEventSource.instances[0]!.emit('progress', { ...job(), ...fields })
    await expect(subscription.done).rejects.toThrow('任务进度格式无效')
  })
})


describe('任务订阅会话清理', () => {
  afterEach(() => { FakeEventSource.instances = []; vi.unstubAllGlobals() })
  it.each(['close', 'logout'])('%s 后忽略已排队的进度事件', async (action) => {
    beginSession()
    vi.stubGlobal('EventSource', FakeEventSource)
    const progress = vi.fn()
    const subscription = subscribeJob('job-1', progress)
    const source = FakeEventSource.instances[0]!
    if (action === 'close') subscription.close(); else invalidateSession()
    source.emit('progress', job('completed'))
    await expect(subscription.done).rejects.toMatchObject({ name: 'AbortError' })
    expect(progress).not.toHaveBeenCalled()
    expect(source.close).toHaveBeenCalledOnce()
  })
})

describe('Compose 磁盘基线协议', () => {
  const content = '\uFEFFservices:\r\n  web: # 中文 🐳\r\n    image: nginx  \r\n'
  const contentHash = createHash('sha256').update(content, 'utf8').digest('hex')
  const project = { id: 'a', workingDirectory: '/a', configFiles: ['/a/compose.yml'] }
  const response = () => ({ projectId: 'a', workingDirectory: '/a', collectedAt: '', files: [{ path: '/a/compose.yml', name: 'compose.yml', content, contentHash, size: Buffer.byteLength(content, 'utf8') }] })
  const payload: ComposeDeployInput = { projectId: 'a', workingDirectory: '/a', configFiles: ['/a/compose.yml'], targetPath: '/a/compose.yml', content: 'edited', baseContentHash: contentHash }
  const invalidHashes = [undefined, null, '', 123, {}, '0'.repeat(63), '0'.repeat(65), 'g'.repeat(64), 'A'.repeat(64), ` ${contentHash}`, `${contentHash} `, `${contentHash}\n`]
  afterEach(() => vi.unstubAllGlobals())

  it('完整保留原始文本和服务端 hash，不依赖浏览器 crypto', async () => {
    vi.stubGlobal('crypto', undefined)
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(response())))
    await expect(readComposeConfig(project, fetcher)).resolves.toEqual(response())
    expect(fetcher).toHaveBeenCalledOnce()
    expect(JSON.parse(fetcher.mock.calls[0]![1].body)).toEqual({ projectId: 'a', workingDirectory: '/a', configFiles: ['/a/compose.yml'] })
  })

  it.each(invalidHashes)('读取拒绝缺失或非 canonical hash：%j', async (invalid) => {
    const result = response()
    Object.assign(result.files[0]!, { contentHash: invalid })
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(result)))
    await expect(readComposeConfig(project, fetcher)).rejects.toMatchObject({ code: 'COMPOSE_CONFIG_RESPONSE_INVALID', message: expect.stringContaining('重新读取') })
  })

  it('每份文件都必须携带有效 hash，不能只检查首份', async () => {
    const result = response()
    result.files.push({ ...result.files[0]!, path: '/a/other.yml', contentHash: '' })
    await expect(readComposeConfig(project, vi.fn().mockResolvedValue(new Response(JSON.stringify(result))))).rejects.toMatchObject({ code: 'COMPOSE_CONFIG_RESPONSE_INVALID' })
  })

  it('发布 JSON 必须原样携带编辑会话的磁盘 hash，而非提交内容 hash', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(job('queued'))))
    await deployComposeConfig(payload, fetcher)
    expect(JSON.parse(fetcher.mock.calls[0]![1].body)).toEqual(payload)
    expect(fetcher.mock.calls[0]![0]).toBe('/api/v1/docker/compose/deploy')
  })

  it.each(invalidHashes)('缺失或无效的发布基线在请求前拒绝：%j', async (invalid) => {
    const fetcher = vi.fn()
    await expect(deployComposeConfig({ ...payload, baseContentHash: invalid } as ComposeDeployInput, fetcher)).rejects.toMatchObject({ code: 'COMPOSE_BASELINE_INVALID' })
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('全零 hash 格式有效，不能擅自改成当前内容 hash', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(job('queued'))))
    await deployComposeConfig({ ...payload, baseContentHash: '0'.repeat(64) }, fetcher)
    expect(JSON.parse(fetcher.mock.calls[0]![1].body).baseContentHash).toBe('0'.repeat(64))
  })
})
