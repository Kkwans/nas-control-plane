import { computed, onScopeDispose, shallowRef } from 'vue'
import type { DockerContainerCreateInput } from '@/api/system'
import { requestDockerContainerCreateJobs, isDockerContainerCreateAdmissionRejected, requestDockerContainerCreateJob, requestDockerContainerCreateJobStatus } from '@/api/dockerContainerCreateJob'
import { captureDockerMutationFence } from './dockerMutationFence'
import { captureSession } from '@/session/sessionLifecycle'

export interface ContainerCreateCompletion {
  readonly containerId: string
  readonly created: true
  readonly started: boolean
}
export interface ContainerCreateJobEvidence {
  readonly id: string
  readonly type: 'docker-container-create'
  readonly status: 'queued' | 'running' | 'completed' | 'failed' | 'interrupted'
  readonly persistenceStatus?: 'pending'
  readonly createResult?: ContainerCreateCompletion
}
export interface ContainerCreateAdmission {
  readonly token: symbol
  readonly generation: number
  readonly runContainer: boolean
  readonly reservedAt: number
  readonly phase: 'confirming' | 'submitting' | 'unknown' | 'known' | 'resolved'
  readonly job?: ContainerCreateJobEvidence
  readonly checking: boolean
  readonly checkVersion: number
  readonly completionVersion: number
  readonly checked: boolean
  readonly lastReadSucceeded: boolean
  readonly inventoryChecked: boolean
  readonly observedActive: boolean
  readonly notice: string
}
type Handle = symbol | ContainerCreateAdmission
const tokenOf = (handle: Handle) => typeof handle === 'symbol' ? handle : handle.token
let registryGeneration = -1
let registry = shallowRef<ContainerCreateAdmission | null>(null)
function sessionRegistry(session: ReturnType<typeof captureSession>) {
  if (registryGeneration !== session.generation) {
    registryGeneration = session.generation
    registry = shallowRef<ContainerCreateAdmission | null>(null)
    const owned = registry
    session.signal.addEventListener('abort', () => { owned.value = null }, { once: true })
  }
  return registry
}
export function containerCreateJobActive(job: { status: string; persistenceStatus?: 'pending' }) {
  return job.status === 'queued' || job.status === 'running' || job.persistenceStatus === 'pending'
}
export function containerCreateCanRelease(entry: ContainerCreateAdmission) {
  if (entry.checking || !entry.checked || !entry.lastReadSucceeded || !entry.inventoryChecked || entry.observedActive) return false
  if (entry.phase === 'unknown') return true
  return entry.phase === 'known' && !!entry.job && !containerCreateJobActive(entry.job) && ['failed', 'interrupted'].includes(entry.job.status)
}
export function containerCreateAdmissionLabel(entry: ContainerCreateAdmission) {
  if (entry.phase === 'confirming') return '等待确认'
  if (entry.phase === 'submitting') return '正在提交，尚未确认接收'
  if (entry.phase === 'unknown') return '接收未知，勿重复提交'
  if (entry.phase === 'resolved') return '创建已完成，等待领取结果'
  if (entry.job?.persistenceStatus === 'pending') return '结果保存中'
  if (entry.job?.status === 'queued') return '已接收，排队中'
  if (entry.job?.status === 'running') return '执行中'
  return '结果需核对，防重复保护仍保留'
}

/** 仅保存意图与安全证据，表单和服务端文案不得进入登录会话锁。 */
function evidence(job: ContainerCreateJobEvidence, runContainer: boolean, jobId?: string): ContainerCreateJobEvidence {
  if (job.type !== 'docker-container-create' || !job.id || (jobId !== undefined && job.id !== jobId)) throw new Error('invalid evidence')
  let createResult: ContainerCreateCompletion | undefined
  if (job.status === 'completed' && job.persistenceStatus !== 'pending') {
    const result = job.createResult
    if (!result || !/^[a-f0-9]{64}$/.test(result.containerId) || result.created !== true || result.started !== runContainer) throw new Error('invalid evidence')
    createResult = Object.freeze({ containerId: result.containerId, created: true, started: result.started })
  }
  return Object.freeze({ id: job.id, type: 'docker-container-create', status: job.status,
    ...(job.persistenceStatus === 'pending' ? { persistenceStatus: 'pending' as const } : {}), ...(createResult ? { createResult } : {}) })
}

/** 会话拥有唯一创建槽；scope 只拥有自己的未派发确认。 */
export function useDockerContainerCreateJobs() {
  const session = captureSession(), slot = sessionRegistry(session)
  const fence = captureDockerMutationFence(session)
  const reservations = new Set<symbol>()
  let disposed = false
  const alive = () => !disposed && !session.signal.aborted
  const current = (handle: Handle) => !session.signal.aborted && slot.value?.generation === session.generation && slot.value.token === tokenOf(handle) ? slot.value : null
  function update(handle: Handle, changes: Partial<ContainerCreateAdmission>) {
    const owned = current(handle)
    if (owned) slot.value = Object.freeze({ ...owned, ...changes })
  }
  function remove(handle: Handle) { if (current(handle)) { fence.release('create', tokenOf(handle)); reservations.delete(tokenOf(handle)); slot.value = null } }
  function reserve(runContainer: boolean) {
    if (!alive() || slot.value || typeof runContainer !== 'boolean') return null
    const entry: ContainerCreateAdmission = Object.freeze({ token: Symbol(), generation: session.generation, runContainer, reservedAt: Date.now(), phase: 'confirming', checking: false, checkVersion: 0, completionVersion: 0, checked: false, lastReadSucceeded: false, inventoryChecked: false, observedActive: false, notice: '' })
    if (!fence.reserve('create', entry.token)) return null
    reservations.add(entry.token); slot.value = entry
    return entry
  }
  function cancel(handle: Handle) {
    if (reservations.has(tokenOf(handle)) && current(handle)?.phase === 'confirming') remove(handle)
  }
  async function dispatch(handle: Handle, input: DockerContainerCreateInput): Promise<'accepted' | 'rejected' | 'unknown' | undefined> {
    const owned = current(handle)
    if (!alive() || !owned || !fence.owns('create', owned.token) || !reservations.has(owned.token) || owned.phase !== 'confirming' || input.runContainer !== owned.runContainer) return
    update(handle, { phase: 'submitting' })
    reservations.delete(owned.token)
    try {
      const job = evidence(await requestDockerContainerCreateJob(input), owned.runContainer)
      // scope 卸载不影响原会话接收晚回执；会话失效则不能写入新槽。
      update(handle, { phase: 'known', job, notice: '已接收不代表完成。请只读核对任务。' })
      return 'accepted'
    } catch (error) {
      if (isDockerContainerCreateAdmissionRejected(error)) { remove(handle); return 'rejected' }
      update(handle, { phase: 'unknown', notice: '无法确认是否已接收，创建可能继续执行。请先只读核对，勿重复提交。' })
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
        const job = evidence(await requestDockerContainerCreateJobStatus(owned.job.id, owned.runContainer, session.signal), owned.runContainer, owned.job.id)
        update(handle, { job, observedActive: containerCreateJobActive(job), ...(job.createResult ? { phase: 'resolved', completionVersion: version } : {}) })
      } else {
        const result = await requestDockerContainerCreateJobs(session.signal)
        const observedActive = result.jobs.some(job => job.type === 'docker-container-create' && containerCreateJobActive(job))
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
      notice: [lastReadSucceeded ? '已只读核对任务。' : '任务读取失败，不能判断未接收或未执行。', inventoryChecked ? '容器清单已刷新；当前状态不能证明先前创建未执行。' : '容器清单未成功刷新。'].join(' ') })
  }
  function release(handle: Handle, checkVersion: number) {
    const owned = current(handle)
    if (!alive() || !owned || owned.checkVersion !== checkVersion || !containerCreateCanRelease(owned)) return false
    remove(handle); return true
  }
  function consumeCompletion(handle: Handle, completionVersion: number): ContainerCreateCompletion | null {
    const owned = current(handle)
    if (!alive() || !owned || owned.phase !== 'resolved' || owned.completionVersion !== completionVersion || !owned.job?.createResult) return null
    const result = owned.job.createResult
    remove(handle)
    return result
  }
  onScopeDispose(() => {
    disposed = true
    const owned = slot.value
    if (owned && reservations.has(owned.token) && owned.phase === 'confirming') cancel(owned.token)
    reservations.clear()
  })
  return { entry: computed(() => slot.value), blocked: computed(() => slot.value !== null || fence.otherMutationsBlocked.value), reserve, cancel, dispatch, check, release, consumeCompletion, alive }
}
