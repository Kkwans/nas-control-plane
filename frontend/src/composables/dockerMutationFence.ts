import { computed, shallowRef } from 'vue'
import { captureSession } from '@/session/sessionLifecycle'

export type DockerMutationKind = 'standalone' | 'create' | 'container' | 'delete' | 'compose'

function validLease(kind: DockerMutationKind, token: symbol): boolean {
  return typeof token === 'symbol' && ['standalone', 'create', 'container', 'delete', 'compose'].includes(kind)
}

let generation = -1
let owningSignal: AbortSignal | undefined
let leases = shallowRef(new Map<symbol, DockerMutationKind>())

function sessionLeases(session: ReturnType<typeof captureSession>) {
  const current = captureSession()
  // 只接受生命周期模块的真实会话，不能凭空创建新的准入域。
  if (session.signal.aborted || session.generation !== current.generation || session.signal !== current.signal) return null
  if (generation === session.generation && owningSignal === session.signal) return leases

  const owned = shallowRef(new Map<symbol, DockerMutationKind>())
  generation = session.generation
  owningSignal = session.signal
  leases = owned
  session.signal.addEventListener('abort', () => { owned.value = new Map() }, { once: true })
  return owned
}

/** 独立项目操作独占；其余 Docker 变更共享准入，生命周期由调用方负责。 */
export function captureDockerMutationFence(session = captureSession()) {
  const captured = { generation: session.generation, signal: session.signal }
  const admitted = sessionLeases(captured)
  const entries = admitted ?? shallowRef(new Map<symbol, DockerMutationKind>())
  const alive = () => admitted !== null && !captured.signal.aborted
  const standaloneBlocked = computed(() => entries.value.size > 0)
  const otherMutationsBlocked = computed(() => [...entries.value.values()].includes('standalone'))

  function owns(kind: DockerMutationKind, token: symbol): boolean {
    return alive() && validLease(kind, token) && entries.value.get(token) === kind
  }

  function reserve(kind: DockerMutationKind, token: symbol): boolean {
    if (!alive() || !validLease(kind, token) || entries.value.has(token)) return false
    if (kind === 'standalone' ? standaloneBlocked.value : otherMutationsBlocked.value) return false
    entries.value = new Map(entries.value).set(token, kind)
    return true
  }

  function release(kind: DockerMutationKind, token: symbol): boolean {
    if (!owns(kind, token)) return false
    const next = new Map(entries.value)
    next.delete(token)
    entries.value = next
    return true
  }

  return { standaloneBlocked, otherMutationsBlocked, reserve, owns, release }
}
