// @vitest-environment jsdom
import { createApp, h, nextTick } from 'vue'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import DockerHubBrowser from './DockerHubBrowser.vue'
const api = vi.hoisted(() => ({ pull: vi.fn(), search: vi.fn(), tags: vi.fn(), subscribe: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/docker', () => ({ pullDockerImage: api.pull, searchDockerHub: api.search, requestDockerHubTags: api.tags, subscribeJob: api.subscribe }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ summary: { host: { architecture: 'amd64' } } }) }))
vi.mock('./NcpSelect.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', () => ({
  ElMessage: { success: api.success, error: api.error },
  ElTooltip: { render: () => null },
  ElInput: { props: ['modelValue'], emits: ['update:modelValue'], setup(_: unknown, { emit }: { emit: (event: string, value: string) => void }) { return () => h('input', { onInput: (e: Event) => emit('update:modelValue', (e.target as HTMLInputElement).value) }) } },
}))
const apps: ReturnType<typeof createApp>[] = []
const flush = async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); await nextTick() }
beforeEach(() => {
  beginSession(); vi.resetAllMocks()
  api.search.mockResolvedValue({ results: [{ namespace: 'library', name: 'alpine', lastUpdated: '2026-10-13', pullCount: 10, starCount: 1 }], count: 1, page: 1 })
  api.tags.mockResolvedValue({ results: [{ name: 'latest', fullSize: 10, architectures: ['amd64'] }] })
})
afterEach(() => apps.splice(0).forEach((app) => app.unmount()))
it.each(['unmount', 'logout'])('%s 后正在提交的拉取不再创建订阅或发送事件', async (action) => {
  let resolve!: (value: unknown) => void
  api.pull.mockReturnValue(new Promise((yes) => { resolve = yes }))
  const created = vi.fn()
  const host = document.createElement('div')
  const app = createApp({ render: () => h(DockerHubBrowser, { onJobCreated: created }) })
  apps.push(app); app.mount(host)
  const input = host.querySelector('input')!; input.value = 'alpine'; input.dispatchEvent(new Event('input'))
  await nextTick()
  host.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true })); await flush()
  ;(host.querySelector('.repository-detail footer button') as HTMLButtonElement).click()
  expect(api.pull).toHaveBeenCalledOnce()
  if (action === 'unmount') apps.pop()!.unmount(); else invalidateSession()
  resolve({ id: 'job-1', status: 'queued' }); await flush()
  expect(created).not.toHaveBeenCalled()
  expect(api.subscribe).not.toHaveBeenCalled()
  expect(api.success).not.toHaveBeenCalled()
})
