// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、网络编辑、确认弹窗及 API 编解码；只替换 fetch。
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
async function toggleNetwork() {
  document.querySelector<HTMLElement>('[aria-label="启用自定义网络"]')!.click()
  await settle()
}
async function toggleDedicated() {
  document.querySelector<HTMLElement>('.network-mode-switch .el-switch')!.click()
  await settle()
}
async function dedicated(values: { name: string; driver?: string; subnet: string; gateway?: string; ip?: string }) {
  await toggleNetwork()
  await toggleDedicated()
  await edit('例如 media-net', values.name)
  await edit('默认 bridge', values.driver || '')
  await edit('172.30.0.0/24', values.subnet)
  await edit('172.30.0.1', values.gateway || '')
  await edit('留空自动分配', values.ip || '')
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

const createOrReuse = '仅在同名网络不存在时创建；已有网络须匹配驱动、子网及显式网关，否则失败，不修改现有网络'
const networkDetails = ['网络策略', '网络驱动', '子网', '网关', '容器固定 IP']

describe('创建确认的网络快照', () => {
  it.each([false, true])('未配置时使用 Docker 默认网络，取消零提交，确认一次，runContainer=%s', async (runContainer) => {
    await mount()
    const open = runContainer ? '创建并启动' : '仅创建'
    await click(open)
    expect(previewValue('网络')).toBe('Docker 默认网络')
    for (const label of networkDetails) expect(previewValue(label)).toBeUndefined()
    expect(createCalls()).toHaveLength(0)
    await click('返回修改')
    expect(createCalls()).toHaveLength(0)
    await click(open)
    const confirm = button(runContainer ? '确认创建并启动' : '确认创建')
    confirm.click()
    confirm.click()
    await settle()
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).not.toHaveProperty('network')
    expect(submittedInput().runContainer).toBe(runContainer)
  })

  it.each([false, true])('仅名称连接已有网络，不误报创建或驱动覆盖，runContainer=%s', async (runContainer) => {
    await mount()
    await toggleNetwork()
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(previewValue('网络')).toBe('"bridge"')
    expect(previewValue('网络策略')).toBe('连接现有网络（不创建或修改网络）')
    for (const label of networkDetails.slice(1)) expect(previewValue(label)).toBeUndefined()
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().network).toEqual({ name: 'bridge' })
    expect(submittedInput().runContainer).toBe(runContainer)
  })

  it.each([false, true])('子网请求展示有效默认驱动及条件网关行为，runContainer=%s', async (runContainer) => {
    await mount()
    await dedicated({ name: '  app-net  ', subnet: '  172.30.0.0/24  ' })
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(previewValue('网络')).toBe('"app-net"')
    expect(previewValue('网络策略')).toBe(createOrReuse)
    expect(previewValue('网络驱动')).toBe('bridge（默认）')
    expect(previewValue('子网')).toBe('"172.30.0.0/24"')
    expect(previewValue('网关')).toBe('新建时由 Docker 分配；复用时保留已有网关')
    expect(previewValue('容器固定 IP')).toBe('自动分配')
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().network).toEqual({ name: 'app-net', subnet: '172.30.0.0/24' })
    expect(submittedInput().runContainer).toBe(runContainer)
  })

  it.each([false, true].flatMap((runContainer) => [
    { runContainer, name: 'ipv4-net', driver: 'macvlan', subnet: '172.30.0.0/24', gateway: '172.30.0.1', ip: '172.30.0.42' },
    { runContainer, name: 'ipv6-net', driver: 'bridge', subnet: 'fd42:1234:5678:9abc::/64', gateway: 'fd42:1234:5678:9abc::1', ip: 'fd42:1234:5678:9abc::1234' },
  ]))('显式 $name 字段逐项展示修剪后请求，runContainer=$runContainer', async ({ runContainer, ...network }) => {
    await mount()
    await dedicated(Object.fromEntries(Object.entries(network).map(([key, value]) => [key, `  ${value}  `])) as typeof network)
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(previewValue('网络策略')).toBe(createOrReuse)
    for (const [label, value] of [['网络', network.name], ['网络驱动', network.driver], ['子网', network.subnet], ['网关', network.gateway], ['容器固定 IP', network.ip]]) {
      expect(previewValue(label!)).toBe(JSON.stringify(value))
    }
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().network).toEqual(network)
    expect(submittedInput().runContainer).toBe(runContainer)
  })

  it.each([false, true])('编辑使旧快照失效，返回修改移除高级字段，runContainer=%s', async (runContainer) => {
    await mount()
    await dedicated({ name: 'app-net', driver: 'macvlan', subnet: '172.30.0.0/24', gateway: '172.30.0.1', ip: '172.30.0.42' })
    const open = runContainer ? '创建并启动' : '仅创建'
    await click(open)
    expect(previewValue('网关')).toBe('"172.30.0.1"')
    // 从真实 DOM 修改背景表单；旧预览必须失效，不能转为尚未确认的新配置。
    await edit('172.30.0.1', '172.30.0.2')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(0)
    await click(open)
    expect(previewValue('网关')).toBe('"172.30.0.2"')
    await click('返回修改')
    expect(createCalls()).toHaveLength(0)
    await toggleDedicated()
    await click(open)
    expect(previewValue('网络')).toBe('"app-net"')
    expect(previewValue('网络策略')).toBe('连接现有网络（不创建或修改网络）')
    for (const label of networkDetails.slice(1)) expect(previewValue(label)).toBeUndefined()
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().network).toEqual({ name: 'app-net' })
    expect(submittedInput().runContainer).toBe(runContainer)
  })
})
