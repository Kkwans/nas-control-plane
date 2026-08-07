import { describe, expect, it } from 'vitest'

import { formatDockerContainerStatus } from './docker'

describe('formatDockerContainerStatus', () => {
  it('localizes running duration and health', () => {
    expect(formatDockerContainerStatus('Up About an hour (healthy)')).toBe('已运行 约 1 小时（健康）')
    expect(formatDockerContainerStatus('Up 12 minutes (health: starting)')).toBe('已运行 12 分钟（健康检查中）')
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
