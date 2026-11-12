import { ref } from 'vue'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'

export type TerminalLease = { readonly tabId: string; readonly attemptId: number }

// 只记录本页尝试；名额不是 Agent 的可用容量，退役 socket 仍占用本页位置。
export function createTerminalPageOwner(limit = 4) {
  const session = captureSession()
  const revision = ref(0)
  let alive = true
  let shown = true
  let selected = ''
  let epoch = 0
  let generation = 0
  const leases = new Map<TerminalLease, { socket: object | null; retire: (reason: string) => void }>()
  const panels = new Map<string, { select: () => void; clear: () => void }>()
  const current = () => alive && shown && !session.signal.aborted && isCurrentSession(session.generation)
  return {
    get session() { return session },
    get occupiedCount() { void revision.value; return leases.size },
    get limit() { return limit },
    get retiredCount() { void revision.value; return [...leases.keys()].filter(lease => !panels.has(lease.tabId)).length },
    get selectionEpoch() { return epoch },
    current,
    selected(tabId: string) { return current() && selected === tabId && panels.has(tabId) },
    register(tabId: string, callbacks: { select: () => void; clear: () => void }) {
      if (panels.has(tabId)) throw new Error('duplicate terminal panel')
      panels.set(tabId, callbacks)
      revision.value++
      return () => { panels.delete(tabId); revision.value++; if (selected === tabId) { selected = ''; epoch++ } }
    },
    deselect() {
      const previous = panels.get(selected)
      selected = ''
      epoch++
      previous?.select()
    },
    select(tabId: string) {
      if (!current() || !panels.has(tabId) || selected === tabId) return
      const previous = panels.get(selected)
      selected = tabId
      epoch++
      previous?.select()
      panels.get(tabId)?.select()
    },
    reserve(tabId: string, retire: (reason: string) => void): TerminalLease | null {
      if (!current() || !panels.has(tabId) || leases.size >= limit) return null
      const lease = { tabId, attemptId: ++generation }
      leases.set(lease, { socket: null, retire })
      revision.value++
      return lease
    },
    bindSocket(lease: TerminalLease, socket: object) {
      const entry = leases.get(lease)
      if (!entry || entry.socket) throw new Error('invalid terminal lease')
      entry.socket = socket
    },
    release(lease: TerminalLease, socket: object | null = null) {
      const entry = leases.get(lease)
      if (entry && entry.socket === socket) { leases.delete(lease); revision.value++ }
    },
    invalidate(reason: string, permanent = false) {
      shown = false
      if (permanent) alive = false
      epoch++
      for (const entry of [...leases.values()]) entry.retire(reason)
      for (const panel of panels.values()) panel.clear()
    },
    show() { if (alive) shown = true },
  }
}
export type TerminalPageOwner = ReturnType<typeof createTerminalPageOwner>
