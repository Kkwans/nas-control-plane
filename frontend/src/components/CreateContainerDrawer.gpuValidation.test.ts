// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、GPU 编辑、确认弹窗及 API 编解码；只替换 fetch。
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
async function toggleGPU() {
  const row = [...document.querySelectorAll<HTMLElement>('.advanced-body .switch-line')].find((node) => node.querySelector('strong')?.textContent === 'GPU')!
  row.querySelector<HTMLElement>('.el-switch')!.click()
  await settle()
}
async function editCount(value: string) {
  const input = document.querySelector<HTMLInputElement>('input[role="spinbutton"][aria-label="GPU 数量（-1 表示全部）"]')!
  expect(input).not.toBeNull()
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  input.dispatchEvent(new Event('change', { bubbles: true }))
  await settle()
}
async function mountGPU() { await mount(); await toggleAdvanced(); await toggleGPU() }
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

describe('GPU 创建输入验证', () => {
  it.each(['0', '1.5', '-0.5', ''])('数量 %j 在确认前阻止两种创建操作', async (value) => {
    await mountGPU()
    await editCount(value)
    for (const label of ['仅创建', '创建并启动']) {
      await click(label)
      expect(document.querySelector('.create-preview')).toBeNull()
      expect(document.querySelector('.el-message')?.textContent).toContain('GPU 数量必须是 -1（全部）或 1–128 的整数。')
      expect(createCalls()).toHaveLength(0)
    }
  })
  it.each(['bad driver', '-nvidia', 'a'.repeat(65), '<b>nvidia</b>'])('拒绝无效驱动 %j', async (value) => {
    await mountGPU()
    await edit('例如 nvidia；留空自动', value)
    await click('仅创建')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(document.querySelector('.el-message')?.textContent).toContain('GPU 驱动必须')
    expect(createCalls()).toHaveLength(0)
  })
  it.each([
    ['gpu,GPU', 'GPU 能力不能重复'],
    ['compute, Compute ', 'GPU 能力不能重复'],
    ['bad-cap', 'GPU 能力只能包含'],
    ['gpu, bad cap', 'GPU 能力只能包含'],
    ['<b>gpu</b>', 'GPU 能力只能包含'],
  ])('拒绝无效能力 %j', async (value, message) => {
    await mountGPU()
    await edit('gpu,compute,utility', value)
    await click('仅创建')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(document.querySelector('.el-message')?.textContent).toContain(message)
    expect(createCalls()).toHaveLength(0)
  })
  it.each([false, true])('边界、自动驱动及空能力保留提交契约，runContainer=%s', async (runContainer) => {
    await mountGPU()
    await edit('例如 nvidia；留空自动', '   ')
    await edit('gpu,compute,utility', ' , , ')
    const label = runContainer ? '创建并启动' : '仅创建'
    for (const [typed, expected] of [['-2', -1], ['-1', -1], ['1', 1], ['128', 128], ['129', 128]] as const) {
      await editCount(typed)
      expect(document.querySelector<HTMLInputElement>('input[role="spinbutton"][aria-label="GPU 数量（-1 表示全部）"]')!.value).toBe(String(expected))
      await click(label)
      expect(document.querySelector('.create-preview')).not.toBeNull()
      expect(createCalls()).toHaveLength(0)
      await click('返回修改')
    }
    await click(label)
    const confirm = button(runContainer ? '确认创建并启动' : '确认创建')
    confirm.click()
    confirm.click()
    await settle()
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ gpus: [{ count: 128, capabilities: [] }], runContainer })
    expect(submittedInput().gpus![0]).not.toHaveProperty('driver')
  })
  it.each([false, true])('有效驱动和混合大小写能力保持原有载荷，runContainer=%s', async (runContainer) => {
    await mountGPU()
    await edit('例如 nvidia；留空自动', '  Nvidia_1.test-driver  ')
    await edit('gpu,compute,utility', ' GPU, Compute, utility_1, ')
    await editCount('-1')
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(document.querySelector('.create-preview')).not.toBeNull()
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().gpus).toEqual([{ driver: 'Nvidia_1.test-driver', count: -1, capabilities: ['GPU', 'Compute', 'utility_1'] }])
    expect(submittedInput().runContainer).toBe(runContainer)
  })
  it('关闭 GPU 后忽略残留无效值', async () => {
    await mountGPU()
    await editCount('0')
    await edit('例如 nvidia；留空自动', 'bad driver')
    await edit('gpu,compute,utility', 'gpu,GPU')
    await toggleGPU()
    await click('仅创建')
    expect(document.querySelector('.create-preview')).not.toBeNull()
    await click('确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).not.toHaveProperty('gpus')
  })
})
