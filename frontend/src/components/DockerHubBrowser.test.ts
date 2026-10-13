// @vitest-environment jsdom
import { createApp, h, nextTick, ref } from 'vue'
import { useDockerImageDownloads } from '@/composables/useDockerImageDownloads'
import type { JobSnapshot } from '@/api/system'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import DockerHubBrowser from './DockerHubBrowser.vue'
const api = vi.hoisted(() => ({ pull: vi.fn(), search: vi.fn(), tags: vi.fn(), subscribe: vi.fn(), list: vi.fn(), cancel: vi.fn(), remove: vi.fn(), retry: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/docker', () => ({ pullDockerImage: api.pull, searchDockerHub: api.search, requestDockerHubTags: api.tags, subscribeJob: api.subscribe, requestJobs: api.list, cancelJob: api.cancel, deleteJob: api.remove, retryJob: api.retry }))
vi.mock('@/composables/useListPreference', () => ({ useListPreference: () => ({ pageSize: ref(10) }) }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ summary: { host: { architecture: 'amd64' } } }) }))
vi.mock('./NcpSelect.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', () => ({
  ElMessage: { success: api.success, error: api.error },
  ElMessageBox: { confirm: vi.fn() },
  ElTooltip: { render: () => null },
  ElInput: { props: ['modelValue'], emits: ['update:modelValue'], setup(_: unknown, { emit }: { emit: (event: string, value: string) => void }) { return () => h('input', { onInput: (e: Event) => emit('update:modelValue', (e.target as HTMLInputElement).value) }) } },
}))
const apps: ReturnType<typeof createApp>[] = []
const flush = async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); await nextTick() }
beforeEach(() => {
  beginSession(); vi.resetAllMocks()
  api.search.mockResolvedValue({ results: [{ namespace: 'library', name: 'alpine', lastUpdated: '2026-10-13', pullCount: 10, starCount: 1 }], count: 1, page: 1 })
  api.tags.mockResolvedValue({ results: [{ name: 'latest', fullSize: 10, architectures: ['amd64'] }] })
})
afterEach(() => apps.splice(0).forEach((app) => app.unmount()))
it.each(['unmount', 'logout'])('%s 后正在提交的拉取不再创建订阅或发送事件', async (action) => {
  let resolve!: (value: unknown) => void
  api.pull.mockReturnValue(new Promise((yes) => { resolve = yes }))
  const created = vi.fn()
  const host = document.createElement('div')
  const app = createApp({ render: () => h(DockerHubBrowser, { onJobCreated: created }) })
  apps.push(app); app.mount(host)
  const input = host.querySelector('input')!; input.value = 'alpine'; input.dispatchEvent(new Event('input'))
  await nextTick()
  host.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true })); await flush()
  ;(host.querySelector('.repository-detail footer button') as HTMLButtonElement).click()
  expect(api.pull).toHaveBeenCalledOnce()
  if (action === 'unmount') apps.pop()!.unmount(); else invalidateSession()
  resolve({ id: 'job-1', status: 'queued' }); await flush()
  expect(created).not.toHaveBeenCalled()
  expect(api.subscribe).not.toHaveBeenCalled()
  expect(api.success).not.toHaveBeenCalled()
})


it.each(['completed', 'failed'] as const)('真实 Hub 创建任务由唯一观察者跟踪，断流 GET 恢复 %s 并只通知一次', async (status) => {
  const snapshot = (fields: Partial<JobSnapshot> = {}): JobSnapshot => ({ id: 'new-pull', type: 'docker-image-pull', status: 'queued', reference: 'alpine:latest', artifactState: 'unknown', message: '', progress: 0, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, createdAt: '', updatedAt: '', ...fields })
  let rejectStream!: (reason: unknown) => void
  const close = vi.fn()
  api.subscribe.mockReturnValue({ close, done: new Promise<JobSnapshot>((_, reject) => { rejectStream = reject }) })
  api.pull.mockResolvedValue(snapshot())
  let downloads!: ReturnType<typeof useDockerImageDownloads>
  const refreshed = vi.fn()
  const host = document.createElement('div')
  const app = createApp({ setup() {
    downloads = useDockerImageDownloads(ref([]), { onCreatedDownloadCompleted: refreshed })
    return () => h(DockerHubBrowser, { onJobCreated: downloads.observeCreatedDownload })
  } })
  apps.push(app); app.mount(host)
  const input = host.querySelector('input')!; input.value = 'alpine'; input.dispatchEvent(new Event('input'))
  await nextTick()
  host.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true })); await flush()
  ;(host.querySelector('.repository-detail footer button') as HTMLButtonElement).click(); await flush()
  expect(api.pull).toHaveBeenCalledExactlyOnceWith('alpine:latest', 10)
  expect(api.subscribe).toHaveBeenCalledOnce()
  expect(api.list).not.toHaveBeenCalled()
  const event = api.subscribe.mock.calls[0]![1] as (job: JobSnapshot) => void
  event(snapshot({ status: 'running', progress: 60, speedBytes: 1024 }))
  expect(downloads.downloadJobs.value[0]?.progress).toBe(60)
  expect(downloads.canShowDownloadSpeed(downloads.downloadJobs.value[0]!)).toBe(true)
  rejectStream(new Error('offline')); await flush()
  expect(downloads.isDownloadObservationInterrupted(downloads.downloadJobs.value[0]!)).toBe(true)
  expect(downloads.canShowDownloadSpeed(downloads.downloadJobs.value[0]!)).toBe(false)
  expect(downloads.downloadJobs.value[0]?.status).toBe('running')
  expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled()
  api.list.mockResolvedValue({ jobs: [snapshot({ status, error: status === 'failed' ? 'fixture pull failure' : undefined })], invalidCount: 0 })
  await downloads.loadDownloadJobs(); await flush()
  expect(downloads.downloadJobs.value[0]?.status).toBe(status)
  if (status === 'completed') {
    expect(api.success).toHaveBeenCalledExactlyOnceWith('镜像 alpine:latest 已拉取')
    expect(refreshed).toHaveBeenCalledOnce()
  } else {
    expect(api.error).toHaveBeenCalledExactlyOnceWith('fixture pull failure')
    expect(refreshed).not.toHaveBeenCalled()
  }
  await downloads.loadDownloadJobs(); await flush()
  downloads.observeCreatedDownload(snapshot()); await flush()
  expect(api.success.mock.calls.length + api.error.mock.calls.length).toBe(1)
  expect(api.subscribe).toHaveBeenCalledOnce()
  expect(api.pull).toHaveBeenCalledOnce()
  expect(api.retry).not.toHaveBeenCalled(); expect(api.cancel).not.toHaveBeenCalled(); expect(api.remove).not.toHaveBeenCalled()
})
