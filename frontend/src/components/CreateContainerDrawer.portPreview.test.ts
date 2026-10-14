// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、端口编辑、确认弹窗及 API 编解码；只替换 fetch。
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
async function edit(index: number, placeholder: string, value: string, kind = 'port') {
  const input = document.querySelectorAll<HTMLElement>(`.repeat-row--${kind}`)[index]!.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  input.dispatchEvent(new Event('change', { bubbles: true }))
  input.dispatchEvent(new Event('blur', { bubbles: true }))
  await settle()
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

async function chooseProtocol(index: number, value: string) {
  const input = document.querySelectorAll<HTMLElement>('.repeat-row--port')[index]!.querySelector<HTMLInputElement>('input[role="combobox"]')!
  input.closest<HTMLElement>('.el-select__wrapper')!.click()
  await settle()
  const popup = document.getElementById(input.getAttribute('aria-controls')!)!
  const option = [...popup.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim().toLowerCase() === value)
  expect(option).toBeDefined()
  option!.click()
  await settle()
}
function previewRows() {
  return [...document.querySelectorAll('.create-preview ol[aria-label="端口映射"] li')].map((row) => [...row.querySelectorAll('span')].map((field) => field.textContent))
}

describe('创建确认的端口绑定预览', () => {
  it.each([false, true])('区分主机 IP、IPv6、默认配置和协议，确认提交同一规范化配置，runContainer=%s', async (runContainer) => {
    await mount()
    const inputs = [
      { hostIp: ' 127.0.0.1 ', hostPort: '8080', containerPort: 80, protocol: 'tcp' },
      { hostIp: '0.0.0.0', hostPort: '8081', containerPort: 81, protocol: 'udp' },
      { hostIp: ' 2001:db8::1 ', hostPort: '8082', containerPort: 82, protocol: 'sctp' },
      { hostIp: '', hostPort: '', containerPort: 83, protocol: 'tcp' },
      { hostIp: '   ', hostPort: '0', containerPort: 84, protocol: 'udp' },
    ]
    for (const [index, input] of inputs.entries()) {
      await click('添加端口映射')
      await edit(index, '主机 IP（可选）', input.hostIp)
      if (input.hostPort) await edit(index, '主机端口', input.hostPort)
      await edit(index, '容器端口', String(input.containerPort))
      await chooseProtocol(index, input.protocol)
    }
    const expectedPreview = [
      ['主机 IP：127.0.0.1', '主机端口：8080', '容器端口：80/tcp'],
      ['主机 IP：0.0.0.0', '主机端口：8081', '容器端口：81/udp'],
      ['主机 IP：2001:db8::1', '主机端口：8082', '容器端口：82/sctp'],
      ['主机 IP：未指定（由 Docker 默认配置决定）', '主机端口：随机', '容器端口：83/tcp'],
      ['主机 IP：未指定（由 Docker 默认配置决定）', '主机端口：随机', '容器端口：84/udp'],
    ]
    const openLabel = runContainer ? '创建并启动' : '仅创建'
    await click(openLabel)
    expect(previewRows()).toEqual(expectedPreview)
    expect(createCalls()).toHaveLength(0)
    await click('返回修改')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(0)
    await click(openLabel)
    expect(previewRows()).toEqual(expectedPreview)
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().runContainer).toBe(runContainer)
    expect(submittedInput().ports).toEqual([
      { hostIp: '127.0.0.1', hostPort: 8080, containerPort: 80, protocol: 'tcp' },
      { hostIp: '0.0.0.0', hostPort: 8081, containerPort: 81, protocol: 'udp' },
      { hostIp: '2001:db8::1', hostPort: 8082, containerPort: 82, protocol: 'sctp' },
      { containerPort: 83, protocol: 'tcp' },
      { hostPort: 0, containerPort: 84, protocol: 'udp' },
    ])
    await settle()
    expect(createCalls()).toHaveLength(1)
  })

  it('没有端口映射时显示未映射', async () => {
    await mount()
    await click('仅创建')
    const label = [...document.querySelectorAll('.create-preview dt')].find((node) => node.textContent === '端口')!
    expect(label.nextElementSibling?.textContent?.trim()).toBe('未映射')
    expect(previewRows()).toEqual([])
    await click('返回修改')
    expect(createCalls()).toHaveLength(0)
  })
})
