// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import AppShell from './AppShell.vue'
import type { SystemConnectionState } from '@/stores/system'

vi.mock('vue-router', () => ({
  RouterLink: { render: () => null },
  useRoute: () => ({ path: '/', meta: { title: '总览' }, params: {}, query: {} }),
}))
vi.mock('element-plus', () => ({ ElTooltip: { inheritAttrs: false, setup: (_: unknown, { slots }: { slots: { default: () => unknown } }) => () => slots.default() } }))
let app: App | undefined
let host: HTMLDivElement
afterEach(() => { app?.unmount(); host?.remove(); vi.unstubAllGlobals() })

describe('AppShell 实时健康徽标', () => {
  it('采用数据健康状态而非 streaming 着色，过期/部分错误优先于连接成功', async () => {
    vi.stubGlobal('matchMedia', () => Object.assign(new EventTarget(), { matches: false }))
    const props = reactive({ deviceName: 'fixture', connectionState: 'connected' as SystemConnectionState, connectionLabel: '数据已同步', connectionDetail: '', realtimeState: 'streaming' as const, refreshIntervalSeconds: 5, userName: 'root', liveDataActive: true })
    host = document.createElement('div'); document.body.append(host)
    app = createApp({ render: () => h(AppShell, props) }); app.mount(host)
    const badge = () => host.querySelector('.connection-state')!
    expect(badge().classList.contains('connection-state--connected')).toBe(true)
    expect(badge().textContent).toContain('实时更新')
    props.connectionState = 'degraded'
    props.connectionLabel = '显示上次数据'
    props.connectionDetail = '已显示的数据超过 15 秒。'
    await nextTick()
    expect(badge().classList.contains('connection-state--degraded')).toBe(true)
    expect(badge().classList.contains('connection-state--connected')).toBe(false)
    expect(badge().textContent).toContain('显示上次数据')
    expect(badge().textContent).not.toContain('实时更新')
    expect(badge().getAttribute('title')).toContain('超过 15 秒')
    props.connectionLabel = '历史写入失败'
    await nextTick()
    expect(badge().textContent).toContain('历史写入失败')
    props.connectionState = 'connected'
    await nextTick()
    expect(badge().textContent).toContain('实时更新')
  })
})
