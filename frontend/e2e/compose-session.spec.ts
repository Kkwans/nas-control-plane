import { expect, test, type Page } from '@playwright/test'
import { createHash } from 'node:crypto'
import { installMockApi } from './mockApi'

const initial = 'services:\n  web:\n    image: nginx:original'
const path = '/volume2/DockerProject/media-stack/compose.yml'
const hash = (content: string) => createHash('sha256').update(content, 'utf8').digest('hex')
async function openWorkspace(page: Page) {
  await installMockApi(page)
  await page.route('**/api/v1/docker/compose/config/read', (route) => route.fulfill({ json: {
    projectId: 'media-stack', workingDirectory: '/volume2/DockerProject/media-stack', collectedAt: '2026-10-13T00:00:00Z',
    files: [{ path, name: 'compose.yml', content: initial, contentHash: hash(initial), size: Buffer.byteLength(initial, 'utf8') }],
  } }))
  await page.route('**/api/v1/docker/compose/drafts?*', (route) => route.fulfill({ json: { content: initial, updatedAt: '2026-10-13T00:00:00Z' } }))
  await page.route('**/api/v1/docker/compose/revisions?*', (route) => route.fulfill({ json: [] }))
  await page.goto('/docker', { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: '查看 Docker 项目 媒体服务', exact: true }).click()
  await page.getByRole('button', { name: '打开 Compose 配置工作台', exact: false }).click()
  await expect(page.locator('.compose-editor-drawer .cm-content')).toContainText('nginx:original')
}
async function edit(page: Page, content: string) {
  const editor = page.locator('.compose-editor-drawer .cm-content')
  await editor.click()
  await editor.press('ControlOrMeta+A')
  await editor.pressSequentially(content)
}

test('Compose 延迟保存只确认提交版本，后续编辑仍未保存', async ({ page }) => {
  await openWorkspace(page)
  let release!: () => void
  const held = new Promise<void>((resolve) => { release = resolve })
  const contents: string[] = []
  await page.route('**/api/v1/docker/compose/drafts', async (route) => {
    const payload = route.request().postDataJSON() as { content: string }
    contents.push(payload.content)
    if (contents.length === 1) await held
    await route.fulfill({ json: { ...payload, updatedAt: '2026-10-13T01:00:00Z' } })
  })
  await edit(page, 'services: A')
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect.poll(() => contents.length).toBe(1)
  await edit(page, 'services: B')
  release()
  await expect(page.locator('.compose-workspace > footer')).toContainText('当前修改尚未保存')
  await expect(page.getByRole('button', { name: '保存草稿', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect.poll(() => contents).toEqual(['services: A', 'services: B'])
  await expect(page.locator('.compose-workspace > footer')).toContainText('0 个文件未保存')
})

test('Compose 校验中编辑后，迟到结果不显示配置有效', async ({ page }) => {
  // 网络响应到达不等于组件已处理 JSON。以 JSON 消费后的下一事件循环任务为
  // 边界，让请求 continuation 和 Vue 微任务全部落定，再检查迟到响应的效果。
  await page.addInitScript(() => {
    const originalFetch = window.fetch.bind(window)
    window.fetch = async (...args: Parameters<typeof fetch>) => {
      const response = await originalFetch(...args)
      const input = args[0]
      const url = input instanceof Request ? input.url : String(input)
      if (url.includes('/api/v1/docker/compose/config/validate')) {
        const originalJson = response.json.bind(response)
        response.json = async () => {
          const payload: unknown = await originalJson()
          const barrier = new MessageChannel()
          barrier.port1.onmessage = () => {
            document.documentElement.dataset.composeValidationConsumed = 'true'
            barrier.port1.close()
            barrier.port2.close()
          }
          barrier.port2.postMessage(null)
          return payload
        }
      }
      return response
    }
  })
  await openWorkspace(page)
  let release!: () => void
  const held = new Promise<void>((resolve) => { release = resolve })
  let received = false
  await page.route('**/api/v1/docker/compose/config/validate', async (route) => {
    received = true
    await held
    await route.fulfill({ json: { valid: true, services: ['old-service'], normalized: initial } })
  })
  await page.getByRole('button', { name: '校验', exact: true }).click()
  await expect.poll(() => received).toBe(true)
  await edit(page, 'services: new-version')
  const response = page.waitForResponse('**/api/v1/docker/compose/config/validate')
  release(); await response
  await expect(page.locator('html')).toHaveAttribute('data-compose-validation-consumed', 'true')
  await expect(page.locator('.compose-status .validation-result')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '校验', exact: true })).toBeEnabled()
})

const composeJob = (fields: Record<string, unknown> = {}) => ({
  id: 'compose-job', type: 'compose-deploy', status: 'queued', message: '已排队', progress: 0,
  createdAt: '2026-10-13T00:00:00Z', updatedAt: '2026-10-13T00:00:00Z',
  downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...fields,
})
async function observeComposeJobs(page: Page) {
  await page.evaluate(() => {
    const host = window as unknown as { composeStreams: EventSource[] }
    host.composeStreams = []
    const Original = window.EventSource
    window.EventSource = class extends Original {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options)
        if (String(url).includes('/api/v1/jobs/')) host.composeStreams.push(this)
      }
    }
  })
  await page.route('**/api/v1/docker/compose/config/validate', (route) => route.fulfill({ json: { valid: true, services: ['web'], normalized: '' } }))
}
async function finishComposeJob(page: Page, fields: Record<string, unknown>) {
  await expect.poll(() => page.evaluate(() => (window as unknown as { composeStreams: EventSource[] }).composeStreams.filter((stream) => stream.readyState !== EventSource.CLOSED).length)).toBe(1)
  await page.evaluate((snapshot) => {
    for (const stream of (window as unknown as { composeStreams: EventSource[] }).composeStreams) {
      if (stream.readyState !== EventSource.CLOSED) stream.dispatchEvent(new MessageEvent('progress', { data: JSON.stringify(snapshot) }))
    }
  }, composeJob(fields))
}
async function publishCompose(page: Page) {
  await page.getByRole('button', { name: '预览并部署', exact: true }).click()
  await page.getByRole('button', { name: '备份并部署', exact: true }).click()
}

test('Compose 完成后只推进已核对目标基线，新编辑保持 dirty 并用于下次发布', async ({ page }) => {
  await openWorkspace(page)
  await observeComposeJobs(page)
  const submitted: { content: string; baseContentHash: string; targetPath: string }[] = []
  await page.route('**/api/v1/docker/compose/deploy', (route) => {
    submitted.push(route.request().postDataJSON())
    return route.fulfill({ status: 202, json: composeJob() })
  })
  let release!: () => void
  const held = new Promise<void>((resolve) => { release = resolve })
  let postRead: unknown
  await page.route('**/api/v1/docker/compose/config/read', async (route) => {
    postRead = route.request().postDataJSON()
    await held
    const content = 'services: A'
    await route.fulfill({ json: {
      projectId: 'media-stack', workingDirectory: '/volume2/DockerProject/media-stack', collectedAt: '2026-10-13T01:00:00Z',
      files: [{ path, name: 'compose.yml', content, contentHash: hash(content), size: content.length }],
    } })
  })
  await edit(page, 'services: A')
  await publishCompose(page)
  await expect.poll(() => submitted.length).toBe(1)
  expect(submitted[0]).toMatchObject({ targetPath: path, content: 'services: A', baseContentHash: hash(initial) })
  await finishComposeJob(page, { status: 'completed', message: '应用命令已完成', progress: 100 })
  await expect.poll(() => postRead).toEqual({ projectId: 'media-stack', workingDirectory: '/volume2/DockerProject/media-stack', configFiles: [path] })
  await edit(page, 'services: B')
  await expect(page.locator('.compose-toolbar .deploy-button')).toBeDisabled()
  release()
  await expect(page.getByRole('button', { name: '预览并部署', exact: true })).toBeEnabled()
  await expect(page.locator('.compose-editor-drawer .cm-content')).toHaveText('services: B')
  await expect(page.locator('.compose-workspace > footer')).toContainText('当前修改尚未保存')
  await publishCompose(page)
  await expect.poll(() => submitted.length).toBe(2)
  expect(submitted[1]).toMatchObject({ targetPath: path, content: 'services: B', baseContentHash: hash('services: A') })
  await finishComposeJob(page, { status: 'failed', errorCode: 'COMPOSE_CONFIG_CONFLICT', message: '配置已变化，请重新读取并比较。' })
  await expect(page.locator('.compose-error')).toContainText('配置已变化')
})

for (const code of ['COMPOSE_CONFIG_CONFLICT', 'COMPOSE_DEPLOY_POSTCHECK_CONFLICT']) {
  test(`Compose ${code} 保留文本和磁盘基线，不触发完成复读`, async ({ page }) => {
    await openWorkspace(page)
    await observeComposeJobs(page)
    const submitted: { content: string; baseContentHash: string }[] = []
    let postReads = 0
    await page.route('**/api/v1/docker/compose/config/read', (route) => { postReads += 1; return route.abort() })
    await page.route('**/api/v1/docker/compose/deploy', (route) => {
      submitted.push(route.request().postDataJSON())
      return route.fulfill({ status: 202, json: composeJob() })
    })
    await edit(page, 'services: edited')
    await publishCompose(page)
    const message = code === 'COMPOSE_CONFIG_CONFLICT'
      ? 'Compose 配置已发生变化，未写入或部署；请保留编辑内容，重新读取并比较。'
      : 'Compose 应用命令已完成，但目标配置随后发生变化；未保存成功版本，请核对配置和运行状态。'
    await finishComposeJob(page, { status: 'failed', errorCode: code, error: message, message })
    await expect(page.locator('.compose-error')).toHaveText(message)
    await expect(page.locator('.compose-editor-drawer .cm-content')).toHaveText('services: edited')
    await expect(page.locator('.compose-workspace > footer')).toContainText('当前修改尚未保存')
    await publishCompose(page)
    await expect.poll(() => submitted.length).toBe(2)
    expect(submitted.map((input) => input.baseContentHash)).toEqual([hash(initial), hash(initial)])
    await finishComposeJob(page, { status: 'failed', errorCode: code, error: message, message })
    await expect(page.locator('.compose-error')).toHaveText(message)
    expect(postReads).toBe(0)
  })
}
