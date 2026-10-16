import type { JobSnapshot } from '@/api/system'

export const jobProgressLimitedWarning = '进度信息受资源限制，仅保留部分层或状态；数值不代表完整下载进度。'

export type JobState = JobSnapshot['status'] | 'persisting' | 'cancelling'
export const jobStatusOptions: { value: JobState | 'all'; label: string }[] = [
  { value: 'all', label: '全部状态' }, { value: 'queued', label: '排队中' },
  { value: 'running', label: '执行中' }, { value: 'persisting', label: '结果保存中' },
  { value: 'cancelling', label: '取消请求已接收' }, { value: 'completed', label: '已完成' },
  { value: 'failed', label: '失败' }, { value: 'cancelled', label: '已取消' },
  { value: 'interrupted', label: '结果未确认' },
]
export function jobState(job: JobSnapshot): JobState {
  if (job.persistenceStatus === 'pending') return 'persisting'
  if ((job.status === 'queued' || job.status === 'running') && job.cancellationStatus === 'requested') return 'cancelling'
  if (job.status === 'interrupted' || job.cancellationStatus === 'unknown') return 'interrupted'
  return job.status
}
export function jobStateLabel(job: JobSnapshot): string {
  return jobStatusOptions.find((option) => option.value === jobState(job))!.label
}
export function jobTypeLabel(type: string): string {
  if (type === 'docker-container-create') return '容器创建'
  if (type === 'docker-container-start') return '容器启动'
  if (type === 'docker-container-stop') return '容器停止'
  if (type === 'docker-container-restart') return '容器重启'
  return type === 'docker-image-remove' ? '镜像删除' : type === 'docker-image-pull' ? '镜像拉取' : type === 'compose-deploy' ? 'Compose 部署' : type
}
export function jobArtifactLabel(job: JobSnapshot): string {
  return job.artifactState === 'present' ? '存在' : job.artifactState === 'deleted' ? '已不存在' : '未核实'
}
/** 创建历史仅有保存状态，不依赖详情回执，也不推断容器身份或当前状态。 */
export function containerCreateStateDescription(state: string): string {
  if (state === 'queued' || state === 'running') return '容器创建任务已接收，尚未确认创建完成。'
  if (state === 'completed') return '任务记录标记为已完成；请核对任务详情中的创建回执与容器当前状态，不保证持续运行。'
  if (state === 'failed' || state === 'interrupted') return '创建结果未确认，可能已产生副作用或残留容器；请只读核对实际状态，勿重复提交。'
  return ''
}
export function jobStateDescription(job: JobSnapshot): string {
  const state = jobState(job)
  if (state === 'persisting') return '任务结果正在保存，尚未确认最终结果。'
  if (state === 'cancelling') return '取消请求已接收，远端执行可能仍在继续。'
  if (job.type === 'docker-container-create') return containerCreateStateDescription(state)
  if (state === 'interrupted') return '执行结果未确认，请检查实际资源状态。'
  if (/^docker-container-(start|stop|restart)$/.test(job.type)) {
    if (state === 'completed') return '本次操作已返回且读回状态符合目标；不代表健康或持续状态，请刷新核对。'
    if (state === 'queued' || state === 'running') return '容器操作任务已接收，尚未确认完成。'
    return '操作可能已执行，请只读核对容器状态，勿重复提交。'
  }
  if (job.type === 'docker-image-remove' && state === 'completed') return '镜像删除任务已完成，可刷新本地镜像核验当前状态。'
  if (job.type === 'docker-image-remove' && (state === 'queued' || state === 'running')) return '删除任务已接收，尚未确认删除完成。'
  if (job.type === 'compose-deploy' && state === 'completed') return '部署命令已完成，配置与实际运行状态仍需核验。'
  return ''
}
export function isTerminalJob(job: JobSnapshot): boolean {
  return job.persistenceStatus !== 'pending' && job.status !== 'queued' && job.status !== 'running'
}
export function canRequestJobCancellation(job: JobSnapshot): boolean {
  return job.type === 'docker-image-pull' && job.cancellable === true &&
    (job.status === 'queued' || job.status === 'running') && job.persistenceStatus !== 'pending' &&
    job.cancellationStatus !== 'requested' && job.cancellationStatus !== 'unknown'
}
export function sortJobs(jobs: JobSnapshot[]): JobSnapshot[] {
  return [...jobs].sort((a, b) => (Date.parse(b.createdAt) - Date.parse(a.createdAt) || a.id.localeCompare(b.id)))
}
/** 相同秒级时间不能证明新旧；终态和已接收的控制标记不可倒退。 */
export function mergeJobSnapshot(current: JobSnapshot | undefined, incoming: JobSnapshot): JobSnapshot {
  if (!current) return incoming
  // 受限证据单调保留，旧快照也不能清除；不改变执行状态的优先级。
  const preserveLimit = (selected: JobSnapshot): JobSnapshot =>
    (current.progressLimited === true || incoming.progressLimited === true) && selected.progressLimited !== true
      ? { ...selected, progressLimited: true } : selected
  if (isTerminalJob(current) && (!isTerminalJob(incoming) || current.status !== incoming.status)) return preserveLimit(current)
  if (Date.parse(current.updatedAt) > Date.parse(incoming.updatedAt)) return preserveLimit(current)
  if (!isTerminalJob(incoming) && (
    (current.persistenceStatus === 'pending' && incoming.persistenceStatus !== 'pending') ||
    (current.cancellationStatus === 'requested' && incoming.cancellationStatus !== 'requested') ||
    (current.status === 'running' && incoming.status === 'queued')
  )) return preserveLimit(current)
  return preserveLimit(incoming)
}
