// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import type { DockerInventory } from '@/api/system'
import DockerContainerPanel from './DockerContainerPanel.vue'

vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ElTooltip: defineComponent({
      props: { disabled: Boolean, triggerKeys: Array, appendTo: Object, content: String, placement: String },
      setup: (props, { slots }) => () => h('div', {
        'data-tooltip-disabled': String(props.disabled),
        'data-tooltip-trigger-keys': JSON.stringify(props.triggerKeys),
        'data-tooltip-append-to': (props.appendTo as HTMLElement | undefined)?.id,
        'data-tooltip-content': props.content,
        'data-tooltip-placement': props.placement,
      }, slots.default?.()),
    }),
  }
})
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))

const container: DockerInventory['containers'][number] = { id: 'demo-container', name: 'demo-web', image: 'demo:latest', state: 'running', status: 'Up', createdAt: '2026-10-13T00:00:00.000Z', projectId: 'demo', projectName: 'demo-project', serviceName: 'web', ports: [] }
let app: App | undefined
let host: HTMLDivElement | undefined
afterEach(() => { app?.unmount(); host?.remove(); app = undefined; host = undefined })

// 工具提示桩只验证属性合同，点击验证真实按钮与 emit；原生 Enter/Space 由浏览器专项验证。
describe('容器详情只读入口', () => {
  for (const surface of ['desktop', 'mobile'] as const) {
    for (const state of ['running', 'stopped']) {
      it.each([null, 'demo-container:stop', 'project:other:restart'])(`${surface}/${state} pending=%s 保持详情可用且不发出动作`, async (actionPending) => {
        const onDetails = vi.fn()
        const onAction = vi.fn()
        const displayedContainer = { ...container, state }
        host = document.createElement('div')
        host.id = 'container-tooltip-target'
        document.body.append(host)
        app = createApp(DockerContainerPanel, { containers: [displayedContainer], query: '', stateFilter: 'all', pageSize: 10, actionPending, tooltipAppendTo: host, onDetails, onAction })
        app.mount(host)
        await nextTick()
        const selector = surface === 'desktop' ? '.container-actions' : '.mobile-actions'
        const controls = [...host.querySelectorAll<HTMLButtonElement>(`${selector} button`)]
        expect(controls).toHaveLength(3)
        const details = controls[2]!
        expect(details.type).toBe('button')
        expect(details.disabled).toBe(false)
        expect(details.getAttribute('aria-busy')).toBeNull()
        if (surface === 'desktop') {
          expect(details.getAttribute('aria-label')).toBe('查看容器详情与日志')
          expect(details.parentElement?.dataset).toMatchObject({ tooltipTriggerKeys: '[]', tooltipDisabled: 'false', tooltipAppendTo: host.id, tooltipContent: '查看容器详情与日志', tooltipPlacement: 'top' })
        } else {
          expect(details.textContent).toBe('详情')
          expect(details.parentElement?.matches('.mobile-actions')).toBe(true)
          expect(details.closest('[data-tooltip-trigger-keys]')).toBeNull()
        }
        expect(controls[0]!.disabled).toBe(Boolean(actionPending))
        expect(controls[1]!.disabled).toBe(Boolean(actionPending) || state !== 'running')
        details.click()
        expect(onDetails.mock.calls).toEqual([[displayedContainer]])
        expect(onDetails.mock.calls[0]![0]).toBe(displayedContainer)
        expect(onAction).not.toHaveBeenCalled()
      })
    }
  }
})
