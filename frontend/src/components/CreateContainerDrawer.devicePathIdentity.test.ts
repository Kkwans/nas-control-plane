// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、设备编辑、确认弹窗和 API 编解码；只替换 fetch。
// 不启动浏览器、HTTP/RPC listener 或 Docker，JSDOM 不证明空白字符的视觉可辨识性。
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
async function edit(index: number, placeholder: string, value: string) {
  const input = document.querySelectorAll<HTMLElement>('.repeat-row--device')[index]!.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}
async function addDevice() {
  if (!document.querySelector('.repeat-row--device')) {
    const toggle = document.querySelector<HTMLButtonElement>('.advanced-toggle')!
    if (toggle.getAttribute('aria-expanded') === 'false') { toggle.click(); await settle() }
  }
  await click('添加设备')
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

describe('创建设备映射保留路径身份', () => {
  it.each([false, true])('真实表单区分尾部空白并提交原值，runContainer=%s', async (runContainer) => {
    const { created } = await mount()
    const expected = []
    for (const suffix of ['', ' ', '\t', '\u00a0', '\u3000']) {
      const index = expected.length
      await addDevice()
      await edit(index, '主机设备 /dev/...', '/dev/source' + suffix)
      await edit(index, '容器设备 /dev/...', '/dev/target' + suffix)
      await edit(index, 'rwm', ' RW ')
      expected.push({ hostPath: '/dev/source' + suffix, containerPath: '/dev/target' + suffix, cgroupPermissions: 'RW' })
    }
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(document.querySelector('.create-preview')).not.toBeNull()
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().devices).toEqual(expected)
    expect(submittedInput().runContainer).toBe(runContainer)
    expect(created).toHaveBeenCalledTimes(1)
  })

  it.each(['主机设备 /dev/...', '容器设备 /dev/...'])('%s 拒绝前导空白', async (field) => {
    await mount()
    await addDevice()
    await edit(0, '主机设备 /dev/...', '/dev/source')
    await edit(0, '容器设备 /dev/...', '/dev/target')
    for (const prefix of [' ', '\t', '\u00a0', '\u3000']) {
      await edit(0, field, prefix + '/dev/selected')
      await click('仅创建')
      expect(document.querySelector('.create-preview')).toBeNull()
    }
    expect(createCalls()).toHaveLength(0)
    expect(document.body.textContent).toContain('设备路径必须使用绝对路径。')
  })

  it('相同含尾空格目标仍拒绝重复', async () => {
    await mount()
    for (let index = 0; index < 2; index++) {
      await addDevice()
      await edit(index, '主机设备 /dev/...', '/dev/source' + index)
      await edit(index, '容器设备 /dev/...', '/dev/target ')
    }
    await click('仅创建')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(0)
    expect(document.body.textContent).toContain('同一个容器设备路径不能重复映射。')
  })
})
