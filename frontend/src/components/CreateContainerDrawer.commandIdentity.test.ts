// @vitest-environment jsdom

import drawerSource from './CreateContainerDrawer.vue?raw'
import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、命令编辑、确认弹窗和 API 编解码；只替换 fetch。
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
async function editCommand(value: string) {
  const toggle = document.querySelector<HTMLButtonElement>('.advanced-toggle')!
  if (toggle.getAttribute('aria-expanded') === 'false') { toggle.click(); await settle() }
  const input = document.querySelector<HTMLTextAreaElement>('textarea[aria-label="容器启动参数（每行一个）"]')!
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
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

describe('创建命令保留 argv 身份', () => {
  it('预览样式保留连续空白并允许换行（静态合同，不替代浏览器验证）', () => {
    const source = drawerSource
    expect(source).toContain('class="command-preview"')
    expect(source).toMatch(/\.command-preview\s*\{\s*white-space:\s*pre-wrap\s*;?\s*\}/)
  })

  it.each([false, true])('保留空白与字面 Shell 字符，预览与提交使用同一 argv，runContainer=%s', async (runContainer) => {
    await mount()
    const expected = ['/app/server ', '  leading  ', '\targument\t', '\u00a0name\u3000', ' ', '"quoted value"', '$APP_MODE && echo ok', '--name=a b']
    await editCommand('\n' + expected.join('\n\n') + '\n')
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain(JSON.stringify(expected))
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput().command).toEqual(expected)
    expect(submittedInput().runContainer).toBe(runContainer)
  })

  it.each(['', '\n\n', ' \t\n\u00a0\u3000'])('全空白编辑器省略 command 并沿用镜像默认命令：%j', async (value) => {
    await mount()
    await editCommand(value)
    await click('仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain('使用镜像默认命令')
    await click('确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).not.toHaveProperty('command')
  })

  it.each([['a b', 'c'], ['a', 'b c']])('预览明确区分参数边界：%j %j', async (...args) => {
    await mount()
    await editCommand(args.join('\n'))
    await click('仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain(JSON.stringify(args))
    expect(createCalls()).toHaveLength(0)
  })

  it('实际 textarea 输入含 NUL 时不打开预览或发送 POST', async () => {
    await mount()
    await editCommand('/app/server\ninvalid\0argument')
    await click('仅创建')
    expect(document.querySelector('.create-preview')).toBeNull()
    expect(createCalls()).toHaveLength(0)
    expect(document.body.textContent).toContain('启动参数不能包含 NUL 或换行控制字符。')
  })
})
