import { describe, expect, it } from 'vitest'

import {
  dockerActionNeedsConfirmation,
  dockerContainerActionConfirmation,
  dockerProjectActionConfirmation,
  dockerProjectActionDisabled,
  dockerProjectActionTargets,
  dockerProjectDeleteDisabledReason,
} from './dockerLifecycle'

const project = {
  id: 'media-stack',
  name: '媒体服务',
  kind: 'compose' as const,
  state: 'running' as const,
  workingDirectory: '/volume2/DockerProject/media-stack',
  configFiles: ['compose.yml'],
  containerCount: 2,
  runningCount: 1,
}

const containers = [
  { id: 'c-running', name: 'media-api', image: 'media-api:latest', state: 'running', projectId: project.id },
  { id: 'c-stopped', name: 'media-worker', image: 'media-worker:latest', state: 'exited', projectId: project.id },
].map((container) => ({
  ...container,
  status: '',
  createdAt: '2026-08-24T00:00:00.000Z',
  ports: [],
  projectName: project.name,
  serviceName: container.name,
}))

describe('docker lifecycle decisions', () => {
  it('does not confirm start but confirms impact-bearing stop and restart', () => {
    expect(dockerActionNeedsConfirmation('start')).toBe(false)
    expect(dockerActionNeedsConfirmation('stop')).toBe(true)
    expect(dockerActionNeedsConfirmation('restart')).toBe(true)
  })

  it('targets the expected containers for compose and standalone projects', () => {
    expect(dockerProjectActionTargets(project, containers, 'start').map((item) => item.id)).toEqual(['c-running', 'c-stopped'])
    expect(dockerProjectActionTargets(project, containers, 'stop').map((item) => item.id)).toEqual(['c-running', 'c-stopped'])
    expect(dockerProjectActionTargets({ ...project, kind: 'standalone' }, containers, 'start').map((item) => item.id)).toEqual(['c-stopped'])
    expect(dockerProjectActionTargets({ ...project, kind: 'standalone' }, containers, 'restart').map((item) => item.id)).toEqual(['c-running'])
  })

  it('keeps disabled state and delete constraints explicit', () => {
    expect(dockerProjectActionDisabled({ ...project, state: 'stopped', runningCount: 0 }, 'start')).toBe(false)
    expect(dockerProjectActionDisabled({ ...project, state: 'running', runningCount: 2 }, 'stop')).toBe(false)
    expect(dockerProjectActionDisabled({ ...project, runningCount: 0 }, 'restart')).toBe(true)
    expect(dockerProjectDeleteDisabledReason(project)).toContain('仍在运行')
    expect(dockerProjectDeleteDisabledReason({ ...project, state: 'stopped', runningCount: 0 })).toBe('')
    expect(dockerProjectDeleteDisabledReason({ ...project, name: 'NAS-Control-Plane', state: 'stopped', runningCount: 0 })).toContain('受保护')
  })

  it.each(['stop', 'restart'] as const)('describes Compose %s scope as execution-time configuration, not an observed target guarantee', (action) => {
    const label = action === 'stop' ? '停止' : '重启'
    const interruption = action === 'stop' ? '服务会中断' : '期间服务会短暂中断'
    expect(dockerProjectActionConfirmation(project, action, 3)).toBe(`将通过 Compose ${label}项目“媒体服务”。确认前最近一次清单观察到该项目 3 个容器；实际操作范围由执行时的 Compose 配置决定。${interruption}，镜像、卷、Compose 文件和工作目录不会删除。`)
    expect(dockerProjectActionConfirmation(project, action, 3)).not.toContain('逐项重启')
  })

  it('preserves standalone confirmation copy exactly', () => {
    const standalone = { ...project, kind: 'standalone' as const }
    expect(dockerProjectActionConfirmation(standalone, 'stop', 2)).toBe('将停止项目“媒体服务”的 2 个容器。容器会停止，但镜像、卷、Compose 文件和工作目录不会删除。')
    expect(dockerProjectActionConfirmation(standalone, 'restart', 1)).toBe('将重启项目“媒体服务”的 1 个容器。容器会逐项重启，镜像、卷、Compose 文件和工作目录不会删除。')
  })

  it('explains interruption and preserved resources before confirmation', () => {
    expect(dockerProjectActionConfirmation(project, 'stop', 1)).toContain('镜像、卷、Compose 文件和工作目录不会删除')
    expect(dockerContainerActionConfirmation({ name: 'media-api', image: 'media-api:latest' }, 'restart')).toContain('短暂中断')
  })
})
