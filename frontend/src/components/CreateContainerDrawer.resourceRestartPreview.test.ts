// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、资源和重启策略编辑、确认弹窗及 API 编解码；只替换 fetch。
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
function namedSpinbutton(label: string) {
  const inputs = [...document.querySelectorAll<HTMLInputElement>('input[role="spinbutton"]')].filter((node) => node.getAttribute('aria-label') === label)
  expect(inputs, `具名数字输入：${label}`).toHaveLength(1)
  expect(inputs[0]!.type).toBe('number')
  return inputs[0]!
}
async function editNumber(label: string, value: string) {
  const input = namedSpinbutton(label)
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  input.dispatchEvent(new Event('change', { bubbles: true }))
  await settle()
}
async function choosePolicy(label: string) {
  const input = document.querySelector<HTMLInputElement>('input[role="combobox"][aria-label="容器重启策略"]')!
  input.closest<HTMLElement>('.el-select__wrapper')!.click()
  await settle()
  const popup = document.getElementById(input.getAttribute('aria-controls')!)!
  const option = [...popup.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === label)
  expect(option).toBeDefined()
  option!.click()
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

const policies = [
  ['no', '不自动重启'],
  ['always', '始终重启'],
  ['unless-stopped', '除非手动停止'],
  ['on-failure', '失败时重启'],
] as const

describe('创建确认的资源与重启策略快照', () => {
  it('资源与重试次数的可访问名称位于真实数字输入上', async () => {
    await mount()
    expect(namedSpinbutton('CPU 限制（核）').value).toBe('')
    expect(namedSpinbutton('内存限制（MiB）').value).toBe('')
    expect(document.querySelector('input[aria-label="最大重试次数"]')).toBeNull()
    await choosePolicy('失败时重启')
    expect(namedSpinbutton('最大重试次数').value).toBe('0')
    expect(createCalls()).toHaveLength(0)
  })

  it.each([false, true])('缺省不请求资源限制，取消零提交，确认仅一次，runContainer=%s', async (runContainer) => {
    await mount()
    const open = runContainer ? '创建并启动' : '仅创建'
    await click(open)
    expect(previewValue('CPU 限制')).toBe('未设置限制')
    expect(previewValue('内存限制')).toBe('未设置限制')
    expect(previewValue('重启策略')).toBe('不自动重启（no）')
    expect(previewValue('最大重试次数')).toBeUndefined()
    expect(createCalls()).toHaveLength(0)
    await click('返回修改')
    expect(createCalls()).toHaveLength(0)
    await click(open)
    const confirm = button(runContainer ? '确认创建并启动' : '确认创建')
    confirm.click()
    confirm.click()
    await settle()
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ restartPolicy: 'no', runContainer })
    for (const key of ['cpu', 'memoryBytes', 'restartMaxRetries']) expect(submittedInput()).not.toHaveProperty(key)
  })

  it.each(policies.flatMap(([policy, label]) => [false, true].map((runContainer) => ({ policy, label, runContainer }))))('精确展示并提交资源及 $policy，runContainer=$runContainer', async ({ policy, label, runContainer }) => {
    await mount()
    await editNumber('CPU 限制（核）', '1.125')
    await editNumber('内存限制（MiB）', '64.123456')
    await choosePolicy(label)
    if (policy === 'on-failure') await editNumber('最大重试次数', '7')
    await click(runContainer ? '创建并启动' : '仅创建')
    const bytes = Math.round(64.123456 * 1024 * 1024)
    expect(previewValue('CPU 限制')).toBe('1.125 核')
    expect(previewValue('内存限制')).toBe(`${bytes} 字节（${bytes / 1024 / 1024} MiB）`)
    expect(previewValue('重启策略')).toBe(`${label}（${policy}）`)
    expect(previewValue('最大重试次数')).toBe(policy === 'on-failure' ? '7 次' : undefined)
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ cpu: 1.125, memoryBytes: bytes, restartPolicy: policy, runContainer })
    if (policy === 'on-failure') expect(submittedInput().restartMaxRetries).toBe(7)
    else expect(submittedInput()).not.toHaveProperty('restartMaxRetries')
  })

  it.each([false, true])('零重试上限与返回修改使用新快照，切换策略丢弃旧次数，runContainer=%s', async (runContainer) => {
    await mount()
    const open = runContainer ? '创建并启动' : '仅创建'
    await choosePolicy('失败时重启')
    await click(open)
    expect(previewValue('最大重试次数')).toBe('0（不限制重试次数）')
    await click('返回修改')
    await editNumber('最大重试次数', '12')
    await editNumber('CPU 限制（核）', '2.5')
    await editNumber('内存限制（MiB）', '128')
    await click(open)
    expect(previewValue('最大重试次数')).toBe('12 次')
    expect(previewValue('CPU 限制')).toBe('2.5 核')
    expect(previewValue('内存限制')).toBe('134217728 字节（128 MiB）')
    await click('返回修改')
    await choosePolicy('始终重启')
    await editNumber('CPU 限制（核）', '')
    await editNumber('内存限制（MiB）', '')
    await click(open)
    expect(previewValue('最大重试次数')).toBeUndefined()
    expect(previewValue('CPU 限制')).toBe('未设置限制')
    expect(previewValue('内存限制')).toBe('未设置限制')
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ restartPolicy: 'always', runContainer })
    for (const key of ['cpu', 'memoryBytes', 'restartMaxRetries']) expect(submittedInput()).not.toHaveProperty(key)
  })

  it.each([false, true])('失败时重启的零上限按零提交，runContainer=%s', async (runContainer) => {
    await mount()
    await choosePolicy('失败时重启')
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(previewValue('最大重试次数')).toBe('0（不限制重试次数）')
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ restartPolicy: 'on-failure', restartMaxRetries: 0, runContainer })
  })
})
