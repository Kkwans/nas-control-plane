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
