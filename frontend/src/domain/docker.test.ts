import { describe, expect, it } from 'vitest'

import { dockerContainerStateDetail, dockerContainerStateLabel, dockerContainerTimingLabel, formatDockerContainerStatus } from './docker'

describe('formatDockerContainerStatus', () => {
  it('localizes running duration and health', () => {
    expect(formatDockerContainerStatus('Up About an hour (healthy)')).toBe('已运行 约 1 小时（健康）')
    expect(formatDockerContainerStatus('Up About a minute (healthy)')).toBe('已运行 约 1 分钟（健康）')
    expect(formatDockerContainerStatus('Up 12 minutes (health: starting)')).toBe('已运行 12 分钟（健康检查中）')
  })

  it('uses structured state facts without leaking English Docker status text', () => {
    expect(dockerContainerStateLabel('running')).toBe('运行中')
    expect(dockerContainerStateDetail({ state: 'running', health: 'healthy', exitCode: 0, restartCount: 0 })).toBe('健康')
    expect(dockerContainerStateDetail({ state: 'exited', exitCode: 137, restartCount: 0 })).toBe('退出码 137')
  })

  it('moves runtime and stop time below the container name', () => {
    expect(dockerContainerTimingLabel({
      state: 'running', createdAt: '2026-08-08T10:00:00Z', startedAt: '2026-08-08T11:00:00Z',
    }, Date.parse('2026-08-08T12:05:00Z'))).toContain('已运行 1 小时 5 分钟')
    expect(dockerContainerTimingLabel({
      state: 'exited', createdAt: '2026-08-08T10:00:00Z', finishedAt: '2026-08-08T11:30:00Z',
    })).toContain('停止')
  })

  it('localizes exited and restarting states', () => {
    expect(formatDockerContainerStatus('Exited (1) 3 weeks ago')).toBe('已退出（代码 1） · 3 周前')
    expect(formatDockerContainerStatus('Restarting (2) 8 seconds ago')).toBe('正在重启（代码 2） · 8 秒前')
  })

  it('handles fixed states and preserves unknown engine text', () => {
    expect(formatDockerContainerStatus('Created')).toBe('已创建')
    expect(formatDockerContainerStatus('Paused')).toBe('已暂停')
    expect(formatDockerContainerStatus('custom runtime state')).toBe('custom runtime state')
  })
})
