// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, createApp, defineComponent, h, nextTick, ref, type App } from 'vue'
import type { JobSnapshot } from '@/api/system'
import DockerImagePanel from './DockerImagePanel.vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'

const hooks = vi.hoisted(() => ({ downloads: vi.fn(), local: vi.fn(), warning: vi.fn(), inventoryRefresh: vi.fn() }))
vi.mock('@/composables/useDockerImageDownloads', () => ({ useDockerImageDownloads: hooks.downloads }))
vi.mock('@/composables/useDockerLocalImages', () => ({ useDockerLocalImages: hooks.local }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ refresh: hooks.inventoryRefresh }) }))
vi.mock('./DockerHubBrowser.vue', () => ({ default: defineComponent({ emits: ['job-created'], setup(_, { emit }) { return () => h('button', { class: 'hub-created-stub', onClick: () => emit('job-created', job()) }, 'fixture 创建下载任务') } }) }))
vi.mock('./CreateContainerDrawer.vue', () => ({ default: defineComponent({ emits: ['created'], setup(_, { emit }) { return () => h('button', { class: 'container-created-stub', onClick: () => emit('created', 'container-A') }, 'fixture 容器已创建') } }) }))
vi.mock('./ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('./NcpSelect.vue', () => ({ default: { render: () => null } }))
vi.mock('./SectionTabs.vue', () => ({ default: defineComponent({ emits: ['update:modelValue'], setup(_, { emit }) { return () => h('button', { onClick: () => emit('update:modelValue', 'downloads') }, '下载任务') } }) }))
vi.mock('element-plus', () => ({
  ElMessage: { warning: hooks.warning },
  ElTooltip: defineComponent({ setup(_, { slots }) { return () => slots.default?.() } }),
  ElInput: defineComponent({ props: ['modelValue'], emits: ['update:modelValue'], setup(props, { emit }) { return () => h('input', { value: props.modelValue, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value) }) } }),
}))

function downloadState() {
  const downloadJobs = ref<JobSnapshot[]>([])
  const downloadQuery = ref('')
  const interrupted = ref(false)
  const trusted = ref(false)
  const filteredDownloadJobs = computed(() => downloadJobs.value.filter((job) => (job.reference ?? '').includes(downloadQuery.value)))
  return {
    downloadJobs, downloadJobsLoading: ref(false), downloadJobsLoaded: ref(false), downloadJobsError: ref(''),
    downloadJobsLastSuccessAt: ref<string | null>(null), invalidDownloadJobCount: ref(0), downloadStatus: ref('all'),
    downloadQuery, downloadPage: ref(1), downloadStatusOptions: [], activePullCount: ref(0), filteredDownloadJobs,
    downloadPageCount: ref(1), pagedDownloadJobs: filteredDownloadJobs,
    loadDownloadJobs: vi.fn(), observeCreatedDownload: vi.fn(), retryDownload: vi.fn(), stopDownload: vi.fn(), confirmDeleteJob: vi.fn(),
    isLiveJob: (job: JobSnapshot) => job.status === 'running' || job.status === 'queued',
    canCancelJob: () => true, canDeleteJob: () => false, canRetryJob: () => true,
    downloadState: (job: JobSnapshot) => job.status,
    downloadStateLabel: (job: JobSnapshot) => job.status === 'running' ? '下载中' : '已完成',
    downloadArtifactLabel: () => '镜像已存在', effectiveTotal: () => 2048,
    isDownloadObservationInterrupted: () => interrupted.value,
    downloadLastObservedAt: () => '2026-10-13T12:30:00Z', canShowDownloadSpeed: () => trusted.value,
    interrupted, trusted,
  }
}
let downloads: ReturnType<typeof downloadState>
const mounted: { app: App; host: HTMLDivElement }[] = []
function job(status: JobSnapshot['status'] = 'running'): JobSnapshot {
  return { id: 'pull-1', type: 'docker-image-pull', status, reference: 'alpine:latest', message: '正在拉取镜像', progress: 50, downloadedBytes: 1024, speedBytes: 1024, createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:29:00Z' } as JobSnapshot
}
async function mountDownloads() {
  const host = document.createElement('div'); document.body.append(host)
  const app = createApp({ render: () => h(DockerImagePanel, { query: '', containers: [], pageSize: 20 }) })
  app.mount(host); mounted.push({ app, host })
  ;(host.querySelector('button') as HTMLButtonElement).click(); await nextTick()
  return host
}
function button(host: HTMLElement, text: string) { return [...host.querySelectorAll('button')].find((item) => item.textContent === text)! }
beforeEach(() => {
  vi.clearAllMocks()
  beginSession()
  downloads = downloadState(); hooks.downloads.mockReturnValue(downloads)
  hooks.local.mockReturnValue({
    images: ref([]), loading: ref(false), error: ref(''), removeFailures: ref([]), selectedImageIds: ref([]), localPage: ref(1),
    localPreference: ref({ sortKey: 'name', sortDirection: 'asc' }), filteredImages: ref([]), localPageCount: ref(1), pagedImages: ref([]),
    allVisibleSelected: ref(false), someVisibleSelected: ref(false), selectedImageCount: ref(0), bulkRemovePending: ref(false),
    refresh: vi.fn(), dateLabel: (value: string) => value,
  })
})
afterEach(() => { for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } })

describe('DockerImagePanel 下载观察状态', () => {
  it('Hub 创建事件交给唯一观察 owner，完成回调保留本地镜像刷新', async () => {
    const host = await mountDownloads()
    const options = hooks.downloads.mock.calls[0]![1] as { onCreatedDownloadCompleted: () => void | Promise<void> }
    const local = hooks.local.mock.results[0]!.value as { images: unknown; refresh: ReturnType<typeof vi.fn> }
    expect(hooks.downloads.mock.calls[0]![0]).toBe(local.images)
    ;(host.querySelector('.hub-created-stub') as HTMLButtonElement).click()
    expect(downloads.observeCreatedDownload).toHaveBeenCalledExactlyOnceWith(job())
    expect(downloads.loadDownloadJobs).toHaveBeenCalledTimes(1)
    expect(downloads.retryDownload).not.toHaveBeenCalled()
    expect(local.refresh).toHaveBeenCalledTimes(1)
    const refreshed = Promise.resolve()
    local.refresh.mockReturnValueOnce(refreshed)
    expect(options.onCreatedDownloadCompleted()).toBe(refreshed)
    expect(local.refresh).toHaveBeenCalledTimes(2)
  })

  it('初次读取显示 loading，刷新有名称且禁用，不伪装空列表', async () => {
    downloads.downloadJobsLoading.value = true
    const host = await mountDownloads()
    expect(host.querySelector('[role="status"]')?.textContent).toContain('正在读取下载任务')
    const refresh = host.querySelector<HTMLButtonElement>('[aria-label="刷新下载任务"]')!
    expect(refresh.disabled).toBe(true); expect(refresh.getAttribute('aria-busy')).toBe('true')
    refresh.click(); expect(downloads.loadDownloadJobs).toHaveBeenCalledTimes(1)
    expect(host.querySelector('.download-workspace .empty-state')).toBeNull()
  })

  it('首次失败使用行内读取重试，恢复成功空列表后才显示真空态', async () => {
    downloads.downloadJobsError.value = '连接暂时不可用'
    const host = await mountDownloads()
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('下载任务读取失败')
    expect(host.querySelector('.download-workspace .empty-state')).toBeNull()
    button(host, '重新加载下载任务').click()
    expect(downloads.loadDownloadJobs).toHaveBeenCalledTimes(2)
    expect(downloads.retryDownload).not.toHaveBeenCalled(); expect(hooks.warning).not.toHaveBeenCalled()
    downloads.downloadJobsError.value = ''; downloads.downloadJobsLoaded.value = true; await nextTick()
    expect(host.querySelector('.download-workspace .empty-state')?.textContent).toBe('暂无镜像下载任务')
  })

  it('筛选无匹配不同于成功空集合，输入绑定仍有效', async () => {
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [job()]
    const host = await mountDownloads()
    const input = host.querySelector<HTMLInputElement>('[aria-label="搜索镜像下载任务"]')!
    input.value = 'not-found'; input.dispatchEvent(new Event('input')); await nextTick()
    expect(downloads.downloadQuery.value).toBe('not-found')
    expect(host.querySelector('.download-workspace .empty-state')?.textContent).toBe('没有匹配的镜像下载任务')
  })

  it('所有记录解析异常时不声称没有任务', async () => {
    downloads.downloadJobsLoaded.value = true; downloads.invalidDownloadJobCount.value = 2
    const host = await mountDownloads()
    expect(host.textContent).toContain('下载记录无法读取')
    expect(host.textContent).not.toContain('暂无镜像下载任务')
  })

  it('缓存刷新失败保留记录与上次成功获取时间；重试中禁用并保留错误', async () => {
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [job()]
    downloads.downloadJobsLastSuccessAt.value = '2026-10-13T12:30:00Z'; downloads.downloadJobsError.value = '刷新超时'
    const host = await mountDownloads()
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('下载任务刷新失败')
    expect(host.querySelector('[role="alert"] time')?.getAttribute('datetime')).toBe('2026-10-13T12:30:00Z')
    expect(host.querySelector('.download-row')?.textContent).toContain('alpine:latest')
    downloads.downloadJobsLoading.value = true; await nextTick()
    expect(button(host, '正在重试…').disabled).toBe(true)
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('刷新超时')
  })

  it('断流仍显示真实下载状态，但明确上次报告并隐藏缓存速度；恢复仅触发读取', async () => {
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [job()]; downloads.interrupted.value = true
    const host = await mountDownloads()
    const row = host.querySelector('.download-row')!
    expect(row.querySelector('.job-state')?.textContent).toBe('下载中')
    expect(row.textContent).toContain('进度连接中断'); expect(row.textContent).toContain('任务可能仍在执行')
    expect(row.textContent).toContain('上次报告：50%'); expect(row.textContent).toContain('上次获取：')
    expect(row.textContent).not.toContain('/s')
    expect(row.querySelector('[role="progressbar"]')?.getAttribute('aria-label')).toContain('上次报告进度')
    button(host, '重新获取进度').click(); expect(downloads.loadDownloadJobs).toHaveBeenCalledTimes(2)
    expect(downloads.retryDownload).not.toHaveBeenCalled(); expect(downloads.stopDownload).not.toHaveBeenCalled(); expect(downloads.confirmDeleteJob).not.toHaveBeenCalled()
    downloads.interrupted.value = false; downloads.trusted.value = true; await nextTick()
    expect(row.textContent).not.toContain('上次报告'); expect(row.textContent).not.toContain('进度连接中断'); expect(row.textContent).toContain('/s')
  })

  it('等待首条实时事件时隐藏快照速度但不宣称断流，真正刷新失败不遮掉健康流', async () => {
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [job()]
    const host = await mountDownloads()
    const row = host.querySelector('.download-row')!
    expect(row.textContent).toContain('上次报告'); expect(row.textContent).not.toContain('/s'); expect(row.textContent).not.toContain('进度连接中断')
    downloads.trusted.value = true; downloads.downloadJobsError.value = '刷新失败'; await nextTick()
    expect(row.textContent).toContain('/s'); expect(row.textContent).not.toContain('上次报告')
  })

  it('终态不展示缓存速度或中断提示且保留产物说明', async () => {
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [job('completed')]
    const host = await mountDownloads()
    const row = host.querySelector('.download-row')!
    expect(row.textContent).toContain('已完成'); expect(row.textContent).toContain('镜像已存在')
    expect(row.textContent).not.toContain('/s'); expect(row.textContent).not.toContain('进度连接中断'); expect(row.textContent).not.toContain('上次报告')
  })
})


describe('DockerImagePanel 创建后对账归属', () => {
  it.each(['session', 'unmount'])('%s 失效发生在 inventory await 期间时不提示或开始镜像 GET', async (kind) => {
    let resolve!: (value: { failed: string[] }) => void
    hooks.inventoryRefresh.mockReturnValue(new Promise<{ failed: string[] }>((done) => { resolve = done }))
    const host = await mountDownloads()
    const local = hooks.local.mock.results[0]!.value as { refresh: ReturnType<typeof vi.fn> }
    expect(local.refresh).toHaveBeenCalledTimes(1)
    host.querySelector<HTMLButtonElement>('.container-created-stub')!.click()
    expect(hooks.inventoryRefresh).toHaveBeenCalledExactlyOnceWith({ inventory: true, force: true })
    if (kind === 'session') invalidateSession()
    else { const entry = mounted.pop()!; entry.app.unmount(); entry.host.remove() }
    resolve({ failed: ['inventory'] }); await Promise.resolve(); await nextTick()
    expect(hooks.warning).not.toHaveBeenCalled(); expect(local.refresh).toHaveBeenCalledTimes(1)
  })

  it.each([false, true])('同页会话保持有效时完成后续只读对账，读取失败=%s', async (failed) => {
    const failures = failed ? ['inventory'] : []
    hooks.inventoryRefresh.mockResolvedValue({ failed: failures })
    const host = await mountDownloads()
    const local = hooks.local.mock.results[0]!.value as { refresh: ReturnType<typeof vi.fn> }
    host.querySelector<HTMLButtonElement>('.container-created-stub')!.click()
    await Promise.resolve(); await nextTick()
    expect(local.refresh).toHaveBeenCalledTimes(2)
    expect(hooks.warning).toHaveBeenCalledTimes(failures.length ? 1 : 0)
  })
})
