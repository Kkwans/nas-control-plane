import { computed, onScopeDispose, shallowRef } from 'vue'
import type { ComposeAction, ComposeActionJobSnapshot, ComposeActionJobInput } from '@/api/composeActionJob'
import { normalizeComposeProjectId, requestComposeActionJobs, isComposeActionAdmissionRejected, requestComposeActionJob, requestComposeActionJobStatus } from '@/api/composeActionJob'
import { captureDockerMutationFence } from './dockerMutationFence'
import { captureSession } from '@/session/sessionLifecycle'

export interface ComposeActionCompletion {
  readonly projectId: string
  readonly action: ComposeAction
  readonly state: 'running' | 'stopped'
  readonly serviceCount: number
}
export interface ComposeActionJobEvidence {
  readonly id: string
  readonly type: `compose-${ComposeAction}`
  readonly status: 'queued' | 'running' | 'completed' | 'failed' | 'interrupted'
  readonly persistenceStatus?: 'pending'
  readonly composeActionResult?: ComposeActionCompletion
}
export interface ComposeActionAdmission {
  readonly token: symbol
  readonly generation: number
  readonly action: ComposeAction
  readonly input: Readonly<ComposeActionJobInput>
  readonly projectId: string
  readonly displayName: string
  readonly workingDirectory: string
  readonly configFiles: readonly string[]
  readonly reservedAt: number
  readonly phase: 'confirming' | 'submitting' | 'unknown' | 'known' | 'resolved'
  readonly job?: ComposeActionJobEvidence
  readonly checking: boolean
  readonly checkVersion: number
  readonly completionVersion: number
  readonly checked: boolean
  readonly lastReadSucceeded: boolean
  readonly inventoryChecked: boolean
  readonly observedActive: boolean
  readonly notice: string
}
type Handle = symbol | ComposeActionAdmission
const tokenOf = (handle: Handle) => typeof handle === 'symbol' ? handle : handle.token
let registryGeneration = -1
let registry = shallowRef<ComposeActionAdmission | null>(null)
function sessionRegistry(session: ReturnType<typeof captureSession>) {
  if (registryGeneration !== session.generation) {
    registryGeneration = session.generation
    registry = shallowRef<ComposeActionAdmission | null>(null)
    const owned = registry
    session.signal.addEventListener('abort', () => { owned.value = null }, { once: true })
  }
  return registry
}
export function composeActionJobActive(job: { status: string; persistenceStatus?: 'pending' }) {
  return job.status === 'queued' || job.status === 'running' || job.persistenceStatus === 'pending'
}
export function composeActionCanRelease(entry: ComposeActionAdmission) {
  if (entry.checking || !entry.checked || !entry.lastReadSucceeded || !entry.inventoryChecked || entry.observedActive) return false
  if (entry.phase === 'unknown') return true
  return entry.phase === 'known' && !!entry.job && !composeActionJobActive(entry.job) && ['failed', 'interrupted'].includes(entry.job.status)
}
export function composeActionAdmissionLabel(entry: ComposeActionAdmission) {
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
function evidence(job: ComposeActionJobSnapshot, projectId: string, action: ComposeAction, jobId?: string): ComposeActionJobEvidence {
  if (job.type !== `compose-${action}` || job.reference !== projectId || !job.id ||
    (jobId !== undefined && job.id !== jobId) || !['queued', 'running', 'completed', 'failed', 'interrupted'].includes(job.status)) throw new Error('invalid evidence')
  let composeActionResult: ComposeActionCompletion | undefined
  if (job.persistenceStatus !== 'pending' && job.composeActionResult) {
    const result = job.composeActionResult
    if (job.status !== 'completed' || result.projectId !== projectId || result.action !== action ||
      result.state !== (action === 'stop' ? 'stopped' : 'running') || !Number.isInteger(result.serviceCount) ||
      result.serviceCount < 1 || result.serviceCount > 1024) throw new Error('invalid evidence')
    composeActionResult = Object.freeze({ projectId, action, state: result.state, serviceCount: result.serviceCount })
  }
  if (job.status === 'completed' && job.persistenceStatus !== 'pending' && !composeActionResult) throw new Error('invalid evidence')
  return Object.freeze({ id: job.id, type: job.type, status: job.status,
    ...(job.persistenceStatus === 'pending' ? { persistenceStatus: 'pending' as const } : {}), ...(composeActionResult ? { composeActionResult } : {}) })
}

const encoder = new TextEncoder()
function validText(value: unknown, limit: number): value is string {
  return typeof value === 'string' && normalizeComposeProjectId(value).length > 0 && encoder.encode(value).length <= limit &&
    !Array.from(value).some(char => { const code = char.codePointAt(0)!; return code < 32 || (code >= 127 && code <= 159) }) &&
    !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value)
}

/** 会话拥有唯一项目操作槽；scope 只拥有自己的未派发确认。 */
export function useComposeActionJobs() {
  const session = captureSession(), slot = sessionRegistry(session)
  const fence = captureDockerMutationFence(session)
  const reservations = new Set<symbol>()
  let disposed = false
  const alive = () => !disposed && !session.signal.aborted
  const current = (handle: Handle) => !session.signal.aborted && slot.value?.generation === session.generation && slot.value.token === tokenOf(handle) ? slot.value : null
  function update(handle: Handle, changes: Partial<ComposeActionAdmission>) {
    const owned = current(handle)
    if (owned) slot.value = Object.freeze({ ...owned, ...changes })
  }
  function remove(handle: Handle) { if (current(handle)) { fence.release('compose', tokenOf(handle)); reservations.delete(tokenOf(handle)); slot.value = null } }
  function reserve(input: ComposeActionJobInput, action: ComposeAction, displayName = input?.projectId) {
    if (!alive() || slot.value || !['start', 'stop', 'restart'].includes(action) || !input ||
      !validText(input.projectId, 256) || !validText(input.workingDirectory, 4096) || !validText(displayName, 4096) ||
      !Array.isArray(input.configFiles) || input.configFiles.length < 1 || input.configFiles.length > 32 ||
      !Array.from(input.configFiles).every(path => validText(path, 4096)) || new Set(input.configFiles).size !== input.configFiles.length) return null
    const frozenInput = Object.freeze({ projectId: normalizeComposeProjectId(input.projectId), workingDirectory: input.workingDirectory,
      configFiles: Object.freeze([...input.configFiles]) })
    if (encoder.encode(JSON.stringify({ workingDirectory: frozenInput.workingDirectory, configFiles: frozenInput.configFiles })).length > 65536) return null
    const entry: ComposeActionAdmission = Object.freeze({ token: Symbol(), generation: session.generation, action, input: frozenInput,
      projectId: frozenInput.projectId, displayName, workingDirectory: frozenInput.workingDirectory, configFiles: frozenInput.configFiles,
      reservedAt: Date.now(), phase: 'confirming', checking: false, checkVersion: 0, completionVersion: 0, checked: false, lastReadSucceeded: false,
      inventoryChecked: false, observedActive: false, notice: '' })
    if (!fence.reserve('compose', entry.token)) return null
    reservations.add(entry.token); slot.value = entry
    return entry
  }
  function cancel(handle: Handle) {
    if (reservations.has(tokenOf(handle)) && current(handle)?.phase === 'confirming') remove(handle)
  }
  async function dispatch(handle: Handle): Promise<'accepted' | 'rejected' | 'unknown' | undefined> {
    const owned = current(handle)
    if (!alive() || !owned || !fence.owns('compose', owned.token) || !reservations.has(owned.token) || owned.phase !== 'confirming') return
    update(handle, { phase: 'submitting' })
    reservations.delete(owned.token)
    try {
      const job = evidence(await requestComposeActionJob(owned.input, owned.action), owned.projectId, owned.action)
      // scope 卸载不影响原会话接收晚回执；会话失效则不能写入新槽。
      update(handle, { phase: 'known', job, notice: '已接收不代表完成。请只读核对任务。' })
      return 'accepted'
    } catch (error) {
      if (isComposeActionAdmissionRejected(error)) { remove(handle); return 'rejected' }
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
        const job = evidence(await requestComposeActionJobStatus(owned.job.id, owned.projectId, owned.action, session.signal), owned.projectId, owned.action, owned.job.id)
        update(handle, { job, observedActive: composeActionJobActive(job), ...(job.status === 'completed' && job.composeActionResult && !composeActionJobActive(job) ? { phase: 'resolved', completionVersion: version } : {}) })
      } else {
        const result = await requestComposeActionJobs(session.signal)
        const observedActive = result.jobs.some(job => /^compose-(start|stop|restart)$/.test(job.type) && composeActionJobActive(job))
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
    if (riskAcknowledged !== true || !alive() || !owned || owned.checkVersion !== checkVersion || !composeActionCanRelease(owned)) return false
    remove(handle); return true
  }
  function consumeCompletion(handle: Handle, completionVersion: number): ComposeActionCompletion | null {
    const owned = current(handle)
    if (!alive() || !owned || owned.phase !== 'resolved' || owned.completionVersion !== completionVersion || !owned.job?.composeActionResult) return null
    const result = owned.job.composeActionResult
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
