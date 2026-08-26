// @vitest-environment jsdom

import { createApp, h } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import ResourceState from './ResourceState.vue'

describe('ResourceState', () => {
  it('renders a recoverable error state with user guidance', () => {
    const host = document.createElement('div')
    const retry = vi.fn()
    const app = createApp({
      render: () => h(ResourceState, {
        state: 'error',
        title: '读取失败',
        message: 'Agent 暂不可用',
        nextStep: '确认服务状态后重试',
        code: 'AGENT_UNAVAILABLE',
        onRetry: retry,
      }),
    })
    app.mount(host)

    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(host.textContent).toContain('下一步：确认服务状态后重试')
    expect(host.textContent).toContain('代码 AGENT_UNAVAILABLE')
    host.querySelector('button')?.click()
    expect(retry).toHaveBeenCalledOnce()
    app.unmount()
  })

  it('keeps empty state free of a retry action', () => {
    const host = document.createElement('div')
    const app = createApp({ render: () => h(ResourceState, { state: 'empty', title: '暂无数据' }) })
    app.mount(host)
    expect(host.querySelector('[role="status"]')).not.toBeNull()
    expect(host.querySelector('button')).toBeNull()
    app.unmount()
  })
})
