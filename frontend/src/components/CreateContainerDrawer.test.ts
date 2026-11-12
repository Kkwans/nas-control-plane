// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, effectScope, h, nextTick, reactive, type App } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { type DockerImageSummary } from '@/api/system'
import CreateContainerDrawer from './CreateContainerDrawer.vue'
import { useDockerContainerCreateJobs } from '@/composables/useDockerContainerCreateJobs'

const api = vi.hoisted(() => ({ create: vi.fn(), resources: vi.fn(), inventory: vi.fn(), paths: vi.fn(), success: vi.fn(), error: vi.fn(), warning: vi.fn() }))
vi.mock('@/api/system', () => ({ NcpApiError: class extends Error {}, CreateContainerOutcomeUnknownError: class extends Error {}, createDockerContainer: api.create, requestDockerResources: api.resources, requestDockerInventory: api.inventory, requestPathEntries: api.paths }))
vi.mock('@/api/dockerContainerCreateJob', () => ({ requestDockerContainerCreateJob: api.create, requestDockerContainerCreateJobStatus: vi.fn(), isDockerContainerCreateAdmissionRejected: () => false }))
vi.mock('./NcpSelect.vue', () => ({ default: { render: () => null } }))
vi.mock('./SectionHeader.vue', () => ({ default: defineComponent({ setup(_, { slots }) { return () => slots.actions?.() } }) }))
vi.mock('element-plus', () => {
  const overlay = defineComponent({ props: ['modelValue', 'title'], emits: ['update:modelValue'], setup(props, { slots, emit }) { return () => props.modelValue ? h('section', { 'data-title': props.title }, [slots.header?.(), h('button', { class: 'close', onClick: () => emit('update:modelValue', false) }, '关闭'), slots.default?.(), slots.footer?.()]) : null } })
  const input = defineComponent({ props: ['modelValue'], emits: ['update:modelValue'], setup(props, { emit }) { return () => h('input', { value: props.modelValue, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value) }) } })
  return { ElDrawer: overlay, ElDialog: overlay, ElInput: input, ElInputNumber: input, ElSwitch: defineComponent({ props: ['modelValue'], emits: ['update:modelValue'], setup(props, { emit }) { return () => h('button', { class: 'switch', onClick: () => emit('update:modelValue', !props.modelValue) }, '切换') } }), ElMessage: { success: api.success, error: api.error, warning: api.warning }, ElTooltip: defineComponent({ setup(_, { slots }) { return () => slots.default?.() } }), ElAlert: defineComponent({ props: ['title'], setup(props) { return () => h('p', props.title) } }), ElEmpty: { render: () => null } }
})
function deferred<T = unknown>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const image = (name: string) => ({ id: name, repoTags: [`${name}:latest`] }) as DockerImageSummary
const emptyResources = { networks: [], volumes: [] }
const result = { id: 'create-1', type: 'docker-container-create', status: 'queued' }
const scopes: ReturnType<typeof effectScope>[] = []
const mounted: { app: App; host: HTMLDivElement }[] = []
async function flush() { await Promise.resolve(); await Promise.resolve(); await nextTick() }
async function mount() {
  const state = reactive({ open: true, image: image('alpine') })
  const created = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const app = createApp({ render: () => h(CreateContainerDrawer, { modelValue: state.open, image: state.image, 'onUpdate:modelValue': (value: boolean) => { state.open = value }, onCreated: created }) })
  app.mount(host); mounted.push({ app, host }); await flush()
  // 弹窗传送到 body；查询覆盖抽屉与弹窗，清理仍只移除实际挂载节点。
  const scope = effectScope(); scopes.push(scope)
  const registry = scope.run(() => useDockerContainerCreateJobs())!
  return { state, host: document.body, created, app, registry }
}
function button(host: HTMLElement, text: string) { return [...host.querySelectorAll('button')].find((item) => item.textContent === text)! }
async function click(host: HTMLElement, text: string) { button(host, text).click(); await flush() }
async function submit(host: HTMLElement) { await click(host, '仅创建'); await click(host, '确认创建') }
async function edit(input: HTMLInputElement, value: string) { input.value = value; input.dispatchEvent(new Event('input')); await flush() }
function name(host: HTMLElement) { return host.querySelector<HTMLInputElement>('[placeholder="例如 mysql-1"]')! }
beforeEach(() => { vi.resetAllMocks(); beginSession(); api.resources.mockResolvedValue(emptyResources); api.inventory.mockResolvedValue({ containers: [] }) })
afterEach(() => { for (const scope of scopes.splice(0)) scope.stop(); for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } expect(document.querySelector('.create-container-dialogs')).toBeNull() })

describe('创建容器草稿所有权', () => {
  it('自定义标题保留抽屉名称与二级标题，切换镜像时同步更新', async () => {
    const { state, host } = await mount()
    expect(host.querySelector('.create-container-drawer')?.getAttribute('data-title')).toBe('从 alpine:latest 创建容器')
    expect(host.querySelector('.drawer-heading h2')?.textContent).toBe('从 alpine:latest 创建容器')
    state.image = image('redis'); await flush()
    expect(host.querySelector('.create-container-drawer')?.getAttribute('data-title')).toBe('从 redis:latest 创建容器')
    expect(host.querySelector('.drawer-heading h2')?.textContent).toBe('从 redis:latest 创建容器')
  })

  it.each(['success', 'failure'])('A 提交关闭重开 B 后 A %s 不覆盖 B，接纳或未知均保留会话锁', async (outcome) => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { state, host, created } = await mount(); await submit(host)
    expect(host.textContent).toContain('提交后关闭不会取消容器创建')
    state.open = false; await flush(); state.open = true; await flush(); await edit(name(host), 'draft-B')
    expect(button(host, '仅创建').disabled).toBe(true)
    if (outcome === 'success') write.resolve(result); else write.reject(new Error('late failure'))
    await flush()
    expect(state.open).toBe(true); expect(name(host).value).toBe('draft-B'); expect(button(host, '仅创建').disabled).toBe(true)
    expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled(); expect(api.create).toHaveBeenCalledTimes(1)
    expect(created).not.toHaveBeenCalled()
  })

  it('当前草稿接纳后关闭并只提交一次，不把202当作完成', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, state, created } = await mount(); await submit(host)
    expect(host.querySelector('[data-title="确认容器配置"]')).toBeNull()
    write.resolve(result); await flush()
    expect(created).not.toHaveBeenCalled(); expect(state.open).toBe(false); expect(api.success).not.toHaveBeenCalled()
  })

  it('同一开窗切镜像会撤销预览并重建草稿', async () => {
    const { state, host } = await mount(); await click(host, '仅创建')
    state.image = image('redis'); await flush()
    expect(host.querySelector('[data-title="确认容器配置"]')).toBeNull(); expect(name(host).value).toBe('redis'); expect(api.create).not.toHaveBeenCalled()
    api.create.mockResolvedValue(result); await submit(host)
    expect(api.create.mock.calls[0]![0].image).toBe('redis:latest')
  })

  it('编辑使预览失效，提交使用独立快照而非可变表单', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host } = await mount(); await click(host, '仅创建'); await edit(name(host), 'edited')
    expect(host.querySelector('[data-title="确认容器配置"]')).toBeNull()
    await submit(host); await edit(name(host), 'later')
    expect(api.create.mock.calls[0]![0].name).toBe('edited'); write.resolve(result); await flush()
    expect(name(host).value).toBe('later'); expect(api.success).not.toHaveBeenCalled()
  })

  it.each([['unmount', 'success'], ['unmount', 'failure'], ['session', 'success'], ['session', 'failure']])('%s 后迟到 %s 不提示、关闭或发 created', async (kind, outcome) => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, app, created, state } = await mount(); await submit(host)
    if (kind === 'unmount') { app.unmount(); mounted.pop() } else invalidateSession()
    if (outcome === 'success') write.resolve(result); else write.reject(new Error('late failure'))
    await flush()
    expect(created).not.toHaveBeenCalled(); expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled(); expect(state.open).toBe(true)
  })


  it('同开窗换镜像时迟到接纳仍保留保护，不能关闭新镜像草稿', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { state, host, created } = await mount(); await submit(host)
    state.image = image('redis'); await flush(); expect(button(host, '仅创建').disabled).toBe(true)
    write.resolve(result); await flush()
    expect(created).not.toHaveBeenCalled(); expect(state.open).toBe(true)
    expect(name(host).value).toBe('redis'); expect(api.success).not.toHaveBeenCalled()
  })

  it('会话失效同步取消预览与资源读取，旧错误不显示且不能提交', async () => {
    const resources = deferred(); api.resources.mockReturnValue(resources.promise)
    const { host } = await mount(); await click(host, '仅创建')
    const signal = api.resources.mock.calls[0]![1] as AbortSignal
    invalidateSession(); await flush(); expect(signal.aborted).toBe(true)
    resources.reject(new Error('old session')); await flush(); await click(host, '仅创建')
    expect(host.querySelector('[data-title="确认容器配置"]')).toBeNull(); expect(api.create).not.toHaveBeenCalled()
    expect(host.textContent).not.toContain('清单暂不可用')
  })

  it('资源 A/B 乱序只接受 B，迟到清单不改用户专用网络名', async () => {
    const a = deferred(); const b = deferred(); api.resources.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const { state, host } = await mount(); const signal = api.resources.mock.calls[0]![1] as AbortSignal
    state.open = false; await flush(); state.open = true; await flush(); expect(signal.aborted).toBe(true)
    const switches = host.querySelectorAll<HTMLButtonElement>('.switch'); switches[0]!.click(); await flush(); host.querySelectorAll<HTMLButtonElement>('.switch')[1]!.click(); await flush()
    const network = host.querySelector<HTMLInputElement>('[placeholder="例如 media-net"]')!; await edit(network, 'mine-net')
    b.resolve({ networks: [{ name: 'other', driver: 'bridge' }], volumes: [] }); await flush(); expect(network.value).toBe('mine-net')
    a.reject(new Error('old')); await flush(); expect(host.textContent).not.toContain('清单暂不可用'); expect(network.value).toBe('mine-net')
  })

  it('路径弹窗关闭重开后旧结果不可显示或选择', async () => {
    const a = deferred(); const b = deferred(); api.paths.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const { host } = await mount(); await click(host, '添加挂载'); await click(host, '浏览')
    const signal = api.paths.mock.calls[0]![4] as AbortSignal
    host.querySelector<HTMLButtonElement>('[data-title="选择 NAS 路径"] .close')!.click(); await flush(); expect(signal.aborted).toBe(true)
    await click(host, '浏览'); b.resolve({ path: '/', parent: '/', entries: [{ path: '/B', name: 'B', type: 'directory', readable: true }] }); await flush()
    a.resolve({ path: '/', parent: '/', entries: [{ path: '/A', name: 'A', type: 'directory', readable: true }] }); await flush()
    expect(host.querySelector('.path-entry__name')?.textContent).toBe('DIRB'); await click(host, '选择')
    expect(host.querySelector<HTMLInputElement>('[placeholder="/volume2/data"]')!.value).toBe('/B')
  })


  it('A 资源结束不能清除 B 的 loading，关闭后的路径失败不会污染重开', async () => {
    const a = deferred(); const b = deferred(); api.resources.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const { state, host } = await mount(); state.open = false; await flush(); state.open = true; await flush()
    a.resolve(emptyResources); await flush(); expect(host.textContent).toContain('正在读取 Docker 网络、卷和端口清单')
    b.resolve(emptyResources); await flush(); expect(host.textContent).not.toContain('正在读取 Docker 网络、卷和端口清单')
    const pathA = deferred(); const pathB = deferred(); api.paths.mockReturnValueOnce(pathA.promise).mockReturnValueOnce(pathB.promise)
    await click(host, '添加挂载'); await click(host, '浏览')
    host.querySelector<HTMLButtonElement>('[data-title="选择 NAS 路径"] .close')!.click(); await flush(); await click(host, '浏览')
    pathA.reject(new Error('late')); await flush(); expect(host.textContent).not.toContain('NAS 目录暂不可读取'); expect(host.textContent).toContain('正在读取目录')
    pathB.resolve({ path: '/', parent: '/', entries: [] }); await flush(); expect(host.textContent).not.toContain('正在读取目录')
  })

  it('移除路径目标行会关闭弹窗并忽略迟到响应', async () => {
    const read = deferred(); api.paths.mockReturnValue(read.promise)
    const { host } = await mount(); await click(host, '添加挂载'); await click(host, '浏览')
    host.querySelector<HTMLButtonElement>('.repeat-row--mount .row-remove')!.click(); await flush()
    read.resolve({ path: '/', parent: '/', entries: [{ path: '/A', name: 'A', type: 'directory', readable: true }] }); await flush()
    expect(host.querySelector('[data-title="选择 NAS 路径"]')).toBeNull()
  })
})


describe('创建任务会话保护归属', () => {
  it.each(['close', 'session'])('%s 清理环境变量、挂载及命令草稿并撤销未派发预览', async (boundary) => {
    const { host, state, registry } = await mount()
    await click(host, '添加环境变量')
    await edit(host.querySelector<HTMLInputElement>('[placeholder="变量，例如 APP_MODE"]')!, 'SECRET')
    await edit(host.querySelector<HTMLInputElement>('[placeholder="值"]')!, 'private-value')
    await click(host, '添加挂载')
    await edit(host.querySelector<HTMLInputElement>('[placeholder="/volume2/data"]')!, '/private/path')
    const command = host.querySelector<HTMLTextAreaElement>('[aria-label="容器启动参数（每行一个）"]')!
    command.value = 'private-command'; command.dispatchEvent(new Event('input')); await flush()
    if (boundary === 'close') { state.open = false; await flush(); state.open = true; await flush() } else { invalidateSession(); await flush() }
    expect(host.querySelector('[placeholder="变量，例如 APP_MODE"]')).toBeNull()
    expect(host.querySelector('[placeholder="/volume2/data"]')).toBeNull()
    expect(host.querySelector<HTMLTextAreaElement>('[aria-label="容器启动参数（每行一个）"]')?.value).toBe('')
    expect(registry.entry.value).toBeNull(); expect(api.create).not.toHaveBeenCalled()
  })

  it('编辑中迟到未知只保留安全意图，阻止按钮、Enter与旧确认重放', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, created, state, registry } = await mount()
    await click(host, '添加环境变量')
    await edit(host.querySelector<HTMLInputElement>('[placeholder="变量，例如 APP_MODE"]')!, 'SECRET')
    await edit(host.querySelector<HTMLInputElement>('[placeholder="值"]')!, 'private-value')
    await click(host, '创建并启动')
    const staleConfirm = button(host, '确认创建并启动')
    staleConfirm.click(); await flush(); await edit(name(host), 'edited-B')
    write.reject(new Error('private-server-diagnostic')); await flush()
    expect(registry.entry.value?.phase).toBe('unknown')
    expect(registry.entry.value?.runContainer).toBe(true)
    const saved = JSON.stringify(registry.entry.value)
    for (const secret of ['alpine', 'edited-B', 'SECRET', 'private-value', 'private-server-diagnostic']) expect(saved).not.toContain(secret)
    expect(host.querySelector('.unresolved-attempt')?.textContent).toContain('任务受理不代表创建成功')
    expect(name(host).value).toBe('edited-B'); expect(state.open).toBe(true)
    expect(button(host, '仅创建').disabled).toBe(true); expect(button(host, '创建并启动').disabled).toBe(true)
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    staleConfirm.click(); await flush()
    expect(api.create).toHaveBeenCalledTimes(1); expect(created).not.toHaveBeenCalled()
    expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled()
  })

  it.each(['closed', 'reopened', 'image-changed'])('迟到未知发生于 %s 后不改草稿或丢失锁', async (boundary) => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, state, created, registry } = await mount(); await submit(host)
    state.open = false; await flush()
    if (boundary !== 'closed') { state.open = true; if (boundary === 'image-changed') state.image = image('redis'); await flush(); await edit(name(host), 'draft-B') }
    write.reject(new Error('unknown')); await flush()
    expect(registry.entry.value?.phase).toBe('unknown')
    if (boundary === 'closed') { state.open = true; await flush() } else expect(name(host).value).toBe('draft-B')
    expect(button(host, '仅创建').disabled).toBe(true)
    expect(host.textContent).toContain('Docker 页面顶部只读核对')
    expect(created).not.toHaveBeenCalled(); expect(api.create).toHaveBeenCalledTimes(1)
  })

  it.each(['accepted', 'unknown'])('卸载后迟到 %s 保留到新抽屉实例，不能重新创建', async (outcome) => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const first = await mount(); await submit(first.host)
    first.app.unmount(); mounted.pop()
    if (outcome === 'accepted') write.resolve(result); else write.reject(new Error('unknown'))
    await flush()
    const second = await mount()
    expect(second.registry.entry.value?.phase).toBe(outcome === 'accepted' ? 'known' : 'unknown')
    expect(button(second.host, '仅创建').disabled).toBe(true)
    expect(first.created).not.toHaveBeenCalled(); expect(api.success).not.toHaveBeenCalled()
    expect(api.create).toHaveBeenCalledTimes(1)
  })

  it('抽屉不再持有或展示原始清单，核对由顶层负责', async () => {
    api.create.mockRejectedValue(new Error('unknown'))
    const { host, state, registry } = await mount(); await submit(host)
    const before = api.inventory.mock.calls.length
    expect(button(host, '只读查看容器列表')).toBeUndefined()
    expect(host.querySelector('.unresolved-inventory')).toBeNull()
    expect(JSON.stringify(registry.entry.value)).not.toContain('containers')
    state.open = false; await flush()
    expect(api.inventory).toHaveBeenCalledTimes(before)
    expect(registry.blocked.value).toBe(true)
  })

  it('确认前取消预览释放会话槽，未派发配置不滞留', async () => {
    const { host, registry } = await mount(); await click(host, '仅创建')
    expect(registry.entry.value?.phase).toBe('confirming')
    await click(host, '返回修改')
    expect(registry.entry.value).toBeNull(); expect(api.create).not.toHaveBeenCalled()
    expect(button(host, '仅创建').disabled).toBe(false)
  })

  it('旧会话迟到未知不污染新实例', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const first = await mount(); await submit(first.host)
    first.app.unmount(); mounted.pop(); invalidateSession(); beginSession()
    const second = await mount()
    write.reject(new Error('old')); await flush()
    expect(second.registry.entry.value).toBeNull(); expect(button(second.host, '仅创建').disabled).toBe(false)
    expect(first.created).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled()
  })
})
