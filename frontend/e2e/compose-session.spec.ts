import { expect, test, type Page } from '@playwright/test'
import { installMockApi } from './mockApi'

const initial = 'services:\n  web:\n    image: nginx:original'
const path = '/volume2/DockerProject/media-stack/compose.yml'
async function openWorkspace(page: Page) {
  await installMockApi(page)
  await page.route('**/api/v1/docker/compose/config/read', (route) => route.fulfill({ json: {
    projectId: 'media-stack', workingDirectory: '/volume2/DockerProject/media-stack', collectedAt: '2026-10-13T00:00:00Z',
    files: [{ path, name: 'compose.yml', content: initial, size: initial.length }],
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
