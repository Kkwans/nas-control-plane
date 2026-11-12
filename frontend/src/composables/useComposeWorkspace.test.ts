/// <reference types="node" />
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createHash } from 'node:crypto'
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
vi.mock('@/api/docker', async (importOriginal) => ({ ...await importOriginal<typeof import('@/api/docker')>(), subscribeJob: api.subscribe }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const flush = async () => { for (let i = 0; i < 15; i++) await Promise.resolve() }
const project = (id: string): DockerProject => ({ id, name: id, kind: 'compose', state: 'running', workingDirectory: `/${id}`, configFiles: [`/${id}/compose.yml`], containerCount: 1, runningCount: 1 })
const hash = (content: string) => createHash('sha256').update(content, 'utf8').digest('hex')
const config = (id: string, content = 'original'): ComposeProjectConfig => ({ projectId: id, workingDirectory: `/${id}`, collectedAt: '', files: [{ path: `/${id}/compose.yml`, name: 'compose.yml', content, contentHash: hash(content), size: Buffer.byteLength(content, 'utf8') }] })
const historical = () => ({ id: 1, projectId: 'a', configPath: '/a/compose.yml', content: 'historical', contentHash: hash('historical'), backupPath: '', createdAt: '2026-10-12' })
const scopes: ReturnType<typeof effectScope>[] = []
function setup(initialProject = project('a')) {
  const open = ref(true)
  const target = ref<DockerProject | null>(initialProject)
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
afterEach(() => { scopes.splice(0).forEach((scope) => scope.stop()); vi.unstubAllGlobals() })

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
    const value = config('a'); value.files.push({ path: '/a/other.yml', name: 'other.yml', content: 'original', contentHash: hash('original'), size: 8 })
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
    api.read.mockResolvedValueOnce(config('a', 'A'))
    confirmation.resolve(true); await first
    expect(feedback.confirm).toHaveBeenCalledTimes(1)
    expect(api.deploy).toHaveBeenCalledTimes(1)
    expect(api.deploy).toHaveBeenCalledWith({ projectId: 'a', workingDirectory: '/a', configFiles: ['/a/compose.yml'], targetPath: '/a/compose.yml', content: 'A', baseContentHash: hash('original') })
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
    const revision = historical()
    api.revisions.mockResolvedValue([revision])
    const { state, feedback, open } = setup(); await flush()
    const confirmation = deferred<boolean>(); feedback.confirm.mockReturnValueOnce(confirmation.promise)
    const rollback = state.rollback(revision)
    revision.content = 'mutated'
    api.read.mockResolvedValueOnce(config('a', 'historical'))
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

describe('Compose 磁盘基线与完成后窄复读', () => {
  it('加载和保存草稿均不替换磁盘基线，文件对象 hash 也不是发布基线来源', async () => {
    api.draft.mockResolvedValue({ content: 'draft D', contentHash: hash('draft D'), updatedAt: 'draft-time' })
    api.save.mockResolvedValue({ content: 'draft E', contentHash: hash('draft E'), updatedAt: 'saved-time' })
    const { state } = setup(); await flush()
    expect(state.activeContent.value).toBe('draft D')
    expect(state.dirty.value).toBe(false)
    state.activeContent.value = 'draft E'
    state.files.value[0]!.contentHash = hash('draft E')
    await state.saveDraft()
    api.read.mockResolvedValueOnce(config('a', 'draft E'))
    await state.deploy()
    expect(api.deploy).toHaveBeenCalledWith(expect.objectContaining({ content: 'draft E', baseContentHash: hash('original') }))
    expect(state.dirty.value).toBe(false)
    expect(state.draftUpdatedAt.value['/a/compose.yml']).toBe('saved-time')
  })

  it.each(['rollback', 'loadRevision'])('历史版本 %s 使用当前磁盘基线，历史 hash 不授权覆盖', async (mode) => {
    const revision = historical()
    api.revisions.mockResolvedValue([revision])
    const { state } = setup(); await flush()
    api.read.mockResolvedValueOnce(config('a', 'historical'))
    if (mode === 'rollback') await state.rollback(revision)
    else { state.loadRevision(revision); await state.deploy() }
    expect(api.deploy).toHaveBeenCalledWith(expect.objectContaining({ content: 'historical', baseContentHash: hash('original') }))
    expect(state.activeContent.value).toBe(mode === 'rollback' ? 'original' : 'historical')
    state.activeContent.value = 'next'
    api.read.mockResolvedValueOnce(config('a', 'next'))
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ content: 'next', baseContentHash: hash('historical') }))
    expect(state.dirty.value).toBe(true)
  })

  it.each([undefined, '', 'bad', 'A'.repeat(64), `${hash('original')}\n`])('冻结时拒绝缺失或非法磁盘基线 %j，不校验、不确认、不提交', async (invalid) => {
    const result = config('a'); Object.assign(result.files[0]!, { contentHash: invalid })
    api.read.mockResolvedValueOnce(result)
    const { state, feedback } = setup(); await flush()
    state.activeContent.value = 'keep this draft'
    await state.deploy()
    expect(api.validate).not.toHaveBeenCalled()
    expect(feedback.confirm).not.toHaveBeenCalled()
    expect(api.deploy).not.toHaveBeenCalled()
    expect(state.activeContent.value).toBe('keep this draft')
    expect(state.error.value).toContain('重新读取')
  })

  it('确认冻结 A/hash(O)，复读 A 后保留新编辑 B 和草稿保存基线，下一次 B 使用 hash(A)', async () => {
    api.draft.mockResolvedValue({ content: 'saved draft', updatedAt: 'saved-time' })
    const { state, feedback } = setup(); await flush()
    const confirmation = deferred<boolean>()
    const reread = deferred<ComposeProjectConfig>()
    feedback.confirm.mockReturnValueOnce(confirmation.promise)
    api.read.mockReturnValueOnce(reread.promise)
    state.activeContent.value = 'A'
    const first = state.deploy(); await flush()
    state.activeContent.value = 'B'
    confirmation.resolve(true); await flush()
    expect(api.deploy).toHaveBeenCalledWith(expect.objectContaining({ content: 'A', baseContentHash: hash('original') }))
    expect(api.read).toHaveBeenLastCalledWith({ id: 'a', workingDirectory: '/a', configFiles: ['/a/compose.yml'] })
    expect(state.deploying.value).toBe(true)
    await state.deploy()
    expect(api.deploy).toHaveBeenCalledTimes(1)
    expect(feedback.confirm).toHaveBeenCalledTimes(1)
    reread.resolve(config('a', 'A')); await first
    expect(state.deploying.value).toBe(false)
    expect(state.activeContent.value).toBe('B')
    expect(state.dirty.value).toBe(true)
    expect(state.draftUpdatedAt.value['/a/compose.yml']).toBe('saved-time')
    expect(state.changedLines.value).toBe(1)
    state.activeContent.value = 'saved draft'
    expect(state.dirty.value).toBe(false)
    state.activeContent.value = 'B'
    api.read.mockResolvedValueOnce(config('a', 'B'))
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ content: 'B', baseContentHash: hash('A') }))
    expect(state.dirty.value).toBe(true)
  })

  it('当前编辑器仍为 A 时也推进磁盘基线，但不会替用户保存草稿', async () => {
    const { state } = setup(); await flush()
    state.activeContent.value = 'A'
    api.read.mockResolvedValueOnce(config('a', 'A'))
    await state.deploy()
    expect(state.changedLines.value).toBe(0)
    expect(state.dirty.value).toBe(true)
    expect(state.dirtyCount.value).toBe(1)
    expect(state.draftUpdatedAt.value['/a/compose.yml']).toBe('2026-10-13')
    state.activeContent.value = 'original'
    expect(state.dirty.value).toBe(false)
    expect(state.changedLines.value).toBe(1)
    api.read.mockResolvedValueOnce(config('a', 'original'))
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ baseContentHash: hash('A') }))
  })

  it.each([
    ['failed', 'COMPOSE_CONFIG_CONFLICT'], ['failed', 'COMPOSE_CONFIG_UNVERIFIED'],
    ['failed', 'COMPOSE_DEPLOY_POSTCHECK_CONFLICT'], ['failed', 'COMPOSE_DEPLOY_POSTCHECK_UNVERIFIED'],
    ['failed', 'COMPOSE_BASELINE_INVALID'], ['failed', 'COMPOSE_BASELINE_UNSUPPORTED'],
    ['cancelled', 'JOB_CANCELLED'], ['interrupted', 'AGENT_RPC_TIMEOUT'],
  ])('终态 %s/%s 不复读、不推进基线或报告完成', async (status, errorCode) => {
    const { state, feedback } = setup(); await flush()
    state.activeContent.value = 'A'
    api.subscribe.mockReturnValue({ close: vi.fn(), done: Promise.resolve({ status, errorCode, error: `safe ${errorCode}` }) })
    await state.deploy()
    expect(state.error.value).toBe(`safe ${errorCode}`)
    expect(api.read).toHaveBeenCalledTimes(1)
    expect(api.revisions).toHaveBeenCalledTimes(1)
    expect(feedback.success).not.toHaveBeenCalledWith('done')
    expect(state.activeContent.value).toBe('A')
    expect(state.changedLines.value).toBe(1)
    expect(state.dirty.value).toBe(true)
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ baseContentHash: hash('original') }))
  })

  it.each(['history succeeds', 'history fails'])('任务完成后外部变更只提示核对，%s 不遮住警告或重写文本', async (historyOutcome) => {
    const { state, feedback } = setup(); await flush()
    state.activeContent.value = 'A'
    api.read.mockResolvedValueOnce(config('a', 'external E'))
    if (historyOutcome === 'history fails') api.revisions.mockRejectedValueOnce(new Error('unavailable'))
    else api.revisions.mockResolvedValueOnce([historical()])
    await state.deploy()
    expect(state.error.value).toContain('任务已完成，但目标配置已变化')
    expect(state.error.value).not.toContain('部署失败')
    expect(state.error.value).not.toContain('版本记录')
    expect(feedback.success).not.toHaveBeenCalledWith('done')
    expect(state.activeContent.value).toBe('A')
    expect(state.dirty.value).toBe(true)
    expect(state.changedLines.value).toBe(1)
    if (historyOutcome === 'history succeeds') expect(state.revisions.value).toEqual([historical()])
    api.subscribe.mockReturnValue({ close: vi.fn(), done: Promise.resolve({ status: 'failed', errorCode: 'COMPOSE_CONFIG_CONFLICT' }) })
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ baseContentHash: hash('original') }))
  })

  it.each(['rejected read', 'missing target', 'duplicate target', 'wrong path', 'wrong project', 'wrong directory', 'missing hash', 'invalid hash'])('完成后的 %s 不推进基线，刷新历史失败不改称部署失败', async (failure) => {
    const { state, feedback } = setup(); await flush()
    state.activeContent.value = 'A'
    const result = config('a', 'A')
    switch (failure) {
      case 'missing target': result.files = []; break
      case 'duplicate target': result.files.push({ ...result.files[0]! }); break
      case 'wrong path': result.files[0]!.path = '/a/other.yml'; break
      case 'wrong project': result.projectId = 'b'; break
      case 'wrong directory': result.workingDirectory = '/elsewhere'; break
      case 'missing hash': Reflect.deleteProperty(result.files[0]!, 'contentHash'); break
      case 'invalid hash': result.files[0]!.contentHash = 'NOT-A-HASH'; break
    }
    if (failure === 'rejected read') api.read.mockRejectedValueOnce(new Error('unavailable'))
    else api.read.mockResolvedValueOnce(result)
    api.revisions.mockRejectedValueOnce(new Error('history unavailable'))
    await state.deploy()
    expect(state.error.value).toContain('任务已完成，但无法重新核实目标配置')
    expect(state.error.value).not.toContain('部署失败')
    expect(state.error.value).not.toContain('版本记录')
    expect(feedback.success).not.toHaveBeenCalledWith('done')
    expect(state.activeContent.value).toBe('A')
    expect(state.dirty.value).toBe(true)
    expect(state.changedLines.value).toBe(1)
    api.subscribe.mockReturnValue({ close: vi.fn(), done: Promise.resolve({ status: 'failed', errorCode: 'COMPOSE_CONFIG_CONFLICT' }) })
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ baseContentHash: hash('original') }))
  })

  it('核对成功但版本记录刷新失败，仍保留已推进基线与完成事实', async () => {
    const { state, feedback } = setup(); await flush()
    state.activeContent.value = 'A'
    api.read.mockResolvedValueOnce(config('a', 'A'))
    api.revisions.mockRejectedValueOnce(new Error('unavailable'))
    await state.deploy()
    expect(state.error.value).toContain('任务已完成，但版本记录刷新失败')
    expect(feedback.success).toHaveBeenCalledWith('done')
    expect(state.changedLines.value).toBe(0)
    api.read.mockResolvedValueOnce(config('a', 'A'))
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ baseContentHash: hash('A') }))
  })

  it('复读期间切活动文件并编辑，只推进冻结目标且不覆盖其他文件', async () => {
    const initial = config('a')
    initial.files.push({ path: '/a/other.yml', name: 'other.yml', content: 'original other', contentHash: hash('original other'), size: 14 })
    api.read.mockResolvedValueOnce(initial)
    api.draft.mockImplementation((_projectId: string, path: string) => Promise.resolve({ content: path.endsWith('other.yml') ? 'original other' : 'original', updatedAt: 'saved-time' }))
    const { state } = setup({ ...project('a'), configFiles: ['/a/compose.yml', '/a/other.yml'] }); await flush()
    const reread = deferred<ComposeProjectConfig>(); api.read.mockReturnValueOnce(reread.promise)
    state.activeContent.value = 'A'; const first = state.deploy(); await flush()
    state.activePath.value = '/a/other.yml'; state.activeContent.value = 'B'
    const result = config('a', 'A')
    result.files.push({ ...initial.files[1]!, content: 'external other', contentHash: hash('external other') })
    reread.resolve(result); await first
    expect(state.activePath.value).toBe('/a/other.yml')
    expect(state.activeContent.value).toBe('B')
    expect(state.files.value[0]!.content).toBe('A')
    expect(state.dirtyCount.value).toBe(2)
    const otherResult = config('a', 'B'); otherResult.files[0]!.path = '/a/other.yml'
    api.read.mockResolvedValueOnce(otherResult)
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ targetPath: '/a/other.yml', content: 'B', baseContentHash: hash('original other') }))
    expect(api.read).toHaveBeenLastCalledWith({ id: 'a', workingDirectory: '/a', configFiles: ['/a/other.yml'] })
    state.activePath.value = '/a/compose.yml'
    api.read.mockResolvedValueOnce(config('a', 'A'))
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ targetPath: '/a/compose.yml', baseContentHash: hash('A') }))
  })

  it.each(['project switch', 'close and reopen'])('复读期间 %s，迟到响应和 finally 不影响新工作区部署', async (change) => {
    const { state, target, open, feedback } = setup(); await flush()
    const oldRead = deferred<ComposeProjectConfig>(); api.read.mockReturnValueOnce(oldRead.promise)
    state.activeContent.value = 'old A'; const oldDeployment = state.deploy(); await flush()
    expect(state.deploying.value).toBe(true)
    const newId = change === 'project switch' ? 'b' : 'a'
    if (change === 'project switch') target.value = project('b')
    else { open.value = false; open.value = true }
    await flush()
    const newRead = deferred<ComposeProjectConfig>(); api.read.mockReturnValueOnce(newRead.promise)
    state.activeContent.value = 'new B'; const newDeployment = state.deploy(); await flush()
    feedback.success.mockClear()
    oldRead.resolve(config('a', 'old A')); await oldDeployment
    expect(state.deploying.value).toBe(true)
    expect(state.activeContent.value).toBe('new B')
    expect(state.error.value).toBeNull()
    expect(feedback.success).not.toHaveBeenCalled()
    expect(api.revisions).toHaveBeenCalledTimes(2)
    newRead.resolve(config(newId, 'new B')); await newDeployment
    expect(state.deploying.value).toBe(false)
    expect(state.changedLines.value).toBe(0)
    expect(state.dirty.value).toBe(true)
    api.read.mockResolvedValueOnce(config(newId, 'new B'))
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ projectId: newId, baseContentHash: hash('new B') }))
  })

  it.each(['logout', 'unmount', 'close'])('复读期间 %s，迟到成功不能回写或刷新历史', async (action) => {
    const { state, scope, open, feedback } = setup(); await flush()
    const reread = deferred<ComposeProjectConfig>(); api.read.mockReturnValueOnce(reread.promise)
    state.activeContent.value = 'A'; const deployment = state.deploy(); await flush()
    feedback.success.mockClear()
    if (action === 'logout') invalidateSession()
    else if (action === 'unmount') scope.stop()
    else open.value = false
    reread.resolve(config('a', 'A')); await deployment
    expect(state.files.value).toEqual([])
    expect(state.error.value).toBeNull()
    expect(state.deploying.value).toBe(false)
    expect(api.revisions).toHaveBeenCalledTimes(1)
    expect(feedback.success).not.toHaveBeenCalled()
  })

  it('复读 pending 期间继续编辑不影响成功推进，全流程不依赖 global crypto', async () => {
    vi.stubGlobal('crypto', undefined)
    const { state } = setup(); await flush()
    const reread = deferred<ComposeProjectConfig>(); api.read.mockReturnValueOnce(reread.promise)
    const raw = '\uFEFFservices:\r\n  web: # 中文 🐳  \r\n'
    state.activeContent.value = raw
    const deployment = state.deploy(); await flush()
    state.activeContent.value = 'newer'
    reread.resolve(config('a', raw)); await deployment
    expect(state.activeContent.value).toBe('newer')
    expect(state.dirty.value).toBe(true)
    api.read.mockResolvedValueOnce(config('a', 'newer'))
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ content: 'newer', baseContentHash: hash(raw) }))
  })

  it('语义相同但字节不同的复读内容也不能推进基线', async () => {
    const { state } = setup(); await flush()
    state.activeContent.value = 'services:\n  web: {}\n'
    api.read.mockResolvedValueOnce(config('a', 'services:\r\n  web: {}\r\n'))
    await state.deploy()
    expect(state.error.value).toContain('目标配置已变化')
    expect(state.activeContent.value).toBe('services:\n  web: {}\n')
    api.subscribe.mockReturnValue({ close: vi.fn(), done: Promise.resolve({ status: 'failed' }) })
    await state.deploy()
    expect(api.deploy).toHaveBeenLastCalledWith(expect.objectContaining({ baseContentHash: hash('original') }))
  })
})
