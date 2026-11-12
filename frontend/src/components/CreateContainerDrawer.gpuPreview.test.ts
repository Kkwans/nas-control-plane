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
function createCalls() { return fetcher.mock.calls.filter(([path, init]) => path === '/api/v1/docker/containers/create-jobs' && init?.method === 'POST') }
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
    if (path === '/api/v1/docker/containers/create-jobs' && init.method === 'POST') {
      return json({ id: 'create-1', type: 'docker-container-create', status: 'queued', cancellable: false, artifactState: 'unknown', message: '已接收', progress: 0, createdAt: collectedAt, updatedAt: collectedAt, downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {} }, 202)
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

function gpuDetails() { return document.querySelector<HTMLOListElement>('ol[aria-label="GPU 请求详情"]') }
function previewValue(label: string) {
  return [...document.querySelectorAll('.create-preview dt')].find((node) => node.textContent === label)?.nextElementSibling?.textContent
}

describe('GPU 创建确认详情', () => {
  it.each([false, true])('明确全部设备、驱动和有效能力，取消无 POST、重复确认仅一次，runContainer=%s', async (runContainer) => {
    await mountGPU()
    await edit('例如 nvidia；留空自动', ' nvidia ')
    await editCount('-1')
    await edit('gpu,compute,utility', ' GPU, Compute,UTILITY ')
    const openLabel = runContainer ? '创建并启动' : '仅创建'
    await click(openLabel)
    expect(previewValue('硬件请求')).toBe('设备映射：0 项；GPU 请求：1 项')
    expect(gpuDetails()?.querySelectorAll('li')).toHaveLength(1)
    expect(gpuDetails()?.textContent).toContain('驱动："nvidia"')
    expect(gpuDetails()?.textContent).toContain('请求数量：全部（-1）')
    expect(gpuDetails()?.textContent).toContain('有效能力：["gpu","compute","utility"]')
    expect(gpuDetails()?.textContent).not.toContain('（默认）')
    expect(createCalls()).toHaveLength(0)
    await click('返回修改')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(0)
    await click(openLabel)
    const confirm = button(runContainer ? '确认创建并启动' : '确认创建')
    confirm.click()
    confirm.click()
    await settle()
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ gpus: [{ driver: 'nvidia', count: -1, capabilities: ['GPU', 'Compute', 'UTILITY'] }], runContainer })
  })

  it.each([false, true])('自动驱动、空能力默认 gpu 和明确数量，runContainer=%s', async (runContainer) => {
    await mountGPU()
    await edit('例如 nvidia；留空自动', '   ')
    await edit('gpu,compute,utility', ' , , ')
    await editCount('2')
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(gpuDetails()?.textContent).toContain('驱动：自动选择')
    expect(gpuDetails()?.textContent).toContain('请求数量：2 个')
    expect(gpuDetails()?.textContent).toContain('有效能力：["gpu"]（默认）')
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().gpus).toEqual([{ count: 2, capabilities: [] }])
    expect(submittedInput().runContainer).toBe(runContainer)
  })

  it('未启用时明确显示，默认启用请求一个 GPU', async () => {
    await mount()
    await click('仅创建')
    expect(previewValue('GPU 配置')).toBe('未启用')
    expect(previewValue('硬件请求')).toBe('设备映射：0 项；GPU 请求：0 项')
    expect(gpuDetails()).toBeNull()
    await click('返回修改')
    await toggleAdvanced()
    await toggleGPU()
    await click('仅创建')
    expect(gpuDetails()?.textContent).toContain('驱动：自动选择')
    expect(gpuDetails()?.textContent).toContain('请求数量：1 个')
    expect(gpuDetails()?.textContent).toContain('有效能力：["gpu"]')
    expect(gpuDetails()?.textContent).not.toContain('（默认）')
    expect(createCalls()).toHaveLength(0)
  })

  it.each([false, true])('编辑和关闭 GPU 使旧确认失效，runContainer=%s', async (runContainer) => {
    await mountGPU()
    await edit('例如 nvidia；留空自动', 'nvidia')
    const openLabel = runContainer ? '创建并启动' : '仅创建'
    const confirmLabel = runContainer ? '确认创建并启动' : '确认创建'
    await click(openLabel)
    const oldConfirm = button(confirmLabel)
    // 真实 DOM 编辑背景表单，旧快照不能确认成未审阅的新配置。
    await editCount('3')
    expect(document.querySelector('.create-preview')).toBeNull()
    oldConfirm.click()
    await settle()
    expect(createCalls()).toHaveLength(0)
    await click(openLabel)
    expect(gpuDetails()?.textContent).toContain('请求数量：3 个')
    const enabledConfirm = button(confirmLabel)
    await toggleGPU()
    expect(document.querySelector('.create-preview')).toBeNull()
    enabledConfirm.click()
    await settle()
    expect(createCalls()).toHaveLength(0)
    await click(openLabel)
    expect(previewValue('GPU 配置')).toBe('未启用')
    expect(gpuDetails()).toBeNull()
    expect(previewValue('硬件请求')).toBe('设备映射：0 项；GPU 请求：0 项')
    await click(confirmLabel)
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).not.toHaveProperty('gpus')
  })
})
