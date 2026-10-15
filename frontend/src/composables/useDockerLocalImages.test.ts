import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref, type EffectScope } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { DockerImageRemoveAdmissionUnknownError } from '@/api/docker'
import { useDockerLocalImages } from './useDockerLocalImages'
const api = vi.hoisted(() => ({ remove: vi.fn(), bulk: vi.fn(), read: vi.fn(), confirm: vi.fn(), success: vi.fn() }))
vi.mock('@/api/docker', async (original) => ({ ...await original<typeof import('@/api/docker')>(), requestDockerImageRemoveJob: api.remove, removeDockerImages: api.bulk, requestDockerImages: api.read }))
vi.mock('element-plus', () => ({ ElMessageBox: { confirm: api.confirm }, ElMessage: { success: api.success, warning: vi.fn(), error: vi.fn() } }))
vi.mock('@/composables/useListPreference', () => ({ useListPreference: () => ({ preference: ref({ sortKey: '', sortDirection: 'asc' }), setSort: vi.fn() }) }))
const id = `sha256:${'a'.repeat(64)}`
const fixture = () => ({ id, repoTags: ['alpine:latest'], repoDigests: [], sizeBytes: 10, createdAt: '', containers: 0 })
let scope: EffectScope
function setup() {
  const active = ref(true)
  scope = effectScope()
  const state = scope.run(() => useDockerLocalImages({ query: ref(''), containers: ref([]), pageSize: ref(20), active }))!
  state.images.value = [fixture()]
  return { state, active }
}
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }
beforeEach(() => { vi.clearAllMocks(); beginSession(); api.confirm.mockResolvedValue(true); api.remove.mockResolvedValue({ id: 'job-1' }); api.read.mockResolvedValue({ images: [fixture()] }) })
afterEach(() => { scope.stop() })
describe('local single-image removal', () => {
  it('locks before confirmation and reports admission rather than completion', async () => {
    const { state } = setup(); const dialog = deferred<boolean>(); api.confirm.mockReturnValue(dialog.promise)
    const first = state.confirmRemove(state.images.value[0]!); await state.confirmRemove(state.images.value[0]!)
    expect(api.confirm).toHaveBeenCalledOnce(); expect(api.remove).not.toHaveBeenCalled()
    dialog.resolve(true); await first
    expect(api.remove).toHaveBeenCalledWith(id); expect(api.bulk).not.toHaveBeenCalled()
    expect(state.removeAdmissions.value[id]).toMatchObject({ state: 'accepted', jobId: 'job-1' })
    expect(api.success).toHaveBeenCalledWith(expect.stringContaining('任务已接收'))
    expect(api.read).not.toHaveBeenCalled()
  })
  it.each(['navigation', 'session', 'target', 'scope'])('suppresses submission after %s changes during confirmation', async (change) => {
    const { state, active } = setup(); const dialog = deferred<boolean>(); api.confirm.mockReturnValue(dialog.promise)
    const pending = state.confirmRemove(state.images.value[0]!)
    if (change === 'navigation') { active.value = false; active.value = true }
    if (change === 'session') invalidateSession()
    if (change === 'target') state.images.value = []
    if (change === 'scope') scope.stop()
    dialog.resolve(true); await pending
    expect(api.remove).not.toHaveBeenCalled()
  })
  it('preserves unknown admission through read-only inventory refresh and blocks bulk bypass', async () => {
    const { state } = setup(); api.remove.mockRejectedValue(new DockerImageRemoveAdmissionUnknownError())
    await state.confirmRemove(state.images.value[0]!); await state.refresh()
    expect(state.removeAdmissions.value[id]?.state).toBe('unknown')
    await state.confirmRemove(state.images.value[0]!); state.selectedImageIds.value = [id]; await state.confirmRemoveSelected()
    expect(api.remove).toHaveBeenCalledOnce(); expect(api.bulk).not.toHaveBeenCalled(); expect(api.read).toHaveBeenCalledOnce()
  })
  it('rechecks session-shared admission after simultaneous instance confirmations', async () => {
    const first = setup().state; const firstScope = scope
    const second = setup().state
    const firstDialog = deferred<boolean>(); const secondDialog = deferred<boolean>()
    api.confirm.mockReturnValueOnce(firstDialog.promise).mockReturnValueOnce(secondDialog.promise)
    try {
      const firstPending = first.confirmRemove(first.images.value[0]!)
      const secondPending = second.confirmRemove(second.images.value[0]!)
      expect(api.confirm).toHaveBeenCalledTimes(2)
      firstDialog.resolve(true); await firstPending
      secondDialog.resolve(true); await secondPending
      expect(api.remove).toHaveBeenCalledExactlyOnceWith(id)
      expect(second.removeAdmissions.value[id]).toMatchObject({ state: 'accepted', jobId: 'job-1' })
    } finally { firstScope.stop() }
  })
  it('retains unknown admission across page remount in the same session', async () => {
    const { state } = setup(); api.remove.mockRejectedValue(new DockerImageRemoveAdmissionUnknownError())
    await state.confirmRemove(state.images.value[0]!); scope.stop()
    const remounted = setup().state
    expect(remounted.removeAdmissions.value[id]?.state).toBe('unknown')
    await remounted.confirmRemove(remounted.images.value[0]!)
    expect(api.remove).toHaveBeenCalledOnce()
  })
  it('retains uncertain admission when navigation races an accepted response', async () => {
    const { state, active } = setup(); const receipt = deferred<{ id: string }>(); api.remove.mockReturnValue(receipt.promise)
    const pending = state.confirmRemove(state.images.value[0]!); await Promise.resolve()
    active.value = false; receipt.resolve({ id: 'job-1' }); await pending; active.value = true
    expect(state.removeAdmissions.value[id]?.state).toBe('unknown'); expect(api.success).not.toHaveBeenCalled()
    await state.confirmRemove(state.images.value[0]!); expect(api.remove).toHaveBeenCalledOnce()
  })
})
