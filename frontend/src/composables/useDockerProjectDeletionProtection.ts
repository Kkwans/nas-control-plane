import { computed, onScopeDispose, shallowRef } from 'vue'
import { deleteDockerProject, isDockerProjectDeleteRejected } from '@/api/docker'
import type { DockerProject } from '@/api/system'
import { captureSession } from '@/session/sessionLifecycle'

export interface ProjectDeletionAdmission {
  readonly token: symbol
  readonly projectId: string
  readonly name: string
  readonly kind: DockerProject['kind']
  phase: 'confirming' | 'submitting' | 'unknown'
  checking: boolean
  checkVersion: number
  inventoryChecked: boolean
  notice: string
}
let generation = -1
let registry = shallowRef(new Map<string, ProjectDeletionAdmission>())
function sessionRegistry(session: ReturnType<typeof captureSession>) {
  if (generation !== session.generation) {
    generation = session.generation
    registry = shallowRef(new Map())
    const owned = registry
    session.signal.addEventListener('abort', () => { owned.value = new Map() }, { once: true })
  }
  return registry
}
export function projectDeletionCanRelease(entry: ProjectDeletionAdmission) {
  return entry.phase === 'unknown' && !entry.checking && entry.inventoryChecked
}
/** 派发后的保护属于原登录会话，而不是路由或清单中的对象。 */
export function useDockerProjectDeletionProtection() {
  const session = captureSession()
  const entries = sessionRegistry(session)
  const reservations = new Set<symbol>()
  let disposed = false
  const alive = () => !disposed && !session.signal.aborted
  const owns = (entry: ProjectDeletionAdmission) => !session.signal.aborted && entries.value.get(entry.projectId)?.token === entry.token
  function update(entry: ProjectDeletionAdmission, changes: Partial<ProjectDeletionAdmission>) {
    if (!owns(entry)) return
    const next = new Map(entries.value)
    next.set(entry.projectId, Object.freeze({ ...next.get(entry.projectId)!, ...changes }))
    entries.value = next
  }
  function remove(entry: ProjectDeletionAdmission) {
    if (!owns(entry)) return
    const next = new Map(entries.value); next.delete(entry.projectId); entries.value = next
  }
  function reserve(project: Pick<DockerProject, 'id' | 'name' | 'kind'>) {
    if (!alive() || !project.id || entries.value.has(project.id)) return null
    const entry: ProjectDeletionAdmission = Object.freeze({ token: Symbol(), projectId: project.id, name: project.name, kind: project.kind, phase: 'confirming', checking: false, checkVersion: 0, inventoryChecked: false, notice: '' })
    reservations.add(entry.token)
    entries.value = new Map(entries.value).set(entry.projectId, entry)
    return entry
  }
  function cancel(entry: ProjectDeletionAdmission) {
    if (reservations.has(entry.token) && entries.value.get(entry.projectId)?.phase === 'confirming') {
      remove(entry)
      reservations.delete(entry.token)
    }
  }
  async function dispatch(entry: ProjectDeletionAdmission) {
    if (!alive() || !reservations.has(entry.token) || !owns(entry) || entries.value.get(entry.projectId)?.phase !== 'confirming') return
    reservations.delete(entry.token)
    update(entry, { phase: 'submitting' })
    try {
      await deleteDockerProject(Object.freeze({ id: entry.projectId, name: entry.name, kind: entry.kind }))
      remove(entry)
      return 'deleted' as const
    } catch (error) {
      if (isDockerProjectDeleteRejected(error)) { remove(entry); return 'rejected' as const }
      update(entry, { phase: 'unknown', notice: '删除结果未知，可能已删除部分或全部资源。请勿重复提交。' })
      return 'unknown' as const
    }
  }
  async function check(entry: ProjectDeletionAdmission, refreshInventory: () => Promise<boolean>) {
    const current = entries.value.get(entry.projectId)
    if (!alive() || !owns(entry) || !current || current.phase !== 'unknown' || current.checking) return
    const version = current.checkVersion + 1
    update(entry, { checking: true, inventoryChecked: false, checkVersion: version })
    let succeeded = false
    try { succeeded = (await refreshInventory()) === true } catch { /* 读取失败不证明删除未发生。 */ }
    update(entry, { checking: false, inventoryChecked: succeeded, notice: succeeded ? '本轮清单读取成功；空清单、同名项目或目标消失都不能证明本次删除结果。项目注册状态可能仍需人工核对。' : '本轮清单读取失败，继续保留保护。' })
  }
  function release(entry: ProjectDeletionAdmission, checkVersion: number) {
    const current = entries.value.get(entry.projectId)
    if (!alive() || !owns(entry) || !current || current.checkVersion !== checkVersion || !projectDeletionCanRelease(current)) return false
    remove(entry)
    return true
  }
  onScopeDispose(() => {
    disposed = true
    for (const entry of entries.value.values()) cancel(entry)
    reservations.clear()
  })
  return { entries: computed(() => [...entries.value.values()]), blockedIds: computed(() => [...entries.value.keys()]), reserve, cancel, dispatch, check, release, alive }
}
