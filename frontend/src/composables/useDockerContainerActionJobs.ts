import { computed, onScopeDispose, shallowRef } from 'vue'
import type { ContainerAction, JobSnapshot } from '@/api/system'
import { requestJobs } from '@/api/docker'
import { isDockerContainerActionAdmissionRejected, requestDockerContainerActionJob, requestDockerContainerActionJobStatus } from '@/api/dockerContainerActionJob'
import { captureDockerMutationFence } from './dockerMutationFence'
import { captureSession } from '@/session/sessionLifecycle'

export interface ContainerActionAdmission {
  readonly token: symbol
  readonly containerId: string
  readonly name: string
  readonly action: ContainerAction
  phase: 'confirming' | 'submitting' | 'unknown' | 'known'
  job?: JobSnapshot
  checking: boolean
  checkVersion: number
  checked: boolean
  inventoryChecked: boolean
  observedActive: boolean
  notice: string
}
let registryGeneration = -1
let registry = shallowRef<Record<string, ContainerActionAdmission>>({})
function sessionRegistry(session: ReturnType<typeof captureSession>) {
  if (registryGeneration !== session.generation) {
    registryGeneration = session.generation
    registry = shallowRef({})
    const owned = registry
    session.signal.addEventListener('abort', () => { owned.value = {} }, { once: true })
  }
  return registry
}
export function containerActionJobActive(job: JobSnapshot) {
  return job.status === 'queued' || job.status === 'running' || job.persistenceStatus === 'pending'
}
export function containerActionCanRelease(entry: ContainerActionAdmission) {
  if (entry.checking || !entry.checked || entry.observedActive) return false
  if (entry.phase === 'unknown') return true
  return entry.phase === 'known' && !!entry.job && !containerActionJobActive(entry.job) && entry.job.status !== 'completed' && entry.inventoryChecked
}
export function containerActionAdmissionLabel(entry: ContainerActionAdmission) {
  if (entry.phase === 'confirming') return '等待确认'
  if (entry.phase === 'submitting') return '正在提交，尚未确认接收'
  if (entry.phase === 'unknown') return '接收未知，勿重复提交'
  if (entry.job?.persistenceStatus === 'pending') return '结果保存中'
  if (entry.job?.status === 'queued') return '已接收，排队中'
  if (entry.job?.status === 'running') return '执行中'
  return '结果需核对，防重复保护仍保留'
}

/** 登录会话拥有锁；视图仅拥有确认框与提示，不拥有已派发操作。 */
export function useDockerContainerActionJobs() {
  const session = captureSession()
  const fence = captureDockerMutationFence(session)
  const entries = sessionRegistry(session)
  let disposed = false
  const alive = () => !disposed && !session.signal.aborted
  const owns = (entry: ContainerActionAdmission) => !session.signal.aborted && entries.value[entry.containerId]?.token === entry.token
  function update(entry: ContainerActionAdmission, changes: Partial<ContainerActionAdmission>) {
    if (!owns(entry)) return
    entries.value = { ...entries.value, [entry.containerId]: { ...entries.value[entry.containerId]!, ...changes } }
  }
  function remove(entry: ContainerActionAdmission) {
    if (!owns(entry)) return
    fence.release('container', entry.token)
    reservations.delete(entry.token)
    const next = { ...entries.value }; delete next[entry.containerId]; entries.value = next
  }
  function reserve(containerId: string, action: ContainerAction, name: string) {
    if (!alive() || entries.value[containerId] || !/^[a-f0-9]{64}$/.test(containerId) || !['start', 'stop', 'restart'].includes(action)) return null
    const entry: ContainerActionAdmission = Object.freeze({ token: Symbol(), containerId, action, name, phase: 'confirming', checking: false, checkVersion: 0, checked: false, inventoryChecked: false, observedActive: false, notice: '' })
    if (!fence.reserve('container', entry.token)) return null
    entries.value = { ...entries.value, [containerId]: entry }
    return entry
  }
  function cancel(entry: ContainerActionAdmission) {
    if (reservations.has(entry.token) && entries.value[entry.containerId]?.phase === 'confirming') remove(entry)
  }
  async function dispatch(entry: ContainerActionAdmission): Promise<'accepted' | 'rejected' | 'unknown' | undefined> {
    if (!alive() || !reservations.has(entry.token) || !fence.owns('container', entry.token) || !owns(entry) || entries.value[entry.containerId]?.phase !== 'confirming') return
    reservations.delete(entry.token)
    update(entry, { phase: 'submitting' })
    try {
      const job = await requestDockerContainerActionJob(entry.containerId, entry.action)
      // 即使视图已卸载，也必须把晚回执保存到原会话；不向旧视图提示。
      update(entry, { phase: 'known', job, notice: '已接收不代表完成。请只读核对任务。' })
      return 'accepted'
    } catch (error) {
      if (isDockerContainerActionAdmissionRejected(error)) { remove(entry); return 'rejected' }
      update(entry, { phase: 'unknown', notice: '无法确认是否已接收，操作可能继续执行。请先只读核对，勿重复提交。' })
      return 'unknown'
    }
  }
  async function check(entry: ContainerActionAdmission, refreshInventory: () => Promise<boolean>) {
    const owned = entries.value[entry.containerId]
    if (!alive() || !owns(entry) || !owned || owned.checking || owned.phase === 'confirming' || owned.phase === 'submitting') return
    const version = owned.checkVersion + 1
    update(entry, { checking: true, checked: false, inventoryChecked: false, checkVersion: version, notice: '' })
    let inventoryChecked = false
    let readFailed = false
    try {
      if (owned.job) {
        const job = await requestDockerContainerActionJobStatus(owned.job.id, entry.containerId, entry.action, session.signal)
        if (job.status === 'completed' && !containerActionJobActive(job)) {
          remove(entry)
          if (alive()) await refreshInventory().catch(() => false)
          return
        }
        update(entry, { job })
      } else {
        const result = await requestJobs()
        // 只观察活动任务，不凭目标/时间/历史认领丢失的 jobId。
        const observedActive = result.jobs.some(job => job.reference === entry.containerId && /^docker-container-(start|stop|restart)$/.test(job.type) && containerActionJobActive(job))
        if (observedActive) update(entry, { observedActive: true })
        if (result.invalidCount) throw new Error('任务列表不完整')
        update(entry, { observedActive })
      }
    } catch { readFailed = true }
    if (!alive() || !owns(entry)) { update(entry, { checking: false }); return }
    try { inventoryChecked = await refreshInventory() } catch { /* 只读失败不是未执行证据。 */ }
    update(entry, { checking: false, checked: true, inventoryChecked,
      notice: [readFailed ? '任务读取失败，不能据此判断未接收或未执行。' : '已只读核对任务。', inventoryChecked ? '容器清单已刷新；当前状态不能证明先前操作未执行。' : '容器清单刷新失败。'].join(' ') })
  }
  function release(entry: ContainerActionAdmission, checkVersion: number) {
    const current = entries.value[entry.containerId]
    if (!alive() || !owns(entry) || !current || current.checkVersion !== checkVersion || !containerActionCanRelease(current)) return false
    remove(entry)
    return true
  }
  onScopeDispose(() => {
    disposed = true
    for (const entry of Object.values(entries.value)) if (entry.phase === 'confirming' && reservations.has(entry.token)) cancel(entry)
    reservations.clear()
  })
  // 仅释放本实例未派发的确认，不能释放别的视图拥有的确认。
  const reservations = new Set<symbol>()
  return { entries: computed(() => Object.values(entries.value)), blockedIds: computed(() => Object.keys(entries.value)),
    reserve: (id: string, action: ContainerAction, name: string) => { const entry = reserve(id, action, name); if (entry) reservations.add(entry.token); return entry },
    cancel, dispatch, check, release, alive }
}
