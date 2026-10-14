// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、权限编辑、确认弹窗及 API 编解码；只替换 fetch。
// 不启动浏览器、HTTP/RPC listener 或 Docker，JSDOM 不替代浏览器布局验证。
const collectedAt = '2026-10-14T00:00:00Z'
const image: DockerImageSummary = { id: 'alpine', repoTags: ['alpine:latest'], repoDigests: [], sizeBytes: 1, createdAt: collectedAt, containers: 0 }
const inventory = {
  collectedAt,
  engine: { serverVersion: 'fixture', operatingSystem: 'fixture', architecture: 'fixture', containers: 0, containersRunning: 0, containersStopped: 0, images: 1 },
  containers: [],
  projects: [],
}
const fetcher = vi.fn<typeof fetch>()
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
let app: App | undefined
let host: HTMLDivElement | undefined

async function settle() { for (let count = 0; count < 30; count++) await nextTick() }
function createCalls() { return fetcher.mock.calls.filter(([path, init]) => path === '/api/v1/docker/containers' && init?.method === 'POST') }
function submittedInput() { return JSON.parse(String(createCalls()[0]![1]!.body)) as DockerContainerCreateInput }
function button(text: string) {
  const found = [...document.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.textContent?.trim() === text)
  expect(found, `按钮：${text}`).toBeDefined()
  return found!
}
async function click(text: string) { button(text).click(); await settle() }
async function edit(placeholder: string, value: string) {
  const input = document.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}
async function toggleAdvanced() {
  document.querySelector<HTMLButtonElement>('.advanced-toggle')!.click()
  await settle()
}
async function togglePrivileged() {
  document.querySelector<HTMLElement>('.advanced-body .el-switch')!.click()
  await settle()
}
function previewValue(label: string) {
  return [...document.querySelectorAll('.create-preview dt')].find((node) => node.textContent === label)?.nextElementSibling?.textContent
}
async function mount() {
  const state = reactive({ open: true })
  const created = vi.fn()
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(CreateContainerDrawer, { modelValue: state.open, image, 'onUpdate:modelValue': (value: boolean) => { state.open = value }, onCreated: created }) })
  app.mount(host)
  await settle()
  expect(fetcher.mock.calls.map(([path]) => path)).toEqual(['/api/v1/docker/resources', '/api/v1/docker/inventory'])
  return { state, created }
}

beforeEach(() => {
  beginSession()
  fetcher.mockReset()
  fetcher.mockImplementation(async (path, init = {}) => {
    if (path === '/api/v1/docker/resources' && !init.method) return json({ collectedAt, networks: [], volumes: [] })
    if (path === '/api/v1/docker/inventory' && !init.method) return json(inventory)
    if (path === '/api/v1/docker/containers' && init.method === 'POST') {
      const input = JSON.parse(String(init.body)) as DockerContainerCreateInput
      return json({ containerId: 'created-id', name: input.name || 'alpine', image: input.image, state: input.runContainer ? 'running' : 'stopped', created: true, started: input.runContainer, runContainer: input.runContainer }, 201)
    }
    throw new Error(`非预期 fixture 请求：${String(path)} ${init.method || 'GET'}`)
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

describe('创建确认的权限预览', () => {
  it.each([false, true])('明确增加与移除方向及特权状态，取消无 POST，确认使用规范化快照，runContainer=%s', async (runContainer) => {
    await mount()
    await toggleAdvanced()
    await edit('NET_ADMIN,SYS_PTRACE', '  NET_ADMIN, , SYS_PTRACE  ,')
    await edit('SYS_ADMIN', '  NET_ADMIN , SYS_ADMIN , ')
    await togglePrivileged()
    const openLabel = runContainer ? '创建并启动' : '仅创建'
    await click(openLabel)
    expect(previewValue('增加 Capabilities')).toBe('["NET_ADMIN","SYS_PTRACE"]')
    expect(previewValue('移除 Capabilities')).toBe('["NET_ADMIN","SYS_ADMIN"]')
    expect(previewValue('特权模式')).toBe('是（privileged）')
    expect(createCalls()).toHaveLength(0)
    await click('返回修改')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(0)
    await togglePrivileged()
    await click(openLabel)
    expect(previewValue('特权模式')).toBe('否')
    expect(previewValue('增加 Capabilities')).toBe('["NET_ADMIN","SYS_PTRACE"]')
    expect(previewValue('移除 Capabilities')).toBe('["NET_ADMIN","SYS_ADMIN"]')
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ privileged: false, capAdd: ['NET_ADMIN', 'SYS_PTRACE'], capDrop: ['NET_ADMIN', 'SYS_ADMIN'], runContainer })
    await settle()
    expect(createCalls()).toHaveLength(1)
  })

  it('默认和空白列表明确显示无，设备与 GPU 请求计数保留', async () => {
    await mount()
    await click('仅创建')
    expect(previewValue('特权模式')).toBe('否')
    expect(previewValue('增加 Capabilities')).toBe('无')
    expect(previewValue('移除 Capabilities')).toBe('无')
    expect(previewValue('硬件请求')).toBe('设备映射：0 项；GPU 请求：0 项')
    await click('返回修改')
    await toggleAdvanced()
    await edit('NET_ADMIN,SYS_PTRACE', ' , , ')
    await edit('SYS_ADMIN', '   ')
    await click('添加设备')
    await edit('主机设备 /dev/...', '/dev/example')
    await edit('容器设备 /dev/...', '/dev/example')
    const gpu = [...document.querySelectorAll<HTMLElement>('.advanced-body .switch-line')].find((node) => node.querySelector('strong')?.textContent === 'GPU')!
    gpu.querySelector<HTMLElement>('.el-switch')!.click()
    await settle()
    await click('仅创建')
    expect(previewValue('增加 Capabilities')).toBe('无')
    expect(previewValue('移除 Capabilities')).toBe('无')
    expect(previewValue('硬件请求')).toBe('设备映射：1 项；GPU 请求：1 项')
    expect(createCalls()).toHaveLength(0)
    await click('返回修改')
    expect(createCalls()).toHaveLength(0)
  })

  it('能力名称按文本安全显示且不改变提交值', async () => {
    await mount()
    await toggleAdvanced()
    const literal = '<b>NET  ADMIN</b>'
    await edit('NET_ADMIN,SYS_PTRACE', literal)
    await edit('SYS_ADMIN', literal)
    await togglePrivileged()
    await click('仅创建')
    expect(previewValue('增加 Capabilities')).toBe(JSON.stringify([literal]))
    expect(previewValue('移除 Capabilities')).toBe(JSON.stringify([literal]))
    expect(document.querySelectorAll('.create-preview b')).toHaveLength(0)
    expect([...document.querySelectorAll('.create-preview code.capability-preview')].filter((node) => node.textContent === JSON.stringify([literal]))).toHaveLength(2)
    expect(previewValue('特权模式')).toBe('是（privileged）')
    await click('确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ privileged: true, capAdd: [literal], capDrop: [literal] })
  })
})
