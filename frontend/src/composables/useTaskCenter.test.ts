// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { NcpApiError, type JobListResult, type JobSnapshot } from '@/api/system'
import { invalidateSession } from '@/session/sessionLifecycle'
import { useTaskCenter } from './useTaskCenter'

const api = vi.hoisted(() => ({ list: vi.fn(), cancel: vi.fn(), subscribe: vi.fn() }))
vi.mock('@/api/docker', () => ({ requestJobs: api.list, cancelJob: api.cancel, subscribeJob: api.subscribe }))
const job = (fields: Partial<JobSnapshot> = {}): JobSnapshot => ({ id: 'a', type: 'docker-image-pull', status: 'running', artifactState: 'unknown', cancellable: true, reference: 'image:tag', message: '', progress: 0, createdAt: '2026-10-13T10:00:00Z', updatedAt: '2026-10-13T10:00:00Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...fields })
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const list = (jobs = [job()], invalidCount = 0): JobListResult => ({ jobs, invalidCount })
const scopes: ReturnType<typeof effectScope>[] = []
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); await nextTick() }
function setup() {
  const scope = effectScope(); scopes.push(scope)
  const open = ref(true)
  const state = scope.run(() => useTaskCenter(open))!
  return { state, open, scope }
}
function visibility(value: 'hidden' | 'visible') {
  Object.defineProperty(document, 'visibilityState', { value, configurable: true })
  document.dispatchEvent(new Event('visibilitychange'))
}
beforeEach(() => {
  vi.useFakeTimers(); vi.resetAllMocks()
  Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
  api.list.mockResolvedValue(list()); api.cancel.mockResolvedValue(job({ cancellationStatus: 'requested' }))
})
afterEach(() => { scopes.splice(0).forEach((scope) => scope.stop()); vi.useRealTimers() })

describe('任务中心观察与竞争', () => {
  it.each([
    ['docker-project-start', '项目启动'], ['docker-project-stop', '项目停止'], ['docker-project-restart', '项目重启'],
  ])('%s 复用中文类型筛选和搜索且不可取消', async (type, label) => {
    const project = job({ id: 'project-job', type, reference: 'standalone' })
    api.list.mockResolvedValue(list([project, job()]))
    const { state } = setup(); await flush()
    expect(state.typeOptions.value).toContainEqual({ value: type, label })
    state.query.value = label; expect(state.filteredJobs.value).toEqual([project])
    state.query.value = ''; state.typeFilter.value = type
    expect(state.filteredJobs.value).toEqual([project])
    expect(state.canCancel(project)).toBe(false)
    await state.requestCancel(project); expect(api.cancel).not.toHaveBeenCalled()
  })
  it('创建任务复用中文类型筛选和搜索且不可取消', async () => {
    const create = job({ id: 'create-job', type: 'docker-container-create', reference: undefined })
    api.list.mockResolvedValue(list([create, job()]))
    const { state } = setup(); await flush()
    expect(state.typeOptions.value).toContainEqual({ value: 'docker-container-create', label: '容器创建' })
    state.query.value = '容器创建'; expect(state.filteredJobs.value).toEqual([create])
    state.query.value = ''; state.typeFilter.value = 'docker-container-create'
    expect(state.filteredJobs.value).toEqual([create])
    expect(state.canCancel(create)).toBe(false)
    await state.requestCancel(create); expect(api.cancel).not.toHaveBeenCalled()
  })
  it('首次加载、混合类型、本地筛选和分页，部分及全量解析异常不当作空集合', async () => {
    const first = deferred<JobListResult>(); api.list.mockReturnValueOnce(first.promise)
    const { state } = setup(); expect(state.loading.value).toBe(true)
    first.resolve(list(Array.from({ length: 23 }, (_, i) => job({ id: `${i}`, type: i === 0 ? 'future-job' : 'compose-deploy' })), 2)); await flush()
    expect(state.loading.value).toBe(false); expect(state.hasLoaded.value).toBe(true)
    expect(state.invalidCount.value).toBe(2); expect(state.pagedJobs.value).toHaveLength(20)
    state.page.value = 2; state.typeFilter.value = 'future-job'
    expect(state.page.value).toBe(1); expect(state.filteredJobs.value).toHaveLength(1)
    expect(state.typeOptions.value).toContainEqual({ value: 'future-job', label: 'future-job' })
    state.query.value = '不存在'; expect(state.filteredJobs.value).toHaveLength(0)
    api.list.mockResolvedValueOnce(list([], 4)); await state.refresh()
    expect(state.hasLoaded.value).toBe(true); expect(state.invalidCount.value).toBe(4)
    expect(state.lastFetchedAt.value).toBeInstanceOf(Date)
  })
  it('成功后 15 秒串行轮询，重复刷新不排队且零 SSE', async () => {
    const pending = deferred<JobListResult>(); api.list.mockReturnValueOnce(pending.promise)
    const { state } = setup()
    await vi.advanceTimersByTimeAsync(60_000); await state.refresh(); await state.refresh()
    expect(api.list).toHaveBeenCalledTimes(1)
    pending.resolve(list()); await flush()
    await vi.advanceTimersByTimeAsync(14_999); expect(api.list).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1); expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.subscribe).not.toHaveBeenCalled()
  })
  it('刷新失败保留快照并禁用取消，30/60 秒退避；手动成功恢复', async () => {
    const { state } = setup(); await flush()
    const fetched = state.lastFetchedAt.value
    api.list.mockRejectedValue(new Error('offline')); await state.refresh()
    expect(state.jobs.value).toHaveLength(1); expect(state.error.value).not.toBe('')
    expect(state.lastFetchedAt.value).toBe(fetched); expect(state.stale.value).toBe(true)
    expect(state.canCancel(state.jobs.value[0]!)).toBe(false)
    await vi.advanceTimersByTimeAsync(29_999); expect(api.list).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1); expect(api.list).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(59_999); expect(api.list).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(1); expect(api.list).toHaveBeenCalledTimes(4)
    api.list.mockResolvedValue(list()); await state.refresh()
    expect(state.stale.value).toBe(false); expect(state.canCancel(state.jobs.value[0]!)).toBe(true)
  })
  it('首次失败保留首次错误语义', async () => {
    api.list.mockRejectedValueOnce(new Error('offline')); const { state } = setup(); await flush()
    expect(state.hasLoaded.value).toBe(false); expect(state.loading.value).toBe(false); expect(state.error.value).not.toBe('')
  })
  it('隐藏时清理轮询并忽略旧读取，恢复可见只校准一次', async () => {
    const pending = deferred<JobListResult>(); api.list.mockReturnValueOnce(pending.promise)
    const { state } = setup(); visibility('hidden')
    pending.resolve(list()); await flush(); expect(state.jobs.value).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(60_000); expect(api.list).toHaveBeenCalledTimes(1)
    visibility('visible'); await flush(); expect(api.list).toHaveBeenCalledTimes(2)
    visibility('hidden'); await vi.advanceTimersByTimeAsync(60_000); expect(api.list).toHaveBeenCalledTimes(2)
  })
  it('隐藏再可见时，等待在途请求释放门闩后只做一次校准', async () => {
    const pending = deferred<JobListResult>(); api.list.mockReturnValueOnce(pending.promise)
    const { state } = setup(); visibility('hidden'); visibility('visible'); visibility('visible')
    expect(api.list).toHaveBeenCalledTimes(1)
    pending.resolve(list([job({ id: 'old' })])); await flush()
    expect(api.list).toHaveBeenCalledTimes(2); expect(state.jobs.value[0]!.id).toBe('a')
  })
  it('关闭、重开和旧 finally 不污染新观察，卸载后停止轮询', async () => {
    const first = deferred<JobListResult>(); const second = deferred<JobListResult>()
    api.list.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const { state, open, scope } = setup(); open.value = false; open.value = true
    first.resolve(list([job({ id: 'old' })])); await flush()
    expect(state.jobs.value).toEqual([]); expect(state.loading.value).toBe(true)
    second.resolve(list()); await flush(); expect(state.jobs.value[0]!.id).toBe('a')
    scope.stop(); await vi.advanceTimersByTimeAsync(60_000); expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.cancel).not.toHaveBeenCalled()
  })
  it('取消快照不被较早的列表覆盖，不重复提交且不乐观写 cancelled', async () => {
    const { state } = setup(); await flush()
    const snapshot = deferred<JobListResult>(); api.list.mockReturnValueOnce(snapshot.promise)
    const reading = state.refresh()
    const response = deferred<JobSnapshot>(); api.cancel.mockReturnValueOnce(response.promise)
    const original = state.jobs.value[0]!; const cancelling = state.requestCancel(original)
    await state.requestCancel(original); expect(api.cancel).toHaveBeenCalledTimes(1)
    expect(state.jobs.value[0]!.status).toBe('running'); expect(state.cancellingIds.value.has('a')).toBe(true)
    response.resolve(job({ cancellationStatus: 'requested' })); await cancelling
    snapshot.resolve(list()); await reading
    expect(state.jobs.value[0]!.cancellationStatus).toBe('requested'); expect(state.canCancel(original)).toBe(false)
  })
  it('先完成后迟到取消不回滚终态，取消响应完成如实展示', async () => {
    const { state } = setup(); await flush()
    const response = deferred<JobSnapshot>(); api.cancel.mockReturnValueOnce(response.promise)
    const cancelling = state.requestCancel(state.jobs.value[0]!)
    api.list.mockResolvedValueOnce(list([job({ status: 'completed' })])); await state.refresh()
    response.resolve(job({ status: 'cancelled' })); await cancelling
    expect(state.jobs.value[0]!.status).toBe('completed')
  })
  it('取消后暂时保留较旧列表遗漏的记录，下一轮遗漏则移出当前列表', async () => {
    const { state } = setup(); await flush()
    const snapshot = deferred<JobListResult>(); api.list.mockReturnValueOnce(snapshot.promise)
    const reading = state.refresh(); await state.requestCancel(state.jobs.value[0]!)
    snapshot.resolve(list([])); await reading; expect(state.jobs.value).toHaveLength(1)
    api.list.mockResolvedValueOnce(list([])); await state.refresh(); expect(state.jobs.value).toEqual([])
  })
  it.each(['JOB_NOT_FOUND', 'JOB_CANCEL_NOT_ALLOWED', 'AGENT_UNAVAILABLE'])('保留稳定错误码 %s 并校准', async (code) => {
    const { state } = setup(); await flush()
    api.cancel.mockRejectedValueOnce(new NcpApiError(code, '未完成请求'))
    await state.requestCancel(state.jobs.value[0]!); await flush()
    expect(state.cancelErrors.value.a).toEqual({ code, message: '未完成请求', reference: 'image:tag' })
    expect(state.jobs.value).toHaveLength(1); expect(api.list).toHaveBeenCalledTimes(2)
  })
  it.each([{ id: 'foreign' }, { type: 'future-job' }, { reference: 'other:tag' }])('取消响应身份错误时保留原记录并校准：%s', async (identity) => {
    const { state } = setup(); await flush()
    api.cancel.mockResolvedValueOnce(job({ ...identity, status: 'cancelled' }))
    await state.requestCancel(state.jobs.value[0]!); await flush()
    expect(state.jobs.value[0]!.id).toBe('a'); expect(state.jobs.value[0]!.status).toBe('running')
    expect(state.cancelErrors.value.a?.code).toBe('JOB_RESPONSE_INVALID')
    expect(api.list).toHaveBeenCalledTimes(2)
  })
  it('多 ID 取消失败合并校准，早于失败的列表不得恢复操作', async () => {
    api.list.mockResolvedValue(list([job(), job({ id: 'b' })])); const { state } = setup(); await flush()
    const old = deferred<JobListResult>(); const calibration = deferred<JobListResult>()
    api.list.mockReturnValueOnce(old.promise).mockReturnValueOnce(calibration.promise)
    const reading = state.refresh()
    api.cancel.mockRejectedValue(new NcpApiError('AGENT_UNAVAILABLE', '暂时不可用'))
    await Promise.all(state.jobs.value.map((item) => state.requestCancel(item)))
    expect(state.stale.value).toBe(true)
    old.resolve(list()); await reading; await flush()
    expect(api.list).toHaveBeenCalledTimes(3); expect(state.stale.value).toBe(true)
    expect(state.canCancel(state.jobs.value[0]!)).toBe(false)
    calibration.resolve(list()); await flush(); expect(state.stale.value).toBe(false)
  })
  it('关闭重开同 ID，旧取消 finally 不能清除新提交状态', async () => {
    const { state, open } = setup(); await flush()
    const old = deferred<JobSnapshot>(); const current = deferred<JobSnapshot>()
    api.cancel.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const previous = state.requestCancel(state.jobs.value[0]!)
    open.value = false; open.value = true; await flush()
    const next = state.requestCancel(state.jobs.value[0]!)
    old.resolve(job({ status: 'cancelled' })); await previous
    expect(state.cancellingIds.value.has('a')).toBe(true); expect(state.jobs.value[0]!.status).toBe('running')
    current.resolve(job({ status: 'completed' })); await next
    expect(state.cancellingIds.value.has('a')).toBe(false); expect(state.jobs.value[0]!.status).toBe('completed')
  })
  it('登出后迟到取消、列表和 finally 都不能写回，新会话不受影响', async () => {
    const { state } = setup(); await flush()
    const response = deferred<JobSnapshot>(); api.cancel.mockReturnValueOnce(response.promise)
    const cancelling = state.requestCancel(state.jobs.value[0]!)
    const old = deferred<JobListResult>(); api.list.mockReturnValueOnce(old.promise); const reading = state.refresh()
    invalidateSession(); const next = setup(); await flush()
    response.resolve(job({ status: 'cancelled' })); old.resolve(list()); await cancelling; await reading
    expect(state.jobs.value).toEqual([]); expect(state.cancellingIds.value.size).toBe(0)
    expect(next.state.jobs.value[0]!.status).toBe('running')
  })
})

describe('任务中心进度受限证据', () => {
  it.each([undefined, false])('取消响应和后续列表中的 %s 不清除受限标记', async (progressLimited) => {
    api.list.mockResolvedValueOnce(list([job({ progressLimited: true })]))
    const { state } = setup(); await flush()
    api.cancel.mockResolvedValueOnce(job({ cancellationStatus: 'requested', progressLimited }))
    await state.requestCancel(state.jobs.value[0]!)
    expect(state.jobs.value[0]).toMatchObject({ cancellationStatus: 'requested', progressLimited: true })
    api.list.mockResolvedValueOnce(list([job({ status: 'completed', progress: 100, progressLimited })]))
    await state.refresh()
    expect(state.jobs.value[0]).toMatchObject({ status: 'completed', progressLimited: true })
  })
})
