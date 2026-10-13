import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { installMockApi } from './mockApi'

// 全部请求由 HTTP fixture 回答；不连接 NAS，不将 202 误报为后端已停止。
const timestamp = '2026-10-13T08:00:00Z'
const longReference = `中文媒体归档项目-${'跨设备增量同步与长期保留'.repeat(9)}`
const longError = `配置校验失败：${'请核对完整目录、容器挂载权限及当前配置，保留编辑内容后再检查实际资源。'.repeat(8)} <script>不应作为 HTML 执行</script>`
const job = (fields: Record<string, unknown> = {}) => ({
  id: 'pull-active', type: 'docker-image-pull', status: 'running', reference: 'registry.example.test/media:latest',
  artifactState: 'unknown', message: '正在下载镜像层', progress: 42,
  createdAt: timestamp, updatedAt: timestamp, downloadedBytes: 42, totalBytes: 100, speedBytes: 10,
  layers: {}, cancellable: true, ...fields,
})
const mixedJobs = () => [
  job(),
  job({ id: 'compose-completed', type: 'compose-deploy', reference: 'media-stack', status: 'completed', progress: 100, message: '应用命令已完成', cancellable: false }),
  job({ id: 'pull-unknown-artifact', reference: 'alpine:3.20', status: 'completed', progress: 100, message: '镜像拉取完成', cancellable: false }),
  job({ id: 'unknown-kind', type: 'future-maintenance', reference: 'future-operation', status: 'interrupted', cancellationStatus: 'unknown', cancellable: true }),
  job({ id: 'compose-long-error', type: 'compose-deploy', reference: longReference, status: 'failed', message: longError, error: longError, errorCode: 'COMPOSE_CONFIG_CONFLICT', cancellable: false }),
]
const dialogOf = (page: Page) => page.getByRole('dialog', { name: '最近任务', exact: true })
const rowOf = (page: Page, reference: string) => dialogOf(page).getByRole('article', { name: reference, exact: true })

async function fixture(page: Page, initial: unknown[] = mixedJobs()) {
  await installMockApi(page)
  let jobs = initial
  let listRequests = 0
  let cancelRequests = 0
  let listFailure = false
  await page.route(/\/api\/v1\/jobs(?:\?.*)?$/, (route) => {
    listRequests += 1
    expect(new URL(route.request().url()).searchParams.get('type')).toBeNull()
    return route.fulfill(listFailure
      ? { status: 503, json: { code: 'AGENT_UNAVAILABLE', message: '任务来源暂时不可用' } }
      : { json: { jobs } })
  })
  await page.route('**/api/v1/jobs/*/cancel', (route) => {
    cancelRequests += 1
    expect(route.request().method()).toBe('POST')
    expect(new URL(route.request().url()).pathname).toBe('/api/v1/jobs/pull-active/cancel')
    jobs = jobs.map((entry) => {
      const value = entry as ReturnType<typeof job>
      return value.id === 'pull-active' ? { ...value, cancellationStatus: 'requested', message: '取消请求已接收，远端可能仍执行' } : value
    })
    return route.fulfill({ status: 202, json: jobs.find((entry) => (entry as ReturnType<typeof job>).id === 'pull-active') })
  })
  return {
    get listRequests() { return listRequests }, get cancelRequests() { return cancelRequests },
    failLists() { listFailure = true }, recoverLists() { listFailure = false },
  }
}

async function openTasks(page: Page, path = '/') {
  await page.goto(path)
  const trigger = page.getByRole('button', { name: '任务', exact: true })
  await expect(trigger).toBeVisible()
  await trigger.click()
  await expect(dialogOf(page)).toBeVisible()
  await expect(dialogOf(page).getByRole('button', { name: '刷新任务', exact: true })).toBeEnabled()
}

for (const path of ['/', '/docker']) {
  test(`${path} 可发现混合最近任务，产物未核实不改变完成结果，关闭不发送取消`, async ({ page }) => {
    const state = await fixture(page)
    await openTasks(page, path)
    const dialog = dialogOf(page)
    const compose = rowOf(page, 'media-stack')
    await expect(compose).toContainText('已完成')
    await expect(compose).toContainText('Compose 部署')
    await compose.getByRole('button', { name: '查看详情', exact: true }).click()
    await expect(compose).toContainText('此任务不支持取消')
    await expect(compose.getByRole('button', { name: '请求取消', exact: true })).toHaveCount(0)
    const unknown = rowOf(page, 'future-operation')
    await expect(unknown).toContainText('future-maintenance')
    await expect(unknown).toContainText('结果未确认')
    await unknown.getByRole('button', { name: '查看详情', exact: true }).click()
    await expect(unknown.getByRole('button', { name: '请求取消', exact: true })).toHaveCount(0)
    const completed = rowOf(page, 'alpine:3.20')
    await completed.getByRole('button', { name: '查看详情', exact: true }).click()
    await expect(completed).toContainText('已完成')
    await expect(completed).toContainText('未核实')
    await expect(completed).not.toContainText('已不存在')
    await expect(dialog).toContainText('关闭面板不会停止任务')
    await dialog.getByRole('button', { name: '关闭最近任务', exact: true }).click()
    await expect(page.locator('.task-center-drawer')).toHaveCount(0)
    await expect(page).toHaveURL(new RegExp(`${path}$`))
    await expect(page.getByRole('heading', { name: path === '/' ? '系统总览' : 'Docker 管理', exact: true })).toBeVisible()
    expect(state.cancelRequests).toBe(0)
  })
}

test('打开并关闭任务中心保留 Docker 页面的搜索状态', async ({ page }) => {
  const state = await fixture(page)
  await page.goto('/docker')
  const search = page.getByRole('textbox', { name: '搜索 Docker 资源', exact: true })
  await search.fill('媒体')
  await page.getByRole('button', { name: '任务', exact: true }).click()
  await expect(rowOf(page, 'media-stack')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialogOf(page)).toHaveCount(0)
  await expect(search).toHaveValue('媒体')
  expect(state.cancelRequests).toBe(0)
})

test('202 仅表示取消请求被接收，键盘操作保持明确状态和模态焦点', async ({ page }) => {
  const state = await fixture(page)
  await page.goto('/')
  const trigger = page.getByRole('button', { name: '任务', exact: true })
  await trigger.focus(); await page.keyboard.press('Enter')
  const dialog = dialogOf(page)
  await expect(dialog).toBeVisible()
  await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true)
  await dialog.getByLabel('搜索已加载记录', { exact: true }).focus()
  await page.keyboard.type('registry.example.test')
  await expect(dialog.getByRole('article')).toHaveCount(1)
  const row = rowOf(page, 'registry.example.test/media:latest')
  await row.getByRole('button', { name: '查看详情', exact: true }).focus(); await page.keyboard.press('Enter')
  await expect(row).toContainText('pull-active')
  await row.getByRole('button', { name: '请求取消', exact: true }).focus(); await page.keyboard.press('Enter')
  await expect.poll(() => state.cancelRequests).toBe(1)
  await expect(row).toContainText('取消请求已接收')
  await expect(row).not.toContainText('已取消')
  await expect(row.getByRole('button', { name: '请求取消', exact: true })).toBeDisabled()
  for (let index = 0; index < 20; index++) {
    await page.keyboard.press('Tab')
    expect(await dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true)
  }
  await page.keyboard.press('Escape')
  await expect(page.locator('.task-center-drawer')).toHaveCount(0)
  await expect(trigger).toBeFocused()
  expect(state.cancelRequests).toBe(1)
})

test('类型和状态筛选仅改变已加载列表，不发起过滤请求', async ({ page }) => {
  const state = await fixture(page)
  await openTasks(page)
  const dialog = dialogOf(page)
  await expect(dialog.getByRole('article')).toHaveCount(5)
  const count = state.listRequests
  await dialog.getByLabel('任务类型', { exact: true }).selectOption({ label: 'Compose 部署' })
  await expect(dialog.getByRole('article')).toHaveCount(2)
  await dialog.getByLabel('任务状态', { exact: true }).selectOption({ label: '已完成' })
  await expect(dialog.getByRole('article')).toHaveCount(1)
  await expect(rowOf(page, 'media-stack')).toBeVisible()
  expect(state.listRequests).toBe(count)
})

test('刷新失败保留列表且禁用取消，恢复成功后重新启用', async ({ page }) => {
  const state = await fixture(page)
  await openTasks(page)
  const row = rowOf(page, 'registry.example.test/media:latest')
  await row.getByRole('button', { name: '查看详情', exact: true }).click()
  const refresh = dialogOf(page).getByRole('button', { name: '刷新任务', exact: true })
  const cancel = row.getByRole('button', { name: '请求取消', exact: true })
  await expect(cancel).toBeEnabled()
  state.failLists(); await refresh.click()
  await expect(dialogOf(page)).toContainText('刷新失败')
  await expect(row).toBeVisible()
  await expect(cancel).toBeDisabled()
  expect(state.cancelRequests).toBe(0)
  state.recoverLists(); await refresh.click()
  await expect(cancel).toBeEnabled()
})

test('无法解析的记录不伪装成空任务列表', async ({ page }) => {
  await fixture(page, [{ id: 'invalid-job', status: 'unrecognized' }])
  await openTasks(page)
  await expect(dialogOf(page)).toContainText('记录无法读取')
  await expect(dialogOf(page)).not.toContainText('尚无可展示记录')
})

test('可见时低频读取，隐藏及关闭停止轮询且不创建逐任务 SSE', async ({ page }) => {
  const state = await fixture(page)
  await page.goto('/')
  await page.evaluate(() => {
    const original = window.EventSource
    const host = window as unknown as { taskJobStreams: number }
    host.taskJobStreams = 0
    window.EventSource = class extends original {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options)
        if (String(url).includes('/api/v1/jobs/')) host.taskJobStreams += 1
      }
    }
  })
  await page.getByRole('button', { name: '任务', exact: true }).click()
  await expect(rowOf(page, 'media-stack')).toBeVisible()
  await expect(dialogOf(page).getByRole('button', { name: '刷新任务', exact: true })).toBeEnabled()
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000))
  // 与 app.spec 相同，暂停在未来而非跨进程传回时已过去的 Date.now()。
  // 暂停后手动校准，让成功读取和下一轮计时共享确定的时间原点。
  const refresh = dialogOf(page).getByRole('button', { name: '刷新任务', exact: true })
  const beforeCalibration = state.listRequests
  await refresh.click()
  await expect.poll(() => state.listRequests).toBe(beforeCalibration + 1)
  await expect(refresh).toBeEnabled()
  const opened = state.listRequests
  await page.clock.runFor(14_900)
  expect(state.listRequests).toBe(opened)
  await page.clock.runFor(200)
  await expect.poll(() => state.listRequests).toBe(opened + 1)
  await expect(dialogOf(page).getByRole('button', { name: '刷新任务', exact: true })).toBeEnabled()
  // 确定性模拟 visibilitychange，不冒充操作系统后台标签验收。
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  const hidden = state.listRequests
  await page.clock.runFor(60_100)
  expect(state.listRequests).toBe(hidden)
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await expect.poll(() => state.listRequests).toBe(hidden + 1)
  await expect(dialogOf(page).getByRole('button', { name: '刷新任务', exact: true })).toBeEnabled()
  // 退出动画使用真实推进的时钟，等待实际卸载，避免暂停 rAF/timeout 制造假失败。
  await page.clock.resume()
  await page.keyboard.press('Escape')
  await expect(page.locator('.task-center-drawer')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '任务', exact: true })).toBeFocused()
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000))
  const closed = state.listRequests
  await page.clock.runFor(60_100)
  expect(state.listRequests).toBe(closed)
  expect(state.cancelRequests).toBe(0)
  expect(await page.evaluate(() => (window as unknown as { taskJobStreams: number }).taskJobStreams)).toBe(0)
})

for (const width of [390, 768]) {
  test(`${width}px 主菜单和任务抽屉互斥，关闭后清除焦点隔离`, async ({ page }) => {
    await fixture(page)
    await page.setViewportSize({ width, height: 844 })
    await openTasks(page)
    await expect(page.getByRole('dialog', { name: '主导航', exact: true })).toHaveCount(0)
    await page.keyboard.press('Escape')
    const menu = page.getByRole('button', { name: '打开主菜单', exact: true })
    await menu.click()
    const navigation = page.getByRole('dialog', { name: '主导航', exact: true })
    await expect(navigation).toBeVisible()
    await expect(dialogOf(page)).toHaveCount(0)
    await page.keyboard.press('Escape'); await expect(menu).toBeFocused()
    await page.getByRole('button', { name: '任务', exact: true }).click()
    await expect(dialogOf(page)).toBeVisible()
    await expect(navigation).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(page.locator('.app-stage')).not.toHaveAttribute('inert')
    await expect(page.locator('body')).not.toHaveClass(/mobile-navigation-open/)
  })
}

async function expectTextWraps(locator: Locator) {
  await expect(locator).toBeVisible()
  const metrics = await locator.evaluate((element) => {
    const bounds = element.getBoundingClientRect()
    const range = document.createRange()
    range.selectNodeContents(element)
    return {
      overflow: element.scrollWidth - element.clientWidth,
      linesFit: [...range.getClientRects()].filter((rect) => rect.width && rect.height)
        .every((rect) => rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1),
    }
  })
  expect(metrics.overflow).toBeLessThanOrEqual(1)
  expect(metrics.linesFit).toBe(true)
}

async function capture(page: Page, info: TestInfo, name: string, topbarBeforeOpen: unknown, fixtureSource = 'frontend/e2e/task-center.spec.ts:mixedJobs + frontend/e2e/mockApi.ts') {
  if (process.env.NCP_CAPTURE_UI !== '1') return
  const drawer = page.locator('.task-center-drawer')
  if (await drawer.count()) {
    // screenshot(animations: disabled) 会完成过渡；元数据必须先等待同一真实打开位置。
    await expect.poll(async () => drawer.evaluate(async (element) => {
      const before = element.getBoundingClientRect()
      await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
      const after = element.getBoundingClientRect()
      return Math.max(Math.abs(after.right - innerWidth), Math.abs(after.x - before.x), Math.abs(after.y - before.y))
    })).toBeLessThanOrEqual(.1)
  }
  const state = await page.evaluate(() => {
    const bounds = (element: Element | null) => element ? element.getBoundingClientRect().toJSON() : null
    const dialog = document.querySelector('.task-center-drawer')
    const drawerBody = dialog?.querySelector('.task-center-drawer__body')
    const heading = dialog?.querySelector('.task-heading')
    const bodyStyle = drawerBody ? getComputedStyle(drawerBody) : null
    const headingStyle = heading ? getComputedStyle(heading) : null
    return {
      viewport: { width: innerWidth, height: innerHeight }, rootFontSize: getComputedStyle(document.documentElement).fontSize,
      reducedMotion: matchMedia('(prefers-reduced-motion: reduce)').matches,
      body: { scrollWidth: document.body.scrollWidth, clientWidth: document.body.clientWidth },
      main: { bounds: bounds(document.querySelector('#app-main')), scrollWidth: document.querySelector('#app-main')?.scrollWidth, clientWidth: document.querySelector('#app-main')?.clientWidth },
      dialog: { bounds: bounds(dialog), scrollWidth: dialog?.scrollWidth, clientWidth: dialog?.clientWidth },
      drawerBodyLayout: drawerBody && bodyStyle ? {
        bounds: bounds(drawerBody), display: bodyStyle.display, flexDirection: bodyStyle.flexDirection,
        padding: bodyStyle.padding, overflow: bodyStyle.overflow, overflowX: bodyStyle.overflowX,
        overflowY: bodyStyle.overflowY, scrollTop: drawerBody.scrollTop,
        scrollHeight: drawerBody.scrollHeight, clientHeight: drawerBody.clientHeight,
      } : null,
      headingLayout: heading && headingStyle ? {
        bounds: bounds(heading), position: headingStyle.position, top: headingStyle.top,
        zIndex: headingStyle.zIndex, backgroundColor: headingStyle.backgroundColor,
      } : null,
      imageDownloads: { bounds: bounds(document.querySelector('.download-workspace')), rows: [...document.querySelectorAll('.download-row')].map((row) => row.textContent) },
      scrollRegions: [...document.querySelectorAll('.task-center-drawer .el-drawer__body, .task-center, .task-list-region')].map((element) => ({
        className: element.className, scrollTop: element.scrollTop, scrollHeight: element.scrollHeight,
        clientHeight: element.clientHeight, bounds: bounds(element),
      })),
      controls: [...(dialog?.querySelectorAll('button, input, select') ?? [])].map((element) => {
        const rect = element.getBoundingClientRect()
        const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)
        const clippingAncestors = []
        for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
          const style = getComputedStyle(ancestor)
          if (/(auto|scroll|hidden|clip)/.test(`${style.overflowX} ${style.overflowY}`)) {
            clippingAncestors.push({ className: ancestor.className, bounds: bounds(ancestor),
              overflowX: style.overflowX, overflowY: style.overflowY, scrollTop: ancestor.scrollTop })
          }
        }
        return { tag: element.tagName, label: element.getAttribute('aria-label') ?? element.textContent,
          bounds: bounds(element), disabled: element.hasAttribute('disabled'),
          centerHit: Boolean(hit && element.contains(hit)), clippingAncestors }
      }),
      activeElement: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label') },
    }
  })
  await mkdir(info.outputPath('ui-captures'), { recursive: true })
  const statePath = info.outputPath('ui-captures', `${name}-capture-state.json`)
  await writeFile(statePath, JSON.stringify({
    sourceSha: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    fixtureSource,
    fixtureOnly: true, topbarBeforeOpen, ...state,
  }, null, 2))
  await page.screenshot({ path: info.outputPath('ui-captures', `${name}.png`), fullPage: false, animations: 'disabled' })
  await info.attach(`${name}-capture-state`, { path: statePath, contentType: 'application/json' })
}

for (const width of [390, 768, 1280, 1440]) {
  for (const fontScale of [1, 2]) {
    test(`${width}px / ${fontScale * 100}% 根字号：长中文错误、横向溢出、减弱动画和截图`, async ({ page }, info) => {
      await fixture(page)
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await page.setViewportSize({ width, height: width === 390 ? 640 : 900 })
      await page.goto('/')
      await expect(page.getByRole('heading', { name: '系统总览', exact: true })).toBeVisible()
      await expect(page.locator('html')).toHaveCSS('font-size', '15px')
      // 200% 是浏览器文字缩放压力场景，区别于产品 13–18px 偏好选项。
      await page.addStyleTag({ content: `html { font-size: ${15 * fontScale}px !important; }` })
      await page.evaluate(() => document.fonts.ready)
      const trigger = page.getByRole('button', { name: '任务', exact: true })
      await expect(trigger).toBeInViewport()
      const topbarBeforeOpen = await page.locator('.topbar').evaluate((element) => ({
        bounds: element.getBoundingClientRect().toJSON(), scrollWidth: element.scrollWidth, clientWidth: element.clientWidth,
        controls: [...element.querySelectorAll('button, .connection-state')].filter((control) => control.getClientRects().length > 0).map((control) => {
          const rect = control.getBoundingClientRect()
          const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)
          return { text: control.textContent, label: control.getAttribute('aria-label'), bounds: rect.toJSON(), unobscured: Boolean(hit && control.contains(hit)) }
        }),
      }))
      expect(topbarBeforeOpen.scrollWidth).toBeLessThanOrEqual(topbarBeforeOpen.clientWidth + 1)
      for (const control of topbarBeforeOpen.controls) {
        expect(control.bounds.left).toBeGreaterThanOrEqual(-1)
        expect(control.bounds.right).toBeLessThanOrEqual(width + 1)
        expect(control.unobscured).toBe(true)
      }
      await capture(page, info, `fixture-task-center-${width}-font-${fontScale * 100}-topbar`, topbarBeforeOpen)
      await trigger.click()
      const dialog = dialogOf(page)
      await expect(rowOf(page, longReference)).toBeVisible()
      await capture(page, info, `fixture-task-center-${width}-font-${fontScale * 100}-list`, topbarBeforeOpen)
      // 验证 Teleport 后专属 body-class 的关键布局契约，避免选择器失效被截图掩盖。
      const drawerBody = dialog.locator('.task-center-drawer__body')
      await expect(drawerBody).toHaveCSS('display', width === 390 ? 'block' : 'flex')
      if (width !== 390) await expect(drawerBody).toHaveCSS('flex-direction', 'column')
      await expect(drawerBody).toHaveCSS('padding', '0px')
      await expect(drawerBody).toHaveCSS('overflow-y', width === 390 ? 'auto' : 'hidden')
      await expect(dialog.locator('.task-heading')).toHaveCSS('position', 'sticky')
      await dialog.getByLabel('搜索已加载记录', { exact: true }).fill('中文媒体归档项目')
      const row = rowOf(page, longReference)
      const details = row.getByRole('button', { name: '查看详情', exact: true })
      // 居中滚动避免 nearest 把控件贴在裁切边缘；仍严格要求完整可见与命中。
      await details.evaluate(async (element) => {
        element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' })
        await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
      })
      // 先保留实际滚动后的几何和像素，失败时也能诊断裁切祖先。
      await capture(page, info, `fixture-task-center-${width}-font-${fontScale * 100}-scrolled-list`, topbarBeforeOpen)
      await expect(details).toBeInViewport({ ratio: 1 })
      expect(await details.evaluate((element) => {
        const bounds = element.getBoundingClientRect()
        const hit = document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
        return Boolean(hit && element.contains(hit))
      })).toBe(true)
      await details.click()
      // 全局 reduced-motion 契约用 .01ms!important 保留动画结束事件。
      const motionDurations = await row.locator('.task-detail-toggle svg').evaluate((element) =>
        getComputedStyle(element).transitionDuration.split(',').map((value) => {
          const duration = value.trim()
          return Number.parseFloat(duration) * (duration.endsWith('ms') ? 1 : 1000)
        }))
      expect(motionDurations.every((milliseconds) => Number.isFinite(milliseconds) && milliseconds <= .01)).toBe(true)
      await expect(row).toContainText(longError)
      await expect(row).toContainText('COMPOSE_CONFIG_CONFLICT')
      await expect(row.locator('script')).toHaveCount(0)
      await expectTextWraps(row.locator('h3'))
      for (const value of await row.locator('dd').all()) await expectTextWraps(value)
      const dimensions = await page.evaluate(() => {
        const main = document.querySelector('#app-main') as HTMLElement
        return { body: document.body.scrollWidth, root: document.documentElement.scrollWidth, main: main.scrollWidth - main.clientWidth }
      })
      expect(dimensions.body).toBeLessThanOrEqual(width)
      expect(dimensions.root).toBeLessThanOrEqual(width)
      expect(dimensions.main).toBeLessThanOrEqual(1)
      const bounds = (await dialog.boundingBox())!
      expect(bounds.x).toBeGreaterThanOrEqual(-1)
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(width + 1)
      await expect(dialog.getByRole('button', { name: '关闭最近任务', exact: true })).toBeInViewport()
      const accessibility = await new AxeBuilder({ page }).include('.task-center-drawer').analyze()
      expect(accessibility.violations).toEqual([])
      await row.locator('dd').filter({ hasText: 'COMPOSE_CONFIG_CONFLICT' }).scrollIntoViewIfNeeded()
      await capture(page, info, `fixture-task-center-${width}-font-${fontScale * 100}-details`, topbarBeforeOpen)
      // 最后一次长详情滚动后再次验收，不能只证明滚动前的关闭按钮可用。
      const close = dialog.getByRole('button', { name: '关闭最近任务', exact: true })
      await expect(close).toBeInViewport({ ratio: 1 })
      expect(await close.evaluate((element) => {
        const bounds = element.getBoundingClientRect()
        const hit = document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)
        return Boolean(hit && element.contains(hit))
      })).toBe(true)
    })
  }
}


test('旧镜像下载面板同样不把未核实产物显示成已删除', async ({ page }, info) => {
  await installMockApi(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const reference = 'not-in-local-inventory:latest'
  await page.route('**/api/v1/jobs?*', (route) => route.fulfill({ json: { jobs: [
    job({ id: 'legacy-unknown-artifact', reference, status: 'completed', progress: 100, message: '拉取命令已完成', cancellable: false }),
  ] } }))
  await page.goto('/docker')
  await page.getByRole('button', { name: '镜像', exact: true }).click()
  await page.getByRole('button', { name: /^下载任务/ }).click()
  const row = page.locator('.download-row').filter({ hasText: reference })
  await expect(row.locator('.job-state')).toHaveText('已完成')
  await expect(row).toContainText('镜像未核实')
  await expect(row).not.toContainText('已删除')
  await capture(page, info, 'fixture-legacy-image-unknown-artifact', null,
    'frontend/e2e/task-center.spec.ts:legacy-unknown-artifact + frontend/e2e/mockApi.ts local image inventory without a matching image')
})
