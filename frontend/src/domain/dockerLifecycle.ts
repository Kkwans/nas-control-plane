import type { ContainerAction, DockerInventory, DockerProject } from '@/api/system'

export type DockerContainer = DockerInventory['containers'][number]

export function dockerActionLabel(action: ContainerAction) {
  return action === 'start' ? '启动' : action === 'stop' ? '停止' : '重启'
}

export function dockerActionNeedsConfirmation(action: ContainerAction) {
  return action !== 'start'
}

export function dockerProjectActionTargets(
  project: DockerProject,
  containers: DockerContainer[],
  action: ContainerAction,
) {
  const projectContainers = containers.filter((container) => container.projectId === project.id)
  if (project.kind === 'compose') return projectContainers
  if (action === 'start') return projectContainers.filter((container) => container.state !== 'running')
  return projectContainers.filter((container) => container.state === 'running')
}

export function dockerProjectActionDisabled(
  project: DockerProject,
  action: ContainerAction,
  busy = false,
) {
  if (busy || project.containerCount === 0) return true
  if (action === 'start') return project.state === 'running'
  if (action === 'stop') return project.state === 'stopped'
  return project.runningCount === 0
}

export function dockerProjectDeleteDisabledReason(
  project: DockerProject,
  busy = false,
) {
  if (project.kind !== 'compose') return '独立容器是虚拟分组，不能作为项目删除；请在容器列表中单独管理。'
  if (project.name.toLowerCase() === 'nas-control-plane') return 'NCP 自身项目受保护，不能从当前控制台删除。'
  if (project.state !== 'stopped' || project.runningCount > 0) return '项目仍在运行，请先停止全部容器。'
  if (busy) return '其他 Docker 操作正在执行，请稍后重试。'
  return ''
}

export function dockerProjectActionConfirmation(
  project: DockerProject,
  action: ContainerAction,
  targetCount: number,
) {
  if (project.kind === 'compose') {
    const interruption = action === 'stop' ? '服务会中断' : '期间服务会短暂中断'
    return `将通过 Compose ${dockerActionLabel(action)}项目“${project.name}”。确认前最近一次清单观察到该项目 ${targetCount} 个容器；实际操作范围由执行时的 Compose 配置决定。${interruption}，镜像、卷、Compose 文件和工作目录不会删除。`
  }
  const countLabel = `${targetCount} 个容器`
  if (action === 'stop') {
    return `将停止项目“${project.name}”的 ${countLabel}。容器会停止，但镜像、卷、Compose 文件和工作目录不会删除。`
  }
  return `将重启项目“${project.name}”的 ${countLabel}。容器会逐项重启，镜像、卷、Compose 文件和工作目录不会删除。`
}

export function dockerContainerActionConfirmation(
  container: Pick<DockerContainer, 'name' | 'image'>,
  action: ContainerAction,
) {
  const name = container.name || container.image || '未命名容器'
  return action === 'stop'
    ? `将停止容器“${name}”。容器、镜像和卷不会删除，之后可以再次启动。`
    : `将重启容器“${name}”。容器、镜像和卷不会删除，期间服务会短暂中断。`
}

/** 后台动作确认绑定完整身份；接收后不能取消。 */
export function dockerContainerActionJobConfirmation(
  container: Pick<DockerContainer, 'id' | 'name' | 'image'>,
  action: ContainerAction,
) {
  return `${dockerContainerActionConfirmation(container, action)} 完整容器 ID：${container.id}。确认后提交后台任务，接收不代表完成，任务不能取消。`
}
