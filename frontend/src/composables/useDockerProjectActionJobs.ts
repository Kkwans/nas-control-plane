import { computed, onScopeDispose, shallowRef } from 'vue'
import type { DockerProjectAction, DockerProjectActionJobSnapshot, DockerProjectActionJobItem } from '@/api/dockerProjectActionJob'
import { requestDockerProjectActionJobs, isDockerProjectActionAdmissionRejected, requestDockerProjectActionJob, requestDockerProjectActionJobStatus } from '@/api/dockerProjectActionJob'
import { captureDockerMutationFence } from './dockerMutationFence'
import { captureSession } from '@/session/sessionLifecycle'

export interface ProjectActionCompletion {
  readonly projectId: 'standalone'
  readonly kind: 'standalone'
  readonly action: DockerProjectAction
  readonly items: readonly Readonly<DockerProjectActionJobItem>[]
}
export interface ProjectActionJobEvidence {
  readonly id: string
  readonly type: `docker-project-${DockerProjectAction}`
  readonly status: 'queued' | 'running' | 'completed' | 'failed' | 'interrupted'
  readonly persistenceStatus?: 'pending'
  readonly projectActionResult?: ProjectActionCompletion
}
export interface ProjectActionAdmission {
  readonly token: symbol
  readonly generation: number
  readonly action: DockerProjectAction
  readonly containerIds: readonly string[]
  readonly reservedAt: number
  readonly phase: 'confirming' | 'submitting' | 'unknown' | 'known' | 'resolved'
  readonly job?: ProjectActionJobEvidence
  readonly checking: boolean
  readonly checkVersion: number
  readonly completionVersion: number
  readonly checked: boolean
  readonly lastReadSucceeded: boolean
  readonly inventoryChecked: boolean
  readonly observedActive: boolean
  readonly notice: string
}
type Handle = symbol | ProjectActionAdmission
const tokenOf = (handle: Handle) => typeof handle === 'symbol' ? handle : handle.token
let registryGeneration = -1
let registry = shallowRef<ProjectActionAdmission | null>(null)
function sessionRegistry(session: ReturnType<typeof captureSession>) {
  if (registryGeneration !== session.generation) {
    registryGeneration = session.generation
    registry = shallowRef<ProjectActionAdmission | null>(null)
    const owned = registry
    session.signal.addEventListener('abort', () => { owned.value = null }, { once: true })
  }
  return registry
}
export function projectActionJobActive(job: { status: string; persistenceStatus?: 'pending' }) {
  return job.status === 'queued' || job.status === 'running' || job.persistenceStatus === 'pending'
}
export function projectActionCanRelease(entry: ProjectActionAdmission) {
  if (entry.checking || !entry.checked || !entry.lastReadSucceeded || !entry.inventoryChecked || entry.observedActive) return false
  if (entry.phase === 'unknown') return true
  return entry.phase === 'known' && !!entry.job && !projectActionJobActive(entry.job) && ['failed', 'interrupted'].includes(entry.job.status)
}
export function projectActionAdmissionLabel(entry: ProjectActionAdmission) {
  if (entry.phase === 'confirming') return '等待确认'
  if (entry.phase === 'submitting') return '正在提交，尚未确认接收'
  if (entry.phase === 'unknown') return '接收未知，勿重复提交'
  if (entry.phase === 'resolved') return '项目操作已完成，等待领取结果'
  if (entry.job?.persistenceStatus === 'pending') return '结果保存中'
  if (entry.job?.status === 'queued') return '已接收，排队中'
  if (entry.job?.status === 'running') return '执行中'
  return '结果需核对，防重复保护仍保留'
}

/** 仅保存冻结意图与白名单证据，不保存服务端文案或任意错误。 */
function evidence(job: DockerProjectActionJobSnapshot, ids: readonly string[], action: DockerProjectAction, jobId?: string): ProjectActionJobEvidence {
  if (job.type !== `docker-project-${action}` || job.reference !== 'standalone' || !job.id ||
    (jobId !== undefined && job.id !== jobId) || !['queued', 'running', 'completed', 'failed', 'interrupted'].includes(job.status)) throw new Error('invalid evidence')
  let projectActionResult: ProjectActionCompletion | undefined
  if (job.persistenceStatus !== 'pending' && job.projectActionResult) {
    const result = job.projectActionResult
    if (result.projectId !== 'standalone' || result.kind !== 'standalone' || result.action !== action ||
      !Array.isArray(result.items) || result.items.length !== ids.length || new Set(result.items.map(item => item.containerId)).size !== ids.length ||
      result.items.some(item => !ids.includes(item.containerId))) throw new Error('invalid evidence')
    const desired = action === 'stop' ? 'stopped' : 'running', opposite = action === 'stop' ? 'running' : 'stopped'
    for (const item of result.items) {
      if (!(item.outcome === 'succeeded' && item.state === desired && item.errorCode === undefined) &&
        !(item.outcome === 'state-mismatch' && item.state === opposite && item.errorCode === 'DOCKER_CONTAINER_STATE_MISMATCH') &&
        !(item.outcome === 'unknown' && item.state === 'unknown' && item.errorCode === 'JOB_EXECUTION_INTERRUPTED')) throw new Error('invalid evidence')
    }
    const mismatch = result.items.some(item => item.outcome === 'state-mismatch'), unknown = result.items.some(item => item.outcome === 'unknown')
    if ((job.status === 'completed' && (mismatch || unknown)) || (job.status === 'failed' && (!mismatch || unknown)) ||
      (job.status === 'interrupted' && !unknown) || !['completed', 'failed', 'interrupted'].includes(job.status)) throw new Error('invalid evidence')
    projectActionResult = Object.freeze({ projectId: 'standalone', kind: 'standalone', action,
      items: Object.freeze(ids.map(id => {
        const item = result.items.find(item => item.containerId === id)!
        return Object.freeze({ containerId: id, outcome: item.outcome, state: item.state, ...(item.errorCode ? { errorCode: item.errorCode } : {}) })
      })) })
  }
  if (job.status === 'completed' && job.persistenceStatus !== 'pending' && !projectActionResult) throw new Error('invalid evidence')
  return Object.freeze({ id: job.id, type: job.type, status: job.status,
    ...(job.persistenceStatus === 'pending' ? { persistenceStatus: 'pending' as const } : {}), ...(projectActionResult ? { projectActionResult } : {}) })
}

/** 会话拥有唯一项目操作槽；scope 只拥有自己的未派发确认。 */
export function useDockerProjectActionJobs() {
  const session = captureSession(), slot = sessionRegistry(session)
  const fence = captureDockerMutationFence(session)
  const reservations = new Set<symbol>()
  let disposed = false
  const alive = () => !disposed && !session.signal.aborted
  const current = (handle: Handle) => !session.signal.aborted && slot.value?.generation === session.generation && slot.value.token === tokenOf(handle) ? slot.value : null
  function update(handle: Handle, changes: Partial<ProjectActionAdmission>) {
    const owned = current(handle)
    if (owned) slot.value = Object.freeze({ ...owned, ...changes })
  }
  function remove(handle: Handle) { if (current(handle)) { fence.release('standalone', tokenOf(handle)); reservations.delete(tokenOf(handle)); slot.value = null } }
  function reserve(containerIds: readonly string[], action: DockerProjectAction) {
    if (!alive() || slot.value || !['start', 'stop', 'restart'].includes(action) || !Array.isArray(containerIds) || containerIds.length < 1 || containerIds.length > 128 || new Set(containerIds).size !== containerIds.length || Array.from(containerIds).some(id => typeof id !== 'string' || !/^[a-f0-9]{64}$/.test(id))) return null
    const entry: ProjectActionAdmission = Object.freeze({ token: Symbol(), generation: session.generation, action, containerIds: Object.freeze([...containerIds]), reservedAt: Date.now(), phase: 'confirming', checking: false, checkVersion: 0, completionVersion: 0, checked: false, lastReadSucceeded: false, inventoryChecked: false, observedActive: false, notice: '' })
    if (!fence.reserve('standalone', entry.token)) return null
    reservations.add(entry.token); slot.value = entry
    return entry
  }
  function cancel(handle: Handle) {
    if (reservations.has(tokenOf(handle)) && current(handle)?.phase === 'confirming') remove(handle)
  }
  async function dispatch(handle: Handle): Promise<'accepted' | 'rejected' | 'unknown' | undefined> {
    const owned = current(handle)
    if (!alive() || !owned || !fence.owns('standalone', owned.token) || !reservations.has(owned.token) || owned.phase !== 'confirming') return
    update(handle, { phase: 'submitting' })
    reservations.delete(owned.token)
    try {
      const job = evidence(await requestDockerProjectActionJob(owned.containerIds, owned.action), owned.containerIds, owned.action)
      // scope 卸载不影响原会话接收晚回执；会话失效则不能写入新槽。
      update(handle, { phase: 'known', job, notice: '已接收不代表完成。请只读核对任务。' })
      return 'accepted'
    } catch (error) {
      if (isDockerProjectActionAdmissionRejected(error)) { remove(handle); return 'rejected' }
      update(handle, { phase: 'unknown', notice: '无法确认是否已接收，项目操作可能继续执行。请先只读核对，勿重复提交。' })
      return 'unknown'
    }
  }
  async function check(handle: Handle, refreshInventory: () => Promise<boolean>) {
    const owned = current(handle)
    if (!alive() || !owned || owned.checking || !['known', 'unknown'].includes(owned.phase)) return
    const version = owned.checkVersion + 1
    update(handle, { checking: true, checked: false, lastReadSucceeded: false, inventoryChecked: false, checkVersion: version, notice: '' })
    let lastReadSucceeded = false, inventoryChecked = false
    try {
      if (owned.phase === 'known' && owned.job) {
        const job = evidence(await requestDockerProjectActionJobStatus(owned.job.id, owned.containerIds, owned.action, session.signal), owned.containerIds, owned.action, owned.job.id)
        update(handle, { job, observedActive: projectActionJobActive(job), ...(job.status === 'completed' && job.projectActionResult && !projectActionJobActive(job) ? { phase: 'resolved', completionVersion: version } : {}) })
      } else {
        const result = await requestDockerProjectActionJobs(session.signal)
        const observedActive = result.jobs.some(job => /^docker-project-(start|stop|restart)$/.test(job.type) && projectActionJobActive(job))
        // 损坏/失败读不能擦掉已观察到的活动证据，也绝不认领列表中的任务。
        if (observedActive) update(handle, { observedActive: true })
        if (result.invalidCount !== 0) throw new Error('incomplete jobs')
        update(handle, { observedActive })
      }
      lastReadSucceeded = true
    } catch { /* 丢弃异常，失败不能证明任务未执行。 */ }
    if (alive() && current(handle)?.checkVersion === version) {
      try { inventoryChecked = (await refreshInventory()) === true } catch { /* 清单失败继续保护。 */ }
    }
    if (current(handle)?.checkVersion !== version) return
    update(handle, { checking: false, checked: true, lastReadSucceeded, inventoryChecked,
      notice: [lastReadSucceeded ? '已只读核对任务。' : '任务读取失败，不能判断未接收或未执行。', inventoryChecked ? '容器清单已刷新；当前状态不能证明先前项目操作未执行。' : '容器清单未成功刷新。'].join(' ') })
  }
  function release(handle: Handle, checkVersion: number, riskAcknowledged: boolean) {
    const owned = current(handle)
    if (riskAcknowledged !== true || !alive() || !owned || owned.checkVersion !== checkVersion || !projectActionCanRelease(owned)) return false
    remove(handle); return true
  }
  function consumeCompletion(handle: Handle, completionVersion: number): ProjectActionCompletion | null {
    const owned = current(handle)
    if (!alive() || !owned || owned.phase !== 'resolved' || owned.completionVersion !== completionVersion || !owned.job?.projectActionResult) return null
    const result = owned.job.projectActionResult
    remove(handle)
    return result
  }
  onScopeDispose(() => {
    disposed = true
    const owned = slot.value
    if (owned && reservations.has(owned.token) && owned.phase === 'confirming') cancel(owned.token)
    reservations.clear()
  })
  return { entry: computed(() => slot.value), blocked: computed(() => slot.value !== null || fence.standaloneBlocked.value), reserve, cancel, dispatch, check, release, consumeCompletion, alive }
}
