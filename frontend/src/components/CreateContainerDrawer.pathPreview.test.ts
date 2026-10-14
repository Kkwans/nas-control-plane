// @vitest-environment jsdom

import drawerSource from './CreateContainerDrawer.vue?raw'
import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、挂载和设备编辑、确认弹窗及 API 编解码；只替换 fetch。
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
async function edit(index: number, placeholder: string, value: string, kind = 'device') {
  const input = document.querySelectorAll<HTMLElement>(`.repeat-row--${kind}`)[index]!.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!
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

async function chooseType(index: number, value: string) {
  const input = document.querySelectorAll<HTMLElement>('.repeat-row--mount')[index]!.querySelector<HTMLInputElement>('input[role="combobox"]')!
  input.closest<HTMLElement>('.el-select__wrapper')!.click()
  await settle()
  const popup = document.getElementById(input.getAttribute('aria-controls')!)!
  const option = [...popup.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === value)
  expect(option).toBeDefined()
  option!.click()
  await settle()
}
function paths(label: string) {
  return [...document.querySelectorAll(`ol[aria-label="${label}"] .path-preview`)].map((node) => node.textContent)
}

describe('创建确认的精确路径预览', () => {
  it('路径样式保留连续空白并允许长路径换行（静态合同，不替代浏览器验证）', () => {
    expect(drawerSource).toMatch(/\.path-preview\s*\{[^}]*white-space:pre-wrap[^}]*overflow-wrap:anywhere/)
  })

  it.each([false, true])('分行标明挂载和设备的精确边界，预览不修改提交，runContainer=%s', async (runContainer) => {
    await mount()
    const mounts = []
    const devices = []
    for (const suffix of ['', '  ', '\t', '\u00a0', '\u3000', '\\"<b>']) {
      const index = mounts.length
      await click('添加挂载')
      await edit(index, '/volume2/data', '/  source' + suffix, 'mount')
      await edit(index, '容器路径，例如 /data', '/target' + suffix, 'mount')
      mounts.push({ type: 'bind', source: '/  source' + suffix, target: '/target' + suffix, readOnly: false })
      await addDevice()
      await edit(index, '主机设备 /dev/...', '/dev/source' + suffix)
      await edit(index, '容器设备 /dev/...', '/dev/target' + suffix)
      devices.push({ hostPath: '/dev/source' + suffix, containerPath: '/dev/target' + suffix, cgroupPermissions: 'rwm' })
    }
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(paths('挂载映射')).toEqual(mounts.flatMap((mount) => [JSON.stringify(mount.source), JSON.stringify(mount.target)]))
    expect(paths('设备映射')).toEqual(devices.flatMap((device) => [JSON.stringify(device.hostPath), JSON.stringify(device.containerPath)]))
    expect(document.querySelectorAll('.create-preview b')).toHaveLength(0)
    expect(document.querySelector('ol[aria-label="挂载映射"]')?.textContent).toContain('主机路径：')
    expect(document.querySelector('ol[aria-label="设备映射"]')?.textContent).toContain('容器路径：')
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ mounts, devices, runContainer })
  })

  it('卷名与 tmpfs 来源说明清晰，返回修改不发送 POST，重新预览读取新值', async () => {
    await mount()
    await click('添加挂载')
    await chooseType(0, 'Docker 卷')
    await edit(0, 'volume-name', 'volume-name', 'mount')
    await edit(0, '容器路径，例如 /data', '/data ', 'mount')
    await click('添加挂载')
    await chooseType(1, '临时目录')
    await edit(1, '容器路径，例如 /data', '/cache  ', 'mount')
    await click('仅创建')
    expect(paths('挂载映射')).toEqual(['"volume-name"', '"/data "', '"/cache  "'])
    expect(document.querySelector('ol[aria-label="挂载映射"]')?.textContent).toContain('卷名：')
    expect(document.querySelector('ol[aria-label="挂载映射"]')?.textContent).toContain('内存挂载，无需来源')
    expect(paths('设备映射')).toEqual([])
    await click('返回修改')
    expect(createCalls()).toHaveLength(0)
    await edit(0, '容器路径，例如 /data', '/data', 'mount')
    await click('仅创建')
    expect(paths('挂载映射')).toEqual(['"volume-name"', '"/data"', '"/cache  "'])
    expect(createCalls()).toHaveLength(0)
  })
})
