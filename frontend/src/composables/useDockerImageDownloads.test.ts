// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, ref } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import type { DockerImageSummary, JobSnapshot } from '@/api/system'
import { useDockerImageDownloads } from './useDockerImageDownloads'

const api = vi.hoisted(() => ({ cancel: vi.fn(), remove: vi.fn(), retry: vi.fn(), list: vi.fn(), subscribe: vi.fn(), confirm: vi.fn(), success: vi.fn(), info: vi.fn(), error: vi.fn() }))
vi.mock('@/api/docker', () => ({ cancelJob: api.cancel, deleteJob: api.remove, retryJob: api.retry, requestJobs: api.list, subscribeJob: api.subscribe }))
vi.mock('element-plus', () => ({ ElMessage: { success: api.success, info: api.info, error: api.error }, ElMessageBox: { confirm: api.confirm } }))
vi.mock('@/composables/useListPreference', () => ({ useListPreference: () => ({ pageSize: ref(10) }) }))
const apps: ReturnType<typeof createApp>[] = []
const job = (fields: Partial<JobSnapshot> = {}): JobSnapshot => ({ id: 'job-1', type: 'docker-image-pull', status: 'running', artifactState: 'unknown', message: '下载中', progress: 50, createdAt: '', updatedAt: '', downloadedBytes: 50, totalBytes: 100, speedBytes: 10, layers: {}, ...fields })
function setup(images = ref<DockerImageSummary[]>([])) {
  let state!: ReturnType<typeof useDockerImageDownloads>
  const app = createApp({ setup() { state = useDockerImageDownloads(images); return () => null } })
  apps.push(app)
  app.mount(document.createElement('div'))
  return state
}
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason?: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

beforeEach(() => {
  beginSession()
  vi.resetAllMocks()
  api.subscribe.mockReturnValue({ close: vi.fn(), done: new Promise(() => {}) })
  api.confirm.mockResolvedValue(undefined)
})
afterEach(() => { apps.splice(0).forEach((app) => app.unmount()) })

describe('下载生命周期呈现', () => {
  it('立即消费取消快照，继续订阅且禁止重复取消和删除', async () => {
    const state = setup()
    const next = job({ cancellationStatus: 'requested' })
    api.cancel.mockResolvedValue(next)
    await state.stopDownload(job())
    expect(state.downloadJobs.value[0]).toEqual(next)
    expect(state.downloadStateLabel(next)).toBe('取消中')
    expect(state.canCancelJob(next)).toBe(false)
    expect(state.canDeleteJob(next)).toBe(false)
    expect(api.subscribe).toHaveBeenCalledOnce()
    await state.stopDownload(next)
    await state.confirmDeleteJob(next)
    expect(api.cancel).toHaveBeenCalledOnce()
    expect(api.remove).not.toHaveBeenCalled()
    expect(api.success).not.toHaveBeenCalled()
  })
  it('待保存结果不能删除、取消或重试；未知结果不宣称停止', async () => {
    const state = setup()
    const pending = job({ persistenceStatus: 'pending' })
    expect(state.downloadStateLabel(pending)).toBe('结果保存中')
    await state.confirmDeleteJob(pending)
    await state.stopDownload(pending)
    await state.retryDownload(pending)
    expect(api.remove).not.toHaveBeenCalled()
    expect(api.cancel).not.toHaveBeenCalled()
    expect(api.retry).not.toHaveBeenCalled()
    expect(state.downloadStateLabel(job({ status: 'interrupted', cancellationStatus: 'unknown' }))).toBe('结果未确认')
  })
  it('支持旧快照缺省字段并尊重不可取消能力', () => {
    const state = setup()
    expect(state.canCancelJob(job())).toBe(true)
    expect(state.canCancelJob(job({ cancellable: false }))).toBe(false)
    expect(state.canDeleteJob(job({ status: 'cancelled', cancellationStatus: 'confirmed' }))).toBe(true)
  })
  it('取消持久化失败不显示成功，也不伪造取消快照', async () => {
    const state = setup()
    api.cancel.mockRejectedValue(new Error('503'))
    await state.stopDownload(job())
    expect(api.success).not.toHaveBeenCalled()
    expect(api.info).not.toHaveBeenCalled()
    expect(api.error).toHaveBeenCalledOnce()
    expect(state.downloadJobs.value).toEqual([])
    expect(state.canCancelJob(job())).toBe(true)
  })
  it('未知结果重试先要求检查资源状态', async () => {
    const state = setup()
    api.confirm.mockRejectedValue('cancel')
    await state.retryDownload(job({ status: 'interrupted', cancellationStatus: 'unknown' }))
    expect(api.confirm).toHaveBeenCalledOnce()
    expect(api.retry).not.toHaveBeenCalled()
  })
})


describe('下载结果与本地镜像核实', () => {
  const image: DockerImageSummary = { id: 'sha256:local', repoTags: ['docker.io/library/alpine:latest'], repoDigests: [], sizeBytes: 1024, createdAt: '', containers: 0 }

  it('已完成但缺少本地镜像清单时不推断镜像已删除', () => {
    const state = setup()
    const completed = job({ status: 'completed', reference: 'library/alpine:latest' })
    state.upsertDownloadJob(completed)
    expect(state.downloadState(completed)).toBe('completed')
    expect(state.downloadStateLabel(completed)).toBe('已完成')
    expect(state.downloadArtifactLabel(completed)).toBe('镜像未核实')
    state.downloadStatus.value = 'deleted'
    expect(state.filteredDownloadJobs.value).toEqual([])
    state.downloadStatus.value = 'completed'
    expect(state.filteredDownloadJobs.value).toEqual([completed])
    expect(state.canDeleteJob(completed)).toBe(true)
  })

  it('明确存在或本地匹配是正向证据，未匹配仍为未核实', () => {
    const images = ref<DockerImageSummary[]>([])
    const state = setup(images)
    const completed = job({ status: 'completed', reference: 'library/alpine:latest' })
    expect(state.downloadArtifactLabel({ ...completed, artifactState: 'present' })).toBe('镜像存在')
    images.value = [image]
    expect(state.downloadArtifactLabel(completed)).toBe('镜像存在')
    expect(state.downloadArtifactLabel({ ...completed, reference: 'library/other:latest' })).toBe('镜像未核实')
    images.value = []
    expect(state.downloadArtifactLabel(completed)).toBe('镜像未核实')
    expect(state.downloadState(completed)).toBe('completed')
  })

  it('明确删除继续显示删除，且不覆盖取消、保存或结果未知语义', () => {
    const state = setup(ref([image]))
    const deleted = job({ status: 'completed', artifactState: 'deleted', reference: 'library/alpine:latest' })
    expect(state.downloadState(deleted)).toBe('deleted')
    expect(state.downloadArtifactLabel(deleted)).toBe('镜像已删除')
    expect(state.downloadState({ ...deleted, persistenceStatus: 'pending' })).toBe('persisting')
    expect(state.downloadState({ ...deleted, status: 'running', cancellationStatus: 'requested' })).toBe('cancelling')
    expect(state.downloadState({ ...deleted, cancellationStatus: 'unknown' })).toBe('interrupted')
    expect(state.downloadState({ ...deleted, status: 'interrupted' })).toBe('interrupted')
  })
})


describe('异步下载状态隔离', () => {
  it('延迟取消响应不能覆盖 SSE 终态或重新打开订阅', async () => {
    const state = setup()
    state.upsertDownloadJob(job())
    const cancel = deferred<JobSnapshot>()
    api.cancel.mockReturnValue(cancel.promise)
    const stopping = state.stopDownload(job())
    state.upsertDownloadJob(job({ status: 'completed' }))
    cancel.resolve(job({ cancellationStatus: 'requested' }))
    await stopping
    expect(state.downloadJobs.value[0]?.status).toBe('completed')
    expect(api.subscribe).not.toHaveBeenCalled()
    expect(api.info).not.toHaveBeenCalled()
  })
  it('重复重试和删除在确认对话框及请求期间均只执行一次', async () => {
    const state = setup()
    const confirm = deferred<void>()
    api.confirm.mockReturnValue(confirm.promise)
    const retry = deferred<JobSnapshot>()
    api.retry.mockReturnValue(retry.promise)
    const previous = job({ status: 'interrupted' })
    const first = state.retryDownload(previous)
    await state.retryDownload(previous)
    expect(api.confirm).toHaveBeenCalledOnce()
    confirm.resolve(); await Promise.resolve(); await Promise.resolve()
    await state.retryDownload(previous)
    expect(api.retry).toHaveBeenCalledOnce()
    retry.resolve(job({ id: 'new' })); await first
    const deleting = deferred<void>()
    api.remove.mockReturnValue(deleting.promise)
    const removal = state.confirmDeleteJob(previous)
    await state.confirmDeleteJob(previous)
    await Promise.resolve()
    expect(api.remove).toHaveBeenCalledOnce()
    deleting.resolve(); await removal
  })
  it.each(['unmount', 'logout'])('%s 后延迟 load/cancel/retry 不再订阅或提示', async (action) => {
    const state = setup()
    const list = deferred<{ jobs: JobSnapshot[]; invalidCount: number }>()
    const cancel = deferred<JobSnapshot>(); const retry = deferred<JobSnapshot>()
    api.list.mockReturnValue(list.promise); api.cancel.mockReturnValue(cancel.promise); api.retry.mockReturnValue(retry.promise)
    const loading = state.loadDownloadJobs(); const stopping = state.stopDownload(job()); const retrying = state.retryDownload(job({ status: 'failed' }))
    if (action === 'unmount') apps.pop()!.unmount(); else invalidateSession()
    list.resolve({ jobs: [job()], invalidCount: 0 }); cancel.resolve(job({ cancellationStatus: 'requested' })); retry.resolve(job({ id: 'new' }))
    await Promise.all([loading, stopping, retrying])
    expect(api.subscribe).not.toHaveBeenCalled()
    expect(state.downloadJobs.value).toEqual([])
    expect(api.info).not.toHaveBeenCalled()
  })
  it('真实组件卸载关闭已有订阅', async () => {
    const state = setup(); const close = vi.fn()
    api.list.mockResolvedValue({ jobs: [job()], invalidCount: 0 })
    api.subscribe.mockReturnValue({ close, done: new Promise(() => {}) })
    await state.loadDownloadJobs()
    apps.pop()!.unmount()
    expect(close).toHaveBeenCalledOnce()
  })
  it('旧列表不复活已删除任务，不覆盖新进度；旧失败不清空任务', async () => {
    const state = setup()
    const terminal = job({ status: 'failed' })
    state.upsertDownloadJob(terminal)
    const list = deferred<{ jobs: JobSnapshot[]; invalidCount: number }>()
    api.list.mockReturnValueOnce(list.promise)
    const loading = state.loadDownloadJobs()
    await state.confirmDeleteJob(terminal)
    list.resolve({ jobs: [terminal], invalidCount: 0 }); await loading
    expect(state.downloadJobs.value).toEqual([])
    const old = deferred<{ jobs: JobSnapshot[]; invalidCount: number }>()
    api.list.mockReturnValueOnce(old.promise)
    const older = state.loadDownloadJobs()
    state.upsertDownloadJob(job({ id: 'new', progress: 99 }))
    api.list.mockResolvedValue({ jobs: [job({ id: 'new', progress: 99 })], invalidCount: 0 })
    await state.loadDownloadJobs()
    old.reject(new Error('503')); await older
    expect(state.downloadJobs.value[0]?.progress).toBe(99)
  })
})


it('列表响应不覆盖请求期间到达的进度或较新的列表', async () => {
  const state = setup()
  state.upsertDownloadJob(job())
  const old = deferred<{ jobs: JobSnapshot[]; invalidCount: number }>()
  api.list.mockReturnValueOnce(old.promise)
  const loading = state.loadDownloadJobs()
  state.upsertDownloadJob(job({ progress: 99, cancellationStatus: 'requested' }))
  old.resolve({ jobs: [job({ progress: 10 })], invalidCount: 0 }); await loading
  expect(state.downloadJobs.value[0]?.progress).toBe(99)
  expect(state.downloadJobs.value[0]?.cancellationStatus).toBe('requested')
  const earlier = deferred<{ jobs: JobSnapshot[]; invalidCount: number }>()
  api.list.mockReturnValueOnce(earlier.promise)
  const first = state.loadDownloadJobs()
  api.list.mockResolvedValueOnce({ jobs: [job({ status: 'completed' })], invalidCount: 0 })
  await state.loadDownloadJobs()
  earlier.resolve({ jobs: [job()], invalidCount: 3 }); await first
  expect(state.downloadJobs.value[0]?.status).toBe('completed')
  expect(state.invalidDownloadJobCount.value).toBe(0)
})
