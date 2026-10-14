// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、选择器、Element Plus 和 API 层；只替换 fetch，不启动服务或连接 Docker。
// JSDOM 验证交互与请求边界，不证明浏览器布局、遮挡或像素绘制。
const collectedAt = '2026-10-14T00:00:00Z'
const resources = {
  collectedAt,
  networks: [],
  volumes: ['data-a', 'data-b', 'undefined', 'null'].map((name) => ({ name, driver: 'local', scope: 'local', mountpoint: `/volumes/${name}` })),
}
const inventory = {
  collectedAt,
  engine: { serverVersion: 'fixture', operatingSystem: 'fixture', architecture: 'fixture', containers: 0, containersRunning: 0, containersStopped: 0, images: 1 },
  containers: [],
  projects: [],
}
const image = (name: string): DockerImageSummary => ({ id: name, repoTags: [`${name}:latest`], repoDigests: [], sizeBytes: 1, createdAt: collectedAt, containers: 0 })
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
const createdResponse = (input: DockerContainerCreateInput) => json({ containerId: 'created-id', name: input.name || 'alpine', image: input.image, state: input.runContainer ? 'running' : 'stopped', created: true, started: input.runContainer, runContainer: input.runContainer }, 201)
const fetcher = vi.fn<typeof fetch>()
let respondToCreate: (input: DockerContainerCreateInput) => Response | Promise<Response>
let app: App | undefined
let host: HTMLDivElement | undefined

async function settle() { for (let count = 0; count < 30; count++) await nextTick() }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done }); return { promise, resolve } }
function createCalls() { return fetcher.mock.calls.filter(([path, init]) => path === '/api/v1/docker/containers' && init?.method === 'POST') }
function submittedInput() { return JSON.parse(String(createCalls()[0]![1]!.body)) as DockerContainerCreateInput }
function button(text: string) {
  const found = [...document.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.textContent?.trim() === text)
  expect(found, `按钮：${text}`).toBeDefined()
  return found!
}
async function click(text: string) { button(text).click(); await settle() }
function combobox(label: string) {
  const input = document.querySelector<HTMLInputElement>(`input[role="combobox"][aria-label="${label}"]`)
  expect(input, `选择器：${label}`).not.toBeNull()
  return input!
}
async function choose(label: string, value: string) {
  const input = combobox(label)
  input.closest<HTMLElement>('.el-select__wrapper')!.click()
  await settle()
  const popup = document.getElementById(input.getAttribute('aria-controls')!)!
  const option = [...popup.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === value)
  expect(option, `选项：${value}`).toBeDefined()
  option!.click()
  await settle()
}
async function edit(placeholder: string, value: string) {
  const input = document.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!
  expect(input).not.toBeNull()
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}
async function clearVolume() {
  const input = combobox('第 1 个 Docker 卷')
  input.focus()
  await settle()
  const clear = input.closest('.ncp-select')!.querySelector<HTMLElement>('.el-select__clear')
  expect(clear).not.toBeNull()
  clear!.click()
  await settle()
}
async function addVolume(name = 'data-a') {
  await click('添加挂载')
  await choose('第 1 个挂载类型', 'Docker 卷')
  await choose('第 1 个 Docker 卷', `${name} · local`)
  await edit('容器路径，例如 /data', '/data')
}
async function submitForm() {
  document.querySelector('form.container-form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await settle()
}
async function mount() {
  const state = reactive({ open: true, image: image('alpine') })
  const created = vi.fn()
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(CreateContainerDrawer, { modelValue: state.open, image: state.image, 'onUpdate:modelValue': (value: boolean) => { state.open = value }, onCreated: created }) })
  app.mount(host)
  await settle()
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual(['/api/v1/docker/resources', '/api/v1/docker/inventory'])
  expect(document.body.textContent).not.toContain('清单暂不可用')
  return { state, created }
}

beforeEach(() => {
  beginSession()
  fetcher.mockReset()
  respondToCreate = createdResponse
  fetcher.mockImplementation(async (path, init = {}) => {
    if (path === '/api/v1/docker/resources' && !init.method) return json(resources)
    if (path === '/api/v1/docker/inventory' && !init.method) return json(inventory)
    if (path === '/api/v1/docker/containers' && init.method === 'POST') return respondToCreate(JSON.parse(String(init.body)) as DockerContainerCreateInput)
    throw new Error(`非预期的 fixture 请求：${String(path)} ${init.method || 'GET'}`)
  })
  vi.stubGlobal('fetch', fetcher)
})

afterEach(async () => {
  app?.unmount()
  app = undefined
  host?.remove()
  host = undefined
  ElMessage.closeAll()
  invalidateSession()
  await settle()
  await vi.waitFor(() => expect(document.querySelector('.el-message')).toBeNull())
  vi.unstubAllGlobals()
})

describe('Docker 卷清空的真实创建流程', () => {
  it.each([false, true])('清空后拒绝空来源与旧确认，重选只提交新卷，runContainer=%s', async (runContainer) => {
    const { state, created } = await mount()
    await addVolume()
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain('volume data-a → /data')
    const staleConfirm = button(runContainer ? '确认创建并启动' : '确认创建')
    await click('返回修改')
    await clearVolume()

    for (const entry of runContainer ? ['创建并启动', '仅创建'] : ['仅创建', '创建并启动']) {
      await click(entry)
      expect(document.querySelector('.create-preview'), `清空后 ${entry} 不得生成预览`).toBeNull()
      expect(document.body.textContent).toContain('挂载项必须填写来源和绝对目标路径。')
    }
    await submitForm()
    staleConfirm.click()
    await settle()
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(0)
    expect(created).not.toHaveBeenCalled()
    expect(state.open).toBe(true)

    await choose('第 1 个 Docker 卷', 'data-b · local')
    await click(runContainer ? '创建并启动' : '仅创建')
    const preview = document.querySelector('.create-preview')!
    expect(preview.textContent).toContain('volume data-b → /data')
    expect(preview.textContent).not.toContain('volume data-a')
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ image: 'alpine:latest', runContainer, mounts: [{ type: 'volume', source: 'data-b', target: '/data', readOnly: false }] })
    expect(createCalls()[0]![1]).toMatchObject({ credentials: 'same-origin', redirect: 'error' })
    expect(created).toHaveBeenCalledExactlyOnceWith('created-id')
    expect(state.open).toBe(false)
  })

  it.each(['undefined', 'null'])('真实名为 %s 的卷仍可在清空后选择并原样提交', async (name) => {
    const { created } = await mount()
    await addVolume()
    await clearVolume()
    await choose('第 1 个 Docker 卷', `${name} · local`)
    await click('仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain(`volume ${name} → /data`)
    await click('确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().mounts).toEqual([{ type: 'volume', source: name, target: '/data', readOnly: false }])
    expect(created).toHaveBeenCalledExactlyOnceWith('created-id')
  })

  it('清空重选后的迟到成功只对账，不能关闭或覆盖重开的新镜像草稿', async () => {
    const pending = deferred<Response>()
    respondToCreate = () => pending.promise
    const { state, created } = await mount()
    await addVolume()
    await clearVolume()
    await choose('第 1 个 Docker 卷', 'data-b · local')
    await click('仅创建')
    await click('确认创建')
    const original = submittedInput()
    state.open = false
    await settle()
    state.image = image('redis')
    state.open = true
    await settle()
    await edit('例如 mysql-1', 'draft-b')
    await addVolume('data-a')
    expect(button('仅创建').disabled).toBe(true)
    expect(button('创建并启动').disabled).toBe(true)
    await submitForm()
    expect(createCalls()).toHaveLength(1)
    pending.resolve(createdResponse(original))
    await settle()
    expect(state.open).toBe(true)
    expect(document.querySelector<HTMLInputElement>('input[placeholder="例如 mysql-1"]')!.value).toBe('draft-b')
    expect(button('仅创建').disabled).toBe(false)
    expect(created).toHaveBeenCalledExactlyOnceWith('created-id')
    expect(document.querySelector('.el-message--success')).toBeNull()
    await click('仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain('redis:latest')
    expect(document.querySelector('.create-preview')?.textContent).toContain('volume data-a → /data')
    expect(original.mounts).toEqual([{ type: 'volume', source: 'data-b', target: '/data', readOnly: false }])
  })

  it('真实 API 将不完整成功转为未知；清空重选、重开和只读清单均不能解锁再次创建', async () => {
    const pending = deferred<Response>()
    respondToCreate = () => pending.promise
    const { state, created } = await mount()
    await addVolume()
    await click('创建并启动')
    const staleConfirm = button('确认创建并启动')
    staleConfirm.click()
    await settle()
    await clearVolume()
    await choose('第 1 个 Docker 卷', 'data-b · local')
    await edit('例如 mysql-1', 'edited-draft')
    pending.resolve(json({ containerId: 'unconfirmed-id' }, 201))
    await settle()
    expect(document.querySelector('[aria-label="创建结果未知"]')?.textContent).toContain('容器可能已经创建')
    expect(document.querySelector('[aria-label="创建结果未知"]')?.textContent).not.toContain('edited-draft')
    expect(submittedInput().mounts?.[0]?.source).toBe('data-a')
    expect(button('仅创建').disabled).toBe(true)
    expect(button('创建并启动').disabled).toBe(true)
    await submitForm()
    staleConfirm.click()
    await settle()
    state.open = false
    await settle()
    state.open = true
    await settle()
    await click('只读查看容器列表')
    expect(document.body.textContent).toContain('本次读取 0 个容器；创建结果仍未知。')
    expect(button('仅创建').disabled).toBe(true)
    expect(button('创建并启动').disabled).toBe(true)
    await submitForm()
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(1)
    expect(created).not.toHaveBeenCalled()
    expect(document.querySelector('.el-message--success')).toBeNull()
  })
})
