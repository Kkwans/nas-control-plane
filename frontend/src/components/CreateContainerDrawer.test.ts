// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, reactive, type App } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { CreateContainerOutcomeUnknownError, type DockerImageSummary } from '@/api/system'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

const api = vi.hoisted(() => ({ create: vi.fn(), resources: vi.fn(), inventory: vi.fn(), paths: vi.fn(), success: vi.fn(), error: vi.fn(), warning: vi.fn() }))
vi.mock('@/api/system', () => ({ NcpApiError: class extends Error {}, CreateContainerOutcomeUnknownError: class extends Error {}, createDockerContainer: api.create, requestDockerResources: api.resources, requestDockerInventory: api.inventory, requestPathEntries: api.paths }))
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
const result = { containerId: 'container-A', name: 'alpine', started: false }
const mounted: { app: App; host: HTMLDivElement }[] = []
async function flush() { await Promise.resolve(); await Promise.resolve(); await nextTick() }
async function mount() {
  const state = reactive({ open: true, image: image('alpine') })
  const created = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const app = createApp({ render: () => h(CreateContainerDrawer, { modelValue: state.open, image: state.image, 'onUpdate:modelValue': (value: boolean) => { state.open = value }, onCreated: created }) })
  app.mount(host); mounted.push({ app, host }); await flush()
  return { state, host, created, app }
}
function button(host: HTMLElement, text: string) { return [...host.querySelectorAll('button')].find((item) => item.textContent === text)! }
async function click(host: HTMLElement, text: string) { button(host, text).click(); await flush() }
async function submit(host: HTMLElement) { await click(host, '仅创建'); await click(host, '确认创建') }
async function edit(input: HTMLInputElement, value: string) { input.value = value; input.dispatchEvent(new Event('input')); await flush() }
function name(host: HTMLElement) { return host.querySelector<HTMLInputElement>('[placeholder="例如 mysql-1"]')! }
beforeEach(() => { vi.resetAllMocks(); beginSession(); api.resources.mockResolvedValue(emptyResources); api.inventory.mockResolvedValue({ containers: [] }) })
afterEach(() => { for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } })

describe('创建容器草稿所有权', () => {
  it('自定义标题保留抽屉名称与二级标题，切换镜像时同步更新', async () => {
    const { state, host } = await mount()
    expect(host.querySelector('.create-container-drawer')?.getAttribute('data-title')).toBe('从 alpine:latest 创建容器')
    expect(host.querySelector('.drawer-heading h2')?.textContent).toBe('从 alpine:latest 创建容器')
    state.image = image('redis'); await flush()
    expect(host.querySelector('.create-container-drawer')?.getAttribute('data-title')).toBe('从 redis:latest 创建容器')
    expect(host.querySelector('.drawer-heading h2')?.textContent).toBe('从 redis:latest 创建容器')
  })

  it.each(['success', 'failure'])('A 提交关闭重开 B 后 A %s 不覆盖 B，锁只在 POST 完结释放', async (outcome) => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { state, host, created } = await mount(); await submit(host)
    expect(host.textContent).toContain('提交后关闭不会取消容器创建')
    state.open = false; await flush(); state.open = true; await flush(); await edit(name(host), 'draft-B')
    expect(button(host, '仅创建').disabled).toBe(true)
    if (outcome === 'success') write.resolve(result); else write.reject(new Error('late failure'))
    await flush()
    expect(state.open).toBe(true); expect(name(host).value).toBe('draft-B'); expect(button(host, '仅创建').disabled).toBe(false)
    expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled(); expect(api.create).toHaveBeenCalledTimes(1)
    expect(created).toHaveBeenCalledTimes(outcome === 'success' ? 1 : 0)
  })

  it('当前草稿正常成功关闭并只创建一次，消费预览不可重放', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, state, created } = await mount(); await submit(host)
    expect(host.querySelector('[data-title="确认容器配置"]')).toBeNull()
    write.resolve(result); await flush()
    expect(created).toHaveBeenCalledExactlyOnceWith('container-A'); expect(state.open).toBe(false); expect(api.success).toHaveBeenCalledTimes(1)
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


  it('同开窗换镜像时迟到 POST 成功只对账，不能关闭新镜像草稿', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { state, host, created } = await mount(); await submit(host)
    state.image = image('redis'); await flush(); expect(button(host, '仅创建').disabled).toBe(true)
    write.resolve(result); await flush()
    expect(created).toHaveBeenCalledExactlyOnceWith('container-A'); expect(state.open).toBe(true)
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


describe('未确认的创建尝试', () => {
  it('编辑中迟到未知属于原尝试，保留最小摘要并阻止所有创建入口', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, created, state } = await mount()
    await click(host, '添加环境变量')
    await edit(host.querySelector<HTMLInputElement>('[placeholder="变量，例如 APP_MODE"]')!, 'SECRET')
    await edit(host.querySelector<HTMLInputElement>('[placeholder="值"]')!, 'private-value')
    await click(host, '创建并启动')
    const staleConfirm = button(host, '确认创建并启动')
    staleConfirm.click(); await flush()
    await edit(name(host), 'edited-B')
    write.reject(new CreateContainerOutcomeUnknownError()); await flush()
    const notice = host.querySelector('[aria-label="创建结果未知"]')!
    expect(notice.textContent).toContain('无法确认本次容器创建是否完成')
    expect(notice.textContent).not.toContain('成功响应')
    expect(notice.textContent).toContain('alpine:latest')
    expect(notice.textContent).toContain('创建并启动')
    expect(notice.textContent).not.toContain('edited-B')
    expect(notice.textContent).not.toContain('private-value')
    expect(notice.textContent).not.toContain('SECRET')
    expect(name(host).value).toBe('edited-B'); expect(state.open).toBe(true)
    expect(button(host, '仅创建').disabled).toBe(true); expect(button(host, '创建并启动').disabled).toBe(true)
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    staleConfirm.click(); await flush()
    expect(host.querySelector('[data-title="确认容器配置"]')).toBeNull()
    expect(api.create).toHaveBeenCalledTimes(1); expect(created).not.toHaveBeenCalled()
    expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled()
  })

  it('A 关闭后 B 收到未知不改 B，关闭重开或切镜像仍保持页面级锁', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, state, created } = await mount(); await submit(host)
    state.open = false; await flush(); state.open = true; state.image = image('redis'); await flush()
    await edit(name(host), 'draft-B')
    write.reject(new CreateContainerOutcomeUnknownError()); await flush()
    expect(name(host).value).toBe('draft-B'); expect(state.open).toBe(true)
    expect(host.querySelector('[aria-label="创建结果未知"]')?.textContent).toContain('alpine:latest')
    state.open = false; await flush(); state.open = true; await flush()
    expect(host.textContent).toContain('容器可能已经创建'); expect(button(host, '仅创建').disabled).toBe(true)
    expect(created).not.toHaveBeenCalled(); expect(api.create).toHaveBeenCalledTimes(1)
  })

  it('关闭期间收到未知，重新打开仍可见且不能重试', async () => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, state } = await mount(); await submit(host)
    state.open = false; await flush(); write.reject(new CreateContainerOutcomeUnknownError()); await flush()
    state.open = true; await flush()
    expect(host.textContent).toContain('容器可能已经创建'); expect(button(host, '仅创建').disabled).toBe(true)
  })

  it('只读清单最新结果胜出；同名、空列表和失败始终不证明结果或解锁', async () => {
    api.create.mockRejectedValue(new CreateContainerOutcomeUnknownError())
    const { host, created } = await mount(); await submit(host)
    const a = deferred(); const b = deferred()
    api.inventory.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    await click(host, '只读查看容器列表'); const signal = api.inventory.mock.calls.at(-1)![1] as AbortSignal
    await click(host, '只读查看容器列表'); expect(signal.aborted).toBe(true)
    b.resolve({ containers: [{ id: 'same-name-but-unproven', name: 'alpine', image: 'alpine:latest', state: 'running', status: 'Up 1 second' }] }); await flush()
    expect(host.textContent).toContain('本次读取 1 个容器'); expect(host.textContent).toContain('Up 1 second')
    a.resolve({ containers: [{ id: 'old', name: 'old-read', image: 'old', state: 'exited', status: 'old' }] }); await flush()
    expect(host.textContent).not.toContain('old-read')
    api.inventory.mockResolvedValueOnce({ containers: [] }); await click(host, '只读查看容器列表')
    expect(host.textContent).toContain('本次读取 0 个容器'); expect(button(host, '仅创建').disabled).toBe(true)
    api.inventory.mockRejectedValueOnce(new Error('private-server-diagnostic')); await click(host, '只读查看容器列表')
    expect(host.textContent).toContain('列表读取失败，创建结果仍未知'); expect(host.textContent).not.toContain('private-server-diagnostic')
    expect(button(host, '创建并启动').disabled).toBe(true); expect(api.create).toHaveBeenCalledTimes(1)
    expect(created).not.toHaveBeenCalled(); expect(api.success).not.toHaveBeenCalled()
  })

  const validVerificationRow = { id: 'observed-id', name: 'observed-name', image: 'alpine:latest', state: 'running', status: 'Up' }
  const malformedVerificationRows = [
    { label: 'null', row: null },
    { label: '数组', row: [] },
    { label: '非对象', row: 'private-invalid-row' },
    ...Object.keys(validVerificationRow).flatMap((field) => [
      { label: `缺少 ${field}`, row: Object.fromEntries(Object.entries(validVerificationRow).filter(([key]) => key !== field)) },
      { label: `${field} 非字符串`, row: { ...validVerificationRow, [field]: { private: 'private-invalid-row' } } },
    ]),
  ]
  it.each(malformedVerificationRows)('清单包含 $label 时整次读取失败，仍保留未知与创建锁', async ({ row }) => {
    api.create.mockRejectedValue(new CreateContainerOutcomeUnknownError())
    const { host, created } = await mount(); await submit(host)
    const read = deferred(); api.inventory.mockReturnValueOnce(read.promise)
    await click(host, '只读查看容器列表')
    read.resolve({ containers: [validVerificationRow, row] }); await flush()
    expect(host.textContent).toContain('容器列表读取失败，创建结果仍未知')
    expect(host.textContent).toContain('容器可能已经创建')
    expect(host.textContent).not.toContain('observed-name'); expect(host.textContent).not.toContain('private-invalid-row')
    expect(host.querySelector('.unresolved-inventory')).toBeNull()
    expect(button(host, '仅创建').disabled).toBe(true); expect(button(host, '创建并启动').disabled).toBe(true)
    expect(api.create).toHaveBeenCalledTimes(1); expect(created).not.toHaveBeenCalled(); expect(api.success).not.toHaveBeenCalled()
  })

  it('只读展示保存已验证字段的独立副本，不保留原始清单行', async () => {
    api.create.mockRejectedValue(new CreateContainerOutcomeUnknownError())
    const { host } = await mount(); await submit(host)
    const row = { ...validVerificationRow, environment: { SECRET: 'private-config' } }
    api.inventory.mockResolvedValueOnce({ containers: [row] }); await click(host, '只读查看容器列表')
    row.name = 'mutated-raw-name'; row.environment.SECRET = 'mutated-private-config'
    await edit(name(host), 'force-rerender')
    expect(host.querySelector('.unresolved-inventory')?.textContent).toContain('observed-name')
    expect(host.textContent).not.toContain('mutated-raw-name'); expect(host.textContent).not.toContain('private-config')
    expect(button(host, '仅创建').disabled).toBe(true)
  })

  it.each(['close', 'session', 'unmount'])('%s 使未确认尝试的迟到只读清单失效', async (boundary) => {
    api.create.mockRejectedValue(new CreateContainerOutcomeUnknownError())
    const { host, state, app } = await mount(); await submit(host)
    const read = deferred(); api.inventory.mockReturnValueOnce(read.promise)
    await click(host, '只读查看容器列表'); const signal = api.inventory.mock.calls.at(-1)![1] as AbortSignal
    if (boundary === 'close') { state.open = false; await flush(); state.open = true; await flush() }
    else if (boundary === 'session') invalidateSession()
    else { app.unmount(); mounted.pop() }
    expect(signal.aborted).toBe(true)
    read.resolve({ containers: [{ id: 'late', name: 'late-inventory', image: 'old', state: 'running', status: 'Up' }] }); await flush()
    expect(host.textContent).not.toContain('late-inventory')
    expect(host.textContent).not.toContain('正在只读获取')
    if (boundary === 'close') expect(button(host, '仅创建').disabled).toBe(true)
    else expect(host.textContent).not.toContain('容器可能已经创建')
  })

  it('关闭时登出也清理未确认尝试，下一会话不继承旧提示', async () => {
    api.create.mockRejectedValue(new CreateContainerOutcomeUnknownError())
    const { host, state } = await mount(); await submit(host)
    state.open = false; await flush(); invalidateSession(); beginSession(); state.open = true; await flush()
    expect(host.textContent).not.toContain('容器可能已经创建'); expect(button(host, '仅创建').disabled).toBe(false)
  })

  it.each(['session', 'unmount'])('%s 后迟到未知不通知、不发 created', async (boundary) => {
    const write = deferred(); api.create.mockReturnValue(write.promise)
    const { host, app, created } = await mount(); await submit(host)
    if (boundary === 'session') invalidateSession(); else { app.unmount(); mounted.pop() }
    write.reject(new CreateContainerOutcomeUnknownError()); await flush()
    expect(host.textContent).not.toContain('容器可能已经创建'); expect(created).not.toHaveBeenCalled()
    expect(api.success).not.toHaveBeenCalled(); expect(api.error).not.toHaveBeenCalled()
  })
})
