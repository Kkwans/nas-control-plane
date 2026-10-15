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
vi.mock('./TaskCenterDrawer.vue', () => ({ default: { render: () => h('section', { 'data-testid': 'removal-tasks' }, '最近任务') } }))
vi.mock('./DockerHubBrowser.vue', () => ({ default: defineComponent({ emits: ['job-created'], setup(_, { emit }) { return () => h('button', { class: 'hub-created-stub', onClick: () => emit('job-created', job()) }, 'fixture 创建下载任务') } }) }))
vi.mock('./CreateContainerDrawer.vue', () => ({ default: defineComponent({ emits: ['created'], setup(_, { emit }) { return () => h('button', { class: 'container-created-stub', onClick: () => emit('created', 'container-A') }, 'fixture 容器已创建') } }) }))
vi.mock('./ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('./NcpSelect.vue', () => ({ default: { render: () => null } }))
vi.mock('./SectionTabs.vue', () => ({ default: defineComponent({ emits: ['update:modelValue'], setup(_, { emit }) { return () => h('button', { onClick: () => emit('update:modelValue', 'downloads') }, '下载任务') } }) }))
vi.mock('element-plus', () => ({
  ElMessage: { warning: hooks.warning },
  ElTooltip: defineComponent({ props: ['appendTo', 'disabled', 'content'], setup(props, { slots }) { return () => h('span', { 'data-tooltip': props.content, 'data-tooltip-owner': (props.appendTo as HTMLElement | undefined)?.tagName, 'data-tooltip-disabled': String(Boolean(props.disabled)) }, slots.default?.()) } }),
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
const mounted: { app: App; host: HTMLElement }[] = []
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
    removeAdmissions: ref({}), images: ref([]), loading: ref(false), error: ref(''), removeFailures: ref([]), selectedImageIds: ref([]), localPage: ref(1),
    localPreference: ref({ sortKey: 'name', sortDirection: 'asc' }), filteredImages: ref([]), localPageCount: ref(1), pagedImages: ref([]),
    allVisibleSelected: ref(false), someVisibleSelected: ref(false), selectedImageCount: ref(0), bulkRemovePending: ref(false),
    refresh: vi.fn(), dateLabel: (value: string) => value,
  })
})
afterEach(() => { for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } })

describe('DockerImagePanel 下载观察状态', () => {
  it('批量未知结果只展示只读镜像核验，不声称存在后台任务', async () => {
    const local = hooks.local() as { removeAdmissions: { value: unknown }; refresh: ReturnType<typeof vi.fn> }
    local.removeAdmissions.value = { 'sha256:fixture': { name: 'alpine', state: 'unknown', kind: 'bulk' } }
    const host = document.createElement('div'); document.body.append(host)
    const app = createApp({ render: () => h(DockerImagePanel, { query: '', containers: [], pageSize: 20 }) })
    app.mount(host); mounted.push({ app, host }); await nextTick()
    const banner = host.querySelector('.removal-admission')!
    expect(banner.textContent).toContain('批量删除结果未确认，请只读核对镜像，勿重复提交。')
    expect(banner.textContent).not.toContain('任务')
    expect([...banner.querySelectorAll('button')].map((item) => item.textContent)).toEqual(['只读刷新镜像'])
    const before = local.refresh.mock.calls.length
    ;(banner.querySelector('button') as HTMLButtonElement).click(); await nextTick()
    expect(local.refresh.mock.calls.length).toBe(before + 1)
  })

  it('镜像提示浮层归属主区域，删除确认及接收/未知期间关闭行提示', async () => {
    const pending = ref(false)
    const local = hooks.local() as Record<string, unknown>
    const image = { id: `sha256:${'a'.repeat(64)}`, repoTags: ['alpine'], repoDigests: [], sizeBytes: 1, createdAt: '', containers: 0 }
    Object.assign(local, { images: ref([image]), pagedImages: ref([image]), filteredImages: ref([image]), containerReferenceCount: () => 0, displayName: () => 'alpine', shortId: () => 'aaaa', isRemovePending: () => pending.value || Boolean((local.removeAdmissions as { value: Record<string, unknown> }).value[image.id]) })
    const host = document.createElement('main'); const container = document.createElement('div'); host.append(container); document.body.append(host)
    const app = createApp({ render: () => h(DockerImagePanel, { query: '', containers: [], pageSize: 20 }) })
    app.mount(container); mounted.push({ app, host }); await nextTick()
    const tooltips = [...host.querySelectorAll<HTMLElement>('[data-tooltip]')]
    expect(tooltips.length).toBeGreaterThan(0)
    expect(tooltips.every((tooltip) => tooltip.dataset.tooltipOwner === 'MAIN')).toBe(true)
    const rowTooltips = [...host.querySelectorAll<HTMLElement>('.resource-row [data-tooltip]')]
    expect(rowTooltips).toHaveLength(3)
    expect(rowTooltips.every((tooltip) => tooltip.dataset.tooltipDisabled === 'false')).toBe(true)
    pending.value = true; await nextTick()
    expect(rowTooltips.every((tooltip) => tooltip.dataset.tooltipDisabled === 'true')).toBe(true)
    for (const state of ['accepted', 'unknown']) {
      (local.removeAdmissions as { value: unknown }).value = { [image.id]: { name: 'alpine', state } }; pending.value = false; await nextTick()
      expect(rowTooltips.every((tooltip) => tooltip.dataset.tooltipDisabled === 'true')).toBe(true)
    }
  })
  it.each(['accepted', 'unknown'])('删除 %s 保留只读核验入口', async (state) => {
    const local = hooks.local() as { removeAdmissions: { value: unknown }; refresh: ReturnType<typeof vi.fn> }
    local.removeAdmissions.value = { 'sha256:fixture': { name: 'alpine', state, jobId: state === 'accepted' ? 'job-1' : undefined } }
    const host = document.createElement('div'); document.body.append(host)
    const app = createApp({ render: () => h(DockerImagePanel, { query: '', containers: [], pageSize: 20 }) })
    app.mount(host); mounted.push({ app, host }); await nextTick()
    expect(host.textContent).toContain(state === 'accepted' ? '尚未确认删除完成' : '请勿重复提交')
    button(host, '只读刷新镜像').click(); await nextTick()
    expect(local.refresh).toHaveBeenCalledTimes(2)
    button(host, '查看最近任务').click(); await nextTick()
    expect(host.querySelector('[data-testid="removal-tasks"]')).not.toBeNull()
  })
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

describe('DockerImagePanel 窄屏下载恢复语义', () => {
  it('同一命名任务保留完整身份、状态、进度、字段名称与只读恢复，不复制观察 owner', async () => {
    const snapshot = { ...job(), reference: `registry.example.test/${'long-project-'.repeat(12)}:latest`, message: '镜像层仍在下载，观察中断不表示任务停止。' }
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [snapshot]; downloads.interrupted.value = true
    const host = await mountDownloads()
    const rows = host.querySelectorAll('.download-row')
    expect(rows).toHaveLength(1)
    const row = rows[0]!
    expect(row.getAttribute('aria-label')).toBe(`${snapshot.reference} 下载任务`)
    expect(row.querySelector('.job-identity strong')?.textContent).toBe(snapshot.reference)
    expect(row.querySelector('.job-identity small')?.textContent).toBe(snapshot.message)
    expect(row.querySelector('.job-state')?.textContent).toBe('下载中')
    expect(row.querySelectorAll('.job-observation-warning')).toHaveLength(1)
    expect(row.querySelectorAll('[role="progressbar"]')).toHaveLength(1)
    expect(row.querySelector('.job-speed .job-mobile-label')?.textContent).toBe('速度：')
    expect(row.querySelector('.job-observed-time .job-mobile-label')?.textContent).toBe('更新时间：')
    expect(row.querySelector('.job-observation-recovery')?.textContent).toContain('任务可能仍在执行')
    const recover = row.querySelector<HTMLButtonElement>('.job-observation-recovery button')!
    expect(recover.getAttribute('aria-label')).toBe(`重新获取 ${snapshot.reference} 的下载进度`)
    recover.click()
    expect(hooks.downloads).toHaveBeenCalledTimes(1)
    expect(downloads.loadDownloadJobs).toHaveBeenCalledTimes(2)
    expect(downloads.retryDownload).not.toHaveBeenCalled()
    expect(downloads.stopDownload).not.toHaveBeenCalled()
    expect(downloads.confirmDeleteJob).not.toHaveBeenCalled()
  })

  it('增加可见取消文字后保留原有写入禁用与读取忙碌守卫', async () => {
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [job()]; downloads.interrupted.value = true
    downloads.canCancelJob = () => false
    const host = await mountDownloads()
    const cancel = host.querySelector<HTMLButtonElement>('.job-actions button')!
    expect(cancel.textContent).toBe('请求取消下载')
    expect(cancel.getAttribute('aria-label')).toBe('请求取消下载（不保证远端立即停止）')
    expect(cancel.disabled).toBe(true)
    cancel.click()
    expect(downloads.stopDownload).not.toHaveBeenCalled()
    downloads.downloadJobsLoading.value = true; await nextTick()
    const recover = host.querySelector<HTMLButtonElement>('.job-observation-recovery button')!
    expect(recover.textContent).toBe('正在获取…')
    expect(recover.disabled).toBe(true)
    recover.click()
    expect(downloads.loadDownloadJobs).toHaveBeenCalledTimes(1)
  })

  it.each([false, true])('带文字重新下载按钮保留既有重试资格，允许=%s', async (allowed) => {
    const snapshot = job('failed')
    downloads.downloadJobsLoaded.value = true; downloads.downloadJobs.value = [snapshot]
    downloads.canRetryJob = () => allowed
    const host = await mountDownloads()
    const retry = host.querySelector<HTMLButtonElement>('.job-actions button')!
    expect(retry.textContent).toBe('重新下载')
    expect(retry.getAttribute('aria-label')).toBe('重新下载')
    expect(retry.disabled).toBe(!allowed)
    retry.click()
    expect(downloads.retryDownload).toHaveBeenCalledTimes(allowed ? 1 : 0)
    if (allowed) expect(downloads.retryDownload).toHaveBeenCalledWith(snapshot)
    expect(downloads.stopDownload).not.toHaveBeenCalled()
    expect(downloads.confirmDeleteJob).not.toHaveBeenCalled()
    expect(host.querySelector('.job-observation-recovery')).toBeNull()
  })
})


describe('下载任务进度受限提示', () => {
  it.each([
    { status: 'running' }, { status: 'completed' }, { status: 'failed' }, { status: 'cancelled' },
    { status: 'interrupted', cancellationStatus: 'unknown' },
    { status: 'running', persistenceStatus: 'pending' }, { status: 'running', cancellationStatus: 'requested' },
  ] as Partial<JobSnapshot>[])('部分统计不显示完整百分比、字节量或速度：%j', async (fields) => {
    downloads.downloadJobsLoaded.value = true
    downloads.downloadJobs.value = [{ ...job(), ...fields, progressLimited: true, progress: 100, totalBytes: 8192 }]
    downloads.trusted.value = true
    const host = await mountDownloads(), row = host.querySelector('.download-row')!
    expect(row.textContent).toContain('进度信息受资源限制，仅保留部分层或状态')
    expect(row.textContent).toContain('完整进度未提供')
    expect(row.querySelector('[role="progressbar"]')).toBeNull()
    expect(row.querySelector('.job-progress')?.textContent).not.toMatch(/%|KiB/)
    expect(row.querySelector('.job-speed')?.textContent).not.toContain('/s')
    if (fields.status === 'completed') expect(row.textContent).toContain('已完成')
    if (fields.persistenceStatus) expect(row.textContent).toContain('尚未确认最终结果')
    if (fields.cancellationStatus === 'requested') expect(row.textContent).toContain('远端执行可能仍在继续')
    if (fields.cancellationStatus === 'unknown') expect(row.textContent).toContain('执行结果未确认')
    downloads.interrupted.value = true; await nextTick()
    expect(row.textContent).toContain('进度信息受资源限制')
    expect(row.textContent).toContain('进度连接中断')
  })
})
