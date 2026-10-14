// @vitest-environment jsdom

import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import type { DockerContainerCreateInput, DockerImageSummary, FileEntriesPage } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'

// 真实抽屉、路径选择、确认弹窗、选择器和 API 编解码；只替换 fetch。
// 不启动浏览器、HTTP/RPC listener 或 Docker，JSDOM 不证明空白字符的视觉可辨识性。
const collectedAt = '2026-10-14T00:00:00Z'
const names = ['selected', 'selected ', 'selected\t', 'selected\u00a0', 'selected\u3000']
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

function page(path: string): FileEntriesPage {
  return {
    path, parent: '/', collectedAt,
    entries: path === '/'
      ? names.map((name) => ({ name, path: `/${name}`, type: 'directory', readable: true }))
      : [{ name: 'marker', path: `${path}/marker`, type: 'file', readable: true }],
  }
}
async function settle() { for (let count = 0; count < 30; count++) await nextTick() }
function createCalls() { return fetcher.mock.calls.filter(([path, init]) => path === '/api/v1/docker/containers' && init?.method === 'POST') }
function submittedInput() { return JSON.parse(String(createCalls()[0]![1]!.body)) as DockerContainerCreateInput }
function requestedPaths() {
  return fetcher.mock.calls.filter(([path]) => String(path).startsWith('/api/v1/files/entries?'))
    .map(([path]) => new URL(String(path), 'http://fixture.invalid').searchParams.get('path'))
}
function button(text: string) {
  const found = [...document.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.textContent?.trim() === text)
  expect(found, `按钮：${text}`).toBeDefined()
  return found!
}
async function click(text: string) { button(text).click(); await settle() }
function mountRow(index: number) {
  const row = document.querySelectorAll<HTMLElement>('.repeat-row--mount')[index]
  expect(row, `挂载行：${index + 1}`).toBeDefined()
  return row!
}
async function edit(index: number, placeholder: string, value: string) {
  const input = mountRow(index).querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)
  expect(input).not.toBeNull()
  input!.value = value
  input!.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}
async function chooseType(index: number, value: string) {
  const input = mountRow(index).querySelector<HTMLInputElement>(`input[role="combobox"][aria-label="第 ${index + 1} 个挂载类型"]`)!
  input.closest<HTMLElement>('.el-select__wrapper')!.click()
  await settle()
  const popup = document.getElementById(input.getAttribute('aria-controls')!)!
  const option = [...popup.querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === value)
  expect(option).toBeDefined()
  option!.click()
  await settle()
}
function pathEntry(name: string) {
  // 不 trim：这正是需要保持身份不同的两条真实 DOM 选项。
  const found = [...document.querySelectorAll<HTMLElement>('.path-entry')]
    .find((node) => node.querySelector('.path-entry__name')?.textContent === `DIR${name}`)
  expect(found, `路径选项：${JSON.stringify(name)}`).toBeDefined()
  return found!
}
async function chooseBind(index: number, name: string) {
  mountRow(index).querySelector<HTMLButtonElement>('.source-picker button')!.click()
  await settle()
  expect(document.querySelectorAll('.path-entry')).toHaveLength(names.length)
  pathEntry(name).querySelector<HTMLButtonElement>('.path-entry__name')!.click()
  await settle()
  expect(requestedPaths().at(-1)).toBe(`/${name}`)
  expect(document.querySelector('.path-browser__toolbar code')?.textContent).toBe(`/${name}`)
  await click('上一级')
  pathEntry(name).querySelector<HTMLButtonElement>('.browse-button')!.click()
  await settle()
  expect(mountRow(index).querySelector<HTMLInputElement>('input[placeholder="/volume2/data"]')?.value).toBe(`/${name}`)
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
    if (String(path).startsWith('/api/v1/files/entries?') && !init.method) {
      const selected = new URL(String(path), 'http://fixture.invalid').searchParams.get('path')!
      expect(['/', ...names.map((name) => `/${name}`)]).toContain(selected)
      return json(page(selected))
    }
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

describe('创建容器挂载路径的端到端组件身份', () => {
  it.each([false, true])('目录选择、GET 编码、预览与 POST 区分尾空格兄弟路径，runContainer=%s', async (runContainer) => {
    const { state, created } = await mount()
    await click('添加挂载')
    await chooseBind(0, 'selected')
    await edit(0, '容器路径，例如 /data', '/data')
    await click('添加挂载')
    await chooseBind(1, 'selected ')
    await edit(1, '容器路径，例如 /data', '/data ')
    expect(requestedPaths()).toEqual(['/', '/selected', '/', '/', '/selected ', '/'])
    await click(runContainer ? '创建并启动' : '仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain('bind /selected → /data；bind /selected  → /data ')
    expect(createCalls()).toHaveLength(0)
    await click(runContainer ? '确认创建并启动' : '确认创建')
    expect(createCalls()).toHaveLength(1)
    expect(submittedInput()).toMatchObject({ runContainer, mounts: [
      { type: 'bind', source: '/selected', target: '/data', readOnly: false },
      { type: 'bind', source: '/selected ', target: '/data ', readOnly: false },
    ] })
    expect(createCalls()[0]![1]).toMatchObject({ credentials: 'same-origin', redirect: 'error' })
    expect(created).toHaveBeenCalledExactlyOnceWith('created-id')
    expect(state.open).toBe(false)
  })

  it.each(['\t', '\u00a0', '\u3000'])('选择与确认保留其他合法路径尾空白 %j', async (suffix) => {
    await mount()
    await click('添加挂载')
    await chooseBind(0, `selected${suffix}`)
    await edit(0, '容器路径，例如 /data', `/data${suffix}`)
    await click('仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain(`bind /selected${suffix} → /data${suffix}`)
    await click('确认创建')
    expect(submittedInput().mounts).toEqual([{ type: 'bind', source: `/selected${suffix}`, target: `/data${suffix}`, readOnly: false }])
  })

  it('bind 来源的前导空白与纯空白在预览前拒绝，不被修剪成另一条路径', async () => {
    await mount()
    await click('添加挂载')
    await edit(0, '容器路径，例如 /data', '/data')
    for (const source of [' /source', '\t/source', '\u00a0/source', ' \t']) {
      await edit(0, '/volume2/data', source)
      for (const action of ['仅创建', '创建并启动']) {
        await click(action)
        expect(document.querySelector('.create-preview')).toBeNull()
      }
    }
    expect(document.body.textContent).toContain('挂载项必须填写来源和绝对目标路径。')
    expect(createCalls()).toHaveLength(0)
  })

  it.each(['主机目录', 'Docker 卷', '临时目录'])('%s 目标路径的前导空白仍在预览前拒绝', async (type) => {
    await mount()
    await click('添加挂载')
    if (type === '主机目录') await edit(0, '/volume2/data', '/source')
    else await chooseType(0, type)
    if (type === 'Docker 卷') await edit(0, 'volume-name', ' app-data ')
    for (const target of [' /data', '\t/data', '\u00a0/data', ' \t']) {
      await edit(0, '容器路径，例如 /data', target)
      await click('仅创建')
      expect(document.querySelector('.create-preview')).toBeNull()
    }
    expect(createCalls()).toHaveLength(0)
  })

  it('Docker 卷名继续 trim，tmpfs 无来源，两个类型的目标路径保留尾空白', async () => {
    await mount()
    await click('添加挂载')
    await chooseType(0, 'Docker 卷')
    await edit(0, 'volume-name', ' app-data \t')
    await edit(0, '容器路径，例如 /data', '/volume ')
    await click('添加挂载')
    await edit(1, '/volume2/data', '/previous-bind ')
    await chooseType(1, '临时目录')
    await edit(1, '容器路径，例如 /data', '/temp\u3000')
    await click('仅创建')
    expect(document.querySelector('.create-preview')?.textContent).toContain('volume app-data → /volume ；tmpfs tmpfs → /temp\u3000')
    await click('确认创建')
    expect(submittedInput().mounts).toEqual([
      { type: 'volume', source: 'app-data', target: '/volume ', readOnly: false },
      { type: 'tmpfs', target: '/temp\u3000', readOnly: false },
    ])
  })
})
