import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, ref } from 'vue'
import type { ComposeProjectConfig, DockerProject, JobSnapshot } from '@/api/system'
import { invalidateSession } from '@/session/sessionLifecycle'
import { useComposeWorkspace } from './useComposeWorkspace'

const api = vi.hoisted(() => ({ read: vi.fn(), draft: vi.fn(), save: vi.fn(), revisions: vi.fn(), validate: vi.fn(), deploy: vi.fn(), subscribe: vi.fn() }))
vi.mock('@/api/control', () => ({ requestComposeDraft: api.draft, saveComposeDraft: api.save, requestComposeRevisions: api.revisions }))
vi.mock('@/api/system', () => ({
  readComposeConfig: api.read, validateComposeConfig: api.validate, deployComposeConfig: api.deploy,
  NcpApiError: class extends Error { constructor(public code: string, message = code) { super(message) } },
}))
vi.mock('@/api/docker', () => ({ subscribeJob: api.subscribe }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const flush = async () => { for (let i = 0; i < 15; i++) await Promise.resolve() }
const project = (id: string): DockerProject => ({ id, name: id, kind: 'compose', state: 'running', workingDirectory: `/${id}`, configFiles: [`/${id}/compose.yml`], containerCount: 1, runningCount: 1 })
const config = (id: string): ComposeProjectConfig => ({ projectId: id, workingDirectory: `/${id}`, collectedAt: '', files: [{ path: `/${id}/compose.yml`, name: 'compose.yml', content: 'original', size: 8 }] })
const scopes: ReturnType<typeof effectScope>[] = []
function setup() {
  const open = ref(true)
  const target = ref<DockerProject | null>(project('a'))
  const feedback = { success: vi.fn(), info: vi.fn(), confirm: vi.fn().mockResolvedValue(true) }
  const scope = effectScope()
  scopes.push(scope)
  const state = scope.run(() => useComposeWorkspace(open, target, feedback))!
  return { state, open, target, feedback, scope }
}
beforeEach(() => {
  vi.resetAllMocks()
  api.read.mockImplementation((p: DockerProject) => Promise.resolve(config(p.id)))
  api.draft.mockResolvedValue({ content: 'original', updatedAt: '2026-10-13' })
  api.revisions.mockResolvedValue([])
  api.validate.mockResolvedValue({ valid: true, services: ['web'], normalized: '' })
  api.deploy.mockResolvedValue({ id: 'job-a' })
  api.subscribe.mockReturnValue({ close: vi.fn(), done: Promise.resolve({ status: 'completed', message: 'done' }) })
})
afterEach(() => { scopes.splice(0).forEach((scope) => scope.stop()) })

describe('Compose 工作区事务隔离', () => {
  it('保存 A 期间编辑 B：仅 A 成为已保存基线，下一次提交 B', async () => {
    const { state } = setup(); await flush()
    const pending = deferred<{ updatedAt: string }>()
    api.save.mockReturnValueOnce(pending.promise).mockResolvedValue({ updatedAt: 'later' })
    state.activeContent.value = 'A'
    const saving = state.saveDraft()
    state.activeContent.value = 'B'
    pending.resolve({ updatedAt: 'now' }); await saving
    expect(state.dirty.value).toBe(true)
    expect(state.dirtyCount.value).toBe(1)
    expect(api.save).toHaveBeenNthCalledWith(1, { projectId: 'a', configPath: '/a/compose.yml', content: 'A' })
    await state.saveDraft()
    expect(api.save).toHaveBeenNthCalledWith(2, { projectId: 'a', configPath: '/a/compose.yml', content: 'B' })
    expect(state.dirty.value).toBe(false)
  })
  it('保存失败保留 dirty', async () => {
    const { state } = setup(); await flush()
    state.activeContent.value = 'edited'
    api.save.mockRejectedValue(new Error('network'))
    await state.saveDraft()
    expect(state.dirty.value).toBe(true)
    expect(state.error.value).toContain('保存失败')
  })
  it('A 慢 B 快：旧读取不能覆盖 B 或清除其 loading', async () => {
    const a = deferred<ComposeProjectConfig>(); const b = deferred<ComposeProjectConfig>()
    api.read.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const { state, target } = setup()
    target.value = project('b')
    a.resolve(config('a')); await flush()
    expect(state.loading.value).toBe(true)
    expect(state.files.value).toEqual([])
    b.resolve(config('b')); await flush()
    expect(state.activePath.value).toBe('/b/compose.yml')
    expect(api.draft).toHaveBeenCalledTimes(1)
    expect(api.draft).toHaveBeenCalledWith('b', '/b/compose.yml')
  })
  it('关闭重开同一项目，旧保存和 finally 不影响新保存', async () => {
    const { state, open } = setup(); await flush()
    const a = deferred<{ updatedAt: string }>(); const b = deferred<{ updatedAt: string }>()
    api.save.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    state.activeContent.value = 'A'; const first = state.saveDraft()
    open.value = false; open.value = true; await flush()
    state.activeContent.value = 'B'; const second = state.saveDraft()
    a.resolve({ updatedAt: 'old' }); await first
    expect(state.saving.value).toBe(true)
    expect(state.dirty.value).toBe(true)
    b.resolve({ updatedAt: 'new' }); await second
    expect(state.dirty.value).toBe(false)
  })
  it('编辑后迟到校验失效，即使文本恢复相同也不能复用旧修订', async () => {
    const { state, feedback } = setup(); await flush()
    const result = deferred<{ valid: boolean; services: string[] }>()
    api.validate.mockReturnValueOnce(result.promise)
    const validation = state.validate()
    state.activeContent.value = 'B'; state.activeContent.value = 'original'
    result.resolve({ valid: true, services: ['old'] })
    expect(await validation).toBe(false)
    expect(state.validation.value).toBeNull()
    expect(feedback.success).not.toHaveBeenCalled()
  })
  it('切文件令旧校验失效，全部文件未保存数保留', async () => {
    const value = config('a'); value.files.push({ path: '/a/other.yml', name: 'other.yml', content: 'original', size: 8 })
    api.read.mockResolvedValue(value)
    const { state } = setup(); await flush()
    const result = deferred<{ valid: boolean; services: string[] }>()
    api.validate.mockReturnValueOnce(result.promise)
    state.activeContent.value = 'edited'; const validation = state.validate()
    state.activePath.value = '/a/other.yml'
    result.resolve({ valid: true, services: ['old'] }); await validation
    expect(state.validation.value).toBeNull()
    expect(state.dirty.value).toBe(false)
    expect(state.dirtyCount.value).toBe(1)
  })
  it('校验期间编辑不得弹出部署确认', async () => {
    const { state, feedback } = setup(); await flush()
    const result = deferred<{ valid: boolean; services: string[] }>()
    api.validate.mockReturnValueOnce(result.promise)
    const deployment = state.deploy(); state.activeContent.value = 'B'
    result.resolve({ valid: true, services: [] }); await deployment
    expect(feedback.confirm).not.toHaveBeenCalled()
    expect(api.deploy).not.toHaveBeenCalled()
    expect(state.deploying.value).toBe(false)
  })
  it('确认使用冻结 payload，重复点击只有一次确认，新编辑不被完成响应覆盖', async () => {
    const { state, feedback } = setup(); await flush()
    const confirmation = deferred<boolean>()
    feedback.confirm.mockReturnValue(confirmation.promise)
    state.activeContent.value = 'A'
    const first = state.deploy(); await flush()
    state.activeContent.value = 'B'
    await state.deploy()
    confirmation.resolve(true); await first
    expect(feedback.confirm).toHaveBeenCalledTimes(1)
    expect(api.deploy).toHaveBeenCalledTimes(1)
    expect(api.deploy).toHaveBeenCalledWith({ projectId: 'a', workingDirectory: '/a', configFiles: ['/a/compose.yml'], targetPath: '/a/compose.yml', content: 'A' })
    expect(state.activeContent.value).toBe('B')
    expect(state.dirty.value).toBe(true)
    expect(state.changedLines.value).toBe(1)
  })
  it('确认期间切项目，旧确认不能提交到任何项目', async () => {
    const { state, feedback, target } = setup(); await flush()
    const confirmation = deferred<boolean>(); feedback.confirm.mockReturnValue(confirmation.promise)
    const deployment = state.deploy(); await flush()
    target.value = project('b'); await flush()
    confirmation.resolve(true); await deployment
    expect(api.deploy).not.toHaveBeenCalled()
    expect(state.activePath.value).toBe('/b/compose.yml')
  })
  it('关闭已提交任务只关闭观察，不误报取消或成功', async () => {
    const { state, feedback, open } = setup(); await flush()
    const job = deferred<JobSnapshot>()
    const close = vi.fn(() => job.reject(new DOMException('closed', 'AbortError')))
    api.subscribe.mockReturnValue({ close, done: job.promise })
    const deployment = state.deploy(); await flush()
    expect(api.deploy).toHaveBeenCalledTimes(1)
    feedback.success.mockClear()
    open.value = false; await deployment
    expect(close).toHaveBeenCalledTimes(1)
    expect(state.error.value).toBeNull()
    expect(feedback.success).not.toHaveBeenCalled()
  })
  it.each(['failed', 'cancelled', 'interrupted'])('后端终态 %s 不显示成功', async (status) => {
    const { state, feedback } = setup(); await flush()
    api.subscribe.mockReturnValue({ close: vi.fn(), done: Promise.resolve({ status, message: status }) })
    await state.deploy()
    expect(state.error.value).toBe(status)
    expect(feedback.success).not.toHaveBeenCalledWith(status)
  })
  it('部署确认说明恢复条件及运行状态复核，失败详情不会被通用文案遮住', async () => {
    const { state, feedback } = setup(); await flush()
    const detail = 'Compose 部署失败；检测到外部修改，未覆盖当前配置，容器状态未确认。'
    api.subscribe.mockReturnValue({ close: vi.fn(), done: Promise.resolve({ status: 'failed', errorCode: 'COMPOSE_DEPLOY_ROLLBACK_CONFLICT', message: detail, error: detail }) })
    await state.deploy()
    expect(feedback.confirm.mock.calls[0]?.[0]).toContain('发现外部修改或无法核实当前文件时会停止')
    expect(feedback.confirm.mock.calls[0]?.[0]).toContain('需核对配置和运行状态')
    expect(state.error.value).toBe(detail)
    expect(feedback.success).not.toHaveBeenCalledWith(detail)
  })
  it('回滚确认冻结版本内容，关闭后确认不得提交', async () => {
    const revision = { id: 1, projectId: 'a', configPath: '/a/compose.yml', content: 'historical', contentHash: 'hash', backupPath: '', createdAt: '2026-10-12' }
    api.revisions.mockResolvedValue([revision])
    const { state, feedback, open } = setup(); await flush()
    const confirmation = deferred<boolean>(); feedback.confirm.mockReturnValueOnce(confirmation.promise)
    const rollback = state.rollback(revision)
    revision.content = 'mutated'
    confirmation.resolve(true); await rollback
    expect(api.deploy.mock.calls[0]?.[0].content).toBe('historical')
    expect(feedback.confirm.mock.calls[0]?.[0]).toContain('发现外部修改或无法核实当前文件时会停止')
    const nextConfirmation = deferred<boolean>(); feedback.confirm.mockReturnValueOnce(nextConfirmation.promise)
    const next = state.rollback(revision)
    open.value = false; nextConfirmation.resolve(true); await next
    expect(api.deploy).toHaveBeenCalledTimes(1)
  })
  it('卸载后迟到提交响应不能新建 Job 观察或发出提示', async () => {
    const { state, feedback, scope } = setup(); await flush()
    const submission = deferred<{ id: string }>(); api.deploy.mockReturnValue(submission.promise)
    const deployment = state.deploy(); await flush()
    feedback.success.mockClear(); scope.stop()
    submission.resolve({ id: 'running-backend-job' }); await deployment
    expect(api.subscribe).not.toHaveBeenCalled()
    expect(feedback.success).not.toHaveBeenCalled()
    expect(state.error.value).toBeNull()
  })
  it('会话失效清空草稿，迟到结果不回写', async () => {
    const { state } = setup(); await flush()
    const save = deferred<{ updatedAt: string }>(); api.save.mockReturnValue(save.promise)
    state.activeContent.value = 'private'; const operation = state.saveDraft()
    invalidateSession(); save.resolve({ updatedAt: 'now' }); await operation
    expect(state.files.value).toEqual([])
    expect(state.error.value).toBeNull()
  })
})
