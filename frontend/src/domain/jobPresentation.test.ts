import { describe, expect, it } from 'vitest'
import type { JobSnapshot } from '@/api/system'
import { canRequestJobCancellation, isTerminalJob, jobArtifactLabel, jobState, jobStateDescription, jobStateLabel, jobTypeLabel, mergeJobSnapshot, sortJobs } from './jobPresentation'

const job = (fields: Partial<JobSnapshot> = {}): JobSnapshot => ({ id: 'a', type: 'docker-image-pull', status: 'running', artifactState: 'unknown', cancellable: true, message: '', progress: 0, createdAt: '2026-10-13T10:00:00Z', updatedAt: '2026-10-13T10:00:00Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...fields })
describe('通用任务状态与能力', () => {
  it('删除任务区分接收与完成且不支持取消', () => {
    expect(jobTypeLabel('docker-image-remove')).toBe('镜像删除')
    expect(jobStateDescription(job({ type: 'docker-image-remove', status: 'queued' }))).toContain('尚未确认删除完成')
    expect(jobStateDescription(job({ type: 'docker-image-remove', status: 'completed' }))).toContain('任务已完成')
    expect(canRequestJobCancellation(job({ type: 'docker-image-remove', cancellable: true }))).toBe(false)
  })
  it.each([['queued', '排队中'], ['running', '执行中'], ['completed', '已完成'], ['failed', '失败'], ['cancelled', '已取消'], ['interrupted', '结果未确认']] as const)('明确展示 %s', (status, label) => { expect(jobStateLabel(job({ status }))).toBe(label) })
  it('保存、取消请求、未知结果优先；产物状态不改写任务状态', () => {
    expect(jobState(job({ persistenceStatus: 'pending', cancellationStatus: 'unknown' }))).toBe('persisting')
    expect(jobState(job({ cancellationStatus: 'requested' }))).toBe('cancelling')
    expect(jobState(job({ cancellationStatus: 'unknown' }))).toBe('interrupted')
    for (const artifactState of ['unknown', 'deleted', 'present'] as const) expect(jobState(job({ status: 'completed', artifactState }))).toBe('completed')
    expect(jobArtifactLabel(job())).toBe('未核实')
    expect(jobStateDescription(job({ type: 'compose-deploy', status: 'completed' }))).toContain('仍需核验')
    expect(jobTypeLabel('future-job')).toBe('future-job')
  })
  it('取消必须是明确支持的活动镜像任务', () => {
    expect(canRequestJobCancellation(job())).toBe(true)
    for (const fields of [{ type: 'compose-deploy' }, { type: 'future-job' }, { cancellable: undefined }, { cancellable: false }, { persistenceStatus: 'pending' }, { cancellationStatus: 'requested' }, { cancellationStatus: 'unknown' }, { status: 'completed' }] as Partial<JobSnapshot>[]) expect(canRequestJobCancellation(job(fields))).toBe(false)
  })
  it('保持同秒级终态、请求标记和运行状态，不用产物未知推断删除', () => {
    const completed = job({ status: 'completed' })
    expect(mergeJobSnapshot(completed, job())).toBe(completed)
    expect(mergeJobSnapshot(completed, job({ status: 'cancelled' }))).toBe(completed)
    const requested = job({ cancellationStatus: 'requested' })
    expect(mergeJobSnapshot(requested, job())).toBe(requested)
    expect(mergeJobSnapshot(requested, completed)).toBe(completed)
    const pending = job({ status: 'completed', persistenceStatus: 'pending' })
    expect(isTerminalJob(pending)).toBe(false)
    expect(mergeJobSnapshot(pending, completed)).toBe(completed)
    expect(mergeJobSnapshot(job(), job({ status: 'queued' })).status).toBe('running')
  })
  it('创建时间倒序，ID 稳定次排序且不修改输入', () => {
    const items = [job({ id: 'b' }), job({ id: 'c', createdAt: '2026-10-14' }), job({ id: 'a' })]
    expect(sortJobs(items).map((item) => item.id)).toEqual(['c', 'a', 'b'])
    expect(items[0]!.id).toBe('b')
  })
})


describe('进度受限证据单调合并', () => {
  it.each([undefined, false])('较新快照中的 %s 不能清除已记录受限状态', (progressLimited) => {
    const current = job({ progressLimited: true })
    for (const status of ['running', 'completed', 'failed', 'interrupted', 'cancelled'] as const) {
      const incoming = job({ status, progressLimited, updatedAt: '2026-10-13T10:00:01Z' })
      expect(mergeJobSnapshot(current, incoming)).toMatchObject({ status, progressLimited: true })
      expect(incoming.progressLimited).toBe(progressLimited)
    }
  })
  it('迟到受限证据不会倒退终态、取消和待保存标记', () => {
    for (const fields of [{ status: 'completed' }, { persistenceStatus: 'pending' }, { cancellationStatus: 'requested' }] as Partial<JobSnapshot>[]) {
      const current = job(fields)
      const merged = mergeJobSnapshot(current, job({ progressLimited: true }))
      expect(merged).toMatchObject({ ...fields, progressLimited: true })
      expect(current.progressLimited).toBeUndefined()
    }
    expect(mergeJobSnapshot(job({ updatedAt: '2026-10-13T10:00:01Z' }), job({ progressLimited: true }))).toMatchObject({ updatedAt: '2026-10-13T10:00:01Z', progressLimited: true })
  })
})

it('排队超时在保存期间保持活动，持久化后为失败', () => {
  const pending = job({ status: 'queued', persistenceStatus: 'pending', message: '任务未开始，结果等待保存' })
  expect(jobStateDescription(pending)).toBe('任务结果正在保存，尚未确认最终结果。')
  expect(isTerminalJob(pending)).toBe(false)
  expect(canRequestJobCancellation(pending)).toBe(false)
  const timeout = job({ status: 'failed', errorCode: 'JOB_QUEUE_TIMEOUT', message: '排队等待超时，任务未开始执行' })
  expect(mergeJobSnapshot(pending, timeout)).toBe(timeout)
  expect(jobStateLabel(timeout)).toBe('失败')
  expect(isTerminalJob(timeout)).toBe(true)
})

describe('单容器操作任务展示', () => {
  it.each([
    ['docker-container-start', '容器启动'],
    ['docker-container-stop', '容器停止'],
    ['docker-container-restart', '容器重启'],
  ])('%s 的名称、结果边界及不可取消能力', (type, label) => {
    expect(jobTypeLabel(type)).toBe(label)
    for (const status of ['queued', 'running'] as const) {
      expect(jobStateDescription(job({ type, status }))).toBe('容器操作任务已接收，尚未确认完成。')
      expect(canRequestJobCancellation(job({ type, status, cancellable: true }))).toBe(false)
    }
    expect(jobStateDescription(job({ type, status: 'completed' }))).toContain('本次操作已返回且读回状态符合目标')
    expect(jobStateDescription(job({ type, status: 'completed' }))).toContain('不代表健康或持续状态')
    expect(jobStateDescription(job({ type, status: 'failed' }))).toContain('操作可能已执行')
    expect(jobStateDescription(job({ type, status: 'failed' }))).toContain('勿重复提交')
    expect(jobStateDescription(job({ type, status: 'interrupted' }))).toContain('执行结果未确认')
    const pending = job({ type, status: 'completed', persistenceStatus: 'pending' })
    expect(jobStateDescription(pending)).toBe('任务结果正在保存，尚未确认最终结果。')
    expect(isTerminalJob(pending)).toBe(false)
    expect(canRequestJobCancellation(pending)).toBe(false)
  })
})

describe('容器创建任务展示', () => {
  const type = 'docker-container-create'
  it('接收不代表完成，完成记录仍需核对详情回执与当前状态', () => {
    expect(jobTypeLabel(type)).toBe('容器创建')
    for (const status of ['queued', 'running'] as const) {
      expect(jobStateDescription(job({ type, status }))).toBe('容器创建任务已接收，尚未确认创建完成。')
    }
    const description = jobStateDescription(job({ type, status: 'completed' }))
    expect(description).toContain('任务记录标记为已完成')
    expect(description).toContain('请核对任务详情中的创建回执与容器当前状态')
    expect(description).not.toContain('回执已持久保存')
    expect(description).toContain('不保证持续运行')
  })
  it.each(['failed', 'interrupted'] as const)('%s 不承诺无副作用，也不建议重放', status => {
    const description = jobStateDescription(job({ type, status }))
    expect(description).toContain('创建结果未确认')
    expect(description).toContain('可能已产生副作用或残留容器')
    expect(description).toContain('只读核对实际状态，勿重复提交')
  })
  it.each(['queued', 'running', 'completed', 'failed', 'interrupted'] as const)('%s 的 pending 仍未确认且不支持取消', status => {
    const pending = job({ type, status, persistenceStatus: 'pending', cancellable: true })
    expect(jobState(pending)).toBe('persisting')
    expect(jobStateDescription(pending)).toBe('任务结果正在保存，尚未确认最终结果。')
    expect(isTerminalJob(pending)).toBe(false)
    expect(canRequestJobCancellation(pending)).toBe(false)
    expect(canRequestJobCancellation(job({ type, status, cancellable: true }))).toBe(false)
  })
})


describe('独立容器项目操作任务展示', () => {
  it.each([
    ['docker-project-start', '项目启动'], ['docker-project-stop', '项目停止'], ['docker-project-restart', '项目重启'],
  ])('%s 仅描述记录，保留不确定性与保存优先级', (type, label) => {
    expect(jobTypeLabel(type)).toBe(label)
    for (const status of ['queued', 'running'] as const) expect(jobStateDescription(job({ type, status }))).toContain('尚未确认完成')
    const completed = jobStateDescription(job({ type, status: 'completed' }))
    expect(completed).toContain('任务记录标记为已完成')
    expect(completed).toContain('逐容器回执与当前状态')
    expect(completed).toContain('不代表所有容器当前健康或持续运行')
    expect(completed).not.toContain('读回状态符合目标')
    expect(jobStateDescription(job({ type, status: 'failed', errorCode: 'DOCKER_PROJECT_STATE_MISMATCH' }))).toContain('操作可能已执行')
    expect(jobStateDescription(job({ type, status: 'failed' }))).toContain('勿重复提交')
    expect(jobStateDescription(job({ type, status: 'interrupted' }))).toContain('项目执行结果未确认')
    expect(jobStateDescription(job({ type, status: 'completed', cancellationStatus: 'unknown' }))).toContain('项目执行结果未确认')
    for (const status of ['queued', 'running', 'completed', 'failed', 'interrupted'] as const) {
      const pending = job({ type, status, persistenceStatus: 'pending', cancellable: true })
      expect(jobStateDescription(pending)).toBe('任务结果正在保存，尚未确认最终结果。')
      expect(isTerminalJob(pending)).toBe(false)
      expect(canRequestJobCancellation(pending)).toBe(false)
      expect(canRequestJobCancellation(job({ type, status, cancellable: true }))).toBe(false)
    }
  })
})
