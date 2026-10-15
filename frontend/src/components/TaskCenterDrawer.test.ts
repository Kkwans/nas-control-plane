// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App, type Ref, type VNodeChild } from 'vue'
import type { JobSnapshot } from '@/api/system'
import TaskCenterDrawer from './TaskCenterDrawer.vue'
import { invalidateSession } from '@/session/sessionLifecycle'
const mock = vi.hoisted(() => ({ state: {} as Record<string, unknown>, finishClose: () => {} }))
vi.mock('@/composables/useTaskCenter', () => ({ useTaskCenter: () => mock.state }))
vi.mock('element-plus', () => ({ ElDrawer: { props: ['modelValue', 'withHeader', 'title', 'bodyClass'], emits: ['closed'], setup: (props: { modelValue: boolean; withHeader: boolean; title: string; bodyClass: string }, { slots, emit }: { slots: Record<string, (arg?: unknown) => VNodeChild>; emit: (event: string) => void }) => { mock.finishClose = () => emit('closed'); return () => h('section', { role: 'dialog', 'aria-label': props.title, 'data-body-class': props.bodyClass, 'data-with-header': String(props.withHeader), 'data-open': String(props.modelValue) }, [slots.header?.({ titleId: 'task-title' }), slots.default?.()]) } } }))
let app: App | undefined
let host: HTMLDivElement
const refresh = vi.fn(), requestCancel = vi.fn()
function job(overrides: Partial<JobSnapshot> = {}): JobSnapshot { return { id: 'job-1', type: 'docker-image-pull', reference: 'alpine:latest', status: 'completed', artifactState: 'unknown', progress: 100, message: '', createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:01:00Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...overrides } }
async function mount(jobs: JobSnapshot[] = [job()], overrides: Record<string, unknown> = {}) {
  mock.state = { jobs: ref(jobs), invalidCount: ref(0), loading: ref(false), refreshing: ref(false), hasLoaded: ref(true), stale: ref(false), error: ref(''), lastFetchedAt: ref(new Date('2026-10-13T12:01:00Z')), typeFilter: ref('all'), statusFilter: ref('all'), query: ref(''), page: ref(1), typeOptions: ref([{ value: 'all', label: '全部类型' }]), statusOptions: ref([{ value: 'all', label: '全部状态' }]), filteredJobs: ref(jobs), pagedJobs: ref(jobs), pageCount: ref(1), cancellingIds: ref(new Set()), cancelErrors: ref({}), refresh, requestCancel, canCancel: (item: JobSnapshot) => item.type === 'docker-image-pull' && item.status === 'running' && item.cancellable === true, ...overrides }
  host = document.createElement('div'); document.body.append(host)
  const close = vi.fn(); app = createApp({ render: () => h(TaskCenterDrawer, { onClose: close }) }); app.mount(host); await nextTick(); return close
}
function set(key: string, value: unknown) { (mock.state[key] as Ref<unknown>).value = value }
function button(text: string) { return [...host.querySelectorAll('button')].find(item => item.textContent?.includes(text))! }
afterEach(() => { app?.unmount(); host?.remove(); vi.clearAllMocks() })
describe('TaskCenterDrawer', () => {
  it('镜像删除接收不显示完成、字节进度或取消，并提供只读刷新', async () => {
    await mount([job({ type: 'docker-image-remove', status: 'queued', cancellable: false })])
    expect(host.textContent).toContain('镜像删除')
    expect(host.textContent).toContain('尚未确认删除完成')
    expect(host.querySelector('progress')).toBeNull()
    button('查看详情').click(); await nextTick()
    expect(host.querySelector('.task-cancel')).toBeNull()
    button('刷新任务').click(); await nextTick()
    expect(refresh).toHaveBeenCalledOnce(); expect(requestCancel).not.toHaveBeenCalled()
  })
  it('自定义 div 标题不创建重复 banner，dialog 仍关联可见标题', async () => {
    await mount(); const dialog = host.querySelector('[role="dialog"]')!
    expect(dialog.getAttribute('data-body-class')).toBe('task-center-drawer__body'); expect(dialog.getAttribute('data-with-header')).toBe('false'); expect(host.querySelector('header')).toBeNull()
    const title = host.querySelector('h2')!; expect(title.textContent).toBe('最近任务'); expect(dialog.getAttribute('aria-labelledby')).toBe(title.id)
  })
  it('筛选器名称由独立标签提供，不混入选项文本', async () => {
    await mount(); const controls = [...host.querySelectorAll('input, select')]
    expect(controls.map(control => document.getElementById(control.getAttribute('aria-labelledby')!)?.textContent)).toEqual(['搜索已加载记录', '任务类型', '任务状态'])
  })
  it('最近列表范围明确，完成与产物未知分别呈现', async () => {
    await mount(); expect(host.textContent).toContain('可能不包含更早历史'); expect(host.textContent).toContain('关闭面板不会停止任务')
    expect(host.textContent).toContain('当前列表 1 条'); expect(host.textContent).toContain('已完成')
    button('查看详情').click(); await nextTick(); expect(host.textContent).toContain('未核实'); expect(host.textContent).not.toContain('已不存在')
    expect(host.querySelector('progress')?.getAttribute('aria-label')).toBe('alpine:latest 进度')
  })
  it('长文本保持纯文本，Compose 无伪造进度和取消', async () => {
    const text = '<img src=x onerror=alert(1)>长中文错误\n第二行'
    await mount([job({ type: 'compose-deploy', message: text, error: text, progress: 0, cancellable: true })]); button('查看详情').click(); await nextTick()
    expect(host.querySelector('img')).toBeNull(); expect(host.textContent).toContain(text); expect(host.textContent).toContain('此任务不支持取消'); expect(host.textContent).toContain('请另行核验配置')
    expect(host.querySelector('progress')).toBeNull(); expect(button('请求取消')).toBeUndefined()
  })
  it('取消能力判定与正在提交状态可感知', async () => {
    const active = job({ status: 'running', cancellable: true }); await mount([active]); button('查看详情').click(); await nextTick()
    button('请求取消').focus(); button('请求取消').click(); expect(requestCancel).toHaveBeenCalledWith(active)
    expect(document.activeElement).toBe(button('收起详情'))
    set('cancellingIds', new Set(['job-1'])); await nextTick(); expect(button('正在提交').disabled).toBe(true)
  })
  it('刷新移除当前取消行时把焦点交还稳定的关闭按钮', async () => {
    await mount([job({ status: 'running', cancellable: true })]); button('查看详情').click(); await nextTick(); button('请求取消').focus()
    set('jobs', []); await nextTick(); expect(document.activeElement).toBe(host.querySelector('[aria-label="关闭最近任务"]'))
  })
  it('首次加载、失败、空集合与全无效记录明确区分', async () => {
    await mount([], { loading: ref(true), hasLoaded: ref(false) }); expect(host.textContent).toContain('正在读取最近任务')
    set('loading', false); set('error', '连接失败'); await nextTick(); expect(host.textContent).toContain('最近任务读取失败'); button('重试').click(); expect(refresh).toHaveBeenCalled()
    set('hasLoaded', true); set('error', ''); set('invalidCount', 2); await nextTick(); expect(host.textContent).toContain('记录无法读取'); expect(host.textContent).not.toContain('尚无可展示记录')
    set('invalidCount', 0); await nextTick(); expect(host.textContent).toContain('尚无可展示记录')
  })
  it('刷新失败保留采样时间；筛选无匹配可清空', async () => {
    await mount([job()], { stale: ref(true), error: ref('暂时失败'), filteredJobs: ref([]), query: ref('missing') }); expect(host.textContent).toContain('当前记录可能已过期'); expect(host.textContent).toContain('上次成功获取')
    button('清空筛选').click(); expect((mock.state.query as Ref<string>).value).toBe('')
  })
  it('关闭先停止观察并保留退场动画，closed 后才通知卸载', async () => {
    const close = await mount(); host.querySelector<HTMLButtonElement>('[aria-label="关闭最近任务"]')!.click(); await nextTick()
    expect(host.querySelector('[role="dialog"]')?.getAttribute('data-open')).toBe('false'); expect(close).not.toHaveBeenCalled()
    mock.finishClose(); expect(close).toHaveBeenCalledOnce()
  })
  it('会话撤销立即关闭且卸载解除监听', async () => {
    const close = await mount(); invalidateSession(); expect(close).toHaveBeenCalledOnce(); app?.unmount(); app = undefined; invalidateSession(); expect(close).toHaveBeenCalledOnce()
  })
})
