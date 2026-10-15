import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref, type EffectScope } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { DockerImageRemoveAdmissionUnknownError } from '@/api/docker'
import { useDockerLocalImages } from './useDockerLocalImages'
const api = vi.hoisted(() => ({ remove: vi.fn(), bulk: vi.fn(), read: vi.fn(), confirm: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/docker', async (original) => ({ ...await original<typeof import('@/api/docker')>(), requestDockerImageRemoveJob: api.remove, removeDockerImages: api.bulk, requestDockerImages: api.read }))
vi.mock('element-plus', () => ({ ElMessageBox: { confirm: api.confirm }, ElMessage: { success: api.success, warning: vi.fn(), error: api.error } }))
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

const otherId = `sha256:${'b'.repeat(64)}`
function select(state: ReturnType<typeof useDockerLocalImages>, ids = [id]) { state.selectedImageIds.value = ids }
function receipt(ids: string[]) { return { items: ids.map((imageId) => ({ imageId, removed: true })), removedCount: ids.length, failedCount: 0, completed: true } }
describe('bulk removal ownership', () => {
  it('settles inventory loading across a mode round trip during a read', async () => {
    const { state, active } = setup(); const response = deferred<{ images: ReturnType<typeof fixture>[] }>(); api.read.mockReturnValue(response.promise)
    const pending = state.refresh(); active.value = false; active.value = true
    response.resolve({ images: [fixture()] }); await pending
    expect(state.loading.value).toBe(false)
  })
  it('releases the reservation on dialog cancellation without a write', async () => {
    const { state } = setup(); select(state); api.confirm.mockRejectedValue('cancel')
    await state.confirmRemoveSelected()
    expect(api.bulk).not.toHaveBeenCalled(); expect(state.bulkRemovePending.value).toBe(false); expect(state.isRemovePending(id)).toBe(false)
    api.confirm.mockResolvedValue(true); api.bulk.mockResolvedValue(receipt([id])); await state.confirmRemoveSelected()
    expect(api.bulk).toHaveBeenCalledOnce()
  })
  it('excludes a selected image already owned by a single confirmation', async () => {
    const { state } = setup(); select(state); const dialog = deferred<boolean>(); api.confirm.mockReturnValue(dialog.promise)
    const pending = state.confirmRemove(state.images.value[0]!); await state.confirmRemoveSelected()
    expect(api.confirm).toHaveBeenCalledOnce(); expect(api.bulk).not.toHaveBeenCalled(); dialog.resolve(true); await pending
  })
  it.each(['alpine:latest', 'sha256:short'])('rejects noncanonical inventory ID %s', async (badId) => {
    const { state } = setup(); state.images.value[0]!.id = badId; select(state, [badId]); await state.confirmRemoveSelected()
    expect(api.confirm).not.toHaveBeenCalled(); expect(api.bulk).not.toHaveBeenCalled()
  })
  it('locks before confirmation, blocks disjoint bulk and selected single writes, and releases when targets disappear', async () => {
    const { state } = setup(); state.images.value.push({ ...fixture(), id: otherId }); select(state)
    const dialog = deferred<boolean>(); api.confirm.mockReturnValue(dialog.promise)
    const pending = state.confirmRemoveSelected()
    expect(state.bulkRemovePending.value).toBe(true)
    select(state, [otherId]); await state.confirmRemoveSelected(); await state.confirmRemove(state.images.value[0]!)
    expect(api.confirm).toHaveBeenCalledOnce(); expect(api.bulk).not.toHaveBeenCalled(); expect(api.remove).not.toHaveBeenCalled()
    state.images.value = []
    dialog.resolve(true); await pending
    expect(api.bulk).not.toHaveBeenCalled(); expect(state.bulkRemovePending.value).toBe(false)
  })
  it.each(['navigation', 'session', 'scope', 'id', 'tags', 'missing', 'in-use'])('does not dispatch after confirmation loses %s ownership', async (change) => {
    const { state, active } = setup(); select(state); const dialog = deferred<boolean>(); api.confirm.mockReturnValue(dialog.promise)
    const pending = state.confirmRemoveSelected()
    if (change === 'navigation') { active.value = false; active.value = true }
    if (change === 'session') beginSession()
    if (change === 'scope') scope.stop()
    if (change === 'id') state.images.value[0]!.id = otherId
    if (change === 'tags') state.images.value[0]!.repoTags.push('changed:tag')
    if (change === 'missing') state.images.value = []
    if (change === 'in-use') state.images.value[0]!.containers = 1
    dialog.resolve(true); await pending
    expect(api.bulk).not.toHaveBeenCalled(); expect(api.success).not.toHaveBeenCalled()
  })
  it.each(['navigation', 'session', 'scope'])('does not publish a late receipt after %s', async (change) => {
    const { state, active } = setup(); select(state); const response = deferred<ReturnType<typeof receipt>>(); api.bulk.mockReturnValue(response.promise)
    const pending = state.confirmRemoveSelected(); await Promise.resolve()
    expect(api.bulk).toHaveBeenCalledExactlyOnceWith([id])
    if (change === 'navigation') { active.value = false; active.value = true }
    if (change === 'session') beginSession()
    if (change === 'scope') scope.stop()
    response.resolve(receipt([id])); await pending
    expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled(); expect(api.read).not.toHaveBeenCalled()
    expect(state.selectedImageIds.value).toEqual([id]); expect(state.removeFailures.value).toEqual([])
    if (change !== 'session') expect(state.removeAdmissions.value[id]).toMatchObject({ kind: 'bulk', state: 'unknown' })
  })
  it('retains lost outcomes through refresh and remount without bulk or single replay', async () => {
    const { state } = setup(); select(state); api.bulk.mockRejectedValue(new Error('lost response'))
    await state.confirmRemoveSelected(); await state.refresh(); scope.stop()
    const remounted = setup().state; select(remounted)
    await remounted.confirmRemoveSelected(); await remounted.confirmRemove(remounted.images.value[0]!)
    expect(api.bulk).toHaveBeenCalledOnce(); expect(api.remove).not.toHaveBeenCalled()
    expect(remounted.removeAdmissions.value[id]).toMatchObject({ kind: 'bulk', state: 'unknown' })
    expect(api.success).not.toHaveBeenCalled(); expect(remounted.removeFailures.value).toEqual([])
  })
  it('keeps concurrent instances from opening another bulk confirmation or single write for reserved IDs', async () => {
    const first = setup().state; const firstScope = scope; const second = setup().state
    const dialog = deferred<boolean>(); api.confirm.mockReturnValue(dialog.promise); api.bulk.mockResolvedValue(receipt([id]))
    try {
      select(first); select(second); const pending = first.confirmRemoveSelected()
      await second.confirmRemoveSelected(); await second.confirmRemove(second.images.value[0]!)
      expect(api.confirm).toHaveBeenCalledOnce(); dialog.resolve(true); await pending
      expect(api.bulk).toHaveBeenCalledOnce(); expect(api.remove).not.toHaveBeenCalled()
    } finally { firstScope.stop() }
  })
  it('retains uncertainty per item while clearing only verified success and preflight in-use failures', async () => {
    const { state } = setup(); const third = `sha256:${'c'.repeat(64)}`
    state.images.value = [id, otherId, third].map((id) => ({ ...fixture(), id })); select(state, [id, otherId, third])
    api.bulk.mockResolvedValue({ items: [{ imageId: id, removed: true }, { imageId: otherId, removed: false, errorCode: 'DOCKER_IMAGE_IN_USE' }, { imageId: third, removed: false, errorCode: 'DOCKER_IMAGE_REMOVE_FAILED' }], removedCount: 1, failedCount: 2, completed: false })
    api.read.mockResolvedValue({ images: state.images.value }); await state.confirmRemoveSelected()
    expect(state.removeAdmissions.value[id]).toBeUndefined(); expect(state.removeAdmissions.value[otherId]).toBeUndefined()
    expect(state.removeAdmissions.value[third]).toMatchObject({ kind: 'bulk', state: 'unknown' })
    expect(state.removeFailures.value.map((item) => item.id)).toEqual([otherId]); expect(state.selectedImageIds.value).toEqual([otherId, third])
    expect(api.success).toHaveBeenCalledWith('已删除 1 个本地镜像')
  })
  it('preserves the 50-item synchronous batch limit', async () => {
    const { state } = setup(); const ids = Array.from({ length: 51 }, (_, i) => `sha256:${i.toString(16).padStart(64, '0')}`)
    state.images.value = ids.map((id) => ({ ...fixture(), id })); select(state, ids); api.bulk.mockImplementation(async (ids: string[]) => receipt(ids))
    await state.confirmRemoveSelected()
    expect(api.bulk.mock.calls.map((args) => args[0].length)).toEqual([50, 1]); expect(api.remove).not.toHaveBeenCalled()
    expect(state.removeAdmissions.value).toEqual({}); expect(api.success).toHaveBeenCalledWith('已删除 51 个本地镜像')
  })
})
