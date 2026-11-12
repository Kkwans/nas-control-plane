import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { requestDockerProjectActionJob, requestDockerProjectActionJobStatus, requestDockerProjectActionJobs, type DockerProjectActionJobSnapshot } from '@/api/dockerProjectActionJob'
import { requestDockerContainerActionJob } from '@/api/dockerContainerActionJob'
import { useDockerProjectActionJobs } from './useDockerProjectActionJobs'
import { useDockerContainerCreateJobs } from './useDockerContainerCreateJobs'
import { useDockerContainerActionJobs } from './useDockerContainerActionJobs'
import { useDockerProjectDeletionProtection } from './useDockerProjectDeletionProtection'
import { captureDockerMutationFence } from './dockerMutationFence'
vi.mock('@/api/dockerProjectActionJob', () => ({ requestDockerProjectActionJob: vi.fn(), requestDockerProjectActionJobStatus: vi.fn(), requestDockerProjectActionJobs: vi.fn(), isDockerProjectActionAdmissionRejected: (error: unknown) => error === 'rejected' }))
vi.mock('@/api/dockerContainerActionJob', () => ({ requestDockerContainerActionJob: vi.fn(), requestDockerContainerActionJobStatus: vi.fn(), isDockerContainerActionAdmissionRejected: (error: unknown) => error === 'rejected' }))
const id = 'a'.repeat(64), scopes: EffectScope[] = []
function mount() {
  const scope = effectScope(); scopes.push(scope)
  return { scope, ...scope.run(() => ({ project: useDockerProjectActionJobs(), create: useDockerContainerCreateJobs(), container: useDockerContainerActionJobs(), deletion: useDockerProjectDeletionProtection(), fence: captureDockerMutationFence() }))! }
}
function job(changes: Partial<DockerProjectActionJobSnapshot> = {}): DockerProjectActionJobSnapshot {
  return { id: 'job-1', type: 'docker-project-start', reference: 'standalone', status: 'queued', artifactState: 'unknown', cancellable: false, message: '', progress: 0, createdAt: '2026-10-16T00:00:00Z', updatedAt: '2026-10-16T00:00:00Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...changes }
}
beforeEach(() => { beginSession(); vi.resetAllMocks(); vi.mocked(requestDockerProjectActionJob).mockResolvedValue(job()); vi.mocked(requestDockerProjectActionJobs).mockResolvedValue({ jobs: [], invalidCount: 0 }) })
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); invalidateSession() })
describe('Docker 注册表共享 fence', () => {
  it('standalone 同步确认阻止跨 scope create/container/delete/compose，取消恢复', () => {
    const a = mount(), b = mount(), entry = a.project.reserve([id], 'start')!
    expect(b.create.blocked.value).toBe(true)
    expect(b.create.reserve(true)).toBeNull()
    expect(b.container.reserve(id, 'stop', 'demo')).toBeNull()
    expect(b.deletion.reserve({ id: 'project', name: 'demo', kind: 'compose' })).toBeNull()
    expect(b.fence.reserve('compose', Symbol())).toBe(false)
    b.project.cancel(entry); expect(a.project.entry.value).not.toBeNull()
    a.project.cancel(entry); expect(b.create.blocked.value).toBe(false)
    expect(b.create.reserve(true)).not.toBeNull()
  })
  it('非项目共享准入保持并发，每个确认独立释放后才允许 standalone', () => {
    const a = mount(), b = mount(), compose = Symbol()
    const create = a.create.reserve(true)!, container = a.container.reserve(id, 'start', 'demo')!, deletion = b.deletion.reserve({ id: 'project', name: 'demo', kind: 'compose' })!
    expect(create).not.toBeNull(); expect(container).not.toBeNull(); expect(deletion).not.toBeNull(); expect(b.fence.reserve('compose', compose)).toBe(true)
    expect(a.project.reserve([id], 'start')).toBeNull()
    a.create.cancel(create); a.container.cancel(container); b.deletion.cancel(deletion)
    expect(a.project.reserve([id], 'start')).toBeNull()
    b.fence.release('compose', compose); expect(a.project.reserve([id], 'start')).not.toBeNull()
  })
  it('容器确认仅所属实例可取消/派发，不释放其他 scope fence', async () => {
    const a = mount(), b = mount(), entry = a.container.reserve(id, 'start', 'demo')!
    b.container.cancel(entry); await b.container.dispatch(entry); b.scope.stop()
    expect(requestDockerContainerActionJob).not.toHaveBeenCalled(); expect(a.project.reserve([id], 'start')).toBeNull()
    a.scope.stop(); expect(mount().project.reserve([id], 'start')).not.toBeNull()
  })
  it.each(['accepted', 'unknown'] as const)('standalone %s 派发后卸载保持跨操作保护', async mode => {
    const a = mount(), entry = a.project.reserve([id], 'start')!
    if (mode === 'unknown') vi.mocked(requestDockerProjectActionJob).mockRejectedValueOnce(new Error('unknown'))
    await a.project.dispatch(entry); a.scope.stop(); const b = mount()
    expect(b.create.blocked.value).toBe(true); expect(b.container.reserve(id, 'stop', 'demo')).toBeNull()
    expect(b.project.entry.value?.phase).toBe(mode === 'accepted' ? 'known' : 'unknown')
  })
  it('明确拒绝释放全部保护', async () => {
    const a = mount(), entry = a.project.reserve([id], 'start')!
    vi.mocked(requestDockerProjectActionJob).mockRejectedValueOnce('rejected'); await a.project.dispatch(entry)
    expect(a.create.blocked.value).toBe(false); expect(a.create.reserve(true)).not.toBeNull()
  })
  it('完成仅消费一次后释放，即便容器清单失败', async () => {
    const a = mount(), entry = a.project.reserve([id], 'start')!; await a.project.dispatch(entry)
    vi.mocked(requestDockerProjectActionJobStatus).mockResolvedValue(job({ status: 'completed', projectActionResult: { projectId: 'standalone', kind: 'standalone', action: 'start', items: [{ containerId: id, outcome: 'succeeded', state: 'running' }] } }))
    await a.project.check(entry, async () => false); expect(a.create.blocked.value).toBe(true)
    expect(a.project.consumeCompletion(entry, 1)).not.toBeNull(); expect(a.project.consumeCompletion(entry, 1)).toBeNull(); expect(a.create.blocked.value).toBe(false)
  })
  it('未知仅当前版本明确风险确认释放，旧会话不会挡住新会话', async () => {
    const a = mount(), entry = a.project.reserve([id], 'start')!
    vi.mocked(requestDockerProjectActionJob).mockRejectedValueOnce(new Error('unknown')); await a.project.dispatch(entry); await a.project.check(entry, async () => true)
    expect(a.project.release(entry, 0, true)).toBe(false); expect(a.project.release(entry, 1, false)).toBe(false); expect(a.create.blocked.value).toBe(true)
    expect(a.project.release(entry, 1, true)).toBe(true); expect(a.create.blocked.value).toBe(false)
    a.project.reserve([id], 'start'); beginSession(); const b = mount(); expect(b.create.blocked.value).toBe(false); expect(b.create.reserve(true)).not.toBeNull()
  })
})
