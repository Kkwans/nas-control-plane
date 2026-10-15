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
